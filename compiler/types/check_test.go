// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/compiler/scanner"
	"github.com/snello-lang/snello/compiler/token"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		src  string
		want []string // one substring per expected error, in position order
	}{
		// Names resolve in any order at the top level.
		{"package p\nfunc f() Node { return g() }\nfunc g() Node { return Node{} }\nstruct Node {}", nil},
		// Locals, parameters and the receiver.
		{"package p\nstruct T { n int }\nfunc (t ref T) f(x int) int { y := x + t.n; return y }", nil},
		// Variants are unqualified names; a bare pattern name is a binding unless it is a variant.
		{"package p\nenum S { A; B(n int) }\nfunc f(s S) int { return match s { A => 0, B(n) => n, other => 1 } }", nil},
		{"package p\nfunc f(x int?) int { return x ?? return 0 }", nil},
		{"package p\nfunc f(x int?) int { if n := x { return n }; return 0 }", nil},
		{"package p\nfunc f(xs []int) { for i, x := range xs { print(i, x) } }", nil},
		{"package p\nfunc f() { x := 1; { x := 2; print(x) }; print(x) }", nil},
		{"package p\nfunc f() { count := 0; g := func() { count += 1 }; g() }", nil},
		{"package p\nfunc f() { x := 1; x, y := 2, 3; print(x, y) }", nil},
		{"package p\nimport \"fmt\"\nfunc f() { fmt.Println(1) }", nil},
		{"package p\nimport \"net/http\"\nfunc f() { http.Get(\"\") }", []string{"2:8: package net/http is not in the standard library"}}, // the use is not reported again
		{"package p\nstruct T { a int }\nfunc f() { t := T{a: 1}; m := map[string]int{\"k\": 1}; print(t, m) }", nil},
		{"package p\nstruct TempDir impl Drop { path string }\nfunc (t mut TempDir) Drop() {}", nil},
		{"package p\nfunc f() { var x, y int = 1, 2; const c = 3; print(x, y, c) }", nil},

		{"package p\nfunc f() { print(y) }", []string{"2:18: undefined: y"}},
		{"package p\nfunc f() { x := nil }", []string{"2:17: nil is not defined; an absent value is None"}},
		{"package p\nfunc f() {}\nfunc f() {}", []string{"3:6: f redeclared in this block; first declared at 2:6"}},
		{"package p\nfunc T() {}\nstruct T { x int }\nfunc g() T { return T{x: 1} }", []string{"3:8: T redeclared in this block; first declared at 2:6", "4:10: T is not a type", "4:21: T is not a type"}}, // the first declaration wins; no panic
		{"package p\nenum A { X(n int) }\nenum B { X(s string) }", []string{"3:10: X redeclared in this block; first declared at 2:10"}},
		{"package p\nfunc f() Result<int, error> { return Ok(1) }", nil},
		{"package p\nfunc f() Result<int> { return Ok(1) }", []string{"2:10: Result takes two type arguments"}}, // Ok(1) against the invalid type says nothing more
		{"package p\nfunc f() { x := 1; x := 2; print(x) }", []string{"2:22: no new variables on the left side of :="}},
		{"package p\nfunc f() { _ := 1 }", []string{"2:14: no new variables on the left side of :="}},
		{"package p\nfunc f(x int) { _ = x; _, y := x, x; print(y) }", nil},
		{"package p\nfunc f() { print(_) }", []string{"2:18: cannot use _ as a value"}},
		{"package p\nfunc f(x int?) { if n := x { print(n) } else { print(n) } }", []string{"2:54: undefined: n"}},
		{"package p\nimport \"fmt\"\nfunc f() { fmt := 1; print(fmt) }", nil}, // a local may shadow an import
		{"package p\nfunc f(x int) { var x int }", []string{"2:21: x redeclared in this block; first declared at 2:8"}},
		{"package p\nstruct T { a int; a string }", []string{"2:19: duplicate field a"}},
		{"package p\nenum A { X }\nenum B { X }", []string{"3:10: X redeclared in this block; first declared at 2:10"}},
		{"package p\nstruct T {}\nfunc (t ref T) m() {}\nfunc (t ref T) m() {}", []string{"4:16: method T.m redeclared; first declared at 3:16"}},
		{"package p\nfunc f() { x := 1; print(x) }\nfunc g() Missing { return 0 }", []string{"3:10: undefined: Missing"}},
		{"package p\nfunc f() { var x f }\nfunc f2() {}", []string{"2:18: f is not a type"}},
		{"package p\nfunc f() { x := 1; { y := x }; print(y) }", []string{"2:38: undefined: y"}},
		{"package p\nfunc f(xs []int) { for i := range xs {}; print(i) }", []string{"2:48: undefined: i"}},
		{"package p\nfunc f<T>(x T) {}", nil},
		{"package p\nstruct S<T> { x T }", nil},
		{"package p\ntrait Shape { func (s ref Self) Area() f64 }\nstruct C impl Shape {}\nfunc (c ref C) Area() f64 { return 1.0 }", nil},
		{"package p\nfunc f(x int) { match x { 1 => y, _ => 0 } }", []string{"2:32: undefined: y"}},
		{"package p\nfunc f(xs [99999999999999999999]int) {}", []string{"2:12: 99999999999999999999 overflows int"}},
		{"package p\nfunc f(xs [-1]int) {}", []string{"2:12: array length must be a non-negative integer constant, not -1"}},
		{"package p\nfunc f(xs [2.5]int) {}", []string{"2:12: array length must be a non-negative integer constant, not 2.5"}},
		{"package p\nvar n = 3\nfunc f(xs [n]int) {}", []string{"3:12: array length must be a non-negative integer constant: n is not constant"}},
		{"package p\nconst N = 4\nfunc f(xs [2 * N]int) {}", nil},
		{"package p\nstruct Grid { cells [Rows * Cols]int }\nconst Rows = 2\nconst Cols = Rows + 1\nfunc f(g ref Grid) [6]int { return g.cells }", nil}, // the length of a field, from constants declared after the type, is the array the function returns
		{"package p\nstruct Grid { cells [Rows]int }\nconst Rows = len(\"ab\")", []string{"3:14: len(...) is not constant"}},                            // reported at the constant, not again at the length
		{"package p\nstruct S { a [N]int }\nconst N Count = 4\ntype Count int", nil},                                                                    // a constant an array length needs, of a type declared after both
		{"package p\nstruct S { a [N]int }\nconst N = int(Celsius(4.0))\ntype Celsius f64", nil},                                                        // a conversion to a type declared after it
		{"package p\ntype A B\ntype B f64\nconst X A = 2.5\nfunc f() f64 { return f64(X) }", nil},                                                       // a type declared over a later one
		{"package p\nconst _ = nope + 1", []string{"2:11: undefined: nope"}},                                                                            // a blank constant is checked too
		{"package p\nconst _ u8 = 300", []string{"2:14: 300 overflows u8"}},
		{"package p\nconst X i8 = -129", []string{"2:15: -129 overflows i8"}}, // once, by the check of the literal
		{"package p\nfunc f() { const x i8 = -(129); print(x) }", []string{"2:27: 129 overflows i8"}},
		{"package p\nconst KB = 1 << 40\nconst X i64 = KB * KB", []string{"3:15: cannot use a value of type int as i64 in declaration"}}, // the type before the value
		{"package p\nfunc main(x int) {}", []string{"2:6: func main must have no type parameters, no parameters and no results"}},
		{"package p\nfunc main() int { return 0 }", []string{"2:6: func main must have"}},
		{"package p\nfunc main<T>() {}", []string{"2:6: func main must have"}},
		{"package p\nstruct S {}\nfunc (s ref S) main(x int) {}", nil}, // a method may be called main
		{"package p\nfunc f() { x := 1; print(x) }\nfunc g() { print(x) }", []string{"3:18: undefined: x"}},
	}
	for _, tt := range tests {
		got := checkErrors(t, tt.src)
		if len(got) != len(tt.want) {
			t.Errorf("%q:\n  got %d errors %q\n  want %d %q", tt.src, len(got), got, len(tt.want), tt.want)
			continue
		}

		for i := range got {
			if !strings.Contains(got[i], tt.want[i]) {
				t.Errorf("%q:\n  got  %q\n  want %q", tt.src, got[i], tt.want[i])
			}
		}
	}
}

// TestInfo checks that every identifier in a small program lands in
// Defs or Uses, pointing at the right object.
func TestInfo(t *testing.T) {
	src := "package p\nstruct T { n int }\nenum S { A; B(k int) }\nfunc (t ref T) get() int { return t.n }\nfunc main() { v := T{n: 1}; x := v.get(); print(x); s := A; print(match s { A => 0, B(k) => k, other => 1 }) }"
	f, file, err := parser.ParseFile("info.snello", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	info := &Info{}
	if _, err := Check(file, f, info); err != nil {
		t.Fatal(err)
	}

	defs := map[string]string{} // name -> object kind
	for ident, obj := range info.Defs {
		defs[ident.Name] = fmt.Sprintf("%T", obj)
	}

	uses := map[string]string{}
	for ident, obj := range info.Uses {
		uses[ident.Name] = fmt.Sprintf("%T", obj)
	}

	wantDefs := map[string]string{"T": "*types.TypeName", "n": "*types.Var", "t": "*types.Var", "get": "*types.Func", "main": "*types.Func", "v": "*types.Var", "x": "*types.Var", "S": "*types.TypeName", "A": "*types.Variant", "B": "*types.Variant", "k": "*types.Var", "s": "*types.Var", "other": "*types.Var"}
	wantUses := map[string]string{"T": "*types.TypeName", "t": "*types.Var", "v": "*types.Var", "x": "*types.Var", "print": "*types.Builtin", "int": "*types.TypeName", "A": "*types.Variant", "B": "*types.Variant", "k": "*types.Var", "s": "*types.Var", "get": "*types.Func", "n": "*types.Var"} // A in a pattern is a use, other a binding; get and n are the selector and the field key
	for name, kind := range wantDefs {
		if defs[name] != kind {
			t.Errorf("Defs[%s] = %q, want %q", name, defs[name], kind)
		}
	}

	for name, kind := range wantUses {
		if uses[name] != kind {
			t.Errorf("Uses[%s] = %q, want %q", name, uses[name], kind)
		}
	}

	if len(defs) != len(wantDefs) || len(uses) != len(wantUses) {
		t.Errorf("Defs names %v and Uses names %v, want %v and %v", defs, uses, wantDefs, wantUses)
	}
}

// handledErrors lists the negative programs whose error the checker
// already reports. Each must produce exactly its annotated error; every
// other negative program must produce no error at all, since the
// checker has no business rejecting it for another reason.
var handledErrors = map[string]bool{
	"nil-used.snello":            true,
	"missing-arm.snello":         true,
	"wrong-question-kind.snello": true,
	"ref-as-mut.snello":          true,
	"use-after-move.snello":      true,
	"move-in-branch.snello":      true,
	"closure-moves.snello":       true,
	"use-after-close.snello":     true,
	"move-in-loop.snello":        true,
	"evaluation-order.snello":    true,
	"moved-in-index.snello":      true,
	"global-moved-init.snello":   true,
	"move-out-of-slice.snello":   true,
	"move-out-of-borrow.snello":  true,
	"partial-move.snello":        true,
	// Borrows.
	"mut-while-ref.snello":            true,
	"two-mut.snello":                  true,
	"two-slots.snello":                true,
	"assign-while-borrowed.snello":    true,
	"move-while-borrowed.snello":      true,
	"assign-through-ref.snello":       true,
	"mut-through-ref-receiver.snello": true,
	"mut-of-constant.snello":          true,
	"element-not-eq.snello":           true,
	"pieces-outlive-string.snello":    true,
	"pieces-stored.snello":            true,
	"defer-holds-view.snello":         true,
	"literal-overflows.snello":        true,
	"closure-writes-viewed.snello":    true,
	// The owner outlives every borrow, on every path.
	"borrow-outlives-block.snello":       true,
	"borrow-field-outlives-block.snello": true,
	"borrow-next-pass.snello":            true,
	"borrow-into-caller.snello":          true,
	"closure-stores-borrow.snello":       true,
	"defer-outlives-block.snello":        true,
	"drop-reads-borrow.snello":           true,
	"borrow-out-of-block.snello":         true,
	"closure-used-where-made.snello":     true,
	"defer-borrows-whole.snello":         true,
	// Escaping borrows.
	"borrow-in-slice.snello":        true,
	"borrow-struct-in-slice.snello": true,
	"closure-stored.snello":         true,
	"callback-kept.snello":          true,
	"borrow-across-spawn.snello":    true,
	"borrow-on-channel.snello":      true,
	"return-two-borrows.snello":     true,
	"return-local-borrow.snello":    true,
	"closure-returned.snello":       true,
	// Task boundaries.
	"use-after-spawn.snello":  true,
	"spawn-twice.snello":      true,
	"spawn-not-send.snello":   true,
	"channel-not-send.snello": true,
	// Lock-protected data, reached only through a guard.
	"guard-across-spawn.snello": true,
	"guard-of-temporary.snello": true,
	// The races of the stage's checkpoint.
	"use-after-send.snello":            true,
	"mutate-through-arc.snello":        true,
	"borrowing-closure-spawned.snello": true,
	// Package variables, reached from every task.
	"global-assigned.snello":          true,
	"global-mut.snello":               true,
	"global-moved.snello":             true,
	"global-not-send.snello":          true,
	"global-declared-not-send.snello": true,
	"global-trait-not-send.snello":    true,
	// The Error trait: ? passes an error on, the derived message, the + flag.
	"try-ambiguous.snello":     true,
	"try-no-variant.snello":    true,
	"error-unprintable.snello": true,
	"format-plus.snello":       true,
	// Formats, checked when the program is compiled.
	"format-verb.snello": true,
	"format-zero.snello": true,
	"format-size.snello": true,
	"time-layout.snello": true,
	// A constant's name is no pattern.
	"match-constant.snello": true,
	// A constant is computed by the compiler, in the arithmetic of its
	// type, in any order but none that leads back to it; a package
	// variable's inferred type is known after its declaration.
	"const-not-constant.snello": true,
	"const-overflows.snello":    true,
	"const-cycle.snello":        true,
	"global-before-type.snello": true,
	// A variadic parameter: the arguments move into ...T and are lent
	// for the call alone to ...ref T; ... passes the slice itself.
	"variadic-moved.snello":    true,
	"variadic-lent.snello":     true,
	"variadic-spread.snello":   true,
	"variadic-deferred.snello": true,
	"variadic-kept.snello":     true,
	// A field's default is a constant of its type, and a struct that
	// declares one has no zero value.
	"default-not-constant.snello": true,
	"default-no-zero.snello":      true,
	// A conversion moves the value, and takes no borrow of it.
	"conversion-moves.snello": true,
	// No part leaves a value whose type implements Drop.
	"move-out-of-drop.snello": true,
	// A field reflection reaches is lent, and a loop over a type's parts
	// is written out per part.
	"reflect-lent.snello": true,
	"reflect-loop.snello": true,
	// A deferred print or built-in saves views of its arguments.
	"defer-printed.snello": true,
	// A range over an iterator: a value with Next on a mut receiver.
	"range-iterator.snello": true,
	// A borrow of a temporary, or of a part of one, dies with its statement.
	"borrow-of-temporary-part.snello": true,
	// A Drop runs once, by the language, on a struct or an enum.
	"drop-by-hand.snello":       true,
	"drop-not-composite.snello": true,
	// A handle carries a task's result to the task that joins, so the
	// result is Send.
	"handle-not-send.snello": true,
	// A channel ends when its last sending handle is dropped: close is gone.
	"close-gone.snello":               true,
	"iter-outlives-source.snello":     true,
	"iter-source-changed.snello":      true,
	"iter-adapter-stored.snello":      true,
	"iter-next-borrows-itself.snello": true,
	"generic-hides-borrow.snello":     true,
	"generic-owned-borrow.snello":     true,
	"generic-mut-takes-borrow.snello": true,
	"borrow-into-itself.snello":       true,
	"borrow-of-itself.snello":         true,
	// A package variable is made on first use, so one named _ is refused.
	"blank-package-var.snello": true,
	// The blank name drops a guard as it is made.
	"blank-guard.snello": true,
	// A match guard runs whether or not its arm is taken: it may not move a value declared before it, and the value is moved after the match.
	"move-in-guard.snello": true,
	// panic takes one message, a string.
	"panic-not-string.snello": true,
	// Nothing runs before main, so a function named init is refused.
	"init-func.snello": true,
	// A borrow of a local leaves in no literal, and a tuple holds no
	// closure that borrows.
	"literal-borrow-escapes.snello": true,
	"tuple-closure-stored.snello":   true,
	// A return ends a function, so no package variable's initializer
	// holds one.
	"return-outside-function.snello": true,
	// An if or a match with an owned branch is a temporary, which no ref
	// slot keeps.
	"branch-temporary.snello": true,
	// An if or a match that gives a borrow gives one from every branch.
	"owned-branch-lent.snello": true,
	// A value an expression makes and lends lives to its statement's end.
	"lent-temporary.snello": true,
	// An assignment writes a place, never a value an expression makes.
	"assign-to-value.snello": true,
	// A value lends what the values it is made of lend, followed through
	// every step that makes it.
	"lent-through-values.snello": true,
}

func TestNegativePrograms(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "errors", "*.snello"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no negative programs found: %v", err)
	}

	conf := libraryConfig()
	for _, path := range paths {
		f, file, src := parseFile(t, path)
		_, cerr := conf.Check("", file, f, nil)
		if !handledErrors[filepath.Base(path)] {
			if cerr != nil {
				t.Errorf("%s: unexpected error: %v", path, cerr)
			}

			continue
		}

		want := map[int]string{}
		for i, line := range strings.Split(string(src), "\n") {
			if m := errorPattern.FindStringSubmatch(line); m != nil {
				want[i+1] = m[1]
			}
		}

		got := map[int][]string{}
		if list, ok := cerr.(scanner.ErrorList); ok {
			for _, e := range list {
				got[e.Pos.Line] = append(got[e.Pos.Line], e.Msg)
			}
		}

		for line, text := range want {
			switch msgs := got[line]; {
			case len(msgs) == 0:
				t.Errorf("%s:%d: no error, want one containing %q", path, line, text)
			case len(msgs) > 1:
				t.Errorf("%s:%d: %d errors, want one: %q", path, line, len(msgs), msgs)
			case !strings.Contains(msgs[0], text):
				t.Errorf("%s:%d: got %q, want it to contain %q", path, line, msgs[0], text)
			}
		}

		for line, msgs := range got {
			if _, ok := want[line]; !ok {
				t.Errorf("%s:%d: unexpected error %q", path, line, msgs[0])
			}
		}
	}
}

var errorPattern = regexp.MustCompile(`// ERROR "(.*)"\s*$`)

// BenchmarkCheck resolves every example program that runs once per
// iteration, with the parse done ahead of time and the record the
// compiler keeps, and reports allocations.
func BenchmarkCheck(b *testing.B) {
	type parsed struct {
		f    *ast.File
		file *token.File
	}

	var files []parsed
	for _, path := range examplePrograms(b) {
		f, file, _ := parseFile(b, path)
		files = append(files, parsed{f, file})
	}

	conf := libraryConfig() // the library packages are checked once, on the first import
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, p := range files {
			if _, err := conf.Check("", p.file, p.f, &Info{}); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func checkErrors(t *testing.T, src string) []string {
	t.Helper()
	f, file, err := parser.ParseFile("test.snello", []byte(src))
	if err != nil {
		t.Fatalf("%q: parse error: %v", src, err)
	}

	_, err = libraryConfig().Check("", file, f, nil)
	if err == nil {
		return nil
	}

	var msgs []string
	for _, e := range err.(scanner.ErrorList) {
		msgs = append(msgs, e.Error())
	}

	return msgs
}

// examplePrograms returns the example programs of one file that run:
// those with their expected output beside them.
func examplePrograms(tb testing.TB) []string {
	tb.Helper()
	var paths []string
	for _, pattern := range []string{filepath.Join("*", "*.snello")} {
		matches, err := filepath.Glob(filepath.Join("..", "..", "examples", pattern))
		if err != nil {
			tb.Fatal(err)
		}

		for _, path := range matches {
			if _, err := os.Stat(strings.TrimSuffix(path, ".snello") + ".out"); err == nil {
				paths = append(paths, path)
			}
		}
	}

	if len(paths) < 20 {
		tb.Fatalf("found %d example programs that run, want at least 20", len(paths))
	}

	return paths
}

func parseFile(tb testing.TB, path string) (*ast.File, *token.File, []byte) {
	tb.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}

	f, file, err := parser.ParseFile(path, src)
	if err != nil {
		tb.Fatalf("%s: %v", path, err)
	}

	return f, file, src
}

// A fileImporter loads the library packages from the std directory
// beside this package, every source of a package built for the host,
// as the compiler's embedded importer does, for the programs that
// import them; this package cannot use that importer, which is built
// on it.
type fileImporter struct {
	conf   Config // the program's
	std    Config // the library's, with what only the standard library may write
	loaded map[string]*Package
}

func libraryConfig() *Config {
	imp := &fileImporter{loaded: make(map[string]*Package)}
	imp.conf.Importer = imp
	imp.std = Config{Importer: imp, Standard: true}
	return &imp.conf
}

func (imp *fileImporter) Import(path string) (*Package, error) {
	if pkg := imp.loaded[path]; pkg != nil {
		return pkg, nil
	}

	dir := filepath.Join("..", "..", "std", path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, errors.New("package " + path + " is not in the standard library")
	}

	fset := token.NewFileSet()
	var files []*token.File
	var syntax []*ast.File
	for _, e := range entries { // a package's sources for the host, as the compiler's importer chooses them
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".snello") || strings.HasSuffix(name, "_test.snello") || !platform.Host().Builds(name) {
			continue
		}

		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}

		file := fset.AddFile(filepath.Join(dir, name), len(src))
		f, err := parser.Parse(file, src)
		if err != nil {
			return nil, err
		}

		files, syntax = append(files, file), append(syntax, f)
	}

	pkg, err := imp.std.CheckFiles(path, files, syntax, nil)
	if err != nil {
		return nil, err
	}

	imp.loaded[path] = pkg
	return pkg, nil
}

// A sourceImporter checks library packages given as source, for a test
// of what crosses a package boundary; they stand for the standard
// library and are checked as it is.
type sourceImporter struct {
	conf   Config
	srcs   map[string]string
	loaded map[string]*Package
	info   *Info // the tables the packages share, as the compiler's importer shares them; nil for none
}

func (imp *sourceImporter) Import(path string) (*Package, error) {
	if pkg := imp.loaded[path]; pkg != nil {
		return pkg, nil
	}

	src, ok := imp.srcs[path]
	if !ok {
		return nil, errors.New("package " + path + " is not in the standard library")
	}

	f, file, err := parser.ParseFile(path+".snello", []byte(src))
	if err != nil {
		return nil, err
	}

	std := Config{Importer: imp.conf.Importer, Standard: true}
	pkg, err := std.Check(path, file, f, imp.info)
	if err != nil {
		return nil, err
	}

	imp.loaded[path] = pkg
	return pkg, nil
}

// TestTraitInstancesAcrossPackages checks that an instance of a
// library's generic trait made by the program's check is the one the
// library made: a program's bound It<int> is met by the library's type
// listing It<int>, and a library bound by the program's type.
func TestTraitInstancesAcrossPackages(t *testing.T) {
	lib := "package it\ntrait It<U> { func (s mut Self) Next() U? }\nstruct Lib impl It<int> { n int }\nfunc (l mut Lib) Next() int? { return None }\nfunc Drain<I: It<int>>(x mut I) int { return 0 }\n"
	tests := []struct {
		src  string
		want string
	}{
		{"import \"it\"\nfunc Sum<I: it.It<int>>(x mut I) int { return 0 }\nfunc f(l mut it.Lib) int { return Sum(mut l) }", ""},
		{"import \"it\"\nstruct Mine impl it.It<int> { n int }\nfunc (m mut Mine) Next() int? { return None }\nfunc f(m mut Mine) int { return it.Drain(mut m) }", ""},
		{"import \"it\"\nfunc Sum<I: it.It<string>>(x mut I) int { return 0 }\nfunc f(l mut it.Lib) int { return Sum(mut l) }", "cannot use it.Lib as the type parameter I of Sum: it.Lib does not implement It<string>"},
	}
	for _, tt := range tests {
		imp := &sourceImporter{srcs: map[string]string{"it": lib}, loaded: map[string]*Package{}}
		imp.conf.Importer = imp
		f, file, err := parser.ParseFile("test.snello", []byte("package main\n"+tt.src))
		if err != nil {
			t.Fatal(err)
		}

		_, err = imp.conf.Check("", file, f, nil)
		got := ""
		if err != nil {
			got = err.Error()
		}

		if tt.want == "" && got != "" || tt.want != "" && !strings.Contains(got, tt.want) {
			t.Errorf("%s:\n  got  %q\n  want %q", tt.src, got, tt.want)
		}
	}
}

// TestCheckFiles checks packages of several files: the files see each
// other's declarations, each sees its own imports, and the messages
// for the mistakes only several files can make.
func TestCheckFiles(t *testing.T) {
	tests := []struct {
		files []string // the sources, a.snello then b.snello
		want  []string // one substring per expected error, in position order
	}{
		{[]string{"package p\nstruct T { n int }\nfunc New() T { return T{n: 1} }", "package p\nfunc f() int { t := New(); return t.n + count }\nvar count = 2"}, nil},
		{[]string{"package p\nfunc f() {}", "package p\nfunc f() {}"}, []string{"b.snello:2:6: f redeclared in this block; first declared at a.snello:2:6"}},
		{[]string{"package p\nimport \"math\"\nfunc f() f64 { return math.Pi }", "package p\nfunc g() f64 { return math.Pi }"}, []string{"b.snello:2:23: undefined: math"}},
		{[]string{"package p\nfunc f() {}", "package q\nfunc g() {}"}, []string{"b.snello:1:9: found packages p (a.snello) and q (b.snello) in pkg"}},
		{[]string{"package p\nstruct A { b B }", "package p\nstruct B { a A }"}, []string{"a.snello:2:8: invalid recursive type A"}},
		{[]string{"package p\nfunc f() int { return g() }", "package p\nfunc g() int { return h }"}, []string{"b.snello:2:23: undefined: h"}},
		{[]string{"package p\ntrait Named { func (s ref Self) Name() string }\nstruct T impl Named {}", "package p\nfunc (t ref T) Name() string { return \"t\" }\nfunc f(t ref T) string { return t.Name() }"}, nil}, // a trait and its implementation across files
		{[]string{"package p\nconst MB = KB * KB\nstruct Page { bytes [KB]byte }", "package p\nimport \"math\"\nconst KB = 1 << 10\nconst Pi2 = math.Pi * 2.0"}, nil},                                                 // a constant of another file, needed by a type before the constants are checked, with that file's imports
		// A field's default is checked with its own file's imports, after a declaration of another file was checked on demand: an array length, a type declared over a later one, and a name the other file gives another package.
		{[]string{"package p\nimport \"os\"\nstruct Conf { arr [N]int; perm os.FileMode = os.ModePerm }", "package p\nconst N = 3"}, nil},
		{[]string{"package p\nimport \"os\"\ntype Alias Later\nstruct Conf { perm os.FileMode = os.ModePerm }", "package p\ntype Later int"}, nil},
		{[]string{"package p\nimport \"os\"\nstruct Conf { arr [N]int; perm int = int(os.ModePerm) }", "package p\nimport os \"math\"\nconst N = 3\nconst Pi2 = os.Pi * 2.0"}, nil},
		{[]string{"package p\nconst A = B + 1", "package p\nconst B = A * 2"}, []string{"a.snello:2:7: initialization cycle: A refers to B, B refers to A"}},
		{[]string{"package p\nconst Low u8 = High - 300", "package p\nconst High u8 = 255"}, []string{"a.snello:2:23: 300 overflows u8"}}, // the literal, typed u8 by its context, before any arithmetic
	}
	for _, tt := range tests {
		fset := token.NewFileSet()
		var files []*token.File
		var syntax []*ast.File
		for i, src := range tt.files {
			name := "pkg/" + string(rune('a'+i)) + ".snello"
			file := fset.AddFile(name, len(src))
			f, err := parser.Parse(file, []byte(src))
			if err != nil {
				t.Fatalf("%q: parse error: %v", src, err)
			}

			files = append(files, file)
			syntax = append(syntax, f)
		}

		var got []string
		if _, err := libraryConfig().CheckFiles("", files, syntax, nil); err != nil {
			for _, e := range err.(scanner.ErrorList) {
				got = append(got, e.Error())
			}
		}

		if len(got) != len(tt.want) {
			t.Errorf("%q: got %d errors %q, want %d %q", tt.files, len(got), got, len(tt.want), tt.want)
			continue
		}

		for i, w := range tt.want {
			if !strings.Contains(got[i], w) {
				t.Errorf("%q: error %d = %q, want it to contain %q", tt.files, i, got[i], w)
			}
		}
	}
}

// TestAt records the names visible at Config.At as the checker resolves
// the name there: the locals of the blocks around it and the function's
// parameters, the package's names, the imports and the universe's, the
// innermost first; no local declared further on or in another block;
// and nothing for a check with no position.
func TestAt(t *testing.T) {
	const src = `package p

import "strings"

var top int

func f(param int) int {
	early := 1
	if early > 0 {
		inner := 2
		_ = inner
	}

	for i := 0; i < 2; i++ {
		inLoop := i
		_ = inLoop + early + param + placeholder
	}

	late := 3
	return late + len(strings.ToUpper("a"))
}
`
	f, file, err := parser.ParseFile("p.snello", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	conf := libraryConfig()
	conf.At = file.Pos(strings.Index(src, "placeholder"))
	info := &Info{}
	conf.Check("", file, f, info) // placeholder is undefined, and the check goes on

	var names []string
	for _, obj := range info.Visible {
		names = append(names, obj.Name())
	}

	first := func(name string) int { return slices.Index(names, name) }
	for _, name := range []string{"inLoop", "i", "early", "param", "top", "f", "strings", "len", "int"} {
		if first(name) < 0 {
			t.Errorf("%s is not visible: %q", name, names)
		}
	}

	for _, name := range []string{"inner", "late"} {
		if first(name) >= 0 {
			t.Errorf("%s is visible, though it is declared in another block or further on", name)
		}
	}

	if !(first("inLoop") < first("early") && first("early") < first("param") && first("param") < first("top") && first("top") < first("int")) {
		t.Errorf("not the innermost first: %q", names)
	}

	conf.At = token.NoPos
	again := &Info{}
	conf.Check("", file, f, again)
	if again.Visible != nil {
		t.Errorf("a check with no position recorded %d names", len(again.Visible))
	}
}
