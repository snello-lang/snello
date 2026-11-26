// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"slices"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// setRoom is the records each block's set has room for in the pass's
// pool before it grows on its own: nearly every set is smaller.
const setRoom = 4

// The move pass over the form: the places that may be moved at each
// point, a set carried forward through the blocks, joined by union where
// paths meet and run to a fixed point around a loop. A read or a borrow
// of a place in the set, or of a place inside one, is a use of a moved
// value; a store to a place gives it and the places inside it back; a
// move of a place the program may not move out of, a borrow, a
// temporary, an element or a field a Drop reads, is refused where it is
// written. A variable declared before a loop and moved inside it, still
// moved where a pass ends, is moved in the loop's next pass: a use of it
// there is a use of what a previous pass moved, reported as such; after
// the loop it is a use of a moved value as any other.

// A formMove records a place moved, where, and in which block: a move
// made in a guard's blocks follows the guard's path. round is one more
// than the head of the loop the record came round, when it did: a use
// of the place in that loop is a use of what a previous pass moved.
type formMove struct {
	place Operand
	pos   token.Pos
	block int
	round int
}

// A formMoves is the pass over one form, the checker's scratch for it
// kept across bodies.
type formMoves struct {
	c     *Checker
	f     *Form
	in    [][]formMove // the moved set where each block begins
	dirty []bool       // the blocks whose set grew since they were last stepped through
	state []formMove   // the set being carried through a block
	round []formMove   // the set carried round a loop, its records marked
	pool  []formMove   // where the blocks' sets begin, setRoom records each, one allocation for the form
}

// formMoves runs the move pass over f, the blocks in the order control
// reaches them, each stepped through once and again while the set where
// it begins grows, until none does.
func (c *Checker) formMoves(f *Form) {
	m := &c.moves
	m.c, m.f = c, f
	n := len(f.Blocks)
	m.in, m.dirty = slices.Grow(m.in[:0], n)[:n], slices.Grow(m.dirty[:0], n)[:n]
	m.pool = slices.Grow(m.pool[:0], n*setRoom)[:n*setRoom]
	for b := range m.in {
		m.in[b], m.dirty[b] = m.pool[b*setRoom:b*setRoom:(b+1)*setRoom], true
	}

	for again := true; again; {
		again = false
		for _, b := range f.order {
			if !m.dirty[b] {
				continue
			}

			m.dirty[b] = false
			state := append(m.state[:0], m.in[b]...)
			for _, in := range f.Instrs[f.Blocks[b].From:f.Blocks[b].To] {
				state = m.step(state, in, b)
			}

			m.state = state
			for _, s := range f.Blocks[b].Succs {
				next := state
				if g := f.Blocks[b].Guard; g != nil && f.Blocks[s].Guard != g && s != g.Taken { // the guard failed: the moves it made, refused already, follow the arm's path alone, and the arms after are not told of them again
					next = slices.DeleteFunc(slices.Clone(state), func(r formMove) bool { return f.Blocks[r.block].Guard == g })
				}

				if f.Blocks[s].Head && s <= b { // a pass's end: the records come round marked
					next = m.roundOf(next, s)
				}

				if m.merge(s, next) {
					m.dirty[s] = true
					again = again || s <= b // a block after this one is stepped through in this round
				}
			}
		}
	}

	for _, cf := range f.closures { // the closures written in the body, after it
		c.formMoves(cf.form)
	}
}

// roundOf returns the records of state as they come round the loop
// whose head is head, each marked by the first loop it came round.
func (m *formMoves) roundOf(state []formMove, head int) []formMove {
	out := m.round[:0]
	for _, r := range state {
		if r.round == 0 {
			r.round = head + 1
		}

		out = append(out, r)
	}

	m.round = out
	return out
}

// merge adds the records of state to the set where block b begins, and
// reports whether the set grew.
func (m *formMoves) merge(b int, state []formMove) bool {
	grew := false
	for _, r := range state {
		if !slices.Contains(m.in[b], r) {
			m.in[b] = append(m.in[b], r)
			grew = true
		}
	}

	return grew
}

// step applies one instruction to the moved set.
func (m *formMoves) step(state []formMove, in Instr, b int) []formMove {
	switch in.Op {
	case OpRead, OpBorrow:
		if !in.Capture || !in.Mut { // a mut borrow a closure captures is no use of the value until the closure runs
			m.checkUse(state, in, false, b)
		}
	case OpMove:
		if !m.movable(in) {
			break
		}

		if g := m.f.Blocks[b].Guard; g != nil && !in.Place.IsTemp() { // a guard runs whether or not its arm is taken: what it moves would be gone for the arms after, so the move is refused
			if v := m.f.Vars[in.Place.Var]; !(v.pos >= g.From && v.pos < g.To) {
				m.c.errorf(in.Pos, guardMovedMessage(m.text(in.Place), g.What))
			}
		}

		m.checkUse(state, in, false, b)
		if !in.Place.IsTemp() {
			state = append(state, formMove{place: in.Place, pos: in.Pos, block: b})
		}
	case OpStore:
		m.checkUse(state, in, true, b)
		state = slices.DeleteFunc(state, func(r formMove) bool { return within(r.place, in.Place) })
	}

	return state
}

// checkUse reports a use of a place moved, wholly or in part, on the
// path. A store is not a use of what the place holds: a whole variable
// may be given a new value, and so may a moved field, but a field of a
// moved value may not.
func (m *formMoves) checkUse(state []formMove, in Instr, store bool, b int) {
	p, e := in.Place, in.Expr
	if p.IsTemp() {
		return
	}

	for i := len(state) - 1; i >= 0; i-- { // the latest record of the place decides
		r := state[i]
		switch {
		case store && r.place == p:
			return
		case within(p, r.place) && r.round != 0 && m.inLoop(b, r.round-1):
			m.c.errorf(basePos(e), loopMovedMessage(m.text(r.place))) // moved on a previous pass of the loop this use is in, and not given a value since
			return
		case within(p, r.place):
			m.c.errorf(basePos(e), movedMessage(m.text(r.place), m.c.lineCol(r.pos)))
			return
		case !store && within(r.place, p):
			m.c.errorf(basePos(e), partiallyMovedMessage(m.text(p), m.text(r.place), m.c.lineCol(r.pos)))
			return
		}
	}
}

// movable reports whether the program may move out of the place in
// moves, and reports why not when it may not: a borrow, a temporary, an
// element of a slice or a map, or a field of a value whose Drop reads
// its fields.
func (m *formMoves) movable(in Instr) bool {
	e := in.Expr
	if part, ok := unparen(e).(*ast.PartExpr); ok {
		m.c.errorf(e.Pos(), partMovedMessage(e, part))
		return false
	}

	if in.Place.IsTemp() {
		if in.Place.Path == "" {
			return false
		}

		if isBorrow(m.c.info.Types[baseExpr(e)]) {
			m.c.errorf(e.Pos(), outOfBorrowMessage(exprText(e)))
		} else {
			m.c.errorf(e.Pos(), outOfTemporaryMessage(e))
		}

		return false
	}

	v := m.f.Vars[in.Place.Var]
	switch kind, fields := placeKindOf(v.typ, in.Place.Path); kind {
	case placeBorrowed:
		m.c.errorf(e.Pos(), outOfBorrowMessage(exprText(e)))
		return false
	case placeIndexed:
		m.c.errorf(e.Pos(), outOfContainerMessage(e, m.c.containerName(e)))
		return false
	default:
		if holder, t := dropHolder(place{v, fields}); holder != "" {
			m.c.errorf(e.Pos(), dropHolderMessage(e, holder, t))
			return false
		}
	}

	return true
}

// inLoop reports whether block b is among the blocks of the loop whose
// head is head, itself or a loop inside it.
func (m *formMoves) inLoop(b, head int) bool {
	for l := b; l != noBlock; l = m.f.Blocks[l].Loop { // the block itself, a head among them, and the loops around it
		if l == head {
			return true
		}
	}

	return false
}

// text names a place for a message, the variable and its fields.
func (m *formMoves) text(p Operand) string {
	name := m.f.Vars[p.Var].name
	if fields := fieldPath(p.Path); fields != "" {
		return name + "." + fields
	}

	return name
}

// baseExpr is the expression a place expression is reached from: the
// call in pick(...).a or *mk().
func baseExpr(e ast.Expr) ast.Expr {
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
		case *ast.SelectorExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.PartExpr:
			e = x.X
		case *ast.UnaryExpr:
			if x.Op != token.MUL {
				return e
			}

			e = x.X
		default:
			return e
		}
	}
}

// basePos is the position of the variable a place expression is
// reached from, the n of *n, n.f or mut n, where a use of a moved value
// is reported: the access's position, behind a ref or mut.
func basePos(e ast.Expr) token.Pos {
	if u, ok := unparen(e).(*ast.UnaryExpr); ok && (u.Op == token.REF || u.Op == token.MUT) {
		e = u.X
	}

	return accessPos(e)
}

// stepType is the type one part of a path reaches from a value of type
// t: what a * points at, an element, or a field; t itself for a part it
// does not name, and nil for a type the checker could not give.
func stepType(t Type, part string) Type {
	if t == nil {
		return nil
	}

	switch part {
	case "*":
		switch u := t.(type) {
		case *Ref:
			return u.elem
		case *Pointer:
			return u.elem
		}
	case "[]":
		switch u := t.Underlying().(type) {
		case *Slice:
			return u.elem
		case *Array:
			return u.elem
		case *Map:
			return u.value
		}
	default:
		if st, ok := stripRef(t).Underlying().(*Struct); ok {
			if f := fieldNamed(st.fields, part); f != nil {
				return f.typ
			}
		}
	}

	return t
}

// within reports whether the place p is q or lies inside q.
func within(p, q Operand) bool {
	return p.Var == q.Var && p.Temp == q.Temp && pathWithin(p.Path, q.Path)
}

// placeKindOf walks the path from a variable's type and says how the
// place is reached, owned, through a borrow or through an element, and
// gives the path of fields alone, which a Drop's holder is found by.
func placeKindOf(t Type, path string) (placeKind, string) {
	kind := placeOwned
	var fields strings.Builder
	for path != "" {
		part, rest, _ := strings.Cut(path, ".")
		path = rest
		if t == nil { // a variable whose type the checker could not give
			break
		}

		switch part {
		case "*":
			if _, ok := t.(*Ref); ok {
				kind = placeBorrowed
			}
		case "[]":
			if kind == placeOwned {
				kind = placeIndexed
			}
		default:
			if part[0] == '?' { // the part a pattern binds, no field of the value
				continue
			}

			if fields.Len() > 0 {
				fields.WriteByte('.')
			}

			fields.WriteString(part)
		}

		t = stepType(t, part)
	}

	return kind, fields.String()
}

// fieldPath is the path with its derefs and elements left out: the
// fields, as a message names a place.
func fieldPath(path string) string {
	var fields strings.Builder
	for path != "" {
		part, rest, _ := strings.Cut(path, ".")
		path = rest
		if part == "*" || part == "[]" || part[0] == '?' {
			continue
		}

		if fields.Len() > 0 {
			fields.WriteByte('.')
		}

		fields.WriteString(part)
	}

	return fields.String()
}
