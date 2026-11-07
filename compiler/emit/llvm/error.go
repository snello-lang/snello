// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"strconv"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// The Error trait's derived methods, its chain, and the printing of
// a trait value. A type that lists Error and writes no Error() has
// the message derived: the value as %v prints it, which is the
// stringer of its structure. A type that writes no Source() has it
// derived as None. %+v prints the chain: the message, then each
// source's message after a colon and a space, asked of each source
// through its table, since a source is a trait value. A trait value
// of Error or of fmt.Stringer prints as its message through its table
// as well. ? passes an error into the function's error type where the
// types differ: wrapped in the variant the checker chose, or boxed as
// a trait value.

// messenger returns the function behind a derived Error(): the
// stringer of the type's structure, taking the value's address as a
// ref method does.
func (e *Emitter) messenger(t *types.Named, pos token.Pos) string {
	return e.stringer(t, pos)
}

// sourcer writes the function behind a derived Source(): None,
// whatever the value.
func (e *Emitter) sourcer(t *types.Named, pos token.Pos) string {
	name := "sn_src_" + lower.Mangle(t)
	if e.state[name] {
		return name
	}

	e.state[name] = true
	ir := e.irType(types.NewOption(types.ErrorTrait), pos)
	e.helper(name, ir, []string{"ptr %v"}, func() {
		e.inst("ret " + value{ir, zero(ir)}.typed())
		e.fn.terminated = true
	})
	return name
}

// messageMethod names the method a trait value prints through: Error
// for a trait refining Error, String for one refining fmt.Stringer.
func messageMethod(tr *types.Trait) string {
	if tr.Refines(types.ErrorTrait) {
		return "Error"
	}

	return "String"
}

// printsAsMessage reports whether a trait value of tr prints: it
// refines Error or fmt.Stringer, whose method gives the text.
func printsAsMessage(tr *types.Trait) bool {
	return tr.Refines(types.ErrorTrait) || types.IsStringer(tr)
}

// traitMessage writes the call printing the trait value at the
// address p, of the trait tr, as its message through its table.
func (e *Emitter) traitMessage(p string, tr *types.Trait) value {
	fn, data := e.tableEntry(p, tr, messageMethod(tr))
	return e.op("call %sn_string "+fn.s+"(ptr "+data.s+")", "%sn_string")
}

// traitSource writes the call asking the trait value at the address p
// for its source, an option of IR type ir.
func (e *Emitter) traitSource(p string, tr *types.Trait, ir string) value {
	fn, data := e.tableEntry(p, tr, "Source")
	return e.op("call "+ir+" "+fn.s+"(ptr "+data.s+")", ir)
}

// tableEntry reads the trait value at the address p: the function its
// table holds under name, and its data pointer.
func (e *Emitter) tableEntry(p string, tr *types.Trait, name string) (fn, data value) {
	tab := e.load("ptr", e.gep("%sn_trait", p, 0))
	data = e.load("ptr", e.gep("%sn_trait", p, 1))
	fn = e.load("ptr", e.elem("ptr", tab.s, strconv.Itoa(e.entry(tr, name))))
	return fn, data
}

// chainer writes the function behind %+v on a value of type t, a
// declared type implementing Error or a trait value of it: the
// message, then each source's message after a colon and a space, each
// source dropped once asked for its own.
func (e *Emitter) chainer(t types.Type, pos token.Pos) string {
	name := "sn_chain_" + lower.Mangle(t)
	if e.state[name] {
		return name
	}

	e.state[name] = true
	ir := e.irType(types.NewOption(types.ErrorTrait), pos)
	e.helper(name, "%sn_string", []string{"ptr %v"}, func() {
		var msg, src value
		switch u := t.(type) {
		case *types.Named:
			m, _ := e.methodNameOf(nil, u, "Error", pos)
			s, _ := e.methodNameOf(nil, u, "Source", pos)
			msg = e.op("call %sn_string @"+m+"(ptr %v)", "%sn_string")
			src = e.op("call "+ir+" @"+s+"(ptr %v)", ir)
		case *types.Trait:
			msg = e.traitMessage("%v", u)
			src = e.traitSource("%v", u, ir)
		}

		out, cur := e.spill(msg), e.spill(src)
		cond, loop, done := e.block("chain.cond"), e.block("chain"), e.block("chain.done")
		e.jump(cond)
		e.label(cond)
		has := e.load("i1", e.gep(ir, cur, 0))
		e.branch(has.s, loop, done)
		e.label(loop)
		val := e.gep(ir, cur, 1)
		part := e.join(e.join(text{e.load("%sn_string", out), true}, e.lit(": ")), text{e.traitMessage(val, types.ErrorTrait), true})
		e.store(part.v, out)
		next := e.traitSource(val, types.ErrorTrait, ir)
		e.dropOf(val, types.ErrorTrait, pos)
		e.store(next, cur)
		e.jump(cond)
		e.label(done)
		e.ret(text{e.load("%sn_string", out), true})
	})
	return name
}

// passed writes the conversion ? makes of the error err, of type
// from, into the error type of the function being written: wrapped in
// the variant the checker chose, boxed as a trait value the type
// implements, or as it is.
func (e *Emitter) passed(x *ast.TryExpr, err value, from types.Type) value {
	want := e.fn.sig.Results().Underlying().(*types.Result).Err()
	if v := e.info.Wraps[x]; v != nil {
		return e.variantValue(want, v, []value{err}, x.Pos())
	}

	if tr, ok := want.(*types.Trait); ok {
		if n, ok := from.(*types.Named); ok {
			return e.box(err, n, tr, x.Pos())
		}
	}

	return err
}
