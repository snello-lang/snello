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

// The move pass over the form: the places that may be moved at each
// point, a set carried forward through the blocks, joined by union where
// paths meet and run to a fixed point around a loop. A read or a borrow
// of a place in the set, or of a place inside one, is a use of a moved
// value; a store to a place gives it and the places inside it back; a
// move of a place the program may not move out of, a borrow, a
// temporary, an element or a field a Drop reads, is refused where it is
// written. A variable declared before a loop and moved inside it, still
// moved where a pass ends, is moved in the loop's next pass, reported
// once where the pass ends; inside the loop the record that came round
// is not a use again, and after the loop it is.

// compareForms makes the checker run the move pass over the form
// beside the tree's and report every disagreement as an error, for the
// tests that run the corpus with both.
var compareForms bool

// A formMove records a place moved, where, and in which block: a move
// made in a loop's blocks is the loop's own. round is the head of the
// loop the record came round, when it did: inside that loop it is the
// loop rule's, reported once, and no use again.
type formMove struct {
	place Operand
	pos   token.Pos
	block int
	round int
}

// A formError is a message at a position the pass over the form
// reports.
type formError struct {
	pos token.Pos
	msg string
}

// A formMoves is the pass over one form.
type formMoves struct {
	c    *Checker
	f    *Form
	in   [][]formMove // the moved set where each block begins
	errs []formError
}

// CompareForms turns the comparison of the move pass over the form with
// the tree's on or off: while both run, every disagreement is reported
// as an error. The tests of the packages that load the library and the
// examples run them with it on.
func CompareForms(on bool) { compareForms = on }

// formMoves runs the move pass over f and returns what it refuses.
func (c *Checker) formMoves(f *Form) []formError {
	m := &formMoves{c: c, f: f, in: make([][]formMove, len(f.Blocks))}
	queued, visited := make([]bool, len(f.Blocks)), make([]bool, len(f.Blocks))
	queue := []int{0}
	queued[0] = true
	var state []formMove
	for len(queue) > 0 {
		b := queue[0]
		queue = queue[1:]
		queued[b], visited[b] = false, true
		state = append(state[:0], m.in[b]...)
		for _, in := range f.Instrs[f.Blocks[b].From:f.Blocks[b].To] {
			state = m.step(state, in, b)
		}

		for _, s := range f.Blocks[b].Succs {
			next := state
			if g := f.Blocks[b].Guard; g != nil && f.Blocks[s].Guard != g && s != g.Taken { // the guard failed: the moves it made, refused already, follow the arm's path alone, and the arms after are not told of them again
				next = slices.DeleteFunc(slices.Clone(state), func(r formMove) bool { return f.Blocks[r.block].Guard == g })
			}

			if f.Blocks[s].Head && s <= b { // a pass's end: the loop's rule reports what it leaves moved, and the records come round marked
				next = m.roundOf(state, s)
			}

			if grew := m.merge(s, next); (grew || !visited[s]) && !queued[s] { // every block once, and again while its set grows
				queued[s] = true
				queue = append(queue, s)
			}
		}
	}

	for _, cf := range f.closures {
		m.errs = append(m.errs, c.formMoves(cf.form)...)
	}

	return m.errs
}

// roundOf reports, where a pass of the loop whose head is head ends
// with the path's moves in state, each variable declared before the
// loop and moved in it, still moved, which the next pass finds so, and
// returns the records marked as come round the loop, those not marked
// by a loop inside it already.
func (m *formMoves) roundOf(state []formMove, head int) []formMove {
	out := make([]formMove, 0, len(state))
	for _, r := range state {
		if r.round == 0 {
			if v := m.f.Vars[r.place.Var]; v.pos < m.f.Blocks[head].Pos && r.block >= head {
				m.report(r.pos, loopMovedMessage(m.text(r.place)))
			}

			r.round = head + 1
		}

		out = append(out, r)
	}

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
				m.report(in.Pos, guardMovedMessage(m.text(in.Place), g.What))
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
		if r.round != 0 && m.inLoop(b, r.round-1) && r.pos == in.Pos {
			continue // come round the loop this block is in, to the move it came from: the loop's rule reported it, once
		}

		switch {
		case store && r.place == p:
			return
		case within(p, r.place):
			m.report(basePos(e), movedMessage(m.text(r.place), m.c.lineCol(r.pos)))
			return
		case !store && within(r.place, p):
			m.report(basePos(e), partiallyMovedMessage(m.text(p), m.text(r.place), m.c.lineCol(r.pos)))
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
		m.report(e.Pos(), partMovedMessage(e, part))
		return false
	}

	if in.Place.IsTemp() {
		if in.Place.Path == "" {
			return false
		}

		if isBorrow(m.c.info.Types[baseExpr(e)]) {
			m.report(e.Pos(), outOfBorrowMessage(exprText(e)))
		} else {
			m.report(e.Pos(), outOfTemporaryMessage(e))
		}

		return false
	}

	v := m.f.Vars[in.Place.Var]
	switch kind, fields := placeKindOf(v.typ, in.Place.Path); kind {
	case placeBorrowed:
		m.report(e.Pos(), outOfBorrowMessage(exprText(e)))
		return false
	case placeIndexed:
		m.report(e.Pos(), outOfContainerMessage(e, m.c.containerName(e)))
		return false
	default:
		if holder, t := dropHolder(place{v, fields}); holder != "" {
			m.report(e.Pos(), dropHolderMessage(e, holder, t))
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

// report keeps a message at a position, once: two instructions of one
// expression, a receiver read and then lent, say the same thing.
func (m *formMoves) report(pos token.Pos, msg string) {
	if !slices.Contains(m.errs, formError{pos, msg}) {
		m.errs = append(m.errs, formError{pos, msg})
	}
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

// isMoveMessage reports whether a message is the move pass's: one of
// its refusals, but for a move the borrow pass refuses, which names what
// the place is borrowed by.
func isMoveMessage(msg string) bool {
	if strings.Contains(msg, " is borrowed by ") || strings.Contains(msg, " is borrowed at ") {
		return false
	}

	for _, prefix := range []string{"use of moved value ", "use of partially moved value ", "cannot move "} {
		if strings.HasPrefix(msg, prefix) {
			return true
		}
	}

	return strings.HasSuffix(msg, loopMovedMessage(""))
}

// comparePasses reports, when both passes run, each message the passes
// over the form give that the tree's did not, and each of the tree's
// the form's did not, among the errors reported since first: the move
// pass's messages against moves, the borrow pass's against borrows. A
// body that does not type is left alone, since what its moves and
// borrows would be is moot.
func (c *Checker) comparePasses(moves, borrows []formError, first int) bool {
	treeMoves, treeBorrows, treeEscapes := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, e := range c.errors[first:] {
		text := e.Pos.String() + ": " + e.Msg
		switch {
		case isMoveMessage(e.Msg):
			treeMoves[text] = true
		case isEscapeMessage(e.Msg):
			treeEscapes[text] = true
		case isBorrowMessage(e.Msg):
			treeBorrows[text] = true
		case !strings.HasPrefix(e.Msg, "form"):
			return false // a typing error: what the walk did after it says nothing
		}
	}

	var escapes []formError // the borrow pass's graph follows the returned borrows too: its errors are told apart by their text, in place
	kept := borrows[:0]
	for _, e := range borrows {
		if isEscapeMessage(e.msg) {
			escapes = append(escapes, e)
		} else {
			kept = append(kept, e)
		}
	}

	c.comparePass("moves", moves, treeMoves)
	c.comparePass("borrows", kept, treeBorrows)
	c.comparePass("escape", escapes, treeEscapes)
	c.comparePass("captures", c.captureErrs, nil) // what the forms say the closures capture, against the walk's record: every difference is one
	c.captureErrs = c.captureErrs[:0]
	return true
}

// compareCaptures reports, while both run, where the captures a closure's
// form says it makes differ from the walk's record: a variable one has
// and the other not, or captured another way.
func (c *Checker) compareCaptures(lit *ast.FuncLit, form, walk []capture) {
	modes := [...]string{byCopy: "copied", byRef: "borrowed", byMut: "borrowed mut", byMove: "moved"}
	for _, f := range form {
		found := false
		for _, w := range walk {
			if w.v == f.v {
				found = true
				if w.mode != f.mode {
					c.captureErrs = append(c.captureErrs, formError{lit.Pos(), f.v.name + " " + modes[f.mode] + " by the form, " + modes[w.mode] + " by the walk"})
				}
			}
		}

		if !found {
			c.captureErrs = append(c.captureErrs, formError{lit.Pos(), f.v.name + " captured by the form, not by the walk"})
		}
	}

	for _, w := range walk {
		if !slices.ContainsFunc(form, func(f capture) bool { return f.v == w.v }) {
			c.captureErrs = append(c.captureErrs, formError{lit.Pos(), w.v.name + " captured by the walk, not by the form"})
		}
	}
}

// compareMoved reports, while both run, where the places the form moves
// differ from the moves the walk recorded for the emitters: every move
// instruction of the body's form and its closures', the captures' aside,
// is a recorded move, and every recorded move in the body is one.
func (c *Checker) compareMoved(f *Form, body *ast.BlockStmt) {
	moved := map[ast.Expr]bool{}
	var walk func(f *Form)
	walk = func(f *Form) {
		for i := range f.Instrs {
			in := &f.Instrs[i]
			if in.Op != OpMove || in.Capture || in.Place.IsTemp() {
				continue
			}

			if _, part := unparen(in.Expr).(*ast.PartExpr); part {
				continue // refused by both, recorded by neither
			}

			kind, fields := placeKindOf(f.Vars[in.Place.Var].typ, in.Place.Path)
			if holder, _ := dropHolder(place{f.Vars[in.Place.Var], fields}); kind == placeOwned && holder == "" { // a move out of a borrow, a container or a value a Drop reads is refused, and recorded by neither
				moved[in.Expr] = true
				if !c.info.Moves[in.Expr] {
					c.errorf(in.Pos, "form moved: "+exprText(in.Expr)+" moved by the form, not by the walk")
				}
			}
		}

		for _, cf := range f.closures {
			walk(cf.form)
		}
	}

	walk(f)
	for e := range c.info.Moves {
		if e.Pos() >= body.Pos() && e.Pos() <= body.Rbrace && !moved[e] {
			c.errorf(e.Pos(), "form moved: "+exprText(e)+" moved by the walk, not by the form")
		}
	}
}

// isEscapeMessage reports whether a message is the escape pass's: a
// borrow returned that the function's source does not hold, or a
// closure that borrows returned.
func isEscapeMessage(msg string) bool {
	if strings.HasPrefix(msg, "cannot return a borrow") {
		return true
	}

	for _, how := range [...]string{"returned", "stored", "sent", "spawned"} {
		if strings.HasSuffix(msg, " and cannot be "+how) {
			return true
		}
	}

	return false
}

// comparePass reports the differences between what a pass over the
// form gave and what the tree's pass of the same kind did.
func (c *Checker) comparePass(what string, form []formError, tree map[string]bool) {
	given := make(map[string]bool, len(form))
	for _, e := range form {
		text := c.position(e.pos).String() + ": " + e.msg
		given[text] = true
		if !tree[text] {
			c.errorf(e.pos, "form "+what+": "+text+" (the tree's pass did not)")
		}
	}

	for text := range tree {
		if !given[text] {
			c.errorf(token.NoPos, "form "+what+" misses: "+text)
		}
	}
}
