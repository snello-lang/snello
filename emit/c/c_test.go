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

	"github.com/teak-lang/teak/parser"
	"github.com/teak-lang/teak/types"
)

// TestRunExamples builds every program under examples/run and compares
// what it prints with the .out file beside it. The folder is the
// playground for the backend: each slice adds the programs it makes
// buildable, and a reader can run any of them with teak build.
func TestRunExamples(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "examples", "run", "*.teak"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no programs under examples/run: %v", err)
	}

	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".teak")
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			want, err := os.ReadFile(strings.TrimSuffix(path, ".teak") + ".out")
			if err != nil {
				t.Fatalf("every program needs its expected output beside it: %v", err)
			}

			if got := buildAndRun(t, name, string(src)); got != string(want) {
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

// TestUnsupported checks that a construct the backend does not lower
// is an error that names it, never silent wrong code.
func TestUnsupported(t *testing.T) {
	src := "package main\nstruct P { x int }\nfunc main() { p := P{x: 1}; print(p.x) }\n"
	f, file, err := parser.ParseFile("t.teak", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{}
	if _, err := types.Check(file, f, info); err != nil {
		t.Fatal(err)
	}

	_, err = Emit(file, f, info)
	if err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Errorf("got %v, want an unsupported-construct error", err)
	}
}

// buildAndRun emits src, compiles it with the system C compiler under
// the warnings the standards require, runs the binary, and returns its
// standard output.
func buildAndRun(t *testing.T, name, src string) string {
	t.Helper()
	out, code := run(t, name, src)
	if code != 0 {
		t.Fatalf("%s exited with %d\n%s", name, code, out)
	}

	return out
}

// run builds src and runs it, returning its standard output and exit code.
func run(t *testing.T, name, src string) (string, int) {
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
