// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/teak-lang/teak/compiler/ast"
	"github.com/teak-lang/teak/compiler/emit/lower"
	"github.com/teak-lang/teak/compiler/token"
	"github.com/teak-lang/teak/compiler/types"
)

// A spawn statement becomes a call of tk_spawn with a trampoline and a
// block of arguments (_docs/design/06-tasks-and-channels.md): the
// receiver and the arguments are evaluated here, moved into a block
// the task owns, and the trampoline, one per spawn statement, calls
// the function with them and frees the block. The callee drops its
// parameters as it does for any call, so nothing is dropped here. A
// closure as the callee is lifted to a function whose first parameters
// are the variables it captures: the checker made every capture a move
// or a copy, so each is a value the task owns, passed like an argument
// and dropped by the lifted function when it ends.

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
	e.spawnCall("tk_task_"+strconv.Itoa(e.tasks), name, wants, values, sig.Results(), s.Pos())
}

// spawnClosure lifts a spawned closure to a function of its captures
// and its parameters, written like any function body with the
// captures as plain locals, and spawns it with the captured values and
// the arguments. A capture the closure moved, or would have borrowed,
// leaves its variable empty here; a Copy value it only reads is copied.
func (e *Emitter) spawnClosure(x *ast.CallExpr, lit *ast.FuncLit, pos token.Pos) {
	sig := e.typeOf(lit).(*types.Signature)
	captured := e.captures(lit)
	params := make([]*types.Var, 0, len(captured)+len(sig.Params()))
	values := make([]string, 0, len(captured)+len(x.Args))
	for _, cap := range captured {
		t := e.sub(cap.Var.Type())
		params = append(params, types.NewVar(cap.Var.Pos(), cap.Var.Name(), t))
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
	_, args := e.callOperands(nil, x.Args, sig)
	values = append(values, args...)
	wants := make([]types.Type, len(params))
	list := make([]string, len(params))
	for i, p := range params {
		wants[i] = p.Type()
		list[i] = e.cType(p.Type(), pos) + " " + lower.Ident(p.Name())
	}

	if len(list) == 0 {
		list = []string{"void"}
	}

	e.tasks++
	task := "tk_task_" + strconv.Itoa(e.tasks)
	name := task + "_fn"
	head := "static " + e.resultType(lifted, pos) + " " + name + "(" + strings.Join(list, ", ") + ")"
	put(&e.protos, head, ";\n")
	outer, outerCaptured := e.body, e.captured
	e.body, e.captured = bytes.Buffer{}, nil // the captures are the function's own locals
	e.line(head + " {")
	e.function(lifted, lit.Body)
	e.line("")
	e.helpers.Write(e.body.Bytes())
	e.body, e.captured = outer, outerCaptured
	e.spawnCall(task, name, wants, values, sig.Results(), pos)
}

// spawnCall writes the block of a task's values, named after the
// task, the trampoline that calls the function with them and frees
// the block, and the spawn. A result the function returns is dropped
// by the trampoline, as the statement discards it.
func (e *Emitter) spawnCall(task, name string, wants []types.Type, values []string, results types.Type, pos token.Pos) {
	st := "struct " + task
	fields, args := "\tchar tk_pad;\n", make([]string, len(values))
	if len(values) > 0 {
		var b strings.Builder
		for i, t := range wants {
			put(&b, "\t", e.cType(t, pos), " a", strconv.Itoa(i), ";\n")
			args[i] = "a->a" + strconv.Itoa(i)
		}

		fields = b.String()
	}

	unpack := ""
	if len(values) > 0 {
		unpack = "\t" + st + " *a = p;\n"
	}

	call := "\t" + name + "(" + strings.Join(args, ", ") + ");\n"
	if results != types.Unit {
		call = "\t" + e.cType(results, pos) + " r = " + name + "(" + strings.Join(args, ", ") + ");\n"
		if needsDrop(results) {
			call += "\t" + e.dropOf("r", results, pos) + "\n"
		} else {
			call += "\t(void)r;\n"
		}
	}

	put(&e.types, st, " {\n", fields, "};\n\n")
	put(&e.helpers, "static void ", task, "_run(void *p) {\n", unpack, call, "\ttk_free(p);\n}\n\n")
	block := e.temp()
	e.line(st + " *" + block + " = tk_alloc(sizeof *" + block + ");")
	for i, v := range values {
		e.line(block + "->a" + strconv.Itoa(i) + " = " + v + ";")
	}

	e.line("tk_spawn(" + task + "_run, " + block + ");")
}

// spawnee names the C function a spawned call of fun runs, with its
// signature and its receiver expression when it is a method call: a
// function of the program or a library, an instance of a generic one,
// or a method of a declared type taking its receiver by value, which
// the checker made sure of. Anything else is not written yet.
func (e *Emitter) spawnee(fun ast.Expr, pos token.Pos) (string, *types.Signature, ast.Expr) {
	switch fun := fun.(type) {
	case *ast.Ident:
		if fn, ok := e.info.Uses[fun].(*types.Func); ok && lower.Key(fn) == "" {
			name, sig := e.calleeName(fun, fn)
			return name, sig, nil
		}
	case *ast.SelectorExpr:
		fn, ok := e.info.Uses[fun.Sel].(*types.Func)
		if !ok || lower.Key(fn) != "" {
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
// entry, hands them to tk_select as an array of cases, and runs the
// body of the case it chose as a chain of ifs, never a C switch, so a
// break inside a case leaves the loop around the select as the
// language says. The last case is the chain's else, since tk_select
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
	e.line("tk_case " + cases + "[" + strconv.Itoa(max(n, 1)) + "];")
	i := 0
	for _, cc := range s.Body {
		if cc.Comm == nil {
			continue
		}

		if send, ok := cc.Comm.(*ast.SendStmt); ok {
			ch := e.looked(send.Chan, lower.ValueType(e.typeOf(send.Chan)))
			elem := lower.ValueType(e.typeOf(send.Chan)).Underlying().(*types.Chan).Elem()
			v := e.spill(e.valueTo(send.Value, elem), elem, send.Pos())
			e.line(cases + "[" + strconv.Itoa(i) + "] = (tk_case){" + ch + ", &" + v + ", true, false};")
			sends = append(sends, sent{i, v, elem})
			i++
			continue
		}

		recv := receiveOf(cc.Comm)
		ch := e.looked(recv.X, lower.ValueType(e.typeOf(recv.X)))
		elem := lower.ValueType(e.typeOf(recv.X)).Underlying().(*types.Chan).Elem()
		got := e.temp()
		e.line(e.cType(elem, recv.Pos()) + " " + got + " = " + e.zero(elem, recv.Pos()) + ";")
		e.line(cases + "[" + strconv.Itoa(i) + "] = (tk_case){" + ch + ", &" + got + ", false, false};")
		e.pre[recv] = "(" + e.cType(e.typeOf(recv), recv.Pos()) + "){.has = " + cases + "[" + strconv.Itoa(i) + "].ok, .val = " + got + "}"
		i++
	}

	if len(s.Body) == 0 {
		e.line("tk_select(" + cases + ", 0, false);")
		e.line("__builtin_unreachable();")
		e.line("}")
		return
	}

	chosen := e.temp()
	e.line("int " + chosen + " = tk_select(" + cases + ", " + strconv.Itoa(n) + ", " + strconv.FormatBool(hasDefault) + ");")
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
		case nil, *ast.SendStmt: // the send happened in tk_select
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
