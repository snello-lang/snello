// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"slices"
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// assignedInPlace is how many places an assignment or declaration has
// before the list of what their values carry is allocated: nearly every
// one has fewer.
const assignedInPlace = 4

// stmts checks a statement list and reports whether it ends in a
// statement that leaves the function, so that a function with results
// can be checked for a missing return as Go does.
func (c *Checker) stmts(list []ast.Stmt) bool {
	leaves := false
	for _, s := range list {
		leaves = c.stmt(s)
	}

	return leaves
}

func (c *Checker) block(b *ast.BlockStmt) bool {
	c.openScope()
	leaves := c.stmts(b.List)
	c.closeScope()
	return leaves
}

// stmt checks one statement and reports whether control cannot continue
// past it: a return, a call that never returns, or a compound statement
// all of whose paths leave.
func (c *Checker) stmt(s ast.Stmt) bool {
	if unfinished(s) { // the parser reported it, and a load that checks what it recovered gives it here
		return false
	}

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
		} else if x.mode != invalid {
			c.writable(s.X, x)
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

	if d.Const {
		c.localConsts(d, t)
		return
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

	ops := c.values(len(d.Names), d.Values, t, d.Pos(), nil)
	objs := c.declareVars(c.scope, d, t)
	spread := len(d.Values) == 1 && len(d.Names) > 1
	for i, name := range d.Names {
		c.assign(ops[i], t, d.Values[min(i, len(d.Values)-1)].Pos(), "declaration")
		if t == nil {
			c.setType(objs[i], inferred(ops[i])) // the variable declared, the blank name's too, which no scope holds
		}

		from := ops[i].expr
		if spread {
			from = d.Values[0] // a part of a tuple taken apart holds what the tuple lends, as with :=
		}

		_, ok := objs[i].(*Var)
		if !ok || isBlank(name) { // the blank name holds nothing: its value is dropped once the statement's values are made
			c.refuseBlankGuard(ops[i], from, name.Pos())
			continue
		}

		c.keepParamIn(ops[i], "given to", name, name.Pos())
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

		c.writable(s.Lhs[0], x)
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

	c.writable(s.Lhs[0], x)
}

// writable refuses an assignment to e, typed as x, that writes nowhere a
// program can read again: a place reached through a ref, which only
// reads, and a value an expression makes with no place behind it, a
// call, a ?? or an if, which the statement would drop with what was
// stored in it. A map's entry, a pointer's target and the blank name
// are places.
func (c *Checker) writable(e ast.Expr, x operand) {
	if x.mode == borrowed {
		c.errorf(e.Pos(), "cannot assign to "+exprText(e)+" through a ref "+c.throughWhat(e))
		return
	}

	if id, ok := unparen(e).(*ast.Ident); x.mode != value || ok && isBlank(id) {
		return
	}

	switch _, kind := c.placeOf(e); {
	case kind != notPlace:
	case c.static(e):
		c.errorf(e.Pos(), "cannot assign to "+exprText(e)+": a constant or a literal is no place")
	default:
		c.errorf(e.Pos(), "cannot assign to "+temporaryText(e)+": it is no place, but a value the expression makes, which the statement would drop with what it stores")
	}
}

func (c *Checker) define(s *ast.AssignStmt) {
	var ops []operand
	var one [1]operand // x := e, the common case, allocates no list
	if len(s.Lhs) == 1 && len(s.Rhs) == 1 {
		one[0] = c.expr(s.Rhs[0], nil)
		ops = one[:]
	} else {
		ops = c.values(len(s.Lhs), s.Rhs, nil, s.TokPos, func(i int) Type { // the right side cannot see the new names
			if id, ok := s.Lhs[i].(*ast.Ident); ok && !isBlank(id) {
				if v, ok := c.scope.Lookup(id.Name).(*Var); ok {
					return v.typ // a name := reuses is assigned
				}
			}

			return nil
		})
	}

	declared := false
	for i, lhs := range s.Lhs {
		ident, ok := lhs.(*ast.Ident)
		if !ok {
			c.errorf(lhs.Pos(), "non-name on the left side of :=")
			continue
		}

		if isBlank(ident) {
			c.refuseBlankGuard(ops[i], sourceOf(s, ops[i]), ident.Pos())
			continue
		}

		if c.scope.Lookup(ident.Name) != nil {
			c.assignTo(ident, ops[i])
			continue
		}

		c.keepParamIn(ops[i], "given to", ident, ident.Pos())
		v := NewVar(ident.Pos(), ident.Name, inferred(ops[i]))
		c.declare(c.scope, ident, v)
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
			c.refuseBlankGuard(x, s.Rhs[0], s.Lhs[0].Pos())
			return
		}

		x := c.target(s.Lhs[0]) // the place is typed first, for the value's hint
		op := c.expr(s.Rhs[0], hintOf(x))
		c.stored(s.Lhs[0], x, op)
		return
	}

	var places [assignedInPlace]operand
	targets := places[:0]
	for _, lhs := range s.Lhs { // each place is typed first, for its value's hint
		var x operand
		if !isBlank(lhs) {
			x = c.target(lhs)
		}

		targets = append(targets, x)
	}

	ops := c.values(len(s.Lhs), s.Rhs, nil, s.TokPos, func(i int) Type {
		if isBlank(s.Lhs[i]) {
			return nil
		}

		return hintOf(targets[i])
	})
	for i, lhs := range s.Lhs {
		if isBlank(lhs) {
			c.refuseBlankGuard(ops[i], sourceOf(s, ops[i]), lhs.Pos())
			continue
		}

		c.stored(lhs, targets[i], ops[i])
	}
}

// refuseBlankGuard refuses the blank name given a value made where it
// stands, from, whose Drop may read what it borrows, a lock's guard,
// m.Lock(): the blank drops it once the statement's values are made,
// so what it guards would end as it begins. A variable dropped into the
// blank, _ = g, is how one is ended early, and stays allowed.
func (c *Checker) refuseBlankGuard(op operand, from ast.Expr, pos token.Pos) {
	if op.mode == invalid || from == nil {
		return
	}

	if _, kind := c.placeOf(from); kind != notPlace {
		return
	}

	if t := valueType(op); guards(t) {
		c.errorf(pos, "the blank name would drop this "+t.String()+" at once, and its Drop ends what it borrows: give it a name to keep it to the end of the scope")
	}
}

// sourceOf returns the expression the value op of the assignment s
// comes from: its own, or the tuple a part of one taken apart, a, b :=
// t, comes from.
func sourceOf(s *ast.AssignStmt, op operand) ast.Expr {
	if len(s.Rhs) == 1 && len(s.Lhs) > 1 {
		return s.Rhs[0]
	}

	return op.expr
}

// assignTo checks an assignment of an already checked value to a
// place: the value fits, the place is owned again, and a borrow
// variable holds what the value lends.
func (c *Checker) assignTo(lhs ast.Expr, op operand) {
	c.stored(lhs, c.target(lhs), op)
}

// stored finishes the typing of an assignment once its two sides are
// checked: x is the target and op the value, which must fit it.
func (c *Checker) stored(lhs ast.Expr, x operand, op operand) {
	if x.mode == invalid {
		return
	}

	if id, ok := unparen(lhs).(*ast.Ident); ok && c.heldVariant(id) {
		c.errorf(lhs.Pos(), "cannot assign to "+id.Name+": the arm written for each variant holds it, and v.[f] reads the variant it holds")
		return
	}

	c.assign(op, x.typ, lhs.Pos(), "assignment")
	c.keepParamIn(op, "assigned to", lhs, lhs.Pos())
	if _, ok := lhs.(*ast.Ident); !ok && !holdsBorrow(x.typ) {
		c.kept(op, "stored", lhs.Pos()) // a field or element keeps the value
	}
}

// storeHolds records what the place p, of type t and reached as kind,
// holds once a value is stored in it, by hold, given the part of p's
// variable the value goes into, and what it lets go of: the part given
// a value, a store through a borrow reaching what the borrow points to;
// prefix is the part of the path that is a borrow, when one is, which
// the store goes through.
func (c *Checker) storeHolds(p place, kind placeKind, prefix string, t Type, hold func(part string), pos token.Pos) {
	switch {
	case kind == placeOwned && prefix != "":
		part := prefix + ".*" + strings.TrimPrefix(p.path, prefix) // w.m.src = ref t, with w.m a mut borrow of h: h holds t
		c.flowAssign(p.v, part)
		hold(part)
		c.flowThrough(p.v, prefix, part, pos)
	case kind == placeOwned:
		if dropReads(t) {
			c.flowUse(p.v, pos) // the old value's Drop runs here and reads what it borrows
		}

		c.flowAssign(p.v, p.path) // the part given a value lets go of what it held
		hold(p.path)              // h.src = ref t: h holds t
	case kind == placeBorrowed:
		part := joinPath("*", p.path) // m.src = ref t, through m := mut h: h holds t
		c.flowAssign(p.v, part)
		hold(part)
		c.flowThrough(p.v, "", part, pos)
	case kind == placeIndexed:
		hold(p.path) // an element, of an index not known here, lets go of nothing
	}
}

// closureStore reports, and refuses, a closure's body storing a borrow
// in the place p of a variable from outside the closure, at pos.
func (c *Checker) closureStore(p place, pos token.Pos) bool {
	f := c.flow
	if f.outer != token.NoPos && !p.v.global && p.v.pos != token.NoPos && p.v.pos < f.outer {
		c.errorf(pos, "cannot store a borrow in "+p.v.name+" from inside a closure: "+p.v.name+" is declared outside it, and what the closure borrows ends with each call")
		return true
	}

	return false
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

// target types the place an assignment writes, before the assignment's
// value, which takes its type as a hint: the typing of the place is no
// use of the value there, and whether it may be written is checked here.
// What the store does to what the place holds, and in what order the
// place is reached beside the value, the passes over the form decide.
func (c *Checker) target(e ast.Expr) operand {
	c.noUse = true
	x := c.expr(e, nil)
	c.noUse = false
	if x.mode != invalid {
		c.writable(e, x)
	}

	return x
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

	if t.dir == RecvOnly {
		c.errorf(s.Arrow, "cannot send on a receive-only channel")
		c.expr(s.Value, nil)
		return
	}

	v := c.expr(s.Value, t.elem)
	c.assign(v, t.elem, s.Value.Pos(), "send")
	c.kept(v, "sent", s.Value.Pos())
}

// returnStmt checks the result against the function's result type:
// none for (), or one value, a tuple for several, return a, b being
// parsed as return (a, b). A tuple of the wrong length is reported by
// its count, whichever way it is written.
func (c *Checker) returnStmt(s *ast.ReturnStmt) {
	if c.sig == nil { // a package variable's initializer, which has no function to return from: x := v ?? return, or a match arm's
		c.errorf(s.Return, "return is only allowed inside a function")
		c.exprs(s.Results)
		return
	}

	want := c.refine(c.sig.results)
	if len(s.Results) == 0 {
		if want != Unit {
			c.errorf(s.Return, "return needs a value of type "+want.String())
		}

		return
	}

	if tuple, ok := s.Results[0].(*ast.TupleExpr); ok && len(tuple.Elts) > 1 { // a tuple of the wrong length is told by its count; any other mismatch, by the assignment
		if t, ok := want.Underlying().(*Tuple); ok && len(t.elems) != len(tuple.Elts) {
			c.errorf(s.Return, "return has "+strconv.Itoa(len(tuple.Elts))+" values; the function returns "+want.String())
			c.exprs(tuple.Elts)
			return
		}
	}

	op := c.expr(s.Results[0], want)
	c.assign(op, want, s.Results[0].Pos(), "return")
	c.returned(s.Results[0], op)
}

func (c *Checker) ifStmt(s *ast.IfStmt) bool {
	c.openScope()
	defer c.closeScope()
	if s.Init != nil {
		c.stmt(s.Init)
	}

	c.condition(s.Cond)
	leaves := c.block(s.Body)
	return c.elseBranch(s.Else, leaves)
}

// elseBranch checks the else of an if, when there is one, and reports
// whether the statement leaves: only when both its branches do.
func (c *Checker) elseBranch(els ast.Stmt, bodyLeaves bool) bool {
	if els == nil {
		return false
	}

	leaves := c.stmt(els)
	return leaves && bodyLeaves
}

// ifBind checks `if pat := value {`: the value is an option, and the
// body sees the pattern's names bound to what is inside. A borrowed
// option binds borrows.
func (c *Checker) ifBind(s *ast.IfBindStmt) bool {
	x := c.expr(s.Value, nil)
	outer := c.scope
	c.openScope()
	if x.mode != invalid {
		inner, m := deref(x)
		if o, ok := inner.Underlying().(*Option); ok {
			b := borrowOf(m)
			c.copies = !isBorrow(x.typ)
			c.pattern(s.Pat, o.elem, b)
			c.refuseMutMapBinding(s.Value, b, outer) // if o := o binds a new o out of the old
		} else {
			c.errorf(s.Value.Pos(), "if x := v binds an option; "+x.typ.String()+" is not one")
			c.pattern(s.Pat, Typ[Invalid], nil)
		}
	} else {
		c.pattern(s.Pat, Typ[Invalid], nil)
	}

	leaves := c.block(s.Body)
	c.closeScope()
	return c.elseBranch(s.Else, leaves)
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
	c.enterLoop(false)
	defer c.leaveLoop()
	if s.Init != nil {
		c.stmt(s.Init)
	}

	if s.Cond != nil {
		c.condition(s.Cond)
	}

	if s.Post != nil {
		c.stmt(s.Post)
	}

	c.block(s.Body)
	return s.Cond == nil && !hasBreak(s.Body.List)
}

// rangeStmt checks for k, v := range x. A slice or array yields index
// and element, a map key and value, a string index and byte, a channel
// each value, and an iterator each item. Ranging over a borrowed
// collection yields borrows of its elements; ranging over an owned one
// moves them out.
//
// An iterator is a value whose type has a method Next on a mut
// receiver, taking nothing and returning U?: its shape makes it one,
// and no trait names it. The loop calls Next until it gives None, and
// yields each U, or the two parts of a U that is a pair, for i, v :=
// range a.Iter(). An iterator that is a variable is consumed, as a
// slice is, unless it holds a borrow or is Copy, when it is lent mut
// for the loop, as range mut it lends it, since Next changes it where
// it lies; one that is a call's result lives for the loop and is
// dropped after it.
func (c *Checker) rangeStmt(s *ast.RangeStmt) {
	if call, b := c.partsCall(s.X); call != nil {
		c.partsLoop(s, call, b)
		return
	}

	x := c.expr(s.X, nil)
	c.openScope()
	defer c.closeScope()
	c.enterLoop(false)
	defer c.leaveLoop()
	var key, val Type = Typ[Invalid], Typ[Invalid]
	if x.mode != invalid {
		var ok bool
		item, iterator, shared := iteratorItem(x)
		switch {
		case iterator && shared:
			c.errorf(s.X.Pos(), "cannot range over "+exprText(s.X)+": Next changes the iterator, which a ref lends for reading; range over the variable, or mut of it")
		case iterator && s.Value == nil:
			key, ok = item, true
		case iterator:
			if pair, isPair := item.Underlying().(*Tuple); isPair && pair.Len() == 2 {
				key, val, ok = pair.At(0), pair.At(1), true
			} else {
				c.errorf(s.Value.Pos(), "range over "+exprText(s.X)+" yields one "+item.String()+" at a time, which takes one iteration variable")
			}
		default:
			if key, val, ok = rangeTypes(x); !ok {
				c.errorf(s.X.Pos(), "cannot range over a value of type "+x.typ.String())
			} else if val == Typ[Invalid] && s.Value != nil { // a channel's one variable: the value
				c.errorf(s.Value.Pos(), "range over "+exprText(s.X)+" permits only one iteration variable")
				key = Typ[Invalid]
			}
		}

	}

	if s.Tok == token.DEFINE {
		c.rangeVar(s.Key, key)
		c.rangeVar(s.Value, val)
	} else {
		c.rangePlace(s.Key, key)
		c.rangePlace(s.Value, val)
	}

	c.block(s.Body)
}

// inParts reports whether a loop over a type's parts is around the
// statement being checked, in the function being checked.
func (c *Checker) inParts() bool {
	return slices.Contains(c.loops, true)
}

// enterLoop records a loop open around the statements being checked,
// parts saying whether it is one over a type's parts, and leaveLoop its
// end.
func (c *Checker) enterLoop(parts bool) { c.loops = append(c.loops, parts) }

func (c *Checker) leaveLoop() { c.loops = c.loops[:len(c.loops)-1] }

// branch checks a break or continue: outside any loop it is an error,
// and so is one that would leave a loop over a type's parts, which is
// written out once per part, each copy a block of its own.
func (c *Checker) branch(tok token.Kind, pos token.Pos) {
	switch {
	case len(c.loops) == 0:
		c.errorf(pos, tok.String()+" is not inside a loop")
	case c.loops[len(c.loops)-1]:
		c.errorf(pos, tok.String()+" cannot leave a loop over a type's parts: it is written out once per part, each copy a block of its own; test the part with an if")
	}
}

// deferStmt checks a defer: the typing of the call; what it borrows is
// held until the function returns by the borrow pass, which meets the
// defer in the form.
func (c *Checker) deferStmt(s *ast.DeferStmt) {
	if c.inParts() {
		c.errorf(s.Defer, "defer cannot be inside a loop over a type's parts, which is written out once per part: defer before the loop")
	}

	c.expr(s.Call, nil)
}

// deferHolds records what the deferred call holds until the function
// returns, by holder: what a deferred closure captures, the receiver,
// and each argument, as deferHold and deferView say; rest are the
// operands of the call's variadic arguments, checked last. partOf gives
// the value the defer statement made that the i-th argument is a part
// of, or nil.
func (c *Checker) deferHolds(holder *Var, call *ast.CallExpr, sig *Signature, rest []operand, pos token.Pos, partOf func(i int) ast.Expr) {
	switch fun := unparen(call.Fun).(type) {
	case *ast.FuncLit:
		for _, cap := range c.captured[fun] {
			if cap.mode != byRef && cap.mode != byMut {
				continue
			}

			if holdsBorrow(cap.v.typ) && !isBorrow(cap.v.typ) {
				c.errorf(pos, "a deferred call borrows only a whole variable, one that is a borrow or holds none: the closure captures "+cap.v.name+", which holds one")
				continue
			}

			b := cap.borrow(holder, pos)
			b.atExit = true // the closure reaches the variable when it runs, after every other use
			c.flowLoan(b)
		}
	case *ast.Ident:
		if c.closureExprBorrows(fun) {
			c.errorf(pos, "a deferred call cannot call "+fun.Name+": it is a closure that borrows; defer the closure itself, func() { ... }()")
		}
	}

	if sig == nil {
		if id, ok := unparen(call.Fun).(*ast.Ident); ok {
			if _, builtin := c.refersTo(id).(*Builtin); builtin {
				for i, arg := range call.Args { // print(s), close(ch): saved when the defer runs, so what they read is held until the call does
					c.deferView(holder, arg, partOf(i), pos)
				}
			}
		}

		return
	}

	if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sig.recv != nil && holdsBorrow(sig.recv.typ) {
		c.deferHold(holder, sig.recv.typ, sel.X, pos)
	}

	fixed := sig.fixed()
	kind, each := sig.variadicArg()
	for i, arg := range call.Args {
		if i >= fixed && kind == VariadicAny {
			if k := i - fixed; k < len(rest) && rest[k].mode != invalid {
				c.deferView(holder, arg, partOf(i), pos) // fmt.Println(t) saves t as a view, any lent, whatever its type
			}

			continue
		}

		_, t, _, packed := argSlot(sig, i, call.Ellipsis.IsValid(), kind, each) // an argument of a variadic parameter of the program's is held as its element, or as the slice after ...
		if packed {
			continue // moved into the function's own slice, and held by nothing: one that borrows was refused as stored
		}

		if holdsBorrow(t) || isSignature(t) && c.closureExprBorrows(arg) {
			c.deferHold(holder, t, arg, pos)
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
// variable is. A literal or a constant outlives the function and is
// held as it is. Anything else, an element, what a borrow points to, a
// value that holds a borrow or a closure that borrows, is refused: what
// it points into may move or die before the call runs.
func (c *Checker) deferHold(holder *Var, t Type, e ast.Expr, pos token.Pos) {
	x := unparen(e)
	if lends(x) {
		x = unparen(x.(*ast.UnaryExpr).X)
	}

	if c.static(x) { // a literal or a constant, which outlives the function: fmt.Printf's format
		return
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

// deferView records what a deferred call saves as a view when the
// defer runs, an argument of the printing functions' ...ref any or of a
// built-in, print(s), close(ch): the places it is lent from, a whole
// variable among them, held until the function returns, so that none
// is given a new value or moved away before then, which would free what
// the saved view sees, unless its type is Copy and the saved value a
// copy; a view made for the call, s.Trim(), by its sources, since the
// view is the call's own and nothing else reads it.
// A call that reaches a variable when it runs holds it by deferHold
// instead, which lets it change. A part of a value the defer statement
// made, part, is refused: the statement drops the value before the call
// reads it.
func (c *Checker) deferView(holder *Var, e, part ast.Expr, pos token.Pos) {
	if part != nil {
		c.errorf(e.Pos(), "a deferred call reads what it prints when it runs, after the defer statement drops "+exprText(unparen(part))+": keep "+exprText(unparen(part))+" in a variable first, and print from it")
		return
	}

	if p, kind := c.placeOf(unparen(e)); kind == placeOwned && !isBorrow(p.v.typ) {
		if !IsCopy(placeType(p)) { // a Copy value saved is a copy, which owns nothing the place could free
			c.flowLoan(borrow{holder: holder, place: p, pos: pos}) // an owned place the saved view sees: s, t.name, xs[i]
		}

		return
	}

	c.sources(e, func(src ast.Expr, _, _ bool) {
		if p, kind := through(c.placeOf(src)); kind != notPlace {
			c.flowLoan(borrow{holder: holder, place: p, pos: pos})
		}
	})
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

// IteratorNext returns the method that makes a value of type t an
// iterator a range loop calls: Next, on a mut receiver, taking nothing
// and returning an option, the item; or nil. The backends call it to
// write the loop as the checker read it.
func IteratorNext(t Type) *Func {
	n, ok := t.(*Named)
	if !ok {
		return nil
	}

	fn := n.Method("Next")
	if fn == nil {
		return nil
	}

	sig, ok := fn.typ.(*Signature)
	if !ok || sig.recv == nil || len(sig.params) != 0 || len(sig.tparams) != 0 {
		return nil
	}

	if r, ok := sig.recv.typ.(*Ref); !ok || !r.mut {
		return nil
	}

	if _, ok := sig.results.(*Option); !ok {
		return nil
	}

	return fn
}

// iteratorItem returns the item a range over x yields when x is an
// iterator, whether it is one, and whether it is reached through a ref,
// which cannot lend it for the mut receiver Next takes.
func iteratorItem(x operand) (item Type, ok, shared bool) {
	base, m := deref(x)
	fn := IteratorNext(base)
	if fn == nil {
		return nil, false, false
	}

	return fn.typ.(*Signature).results.(*Option).elem, true, m == borrowed
}

// isIterator reports whether a slice type stands for the iterator the
// library's Fields and Split give, a slice of borrows, a type no
// program can write.
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

	if x := c.target(e); x.mode != invalid {
		c.assign(operand{value, t, nil, nil}, x.typ, e.Pos(), "range assignment")
	}
}

func (c *Checker) selectStmt(s *ast.SelectStmt) bool {
	if c.inParts() {
		c.errorf(s.Select, "select cannot be inside a loop over a type's parts, which is written out once per part: select in a function the loop calls")
	}

	allLeave := true
	for _, cc := range s.Body {
		c.openScope()
		if cc.Comm != nil {
			c.stmt(cc.Comm)
		}

		if !c.stmts(cc.Body) {
			allLeave = false
		}

		c.closeScope()
	}

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

// unfinished reports whether s is a statement the parser left without
// the body its form has, an if, a for or a loop over a range written
// without its block, which only a file that does not parse holds: the
// parser reported it, and a check of the file as the parser recovered
// it passes it over.
func unfinished(s ast.Stmt) bool {
	switch s := s.(type) {
	case *ast.IfStmt:
		return s.Body == nil
	case *ast.IfBindStmt:
		return s.Body == nil
	case *ast.ForStmt:
		return s.Body == nil
	case *ast.RangeStmt:
		return s.Body == nil
	}

	return false
}

// refuseMutMapBinding refuses a pattern bound mut through a map read, x
// being the value matched and b the borrow its bindings get: a key the
// map does not hold reads the zero value, and a write to it would be
// lost.
func (c *Checker) refuseMutMapBinding(x ast.Expr, b *Ref, outer *Scope) {
	if b == nil || !b.mut || c.scope.Len() == 0 {
		return
	}

	names := c.names
	c.names = outer // the value's names resolve outside the pattern's: match o { Some(o) => ... } binds a new o out of the old
	defer func() { c.names = names }()
	if ix, ok := x.(*ast.IndexExpr); ok && isMap(stripRef(c.placeType(ix.X))) {
		c.errorf(x.Pos(), "cannot bind mut through "+exprText(x)+": a key the map does not hold reads the zero value; use "+exprText(ix.X)+".GetMut("+exprText(ix.Index)+")")
	}
}
