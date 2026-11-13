// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"slices"
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// Every value is freed where the checker said its owner dies, at the
// points the lowering note lists. The emitter keeps a frame per scope
// it writes, holding the slots of the owned variables declared there
// that have something to release, and writes their drops, last
// declared first, where the scope ends: at the closing brace, before a
// return, break or continue, and at the end of a match arm. A move
// empties the slot it moves from, so a slot is dropped exactly once
// whatever path reaches the end of its scope: the drop of an emptied
// slot does nothing. A value that no variable holds is a temporary of
// the statement that made it and is dropped when the statement ends.
// A type with a Drop method carries a flag saying it is live, set
// when a value is built and cleared when it moves, so the method runs
// once. A value whose drop runs a Drop method is registered with the
// runtime's guard stack while it is owned, so that a panic runs it.

// A frame is a scope being written: the owned slots declared in it
// that need dropping, in order; whether it is a loop body, which a
// break or continue leaves; and how many statement temporaries were
// pending when it opened.
type frame struct {
	vars []owned
	loop bool
	mark int
}

// An owned value the emitter will drop: the address of its slot, its
// Snello type, and when it was registered among the function's values
// and deferred calls, which a return releases last registered first.
type owned struct {
	slot string
	typ  types.Type
	seq  int
}

// needsDrop reports whether a value of type t releases something when
// it is dropped: a string that may own its bytes, a slice, map or
// owned pointer, a channel handle, a type with a Drop method, a trait
// value, or a type holding one of these. Borrows, numbers and function
// values do not.
func needsDrop(t types.Type) bool {
	switch t := t.(type) {
	case *types.Basic:
		return isString(t)
	case *types.Named:
		if t.Implements(types.DropTrait) {
			return true
		}
	case *types.Pointer, *types.Slice, *types.Map, *types.Trait, *types.Chan:
		return true
	}

	return anyPart(t, needsDrop)
}

// guarded reports whether dropping a value of type t runs a Drop
// method, its own or one of a part, which a panic must still run. The
// answer for a declared type is kept, as a type may hold a pointer to
// itself.
func (e *Emitter) guarded(t types.Type) bool {
	if _, ok := t.(*types.Trait); ok {
		return true // whatever the value holds may have a Drop method
	}

	n, ok := t.(*types.Named)
	if !ok {
		return anyPart(t, e.guarded)
	}

	if g, done := e.guards[n]; done {
		return g
	}

	e.guards[n] = false // reached through itself, a type adds nothing
	e.guards[n] = hasDrop(n) || anyPart(n, e.guarded)
	return e.guards[n]
}

// anyPart reports whether f holds for a part of a value of type t.
func anyPart(t types.Type, f func(types.Type) bool) bool {
	switch t := t.(type) {
	case *types.Named:
		return f(t.Underlying())
	case *types.Pointer:
		return f(t.Elem())
	case *types.Slice:
		return f(t.Elem())
	case *types.Array:
		return f(t.Elem())
	case *types.Map:
		return f(t.Key()) || f(t.Value())
	case *types.Option:
		return f(t.Elem())
	case *types.Result:
		return f(t.Val()) || f(t.Err())
	case *types.Tuple:
		for i := 0; i < t.Len(); i++ {
			if f(t.At(i)) {
				return true
			}
		}
	case *types.Struct:
		for i := 0; i < t.NumFields(); i++ {
			if f(t.Field(i).Type()) {
				return true
			}
		}
	case *types.Enum:
		for i := 0; i < t.NumVariants(); i++ {
			for _, v := range t.Variant(i).Fields() {
				if f(v.Type()) {
					return true
				}
			}
		}
	}

	return false
}

// hasDrop reports whether t is a declared type with a Drop method,
// which carries the live flag.
func hasDrop(t types.Type) bool {
	n, ok := t.(*types.Named)
	return ok && n.Implements(types.DropTrait)
}

// liveIndex is the index of the live flag in a struct or enum with a
// Drop method: after the fields, or after the tag and payload.
func liveIndex(t *types.Named) int {
	if st, ok := t.Underlying().(*types.Struct); ok {
		return st.NumFields()
	}

	return 2
}

// dropOf writes the call that drops the value of type t at the
// address p.
func (e *Emitter) dropOf(p string, t types.Type, pos token.Pos) {
	e.inst("call void @" + e.dropper(t, pos) + "(ptr " + p + ")")
}

// dropIfNeeded is dropOf for a type that owns something, nothing
// otherwise.
func (e *Emitter) dropIfNeeded(p string, t types.Type, pos token.Pos) {
	if needsDrop(t) {
		e.dropOf(p, t, pos)
	}
}

// elemDropper returns the name of the function the runtime is given to
// drop an element of a channel, or a key or a value of a map, of type
// elem with, writing it on first use: dropper's under the name of
// lower.ElemDrop, which the analysis of the linked program takes for
// the targets of the runtime's calls of such drops.
func (e *Emitter) elemDropper(elem types.Type, pos token.Pos) string {
	name := lower.ElemDrop(elem)
	if e.state[name] {
		return name
	}

	e.state[name] = true
	drop := e.dropper(elem, pos)
	e.helper(name, "void", []string{"ptr %v"}, func() {
		e.inst("call void @" + drop + "(ptr %v)")
		e.inst("ret void")
		e.fn.terminated = true
	})

	return name
}

// dropper returns the name of the function dropping a value of type t
// at an address, writing it on first use: a Drop method first, when
// the value is live, then each part that owns something, and the
// memory a string, pointer, slice or map holds; in a debug build the
// place is poisoned after, as the C runtime's drops poison it.
func (e *Emitter) dropper(t types.Type, pos token.Pos) string {
	name := "sn_drop_" + lower.Mangle(t)
	if e.state[name] {
		return name
	}

	e.state[name] = true
	e.helper(name, "void", []string{"ptr %v"}, func() {
		ir := e.irType(t, pos)
		if hasDrop(t) {
			live := e.gep(ir, "%v", liveIndex(t.(*types.Named)))
			flag := e.load("i1", live)
			run, done := e.block("live"), e.block("dead")
			e.branch(flag.s, run, done)
			e.label(done)
			e.inst("ret void")
			e.fn.terminated = true
			e.label(run)
			e.store(value{"i1", "false"}, live)
			e.inst("call void @" + e.dropMethod(t) + "(ptr %v)")
		}

		switch u := t.Underlying().(type) {
		case *types.Basic:
			cap := e.load("i64", e.gep(ir, "%v", 2))
			owned := e.op("icmp sgt i64 "+cap.s+", 0", "i1")
			free, done := e.block("free"), e.block("done")
			e.branch(owned.s, free, done)
			e.label(free)
			e.check(ir, "%v", t)
			e.declare("sn_free", "declare void @sn_free(ptr)")
			e.inst("call void @sn_free(ptr " + e.load("ptr", e.gep(ir, "%v", 0)).s + ")")
			e.jump(done)
			e.label(done)
		case *types.Struct:
			for i := 0; i < u.NumFields(); i++ {
				if f := u.Field(i); needsDrop(f.Type()) {
					e.dropOf(e.gep(ir, "%v", i), f.Type(), pos)
				}
			}
		case *types.Enum:
			tag := e.load("i32", e.gep(ir, "%v", 0))
			payload := e.gep(ir, "%v", 1)
			done := e.block("done")
			var cases []string
			blocks := make([]string, u.NumVariants())
			for i := 0; i < u.NumVariants(); i++ {
				if !anyOwned(u.Variant(i).Fields()) {
					continue
				}

				blocks[i] = e.block("variant")
				cases = append(cases, "i32 "+strconv.Itoa(i)+", label %"+blocks[i])
			}

			e.inst("switch i32 " + tag.s + ", label %" + done + " [ " + strings.Join(cases, " ") + " ]")
			e.fn.terminated = true
			for i := 0; i < u.NumVariants(); i++ {
				if blocks[i] == "" {
					continue
				}

				e.label(blocks[i])
				vt := e.variantType(u.Variant(i).Fields(), pos)
				for j, f := range u.Variant(i).Fields() {
					if needsDrop(f.Type()) {
						e.dropOf(e.gep(vt, payload, j), f.Type(), pos)
					}
				}

				e.jump(done)
			}

			e.label(done)
		case *types.Option:
			has, val := e.optionParts(ir, "%v", u)
			some, done := e.block("some"), e.block("done")
			e.branch(has.s, some, done)
			e.label(some)
			e.dropOf(val, u.Elem(), pos)
			e.jump(done)
			e.label(done)
		case *types.Result:
			ok := e.load("i1", e.gep(ir, "%v", 0))
			payload := e.gep(ir, "%v", 1)
			yes, no, done := e.block("ok"), e.block("err"), e.block("done")
			e.branch(ok.s, yes, no)
			e.label(yes)
			e.dropIfNeeded(payload, u.Val(), pos)
			e.jump(done)
			e.label(no)
			e.dropIfNeeded(payload, u.Err(), pos)
			e.jump(done)
			e.label(done)
		case *types.Tuple:
			for i := 0; i < u.Len(); i++ {
				if needsDrop(u.At(i)) {
					e.dropOf(e.gep(ir, "%v", i), u.At(i), pos)
				}
			}
		case *types.Pointer:
			p := e.load("ptr", "%v")
			isNull := e.op("icmp eq ptr "+p.s+", null", "i1")
			free, done := e.block("free"), e.block("done")
			e.branch(isNull.s, done, free)
			e.label(free)
			e.dropIfNeeded(e.deref(p).s, u.Elem(), pos)
			e.declare("sn_release", "declare void @sn_release(ptr)")
			e.inst("call void @sn_release(ptr %v)")
			e.jump(done)
			e.label(done)
		case *types.Slice:
			if needsDrop(u.Elem()) {
				e.ownsBuffer("%v", "replacing") // before an element is dropped, so a refused drop leaves the slice whole
				e.eachElement("%v", u.Elem(), pos, func(p string) { e.dropOf(p, u.Elem(), pos) })
			}

			e.declare("sn_slice_free", "declare void @sn_slice_free(ptr)")
			e.inst("call void @sn_slice_free(ptr %v)")
		case *types.Array:
			for i := int64(0); i < u.Len(); i++ {
				e.dropOf(e.gep(ir, "%v", int(i)), u.Elem(), pos)
			}
		case *types.Map:
			e.declare("sn_map_free", "declare void @sn_map_free(ptr, ptr)")
			e.inst("call void @sn_map_free(ptr %v, ptr " + e.mapType(u, pos) + ")")
		case *types.Chan:
			e.declare("sn_chan_drop", "declare void @sn_chan_drop(ptr)")
			e.inst("call void @sn_chan_drop(ptr %v)")
		case *types.Trait:
			tab := e.load("ptr", e.gep(ir, "%v", 0))
			isNull := e.op("icmp eq ptr "+tab.s+", null", "i1")
			drop, done := e.block("drop"), e.block("done")
			e.branch(isNull.s, done, drop)
			e.label(drop)
			fn := e.load("ptr", tab.s) // the drop is the table's first entry
			e.inst("call void " + fn.s + "(ptr " + e.load("ptr", e.gep(ir, "%v", 1)).s + ")")
			e.store(value{"ptr", "null"}, e.gep(ir, "%v", 0))
			e.jump(done)
			e.label(done)
		default:
			e.unsupported(pos, "dropping values of type "+t.String())
		}

		if e.cfg.Debug {
			e.poison("%v", t)
		}

		e.inst("ret void")
		e.fn.terminated = true
	})
	return name
}

// anyOwned reports whether a variant's fields own something.
func anyOwned(fields []*types.Var) bool {
	for _, f := range fields {
		if needsDrop(f.Type()) {
			return true
		}
	}

	return false
}

// dropMethod returns the name of the Drop method of t, asking for its
// instance when t is an instance of a generic type or the method is a
// trait's default.
func (e *Emitter) dropMethod(t types.Type) string {
	if n, ok := t.(*types.Named); ok {
		if fn := n.Method("Drop"); fn != nil && e.generics[fn.Origin()] != nil {
			name, _ := e.callInstance(nil, fn, n)
			return name
		}
	}

	return lower.MethodName(t, "Drop")
}

// eachElement writes a loop over the elements of the slice at the
// address s, calling body with the address of each.
func (e *Emitter) eachElement(s string, elem types.Type, pos token.Pos, body func(p string)) {
	e.eachElementIndexed(s, elem, pos, func(p, _ string) { body(p) })
}

// poison fills the place at p with the runtime's poison byte, so that
// a value read after its release is seen by whatever reads it.
func (e *Emitter) poison(p string, t types.Type) {
	e.declare("llvm.memset.p0.i64", "declare void @llvm.memset.p0.i64(ptr, i8, i64, i1)")
	e.inst("call void @llvm.memset.p0.i64(ptr " + p + ", i8 -35, i64 " + strconv.FormatInt(e.size(t), 10) + ", i1 false)")
}

// check writes a debug build's check of the string at the address p
// before its bytes are released or read: a poisoned length or
// capacity is negative, and an owned string's block must be live.
func (e *Emitter) check(ir, p string, t types.Type) {
	if !e.cfg.Debug {
		return
	}

	e.declare("sn_released", "declare void @sn_released(ptr)")
	e.declare("sn_check_block", "declare void @sn_check_block(ptr, ptr)")
	what := e.constBytes("string")
	n := e.load("i64", e.gep(ir, p, 1))
	cap := e.load("i64", e.gep(ir, p, 2))
	bad1 := e.op("icmp slt i64 "+n.s+", 0", "i1")
	bad2 := e.op("icmp slt i64 "+cap.s+", 0", "i1")
	bad := e.op("or i1 "+bad1.s+", "+bad2.s, "i1")
	fail, ok := e.block("released"), e.block("live")
	e.branch(bad.s, fail, ok)
	e.label(fail)
	e.inst("call void @sn_released(ptr " + what + ")")
	e.inst("unreachable")
	e.fn.terminated = true
	e.label(ok)
	owned := e.op("icmp sgt i64 "+cap.s+", 0", "i1")
	block, done := e.block("block"), e.block("checked")
	e.branch(owned.s, block, done)
	e.label(block)
	e.inst("call void @sn_check_block(ptr " + e.load("ptr", e.gep(ir, p, 0)).s + ", ptr " + what + ")")
	e.jump(done)
	e.label(done)
}

// zero writes the zero value of type t at the address p, what a place
// holds once its value moved away.
func (e *Emitter) zero(p string, t types.Type, pos token.Pos) {
	ir := e.irType(t, pos)
	e.store(value{ir, zero(ir)}, p)
}

// push opens a frame for a scope; loop says a break or continue leaves
// it.
func (e *Emitter) push(loop bool) {
	e.fn.frames = append(e.fn.frames, frame{loop: loop, mark: len(e.fn.temps)})
}

// pop closes the innermost frame, writing the drops of its variables.
func (e *Emitter) pop(pos token.Pos) {
	n := len(e.fn.frames) - 1
	e.dropVars(e.fn.frames[n].vars, pos)
	e.fn.frames = e.fn.frames[:n]
}

// own records a variable just declared in the innermost frame, when
// its type has something to release and it owns its value.
func (e *Emitter) own(slot string, t types.Type) {
	if _, borrow := t.(*types.Ref); borrow || !needsDrop(t) || len(e.fn.frames) == 0 {
		return
	}

	f := &e.fn.frames[len(e.fn.frames)-1]
	e.seq++
	f.vars = append(f.vars, owned{slot, t, e.seq})
	e.guard(slot, t)
}

// hold records a temporary of the statement being written, dropped
// when the statement ends.
func (e *Emitter) hold(slot string, t types.Type) {
	if needsDrop(t) {
		e.seq++
		e.fn.temps = append(e.fn.temps, owned{slot, t, e.seq})
		e.guard(slot, t)
	}
}

// guard registers an owned value with the runtime when its drop runs
// a Drop method, so that a panic runs it too.
func (e *Emitter) guard(slot string, t types.Type) {
	if e.guarded(t) {
		e.declare("sn_guard", "declare void @sn_guard(ptr, ptr)")
		e.inst("call void @sn_guard(ptr " + slot + ", ptr @" + e.dropper(t, token.NoPos) + ")")
	}
}

func (e *Emitter) dropVars(vars []owned, pos token.Pos) {
	for _, v := range slices.Backward(vars) {
		e.dropOf(v.slot, v.typ, pos)
		if e.guarded(v.typ) {
			e.declare("sn_unguard", "declare void @sn_unguard(ptr)")
			e.inst("call void @sn_unguard(ptr " + v.slot + ")")
		}
	}
}

// dropTemps writes the drops of the temporaries recorded since mark
// and forgets them.
func (e *Emitter) dropTemps(mark int, pos token.Pos) {
	e.dropVars(e.fn.temps[mark:], pos)
	e.fn.temps = e.fn.temps[:mark]
}

// leave writes the drops a jump out of the frames from index from
// runs: the temporaries pending since those frames opened, then their
// variables, innermost first. The frames stay open for the code after
// the jump.
func (e *Emitter) leave(from int, pos token.Pos) {
	if from >= len(e.fn.frames) {
		return
	}

	e.dropVars(e.fn.temps[e.fn.frames[from].mark:], pos)
	for _, f := range slices.Backward(e.fn.frames[from:]) {
		e.dropVars(f.vars, pos)
	}
}

// loopFrame is the index of the innermost loop body frame, which a
// break or continue leaves.
func (e *Emitter) loopFrame() int {
	for i, f := range slices.Backward(e.fn.frames) {
		if f.loop {
			return i
		}
	}

	return len(e.fn.frames)
}

// scoped writes what f writes and drops the temporaries it made at
// once, for code inside a block the statement's own drops cannot
// reach.
func (e *Emitter) scoped(pos token.Pos, f func()) {
	mark := len(e.fn.temps)
	f()
	e.dropTemps(mark, pos)
}

// moved writes a read of the place x that moves its value away: the
// value is loaded and the place is emptied, so its drop later does
// nothing.
func (e *Emitter) moved(x ast.Expr) value {
	t := e.typeOf(x)
	p := e.lvalue(x)
	v := e.load(e.irType(t, x.Pos()), p)
	e.zero(p, t, x.Pos())
	return v
}

// isLent reports whether the value of x is someone else's, lent to the
// expression around it and never a temporary to drop: a read of a
// variable, a field or an element, or a borrow whatever gave it, as a
// call that returns ref string lends its caller's string.
func (e *Emitter) isLent(x ast.Expr) bool {
	if _, borrow := e.typeOf(x).(*types.Ref); borrow {
		return true
	}

	switch x := x.(type) {
	case *ast.Ident:
		_, ok := e.info.Uses[x].(*types.Var)
		return ok
	case *ast.ParenExpr:
		return e.isLent(x.X)
	case *ast.SelectorExpr:
		_, ok := e.info.Uses[x.Sel].(*types.Var)
		return ok
	case *ast.IndexExpr:
		return true
	case *ast.UnaryExpr:
		return x.Op == token.MUL || x.Op == token.REF || x.Op == token.MUT
	}

	return false
}

// discard drops the value v of type t that an expression statement
// computed and no one holds.
func (e *Emitter) discard(v value, t types.Type, pos token.Pos) {
	if needsDrop(t) {
		e.dropOf(e.spill(v), t, pos)
	}
}

// cloner returns the name of the function copying a value of type t
// part by part, writing it on first use, for a slice or map whose
// elements own something: a bitwise copy would be dropped twice. It
// takes the value's address and returns the copy.
func (e *Emitter) cloner(t types.Type, pos token.Pos) string {
	name := "sn_clone_" + lower.Mangle(t)
	if e.state[name] {
		return name
	}

	e.state[name] = true
	ir := e.irType(t, pos)
	e.helper(name, ir, []string{"ptr %v"}, func() {
		out := e.slot(ir)
		e.store(value{ir, zero(ir)}, out)
		if hasDrop(t) {
			e.store(value{"i1", "true"}, e.gep(ir, out, liveIndex(t.(*types.Named))))
		}

		switch u := t.Underlying().(type) {
		case *types.Basic:
			e.store(e.rt("sn_string_clone", "%sn_string", e.load(ir, "%v")), out)
		case *types.Struct:
			for i := 0; i < u.NumFields(); i++ {
				ft := u.Field(i).Type()
				e.store(e.cloneAt(e.gep(ir, "%v", i), ft, pos), e.gep(ir, out, i))
			}
		case *types.Enum:
			tag := e.load("i32", e.gep(ir, "%v", 0))
			e.store(tag, e.gep(ir, out, 0))
			e.payloads(u, tag, pos, func(i int, vt string) {
				src, dst := e.gep(ir, "%v", 1), e.gep(ir, out, 1)
				for j, f := range u.Variant(i).Fields() {
					e.store(e.cloneAt(e.gep(vt, src, j), f.Type(), pos), e.gep(vt, dst, j))
				}
			})
		case *types.Option:
			has, val := e.optionParts(ir, "%v", u)
			some, done := e.block("some"), e.block("done")
			e.branch(has.s, some, done)
			e.label(some)
			e.storeOption(out, u, e.cloneAt(val, u.Elem(), pos), pos)
			e.jump(done)
			e.label(done)
		case *types.Result: // the flag, then the half there is, in the one place the two share
			ok := e.load("i1", e.gep(ir, "%v", 0))
			e.store(ok, e.gep(ir, out, 0))
			okb, errb, done := e.block("ok"), e.block("err"), e.block("done")
			e.branch(ok.s, okb, errb)
			e.label(okb)
			e.store(e.cloneAt(e.gep(ir, "%v", 1), u.Val(), pos), e.gep(ir, out, 1))
			e.jump(done)
			e.label(errb)
			e.store(e.cloneAt(e.gep(ir, "%v", 1), u.Err(), pos), e.gep(ir, out, 1))
			e.jump(done)
			e.label(done)
		case *types.Tuple:
			for i := 0; i < u.Len(); i++ {
				e.store(e.cloneAt(e.gep(ir, "%v", i), u.At(i), pos), e.gep(ir, out, i))
			}
		case *types.Pointer:
			p := e.load("ptr", "%v")
			isNull := e.op("icmp eq ptr "+p.s+", null", "i1")
			copy, done := e.block("copy"), e.block("done")
			e.branch(isNull.s, done, copy)
			e.label(copy)
			cell := e.alloc(u.Elem(), pos)
			e.store(e.cloneAt(e.deref(p).s, u.Elem(), pos), cell)
			e.store(value{"ptr", cell}, out)
			e.jump(done)
			e.label(done)
		case *types.Slice:
			n := e.load("i64", e.gep(ir, "%v", 1))
			e.sliceMake(out, n, n, u.Elem(), pos)
			dst := e.load("ptr", e.gep(ir, out, 0))
			et := e.irType(u.Elem(), pos)
			e.eachElementIndexed("%v", u.Elem(), pos, func(p, i string) {
				e.store(e.cloneAt(p, u.Elem(), pos), e.elem(et, dst.s, i))
			})
		case *types.Array:
			for i := int64(0); i < u.Len(); i++ {
				e.store(e.cloneAt(e.gep(ir, "%v", int(i)), u.Elem(), pos), e.gep(ir, out, int(i)))
			}
		case *types.Map:
			e.eachEntry("%v", u, pos, func(k, v string) {
				e.mapSet(out, u, e.cloneAt(k, u.Key(), pos), e.cloneAt(v, u.Value(), pos), pos)
			})
		default:
			e.unsupported(pos, "cloning values of type "+t.String())
		}

		e.inst("ret " + e.load(ir, out).typed())
		e.fn.terminated = true
	})
	return name
}

// cloneAt writes the copy of the value of type t at the address p:
// itself when it owns nothing, the Clone a declared type wrote, or a
// copy part by part, which is what a derived Clone is.
func (e *Emitter) cloneAt(p string, t types.Type, pos token.Pos) value {
	ir := e.irType(t, pos)
	if n, ok := t.(*types.Named); ok && written(n, "Clone") {
		name, _ := e.methodNameOf(nil, n, "Clone", pos)
		return e.op("call "+ir+" @"+name+"(ptr "+p+")", ir)
	}

	switch {
	case !needsDrop(t) && !lower.WritesClone(t): // its bytes, unless a type inside writes a Clone, which runs
		return e.load(ir, p)
	case isString(t):
		return e.rt("sn_string_clone", "%sn_string", e.load(ir, p))
	case isChan(t):
		return e.rt("sn_chan_clone", "ptr", e.load(ir, p)) // another handle on the same channel
	}

	return e.op("call "+ir+" @"+e.cloner(t, pos)+"(ptr "+p+")", ir)
}

// written reports whether the declared type has a method of its own
// under name, declared rather than derived from its fields.
func written(n *types.Named, name string) bool {
	fn := n.Method(name)
	return fn != nil && !fn.Derived()
}

// helper writes a generated function of the module under name, with
// the given result type and parameters, its body written by f into a
// function of its own.
func (e *Emitter) helper(name, result string, params []string, f func()) {
	outer := e.fn
	e.fn = &function{vars: make(map[*types.Var]string), pre: make(map[ast.Expr]string)}
	f()
	var b strings.Builder
	b.WriteString("define internal " + result + " @" + name + "(" + strings.Join(params, ", ") + ")" + funcAttrs + " {\nentry:\n")
	b.Write(e.fn.allocas.Bytes())
	b.Write(e.fn.body.Bytes())
	b.WriteString("}\n\n")
	e.helpers.WriteString(b.String())
	e.fn = outer
}

func isChan(t types.Type) bool {
	_, ok := t.Underlying().(*types.Chan)
	return ok
}
