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

// The borrow pass over the form: the graph of flow.go built from the
// form's blocks, with the instructions as its events, solved once the
// form is read. A temporary whose value is or holds a borrow
// has a holder of its own in the graph where the order matters: a borrow
// made into a temporary, lent for the call that takes it or until its
// statement drops it; a value a call or a statement with several values
// takes, lent its sources while the temporary is used; a value carried
// out of a variable its statement stores, holding what it lends before
// any store; and a temporary a binding is taken from. Any other value
// says what it lends where it is stored, by its expression, so that
// what a variable holds is decided in the form's order.

// borrowPasses keeps the scratch of the borrow pass for reuse across
// bodies and checkers, as the lowering's and the graphs are kept.
var borrowPasses = sync.Pool{New: func() any { return new(formBorrows) }}

// freePasses gives the scratch of the two passes over the form back for
// reuse, with nothing of the package in it.
func (c *Checker) freePasses() {
	if m := c.moves; m != nil {
		m.c, m.f = nil, nil
		movePasses.Put(m)
		c.moves = nil
	}

	if m := c.borrowPass; m != nil {
		m.checker, m.form = nil, nil
		clear(m.holders)
		if len(m.names) > cacheMax {
			clear(m.names)
		}
		borrowPasses.Put(m)
		c.borrowPass = nil
	}
}

// formGraphs, when a test sets it, is given each pass once its graph is
// solved, for the test to print the graph and the pass's table.
var formGraphs func(*formBorrows)

// A formBorrows is the pass over one form, kept with the checker and
// reused across bodies.
type formBorrows struct {
	checker *Checker
	form    *Form
	holders []*Var            // per temporary, the holder standing for it in the graph when it is or holds a borrow, or is bound from; nil otherwise
	name    []byte            // scratch for the fields a place is named by
	names   map[string]string // the fields names made so far, one string each
}

// formBorrows runs the borrow pass over f, the form of a body of
// signature sig, for a closure beginning at outer, then the passes of
// the closures written in the body under its graph, reporting what they
// refuse.
func (c *Checker) formBorrows(f *Form, sig *Signature, outer token.Pos) {
	saved, outerSig := c.flow, c.sig
	c.flow, c.sig = c.newFlow(outer), sig // the recorders decide by the body's own signature: a closure's parameters are its own
	if outer != token.NoPos {
		c.flow.parent = saved // a closure's body sees what the variables of the body around it hold
	}

	m := c.borrowPass
	if m == nil {
		m = borrowPasses.Get().(*formBorrows)
		m.checker = c
		c.borrowPass = m
	}

	m.form = f
	m.holders = slices.Grow(m.holders[:0], len(f.Temps))[:len(f.Temps)]
	clear(m.holders)
	fl := c.flow
	for range f.Blocks[1:] {
		fl.newBlock()
	}

	fl.loopOf = slices.Grow(fl.loopOf[:0], len(f.Blocks))
	for b := range f.Blocks {
		for _, s := range f.Blocks[b].Succs {
			fl.edge(b, s)
		}

		fl.loopOf = append(fl.loopOf, f.Blocks[b].Loop)
		if f.Blocks[b].Head {
			fl.loops = append(fl.loops, loopSpan{head: b})
		}
	}

	c.flowParams(sig)
	for _, b := range f.order { // what control cannot reach is not checked
		fl.cur = b
		for i := f.Blocks[b].From; i < f.Blocks[b].To; i++ {
			m.step(&f.Instrs[i])
		}
	}

	c.solve()
	if formGraphs != nil {
		formGraphs(m) // a test reads the graph before it is freed
	}

	m.keptUses()
	c.eachFlowError(func(e flowError) { c.errorf(e.pos, e.msg) })
	for _, cf := range f.closures { // a closure written in this body: its graph sees this one as its parent
		c.formBorrows(cf.form, cf.sig, cf.pos)
	}

	c.freeFlow(c.flow)
	c.flow, c.sig = saved, outerSig
}

// step records the events of one instruction.
func (m *formBorrows) step(in *Instr) {
	c, f := m.checker, m.form
	switch in.Op {
	case OpRead:
		if in.Capture {
			return // a closure's capture: the closure's making is the access
		}

		p, kind, _ := m.placeOf(in.Place)
		if kind == notPlace {
			if in.Place.Temp != NoTemp && m.holders[in.Place.Temp] == nil {
				m.made(in.Place, in.Pos, false) // a value chosen by an if or a match, read where its arms join: made here
			}

			m.use(in.Place, in.Pos)
			return
		}

		c.flowUse(p.v, basePos(in.Expr)) // a use keeps what the variable holds lent
		what := readAccess
		if in.Mut {
			what = mutAccess // an arm's bindings reach the place through a mut borrow
		}

		c.access(in.Expr, p, what, in.Expr.Pos())
	case OpMove:
		if in.Capture {
			return
		}

		if p, kind, _ := m.placeOf(in.Place); kind == placeOwned {
			c.access(in.Expr, p, moveAccess, in.Expr.Pos())
		} else {
			m.use(in.Place, in.Pos)
		}
	case OpBorrow:
		if !in.Capture {
			m.borrowed(in)
		}
	case OpMake, OpCall:
		if lit := funcLitOf(in.Expr); lit != nil && in.Op == OpMake {
			c.closureAccesses(lit) // where the closure is made; a send names the literal again, as a call
		}

		m.uses(in.Args, in.Pos)
		m.made(in.To, in.Pos, in.Op == OpCall)
		if call, ok := in.Expr.(*ast.CallExpr); ok && in.Op == OpCall {
			m.stores(call)
		}
	case OpStore:
		m.store(in)
		if in.To.IsValue() && !f.Temps[in.To.Temp].Carried {
			m.use(in.To, in.Pos) // after the store's access, which what a value lent for the statement still collides with; a carried value is held until the store copies it
		}
	case OpDrop:
		if in.Place.IsTemp() {
			if h := m.holders[in.Place.Temp]; h != nil {
				t := &f.Temps[in.Place.Temp]
				c.flowDrop(h, in.Pos, in.why, isBorrow(t.Type) && !t.Loop) // a borrow made here is lent until the statement drops it; a value's loans end with its last use, and a loop's holder is read at each pass
			}

			return
		}

		m.dropVar(f.Vars[in.Place.Var], in.Pos, in.why)
	case OpLeave:
		for _, a := range in.Args {
			m.dropVar(f.Vars[a.Var], in.Pos, in.why)
		}
	case OpDefer:
		m.uses(in.Args, in.Pos)
		if call, ok := in.Expr.(*ast.CallExpr); ok {
			if holder := c.flowDeferred(in.Pos); holder != nil {
				rest := make([]operand, len(call.Args)) // the variadic arguments, as the typing left them: one it refused has no type
				for i, a := range call.Args {
					if c.info.Types[a] != nil {
						rest[i].mode = value
					}
				}

				c.deferHolds(holder, call, c.calledBy(call).sig, rest, in.Pos)
			}
		}
	case OpReturn:
		m.uses(in.Args, in.Pos)
		m.returned(in)
	}
}

// keptUses checks each closure value the body puts where a closure that
// borrows may not go, from the loans before the use: stored, sent or
// spawned, it is refused here; given to a parameter its callee may keep,
// it is refused at the package's end, when what the callee keeps is
// known (keepCalls).
func (m *formBorrows) keptUses() {
	c := m.checker
	for _, k := range m.form.kept {
		op := c.valueOf(k.expr)
		if k.param == nil {
			c.cannotBe(op, k.how, k.pos)
		} else if what := c.borrowsWhat(op, k.pos); what != "" {
			c.keepCalls = append(c.keepCalls, keepCall{k.param, k.callee, what, k.pos}) // refused at the package's end, when what the callee keeps is known
		}
	}
}

// valueOf is the value of e as an operand, with the type the typing
// gave it.
func (c *Checker) valueOf(e ast.Expr) operand {
	return operand{mode: value, typ: c.info.Types[e], expr: e}
}

// returned checks what the returned value lets out of the function: a
// borrow must come from the function's source,
// followed through the loans in effect at the return, and a closure
// that borrows cannot leave. The value a ? returns is the function's
// own result, which lets nothing out that its expression's type did
// not say.
func (m *formBorrows) returned(in *Instr) {
	if _, try := in.Expr.(*ast.TryExpr); try {
		return
	}

	c := m.checker
	op := c.valueOf(in.Expr)
	if len(c.keptParams(op)) > 0 {
		return // a parameter returned: what the function keeps, checked at its calls by the typing
	}

	c.returnEscapes(op, in.Expr.Pos())
}

// borrowed records a borrow made into a temporary: the access to the
// place, and the temporary's holder, lent the place for the call that
// takes it or until the statement drops it; for a range loop, lent for
// every pass, read at the start of each. A borrow of a part of a
// temporary, or of what a value made here lends, is held by nothing of
// its own: the loop's holder lends what the value lends.
func (m *formBorrows) borrowed(in *Instr) {
	c := m.checker
	t := &m.form.Temps[in.To.Temp]
	p, kind, _ := m.placeOf(in.Place)
	if kind == notPlace {
		m.use(in.Place, in.Pos)
		if !t.Loop {
			return
		}

		h := m.holder(in.To, in.Pos, "")
		c.sources(m.form.Temps[in.Place.Temp].Expr, func(src ast.Expr, _, _ bool) {
			if p, kind := through(c.placeOf(src)); kind != notPlace {
				c.loopLoan(h, p, in.Mut, in.Pos)
			}
		})

		m.eachPass(h, in.Pos)
		return
	}

	c.access(in.Expr, p, borrowAccess(in.Mut), in.Expr.Pos())
	h := m.holder(in.To, in.Pos, t.Named)
	if t.Loop {
		p, _ = through(p, kind)
		c.loopLoan(h, p, in.Mut, in.Pos)
		m.eachPass(h, in.Pos)
		return
	}

	c.flowLoan(borrow{holder: h, place: p, mut: in.Mut, pos: in.Pos, call: t.Named == ""}) // a construct's holder is named in a message; a borrow lent for a call is not
}

// placeOf is the place an operand names as the graph names one, the
// variable and its fields up to the first element, how it is
// reached, owned, through a borrow or through an element, and the type
// of the value at the end of the path. A borrow in a field is a part of
// the owned value, which the store's rule reaches through; only a
// variable that is a borrow reaches its places through a borrow. The
// parts a pattern names are no fields: a binding holds what the whole
// lends.
func (m *formBorrows) placeOf(o Operand) (place, placeKind, Type) {
	if o.IsTemp() {
		return place{}, notPlace, nil
	}

	v := m.form.Vars[o.Var]
	kind, t := placeOwned, v.typ
	fields := o.Path
	if i := strings.Index(fields, "[]"); i >= 0 {
		fields = strings.TrimSuffix(fields[:i], ".") // the graph names a place up to its first element
	}

	plain := !strings.ContainsAny(fields, "*?") // the fields alone: the path itself names them
	named := m.name[:0]
	for path := o.Path; path != ""; {
		part, rest, _ := strings.Cut(path, ".")
		path = rest
		switch {
		case part == "*":
			if _, ok := t.(*Ref); ok && kind == placeOwned && len(named) == 0 {
				kind = placeBorrowed
			}
		case part == "[]":
			kind = placeIndexed
		case part[0] == '?':
			t = nil // the parts a pattern names are no fields: a binding holds what the whole lends
			continue
		case kind != placeIndexed && !plain:
			if len(named) > 0 {
				named = append(named, '.')
			}

			named = append(named, part...)
		}

		t = stepType(t, part)
	}

	m.name = named
	if !plain {
		fields = m.nameOfFields(named)
	}

	return place{v, fields}, kind, t
}

// nameOfFields is the string of the fields named in b, made once per
// name: the places of a body are named by few names, many times.
func (m *formBorrows) nameOfFields(b []byte) string {
	if s, ok := m.names[string(b)]; ok {
		return s
	}

	if m.names == nil {
		m.names = make(map[string]string)
	}

	s := string(b)
	m.names[s] = s
	return s
}

// root is the operand a pattern's part is bound from: the operand
// without the parts the pattern names.
func root(o Operand) Operand {
	if i := strings.Index(o.Path, "?"); i >= 0 {
		o.Path = strings.TrimSuffix(o.Path[:i], ".")
	}

	return o
}

// holder makes the holder standing for the temporary t in the graph,
// assigned where it is made.
func (m *formBorrows) holder(t Operand, pos token.Pos, name string) *Var {
	h := m.checker.newHolder(pos, name)
	m.holders[t.Temp] = h
	return h
}

// use records that the temporary o, or the temporary a part of it is
// reached from, is used here, when it has a holder.
func (m *formBorrows) use(o Operand, pos token.Pos) {
	if o.IsValue() {
		if h := m.holders[o.Temp]; h != nil {
			m.checker.flowUse(h, pos)
		}
	}
}

func (m *formBorrows) uses(args []Operand, pos token.Pos) {
	for _, a := range args {
		m.use(a, pos)
	}
}

// eachPass records that the loop whose head the block being written
// goes to reads its holder h at the start of each pass, as the loop
// walks what it holds.
func (m *formBorrows) eachPass(h *Var, pos token.Pos) {
	fl := m.checker.flow
	for _, s := range m.form.Blocks[fl.cur].Succs {
		if m.form.Blocks[s].Head {
			cur := fl.cur
			fl.cur = s
			m.checker.flowUse(h, pos)
			fl.cur = cur
			return
		}
	}
}

// made gives the temporary t, filled by a make, a call or the arms of
// an if or a match, a holder of what its value lends, when the value is or holds a borrow and a call or a statement
// with several values takes it, or it is carried, or a binding is taken
// from a part of it. A value taken by a call lends each source where
// the source stands, with an access to it there when a call made the
// value; a carried value holds what it lends where the place is stored;
// a literal, a tuple, a variant or a closure holds what its parts lend.
// The loans live while the temporary is used, as the loans lent for a
// call do.
func (m *formBorrows) made(t Operand, pos token.Pos, call bool) {
	if t.Temp == NoTemp {
		return
	}

	c, info := m.checker, &m.form.Temps[t.Temp]
	e := info.Expr
	lends := (info.Given || info.Carried) && info.Type != nil && (holdsBorrow(info.Type) || isSignature(info.Type))
	if !info.Bound && !lends {
		return // stored, taken apart or dropped: a value lends its sources only where a call or a statement with several values takes it, and a store says what it lends
	}

	h := m.holder(t, pos, m.nameOf(info))
	if !lends {
		c.holdsTemporary(h) // bound from, holding nothing of its own: a borrow of it returned is of a temporary
		return
	}

	f := c.flow
	first := len(f.loans)
	switch {
	case info.Carried || isFuncLit(e):
		c.holdValue(h, "", info.Type, e, pos) // what a variable the value is stored in would hold
	default:
		mut, _ := borrowIn(info.Type)
		c.sources(e, func(src ast.Expr, read, deref bool) {
			if p, kind := c.placeOf(src); kind != notPlace && call {
				c.access(src, p, borrowAccess(mut), src.Pos()) // the call's result lends the place: accessed where it is lent for the call
			}

			c.deriveFrom(h, "", mut, src, read, deref, src.Pos())
		})
	}

	for i := first; i < len(f.loans); i++ {
		if f.loans[i].holder == h {
			f.loans[i].call = true // lent for a call or a statement, held by no variable
		}
	}
}

// funcLitOf is the closure e is, or nil.
func funcLitOf(e ast.Expr) *ast.FuncLit {
	lit, _ := unparen(e).(*ast.FuncLit)
	return lit
}

// nameOf names a temporary's holder for a message: a temporary a
// binding is taken from by the expression whose value it holds; the
// holder of a value lent for a call is named by nothing.
func (m *formBorrows) nameOf(t *TempInfo) string {
	if t.Bound {
		return exprText(unparen(t.Expr))
	}

	return ""
}

// stores records what a call stores where its callee says: Push, Insert
// and Extend put their argument in the receiver, and a generic call puts
// what one argument holds into another lent mut.
func (m *formBorrows) stores(call *ast.CallExpr) {
	c := m.checker
	k := c.calledBy(call)
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok && k.sig != nil && k.sig.recv != nil && k.obj == nil {
		c.storedElement(call, sel, k.sig)
	}

	if _, ok := k.obj.(*Func); ok && k.sig != nil {
		c.passStores(call)
	}
}

// store records a store: the access to the place, what it lets go of,
// and what it holds once the value is in it.
func (m *formBorrows) store(in *Instr) {
	c := m.checker
	if in.Place.IsTemp() { // an arm's value, into the temporary of the if or the match that chooses it: what it lends is decided where the chosen value is read, or stored, by the expression
		m.usedWhereMade(in.To, "chosen by if or match, or come out of a block", in.Expr.Pos())
		return
	}

	p, kind, t := m.placeOf(in.Place)
	pos := in.Expr.Pos()
	if in.To.IsValue() && (in.To.Path != "" || m.form.Temps[m.form.Temps[in.To.Temp].Origin].Bound) { // a part of a temporary, an element ranged over or what a pattern binds, the whole or a part: the place holds the temporary, dropped where its statement ends, and what the value lends
		if in.Decl {
			c.flowAssign(p.v, "")
		}

		if holdsBorrow(p.v.typ) {
			if tmp := m.temporary(in.To); tmp != nil {
				c.flowLoan(borrow{holder: p.v, place: place{tmp, ""}, pos: pos})
			}

			c.derive(p.v, p.path, t, m.form.Temps[in.To.Temp].Expr, pos)
		}

		return
	}

	if r := root(in.To); !in.To.IsTemp() && r.Path != in.To.Path { // a pattern takes a place apart: the binding holds what the whole lends
		c.flowAssign(p.v, "")
		m.holdPlace(p.v, "", p.v.typ, r, pos)
		return
	}

	if p.path != "" || kind != placeOwned {
		c.flowUse(p.v, basePos(in.Expr)) // a store into a part reaches it through the variable, a use of what it holds
	}

	if c.flow.outer != token.NoPos && t != nil && (holdsBorrow(t) && m.brings(in.To) || isSignature(t) && m.closureBorrows(in.To, pos)) && c.closureStore(p, pos) {
		return // a closure's body storing a borrow, or a closure that borrows, in a variable from outside it
	}

	if in.Decl {
		if isSignature(p.v.typ) && !in.To.IsTemp() {
			m.usedWhereMade(in.To, "copied to another variable", pos)
		}

		c.flowAssign(p.v, "") // a declaration gives the variable its value: what it held on a loop's last pass is let go
		m.hold(p.v, "", p.v.typ, in)
		return
	}

	if kind == placeOwned && p.path == "" && isBorrow(p.v.typ) {
		c.access(in.Expr, p, rebindAccess, accessPos(in.Expr)) // a closure that reads it when called would see the new borrow
		c.flowAssign(p.v, "")                                  // the assignment points it somewhere else
		m.hold(p.v, "", p.v.typ, in)
		return
	}

	c.access(in.Expr, p, writeAccess, accessPos(in.Expr))
	switch {
	case kind == placeOwned && p.path == "" && isSignature(t):
		m.usedWhereMade(in.To, "assigned with =", pos)
		c.flowAssign(p.v, "")
	case t != nil && holdsBorrow(t):
		prefix := ""
		if kind == placeOwned {
			prefix = refPrefix(p)
		}

		c.storeHolds(p, kind, prefix, t, func(part string) { m.hold(p.v, part, t, in) }, pos)
	}
}

// usedWhereMade refuses the value o, a closure that borrows, anywhere
// but where it is made: bound with :=, called, or passed straight to a
// call, how saying where it was put instead. What it borrows is then
// known where it is used, with nothing to follow.
func (m *formBorrows) usedWhereMade(o Operand, how string, pos token.Pos) {
	if m.closureBorrows(o, pos) {
		m.checker.errorf(pos, "a closure that borrows cannot be "+how+": it is used where it is made, bound with :=, called, or passed to a call")
	}
}

// closureBorrows reports whether the value o is a closure that borrows:
// one written here that captures by borrow, or a variable holding one
// before pos.
func (m *formBorrows) closureBorrows(o Operand, pos token.Pos) bool {
	c := m.checker
	switch {
	case o.IsValue():
		e := m.form.Temps[o.Temp].Expr
		return e != nil && c.closureExprBorrows(e)
	case !o.IsTemp() && o.Path == "":
		v := m.form.Vars[o.Var]
		return isSignature(v.typ) && c.heldBy(v, pos) != nil
	}

	return false
}

// hold records that the part path of v, of type t, holds what the
// value stored by in lends: what the stored temporary's holder holds,
// copied, or what the value's expression lends, or what the place read
// holds. A borrow of a temporary is refused where a variable would keep
// it.
func (m *formBorrows) hold(v *Var, path string, t Type, in *Instr) {
	c := m.checker
	if !in.To.IsTemp() {
		if from := m.form.Vars[in.To.Var]; isMut(t) && in.To.Path == "" && isMut(from.typ) {
			c.flowCopy(v, from, "", path, in.Expr.Pos()) // q := p moves a mut borrow: q holds what p held
			return
		}

		m.holdPlace(v, path, t, in.To, in.Expr.Pos())
		return
	}

	if in.To.Temp == NoTemp {
		return
	}

	h := m.holders[in.To.Temp]
	if info := &m.form.Temps[in.To.Temp]; h != nil && (info.Given || info.Carried) {
		c.flowCopy(v, h, "", path, in.Expr.Pos()) // what the holder took as the value was made, lent until now
		return
	}

	c.holdIn(v, path, t, m.form.Temps[in.To.Temp].Expr, in.Expr.Pos()) // by the expression; for a chosen value, each arm's; a borrow of a temporary is refused
}

// holdPlace records that the part path of v, of type t, holds what the
// place o, read as a value, lends, as derive does for a place read: a
// ref borrow the place holds is copied, any other place is borrowed; a
// place behind a * is read through the borrow, x := *r holding what r
// points at holds.
func (m *formBorrows) holdPlace(v *Var, path string, t Type, o Operand, pos token.Pos) {
	mut, ok := borrowIn(t)
	if !ok {
		return
	}

	deref := strings.HasSuffix(o.Path, "*")
	if deref {
		o.Path = withoutDeref(o.Path)
	}

	if p, kind, _ := m.placeOf(o); kind != notPlace {
		m.checker.derivePlace(v, path, mut, p, kind, true, deref, pos)
	}
}

// brings reports whether the stored value o carries a borrow of a place
// or a closure's captures, which a variable outside a closure may not be
// given from inside it: a variant or a constant carries none.
func (m *formBorrows) brings(o Operand) bool {
	if !o.IsTemp() {
		return true
	}

	if o.Temp == NoTemp {
		return false
	}

	if m.holders[o.Temp] != nil {
		return true
	}

	e := m.form.Temps[o.Temp].Expr
	if e == nil {
		return false
	}

	found := false
	m.checker.sources(e, func(src ast.Expr, _, _ bool) {
		if _, kind := m.checker.placeOf(src); kind != notPlace {
			found = true
		}
	})
	return found
}

// temporary returns the holder standing for the owned temporary a part
// of o is bound from, the temporary itself, the one a borrow of a part
// of it was made of, or the one a call's result borrows from, when the
// statement drops it; nil for a part of what a variable lends.
func (m *formBorrows) temporary(o Operand) *Var {
	t := m.form.Temps[o.Temp].Origin
	if isBorrow(m.form.Temps[t].Type) {
		return nil // a borrow of a variable's place: what the binding holds is the place's
	}

	return m.holders[t]
}

// dropVar records the drop of a variable where its scope ends or a
// path leaves it, for the reason why.
func (m *formBorrows) dropVar(v *Var, pos token.Pos, why dropWhy) {
	if dropsHere(v) {
		m.checker.flowDrop(v, pos, why, dropRuns(v.typ))
	}
}
