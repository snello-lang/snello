// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"slices"
	"strings"
	"sync"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

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
//
// The sets are bitsets over the form's move sites, the move
// instructions of places that may be moved, numbered in order. Each
// block carries one set of the sites reached on this pass of every loop
// and, per loop, one of the sites that came round it: a site's bit
// moves from the first to the loop's set where the loop's back edge is
// taken, and stays there round the loops outside, so a use inside that
// loop is a use of what a previous pass moved, and a use outside it is
// a use of the move at its position.

// movePasses keeps the scratch of the move pass for reuse across bodies
// and checkers, as the lowering's and the graphs are kept.
var movePasses = sync.Pool{New: func() any { return new(formMoves) }}

// A formMoves is the pass over one form, the checker's scratch for it
// kept across bodies.
type formMoves struct {
	c     *Checker
	f     *Form
	site  []int32 // per instruction, its move site, or -1
	at    []int32 // per site, its instruction
	block []int32 // per site, its block
	loop  []int32 // per block, 1 + the index of the loop it heads, or 0
	loops int     // how many loops the form has
	words int     // the words of one set
	in    bitset  // the sets where each block begins, sets() of them per block, words each
	state bitset  // the sets carried through a block
	next  bitset  // the sets carried along an edge that changes them
	dirty []bool  // the blocks whose sets grew since they were last stepped through
}

// sets is how many sets a block carries: the sites reached on this
// pass, and those come round each loop.
func (m *formMoves) sets() int { return 1 + m.loops }

// formMoves runs the move pass over f, the blocks in the order control
// reaches them, each stepped through once and again while the sets where
// it begins grow, until none do.
func (c *Checker) formMoves(f *Form) {
	m := c.moves
	if m == nil {
		m = movePasses.Get().(*formMoves)
		c.moves = m
	}

	m.c, m.f = c, f
	m.number()
	n := len(f.Blocks)
	size := m.sets() * m.words
	m.in = slices.Grow(m.in[:0], n*size)[:n*size]
	clear(m.in)
	m.state, m.next = slices.Grow(m.state[:0], size)[:size], slices.Grow(m.next[:0], size)[:size]
	m.dirty = slices.Grow(m.dirty[:0], n)[:n]
	for b := range m.dirty {
		m.dirty[b] = true
	}

	for again := true; again; {
		again = false
		for _, b := range f.order {
			if !m.dirty[b] {
				continue
			}

			m.dirty[b] = false
			copy(m.state, m.blockIn(b))
			for i := f.Blocks[b].From; i < f.Blocks[b].To; i++ {
				m.step(i, b)
			}

			for _, s := range f.Blocks[b].Succs {
				next := m.state
				if g := f.Blocks[b].Guard; g != nil && f.Blocks[s].Guard != g && s != g.Taken { // the guard failed: the moves it made, refused already, follow the arm's path alone, and the arms after are not told of them again
					next = m.changed(next)
					m.without(g)
				}

				if f.Blocks[s].Head && s <= b { // a pass's end: the sites reached on it come round the loop
					next = m.changed(next)
					m.round(s)
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

// number gives the form's move sites their numbers, the loops theirs,
// and sizes the sets.
func (m *formMoves) number() {
	f := m.f
	m.site = slices.Grow(m.site[:0], len(f.Instrs))[:len(f.Instrs)]
	m.at, m.block = m.at[:0], m.block[:0]
	for b, blk := range f.Blocks {
		for i := blk.From; i < blk.To; i++ {
			m.site[i] = -1
			if in := &f.Instrs[i]; in.Op == OpMove && !in.Place.IsTemp() {
				m.site[i] = int32(len(m.at))
				m.at, m.block = append(m.at, int32(i)), append(m.block, int32(b))
			}
		}
	}

	m.loop, m.loops = slices.Grow(m.loop[:0], len(f.Blocks))[:len(f.Blocks)], 0
	for b, blk := range f.Blocks {
		m.loop[b] = 0
		if blk.Head {
			m.loops++
			m.loop[b] = int32(m.loops)
		}
	}

	m.words = (len(m.at) + 63) / 64
}

// blockIn is the sets where block b begins.
func (m *formMoves) blockIn(b int) bitset {
	size := m.sets() * m.words
	return m.in[b*size : (b+1)*size]
}

// set is the k-th set of sets: the sites reached on this pass for 0,
// those come round the loop numbered k otherwise.
func (m *formMoves) set(sets bitset, k int) bitset { return sets[k*m.words : (k+1)*m.words] }

// changed returns the sets an edge changes, next: the state copied into
// next when it is not there yet.
func (m *formMoves) changed(sets bitset) bitset {
	if len(sets) == 0 || &sets[0] != &m.next[0] {
		copy(m.next, m.state)
	}

	return m.next
}

// without takes out of next the sites in the blocks of the guard g.
func (m *formMoves) without(g *Guard) {
	for s, b := range m.block {
		if m.f.Blocks[b].Guard == g {
			for k := 0; k < m.sets(); k++ {
				m.set(m.next, k).del(s)
			}
		}
	}
}

// round makes next the sets come round the loop whose head is head: the
// sites reached on this pass are the loop's now, and the sites come
// round a loop inside it stay that loop's.
func (m *formMoves) round(head int) {
	fresh, loop := m.set(m.next, 0), m.set(m.next, int(m.loop[head]))
	loop.union(fresh)
	clear(fresh)
}

// merge adds the sets of sets to those where block b begins, and
// reports whether they grew.
func (m *formMoves) merge(b int, sets bitset) bool { return m.blockIn(b).union(sets) }

// step applies the instruction i of block b to the sets of the state.
func (m *formMoves) step(i, b int) {
	in := &m.f.Instrs[i]
	switch in.Op {
	case OpRead, OpBorrow:
		if !in.Capture || !in.Mut { // a mut borrow a closure captures is no use of the value until the closure runs
			m.checkUse(in, false, b)
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

		m.checkUse(in, false, b)
		if s := m.site[i]; s >= 0 {
			m.set(m.state, 0).set(int(s))
		}
	case OpStore:
		m.checkUse(in, true, b)
		for k := 0; k < m.sets(); k++ { // the place and the places inside it are given back
			set := m.set(m.state, k)
			set.each(func(s int) bool {
				if within(m.f.Instrs[m.at[s]].Place, in.Place) {
					set.del(s)
				}

				return true
			})
		}
	}
}

// checkUse reports a use of a place moved, wholly or in part, on the
// path. A store is not a use of what the place holds: a whole variable
// may be given a new value, and so may a moved field, but a field of a
// moved value may not. A move reached on this pass is reported at its
// position; one come round the loop the use is in is reported as a
// previous pass's.
func (m *formMoves) checkUse(in *Instr, store bool, b int) {
	p, e := in.Place, in.Expr
	if p.IsTemp() {
		return
	}

	for k := 0; k < m.sets(); k++ { // this pass's sites first: a move on the path to the use decides over one come round
		found := false
		m.set(m.state, k).each(func(s int) bool {
			moved := &m.f.Instrs[m.at[s]]
			switch {
			case store && moved.Place == p:
			case within(p, moved.Place) && k > 0 && m.inLoop(b, k):
				m.c.errorf(basePos(e), loopMovedMessage(m.text(moved.Place))) // moved on a previous pass of the loop this use is in, and not given a value since
			case within(p, moved.Place):
				m.c.errorf(basePos(e), movedMessage(m.text(moved.Place), m.c.lineCol(moved.Pos)))
			case !store && within(moved.Place, p):
				m.c.errorf(basePos(e), partiallyMovedMessage(m.text(p), m.text(moved.Place), m.c.lineCol(moved.Pos)))
			default:
				return true
			}

			found = true
			return false
		})

		if found {
			return
		}
	}
}

// movable reports whether the program may move out of the place in
// moves, and reports why not when it may not: a borrow, a temporary, an
// element of a slice or a map, or a field of a value whose Drop reads
// its fields.
func (m *formMoves) movable(in *Instr) bool {
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

// inLoop reports whether block b is among the blocks of the loop
// numbered loop, itself or a loop inside it.
func (m *formMoves) inLoop(b, loop int) bool {
	for l := b; l != noBlock; l = m.f.Blocks[l].Loop { // the block itself, a head among them, and the loops around it
		if int(m.loop[l]) == loop {
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
