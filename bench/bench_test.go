// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package bench holds the programs behind the performance table of
// _docs/design/01-language-design.md, each written in Snello and in
// Go, and the tests that build and time them. The timing runs only
// when asked, SNELLO_BENCH=1 go test ./bench -v, since it takes
// minutes and its numbers are for the design document, not for a
// pass or fail; without the variable the tests check that every Snello
// program here builds and prints what its Go version prints, and that
// a program's peak memory is what it keeps alive, however much it
// allocates and frees on the way. With SNELLO_BENCH_OUT naming a file,
// the table is written there too, which is how the CI workflow puts it
// in the job's summary.
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

	"github.com/snello-lang/snello/compiler/emit/c"
	"github.com/snello-lang/snello/compiler/emit/llvm"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/types"
)

// programs are the benchmarks, by the stem of their files.
var programs = []string{"fib", "tree", "words", "churn", "tasks", "memory"}

// runs is how many times a binary is timed, after a first run that is
// discarded, since the first run of a new binary pays for the system's
// check of it, on macOS most of all. The table reports the median of
// the runs with the least and the most.
const runs = 10

// timed are the programs that time themselves and print the timings on
// their last line, which varies from run to run and is not compared.
var timed = map[string]bool{"tasks": true, "memory": true}

// TestBenchmarks builds every program here through both Snello backends
// and in Go, checks that they print the same, and when SNELLO_BENCH is
// set times them all, runs times each after a discarded first, with
// the peak memory of each binary and the timings a timed program
// reports, printing the table the design document records: the
// median, the least and the most of each.
func TestBenchmarks(t *testing.T) {
	timing := os.Getenv("SNELLO_BENCH") != ""
	dir := t.TempDir()
	var rows []string
	for _, name := range programs {
		bins := map[string]string{
			"snello (llvm)": buildSnello(t, dir, name, "llvm"),
			"snello (c)":    buildSnello(t, dir, name, "c"),
			"go":            buildGo(t, dir, name),
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
			s := measure(t, bins[tool])
			rows = append(rows, "| "+name+" | "+tool+" | "+ms(s.median)+" | "+ms(s.least)+" | "+ms(s.most)+" | "+megabytes(s.peak)+" | "+s.reported+" |")
		}
	}

	if !timing {
		return
	}

	table := "| program | tool | median | least | most | peak memory | reported |\n|---|---|---|---|---|---|---|\n" + strings.Join(rows, "\n") + "\n"
	heading := runtime.GOOS + "/" + runtime.GOARCH + ", " + strconv.Itoa(runtime.NumCPU()) + " processors, " + strconv.Itoa(runs) + " runs after a discarded first\n\n"
	t.Logf("\n%s%s", heading, table)
	if out := os.Getenv("SNELLO_BENCH_OUT"); out != "" {
		if err := os.WriteFile(out, []byte(heading+table), 0o644); err != nil {
			t.Fatal(err)
		}
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
	src, err := os.ReadFile("memory.snello")
	if err != nil {
		t.Fatal(err)
	}

	for _, backend := range []string{"llvm", "c"} {
		peaks := make(map[int]int64)
		for _, n := range []int{1, 2000, 10000} {
			variant := strings.Replace(string(src), "const rounds = 2000", "const rounds = "+strconv.Itoa(n), 1)
			bin := buildSnelloSource(t, t.TempDir(), "memory", []byte(variant), backend)
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

// A summary is what the table says of a binary: the median, least
// and most of its runs' times, the median of their peaks, and the
// timings a timed program reported, each number summarized the same
// way, "997 (980–1020)".
type summary struct {
	median, least, most time.Duration
	peak                int64
	reported            string
}

// measure runs a binary once for nothing, then runs times, and
// summarizes the runs.
func measure(t *testing.T, bin string) summary {
	t.Helper()
	run(t, bin)
	ms := make([]measurement, runs)
	for i := range ms {
		ms[i] = run(t, bin)
	}

	tooks := make([]int64, runs)
	peaks := make([]int64, runs)
	for i, m := range ms {
		tooks[i] = int64(m.took)
		peaks[i] = m.peak
	}

	median, least, most := stats(tooks)
	s := summary{median: time.Duration(median), least: time.Duration(least), most: time.Duration(most)}
	s.peak, _, _ = stats(peaks)
	if ms[0].reported != "" {
		s.reported = summarize(ms)
	}

	return s
}

// summarize writes the line a timed program printed with each of its
// numbers replaced by the median of the runs and the least and the
// most in parentheses; a word that is not a number in every run, or
// a line whose shape varies, is left as the first run printed it.
func summarize(ms []measurement) string {
	words := strings.Fields(ms[0].reported)
	lines := make([][]string, len(ms))
	for i, m := range ms {
		if lines[i] = strings.Fields(m.reported); len(lines[i]) != len(words) {
			return ms[0].reported
		}
	}

	for i := range words {
		values := make([]int64, 0, len(ms))
		for _, fields := range lines {
			v, err := strconv.ParseInt(fields[i], 10, 64)
			if err != nil {
				break
			}

			values = append(values, v)
		}

		if len(values) != len(ms) {
			continue
		}

		median, least, most := stats(values)
		words[i] = strconv.FormatInt(median, 10) + " (" + strconv.FormatInt(least, 10) + "–" + strconv.FormatInt(most, 10) + ")"
	}

	return strings.Join(words, " ")
}

// TestSummary checks the arithmetic of the table: the median of an
// even count is the mean of its two middle values, and a timed
// program's numbers are summarized word by word while its words that
// are not numbers, and a line whose shape differs, are left alone.
func TestSummary(t *testing.T) {
	if median, least, most := stats([]int64{4, 1, 3, 2}); median != 2 || least != 1 || most != 4 {
		t.Errorf("stats of 4 1 3 2: %d %d %d, want 2 1 4", median, least, most)
	}

	if median, _, _ := stats([]int64{5, 1, 3}); median != 3 {
		t.Errorf("stats of 5 1 3: median %d, want 3", median)
	}

	runs := []measurement{{reported: "spawn 10 ns/op switch 7 ns/op"}, {reported: "spawn 30 ns/op switch 5 ns/op"}, {reported: "spawn 20 ns/op switch 6 ns/op"}}
	if got, want := summarize(runs), "spawn 20 (10–30) ns/op switch 6 (5–7) ns/op"; got != want {
		t.Errorf("summarize: %q, want %q", got, want)
	}

	odd := []measurement{{reported: "a 1"}, {reported: "a 1 b"}}
	if got := summarize(odd); got != "a 1" {
		t.Errorf("summarize of lines of two shapes: %q, want the first line", got)
	}
}

// stats returns the median, the least and the most of the values.
func stats(values []int64) (median, least, most int64) {
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	n := len(sorted)
	median = sorted[n/2]
	if n%2 == 0 {
		median = (sorted[n/2-1] + sorted[n/2]) / 2
	}

	return median, sorted[0], sorted[n-1]
}

// ms writes a duration in whole milliseconds.
func ms(d time.Duration) string {
	return d.Round(time.Millisecond).String()
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
	if timed[strings.TrimSuffix(filepath.Base(bin), "-go")] {
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

// buildSnello builds name.snello through the backend into dir.
func buildSnello(t *testing.T, dir, name, backend string) string {
	t.Helper()
	src, err := os.ReadFile(name + ".snello")
	if err != nil {
		t.Fatal(err)
	}

	return buildSnelloSource(t, dir, name, src, backend)
}

// buildSnelloSource builds src, as the program name, through the backend
// into dir.
func buildSnelloSource(t *testing.T, dir, name string, src []byte, backend string) string {
	t.Helper()
	f, file, err := parser.ParseFile(name+".snello", src)
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
		libs = append(libs, lower.Library{Path: p.Path, File: p.File, Syntax: p.Syntax, CFiles: p.CFiles, Links: p.Links})
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
