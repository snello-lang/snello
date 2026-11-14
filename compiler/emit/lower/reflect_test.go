// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lower

import (
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/types"
)

// reflectSrc is a reflecting function and the types it is given.
const reflectSrc = `package main
struct P { x int; name string = "p" }
enum Sh { Dot; Pair(a int, b string) }
func walk<T>(v ref T) int {
	return match T {
		int => 1,
		[]E => 2,
		struct => { n := 0; for f := range fields(T) { n += walk(ref v.[f]) }; n },
		enum => match v { for c := range variants(T) => { n := 0; for f := range fields(c) { n += walk(ref v.[f]) }; n } },
		_ => 0,
	}
}
func main() { print(walk(ref P{x: 1}), walk(ref Dot)) }`

// TestReflect asks the helpers what a backend asks when it compiles a
// reflecting function for a type: the arm the type takes, with a
// shape's name bound; the parameter a match is on; the copies of a loop
// over a struct's fields, an enum's variants and a variant's fields; and
// the field each selector of a v.[f] names in a copy.
func TestReflect(t *testing.T) {
	f, info := checkSource(t, reflectSrc)
	walk := f.Decls[2].(*ast.FuncDecl)
	match := walk.Body.List[0].(*ast.ReturnStmt).Results[0].(*ast.MatchExpr)
	tp := MatchedParam(info, match)
	if tp == nil {
		t.Fatal("no type parameter for match T")
	}

	sub := map[*types.TypeParam]types.Type{}
	if arm, _ := TypeArm(info, match, types.Typ[types.Int], sub); arm != match.Arms[0] {
		t.Errorf("int took arm %v, want the first", arm)
	}

	arm, bound := TypeArm(info, match, types.NewSlice(types.Typ[types.String]), sub)
	if arm != match.Arms[1] || len(bound) != 1 {
		t.Errorf("[]string took arm %v binding %v, want the second binding E", arm, bound)
	}

	p := info.Defs[f.Decls[0].(*ast.TypeDecl).Name].Type()
	sub[tp] = p
	loop := match.Arms[2].Body.(*ast.BlockStmt).List[1].(*ast.RangeStmt)
	part, copies := PartsOf(info, loop, sub, nil)
	if part == nil || len(copies) != 2 || copies[0].Name != "x" || copies[1].Name != "name" || copies[0].Default != nil || copies[1].Default == nil {
		t.Fatalf("fields of P: %v, want x and name, name with its default", copies)
	}

	NameParts(info, part, copies[1])
	sels := info.Parts[part]
	if len(sels) != 1 || sels[0].Sel.Name != "name" || info.Uses[sels[0].Sel] != copies[1].Field {
		t.Errorf("the selector of v.[f] names %v, want name", sels)
	}

	sh := info.Defs[f.Decls[1].(*ast.TypeDecl).Name].Type()
	sub[tp] = sh
	vm := match.Arms[3].Body.(*ast.MatchExpr)
	variant, variants := ArmParts(info, vm.Arms[0].Pat.(*ast.PartsPat), sub)
	if len(variants) != 2 || variants[0].Name != "Dot" || variants[1].Name != "Pair" {
		t.Fatalf("variants of Sh: %v, want Dot and Pair", variants)
	}

	inner := vm.Arms[0].Body.(*ast.BlockStmt).List[1].(*ast.RangeStmt)
	_, pairFields := PartsOf(info, inner, sub, map[*types.Part]PartCopy{variant: variants[1]})
	if len(pairFields) != 2 || pairFields[1].Name != "b" || pairFields[1].Variant != variants[1].Variant || pairFields[1].Index != 1 {
		t.Errorf("fields of Pair: %v, want a and b of Pair", pairFields)
	}

	if PartUsed(info, &ast.Ident{Name: "x"}) != nil {
		t.Error("a name the checker never saw stands for a part")
	}
}

// TestAttrOf asks what attr(x, A) reads in each copy and instance: the
// attribute on the copy's field or variant, the one on the type a type
// parameter stands for, an instance of a generic type reading its
// declaration's, and none where none is written.
func TestAttrOf(t *testing.T) {
	f, info := checkSource(t, `package main
type Key string impl Attribute
struct S @Key("s") { a int @Key("x"); b int }
struct G<T> @Key("g") { v T }
func keys<T>() int {
	return match T {
		struct => { n := 0; for f := range fields(T) { if attr(f, Key).IsSome() { n += 1 } }; if attr(T, Key).IsSome() { n += 10 }; n },
		_ => 0,
	}
}
func main() { print(keys<S>(), keys<G<int>>()) }`)
	keys := f.Decls[3].(*ast.FuncDecl)
	match := keys.Body.List[0].(*ast.ReturnStmt).Results[0].(*ast.MatchExpr)
	body := match.Arms[0].Body.(*ast.BlockStmt)
	loop := body.List[1].(*ast.RangeStmt)
	ifField := loop.Body.List[0].(*ast.IfStmt)
	fieldCall := ifField.Cond.(*ast.CallExpr).Fun.(*ast.SelectorExpr).X.(*ast.CallExpr)
	typeCall := body.List[2].(*ast.IfStmt).Cond.(*ast.CallExpr).Fun.(*ast.SelectorExpr).X.(*ast.CallExpr)
	tp := MatchedParam(info, match)
	key := info.Defs[f.Decls[0].(*ast.TypeDecl).Name].Type()
	for _, tt := range []struct {
		typ   string
		field []string // the expression of the attribute on each field, "" for none
		ty    bool
	}{
		{"S", []string{`Key("x")`, ""}, true},
		{"G", []string{""}, true},
	} {
		named := info.Defs[declNamed(f, tt.typ)].Type().(*types.Named)
		var arg types.Type = named
		if named.TypeParams() != nil {
			arg = types.Instantiate(named, []types.Type{types.Typ[types.Int]})
		}

		sub := map[*types.TypeParam]types.Type{tp: arg}
		part, copies := PartsOf(info, loop, sub, nil)
		for i, c := range copies {
			a, ok := AttrOf(info, fieldCall, c.Sub, map[*types.Part]PartCopy{part: c})
			got := ""
			if ok {
				got = ast.Source(a.Value)
			}

			if got != tt.field[i] {
				t.Errorf("%s field %d: got attribute %q (found %v), want %q", tt.typ, i, got, ok, tt.field[i])
			}

			if !types.Identical(a.Type, key) && ok {
				t.Errorf("%s field %d: attribute of type %v, want Key", tt.typ, i, a.Type)
			}
		}

		if _, ok := AttrOf(info, typeCall, sub, nil); ok != tt.ty {
			t.Errorf("%s: type attribute found %v, want %v", tt.typ, ok, tt.ty)
		}
	}
}

// declNamed returns the name of the type declaration called name in f.
func declNamed(f *ast.File, name string) *ast.Ident {
	for _, d := range f.Decls {
		if td, ok := d.(*ast.TypeDecl); ok && td.Name.Name == name {
			return td.Name
		}
	}

	return nil
}
