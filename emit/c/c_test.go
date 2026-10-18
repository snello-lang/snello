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
// prints with the .out file beside it, as a release build and as a
// debug build, which fails a program that touches a released value or
// ends with a value never released. examples/run is the playground
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

// TestExit checks that os.Exit ends the program with its code after
// flushing what was printed.
func TestExit(t *testing.T) {
	src := "package main\nimport (\"fmt\"; \"os\")\nfunc main() { fmt.Println(\"bye\"); os.Exit(3); fmt.Println(\"not reached\") }\n"
	out, _, code := run(t, "exit", src, false)
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
func bump(xs mut []Slot) { match mut xs[0] { Full(n) => *n += 1, Vacant => {} } }
func main() {
	xs := []Slot{Full(1)}
	bump(mut xs)
	fmt.Println(xs)
	i := len(xs) // one past the end: the boundary the check must catch
	fmt.Println(xs[i])
}
`
	out, _, code := run(t, "panics", src, false)
	if out != "[Full(2)]\n" || code != 2 {
		t.Errorf("got output %q and exit code %d, want \"[Full(2)]\\n\" and 2", out, code)
	}
}

// TestPanicDrops checks that a panic runs the Drop methods of the
// values still owned, innermost first, and of no value that moved away
// or was dropped before, in both builds; that a temporary dropped
// after a variable its statement declared leaves that variable
// registered; and that a Drop panicking during the unwinding ends the
// program with its own message.
func TestPanicDrops(t *testing.T) {
	prelude := `package main
import "fmt"
struct Guard impl Drop { name string }
func (g mut Guard) Drop() { fmt.Println("drop", g.name) }
func take(g Guard) { fmt.Println("took", g.name) }
func name(g ref Guard) string { return g.name.Clone() }
`
	tests := []struct{ name, body, out, err string }{
		{"order", `
	a := Guard{name: "a"}
	b := Guard{name: "b"}
	take(b)
	{
		c := Guard{name: "c"}
		fmt.Println("inner", c.name)
	}
	g := Guard{name: "g" + name(Guard{name: "t"})}
	d := Guard{name: "d"}
	xs := []int{}
	i := len(xs)
	fmt.Println(xs[i], d.name, g.name)`, "took b\ndrop b\ninner c\ndrop c\ndrop t\ndrop d\ndrop gt\ndrop a\n", "panic: index out of range"},
		{"panicking-drop", `
	a := Guard{name: "a"}
	b := Guard{name: "b"}
	c := Guard{name: "c"}
	if b.name == "b" {
		panic("first")
	}
	fmt.Println(a.name, c.name)`, "drop c\n", "panic: first\npanic: in drop\n"},
	}
	for _, tt := range tests {
		src := prelude
		if tt.name == "panicking-drop" {
			src += "struct Bad impl Drop { name string }\nfunc (b mut Bad) Drop() { panic(\"in drop\") }\n"
			src = strings.Replace(src, "func main", "func main", 1)
		}

		body := tt.body
		if tt.name == "panicking-drop" {
			body = strings.Replace(body, `b := Guard{name: "b"}`, `b := Bad{name: "b"}`, 1)
		}

		src += "func main() {" + body + "\n}\n"
		for _, debug := range []bool{false, true} {
			out, stderr, code := run(t, "panicdrops", src, debug)
			if out != tt.out || code != 2 || !strings.Contains(stderr, tt.err) {
				t.Errorf("%s, debug=%v: got output %q, stderr %q and exit code %d, want %q, %q and 2", tt.name, debug, out, stderr, code, tt.out, tt.err)
			}
		}
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
	for b.Loop() {
		for _, p := range programs {
			if _, err := Emit(p.file, p.f, p.info); err != nil {
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
		{"file", `tk_file f = {7}; tk_file_drop(&f); tk_file_drop(&f);`, "panic: a released file was used", 2},
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

	cmd := exec.Command(bin)
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

func TestMain(m *testing.M) {
	if _, err := exec.LookPath("cc"); err != nil {
		os.Stderr.WriteString("skipping emit/c tests: no C compiler on the PATH\n")
		os.Exit(0)
	}

	os.Exit(m.Run())
}
