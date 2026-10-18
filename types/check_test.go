// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/parser"
	"github.com/teak-lang/teak/scanner"
	"github.com/teak-lang/teak/token"
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
		{"package p\nimport \"net/http\"\nfunc f() { http.Get(\"\") }", nil},
		{"package p\nstruct T { a int }\nfunc f() { t := T{a: 1}; m := map[string]int{\"k\": 1}; print(t, m) }", nil},
		{"package p\nstruct TempDir impl Drop { path string }\nfunc (t mut TempDir) Drop() {}", nil},
		{"package p\nfunc f() { var x, y int = 1, 2; const c = 3; print(x, y, c) }", nil},

		{"package p\nfunc f() { print(y) }", []string{"2:18: undefined: y"}},
		{"package p\nfunc f() { x := nil }", []string{"2:17: nil is not defined; an absent value is None"}},
		{"package p\nfunc f() {}\nfunc f() {}", []string{"3:6: f redeclared in this block; first declared at 2:6"}},
		{"package p\nfunc T() {}\nstruct T { x int }\nfunc g() T { return T{x: 1} }", []string{"3:8: T redeclared in this block; first declared at 2:6", "4:10: T is not a type", "4:21: T is not a type"}}, // the first declaration wins; no panic
		{"package p\nenum A { X(n int) }\nenum B { X(s string) }", []string{"3:10: X redeclared in this block; first declared at 2:10"}},
		{"package p\nfunc f() Result<int, error> { return Ok(1) }", nil},
		{"package p\nfunc f() Result<int> { return Ok(1) }", []string{"2:10: Result takes two type arguments"}},
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
		{"package p\nfunc f() { x := 1; print(x()) }\nfunc g() Missing {}", []string{"3:10: undefined: Missing"}},
		{"package p\nfunc f() { var x f }\nfunc f2() {}", []string{"2:18: f is not a type"}},
		{"package p\nfunc f() { x := 1; { y := x }; print(y) }", []string{"2:38: undefined: y"}},
		{"package p\nfunc f() { for i := range 3 {}; print(i) }", []string{"2:39: undefined: i"}},
		{"package p\nfunc f<T>(x T) {}", []string{"2:1: generic functions are not part of core Teak", "2:13: undefined: T"}},
		{"package p\nstruct S<T> { x T }", []string{"2:1: generic types are not part of core Teak", "2:17: undefined: T"}},
		{"package p\ntrait Shape {}", []string{"2:1: traits are not part of core Teak"}},
		{"package p\nfunc f(x int) { match x { 1 => y, _ => 0 } }", []string{"2:32: undefined: y"}},
		{"package p\nfunc f(xs [99999999999999999999]int) {}", []string{"2:12: array length must be a non-negative integer literal"}},
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
	f, file, err := parser.ParseFile("info.teak", []byte(src))
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
	wantUses := map[string]string{"T": "*types.TypeName", "t": "*types.Var", "v": "*types.Var", "x": "*types.Var", "print": "*types.Builtin", "int": "*types.TypeName", "A": "*types.Variant", "B": "*types.Variant", "k": "*types.Var", "s": "*types.Var"} // A in a pattern is a use, other a binding
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

// TestCoreExamples checks every positive core program and expects no
// errors: every identifier in them resolves.
func TestCoreExamples(t *testing.T) {
	for _, path := range corePrograms(t) {
		f, file, _ := parseFile(t, path)
		if _, err := Check(file, f, nil); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
}

// handledErrors lists the negative programs whose error the checker
// already reports. Each must produce exactly its annotated error; every
// other negative program must produce no error at all, since the
// checker has no business rejecting it for another reason.
var handledErrors = map[string]bool{
	"nil-used.teak": true,
}

func TestNegativePrograms(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "examples", "core", "errors", "*.teak"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no negative programs found: %v", err)
	}

	for _, path := range paths {
		f, file, src := parseFile(t, path)
		_, cerr := Check(file, f, nil)
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

// BenchmarkCheck resolves every core program once per iteration, with
// the parse done ahead of time, and reports allocations.
func BenchmarkCheck(b *testing.B) {
	type parsed struct {
		f    *ast.File
		file *token.File
	}

	var files []parsed
	for _, path := range corePrograms(b) {
		f, file, _ := parseFile(b, path)
		files = append(files, parsed{f, file})
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		for _, p := range files {
			if _, err := Check(p.file, p.f, nil); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func checkErrors(t *testing.T, src string) []string {
	t.Helper()
	f, file, err := parser.ParseFile("test.teak", []byte(src))
	if err != nil {
		t.Fatalf("%q: parse error: %v", src, err)
	}

	_, err = Check(file, f, nil)
	if err == nil {
		return nil
	}

	var msgs []string
	for _, e := range err.(scanner.ErrorList) {
		msgs = append(msgs, e.Error())
	}

	return msgs
}

// corePrograms returns the positive core programs under examples/core.
func corePrograms(tb testing.TB) []string {
	tb.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "examples", "core", "*.teak"))
	if err != nil || len(paths) < 8 {
		tb.Fatalf("found %d core programs, want at least 8: %v", len(paths), err)
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
