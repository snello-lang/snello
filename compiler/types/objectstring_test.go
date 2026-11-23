// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"cmp"
	"maps"
	"slices"
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/parser"
)

const declsSrc = `package p

import ("fmt"; "strings")

const Limit int = 10
var count int

trait Shape {
	func (s ref Self) Area() f64
	func (s ref Self) Twice() f64 { return 2 * s.Area() }
}

trait Named {
	var name string
}

trait Solid: Shape {}

struct Rect impl Shape, Named {
	w f64
	h f64
}

func (r ref Rect) Area() f64 { return r.w * r.h }

func Rect.Square(side f64) Rect { return Rect{name: "square", w: side, h: side} }

struct Pair<T: Clone> {
	a T
	b T
}

func Pair<T>.Of(a T, b T) Pair<T> { return Pair{a: a, b: b} }

enum Kind impl Copy {
	Round
	Corner(f64, f64)
}

type Celsius f64

func Largest<A: Shape, B: Shape + Named>(a ref A, b ref B) f64 { return a.Area() }

func Join(sep string, parts ...string) string { return sep }

func Reset() {}

func Upper(s string) string { return strings.ToUpper(s) }

func Ints() Pair<int> { return Pair<int>.Of(1, 2) }

func Show() { fmt.Println("shown") }

func Names<T>() int {
	n := 0
	match T {
		struct => {
			for fld := range fields(T) { n += len(fld.name) }
		}
		enum => {
			for vr := range variants(T) {
				for vf := range fields(vr) { n += len(vf.name) }
			}
		}
		_ => {}
	}

	return n
}
`

// TestObjectString writes each kind of declaration as a reader sees it:
// a function, a method, a function of a type, generic or not, and an
// instance of one as its declaration, a struct, an enum and a trait
// with their members, a type, a type parameter, a variable, a field, a
// constant, a variant, an imported package and a loop's parts; and the
// library's printing function with its ...ref any, in its type too.
func TestObjectString(t *testing.T) {
	f, file, err := parser.ParseFile("test.snello", []byte(declsSrc))
	if err != nil {
		t.Fatal(err)
	}

	info := &Info{Defs: map[*ast.Ident]Object{}, Uses: map[*ast.Ident]Object{}}
	if _, err := libraryConfig().Check("", file, f, info); err != nil {
		t.Fatal(err)
	}

	// An object is named by its name, and one declared after another of
	// the same name by its name and a prime: Area is the trait's, Area'
	// the struct's.
	objs := map[string]Object{}
	owners := map[string]*TypeName{}
	ids := slices.Collect(maps.Keys(info.Defs))
	slices.SortFunc(ids, func(a, b *ast.Ident) int { return cmp.Compare(a.NamePos, b.NamePos) })
	for _, id := range ids {
		if obj := info.Defs[id]; obj != nil {
			name := id.Name
			for objs[name] != nil {
				name += "'"
			}

			objs[name] = obj
		}
	}

	for id, obj := range info.Uses {
		switch obj.(type) {
		case *PkgName:
			objs[id.Name] = obj
		case *Func:
			switch id.Name {
			case "Of":
				objs["Of@use"] = obj // the instance Pair<int>.Of
			case "Println":
				objs[id.Name] = obj
			}
		}
	}

	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Of != nil {
			owners[fn.Name.Name] = info.Uses[fn.Of.Name].(*TypeName)
			owners[fn.Name.Name+"@use"] = owners[fn.Name.Name]
		}
	}

	tests := map[string]string{
		"Largest": "func Largest<A: Shape, B: Shape + Named>(a ref A, b ref B) f64",
		"Area":    "func (s ref Self) Area() f64",
		"Area'":   "func (r ref Rect) Area() f64",
		"Square":  "func Rect.Square(side f64) Rect",
		"Of":      "func Pair<T>.Of(a T, b T) Pair<T>",
		"Of@use":  "func Pair<T>.Of(a T, b T) Pair<T>",
		"fld":     "field fld of T",
		"vr":      "variant vr of T",
		"vf":      "field vf of variant vr of T",
		"Join":    "func Join(sep string, parts ...string) string",
		"Println": "func Println(args ...ref any)",
		"Reset":   "func Reset()",
		"Rect":    "struct Rect impl Shape, Named {\n\tw f64\n\th f64\n\tname string\n}",
		"Pair":    "struct Pair<T: Clone> {\n\ta T\n\tb T\n}",
		"Kind":    "enum Kind impl Copy {\n\tRound\n\tCorner(f64, f64)\n}",
		"Shape":   "trait Shape {\n\tfunc (s ref Self) Area() f64\n\tfunc (s ref Self) Twice() f64\n}",
		"Named":   "trait Named {\n\tvar name string\n}",
		"Solid":   "trait Solid: Shape {\n}",
		"Celsius": "type Celsius f64",
		"B":       "B: Shape + Named",
		"count":   "var count int",
		"side":    "var side f64",
		"w":       "field w f64",
		"Limit":   "const Limit int = 10",
		"Corner":  "Kind.Corner(f64, f64)",
		"strings": `package strings ("strings")`,
	}
	for name, want := range tests {
		obj := objs[name]
		if obj == nil {
			t.Errorf("no object %s", name)
			continue
		}

		if got := ObjectString(obj, owners[name]); got != want {
			t.Errorf("%s:\n got: %q\nwant: %q", name, got, want)
		}
	}

	if got := objs["Println"].Type().String(); got != "func(...ref any)" {
		t.Errorf("the library's printing function's type: %q, want func(...ref any)", got)
	}
}
