// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"math/bits"
	"slices"
	"strings"
	"sync"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// The borrow pass decides where a borrow is live along the paths a
// function can take, not by the order of its lines. As the checker
// walks a function it records what happens in it, in blocks joined by
// the edges control can take: the arms of an if, a match, a ?? or a
// select, a loop's body back to its head and out of it, a break and a
// continue. At the function's end three passes run over the blocks to a
// fixed point, as the liveness pass of the Go compiler runs over its
// SSA blocks: liveness, backwards, finds where a variable may still be
// used before it is given another value or dropped; the loans in
// effect, forwards, find which borrows a variable may hold at each
// point; and the variables dropped, forwards, which may be gone there.
// A loan is live where it is in effect and its holder is live, and
// every access, drop and new loan is checked against what is live where
// it is made. A closure's body has a graph of its own.

// A flowKind says what an event does.
type flowKind uint8

const (
	evUse    flowKind = iota // v, or a part of it, is read
	evLoan                   // the loan i is made
	evCopy                   // the copy i: a variable takes what another holds
	evAssign                 // v, or the part path of it, is given a value
	evAccess                 // the access i is made
	evDrop                   // the drop i: a variable dies
	evReturn                 // the return i lends a borrow its function's source must hold
)

// A flowEvent is one thing a block does, in the order it does them.
type flowEvent struct {
	kind flowKind
	v    *Var
	path string
	i    int
	pos  token.Pos
}

// A flowBlock is a straight run of events and the blocks control may
// go to after it, with what the passes found at its two ends.
type flowBlock struct {
	events  []flowEvent
	succs   []int
	preds   []int
	liveIn  []uint64 // the variables live where the block begins
	liveOut []uint64
	in      []uint64 // the loans in effect where the block begins
	out     []uint64
	gone    []uint64 // the variables that may be dropped where the block begins
	goneOut []uint64
}

// A flowAccess is an access to a place, checked against the loans live
// where it is made.
type flowAccess struct {
	e     ast.Expr
	p     place
	what  access
	pos   token.Pos
	seq   int
	block int  // the block it is made in
	gone  bool // a later access at the same position, in the same block, takes its place
}

// A dropWhy says where a variable is dropped, for the message.
type dropWhy uint8

const (
	dropBlock dropWhy = iota // at the end of the block that declares it
	dropPass                 // at the end of a pass of the loop that declares it
	dropLeave                // where a break or continue leaves its block
	dropExit                 // at the end of the function's body, after its deferred calls run
)

// A flowDrop is a variable dying where its block ends.
type flowDrop struct {
	v    *Var
	pos  token.Pos
	why  dropWhy
	seq  int
	runs bool // the variable's type runs a Drop, which may read what it borrows: the drop is a use of it
}

// A flowCopy is a variable taking every loan another has in effect: q
// := r, h := g, or r = h.src, which takes what h holds in its part
// path, into the part toPath of to. A store through a borrow variable,
// m.src = ref t with m := mut h, is one too, the other way round: each
// place the variable through points to takes what was stored through
// it. pairs holds the loans each new loan comes from, the second -1
// for a plain copy, and the new loan.
type flowCopy struct {
	to, from     *Var
	path, toPath string
	through      bool
	flat         bool // the loans copied all go in the part toPath: a borrow of what a borrow variable points to reaches whatever it holds, in parts its own type does not name
	atExit       bool // the loans copied are a deferred call's, reached when the function returns
	pos          token.Pos
	pairs        [][3]int
}

// A flowReturn is a borrow returned out of v, to be followed through
// the loans in effect at the return to the function's source.
type flowReturn struct {
	v   *Var
	pos token.Pos
	seq int
}

// A flowError is an error found at the function's end, reported in the
// order the walk met what it is about.
type flowError struct {
	seq    int
	pos    token.Pos
	msg    string
	access bool
}

// A flow is the graph of one function's or closure's body.
type flow struct {
	blocks   []flowBlock
	cur      int
	loans    []borrow
	accesses []flowAccess
	drops    []flowDrop
	copies   []flowCopy
	rets     []flowReturn
	errs     []flowError
	deferred []*Var        // the holders of what deferred calls borrow, live until the function returns
	fnScope  *Scope        // the scope of the body's parameters, the outermost a return drops
	outside  map[*Var]*Var // what the caller owns behind each parameter that carries a borrow, and the parameter
	parent   *flow         // for a closure's body, the graph of the body it is written in
	ends     []int         // the ends of the branches of the open forks
	breaks   []int         // the breaks of the open loops
	aside    []flowEvent   // the events of an assignment's target, which take effect after its value
	asiding  int
	outer    token.Pos // for a closure's body, where the closure begins: a variable declared before it may be used after the body
	seq      int
	back     bool         // an edge goes to a block made before its source, a loop: the passes iterate to a fixed point; without one, each block is reached from blocks made before it, and one pass in that order decides
	vars     map[*Var]int // the variables the solver numbers, kept and emptied for each body solved

	// The loans each variable holds, as a chain through the loans: the
	// first and last loan of each variable, by its number, and the loan
	// after each loan with the same holder, -1 at the ends.
	first, last, next []int

	// The solver's memory, kept with the graph: the blocks' sets, the
	// variables live where the body ends, and two sets to work in.
	words    []uint64
	exit     bitset
	scratch  bitset
	scratch2 bitset
	scratch3 bitset
}

// flows keeps the graphs for reuse across bodies and checks, since a
// body's graph is gone once it is solved.
var flows = sync.Pool{New: func() any { return new(flow) }}

// newFlow starts the graph of a body. The graphs are kept for reuse.
func (c *Checker) newFlow(outer token.Pos) *flow {
	f := flows.Get().(*flow)
	f.outer = outer
	f.cur = f.newBlock()
	return f
}

// freeFlow empties a graph and keeps it for the next body.
func (c *Checker) freeFlow(f *flow) {
	f.blocks = f.blocks[:0]
	f.loans, f.accesses, f.drops, f.copies, f.rets, f.errs = f.loans[:0], f.accesses[:0], f.drops[:0], f.copies[:0], f.rets[:0], f.errs[:0]
	f.ends, f.breaks, f.aside, f.asiding, f.seq = f.ends[:0], f.breaks[:0], f.aside[:0], 0, 0
	f.deferred, f.parent, f.fnScope, f.outside, f.back = f.deferred[:0], nil, nil, nil, false
	flows.Put(f)
}

// newBlock adds an empty block reached from preds.
func (f *flow) newBlock(preds ...int) int {
	n := len(f.blocks)
	if n < cap(f.blocks) {
		f.blocks = f.blocks[:n+1]
		b := &f.blocks[n]
		b.events, b.succs, b.preds = b.events[:0], b.succs[:0], b.preds[:0]
	} else {
		f.blocks = append(f.blocks, flowBlock{})
	}

	for _, p := range preds {
		f.edge(p, n)
	}

	return n
}

func (f *flow) edge(from, to int) {
	f.blocks[from].succs = append(f.blocks[from].succs, to)
	f.blocks[to].preds = append(f.blocks[to].preds, from)
	if to <= from {
		f.back = true
	}
}

// add records an event in the current block, or aside while the target
// of an assignment is checked. Nothing is recorded where control cannot
// reach.
func (c *Checker) flowAdd(ev flowEvent) {
	f := c.flow
	if f == nil || c.dead {
		return
	}

	if f.asiding > 0 {
		f.aside = append(f.aside, ev)
		return
	}

	b := &f.blocks[f.cur]
	b.events = append(b.events, ev)
}

// flowUse records a read of v, when v could hold a borrow: a use keeps
// what its variable holds lent, and a variable whose type holds none, a
// string or an int, has nothing to keep.
func (c *Checker) flowUse(v *Var, pos token.Pos) {
	if t := v.typ; t == Typ[Invalid] || holdsBorrow(t) || isSignature(t) {
		c.flowAdd(flowEvent{kind: evUse, v: v, pos: pos})
	}
}

// flowAssign records that v, or the part path of it, is given a value.
func (c *Checker) flowAssign(v *Var, path string) {
	c.flowAdd(flowEvent{kind: evAssign, v: v, path: path})
}

// flowLoan records that b.holder holds a borrow of b.place from here.
// A place that holds borrows itself, a borrow variable or a struct with
// a borrow in it, is reached through: the holder takes what the place
// holds now, as a copy, since that is what it can reach, and what the
// place holds later is not its concern.
func (c *Checker) flowLoan(b borrow) {
	f := c.flow
	if f == nil || c.dead {
		return
	}

	f.loans = append(f.loans, b)
	c.flowAdd(flowEvent{kind: evLoan, i: len(f.loans) - 1})
	switch {
	case isBorrow(b.v.typ):
		c.flowCopyOf(flowCopy{to: b.holder, from: b.v, toPath: b.hpath, flat: true, atExit: b.atExit, pos: b.pos})
	case holdsBorrow(placeType(b.place)) && isBorrow(placeType(place{b.holder, b.hpath})):
		c.flowCopyOf(flowCopy{to: b.holder, from: b.v, path: b.place.path, toPath: joinPath(b.hpath, "*"), atExit: b.atExit, pos: b.pos}) // m := mut h reaches what h holds through m: m.src = ... lets go of it
	case holdsBorrow(placeType(b.place)):
		c.flowCopyOf(flowCopy{to: b.holder, from: b.v, path: b.place.path, toPath: b.hpath, atExit: b.atExit, pos: b.pos})
	}
}

// flowCopy records that the part toPath of to takes every loan the
// part path of from has in effect here, each in the part of to that
// reaches it.
func (c *Checker) flowCopy(to, from *Var, path, toPath string, pos token.Pos) {
	c.flowCopyOf(flowCopy{to: to, from: from, path: path, toPath: toPath, pos: pos})
}

func (c *Checker) flowCopyOf(cp flowCopy) {
	f := c.flow
	if f == nil || c.dead {
		return
	}

	f.copies = append(f.copies, cp)
	c.flowAdd(flowEvent{kind: evCopy, i: len(f.copies) - 1})
}

// flowThrough records a store through a borrow, m itself or its part
// prefix, into the part path of m, which holds a "*" where the borrow is
// passed through: each place the borrow points to takes what was
// stored.
func (c *Checker) flowThrough(m *Var, prefix, path string, pos token.Pos) {
	f := c.flow
	if f == nil || c.dead {
		return
	}

	c.flowCopyOf(flowCopy{to: m, from: m, path: path, toPath: prefix, through: true, pos: pos})
}

// flowDeferred records that what a deferred call borrows is held until
// the function returns, by a holder of its own, which it returns.
func (c *Checker) flowDeferred(pos token.Pos) *Var {
	f := c.flow
	if f == nil {
		return nil
	}

	v := NewVar(pos, "", Typ[Invalid])
	f.deferred = append(f.deferred, v)
	return v
}

// flowAccess records an access to the place p, made by e at pos.
func (c *Checker) flowAccess(e ast.Expr, p place, what access, pos token.Pos) {
	f := c.flow
	if f == nil || c.dead {
		return
	}

	for i := len(f.accesses) - 1; i >= 0 && f.accesses[i].pos >= pos; i-- { // a call's arguments come after its receiver
		if a := &f.accesses[i]; a.pos == pos && a.p.v == p.v && a.block == f.cur {
			a.gone = true // one access seen twice, as a receiver is read before the arguments and lent after them: the later one stands; one per arm of a match is each arm's own
			break
		}
	}

	f.seq++
	f.accesses = append(f.accesses, flowAccess{e: e, p: p, what: what, pos: pos, seq: f.seq, block: f.cur})
	c.flowAdd(flowEvent{kind: evAccess, i: len(f.accesses) - 1})
}

// flowReturn records a borrow returned out of v.
func (c *Checker) flowReturn(v *Var, pos token.Pos) {
	f := c.flow
	if f == nil || c.dead {
		return
	}

	f.seq++
	f.rets = append(f.rets, flowReturn{v, pos, f.seq})
	c.flowAdd(flowEvent{kind: evReturn, i: len(f.rets) - 1})
}

// flowError keeps an error for the function's end; access says it is
// an access's, which a later access at the same position replaces.
func (c *Checker) flowError(pos token.Pos, msg string, access bool) {
	f := c.flow
	f.seq++
	f.errs = append(f.errs, flowError{seq: f.seq, pos: pos, msg: msg, access: access})
}

// dropScope records the drop of every variable s declares, at end, the
// last declared first.
func (c *Checker) dropScope(s *Scope, end token.Pos, why dropWhy) {
	f := c.flow
	if f == nil || c.dead || len(s.elems) == 0 {
		return
	}

	mark := len(f.drops)
	for _, obj := range s.elems {
		if v, ok := obj.(*Var); ok && !v.field && !v.global && v.pos != token.NoPos && !(isMut(v.typ) && c.isParam(v)) {
			f.drops = append(f.drops, flowDrop{v: v, pos: end, why: why, runs: holdsBorrow(v.typ) && dropReads(v.typ)}) // a mut parameter is the caller's, never dropped here
		}
	}

	added := f.drops[mark:]
	slices.SortFunc(added, func(a, b flowDrop) int { return int(b.v.pos) - int(a.v.pos) }) // the last declared first, as owners are dropped
	for i := range added {
		f.seq++
		added[i].seq = f.seq
		c.flowAdd(flowEvent{kind: evDrop, v: added[i].v, i: mark + i})
	}
}

// flowLoopBody ends the test of the innermost loop and begins its body:
// the way out leaves from the test, before any of the body.
func (c *Checker) flowLoopBody() {
	f := c.flow
	if f == nil {
		return
	}

	r := &c.regions[len(c.regions)-1]
	r.test = f.cur
	f.cur = f.newBlock(r.test)
}

// flowIn records what f does into the block b instead of the current
// one: a match's move of its scrutinee, made before any arm.
func (c *Checker) flowIn(b int, f func()) {
	fl := c.flow
	if fl == nil {
		f()
		return
	}

	cur := fl.cur
	fl.cur = b
	f()
	fl.cur = cur
}

// flowGuard ends a match guard's block and begins the arm's body after
// it, and makes the block the guard fails into, which it returns;
// flowGuardFails fills that block, dropping what the arm's pattern
// bound, and leads it on to the next arm, which runs when the guard is
// false.
func (c *Checker) flowGuard() int {
	f := c.flow
	if f == nil || c.dead {
		return -1
	}

	g := f.cur
	fails := f.newBlock(g)
	f.cur = f.newBlock(g)
	return fails
}

func (c *Checker) flowGuardFails(fails int, arm *Scope, pos token.Pos) {
	f := c.flow
	if f == nil || fails < 0 {
		return
	}

	dead := c.dead
	c.dead = false // the arm's body may have left; the way past its guard has not
	c.flowIn(fails, func() { c.dropScope(arm, pos, dropBlock) })
	c.dead = dead
	f.edge(fails, f.cur)
}

// flowReturnDrops records the drops a return makes: every variable of
// every open scope of the function, after its deferred calls run.
func (c *Checker) flowReturnDrops(pos token.Pos) {
	if f := c.flow; f != nil && f.fnScope != nil {
		c.dropScopes(f.fnScope.parent, pos, dropExit)
	}
}

// dropScopes records the drops of the scopes from the current one out
// to floor, which is kept: what a break or continue leaves.
func (c *Checker) dropScopes(floor *Scope, pos token.Pos, why dropWhy) {
	for s := c.scope; s != nil && s != floor; s = s.parent {
		c.dropScope(s, pos, why)
	}
}

// setAside starts setting the events recorded aside: an assignment's
// target is checked before its value, but takes effect after it.
func (c *Checker) setAside() int {
	if c.flow == nil {
		return 0
	}

	c.flow.asiding++
	return len(c.flow.aside)
}

// endAside stops setting events aside; putAside records those set aside
// since mark, once the value is checked.
func (c *Checker) endAside() {
	if c.flow != nil {
		c.flow.asiding--
	}
}

func (c *Checker) putAside(mark int) {
	f := c.flow
	if f == nil {
		return
	}

	if !c.dead {
		b := &f.blocks[f.cur]
		b.events = append(b.events, f.aside[mark:]...)
	}

	f.aside = f.aside[:mark]
}

// flowFork starts the branches of a fork: each begins at the block the
// fork is made in.
func (c *Checker) flowFork(fk *fork) {
	f := c.flow
	if f == nil {
		return
	}

	fk.from, fk.base = f.cur, len(f.ends)
	f.cur = f.newBlock(fk.from)
}

// flowBranchEnd ends one branch of a fork: it reaches the join unless
// it left.
func (c *Checker) flowBranchEnd(fk *fork) {
	f := c.flow
	if f == nil {
		return
	}

	if !c.dead {
		f.ends = append(f.ends, f.cur)
	}

	f.cur = f.newBlock(fk.from)
}

// flowJoin joins the branches of a fork; when they do not cover every
// path, the way past them all joins too.
func (c *Checker) flowJoin(fk *fork, complete bool) {
	f := c.flow
	if f == nil {
		return
	}

	if !complete {
		f.ends = append(f.ends, f.cur)
	}

	f.cur = f.newBlock(f.ends[fk.base:]...)
	f.ends = f.ends[:fk.base]
}

// flowLoop starts a loop: its head is where each pass begins.
func (c *Checker) flowLoop(r *region) {
	f := c.flow
	if f == nil {
		return
	}

	r.head = f.newBlock(f.cur)
	r.test, r.cont, r.base, r.floor = r.head, r.head, len(f.breaks), c.scope
	f.cur = r.head
}

// flowLoopEnd ends a loop: the end of its body goes round to its head,
// through what a continue reaches, and the way out joins its breaks and,
// for a loop with a condition, its test.
func (c *Checker) flowLoopEnd(r *region) {
	f := c.flow
	if f == nil {
		return
	}

	if !c.dead {
		c.dropScopes(r.floor, r.end, dropPass)
		f.edge(f.cur, r.cont)
	}

	exit := f.newBlock(f.breaks[r.base:]...)
	f.breaks = f.breaks[:r.base]
	if !r.infinite {
		f.edge(r.test, exit)
	}

	f.cur = exit
}

// flowBranch records a break or continue out of the loop r.
func (c *Checker) flowBranch(r *region, tok token.Kind, pos token.Pos) {
	f := c.flow
	if f == nil || c.dead {
		return
	}

	c.dropScopes(r.floor, pos, dropLeave)
	if tok == token.CONTINUE {
		f.edge(f.cur, r.cont)
	} else {
		f.breaks = append(f.breaks, f.cur)
	}

	f.cur = f.newBlock()
}

// A bitset is a set of small integers.
type bitset []uint64

func (s bitset) has(i int) bool { return s[i/64]&(1<<(i%64)) != 0 }
func (s bitset) set(i int)      { s[i/64] |= 1 << (i % 64) }
func (s bitset) del(i int)      { s[i/64] &^= 1 << (i % 64) }

// union adds t to s and reports whether s grew.
func (s bitset) union(t bitset) bool {
	grew := false
	for i, w := range t {
		if s[i]|w != s[i] {
			s[i] |= w
			grew = true
		}
	}

	return grew
}

// each calls f for every member, in order.
func (s bitset) each(f func(int) bool) {
	for i, w := range s {
		for w != 0 {
			j := bits.TrailingZeros64(w)
			if !f(i*64 + j) {
				return
			}

			w &^= 1 << j
		}
	}
}

// varIndex numbers v among the variables of the graph.
func (f *flow) varIndex(v *Var) int {
	if i, ok := f.vars[v]; ok {
		return i
	}

	i := len(f.vars)
	f.vars[v] = i
	f.first, f.last = append(f.first, -1), append(f.last, -1)
	return i
}

// hold links the loan i into the chain of the loans its holder holds,
// after the ones there.
func (f *flow) hold(i int) {
	h := f.varIndex(f.loans[i].holder)
	for len(f.next) <= i {
		f.next = append(f.next, -1)
	}

	f.next[i] = -1
	if f.last[h] < 0 {
		f.first[h] = i
	} else {
		f.next[f.last[h]] = i
	}

	f.last[h] = i
}

// heldFirst returns the first loan v holds, or -1; the next follow
// through next.
func (f *flow) heldFirst(v *Var) int {
	if i, ok := f.vars[v]; ok {
		return f.first[i]
	}

	return -1
}

// solveFlow runs the passes over the graph of the body just
// walked and reports what collides, in the order the walk met it.
func (c *Checker) solveFlow() {
	f := c.flow
	if len(f.loans) == 0 && len(f.rets) == 0 && len(f.copies) == 0 {
		c.reportFlow() // no variable holds a borrow and none is returned: nothing to decide
		return
	}

	if f.vars == nil {
		f.vars = make(map[*Var]int)
	} else {
		clear(f.vars)
	}

	f.first, f.last, f.next = f.first[:0], f.last[:0], f.next[:0]
	for bi := range f.blocks {
		for _, ev := range f.blocks[bi].events {
			if ev.v != nil {
				f.varIndex(ev.v)
			}
		}
	}

	for i := range f.loans {
		f.varIndex(f.loans[i].v)
		f.hold(i)
	}

	c.copyLoans()
	nv, nl := (len(f.vars)+63)/64, (len(f.loans)+63)/64
	ns := max(nv, nl)
	need := len(f.blocks)*(4*nv+2*nl) + nv + 3*ns + nl
	if cap(f.words) < need {
		f.words = make([]uint64, need)
	}

	words := f.words[:need]
	clear(words)
	take := func(n int) []uint64 {
		w := words[:n:n]
		words = words[n:]
		return w
	}

	f.exit, f.scratch, f.scratch2, f.scratch3 = take(nv), take(ns), take(ns), take(ns)
	for bi := range f.blocks {
		b := &f.blocks[bi]
		b.liveIn, b.liveOut, b.gone, b.goneOut = take(nv), take(nv), take(nv), take(nv)
		b.in, b.out = take(nl), take(nl)
	}

	for v, i := range f.vars {
		if c.liveAtExit(v) {
			f.exit.set(i)
		}
	}

	c.liveness()
	c.forwards()
	c.checkFlow(take(nl))
	c.reportFlow()
}

// reportFlow reports the errors the body's graph found, in the order
// the walk met what they are about.
func (c *Checker) reportFlow() {
	f := c.flow
	slices.SortStableFunc(f.errs, func(a, b flowError) int { return a.seq - b.seq })
	for i, e := range f.errs {
		if i+1 < len(f.errs) && e.access && f.errs[i+1].access && f.errs[i+1].pos == e.pos {
			continue // two accesses at one position are one seen twice, as the operand of a borrow is read and then lent: the later, more specific message wins
		}

		c.errorf(e.pos, e.msg)
	}
}

// liveAtExit reports whether v outlives the body: a variable from
// outside a closure, which may be used after the closure's body, and
// the holder of what a deferred call borrows, which runs as the
// function returns.
func (c *Checker) liveAtExit(v *Var) bool {
	f := c.flow
	if v.pos == token.NoPos || v.global {
		return false
	}

	return f.outer != token.NoPos && v.pos < f.outer || slices.Contains(f.deferred, v)
}

// copyLoans makes the loans the copies give, until no copy has a source
// loan without its new loan: a plain copy gives its new holder a loan
// of each place the source's part may hold; a store through a borrow
// variable gives each place the variable points to a loan of each
// place stored through it.
func (c *Checker) copyLoans() {
	f := c.flow
	for grew := true; grew; {
		grew = false
		for ci := range f.copies {
			cp := &f.copies[ci]
			srcs := f.heldFirst(cp.from)
			for a := srcs; a >= 0; a = f.next[a] {
				la := f.loans[a]
				if cp.through {
					if strings.Contains(la.hpath, "*") || la.hpath != cp.toPath && la.hpath != "" { // a loan of where the borrow points, not one stored through it
						continue
					}

					for b := srcs; b >= 0; b = f.next[b] {
						lb := f.loans[b]
						star := strings.Index(lb.hpath, "*")
						if star < 0 || !pathWithin(lb.hpath, cp.path) || lb.v == cp.from || f.outside[lb.v] == cp.from {
							continue // what the variable held of itself already, l.text = l.title, stays where it was
						}

						rest := strings.TrimPrefix(lb.hpath[star+1:], ".")
						grew = c.copyPair(cp, a, b, borrow{holder: la.v, place: lb.place, mut: lb.mut, pos: lb.pos, hpath: joinPath(la.path, rest)}) || grew
					}

					continue
				}

				hpath := cp.toPath
				switch {
				case pathWithin(la.hpath, cp.path): // the loan is in the part copied: it keeps its place under the new part, g = h with h.src lent puts it in g.src
					if !cp.flat {
						hpath = joinPath(cp.toPath, strings.TrimPrefix(strings.TrimPrefix(la.hpath, cp.path), "."))
					}
				case pathWithin(cp.path, la.hpath): // a part of what the loan covers is copied
				default:
					continue
				}

				grew = c.copyPair(cp, a, -1, borrow{holder: cp.to, place: la.place, mut: la.mut, pos: cp.pos, hpath: hpath, loop: la.loop, variable: la.variable, atExit: cp.atExit}) || grew
			}
		}
	}
}

// copyPair records that the copy cp makes the loan nb from the loans a
// and b, making nb unless the copy made it already, and reports whether
// the pair is new.
func (c *Checker) copyPair(cp *flowCopy, a, b int, nb borrow) bool {
	f := c.flow
	if nb.holder == nb.v || slices.ContainsFunc(cp.pairs, func(p [3]int) bool { return p[0] == a && p[1] == b }) {
		return false // a variable reached through itself, cur = mut m.next where m borrowed cur, holds nothing of itself
	}

	dst := -1
	for _, p := range cp.pairs {
		if d := f.loans[p[2]]; d.holder == nb.holder && d.place == nb.place && d.mut == nb.mut && d.hpath == nb.hpath {
			dst = p[2]
			break
		}
	}

	if dst < 0 {
		f.loans = append(f.loans, nb)
		dst = len(f.loans) - 1
		f.varIndex(nb.v)
		f.hold(dst)
	}

	cp.pairs = append(cp.pairs, [3]int{a, b, dst})
	return true
}

// liveness finds, backwards, the variables live where each block
// begins and ends: those some path from there reaches a use of before
// it is given another value or dropped.
func (c *Checker) liveness() {
	f := c.flow
	for changed := true; changed; {
		changed = false
		for bi := len(f.blocks) - 1; bi >= 0; bi-- {
			b := &f.blocks[bi]
			out := bitset(b.liveOut)
			if len(b.succs) == 0 {
				out.union(f.exit)
			}

			for _, s := range b.succs {
				out.union(f.blocks[s].liveIn)
			}

			live := f.scratch[:len(out)]
			copy(live, out)
			for i := len(b.events) - 1; i >= 0; i-- {
				c.liveStep(live, b.events[i])
			}

			if bitset(b.liveIn).union(live) {
				changed = true
			}
		}

		changed = changed && f.back
	}
}

// liveStep takes the live set after ev to the one before it. A drop
// ends a variable; one whose Drop may read what it borrows uses it.
func (c *Checker) liveStep(live bitset, ev flowEvent) {
	f := c.flow
	switch ev.kind {
	case evUse:
		live.set(f.vars[ev.v])
	case evAssign:
		if ev.path == "" {
			live.del(f.vars[ev.v])
		}
	case evDrop:
		live.del(f.vars[ev.v])
		if f.drops[ev.i].runs {
			live.set(f.vars[ev.v])
		}
	}
}

// forwards finds the loans in effect where each block begins and ends,
// those some path from where they were made reaches with their holder,
// or the part of it they are in, given no other value on the way; and
// the variables that may be dropped there.
func (c *Checker) forwards() {
	f := c.flow
	for changed := true; changed; {
		changed = false
		for bi := range f.blocks {
			b := &f.blocks[bi]
			in, gone := bitset(b.in), bitset(b.gone)
			for _, p := range b.preds {
				in.union(f.blocks[p].out)
				gone.union(f.blocks[p].goneOut)
			}

			cur, dropped := f.scratch[:len(in)], f.scratch2[:len(gone)]
			copy(cur, in)
			copy(dropped, gone)
			for _, ev := range b.events {
				c.forwardStep(cur, dropped, ev)
			}

			if bitset(b.out).union(cur) {
				changed = true
			}

			if bitset(b.goneOut).union(dropped) {
				changed = true
			}
		}

		changed = changed && f.back
	}
}

// forwardStep takes the loans in effect and the variables dropped
// before ev to those after it. A drop leaves what the variable holds in
// effect: a value copied out of its block holds it still.
func (c *Checker) forwardStep(in, dropped bitset, ev flowEvent) {
	f := c.flow
	switch ev.kind {
	case evLoan:
		in.set(ev.i)
	case evCopy:
		cp := &f.copies[ev.i]
		if cp.through {
			c.throughEnds(cp, in)
		}

		for _, p := range cp.pairs {
			if in.has(p[0]) && (p[1] < 0 || in.has(p[1])) {
				in.set(p[2])
			}
		}
	case evAssign:
		for i := f.heldFirst(ev.v); i >= 0; i = f.next[i] {
			if pathWithin(f.loans[i].hpath, ev.path) {
				in.del(i)
			}
		}

		if ev.path == "" {
			dropped.del(f.vars[ev.v])
		}
	case evDrop:
		dropped.set(f.vars[ev.v])
	}
}

// throughEnds lets go of what the place a store through a borrow writes
// held before, m.src = ref z with m := mut h ends h's hold on what src
// held, when the borrow has one place it may point to here; pointing to
// one of several, m = mut h2 on some path, it may leave each as it was.
func (c *Checker) throughEnds(cp *flowCopy, in bitset) {
	f := c.flow
	target := -1
	for i := f.heldFirst(cp.from); i >= 0; i = f.next[i] {
		if b := &f.loans[i]; in.has(i) && b.hpath == cp.toPath && !strings.Contains(b.hpath, "*") {
			if target >= 0 {
				return
			}

			target = i
		}
	}

	if target < 0 {
		return
	}

	la := &f.loans[target]
	rest := strings.TrimPrefix(cp.path[strings.Index(cp.path, "*")+1:], ".")
	part := joinPath(la.place.path, rest)
	for i := f.heldFirst(la.v); i >= 0; i = f.next[i] {
		if pathWithin(f.loans[i].hpath, part) {
			in.del(i)
		}
	}
}

// pathWithin reports whether the part p of a variable is the part q or
// inside it; every part is inside the whole, "".
func pathWithin(p, q string) bool {
	return q == "" || p == q || len(p) > len(q) && p[:len(q)] == q && p[len(q)] == '.'
}

// joinPath writes the part q of the part p of a variable.
func joinPath(p, q string) string {
	switch {
	case p == "":
		return q
	case q == "":
		return p
	}

	return p + "." + q
}

// checkFlow walks each block with what the passes found and checks
// every access, drop, new loan and return against the loans live where
// it is. Few events need the live set, so it is computed for those.
func (c *Checker) checkFlow(reported bitset) {
	f := c.flow
	for bi := range f.blocks {
		b := &f.blocks[bi]
		in, dropped := f.scratch[:len(b.in)], f.scratch3[:len(b.gone)]
		copy(in, b.in)
		copy(dropped, b.gone)
		for i, ev := range b.events {
			switch ev.kind {
			case evAccess:
				if a := &f.accesses[ev.i]; !a.gone && c.collidesAny(in, a.p, a.what) {
					c.checkAccess(a, in, c.liveAfter(bi, i), bi, i)
				}
			case evDrop:
				if d := &f.drops[ev.i]; c.lendsAny(in, d.v) {
					c.checkDrop(d, in, c.liveAfter(bi, i), reported, bi, i)
				}
			case evLoan:
				c.checkGone(ev.i, dropped, reported)
				c.checkEscape(ev.i, reported)
			case evCopy:
				for _, p := range f.copies[ev.i].pairs {
					if in.has(p[0]) && (p[1] < 0 || in.has(p[1])) {
						c.checkGone(p[2], dropped, reported)
						c.checkEscape(p[2], reported)
					}
				}
			case evReturn:
				r := f.rets[ev.i]
				c.followReturn(r.v, r.pos, r.seq, in, nil)
			}

			c.forwardStep(in, dropped, ev)
		}
	}
}

// collidesAny reports whether a loan in effect collides with an access
// to p, and lendsAny whether one lends v: whether the event needs the
// live set at all.
func (c *Checker) collidesAny(in bitset, p place, what access) bool {
	f := c.flow
	found := false
	in.each(func(li int) bool {
		found = f.loans[li].collides(p, what)
		return !found
	})

	return found
}

func (c *Checker) lendsAny(in bitset, v *Var) bool {
	f := c.flow
	found := false
	in.each(func(li int) bool {
		found = f.loans[li].v == v
		return !found
	})

	return found
}

// checkGone reports a loan made of a variable that may already be
// dropped: a borrow a block's value carries out of the block it
// borrows, { t := "b".Clone(); ref t }. A borrow variable is a
// pointer, whose owner is elsewhere.
func (c *Checker) checkGone(li int, dropped, reported bitset) {
	f := c.flow
	b := &f.loans[li]
	if reported.has(li) || isBorrow(b.v.typ) && !b.variable || !dropped.has(f.vars[b.v]) {
		return
	}

	reported.set(li)
	for _, d := range f.drops {
		if d.v == b.v {
			f.errs = append(f.errs, flowError{seq: d.seq, pos: b.pos, msg: "cannot keep a borrow of " + b.v.name + ": " + b.v.name + " is dropped" + dropWhere(d.why) + c.lineCol(d.pos)})
			return
		}
	}
}

// checkEscape reports a borrow stored where its function cannot follow
// it: into what the caller owns behind a parameter, of anything but
// that same place, l.text = name with l a mut parameter; or, from a
// closure's body, into a variable outside it, of what is the closure's
// own. Neither a signature nor a closure can say the place now borrows
// it, so the caller or the closure's maker would not keep it alive.
func (c *Checker) checkEscape(li int, reported bitset) {
	f := c.flow
	b := &f.loans[li]
	if reported.has(li) {
		return
	}

	what := b.v.name
	if param := f.outside[b.v]; param != nil {
		what = param.name
	}

	if param := f.outside[b.holder]; param != nil && f.outside[b.v] != param {
		reported.set(li)
		f.errs = append(f.errs, flowError{seq: f.seq, pos: b.pos, msg: "cannot store a borrow of " + what + " in " + param.name + ": " + param.name + " is the caller's, and a signature cannot say that it borrows " + what})
		return
	}

	inside := b.v.pos >= f.outer || f.outside[b.v] != nil
	if f.outer != token.NoPos && b.holder.pos != token.NoPos && b.holder.pos < f.outer && !b.holder.global && inside && f.outside[b.holder] == nil {
		reported.set(li)
		f.errs = append(f.errs, flowError{seq: f.seq, pos: b.pos, msg: "cannot store a borrow in " + b.holder.name + " from inside a closure: " + b.holder.name + " is declared outside it, and what the closure borrows ends with each call"})
	}
}

// flowParams gives each parameter that carries a borrow a borrow of what
// the caller owns behind it, a variable of its own standing for it, so
// that a store reaching the caller's places is seen however it gets
// there: through a copy, a literal or a field.
func (c *Checker) flowParams(sig *Signature) {
	f := c.flow
	if f == nil {
		return
	}

	for i := -1; i < len(sig.params); i++ {
		p := sig.recv
		if i >= 0 {
			p = sig.params[i]
		}

		if p == nil {
			continue
		}

		borrowPaths(p.typ, "", func(path string, mut bool) {
			if f.outside == nil {
				f.outside = make(map[*Var]*Var)
			}

			o := NewVar(p.pos, p.name, Typ[Invalid])
			f.outside[o] = p
			c.flowLoan(borrow{holder: p, place: place{o, ""}, mut: mut, pos: p.pos, hpath: path}) // in the field that carries it: w.h = mut local lets go of the caller's
		})
	}
}

// borrowPaths calls f with the part of a value of type t that is or
// carries a borrow, and whether it is mut: each struct field on its
// own, the whole of anything else borrowIn looks into.
func borrowPaths(t Type, path string, f func(path string, mut bool)) {
	switch u := t.(type) {
	case *Named:
		if u.holdsBorrow() {
			borrowPaths(u.Underlying(), path, f)
		}

		return
	case *Struct:
		for _, fld := range u.fields {
			borrowPaths(fld.typ, joinPath(path, fld.name), f)
		}

		return
	}

	if mut, ok := borrowIn(t); ok {
		f(path, mut)
	}
}

// dropWhere says where a drop is, for a message.
func dropWhere(why dropWhy) string {
	// dropExit reads as the end of a block, which it is.
	switch why {
	case dropPass:
		return " at the end of the loop's pass at "
	case dropLeave:
		return " where its block is left at "
	}

	return " at the end of its block at "
}

// liveAfter computes the variables live just after the event ei of
// block bi, backwards from the block's end, in the second scratch set.
func (c *Checker) liveAfter(bi, ei int) bitset {
	f := c.flow
	b := &f.blocks[bi]
	live := f.scratch2[:len(b.liveOut)]
	copy(live, b.liveOut)
	if len(b.succs) == 0 {
		live.union(f.exit)
	}

	for i := len(b.events) - 1; i > ei; i-- {
		c.liveStep(live, b.events[i])
	}

	return live
}

// holderText names a loan's holder for a message.
func (c *Checker) holderText(b *borrow) string {
	switch {
	case b.loop:
		return "the range loop"
	case c.flow != nil && slices.Contains(c.flow.deferred, b.holder):
		return "the deferred call"
	}

	return b.holder.name
}

// useText writes the use that keeps the loan b live, found from the
// event ei of block bi, for a message about something at pos.
func (c *Checker) useText(b *borrow, bi, ei int, pos token.Pos) string {
	use, drop := c.findUse(b, bi, ei)
	switch {
	case slices.Contains(c.flow.deferred, b.holder):
		return ", and the deferred call runs when the function returns"
	case use == token.NoPos && c.liveAtExit(b.holder):
		return ", and " + c.holderText(b) + " outlives the closure"
	case use == token.NoPos:
		return ""
	case drop:
		return ", used when " + b.holder.name + " is dropped at " + c.lineCol(use) + ", by its Drop"
	case use < pos:
		return ", used in the next pass of the loop at " + c.lineCol(use)
	}

	return ", used at " + c.lineCol(use)
}

// checkAccess reports an access that collides with a loan live where
// it is made: the first such loan.
func (c *Checker) checkAccess(a *flowAccess, in, live bitset, bi, ei int) {
	f := c.flow
	in.each(func(li int) bool {
		b := &f.loans[li]
		if !b.collides(a.p, a.what) || !c.loanLive(li, live) {
			return true
		}

		f.errs = append(f.errs, flowError{seq: a.seq, pos: a.pos, msg: c.collision(a.e, a.p, a.what, b) + c.useText(b, bi, ei, a.pos), access: true})
		return false
	})
}

// checkDrop reports a loan of a variable dropped here that is live: a
// borrow kept past its owner's end, once for each loan.
func (c *Checker) checkDrop(d *flowDrop, in, live bitset, reported bitset, bi, ei int) {
	f := c.flow
	in.each(func(li int) bool {
		b := &f.loans[li]
		if b.v != d.v || b.holder == d.v || reported.has(li) || !c.loanLive(li, live) {
			return true
		}

		if isBorrow(d.v.typ) && !b.variable {
			return true // a borrow variable is a pointer: what is lent through it is its owner's, which lives on; a closure that reads the variable itself does not
		}

		if d.why == dropExit && slices.Contains(f.deferred, b.holder) {
			return true // the deferred calls run before the function's own variables are dropped
		}

		reported.set(li)
		msg := "cannot keep a borrow of " + d.v.name + " in " + c.holderText(b) + ": " + d.v.name + " is dropped" + dropWhere(d.why) + c.lineCol(d.pos)
		f.errs = append(f.errs, flowError{seq: d.seq, pos: b.pos, msg: msg + c.useText(b, bi, ei, d.pos)})
		return true
	})
}

// loanLive reports whether the loan li, in effect, is live: whether its
// holder is. What reaches a place through a holder copied the holder's
// loans when it did, so a holder's own liveness is all there is.
func (c *Checker) loanLive(li int, live bitset) bool {
	f := c.flow
	return live.has(f.vars[f.loans[li].holder])
}

// findUse finds the first use of the loan b's holder that some path
// from just after the event ei of block bi reaches before the holder
// is given another value or dropped, and whether it is the holder's
// Drop; none for a range loop, whose use is its next pass. It runs only
// for a message.
func (c *Checker) findUse(b *borrow, bi, ei int) (token.Pos, bool) {
	if b.loop {
		return token.NoPos, false
	}

	return c.firstUse(b.holder, bi, ei)
}

// firstUse finds the first use of v some path from just after the event
// ei of block bi reaches before v is given another value or dropped.
func (c *Checker) firstUse(v *Var, bi, ei int) (token.Pos, bool) {
	f := c.flow
	seen := make([]bool, len(f.blocks))
	type at struct{ block, from int }
	queue := []at{{bi, ei + 1}}
	for len(queue) > 0 {
		q := queue[0]
		queue = queue[1:]
		blk := &f.blocks[q.block]
		stop := false
		for _, ev := range blk.events[q.from:] {
			if ev.v != v {
				continue
			}

			switch {
			case ev.kind == evUse:
				return ev.pos, false
			case ev.kind == evDrop && f.drops[ev.i].runs:
				return f.drops[ev.i].pos, true
			case ev.kind == evDrop, ev.kind == evAssign && ev.path == "":
				stop = true
			}

			if stop {
				break
			}
		}

		if stop {
			continue
		}

		for _, s := range blk.succs {
			if !seen[s] {
				seen[s] = true
				queue = append(queue, at{s, 0})
			}
		}
	}

	return token.NoPos, false
}
