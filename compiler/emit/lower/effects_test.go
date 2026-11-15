// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lower

import (
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
)

// TestHasEffects checks which operands have effects whose order is
// visible, and which are constants, on the right sides of main's
// statements in turn.
func TestHasEffects(t *testing.T) {
	f, info := checkSource(t, `package main
const k = 3
func tick(c mut int) int { *c += 1; return *c }
func peek(p mut int) int { return *p }
struct P { a int; b int }
func main() {
	c, m, xs, ch := 0, map[int]int{}, []int{1}, make(chan int, 1)
	v1 := tick(mut c)
	v2 := len(xs) + cap(xs)
	v3 := xs[c] + m[c]
	v4 := <-ch
	v5 := match c { 0 => 1, _ => 2 }
	v6 := if c > 0 { 1 } else { 2 }
	v7 := peek(mut m[1])
	v8 := k
	v9 := 7
	v10 := P{a: c, b: tick(mut c)}
	v11 := (c, xs[0:1])
	ps := map[int]P{}
	v12 := peek(mut ps[1].a)
	v13 := len(ch)
	print(v1, v2, v3, v4, v5, v6, v7, v8, v9, v10.a, v11, v12, v13)
}`)
	body := f.Decls[len(f.Decls)-1].(*ast.FuncDecl).Body.List
	tests := []struct {
		what              string
		effects, constant bool
	}{
		{"a call", true, false},
		{"len and cap of a slice, which only read", false, false},
		{"an element and a map read", false, false},
		{"a channel receive", true, false},
		{"a match", true, false},
		{"an if", true, false},
		{"a call lending mut m[1], which makes the entry", true, false},
		{"a constant", false, true},
		{"a literal", false, true},
		{"a struct literal with a call among its values", true, false},
		{"a tuple of a variable and a slice", false, false},
	}
	for i, tt := range tests {
		x := body[i+1].(*ast.AssignStmt).Rhs[0]
		if got := HasEffects(info, x); got != tt.effects {
			t.Errorf("%s: HasEffects %v, want %v", tt.what, got, tt.effects)
		}

		if got := IsConstant(info, x); got != tt.constant {
			t.Errorf("%s: IsConstant %v, want %v", tt.what, got, tt.constant)
		}
	}

	call := body[7].(*ast.AssignStmt).Rhs[0].(*ast.CallExpr)
	if lent := call.Args[0]; !HasEffects(info, lent) {
		t.Errorf("mut m[1]: no effects, want the entry it makes")
	}

	if n := body[14].(*ast.AssignStmt).Rhs[0]; !HasEffects(info, n) {
		t.Errorf("len(ch): no effects, want a channel's length read in its order")
	}

	field := body[13].(*ast.AssignStmt).Rhs[0].(*ast.CallExpr)
	if lent := field.Args[0]; !HasEffects(info, lent) {
		t.Errorf("mut ps[1].a: no effects, want the entry a borrow of its field makes")
	}
}

// TestAnyElement checks that a composite literal's keys and values are
// each looked at, and a struct's field names are not.
func TestAnyElement(t *testing.T) {
	f, _ := checkSource(t, `package main
struct P { a int }
func main() {
	m := map[string]int{"k": 1}
	p := P{a: 2}
	print(len(m), p.a)
}`)
	body := f.Decls[len(f.Decls)-1].(*ast.FuncDecl).Body.List
	var seen []string
	name := func(x ast.Expr) bool {
		seen = append(seen, ast.Source(x))
		return false
	}

	AnyElement(body[0].(*ast.AssignStmt).Rhs[0].(*ast.CompositeLit), name)
	AnyElement(body[1].(*ast.AssignStmt).Rhs[0].(*ast.CompositeLit), name)
	if got := len(seen); got != 4 || seen[0] != `"k"` || seen[1] != "1" || seen[3] != "2" {
		t.Errorf("AnyElement looked at %q, want the key and value of the map, then the field's name and value", seen)
	}

	stop := 0
	if !AnyElement(body[0].(*ast.AssignStmt).Rhs[0].(*ast.CompositeLit), func(ast.Expr) bool { stop++; return true }) || stop != 1 {
		t.Errorf("AnyElement went on after f held, %d calls", stop)
	}
}

// TestMayChange checks which values may change what *r reads, through
// a mut borrow a value passed on holds, a struct, an option or a
// receiver, and which change nothing it reads: each function reads *r
// and then makes its value.
func TestMayChange(t *testing.T) {
	f, info := checkSource(t, `package main
struct Lent { p mut int }
struct Plain { n int }
func (l Lent) Bump() int { *l.p = 2; return 9 }
func (q ref Plain) Get() int { return q.n }
func bumpLent(l Lent) int { *l.p = 2; return 9 }
func bumpThrough(l mut Lent) int { *l.p = 2; return 9 }
func bumpSome(o (mut int)?) int { return 9 }
func twice(n int) int { return n * 2 }
func literal(r mut int) { k := *r; v := bumpLent(Lent{p: r}); print(k, v) }
func variable(r mut int, l Lent) { k := *r; v := bumpLent(l); print(k, v) }
func receiver(r mut int, l Lent) { k := *r; v := l.Bump(); print(k, v) }
func option(r mut int) { k := *r; v := bumpSome(Some(r)); print(k, v) }
func borrowOfHolder(r mut int, l mut Lent) { k := *r; v := bumpThrough(l); print(k, v) }
func copied(r mut int) { k := *r; v := twice(*r); print(k, v) }
func plain(r mut int, q Plain) { k := *r; v := q.Get(); print(k, v) }
func main() {}`)
	tests := []struct {
		fn   string
		want bool
	}{
		{"literal", true},        // a struct made in the call, holding r
		{"variable", true},       // a struct variable holding a mut borrow
		{"receiver", true},       // a by-value receiver holding one
		{"option", true},         // an option of r
		{"borrowOfHolder", true}, // a mut borrow of a struct holding one
		{"copied", false},        // a copy of what r points to
		{"plain", false},         // a ref method on a value holding no borrow
	}
	for _, tt := range tests {
		var body []ast.Stmt
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == tt.fn {
				body = fn.Body.List
			}
		}

		read, value := body[0].(*ast.AssignStmt).Rhs[0], body[1].(*ast.AssignStmt).Rhs[0]
		if got := MayChange(info, value, read); got != tt.want {
			t.Errorf("%s: MayChange %v, want %v", tt.fn, got, tt.want)
		}
	}
}
