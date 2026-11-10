// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lower

import (
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
