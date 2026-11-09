// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// The borrow pass runs inside the checker's walk
// beside the move pass. A borrow is created by ref or mut on a place,
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

// accessExpr resolves e to its place and records what the access does
// to it. For an assignment to a variable holding a borrow it returns
// that variable: the assignment rebinds it and ends what it held.
func (c *Checker) accessExpr(e ast.Expr, what access, pos token.Pos) *Var {
	p, kind := c.placeOf(e)
	if kind == notPlace {
		return nil
	}

	c.capture(p.v, byMut)
	if what == writeAccess && kind == placeOwned && p.path == "" && isBorrow(p.v.typ) {
		c.access(e, p, rebindAccess, pos) // a closure that reads it when called would see the new borrow
		c.flowAssign(p.v, "")             // the assignment points it somewhere else; what borrowed through it copied what it held, and still points there
		return p.v
	}

	c.access(e, p, what, pos)
	return nil
}

// access checks an access to a place against the borrows lent for the
// call being checked, at once, and records it in the graph, where the
// borrows held by variables are decided at the end of the function.
func (c *Checker) access(e ast.Expr, p place, what access, pos token.Pos) {
	if p.v.global && what != readAccess && what != refAccess { // a package variable is reached from every task: read and lent as ref, never changed or taken
		c.errorf(pos, p.v.name+" is a package variable, shared by every task; change it through a sync.Mutex, a sync.Atomic or a sync.Counter it holds")
		return
	}

	for i := range c.temps {
		if t := &c.temps[i]; t.collides(p, what) {
			if c.flow == nil {
				c.errorf(pos, c.collision(e, p, what, t))
				return
			}

			c.flowError(pos, c.collision(e, p, what, t), true)
			return
		}
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

	if b.holder != nil {
		msg += " by " + c.holderText(b)
	}

	return msg + " at " + c.lineCol(b.pos)
}

// lend records a borrow of e for the call or statement being checked,
// after checking the access, so that a later argument or the receiver
// collides with it. A receiver is lent after the arguments are checked,
// so a borrow they end is over before it.
func (c *Checker) lend(e ast.Expr, mut bool) {
	p, kind := c.placeOf(e)
	if kind == notPlace {
		return
	}

	what := refAccess
	if mut {
		what = mutAccess
		c.capture(p.v, byMut)
	}

	c.access(e, p, what, e.Pos())
	c.temps = append(c.temps, borrow{place: p, mut: mut, pos: e.Pos()})
}

// lendCaptures lends what a closure literal captures for the call it
// is passed to, so that the call cannot borrow the same variable again.
func (c *Checker) lendCaptures(e ast.Expr, pos token.Pos) {
	for _, cap := range c.capturesOf(e) {
		if cap.mode == byMove {
			continue
		}

		c.temps = append(c.temps, cap.borrow(nil, pos)) // the closure's creation made the access
	}
}

// hold records that v holds the borrow its value carries, when the
// value is a borrow and the place it was lent from can be found: r in
// r := ref xs holds xs, n in n := cur ?? return holds what cur holds,
// f in f := m.Get(k) holds m, and v in v := pick(0, ref m) holds m,
// the argument of pick's borrowed parameter. A borrow of a temporary
// cannot be held at all: t := s.Clone().Trim() would outlive the clone.
func (c *Checker) hold(v *Var, e ast.Expr, pos token.Pos) {
	c.holdIn(v, "", v.typ, e, pos)
}

// holdIn records that the part path of v, of type t, holds the borrow e
// carries: h.src = ref t makes h hold t.
func (c *Checker) holdIn(v *Var, path string, t Type, e ast.Expr, pos token.Pos) {
	if e == nil {
		return
	}

	if _, ok := borrowIn(t); ok {
		if tmp := c.borrowOfTemporary(e); tmp != nil {
			c.errorf(pos, "cannot keep a borrow of a temporary: "+exprText(tmp)+" is dropped when the statement ends")
			return
		}
	}

	if id, ok := unparen(e).(*ast.Ident); ok && isSignature(t) {
		c.usedWhereMade(id, "copied to another variable", pos)
		return
	}

	if id, ok := unparen(e).(*ast.Ident); ok && isMut(t) {
		if p, kind := c.placeOf(id); kind == placeOwned {
			c.flowCopy(v, p.v, "", path, pos) // q := p moves a mut borrow: q holds what p held
			return
		}
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

	if lit, ok := unparen(e).(*ast.CompositeLit); ok {
		for _, elt := range lit.Elts { // l := Lexer{src: ref s}: l holds s, in its field src
			part := path
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				if key, ok := kv.Key.(*ast.Ident); ok {
					part = joinPath(path, key.Name)
				}

				elt = kv.Value
			}

			c.derive(v, part, t, elt, pos)
		}

		return
	}

	if call, ok := unparen(e).(*ast.CallExpr); ok {
		if _, ok := c.calleeOf(call).(*Variant); ok {
			for _, arg := range call.Args { // p := Pair(ref a, ref b): p holds both
				c.derive(v, path, t, arg, pos)
			}

			return
		}
	}

	c.derive(v, path, t, e, pos)
}

// noteTemporary records that e, lent to a call or a ref, is a
// temporary when its value is owned and no variable holds it, other
// than a literal or a constant, which live for the whole program.
func (c *Checker) noteTemporary(e ast.Expr, x operand) {
	if x.mode == value && !isBorrow(x.typ) && !c.static(e) {
		c.markTemporary(e, e)
	}
}

// markTemporary records that the value of e is, or borrows, the
// temporary tmp.
func (c *Checker) markTemporary(e, tmp ast.Expr) {
	if c.temporary == nil {
		c.temporary = make(map[ast.Expr]ast.Expr)
	}

	c.temporary[e] = tmp
}

// static reports whether e is a literal or a constant, whose value
// outlives every statement.
func (c *Checker) static(e ast.Expr) bool {
	switch e := unparen(e).(type) {
	case *ast.BasicLit:
		return true
	case *ast.Ident:
		_, ok := c.resolve(e.Name).(*Const)
		return ok
	}

	return false
}

// borrowOfTemporary returns the temporary a borrow in e was lent
// from, looking through parentheses, ?? and ?, and into the elements
// of a literal or tuple, or nil when every borrow in e comes from a
// place.
func (c *Checker) borrowOfTemporary(e ast.Expr) ast.Expr {
	if vs, ok := branches(e); ok {
		for _, v := range vs { // a branch that lends a temporary
			if tmp := c.borrowOfTemporary(v); tmp != nil {
				return tmp
			}
		}

		return nil
	}

	switch x := e.(type) {
	case *ast.ParenExpr:
		return c.borrowOfTemporary(x.X)
	case *ast.TryExpr:
		return c.borrowOfTemporary(x.X)
	case *ast.TupleExpr:
		for _, elt := range x.Elts {
			if tmp := c.borrowOfTemporary(elt); tmp != nil {
				return tmp
			}
		}
	case *ast.CompositeLit:
		for _, elt := range x.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				elt = kv.Value
			}

			if tmp := c.borrowOfTemporary(elt); tmp != nil {
				return tmp
			}
		}
	}

	return c.temporary[e]
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

	c.sources(e, func(src ast.Expr, read, deref bool) {
		p, kind := c.placeOf(src)
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
			c.flowLoan(borrow{holder: v, place: p, mut: mut, pos: pos, hpath: path})
		}
	})
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

// holdPattern records the bindings of a pattern matched through a
// borrow of the place src as holders of that borrow, in the order they
// are written.
func (c *Checker) holdPattern(p ast.Pattern, x ast.Expr) {
	switch p := p.(type) {
	case *ast.IdentPat:
		if v, ok := c.scope.Lookup(p.Name.Name).(*Var); ok {
			c.derive(v, "", v.typ, x, p.Name.Pos())
		}
	case *ast.TuplePat:
		for _, elt := range p.Elts {
			c.holdPattern(elt, x)
		}
	case *ast.VariantPat:
		for _, arg := range p.Args {
			c.holdPattern(arg, x)
		}
	case *ast.OrPat:
		for _, alt := range p.Alts {
			c.holdPattern(alt, x)
		}
	}
}

// bindThrough checks the bindings of a match or if-binding once the
// arm's scope holds them. On a scrutinee reached through a borrow they
// hold the borrow, and mut bindings borrow the scrutinee mut. On an
// owned scrutinee a binding holds what the value lends, as one bound
// by := does: v in if v := m.Get(k) holds m. A mut binding through a
// map read is refused, since a key the map does not hold reads the
// zero value and a write to it would be lost.
func (c *Checker) bindThrough(p ast.Pattern, x ast.Expr, b *Ref) {
	if c.scope.Len() == 0 {
		return
	}

	if b == nil {
		c.holdPattern(p, x)
		return
	}

	if b.mut {
		if ix, ok := x.(*ast.IndexExpr); ok && isMap(stripRef(c.placeType(ix.X))) {
			c.errorf(x.Pos(), "cannot bind mut through "+exprText(x)+": a key the map does not hold reads the zero value; use "+exprText(ix.X)+".GetMut("+exprText(ix.Index)+")")
			return
		}

		c.accessExpr(x, mutAccess, x.Pos())
	}

	c.holdPattern(p, x)
}

// borrowSources finds the place expressions a borrow-valued expression
// lends from: the operand of ref or mut, the receiver of a method or
// the argument a function's result borrows (callSource), the operand
// of a slice expression, or the place itself when a borrow is read out
// of a variable, field or element. A value chosen at run time lends
// from every place it may come from: both sides of ??, and each branch
// of an if or match expression. None when there is none.
func (c *Checker) borrowSources(e ast.Expr) []ast.Expr {
	var srcs []ast.Expr
	c.sources(e, func(src ast.Expr, _, _ bool) { srcs = append(srcs, src) })
	return srcs
}

// sources calls f with each place borrowSources finds, whether the
// borrow is read out of the place, a borrow the place holds, rather
// than lent from it: r in q := r, h.src in r = h.src, against s in ref
// s and m in m.Get(k); and whether it was reached through a *, x := *r,
// which reads what the borrow r points at and not r's own borrow of it.
func (c *Checker) sources(e ast.Expr, f func(src ast.Expr, read, deref bool)) {
	c.sourcesLent(e, false, false, f)
}

func (c *Checker) sourcesLent(e ast.Expr, lent, deref bool, f func(ast.Expr, bool, bool)) {
	if vs, ok := branches(e); ok {
		for _, v := range vs {
			c.sourcesLent(v, lent, deref, f)
		}

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

		if src := c.callSource(e); src != nil {
			c.sourcesLent(src, true, deref, f) // a call's result borrows its source: lent, whatever the source is
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

// branches returns the expressions the value of e is chosen from at run
// time, and whether e is such a choice: both sides of x ?? y, and the
// value of each branch of an if or match expression, the expression a
// branch's block ends with. A side that leaves, ?? return, gives none.
func branches(e ast.Expr) ([]ast.Expr, bool) {
	var vs []ast.Expr
	switch x := unparen(e).(type) {
	case *ast.CoalesceExpr:
		vs = append(append(vs, x.X), valueOf(x.Else)...)
	case *ast.IfExpr:
		for x != nil {
			vs = append(vs, valueOf(x.Then)...)
			switch els := x.Else.(type) {
			case *ast.IfExpr:
				x = els
				continue
			default:
				vs = append(vs, valueOf(els)...)
			}

			x = nil
		}
	case *ast.MatchExpr:
		for _, arm := range x.Arms {
			vs = append(vs, valueOf(arm.Body)...)
		}
	default:
		return nil, false
	}

	return vs, true
}

// valueOf returns the expression a branch yields: the branch itself
// when it is one, or the expression its block ends with.
func valueOf(n ast.Node) []ast.Expr {
	switch n := n.(type) {
	case *ast.BlockStmt:
		if len(n.List) > 0 {
			if s, ok := n.List[len(n.List)-1].(*ast.ExprStmt); ok {
				return []ast.Expr{s.X}
			}
		}
	case ast.Expr:
		return []ast.Expr{n}
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

// pin resolves the variables a value carrying a borrow names, while the
// block or arm that yields it is still open: x := if c { t := ...; ref t
// } else { ... } holds what the block's names hold, though the names
// are gone by the time x takes the value, and a borrow of one dropped
// with the block is refused there.
func (c *Checker) pin(e ast.Expr, t Type) {
	if c.flow == nil || !holdsBorrow(t) && !isSignature(t) {
		return
	}

	if isSignature(t) && c.usedWhereMade(e, "chosen by if or match, or come out of a block", e.Pos()) {
		return
	}

	c.sources(e, func(src ast.Expr, _, _ bool) {
		for {
			switch x := src.(type) {
			case *ast.ParenExpr:
				src = x.X
			case *ast.SelectorExpr:
				src = x.X
			case *ast.IndexExpr:
				src = x.X
			case *ast.SliceExpr:
				src = x.X
			case *ast.UnaryExpr:
				src = x.X
			case *ast.Ident:
				if v, ok := c.resolve(x.Name).(*Var); ok {
					if c.pinned == nil {
						c.pinned = make(map[*ast.Ident]*Var)
					}

					c.pinned[x] = v
				}

				return
			default:
				return
			}
		}
	})
}
