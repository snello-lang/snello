// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// refuseCall checks refuse(msg), which refuses the program where it is
// reached when the program is compiled: in a generic function, for each
// type whose instance reaches it, through the arms its types take and
// the matches on attr and on a part's name the compiler decides; in any
// other function, where it is written. Its message is text the compiler
// knows, string constants and parts' names, c.name, joined with +. It
// is no value, as panic is, and never runs.
func (c *Checker) refuseCall(e *ast.CallExpr) operand {
	never := operand{value, Typ[Never], nil, nil}
	if len(e.Args) != 1 {
		c.errorf(e.Lparen, "refuse takes one message, the text the program is refused with: refuse(\"json.Tag needs named fields\")")
		c.exprs(e.Args)
		return never
	}

	x := c.expr(e.Args[0], Typ[String])
	if x.mode == invalid {
		return never
	}

	if !isString(x.typ) {
		c.assign(x, Typ[String], e.Args[0].Pos(), "argument") // says what the message's type is
		return never
	}

	if bad := c.unknownMessage(e.Args[0]); bad != nil {
		c.errorf(bad.Pos(), "refuse's message is text the compiler knows: string constants and parts' names, c.name, joined with +; "+exprText(bad)+" is not")
		return never
	}

	if !c.inGeneric() { // reached by every program that holds it
		c.errorf(e.Pos(), c.constMessage(e.Args[0]))
		return never
	}

	c.refuses = true // the walk reports it, so it runs whether or not the program matches on a type
	return never
}

// inGeneric reports whether a type parameter is in scope where the
// checker is: in a generic function, a method of a generic type or a
// trait's default, or a closure inside one.
func (c *Checker) inGeneric() bool {
	for s := c.scope; s != nil && s != c.pkg.scope; s = s.parent {
		for _, obj := range s.elems {
			if tn, ok := obj.(*TypeName); ok {
				if _, ok := tn.typ.(*TypeParam); ok {
					return true
				}
			}
		}
	}

	return false
}

// constMessage returns the text of a refuse's message made of constants
// alone, as one outside a generic function is.
func (c *Checker) constMessage(x ast.Expr) string {
	switch x := x.(type) {
	case *ast.ParenExpr:
		return c.constMessage(x.X)
	case *ast.BinaryExpr:
		return c.constMessage(x.X) + c.constMessage(x.Y)
	case *ast.BasicLit:
		v, _ := literalValue(x)
		return v.Str()
	case *ast.Ident:
		if k, ok := c.resolve(x.Name).(*Const); ok {
			return k.val.Str()
		}
	case *ast.SelectorExpr:
		if k, ok := c.packageObject(x).(*Const); ok {
			return k.val.Str()
		}
	}

	return ""
}

// unknownMessage returns the part of a refuse's message the compiler
// cannot know, or nil when it knows it all.
func (c *Checker) unknownMessage(x ast.Expr) ast.Expr {
	switch x := x.(type) {
	case *ast.ParenExpr:
		return c.unknownMessage(x.X)
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			if bad := c.unknownMessage(x.X); bad != nil {
				return bad
			}

			return c.unknownMessage(x.Y)
		}
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			return nil
		}
	case *ast.Ident:
		if _, ok := c.resolve(x.Name).(*Const); ok {
			return nil
		}
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok && x.Sel.Name == "name" {
			if _, obj := c.scope.LookupParent(id.Name); obj != nil {
				if _, isPart := obj.(*Part); isPart {
					return nil
				}
			}
		}

		if _, ok := c.packageObject(x).(*Const); ok {
			return nil
		}
	}

	return x
}

// message writes the text of a refuse the walk reached: its constants,
// and each part's name as the copy being walked gives it.
func (w *reachWalk) message(x ast.Expr) string {
	switch x := x.(type) {
	case *ast.ParenExpr:
		return w.message(x.X)
	case *ast.BinaryExpr:
		return w.message(x.X) + w.message(x.Y)
	case *ast.BasicLit:
		v, _ := literalValue(x)
		return v.Str()
	case *ast.Ident:
		if k, ok := w.info.Uses[x].(*Const); ok {
			return k.val.Str()
		}
	case *ast.SelectorExpr:
		if name, ok := w.partName(x); ok {
			return name
		}

		if k, ok := w.info.Uses[x.Sel].(*Const); ok {
			return k.val.Str()
		}
	}

	return ""
}
