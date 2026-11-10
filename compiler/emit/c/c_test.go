// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/internal/backendtest"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// TestRunExamples builds every program the backends' tests share, and
// the example programs of several packages, and compares what each
// prints with the .out file beside it, as a release build and as a
// debug build, which fails a program that touches a released value or
// ends with a value never released. Each slice of the backend adds the
// programs it makes buildable to the shared folder.
func TestRunExamples(t *testing.T) {
	run, err := filepath.Glob(filepath.Join(backendtest.Shared, "*.snello"))
	if err != nil || len(run) == 0 {
		t.Fatalf("no programs under %s: %v", backendtest.Shared, err)
	}

	for _, path := range run {
		name := strings.TrimSuffix(filepath.Base(path), ".snello")
		want, err := os.ReadFile(strings.TrimSuffix(path, ".snello") + ".out")
		if err != nil {
			t.Errorf("%s: every program the backends' tests share needs its expected output beside it", path)
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

				if got := buildAndRun(t, name, string(src), debug, strings.TrimSuffix(path, ".snello")+".files"); got != string(want) {
					t.Errorf("%s printed:\n%s\nwant:\n%s", path, got, want)
				}
			})
		}
	}

	dirs, err := backendtest.Directories(backendtest.Examples)
	if err != nil {
		t.Fatal(err)
	}

	for _, dir := range dirs {
		want, err := os.ReadFile(filepath.Join(dir, filepath.Base(dir)+".out"))
		if err != nil {
			t.Errorf("%s: every program of several packages needs its expected output beside it", dir)
			continue
		}

		for _, debug := range []bool{false, true} {
			suffix := ""
			if debug {
				suffix = "-debug"
			}

			t.Run(filepath.Base(dir)+suffix, func(t *testing.T) {
				t.Parallel()
				out, stderr, code := runDir(t, dir, debug)
				if code != 0 {
					t.Fatalf("%s exited with %d\n%s%s", dir, code, out, stderr)
				}

				if out != string(want) {
					t.Errorf("%s printed:\n%s\nwant:\n%s", dir, out, want)
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

// TestGCC builds programs with gcc where the system has one, since a
// Linux system's cc is gcc and clang is what every other test here
// compiles with: the runtime and the C the emitter writes must pass
// gcc's warnings, which are not clang's. The programs are the ones
// whose C gcc has refused, an array's zero inside a struct and an
// option a Pop fills, and the ones that lean on a compiler's own
// extensions: the bit builtins, of which gcc has no bit reverse, the
// vector type and the 128-bit product of the random source, and the
// narrow arithmetic and the shifts.
func TestGCC(t *testing.T) {
	version, err := exec.Command("gcc", "--version").Output()
	if err != nil {
		t.Skip("no gcc")
	}

	if bytes.Contains(version, []byte("clang")) { // a Mac's gcc is clang under another name
		t.Skip("gcc is clang here")
	}

	t.Setenv("CC", "gcc")
	chosen := map[string]bool{
		"heap": true, "heap-edges": true, "bit-functions": true, "math-functions": true, "random-numbers": true,
		"narrow-integers": true, "narrow-integers-assigned": true, "multiply-then-add": true, "integer-methods": true,
	}
	programs := []backendtest.Program{{
		Name: "array-zero",
		Src:  "package main\nimport \"fmt\"\nstruct Pair { a [2]int }\nfunc main() {\n\tp := Pair{a: [2]int{}}\n\tfmt.Println(p.a[0], p.a[1])\n}\n",
		Out:  "0 0\n",
	}}
	for _, p := range backendtest.Programs(testing.Short()) {
		if chosen[p.Name] {
			delete(chosen, p.Name)
			programs = append(programs, p)
		}
	}

	for name := range chosen {
		t.Errorf("no program is named %s", name)
	}

	for _, p := range programs {
		t.Run(p.Name, func(t *testing.T) {
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
		f, file, err := parser.ParseFile("t.snello", []byte("package main\n"+r.Src+"\n"))
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

// BenchmarkEmit emits every program the backends' tests share once per
// iteration, parsed and checked ahead of time, and reports
// allocations, the number the coding standards track.
func BenchmarkEmit(b *testing.B) {
	type checked struct {
		f    *ast.File
		file *token.File
		info *types.Info
		libs []lower.Library
	}

	paths, _ := filepath.Glob(filepath.Join(backendtest.Shared, "*.snello"))
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
		{"string", `sn_string s = sn_string_clone(SN_STR("hi", 2)); sn_string_drop(&s); sn_print_string(s);`, "panic: a released string was used", 2},
		{"stale", `sn_string s = sn_string_clone(SN_STR("hi", 2)); sn_string copy = s; sn_string_drop(&s); sn_print_string(copy);`, "panic: a released string was used", 2},
		{"hash", `sn_string s = sn_string_clone(SN_STR("hi", 2)); sn_string_drop(&s); sn_print_uint(sn_hash_string(s));`, "panic: a released string was used", 2},
		{"stale-pointer", `int64_t *p = sn_alloc(8); int64_t *q = p; sn_release((void **)&p); sn_print_int(SN_DEREF(q));`, "panic: a released pointer was used", 2},
		{"slice", `sn_slice xs; sn_slice_make(&xs, 2, 2, 8); sn_slice_free(&xs); sn_print_int(sn_index(0, xs.len));`, "panic: a released slice was used", 2},
		{"pointer", `int64_t *p = sn_alloc(8); *p = 1; sn_release((void **)&p); sn_print_int(SN_DEREF(p));`, "panic: a released pointer was used", 2},
		{"moved", `int64_t *p = NULL; sn_print_int(SN_DEREF(p));`, "panic: a moved pointer was used", 2},
		{"twice", `void *p = sn_alloc(8); sn_free(p); sn_free(p);`, "panic: a block was released twice", 2},
		{"leak", `void *p = sn_alloc(8); (void)p;`, "snello: 1 blocks (8 bytes) not released", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			csrc := "#include \"snello.h\"\nint main(int argc, char **argv) { sn_init(argc, argv); " + tt.body + " return sn_finish(); }\n"
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
	body := `int64_t fds[5] = {0}, port = 0, bad = 0; sn_string call;
	bad += sn_os_create(SN_STR("flags.txt", 9), &fds[0]);
	bad += sn_os_open(SN_STR("flags.txt", 9), &fds[1]);
	bad += sn_net_listen(SN_STR("127.0.0.1", 9), 0, &fds[2], &port, &call);
	bad += sn_net_dial(SN_STR("127.0.0.1", 9), port, 0, &fds[3], &call);
	bad += sn_net_accept(fds[2], &fds[4]);
	for (int i = 0; i < 5; i++) {
		char line[32];
		int n = snprintf(line, sizeof line, "%d %d %d\n", i, (fcntl((int)fds[i], F_GETFD) & FD_CLOEXEC) != 0, (fcntl((int)fds[i], F_GETFL) & O_NONBLOCK) != 0);
		sn_print_string(SN_STR(line, n));
	}
	sn_print_int(bad);`
	csrc := "#include \"snello.h\"\n#include <fcntl.h>\n#include <stdio.h>\nint main(int argc, char **argv) { sn_init(argc, argv); " + body + " return sn_finish(); }\n"
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

// run builds src, a Snello program, and runs it, returning its standard
// output, its standard error and its exit code.
func run(t *testing.T, name, src string, debug bool, fixtures ...string) (string, string, int) {
	t.Helper()
	f, file, err := parser.ParseFile(name+".snello", []byte(src))
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

// runDir builds the program in dir, a main package of one or several
// files with the packages of its own found through the module file
// above it, as snello build DIR does, and runs it.
func runDir(t *testing.T, dir string, debug bool) (string, string, int) {
	t.Helper()
	prog, info, libs, err := backendtest.LoadDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}

	csrc, err := EmitFiles(prog.Files, prog.Syntax, info, libs...)
	if err != nil {
		t.Fatal(err)
	}

	return execute(t, t.TempDir(), filepath.Base(dir), csrc, debug, lower.Bindings(info, libs)...)
}

// execute compiles the C source as the program name in dir, with the
// runtime and the C side of the packages that bind C, and runs it there.
func execute(t *testing.T, dir, name string, csrc []byte, debug bool, bindings ...lower.Binding) (string, string, int) {
	t.Helper()
	bin, out, err := Build(dir, name, csrc, debug, bindings...)
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
		libs = append(libs, lower.Library{Path: p.Path, File: p.File, Syntax: p.Syntax, CFiles: p.CFiles, Links: p.Links})
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

// emitSource parses, checks and emits the program src with the
// standard library, and returns what the backend writes, failing the
// test on any error. The tests beside each file of the backend read
// the output by the function that wrote it.
func emitSource(t *testing.T, src string) string {
	t.Helper()
	f, file, err := parser.ParseFile("t.snello", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{}
	imp := importer.NewImporter(info)
	if _, err := (&types.Config{Importer: imp}).Check("", file, f, info); err != nil {
		t.Fatal(err)
	}

	out, err := Emit(file, f, info, libraries(imp)...)
	if err != nil {
		t.Fatal(err)
	}

	return string(out)
}

// cFunction returns the body of the C function whose definition starts
// with head, up to its closing brace, or fails the test.
func cFunction(t *testing.T, out, head string) string {
	t.Helper()
	i := strings.Index(out, head+" {\n")
	if i < 0 {
		t.Fatalf("no function written as %q", head)
	}

	body := out[i:]
	return body[:strings.Index(body, "\n}\n")]
}
