// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// A spawn statement becomes a call of sn_spawn with a trampoline and a
// block of arguments (_docs/design/06-tasks-and-channels.md): the
// receiver and the arguments are evaluated here, moved into a block
// the task owns, and the trampoline, one per spawn statement, calls
// the function with them and frees the block. The callee drops its
// parameters as it does for any call, so nothing is dropped here. A
// closure as the callee is lifted to a function whose first parameters
// are the variables it captures: the checker made every capture a move
// or a copy, so each is a value the task owns, passed like an argument
// and dropped by the lifted function when it ends.

// chanOperand writes the channel an operation is on, which the
// operation borrows: a handle a call made, which no place holds, is a
// temporary of the statement and is dropped at its end.
func (e *Emitter) chanOperand(x ast.Expr) value {
	t := e.typeOf(x)
	ch := e.valueTo(x, lower.ValueType(t))
	if _, owned := t.Underlying().(*types.Chan); owned && !e.isPlace(x) {
		e.hold(e.spill(ch), t)
	}

	return ch
}

// receive writes <-ch: the value received as Some, or None once the
// channel is closed and empty.
func (e *Emitter) receive(x *ast.UnaryExpr) value {
	opt := e.typeOf(x)
	elem := lower.ValueType(opt).Underlying().(*types.Option).Elem()
	ch := e.chanOperand(x.X)
	got := e.slot(e.irType(elem, x.Pos()))
	e.zero(got, elem, x.Pos())
	ok := e.rt("sn_chan_recv", "i1", ch, value{"ptr", got})
	return e.optionOf(opt, ok, e.load(e.irType(elem, x.Pos()), got), x.Pos())
}

// sendStmt writes ch <- v: the value, moved out of its place, goes to
// the runtime by address and is copied into the channel.
func (e *Emitter) sendStmt(s *ast.SendStmt) {
	ch := e.chanOperand(s.Chan)
	elem := lower.ValueType(e.typeOf(s.Chan)).Underlying().(*types.Chan).Elem()
	v := e.spill(e.valueTo(s.Value, elem))
	e.rt("sn_chan_send", "void", ch, value{"ptr", v})
}

// rangeChan writes a loop receiving from the channel ch until it is
// closed and empty. Each value received is owned by the loop variable,
// or dropped at once when the loop binds none.
func (e *Emitter) rangeChan(s *ast.RangeStmt, ch value, elem types.Type) {
	got := e.slot(e.irType(elem, s.Pos()))
	again, body, end := e.block("range.recv"), e.block("range.body"), e.block("range.end")
	e.jump(again)
	e.label(again)
	e.zero(got, elem, s.Pos())
	ok := e.rt("sn_chan_recv", "i1", ch, value{"ptr", got})
	e.branch(ok.s, body, end)
	e.label(body)
	e.fn.loops = append(e.fn.loops, loop{next: again, end: end, frame: len(e.fn.frames)})
	e.push(true)
	if s.Key == nil || isBlank(s.Key) {
		e.dropIfNeeded(got, elem, s.Pos())
	} else {
		e.rangeVar(s.Key, s.Tok, got, elem, true)
	}

	e.stmts(s.Body.List)
	if !e.fn.terminated {
		e.pop(s.Body.Rbrace)
		e.jump(again)
	} else {
		e.fn.frames = e.fn.frames[:len(e.fn.frames)-1]
	}

	e.fn.loops = e.fn.loops[:len(e.fn.loops)-1]
	e.label(end)
}

// spawnStmt writes spawn f(x), spawn v.M(x) or spawn func() { ... }().
func (e *Emitter) spawnStmt(s *ast.SpawnStmt) {
	x, ok := s.Call.(*ast.CallExpr)
	if !ok {
		return // reported by the checker
	}

	fun := x.Fun
	for {
		p, ok := fun.(*ast.ParenExpr)
		if !ok {
			break
		}

		fun = p.X
	}

	if lit, ok := fun.(*ast.FuncLit); ok {
		e.spawnClosure(x, lit, s.Pos())
		return
	}

	name, sig, recv := e.spawnee(fun, s.Pos())
	if name == "" {
		return
	}

	wants, values := e.callOperands(recv, x.Args, sig)
	e.tasks++
	e.spawnCall(e.tasks-1, name, sig, wants, values, s.Pos())
}

// spawnClosure lifts a spawned closure to a function of its captures
// and its parameters, written like any function body with the
// captures as parameters under their own names, and spawns it with the
// captured values and the arguments. A capture the closure moved, or
// would have borrowed, leaves its variable empty here; a Copy value it
// only reads is copied.
func (e *Emitter) spawnClosure(x *ast.CallExpr, lit *ast.FuncLit, pos token.Pos) {
	sig := e.typeOf(lit).(*types.Signature)
	captured := e.captures(lit)
	params := make([]*types.Var, 0, len(captured)+len(sig.Params()))
	values := make([]value, 0, len(captured)+len(x.Args))
	wants := make([]types.Type, 0, len(captured)+len(x.Args))
	for _, cap := range captured {
		t := e.sub(cap.Var.Type())
		params = append(params, cap.Var) // the variable itself: the body reaches it as the parameter it became
		wants = append(wants, t)
		at := e.varSlot(cap.Var, pos)
		v := e.load(e.irType(t, pos), at)
		if !cap.Copied && needsDrop(t) { // moved into the task: the place is emptied
			e.zero(at, t, pos)
		}

		values = append(values, v)
	}

	params = append(params, e.info.Types[lit].(*types.Signature).Params()...) // the declaration's own objects, which the body names
	lifted := types.NewSignature(nil, params, sig.Results())
	argWants, args := e.callOperands(nil, x.Args, sig)
	wants, values = append(wants, argWants...), append(values, args...)
	k := e.tasks
	e.tasks++
	name := lower.SpawnTask(k) + "_fn"
	e.writeBody(name, lifted, params, nil, nil, lit.Body, pos)
	e.spawnCall(k, name, lifted, wants, values, pos)
}

// spawnCall writes the block of a task's values, the trampoline that
// calls the function with them and frees the block, and the spawn. A
// result the function returns is dropped by the trampoline, as the
// statement discards it.
func (e *Emitter) spawnCall(k int, name string, sig *types.Signature, wants []types.Type, values []value, pos token.Pos) {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = v.typ
	}

	st := "{ " + strings.Join(parts, ", ") + " }"
	e.helper(lower.SpawnEntry(k), "void", []string{"ptr %p"}, func() {
		args := make([]value, len(values))
		for i, v := range values {
			args[i] = e.load(v.typ, e.gep(st, "%p", i))
		}

		r := e.callValues(name, sig, wants, args, pos)
		e.discard(r, sig.Results(), pos)
		e.declare("sn_free", "declare void @sn_free(ptr)")
		e.inst("call void @sn_free(ptr %p)")
		e.inst("ret void")
		e.fn.terminated = true
	})

	e.declare("sn_alloc", "declare ptr @sn_alloc(i64)")
	block := e.op("call ptr @sn_alloc(i64 "+strconv.FormatInt(max(e.structSize(wants), 1), 10)+")", "ptr")
	for i, v := range values {
		e.store(v, e.gep(st, block.s, i))
	}

	bound := e.op("load volatile i32, ptr getelementptr inbounds (i32, ptr @"+lower.StackBounds+", i64 "+strconv.Itoa(k)+")", "i32")
	e.declare("sn_spawn", "declare void @sn_spawn(ptr, ptr, i32)")
	e.inst("call void @sn_spawn(ptr @" + lower.SpawnEntry(k) + ", " + block.typed() + ", " + bound.typed() + ")")
}

// spawnee names the function a spawned call of fun runs, with its
// signature and its receiver expression when it is a method call: a
// function of the program or a library, an instance of a generic one,
// or a method of a declared type taking its receiver by value, which
// the checker made sure of. Anything else is not written yet.
func (e *Emitter) spawnee(fun ast.Expr, pos token.Pos) (string, *types.Signature, ast.Expr) {
	switch fun := fun.(type) {
	case *ast.Ident:
		if fn, ok := e.info.Uses[fun].(*types.Func); ok && lower.HasSymbol(fn) {
			name, sig := e.calleeName(fun, fn)
			return name, sig, nil
		}
	case *ast.SelectorExpr:
		fn, ok := e.info.Uses[fun.Sel].(*types.Func)
		if !ok || !lower.HasSymbol(fn) {
			break
		}

		if e.packageOf(fun.X) != nil {
			name, sig := e.calleeName(fun.Sel, fn)
			return name, sig, nil
		}

		sig := fn.Type().(*types.Signature)
		if named, ok := lower.ValueType(sig.Recv().Type()).(*types.Named); ok {
			name, sig := e.methodNameOf(fun.Sel, named, fun.Sel.Name, pos)
			if sig == nil {
				return "", nil, nil
			}

			return name, sig, fun.X
		}
	}

	e.unsupported(pos, "a spawned call of this kind")
	return "", nil, nil
}

// calleeName names a function at a call, instantiated when it is
// generic.
func (e *Emitter) calleeName(id *ast.Ident, fn *types.Func) (string, *types.Signature) {
	if e.generics[fn] != nil {
		return e.callInstance(id, fn, nil)
	}

	return lower.FuncName(fn), fn.Type().(*types.Signature)
}

// A select evaluates every case's channel, and a send case's value, on
// entry, hands them to sn_select as an array of cases, and runs the
// body of the case it chose, each under a test of the chosen index, so
// a break inside a case leaves the loop around the select as the
// language says. A receive case gets its value as the Option the
// ordinary receive would build, pre-evaluated for the case's own
// statement, so a binding, an assignment and a bare receive are
// written by the paths that write them anywhere else. The value of a
// send case that was not chosen is dropped after the select, since the
// move out of its place happened on entry.

// selectStmt writes a select statement.
func (e *Emitter) selectStmt(s *ast.SelectStmt) {
	type sent struct {
		index int
		slot  string
		typ   types.Type
	}

	type received struct {
		index int
		recv  *ast.UnaryExpr
		got   string
	}

	var sends []sent
	var recvs []received
	hasDefault := false
	n := 0
	for _, cc := range s.Body {
		if cc.Comm == nil {
			hasDefault = true
		} else {
			n++
		}
	}

	cases := e.slot("[" + strconv.Itoa(max(n, 1)) + " x %sn_case]")
	at := func(i int) string {
		return e.op("getelementptr inbounds ["+strconv.Itoa(max(n, 1))+" x %sn_case], ptr "+cases+", i64 0, i64 "+strconv.Itoa(i), "ptr").s
	}

	setCase := func(i int, ch value, v string, send bool) {
		c := at(i)
		e.store(ch, e.gep("%sn_case", c, 0))
		e.store(value{"ptr", v}, e.gep("%sn_case", c, 1))
		e.store(value{"i8", strconv.Itoa(boolBit(send))}, e.gep("%sn_case", c, 2))
		e.store(value{"i8", "0"}, e.gep("%sn_case", c, 3))
	}

	i := 0
	for _, cc := range s.Body {
		if cc.Comm == nil {
			continue
		}

		if send, ok := cc.Comm.(*ast.SendStmt); ok {
			ch := e.chanOperand(send.Chan)
			elem := lower.ValueType(e.typeOf(send.Chan)).Underlying().(*types.Chan).Elem()
			v := e.spill(e.valueTo(send.Value, elem))
			setCase(i, ch, v, true)
			sends = append(sends, sent{i, v, elem})
			i++
			continue
		}

		recv := receiveOf(cc.Comm)
		ch := e.chanOperand(recv.X)
		elem := lower.ValueType(e.typeOf(recv.X)).Underlying().(*types.Chan).Elem()
		got := e.slot(e.irType(elem, recv.Pos()))
		e.zero(got, elem, recv.Pos())
		setCase(i, ch, got, false)
		recvs = append(recvs, received{i, recv, got})
		i++
	}

	chosen := e.rt("sn_select", "i32", value{"ptr", cases}, value{"i32", strconv.Itoa(n)}, value{"i1", strconv.FormatBool(hasDefault)})
	for _, snd := range sends {
		if !needsDrop(snd.typ) {
			continue
		}

		unsent := e.op("icmp ne "+chosen.typed()+", "+strconv.Itoa(snd.index), "i1")
		drop, next := e.block("unsent"), e.block("sent")
		e.branch(unsent.s, drop, next)
		e.label(drop)
		e.dropOf(snd.slot, snd.typ, s.Pos())
		e.jump(next)
		e.label(next)
	}

	for _, r := range recvs { // the option the receive would have built, from the case's verdict
		opt := e.typeOf(r.recv)
		elem := lower.ValueType(opt).Underlying().(*types.Option).Elem()
		ok := e.op("trunc "+e.load("i8", e.gep("%sn_case", at(r.index), 3)).typed()+" to i1", "i1")
		e.fn.pre[r.recv] = e.spill(e.optionOf(opt, ok, e.load(e.irType(elem, r.recv.Pos()), r.got), r.recv.Pos()))
	}

	end := e.block("select.end")
	i = 0
	for _, cc := range s.Body {
		var cond value
		if cc.Comm == nil {
			cond = e.op("icmp slt "+chosen.typed()+", 0", "i1") // the default
		} else {
			cond = e.op("icmp eq "+chosen.typed()+", "+strconv.Itoa(i), "i1")
			i++
		}

		body, next := e.block("case"), e.block("case.next")
		e.branch(cond.s, body, next)
		e.label(body)
		e.push(false)
		switch cc.Comm.(type) {
		case nil, *ast.SendStmt: // the send happened in sn_select
		default:
			e.stmt(cc.Comm) // the receive, pre-evaluated: a binding, an assignment or a discard
			delete(e.fn.pre, receiveOf(cc.Comm))
		}

		e.stmts(cc.Body)
		if !e.fn.terminated {
			e.pop(cc.Case)
			e.jump(end)
		} else {
			e.fn.frames = e.fn.frames[:len(e.fn.frames)-1]
		}

		e.label(next)
	}

	e.jump(end)
	e.label(end)
}

// boolBit is 1 for true and 0 for false, as C stores a bool.
func boolBit(b bool) int {
	if b {
		return 1
	}

	return 0
}

// receiveOf returns the receive expression of a select case that is
// not a send: the expression of an expression statement, or the right
// side of an assignment.
func receiveOf(comm ast.Stmt) *ast.UnaryExpr {
	var x ast.Expr
	switch comm := comm.(type) {
	case *ast.ExprStmt:
		x = comm.X
	case *ast.AssignStmt:
		x = comm.Rhs[0]
	}

	for {
		p, ok := x.(*ast.ParenExpr)
		if !ok {
			break
		}

		x = p.X
	}

	return x.(*ast.UnaryExpr)
}

// stackBounds writes the table of the stack each spawn's task can use,
// lower.StackBounds, every entry lower.Unbounded until the analysis of
// the linked program writes the bounds it proves; each spawn loads its
// entry volatile, so that no optimizer reads it as the constant it
// starts as. A program with no spawn has a table of one entry.
func (e *Emitter) stackBounds(out *bytes.Buffer) {
	n := max(e.tasks, 1)
	unbounded := uint32(lower.Unbounded)
	entries := strings.Repeat("i32 "+strconv.Itoa(int(int32(unbounded)))+", ", n) // the IR writes an i32 signed
	put(out, "@", lower.StackBounds, " = global [", strconv.Itoa(n), " x i32] [", strings.TrimSuffix(entries, ", "), "]\n\n")
}
