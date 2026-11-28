// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lower

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/types"
)

// TestVariable finds the variable an expression names, through
// parentheses, and nothing for a field, an element or a call.
func TestVariable(t *testing.T) {
	f, file, err := parser.ParseFile("t.snello", []byte(`package main
struct Box { n int }
func main() {
	b := Box{n: 1}
	xs := []int{1}
	_ = b
	_ = (b)
	_ = b.n
	_ = xs[0]
	_ = len(xs)
}`))
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{}
	if _, err := (&types.Config{}).Check("", file, f, info); err != nil {
		t.Fatal(err)
	}

	body := f.Decls[1].(*ast.FuncDecl).Body.List
	wants := []string{"", "", "b", "b", "", "", ""}
	for i, s := range body[2:] {
		x := s.(*ast.AssignStmt).Rhs[0]
		got := ""
		if v := Variable(info, x); v != nil {
			got = v.Name()
		}

		if got != wants[i+2] {
			t.Errorf("statement %d: Variable = %q, want %q", i+2, got, wants[i+2])
		}
	}
}

// TestClonesAsBytes copies an element as its bytes only when its clone
// is them: a Copy type that writes no Clone, nor any type inside it.
func TestClonesAsBytes(t *testing.T) {
	f, file, err := parser.ParseFile("t.snello", []byte(`package main
struct Plain { n int }
struct Own impl Clone { n int }
func (o ref Own) Clone() Own { return Own{n: o.n + 1} }
struct Holds { o Own }
func main() {
	var a int
	var b Plain
	var c Own
	var d Holds
	var e string
	var g (int, Own)
	_ = (a, b, c, d, e, g)
}`))
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{}
	if _, err := (&types.Config{}).Check("", file, f, info); err != nil {
		t.Fatal(err)
	}

	wants := map[string]bool{"a": true, "b": true, "c": false, "d": false, "e": false, "g": false}
	for id, obj := range info.Defs {
		v, ok := obj.(*types.Var)
		if !ok {
			continue
		}

		if want, ok := wants[id.Name]; ok {
			if got := ClonesAsBytes(v.Type()); got != want {
				t.Errorf("ClonesAsBytes(%s %s) = %v, want %v", id.Name, v.Type(), got, want)
			}
		}
	}
}

// TestBitOp reads a function of math/bits from its key: the operation,
// and the width its name ends with, 64 for the one on uint; and reads
// no other function as one.
func TestBitOp(t *testing.T) {
	tests := []struct {
		key   string
		op    string
		width int
		ok    bool
	}{
		{"math/bits.LeadingZeros", "LeadingZeros", 64, true},
		{"math/bits.LeadingZeros8", "LeadingZeros", 8, true},
		{"math/bits.TrailingZeros16", "TrailingZeros", 16, true},
		{"math/bits.OnesCount32", "OnesCount", 32, true},
		{"math/bits.RotateLeft64", "RotateLeft", 64, true},
		{"math/bits.Reverse", "Reverse", 64, true},
		{"math/bits.Len8", "Len", 8, true},
		{"math/bits.Len7", "", 0, false},
		{"math/bits.Len128", "", 0, false},
		{"math/bits.Mul64", "", 0, false},
		{"math/bits.", "", 0, false},
		{"math.Len", "", 0, false},
		{"bits.Len", "", 0, false},
		{"fmt.Println", "", 0, false},
	}
	for _, tt := range tests {
		if op, width, ok := BitOp(tt.key); op != tt.op || width != tt.width || ok != tt.ok {
			t.Errorf("BitOp(%q) = %q, %d, %t, want %q, %d, %t", tt.key, op, width, ok, tt.op, tt.width, tt.ok)
		}
	}
}

// TestKey gives a key to the functions the compiler writes, fmt's
// and those of math/bits, and none to a function of the library's own
// or of the program's; HasSymbol holds for every function but the ones
// with a key that the runtime does not define, so for those of
// math/bits too.
func TestKey(t *testing.T) {
	f, file, err := parser.ParseFile("t.snello", []byte(`package main
import ("fmt"; "math"; "math/bits"; "os")
func twice(n int) int { return n * 2 }
func main() {
	fmt.Println(1)
	_ = fmt.Sprintf("%d", 1)
	fmt.Fprint(mut os.Stdout(), 1)
	_ = bits.Len8(1)
	_ = bits.RotateLeft(1, 2)
	_ = math.Floor(1)
	_ = twice(1)
}`))
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{}
	if _, err := (&types.Config{Importer: importer.NewImporter(info)}).Check("", file, f, info); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		key    string
		symbol bool
	}{
		{"fmt.Println", false},
		{"fmt.Sprintf", false},
		{"fmt.Fprint", false},
		{"math/bits.Len8", true},
		{"math/bits.RotateLeft", true},
		{"", true},
		{"", true},
	}
	for i, s := range f.Decls[len(f.Decls)-1].(*ast.FuncDecl).Body.List {
		var call ast.Expr
		switch s := s.(type) {
		case *ast.ExprStmt:
			call = s.X
		case *ast.AssignStmt:
			call = s.Rhs[0]
		}

		var fn *types.Func
		switch fun := call.(*ast.CallExpr).Fun.(type) {
		case *ast.Ident:
			fn = info.Uses[fun].(*types.Func)
		case *ast.SelectorExpr:
			fn = info.Uses[fun.Sel].(*types.Func)
		}

		if got := Key(fn); got != tests[i].key {
			t.Errorf("Key(%s) = %q, want %q", fn.Name(), got, tests[i].key)
		}

		if got := HasSymbol(fn); got != tests[i].symbol {
			t.Errorf("HasSymbol(%s) = %t, want %t", fn.Name(), got, tests[i].symbol)
		}
	}
}

// checkSource checks a program for the tests of what the backends
// read from a checked program.
func checkSource(t *testing.T, src string) (*ast.File, *types.Info) {
	t.Helper()
	f, file, err := parser.ParseFile("t.snello", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{}
	if _, err := (&types.Config{Importer: importer.NewImporter(info)}).Check("", file, f, info); err != nil {
		t.Fatal(err)
	}

	return f, info
}

// TestFramedVariadic keeps a ...T slice in its caller's frame when the
// function reads it, ranges over it, changes it or lends it, and not
// when it moves it whole or a closure captures it.
func TestFramedVariadic(t *testing.T) {
	f, info := checkSource(t, `package main
func reads(xs ...int) int { n := len(xs); for _, x := range xs { n += x }; return n }
func pushes(xs ...int) { xs.Push(1); xs[0] = 2 }
func lends(xs ...string) int { return count(ref xs) }
func count(xs ref []string) int { return len(xs) }
func returns(xs ...int) []int { return xs }
func stores(xs ...int) { ys := xs; print(ys) }
func passes(xs ...int) int { return reads(xs...) }
func captures(xs ...int) int { f := func() int { return len(xs) }; return f() }
func fixed(xs []int) int { return len(xs) }
func main() {}`)
	want := map[string]bool{"reads": true, "pushes": true, "lends": true, "returns": false, "stores": false, "passes": false, "captures": false}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name == "main" || fd.Name.Name == "count" {
			continue
		}

		sig := info.Defs[fd.Name].Type().(*types.Signature)
		xs, stays := FramedVariadic(info, sig, fd.Body)
		if fd.Name.Name == "fixed" {
			if xs != nil {
				t.Errorf("fixed: a slice parameter is no variadic one")
			}

			continue
		}

		if xs == nil || stays != want[fd.Name.Name] {
			t.Errorf("%s: framed %v, stays %v; want stays %v", fd.Name.Name, xs != nil, stays, want[fd.Name.Name])
		}
	}
}

// TestDeferredLent finds the local variables a deferred call lends to a
// ref parameter, with ref or without, a variadic ...ref one's included,
// and none it takes by value or lends mut, which is an address already.
func TestDeferredLent(t *testing.T) {
	f, info := checkSource(t, `package main
func one(a ref string, b string, c mut int) {}
func many(xs ...ref string) {}
func main() {
	s, t := "s".Clone(), "t".Clone()
	u := "u".Clone()
	n := 1
	defer one(s, t, mut n)
	defer many(u, ref u)
}`)
	var calls []*ast.CallExpr
	for _, st := range f.Decls[len(f.Decls)-1].(*ast.FuncDecl).Body.List {
		if d, ok := st.(*ast.DeferStmt); ok {
			calls = append(calls, d.Call.(*ast.CallExpr))
		}
	}

	for i, want := range [][]string{{"s", "", ""}, {"u", "u"}} {
		lent := DeferredLent(info, calls[i])
		for j, name := range want {
			got := ""
			if j < len(lent) && lent[j] != nil {
				got = lent[j].Name()
			}

			if got != name {
				t.Errorf("call %d, argument %d: lent from %q, want %q", i, j, got, name)
			}
		}
	}
}

// TestMayLeave finds a ?, a ?? and the branching expressions that may
// leave a statement, outside a closure's body.
func TestMayLeave(t *testing.T) {
	for _, tt := range []struct {
		src  string
		want bool
	}{
		{"f(1, 2)", false},
		{"f(1, g()?)", true},
		{"[]int{1, g() ?? 0}", true},
		{"T{a: if c { 1 } else { 2 }}", true},
		{"f(func() int { return g()? })", false},
		{"a.b[c + d]", false},
	} {
		f, _, err := parser.ParseFile("t.snello", []byte("package p\nfunc h() { _ = "+tt.src+" }"))
		if err != nil {
			t.Fatalf("%s: %v", tt.src, err)
		}

		x := f.Decls[0].(*ast.FuncDecl).Body.List[0].(*ast.AssignStmt).Rhs[0]
		if got := MayLeave(x); got != tt.want {
			t.Errorf("%s: MayLeave %v, want %v", tt.src, got, tt.want)
		}
	}
}

// TestMayEnd finds what may end a statement: what may leave it, and a
// call, a spawn, an index, a slice, a division or a remainder, which
// may panic; a variable, a field, a sum and a literal of such cannot.
func TestMayEnd(t *testing.T) {
	for _, tt := range []struct {
		src  string
		want bool
	}{
		{"a", false},
		{"a.b", false},
		{"a + b", false},
		{"T{a: 1, b: c}", false},
		{"(a, b)", false},
		{"f(1)", true},
		{"a.b[c]", true},
		{"a[1:2]", true},
		{"a / b", true},
		{"a % b", true},
		{"-g()", true},
		{"T{a: g()}", true},
		{"g()?", true},
		{"spawn g()", true},
	} {
		f, _, err := parser.ParseFile("t.snello", []byte("package p\nfunc h() { _ = "+tt.src+" }"))
		if err != nil {
			t.Fatalf("%s: %v", tt.src, err)
		}

		x := f.Decls[0].(*ast.FuncDecl).Body.List[0].(*ast.AssignStmt).Rhs[0]
		if got := MayEnd(x); got != tt.want {
			t.Errorf("%s: MayEnd %v, want %v", tt.src, got, tt.want)
		}
	}
}

// TestLeftOut finds the fields with defaults that a struct literal
// leaves out, in the struct's order, a positional literal's included,
// and none of a struct that declares no default.
func TestLeftOut(t *testing.T) {
	f, info := checkSource(t, `package main
struct Size { w int; h int = 1; d int = 2 }
struct Plain { a int; b int }
func main() {
	print(Size{}, Size{d: 5}, Size{3}, Size{3, 4, 5}, Size{w: 1, d: 2, h: 3}, Plain{})
}`)
	main := f.Decls[len(f.Decls)-1].(*ast.FuncDecl)
	args := main.Body.List[0].(*ast.ExprStmt).X.(*ast.CallExpr).Args
	for i, want := range []string{"h d", "h", "h d", "", "", ""} {
		lit := args[i].(*ast.CompositeLit)
		var got []string
		st := info.Types[lit].Underlying().(*types.Struct)
		for _, i := range LeftOut(st, lit) {
			got = append(got, st.Field(i).Name())
		}

		if strings.Join(got, " ") != want {
			t.Errorf("%s: left out %q, want %q", ast.Source(lit), got, want)
		}
	}
}

// TestTaken makes a conversion take an operand that is not Copy, as an
// argument by value is taken, and only read a Copy one.
func TestTaken(t *testing.T) {
	for _, tt := range []struct {
		t    types.Type
		want bool
	}{
		{types.Typ[types.String], true},
		{types.NewSlice(types.Typ[types.Int]), true},
		{types.Typ[types.Int], false},
		{types.Typ[types.Float64], false},
	} {
		if got := Taken(tt.t); (got != nil) != tt.want || got != nil && got[0] != tt.t {
			t.Errorf("Taken(%s) = %v, want taken %v", tt.t, got, tt.want)
		}
	}
}

// TestSelfFormatted sends a verb to a type's own Format when the type
// lists fmt.Formatter, or is a trait value of it, for every verb but
// %+v, an error's chain, which fmt prints itself; a type that lists
// Stringer or Error alone, or nothing, is printed by the backend.
func TestSelfFormatted(t *testing.T) {
	f, info := checkSource(t, `package main
import "fmt"
struct Money impl fmt.Formatter { cents int }
func (m ref Money) Format(f mut fmt.State, verb rune) { f.Pad("$") }
struct Fail impl Error, fmt.Formatter { code int }
func (e ref Fail) Format(f mut fmt.State, verb rune) { f.WriteString("fail") }
struct Point impl fmt.Stringer { x int }
func (p ref Point) String() string { return "p" }
func main() {
	var tv fmt.Formatter = Money{cents: 2}
	m, e, p, n := Money{cents: 1}, Fail{code: 3}, Point{x: 1}, 7
	_ = m
	_ = e
	_ = p
	_ = n
	_ = tv
}`)
	body := f.Decls[len(f.Decls)-1].(*ast.FuncDecl).Body.List
	plain := types.FormatPiece{Verb: 'v', Width: -1, Prec: -1}
	chain := types.FormatPiece{Verb: 'v', Plus: true, Width: -1, Prec: -1}
	hex := types.FormatPiece{Verb: 'x', Width: 8, Prec: -1}
	tests := []struct {
		at    int
		piece types.FormatPiece
		want  bool
	}{
		{2, plain, true},  // a Formatter, %v
		{2, hex, true},    // and every other verb, with a width
		{3, plain, true},  // a Formatter that is an error, %v
		{3, chain, false}, // but %+v is the chain
		{4, plain, false}, // a Stringer
		{4, hex, false},
		{5, plain, false}, // an int
		{6, plain, true},  // a trait value of Formatter
		{6, hex, true},
	}
	for _, tt := range tests {
		x := body[tt.at].(*ast.AssignStmt).Rhs[0]
		if got := SelfFormatted(tt.piece, info.Types[x]); got != tt.want {
			t.Errorf("SelfFormatted(%%%c, %s) = %v, want %v", tt.piece.Verb, info.Types[x], got, tt.want)
		}
	}
}
