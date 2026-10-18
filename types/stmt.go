// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strconv"

	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/token"
)

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
		if x.mode != invalid && !hasInfo(valueType(x), IsNumeric) {
			c.errorf(s.TokPos, s.Tok.String()+" is not defined on "+x.typ.String())
		}
	case *ast.AssignStmt:
		c.assignStmt(s)
	case *ast.SpawnStmt:
		c.expr(s.Call, nil)
	case *ast.DeferStmt:
		c.expr(s.Call, nil)
	case *ast.ReturnStmt:
		c.returnStmt(s)
		return true
	case *ast.BranchStmt:
		return true
	case *ast.IfStmt:
		return c.ifStmt(s)
	case *ast.IfBindStmt:
		return c.ifBind(s)
	case *ast.ForStmt:
		return c.forStmt(s)
	case *ast.RangeStmt:
		c.rangeStmt(s)
	case *ast.SwitchStmt:
		return c.switchStmt(s)
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
		}

		return
	}

	ops := c.values(len(d.Names), d.Values, t, d.Pos())
	c.declareVars(c.scope, d, t)
	for i, name := range d.Names {
		c.assign(ops[i], t, d.Values[min(i, len(d.Values)-1)].Pos(), "declaration")
		if t == nil {
			c.setType(c.scope.Lookup(name.Name), inferred(ops[i]))
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
		y := c.expr(s.Rhs[0], hintOf(x))
		if x.mode == invalid || y.mode == invalid {
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

		c.assign(y, x.typ, s.Rhs[0].Pos(), "assignment")
	}
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
			continue // _ := x declares nothing
		}

		if old := c.scope.Lookup(ident.Name); old != nil {
			c.recordUse(ident, old)
			if v, ok := old.(*Var); ok && v.typ != nil {
				c.assign(ops[i], v.typ, lhs.Pos(), "assignment")
			}

			continue
		}

		c.declare(c.scope, ident, NewVar(ident.Pos(), ident.Name, inferred(ops[i])))
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
			c.expr(s.Rhs[0], nil) // _ = x discards x
			return
		}

		x := c.expr(s.Lhs[0], nil)
		y := c.expr(s.Rhs[0], hintOf(x))
		if x.mode != invalid {
			c.assign(y, x.typ, s.Rhs[0].Pos(), "assignment")
		}

		return
	}

	ops := c.values(len(s.Lhs), s.Rhs, nil, s.TokPos)
	for i, lhs := range s.Lhs {
		if isBlank(lhs) {
			continue
		}

		if x := c.expr(lhs, nil); x.mode != invalid {
			c.assign(ops[i], x.typ, lhs.Pos(), "assignment")
		}
	}
}

func (c *Checker) send(s *ast.SendStmt) {
	ch := c.expr(s.Chan, nil)
	if ch.mode == invalid {
		c.expr(s.Value, nil)
		return
	}

	t, ok := valueType(ch).Underlying().(*Chan)
	if !ok {
		c.errorf(s.Arrow, "cannot send on a value of type "+ch.typ.String())
		c.expr(s.Value, nil)
		return
	}

	c.assign(c.expr(s.Value, t.elem), t.elem, s.Value.Pos(), "send")
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
		c.assign(c.expr(s.Results[0], want), want, s.Results[0].Pos(), "return")
	default:
		t, ok := want.(*Tuple)
		if !ok || len(t.elems) != len(s.Results) {
			c.errorf(s.Return, "return has "+strconv.Itoa(len(s.Results))+" values; the function returns "+want.String())
			c.exprs(s.Results)
			return
		}

		for i, r := range s.Results {
			c.assign(c.expr(r, t.elems[i]), t.elems[i], r.Pos(), "return")
		}
	}
}

func (c *Checker) ifStmt(s *ast.IfStmt) bool {
	c.openScope()
	defer c.closeScope()
	if s.Init != nil {
		c.stmt(s.Init)
	}

	c.condition(s.Cond)
	leaves := c.block(s.Body)
	if s.Else == nil {
		return false
	}

	return c.stmt(s.Else) && leaves
}

// ifBind checks `if pat := value {`: the value is an option, and the
// body sees the pattern's names bound to what is inside. A borrowed
// option binds borrows.
func (c *Checker) ifBind(s *ast.IfBindStmt) bool {
	x := c.expr(s.Value, nil)
	c.openScope()
	if x.mode != invalid {
		inner, m := deref(x)
		if o, ok := inner.Underlying().(*Option); ok {
			c.pattern(s.Pat, o.elem, borrowOf(m))
		} else {
			c.errorf(s.Value.Pos(), "if x := v binds an option; "+x.typ.String()+" is not one")
			c.pattern(s.Pat, Typ[Invalid], nil)
		}
	} else {
		c.pattern(s.Pat, Typ[Invalid], nil)
	}

	leaves := c.block(s.Body)
	c.closeScope()
	if s.Else == nil {
		return false
	}

	return c.stmt(s.Else) && leaves
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
// or an iterator each item. Ranging over a borrowed collection yields
// borrows of its elements; ranging over an owned one moves them out.
func (c *Checker) rangeStmt(s *ast.RangeStmt) {
	x := c.expr(s.X, nil)
	c.openScope()
	defer c.closeScope()
	var key, val Type = Typ[Invalid], Typ[Invalid]
	if x.mode != invalid {
		var ok bool
		if key, val, ok = rangeTypes(x); !ok {
			c.errorf(s.X.Pos(), "cannot range over a value of type "+x.typ.String())
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
		return t.elem, Typ[Invalid], true
	case *Basic:
		if t.kind == String {
			return Typ[Int], aliases[0], true
		}
	}

	return Typ[Invalid], Typ[Invalid], false
}

// isIterator reports whether a slice type stands for an iterator: the
// library types Fields and Split as a slice of borrows, a type no
// program can write, until the iterator trait exists (stage 2).
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

	x := c.expr(e, nil)
	if x.mode != invalid {
		c.assign(operand{value, t, nil}, x.typ, e.Pos(), "range assignment")
	}
}

func (c *Checker) switchStmt(s *ast.SwitchStmt) bool {
	c.openScope()
	defer c.closeScope()
	if s.Init != nil {
		c.stmt(s.Init)
	}

	var tag Type = Typ[Bool]
	if s.Tag != nil {
		if op := c.expr(s.Tag, nil); op.mode != invalid {
			tag = valueType(op)
		}
	}

	hasDefault, allLeave := false, true
	for _, cc := range s.Body {
		c.openScope()
		if len(cc.List) == 0 {
			hasDefault = true
		}

		for _, e := range cc.List {
			c.assign(c.expr(e, tag), tag, e.Pos(), "case")
		}

		if !c.stmts(cc.Body) || hasBreak(cc.Body) {
			allLeave = false
		}

		c.closeScope()
	}

	return hasDefault && allLeave
}

func (c *Checker) selectStmt(s *ast.SelectStmt) bool {
	allLeave := true
	for _, cc := range s.Body {
		c.openScope()
		if cc.Comm != nil {
			c.stmt(cc.Comm)
		}

		if !c.stmts(cc.Body) || hasBreak(cc.Body) {
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
			return operand{value, Typ[Never], nil}
		}

		return operand{value, Unit, nil}
	}

	return invalidOperand
}

func (c *Checker) exprs(es []ast.Expr) {
	for _, e := range es {
		c.expr(e, nil)
	}
}

// hasBreak reports whether a break in the list leaves the enclosing
// loop or switch: one not inside a nested loop, switch or select.
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
