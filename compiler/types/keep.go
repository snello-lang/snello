// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"path/filepath"
	"strconv"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// The keeping pass decides which parameters a function keeps: those
// whose argument it lets outlive the call, by storing it in a field, an
// element or a map, putting it in a variant, returning it, sending it,
// spawning it, giving it to another variable, or passing it to a
// parameter that is kept, in its own body or in a closure written
// inside it. Calling it, and passing it to a parameter that is not
// kept, keep nothing. Only a parameter whose argument may be a closure
// counts: one of a function type, or of a type parameter's type, which
// a function type may instantiate. Nothing is written in the source:
// the body says, as Go's escape analysis decides from a body that a
// parameter leaks.
//
// A closure that borrows may be passed only to a parameter that is not
// kept. Where a call goes through a value, a closure or a function or
// method named without being called, the callee is not known; a
// function that keeps a parameter therefore cannot be used as a value,
// and a closure cannot keep its own, so that a call through a value
// keeps nothing. A trait's method keeps what its default body keeps,
// nothing when it has none, and a type's method may keep no more than
// the trait's declaration does, since a call through the trait decides
// by it.
//
// A parameter may be passed on to a function whose body is checked
// later, so the pass ends with the package: what a parameter is passed
// to is followed to a fixed point, and then every call that passed a
// closure that borrows is checked against it. A package another
// imports is finished first, and its functions' parameters are known
// by then.

// A keepEdge is a parameter passed to another function's parameter: the
// first is kept when the second is.
type keepEdge struct {
	from, to *Var
	callee   string
	pos      token.Pos
}

// A keepCall is a closure that borrows, passed to a parameter, decided
// at the package's end.
type keepCall struct {
	param  *Var
	callee string
	what   string // what the closure borrows, for the message
	pos    token.Pos
}

// A keepValue is a function or method used as a value, or a closure:
// neither may keep a parameter.
type keepValue struct {
	sig     *Signature
	name    string // the function's, or "" for a closure
	pos     token.Pos
	closure bool
}

// A keepImpl is a type's method and the trait's declaration of it.
type keepImpl struct {
	have, want *Func
}

// mayHoldClosure reports whether a parameter's argument may be a
// closure: the parameter's type is a function type or a type
// parameter.
func mayHoldClosure(t Type) bool {
	switch t.(type) {
	case *Signature, *TypeParam:
		return true
	}

	return false
}

// ownParam reports whether v is a parameter whose argument may be a
// closure of the function being checked, or of one of the functions
// around the closure being checked: a closure may run while the
// function around it does, so what it keeps, the function keeps.
func (c *Checker) ownParam(v *Var) bool {
	if v == nil || !mayHoldClosure(v.typ) {
		return false
	}

	if c.sig != nil && c.sig.has(v) {
		return true
	}

	for _, sig := range c.enclosing {
		if sig != nil && sig.has(v) {
			return true
		}
	}

	return false
}

// has reports whether v is one of the signature's parameters.
func (s *Signature) has(v *Var) bool {
	for _, p := range s.params {
		if p == v {
			return true
		}
	}

	return false
}

// keepParam records that the function being checked keeps each
// parameter the value op may be, because of how at pos, and reports
// whether there was one.
func (c *Checker) keepParam(op operand, how string, pos token.Pos) bool {
	params := c.keptParams(op)
	for _, v := range params {
		c.markKept(v, how, pos)
	}

	return len(params) > 0
}

// keepParamIn is keepParam for a value given to the place or variable
// named by target, the reason written only when it is needed.
func (c *Checker) keepParamIn(op operand, how string, target ast.Expr, pos token.Pos) {
	for _, v := range c.keptParams(op) {
		c.markKept(v, how+" "+exprText(target), pos)
	}
}

// keptParams returns the parameters the value op may be: the one it
// names, or those the branches of an if or match expression yield, a
// name resolved here since a branch's value carries none.
func (c *Checker) keptParams(op operand) []*Var {
	if op.mode == invalid || op.expr == nil {
		return nil
	}

	var params []*Var
	c.eachValue(op.expr, func(e ast.Expr) {
		if v := c.paramNamed(e); v != nil {
			params = append(params, v)
		}
	})

	return params
}

// paramNamed returns the parameter the expression names, when it is
// an identifier naming one that may hold a closure, else nil.
func (c *Checker) paramNamed(e ast.Expr) *Var {
	id, ok := unparen(e).(*ast.Ident)
	if !ok {
		return nil // f() or a field of it, not the parameter's own value
	}

	if v, ok := c.resolve(id.Name).(*Var); ok && c.ownParam(v) {
		return v
	}

	return nil
}

// eachValue calls f with each expression the value of e may be: e
// itself, or what each branch of an if or match expression yields, to
// any depth.
func (c *Checker) eachValue(e ast.Expr, f func(ast.Expr)) {
	if vs, ok := branches(e); ok {
		for _, v := range vs {
			c.eachValue(v, f)
		}

		return
	}

	f(e)
}

// markKept records that v is kept, with the first reason found, and
// where, as this package's files place it.
func (c *Checker) markKept(v *Var, how string, pos token.Pos) {
	if !v.kept {
		v.kept, v.keptBy = true, &keptBy{how, c.position(pos)}
	}
}

// keptWhere formats where a parameter is kept, line:col in the file
// being checked, with the file's name from another file or package.
func (c *Checker) keptWhere(v *Var) string {
	at := strconv.Itoa(v.keptBy.at.Line) + ":" + strconv.Itoa(v.keptBy.at.Column)
	if v.keptBy.at.Filename != c.file.Name() {
		return filepath.Base(v.keptBy.at.Filename) + ":" + at
	}

	return at
}

// declared returns the parameter a call's i-th argument is passed to as
// the callee declares it, through the instances of a generic function
// or of a generic type's method, and the callee's name; nil when the
// callee is not a function known here, a closure in a variable or a
// field.
func declared(callee Object, i int) (*Var, string) {
	f, ok := callee.(*Func)
	if !ok {
		return nil, ""
	}

	for f.origin != nil {
		f = f.origin
	}

	sig, ok := f.typ.(*Signature)
	if !ok || i >= len(sig.params) {
		return nil, ""
	}

	return sig.params[i], f.name
}

// keepArg checks the argument op passed as the call's i-th argument:
// a parameter of the function being checked passed on is kept when
// the one it is passed to is; a closure that borrows may not be passed
// to a parameter that is kept. Both are decided at the package's end.
// It reports false for a callee not known here, a value, and for a
// predeclared method, whose parameters carry kept themselves, which
// argEffects then decides.
func (c *Checker) keepArg(op operand, i int, pos token.Pos) bool {
	p, callee := declared(c.calling, i)
	if p == nil || p.pos == token.NoPos || !mayHoldClosure(p.typ) {
		return false
	}

	for _, v := range c.keptParams(op) {
		c.keepEdges = append(c.keepEdges, keepEdge{v, p, callee, pos})
	}

	if what := c.borrowsWhat(op, pos); what != "" {
		c.keepCalls = append(c.keepCalls, keepCall{p, callee, what, pos})
	}

	return true
}

// borrowsWhat names what the closure op borrows, "" when op is not a
// closure that borrows: a closure written out, a variable holding one,
// or either yielded by a branch of an if or match expression.
func (c *Checker) borrowsWhat(op operand, pos token.Pos) string {
	if op.mode == invalid || op.expr == nil {
		return ""
	}

	what := ""
	c.eachValue(op.expr, func(e ast.Expr) {
		if what != "" {
			return
		}

		if caps := c.capturesOf(e); caps != nil {
			what = "closure captures " + caps[0].v.name + " by borrow"
			return
		}

		if id, ok := unparen(e).(*ast.Ident); ok {
			if v, ok := c.resolve(id.Name).(*Var); ok && isSignature(v.typ) {
				if b := c.heldBy(v, pos); b != nil {
					what = "closure " + v.name + " captures " + b.v.name + " by borrow"
				}
			}
		}
	})

	return what
}

// keepMessage writes the refusal of a closure that borrows, passed to
// a parameter its callee keeps, and what to do instead.
func (c *Checker) keepMessage(what, callee string, p *Var) string {
	return what + " and cannot be passed to " + callee + ", which keeps " + p.name + ": " + p.keptBy.how + " at " + c.keptWhere(p) + "; pass a closure that borrows nothing, or give it what it needs by value"
}

// keepValueOf records a function or method named without being called:
// it may keep no parameter, since a call through the value it gives
// cannot know.
func (c *Checker) keepValueOf(f *Func, pos token.Pos) {
	for f.origin != nil {
		f = f.origin
	}

	if sig, ok := f.typ.(*Signature); ok {
		c.keepValues = append(c.keepValues, keepValue{sig: sig, name: f.name, pos: pos})
	}
}

// resolveKeeps ends the keeping pass for the package: follows each
// parameter passed on to a fixed point, then refuses each closure that
// borrows passed to a kept parameter, each function used as a value or
// closure that keeps one, and each method that keeps what its trait's
// declaration does not.
func (c *Checker) resolveKeeps() {
	for changed := true; changed; {
		changed = false
		for _, e := range c.keepEdges {
			if e.to.kept && !e.from.kept {
				c.markKept(e.from, "passed to "+e.callee+", which keeps it", e.pos)
				changed = true
			}
		}
	}

	for _, k := range c.keepCalls {
		if k.param.kept {
			c.errorf(k.pos, c.keepMessage(k.what, k.callee, k.param))
		}
	}

	for _, v := range c.keepValues {
		for _, p := range v.sig.params {
			if !p.kept || p.keptBy == nil {
				continue // a predeclared method's, which says so itself
			}

			if v.closure {
				c.errorf(p.pos, "a closure cannot keep its parameter "+p.name+": "+p.keptBy.how+" at "+c.keptWhere(p)+"; a call through a value keeps nothing")
			} else {
				c.errorf(v.pos, "cannot use "+v.name+" as a value: it keeps its parameter "+p.name+", "+p.keptBy.how+" at "+c.keptWhere(p)+", and a call through a value keeps nothing")
			}

			break
		}
	}

	for _, m := range c.keepImpls {
		have, want := m.have, m.want
		for have.origin != nil {
			have = have.origin
		}

		for want.origin != nil {
			want = want.origin
		}

		hsig, ok1 := have.typ.(*Signature)
		wsig, ok2 := want.typ.(*Signature)
		if !ok1 || !ok2 || have == want {
			continue
		}

		for i, p := range hsig.params {
			if i < len(wsig.params) && p.kept && !wsig.params[i].kept {
				c.errorf(have.pos, "method "+have.name+" keeps its parameter "+p.name+", "+p.keptBy.how+" at "+c.keptWhere(p)+", which the trait's "+want.name+" does not: a call through the trait would not know")
				break
			}
		}
	}

	c.keepEdges, c.keepCalls, c.keepValues, c.keepImpls = nil, nil, nil, nil
}
