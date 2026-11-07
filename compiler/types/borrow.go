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
// by the variable it is bound to and lives until that variable's last
// use. Any number of ref borrows of a place may be live together, or
// one mut; while a place is lent the owner may not assign to it or move
// it, and while it is lent mut nothing else may read it. An access that
// collides with a borrow is kept as a conflict until the end of the
// function, when every variable's last use is known and the borrow's
// liveness at the access can be decided; a borrow lent for one call
// lives for that call only and collides at once. See
// _docs/book/06-borrows.md for the rules as the reader sees them.

// A borrow records a place lent to a holder: the variable holding the
// borrow, or none for a range loop that holds its collection for the
// whole body, the place lent, whether it is lent mut, where it began
// and, once the holder was rebound, where it ended and the holder's
// last use before that.
type borrow struct {
	holder *Var
	place
	mut   bool
	pos   token.Pos
	end   token.Pos // where the holder was rebound, or NoPos while it holds the borrow
	last  token.Pos // the holder's last use before end
	until token.Pos // for a range loop, the end of its body
}

// A conflict is an access that collided with a borrow, kept until the
// function's end. A temporary borrow is live by construction; a held
// one is live when its holder is used after the access.
type conflict struct {
	pos   token.Pos // where the access is reported
	at    token.Pos // where it takes effect: a receiver is borrowed after its arguments
	msg   string
	b     int         // the index of the borrow in c.borrows, or -1 for a temporary
	loops []token.Pos // where the loops around pos begin
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
		c.rebind(p.v, pos)
		return p.v
	}

	c.access(e, p, what, pos, pos)
	return nil
}

// access checks an access to a place against the borrows live at that
// point: the ones lent for the call being checked and the ones held by
// variables, which may or may not be live and are decided at the end
// of the function. The access is reported at pos and takes effect at
// at, which is later for a receiver, borrowed after its arguments.
func (c *Checker) access(e ast.Expr, p place, what access, pos, at token.Pos) {
	if p.v.global && what != readAccess && what != refAccess { // a package variable is reached from every task: read and lent as ref, never changed or taken
		c.errorf(pos, p.v.name+" is a package variable, shared by every task; change it through a sync.Mutex, a sync.Atomic or a sync.Counter it holds")
		return
	}

	for i := range c.temps {
		if t := &c.temps[i]; t.collides(p, what) {
			c.conflictAt(pos, at, c.collision(e, p, what, t), -1)
			return
		}
	}

	for i := range c.borrows {
		if b := &c.borrows[i]; b.end == token.NoPos && b.collides(p, what) {
			c.conflictAt(pos, at, c.collision(e, p, what, b), i)
			return
		}
	}
}

// collides reports whether an access to p collides with the borrow:
// the places must overlap, and a read or a ref borrow is fine beside a
// ref borrow.
func (b *borrow) collides(p place, what access) bool {
	if b.v != p.v || !b.place.within(p) && !p.within(b.place) {
		return false
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
	case writeAccess:
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

	switch {
	case b.holder != nil:
		msg += " by " + b.holder.name
	case b.until != token.NoPos:
		msg += " by the range loop"
	}

	return msg + " at " + c.lineCol(b.pos)
}

// conflictAt keeps a conflict for the end of the function. Two accesses
// at one position are one access seen twice, as the operand of a
// borrow is read and then lent; the later, more specific message wins.
func (c *Checker) conflictAt(pos, at token.Pos, msg string, b int) {
	if n := len(c.conflicts); n > 0 && c.conflicts[n-1].pos == pos {
		c.conflicts[n-1] = conflict{pos, at, msg, b, c.conflicts[n-1].loops}
		return
	}

	var loops []token.Pos
	for _, r := range c.regions {
		if r.loop {
			loops = append(loops, r.pos)
		}
	}

	c.conflicts = append(c.conflicts, conflict{pos, at, msg, b, loops})
}

// lend records a borrow of e for the call or statement being checked,
// after checking the access, so that a later argument or the receiver
// collides with it. The borrow takes effect at at: for a receiver, the
// end of the call, once the arguments are evaluated.
func (c *Checker) lend(e ast.Expr, mut bool, at token.Pos) {
	p, kind := c.placeOf(e)
	if kind == notPlace {
		return
	}

	what := refAccess
	if mut {
		what = mutAccess
		c.capture(p.v, byMut)
	}

	c.access(e, p, what, e.Pos(), at)
	c.temps = append(c.temps, borrow{place: p, mut: mut, pos: e.Pos()})
}

// lendCaptures lends what a closure literal captures for the call it
// is passed to, so that the call cannot borrow the same variable again.
func (c *Checker) lendCaptures(e ast.Expr, pos token.Pos) {
	for _, cap := range c.capturesOf(e) {
		if cap.mode == byMove {
			continue
		}

		b := cap.borrow(nil, pos)
		what := refAccess
		if b.mut {
			what = mutAccess
		}

		c.access(&ast.Ident{NamePos: pos, Name: cap.v.name}, b.place, what, pos, pos) // named for the message
		c.temps = append(c.temps, b)
	}
}

// hold records that v holds the borrow its value carries, when the
// value is a borrow and the place it was lent from can be found: r in
// r := ref xs holds xs, n in n := cur ?? return holds what cur holds,
// f in f := m.Get(k) holds m, and v in v := pick(0, ref m) holds m,
// the argument of pick's borrowed parameter. A borrow of a temporary
// cannot be held at all: t := s.Clone().Trim() would outlive the clone.
func (c *Checker) hold(v *Var, e ast.Expr, pos token.Pos) {
	if e == nil {
		return
	}

	if _, ok := borrowIn(v.typ); ok {
		if tmp := c.borrowOfTemporary(e); tmp != nil {
			c.errorf(pos, "cannot keep a borrow of a temporary: "+exprText(tmp)+" is dropped when the statement ends")
			return
		}
	}

	if id, ok := unparen(e).(*ast.Ident); ok && isMut(v.typ) {
		if p, kind := c.placeOf(id); kind == placeOwned {
			c.transfer(p.v, v, pos) // q := p moves a mut borrow: q holds what p held
			return
		}
	}

	if id, ok := unparen(e).(*ast.Ident); ok && isSignature(v.typ) {
		if p, kind := c.placeOf(id); kind == placeOwned {
			for i := range c.borrows { // h := g: h holds what the closure g holds
				if b := c.borrows[i]; b.holder == p.v && b.pos < pos && (b.end == token.NoPos || b.end > pos) {
					c.borrows = append(c.borrows, borrow{holder: v, place: b.place, mut: b.mut, pos: pos})
				}
			}

			return
		}
	}

	if caps := c.capturesOf(e); caps != nil {
		for _, cap := range caps { // g := func() { n += 1 }: g holds n
			if cap.mode != byMove { // the closure owns what it moved; the variable is dead outside
				c.borrows = append(c.borrows, cap.borrow(v, pos))
			}
		}

		return
	}

	if lit, ok := unparen(e).(*ast.CompositeLit); ok {
		for _, elt := range lit.Elts { // l := Lexer{src: ref s}: l holds s
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				elt = kv.Value
			}

			c.derive(v, c.borrowSources(elt), pos)
		}

		return
	}

	if call, ok := unparen(e).(*ast.CallExpr); ok {
		if _, ok := c.calleeOf(call).(*Variant); ok {
			for _, arg := range call.Args { // p := Pair(ref a, ref b): p holds both
				c.derive(v, c.borrowSources(arg), pos)
			}

			return
		}
	}

	c.derive(v, c.borrowSources(e), pos)
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

// derive records that v holds a borrow of the place src, when v's type
// carries a borrow and src names a place.
func (c *Checker) derive(v *Var, srcs []ast.Expr, pos token.Pos) {
	mut, ok := borrowIn(v.typ)
	if !ok {
		return
	}

	for _, src := range srcs {
		if p, kind := through(c.placeOf(src)); kind != notPlace {
			c.borrows = append(c.borrows, borrow{holder: v, place: p, mut: mut, pos: pos})
		}
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

// transfer moves the borrows from holds to to: the old holder is dead
// after the move, and the new one is used in its place.
func (c *Checker) transfer(from, to *Var, pos token.Pos) {
	n := len(c.borrows)
	for i := range c.borrows[:n] {
		if b := &c.borrows[i]; b.holder == from && b.end == token.NoPos {
			b.end, b.last = pos, from.lastUse
			c.borrows = append(c.borrows, borrow{holder: to, place: b.place, mut: b.mut, pos: pos})
		}
	}
}

// holdPattern records the bindings of a pattern matched through a
// borrow of the place src as holders of that borrow, in the order they
// are written.
func (c *Checker) holdPattern(p ast.Pattern, srcs []ast.Expr) {
	switch p := p.(type) {
	case *ast.IdentPat:
		if v, ok := c.scope.Lookup(p.Name.Name).(*Var); ok {
			c.derive(v, srcs, p.Name.Pos())
		}
	case *ast.TuplePat:
		for _, elt := range p.Elts {
			c.holdPattern(elt, srcs)
		}
	case *ast.VariantPat:
		for _, arg := range p.Args {
			c.holdPattern(arg, srcs)
		}
	case *ast.OrPat:
		for _, alt := range p.Alts {
			c.holdPattern(alt, srcs)
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
		c.holdPattern(p, c.borrowSources(x))
		return
	}

	if b.mut {
		if ix, ok := x.(*ast.IndexExpr); ok && isMap(stripRef(c.placeType(ix.X))) {
			c.errorf(x.Pos(), "cannot bind mut through "+exprText(x)+": a key the map does not hold reads the zero value; use "+exprText(ix.X)+".GetMut("+exprText(ix.Index)+")")
			return
		}

		c.accessExpr(x, mutAccess, x.Pos())
	}

	c.holdPattern(p, c.borrowSources(x))
}

// rebind ends the borrows v holds: an assignment to a borrow variable
// points it somewhere else.
func (c *Checker) rebind(v *Var, pos token.Pos) {
	for i := range c.borrows {
		if b := &c.borrows[i]; b.holder == v && b.end == token.NoPos {
			b.end, b.last = pos, v.lastUse
		}
	}
}

// borrowSources finds the place expressions a borrow-valued expression
// lends from: the operand of ref or mut, the receiver of a method or
// the argument a function's result borrows (callSource), the operand
// of a slice expression, or the place itself when a borrow is read out
// of a variable, field or element. A value chosen at run time lends
// from every place it may come from: both sides of ??, and each branch
// of an if or match expression. None when there is none.
func (c *Checker) borrowSources(e ast.Expr) []ast.Expr {
	if vs, ok := branches(e); ok {
		var srcs []ast.Expr
		for _, v := range vs {
			srcs = append(srcs, c.borrowSources(v)...)
		}

		return srcs
	}

	switch e := e.(type) {
	case *ast.ParenExpr:
		return c.borrowSources(e.X)
	case *ast.UnaryExpr:
		if e.Op == token.REF || e.Op == token.MUT {
			return []ast.Expr{e.X}
		}
	case *ast.TryExpr:
		return c.borrowSources(e.X)
	case *ast.SliceExpr:
		return []ast.Expr{e.X}
	case *ast.CallExpr:
		if src := c.callSource(e); src != nil {
			return c.borrowSources(src)
		}
	case *ast.Ident, *ast.SelectorExpr, *ast.IndexExpr:
		return []ast.Expr{e}
	}

	return nil
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

// resolveBorrows reports the conflicts of the function just checked
// whose borrow was live at the access, and clears the pass for the next
// function.
func (c *Checker) resolveBorrows() {
	for _, k := range c.conflicts {
		if k.b < 0 {
			c.errorf(k.pos, k.msg)
			continue
		}

		if use, next, ok := c.liveAt(&c.borrows[k.b], k.at, k.loops); ok {
			msg := k.msg
			switch {
			case next:
				msg += ", used in the next pass of the loop at " + c.lineCol(use)
			case use != token.NoPos:
				msg += ", used at " + c.lineCol(use)
			}

			c.errorf(k.pos, msg)
		}
	}

	c.borrows, c.temps, c.conflicts = c.borrows[:0], c.temps[:0], c.conflicts[:0]
}

// liveAt reports whether b is live at pos: a range loop's borrow lives
// for its body; a held borrow lives while its holder is used after pos,
// or inside a loop around pos that began after the borrow, since the
// next pass reaches that use again, or while a borrow derived from it
// is live. The use that keeps it live is returned for the message, and
// whether it is reached by the next pass of a loop.
func (c *Checker) liveAt(b *borrow, pos token.Pos, loops []token.Pos) (use token.Pos, next, live bool) {
	if b.until != token.NoPos {
		return token.NoPos, false, pos < b.until
	}

	if b.end != token.NoPos && b.end <= pos {
		return token.NoPos, false, false
	}

	last := b.last
	if b.end == token.NoPos {
		last = b.holder.lastUse
	}

	if last > pos {
		return last, false, true
	}

	for _, l := range loops {
		if l > b.pos && last > l {
			return last, true, true
		}
	}

	for i := range c.borrows {
		d := &c.borrows[i]
		if d.v != b.holder || d.pos <= b.pos || d.pos >= pos || b.end != token.NoPos && d.pos >= b.end {
			continue
		}

		if use, next, ok := c.liveAt(d, pos, loops); ok {
			return use, next, true
		}
	}

	return token.NoPos, false, false
}

// throughWhat names what a ref borrow was reached through, for a
// message: the method's receiver, or a borrow.
func (c *Checker) throughWhat(e ast.Expr) string {
	if p, kind := c.placeOf(e); kind != notPlace && c.recv != nil && c.recv == p.v {
		return "receiver"
	}

	return "borrow"
}
