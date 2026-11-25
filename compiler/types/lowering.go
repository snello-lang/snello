// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"sync"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// The lowering writes a typed body as its form: the blocks and
// instructions of form.go, in the order the program evaluates them. It
// runs once a body is typed, reading the types and objects the walk
// recorded, and decides for each use of a place what the program does
// with its value: a Copy value is read, an owned value given where a
// borrow is wanted is lent, any other is moved. A statement's
// temporaries that no place took are dropped where the statement ends;
// a scope's variables where it ends, the last declared first; a path
// that leaves drops what it leaves.

// noBlock is the block of code control cannot reach: nothing is written
// to it.
const noBlock = -1

// lowerings keeps the scratch of the lowering for reuse across bodies
// and checkers, as the flow graphs are kept: its lists grow to the
// largest body once.
var lowerings = sync.Pool{New: func() any { return &lowering{} }}

// A lowering is the state of lowering one body.
type lowering struct {
	c         *Checker
	f         *Form       // the form being written: the scratch's, or a kept body's own
	cur       int         // the block being written, or noBlock
	scopes    []int       // the open scopes, the function's first: where each one's variables begin on declared
	loops     []lowLoop   // the open loops, the innermost last
	guard     *Guard      // the match guard whose condition is being lowered, which every block made meanwhile belongs to; nil otherwise
	owned     []ownedTemp // the owned temporaries the statements being lowered made, dropped at each one's end unless taken; the current statement's from ownedMark
	ownedMark int         // where the owned temporaries of the statement or arm being lowered begin; those before it belong to the statements and expressions around it
	taken     []bool      // each temporary, whether a place or a call took it
	bases     []reached   // the places reached through a call within the statement, reached once
	params    int         // how many of the form's variables are the receiver and the parameters, the first ones
	results   Type
	spawn     token.Pos // the spawn a closure being lowered is the call of, where its captures move; none otherwise

	declared     []int     // the variables the open scopes declared, in order, each scope a range of it
	dropsScratch []Operand // what drops builds its list in, copied into the form by the leave
	argStack     []Operand // what the operands of a call, a literal, a closure or a statement's values are gathered on, inner lists above outer, copied into the form once gathered
	inner        *lowering // the scratch of the closures written in this body, one depth down
}

// An ownedTemp is a temporary a statement owns, and how many scopes
// were open when it was made: a path that leaves those scopes drops it.
type ownedTemp struct {
	temp  int
	scope int
}

// A reached is a value a place was reached through within a statement,
// a call's result, and the operand holding it.
type reached struct {
	e ast.Expr
	o Operand
}

// A lowLoop is an open loop: its head, the block a continue goes to,
// the block a break goes to, and how many scopes were open outside it.
type lowLoop struct {
	head, cont, exit int
	scopes           int
}

// lowerBody lowers a typed body to its form and records it when the
// caller keeps forms; otherwise the form is the checker's scratch,
// reused for the next body once the passes have read it.
func (c *Checker) lowerBody(sig *Signature, body *ast.BlockStmt) *Form {
	l := c.lowering
	if l == nil {
		l = lowerings.Get().(*lowering)
		l.c = c
		c.lowering = l
	}

	if l.f == nil {
		l.f = &Form{}
	} else {
		l.f.Vars, l.f.Blocks, l.f.Instrs, l.f.Temps = l.f.Vars[:0], l.f.Blocks[:0], l.f.Instrs[:0], l.f.Temps[:0]
		l.f.operands, l.f.succs, l.f.closures = l.f.operands[:0], l.f.succs[:0], l.f.closures[:0]
	}

	l.taken, l.bases, l.declared = l.taken[:0], l.bases[:0], l.declared[:0]
	l.scopes, l.loops, l.owned, l.ownedMark = l.scopes[:0], l.loops[:0], l.owned[:0], 0
	l.results, l.spawn, l.guard = sig.results, token.NoPos, nil
	wasPass := c.formPass
	c.formPass = true // the names the lowering resolves through the recorders are the typing's: the scopes are closed
	defer func() { c.formPass = wasPass }()
	l.cur = l.newBlock()
	l.openScope()
	if sig.recv != nil {
		l.declare(sig.recv)
	}

	for _, p := range sig.params {
		l.declare(p)
	}

	l.params = len(l.f.Vars)
	l.stmts(body.List)
	if l.cur != noBlock { // the body ends by falling off: a function with no results returns here
		l.leave(0, body.Rbrace, nil)
	}

	l.closeScope(body.Rbrace)
	l.endBlock()
	f := l.f
	if c.info != nil && c.info.Forms != nil {
		c.info.Forms[body] = f
		l.f = nil // the form kept is its own; the next body gets a new scratch
	}

	return f
}

// freeLowerings gives the checker's lowering scratch, and the scratch of
// every depth of closure under it, back for reuse.
func (c *Checker) freeLowerings() {
	for l := c.lowering; l != nil; {
		inner := l.inner
		l.inner, l.c = nil, nil
		if l.f != nil {
			clear(l.f.Instrs[:cap(l.f.Instrs)]) // what the instructions name, the tree and its types, is let go of now
			clear(l.f.Vars[:cap(l.f.Vars)])
			clear(l.f.Temps[:cap(l.f.Temps)])
		}

		clear(l.bases[:cap(l.bases)])
		lowerings.Put(l)
		l = inner
	}

	c.lowering = nil
}

// Blocks.

// newBlock adds a block, in the innermost loop open and in the guard
// being lowered, when one is.
func (l *lowering) newBlock() int {
	loop := noBlock
	if len(l.loops) > 0 {
		loop = l.loops[len(l.loops)-1].head
	}

	l.f.Blocks = append(l.f.Blocks, FormBlock{From: len(l.f.Instrs), To: len(l.f.Instrs), Loop: loop, Guard: l.guard})
	return len(l.f.Blocks) - 1
}

// start begins writing block b, after ending the one being written; a
// block no edge reaches is code control cannot reach, and nothing is
// written until a block that is reached begins.
func (l *lowering) start(b int) {
	l.endBlock()
	if l.f.Blocks[b].preds == 0 {
		l.cur = noBlock
		return
	}

	l.cur = b
	l.f.Blocks[b].From, l.f.Blocks[b].To = len(l.f.Instrs), len(l.f.Instrs)
}

func (l *lowering) endBlock() {
	if l.cur != noBlock {
		l.f.Blocks[l.cur].To = len(l.f.Instrs)
	}
}

// edge adds an edge from block from to block to: a block's successors
// are kept in the form's one list, and a block's slice of it grows at
// its end when it is the last added, or is copied to the end otherwise.
func (l *lowering) edge(from, to int) {
	if from == noBlock {
		return
	}

	b := &l.f.Blocks[from]
	f := l.f
	f.Blocks[to].preds++
	if n := len(b.Succs); n > 0 && len(f.succs) >= n && &f.succs[len(f.succs)-1] == &b.Succs[n-1] {
		f.succs = append(f.succs, to)
		b.Succs = f.succs[len(f.succs)-n-1 : len(f.succs) : len(f.succs)]
		return
	}

	start := len(f.succs)
	f.succs = append(f.succs, b.Succs...)
	f.succs = append(f.succs, to)
	b.Succs = f.succs[start:len(f.succs):len(f.succs)]
}

// args keeps a list of operands in the form's one list and returns the
// slice of it.
func (l *lowering) args(ops ...Operand) []Operand {
	start := len(l.f.operands)
	l.f.operands = append(l.f.operands, ops...)
	return l.f.operands[start:len(l.f.operands):len(l.f.operands)]
}

// goTo ends the block being written with an edge to b and begins b.
func (l *lowering) goTo(b int) {
	l.edge(l.cur, b)
	l.start(b)
}

// branch ends the block being written with edges to a and b, the two
// ways a condition goes, and begins a.
func (l *lowering) branch(a, b int) {
	l.edge(l.cur, a)
	l.edge(l.cur, b)
	l.start(a)
}

// joinTo ends an arm with an edge to the join, unless the arm left.
func (l *lowering) joinTo(join int) {
	if l.cur != noBlock {
		l.edge(l.cur, join)
		l.endBlock()
		l.cur = noBlock
	}
}

// emit writes an instruction into the block being written; none where
// control cannot reach.
func (l *lowering) emit(in Instr) {
	if l.cur != noBlock {
		l.f.Instrs = append(l.f.Instrs, in)
	}
}

// Scopes and variables.

func (l *lowering) openScope() { l.scopes = append(l.scopes, len(l.declared)) }

// closeScope ends the innermost scope, dropping its variables where
// control reaches the end, the last declared first; where control has
// left, the drops are not written.
func (l *lowering) closeScope(pos token.Pos) { l.closeScopeAs(pos, dropBlock) }

// closePass ends a loop's scope at the end of a pass, where the loop's
// variables die.
func (l *lowering) closePass(pos token.Pos) { l.closeScopeAs(pos, dropPass) }

func (l *lowering) closeScopeAs(pos token.Pos, why dropWhy) {
	l.dropScope(pos, why)
	l.declared = l.declared[:l.scopes[len(l.scopes)-1]]
	l.scopes = l.scopes[:len(l.scopes)-1]
}

// dropScope drops the innermost scope's variables at pos, the last
// declared first, leaving the scope open: a path out of the scope
// drops them where it leaves.
func (l *lowering) dropScope(pos token.Pos, why dropWhy) {
	from := l.scopes[len(l.scopes)-1]
	for i := len(l.declared) - 1; i >= from; i-- {
		l.drop(l.declared[i], pos, why)
	}
}

// scopeVars returns the variables the scope s declared, in order.
func (l *lowering) scopeVars(s int) []int {
	to := len(l.declared)
	if s+1 < len(l.scopes) {
		to = l.scopes[s+1]
	}

	return l.declared[l.scopes[s]:to]
}

func (l *lowering) declare(v *Var) int {
	i := l.index(v)
	l.declared = append(l.declared, i)
	return i
}

// index returns the variable's index in the form, adding it when it is
// new: a body's variables are few, so the list is searched.
func (l *lowering) index(v *Var) int {
	for i, w := range l.f.Vars {
		if w == v {
			return i
		}
	}

	l.f.Vars = append(l.f.Vars, v)
	return len(l.f.Vars) - 1
}

// named returns the variable a name declares or refers to, nil for a
// blank name or one that is neither.
func (l *lowering) named(id *ast.Ident) *Var {
	if id == nil || isBlank(id) {
		return nil
	}

	if v, ok := l.c.info.Defs[id].(*Var); ok {
		return v
	}

	v, _ := l.c.info.Uses[id].(*Var)
	return v
}

// declareStore writes the value o into the variable v, declared now.
func (l *lowering) declareStore(v *Var, o Operand, e ast.Expr) {
	l.storeAs(Operand{Var: l.declare(v)}, o, e, true)
}

// storeName writes the value o into the name id, declared by the
// statement when define says so, or assigned.
func (l *lowering) storeName(id *ast.Ident, define bool, o Operand) {
	if v, ok := l.c.info.Defs[id].(*Var); ok && define {
		l.declareStore(v, o, id)
	} else if v, ok := l.c.info.Uses[id].(*Var); ok {
		l.store(Operand{Var: l.index(v)}, o, id)
	}
}

// bound marks the owned temporary a part of o is bound from, when o is
// a part of a temporary or of a borrow of one: a binding or a loop
// variable holds the temporary until its statement drops it.
func (l *lowering) bound(o Operand) {
	if o.IsValue() && o.Path != "" {
		l.f.Temps[l.f.Temps[o.Temp].Origin].Bound = true
	}
}

// given marks the temporary o as taken by a call, a defer or a statement
// with several values: what it lends is lent until then.
func (l *lowering) given(o Operand) {
	if o.IsValue() {
		l.f.Temps[o.Temp].Given = true
	}
}

// derives marks the temporary t as the temporary from is, or is a part
// of: a borrow of a part of a temporary, or of a value made here, and a
// call's result that borrows from one are of that temporary.
func (l *lowering) derives(t, from Operand) {
	if from.IsValue() {
		l.f.Temps[t.Temp].Origin = l.f.Temps[from.Temp].Origin
	}
}

// isBorrowTemp reports whether o is a borrow made here, into a
// temporary.
func (l *lowering) isBorrowTemp(o Operand) bool {
	return o.IsValue() && isBorrow(l.f.Temps[o.Temp].Type)
}

// drop drops the variable at index i, unless it is a mut parameter,
// which is the caller's.
func (l *lowering) drop(i int, pos token.Pos, why dropWhy) {
	if l.droppable(i) {
		l.emit(Instr{Op: OpDrop, Place: Operand{Var: i}, Pos: pos, why: why})
	}
}

// droppable reports whether the variable at index i is the body's to
// drop: every one but a mut parameter, whose value is the caller's.
func (l *lowering) droppable(i int) bool {
	return !(i < l.params && isMut(l.f.Vars[i].typ))
}

// drops lists what a path leaving the scopes above floor drops, the
// innermost scope's variables first, the last declared first.
func (l *lowering) drops(floor int) []Operand {
	out := l.dropsScratch[:0]
	for s := len(l.scopes) - 1; s >= floor; s-- {
		vars := l.scopeVars(s)
		for i := len(vars) - 1; i >= 0; i-- {
			if l.droppable(vars[i]) {
				out = append(out, Operand{Var: vars[i]})
			}
		}
	}

	l.dropsScratch = out
	return out
}

// leave writes the leaving of the scopes above floor at pos, dropping
// the temporaries made in them and what the scopes hold, with an edge
// to b when the path goes on there, a break or a continue, and none for
// a return; control is gone from the block after it.
func (l *lowering) leave(floor int, pos token.Pos, e ast.Expr, to ...int) {
	why := dropExit
	if len(to) > 0 {
		why = dropLeave
	}

	l.dropOwnedLeaving(floor, pos)
	l.emit(Instr{Op: OpLeave, Args: l.args(l.drops(floor)...), Expr: e, Pos: pos, why: why})
	for _, b := range to {
		l.edge(l.cur, b)
	}

	l.endBlock()
	l.cur = noBlock
}

// Temporaries.

func (l *lowering) temp(t Type, owned bool, e ast.Expr) Operand {
	n := len(l.f.Temps)
	l.f.Temps = append(l.f.Temps, TempInfo{Expr: e, Type: t, Origin: n})
	l.taken = append(l.taken, false)
	if owned {
		l.own(Temp(n))
	}

	return Temp(n)
}

// own makes the statement own the temporary o, dropped at the
// statement's end unless taken, when its type is not Copy, or holds a
// borrow made here, which the statement lends until its end: its drop
// is where the loan ends. The result of an if, a match, a ?? or a ? is
// owned once its arms are written, so that an arm's own statements do
// not drop it.
func (l *lowering) own(o Operand) {
	if t := l.f.Temps[o.Temp].Type; t != nil && (!IsCopy(t) || holdsBorrow(t)) {
		l.owned = append(l.owned, ownedTemp{o.Temp, len(l.scopes)})
	}
}

// take marks a temporary as taken by a place, a call or a value made
// from it, so that the statement's end drops it no more. A part of a
// temporary taken leaves the temporary to be dropped: a Copy part is
// read, and no other may be moved out of it.
func (l *lowering) take(o Operand) {
	if o.IsValue() && o.Path == "" {
		l.taken[o.Temp] = true
	}
}

// nest begins the owned temporaries of a statement, or of the arms of
// an expression, apart from those of what is around it, so that their
// end drops their own alone, and returns what ends the nesting.
func (l *lowering) nest() func() {
	mark := l.ownedMark
	l.ownedMark = len(l.owned)
	return func() { l.ownedMark = mark }
}

// dropOwned drops the owned temporaries of the statement no place
// took, at pos, where the statement ends, and begins the next
// statement's.
func (l *lowering) dropOwned(pos token.Pos) { l.dropOwnedAs(pos, dropStatement) }

func (l *lowering) dropOwnedAs(pos token.Pos, why dropWhy) {
	for i := len(l.owned) - 1; i >= l.ownedMark; i-- {
		if o := l.owned[i]; !l.taken[o.temp] {
			l.emit(Instr{Op: OpDrop, Place: Temp(o.temp), Pos: pos, why: why})
		}
	}

	l.owned = l.owned[:l.ownedMark]
}

// dropOwnedLeaving drops, at pos, every owned temporary made in the
// scopes from floor up, those of the statements and expressions around
// the one leaving included, as a leave of those scopes abandons them;
// the list keeps them for the paths that go on.
func (l *lowering) dropOwnedLeaving(floor int, pos token.Pos) {
	for i := len(l.owned) - 1; i >= 0; i-- {
		if o := l.owned[i]; o.scope >= floor && !l.taken[o.temp] {
			l.emit(Instr{Op: OpDrop, Place: Temp(o.temp), Pos: pos, why: dropStatement})
		}
	}

	l.owned = l.owned[:l.ownedMark]
}
