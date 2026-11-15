// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lower

import (
	"math"
	"math/big"
	"strconv"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// Folded returns x written as the literal of the value the checker
// folded for it, a constant's value or a field of a struct constant, or
// x itself when the checker folded none or x is such a literal already: a number, negated when it is
// below zero or a negative zero, true or false, or a string. Each node
// made has x's type, so a backend writes it as it writes a literal the
// program gave, and both backends write the same value, computed once.
func Folded(info *types.Info, x ast.Expr) ast.Expr {
	v, ok := info.Values[x]
	if !ok || written(x) {
		return x // nothing folded, or a literal the checker read as it is: the value the backends write from it
	}

	t := info.Types[x]
	pos := x.Pos()
	var out ast.Expr
	negative := false
	switch v.Kind() {
	case types.ConstInt:
		n := v.Int()
		negative = n.Sign() < 0
		out = &ast.BasicLit{ValuePos: pos, Kind: token.INT, Value: new(big.Int).Abs(n).String()}
	case types.ConstFloat:
		f := v.Float()
		negative = math.Signbit(f)
		out = &ast.BasicLit{ValuePos: pos, Kind: token.FLOAT, Value: strconv.FormatFloat(math.Abs(f), 'g', -1, 64)}
	case types.ConstBool:
		id := &ast.Ident{NamePos: pos, Name: strconv.FormatBool(v.Bool())}
		info.Uses[id] = types.Universe.Lookup(id.Name) // the predeclared constant, which every backend writes as itself
		out = id
	case types.ConstString:
		out = &ast.BasicLit{ValuePos: pos, Kind: token.STRING, Value: strconv.Quote(v.Str())}
	default:
		return x
	}

	info.Types[out] = t
	if negative {
		out = &ast.UnaryExpr{OpPos: pos, Op: token.SUB, X: out}
		info.Types[out] = t
	}

	return out
}

// written reports whether x is a literal as the backends write one: a
// number, character or string, a number after a minus sign, or true or
// false, whose value is the value the checker folded from it.
func written(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.BasicLit:
		return true
	case *ast.UnaryExpr:
		_, ok := x.X.(*ast.BasicLit)
		return ok && x.Op == token.SUB && x.X.(*ast.BasicLit).Kind != token.STRING
	case *ast.Ident:
		return x.Name == "true" || x.Name == "false"
	}

	return false
}
