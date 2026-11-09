// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// stmts writes the statements of a block.
func (e *Emitter) stmts(list []ast.Stmt) {
	for _, s := range list {
		e.stmt(s)
	}
}

// blockStmt writes a nested block as a scope of its own.
func (e *Emitter) blockStmt(b *ast.BlockStmt) {
	e.push(false)
	e.stmts(b.List)
	if e.fn.terminated {
		e.fn.frames = e.fn.frames[:len(e.fn.frames)-1]
		return
	}

	e.pop(b.Rbrace)
}

// stmt writes one statement and then drops the temporaries it made.
func (e *Emitter) stmt(s ast.Stmt) {
	mark := len(e.fn.temps)
	e.stmtInner(s)
	if e.fn.terminated {
		e.fn.temps = e.fn.temps[:mark] // dropped by the leave the statement wrote
		return
	}

	e.dropTemps(mark, s.Pos())
}

func (e *Emitter) stmtInner(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.BlockStmt:
		e.blockStmt(s)
	case *ast.DeclStmt:
		e.declStmt(s.Decl)
	case *ast.ExprStmt:
		v := e.expr(s.X)
		if !e.fn.terminated {
			e.discard(v, e.typeOf(s.X), s.Pos())
		}
	case *ast.IncDecStmt:
		e.incDec(s)
	case *ast.AssignStmt:
		e.assign(s)
	case *ast.ReturnStmt:
		e.returnStmt(s)
	case *ast.BranchStmt:
		e.branchStmt(s)
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
		e.unsupported(s.Pos(), strings.TrimPrefix(fmt.Sprintf("%T", s), "*ast.")+" statements")
	}
}

// declStmt writes var x T = v: a slot for each name, holding the value
// given or the zero of its type. A const declared in a function is a
// constant global, as one declared at the top level is, under a name
// of its own, since two functions may each declare one of a name.
func (e *Emitter) declStmt(d *ast.VarDecl) {
	if d.Const {
		for i, name := range d.Names {
			k := e.info.Defs[name].(*types.Const)
			sym := lower.ObjName(k) + "." + strconv.Itoa(len(e.localConst))
			value := ""
			if i < len(d.Values) {
				value = e.staticValue(d.Values[i], k.Type())
			}

			if value == "" {
				e.unsupported(name.Pos(), "a const whose value is not a literal")
				return
			}

			e.localConst[k] = sym
			put(&e.globalDefs, "@", sym, " = internal constant ", e.irType(k.Type(), name.Pos()), " ", value, "\n")
		}

		return
	}

	var parts []value
	if len(d.Values) == 1 && len(d.Names) > 1 {
		parts = e.spread(d.Values[0], len(d.Names))
	}

	for i, name := range d.Names {
		v := e.info.Defs[name].(*types.Var)
		vt := e.sub(v.Type())
		var init value
		switch {
		case parts != nil:
			init = parts[i]
		case i < len(d.Values):
			init = e.valueTo(d.Values[i], vt)
		default:
			t := e.irType(vt, name.Pos())
			init = value{t, zero(t)}
		}

		slot := e.local(v)
		e.store(init, slot)
		e.own(slot, vt)
	}
}

// assign writes x := v, x = v and x op= v.
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

		e.storeTo(lhs, s.Tok, e.valueTo(rhs, t), t)
	default:
		t := lower.ValueType(e.typeOf(lhs))
		if call, ok := e.operatorCall(t, s.Tok.Binary(), false, lhs, []ast.Expr{rhs}, s.Pos()); ok {
			p := e.placeAt(e.lvalue(lhs), e.typeOf(lhs))
			e.store(call, p) // a += b on a type with an Add: the old value went into the method
			return
		}

		p := e.placeAt(e.lvalue(lhs), e.typeOf(lhs))
		ir := e.irType(t, lhs.Pos())
		if isString(t) { // s += t appends in place when s owns the room, and grows by doubling when not
			r := e.spill(e.valueTo(rhs, t))
			e.rt("sn_string_append", "void", value{"ptr", p}, value{"ptr", r})
			if !e.isRead(rhs) {
				e.dropOf(r, t, s.TokPos)
			}

			return
		}

		old := e.load(ir, p)
		v := e.arith(s.Tok.Binary(), old, e.valueTo(rhs, t), t, s.TokPos)
		e.store(v, p)
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

	values, kinds := make([]value, len(s.Lhs)), make([]types.Type, len(s.Lhs))
	switch {
	case len(s.Rhs) == 1 && len(s.Lhs) > 1:
		values = e.spread(s.Rhs[0], len(s.Lhs))
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

			values[i] = e.valueTo(rhs, kinds[i])
		}
	default:
		e.unsupported(s.TokPos, "this assignment of several values")
		return
	}

	for i, lhs := range s.Lhs {
		e.storeTo(lhs, s.Tok, values[i], kinds[i])
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

// storeTo writes v, of type t, into lhs: a declaration for a name :=
// introduces, a drop for _, an assignment otherwise, including a name
// := reuses, which drops what the place held before.
func (e *Emitter) storeTo(lhs ast.Expr, tok token.Kind, v value, t types.Type) {
	if isBlank(lhs) {
		e.discard(v, t, lhs.Pos())
		return
	}

	if name, ok := lhs.(*ast.Ident); ok && tok == token.DEFINE {
		if obj, declared := e.info.Defs[name].(*types.Var); declared {
			slot := e.local(obj)
			e.store(v, slot)
			e.own(slot, e.sub(obj.Type()))
			return
		}
	}

	p := e.lvalue(lhs)
	if needsDrop(t) {
		e.dropOf(p, t, lhs.Pos())
	}

	e.store(v, p)
}

// spread reads a tuple value and returns its n elements.
func (e *Emitter) spread(x ast.Expr, n int) []value {
	t := e.typeOf(x).Underlying().(*types.Tuple)
	v := e.expr(x)
	parts := make([]value, n)
	for i := range parts {
		parts[i] = e.extract(v, i, e.irType(t.At(i), x.Pos()))
	}

	return parts
}

// incDec writes x++ and x--.
func (e *Emitter) incDec(s *ast.IncDecStmt) {
	p := e.placeAt(e.lvalue(s.X), e.typeOf(s.X))
	t := lower.ValueType(e.typeOf(s.X))
	ir := e.irType(t, s.X.Pos())
	old := e.load(ir, p)
	op := token.ADD
	if s.Tok == token.DEC {
		op = token.SUB
	}

	one := value{ir, "1"}
	if isFloat(t) {
		one.s = hexFloat(1, bits(t))
	}

	e.store(e.arith(op, old, one, t, s.TokPos), p)
}

// returnStmt writes a return: with deferred calls pending, the value
// is computed first, then the calls run, then the frames are dropped
// and the value returned.
func (e *Emitter) returnStmt(s *ast.ReturnStmt) {
	switch len(s.Results) {
	case 0:
		e.leaveFunction(s.Pos())
		e.inst("ret void")
	case 1:
		v := e.valueTo(s.Results[0], e.fn.sig.Results())
		e.leaveFunction(s.Pos())
		if e.fn.sig.Results() == types.Unit { // a generic function returning its T, given (): the function is void, and the value was evaluated for its effects
			e.inst("ret void")
			break
		}

		e.inst("ret " + v.typed())
	default:
		e.unsupported(s.Return, "returning several values")
		return
	}

	e.fn.terminated = true
}

// branchStmt writes break and continue as jumps to the innermost
// loop's blocks, after the drops of the loop body's variables.
func (e *Emitter) branchStmt(s *ast.BranchStmt) {
	if len(e.fn.loops) == 0 {
		e.unsupported(s.TokPos, s.Tok.String()+" outside a loop")
		return
	}

	e.leave(e.loopFrame(), s.Pos())
	l := e.fn.loops[len(e.fn.loops)-1]
	if s.Tok == token.BREAK {
		e.jump(l.end)
	} else {
		e.jump(l.next)
	}
}

// ifStmt writes an if: the condition branches to the then block or
// the else block, and both join after. An init statement runs in a
// scope of its own, so its names end with the if.
func (e *Emitter) ifStmt(s *ast.IfStmt) {
	if s.Init != nil {
		e.push(false)
		e.stmt(s.Init)
	}

	c := e.scopedBool(s.Cond)
	then, end := e.block("then"), e.block("end")
	els := end
	if s.Else != nil {
		els = e.block("else")
	}

	e.branch(c.s, then, els)
	e.label(then)
	e.blockStmt(s.Body)
	if !e.fn.terminated {
		e.jump(end)
	}

	if s.Else != nil {
		e.label(els)
		e.stmt(s.Else)
		if !e.fn.terminated {
			e.jump(end)
		}
	}

	e.label(end)
	if s.Init != nil {
		e.pop(s.Body.Rbrace)
	}
}

// forStmt writes the three loop forms: the init in a scope of its own,
// then a block testing the condition, the body, a block running the
// post statement that continue jumps to, and the block after that
// break jumps to.
func (e *Emitter) forStmt(s *ast.ForStmt) {
	e.push(false)
	if s.Init != nil {
		e.stmt(s.Init)
	}

	cond, body, post, end := e.block("for.cond"), e.block("for.body"), e.block("for.post"), e.block("for.end")
	e.jump(cond)
	e.label(cond)
	if s.Cond != nil {
		c := e.scopedBool(s.Cond)
		e.branch(c.s, body, end)
	} else {
		e.jump(body)
	}

	e.label(body)
	e.fn.loops = append(e.fn.loops, loop{next: post, end: end, frame: len(e.fn.frames)})
	e.push(true)
	e.stmts(s.Body.List)
	if !e.fn.terminated {
		e.pop(s.Body.Rbrace)
		e.jump(post)
	} else {
		e.fn.frames = e.fn.frames[:len(e.fn.frames)-1]
	}

	e.fn.loops = e.fn.loops[:len(e.fn.loops)-1]
	e.label(post)
	if s.Post != nil {
		e.stmt(s.Post)
	}

	e.jump(cond)
	e.label(end)
	e.pop(s.Body.Rbrace)
}

// rangeStmt writes for k, v := range x over a slice, an array, a
// string, a map or a channel: an index loop with the key and value
// bound at the top of each pass. A value reached through a mut borrow
// is bound as a pointer into the collection. An owned collection is
// used up by the loop and dropped after it.
func (e *Emitter) rangeStmt(s *ast.RangeStmt) {
	e.push(false)
	p, t, held := e.consumed(s.X)
	if held != "" {
		e.hold(held, e.typeOf(s.X)) // an owned collection is used up by the loop and dropped after it
	}

	consume := held != ""
	switch u := t.Underlying().(type) {
	case *types.Map:
		e.rangeMap(s, p, u, consume)
	case *types.Chan:
		e.rangeChan(s, e.load("ptr", p), u.Elem())
	default:
		if isString(t) {
			e.rangeString(s, p)
			break
		}

		e.rangeIndexed(s, p, t, consume)
	}

	e.pop(s.Body.Rbrace)
}

// rangeString writes a loop over the runes of the string at p: each
// step decodes the rune at the offset and moves on by its width, which
// the step after the body, where a continue goes, adds.
func (e *Emitter) rangeString(s *ast.RangeStmt, p string) {
	base := e.load("ptr", e.gep("%sn_string", p, 0))
	n := e.load("i64", e.gep("%sn_string", p, 1))
	i, w, r := e.slot("i64"), e.slot("i64"), e.slot("i32")
	e.store(value{"i64", "0"}, i)
	cond, body, post, end := e.block("range.cond"), e.block("range.body"), e.block("range.post"), e.block("range.end")
	e.jump(cond)
	e.label(cond)
	cur := e.load("i64", i)
	more := e.op("icmp slt i64 "+cur.s+", "+n.s, "i1")
	e.branch(more.s, body, end)
	e.label(body)
	at := e.elem("i8", base.s, cur.s)
	left := e.op("sub i64 "+n.s+", "+cur.s, "i64")
	e.declare("sn_utf8_decode", "declare i32 @sn_utf8_decode(ptr, i64, ptr)")
	width := e.op("call i32 @sn_utf8_decode(ptr "+at+", i64 "+left.s+", ptr "+r+")", "i32")
	e.store(e.op("sext i32 "+width.s+" to i64", "i64"), w)
	e.fn.loops = append(e.fn.loops, loop{next: post, end: end, frame: len(e.fn.frames)})
	e.push(true)
	e.rangeVar(s.Key, s.Tok, e.spill(cur), types.Typ[types.Int], false)
	e.rangeVar(s.Value, s.Tok, r, types.Typ[types.Rune], false)
	e.stmts(s.Body.List)
	if !e.fn.terminated {
		e.pop(s.Body.Rbrace)
		e.jump(post)
	} else {
		e.fn.frames = e.fn.frames[:len(e.fn.frames)-1]
	}

	e.fn.loops = e.fn.loops[:len(e.fn.loops)-1]
	e.label(post)
	e.store(e.op("add i64 "+cur.s+", "+e.load("i64", w).s, "i64"), i)
	e.jump(cond)
	e.label(end)
}

// rangeIndexed writes a range over a slice, an array or a string.
func (e *Emitter) rangeIndexed(s *ast.RangeStmt, p string, t types.Type, consume bool) {
	var elemType types.Type = types.Typ[types.Uint8]
	var base value
	var n value
	iterator := false
	switch u := t.Underlying().(type) {
	case *types.Slice:
		elemType = u.Elem()
		_, iterator = elemType.(*types.Ref)
		base = e.load("ptr", e.gep("%sn_slice", p, 0))
		n = e.load("i64", e.gep("%sn_slice", p, 1))
	case *types.Array:
		elemType = u.Elem()
		base = value{"ptr", p}
		n = value{"i64", fmt.Sprint(u.Len())}
	case *types.Basic:
		base = e.load("ptr", e.gep("%sn_string", p, 0))
		n = e.load("i64", e.gep("%sn_string", p, 1))
	default:
		e.unsupported(s.X.Pos(), "ranging over a value of type "+t.String())
		return
	}

	i := e.slot("i64")
	e.store(value{"i64", "0"}, i)
	cond, body, post, end := e.block("range.cond"), e.block("range.body"), e.block("range.post"), e.block("range.end")
	e.jump(cond)
	e.label(cond)
	cur := e.load("i64", i)
	more := e.op("icmp slt i64 "+cur.s+", "+n.s, "i1")
	e.branch(more.s, body, end)
	e.label(body)
	e.fn.loops = append(e.fn.loops, loop{next: post, end: end, frame: len(e.fn.frames)})
	e.push(true)
	item := e.elem(e.irType(elemType, s.Pos()), base.s, cur.s)
	if iterator {
		e.rangeVar(s.Key, s.Tok, item, elemType, consume)
	} else {
		e.rangeVar(s.Key, s.Tok, e.spill(cur), types.Typ[types.Int], false)
		e.rangeVar(s.Value, s.Tok, item, elemType, consume)
	}

	e.stmts(s.Body.List)
	if !e.fn.terminated {
		e.pop(s.Body.Rbrace)
		e.jump(post)
	} else {
		e.fn.frames = e.fn.frames[:len(e.fn.frames)-1]
	}

	e.fn.loops = e.fn.loops[:len(e.fn.loops)-1]
	e.label(post)
	e.store(e.op("add i64 "+cur.s+", 1", "i64"), i)
	e.jump(cond)
	e.label(end)
}

// rangeMap writes a range over a map in insertion order, through the
// runtime's walk of its entries.
func (e *Emitter) rangeMap(s *ast.RangeStmt, p string, m *types.Map, consume bool) {
	e.declare("sn_map_next", "declare i64 @sn_map_next(ptr, i64)")
	i := e.slot("i64")
	e.store(e.op("call i64 @sn_map_next(ptr "+p+", i64 0)", "i64"), i)
	cond, body, post, end := e.block("range.cond"), e.block("range.body"), e.block("range.post"), e.block("range.end")
	e.jump(cond)
	e.label(cond)
	cur := e.load("i64", i)
	more := e.op("icmp sge i64 "+cur.s+", 0", "i1")
	e.branch(more.s, body, end)
	e.label(body)
	e.fn.loops = append(e.fn.loops, loop{next: post, end: end, frame: len(e.fn.frames)})
	e.push(true)
	keys := e.load("ptr", e.gep("%sn_map", p, 0))
	vals := e.load("ptr", e.gep("%sn_map", p, 1))
	e.rangeVar(s.Key, s.Tok, e.elem(e.irType(m.Key(), s.Pos()), keys.s, cur.s), m.Key(), consume)
	e.rangeVar(s.Value, s.Tok, e.elem(e.irType(m.Value(), s.Pos()), vals.s, cur.s), m.Value(), consume)
	e.stmts(s.Body.List)
	if !e.fn.terminated {
		e.pop(s.Body.Rbrace)
		e.jump(post)
	} else {
		e.fn.frames = e.fn.frames[:len(e.fn.frames)-1]
	}

	e.fn.loops = e.fn.loops[:len(e.fn.loops)-1]
	e.label(post)
	next := e.op("add i64 "+cur.s+", 1", "i64")
	e.store(e.op("call i64 @sn_map_next(ptr "+p+", i64 "+next.s+")", "i64"), i)
	e.jump(cond)
	e.label(end)
}

// rangeVar binds one range variable to the item at the address p, of
// Snello type itemType: a new name for :=, an assignment for =, nothing
// for _. A mut binding of the item is its address. When the loop
// consumes its collection, an item bound by value leaves it.
func (e *Emitter) rangeVar(x ast.Expr, tok token.Kind, p string, itemType types.Type, consume bool) {
	if x == nil || isBlank(x) {
		return
	}

	if tok != token.DEFINE {
		e.storeTo(x, tok, e.load(e.irType(itemType, x.Pos()), p), itemType)
		return
	}

	name := x.(*ast.Ident)
	v := e.info.Defs[name].(*types.Var)
	vt := e.sub(v.Type())
	slot := e.local(v)
	if depth(vt) > depth(itemType) {
		e.store(value{"ptr", p}, slot)
	} else {
		e.store(e.load(e.irType(vt, name.Pos()), p), slot)
	}

	e.own(slot, vt)
	if _, borrow := vt.(*types.Ref); consume && !borrow && needsDrop(vt) {
		e.zero(p, itemType, name.Pos())
	}
}

// A deferred call runs on every exit of its function, a return or a
// panic, as the C backend writes it (defer.go there): the defer
// statement saves what the call needs into a record in the function's
// frame, the arguments, a receiver it takes by value, a callee that is
// a value, and the address of a receiver it lends; the call is a function
// of its own, the thunk, taking the record's address; the statement
// sets the record's flag and registers the record with the thunk on
// the runtime's guard stack, and a return takes the guard off and
// calls the thunk, last registered first among the function's values
// and deferred calls. The flag is cleared at the function's entry, so
// a return that a defer statement did not reach skips its call.

// deferStmt writes defer f(x): the saved values, the record, the flag
// and the guard here, and the thunk into the module.
func (e *Emitter) deferStmt(s *ast.DeferStmt) {
	call, ok := s.Call.(*ast.CallExpr)
	if !ok {
		e.unsupported(s.Pos(), "defer of something that is not a call")
		return
	}

	if len(e.fn.loops) > 0 {
		e.unsupported(s.Pos(), "defer inside a loop")
		return
	}

	var saved []ast.Expr
	var vals []value
	save := func(x ast.Expr) {
		saved = append(saved, x)
		vals = append(vals, e.expr(x))
	}

	for _, a := range call.Args {
		save(a)
	}

	var recv *types.Var // a receiver lent, reached through its address
	var recvAt string
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr: // a method: its receiver, by value or by address
		switch {
		case e.packageOf(fun.X) != nil:
		case !e.lentReceiver(fun):
			save(fun.X)
		default:
			recv = lower.Variable(e.info, fun.X)
			if recv == nil {
				e.unsupported(fun.X.Pos(), "a deferred call's receiver that is not a variable")
				return
			}

			recvAt = e.varSlot(recv, fun.X.Pos())
		}
	case *ast.Ident: // a function value in a variable; a named function needs nothing
		if _, variable := e.info.Uses[fun].(*types.Var); variable {
			save(fun)
		}
	default:
		save(fun)
	}

	members := []string{"i1"}
	for _, v := range vals {
		members = append(members, v.typ)
	}

	if recv != nil {
		members = append(members, "ptr")
	}

	typ := "{ " + strings.Join(members, ", ") + " }"
	rec := e.slot(typ)
	put(&e.fn.allocas, "  store i1 false, ptr ", rec, ", align 8\n") // the flag, first in the record, cleared at the entry
	for i, v := range vals {
		e.store(v, e.gep(typ, rec, i+1))
	}

	if recv != nil {
		e.store(value{"ptr", recvAt}, e.gep(typ, rec, len(vals)+1))
	}

	e.closures++
	thunk := "sn_defer_" + strconv.Itoa(e.closures)
	e.helper(thunk, "void", []string{"ptr %rec"}, func() {
		for i, x := range saved {
			e.fn.pre[x] = e.gep(typ, "%rec", i+1)
		}

		if recv != nil {
			e.fn.captured = map[*types.Var]string{recv: e.load("ptr", e.gep(typ, "%rec", len(vals)+1)).s}
		}

		e.scoped(call.Pos(), func() { // the call's own temporaries are dropped with it
			v := e.expr(call)
			e.discard(v, e.typeOf(call), call.Pos())
		})

		e.inst("ret void")
	})

	e.store(value{"i1", "true"}, rec)
	e.declare("sn_guard", "declare void @sn_guard(ptr, ptr)")
	e.inst("call void @sn_guard(ptr " + rec + ", ptr @" + thunk + ")")
	e.seq++
	e.fn.defers = append(e.fn.defers, deferred{rec, thunk, e.seq})
}

// runDefer writes a deferred call at a return: when its statement ran,
// its guard comes off, so that a panic in the call does not run it
// again, and the thunk makes the call.
func (e *Emitter) runDefer(d deferred) {
	set := e.load("i1", d.rec)
	run, skip := e.block("defer"), e.block("defer.skip")
	e.branch(set.s, run, skip)
	e.label(run)
	e.declare("sn_unguard", "declare void @sn_unguard(ptr)")
	e.inst("call void @sn_unguard(ptr " + d.rec + ")")
	e.inst("call void @" + d.thunk + "(ptr " + d.rec + ")")
	e.jump(skip)
	e.label(skip)
}

// leaveFunction writes what a return runs: the temporaries pending,
// the variables of every open frame, innermost first, and the deferred
// calls that ran, each before every value registered before it, so
// that a deferred call runs before the drops of what it may borrow, as
// a panic's unwinding of the guard stack runs it.
func (e *Emitter) leaveFunction(pos token.Pos) {
	var order []owned
	for _, v := range slices.Backward(e.fn.temps[e.fn.frames[0].mark:]) {
		order = append(order, v)
	}

	for _, f := range slices.Backward(e.fn.frames) {
		for _, v := range slices.Backward(f.vars) {
			order = append(order, v)
		}
	}

	d := len(e.fn.defers)
	for _, v := range order {
		for ; d > 0 && e.fn.defers[d-1].seq > v.seq; d-- {
			e.runDefer(e.fn.defers[d-1])
		}

		e.dropVars([]owned{v}, pos)
	}

	for ; d > 0; d-- {
		e.runDefer(e.fn.defers[d-1])
	}
}

// lentReceiver reports whether a method call lends its receiver, by
// ref or mut, so that the deferred call reads the variable when it
// runs, rather than taking it by value, as Option's and Result's
// Expect, Unwrap, UnwrapOr, Map and MapErr do; every other predeclared
// method lends its receiver.
func (e *Emitter) lentReceiver(sel *ast.SelectorExpr) bool {
	fn, ok := e.info.Uses[sel.Sel].(*types.Func)
	if !ok {
		return !consuming[sel.Sel.Name]
	}

	_, ok = fn.Type().(*types.Signature).Recv().Type().(*types.Ref)
	return ok
}

// The predeclared methods that take their receiver by value.
var consuming = map[string]bool{"Expect": true, "Unwrap": true, "UnwrapOr": true, "Map": true, "MapErr": true}

func isBlank(x ast.Expr) bool {
	id, ok := x.(*ast.Ident)
	return ok && id.Name == "_"
}
