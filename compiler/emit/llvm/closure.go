// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"sort"
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// A function value is a pair: a function taking an environment pointer
// before its parameters, and a box of a few words. A closure is lifted
// to a function of its own with an environment struct; the function
// receives the address of the pair's box. A closure that borrows what
// it captures keeps pointers to the variables in an environment that
// lives in the frame of the function that wrote it, and the box holds
// the environment's address: such a closure is a borrow, which the
// checker keeps from outliving the frame, so nothing is allocated and
// nothing is freed. A closure that captures Copy values it only reads
// holds their copies in the environment, and the environment lives in
// the box itself, so the closure is a value that goes anywhere,
// returned included, with nothing to drop. A named function used as a
// value gets an adapter that ignores the environment.

// minBoxWords is the least size of the box in words. The box grows to
// the largest closure of copies in the program, which sizeBoxes finds
// before any type is written, since the IR type of every function
// value names the count; a closure in a generic instance that needs
// more makes Emit write the module again with room for it.
const minBoxWords = 4

// sizeBoxes sets the box size from the closures the checker recorded:
// the words of the environment of each closure holding only copies.
// A closure capturing a function value depends on the size being
// found, so the pass repeats until it settles.
func (e *Emitter) sizeBoxes() {
	for grown := true; grown; {
		grown = false
		for _, caps := range e.info.Captures {
			var fields []types.Type
			inline := true
			for _, c := range caps {
				inline = inline && c.Copied
				fields = append(fields, c.Var.Type())
			}

			if n := int((e.structSize(fields) + 7) / 8); inline && n > e.boxWords {
				e.boxWords, grown = n, true
			}
		}
	}
}

// funcLit writes a closure: the lifted function into the module, the
// environment's construction here, and returns the pair.
func (e *Emitter) funcLit(x *ast.FuncLit) value {
	sig := e.typeOf(x).(*types.Signature)
	params := e.info.Types[x].(*types.Signature).Params() // the declaration's own objects, which the body names
	pair := e.irType(sig, x.Pos())
	e.closures++
	name := "sn_closure_" + strconv.Itoa(e.closures)
	captured := e.captures(x)
	inline := true // every capture a copy: the environment is the box
	parts := make([]string, len(captured))
	for i, cap := range captured {
		if cap.Copied {
			parts[i] = e.irType(e.sub(cap.Var.Type()), cap.Var.Pos())
		} else {
			parts[i] = "ptr"
			inline = false
		}
	}

	env := "{ " + strings.Join(parts, ", ") + " }"
	if inline {
		var fields []types.Type
		for _, cap := range captured {
			fields = append(fields, e.sub(cap.Var.Type()))
		}

		e.boxNeed = max(e.boxNeed, int((e.structSize(fields)+7)/8)) // the copies as this instance has them, which the next pass makes room for
	}

	e.writeBody(name, sig, params, []string{"ptr %env"}, func() map[*types.Var]string {
		base := "%env"
		if !inline {
			base = e.load("ptr", "%env").s // the box's first word holds the environment's address
		}

		reach := make(map[*types.Var]string, len(captured))
		for i, cap := range captured {
			at := e.gep(env, base, i)
			if !cap.Copied {
				at = e.load("ptr", at).s // the variable's own place
			}

			reach[cap.Var] = at
		}

		return reach
	}, x.Body, x.Pos())

	out := e.insert(value{pair, "zeroinitializer"}, value{"ptr", "@" + name}, 0)
	if inline {
		slot := e.spill(out)
		box := e.gep(pair, slot, 1)
		for i, cap := range captured {
			t := e.sub(cap.Var.Type())
			e.store(e.load(e.irType(t, cap.Var.Pos()), e.varSlot(cap.Var, x.Pos())), e.gep(env, box, i))
		}

		return e.load(pair, slot)
	}

	frame := e.slot(env) // the environment, in this function's frame
	for i, cap := range captured {
		at := e.varSlot(cap.Var, x.Pos())
		if cap.Copied {
			t := e.sub(cap.Var.Type())
			e.store(e.load(e.irType(t, cap.Var.Pos()), at), e.gep(env, frame, i))
		} else {
			e.store(value{"ptr", at}, e.gep(env, frame, i))
		}
	}

	addr := e.op("ptrtoint ptr "+frame+" to i64", "i64")
	return e.op("insertvalue "+out.typed()+", "+addr.typed()+", 1, 0", pair)
}

// captures lists the variables a closure refers to that are declared
// outside it, in order of declaration, as the checker recorded them,
// each saying whether the closure holds a copy. A variable an inner
// closure captures is captured by the outer one too.
func (e *Emitter) captures(x *ast.FuncLit) []types.Capture {
	caps := append([]types.Capture(nil), e.info.Captures[x]...)
	sort.Slice(caps, func(i, j int) bool { return caps[i].Var.Pos() < caps[j].Var.Pos() })
	return caps
}

// funcValue writes a named function as a function value: an adapter
// that drops the environment, written once, paired with no
// environment.
func (e *Emitter) funcValue(fn *types.Func, pos token.Pos) value {
	sig := fn.Type().(*types.Signature)
	pair := e.irType(sig, pos)
	if e.generics[fn] != nil {
		e.unsupported(pos, "a generic function as a value")
		return value{pair, "zeroinitializer"}
	}

	name := "sn_fnval_" + lower.FuncName(fn)
	if !e.state[name] {
		e.state[name] = true
		params := []string{"ptr %env"}
		args := make([]value, len(sig.Params()))
		for i, p := range sig.Params() {
			args[i] = value{e.irType(p.Type(), pos), "%a" + strconv.Itoa(i)}
			params = append(params, args[i].typed())
		}

		e.helper(name, e.resultType(sig.Results(), pos), params, func() {
			wants := make([]types.Type, len(sig.Params()))
			for i, p := range sig.Params() {
				wants[i] = p.Type()
			}

			r := e.callValues(lower.FuncName(fn), sig, wants, args, pos)
			if sig.Results() == types.Unit {
				e.inst("ret void")
			} else {
				e.inst("ret " + r.typed())
			}

			e.fn.terminated = true
		})
	}

	return e.insert(value{pair, "zeroinitializer"}, value{"ptr", "@" + name}, 0)
}

// callValue writes a call of a function value: its function with the
// address of its box first.
func (e *Emitter) callValue(x *ast.CallExpr, sig *types.Signature) value {
	pair := e.irType(sig, x.Pos())
	f := e.spill(e.valueTo(x.Fun, sig))
	fn := e.load("ptr", e.gep(pair, f, 0))
	parts := []string{"ptr " + e.gep(pair, f, 1)}
	_, values := e.callOperands(nil, x.Args, sig)
	for _, v := range values {
		parts = append(parts, v.typed())
	}

	call := "call " + e.resultType(sig.Results(), x.Pos()) + " " + fn.s + "(" + strings.Join(parts, ", ") + ")"
	if sig.Results() == types.Unit {
		e.inst(call)
		return unit
	}

	return e.op(call, e.irType(sig.Results(), x.Pos()))
}
