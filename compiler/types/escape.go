// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strconv"
	"strings"

	"github.com/teak-lang/teak/compiler/ast"
	"github.com/teak-lang/teak/compiler/token"
)

// The escape pass keeps a borrow inside the
// function that can see its owner. Owned data cannot hold one: a slice,
// map, channel or pointer of a type that holds a borrow is refused as a
// type, and a closure that captures a local is a borrow that cannot be
// stored, sent, spawned or returned. A returned borrow is tied to one
// source, the receiver or the only borrowed parameter, so a signature
// with two borrowed parameters and a borrowed result is refused, and
// a return of a borrow of anything else is. See
// _docs/book/06-borrows.md, rules 2, 3 and 6.

// A closure being checked: where it begins, and the locals of the
// enclosing functions it refers to, which it captures by borrow.
type closure struct {
	pos      token.Pos
	captures []capture
}

type capture struct {
	v    *Var
	mode captureMode
}

// A captureMode says what a closure does with a variable it captures;
// the strongest use seen wins.
type captureMode int8

const (
	byCopy captureMode = iota // a Copy value only read: copied into the closure when it is made, so no borrow
	byRef                     // read only: the closure borrows it
	byMut                     // assigned, or lent mut: the closure borrows it mut
	byMove                    // moved: the variable is dead outside, and the closure owns the value
)

// borrow is the borrow a holder of the closure holds for the capture.
func (cap capture) borrow(holder *Var, pos token.Pos) borrow {
	p, _ := through(place{cap.v, ""}, placeOwned)
	return borrow{holder: holder, place: p, mut: cap.mode == byMut, pos: pos}
}

// holdsBorrow reports whether a value of type t carries a borrow: a
// ref or mut, or a struct, enum, option, result, tuple or array with
// such a part. A slice, map, channel or pointer never does, since one
// that would is refused as a type.
func holdsBorrow(t Type) bool {
	switch t := t.(type) {
	case *Ref:
		return true
	case *Option:
		return holdsBorrow(t.elem)
	case *Result:
		return holdsBorrow(t.val) || holdsBorrow(t.err)
	case *Array:
		return holdsBorrow(t.elem)
	case *Tuple:
		for _, e := range t.elems {
			if holdsBorrow(e) {
				return true
			}
		}
	case *Struct:
		return anyHoldsBorrow(t.fields)
	case *Enum:
		for _, v := range t.variants {
			if anyHoldsBorrow(v.fields) {
				return true
			}
		}
	case *Named:
		return t.holdsBorrow()
	}

	return false
}

func anyHoldsBorrow(vs []*Var) bool {
	for _, v := range vs {
		if holdsBorrow(v.typ) {
			return true
		}
	}

	return false
}

// holdsBorrow caches the answer on the named type, as isCopy does, with
// the same provisional answer for a declaration that reaches itself.
func (n *Named) holdsBorrow() bool {
	if n.borrowState == copyUnknown && n.Underlying() != nil {
		n.borrowState = copyNo
		if holdsBorrow(n.underlying) {
			n.borrowState = copyYes
		}
	}

	return n.borrowState == copyYes
}

// storable reports whether elem may be held by owned data of the given
// kind, a slice, map, channel or pointer, reporting why not: a borrow
// in owned data has no owner to be tied to, and a channel carries its
// values to another task, so they must be Send as well. Before the
// declarations are resolved the question waits, since elem may be a
// struct whose fields are not known yet.
func (c *Checker) storable(elem Type, kind string, pos token.Pos) bool {
	if !c.declared {
		c.pending = append(c.pending, pendingStore{elem, kind, pos})
		return true
	}

	if !holdsBorrow(elem) {
		return kind != "a channel" || c.sendable(elem, pos) // what a channel carries is owned by another task
	}

	verb := " cannot hold "
	if kind == "a channel" {
		verb = " cannot carry "
	}

	if n, ok := elem.(*Named); ok {
		c.errorf(pos, kind+verb+n.obj.name+": it holds a borrow")
	} else {
		c.errorf(pos, kind+verb+"a borrow")
	}

	return false
}

// storableParts asks the storable question for every piece of owned
// data written inside t, a slice, pointer, map or channel, as typ asks
// it when a type is written: for a type the checker builds itself, a
// trait's field with the implementing type in place of Self.
func (c *Checker) storableParts(t Type, pos token.Pos) {
	walkTypes(t, func(t Type) {
		switch t := t.(type) {
		case *Slice:
			c.storable(t.elem, "a slice", pos)
		case *Pointer:
			c.storable(t.elem, "a pointer", pos)
		case *Map:
			c.storable(t.key, "a map", pos)
			c.storable(t.value, "a map", pos)
		case *Chan:
			c.storable(t.elem, "a channel", pos)
		}
	})
}

// A pendingStore is a storable question asked while the declarations
// were being resolved.
type pendingStore struct {
	elem Type
	kind string
	pos  token.Pos
}

// resolvePending answers the storable questions asked before the
// declarations were resolved.
func (c *Checker) resolvePending() {
	c.declared = true
	for _, p := range c.pending {
		c.storable(p.elem, p.kind, p.pos)
	}

	c.pending = nil
	for _, k := range c.pendingKeys {
		c.keyable(k.key, k.pos)
	}

	c.pendingKeys = nil
}

// checkSources checks a signature that returns a borrow against rule
// 3: the borrow is tied to the receiver when that is a borrow, and
// otherwise to the only parameter that holds one.
func (c *Checker) checkSources(name string, sig *Signature, pos token.Pos) {
	if !holdsBorrow(sig.results) || sig.recv != nil && isBorrow(sig.recv.typ) {
		return
	}

	n := 0
	for _, p := range sig.params {
		if holdsBorrow(p.typ) {
			n++
		}
	}

	switch n {
	case 0, 1: // with none, every return of a borrow is of a local and reported there
	case 2:
		c.errorf(pos, name+" returns a borrow but has two borrowed parameters")
	default:
		c.errorf(pos, name+" returns a borrow but has "+strconv.Itoa(n)+" borrowed parameters")
	}
}

// capture notes that the closures being checked refer to v, when v is
// a local of a function enclosing them, declared before they begin.
func (c *Checker) capture(v *Var, mode captureMode) {
	if len(c.closures) == 0 || v.pos == token.NoPos || v.field || c.pkg.scope.Lookup(v.name) == v {
		return
	}

	if mode == byRef && IsCopy(v.typ) && !holdsBorrow(v.typ) && !isSignature(v.typ) {
		mode = byCopy // a number read by a closure is copied in: the closure holds no borrow and may be returned; a borrow variable, Copy too, stays a borrow, and so does a function value, whose borrows are the holder's, not the type's
	}

	for i := range c.closures {
		cl := &c.closures[i]
		if v.pos > cl.pos {
			continue // declared inside the closure, or after it
		}

		if j := cl.index(v); j >= 0 {
			cl.captures[j].mode = max(cl.captures[j].mode, mode)
		} else {
			cl.captures = append(cl.captures, capture{v, mode})
		}
	}
}

func (cl *closure) index(v *Var) int {
	for j := range cl.captures {
		if cl.captures[j].v == v {
			return j
		}
	}

	return -1
}

// capturesOf returns what a closure literal borrows, nil for one that
// stands on its own: a Copy value it only reads is copied in and is no
// borrow.
func (c *Checker) capturesOf(e ast.Expr) []capture {
	lit, ok := unparen(e).(*ast.FuncLit)
	if !ok {
		return nil
	}

	var borrowed []capture
	for _, cap := range c.captured[lit] {
		if cap.mode != byCopy {
			borrowed = append(borrowed, cap)
		}
	}

	return borrowed
}

// kept checks a value put into owned data, sent, spawned or returned:
// a closure that captures a local, written out or held by a variable,
// is a borrow and cannot go. how says which, for the message.
func (c *Checker) kept(op operand, how string, pos token.Pos) {
	if op.mode == invalid || op.expr == nil {
		return
	}

	if caps := c.capturesOf(op.expr); caps != nil {
		c.errorf(pos, "closure captures "+caps[0].v.name+" by borrow and cannot be "+how)
		return
	}

	if v, ok := op.obj.(*Var); ok && isSignature(op.typ) {
		if b := c.heldBy(v, pos); b != nil {
			c.errorf(pos, "closure "+v.name+" captures "+b.v.name+" by borrow and cannot be "+how)
		}
	}
}

func isSignature(t Type) bool {
	_, ok := t.(*Signature)
	return ok
}

// heldBy returns a borrow v holds at pos, or nil.
func (c *Checker) heldBy(v *Var, pos token.Pos) *borrow {
	for i := range c.borrows {
		if b := &c.borrows[i]; b.holder == v && b.pos < pos && (b.end == token.NoPos || b.end > pos) {
			return b
		}
	}

	return nil
}

// returned checks the value of a return statement when the function
// returns a borrow: every borrow in it must come from the function's
// one source, the receiver or the borrowed parameter; a borrow of a
// local, or of a parameter taken by value, dies with the function. A
// closure that captures a local is a borrow of that local.
func (c *Checker) returned(e ast.Expr, op operand) {
	if op.mode == invalid {
		return
	}

	if isSignature(op.typ) {
		c.kept(op, "returned", e.Pos())
		return
	}

	if holdsBorrow(c.sig.results) {
		if tmp := c.borrowOfTemporary(e); tmp != nil {
			c.errorf(e.Pos(), "cannot return a borrow of a temporary: "+exprText(tmp)+" is dropped when the statement ends")
			return
		}

		c.source(e, e.Pos())
	}
}

// source finds the places the borrows in e come from and checks each,
// in every branch of a value chosen at run time.
func (c *Checker) source(e ast.Expr, pos token.Pos) {
	if vs, ok := branches(e); ok {
		for _, v := range vs {
			c.source(v, pos)
		}

		return
	}

	switch e := e.(type) {
	case *ast.ParenExpr:
		c.source(e.X, pos)
	case *ast.UnaryExpr:
		if e.Op == token.REF || e.Op == token.MUT {
			c.sourceVar(e.X, pos, true)
		}
	case *ast.TryExpr:
		c.source(e.X, pos)
	case *ast.SliceExpr:
		c.sourceVar(e.X, pos, true)
	case *ast.TupleExpr:
		for _, elt := range e.Elts {
			if !c.carriesBorrow(elt) {
				continue
			}

			c.source(elt, pos)
		}
	case *ast.CompositeLit:
		for _, elt := range e.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				elt = kv.Value
			}

			if !c.carriesBorrow(elt) {
				continue // a count beside a borrow in a literal
			}

			c.source(elt, pos)
		}
	case *ast.CallExpr:
		if _, ok := c.calleeOf(e).(*Variant); ok {
			for _, arg := range e.Args { // Some(ref n.val)
				c.source(arg, pos)
			}
		} else if src := c.callSource(e); src != nil {
			c.source(src, pos)
		}
	case *ast.Ident, *ast.SelectorExpr, *ast.IndexExpr:
		c.sourceVar(e, pos, false)
	}
}

// callSource is the operand a call's result borrows from, by rule 3:
// the receiver of a method, or the argument passed to the function's
// one borrowed parameter (borrowedParam), wherever it stands in the
// list; for a variant, its first argument. Nil when the call lends
// nothing of its caller's.
func (c *Checker) callSource(e *ast.CallExpr) ast.Expr {
	obj := c.calleeOf(e)
	if fun, ok := e.Fun.(*ast.SelectorExpr); ok && obj == nil {
		return fun.X // a method's result borrows its receiver
	}

	switch obj := obj.(type) {
	case *Variant:
		if len(e.Args) > 0 {
			return e.Args[0]
		}
	case *Func, *Var:
		if sig, ok := obj.Type().(*Signature); ok {
			if i := borrowedParam(sig); i >= 0 && i < len(e.Args) {
				return e.Args[i]
			}
		}
	}

	return nil
}

// calleeOf resolves the function, variant or closure variable a call
// names, in its own package or through another's, bufio.NewReader. Nil
// for a method call, whose callee depends on the receiver's type, and
// for a name that did not resolve.
func (c *Checker) calleeOf(e *ast.CallExpr) Object {
	if fun, ok := e.Fun.(*ast.Ident); ok {
		_, obj := c.scope.LookupParent(fun.Name)
		return obj
	}

	return c.named(e.Fun)
}

// borrowedParam is the index of the one parameter that holds a borrow,
// or -1. A generic function may take a borrow through a type parameter
// instead, wrap(ref s), so with no borrowed parameter the first one of
// a type parameter's type is the source.
func borrowedParam(sig *Signature) int {
	for i, p := range sig.params {
		if holdsBorrow(p.typ) {
			return i
		}
	}

	for i, p := range sig.params {
		if _, ok := p.typ.(*TypeParam); ok {
			return i
		}
	}

	return -1
}

// sourceVar follows the place e reaches to the variable that owns what
// is returned, through the borrows held by the variables on the way,
// and reports a borrow of anything but the function's source. lent
// says the place itself is borrowed, as in ref s, rather than read for
// the borrow it holds.
func (c *Checker) sourceVar(e ast.Expr, pos token.Pos, lent bool) {
	p, kind := c.placeOf(e)
	if kind == notPlace {
		return
	}

	v, before := p.v, pos
	if c.pkg.scope.Lookup(v.name) == v {
		c.errorf(pos, "cannot return a borrow of "+v.name+": a returned borrow is tied to the receiver or a borrowed parameter")
		return
	}

	if lent && kind != placeBorrowed && !c.isParam(v) {
		c.errorf(pos, "cannot return a borrow of local "+v.name)
		return
	}

	for !c.isParam(v) {
		b := c.heldBy(v, before)
		if b == nil {
			if !holdsBorrow(v.typ) {
				c.errorf(pos, "cannot return a borrow of local "+v.name)
			}

			return // else a borrow with no record to follow: from a call, or built in a literal
		}

		v, before = b.v, b.pos
	}

	switch {
	case c.sig.recv == v && isBorrow(v.typ):
	case c.sig.recv == v || !holdsBorrow(v.typ):
		c.errorf(pos, "cannot return a borrow of local "+v.name) // taken by value, it dies at return
	case c.sig.recv != nil && isBorrow(c.sig.recv.typ):
		c.errorf(pos, "cannot return a borrow of "+v.name+": a method's returned borrow is tied to its receiver")
	}
}

// carriesBorrow reports whether an operand of a literal can bring a
// borrow into it: anything but a place whose type holds none, such as
// an int beside a borrow. A place is read for the borrow it holds, so
// a value is only what the place's type says.
func (c *Checker) carriesBorrow(e ast.Expr) bool {
	p, kind := c.placeOf(e)
	return kind == notPlace || holdsBorrow(placeType(p))
}

// placeType returns the type of the value at p: the variable's own, or
// the last field's on the path, each field reached through the borrows
// and pointers in front of the value holding it.
func placeType(p place) Type {
	t := p.v.typ
	if p.path == "" {
		return t
	}

	for _, name := range strings.Split(p.path, ".") {
		s, ok := stripRef(t).Underlying().(*Struct)
		if !ok {
			return t
		}

		f := fieldNamed(s.fields, name)
		if f == nil {
			return t
		}

		t = f.typ
	}

	return t
}

// isParam reports whether v is the receiver or a parameter of the
// function being checked.
func (c *Checker) isParam(v *Var) bool {
	if c.sig == nil {
		return false
	}

	if c.sig.recv == v {
		return true
	}

	for _, p := range c.sig.params {
		if p == v {
			return true
		}
	}

	return false
}
