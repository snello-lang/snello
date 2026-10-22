// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/teak-lang/teak/compiler/ast"
	"github.com/teak-lang/teak/compiler/emit/internal/backendtest"
	"github.com/teak-lang/teak/compiler/emit/lower"
	"github.com/teak-lang/teak/compiler/importer"
	"github.com/teak-lang/teak/compiler/parser"
	"github.com/teak-lang/teak/compiler/token"
	"github.com/teak-lang/teak/compiler/types"
)

// TestRunExamples builds every program under examples/run and the
// core programs that have an expected output, and compares what each
// prints with the .out file beside it, as a release build and as a
// debug build, which fails a program that touches a released value or
// ends with a value never released. examples/run is the playground
// for the backend: each slice adds the programs it makes buildable,
// and a reader can run any of them with teak build. A core program
// joins the test when its slice lands, by gaining its .out file.
func TestRunExamples(t *testing.T) {
	run, err := filepath.Glob(filepath.Join("..", "..", "..", "examples", "run", "*.teak"))
	if err != nil || len(run) == 0 {
		t.Fatalf("no programs under examples/run: %v", err)
	}

	core, _ := filepath.Glob(filepath.Join("..", "..", "..", "examples", "core", "*.teak"))
	for _, path := range append(run, core...) {
		name := strings.TrimSuffix(filepath.Base(path), ".teak")
		want, err := os.ReadFile(strings.TrimSuffix(path, ".teak") + ".out")
		if err != nil {
			if filepath.Base(filepath.Dir(path)) == "run" {
				t.Errorf("%s: every program under examples/run needs its expected output beside it", path)
			}

			continue
		}

		for _, debug := range []bool{false, true} {
			suffix := ""
			if debug {
				suffix = "-debug"
			}

			t.Run(name+suffix, func(t *testing.T) {
				t.Parallel()
				src, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}

				if got := buildAndRun(t, name, string(src), debug, strings.TrimSuffix(path, ".teak")+".files"); got != string(want) {
					t.Errorf("%s printed:\n%s\nwant:\n%s", path, got, want)
				}
			})
		}
	}
}

// TestPrograms builds and runs the programs every backend runs, from
// emit/internal/backendtest, and checks each against what it must give.
func TestPrograms(t *testing.T) {
	for _, p := range backendtest.Programs(testing.Short()) {
		t.Run(p.Name, func(t *testing.T) {
			if why := p.Skip(); why != "" {
				t.Skip(why)
			}

			for _, debug := range p.Builds() {
				out, stderr, code := run(t, p.Name, p.Src, debug)
				if bad := p.Check(out, stderr, code); bad != "" {
					t.Errorf("debug=%v: %s", debug, bad)
				}
			}
		})
	}
}

// TestUnsupported checks that a construct the backend does not lower
// is an error that names it, never silent wrong code.
func TestUnsupported(t *testing.T) {
	for _, r := range backendtest.Refusals {
		f, file, err := parser.ParseFile("t.teak", []byte("package main\n"+r.Src+"\n"))
		if err != nil {
			t.Fatal(err)
		}

		info := &types.Info{}
		imp := importer.NewImporter(info)
		if _, err := (&types.Config{Importer: imp}).Check("", file, f, info); err != nil {
			t.Fatal(err)
		}

		_, err = Emit(file, f, info, libraries(imp)...)
		if err == nil || !strings.Contains(err.Error(), "does not support") || !strings.Contains(err.Error(), r.Want) {
			t.Errorf("%s: got %v, want an error naming %q", r.Src, err, r.Want)
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
		libs []lower.Library
	}

	paths, _ := filepath.Glob(filepath.Join("..", "..", "..", "examples", "run", "*.teak"))
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
		imp := importer.NewImporter(info)
		if _, err := (&types.Config{Importer: imp}).Check("", file, f, info); err != nil {
			b.Fatal(err)
		}

		programs = append(programs, checked{f, file, info, libraries(imp)})
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, p := range programs {
			if _, err := Emit(p.file, p.f, p.info, p.libs...); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// TestDebugChecks runs C programs against the runtime built with its
// checks, since no program the checker accepts touches a released
// value: each panics naming the released value, or fails at its end
// for the block it never released.
func TestDebugChecks(t *testing.T) {
	tests := []struct {
		name, body, want string
		code             int
	}{
		{"string", `tk_string s = tk_string_clone(TK_STR("hi", 2)); tk_string_drop(&s); tk_print_string(s);`, "panic: a released string was used", 2},
		{"stale", `tk_string s = tk_string_clone(TK_STR("hi", 2)); tk_string copy = s; tk_string_drop(&s); tk_print_string(copy);`, "panic: a released string was used", 2},
		{"hash", `tk_string s = tk_string_clone(TK_STR("hi", 2)); tk_string_drop(&s); tk_print_uint(tk_hash_string(s));`, "panic: a released string was used", 2},
		{"stale-pointer", `int64_t *p = tk_alloc(8); int64_t *q = p; tk_release((void **)&p); tk_print_int(TK_DEREF(q));`, "panic: a released pointer was used", 2},
		{"slice", `tk_slice xs; tk_slice_make(&xs, 2, 2, 8); tk_slice_free(&xs); tk_print_int(tk_index(0, xs.len));`, "panic: a released slice was used", 2},
		{"pointer", `int64_t *p = tk_alloc(8); *p = 1; tk_release((void **)&p); tk_print_int(TK_DEREF(p));`, "panic: a released pointer was used", 2},
		{"moved", `int64_t *p = NULL; tk_print_int(TK_DEREF(p));`, "panic: a moved pointer was used", 2},
		{"twice", `void *p = tk_alloc(8); tk_free(p); tk_free(p);`, "panic: a block was released twice", 2},
		{"leak", `void *p = tk_alloc(8); (void)p;`, "teak: 1 blocks (8 bytes) not released", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			csrc := "#include \"teak.h\"\nint main(int argc, char **argv) { tk_init(argc, argv); " + tt.body + " return tk_finish(); }\n"
			_, stderr, code := execute(t, t.TempDir(), tt.name, []byte(csrc), true)
			if !strings.Contains(stderr, tt.want) || code != tt.code {
				t.Errorf("%s: exit %d with %q, want exit %d with %q", tt.name, code, stderr, tt.code, tt.want)
			}
		})
	}
}

// TestDescriptorFlags checks the descriptors the runtime makes: each is
// closed on exec, as Go makes every one, so that a program the process
// starts inherits none, and a socket is non-blocking, as the poller
// needs. A file created, a file opened, a listening socket, a dialed
// connection and an accepted one print their number, then whether each
// flag is set.
func TestDescriptorFlags(t *testing.T) {
	body := `int64_t fds[5] = {0}, port = 0, bad = 0; tk_string call;
	bad += tk_os_create(TK_STR("flags.txt", 9), &fds[0]);
	bad += tk_os_open(TK_STR("flags.txt", 9), &fds[1]);
	bad += tk_net_listen(TK_STR("127.0.0.1", 9), 0, &fds[2], &port, &call);
	bad += tk_net_dial(TK_STR("127.0.0.1", 9), port, 0, &fds[3], &call);
	bad += tk_net_accept(fds[2], &fds[4]);
	for (int i = 0; i < 5; i++) {
		char line[32];
		int n = snprintf(line, sizeof line, "%d %d %d\n", i, (fcntl((int)fds[i], F_GETFD) & FD_CLOEXEC) != 0, (fcntl((int)fds[i], F_GETFL) & O_NONBLOCK) != 0);
		tk_print_string(TK_STR(line, n));
	}
	tk_print_int(bad);`
	csrc := "#include \"teak.h\"\n#include <fcntl.h>\n#include <stdio.h>\nint main(int argc, char **argv) { tk_init(argc, argv); " + body + " return tk_finish(); }\n"
	out, stderr, code := execute(t, t.TempDir(), "flags", []byte(csrc), false)
	if want := "0 1 0\n1 1 0\n2 1 1\n3 1 1\n4 1 1\n0"; out != want || code != 0 {
		t.Errorf("exit %d, printed %q%s, want %q", code, out, stderr, want)
	}
}

// buildAndRun emits src, compiles it with the system C compiler under
// the warnings the standards require, runs the binary, and returns its
// standard output. The files under fixtures, if the directory exists,
// are copied beside the binary first, for a program that reads them.
func buildAndRun(t *testing.T, name, src string, debug bool, fixtures ...string) string {
	t.Helper()
	out, stderr, code := run(t, name, src, debug, fixtures...)
	if code != 0 {
		t.Fatalf("%s exited with %d\n%s%s", name, code, out, stderr)
	}

	return out
}

// run builds src, a Teak program, and runs it, returning its standard
// output, its standard error and its exit code.
func run(t *testing.T, name, src string, debug bool, fixtures ...string) (string, string, int) {
	t.Helper()
	f, file, err := parser.ParseFile(name+".teak", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{}
	imp := importer.NewImporter(info)
	if _, err := (&types.Config{Importer: imp}).Check("", file, f, info); err != nil {
		t.Fatal(err)
	}

	csrc, err := Emit(file, f, info, libraries(imp)...)
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

	return execute(t, dir, name, csrc, debug)
}

// execute compiles the C source as the program name in dir, with the
// runtime, and runs it there.
func execute(t *testing.T, dir, name string, csrc []byte, debug bool) (string, string, int) {
	t.Helper()
	bin, out, err := Build(dir, name, csrc, debug)
	if err != nil {
		t.Fatalf("%v\n%s\n--- emitted C ---\n%s", err, out, csrc)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second) // a program that hangs fails rather than stalling the suite
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	cmd.Dir = dir
	stdout, err := cmd.Output()
	if exit, ok := err.(*exec.ExitError); ok {
		return string(stdout), string(exit.Stderr), exit.ExitCode()
	}

	if err != nil {
		t.Fatalf("running %s: %v", name, err)
	}

	return string(stdout), "", 0
}

// libraries returns the packages an importer loaded, as Emit takes
// them.
func libraries(imp *importer.Importer) []lower.Library {
	var libs []lower.Library
	for _, p := range imp.Packages() {
		libs = append(libs, lower.Library{File: p.File, Syntax: p.Syntax})
	}

	return libs
}

func TestMain(m *testing.M) {
	if _, err := exec.LookPath("cc"); err != nil {
		os.Stderr.WriteString("skipping emit/c tests: no C compiler on the PATH\n")
		os.Exit(0)
	}

	os.Exit(m.Run())
}
