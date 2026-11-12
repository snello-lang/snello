// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"strconv"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
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
			e.discard(x, e.typeOf(s.X), s.Pos())
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
			e.jumped[e.loops[n-1]] = true
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
	case *ast.DeferStmt:
		e.deferStmt(s)
	case *ast.SendStmt:
		e.sendStmt(s)
	case *ast.SpawnStmt:
		e.spawnStmt(s)
	case *ast.SelectStmt:
		e.selectStmt(s)
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
		vt := e.sub(v.Type())
		local := e.declareLocal(name)
		decl := e.cType(vt, name.Pos()) + " " + local + " = "
		switch {
		case parts != nil:
			decl += parts[i]
		case i < len(d.Values):
			decl += e.valueTo(d.Values[i], vt)
		default:
			decl += zeroValue
		}

		e.line(decl + ";")
		e.discardUnused(name)
		e.own(local, vt)
	}
}

// discardUnused writes (void)x for a declared name nothing refers to.
func (e *Emitter) discardUnused(name *ast.Ident) {
	if name.Name == "_" {
		return
	}

	if v, ok := e.info.Defs[name].(*types.Var); ok && !e.used[v] {
		e.line("(void)" + e.localName(v) + ";") // the name declareLocal gave it
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
			t = e.typeOf(rhs) // _ = x
		}

		e.store(lhs, s.Tok, e.valueTo(rhs, t), t)
	default:
		t := lower.ValueType(e.typeOf(lhs))
		place := e.lvalue(lhs)
		if call := e.operatorCall(t, s.Tok.Binary(), false, lhs, []ast.Expr{rhs}, s.Pos()); call != "" {
			e.line(place + " = " + call + ";") // a += b on a type with an Add: the old value goes into the method
			return
		}

		if isString(t) { // s += t appends in place when s owns the room, and grows by doubling when not
			r := e.temp()
			e.line("sn_string " + r + " = " + e.valueTo(rhs, t) + ";")
			e.line("sn_string_append(&" + place + ", &" + r + ");")
			if !e.isLent(rhs) {
				e.line("sn_string_drop(&" + r + ");")
			}

			return
		}

		if op := s.Tok.Binary(); divides(op, t) || shifts(op, t) { // through a pointer, so the place is reached once
			fn := e.shifter
			if divides(op, t) {
				fn = e.divider
			}

			at := e.temp()
			e.line(e.cType(t, s.Pos()) + " *" + at + " = &" + place + ";")
			e.line("*" + at + " = " + fn(op, t, s.Pos()) + "(*" + at + ", " + e.valueTo(rhs, t) + ");")
			return
		}

		if s.Tok == token.AND_NOT_ASSIGN { // C has no &^: the bits of the right side are cleared with & and ~
			e.line(place + " &= ~(" + e.valueTo(rhs, t) + ");")
			return
		}

		e.line(place + " " + s.Tok.String() + " " + e.valueTo(rhs, t) + ";")
	}
}

// assignMany writes a, b := x, y or a, b = y, x: every right side goes
// into a temporary before any left side changes, as Snello and Go
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
		tuple := e.typeOf(s.Rhs[0]).Underlying().(*types.Tuple)
		for i := range kinds {
			kinds[i] = tuple.At(i)
		}
	case len(s.Lhs) == len(s.Rhs):
		for i, rhs := range s.Rhs {
			kinds[i] = e.target(s.Lhs[i], s.Tok)
			if kinds[i] == nil {
				kinds[i] = e.typeOf(rhs) // _
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
			return e.sub(obj.Type())
		}
	}

	return e.typeOf(lhs)
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
			local := e.declareLocal(name)
			e.line(e.cType(e.sub(obj.Type()), name.Pos()) + " " + local + " = " + value + ";")
			e.discardUnused(name)
			e.own(local, e.sub(obj.Type()))
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
	e.line(e.cType(e.typeOf(x), x.Pos()) + " " + tmp + " = " + e.expr(x) + ";")
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
	default:
		v := e.valueTo(s.Results[0], e.result)
		if e.result == types.Unit { // a generic function returning its T, given (): the function is void, and the value is evaluated for its effects
			e.line("(void)(" + v + ");")
			e.leaveFunction(s.Pos())
			e.line("return;")
			return
		}

		if len(e.defers) == 0 && e.nothingToDrop() { // asked after the value is written: it may have left temporaries to drop
			e.line("return " + v + ";")
			return
		}

		v = e.spill(v, e.result, s.Pos())
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
// its names end with the if, as they do in Snello.
func (e *Emitter) ifStmt(s *ast.IfStmt) {
	if s.Init != nil {
		e.line("{")
		e.push(false)
		e.stmt(s.Init)
	}

	// The condition's temporaries are dropped once it is decided, before
	// either branch runs: nothing binds a borrow of them, as an if-binding
	// does, and a guard a condition took would otherwise stay locked
	// through the body.
	mark := len(e.temps)
	c := e.cond(s.Cond)
	if len(e.temps) > mark {
		v := e.temp()
		e.line("bool " + v + " = " + c + ";")
		e.dropTemps(mark, s.Cond.Pos())
		c = "(" + v + ")"
	}

	e.line("if " + c + " {")
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

// forStmt writes Snello's three loop forms. A condition that calls,
// needs statements or builds a temporary is tested at the top of the
// body, so that what it needs runs, and its temporaries are dropped, on
// every pass; a post statement that needs statements is written at the
// end of the body, under a label when a continue jumps to it.
func (e *Emitter) forStmt(s *ast.ForStmt) {
	e.line("{")
	e.push(false)
	if s.Init != nil {
		e.stmt(s.Init)
	}

	cond := ""
	if s.Cond != nil && !e.hasEffects(s.Cond) {
		mark, start := len(e.temps), e.body.Len()
		cond = e.expr(s.Cond)
		if len(e.temps) > mark { // a temporary the condition needs is built and dropped on every pass, in the body
			e.body.Truncate(start)
			e.temps, cond = e.temps[:mark], ""
		}
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
		if e.jumped[label] {
			e.line(label + ":;")
		}

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
		e.hold(held, e.typeOf(s.X)) // an owned collection is used up by the loop and dropped after it
	}

	consume := held != ""
	e.line("{")
	i := e.temp()
	e.loops = append(e.loops, "") // continue reaches the loop's own step
	defer func() { e.loops = e.loops[:len(e.loops)-1] }()
	if fn := types.IteratorNext(t); fn != nil {
		e.rangeNext(s, place, t.(*types.Named), fn)
		e.line("}")
		return
	}

	if m, ok := t.Underlying().(*types.Map); ok {
		e.line("for (int64_t " + i + " = sn_map_next(&" + place + ", 0); " + i + " >= 0; " + i + " = sn_map_next(&" + place + ", " + i + " + 1)) {")
		e.push(true)
		e.rangeVar(s.Key, s.Tok, "(("+e.cType(m.Key(), s.Pos())+" *)"+place+".keys)["+i+"]", m.Key(), consume)
		e.rangeVar(s.Value, s.Tok, "(("+e.cType(m.Value(), s.Pos())+" *)"+place+".vals)["+i+"]", m.Value(), consume)
		e.stmts(s.Body.List)
		e.pop(s.Body.Rbrace)
		e.line("}")
		e.line("}")
		return
	}

	if ch, ok := t.Underlying().(*types.Chan); ok {
		e.rangeChan(s, place, ch.Elem())
		e.line("}")
		return
	}

	if isString(t) {
		e.rangeString(s, place, i)
		e.line("}")
		return
	}

	e.line("for (sn_int " + i + " = 0; " + i + " < " + place + ".len; " + i + "++) {")
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

// rangeNext writes a loop over an iterator at place, calling its Next
// until it gives None and binding each item, or the two parts of an
// item that is a pair; an item the loop binds nothing to is dropped.
func (e *Emitter) rangeNext(s *ast.RangeStmt, place string, t *types.Named, fn *types.Func) {
	name, sig := e.methodNameOf(nil, t, fn.Name(), s.Pos())
	if sig == nil {
		return
	}

	item := sig.Results().(*types.Option).Elem()
	got := e.temp()
	e.line("for (;;) {")
	e.push(true)
	e.line(e.cType(sig.Results(), s.Pos()) + " " + got + " = " + name + "(&" + place + ");")
	e.line("if (!" + got + ".has) { break; }")
	if s.Value == nil {
		e.rangeItem(s.Key, s.Tok, got+".val", item, s.Pos())
	} else {
		k, kt := tupleElem(got+".val", item, 0)
		v, vt := tupleElem(got+".val", item, 1)
		e.rangeItem(s.Key, s.Tok, k, kt, s.Pos())
		e.rangeItem(s.Value, s.Tok, v, vt, s.Pos())
	}

	e.stmts(s.Body.List)
	e.pop(s.Body.Rbrace)
	e.line("}")
}

// rangeItem binds x to an iterator's item at place, which is the loop's
// own to move out of, or drops the item when the loop binds nothing.
func (e *Emitter) rangeItem(x ast.Expr, tok token.Kind, place string, t types.Type, pos token.Pos) {
	if x == nil || isBlank(x) {
		if needsDrop(t) {
			e.line(e.dropOf(place, t, pos))
		}

		return
	}

	e.rangeVar(x, tok, place, t, true)
}

// rangeString writes a loop over the runes of the string at place: each
// step decodes the rune at the offset i and moves on by its width, so a
// continue, which runs the loop's step, moves on as well.
func (e *Emitter) rangeString(s *ast.RangeStmt, place, i string) {
	w, r := e.temp(), e.temp()
	e.line("sn_int " + w + " = 0;")
	e.line("for (sn_int " + i + " = 0; " + i + " < " + place + ".len; " + i + " += " + w + ") {")
	e.line("int32_t " + r + ";")
	e.line(w + " = sn_utf8_decode((const uint8_t *)" + place + ".ptr + " + i + ", " + place + ".len - " + i + ", &" + r + ");")
	e.push(true)
	e.rangeVar(s.Key, s.Tok, i, types.Typ[types.Int], false)
	e.rangeVar(s.Value, s.Tok, r, types.Typ[types.Rune], false)
	e.stmts(s.Body.List)
	e.pop(s.Body.Rbrace)
	e.line("}")
}

// rangeChan writes a loop receiving from a channel until it is closed
// and empty. Each value received is owned by the loop variable, or
// dropped at once when the loop binds none.
func (e *Emitter) rangeChan(s *ast.RangeStmt, ch string, elem types.Type) {
	got := e.temp()
	e.line("for (;;) {")
	e.push(true)
	e.line(e.cType(elem, s.Pos()) + " " + got + " = " + e.zero(elem, s.Pos()) + ";")
	e.line("if (!sn_chan_recv(" + ch + ", &" + got + ")) { break; }")
	if s.Key == nil || isBlank(s.Key) {
		if needsDrop(elem) {
			e.line(e.dropOf(got, elem, s.Pos()))
		}
	} else {
		e.rangeVar(s.Key, s.Tok, got, elem, true)
	}

	e.stmts(s.Body.List)
	e.pop(s.Body.Rbrace)
	e.line("}")
}

// sendStmt writes ch <- v: the value, moved out of its place, goes to
// the runtime by address and is copied into the channel.
func (e *Emitter) sendStmt(s *ast.SendStmt) {
	ch := e.looked(s.Chan, lower.ValueType(e.typeOf(s.Chan)))
	elem := lower.ValueType(e.typeOf(s.Chan)).Underlying().(*types.Chan).Elem()
	v := e.spill(e.valueTo(s.Value, elem), elem, s.Pos())
	e.line("sn_chan_send(" + ch + ", &" + v + ");")
}

// rangeVar binds one range variable to the C place of the current
// item, of Snello type itemType: a new name for :=, an assignment for =,
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
	vt := e.sub(e.info.Defs[name].Type())
	value := item
	if depth(vt) > depth(itemType) {
		value = "&" + item
	}

	local := e.declareLocal(name)
	e.line(e.cType(vt, name.Pos()) + " " + local + " = " + value + ";")
	e.discardUnused(name)
	e.own(local, vt)
	if _, borrow := vt.(*types.Ref); consume && !borrow && needsDrop(vt) {
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

		t := lower.ValueType(e.typeOf(s.Lhs[0]))
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

// isFloat reports whether t is a floating-point number.
func isFloat(t types.Type) bool {
	b, ok := lower.ValueType(t).Underlying().(*types.Basic)
	return ok && b.Info()&types.IsFloat != 0
}
