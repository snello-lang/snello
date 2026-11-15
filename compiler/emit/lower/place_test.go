// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lower

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
)

// TestPlaceOperands checks the operands of an assignment's place, in the
// order they are evaluated, and whether the place lies in a map's entry.
func TestPlaceOperands(t *testing.T) {
	f, info := checkSource(t, `package main
struct W { xs []int; m map[string]int }
func main() {
	i, j, x := 0, 1, 0
	a := [][]int{[]int{1, 2}}
	m := map[string]W{}
	w := W{xs: []int{1}, m: map[string]int{}}
	a[i][j] = 1
	m["k"].xs[i + j] = 2
	w.xs[0] = 3
	w.m["z"] = 4
	x = 5
	print(x)
}`)
	body := f.Decls[len(f.Decls)-1].(*ast.FuncDecl).Body.List
	tests := []struct {
		operands string
		viaMap   bool
	}{
		{"i, j", false},
		{`"k", i + j`, true},
		{"0", false},
		{`"z"`, true},
		{"", false},
	}
	for n, tt := range tests {
		place := body[n+4].(*ast.AssignStmt).Lhs[0]
		var got []string
		PlaceOperands(place, func(op ast.Expr) { got = append(got, ast.Source(op)) })
		if strings.Join(got, ", ") != tt.operands {
			t.Errorf("%s: operands %q, want %q", ast.Source(place), strings.Join(got, ", "), tt.operands)
		}

		if ThroughMap(info, place) != tt.viaMap {
			t.Errorf("%s: ThroughMap %v, want %v", ast.Source(place), !tt.viaMap, tt.viaMap)
		}
	}
}
