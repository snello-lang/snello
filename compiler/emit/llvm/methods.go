// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"strconv"

	"github.com/teak-lang/teak/compiler/ast"
	"github.com/teak-lang/teak/compiler/emit/lower"
	"github.com/teak-lang/teak/compiler/token"
	"github.com/teak-lang/teak/compiler/types"
)

// The predeclared methods of slices, maps, strings, bytes, options and
// results, and the built-ins that work on them, are lowered here onto
// the runtime: the library table of _docs/design/05-standard-library.md.
// What teak.h writes as a static inline function, an index check or a
// character class, is written here as the instructions it is, since
// the IR cannot call what the C compiler never emits.

// methodCall writes a call of a predeclared method on sel.X.
func (e *Emitter) methodCall(x *ast.CallExpr, sel *ast.SelectorExpr) value {
	recv := lower.ValueType(e.typeOf(sel.X))
	switch t := recv.Underlying().(type) {
	case *types.Slice:
		return e.sliceMethod(x, sel, t)
	case *types.Map:
		return e.mapMethod(x, sel, t)
	case *types.Chan:
		call, _ := e.standardMethod(x, sel, recv) // Clone, another handle
		return call
	case *types.Basic:
		if call, ok := e.standardMethod(x, sel, recv); ok {
			return call
		}

		if t.Kind() == types.String {
			return e.stringMethod(x, sel)
		}

		if t.Kind() == types.Uint8 {
			return e.byteMethod(x, sel)
		}
	case *types.Option:
		return e.optionMethod(x, sel, "None")
	case *types.Result:
		return e.optionMethod(x, sel, "Err")
	}

	e.unsupported(x.Pos(), "the method "+sel.Sel.Name+" on "+recv.String())
	return unit
}

// mutating names the predeclared methods that change their receiver.
var mutating = map[string]bool{
	"Push": true, "Pop": true, "Insert": true, "Remove": true, "RemoveValue": true,
	"Clear": true, "Swap": true, "Reverse": true, "Sort": true, "SortBy": true, "GetMut": true,
}

// receiver writes the place a predeclared method is called on, made
// when the method changes it and it is a new map element.
func (e *Emitter) receiver(sel *ast.SelectorExpr) (string, types.Type) {
	if mutating[sel.Sel.Name] {
		return e.changed(sel.X)
	}

	return e.scrutinee(sel.X)
}

// sizeOf writes the size of a value of type t as a word, for the
// runtime's functions over elements of any type.
func (e *Emitter) sizeOf(t types.Type) value {
	return value{"i64", strconv.FormatInt(e.size(t), 10)}
}

func (e *Emitter) sliceMethod(x *ast.CallExpr, sel *ast.SelectorExpr, t *types.Slice) value {
	p, _ := e.receiver(sel)
	elem := t.Elem()
	size := e.sizeOf(elem)
	switch sel.Sel.Name {
	case "Push":
		v := e.spill(e.valueTo(x.Args[0], elem))
		e.rt("tk_slice_push", "void", value{"ptr", p}, value{"ptr", v}, size)
		return unit
	case "Pop":
		out := e.slot(e.irType(elem, x.Pos()))
		e.zero(out, elem, x.Pos())
		has := e.rt("tk_slice_pop", "i1", value{"ptr", p}, value{"ptr", out}, size)
		return e.optionOf(e.typeOf(x), has, e.load(e.irType(elem, x.Pos()), out), x.Pos())
	case "Insert":
		args := e.operands(x.Args, []types.Type{types.Typ[types.Int], elem})
		v := e.spill(args[1])
		e.rt("tk_slice_insert", "void", value{"ptr", p}, args[0], value{"ptr", v}, size)
		return unit
	case "Remove":
		out := e.slot(e.irType(elem, x.Pos()))
		i := e.valueTo(x.Args[0], types.Typ[types.Int])
		e.rt("tk_slice_remove", "void", value{"ptr", p}, i, value{"ptr", out}, size)
		return e.load(e.irType(elem, x.Pos()), out)
	case "RemoveValue", "Contains":
		v := e.lookedAt(x.Args[0], elem)
		return e.op("call i1 @"+e.search(sel.Sel.Name, t, x.Pos())+"(ptr "+p+", ptr "+v+")", "i1")
	case "Clear":
		e.ownsBuffer(p, "Clear")
		if needsDrop(elem) {
			e.eachElement(p, elem, x.Pos(), func(q string) { e.dropOf(q, elem, x.Pos()) })
		}

		e.store(value{"i64", "0"}, e.gep("%tk_slice", p, 1))
		return unit
	case "Swap":
		args := e.operands(x.Args, []types.Type{types.Typ[types.Int], types.Typ[types.Int]})
		e.rt("tk_slice_swap", "void", value{"ptr", p}, args[0], args[1], size)
		return unit
	case "Reverse":
		e.rt("tk_slice_reverse", "void", value{"ptr", p}, size)
		return unit
	case "Clone":
		if needsDrop(elem) {
			return e.op("call %tk_slice @"+e.cloner(t, x.Pos())+"(ptr "+p+")", "%tk_slice") // element by element, so that each copy owns its own
		}

		out := e.slot("%tk_slice")
		e.rt("tk_slice_clone", "void", value{"ptr", p}, value{"ptr", out}, size)
		return e.load("%tk_slice", out)
	case "SortBy":
		less := e.spill(e.valueTo(x.Args[0], e.typeOf(x.Args[0])))
		e.rt("tk_slice_sort", "void", value{"ptr", p}, size, value{"ptr", "@" + e.lessAdapter(elem, x.Pos())}, value{"ptr", less})
		return unit
	case "Sort":
		e.rt("tk_slice_sort", "void", value{"ptr", p}, size, value{"ptr", e.ordering(elem, x.Pos())}, value{"ptr", "null"})
		return unit
	}

	e.unsupported(x.Pos(), "the method "+sel.Sel.Name+" on slices")
	return unit
}

// lessAdapter returns the name of the function that lets the runtime
// sort call a Teak less function held in the context, written once per
// element type: the runtime hands it the addresses of two elements,
// which are the ref borrows the function takes.
func (e *Emitter) lessAdapter(elem types.Type, pos token.Pos) string {
	name := "tk_lessfn_" + lower.Mangle(elem)
	if e.state[name] {
		return name
	}

	e.state[name] = true
	less := types.NewSignature(nil, []*types.Var{types.NewVar(token.NoPos, "a", types.NewRef(elem, false)), types.NewVar(token.NoPos, "b", types.NewRef(elem, false))}, types.Typ[types.Bool])
	pair := e.irType(less, pos)
	e.helper(name, "zeroext i1", []string{"ptr %ctx", "ptr %a", "ptr %b"}, func() {
		fn := e.load("ptr", e.gep(pair, "%ctx", 0))
		box := e.gep(pair, "%ctx", 1)
		a, b := value{"ptr", "%a"}, value{"ptr", "%b"}
		if viewed(elem) { // the function takes the elements as views
			a, b = e.load(e.irType(elem, pos), "%a"), e.load(e.irType(elem, pos), "%b")
		}

		r := e.op("call i1 "+fn.s+"(ptr "+box+", "+a.typed()+", "+b.typed()+")", "i1")
		e.inst("ret " + r.typed())
		e.fn.terminated = true
	})
	return name
}

// ordering returns the function that compares two elements with <,
// for Sort on numbers and strings, and on a type with a Cmp, written or
// derived, through it: a before b when a.Cmp(b) is Less, as sort.Sort
// orders. It is a pointer operand.
func (e *Emitter) ordering(elem types.Type, pos token.Pos) string {
	name := "tk_lt_" + lower.Mangle(elem)
	n, cmp := lower.ValueType(elem).(*types.Named)
	cmp = cmp && n.Method("Cmp") != nil
	b, ok := lower.ValueType(elem).Underlying().(*types.Basic)
	if !cmp && (!ok || b.Kind() == types.Bool) {
		e.unsupported(pos, "Sort on a slice of "+elem.String()+"; use SortBy")
		return "null"
	}

	if e.state[name] {
		return "@" + name
	}

	e.state[name] = true
	e.helper(name, "zeroext i1", []string{"ptr %ctx", "ptr %a", "ptr %b"}, func() {
		if cmp {
			tag := e.extract(e.compare("%a", "%b", elem, pos), 0, "i32")
			e.inst("ret " + e.op("icmp eq "+tag.typed()+", 0", "i1").typed()) // Less is the first variant
			e.fn.terminated = true
			return
		}

		ir := e.irType(elem, pos)
		l, r := e.load(ir, "%a"), e.load(ir, "%b")
		var less value
		if isString(elem) {
			c := e.rt("tk_string_cmp", "i32", l, r)
			less = e.op("icmp slt "+c.typed()+", 0", "i1")
		} else {
			less = e.less(l, r, elem)
		}

		e.inst("ret " + less.typed())
		e.fn.terminated = true
	})
	return "@" + name
}

// ownsBuffer asks the runtime whether the slice at p owns its buffer,
// which panics naming what when it is a sub-slice, whose length is its
// slice's to change.
func (e *Emitter) ownsBuffer(p, what string) {
	e.declare("tk_slice_own", "declare void @tk_slice_own(ptr, ptr)")
	e.inst("call void @tk_slice_own(ptr " + p + ", ptr " + e.constBytes(what) + ")")
}

// search returns the name of the function writing RemoveValue or
// Contains, which walks the slice comparing elements with the
// element's equality; a removed element is dropped.
func (e *Emitter) search(what string, t *types.Slice, pos token.Pos) string {
	name := "tk_" + what + "_" + lower.Mangle(t.Elem())
	if e.state[name] {
		return name
	}

	e.state[name] = true
	e.helper(name, "i1", []string{"ptr %s", "ptr %v"}, func() {
		e.eachElementIndexed("%s", t.Elem(), pos, func(p, i string) {
			found, next := e.block("found"), e.block("next")
			e.branch(e.equal(p, "%v", t.Elem(), pos).s, found, next)
			e.label(found)
			if what == "RemoveValue" {
				out := e.slot(e.irType(t.Elem(), pos))
				e.rt("tk_slice_remove", "void", value{"ptr", "%s"}, value{"i64", i}, value{"ptr", out}, e.sizeOf(t.Elem()))
				e.dropIfNeeded(out, t.Elem(), pos)
			}

			e.inst("ret i1 true")
			e.fn.terminated = true
			e.label(next)
		})
		e.inst("ret i1 false")
		e.fn.terminated = true
	})
	return name
}

func (e *Emitter) mapMethod(x *ast.CallExpr, sel *ast.SelectorExpr, t *types.Map) value {
	p, _ := e.receiver(sel)
	m := value{"ptr", p}
	mt := value{"ptr", e.mapType(t, x.Pos())}
	switch sel.Sel.Name {
	case "Get", "GetMut":
		found := e.rt("tk_map_find", "ptr", m, mt, value{"ptr", e.key(x.Args[0], t.Key())})
		if sel.Sel.Name == "GetMut" {
			return found // (mut V)? is the pointer itself, null for None
		}

		rt := e.typeOf(x)
		out := e.slot(e.irType(rt, x.Pos()))
		e.zero(out, rt, x.Pos())
		isNull := e.op("icmp eq ptr "+found.s+", null", "i1")
		some, done := e.block("some"), e.block("done")
		e.branch(isNull.s, done, some)
		e.label(some)
		opt := lower.ValueType(rt).Underlying().(*types.Option)
		got := found
		if depth(opt.Elem()) == 0 { // the value itself, not a borrow of the entry
			got = e.load(e.irType(t.Value(), x.Pos()), found.s)
		}

		e.storeOption(out, opt, got, x.Pos())
		e.jump(done)
		e.label(done)
		return e.load(e.irType(rt, x.Pos()), out)
	case "Insert":
		args := e.operands(x.Args, []types.Type{t.Key(), t.Value()})
		e.mapSet(p, t, args[0], args[1], x.Pos())
		return unit
	case "Remove":
		out := e.slot(e.irType(t.Value(), x.Pos()))
		e.zero(out, t.Value(), x.Pos())
		has := e.rt("tk_map_remove", "i1", m, mt, value{"ptr", e.key(x.Args[0], t.Key())}, value{"ptr", out})
		return e.optionOf(e.typeOf(x), has, e.load(e.irType(t.Value(), x.Pos()), out), x.Pos())
	case "Contains":
		found := e.rt("tk_map_find", "ptr", m, mt, value{"ptr", e.key(x.Args[0], t.Key())})
		return e.op("icmp ne ptr "+found.s+", null", "i1")
	case "Clone":
		if needsDrop(t.Key()) || needsDrop(t.Value()) {
			return e.op("call %tk_map @"+e.cloner(t, x.Pos())+"(ptr "+p+")", "%tk_map")
		}

		out := e.slot("%tk_map")
		e.store(value{"%tk_map", "zeroinitializer"}, out)
		e.rt("tk_map_clone", "void", m, mt, value{"ptr", out})
		return e.load("%tk_map", out)
	}

	e.unsupported(x.Pos(), "the method "+sel.Sel.Name+" on maps")
	return unit
}

// optionOf builds the option or Result-free option value of type t
// holding v when has is true and nothing otherwise.
func (e *Emitter) optionOf(t types.Type, has, v value, pos token.Pos) value {
	u := lower.ValueType(t).Underlying().(*types.Option)
	ir := e.irType(t, pos)
	if nullable(u) {
		return e.op("select i1 "+has.s+", "+v.typed()+", ptr null", "ptr")
	}

	out := e.insert(value{ir, "zeroinitializer"}, has, 0)
	return e.insert(out, v, 1)
}

// mapIndex writes m[k]: when written to, the address of the entry's
// value, made with the zero value when the key is new; when read, the
// address of the value or of a zero value, with the map unchanged. A
// key the statement owns, built there or moved out of its variable,
// goes to the new entry; a key only read from its variable, as m[k]
// op= v reads it, is cloned for the new entry and stays its owner's.
func (e *Emitter) mapIndex(x *ast.IndexExpr, p string, t *types.Map, write bool) string {
	if write {
		k := e.valueTo(x.Index, t.Key())
		if e.isRead(x.Index) && !e.info.Moves[x.Index] {
			return e.op("call ptr @"+e.mapLentSlotter(t, x.Pos())+"(ptr "+p+", ptr "+e.spill(k)+")", "ptr").s
		}

		return e.op("call ptr @"+e.mapSlotter(t, x.Pos())+"(ptr "+p+", "+k.typed()+")", "ptr").s
	}

	zero := e.slot(e.irType(t.Value(), x.Pos()))
	e.zero(zero, t.Value(), x.Pos())
	return e.rt("tk_map_get", "ptr", value{"ptr", p}, value{"ptr", e.mapType(t, x.Pos())}, value{"ptr", e.key(x.Index, t.Key())}, value{"ptr", zero}).s
}

// key writes a map key the operation only looks up, in a slot the
// runtime takes by address; a key built here is held for the statement
// and dropped with it.
func (e *Emitter) key(x ast.Expr, t types.Type) string {
	slot := e.spill(e.valueTo(x, t))
	if e.lent(x, nil) {
		e.hold(slot, t)
	}

	return slot
}

// lookedAt writes x, of type t, where the operation only looks at its
// value, as a ref parameter would, and returns its address: a value
// built here is held for the statement and dropped with it.
func (e *Emitter) lookedAt(x ast.Expr, t types.Type) string {
	v := e.operands([]ast.Expr{x}, []types.Type{types.NewRef(t, false)})[0]
	if viewed(t) {
		return e.spill(v) // lent as a view: a slot holds it
	}

	return v.s
}

// looked is lookedAt with the value read.
func (e *Emitter) looked(x ast.Expr, t types.Type) value {
	v := e.operands([]ast.Expr{x}, []types.Type{types.NewRef(t, false)})[0]
	if viewed(t) {
		return v
	}

	return e.load(e.irType(t, x.Pos()), v.s)
}

// mapSlotter returns the name of the function giving the address of
// the value for a key in a map of type t, writing it on first use: the
// key is taken, kept by a new entry and dropped when an entry already
// has it.
func (e *Emitter) mapSlotter(t *types.Map, pos token.Pos) string {
	name := "tk_mapslot_" + lower.Mangle(t)
	if e.state[name] {
		return name
	}

	e.state[name] = true
	kt := e.irType(t.Key(), pos)
	e.helper(name, "ptr", []string{"ptr %m", kt + " %k"}, func() {
		k := e.spill(value{kt, "%k"})
		mt := value{"ptr", e.mapType(t, pos)}
		found := e.rt("tk_map_find", "ptr", value{"ptr", "%m"}, mt, value{"ptr", k})
		isNull := e.op("icmp eq ptr "+found.s+", null", "i1")
		new, have := e.block("new"), e.block("have")
		e.branch(isNull.s, new, have)
		e.label(have)
		e.dropIfNeeded(k, t.Key(), pos)
		e.inst("ret " + found.typed())
		e.fn.terminated = true
		e.label(new)
		slot := e.rt("tk_map_slot", "ptr", value{"ptr", "%m"}, mt, value{"ptr", k})
		e.inst("ret " + slot.typed())
		e.fn.terminated = true
	})
	return name
}

// mapLentSlotter returns the name of the function giving the address
// of the value for a key the caller keeps, at the address k, writing it
// on first use: a new entry takes a copy of the key.
func (e *Emitter) mapLentSlotter(t *types.Map, pos token.Pos) string {
	name := "tk_mapslot_lent_" + lower.Mangle(t)
	if e.state[name] {
		return name
	}

	e.state[name] = true
	e.helper(name, "ptr", []string{"ptr %m", "ptr %k"}, func() {
		mt := value{"ptr", e.mapType(t, pos)}
		found := e.rt("tk_map_find", "ptr", value{"ptr", "%m"}, mt, value{"ptr", "%k"})
		isNull := e.op("icmp eq ptr "+found.s+", null", "i1")
		new, have := e.block("new"), e.block("have")
		e.branch(isNull.s, new, have)
		e.label(have)
		e.inst("ret " + found.typed())
		e.fn.terminated = true
		e.label(new)
		copy := e.spill(e.cloneAt("%k", t.Key(), pos))
		slot := e.rt("tk_map_slot", "ptr", value{"ptr", "%m"}, mt, value{"ptr", copy})
		e.inst("ret " + slot.typed())
		e.fn.terminated = true
	})
	return name
}

// mapSet writes the entry for the key k in the map at the address p,
// with v as its value; an entry already there drops its old value.
func (e *Emitter) mapSet(p string, t *types.Map, k, v value, pos token.Pos) {
	name := "tk_mapset_" + lower.Mangle(t)
	if !e.state[name] {
		e.state[name] = true
		kt, vt := e.irType(t.Key(), pos), e.irType(t.Value(), pos)
		e.helper(name, "void", []string{"ptr %m", kt + " %k", vt + " %v"}, func() {
			slot := e.op("call ptr @"+e.mapSlotter(t, pos)+"(ptr %m, "+kt+" %k)", "ptr")
			e.dropIfNeeded(slot.s, t.Value(), pos)
			e.store(value{vt, "%v"}, slot.s)
			e.inst("ret void")
			e.fn.terminated = true
		})
	}

	e.inst("call void @" + name + "(ptr " + p + ", " + k.typed() + ", " + v.typed() + ")")
}

// mapType returns the global holding the descriptor the runtime needs
// for a map type, its key and value sizes, the key's hash and equality
// and the drops of both, writing it and the two key functions on first
// use.
func (e *Emitter) mapType(t *types.Map, pos token.Pos) string {
	name := "tk_mt_" + lower.Mangle(t)
	if e.state[name] {
		return "@" + name
	}

	e.state[name] = true
	hash, eq := "tk_khash_"+lower.Mangle(t.Key()), "tk_keq_"+lower.Mangle(t.Key())
	if !e.state[hash] {
		e.state[hash] = true
		e.helper(hash, "i64", []string{"ptr %p"}, func() {
			e.inst("ret " + e.hash("%p", t.Key(), pos).typed())
			e.fn.terminated = true
		})
		e.helper(eq, "zeroext i1", []string{"ptr %a", "ptr %b"}, func() {
			e.inst("ret " + e.equal("%a", "%b", t.Key(), pos).typed())
			e.fn.terminated = true
		})
	}

	kdrop, vdrop := "null", "null"
	if needsDrop(t.Key()) {
		kdrop = "@" + e.dropper(t.Key(), pos)
	}

	if needsDrop(t.Value()) {
		vdrop = "@" + e.dropper(t.Value(), pos)
	}

	put(&e.consts, "@", name, " = internal constant %tk_map_type { i64 ", e.sizeOf(t.Key()).s, ", i64 ", e.sizeOf(t.Value()).s, ", ptr @", hash, ", ptr @", eq, ", ptr ", kdrop, ", ptr ", vdrop, " }\n")
	return "@" + name
}

// eachEntry writes a loop over the live entries of the map at the
// address m, in insertion order, calling body with the addresses of
// each key and value.
func (e *Emitter) eachEntry(m string, t *types.Map, pos token.Pos, body func(k, v string)) {
	e.declare("tk_map_next", "declare i64 @tk_map_next(ptr, i64)")
	i := e.slot("i64")
	e.store(e.op("call i64 @tk_map_next(ptr "+m+", i64 0)", "i64"), i)
	cond, loop, done := e.block("entry.cond"), e.block("entry"), e.block("entry.done")
	e.jump(cond)
	e.label(cond)
	cur := e.load("i64", i)
	more := e.op("icmp sge i64 "+cur.s+", 0", "i1")
	e.branch(more.s, loop, done)
	e.label(loop)
	keys := e.load("ptr", e.gep("%tk_map", m, 0))
	vals := e.load("ptr", e.gep("%tk_map", m, 1))
	body(e.elem(e.irType(t.Key(), pos), keys.s, cur.s), e.elem(e.irType(t.Value(), pos), vals.s, cur.s))
	next := e.op("add i64 "+cur.s+", 1", "i64")
	e.store(e.op("call i64 @tk_map_next(ptr "+m+", i64 "+next.s+")", "i64"), i)
	e.jump(cond)
	e.label(done)
}

// eachElementIndexed writes a loop over the elements of the slice at
// the address s, calling body with the address and the index of each.
func (e *Emitter) eachElementIndexed(s string, elem types.Type, pos token.Pos, body func(p, i string)) {
	ptr := e.load("ptr", e.gep("%tk_slice", s, 0))
	n := e.load("i64", e.gep("%tk_slice", s, 1))
	i := e.slot("i64")
	e.store(value{"i64", "0"}, i)
	cond, loop, done := e.block("each.cond"), e.block("each"), e.block("each.done")
	e.jump(cond)
	e.label(cond)
	cur := e.load("i64", i)
	more := e.op("icmp slt i64 "+cur.s+", "+n.s, "i1")
	e.branch(more.s, loop, done)
	e.label(loop)
	body(e.elem(e.irType(elem, pos), ptr.s, cur.s), cur.s)
	next := e.op("add i64 "+cur.s+", 1", "i64")
	e.store(next, i)
	e.jump(cond)
	e.label(done)
}

// sliceMake writes a slice of n elements of type elem, with room for c,
// into the slot out.
func (e *Emitter) sliceMake(out string, n, c value, elem types.Type, pos token.Pos) {
	e.rt("tk_slice_make", "void", value{"ptr", out}, n, c, e.sizeOf(elem))
}

// mapLit writes map[K]V{k: v}: an empty map, then one insertion per
// entry.
func (e *Emitter) mapLit(x *ast.CompositeLit, t types.Type, mt *types.Map) value {
	out := e.slot("%tk_map")
	e.store(value{"%tk_map", "zeroinitializer"}, out)
	for _, elt := range x.Elts {
		kv := elt.(*ast.KeyValueExpr)
		parts := e.operands([]ast.Expr{kv.Key, kv.Value}, []types.Type{mt.Key(), mt.Value()})
		e.mapSet(out, mt, parts[0], parts[1], x.Pos())
	}

	return e.load("%tk_map", out)
}

// sliceLit writes []T{a, b}: a slice made to size, then filled.
func (e *Emitter) sliceLit(x *ast.CompositeLit, t types.Type, st *types.Slice) value {
	if len(x.Elts) == 0 {
		return value{"%tk_slice", "zeroinitializer"}
	}

	wants := make([]types.Type, len(x.Elts))
	for i := range wants {
		wants[i] = st.Elem()
	}

	parts := e.operands(x.Elts, wants)
	n := value{"i64", strconv.Itoa(len(parts))}
	out := e.slot("%tk_slice")
	e.sliceMake(out, n, n, st.Elem(), x.Pos())
	base := e.load("ptr", e.gep("%tk_slice", out, 0))
	et := e.irType(st.Elem(), x.Pos())
	for i, p := range parts {
		e.store(p, e.elem(et, base.s, strconv.Itoa(i)))
	}

	return e.load("%tk_slice", out)
}

func (e *Emitter) stringMethod(x *ast.CallExpr, sel *ast.SelectorExpr) value {
	s := e.looked(sel.X, types.Typ[types.String])
	args := make([]value, len(x.Args))
	for i, a := range x.Args {
		args[i] = e.looked(a, types.Typ[types.String])
	}

	switch sel.Sel.Name {
	case "Len":
		return e.extract(s, 1, "i64")
	case "ToUpper":
		return e.rt("tk_to_upper", "%tk_string", s)
	case "ToLower":
		return e.rt("tk_to_lower", "%tk_string", s)
	case "Trim":
		return e.rt("tk_trim", "%tk_string", s)
	case "HasPrefix":
		return e.rt("tk_has_prefix", "i1", s, args[0])
	case "HasSuffix":
		return e.rt("tk_has_suffix", "i1", s, args[0])
	case "Contains":
		return e.rt("tk_string_contains", "i1", s, args[0])
	case "Fields", "Lines", "Split":
		out := e.slot("%tk_slice")
		name := map[string]string{"Fields": "tk_fields", "Lines": "tk_lines", "Split": "tk_split"}[sel.Sel.Name]
		e.rt(name, "void", append(append([]value{s}, args...), value{"ptr", out})...)
		return e.load("%tk_slice", out)
	case "SplitOnce":
		before, after := e.slot("%tk_string"), e.slot("%tk_string")
		has := e.rt("tk_split_once", "i1", s, args[0], value{"ptr", before}, value{"ptr", after})
		pair := e.irType(lower.ValueType(e.typeOf(x)).Underlying().(*types.Option).Elem(), x.Pos())
		tuple := e.insert(value{pair, "zeroinitializer"}, e.load("%tk_string", before), 0)
		tuple = e.insert(tuple, e.load("%tk_string", after), 1)
		return e.optionOf(e.typeOf(x), has, tuple, x.Pos())
	}

	e.unsupported(x.Pos(), "the method "+sel.Sel.Name+" on strings")
	return unit
}

// byteMethod writes the character classes of a byte as the
// comparisons teak.h writes them, which are ASCII, as a byte is.
func (e *Emitter) byteMethod(x *ast.CallExpr, sel *ast.SelectorExpr) value {
	c := e.looked(sel.X, types.Typ[types.Uint8])
	between := func(lo, hi byte) value {
		ge := e.op("icmp uge "+c.typed()+", "+strconv.Itoa(int(lo)), "i1")
		le := e.op("icmp ule "+c.typed()+", "+strconv.Itoa(int(hi)), "i1")
		return e.op("and i1 "+ge.s+", "+le.s, "i1")
	}

	switch sel.Sel.Name {
	case "IsDigit":
		return between('0', '9')
	case "IsSpace":
		out := value{"i1", "false"}
		for _, ws := range []byte{' ', '\t', '\n', '\r', '\f', '\v'} {
			is := e.op("icmp eq "+c.typed()+", "+strconv.Itoa(int(ws)), "i1")
			out = e.op("or i1 "+out.s+", "+is.s, "i1")
		}

		return out
	case "IsLetter":
		upper, lower := between('A', 'Z'), between('a', 'z')
		return e.op("or i1 "+upper.s+", "+lower.s, "i1")
	case "IsUpper":
		return between('A', 'Z')
	case "IsLower":
		return between('a', 'z')
	}

	e.unsupported(x.Pos(), "the method "+sel.Sel.Name+" on bytes")
	return unit
}

// standardMethod writes the method of a standard trait on a number, a
// string or a bool, Eq, Cmp, Hash or Clone, from the comparison, hash
// and copy the backend writes for the type; false for any other
// method.
func (e *Emitter) standardMethod(x *ast.CallExpr, sel *ast.SelectorExpr, recv types.Type) (value, bool) {
	switch sel.Sel.Name {
	case "Eq", "Cmp":
		a := e.lookedAt(sel.X, recv)
		b := e.lookedAt(x.Args[0], recv)
		if sel.Sel.Name == "Eq" {
			return e.equal(a, b, recv, x.Pos()), true
		}

		return e.compare(a, b, recv, x.Pos()), true
	case "Hash":
		return e.hash(e.lookedAt(sel.X, recv), recv, x.Pos()), true
	case "Clone":
		return e.cloneAt(e.lookedAt(sel.X, recv), recv, x.Pos()), true
	}

	return unit, false
}

// optionMethod writes the methods options and results share; absent
// is the name of the empty case for the panic message.
func (e *Emitter) optionMethod(x *ast.CallExpr, sel *ast.SelectorExpr, absent string) value {
	p, t, held := e.consumed(sel.X)
	if held != "" {
		e.hold(held, e.typeOf(sel.X))
	}

	ir := e.irType(t, x.Pos())
	var has value
	var val string
	var elem types.Type
	r, isResult := t.Underlying().(*types.Result)
	if isResult {
		has = e.load("i1", e.gep(ir, p, 0))
		val = e.gep(ir, p, 1)
		elem = r.Val()
	} else {
		u := t.Underlying().(*types.Option)
		has, val = e.optionParts(ir, p, u)
		elem = u.Elem()
	}

	switch sel.Sel.Name {
	case "IsSome", "IsOk":
		return has
	case "IsNone", "IsErr":
		return e.op("xor i1 "+has.s+", true", "i1")
	case "Expect", "Unwrap":
		fail, ok := e.block("absent"), e.block("present")
		e.branch(has.s, ok, fail)
		e.label(fail)
		msg := e.lit(sel.Sel.Name + " on " + absent)
		if sel.Sel.Name == "Expect" {
			msg = text{e.looked(x.Args[0], types.Typ[types.String]), false}
		}

		if isResult {
			msg = e.join(e.join(msg, e.lit(": ")), e.textAt(val, r.Err(), x.Pos(), false))
		}

		e.panic(e.built(msg))
		e.label(ok)
		return e.inside(val, elem, e.typeOf(x), held, e.typeOf(sel.X), x.Pos())
	case "UnwrapOr":
		rt := e.typeOf(x)
		out := e.spill(e.valueTo(x.Args[0], rt))
		some, done := e.block("some"), e.block("done")
		e.branch(has.s, some, done)
		e.label(some)
		e.dropIfNeeded(out, rt, x.Pos())
		e.store(e.inside(val, elem, rt, held, e.typeOf(sel.X), x.Pos()), out)
		e.jump(done)
		e.label(done)
		return e.load(e.irType(rt, x.Pos()), out)
	case "Map", "MapErr":
		return e.mapped(x, sel, p, t, held, has, val)
	}

	e.unsupported(x.Pos(), "the method "+sel.Sel.Name+" on "+t.String())
	return unit
}

// mapped writes Map and MapErr on the option or result at the address
// p, of type t: the function argument is called on the part it maps,
// moved out of the receiver, and the other part, when there is one,
// moves into the result as it is. The function's type comes from the
// instance the checker recorded for the call.
func (e *Emitter) mapped(x *ast.CallExpr, sel *ast.SelectorExpr, p string, t types.Type, held string, has value, val string) value {
	inst, ok := e.info.Instances[sel.Sel]
	if !ok {
		e.unsupported(x.Pos(), sel.Sel.Name+" without its instance")
		return unit
	}

	fsig := e.sub(inst.Type.Params()[0].Type()).(*types.Signature)
	f := e.spill(e.valueTo(x.Args[0], fsig))
	pair := e.irType(fsig, x.Pos())
	fn := e.load("ptr", e.gep(pair, f, 0))
	box := e.gep(pair, f, 1)
	through := func(v value) value {
		return e.op("call "+e.irType(fsig.Results(), x.Pos())+" "+fn.s+"(ptr "+box+", "+v.typed()+")", e.irType(fsig.Results(), x.Pos()))
	}

	rt := e.typeOf(x)
	ir := e.irType(rt, x.Pos())
	out := e.slot(ir)
	e.zero(out, rt, x.Pos())
	mapsErr := sel.Sel.Name == "MapErr"
	heldType := e.typeOf(sel.X)
	some, none, done := e.block("some"), e.block("none"), e.block("done")
	e.branch(has.s, some, none)
	e.label(some)
	switch u := t.Underlying().(type) {
	case *types.Option:
		v := through(e.taken(val, u.Elem(), held, heldType, x.Pos()))
		e.storeOption(out, lower.ValueType(rt).Underlying().(*types.Option), v, x.Pos())
	case *types.Result:
		v := e.taken(val, u.Val(), held, heldType, x.Pos())
		if !mapsErr {
			v = through(v)
		}

		e.store(value{"i1", "true"}, e.gep(ir, out, 0))
		e.store(v, e.gep(ir, out, 1))
	}

	e.jump(done)
	e.label(none)
	if u, isResult := t.Underlying().(*types.Result); isResult {
		err := e.taken(val, u.Err(), held, heldType, x.Pos())
		if mapsErr {
			err = through(err)
		}

		e.store(err, e.gep(ir, out, 1))
	}

	e.jump(done)
	e.label(done)
	return e.load(ir, out)
}
