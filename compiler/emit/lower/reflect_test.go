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
