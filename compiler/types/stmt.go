// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// stmts checks a statement list and reports whether it ends in a
// statement that leaves the function, so that a function with results
// can be checked for a missing return as Go does.
func (c *Checker) stmts(list []ast.Stmt) bool {
	leaves := false
	for _, s := range list {
		leaves = c.stmt(s)
		if leaves {
			c.dead = true
		}
	}

	return leaves
}

func (c *Checker) block(b *ast.BlockStmt) bool {
	c.openScope()
	leaves := c.stmts(b.List)
	c.dropScope(c.scope, b.Rbrace, dropBlock)
	c.closeScope()
	return leaves
}

// endScope drops what the current scope declares, at end, and closes it.
func (c *Checker) endScope(end token.Pos) {
	c.dropScope(c.scope, end, dropBlock)
	c.closeScope()
}

// stmt checks one statement and reports whether control cannot continue
// past it: a return, a call that never returns, or a compound statement
// all of whose paths leave.
func (c *Checker) stmt(s ast.Stmt) bool {
	lent := len(c.temps)
	defer func() { c.temps = c.temps[:lent] }() // what the statement lent is given back
	switch s := s.(type) {
	case *ast.BlockStmt:
		return c.block(s)
	case *ast.DeclStmt:
		c.declStmt(s.Decl)
	case *ast.ExprStmt:
		op := c.expr(s.X, nil)
		return op.mode != invalid && isNever(op.typ)
	case *ast.SendStmt:
		c.send(s)
	case *ast.IncDecStmt:
		x := c.expr(s.X, nil)
		if x.mode != invalid && !hasInfo(x.typ, IsNumeric) { // a bare borrow is written through with (*x)++, as x = v rebinds it
			c.errorf(s.TokPos, s.Tok.String()+" is not defined on "+x.typ.String())
		} else if x.mode != invalid && c.writable(s.X, x) {
			c.accessExpr(s.X, writeAccess, accessPos(s.X))
		}
	case *ast.AssignStmt:
		c.assignStmt(s)
	case *ast.SpawnStmt:
		c.spawning, _ = s.Call.(*ast.CallExpr)
		c.expr(s.Call, nil)
		c.spawning = nil
	case *ast.DeferStmt:
		c.deferStmt(s)
	case *ast.ReturnStmt:
		c.returnStmt(s)
		c.flowReturnDrops(s.Return)
		return true
	case *ast.BranchStmt:
		c.branch(s.Tok, s.Pos())
		return true
	case *ast.IfStmt:
		return c.ifStmt(s)
	case *ast.IfBindStmt:
		return c.ifBind(s)
	case *ast.ForStmt:
		return c.forStmt(s)
	case *ast.RangeStmt:
		c.rangeStmt(s)
	case *ast.SelectStmt:
		return c.selectStmt(s)
	}

	return false
}

// declStmt checks var x T = v or const inside a body. The values are
// checked before the names exist, as the right side cannot see them.
func (c *Checker) declStmt(d *ast.VarDecl) {
	var t Type
	if d.Type != nil {
		t = c.typ(d.Type)
	}

	if len(d.Values) == 0 {
		c.declareVars(c.scope, d, t)
		if t == nil {
			c.errorf(d.Pos(), "a declaration without a value needs a type")
		} else {
			c.zeroDecl(d, t)
		}

		return
	}

	ops := c.values(len(d.Names), d.Values, t, d.Pos())
	c.declareVars(c.scope, d, t)
	for i, name := range d.Names {
		c.assign(ops[i], t, d.Values[min(i, len(d.Values)-1)].Pos(), "declaration")
		if t == nil {
			c.setType(c.scope.Lookup(name.Name), inferred(ops[i]))
			c.maybeMove(ops[i], inferred(ops[i]))
		}

		if v, ok := c.scope.Lookup(name.Name).(*Var); ok {
			c.keepParamIn(ops[i], "given to", name, name.Pos())
			c.hold(v, ops[i].expr, name.Pos())
		}
	}
}

// assignStmt checks =, := and the operator assignments. With :=, a
// name not yet declared in the current scope is declared with the
// value's type, one already there is assigned, and at least one must be
// new, as in Go.
func (c *Checker) assignStmt(s *ast.AssignStmt) {
	switch s.Tok {
	case token.DEFINE:
		c.define(s)
	case token.ASSIGN:
		c.plainAssign(s)
	default: // +=, -=, ...
		if len(s.Lhs) != 1 || len(s.Rhs) != 1 {
			c.errorf(s.TokPos, s.Tok.String()+" takes one place and one value")
			c.exprs(s.Lhs)
			c.exprs(s.Rhs)
			return
		}

		x := c.expr(s.Lhs[0], nil)
		if x.mode == invalid {
			return
		}

		if fn := OperatorMethod(valueType(x), s.Tok.Binary(), false); fn != nil {
			c.operatorAssign(s, x, fn)
			return
		}

		y := c.expr(s.Rhs[0], hintOf(x))
		if y.mode == invalid {
			return
		}

		need := IsInteger
		switch s.Tok {
		case token.ADD_ASSIGN:
			need = IsNumeric | IsString
		case token.SUB_ASSIGN, token.MUL_ASSIGN, token.QUO_ASSIGN:
			need = IsNumeric
		}

		if !hasInfo(x.typ, need) { // a bare borrow is written through with *x op= v, as x = v rebinds it
			c.errorf(s.TokPos, s.Tok.String()+" is not defined on "+x.typ.String())
			return
		}

		if c.writable(s.Lhs[0], x) {
			c.accessExpr(s.Lhs[0], writeAccess, accessPos(s.Lhs[0]))
		}

		c.fits(y, x.typ, s.Rhs[0].Pos(), "assignment") // an operator reads its right side; nothing moves
	}
}

// writable reports whether the place x denotes may be written, and
// reports why not: a value reached through a ref is read-only.
// operatorAssign checks a += b on a type with an Add as a = a + b: the
// place must be writable, and the right operand must fit the method's
// parameter. The left operand's value goes into the method and the
// result comes back to the place within the statement, so nothing is
// moved as far as the rest of the function can see; the method returns
// Self, so the result fits the place.
func (c *Checker) operatorAssign(s *ast.AssignStmt, x operand, fn *Func) {
	param := fn.typ.(*Signature).params[0].typ
	if y := c.expr(s.Rhs[0], param); y.mode != invalid {
		c.assign(y, param, s.Rhs[0].Pos(), "the right operand of "+s.Tok.String())
	}

	if c.writable(s.Lhs[0], x) {
		c.accessExpr(s.Lhs[0], writeAccess, accessPos(s.Lhs[0]))
	}
}

func (c *Checker) writable(e ast.Expr, x operand) bool {
	if x.mode != borrowed {
		return true
	}

	c.errorf(e.Pos(), "cannot assign to "+exprText(e)+" through a ref "+c.throughWhat(e))
	return false
}

func (c *Checker) define(s *ast.AssignStmt) {
	var ops []operand
	var one [1]operand // x := e, the common case, allocates no list
	if len(s.Lhs) == 1 && len(s.Rhs) == 1 {
		one[0] = c.expr(s.Rhs[0], nil)
		ops = one[:]
	} else {
		ops = c.values(len(s.Lhs), s.Rhs, nil, s.TokPos) // the right side cannot see the new names
	}

	declared := false
	for i, lhs := range s.Lhs {
		ident, ok := lhs.(*ast.Ident)
		if !ok {
			c.errorf(lhs.Pos(), "non-name on the left side of :=")
			continue
		}

		if isBlank(ident) {
			c.maybeMove(ops[i], inferred(ops[i])) // _ declares nothing and drops the value, so an owned one moves
			continue
		}

		if c.scope.Lookup(ident.Name) != nil {
			c.assignTo(ident, ops[i])
			continue
		}

		c.keepParamIn(ops[i], "given to", ident, ident.Pos())
		c.maybeMove(ops[i], inferred(ops[i]))
		v := NewVar(ident.Pos(), ident.Name, inferred(ops[i]))
		c.declare(c.scope, ident, v)
		c.hold(v, ops[i].expr, ident.Pos())
		declared = true
	}

	if !declared {
		c.errorf(s.TokPos, "no new variables on the left side of :=")
	}
}

// plainAssign checks x = v. With one place and one value the place's
// type is the value's hint, so x = None and s.head = Some(&Node{...})
// type themselves from x.
func (c *Checker) plainAssign(s *ast.AssignStmt) {
	if len(s.Lhs) == 1 && len(s.Rhs) == 1 {
		if isBlank(s.Lhs[0]) {
			x := c.expr(s.Rhs[0], nil) // _ = x drops x, so an owned x moves
			c.maybeMove(x, inferred(x))
			return
		}

		x, holder, mark := c.target(s.Lhs[0], true) // the target is evaluated first, as in Go, and given its value after the value is made
		op := c.expr(s.Rhs[0], hintOf(x))
		c.putAside(mark)
		c.stored(s.Lhs[0], x, holder, op)
		return
	}

	ops := c.values(len(s.Lhs), s.Rhs, nil, s.TokPos)
	for i, lhs := range s.Lhs {
		if isBlank(lhs) {
			c.maybeMove(ops[i], inferred(ops[i]))
		} else {
			c.assignTo(lhs, ops[i])
		}
	}
}

// assignTo checks an assignment of an already checked value to a
// place: the value fits, the place is owned again, and a borrow
// variable holds what the value lends.
func (c *Checker) assignTo(lhs ast.Expr, op operand) {
	x, holder, _ := c.target(lhs, false)
	c.stored(lhs, x, holder, op)
}

// stored finishes an assignment once its two sides are checked: x is
// the target, holder the borrow variable it names, if any, and op the
// value.
func (c *Checker) stored(lhs ast.Expr, x operand, holder *Var, op operand) {
	if x.mode == invalid {
		return
	}

	c.assign(op, x.typ, lhs.Pos(), "assignment")
	c.keepParamIn(op, "assigned to", lhs, lhs.Pos())
	c.revive(lhs)
	p, kind := c.placeOf(lhs)
	if kind != notPlace && c.storeRefused(p, kind, x, op, lhs.Pos()) {
		return
	}

	prefix := ""
	if kind == placeOwned && holdsBorrow(x.typ) {
		prefix = refPrefix(p)
	}

	switch {
	case holder != nil:
		c.hold(holder, op.expr, lhs.Pos())
	case kind == placeOwned && p.path == "" && isSignature(x.typ):
		if op.expr != nil && c.usedWhereMade(op.expr, "assigned with =", lhs.Pos()) {
			return
		}

		c.flowAssign(p.v, "")
	case kind == placeOwned && holdsBorrow(x.typ) && prefix != "":
		part := prefix + ".*" + strings.TrimPrefix(p.path, prefix) // w.m.src = ref t, with w.m a mut borrow of h: h holds t
		c.flowAssign(p.v, part)
		c.holdIn(p.v, part, x.typ, op.expr, lhs.Pos())
		c.flowThrough(p.v, prefix, part, lhs.Pos())
	case kind == placeOwned && holdsBorrow(x.typ):
		if dropReads(x.typ) {
			c.flowUse(p.v, lhs.Pos()) // the old value's Drop runs here and reads what it borrows
		}

		c.flowAssign(p.v, p.path)                        // the part given a value lets go of what it held
		c.holdIn(p.v, p.path, x.typ, op.expr, lhs.Pos()) // h.src = ref t: h holds t
	case kind == placeBorrowed && holdsBorrow(x.typ):
		part := joinPath("*", p.path) // m.src = ref t, through m := mut h: h holds t
		c.flowAssign(p.v, part)
		c.holdIn(p.v, part, x.typ, op.expr, lhs.Pos())
		c.flowThrough(p.v, "", part, lhs.Pos())
	case kind == placeIndexed && holdsBorrow(x.typ):
		c.holdIn(p.v, p.path, x.typ, op.expr, lhs.Pos()) // an element, of an index not known here, lets go of nothing
	default:
		if _, ok := lhs.(*ast.Ident); !ok {
			c.kept(op, "stored", lhs.Pos()) // a field or element keeps the value
		}
	}
}

// storeRefused reports, and refuses, a closure's body storing a borrow
// in a variable from outside it: what the closure borrows ends with
// each call.
func (c *Checker) storeRefused(p place, kind placeKind, x, op operand, pos token.Pos) bool {
	f := c.flow
	if f == nil || !c.bringsBorrow(x.typ, op) && !(isSignature(x.typ) && c.closureBorrows(op)) {
		return false
	}

	if f.outer != token.NoPos && !p.v.global && p.v.pos != token.NoPos && p.v.pos < f.outer {
		c.errorf(pos, "cannot store a borrow in "+p.v.name+" from inside a closure: "+p.v.name+" is declared outside it, and what the closure borrows ends with each call")
		return true
	}

	return false // a store that reaches the caller's places is refused on the graph, where copies and stores through a borrow are followed (checkEscape)
}

// bringsBorrow reports whether the value op, of type t, brings a
// borrow with it: one lent or read out of some place, Some(ref s) or r,
// against None, which carries nothing whatever its type holds.
func (c *Checker) bringsBorrow(t Type, op operand) bool {
	if !holdsBorrow(t) || op.expr == nil {
		return false
	}

	if c.capturesOf(op.expr) != nil {
		return true
	}

	found := false
	c.sources(op.expr, func(src ast.Expr, _ bool) {
		if id, ok := unparen(src).(*ast.Ident); ok {
			if _, isVar := c.resolve(id.Name).(*Var); !isVar {
				return // a variant or a constant, not a place
			}
		}

		found = true
	})

	return found
}

// refPrefix returns the part of p's path that names a borrow, w.m in
// w.m.src with w.m a mut borrow, where a store through it is a store
// into what it points to; "" for a path through no borrow.
func refPrefix(p place) string {
	if p.path == "" {
		return ""
	}

	parts := strings.Split(p.path, ".")
	for i := 1; i < len(parts); i++ {
		prefix := strings.Join(parts[:i], ".")
		if isBorrow(placeType(place{p.v, prefix})) {
			return prefix
		}
	}

	return ""
}

// closureBorrows reports whether a function value borrows: a closure
// literal that captures by borrow, or a variable holding one.
func (c *Checker) closureBorrows(op operand) bool {
	return op.expr != nil && c.closureExprBorrows(op.expr)
}

// closureExprBorrows reports whether e is a function value that
// borrows: a closure literal that captures by borrow, or a variable
// holding one.
func (c *Checker) closureExprBorrows(e ast.Expr) bool {
	if c.capturesOf(e) != nil {
		return true
	}

	if id, ok := unparen(e).(*ast.Ident); ok {
		if v, ok := c.resolve(id.Name).(*Var); ok && isSignature(v.typ) {
			return c.heldBy(v, e.Pos()) != nil
		}
	}

	return false
}

// usedWhereMade refuses a closure that borrows anywhere but where it is
// made: bound with :=, called, or passed straight to a call. What it
// borrows is then known where it is used, with nothing to follow.
func (c *Checker) usedWhereMade(e ast.Expr, how string, pos token.Pos) bool {
	if !c.closureExprBorrows(e) {
		return false
	}

	c.errorf(pos, "a closure that borrows cannot be "+how+": it is used where it is made, bound with :=, called, or passed to a call")
	return true
}

// target checks the place an assignment writes, which is not a use of
// the value there but an access to it, and returns the variable when
// the place is a borrow variable: the assignment rebinds it.
//
// With after, the store itself is set aside, to be recorded once the
// value is checked, and the mark to put it back at is returned.
func (c *Checker) target(e ast.Expr, after bool) (operand, *Var, int) {
	c.noUse = true
	x := c.expr(e, nil)
	c.noUse = false
	mark := -1
	if after {
		mark = c.setAside()
		defer c.endAside()
	}

	if x.mode == invalid || !c.writable(e, x) {
		return x, nil, mark
	}

	return x, c.accessExpr(e, writeAccess, accessPos(e)), mark
}

func (c *Checker) send(s *ast.SendStmt) {
	ch := c.expr(s.Chan, nil)
	if ch.mode == invalid {
		c.expr(s.Value, nil)
		return
	}

	t, ok := readType(ch).Underlying().(*Chan)
	if !ok {
		c.errorf(s.Arrow, "cannot send on a value of type "+ch.typ.String())
		c.expr(s.Value, nil)
		return
	}

	v := c.expr(s.Value, t.elem)
	c.assign(v, t.elem, s.Value.Pos(), "send")
	c.kept(v, "sent", s.Value.Pos())
}

// returnStmt checks the results against the function's result type:
// none for (), one value, or one tuple value or a list for several.
func (c *Checker) returnStmt(s *ast.ReturnStmt) {
	want := c.sig.results
	switch {
	case len(s.Results) == 0:
		if want != Unit {
			c.errorf(s.Return, "return needs a value of type "+want.String())
		}
	case len(s.Results) == 1:
		op := c.expr(s.Results[0], want)
		c.assign(op, want, s.Results[0].Pos(), "return")
		c.returned(s.Results[0], op)
	default:
		t, ok := want.(*Tuple)
		if !ok || len(t.elems) != len(s.Results) {
			c.errorf(s.Return, "return has "+strconv.Itoa(len(s.Results))+" values; the function returns "+want.String())
			c.exprs(s.Results)
			return
		}

		for i, r := range s.Results {
			op := c.expr(r, t.elems[i])
			c.assign(op, t.elems[i], r.Pos(), "return")
			c.returned(r, op)
		}
	}
}

func (c *Checker) ifStmt(s *ast.IfStmt) bool {
	c.openScope()
	defer c.endScope(s.Body.Rbrace)
	if s.Init != nil {
		c.stmt(s.Init)
	}

	c.condition(s.Cond)
	f := c.forkAt()
	leaves := c.block(s.Body)
	return c.elseBranch(&f, s.Else, leaves)
}

// elseBranch ends the body of an if, checks its else if there is one,
// and joins the two: the statement leaves only when both do.
func (c *Checker) elseBranch(f *fork, els ast.Stmt, bodyLeaves bool) bool {
	c.branchEnd(f)
	if els == nil {
		c.joinAt(f, false)
		return false
	}

	leaves := c.stmt(els)
	c.branchEnd(f)
	c.joinAt(f, true)
	return leaves && bodyLeaves
}

// ifBind checks `if pat := value {`: the value is an option, and the
// body sees the pattern's names bound to what is inside. A borrowed
// option binds borrows.
func (c *Checker) ifBind(s *ast.IfBindStmt) bool {
	x := c.expr(s.Value, nil)
	outer := c.scope
	c.openScope()
	c.bindsOwned = false
	if x.mode != invalid {
		inner, m := deref(x)
		if o, ok := inner.Underlying().(*Option); ok {
			b := borrowOf(m)
			c.copies = !isBorrow(x.typ)
			c.pattern(s.Pat, o.elem, b)
			c.resolvingIn(outer, func() { c.bindThrough(s.Pat, s.Value, b) }) // if o := o binds a new o out of the old
		} else {
			c.errorf(s.Value.Pos(), "if x := v binds an option; "+x.typ.String()+" is not one")
			c.pattern(s.Pat, Typ[Invalid], nil)
		}
	} else {
		c.pattern(s.Pat, Typ[Invalid], nil)
	}

	if c.bindsOwned && held(s.Value, x.mode) {
		c.resolvingIn(outer, func() { c.move(s.Value, variable) }) // a part bound by value leaves the place; a temporary's part may not leave it
	}

	f := c.forkAt()
	leaves := c.block(s.Body)
	c.endScope(s.Body.Rbrace)
	return c.elseBranch(&f, s.Else, leaves)
}

// borrowOf turns the mode of a scrutinee into the borrow its bindings
// get: none for an owned value, ref or mut for a borrowed place.
func borrowOf(m mode) *Ref {
	switch m {
	case borrowed:
		return &Ref{nil, false}
	case mutBorrowed:
		return &Ref{nil, true}
	}

	return nil
}

func (c *Checker) forStmt(s *ast.ForStmt) bool {
	c.openScope()
	defer c.closeScope()
	if s.Init != nil {
		c.stmt(s.Init)
	}

	c.enterRegion(true, s.Cond == nil, s.For)
	if s.Cond != nil {
		c.condition(s.Cond)
	}

	c.flowPost(s)
	c.block(s.Body)
	c.leaveRegion()
	c.dropScope(c.scope, s.Body.Rbrace, dropBlock)
	return s.Cond == nil && !hasBreak(s.Body.List)
}

// flowPost checks a for loop's post statement, which the walk checks
// before the body but which runs after it, each pass: in the graph it
// is a block of its own between the body's end, or a continue, and
// the loop's head, and the body begins after the test.
func (c *Checker) flowPost(s *ast.ForStmt) {
	f := c.flow
	if f == nil {
		if s.Post != nil {
			c.stmt(s.Post)
		}

		return
	}

	r := &c.regions[len(c.regions)-1]
	test := f.cur
	if s.Post != nil {
		r.cont = f.newBlock()
		f.cur = r.cont
		c.stmt(s.Post)
		f.edge(f.cur, r.head)
		f.cur = test
	}

	c.flowLoopBody()
}

// rangeStmt checks for k, v := range x. A slice or array yields index
// and element, a map key and value, a string index and byte, a channel
// or an iterator each item. Ranging over a borrowed collection yields
// borrows of its elements; ranging over an owned one moves them out.
func (c *Checker) rangeStmt(s *ast.RangeStmt) {
	x := c.expr(s.X, nil)
	c.openScope()
	defer c.closeScope()
	var key, val Type = Typ[Invalid], Typ[Invalid]
	var loopHolder *Var
	var places []place
	var placesMut bool
	if x.mode != invalid {
		var ok bool
		if key, val, ok = rangeTypes(x); !ok {
			c.errorf(s.X.Pos(), "cannot range over a value of type "+x.typ.String())
		} else if val == Typ[Invalid] && s.Value != nil { // a channel's or an iterator's one variable: the value, or the item
			c.errorf(s.Value.Pos(), "range over "+exprText(s.X)+" permits only one iteration variable")
			key = Typ[Invalid]
		}

		mut, lent := borrowIn(x.typ)
		switch {
		case x.mode == variable && isChan(readType(x)):
			loopHolder, places, placesMut = c.holdRange(s, mut) // a handle is lent for the loop, never consumed: the loop receives through a ref of it, as every operation on a channel does
		case x.mode == variable && isString(readType(x)):
			loopHolder, places, placesMut = c.holdRange(s, false) // a string is lent for the loop: its runes are copies, so consuming it would gain nothing
		case held(s.X, x.mode) && !isBorrow(x.typ):
			c.maybeMove(x, x.typ) // ranging over an owned collection by value consumes it; through a borrow it lends
		case lent || x.mode != value:
			loopHolder, places, placesMut = c.holdRange(s, mut || x.mode == mutBorrowed) // the collection, or what an iterator borrows, is lent for the loop
		}
	}

	c.enterRegion(true, false, s.For)
	r := &c.regions[len(c.regions)-1]
	r.floor, r.end = c.scope.parent, s.Body.Rbrace // the variables are the pass's own, dropped at its end
	if loopHolder != nil {
		c.flowUse(loopHolder, s.For) // each pass reads the collection the loop holds
	}

	c.flowLoopBody()
	if s.Tok == token.DEFINE {
		c.rangeVar(s.Key, key)
		c.rangeVar(s.Value, val)
	} else {
		c.rangePlace(s.Key, key)
		c.rangePlace(s.Value, val)
	}

	c.holdElements(s, places, placesMut)

	c.block(s.Body)
	c.leaveRegion()
}

// holdRange records the borrow a range loop holds of its collection
// for every pass: the loop reads or changes the elements in turn, so
// nothing may change the collection under it. The loop's own variable
// holds it, read at the start of each pass; it is returned, with the
// places lent and how.
func (c *Checker) holdRange(s *ast.RangeStmt, mut bool) (*Var, []place, bool) {
	var holder *Var
	var lent []place
	for _, src := range c.borrowSources(s.X) {
		if p, kind := through(c.placeOf(src)); kind != notPlace {
			if holder == nil {
				holder = NewVar(s.For, "", Typ[Invalid])
			}

			c.flowLoan(borrow{holder: holder, place: p, mut: mut, pos: s.For, loop: true})
			lent = append(lent, p)
		}
	}

	return holder, lent, mut
}

// holdElements records that a range loop's variables that are borrows,
// an element of a borrowed collection or an iterator's item, hold the
// collection: a use of one keeps it lent, past a break.
func (c *Checker) holdElements(s *ast.RangeStmt, lent []place, mut bool) {
	if len(lent) == 0 {
		return
	}

	for _, e := range []ast.Expr{s.Key, s.Value} {
		id, ok := e.(*ast.Ident)
		if !ok || isBlank(id) {
			continue
		}

		v, ok := c.resolve(id.Name).(*Var)
		if !ok || !holdsBorrow(v.typ) {
			continue
		}

		for _, p := range lent {
			c.flowLoan(borrow{holder: v, place: p, mut: mut, pos: id.Pos()})
		}
	}
}

// deferStmt checks a defer: what the deferred call borrows, its
// receiver, its arguments and what a deferred closure captures, is held
// until the function returns, when the call runs.
func (c *Checker) deferStmt(s *ast.DeferStmt) {
	call, ok := unparen(s.Call).(*ast.CallExpr)
	if !ok {
		c.expr(s.Call, nil)
		return
	}

	saved, savedSig := c.deferring, c.deferSig
	c.deferring, c.deferSig = call, nil
	c.expr(s.Call, nil)
	sig := c.deferSig
	c.deferring, c.deferSig = saved, savedSig
	holder := c.flowDeferred(s.Pos())
	if holder == nil {
		return
	}

	switch fun := unparen(call.Fun).(type) {
	case *ast.FuncLit:
		for _, cap := range c.captured[fun] {
			if cap.mode != byRef && cap.mode != byMut {
				continue
			}

			if holdsBorrow(cap.v.typ) && !isBorrow(cap.v.typ) {
				c.errorf(s.Pos(), "a deferred call borrows only a whole variable, one that is a borrow or holds none: the closure captures "+cap.v.name+", which holds one")
				continue
			}

			b := cap.borrow(holder, s.Pos())
			b.atExit = true // the closure reaches the variable when it runs, after every other use
			c.flowLoan(b)
		}
	case *ast.Ident:
		if c.closureExprBorrows(fun) {
			c.errorf(s.Pos(), "a deferred call cannot call "+fun.Name+": it is a closure that borrows; defer the closure itself, func() { ... }()")
		}
	}

	if sig == nil {
		return
	}

	if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sig.recv != nil && holdsBorrow(sig.recv.typ) {
		c.deferHold(holder, sig.recv.typ, sel.X, s.Pos())
	}

	for i, arg := range call.Args {
		if i >= len(sig.params) {
			break
		}

		if t := sig.params[i].typ; holdsBorrow(t) || isSignature(t) && c.closureExprBorrows(arg) {
			c.deferHold(holder, t, arg, s.Pos())
		}
	}
}

// deferHold records what a deferred call borrows through the receiver
// or argument e, of type t. It may borrow only a whole variable: one
// that holds no borrow, t in defer t.Add(5) or s in defer show(ref s),
// which the call reaches when the function returns, after every other
// use, so it is kept from being moved or dropped and free to be read
// and changed until then; or a borrow variable, s in defer s.cleanup()
// with s a mut receiver, which the call reads then, so it may not be
// pointed elsewhere before, and what it points to is held as the
// variable is. Anything else, an element, what a borrow points to, a
// value that holds a borrow or a closure that borrows, is refused: what
// it points into may move or die before the call runs.
func (c *Checker) deferHold(holder *Var, t Type, e ast.Expr, pos token.Pos) {
	x := unparen(e)
	if u, ok := x.(*ast.UnaryExpr); ok && (u.Op == token.REF || u.Op == token.MUT) {
		x = unparen(u.X)
	}

	if id, ok := x.(*ast.Ident); ok && !isSignature(t) {
		if p, kind := c.placeOf(id); kind == placeOwned {
			switch {
			case isBorrow(p.v.typ):
				c.flowLoan(borrow{holder: holder, place: p, mut: isMut(p.v.typ), pos: pos, atExit: true, variable: true})
				return
			case !holdsBorrow(p.v.typ):
				mut, _ := borrowIn(t)
				c.flowLoan(borrow{holder: holder, place: p, mut: mut, pos: pos, atExit: true})
				return
			}
		}
	}

	fix := "take the value, or call it before the function returns"
	if isSignature(t) {
		fix = "defer a closure that makes the call, func() { ... }()"
	}

	c.errorf(e.Pos(), "a deferred call borrows only a whole variable, one that is a borrow or holds none: "+exprText(e)+" is not one; "+fix)
}

// rangeTypes returns what the two range variables get for a value, or
// false when the value cannot be ranged over.
func rangeTypes(x operand) (key, val Type, ok bool) {
	base, m := deref(x)
	b := borrowOf(m)
	switch t := base.Underlying().(type) {
	case *Slice:
		if isIterator(t) {
			return t.elem, Typ[Invalid], true // one variable: the item, as for a channel
		}

		return Typ[Int], wrap(t.elem, b), true
	case *Array:
		return Typ[Int], wrap(t.elem, b), true
	case *Map:
		return wrap(t.key, b), wrap(t.value, b), true
	case *Chan:
		if t.dir == SendOnly {
			return Typ[Invalid], Typ[Invalid], false
		}

		return t.elem, Typ[Invalid], true // one variable: the value received, owned
	case *Basic:
		if t.kind == String {
			return Typ[Int], aliases[1], true // the offset each rune starts at, and the rune
		}
	}

	return Typ[Invalid], Typ[Invalid], false
}

// isIterator reports whether a slice type stands for an iterator: the
// library types Fields and Split as a slice of borrows, a type no
// program can write, until the iterator trait exists.
func isIterator(t *Slice) bool {
	_, ok := t.elem.(*Ref)
	return ok
}

// wrap gives an element the borrow of the collection it came from.
func wrap(t Type, b *Ref) Type {
	if b == nil {
		return t
	}

	return NewRef(t, b.mut)
}

func (c *Checker) rangeVar(e ast.Expr, t Type) {
	if e == nil {
		return
	}

	ident, ok := e.(*ast.Ident)
	if !ok {
		c.errorf(e.Pos(), "non-name on the left side of range :=")
		return
	}

	c.declare(c.scope, ident, NewVar(ident.Pos(), ident.Name, t))
}

func (c *Checker) rangePlace(e ast.Expr, t Type) {
	if e == nil || isBlank(e) {
		return
	}

	x, _, _ := c.target(e, false)
	if x.mode != invalid {
		c.assign(operand{value, t, nil, nil}, x.typ, e.Pos(), "range assignment")
		c.revive(e)
	}
}

func (c *Checker) selectStmt(s *ast.SelectStmt) bool {
	allLeave := true
	c.enterRegion(false, false, s.Select)
	f := c.forkAt()
	for _, cc := range s.Body {
		c.openScope()
		if cc.Comm != nil {
			c.stmt(cc.Comm)
		}

		if !c.stmts(cc.Body) {
			allLeave = false
		}

		c.endScope(cc.Case)
		c.branchEnd(&f)
	}

	c.joinAt(&f, true)
	c.leaveRegion()
	return allLeave
}

// node checks whatever a match arm body may be: an expression with a
// hint, a block, or a statement. It returns the arm's value: the type
// never for a body that leaves, () for an ordinary statement.
func (c *Checker) node(n ast.Node, expected Type) operand {
	switch n := n.(type) {
	case ast.Expr:
		return c.expr(n, expected)
	case *ast.BlockStmt:
		return c.blockValue(n, expected)
	case ast.Stmt:
		if c.stmt(n) {
			c.dead = true
			return operand{value, Typ[Never], nil, nil}
		}

		return operand{value, Unit, nil, nil}
	}

	return invalidOperand
}

func (c *Checker) exprs(es []ast.Expr) {
	for _, e := range es {
		c.expr(e, nil)
	}
}

// hasBreak reports whether a break in the list leaves the enclosing
// loop: one not inside a nested loop. A match or select is not a loop,
// so a break in one of their arms or cases counts.
func hasBreak(list []ast.Stmt) bool {
	for _, s := range list {
		switch s := s.(type) {
		case *ast.BranchStmt:
			if s.Tok == token.BREAK {
				return true
			}
		case *ast.BlockStmt:
			if hasBreak(s.List) {
				return true
			}
		case *ast.SelectStmt:
			for _, cc := range s.Body {
				if hasBreak(cc.Body) {
					return true
				}
			}
		case *ast.IfStmt:
			if hasBreak(s.Body.List) || s.Else != nil && hasBreak([]ast.Stmt{s.Else}) {
				return true
			}
		case *ast.IfBindStmt:
			if hasBreak(s.Body.List) || s.Else != nil && hasBreak([]ast.Stmt{s.Else}) {
				return true
			}
		case *ast.ExprStmt:
			if m, ok := s.X.(*ast.MatchExpr); ok && matchHasBreak(m) {
				return true
			}
		}
	}

	return false
}

func matchHasBreak(m *ast.MatchExpr) bool {
	for _, arm := range m.Arms {
		switch b := arm.Body.(type) {
		case *ast.BranchStmt:
			if b.Tok == token.BREAK {
				return true
			}
		case *ast.BlockStmt:
			if hasBreak(b.List) {
				return true
			}
		}
	}

	return false
}
