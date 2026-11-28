// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
	"slices"
)

// The rules of borrowing, which the borrow pass over the form
// (formborrows.go) applies. A borrow is created by ref or mut on a place,
// by a method on its receiver, by a parameter that lends, by a pattern
// matched through a borrow, and by ?? on a borrowed option; it is held
// by the variable it is bound to, or by a field or element of it, and
// lives while some path from where it is reaches a use of its holder.
// Any number of ref borrows of a place may be live together, or one
// mut; while a place is lent the owner may not assign to it, move it
// or drop it, and while it is lent mut nothing else may read it. The
// accesses and the borrows are recorded in the body's graph and
// decided at its end, along its paths (flow.go); a borrow lent for one
// call lives for that call only and collides at once.

// A borrow records a place lent to a holder: the variable holding the
// borrow, through the part hpath of it, or a range loop's own variable,
// which holds its collection for every pass; the place lent, whether
// it is lent mut, and where it began.
type borrow struct {
	holder *Var
	place
	mut   bool
	pos   token.Pos
	hpath string // the part of the holder the borrow is stored in, or "" for the whole; "*" begins a part stored through the holder, a borrow
	loop  bool   // held by a range loop
	// variable says the borrow is of the variable itself, which is a
	// borrow: a closure that reads it when called would see what it is
	// given after.
	variable bool
	// atExit says the borrow is a deferred call's, of a whole variable:
	// the call reaches the variable only when the function returns, after
	// every other use, so reading and changing it before then is fine,
	// and only moving it away or dropping it is not.
	atExit bool
	// lent says the borrow was copied from a borrow variable a place
	// behind which was lent, x := ref r.f taking what r holds: the loan
	// of that place, made beside it and copied with it wherever it goes,
	// says what x borrows of r's.
	lent bool
	// read says the borrow was read out of the place, a borrow stored
	// there, x := h.src, rather than a borrow of the place itself, x :=
	// ref h.own: the holder holds what the place pointed at.
	read bool
	// call says the holder stands for a temporary lent for a call or a
	// statement, which no variable holds: a message names no holder and
	// no use, the loan copies nothing the place holds, and it is of no
	// place the owner may have dropped or that escapes, as for the loans
	// lent for one call today.
	call bool
}

// An access says what an expression does to a place, which decides the
// borrows it collides with: a read collides with a mut borrow, a write
// or a move with any, a ref borrow with a mut one, a mut borrow with
// any.
type access int8

const (
	readAccess access = iota
	writeAccess
	moveAccess
	refAccess
	mutAccess
	rebindAccess // a borrow variable given another borrow: what borrowed through it is unmoved, a closure that reads it is not
)

// access checks an access to a place against the borrows lent for the
// call being checked, at once, and records it in the graph, where the
// borrows held by variables are decided at the end of the function.
func (c *Checker) access(e ast.Expr, p place, what access, pos token.Pos) {

	if p.v.global && what != readAccess && what != refAccess { // a package variable is reached from every task: read and lent as ref, never changed or taken
		c.errorf(pos, p.v.name+" is a package variable, shared by every task; change it through a sync.Mutex, a sync.Atomic or a sync.Counter it holds")
		return
	}

	c.flowAccess(e, p, what, pos)
}

// collides reports whether an access to p collides with the borrow:
// the places must overlap, and a read or a ref borrow is fine beside a
// ref borrow.
func (b *borrow) collides(p place, what access) bool {
	if what == rebindAccess {
		return b.variable && b.v == p.v
	}

	if b.v != p.v || !b.place.within(p) && !p.within(b.place) {
		return false
	}

	if b.atExit {
		return what == moveAccess
	}

	return b.mut || what != readAccess && what != refAccess
}

// collision writes the message for an access that collides with b, up
// to where the borrow began; the use that keeps it live is added when
// the conflict is resolved.
func (c *Checker) collision(e ast.Expr, p place, what access, b *borrow) string {
	var msg string
	switch what {
	case readAccess:
		msg = "cannot use " + exprText(e) + ": " + b.text() + " is borrowed as mut"
	case writeAccess, rebindAccess:
		msg = "cannot assign to " + exprText(e) + ": " + b.text() + " is borrowed"
		if c.flow != nil && slices.Contains(c.flow.slots, p.v) {
			msg = "cannot make the view " + exprText(e) + " again: the one it made before is still held" // its header's slot written while a borrow of it is used
		}
	case moveAccess:
		msg = "cannot move " + p.text() + ": " + b.text() + " is borrowed"
	case refAccess:
		msg = "cannot borrow " + exprText(e) + " as ref: " + b.text() + " is already borrowed as mut"
	case mutAccess:
		msg = "cannot borrow " + exprText(e) + " as mut: " + b.text() + " is borrowed as ref"
		if b.mut {
			msg = "cannot borrow " + exprText(e) + " as mut: " + b.text() + " is already borrowed as mut"
		}
	}

	if b.holder != nil && !b.call {
		if by := c.holderText(b); by != "" {
			msg += " by " + by
		}
	}

	return msg + " at " + c.lineCol(b.pos)
}

// borrowAccess is the access a borrow makes, ref or mut.
func borrowAccess(mut bool) access {
	if mut {
		return mutAccess
	}

	return refAccess
}

// holdValue records that the part path of v, of type t, holds what the
// closure or function value e holds: what a closure written here
// captures, or what the value's expression lends.
func (c *Checker) holdValue(v *Var, path string, t Type, e ast.Expr, pos token.Pos) {
	if _, ok := unparen(e).(*ast.Ident); ok && isSignature(t) {
		return // a closure variable copied: refused where the store is made, by the operand
	}

	if caps := c.capturesOf(e); caps != nil {
		for _, cap := range caps { // g := func() { n += 1 }: g holds n
			if cap.mode != byMove { // the closure owns what it moved; the variable is dead outside
				b := cap.borrow(v, pos)
				b.hpath = path
				c.flowLoan(b)
			}
		}

		return
	}

	c.derive(v, path, t, e, pos)
}

// static reports whether e is a literal or a constant, this package's
// or another's, time.TimeOnly, whose value outlives every statement.
func (c *Checker) static(e ast.Expr) bool {
	switch e := unparen(e).(type) {
	case *ast.BasicLit:
		return true
	case *ast.Ident:
		_, ok := c.refersTo(e).(*Const)
		return ok
	case *ast.SelectorExpr:
		_, ok := c.named(e).(*Const)
		return ok
	}

	return false
}

// temporaryText names the temporary e for a message: an if's or a
// match's value by the construct, since its text is its branches, and
// any other by its text.
func temporaryText(e ast.Expr) string {
	switch unparen(e).(type) {
	case *ast.IfExpr:
		return "the if's value"
	case *ast.MatchExpr:
		return "the match's value"
	case *ast.CoalesceExpr:
		return "the ??'s value"
	}

	return exprText(e)
}

// derive records that the part path of v, of type t, holds what e
// carries, when t carries a borrow: a borrow of each place e lends from,
// and what each place e reads a ref borrow out of holds, q := r or r =
// h.src, which v copies. A mut borrow read out of a place, m := cur ??
// return, is borrowed from it: the place stays lent while v lives.
func (c *Checker) derive(v *Var, path string, t Type, e ast.Expr, pos token.Pos) {
	mut, ok := borrowIn(t)
	if !ok {
		return
	}

	c.sources(e, func(src ast.Expr, read, deref bool) { c.deriveFrom(v, path, mut, src, read, deref, pos) })
}

// deriveFrom records that the part path of v holds what one source of a
// value lends, the place src, read as a value or lent, through a * or
// not.
func (c *Checker) deriveFrom(v *Var, path string, mut bool, src ast.Expr, read, deref bool, pos token.Pos) {
	p, kind := c.placeOf(src)
	c.derivePlace(v, path, mut, p, kind, read, deref, pos)
}

// derivePlace records that the part path of v holds what the place p,
// reached as kind says, lends: a ref borrow read out of an owned place
// is copied, and any other place is borrowed, mut when the value is, at
// pos.
func (c *Checker) derivePlace(v *Var, path string, mut bool, p place, kind placeKind, read, deref bool, pos token.Pos) {
	if kind == placeOwned && read && holdsBorrow(placeType(p)) {
		if mut, _ := borrowIn(placeType(p)); !mut {
			from := p.path
			if deref && isBorrow(placeType(p)) {
				from = joinPath(p.path, "*") // x := *r holds what r points at holds, as a copy of it would, and not r's own borrow of it
			}

			c.flowCopy(v, p.v, from, path, pos) // a ref borrow is copied: v holds what p holds, and p is free
			return
		}
	}

	if p, kind := through(p, kind); kind != notPlace {
		at := placeType(p)
		if kind == placeIndexed {
			at = stepType(at, "[]") // an element read: the borrow it is, not the slice's
		}

		c.flowLoan(borrow{holder: v, place: p, mut: mut, pos: pos, hpath: path, read: read && isBorrow(at)})
	}
}

// accessPos is the position an access to the place e is reported at:
// the place behind any * and parentheses, where the operand was read,
// so that the read and the write of *p = v are seen as one access.
func accessPos(e ast.Expr) token.Pos {
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
		case *ast.UnaryExpr:
			if x.Op != token.MUL {
				return e.Pos()
			}

			e = x.X
		default:
			return e.Pos()
		}
	}
}

func unparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}

		e = p.X
	}
}

// sources calls f with each place expression a borrow-valued
// expression lends from: the operand of ref or mut, the receiver of a
// method or the argument a function's result borrows (callSource), the
// operand of a slice expression, or the place itself when a borrow is
// read out of a variable, field or element, and every place a value
// chosen at run time may come from, both sides of ??, and each branch
// of an if or match expression. It says whether the borrow is read out
// of the place, a borrow the place holds, rather than lent from it: r in
// q := r, h.src in r = h.src, against s in ref s and m in m.Get(k); and
// whether it was reached through a *, x := *r, which reads what the
// borrow r points at and not r's own borrow of it.
func (c *Checker) sources(e ast.Expr, f func(src ast.Expr, read, deref bool)) {
	c.sourcesLent(e, false, false, f)
}

func (c *Checker) sourcesLent(e ast.Expr, lent, deref bool, f func(ast.Expr, bool, bool)) {
	if eachBranch(e, func(v ast.Expr) { c.sourcesLent(v, lent, deref, f) }) {
		return
	}

	switch e := e.(type) {
	case *ast.ParenExpr:
		c.sourcesLent(e.X, lent, deref, f)
	case *ast.UnaryExpr:
		switch e.Op {
		case token.REF, token.MUT:
			f(e.X, false, deref)
		case token.MUL:
			c.sourcesLent(e.X, lent, true, f) // *f reads what the borrow f points at, a borrow itself when it holds one: x := *f holds what f holds
		}
	case *ast.TryExpr:
		c.sourcesLent(e.X, lent, deref, f)
	case *ast.SliceExpr:
		f(e.X, false, deref)
	case *ast.CallExpr:
		if _, ok := c.calleeOf(e).(*Variant); ok {
			for _, arg := range e.Args { // Pair(ref a, ref b) carries both
				c.sourcesLent(arg, lent, deref, f)
			}

			return
		}

		passed := c.passed[e]
		if src := c.callSource(e); src != nil && !passed.only {
			fn, ok := c.calledBy(e).obj.(*Func)
			kept := ok && fn.wordKept()
			c.sourcesLent(src, !kept, deref, f) // a call's result borrows its source: lent, whatever the source is; a method keeping its trait's word holds what the receiver holds instead
			if kept {
				for _, x := range e.Args {
					c.sourcesLent(x, false, deref, f) // and what each argument holds, as a generic call's result does
				}
			}
		}

		for _, x := range passed.args {
			c.sourcesLent(x, false, deref, f) // a generic call's result holds what its arguments hold (passing.go)
		}
	case *ast.TupleExpr:
		for _, elt := range e.Elts { // (ref w, 1) carries w
			c.sourcesLent(elt, lent, deref, f)
		}
	case *ast.CompositeLit:
		for _, elt := range e.Elts { // Holder{src: ref t} carries t
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				elt = kv.Value
			}

			c.sourcesLent(elt, lent, deref, f)
		}
	case *ast.Ident, *ast.SelectorExpr, *ast.IndexExpr:
		f(e, !lent, deref)
	}
}

// eachBranch calls f with each expression the value of e is chosen
// from at run time, and reports whether e is such a choice: both sides
// of x ?? y, and the value of each branch of an if or match expression,
// the expression a branch's block ends with. A side that leaves, ??
// return, gives none. It allocates nothing.
func eachBranch(e ast.Expr, f func(ast.Expr)) bool {
	yield := func(n ast.Node) {
		if v := valueOf(n); v != nil {
			f(v)
		}
	}

	switch x := unparen(e).(type) {
	case *ast.CoalesceExpr:
		f(x.X)
		yield(x.Else)
	case *ast.IfExpr:
		for x != nil {
			yield(x.Then)
			els, chained := x.Else.(*ast.IfExpr)
			if !chained {
				yield(x.Else)
			}

			x = els
		}
	case *ast.MatchExpr:
		for _, arm := range x.Arms {
			yield(arm.Body)
		}
	default:
		return false
	}

	return true
}

// valueOf returns the expression a branch yields: the branch itself
// when it is one, or the expression its block ends with; nil for none.
func valueOf(n ast.Node) ast.Expr {
	switch n := n.(type) {
	case *ast.BlockStmt:
		if len(n.List) > 0 {
			if s, ok := n.List[len(n.List)-1].(*ast.ExprStmt); ok {
				return s.X
			}
		}
	case ast.Expr:
		return n
	}

	return nil
}

// borrowIn reports whether a value of type t is or carries a borrow,
// and whether that borrow is mut: a ref or mut, an option, result or
// array of one, the slice of borrows an iterator stands for, a tuple
// holding one, or a struct or enum with such a field, which is a
// borrow itself; the kinds holdsBorrow looks into.
func borrowIn(t Type) (mut, ok bool) {
	switch t := t.(type) {
	case *Ref:
		return t.mut, true
	case *Option:
		return borrowIn(t.elem)
	case *Result:
		if mut, ok := borrowIn(t.val); ok {
			return mut, true
		}

		return borrowIn(t.err)
	case *Slice:
		return borrowIn(t.elem)
	case *Array:
		return borrowIn(t.elem)
	case *Tuple:
		for _, e := range t.elems {
			if mut, ok := borrowIn(e); ok {
				return mut, true
			}
		}
	case *Named:
		if t.holdsBorrow() {
			return borrowIn(t.Underlying())
		}
	case *Struct:
		return borrowInVars(t.fields)
	case *Enum:
		for _, v := range t.variants {
			if mut, ok := borrowInVars(v.fields); ok {
				return mut, true
			}
		}
	}

	return false, false
}

func borrowInVars(vs []*Var) (mut, ok bool) {
	for _, v := range vs {
		if mut, ok := borrowIn(v.typ); ok {
			return mut, true
		}
	}

	return false, false
}

// throughWhat names what a ref borrow was reached through, for a
// message: the method's receiver, or a borrow.
func (c *Checker) throughWhat(e ast.Expr) string {
	if p, kind := c.placeOf(e); kind != notPlace && c.recv != nil && c.recv == p.v {
		return "receiver"
	}

	return "borrow"
}

// closureExprBorrows reports whether the value e is a closure that
// borrows: one written here that captures by borrow, a variable holding
// one, or either chosen by a branch of an if or a match.
func (c *Checker) closureExprBorrows(e ast.Expr) bool {
	return c.borrowsWhat(c.valueOf(e), e.Pos()) != ""
}
