// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"bytes"
	"sort"
	"strconv"
	"strings"

	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/token"
	"github.com/teak-lang/teak/types"
)

// A function value is a pair in C: a function taking an environment
// pointer before its parameters, and the environment. A closure is
// lifted to a function of its own, and its environment holds a pointer
// to each variable it captures, which is what capturing by borrow
// means: the closure reads and writes the variable itself. The
// environment lives in the frame of the function that wrote the
// closure, declared at its top: a closure with captures is a borrow,
// which the checker keeps from outliving that frame, so nothing is
// allocated and nothing is freed. A named function used as a value
// gets an adapter that ignores the environment.

// funcLit writes a closure: the lifted function and its environment
// type into their sections, the environment's allocation here, and
// returns the pair.
func (e *Emitter) funcLit(x *ast.FuncLit) string {
	sig := e.info.Types[x].(*types.Signature)
	pair := e.cType(sig, x.Pos())
	e.closures++
	name := "tk_closure_" + strconv.Itoa(e.closures)
	env := "tk_env_" + strconv.Itoa(e.closures)
	captured := e.captures(x)
	var fields strings.Builder
	for _, v := range captured {
		put(&fields, "\t", e.cType(v.Type(), v.Pos()), " *", ident(v.Name()), ";\n")
	}

	put(&e.types, "struct ", env, " {\n", fields.String(), "\tchar tk_pad;\n};\n\n")
	head := "static " + e.resultType(sig, x.Pos()) + " " + name + "(void *tk_envp" + e.closureParamList(sig, x.Pos()) + ")"
	put(&e.protos, head, ";\n")

	// The body goes into a buffer of its own, with the captured
	// variables reached through the environment.
	outer := e.body
	e.body = bytes.Buffer{}
	outerCaptured := e.captured
	e.captured = make(map[*types.Var]string, len(captured))
	for _, v := range captured {
		e.captured[v] = "(*tk_env->" + ident(v.Name()) + ")"
	}

	e.line(head + " {")
	e.line("struct " + env + " *tk_env = tk_envp;")
	e.line("(void)tk_env;")
	e.function(sig, x.Body)
	e.line("")
	e.helpers.Write(e.body.Bytes())
	e.body = outer
	cell := e.temp()
	e.prologue = append(e.prologue, "struct "+env+" "+cell+";")
	for _, v := range captured {
		e.line(cell + "." + ident(v.Name()) + " = &" + e.variable(v, outerCaptured) + ";")
	}

	e.captured = outerCaptured
	return "(" + pair + "){" + name + ", &" + cell + "}"
}

// captures lists the local variables a closure refers to that are
// declared outside it, in order of declaration: every use inside the
// closure's text of a variable declared before it. A variable an inner
// closure captures is captured by the outer one too.
func (e *Emitter) captures(x *ast.FuncLit) []*types.Var {
	from, to := x.Pos(), x.Body.Rbrace
	seen := make(map[*types.Var]bool)
	var vars []*types.Var
	for id, obj := range e.info.Uses {
		v, ok := obj.(*types.Var)
		if !ok || v.IsField() || v.Pos() == token.NoPos || id.Pos() < from || id.Pos() > to || v.Pos() >= from || seen[v] {
			continue
		}

		seen[v] = true
		vars = append(vars, v)
	}

	sort.Slice(vars, func(i, j int) bool { return vars[i].Pos() < vars[j].Pos() })
	return vars
}

// variable writes how a variable is reached in the enclosing
// function: by name, or through the environment when that function is
// itself a closure that captured it.
func (e *Emitter) variable(v *types.Var, captured map[*types.Var]string) string {
	if s, ok := captured[v]; ok {
		return s
	}

	return ident(v.Name())
}

// closureParams writes the parameter types of a function value's
// function, the environment first.
func (e *Emitter) closureParams(sig *types.Signature, pos token.Pos) string {
	return "void *" + e.closureParamList(sig, pos)
}

func (e *Emitter) closureParamList(sig *types.Signature, pos token.Pos) string {
	var b strings.Builder
	for _, p := range sig.Params() {
		put(&b, ", ", e.cType(p.Type(), pos))
		if p.Name() != "" {
			put(&b, " ", ident(p.Name()))
		}
	}

	return b.String()
}

// funcValue writes a named function as a function value: an adapter
// that drops the environment, written once, paired with no
// environment.
func (e *Emitter) funcValue(fn *types.Func, pos token.Pos) string {
	sig := fn.Type().(*types.Signature)
	pair := e.cType(sig, pos)
	name := "tk_fnval_" + ident(fn.Name())
	if e.state[name] == undeclared {
		e.state[name] = defined
		args := make([]string, len(sig.Params()))
		for i, p := range sig.Params() {
			args[i] = ident(p.Name())
		}

		call := funcName(fn) + "(" + strings.Join(args, ", ") + ")"
		if sig.Results() != types.Unit {
			call = "return " + call
		}

		head := "static " + e.resultType(sig, pos) + " " + name + "(void *tk_env" + e.closureParamList(sig, pos) + ")"
		put(&e.protos, head, ";\n")
		put(&e.helpers, head, " {\n\t(void)tk_env;\n\t", call, ";\n}\n\n")
	}

	return "(" + pair + "){" + name + ", NULL}"
}

// callValue writes a call of a function value: its function with its
// environment first.
func (e *Emitter) callValue(x *ast.CallExpr, sig *types.Signature) string {
	var f string
	if e.hasEffects(x.Fun) {
		f = e.saved(x.Fun, sig)
	} else {
		f = e.valueTo(x.Fun, sig)
	}

	args := e.args(nil, x.Args, sig)
	if args != "" {
		args = ", " + args
	}

	return f + ".fn(" + f + ".env" + args + ")"
}
