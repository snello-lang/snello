// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/parser"
)

// prelude is the declarations the expression tests share.
const prelude = `package p
import ("fmt"; "os")
struct Node { value string; next *Node?; n int }
enum State { Closed; Open(id int); Failed(reason string) }
enum Token { Num(f64); Plus; Minus }
type NodeID int
func keep(s string) {}
func read(s ref string) int { return len(s) }
func grow(xs mut []int) { xs.Push(1) }
`

func TestExprErrors(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		// Literals take their type from the context, and there is no untyped arithmetic.
		{"func f() f64 { v := f64(2); return v*10 + 1 }", nil},
		{"func f() int { x := 1; return x + 2.5 }", []string{"mismatched types int and f64 in +"}},
		{"func f() i64 { return 1 }", nil},
		{"func f() NodeID { return 0 }", nil},
		{"func f(n NodeID) int { return n }", []string{"cannot use n (NodeID) as int in return"}},
		{"func f(b byte) bool { return b == '0' }", nil},
		{"func f() string { return \"a\" + 1 }", []string{"mismatched types string and int in +"}},
		// Calls.
		{"func f() { keep(1) }", []string{"cannot use a value of type int as string in argument"}},
		{"func f() { keep(\"a\", \"b\") }", []string{"wrong number of arguments: have 2, want 1"}},
		{"func f(s string) int { return read(s) }", nil},     // the implicit borrow
		{"func f(s string) int { return read(ref s) }", nil}, // written out
		{"func f(xs []int) { grow(mut xs) }", nil},
		{"func f(xs []int) { grow(xs) }", []string{"cannot use xs ([]int) as mut []int in argument"}},
		{"func f(xs ref []int) { grow(xs) }", []string{"cannot use xs (ref []int) as mut []int in argument"}},
		{"func f(xs mut []int) { read(\"a\"); grow(xs) }", nil}, // a mut passes on as mut
		{"func f() { x := 1; x() }", []string{"cannot call a value of type int"}},
		{"func f() { fmt.Println(1, \"a\", Closed); fmt.Printf(\"%d\", 1) }", nil},
		{"func f() { fmt.Nope() }", []string{"undefined: fmt.Nope"}},
		// Fields, methods and indexing through borrows and pointers.
		{"func f(n ref Node) int { return n.n }", nil},
		{"func f(n *Node) string { return n.value.Clone() }", nil},
		{"func f(n ref Node) int { return n.n }; func g(p *Node) int { return f(p) }", nil},
		{"func f(n mut Node) {}; func g(p *Node) { f(p) }", []string{"cannot use p (*Node) as mut Node in argument"}},
		{"func f(n ref Node) int { return n.missing }", []string{"missing is not a field or method of ref Node"}},
		{"func f(xs ref []Node, i NodeID) int { return xs[i].n }", nil},
		{"func f(xs []int) int { return xs[\"a\"] }", []string{"index must be an integer, not string"}},
		{"func f(s ref string) ref string { return s[1:2] }", nil},
		{"func f(s ref string) int { n := 0; for l := range s.Lines() { if s[0].IsDigit() || l.Len() > 0 { n += 1 } }; return n }", nil},
		{"func f(a int, b int) int { return min(a, b, 3) + max(1, b) }", nil},
		{"func f(a f64) f64 { return min(a, 2) }", nil}, // the literal takes the type of a
		{"func f(a int, b f64) int { return min(a, b) }", []string{"cannot use b (f64) as int in argument"}},
		{"func f(a int) int { return min(a) }", []string{"min takes at least two arguments"}},
		{"func f(s ref string) ref string { return max(s, s) }", []string{"max takes numbers, not string"}},
		{"func f() string { return min(\"a\", \"b\") }", []string{"min takes numbers, not string"}},
		{"func f() f64 { return min(1, 2.5) }", nil},    // literals alone with a point among them are f64
		{"func f(a f64) f64 { return min(2, a) }", nil}, // the literal comes last whatever its place
		{"func f() int { return min(1, 2.5) }", []string{"cannot use a value of type f64 as int in return"}},
		{"func f(xs mut []int) ref []int { return xs[1:] }", nil},
		{"func f() [3]int { return [3]int{1, 2} }", nil},                                                           // the rest is zero
		{"func f() [2]int { return [2]int{1, 2, 3} }", []string{"too many elements for [2]int: 3"}},                // a sub-slice is a view, even of a slice lent mut
		{"func f(xs mut []int) { v := xs[1:]; v.Push(7) }", []string{"cannot borrow v as mut: v is a ref borrow"}}, // so nothing grows through it
		{"func f(m map[string]int) int { return m[\"k\"] }", nil},
		{"func f(m map[string]int) int? { return m.Get(\"k\") }", []string{"cannot use a value of type ref int? as int? in return"}},
		// Options and results.
		{"func f() int? { return None }", nil},
		{"func f() { x := None }", []string{"cannot tell the type of None here"}},
		{"func f() int { x := Some + 1; return x }", []string{"Some must be called: Some(x)"}},
		{"func f() { x := len; print(x) }", []string{"len must be called"}},
		{"func f() *Node? { return Some(&Node{value: \"a\", next: None, n: 0}) }", nil},
		{"func f() Node { return Node{\"a\", None, 0} }", nil}, // positional: every field in order
		{"func f() Node { return Node{\"a\", 0} }", []string{"a positional struct literal gives every field: Node has 3 fields, not 2"}},
		{"func f() Node { return Node{\"a\", n: 0} }", []string{"a struct literal names its fields or gives them all in order, not both"}},
		{"func f() Node { return Node{1, None, 0} }", []string{"cannot use a value of type int as string in field value"}},
		{"func f(n ref Node) ref string? { return Some(ref n.value) }", nil},
		{"func f(o int?) string? { return o.Map(func(v int) string { return \"x\" }) }", nil}, // U from the function's result
		{"func f(r Result<int, string>) Result<string, string> { return r.Map(func(v int) string { return \"x\" }) }", nil},
		{"func f(r Result<int, string>) Result<int, bool> { return r.MapErr(func(e string) bool { return e.Len() > 0 }) }", nil},
		{"func f(o int?, g func(int) string) string? { return o.Map(g) }", nil}, // a function value too
		{"func f(o int?) string? { return o.Map(func(v string) string { return v }) }", []string{"cannot use a value of type func(string) string as func(int) string in argument"}},
		{"func f(o string?) { p := o.Map(func(v string) int { return v.Len() }); print(p); print(o) }", []string{"use of moved value o"}}, // Map takes its receiver
		{"func f(o int?) { o.MapErr(func(e int) int { return e }) }", []string{"MapErr is not a field or method of int?"}},
		{"func f() Result<int, string> { return Ok(1) }", nil},
		{"func f() Result<int, string> { return Err(2) }", []string{"cannot use a value of type int as string in argument"}},
		{"func f() { x := Ok(1) }", []string{"cannot tell the type of Ok(...) here"}},
		{"func f(x int?) int { return x ?? 0 }", nil},
		{"func f(x int?) int { return x ?? return 7 }", nil},
		{"func f(x int?) int { return x ?? \"a\" }", []string{"cannot use a value of type string as int in else value"}},
		{"func f(x int) int { return x ?? 0 }", []string{"?? needs an option or a Result on its left, not int"}},
		{"func f(x mut (*Node?)) mut *Node { return x ?? return x ?? panic(\"empty\") }", nil},
		{"func f(x Result<int, string>) Result<int, string> { return Ok(x?) }", nil},
		{"func f(x Result<int, string>) Result<int, bool> { return Ok(x?) }", []string{"? cannot pass on an error of type string from a function returning Result<int, bool>"}},
		// ? passes an error on where an assignment would, and into the one variant that holds its type.
		{"struct A impl Error { n int }\nenum E impl Error { Wrap(a A); Other }\nfunc f(x Result<int, A>) Result<int, E> { return Ok(x?) }", nil},
		{"struct A impl Error { n int }\nenum E impl Error { Wrap(a A); Again(a A) }\nfunc f(x Result<int, A>) Result<int, E> { return Ok(x?) }", []string{"? cannot pass on an error of type A from a function returning Result<int, E>: E holds an A in Wrap and in Again; convert it"}},
		{"struct A impl Error { n int }\nenum E impl Error { Other; Two(a A, n int) }\nfunc f(x Result<int, A>) Result<int, E> { return Ok(x?) }", []string{"? cannot pass on an error of type A from a function returning Result<int, E>: no variant of E holds an A"}},
		{"struct A impl Error { n int }\nfunc f(x Result<int, A>) Result<int, Error> { return Ok(x?) }", nil},
		{"struct B { n int }\nfunc f(x Result<int, B>) Result<int, Error> { return Ok(x?) }", []string{"? cannot pass on an error of type B from a function returning Result<int, Error>: B does not implement Error"}},
		{"struct A impl Error { n int }\nstruct S impl Error { n int }\nfunc f(x Result<int, A>) Result<int, S> { return Ok(x?) }", []string{"? cannot pass on an error of type A from a function returning Result<int, S>: S is not an enum"}},
		{"struct A impl Error { n int }\nfunc f<E: Error>(x Result<int, A>) Result<int, E> { return Ok(x?) }", []string{"? cannot pass on an error of type A from a function returning Result<int, E>: E is a type parameter"}},
		{"struct A impl Error { n int }\nenum W<T> impl Error { Wrap(t T); Other }\nfunc f(x Result<int, A>) Result<int, W<A>> { return Ok(x?) }", nil}, // a generic instance holds the type in one variant
		{"func f(x int?) Result<int, string> { return Ok(x?) }", []string{"cannot use ? on an option in a function returning a Result"}},
		{"func f(x int?) int { return x? }", []string{"? is only allowed in a function returning an option or a Result"}},
		{"func f(x int?) int { if n := x { return n }; return 0 }", nil},
		{"func f(x int) int { if n := x { return n }; return 0 }", []string{"if x := v binds an option; int is not one"}},
		// Match: bindings, borrows, exhaustiveness with a witness.
		{"func f(s State) int { return match s { Closed => 0, Open(id) => id, Failed(_) => 1 } }", nil},
		{"func f(s State) int { return match s { Closed => 0, Open(id) => id } }", []string{"match is not exhaustive: missing Failed(_)"}},
		{"func f(s State) int { return match s { Closed => 0, other => 1 } }", nil},
		{"func f(s State) int { return match s { Open(id) if id > 0 => id, Closed => 0, Open(_) => 2, Failed(_) => 3 } }", nil},
		{"func f(s State) int { return match s { Open(id) if id > 0 => id, Closed => 0, Failed(_) => 3 } }", []string{"missing Open(_)"}},
		{"func f(s ref State) string { return match s { Failed(r) => \"failed: \" + r, _ => \"\" } }", nil},
		{"func f(a int?, b int?) int { return match (a, b) { (None, _) => 0, (_, None) => 1, (Some(x), Some(y)) => x + y } }", nil},
		{"func f(a int?, b int?) int { return match (a, b) { (None, _) => 0, (Some(x), Some(y)) => x + y } }", []string{"missing (Some(_), None)"}},
		{"func f(t Token) bool { return match t { Plus | Minus => true, _ => false } }", nil},
		{"func f(t Token) bool { return match t { Plus | Minus => true, Num(_) => false } }", nil},
		{"func f(b bool) int { return match b { true => 1, false => 0 } }", nil},
		{"func f(b bool) int { return match b { true => 1 } }", []string{"missing false"}},
		{"func f(c byte) int { return match c { '0'..='9' => 1, 'a' => 2 } }", []string{"missing _"}},
		{"func f(s State) int { return match s { Closed => 0, Open(id) => \"a\", Failed(_) => 1 } }", []string{"match branches have different types: int and string"}},
		{"func f(s State) int { return match s { Open(id, x) => 0, _ => 1 } }", []string{"Open has 1 fields, not 2"}},
		{"func f(s State) int { return match s { Some(x) => 0, _ => 1 } }", []string{"Some is not a variant of State"}},
		{"func f(s State) int { return match s { Open => 0, _ => 1 } }", []string{"Open has fields; write Open(...)"}},
		// if as an expression, blocks, and leaving.
		{"func f(c bool) int { return if c { 1 } else { 2 } }", nil},
		{"func f(c bool) int { return if c { 1 } else { \"a\" } }", []string{"if branches have different types: int and string"}},
		{"func f(c bool) int { return if c { 1 } else { return 2 } }", nil},
		{"func f(c bool) int { if c { return 1 } }", []string{"missing return at the end of the function"}},
		{"func f(c bool) int { if c { return 1 } else { return 2 } }", nil},
		{"func f() int { for { } }", nil},
		{"func f() int { for { break } }", []string{"missing return at the end of the function"}},
		{"func f() int { for { match Some(1) { Some(_) => break, None => {} } } }", []string{"missing return at the end of the function"}}, // the break leaves the loop, so the end is reached
		{"func f(s State) int { match s { Closed => return 0, _ => return 1 } }", nil},
		{"func f() int { panic(\"no\") }", nil},
		{"func f() int { os.Exit(1) }", nil},
		// Declarations and assignment.
		{"func f() { var x int = \"a\" }", []string{"cannot use a value of type string as int in declaration"}},
		{"func f() { x := 1; x = \"a\" }", []string{"cannot use a value of type string as int in assignment"}},
		{"func f() { x := 1; x += 2.5 }", []string{"cannot use a value of type f64 as int in assignment"}},
		{"func f() { s := \"a\"; s += \"b\" }", nil},
		{"func f() { x := 1; v := mut x; v += 2 }", []string{"+= is not defined on mut int"}},
		{"func f() { x := 1; v := mut x; *v += 2 }", nil},
		{"func f() (int, string) { return (1, \"a\") }", nil},
		{"func f() (int, string) { return 1, \"a\" }", nil},
		{"func f() (int, string) { return 1 }", []string{"cannot use a value of type int as (int, string) in return"}},
		{"func g() (int, string) { return (1, \"a\") }\nfunc f() { a, b := g(); print(a, b) }", nil},
		{"func g() (int, string) { return (1, \"a\") }\nfunc f() { a, b, c := g() }", []string{"assignment mismatch: 3 names but a value of type (int, string)"}},
		{"func f(n Node) { v := n.value; print(v) }", nil},
		{"func f(n mut Node) { n.n = 1; n.value = \"x\" }", nil},
		{"func f(p mut (*Node?)) { *p = None }", nil},
		{"func f(p mut *Node?) { *p = None }", []string{"cannot dereference a value of type mut *Node?"}}, // an optional borrow: ? binds outside mut
		{"func f(x int) { *x = 1 }", []string{"cannot dereference a value of type int"}},
		{"func f(n Node) { x := take(mut n.next); print(x) }", nil},
		{"func f(n Node) { x := take(n.next) }", []string{"take moves a value out of a place written mut"}},
		// take leaves the zero value behind, so the place's type must have one: a null handle, pointer or function is none.
		{"struct H { ch chan int }\nfunc f(h mut H) { c := take(mut h.ch); print(c) }", []string{"cannot take h.ch: chan int has no zero value to leave behind"}},
		{"struct B { p *int }\nfunc f(b mut B) { p := take(mut b.p); print(p) }", []string{"cannot take b.p: *int has no zero value to leave behind"}},
		{"struct H { ch chan int }\nstruct W { h H }\nfunc f(w mut W) { h := take(mut w.h); print(h) }", []string{"cannot take w.h: H has no zero value to leave behind"}},
		{"enum E { A(ch chan int); B }\nstruct W { e E }\nfunc f(w mut W) { e := take(mut w.e); print(e) }", []string{"cannot take w.e: E has no zero value to leave behind"}},
		{"enum E { B; A(ch chan int) }\nstruct W { e E }\nfunc f(w mut W) { e := take(mut w.e); print(e) }", nil}, // the zero is the first variant, which holds nothing
		{"struct W { g func(); t (int, chan int) }\nfunc f(w mut W) { g := take(mut w.g); t := take(mut w.t); g(); print(t) }", []string{"cannot take w.g: func() has no zero value to leave behind", "cannot take w.t: (int, chan int) has no zero value to leave behind"}},
		{"struct W { xs []chan int; o (chan int)?; r Result<chan int, string> }\nfunc f(w mut W) { xs := take(mut w.xs); o := take(mut w.o); r := take(mut w.r); print(xs, o, r) }", nil}, // empty, None and an Err of "" are zero values
		{"func f() { xs := make([]int, 3); m := make(map[string]int); print(len(xs), len(m), cap(xs)) }", nil},
		{"func f() { x := make(int) }", []string{"make builds a slice, map or channel, not int"}},
		{"func f(x int) { print(len(x)) }", []string{"len is not defined on int"}},
		{"func f() { x := Node{value: \"a\", missing: 1} }", []string{"unknown field missing"}},
		{"func f() { x := Node{\"a\"} }", []string{"a positional struct literal gives every field: Node has 3 fields, not 1"}},
		{"func f() { xs := []Node{{value: \"a\", next: None, n: 0}}; print(xs) }", nil},
		{"func f() { x := int(2.5); y := f64(3); z := NodeID(1); w := []byte(\"a\"); print(x, y, z, w) }", nil},
		{"func f() { x := int(\"a\") }", []string{"cannot convert string to int"}},
		{"func f(xs ref []Node) { for i, n := range xs { print(i, n.n) } }", nil},
		{"func f(m map[string]int) { for k, v := range m { keep(k); print(v) } }", nil},
		{"func f(s string) { for w := range s.Fields() { keep(w.ToLower()) } }", nil},
		{"func f(x int) { for i := range x {} }", []string{"cannot range over a value of type int"}},
		{"func f(ch chan int) { ch <- 1; x := <-ch; print(x) }", nil},
		{"func f(ch chan int) { ch <- \"a\" }", []string{"cannot use a value of type string as int in send"}},
		{"func f() { g := func(a ref Node) int { return a.n }; print(g) }", nil},
		{"func f(xs []int) { xs.SortBy(func(a ref int, b ref int) bool { return a < b }) }", nil},
		{"func f(xs []int) { xs.SortBy(func(a int) bool { return true }) }", []string{"cannot use a value of type func(int) bool as func(ref int, ref int) bool in argument"}},
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+tt.src)
		if len(got) != len(tt.want) {
			t.Errorf("%s:\n  got %d errors %q\n  want %d %q", tt.src, len(got), got, len(tt.want), tt.want)
			continue
		}

		for i := range got {
			if !strings.Contains(got[i], tt.want[i]) {
				t.Errorf("%s:\n  got  %q\n  want %q", tt.src, got[i], tt.want[i])
			}
		}
	}
}

// TestExprTypes checks the type recorded for the expression on the
// right of the last := in each program: the types borrows and context
// give to things.
func TestExprTypes(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		{"func f(xs []Node) { x := ref xs[0] }", "ref Node"},
		{"func f(xs mut []Node) { x := mut xs[0] }", "mut Node"},
		{"func f(n ref Node) { x := n.value.Clone() }", "string"},
		{"func f(n ref Node) { x := ref n.value }", "ref string"},
		{"func f(cur mut (*Node?)) { x := cur ?? return }", "mut *Node"},
		{"func f(cur ref (*Node?)) { x := cur ?? return }", "ref *Node"},
		{"func f(n *Node) { x := n.next ?? return }", "*Node"},
		{"func f(n ref Node) { x := n.next ?? return }", "ref *Node"}, // through a borrowed place
		{"func f(n mut Node) { x := n.next ?? return }", "mut *Node"},
		{"func f() { x := 1 }", "int"},
		{"func f() { x := 'a' }", "rune"},
		{"func f() { x := 1.5 }", "f64"},
		{"func f(v f64) { x := v*10 + 1 }", "f64"},
		{"func f(v f64) { x := 10*v }", "f64"},
		{"func f(id NodeID) { x := Some(id) }", "NodeID?"},
		{"func f(n ref Node) { x := Some(ref n.value) }", "ref string?"},
		{"func f(s string) { x := s[1:3] }", "ref string"},
		{"func f(s string) { x := s[0] }", "byte"},
		{"func f(s ref string) { x := s.Trim() }", "ref string"},
		{"func f(s ref string) { x := s.Trim().Clone() }", "string"},
		{"func f(m map[string]int) { x := m.Get(\"k\") }", "ref int?"},
		{"func f(c bool) { x := if c { 1 } else { 2 } }", "int"},
		{"func f(s State) { x := match s { Closed => \"c\", _ => \"o\" } }", "string"},
		{"func f(s ref State) { x := match s { Failed(r) => r, _ => return } }", "ref string"},
		{"func f() { x := &Node{value: \"a\", next: None, n: 0} }", "*Node"},
		{"func f() { x := (1, \"a\") }", "(int, string)"},
		{"func f() { x := panic(\"no\") }", "never"},
	}
	for _, tt := range tests {
		f, file, err := parser.ParseFile("t.snello", []byte(prelude+tt.src))
		if err != nil {
			t.Fatalf("%s: %v", tt.src, err)
		}

		info := &Info{}
		if _, err := Check(file, f, info); err != nil {
			t.Errorf("%s: %v", tt.src, err)
			continue
		}

		got := lastDefineType(f, info)
		if got != tt.want {
			t.Errorf("%s:\n  got  %s\n  want %s", tt.src, got, tt.want)
		}
	}
}

// lastDefineType finds the last x := e in the last function and
// returns the recorded type of e.
func lastDefineType(f *ast.File, info *Info) string {
	fn := f.Decls[len(f.Decls)-1].(*ast.FuncDecl)
	var last ast.Expr
	for _, s := range fn.Body.List {
		if a, ok := s.(*ast.AssignStmt); ok && len(a.Rhs) == 1 {
			last = a.Rhs[0]
		}
	}

	if t := info.Types[last]; t != nil {
		return t.String()
	}

	return "(no type recorded)"
}

// The article a message puts before a type's name.
func TestArticle(t *testing.T) {
	for _, tt := range []struct{ name, want string }{
		{"io.Error", "an io.Error"},
		{"A", "an A"},
		{"strconv.Error", "a strconv.Error"},
		{"E", "an E"},
		{"Code", "a Code"},
		{"", "a "},
	} {
		if got := article(tt.name); got != tt.want {
			t.Errorf("article(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// What the checker records for the backends at each x? whose error
// type differs from the function's: the variant it wraps the error in,
// and nothing for a trait value or the same type.
func TestWraps(t *testing.T) {
	const src = `package p
struct A impl Error { n int }
enum E impl Error { Wrap(a A); Other }
func wrap(x Result<int, A>) Result<int, E> { return Ok(x?) }
func box(x Result<int, A>) Result<int, Error> { return Ok(x?) }
func same(x Result<int, E>) Result<int, E> { return Ok(x?) }
`
	f, file, err := parser.ParseFile("test.snello", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	info := &Info{}
	if _, err := libraryConfig().Check("", file, f, info); err != nil {
		t.Fatal(err)
	}

	if n := len(info.Wraps); n != 1 {
		t.Fatalf("%d conversions recorded, want 1: %v", n, info.Wraps)
	}

	for e, v := range info.Wraps {
		if v.Name() != "Wrap" || v.Parent().Obj().Name() != "E" {
			t.Errorf("x? at %v wraps in %s.%s, want E.Wrap", file.Position(e.Pos()), v.Parent().Obj().Name(), v.Name())
		}

		if line := file.Position(e.Pos()).Line; line != 4 {
			t.Errorf("the conversion recorded is on line %d, want line 4, the enum's", line)
		}
	}

}
