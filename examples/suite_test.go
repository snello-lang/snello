// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package examples holds the twenty example programs and the test that
// builds and runs them: the acceptance suite of the language, each
// program compared with the output recorded under expected/.
package examples

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/snello-lang/snello/compiler/emit/c"
	"github.com/snello-lang/snello/compiler/emit/llvm"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/types"
)

// examplePatterns name the twenty example programs: the files here and
// the Snello programs inside the two folders that hold a Go version
// beside the Snello one. The core and run suites have their own tests.
var examplePatterns = []string{"*.snello", "01-tree/*.snello", "02-pipeline/*.snello"}

// programs returns the paths of the example programs.
func programs(t testing.TB) []string {
	var paths []string
	for _, pattern := range examplePatterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}

		paths = append(paths, matches...)
	}

	sort.Strings(paths)
	return paths
}

// backends are the two the suite runs every example through: the
// LLVM backend, the default, and the C backend, the reference
// implementation the LLVM backend is held to.
var backends = []string{"llvm", "c"}

// compile parses, checks and emits a program with the library packages
// it imports through the backend, returning the source the backend
// writes or the first error.
func compile(path, backend string) ([]byte, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	f, file, err := parser.ParseFile(path, src)
	if err != nil {
		return nil, err
	}

	info := &types.Info{}
	imp := importer.NewImporter(info)
	if _, err := (&types.Config{Importer: imp}).Check("", file, f, info); err != nil {
		return nil, err
	}

	pkgs := imp.Packages()
	libs := make([]lower.Library, len(pkgs))
	for i, p := range pkgs {
		libs[i] = lower.Library{Path: p.Path, File: p.File, Syntax: p.Syntax}
	}

	if backend == "llvm" {
		return llvm.Emit(llvm.Config{}, file, f, info, libs...)
	}

	return c.Emit(file, f, info, libs...)
}

// build compiles what a backend emitted into dir, as a release or a
// debug build, and returns the binary.
func build(dir, name, backend string, src []byte, debug bool) (string, []byte, error) {
	if backend == "llvm" {
		return llvm.Build(dir, name, src, debug)
	}

	return c.Build(dir, name, src, debug)
}

// pendingExamples are the programs without an expected output yet, and
// what each waits for. A program that gains its output comes off the
// list; one that loses it, by a misnamed fixture, is noticed.
var pendingExamples = map[string]string{
	"03-http-cache": "net/http, db and json, later libraries over the net package",
	"19-cli-grep":   "regexp, a later library",
}

// TestExamples builds every example program that has its expected
// output under expected/, through both backends, as a release build
// and as a debug build, runs it with the files its fixture folder
// holds, and compares what it prints: the differential test of the two
// backends, which must print the same thing as each other and as the
// output recorded. A program without an expected output must be on
// the pending list, with its reason; the time each step takes is
// logged, which is how the compile time of the suite is recorded.
func TestExamples(t *testing.T) {
	var pending []string
	green := 0
	var front, back time.Duration
	for _, path := range programs(t) {
		name := strings.TrimSuffix(filepath.Base(path), ".snello")
		want, err := os.ReadFile(filepath.Join("expected", name+".out"))
		if err != nil {
			if _, known := pendingExamples[name]; !known {
				t.Errorf("%s has no expected output and is not on the pending list", name)
			}

			pending = append(pending, name)
			continue
		}

		if reason, listed := pendingExamples[name]; listed {
			t.Errorf("%s has an expected output but is still listed as pending for %s", name, reason)
		}

		green++
		t.Run(name, func(t *testing.T) {
			printed := make(map[string]string)
			for _, backend := range backends {
				start := time.Now()
				src, err := compile(path, backend)
				if err != nil {
					t.Fatalf("%s does not compile through %s: %v", path, backend, err)
				}

				front += time.Since(start)
				for _, debug := range []bool{false, true} {
					start = time.Now()
					dir := t.TempDir()
					bin, out, err := build(dir, name, backend, src, debug)
					if err != nil {
						t.Fatalf("%s (debug %v): %v\n%s", backend, debug, err, out)
					}

					back += time.Since(start)
					placeFixtures(t, name, dir)
					cmd := exec.Command(bin)
					cmd.Dir = dir
					stdout, err := cmd.Output()
					if exit, ok := err.(*exec.ExitError); ok {
						t.Fatalf("%s, debug %v: exited with %d\n%s%s", backend, debug, exit.ExitCode(), stdout, exit.Stderr)
					} else if err != nil {
						t.Fatal(err)
					}

					if string(stdout) != string(want) {
						t.Errorf("%s, debug %v: printed\n%s--- expected ---\n%s", backend, debug, stdout, want)
					}

					printed[backend] = string(stdout)
				}
			}

			if printed["llvm"] != printed["c"] {
				t.Errorf("the backends disagree:\n--- llvm ---\n%s--- c ---\n%s", printed["llvm"], printed["c"])
			}
		})
	}

	t.Logf("%d examples green, %d pending: %s", green, len(pending), strings.Join(pending, ", "))
	t.Logf("compile time: %v in the compiler, %v in clang and cc for two builds through each backend", front.Round(time.Millisecond), back.Round(time.Millisecond))
}

// placeFixtures copies the files of expected/<name>.files/, when the
// folder exists, into dir, where the program runs.
func placeFixtures(t *testing.T, name, dir string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("expected", name+".files"))
	if err != nil {
		return
	}

	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join("expected", name+".files", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(dir, entry.Name()), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// BenchmarkExamples measures the compiler on the examples that run:
// parsing, checking and emitting each through the default backend,
// without clang.
func BenchmarkExamples(b *testing.B) {
	var paths []string
	for _, path := range programs(b) {
		name := strings.TrimSuffix(filepath.Base(path), ".snello")
		if _, err := os.Stat(filepath.Join("expected", name+".out")); err == nil {
			paths = append(paths, path)
		}
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, path := range paths {
			if _, err := compile(path, "llvm"); err != nil {
				b.Fatal(err)
			}
		}
	}
}
