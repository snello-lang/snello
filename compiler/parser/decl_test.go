// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parser

import (
	"strings"
	"testing"

	"github.com/teak-lang/teak/compiler/ast"
	"github.com/teak-lang/teak/compiler/scanner"
)

// TestDecls parses each declaration and prints it back in canonical
// form. A function body prints as {...}; its statements have their own
// tests.
func TestDecls(t *testing.T) {
	tests := []struct{ src, want string }{
		// Functions and methods, from the examples.
		{"func main() {}", "func main() {...}"},
		{"func (t mut Tree) AddChild(parent NodeID, value string) NodeID {}", "func (t mut Tree) AddChild(parent NodeID, value string) NodeID {...}"},
		{"func (s ref Stack<T>) Peek() ref T? {}", "func (s ref Stack<T>) Peek() (ref T)? {...}"},
		{"func (f File) Close() {}", "func (f File) Close() {...}"},
		{"func (p mut Parser) advance() Result<(), ParseError> {}", "func (p mut Parser) advance() Result<(), ParseError> {...}"},
		{"func (a Vec3) Add(b Vec3) Vec3 { return Vec3{x: a.x + b.x} }", "func (a Vec3) Add(b Vec3) Vec3 {...}"},
		{"func MaxBy<T: Ord + Clone>(xs ref []T) T? {}", "func MaxBy<T: Ord + Clone>(xs ref []T) (T)? {...}"},
		{"func New<K: Hash + Eq + Clone, V>(cap int) Cache<K, V> {}", "func New<K: Hash + Eq + Clone, V>(cap int) Cache<K, V> {...}"},
		{"func popMin<K: Ord, V>(n *Node<K, V>) (*Node<K, V>, *Node<K, V>?) {}", "func popMin<K: Ord, V>(n *Node<K, V>) (*Node<K, V>, (*Node<K, V>)?) {...}"},
		{"func f() int", "func f() int"}, // no body: the checker's problem, not the parser's
		{"func Println(args ...ref any)", "func Println(args ...ref any)"},
		{"func Fprintf<W: io.Writer>(w mut W, format ref string, args ...ref any)", "func Fprintf<W: io.Writer>(w mut W, format ref string, args ...ref any)"},

		// Types: definitions, structs with impl lists, enums.
		{"type NodeID int", "type NodeID int"},
		{"type Duration i64 impl fmt.Stringer, Eq", "type Duration i64 impl fmt.Stringer, Eq"}, // the traits after the type it is declared over
		{"struct Circle impl Shape, Named, Logged {\n\tr f64\n}", "struct Circle impl Shape, Named, Logged {r f64}"},
		{"struct Node { id int; next *Node? }", "struct Node {id int; next (*Node)?}"},
		{"struct Rect { w, h f64; Point }", "struct Rect {w, h f64; Point}"},
		{"struct Empty {}", "struct Empty {}"},
		{"struct Cache<K: Hash + Eq + Clone, V> {}", "struct Cache<K: Hash + Eq + Clone, V> {}"},
		{"struct MapIter<I: Iterator<T>, T, U> impl Iterator<U> {}", "struct MapIter<I: Iterator<T>, T, U> impl Iterator<U> {}"},
		{"enum Expr {\n\tLit(f64)\n\tNeg(*Expr)\n\tBin(op Token, lhs *Expr, rhs *Expr)\n}", "enum Expr {Lit(f64); Neg(*Expr); Bin(op Token, lhs *Expr, rhs *Expr)}"},
		{"enum Token impl Copy, Eq { Plus; Minus }", "enum Token impl Copy, Eq {Plus; Minus}"},
		{"enum Value { Null; Array([]Value); Object(map[string]Value) }", "enum Value {Null; Array([]Value); Object(map[string]Value)}"},
		{"enum Msg { Withdraw(amount int, reply chan<- Result<int, string>) }", "enum Msg {Withdraw(amount int, reply chan<- Result<int, string>)}"},

		// Traits: required and default methods, fields, refinement.
		{"trait Shape {\n\tfunc (s ref Self) Area() f64\n\tfunc (s ref Self) Describe() string { return s.Name() }\n}", "trait Shape {func (s ref Self) Area() f64; func (s ref Self) Describe() string {...}}"},
		{"trait Named {\n\tvar name string\n\tfunc (n ref Self) Name() string { return n.name }\n}", "trait Named {var name string; func (n ref Self) Name() string {...}}"},
		{"trait Logged: Shape {}", "trait Logged: Shape {}"},
		{"trait Iterator<T> {\n\tfunc (it mut Self) Next() T?\n\tfunc (it Self) Map<U>(f func(T) U) MapIter<Self, T, U> {}\n}", "trait Iterator<T> {func (it mut Self) Next() (T)?; func (it Self) Map<U>(f func(T) U) MapIter<Self, T, U> {...}}"},

		// Variables and constants.
		{"var x int", "var x int"},
		{"var x, y int = 1, 2", "var x, y int = 1, 2"},
		{"const limit = 1 << 10", "const limit = (1 << 10)"},
		{"var f = func() int { return 1 }", "var f = func() int {...}"},
		{"var m = map[string]int{\"a\": 1}", "var m = map[string]int{\"a\": 1}"},
	}
	for _, tt := range tests {
		d, errs := parseDeclString(tt.src)
		if len(errs) > 0 {
			t.Errorf("%q: unexpected errors: %v", tt.src, errs)
			continue
		}

		if got := declString(d); got != tt.want {
			t.Errorf("%q: parsed as %q, want %q", tt.src, got, tt.want)
		}
	}
}

func TestDeclErrors(t *testing.T) {
	tests := []struct {
		src  string
		want string // substring of the first error
		n    int    // total errors expected
	}{
		{"struct T", "expected '{', found 'newline'", 1},
		{"func f(a ...int, b int) {}", "can only use ... with the final parameter", 1},
		{"struct T\nfunc f() {}", "expected '{', found 'newline'", 1},
		{"type T struct {}", "a struct or enum declaration is spelled 'struct T {' or 'enum T {'", 1}, // Go's spelling
		{"type T enum { A }", "a struct or enum declaration is spelled 'struct T {' or 'enum T {'", 1},
		{"enum E {\n\tLit f64\n}", "expected '(' or newline, found 'f64'", 1},
		{"enum E {\n\t3\n}", "expected variant or '}', found '3'", 1},
		{"struct T<K Ord> {}", "expected '>', found 'Ord'", 1},               // the stray '>' is on the same line, so not reported
		{"struct T { a int\ntype", "expected field or '}', found 'type'", 2}, // then the stray type declaration ends at EOF
		{"struct T { a int b }", "expected ';' or newline, found 'b'", 1},
		{"trait T {\n\tx int\n}", "expected method, field or '}', found 'x'", 1},
		{"trait T {\n\tx int\n\tfunc (s ref Self) F()\n}", "expected method, field or '}', found 'x'", 1},
		{"trait T { 3 func (s ref Self) F() }", "expected method, field or '}', found '3'", 1}, // resumes at func
		{"var x", "expected type or '=', found 'newline'", 1},
		{"func f() {", "expected '}', found 'EOF'", 1},
		{"func (Tree) F() {}", "receiver needs a name and a type", 1},
		{"func () F() {}", "receiver needs a name and a type", 1},
		{"func f(,) {}", "expected type, found ','", 1},
	}
	for _, tt := range tests {
		_, errs := parseDeclString(tt.src)
		if len(errs) == 0 || !strings.Contains(errs[0], tt.want) {
			t.Errorf("%q: got %v, want an error containing %q", tt.src, errs, tt.want)
			continue
		}

		if len(errs) != tt.n {
			t.Errorf("%q: %d errors, want %d: %v", tt.src, len(errs), tt.n, errs)
		}
	}
}

// TestGoSpelling checks that Go's `type T struct` is reported once with
// the Teak spelling and still parsed whole, impl list included.
func TestGoSpelling(t *testing.T) {
	d, errs := parseDeclString("type T struct impl Shape { x int }")
	if len(errs) != 1 {
		t.Fatalf("got %v, want one error", errs)
	}

	if got, want := declString(d), "struct T impl Shape {x int}"; got != want {
		t.Errorf("parsed as %q, want %q", got, want)
	}
}

// parseDeclString parses src as the one declaration of a file and
// returns it with the error messages.
func parseDeclString(src string) (ast.Decl, []string) {
	f, _, err := ParseFile("test.teak", []byte("package p\n"+src+"\n"))

	var errs []string
	if list, ok := err.(scanner.ErrorList); ok {
		for _, e := range list {
			errs = append(errs, e.Error())
		}
	}

	if len(f.Decls) == 0 {
		return nil, errs
	}

	return f.Decls[0], errs
}

// declString renders a declaration in canonical source form, using
// typeString for every type in it.
func declString(d ast.Decl) string {
	switch d := d.(type) {
	case *ast.FuncDecl:
		s := "func "
		if d.Recv != nil {
			s += "(" + d.Recv.Name.Name + " " + typeString(d.Recv.Type) + ") "
		}

		s += d.Name.Name + typeParamList(d.TypeParams) + "(" + paramList(d.Params) + ")"
		switch len(d.Results) {
		case 0:
		case 1:
			s += " " + typeString(d.Results[0])
		default:
			s += " (" + typeList(d.Results, ", ") + ")"
		}

		if d.Body != nil {
			s += " {...}"
		}

		return s
	case *ast.TypeDecl:
		s := d.Name.Name + typeParamList(d.TypeParams)
		impls := ""
		if len(d.Impls) > 0 {
			impls = " impl " + typeList(d.Impls, ", ")
		}

		switch def := d.Def.(type) {
		case *ast.StructType:
			return "struct " + s + impls + " {" + fieldList(def.Fields) + "}"
		case *ast.EnumType:
			var variants []string
			for _, v := range def.Variants {
				vs := v.Name.Name
				if v.Fields != nil {
					vs += "(" + paramList(v.Fields) + ")"
				}

				variants = append(variants, vs)
			}

			return "enum " + s + impls + " {" + strings.Join(variants, "; ") + "}"
		}

		return "type " + s + " " + typeString(d.Def) + impls
	case *ast.TraitDecl:
		s := "trait " + d.Name.Name + typeParamList(d.TypeParams)
		if len(d.Refines) > 0 {
			s += ": " + typeList(d.Refines, ", ")
		}

		var members []string
		for _, f := range d.Fields {
			members = append(members, "var "+fieldString(f))
		}

		for _, m := range d.Methods {
			members = append(members, declString(m))
		}

		return s + " {" + strings.Join(members, "; ") + "}"
	case *ast.VarDecl:
		s := "var "
		if d.Const {
			s = "const "
		}

		var names []string
		for _, n := range d.Names {
			names = append(names, n.Name)
		}

		s += strings.Join(names, ", ")
		if d.Type != nil {
			s += " " + typeString(d.Type)
		}

		if len(d.Values) > 0 {
			s += " = " + exprList(d.Values)
		}

		return s
	}

	return "?"
}

func typeParamList(params []*ast.TypeParam) string {
	if len(params) == 0 {
		return ""
	}

	var parts []string
	for _, tp := range params {
		s := tp.Name.Name
		if len(tp.Bounds) > 0 {
			s += ": " + typeList(tp.Bounds, " + ")
		}

		parts = append(parts, s)
	}

	return "<" + strings.Join(parts, ", ") + ">"
}
