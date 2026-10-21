// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"strconv"
	"strings"

	"github.com/teak-lang/teak/compiler/ast"
	"github.com/teak-lang/teak/compiler/emit/lower"
	"github.com/teak-lang/teak/compiler/token"
	"github.com/teak-lang/teak/compiler/types"
)

// A trait used as a type is an interface value: a pointer to a table
// and a pointer to the data it owns, on the heap. The table is an
// array of function pointers: the drop first, then one entry per
// method a value of the trait can call, then one accessor per trait
// field. One table is written per implementing type and trait, when a
// value of the type first becomes a value of the trait: each entry is
// a small adapter that calls the method the type has for that name,
// resolved as any call on the type would be, so an inherited default
// or a generic instance's method serves as well as an own method.
// Making the value is one allocation and a copy; calling a method is
// one indirect call. A ref or mut receiver is the data pointer itself;
// a value receiver takes the data and frees the box, as the trait
// value was consumed.

// box writes the conversion of v, a value of the declared type from,
// into a value of the trait type to, with the table written once per
// pair.
func (e *Emitter) box(v value, from *types.Named, to *types.Trait, pos token.Pos) value {
	name := "tk_" + lower.NamedName(from) + "_as_" + lower.Ident(to.Obj().Name())
	if !e.state[name] {
		e.state[name] = true
		e.table(name, from, to, pos)
	}

	p := e.alloc(from, pos)
	e.store(v, p)
	out := e.insert(value{"%tk_trait", "zeroinitializer"}, value{"ptr", "@" + name + "_tab"}, 0)
	return e.insert(out, value{"ptr", p}, 1)
}

// table writes the adapters and the table for the declared type from
// used as the trait to, under the given name.
func (e *Emitter) table(name string, from *types.Named, to *types.Trait, pos token.Pos) {
	entries := []string{"ptr @" + name + "_drop"}
	e.helper(name+"_drop", "void", []string{"ptr %p"}, func() {
		e.dropIfNeeded("%p", from, pos)
		e.declare("tk_free", "declare void @tk_free(ptr)")
		e.inst("call void @tk_free(ptr %p)")
		e.inst("ret void")
		e.fn.terminated = true
	})
	for _, fn := range to.ValueMethods() {
		adapter := name + "_" + lower.Ident(fn.Name())
		entries = append(entries, "ptr @"+adapter)
		e.adapter(adapter, from, fn, pos)
	}

	ir := e.irType(from, pos)
	for _, f := range to.ValueFields() {
		accessor := name + "_f_" + lower.Ident(f.Name())
		entries = append(entries, "ptr @"+accessor)
		e.helper(accessor, "ptr", []string{"ptr %p"}, func() {
			st := from.Underlying().(*types.Struct)
			e.inst("ret ptr " + e.gep(ir, "%p", fieldIndex(st, f.Name())))
			e.fn.terminated = true
		})
	}

	put(&e.consts, "@", name, "_tab = internal constant [", strconv.Itoa(len(entries)), " x ptr] [", strings.Join(entries, ", "), "]\n")
}

// adapter writes the table entry for one method: it takes the data
// pointer and the method's parameters, and calls the method the type
// from has under that name, with the receiver as the method wants it.
func (e *Emitter) adapter(name string, from *types.Named, fn *types.Func, pos token.Pos) {
	sig := fn.Type().(*types.Signature)
	target, tsig := e.methodNameOf(nil, from, fn.Name(), pos)
	if tsig == nil {
		return
	}

	params := []string{"ptr %p"}
	args := []value{{"ptr", "%p"}}
	wants := []types.Type{tsig.Recv().Type()}
	for i, p := range sig.Params() {
		a := value{e.irType(p.Type(), pos), "%a" + strconv.Itoa(i)} // positional: a required method may leave its parameters unnamed
		params = append(params, a.typed())
		args = append(args, a)
		wants = append(wants, p.Type())
	}

	e.helper(name, e.resultType(sig.Results(), pos), params, func() {
		free := false
		if _, borrow := tsig.Recv().Type().(*types.Ref); !borrow { // the receiver is Self, ref Self or mut Self, as the checker insists
			args[0] = e.load(e.irType(from, pos), "%p") // a value receiver takes the data; the trait value was consumed
			free = true
		}

		r := e.callValues(target, tsig, wants, args, pos)
		if free {
			e.declare("tk_free", "declare void @tk_free(ptr)")
			e.inst("call void @tk_free(ptr %p)")
		}

		if sig.Results() == types.Unit {
			e.inst("ret void")
		} else {
			e.inst("ret " + r.typed())
		}

		e.fn.terminated = true
	})
}

// traitCall writes the call x of a method on the trait value sel.X,
// wanted as recvType: one indirect call through the table, with the
// data pointer first. A value built here and only lent is dropped when
// the statement ends, as any such operand is.
func (e *Emitter) traitCall(x *ast.CallExpr, sel *ast.SelectorExpr, recvType types.Type, sig *types.Signature) value {
	tr := lower.ValueType(recvType).(*types.Trait)
	recv := e.operands([]ast.Expr{sel.X}, []types.Type{recvType})[0]
	v := e.derefTo(recv, recvType, tr)
	tab, data := e.extract(v, 0, "ptr"), e.extract(v, 1, "ptr")
	fn := e.load("ptr", e.elem("ptr", tab.s, strconv.Itoa(e.entry(tr, sel.Sel.Name))))
	parts := []string{data.typed()}
	_, values := e.callOperands(nil, x.Args, sig)
	for _, a := range values {
		parts = append(parts, a.typed())
	}

	call := "call " + e.resultType(sig.Results(), x.Pos()) + " " + fn.s + "(" + strings.Join(parts, ", ") + ")"
	if sig.Results() == types.Unit {
		e.inst(call)
		return unit
	}

	return e.op(call, e.irType(sig.Results(), x.Pos()))
}

// entry is the index in a trait's table of the method under name,
// after the drop.
func (e *Emitter) entry(tr *types.Trait, name string) int {
	for i, fn := range tr.ValueMethods() {
		if fn.Name() == name {
			return 1 + i
		}
	}

	return -1
}

// traitField writes the address of the field f of the trait value at
// the address p: a place in the box reached through the table's
// accessor, which can be read, written and emptied as any field.
func (e *Emitter) traitField(p string, tr *types.Trait, f *types.Var) string {
	tab := e.load("ptr", e.gep("%tk_trait", p, 0))
	data := e.load("ptr", e.gep("%tk_trait", p, 1))
	index := 1 + len(tr.ValueMethods())
	for i, field := range tr.ValueFields() {
		if field.Name() == f.Name() {
			index += i
			break
		}
	}

	accessor := e.load("ptr", e.elem("ptr", tab.s, strconv.Itoa(index)))
	return e.op("call ptr "+accessor.s+"(ptr "+data.s+")", "ptr").s
}
