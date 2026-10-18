// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"strconv"

	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/token"
	"github.com/teak-lang/teak/types"
)

func (e *Emitter) stmts(list []ast.Stmt) {
	for _, s := range list {
		e.stmt(s)
	}
}

func (e *Emitter) block(b *ast.BlockStmt) {
	e.line("{")
	e.push(false)
	e.stmts(b.List)
	e.pop(b.Rbrace)
	e.line("}")
}

// stmt writes one statement and then drops the temporaries it made.
func (e *Emitter) stmt(s ast.Stmt) {
	mark := len(e.temps)
	e.stmtInner(s)
	e.dropTemps(mark, s.Pos())
}

func (e *Emitter) stmtInner(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.BlockStmt:
		e.block(s)
	case *ast.DeclStmt:
		e.declStmt(s.Decl)
	case *ast.ExprStmt:
		if x := e.expr(s.X); x != "" {
			e.discard(x, e.info.Types[s.X], s.Pos())
		}
	case *ast.IncDecStmt:
		e.line(e.expr(s.X) + s.Tok.String() + ";")
	case *ast.AssignStmt:
		e.assign(s)
	case *ast.ReturnStmt:
		e.returnStmt(s)
	case *ast.BranchStmt:
		e.leave(e.loopFrame(), s.Pos()) // the loop body's variables die here
		if n := len(e.loops); s.Tok == token.CONTINUE && n > 0 && e.loops[n-1] != "" {
			e.line("goto " + e.loops[n-1] + ";")
			break
		}

		e.line(s.Tok.String() + ";")
	case *ast.IfStmt:
		e.ifStmt(s)
	case *ast.IfBindStmt:
		e.ifBind(s)
	case *ast.ForStmt:
		e.forStmt(s)
	case *ast.RangeStmt:
		e.rangeStmt(s)
	case *ast.SwitchStmt:
		e.unsupported(s.Pos(), "switch")
	case *ast.DeferStmt:
		e.deferStmt(s)
	case *ast.SendStmt, *ast.SelectStmt, *ast.SpawnStmt:
		e.unsupported(s.Pos(), "channels and tasks")
	case *ast.EmptyStmt:
	default:
		e.unsupported(s.Pos(), "this kind of statement")
	}
}

// declStmt writes var x T = v. A name that is never used gets a (void)
// cast, as the C compiler is asked to treat an unused variable as an
// error.
func (e *Emitter) declStmt(d *ast.VarDecl) {
	var parts []string
	if len(d.Values) == 1 && len(d.Names) > 1 {
		parts = e.spread(d.Values[0], len(d.Names))
	}

	for i, name := range d.Names {
		v := e.info.Defs[name]
		decl := e.cType(v.Type(), name.Pos()) + " " + ident(name.Name) + " = "
		switch {
		case parts != nil:
			decl += parts[i]
		case i < len(d.Values):
			decl += e.valueTo(d.Values[i], v.Type())
		default:
			decl += zeroValue
		}

		e.line(decl + ";")
		e.discardUnused(name)
		e.own(ident(name.Name), v.Type())
	}
}

// discardUnused writes (void)x for a declared name nothing refers to.
func (e *Emitter) discardUnused(name *ast.Ident) {
	if name.Name == "_" {
		return
	}

	if obj := e.info.Defs[name]; obj != nil && !e.used[obj] {
		e.line("(void)" + ident(name.Name) + ";")
	}
}

func (e *Emitter) assign(s *ast.AssignStmt) {
	if len(s.Lhs) != 1 || len(s.Rhs) != 1 {
		e.assignMany(s)
		return
	}

	lhs, rhs := s.Lhs[0], s.Rhs[0]
	switch s.Tok {
	case token.DEFINE, token.ASSIGN:
		t := e.target(lhs, s.Tok)
		if t == nil {
			t = e.info.Types[rhs] // _ = x
		}

		e.store(lhs, s.Tok, e.valueTo(rhs, t), t)
	default:
		t := valueType(e.info.Types[lhs])
		place := e.lvalue(lhs)
		if isString(t) { // s += t has no C operator; the old left side is freed after the join
			e.line(place + " = " + join(text{place, true}, text{e.valueTo(rhs, t), !e.isRead(rhs)}).s + ";")
			return
		}

		e.line(place + " " + s.Tok.String() + " " + e.valueTo(rhs, t) + ";")
	}
}

// assignMany writes a, b := x, y or a, b = y, x: every right side goes
// into a temporary before any left side changes, as Teak and Go
// require. With one right side, a tuple, each name takes one element.
func (e *Emitter) assignMany(s *ast.AssignStmt) {
	if s.Tok != token.DEFINE && s.Tok != token.ASSIGN {
		e.unsupported(s.TokPos, "this assignment of several values")
		return
	}

	temps, kinds := make([]string, len(s.Lhs)), make([]types.Type, len(s.Lhs))
	switch {
	case len(s.Rhs) == 1 && len(s.Lhs) > 1:
		temps = e.spread(s.Rhs[0], len(s.Lhs))
		tuple := e.info.Types[s.Rhs[0]].Underlying().(*types.Tuple)
		for i := range kinds {
			kinds[i] = tuple.At(i)
		}
	case len(s.Lhs) == len(s.Rhs):
		for i, rhs := range s.Rhs {
			kinds[i] = e.target(s.Lhs[i], s.Tok)
			if kinds[i] == nil {
				kinds[i] = e.info.Types[rhs] // _
			}

			temps[i] = e.temp()
			e.line(e.cType(kinds[i], rhs.Pos()) + " " + temps[i] + " = " + e.valueTo(rhs, kinds[i]) + ";")
		}
	default:
		e.unsupported(s.TokPos, "this assignment of several values")
		return
	}

	for i, lhs := range s.Lhs {
		e.store(lhs, s.Tok, temps[i], kinds[i])
	}
}

// target is the type a value stored into lhs takes: the declared type
// of a name := introduces, otherwise the type of the place.
func (e *Emitter) target(lhs ast.Expr, tok token.Kind) types.Type {
	if name, ok := lhs.(*ast.Ident); ok && tok == token.DEFINE {
		if obj, declared := e.info.Defs[name]; declared {
			return obj.Type()
		}
	}

	return e.info.Types[lhs]
}

// store writes value, of type t, into lhs: a declaration for a name
// := introduces, a drop for _, an assignment otherwise, including a
// name := reuses, which drops what the place held before.
func (e *Emitter) store(lhs ast.Expr, tok token.Kind, value string, t types.Type) {
	if isBlank(lhs) {
		e.discard(value, t, lhs.Pos())
		return
	}

	if name, ok := lhs.(*ast.Ident); ok && tok == token.DEFINE {
		if obj, declared := e.info.Defs[name]; declared {
			e.line(e.cType(obj.Type(), name.Pos()) + " " + ident(name.Name) + " = " + value + ";")
			e.discardUnused(name)
			e.own(ident(name.Name), obj.Type())
			return
		}
	}

	place := e.lvalue(lhs)
	if needsDrop(t) {
		tmp := e.temp()
		e.line(e.cType(t, lhs.Pos()) + " " + tmp + " = " + value + ";")
		e.line(e.dropOf(place, t, lhs.Pos()))
		value = tmp
	}

	e.line(place + " = " + value + ";")
}

// spread writes a tuple value into a temporary and names its n
// elements.
func (e *Emitter) spread(x ast.Expr, n int) []string {
	tmp := e.temp()
	e.line(e.cType(e.info.Types[x], x.Pos()) + " " + tmp + " = " + e.expr(x) + ";")
	parts := make([]string, n)
	for i := range parts {
		parts[i] = tmp + ".e" + strconv.Itoa(i)
	}

	return parts
}

// returnStmt writes a return. With deferred calls pending, the value
// is computed first, then the calls run, then the value is returned.
func (e *Emitter) returnStmt(s *ast.ReturnStmt) {
	switch {
	case len(s.Results) > 1:
		e.unsupported(s.Return, "returning several values")
	case len(s.Results) == 0:
		e.leaveFunction(s.Pos())
		e.line("return;")
	case len(e.defers) == 0 && e.nothingToDrop():
		e.line("return " + e.valueTo(s.Results[0], e.result) + ";")
	default:
		v := e.saved(s.Results[0], e.result)
		e.leaveFunction(s.Pos())
		e.line("return " + v + ";")
	}
}

// nothingToDrop reports whether no open frame holds a variable to
// drop and no temporary is pending, so a return needs no temporary.
func (e *Emitter) nothingToDrop() bool {
	for _, f := range e.frames {
		if len(f.vars) > 0 {
			return false
		}
	}

	return len(e.temps) == 0
}

// ifStmt writes an if, with an init statement in a block of its own so
// its names end with the if, as they do in Teak.
func (e *Emitter) ifStmt(s *ast.IfStmt) {
	if s.Init != nil {
		e.line("{")
		e.push(false)
		e.stmt(s.Init)
	}

	e.line("if " + e.cond(s.Cond) + " {")
	e.push(false)
	e.stmts(s.Body.List)
	e.pop(s.Body.Rbrace)
	e.elseBranch(s.Else)
	if s.Init != nil {
		e.pop(s.Body.Rbrace)
		e.line("}")
	}
}

// elseBranch closes the body of an if and writes its else, if any: a
// block, or another if written inside a block of its own so that the
// statements its condition needs come first.
func (e *Emitter) elseBranch(els ast.Stmt) {
	switch els := els.(type) {
	case nil:
		e.line("}")
	case *ast.BlockStmt:
		e.line("} else {")
		e.push(false)
		e.stmts(els.List)
		e.pop(els.Rbrace)
		e.line("}")
	default:
		e.line("} else {")
		e.stmt(els)
		e.line("}")
	}
}

// forStmt writes Teak's three loop forms. A condition that calls or
// needs statements is tested at the top of the body, so that what it
// needs runs on every pass; a post statement that does is written at
// the end of the body under a label that continue jumps to.
func (e *Emitter) forStmt(s *ast.ForStmt) {
	e.line("{")
	e.push(false)
	if s.Init != nil {
		e.stmt(s.Init)
	}

	cond := ""
	if s.Cond != nil && !e.hasEffects(s.Cond) {
		cond = e.expr(s.Cond)
	}

	post, label := "", ""
	if s.Post != nil {
		var ok bool
		if post, ok = e.simpleStmt(s.Post); !ok {
			label = e.temp() + "_post"
		}
	}

	e.line("for (; " + cond + "; " + post + ") {")
	if s.Cond != nil && cond == "" {
		e.line("if (!" + parenthesized(e.boolean(s.Cond)) + ") { break; }")
	}

	e.loops = append(e.loops, label)
	e.push(true)
	e.stmts(s.Body.List)
	e.pop(s.Body.Rbrace)
	e.loops = e.loops[:len(e.loops)-1]
	if label != "" {
		e.line(label + ":;")
		e.stmt(s.Post)
	}

	e.line("}")
	e.pop(s.Body.Rbrace)
	e.line("}")
}

// rangeStmt writes for k, v := range x over a slice or a string: an
// index loop, with the key and value bound at the top of each pass. A
// value reached through a mut borrow is bound as a pointer into the
// slice. Fields and Split yield a slice of borrows whose one variable
// is the item.
func (e *Emitter) rangeStmt(s *ast.RangeStmt) {
	place, t, held := e.consumed(s.X)
	if held != "" {
		e.hold(held, e.info.Types[s.X]) // an owned collection is used up by the loop and dropped after it
	}

	consume := held != ""
	e.line("{")
	i := e.temp()
	e.loops = append(e.loops, "") // continue reaches the loop's own step
	defer func() { e.loops = e.loops[:len(e.loops)-1] }()
	if m, ok := t.Underlying().(*types.Map); ok {
		e.line("for (int64_t " + i + " = tk_map_next(&" + place + ", 0); " + i + " >= 0; " + i + " = tk_map_next(&" + place + ", " + i + " + 1)) {")
		e.push(true)
		e.rangeVar(s.Key, s.Tok, "(("+e.cType(m.Key(), s.Pos())+" *)"+place+".keys)["+i+"]", m.Key(), consume)
		e.rangeVar(s.Value, s.Tok, "(("+e.cType(m.Value(), s.Pos())+" *)"+place+".vals)["+i+"]", m.Value(), consume)
		e.stmts(s.Body.List)
		e.pop(s.Body.Rbrace)
		e.line("}")
		e.line("}")
		return
	}

	e.line("for (tk_int " + i + " = 0; " + i + " < " + place + ".len; " + i + "++) {")
	elem := place + ".ptr[" + i + "]"
	var elemType types.Type = types.Typ[types.Uint8]
	iterator := false
	switch u := t.Underlying().(type) {
	case *types.Slice:
		elemType = u.Elem()
		_, iterator = elemType.(*types.Ref)
	case *types.Basic:
	default:
		e.unsupported(s.X.Pos(), "ranging over a value of type "+t.String())
	}

	e.push(true)
	if iterator {
		e.rangeVar(s.Key, s.Tok, elem, elemType, consume)
	} else {
		e.rangeVar(s.Key, s.Tok, i, types.Typ[types.Int], false)
		e.rangeVar(s.Value, s.Tok, elem, elemType, consume)
	}

	e.stmts(s.Body.List)
	e.pop(s.Body.Rbrace)
	e.line("}")
	e.line("}")
}

// rangeVar binds one range variable to the C place of the current
// item, of Teak type itemType: a new name for :=, an assignment for =,
// nothing for _. A mut binding of the item is its address. When the
// loop consumes its collection, an item bound by value leaves it.
func (e *Emitter) rangeVar(x ast.Expr, tok token.Kind, item string, itemType types.Type, consume bool) {
	if x == nil || isBlank(x) {
		return
	}

	if tok != token.DEFINE {
		e.store(x, tok, item, itemType)
		return
	}

	name := x.(*ast.Ident)
	v := e.info.Defs[name]
	value := item
	if depth(v.Type()) > depth(itemType) {
		value = "&" + item
	}

	e.line(e.cType(v.Type(), name.Pos()) + " " + ident(name.Name) + " = " + value + ";")
	e.discardUnused(name)
	e.own(ident(name.Name), v.Type())
	if _, borrow := v.Type().(*types.Ref); consume && !borrow && needsDrop(v.Type()) {
		e.line(item + " = " + e.zero(itemType, name.Pos()) + ";")
	}
}

// simpleStmt writes a statement that C accepts as an expression, for
// the post clause of a for: x++, x += 1, x = e. A value that calls or
// needs statements cannot go there, as they would run once, before the
// loop; the caller writes such a statement in the body.
func (e *Emitter) simpleStmt(s ast.Stmt) (string, bool) {
	switch s := s.(type) {
	case *ast.IncDecStmt:
		return e.expr(s.X) + s.Tok.String(), true
	case *ast.AssignStmt:
		if len(s.Lhs) != 1 || len(s.Rhs) != 1 || s.Tok == token.DEFINE || e.hasEffects(s.Rhs[0]) {
			return "", false
		}

		t := valueType(e.info.Types[s.Lhs[0]])
		if s.Tok == token.ASSIGN {
			return e.lvalue(s.Lhs[0]) + " = " + e.valueTo(s.Rhs[0], t), true
		}

		if isString(t) {
			return "", false
		}

		return e.lvalue(s.Lhs[0]) + " " + s.Tok.String() + " " + e.valueTo(s.Rhs[0], t), true
	}

	return "", false
}

// cond writes a condition in exactly one pair of parentheses, as the C
// compiler warns about a second pair around a comparison.
func (e *Emitter) cond(x ast.Expr) string {
	return parenthesized(e.expr(x))
}

// parenthesized writes s in exactly one pair of parentheses.
func parenthesized(s string) string {
	if len(s) > 1 && s[0] == '(' && s[len(s)-1] == ')' && balanced(s[1:len(s)-1]) {
		return s
	}

	return "(" + s + ")"
}

// balanced reports whether s has matching parentheses throughout, so
// that an outer pair is one pair and not two expressions side by side.
func balanced(s string) bool {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}

	return depth == 0
}

func isBlank(x ast.Expr) bool {
	id, ok := x.(*ast.Ident)
	return ok && id.Name == "_"
}

// isString reports whether t is text in C: a string, or an error,
// which is kept as its message.
func isString(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && (b.Kind() == types.String || b.Kind() == types.Error)
}
