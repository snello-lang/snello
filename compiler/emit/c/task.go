// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

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
// block of arguments: the receiver and the arguments are evaluated
// here, moved into a block the task owns, and the trampoline, one per
// spawn statement, calls the function with them and frees the block.
// The callee drops its parameters as it does for any call, so nothing
// is dropped here. A closure as the callee is lifted to a function
// whose first parameters are the variables it captures: the checker
// made every capture a move or a copy, so each is a value the task
// owns, passed like an argument and dropped by the lifted function
// when it ends.

// A handle is what a spawn written as an expression gives: the channel
// of one slot the task's result or panic goes through, made once the
// operands are evaluated, since an operand may leave the statement,
// whose second handle the task takes; and the type of what goes
// through it, a Result of the call's result and task.Panic.
type handle struct {
	done string
	elem types.Type
}

// spawnStmt writes spawn f(x), spawn v.M(x) or spawn func() { ... }().
func (e *Emitter) spawnStmt(s *ast.SpawnStmt) {
	if x, ok := s.Call.(*ast.CallExpr); ok {
		e.spawn(x, nil, s.Pos())
	}
}

// spawnExpr writes h := spawn f(x): the channel the handle and the
// task share, the spawn with the task's handle in its block, and the
// value, a task.Handle holding the other.
func (e *Emitter) spawnExpr(x *ast.SpawnExpr) string {
	call, ok := x.Call.(*ast.CallExpr)
	if !ok {
		return "0" // reported by the parser
	}

	t := e.typeOf(x).(*types.Named)
	field := t.Underlying().(*types.Struct).Field(0)
	h := &handle{elem: field.Type().Underlying().(*types.Chan).Elem()}
	e.spawn(call, h, x.Pos())
	if h.done == "" {
		return "0" // the spawn was not written
	}

	return "(" + e.cType(t, x.Pos()) + "){." + lower.Ident(field.Name()) + " = " + h.done + "}"
}

// spawn writes the spawn of the call x, with a handle when the spawn
// is an expression.
func (e *Emitter) spawn(x *ast.CallExpr, h *handle, pos token.Pos) {
	fun := x.Fun
	for {
		p, ok := fun.(*ast.ParenExpr)
		if !ok {
			break
		}

		fun = p.X
	}

	if lit, ok := fun.(*ast.FuncLit); ok {
		e.spawnClosure(x, lit, h, pos)
		return
	}

	name, sig, recv := e.spawnee(fun, pos)
	if name == "" {
		return
	}

	wants, values := e.callOperands(recv, x.Args, x.Ellipsis.IsValid(), sig)
	e.unframeLast(sig, x.Ellipsis.IsValid(), values, pos) // the task outlives this frame
	e.tasks++
	e.spawnCall(e.tasks-1, name, wants, values, sig.Results(), h, pos)
}

// spawnClosure lifts a spawned closure to a function of its captures
// and its parameters, written like any function body with the
// captures as plain locals, and spawns it with the captured values and
// the arguments. A capture the closure moved, or would have borrowed,
// leaves its variable empty here; a Copy value it only reads is copied.
func (e *Emitter) spawnClosure(x *ast.CallExpr, lit *ast.FuncLit, h *handle, pos token.Pos) {
	sig := e.typeOf(lit).(*types.Signature)
	captured := e.captures(lit)
	params := make([]*types.Var, 0, len(captured)+len(sig.Params()))
	values := make([]string, 0, len(captured)+len(x.Args))
	for _, cap := range captured {
		t := e.sub(cap.Var.Type())
		p := types.NewVar(cap.Var.Pos(), cap.Var.Name(), t)
		if local, renamed := e.names.renamed[cap.Var]; renamed {
			e.names.renamed[p] = local // the body names the capture as the enclosing function did
		}

		params = append(params, p)
		v := e.variable(cap.Var, e.captured)
		if !cap.Copied && needsDrop(t) { // moved into the task: the place is emptied
			tmp := e.temp()
			e.line(e.cType(t, pos) + " " + tmp + " = " + v + ";")
			e.line(v + " = " + e.zero(t, pos) + ";")
			v = tmp
		}

		values = append(values, v)
	}

	params = append(params, sig.Params()...)
	lifted := types.NewSignature(nil, params, sig.Results())
	_, args := e.callOperands(nil, x.Args, x.Ellipsis.IsValid(), sig)
	e.unframeLast(sig, x.Ellipsis.IsValid(), args, pos) // the task outlives this frame
	values = append(values, args...)
	wants := make([]types.Type, len(params))
	list := make([]string, len(params))
	for i, p := range params {
		wants[i] = p.Type()
		list[i] = e.cType(p.Type(), pos) + " " + e.localName(p)
	}

	if len(list) == 0 {
		list = []string{"void"}
	}

	k := e.tasks
	e.tasks++
	name := lower.SpawnTask(k) + "_fn"
	head := "static " + e.resultType(lifted, pos) + " " + name + "(" + strings.Join(list, ", ") + ")"
	put(&e.protos, head, ";\n")
	outer, outerCaptured := e.body, e.captured
	e.body, e.captured = bytes.Buffer{}, nil // the captures are the function's own locals
	e.nclosure++
	display := e.within + ".func" + strconv.Itoa(e.nclosure)
	e.traced = append(e.traced, lower.TraceName{Symbol: name, Name: display})
	outerAt, outerWithin, outerClosures := e.at, e.within, e.nclosure
	e.at, e.within, e.nclosure = pos, display, 0
	e.line(head + " {")
	e.function(lifted, lit.Body)
	e.endPositions()
	e.at, e.within, e.nclosure = outerAt, outerWithin, outerClosures
	e.line("")
	e.helpers.Write(e.body.Bytes())
	e.body, e.captured = outer, outerCaptured
	e.spawnCall(k, name, wants, values, sig.Results(), h, pos)
}

// spawnCall writes the block of a task's values, named after the
// task and built in the spawning function's frame, which the runtime
// copies into the task's record; the trampoline that calls the
// function with the copy; and the spawn. A result the function
// returns is dropped by the trampoline, as the statement discards it;
// with a handle the block also holds the task's channel handle and the
// result, which the trampoline sends after the call, or the panic that
// ended it.
func (e *Emitter) spawnCall(k int, name string, wants []types.Type, values []string, results types.Type, h *handle, pos token.Pos) {
	st := "struct " + lower.SpawnTask(k)
	var fields strings.Builder
	args := make([]string, len(values))
	for i, t := range wants {
		put(&fields, "\t", e.cType(t, pos), " a", strconv.Itoa(i), ";\n")
		args[i] = "a->a" + strconv.Itoa(i)
	}

	if h != nil {
		fields.WriteString("\tsn_chan *sn_done;\n")
		if results != types.Unit {
			put(&fields, "\t", e.cType(results, pos), " sn_result;\n")
		}
	}

	if fields.Len() == 0 {
		fields.WriteString("\tchar sn_pad;\n")
	}

	put(&e.types, st, " {\n", fields.String(), "};\n\n")
	call := name + "(" + strings.Join(args, ", ") + ")"
	if h == nil {
		e.spawnEntry(k, st, call, len(values) > 0, results, pos)
	} else {
		e.caughtEntry(k, st, call, len(values) > 0, results, h.elem, pos)
	}

	block := e.temp()
	e.line(st + " " + block + " = {0};") // whole, so that a block with no value is defined where it is copied from
	for i, v := range values {
		e.line(block + ".a" + strconv.Itoa(i) + " = " + v + ";")
	}

	if h != nil {
		h.done = e.temp()
		e.line("sn_chan *" + h.done + " = sn_chan_handle(" + e.elemSize(h.elem, pos) + ", " + e.elemDropper(lower.ChanDrop(h.elem), h.elem, pos) + ", " + e.madeAt(pos) + ");") // one slot, with the task's handle counted
		e.line(block + ".sn_done = " + h.done + ";")                                                                                                                            // the task's handle, which sn_chan_handle counted
	}

	entry := lower.SpawnEntry(k) + ", &" + block + ", sizeof " + block + ", " + lower.StackBounds + "[" + strconv.Itoa(k) + "]"
	if h == nil {
		e.line("sn_spawn(" + entry + ");")
		return
	}

	e.line("sn_spawn_handle(" + entry + ", " + h.done + ");") // the channel says where the task's own scope is
}

// spawnEntry writes the trampoline of a spawn statement's task: the
// call on the record's copy of the block, its result dropped.
func (e *Emitter) spawnEntry(k int, st, call string, unpack bool, results types.Type, pos token.Pos) {
	body := "\t" + call + ";\n"
	if results != types.Unit {
		body = "\t" + e.cType(results, pos) + " r = " + call + ";\n"
		if needsDrop(results) {
			body += "\t" + e.dropOf("r", results, pos) + "\n"
		} else {
			body += "\t(void)r;\n"
		}
	}

	a := ""
	if unpack {
		a = "\t" + st + " *a = p;\n"
	}

	if !unpack {
		a = "\t(void)p;\n"
	}

	put(&e.helpers, "static void ", lower.SpawnEntry(k), "(void *p) {\n", a, body, "}\n\n")
}

// caughtEntry writes the trampoline of a task with a handle: the call
// in a function of its own, run under the runtime's mark, which puts
// its result in the block; and the entry, which offers the handle Ok
// with the result or Err with the panic, drops the value when the
// handle was dropped before, gives the task's channel handle up and
// frees the block.
func (e *Emitter) caughtEntry(k int, st, call string, unpack bool, results, elem types.Type, pos token.Pos) {
	res := elem.(*types.Result)
	a, body := "\t(void)p;\n", "\t"+call+";\n"
	if unpack || results != types.Unit {
		a = "\t" + st + " *a = p;\n"
	}

	if results != types.Unit {
		body = "\ta->sn_result = " + call + ";\n"
	}

	put(&e.helpers, "static void ", lower.SpawnBody(k), "(void *p) {\n", a, body, "}\n\n")
	returned := e.zero(types.Unit, pos)
	if results != types.Unit {
		returned = "a->sn_result"
	}

	failed := res.Err().(*types.Named)
	message := failed.Underlying().(*types.Struct).Field(0)
	panicked := "(" + e.cType(failed, pos) + "){." + lower.Ident(message.Name()) + " = sn_msg}"
	put(&e.helpers, "static void ", lower.SpawnEntry(k), "(void *p) {\n",
		"\t", st, " *a = p;\n",
		"\tsn_string sn_msg = {0};\n",
		"\t", e.cType(elem, pos), " r;\n",
		"\tif (sn_catch(", lower.SpawnBody(k), ", a, &sn_msg, true)) {\n",
		"\t\tsn_handle_failed(a->sn_done); // cancels the task's group, if one took it\n",
		"\t\tr = ", e.variantValue(elem, types.ErrVariant, []string{panicked}, pos), ";\n",
		"\t} else {\n",
		"\t\tr = ", e.variantValue(elem, types.OkVariant, []string{returned}, pos), ";\n",
		"\t}\n",
		"\tif (!sn_chan_offer(a->sn_done, &r)) {\n",
		"\t\t", e.dropOf("r", elem, pos), "\n",
		"\t}\n",
		"\tsn_chan_drop(&a->sn_done, "+strconv.Itoa(lower.HandleBoth)+");\n}\n\n")
}

// spawnee names the C function a spawned call of fun runs, with its
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

		if fn.Static() { // a function of a type: no receiver
			name, sig := e.staticCallee(fun, pos)
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
// body of the case it chose as a chain of ifs, never a C switch, so a
// break inside a case leaves the loop around the select as the
// language says. The last case is the chain's else, since sn_select
// chose one of them, so the C compiler sees that a select whose every
// case returns does not fall through; a select with no case never
// returns at all. A receive case gets its value as the Option the
// ordinary receive would build, pre-evaluated for the case's own
// statement, so a binding, an assignment and a bare receive are
// written by the paths that write them anywhere else. The value of a
// send case that was not chosen is dropped after the select, since the
// move out of its place happened on entry.

// selectStmt writes a select statement.
func (e *Emitter) selectStmt(s *ast.SelectStmt) {
	type sent struct {
		index int
		value string
		typ   types.Type
	}

	var sends []sent
	hasDefault := false
	n := 0
	for _, cc := range s.Body {
		if cc.Comm == nil {
			hasDefault = true
		} else {
			n++
		}
	}

	e.line("{")
	mark := len(e.temps) // a case's channel made here, a handle a call returned, is dropped in this block
	cases := e.temp()
	e.line("sn_case " + cases + "[" + strconv.Itoa(max(n, 1)) + "];")
	i := 0
	for _, cc := range s.Body {
		if cc.Comm == nil {
			continue
		}

		if send, ok := cc.Comm.(*ast.SendStmt); ok {
			ch := e.looked(send.Chan, lower.ValueType(e.typeOf(send.Chan)))
			elem := lower.ValueType(e.typeOf(send.Chan)).Underlying().(*types.Chan).Elem()
			v := e.spill(e.valueTo(send.Value, elem), elem, send.Pos())
			e.line(cases + "[" + strconv.Itoa(i) + "] = (sn_case){" + ch + ", &" + v + ", true, false};")
			sends = append(sends, sent{i, v, elem})
			i++
			continue
		}

		recv := receiveOf(cc.Comm)
		ch := e.looked(recv.X, lower.ValueType(e.typeOf(recv.X)))
		elem := lower.ValueType(e.typeOf(recv.X)).Underlying().(*types.Chan).Elem()
		got := e.temp()
		e.line(e.cType(elem, recv.Pos()) + " " + got + " = " + e.zero(elem, recv.Pos()) + ";")
		e.line(cases + "[" + strconv.Itoa(i) + "] = (sn_case){" + ch + ", &" + got + ", false, false};")
		e.pre[recv] = "(" + e.cType(e.typeOf(recv), recv.Pos()) + "){.has = " + cases + "[" + strconv.Itoa(i) + "].ok, .val = " + got + "}"
		i++
	}

	if len(s.Body) == 0 {
		e.line("sn_select(" + cases + ", 0, false);")
		e.line("__builtin_unreachable();")
		e.line("}")
		return
	}

	chosen := e.temp()
	e.line("int " + chosen + " = sn_select(" + cases + ", " + strconv.Itoa(n) + ", " + strconv.FormatBool(hasDefault) + ");")
	for _, snd := range sends {
		if needsDrop(snd.typ) {
			e.line("if (" + chosen + " != " + strconv.Itoa(snd.index) + ") { " + e.dropOf(snd.value, snd.typ, s.Pos()) + " }")
		}
	}

	i = 0
	for j, cc := range s.Body {
		cond := chosen + " < 0" // the default
		if cc.Comm != nil {
			cond = chosen + " == " + strconv.Itoa(i)
			i++
		}

		switch {
		case j == 0:
			e.line("if (" + cond + ") {")
		case j == len(s.Body)-1:
			e.line("} else {")
		default:
			e.line("} else if (" + cond + ") {")
		}

		e.push(false)
		switch cc.Comm.(type) {
		case nil, *ast.SendStmt: // the send happened in sn_select
		default:
			e.stmt(cc.Comm) // the receive, pre-evaluated: a binding, an assignment or a discard
			delete(e.pre, receiveOf(cc.Comm))
		}

		e.stmts(cc.Body)
		e.pop(cc.Case)
	}

	e.line("}")
	e.dropTemps(mark, s.Pos())
	e.line("}")
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
// the linked program writes the bounds it proves; volatile, so that no
// optimizer reads an entry as the constant it starts as. A program with
// no spawn has a table of one entry, which the runtime never reads.
func (e *Emitter) stackBounds(out *bytes.Buffer) {
	n := max(e.tasks, 1)
	entries := strings.Repeat(strconv.FormatUint(lower.Unbounded, 10)+"u, ", n)
	put(out, "volatile uint32_t ", lower.StackBounds, "[", strconv.Itoa(n), "] = {", strings.TrimSuffix(entries, ", "), "};\n\n")
}
