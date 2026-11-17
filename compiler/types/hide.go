// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// A generic body hides no borrow. A body that puts a value of its type
// parameter in a trait value or a closure makes a value whose type no
// longer shows the parameter, so the borrow a type argument would hold
// could leave unseen: that parameter takes no type argument that holds
// a borrow. What a body hides passes along the generic calls it makes,
// in an argument or as the receiver, and a method reached through a
// bound is the instance's, so an instance of a generic type whose
// methods hide its parameter is checked where it is made.

// A hiding says where a body puts a value of a type parameter where its
// type no longer shows it, and how.
type hiding struct {
	at  token.Position
	how string // "a trait value", "a closure", or "the call of f" it passes the parameter to
}

// A hideEdge is a generic call made in a generic body: the callee's
// type parameter p stands for t, which mentions the body's own, so
// that what the callee hides of p the body hides of those.
type hideEdge struct {
	p    *TypeParam
	t    Type
	pos  token.Pos
	what string
}

// A pendingHide is a use whose type argument for p holds a borrow,
// checked against what p's declaration hides once every body is.
type pendingHide struct {
	p    *TypeParam
	arg  Type
	pos  token.Pos
	what string
}

// hide notes that the body being checked puts a value of type t in a
// trait value or a closure, how says which: every type parameter t
// mentions takes no type argument that holds a borrow, since the value
// made would hide it.
func (c *Checker) hide(t Type, pos token.Pos, how string) {
	if !holdsTypeParam(t) {
		return
	}

	walkTypes(t, func(t Type) {
		if p, ok := t.(*TypeParam); ok && p.hidden == nil {
			p.hidden = &hiding{at: c.position(pos), how: how}
		}
	})
}

// recordHideEdge records the generic call e of fn, whose signature in
// the call is inst, when it passes on a type parameter of the body
// making it, in an argument or the receiver: h.Hide() with h of a type
// parameter H passes H on as the Self of Hide's trait.
func (c *Checker) recordHideEdge(e *ast.CallExpr, fn *Func, inst *Signature) {
	if !holdsTypeParam(inst) && (inst.recv == nil || !holdsTypeParam(inst.recv.typ)) {
		return
	}

	decl := genericSig(fn)
	if decl == nil {
		return
	}

	for _, b := range bindings(decl, inst) {
		if holdsTypeParam(b.arg) {
			c.hideEdges = append(c.hideEdges, hideEdge{b.p, b.arg, e.Lparen, exprText(e.Fun)})
		}
	}
}

// instanceHides checks the instance t, whose arguments hold a borrow,
// against what its declaration's methods hide, once every body is
// checked: a method called through a bound, which no call in a generic
// body names, may put a value of the type's parameter in a trait value
// or a closure.
func (c *Checker) instanceHides(t *Named, pos token.Pos) {
	for i, a := range t.targs {
		if i < len(t.orig.tparams) && holdsBorrow(a) {
			c.pendingHides = append(c.pendingHides, pendingHide{t.orig.tparams[i], a, pos, t.orig.obj.name})
		}
	}
}

// resolveHides passes on, through the generic calls each body makes,
// what the callees hide, to a fixed point, and then refuses each type
// argument that holds a borrow for a parameter its declaration hides.
func (c *Checker) resolveHides() {
	for grew := true; grew; {
		grew = false
		for _, e := range c.hideEdges {
			if e.p.hidden == nil {
				continue
			}

			walkTypes(e.t, func(t Type) {
				if q, ok := t.(*TypeParam); ok && q.hidden == nil {
					q.hidden = &hiding{at: c.position(e.pos), how: "the call of " + e.what}
					grew = true
				}
			})
		}
	}

	for _, h := range c.pendingHides {
		if h.p.hidden != nil {
			c.errorf(h.pos, h.what+"'s "+h.p.obj.name+" cannot hold "+h.arg.String()+": it holds a borrow, which "+h.what+" hides in "+h.p.hidden.how+" at "+c.positionText(h.p.hidden.at))
		}
	}

	c.hideEdges, c.pendingHides = nil, nil
}
