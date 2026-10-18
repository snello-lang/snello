// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/parser"
	"github.com/teak-lang/teak/token"
	"github.com/teak-lang/teak/types"
)

// TestRunExamples builds every program under examples/run and the
// core programs that have an expected output, and compares what each
// prints with the .out file beside it. examples/run is the playground
// for the backend: each slice adds the programs it makes buildable,
// and a reader can run any of them with teak build. A core program
// joins the test when its slice lands, by gaining its .out file.
func TestRunExamples(t *testing.T) {
	run, err := filepath.Glob(filepath.Join("..", "..", "examples", "run", "*.teak"))
	if err != nil || len(run) == 0 {
		t.Fatalf("no programs under examples/run: %v", err)
	}

	core, _ := filepath.Glob(filepath.Join("..", "..", "examples", "core", "*.teak"))
	for _, path := range append(run, core...) {
		name := strings.TrimSuffix(filepath.Base(path), ".teak")
		want, err := os.ReadFile(strings.TrimSuffix(path, ".teak") + ".out")
		if err != nil {
			if filepath.Base(filepath.Dir(path)) == "run" {
				t.Errorf("%s: every program under examples/run needs its expected output beside it", path)
			}

			continue
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			if got := buildAndRun(t, name, string(src), strings.TrimSuffix(path, ".teak")+".files"); got != string(want) {
				t.Errorf("%s printed:\n%s\nwant:\n%s", path, got, want)
			}
		})
	}
}

// TestExit checks that os.Exit ends the program with its code after
// flushing what was printed.
func TestExit(t *testing.T) {
	src := "package main\nimport (\"fmt\"; \"os\")\nfunc main() { fmt.Println(\"bye\"); os.Exit(3); fmt.Println(\"not reached\") }\n"
	out, code := run(t, "exit", src)
	if out != "bye\n" || code != 3 {
		t.Errorf("got output %q and exit code %d, want \"bye\\n\" and 3", out, code)
	}
}

// TestPanics checks that a checked index stops the program with the
// panic exit code after flushing what was printed, and that a match
// through a mut borrow of an element writes the element, not a copy.
func TestPanics(t *testing.T) {
	src := `package main
import "fmt"
enum Slot { Full(n int); Vacant }
func bump(xs mut []Slot) { match xs[0] { Full(n) => *n += 1, Vacant => {} } }
func main() {
	xs := []Slot{Full(1)}
	bump(mut xs)
	fmt.Println(xs)
	i := len(xs) // one past the end: the boundary the check must catch
	fmt.Println(xs[i])
}
`
	out, code := run(t, "panics", src)
	if out != "[Full(2)]\n" || code != 2 {
		t.Errorf("got output %q and exit code %d, want \"[Full(2)]\\n\" and 2", out, code)
	}
}

// TestUnsupported checks that a construct the backend does not lower
// is an error that names it, never silent wrong code.
func TestUnsupported(t *testing.T) {
	tests := []struct{ src, want string }{
		{"func work() {}\nfunc main() { spawn work() }", "channels and tasks"},
		{"func main() { ch := make(chan int); ch <- 1 }", "channels"},
	}
	for _, tt := range tests {
		f, file, err := parser.ParseFile("t.teak", []byte("package main\n"+tt.src+"\n"))
		if err != nil {
			t.Fatal(err)
		}

		info := &types.Info{}
		if _, err := types.Check(file, f, info); err != nil {
			t.Fatal(err)
		}

		_, err = Emit(file, f, info)
		if err == nil || !strings.Contains(err.Error(), "does not support") || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: got %v, want an error naming %q", tt.src, err, tt.want)
		}
	}
}

// BenchmarkEmit emits every program under examples/run once per
// iteration, parsed and checked ahead of time, and reports
// allocations, the number the coding standards track.
func BenchmarkEmit(b *testing.B) {
	type checked struct {
		f    *ast.File
		file *token.File
		info *types.Info
	}

	paths, _ := filepath.Glob(filepath.Join("..", "..", "examples", "run", "*.teak"))
	var programs []checked
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			b.Fatal(err)
		}

		f, file, err := parser.ParseFile(path, src)
		if err != nil {
			b.Fatal(err)
		}

		info := &types.Info{}
		if _, err := types.Check(file, f, info); err != nil {
			b.Fatal(err)
		}

		programs = append(programs, checked{f, file, info})
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		for _, p := range programs {
			if _, err := Emit(p.file, p.f, p.info); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// buildAndRun emits src, compiles it with the system C compiler under
// the warnings the standards require, runs the binary, and returns its
// standard output. The files under fixtures, if the directory exists,
// are copied beside the binary first, for a program that reads them.
func buildAndRun(t *testing.T, name, src string, fixtures ...string) string {
	t.Helper()
	out, code := run(t, name, src, fixtures...)
	if code != 0 {
		t.Fatalf("%s exited with %d\n%s", name, code, out)
	}

	return out
}

// run builds src and runs it, returning its standard output and exit code.
func run(t *testing.T, name, src string, fixtures ...string) (string, int) {
	t.Helper()
	f, file, err := parser.ParseFile(name+".teak", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{}
	if _, err := types.Check(file, f, info); err != nil {
		t.Fatal(err)
	}

	csrc, err := Emit(file, f, info)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	for _, fixture := range fixtures {
		entries, err := os.ReadDir(fixture)
		if err != nil {
			continue // no files to place
		}

		for _, entry := range entries {
			data, err := os.ReadFile(filepath.Join(fixture, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(filepath.Join(dir, entry.Name()), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	bin, out, err := Build(dir, name, csrc)
	if err != nil {
		t.Fatalf("%v\n%s\n--- emitted C ---\n%s", err, out, csrc)
	}

	cmd := exec.Command(bin)
	cmd.Dir = dir
	stdout, err := cmd.Output()
	if exit, ok := err.(*exec.ExitError); ok {
		return string(stdout), exit.ExitCode()
	}

	if err != nil {
		t.Fatalf("running %s: %v", name, err)
	}

	return string(stdout), 0
}

func TestMain(m *testing.M) {
	if _, err := exec.LookPath("cc"); err != nil {
		os.Stderr.WriteString("skipping emit/c tests: no C compiler on the PATH\n")
		os.Exit(0)
	}

	os.Exit(m.Run())
}
