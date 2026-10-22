// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"bytes"
	"sort"
	"strconv"
	"strings"

	"github.com/teak-lang/teak/compiler/ast"
	"github.com/teak-lang/teak/compiler/emit/lower"
	"github.com/teak-lang/teak/compiler/token"
	"github.com/teak-lang/teak/compiler/types"
)

// A function value is a pair in C: a function taking an environment
// pointer before its parameters, and a box of a few words. A closure
// is lifted to a function of its own with an environment struct; the
// function receives the address of the pair's box. A closure that
// borrows what it captures keeps pointers to the variables in an
// environment that lives in the frame of the function that wrote it,
// and the box holds the environment's address: such a closure is a
// borrow, which the checker keeps from outliving the frame, so nothing
// is allocated and nothing is freed. A closure that captures Copy
// values it only reads holds their copies in the environment, and the
// environment lives in the box itself, so the closure is a value that
// goes anywhere, returned included, with nothing to drop. A named
// function used as a value gets an adapter that ignores the
// environment.

// minBoxWords is the least size of the box in 64-bit words. The box
// grows to the largest closure of copies in the program, counted in
// words by the Teak types captured (words), and the C compiler checks
// the count against the real layout.
const minBoxWords = 4

// funcLit writes a closure: the lifted function and its environment
// type into their sections, the environment's construction here, and
// returns the pair.
func (e *Emitter) funcLit(x *ast.FuncLit) string {
	sig := e.typeOf(x).(*types.Signature)
	pair := e.cType(sig, x.Pos())
	e.closures++
	name := "tk_closure_" + strconv.Itoa(e.closures)
	env := "tk_env_" + strconv.Itoa(e.closures)
	captured := e.captures(x)
	inline := true // every capture a copy: the environment is the box
	var fields strings.Builder
	for _, cap := range captured {
		star := " *"
		if cap.Copied {
			star = " "
		} else {
			inline = false
		}

		put(&fields, "\t", e.cType(e.sub(cap.Var.Type()), cap.Var.Pos()), star, lower.Ident(cap.Var.Name()), ";\n")
	}

	put(&e.types, "struct ", env, " {\n", fields.String(), "\tchar tk_pad;\n};\n")
	if inline {
		words := 1 // the pad
		for _, cap := range captured {
			words += e.words(e.sub(cap.Var.Type()))
		}

		e.boxWords = max(e.boxWords, words)
		put(&e.types, "_Static_assert(sizeof(struct ", env, ") <= TK_BOX_WORDS * sizeof(uint64_t), \"a closure copies more than its box holds\");\n")
	}

	put(&e.types, "\n")
	head := "static " + e.resultType(sig, x.Pos()) + " " + name + "(void *tk_envp" + e.closureParamList(sig, x.Pos()) + ")"
	put(&e.protos, head, ";\n")

	// The body goes into a buffer of its own, with the captured
	// variables reached through the environment.
	outer := e.body
	e.body = bytes.Buffer{}
	outerCaptured := e.captured
	e.captured = make(map[*types.Var]string, len(captured))
	for _, cap := range captured {
		if cap.Copied {
			e.captured[cap.Var] = "(tk_env->" + lower.Ident(cap.Var.Name()) + ")"
		} else {
			e.captured[cap.Var] = "(*tk_env->" + lower.Ident(cap.Var.Name()) + ")"
		}
	}

	e.line(head + " {")
	if inline {
		e.line("struct " + env + " *tk_env = tk_envp;")
	} else {
		e.line("struct " + env + " *tk_env = *(struct " + env + " **)tk_envp;")
	}

	e.line("(void)tk_env;")
	e.function(sig, x.Body)
	e.line("")
	e.helpers.Write(e.body.Bytes())
	e.body = outer
	cell := e.temp() // the pair itself, or the environment in the frame
	if inline {
		e.line(pair + " " + cell + " = {" + name + ", {0}};")
		for _, cap := range captured {
			e.line("((struct " + env + " *)" + cell + ".box)->" + lower.Ident(cap.Var.Name()) + " = " + e.variable(cap.Var, outerCaptured) + ";")
		}

		e.captured = outerCaptured
		return cell
	}

	e.prologue = append(e.prologue, "struct "+env+" "+cell+";")
	for _, cap := range captured {
		value := e.variable(cap.Var, outerCaptured)
		if !cap.Copied {
			value = "&" + value
		}

		e.line(cell + "." + lower.Ident(cap.Var.Name()) + " = " + value + ";")
	}

	e.captured = outerCaptured
	return "(" + pair + "){" + name + ", {(uint64_t)(uintptr_t)&" + cell + "}}"
}

// words counts the 64-bit words a value of type t takes in C, rounding
// each part up to a word, so the count is never short: the box of a
// function value is sized by it, and the C compiler checks the real
// layout against the box.
func (e *Emitter) words(t types.Type) int {
	switch u := t.Underlying().(type) {
	case *types.Basic:
		if u.Kind() == types.String {
			return 3
		}

		return 1
	case *types.Struct:
		n := 1 // a live flag, or the pad of an empty struct
		for i := 0; i < u.NumFields(); i++ {
			n += e.words(u.Field(i).Type())
		}

		return n
	case *types.Enum:
		n := 0
		for i := 0; i < u.NumVariants(); i++ {
			m := 0
			for _, f := range u.Variant(i).Fields() {
				m += e.words(f.Type())
			}

			n = max(n, m)
		}

		return n + 1
	case *types.Option:
		return 1 + e.words(u.Elem())
	case *types.Result:
		return 1 + max(e.words(u.Val()), e.words(u.Err()))
	case *types.Tuple:
		n := 1
		for i := 0; i < u.Len(); i++ {
			n += e.words(u.At(i))
		}

		return n
	case *types.Array:
		return int(u.Len()) * e.words(u.Elem())
	case *types.Signature:
		return 1 + max(e.boxWords, minBoxWords)
	}

	return 3 // a slice, map or pointer: never Copy, so never copied in, counted anyway
}

// captures lists the variables a closure refers to that are declared
// outside it, in order of declaration, as the checker recorded them,
// each saying whether the closure holds a copy. A variable an inner
// closure captures is captured by the outer one too.
func (e *Emitter) captures(x *ast.FuncLit) []types.Capture {
	caps := append([]types.Capture(nil), e.info.Captures[x]...) // the checker's list, in the order of their declarations
	sort.Slice(caps, func(i, j int) bool { return caps[i].Var.Pos() < caps[j].Var.Pos() })
	return caps
}

// variable writes how a variable is reached in the enclosing
// function: by name, or through the environment when that function is
// itself a closure that captured it.
func (e *Emitter) variable(v *types.Var, captured map[*types.Var]string) string {
	if s, ok := captured[v]; ok {
		return s
	}

	return e.localName(v)
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
			put(&b, " ", e.localName(p))
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
	name := "tk_fnval_" + lower.FuncName(fn)
	if e.state[name] == undeclared {
		e.state[name] = defined
		args := make([]string, len(sig.Params()))
		for i, p := range sig.Params() {
			args[i] = e.localName(p)
		}

		call := lower.FuncName(fn) + "(" + strings.Join(args, ", ") + ")"
		if sig.Results() != types.Unit {
			call = "return " + call
		}

		head := "static " + e.resultType(sig, pos) + " " + name + "(void *tk_env" + e.closureParamList(sig, pos) + ")"
		put(&e.protos, head, ";\n")
		put(&e.helpers, head, " {\n\t(void)tk_env;\n\t", call, ";\n}\n\n")
	}

	return "(" + pair + "){" + name + ", {0}}"
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

	return f + ".fn(" + f + ".box" + args + ")"
}
