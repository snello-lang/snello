// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package bench holds the programs behind the performance table of
// _docs/design/01-language-design.md, each written in Teak, Go and
// Rust, and the tests that build and time them. The timing runs only
// when asked, TEAK_BENCH=1 go test ./bench -v, since it takes a
// minute and its numbers are for the design document, not for a
// pass or fail; without the variable the tests check that every Teak
// program here builds and prints what its Go version prints, and that
// a program's peak memory is what it keeps alive, however much it
// allocates and frees on the way.
package bench

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/teak-lang/teak/compiler/emit/c"
	"github.com/teak-lang/teak/compiler/emit/llvm"
	"github.com/teak-lang/teak/compiler/emit/lower"
	"github.com/teak-lang/teak/compiler/importer"
	"github.com/teak-lang/teak/compiler/parser"
	"github.com/teak-lang/teak/compiler/types"
)

// programs are the benchmarks, by the stem of their files.
var programs = []string{"fib", "tree", "words", "tasks", "memory"}

// timed are the programs that time themselves and print the timings on
// their last line, which varies from run to run and is not compared.
var timed = map[string]bool{"tasks": true, "memory": true}

// TestBenchmarks builds every program here through both Teak backends
// and in Go, checks that they print the same, and when TEAK_BENCH is
// set builds the Rust versions too and times them all, the best of
// three runs each, with the peak memory of each binary and the timings
// a timed program reports, printing the table the design document
// records.
func TestBenchmarks(t *testing.T) {
	timing := os.Getenv("TEAK_BENCH") != ""
	dir := t.TempDir()
	var rows []string
	for _, name := range programs {
		bins := map[string]string{
			"teak (llvm)": buildTeak(t, dir, name, "llvm"),
			"teak (c)":    buildTeak(t, dir, name, "c"),
			"go":          buildGo(t, dir, name),
		}
		if timing {
			if _, err := os.Stat(name + ".rs"); err == nil {
				bins["rust"] = buildRust(t, dir, name)
			}
		}

		outputs := make(map[string]string)
		for tool, bin := range bins {
			outputs[tool] = output(t, bin)
			if timed[name] {
				outputs[tool] = result(outputs[tool])
			}
		}

		for tool, out := range outputs {
			if out != outputs["go"] {
				t.Errorf("%s: %s printed %q, go printed %q", name, tool, out, outputs["go"])
			}
		}

		if !timing {
			continue
		}

		tools := make([]string, 0, len(bins))
		for tool := range bins {
			tools = append(tools, tool)
		}

		sort.Strings(tools)
		for _, tool := range tools {
			r := best(t, bins[tool])
			rows = append(rows, "| "+name+" | "+tool+" | "+r.took.Round(time.Millisecond).String()+" | "+megabytes(r.peak)+" | "+r.reported+" |")
		}
	}

	if timing {
		t.Logf("\n| program | tool | best of three | peak memory | reported |\n|---|---|---|---|---|\n%s", strings.Join(rows, "\n"))
	}
}

// TestNoCollector builds the memory benchmark three times, with one
// round, with its own two thousand, and with five times as many, and
// checks that the peak memory of the three is the same: the small
// trees, sixteen and then eighty million blocks, cost nothing beyond
// the live tree, since each is freed where its owner dies and its
// memory is the next one's. A collector that lets garbage pile up to
// a multiple of the live heap before a cycle fails this test; Go's,
// at its default setting, peaks at about twice the live heap.
func TestNoCollector(t *testing.T) {
	src, err := os.ReadFile("memory.teak")
	if err != nil {
		t.Fatal(err)
	}

	for _, backend := range []string{"llvm", "c"} {
		peaks := make(map[int]int64)
		for _, n := range []int{1, 2000, 10000} {
			variant := strings.Replace(string(src), "const rounds = 2000", "const rounds = "+strconv.Itoa(n), 1)
			bin := buildTeakSource(t, t.TempDir(), "memory", []byte(variant), backend)
			peaks[n] = run(t, bin).peak
		}

		live := peaks[1]
		for _, n := range []int{2000, 10000} {
			if peaks[n] > live+live/10 {
				t.Errorf("%s: %d rounds peaked at %s, the live tree alone at %s: freed memory is not reused", backend, n, megabytes(peaks[n]), megabytes(live))
			}
		}

		t.Logf("%s: peak %s with one round, %s with 2000, %s with 10000", backend, megabytes(live), megabytes(peaks[2000]), megabytes(peaks[10000]))
	}
}

// A measurement is one run of a binary: how long it took, its peak
// resident memory in bytes, and the timings it printed itself, if it
// is timed.
type measurement struct {
	took     time.Duration
	peak     int64
	reported string
}

// best runs a binary three times and returns its fastest run.
func best(t *testing.T, bin string) measurement {
	t.Helper()
	var fastest measurement
	for i := 0; i < 3; i++ {
		if m := run(t, bin); fastest.took == 0 || m.took < fastest.took {
			fastest = m
		}
	}

	return fastest
}

// run runs a binary once and measures it.
func run(t *testing.T, bin string) measurement {
	t.Helper()
	cmd := exec.Command(bin)
	start := time.Now()
	out, err := cmd.Output()
	took := time.Since(start)
	if exit, ok := err.(*exec.ExitError); ok {
		t.Fatalf("%s: %v\n%s", bin, err, exit.Stderr)
	}

	if err != nil {
		t.Fatalf("%s: %v", bin, err)
	}

	peak := cmd.ProcessState.SysUsage().(*syscall.Rusage).Maxrss
	if runtime.GOOS != "darwin" {
		peak *= 1024 // Linux counts the peak in kilobytes, Darwin in bytes
	}

	m := measurement{took: took, peak: peak}
	if timed[strings.TrimSuffix(strings.TrimSuffix(filepath.Base(bin), "-go"), "-rs")] {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		m.reported = lines[len(lines)-1]
	}

	return m
}

// result returns what a timed program printed before its timings.
func result(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.Join(lines[:len(lines)-1], "\n")
}

// megabytes writes a count of bytes in whole megabytes.
func megabytes(n int64) string {
	return strconv.FormatInt(n>>20, 10) + " MB"
}

// buildTeak builds name.teak through the backend into dir.
func buildTeak(t *testing.T, dir, name, backend string) string {
	t.Helper()
	src, err := os.ReadFile(name + ".teak")
	if err != nil {
		t.Fatal(err)
	}

	return buildTeakSource(t, dir, name, src, backend)
}

// buildTeakSource builds src, as the program name, through the backend
// into dir.
func buildTeakSource(t *testing.T, dir, name string, src []byte, backend string) string {
	t.Helper()
	f, file, err := parser.ParseFile(name+".teak", src)
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{}
	imp := importer.NewImporter(info)
	if _, err := (&types.Config{Importer: imp}).Check("", file, f, info); err != nil {
		t.Fatal(err)
	}

	var libs []lower.Library
	for _, p := range imp.Packages() {
		libs = append(libs, lower.Library{File: p.File, Syntax: p.Syntax})
	}

	out := filepath.Join(dir, backend)
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}

	var bin string
	var log []byte
	if backend == "llvm" {
		module, err := llvm.Emit(llvm.Config{}, file, f, info, libs...)
		if err != nil {
			t.Fatal(err)
		}

		bin, log, err = llvm.Build(out, name, module, false)
	} else {
		csrc, err := c.Emit(file, f, info, libs...)
		if err != nil {
			t.Fatal(err)
		}

		bin, log, err = c.Build(out, name, csrc, false)
	}

	if err != nil {
		t.Fatalf("%s through %s: %v\n%s", name, backend, err, log)
	}

	return bin
}

// buildGo builds name.go into dir.
func buildGo(t *testing.T, dir, name string) string {
	t.Helper()
	bin := filepath.Join(dir, name+"-go")
	if out, err := exec.Command("go", "build", "-o", bin, name+".go").CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", name, err, out)
	}

	return bin
}

// buildRust builds name.rs into dir with optimization on.
func buildRust(t *testing.T, dir, name string) string {
	t.Helper()
	bin := filepath.Join(dir, name+"-rs")
	if out, err := exec.Command("rustc", "-O", "-o", bin, name+".rs").CombinedOutput(); err != nil {
		t.Fatalf("rustc %s: %v\n%s", name, err, out)
	}

	return bin
}

// output runs a binary and returns what it printed.
func output(t *testing.T, bin string) string {
	t.Helper()
	out, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatalf("%s: %v", bin, err)
	}

	return string(out)
}

func TestMain(m *testing.M) {
	if _, err := exec.LookPath("cc"); err != nil {
		os.Stderr.WriteString("skipping bench tests: no C compiler on the PATH\n")
		os.Exit(0)
	}

	os.Exit(m.Run())
}
