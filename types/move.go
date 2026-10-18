// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/token"
)

// The move pass (task 1.6 of stage 1) runs inside the checker's walk of
// a body. A value goes to a new owner when it is assigned, passed by
// value, returned, sent, put into a literal or captured by a closure
// that consumes it; after that the old name is dead. The pass keeps the
// list of places moved on the path being checked, forks the list at a
// branch and joins the branches' lists after, and keeps a loop's list to
// report a value moved on one pass and reached again on the next. A
// Copy value is never moved. See _docs/book/05-ownership.md for the
// rules as the reader sees them.

// A place is a variable, or a field path inside one: n, n.value.
type place struct {
	v    *Var
	path string // the field names after the variable, joined by dots
}

// A placeKind says how a place is reached: owned, through a borrow, or
// through an index, which lends the element.
type placeKind int

const (
	notPlace placeKind = iota
	placeOwned
	placeBorrowed
	placeIndexed
)

// A move records a place that was moved and where.
type move struct {
	place
	pos token.Pos
}

// A region is a loop, switch or select open at the current point, with
// the moves on the paths that leave it. A loop's end paths are those
// that reach its next iteration.
type region struct {
	loop  bool
	pos   token.Pos
	mark  int // len(moved) at entry
	end   []move
	exits []move
}

// A fork remembers the state at a branch point, so that every branch
// starts from it and their results are joined after.
type fork struct {
	mark     int
	dead     bool
	moves    []move // the moves of the branches that do not leave
	all      bool   // whether every branch so far leaves
	branches int
}

func (p place) text() string {
	if p.path == "" {
		return p.v.name
	}

	return p.v.name + "." + p.path
}

// within reports whether p is q or lies inside q.
func (p place) within(q place) bool {
	return p.v == q.v && (q.path == "" || p.path == q.path || len(p.path) > len(q.path) && p.path[:len(q.path)] == q.path && p.path[len(q.path)] == '.')
}

// placeOf resolves an expression to the place it denotes and how it is
// reached. Names are looked up again rather than carried on operands,
// so that nothing is allocated per expression on the way.
func (c *Checker) placeOf(e ast.Expr) (place, placeKind) {
	switch e := e.(type) {
	case *ast.Ident:
		_, obj := c.scope.LookupParent(e.Name)
		v, ok := obj.(*Var)
		if !ok || v.field || v.pos == token.NoPos {
			return place{}, notPlace
		}

		return place{v, ""}, placeOwned // the variable itself, even when its value is a borrow
	case *ast.ParenExpr:
		return c.placeOf(e.X)
	case *ast.UnaryExpr:
		if e.Op == token.MUL {
			return through(c.placeOf(e.X))
		}
	case *ast.SelectorExpr:
		p, kind := through(c.placeOf(e.X))
		if kind == placeOwned || kind == placeBorrowed {
			if p.path == "" {
				p.path = e.Sel.Name
			} else {
				p.path += "." + e.Sel.Name
			}
		}

		return p, kind
	case *ast.IndexExpr:
		p, kind := c.placeOf(e.X)
		if kind == notPlace {
			return p, notPlace
		}

		return p, placeIndexed
	}

	return place{}, notPlace
}

// through says how what a place holds is reached: through a borrow
// when the place is a variable holding one.
func through(p place, kind placeKind) (place, placeKind) {
	if kind == placeOwned && p.path == "" {
		if _, ok := p.v.typ.(*Ref); ok {
			return p, placeBorrowed
		}
	}

	return p, kind
}

// checkUse reports a use of a place that was moved, wholly or in part,
// on the current path. An assignment target is not a use of what it
// holds: a whole variable may be given a new value, and so may a moved
// field, but a field of a moved value may not.
func (c *Checker) checkUse(e ast.Expr) {
	if len(c.moved) == 0 || c.inner {
		return
	}

	p, kind := c.placeOf(e)
	if kind == notPlace || c.noUse && kind == placeOwned && p.path == "" {
		return
	}

	for _, m := range c.moved {
		switch {
		case c.noUse && kind == placeOwned && m.place == p:
			return // the moved field is given a new value
		case p.within(m.place) || kind == placeIndexed && m.v == p.v:
			c.errorf(e.Pos(), "use of moved value "+m.text()+"; moved at "+c.lineCol(m.pos))
			return
		case m.within(p) && !c.noUse:
			c.errorf(e.Pos(), "use of partially moved value "+p.text()+"; "+m.text()+" moved at "+c.lineCol(m.pos))
			return
		}
	}
}

// isPlaceChain reports whether e reaches a place through selectors,
// indexes, parentheses and dereferences only, so that a selector on it
// checks the whole chain as one use.
func isPlaceChain(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.Ident:
		return true
	case *ast.ParenExpr:
		return isPlaceChain(e.X)
	case *ast.SelectorExpr:
		return isPlaceChain(e.X)
	case *ast.IndexExpr:
		return isPlaceChain(e.X)
	case *ast.UnaryExpr:
		return e.Op == token.MUL && isPlaceChain(e.X)
	}

	return false
}

// operandOf evaluates an expression that is read for its value inside a
// place, such as an index, with the flags of the place around it off.
func (c *Checker) operandOf(e ast.Expr, hint Type) operand {
	inner, noUse := c.inner, c.noUse
	c.inner, c.noUse = false, false
	op := c.expr(e, hint)
	c.inner, c.noUse = inner, noUse
	return op
}

// maybeMove records the move of an operand consumed as a value of type
// t: a place of a non-Copy type, when t is not a borrow and not any,
// which lend instead. A mut borrow held in a variable is itself not
// Copy, so binding it to another name moves it; passing it to a call
// lends it again, which the caller handles.
func (c *Checker) maybeMove(op operand, t Type) {
	if op.expr == nil || op.mode != variable && op.mode != borrowed && op.mode != mutBorrowed {
		return
	}

	if _, ok := t.(*Ref); ok && !isMut(op.typ) || t == nil || isAny(t) || IsCopy(readType(op)) {
		return
	}

	c.move(op.expr)
}

func isMut(t Type) bool {
	r, ok := t.(*Ref)
	return ok && r.mut
}

// move records that the place e denotes is moved, or reports why it
// cannot be: nothing moves out through a borrow, and an element is lent
// by its index, never moved.
func (c *Checker) move(e ast.Expr) {
	p, kind := c.placeOf(e)
	switch kind {
	case placeOwned:
		c.moved = append(c.moved, move{p, e.Pos()})
	case placeBorrowed:
		c.errorf(e.Pos(), "cannot move "+p.text()+" out of a borrow")
	case placeIndexed:
		c.errorf(e.Pos(), "cannot move "+exprText(e)+" out of the "+c.containerName(e))
	}
}

// moveBefore records a move that belongs before a set of branches, when
// it is only known inside one of them: the move goes under the fork,
// so that every branch and the join see it.
func (c *Checker) moveBefore(e ast.Expr, f *fork) {
	n := len(c.moved)
	c.move(e)
	if len(c.moved) == n {
		return // not a place, or reported
	}

	m := c.moved[n]
	copy(c.moved[f.mark+1:], c.moved[f.mark:n])
	c.moved[f.mark] = m
	f.mark++
}

// revive makes the place e denotes owned again: an assignment to it
// ends any move of it or of a part of it.
func (c *Checker) revive(e ast.Expr) {
	p, kind := c.placeOf(e)
	if kind != placeOwned || len(c.moved) == 0 {
		return
	}

	kept := c.moved[:0]
	for _, m := range c.moved {
		if !m.within(p) {
			kept = append(kept, m)
		}
	}

	c.moved = kept
}

// containerName names what an index reaches into, for a message.
func (c *Checker) containerName(e ast.Expr) string {
	for {
		switch x := e.(type) {
		case *ast.IndexExpr:
			if _, isMap := c.placeType(x.X).Underlying().(*Map); isMap {
				return "map"
			}

			return "slice"
		case *ast.SelectorExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		default:
			return "slice"
		}
	}
}

// placeType is the type of the value a place expression reaches, found
// through the variable's type and the fields' types.
func (c *Checker) placeType(e ast.Expr) Type {
	switch e := e.(type) {
	case *ast.Ident:
		if _, obj := c.scope.LookupParent(e.Name); obj != nil && obj.Type() != nil {
			return obj.Type()
		}
	case *ast.ParenExpr:
		return c.placeType(e.X)
	case *ast.UnaryExpr:
		return stripRef(c.placeType(e.X))
	case *ast.SelectorExpr:
		if st, ok := stripRef(c.placeType(e.X)).Underlying().(*Struct); ok {
			if f := fieldOf(st, e.Sel.Name); f != nil {
				return f.typ
			}
		}
	case *ast.IndexExpr:
		switch t := stripRef(c.placeType(e.X)).Underlying().(type) {
		case *Slice:
			return t.elem
		case *Map:
			return t.value
		}
	}

	return Typ[Invalid]
}

// stripRef is the type behind any borrows and placeOwned pointers.
func stripRef(t Type) Type {
	for {
		switch u := t.(type) {
		case *Ref:
			t = u.elem
		case *Pointer:
			t = u.elem
		default:
			return t
		}
	}
}

// exprText writes a place expression as the source would, for a
// message: xs[0], n.value.
func exprText(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.BasicLit:
		return e.Value
	case *ast.ParenExpr:
		return "(" + exprText(e.X) + ")"
	case *ast.SelectorExpr:
		return exprText(e.X) + "." + e.Sel.Name
	case *ast.IndexExpr:
		return exprText(e.X) + "[" + exprText(e.Index) + "]"
	case *ast.UnaryExpr:
		return e.Op.String() + exprText(e.X)
	}

	return "..."
}

// forkAt starts a set of branches from the current state.
func (c *Checker) forkAt() fork {
	return fork{mark: len(c.moved), dead: c.dead, all: true}
}

// branchEnd ends one branch: its moves count after the join unless it
// left, and the state goes back to the fork for the next branch.
func (c *Checker) branchEnd(f *fork) {
	if !c.dead {
		f.moves = append(f.moves, c.moved[f.mark:]...)
		f.all = false
	}

	f.branches++
	c.moved = c.moved[:f.mark]
	c.dead = f.dead
}

// joinAt applies the branches' moves after the join. complete says
// whether the branches cover every path; otherwise a path with no
// moves continues as well.
func (c *Checker) joinAt(f *fork, complete bool) {
	c.moved = append(c.moved, f.moves...)
	c.dead = f.dead || complete && f.all && f.branches > 0
}

// enterRegion opens a loop, switch or select, and leaveRegion closes it,
// applying the moves of the paths that left it by break. For a loop,
// the moves on the paths reaching the next iteration are checked: a
// value declared before the loop and moved on such a path is gone when
// the loop comes round. The point after a region is reachable; the
// caller's statement result says when it is not.
func (c *Checker) enterRegion(loop bool, pos token.Pos) {
	c.regions = append(c.regions, region{loop: loop, pos: pos, mark: len(c.moved)})
}

func (c *Checker) leaveRegion() {
	r := c.regions[len(c.regions)-1]
	c.regions = c.regions[:len(c.regions)-1]
	if !c.dead {
		r.end = append(r.end, c.moved[r.mark:]...)
	}

	c.moved = c.moved[:r.mark]
	if r.loop {
		for _, m := range r.end {
			if m.v.pos < r.pos {
				c.errorf(m.pos, m.text()+" moved in a previous iteration of the loop")
			}
		}
	}

	c.moved = append(c.moved, r.end...)
	c.moved = append(c.moved, r.exits...)
	c.dead = false
}

// branch records a break or continue: the moves so far go to the
// region it leaves, and the path ends here.
func (c *Checker) branch(tok token.Kind) {
	if c.dead {
		return
	}

	for i := len(c.regions) - 1; i >= 0; i-- {
		r := &c.regions[i]
		if tok == token.CONTINUE && !r.loop {
			continue
		}

		if tok == token.CONTINUE {
			r.end = append(r.end, c.moved[r.mark:]...)
		} else {
			r.exits = append(r.exits, c.moved[r.mark:]...)
		}

		return
	}
}
