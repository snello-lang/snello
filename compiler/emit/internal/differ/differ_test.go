// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package differ holds the backends to each other where a written
// expectation would miss what neither was asked: it builds the same
// programs through the C and the LLVM backend and requires the same
// output, in a release and a debug build, with nothing left at the
// end. The programs put a value no variable holds, a temporary, in
// every statement form, and the kinds of temporary that release
// something when dropped: a value with a Drop method, which prints, so
// the order of the drops and the bodies is in the output; a lock's
// guard, whose body takes the lock again, so a guard held too long is
// a deadlock; a channel handle; and a string.
package differ

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

// A kind is a temporary: the declarations it needs, an expression
// that builds one and reads an int from it through a ref, @R, one that
// changes it through a mut, @M, one that binds a borrow of a part, @B,
// and what a body does with it, @BODY.
type kind struct {
	name, decls, read, change, bind, body string
}

var kinds = []kind{
	{
		name: "drop",
		decls: `struct T impl Drop { n int }
func (t mut T) Drop() { fmt.Println("drop", t.n) }
func (t ref T) Get() int { return t.n }
func (t mut T) Bump() int { t.n += 100; return t.n }
func (t ref T) Opt() ref int? { return Some(ref t.n) }
func mk(n int) T { return T{n: n} }
`,
		read: "mk(%).Get()", change: "mk(%).Bump()", bind: "mk(%).Opt()", body: `fmt.Println("body", %)`,
	},
	{
		name:  "guard",
		decls: "",
		read:  "*g.Lock().Get()", change: "lockBump(ref g)", bind: "",
		body: `*g.Lock().Get() += 1; fmt.Println("body", %)`, // takes the lock again: a guard the statement still held would never give it back
	},
	{
		name: "chan",
		decls: `func give(n int) chan int { ch := make(chan int, 1); ch <- n; return ch }
`,
		read: "len(give(%))", change: "(<-give(%) ?? 0)", bind: "", body: `fmt.Println("body", %)`,
	},
	{
		name: "string",
		decls: `func word(n int) string { return strings.Repeat("ab", n) }
`,
		read: "len(word(%))", change: "len(word(%).ToUpper())", bind: "", body: `fmt.Println("body", %)`,
	},
	{
		name: "part", // a field of a temporary is a place in it, dropped once with the whole
		decls: `struct T impl Drop { n int }
func (t mut T) Drop() { fmt.Println("drop", t.n) }
func (t ref T) Get() int { return t.n }
func (t mut T) Bump() int { t.n += 100; return t.n }
func (t ref T) Opt() ref int? { return Some(ref t.n) }
struct W { t T; s string; xs []int }
func mk(n int) W { return W{t: T{n: n}, s: strings.Repeat("ab", n), xs: []int{n}} }
`,
		read: "mk(%).t.Get()", change: "mk(%).t.Bump()", bind: "mk(%).t.Opt()", body: `fmt.Println("body", %, len(mk(%).s), mk(%).xs[0])`,
	},
	{
		name: "element", // so is an element of a temporary slice of strings, which the debug build sees released twice or leaked
		decls: `func mk(n int) []string { return []string{strings.Repeat("ab", n), strings.Repeat("c", n)} }
func grow(s mut string) int { *s = *s + "!"; return len(s) }
`,
		read: "len(mk(%)[1])", change: "grow(mut mk(%)[0])", bind: "", body: `fmt.Println("body", %, mk(%)[0])`,
	},
	{
		name: "lent", // a temporary lent with ref or mut is held once and dropped once
		decls: `struct L { s string; xs []int }
func peek(l ref L) int { return len(l.s) + l.xs[0] }
func bump(l mut L) int { l.xs.Push(1); l.s = l.s + "!"; return len(l.s) + len(l.xs) }
func opt(l ref L) ref int? { return Some(ref l.xs[0]) }
func mk(n int) L { return L{s: strings.Repeat("ab", n), xs: []int{n}} }
`,
		read: "peek(ref mk(%))", change: "bump(mut mk(%))", bind: "opt(ref mk(%))", body: `fmt.Println("body", %, peek(ref mk(%)), bump(mut L{s: "x", xs: []int{%}}))`,
	},
}

// forms are the statement forms, each a template over @R, @M, @B and
// @BODY with the number % that tells its lines apart.
var forms = []string{
	"use(@R)",
	"use(@M)",
	"if @R >= 0 { @BODY }",
	"if @M < 0 { use(0) } else { @BODY }",
	"x := @R; x += @M; use(x)",
	"for i := 0; i < 2 && @R >= 0; i++ { @BODY }",
	"match @R { _ => use(%) }",
	"use(ret%())",
	"use(twice(@R))",
	"{ defer use(@R); use(-%) }",
	"d := done.Clone(); spawn func() { use(@R); d <- () }(); <-done",
	"if v := @B { use(*v) }",
	"if no() && @R >= 0 { use(0) } else { @BODY }", // the right side never runs: nothing it would build is dropped
	"ok := yes() || @M >= 0; if ok { @BODY }",
	"if yes() && @R >= 0 { @BODY }",
}

// program writes the program for k: a function per form, run in turn.
func program(k kind) string {
	var b strings.Builder
	b.WriteString("package main\n\nimport (\"fmt\"; \"strings\"; \"sync\")\n\n")
	b.WriteString(k.decls)
	b.WriteString("func use(n int) { fmt.Println(\"use\", n) }\nfunc twice(n int) int { return 2 * n }\n")
	b.WriteString("func no() bool { return false }\nfunc yes() bool { return true }\n")
	b.WriteString("func lockBump(m ref sync.Mutex<int>) int { g := m.Lock(); *g.Get() += 1; return *g.Get() }\n")
	var calls []string
	for i, f := range forms {
		if strings.Contains(strings.ReplaceAll(f, "@BODY", ""), "@B") && k.bind == "" {
			continue
		}

		n := strconv.Itoa(i + 1)
		expand := func(s string) string {
			s = strings.ReplaceAll(s, "@BODY", strings.ReplaceAll(k.body, "%", n)) // before @B, which it begins with
			s = strings.ReplaceAll(s, "@R", strings.ReplaceAll(k.read, "%", n))
			s = strings.ReplaceAll(s, "@M", strings.ReplaceAll(k.change, "%", n))
			s = strings.ReplaceAll(s, "@B", strings.ReplaceAll(k.bind, "%", n))
			return strings.ReplaceAll(s, "%", n)
		}

		b.WriteString("func ret" + n + "() int { g := sync.NewMutex(0); _ = ref g; return " + expand("@R") + " }\n")
		b.WriteString("func form" + n + "() {\n\tg := sync.NewMutex(0)\n\t_ = ref g\n\tdone := make(chan ())\n\t_ = ref done\n\t" + expand(f) + "\n\tfmt.Println(\"end\", " + n + ")\n}\n")
		calls = append(calls, "\tform"+n+"()\n")
	}

	b.WriteString("func main() {\n" + strings.Join(calls, "") + "}\n")
	return b.String()
}

// TestBackendsAgree builds each program through both backends, in a
// release and a debug build, and requires one output and a clean exit
// from all four.
func TestBackendsAgree(t *testing.T) {
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			src := program(k)
			outs := map[string]string{}
			for _, backend := range []string{"c", "llvm"} {
				for _, debug := range []bool{false, true} {
					name := backend + map[bool]string{false: "", true: "-debug"}[debug]
					out, code := run(t, k.name, src, backend, debug)
					if code != 0 {
						t.Errorf("%s: exit %d\n%s\n--- program ---\n%s", name, code, out, src)
						continue
					}

					outs[name] = out
				}
			}

			if outs["c"] != outs["llvm"] || outs["c"] != outs["c-debug"] || outs["llvm"] != outs["llvm-debug"] {
				t.Errorf("the backends disagree\n--- c ---\n%s--- llvm ---\n%s--- program ---\n%s", outs["c"], outs["llvm"], src)
			}
		})
	}
}

// run builds src through backend and runs it, returning what it wrote
// to both streams and its exit code; a program that hangs is stopped.
func run(t *testing.T, name, src, backend string, debug bool) (string, int) {
	t.Helper()
	f, file, err := parser.ParseFile(name+".snello", []byte(src))
	if err != nil {
		t.Fatalf("%v\n%s", err, src)
	}

	info := &types.Info{}
	imp := importer.NewImporter(info)
	if _, err := (&types.Config{Importer: imp}).Check("", file, f, info); err != nil {
		t.Fatalf("%v\n%s", err, src)
	}

	pkgs := imp.Packages()
	libs := make([]lower.Library, len(pkgs))
	for i, p := range pkgs {
		libs[i] = lower.Library{Path: p.Path, File: p.File, Syntax: p.Syntax, CFiles: p.CFiles, Links: p.Links}
	}

	var emitted []byte
	if backend == "llvm" {
		emitted, err = llvm.Emit(llvm.Config{}, file, f, info, libs...)
	} else {
		emitted, err = c.Emit(file, f, info, libs...)
	}

	if err != nil {
		t.Fatalf("%s: %v", backend, err)
	}

	dir := t.TempDir()
	var bin string
	var out []byte
	if backend == "llvm" {
		bin, out, err = llvm.Build(dir, name, emitted, debug)
	} else {
		bin, out, err = c.Build(dir, name, emitted, debug)
	}

	if err != nil {
		t.Fatalf("%s: %v\n%s", backend, err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	cmd.Dir = filepath.Dir(bin)
	got, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); ok {
		return string(got), exit.ExitCode()
	}

	if err != nil {
		t.Fatalf("running %s: %v", bin, err)
	}

	return string(got), 0
}

func TestMain(m *testing.M) {
	if _, err := exec.LookPath("clang"); err != nil && os.Getenv("CLANG") == "" {
		os.Exit(0) // the LLVM backend needs clang: CI and every developer machine have it
	}

	os.Exit(m.Run())
}
