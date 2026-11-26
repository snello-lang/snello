// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"slices"
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// The escape pass keeps a borrow inside the
// function that can see its owner. Owned data cannot hold one: a slice,
// map, channel or pointer of a type that holds a borrow is refused as a
// type, and a closure that captures a local is a borrow that cannot be
// stored, sent, spawned or returned. A returned borrow is tied to one
// source, the receiver or the only borrowed parameter, so a signature
// with two borrowed parameters and a borrowed result is refused, and
// a return of a borrow of anything else is.

// A capture is a variable of the body around a closure that the closure
// refers to, and how: read from the form of the closure's body.
type capture struct {
	v     *Var
	mode  captureMode
	reads bool // the body reads the variable, or borrows or moves it, beside assigning it: a value moved before the closure is gone for it
}

// A pendingStore is a storable question asked while the declarations
// were being resolved.
type pendingStore struct {
	elem Type
	kind string
	pos  token.Pos
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
	return borrow{holder: holder, place: p, mut: cap.mode == byMut, pos: pos, variable: isBorrow(cap.v.typ)}
}

// holdsBorrow reports whether a value of type t carries a borrow: a
// ref or mut, or a struct, enum, option, result, tuple, array or slice
// with such a part. A program cannot write a slice of borrows, which is
// refused as a type, but Split, SplitN, Fields and Lines give one, the
// pieces of the string they lend, and the slice holds that string's
// borrow for as long as it is used. A map, channel or pointer never
// does, since one that would is refused as a type.
func holdsBorrow(t Type) bool {
	switch t := t.(type) {
	case *Ref:
		return true
	case *Slice:
		return holdsBorrow(t.elem)
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

// HoldsMutBorrow reports whether a value of type t carries a mut
// borrow: a mut, or a struct, enum, option, result, tuple, array or
// slice with one in a part. A call given such a value may change what
// the borrow points to.
func HoldsMutBorrow(t Type) bool {
	if !holdsBorrow(t) {
		return false // a type that holds no borrow, the most, is answered from the named type's cache
	}

	switch t := t.(type) {
	case *Ref:
		return t.mut
	case *Slice:
		return HoldsMutBorrow(t.elem)
	case *Option:
		return HoldsMutBorrow(t.elem)
	case *Result:
		return HoldsMutBorrow(t.val) || HoldsMutBorrow(t.err)
	case *Array:
		return HoldsMutBorrow(t.elem)
	case *Tuple:
		return slices.ContainsFunc(t.elems, HoldsMutBorrow)
	case *Struct:
		return anyHoldsMutBorrow(t.fields)
	case *Enum:
		for _, v := range t.variants {
			if anyHoldsMutBorrow(v.fields) {
				return true
			}
		}
	case *Named:
		return HoldsMutBorrow(t.Underlying()) // a type holding a borrow never holds itself: a slice of one is refused, and an option of one has no size
	}

	return false
}

func anyHoldsMutBorrow(vs []*Var) bool {
	for _, v := range vs {
		if HoldsMutBorrow(v.typ) {
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
		name := n.obj.name
		if n.targs != nil {
			name = n.String() // an instance is told by its arguments: iter.Take<deque.Iter<int>, ref int>
		}

		c.errorf(pos, kind+verb+name+": it holds a borrow")
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

// ownedParts refuses an instance that would put a borrow where the
// checker does not follow it: a type argument that holds a borrow may
// make an instance, which then holds the borrow, but not one that keeps
// the argument in a map, a channel or a pointer, written in t or in the
// fields of an instance inside t whose arguments hold a borrow. A
// slice of one is followed, as the slice Split gives is. Each instance
// is looked into once, which also ends the walk of a type that refers
// to itself. Before the declarations are resolved the question waits,
// as storable's does, since an instance's fields are not known yet.
func (c *Checker) ownedParts(t Type, pos token.Pos) {
	if !c.declared {
		c.pendingOwned = append(c.pendingOwned, pendingStore{t, "", pos})
		return
	}

	walkTypes(t, func(t Type) {
		switch t := t.(type) {
		case *Pointer:
			c.ownedElem(t.elem, "a pointer", pos)
		case *Map:
			c.ownedElem(t.key, "a map", pos)
			c.ownedElem(t.value, "a map", pos)
		case *Chan:
			c.ownedElem(t.elem, "a channel", pos)
		case *Named:
			if t.orig != nil && !t.partsSeen && slices.ContainsFunc(t.targs, holdsBorrow) {
				t.partsSeen = true
				c.ownedParts(t.Underlying(), pos)
				c.instanceHides(t, pos)
			}
		}
	})
}

// ownedElem refuses elem in owned data of the given kind when it holds
// a borrow, as storable does; one that holds none was asked about where
// its type was written.
func (c *Checker) ownedElem(elem Type, kind string, pos token.Pos) {
	if holdsBorrow(elem) {
		c.storable(elem, kind, pos)
	}
}

// resolvePending answers the storable questions asked before the
// declarations were resolved.
func (c *Checker) resolvePending() {
	c.declared = true
	for _, p := range c.pending {
		c.storable(p.elem, p.kind, p.pos)
	}

	c.pending = nil
	for _, p := range c.pendingOwned {
		c.ownedParts(p.elem, p.pos)
	}

	c.pendingOwned = nil
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
		if holdsBorrow(p.typ) && !p.lent { // a variadic ...ref parameter is never the source
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

// captureModeOf is how a closure captures v given the strongest use it
// makes of it: a number read by a closure is copied in, so the closure
// holds no borrow and may be returned; a borrow variable, Copy too, stays
// a borrow, and so does a function value, whose borrows are the holder's,
// not the type's.
func captureModeOf(v *Var, mode captureMode) captureMode {
	if mode == byRef && IsCopy(v.typ) && !holdsBorrow(v.typ) && !isSignature(v.typ) {
		return byCopy
	}

	return mode
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

// kept records what a value put into owned data, sent, spawned or
// returned says of the function: a parameter so kept is one the
// function keeps, which its callers are checked against. how says
// which, for the message. Whether a closure that borrows may go there
// the borrow pass decides.
func (c *Checker) kept(op operand, how string, pos token.Pos) {
	if op.mode == invalid || op.expr == nil {
		return
	}

	c.keepParam(op, how, pos) // a parameter of the function, which the function then keeps: its callers are checked against that; whether a closure that borrows goes there the borrow pass decides
}

// cannotBe refuses the value op where it is kept, how saying where,
// when it is a closure that borrows.
func (c *Checker) cannotBe(op operand, how string, pos token.Pos) {
	if what := c.borrowsWhat(op, pos); what != "" {
		c.errorf(pos, what+" and cannot be "+how)
	}
}

func isSignature(t Type) bool {
	_, ok := t.(*Signature)
	return ok
}

// heldBy returns a borrow v was given before pos, itself or through a
// copy of another variable, or nil: whether a closure variable may hold
// a borrow when it is stored.
func (c *Checker) heldBy(v *Var, pos token.Pos) *borrow {
	for f := c.flow; f != nil; f = f.parent { // a closure's body sees the variables of the bodies around it
		if b := heldIn(f, v, pos); b != nil {
			return b
		}
	}

	return nil
}

// heldIn looks for what heldBy looks for in the graph f.
func heldIn(f *flow, v *Var, pos token.Pos) *borrow {
	for i := range f.loans {
		if b := &f.loans[i]; b.holder == v && b.pos < pos {
			return b
		}
	}

	for _, cp := range f.copies {
		if cp.to == v && cp.pos < pos && !cp.through {
			if b := heldIn(f, cp.from, cp.pos); b != nil {
				return b
			}
		}
	}

	return nil
}

// returned records what the value of a return statement says of the
// function: a parameter returned is one the function keeps, which its
// callers are checked against. What else the value lets out, the borrow
// pass decides (returnEscapes).
func (c *Checker) returned(e ast.Expr, op operand) {
	if op.mode == invalid {
		return
	}

	c.keepParam(op, "returned", e.Pos()) // a parameter returned is what the function keeps, checked at its calls; what else the value lets out the borrow pass decides
}

// returnEscapes checks what the returned value op, no parameter, lets
// out of the function: a closure that borrows cannot leave, and a
// borrow must come from the function's source, followed through the
// loans in effect at the return.
func (c *Checker) returnEscapes(op operand, pos token.Pos) {
	e := op.expr
	if isSignature(op.typ) {
		c.cannotBe(op, "returned", pos)
		return
	}

	if holdsBorrow(c.sig.results) {
		if tmp := c.borrowOfTemporary(e); tmp != nil {
			c.errorf(pos, "cannot return a borrow of a temporary: "+exprText(tmp)+" is dropped when the statement ends")
			return
		}

		c.source(e, pos)
	}
}

// source finds the places the borrows in e come from and checks each,
// in every branch of a value chosen at run time.
func (c *Checker) source(e ast.Expr, pos token.Pos) {
	if eachBranch(e, func(v ast.Expr) { c.source(v, pos) }) {
		return
	}

	switch e := e.(type) {
	case *ast.ParenExpr:
		c.source(e.X, pos)
	case *ast.UnaryExpr:
		switch e.Op {
		case token.REF, token.MUT:
			c.sourceVar(e.X, pos, true)
		case token.MUL:
			c.source(e.X, pos) // *r lent on, first(*r): what r points at, so the borrow r holds
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
		} else if src := c.callSource(e); src != nil && !c.passed[e].only {
			c.source(src, pos)
		}

		for _, x := range c.passed[e].args {
			c.source(x, pos) // a generic call's result holds what its arguments hold
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
		return c.refersTo(fun)
	}

	return c.named(e.Fun)
}

// borrowedParam is the index of the one parameter that holds a borrow,
// a variadic ...ref one aside, since no borrow of it is returned, or -1. A generic function may take a borrow through a type parameter
// instead, wrap(ref s), so with no borrowed parameter the first one of
// a type parameter's type is the source.
func borrowedParam(sig *Signature) int {
	for i, p := range sig.params {
		if holdsBorrow(p.typ) && !p.lent {
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

	v := p.v
	if v.global {
		c.errorf(pos, "cannot return a borrow of "+v.name+": a returned borrow is tied to the receiver or a borrowed parameter")
		return
	}

	if v.lent {
		c.errorf(pos, lentReturn(v)) // a closure's own return too, where v is no parameter of its signature
		return
	}

	if c.keepsWord() && c.isParam(v) && c.wordSource(v, p.path, lent, kind == placeIndexed, pos, c.errorf) {
		return // an implementation keeping its trait's word returns only what is stored behind its parameters (word.go)
	}

	throughBorrow := kind == placeBorrowed || kind == placeIndexed && isBorrow(v.typ) // ref ys[0], ys a borrow: what ys borrows, not ys
	if lent && !throughBorrow && !c.isParam(v) {
		c.errorf(pos, "cannot return a borrow of local "+v.name)
		return
	}

	if !c.isParam(v) {
		c.flowReturn(v, pos, returnedPart{path: p.path, lent: lent, indexed: kind == placeIndexed}) // followed to its source through the loans in effect at the return, at the function's end
		return
	}

	c.sourceParam(v, pos, c.errorf)
}

// followReturn follows a borrow returned out of v through the loans v
// may hold at the return, each to the variable it borrows, until it
// reaches the function's receiver or parameters.
func (c *Checker) followReturn(v *Var, part returnedPart, pos token.Pos, seq int, in bitset, seen []*Var) {
	f := c.flow
	report := func(pos token.Pos, msg string) { f.errs = append(f.errs, flowError{seq: seq, pos: pos, msg: msg}) }
	if c.keepsWord() {
		report = c.word.once(seq, report) // one return breaks the word once, however many ways it reaches the parameter
	}

	if param := f.outside[v]; param != nil && c.keepsWord() {
		c.wordHeld(v, param, pos, report)
		return
	}

	if param := f.outside[v]; param != nil {
		v = param // what the caller owns behind a parameter is reached through the parameter
	}

	if v.lent {
		report(pos, lentReturn(v))
		return
	}

	if c.isParam(v) {
		c.sourceParam(v, pos, report)
		return
	}

	seen = append(seen, v)
	found := false
	in.each(func(li int) bool {
		if b := &f.loans[li]; b.holder == v {
			found = true
			if c.keepsWord() && c.wordLoan(b, part, pos, report) {
				return true
			}

			if !slices.Contains(seen, b.v) {
				c.followReturn(b.v, part.through(b), pos, seq, in, seen)
			}
		}

		return true
	})

	switch {
	case found:
	case slices.Contains(f.temporaries, v):
		report(pos, "cannot return a borrow of a temporary: "+v.name+" is dropped when its statement ends")
	case f.outer != token.NoPos && v.pos < f.outer: // a closure's body: the variable is the enclosing function's, captured
		report(pos, "cannot return a borrow of "+v.name+" from a closure: "+v.name+" is captured, and a closure's returned borrow is tied to its parameters")
	case !holdsBorrow(v.typ):
		report(pos, "cannot return a borrow of local "+v.name) // else a borrow with no record to follow: from a call, or built in a literal
	}
}

// sourceParam reports a returned borrow of the receiver or a parameter
// that is not the function's source.
func (c *Checker) sourceParam(v *Var, pos token.Pos, report func(token.Pos, string)) {
	switch {
	case v.lent:
		report(pos, lentReturn(v))
	case c.sig.recv == v && isBorrow(v.typ):
	case c.sig.recv == v || !holdsBorrow(v.typ):
		report(pos, "cannot return a borrow of local "+v.name) // taken by value, it dies at return
	case c.sig.recv != nil && isBorrow(c.sig.recv.typ):
		report(pos, "cannot return a borrow of "+v.name+": a method's returned borrow is tied to its receiver")
	}
}

// lentReturn refuses a returned borrow of v, a variadic ...ref
// parameter, whose arguments the caller lays out for the call alone,
// from the function's body or from a closure inside it.
func lentReturn(v *Var) string {
	return "cannot return a borrow of " + v.name + ": the arguments of a variadic ...ref parameter are lent for the call alone"
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
