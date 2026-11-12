// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// The predeclared methods of slices, maps, strings, bytes, options and
// results, and the built-ins that work on them, are lowered here onto
// the runtime: the library table of _docs/design/05-standard-library.md.
// What snello.h writes as a static inline function, an index check or a
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
	case *types.Tuple:
		if call, ok := e.standardMethod(x, sel, recv); ok {
			return call // Eq, which expect.Equal calls on any Eq type
		}
	case *types.Basic:
		if call, ok := e.standardMethod(x, sel, recv); ok {
			return call
		}

		if t.Kind() == types.String {
			return e.stringMethod(x, sel)
		}

		if t.Info()&types.IsInteger != 0 {
			if call, ok := e.integerMethod(x, sel, recv, t); ok {
				return call
			}
		}

		if t.Kind() == types.Uint8 {
			return e.byteMethod(x, sel)
		}
	case *types.Option:
		if call, ok := e.standardMethod(x, sel, recv); ok {
			return call
		}

		return e.optionMethod(x, sel, "None")
	case *types.Result:
		if call, ok := e.standardMethod(x, sel, recv); ok {
			return call
		}

		return e.optionMethod(x, sel, "Err")
	}

	e.unsupported(x.Pos(), "the method "+sel.Sel.Name+" on "+recv.String())
	return unit
}

// mutating names the predeclared methods that change their receiver.
var mutating = map[string]bool{
	"Push": true, "Pop": true, "Insert": true, "Remove": true, "RemoveValue": true,
	"Clear": true, "Swap": true, "Reverse": true, "Sort": true, "SortBy": true, "GetMut": true,
	"Extend": true, "Truncate": true, "GetOrInsert": true,
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
		if e.cfg.Debug { // the runtime's checks on every push
			e.rt("sn_slice_push", "void", value{"ptr", p}, value{"ptr", v}, size)
			return unit
		}

		// With room, in place; growing, and a sub-slice's panic, the
		// runtime's. A sub-slice's capacity is below every length.
		n := e.load("i64", e.gep("%sn_slice", p, 1))
		room := e.op("icmp slt i64 "+n.s+", "+e.load("i64", e.gep("%sn_slice", p, 2)).s, "i1")
		fast, slow, done := e.block("push.fast"), e.block("push.slow"), e.block("push.done")
		e.branch(room.s, fast, slow)
		e.label(fast)
		t := e.irType(elem, x.Pos())
		base := e.load("ptr", e.gep("%sn_slice", p, 0))
		e.store(e.load(t, v), e.elem(t, base.s, n.s))
		e.store(e.op("add i64 "+n.s+", 1", "i64"), e.gep("%sn_slice", p, 1))
		e.jump(done)
		e.label(slow)
		e.rt("sn_slice_push", "void", value{"ptr", p}, value{"ptr", v}, size)
		e.jump(done)
		e.label(done)
		return unit
	case "Pop":
		t := e.irType(elem, x.Pos())
		out := e.slot(t)
		e.zero(out, elem, x.Pos())
		if e.cfg.Debug {
			has := e.rt("sn_slice_pop", "i1", value{"ptr", p}, value{"ptr", out}, size)
			return e.optionOf(e.typeOf(x), has, e.load(t, out), x.Pos())
		}

		// A slice of its own with an element, in place; the rest the
		// runtime's.
		n := e.load("i64", e.gep("%sn_slice", p, 1))
		some := e.op("icmp sgt i64 "+n.s+", 0", "i1")
		own := e.op("icmp sge i64 "+e.load("i64", e.gep("%sn_slice", p, 2)).s+", 0", "i1")
		fast, slow, done := e.block("pop.fast"), e.block("pop.slow"), e.block("pop.done")
		e.branch(e.op("and i1 "+some.s+", "+own.s, "i1").s, fast, slow)
		e.label(fast)
		last := e.op("sub i64 "+n.s+", 1", "i64")
		e.store(last, e.gep("%sn_slice", p, 1))
		base := e.load("ptr", e.gep("%sn_slice", p, 0))
		e.store(e.load(t, e.elem(t, base.s, last.s)), out)
		e.jump(done)
		e.label(slow)
		got := e.rt("sn_slice_pop", "i1", value{"ptr", p}, value{"ptr", out}, size)
		e.jump(done)
		e.label(done)
		has := e.op("phi i1 [ true, %"+fast+" ], [ "+got.s+", %"+slow+" ]", "i1")
		return e.optionOf(e.typeOf(x), has, e.load(t, out), x.Pos())
	case "Insert":
		args := e.operands(x.Args, []types.Type{types.Typ[types.Int], elem})
		v := e.spill(args[1])
		e.rt("sn_slice_insert", "void", value{"ptr", p}, args[0], value{"ptr", v}, size)
		return unit
	case "Remove":
		out := e.slot(e.irType(elem, x.Pos()))
		i := e.valueTo(x.Args[0], types.Typ[types.Int])
		e.rt("sn_slice_remove", "void", value{"ptr", p}, i, value{"ptr", out}, size)
		return e.load(e.irType(elem, x.Pos()), out)
	case "RemoveValue", "Contains":
		v := e.lookedAt(x.Args[0], elem)
		return e.op("call i1 @"+e.search(sel.Sel.Name, t, x.Pos())+"(ptr "+p+", ptr "+v+")", "i1")
	case "Clear":
		e.ownsBuffer(p, "Clear")
		if needsDrop(elem) {
			e.eachElement(p, elem, x.Pos(), func(q string) { e.dropOf(q, elem, x.Pos()) })
		}

		e.store(value{"i64", "0"}, e.gep("%sn_slice", p, 1))
		return unit
	case "Swap": // both indexes checked, in order, then the two elements exchanged in place
		args := e.operands(x.Args, []types.Type{types.Typ[types.Int], types.Typ[types.Int]})
		base := e.load("ptr", e.gep("%sn_slice", p, 0))
		n := e.load("i64", e.gep("%sn_slice", p, 1))
		a := e.checkIndex(args[0], n)
		b := e.checkIndex(args[1], n)
		t := e.irType(elem, x.Pos())
		pa, pb := e.elem(t, base.s, a.s), e.elem(t, base.s, b.s)
		va, vb := e.load(t, pa), e.load(t, pb)
		e.store(vb, pa)
		e.store(va, pb)
		return unit
	case "Reverse":
		e.rt("sn_slice_reverse", "void", value{"ptr", p}, size)
		return unit
	case "Clone":
		if !lower.ClonesAsBytes(elem) {
			return e.op("call %sn_slice @"+e.cloner(t, x.Pos())+"(ptr "+p+")", "%sn_slice") // element by element, so that each copy owns its own and a Clone of the type's own runs
		}

		out := e.slot("%sn_slice")
		e.rt("sn_slice_clone", "void", value{"ptr", p}, value{"ptr", out}, size)
		return e.load("%sn_slice", out)
	case "SortBy":
		less := e.spill(e.valueTo(x.Args[0], e.typeOf(x.Args[0])))
		e.inst("call void @" + e.sorter("sn_sortby_"+lower.Mangle(elem), "@"+e.lessAdapter(elem, x.Pos()), size) + "(ptr " + p + ", ptr " + less + ")")
		return unit
	case "Sort":
		if order := e.ordering(elem, x.Pos()); order != "null" {
			e.inst("call void @" + e.sorter("sn_sort_"+lower.Mangle(elem), order, size) + "(ptr " + p + ", ptr null)")
		}

		return unit
	case "Extend": // one growth to the final length, then a copy of the bytes or a clone of each element
		ys := e.spill(e.looked(x.Args[0], t))
		yn := e.load("i64", e.gep("%sn_slice", ys, 1))
		grow, done := e.block("extend"), e.block("extend.done")
		e.branch(e.op("icmp sgt i64 "+yn.s+", 0", "i1").s, grow, done)
		e.label(grow)
		n := e.load("i64", e.gep("%sn_slice", p, 1))
		need := e.op("add i64 "+n.s+", "+yn.s, "i64")
		e.rt("sn_slice_reserve", "void", value{"ptr", p}, need, size, value{"ptr", e.constBytes("Extend")})
		et := e.irType(elem, x.Pos())
		dst := e.elem(et, e.load("ptr", e.gep("%sn_slice", p, 0)).s, n.s)
		src := e.load("ptr", e.gep("%sn_slice", ys, 0))
		if !lower.ClonesAsBytes(elem) { // a Clone a type in the element writes runs; only an element with none is its bytes
			e.forRange(value{"i64", "0"}, yn, func(i string) {
				e.store(e.cloneAt(e.elem(et, src.s, i), elem, x.Pos()), e.elem(et, dst, i))
			})
		} else {
			bytes := e.op("mul i64 "+yn.s+", "+size.s, "i64")
			e.declare("llvm.memcpy.p0.p0.i64", "declare void @llvm.memcpy.p0.p0.i64(ptr, ptr, i64, i1)")
			e.inst("call void @llvm.memcpy.p0.p0.i64(ptr " + dst + ", ptr " + src.s + ", i64 " + bytes.s + ", i1 false)")
		}

		e.store(need, e.gep("%sn_slice", p, 1))
		e.jump(done)
		e.label(done)
		return unit
	case "Index":
		v := e.lookedAt(x.Args[0], elem)
		var at value
		if byteEqual(elem) { // memchr, when equality is the byte's
			base := e.load("ptr", e.gep("%sn_slice", p, 0))
			n := e.load("i64", e.gep("%sn_slice", p, 1))
			at = e.rt("sn_slice_index_byte", "i64", base, n, e.load("i8", v))
		} else {
			at = e.op("call i64 @"+e.position(t, x.Pos())+"(ptr "+p+", ptr "+v+")", "i64")
		}

		return e.someIndex(at, x)
	case "First", "Last":
		rt := e.typeOf(x)
		out := e.slot(e.irType(rt, x.Pos()))
		e.zero(out, rt, x.Pos())
		n := e.load("i64", e.gep("%sn_slice", p, 1))
		some, done := e.block("some"), e.block("done")
		e.branch(e.op("icmp sgt i64 "+n.s+", 0", "i1").s, some, done)
		e.label(some)
		i := "0"
		if sel.Sel.Name == "Last" {
			i = e.op("sub i64 "+n.s+", 1", "i64").s
		}

		at := e.elem(e.irType(elem, x.Pos()), e.load("ptr", e.gep("%sn_slice", p, 0)).s, i)
		opt := lower.ValueType(rt).Underlying().(*types.Option)
		got := value{"ptr", at}
		if depth(opt.Elem()) == 0 { // the element itself, not a borrow of it
			got = e.load(e.irType(elem, x.Pos()), at)
		}

		e.storeOption(out, opt, got, x.Pos())
		e.jump(done)
		e.label(done)
		return e.load(e.irType(rt, x.Pos()), out)
	case "Truncate": // the elements from n on dropped, first to last, as Clear drops them; the memory kept
		n := e.valueTo(x.Args[0], types.Typ[types.Int])
		neg := e.op("icmp slt i64 "+n.s+", 0", "i1")
		n = e.op("select i1 "+neg.s+", i64 0, i64 "+n.s, "i64")
		length := e.load("i64", e.gep("%sn_slice", p, 1))
		cut, done := e.block("truncate"), e.block("truncate.done")
		e.branch(e.op("icmp slt i64 "+n.s+", "+length.s, "i1").s, cut, done)
		e.label(cut)
		e.ownsBuffer(p, "Truncate")
		if needsDrop(elem) {
			et := e.irType(elem, x.Pos())
			base := e.load("ptr", e.gep("%sn_slice", p, 0))
			e.forRange(n, length, func(i string) { e.dropOf(e.elem(et, base.s, i), elem, x.Pos()) })
		}

		e.store(n, e.gep("%sn_slice", p, 1))
		e.jump(done)
		e.label(done)
		return unit
	case "Equal":
		return e.rt("sn_bytes_equal", "i1", e.load("%sn_slice", p), e.looked(x.Args[0], t))
	case "Find":
		return e.someIndex(e.rt("sn_bytes_find", "i64", e.load("%sn_slice", p), e.looked(x.Args[0], t)), x)
	}

	e.unsupported(x.Pos(), "the method "+sel.Sel.Name+" on slices")
	return unit
}

// lessAdapter returns the name of the function that lets the runtime
// sort call a Snello less function held in the context, written once per
// element type: the runtime hands it the addresses of two elements,
// which are the ref borrows the function takes.
func (e *Emitter) lessAdapter(elem types.Type, pos token.Pos) string {
	name := "sn_lessfn_" + lower.Mangle(elem)
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

// sorter returns the name of the function that sorts a slice by less,
// one for each element type and order: the runtime's sort is inlined
// into it when the module is optimized with the runtime, and with the
// element's size and less constants there, clang compiles one sort for
// the type with the comparison inlined, as a generic sort is
// instantiated for each type.
func (e *Emitter) sorter(name, less string, size value) string {
	if e.state[name] {
		return name
	}

	e.state[name] = true
	e.helper(name, "void", []string{"ptr %s", "ptr %ctx"}, func() {
		e.rt("sn_slice_sort", "void", value{"ptr", "%s"}, size, value{"ptr", less}, value{"ptr", "%ctx"})
		e.inst("ret void")
		e.fn.terminated = true
	})
	return name
}

// ordering returns the function that compares two elements with <,
// for Sort on numbers and strings, and on a type with a Cmp, written or
// derived, through it: a before b when a.Cmp(b) is Less, as sort.Sort
// orders. It is a pointer operand.
func (e *Emitter) ordering(elem types.Type, pos token.Pos) string {
	name := "sn_lt_" + lower.Mangle(elem)
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
			c := e.rt("sn_string_cmp", "i32", l, r)
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
	e.declare("sn_slice_own", "declare void @sn_slice_own(ptr, ptr)")
	e.inst("call void @sn_slice_own(ptr " + p + ", ptr " + e.constBytes(what) + ")")
}

// forRange writes a loop running body over the indexes from lo up to
// hi, hi excluded.
func (e *Emitter) forRange(lo, hi value, body func(i string)) {
	i := e.slot("i64")
	e.store(lo, i)
	cond, loop, done := e.block("range.cond"), e.block("range"), e.block("range.done")
	e.jump(cond)
	e.label(cond)
	cur := e.load("i64", i)
	e.branch(e.op("icmp slt i64 "+cur.s+", "+hi.s, "i1").s, loop, done)
	e.label(loop)
	body(cur.s)
	e.store(e.op("add i64 "+cur.s+", 1", "i64"), i)
	e.jump(cond)
	e.label(done)
}

// byteEqual reports whether a value of type t is one byte compared as
// the byte it is: u8 or i8, or a type declared over one that writes no
// Eq of its own.
func byteEqual(t types.Type) bool {
	t = lower.ValueType(t)
	if n, ok := t.(*types.Named); ok && written(n, "Eq") {
		return false
	}

	b, ok := t.Underlying().(*types.Basic)
	return ok && (b.Kind() == types.Uint8 || b.Kind() == types.Int8)
}

// someIndex writes the option a search returns from the offset at, -1
// when it found nothing.
func (e *Emitter) someIndex(at value, x *ast.CallExpr) value {
	has := e.op("icmp sge i64 "+at.s+", 0", "i1")
	return e.optionOf(e.typeOf(x), has, at, x.Pos())
}

// position returns the name of the function writing Index on a slice
// of t's element type, which walks the slice comparing elements with
// the element's equality and gives the first equal one's index, or -1.
func (e *Emitter) position(t *types.Slice, pos token.Pos) string {
	name := "sn_Index_" + lower.Mangle(t.Elem())
	if e.state[name] {
		return name
	}

	e.state[name] = true
	e.helper(name, "i64", []string{"ptr %s", "ptr %v"}, func() {
		e.eachElementIndexed("%s", t.Elem(), pos, func(p, i string) {
			found, next := e.block("found"), e.block("next")
			e.branch(e.equal(p, "%v", t.Elem(), pos).s, found, next)
			e.label(found)
			e.inst("ret i64 " + i)
			e.fn.terminated = true
			e.label(next)
		})
		e.inst("ret i64 -1")
		e.fn.terminated = true
	})
	return name
}

// search returns the name of the function writing RemoveValue or
// Contains, which walks the slice comparing elements with the
// element's equality; a removed element is dropped.
func (e *Emitter) search(what string, t *types.Slice, pos token.Pos) string {
	name := "sn_" + what + "_" + lower.Mangle(t.Elem())
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
				e.rt("sn_slice_remove", "void", value{"ptr", "%s"}, value{"i64", i}, value{"ptr", out}, e.sizeOf(t.Elem()))
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
		found := e.rt("sn_map_find", "ptr", m, mt, value{"ptr", e.key(x.Args[0], t.Key())})
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
	case "GetOrInsert":
		args := e.operands(x.Args, []types.Type{t.Key(), t.Value()})
		return e.op("call ptr @"+e.mapGetOrInsert(t, x.Pos())+"(ptr "+p+", "+args[0].typed()+", "+args[1].typed()+")", "ptr")
	case "Remove":
		out := e.slot(e.irType(t.Value(), x.Pos()))
		e.zero(out, t.Value(), x.Pos())
		has := e.rt("sn_map_remove", "i1", m, mt, value{"ptr", e.key(x.Args[0], t.Key())}, value{"ptr", out})
		return e.optionOf(e.typeOf(x), has, e.load(e.irType(t.Value(), x.Pos()), out), x.Pos())
	case "Contains":
		found := e.rt("sn_map_find", "ptr", m, mt, value{"ptr", e.key(x.Args[0], t.Key())})
		return e.op("icmp ne ptr "+found.s+", null", "i1")
	case "Entry": // the next entry's place, key and value, the borrows as addresses into the map
		e.declare("sn_map_next", "declare i64 @sn_map_next(ptr, i64)")
		at := e.op("call i64 @sn_map_next(ptr "+p+", "+e.operands(x.Args, []types.Type{types.Typ[types.Int]})[0].typed()+")", "i64")
		has := e.op("icmp sge i64 "+at.s+", 0", "i1")
		keys := e.load("ptr", e.gep("%sn_map", p, 0))
		vals := e.load("ptr", e.gep("%sn_map", p, 1))
		k := e.elem(e.irType(t.Key(), x.Pos()), keys.s, at.s)
		v := e.elem(e.irType(t.Value(), x.Pos()), vals.s, at.s)
		item := lower.ValueType(e.typeOf(x)).Underlying().(*types.Option).Elem()
		pair := item.Underlying().(*types.Tuple)
		kv, vv := value{"ptr", k}, value{"ptr", v}
		if depth(pair.At(1)) == 0 { // a ref of a string or a slice is the view itself, not an address
			kv = e.load(e.irType(t.Key(), x.Pos()), k)
		}

		if depth(pair.At(2)) == 0 {
			vv = e.load(e.irType(t.Value(), x.Pos()), v)
		}

		tup := e.insert(value{e.irType(item, x.Pos()), "zeroinitializer"}, at, 0)
		tup = e.insert(tup, kv, 1)
		tup = e.insert(tup, vv, 2)
		return e.optionOf(e.typeOf(x), has, tup, x.Pos())
	case "Clone":
		if !lower.ClonesAsBytes(t.Key()) || !lower.ClonesAsBytes(t.Value()) {
			return e.op("call %sn_map @"+e.cloner(t, x.Pos())+"(ptr "+p+")", "%sn_map")
		}

		out := e.slot("%sn_map")
		e.store(value{"%sn_map", "zeroinitializer"}, out)
		e.rt("sn_map_clone", "void", m, mt, value{"ptr", out})
		return e.load("%sn_map", out)
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
		if e.isLent(x.Index) && !e.info.Moves[x.Index] {
			return e.op("call ptr @"+e.mapLentSlotter(t, x.Pos())+"(ptr "+p+", ptr "+e.spill(k)+")", "ptr").s
		}

		return e.op("call ptr @"+e.mapSlotter(t, x.Pos())+"(ptr "+p+", "+k.typed()+")", "ptr").s
	}

	zero := e.slot(e.irType(t.Value(), x.Pos()))
	e.zero(zero, t.Value(), x.Pos())
	return e.rt("sn_map_get", "ptr", value{"ptr", p}, value{"ptr", e.mapType(t, x.Pos())}, value{"ptr", e.key(x.Index, t.Key())}, value{"ptr", zero}).s
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
	name := "sn_mapslot_" + lower.Mangle(t)
	if e.state[name] {
		return name
	}

	e.state[name] = true
	kt := e.irType(t.Key(), pos)
	e.helper(name, "ptr", []string{"ptr %m", kt + " %k"}, func() {
		k := e.spill(value{kt, "%k"})
		mt := value{"ptr", e.mapType(t, pos)}
		added := e.slot("ptr")
		slot := e.rt("sn_map_slot", "ptr", value{"ptr", "%m"}, mt, value{"ptr", k}, value{"ptr", added})
		isNew := e.op("icmp ne ptr "+e.load("ptr", added).s+", null", "i1")
		new, have := e.block("new"), e.block("have")
		e.branch(isNew.s, new, have)
		e.label(have)
		e.dropIfNeeded(k, t.Key(), pos) // the entry keeps the key it has
		e.inst("ret " + slot.typed())
		e.fn.terminated = true
		e.label(new)
		e.inst("ret " + slot.typed())
		e.fn.terminated = true
	})
	return name
}

// mapLentSlotter returns the name of the function giving the address
// of the value for a key the caller keeps, at the address k, writing it
// on first use: a new entry takes a copy of the key.
func (e *Emitter) mapLentSlotter(t *types.Map, pos token.Pos) string {
	name := "sn_mapslot_lent_" + lower.Mangle(t)
	if e.state[name] {
		return name
	}

	e.state[name] = true
	e.helper(name, "ptr", []string{"ptr %m", "ptr %k"}, func() {
		mt := value{"ptr", e.mapType(t, pos)}
		added := e.slot("ptr")
		slot := e.rt("sn_map_slot", "ptr", value{"ptr", "%m"}, mt, value{"ptr", "%k"}, value{"ptr", added})
		key := e.load("ptr", added)
		isNew := e.op("icmp ne ptr "+key.s+", null", "i1")
		new, have := e.block("new"), e.block("have")
		e.branch(isNew.s, new, have)
		e.label(have)
		e.inst("ret " + slot.typed())
		e.fn.terminated = true
		e.label(new)
		e.store(e.cloneAt("%k", t.Key(), pos), key.s) // the entry keeps a key of its own
		e.inst("ret " + slot.typed())
		e.fn.terminated = true
	})
	return name
}

// mapGetOrInsert returns the name of the function giving the address of
// the value for a key, holding v when the key was absent, written on
// first use: one hash and one probe, the slot the table finds or makes;
// a key already there keeps its value, and k and v are dropped.
func (e *Emitter) mapGetOrInsert(t *types.Map, pos token.Pos) string {
	name := "sn_mapgetins_" + lower.Mangle(t)
	if e.state[name] {
		return name
	}

	e.state[name] = true
	kt, vt := e.irType(t.Key(), pos), e.irType(t.Value(), pos)
	e.helper(name, "ptr", []string{"ptr %m", kt + " %k", vt + " %v"}, func() {
		k, v := e.spill(value{kt, "%k"}), e.spill(value{vt, "%v"})
		added := e.slot("ptr")
		slot := e.rt("sn_map_slot", "ptr", value{"ptr", "%m"}, value{"ptr", e.mapType(t, pos)}, value{"ptr", k}, value{"ptr", added})
		isNew := e.op("icmp ne ptr "+e.load("ptr", added).s+", null", "i1")
		new, have := e.block("new"), e.block("have")
		e.branch(isNew.s, new, have)
		e.label(have)
		e.dropIfNeeded(k, t.Key(), pos)
		e.dropIfNeeded(v, t.Value(), pos)
		e.inst("ret " + slot.typed())
		e.fn.terminated = true
		e.label(new)
		e.store(e.load(vt, v), slot.s)
		e.inst("ret " + slot.typed())
		e.fn.terminated = true
	})
	return name
}

// mapSet writes the entry for the key k in the map at the address p,
// with v as its value; an entry already there drops its old value.
func (e *Emitter) mapSet(p string, t *types.Map, k, v value, pos token.Pos) {
	name := "sn_mapset_" + lower.Mangle(t)
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
	name := "sn_mt_" + lower.Mangle(t)
	if e.state[name] {
		return "@" + name
	}

	e.state[name] = true
	hash, eq := "sn_khash_"+lower.Mangle(t.Key()), "sn_keq_"+lower.Mangle(t.Key())
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

	put(&e.consts, "@", name, " = internal constant %sn_map_type { i64 ", e.sizeOf(t.Key()).s, ", i64 ", e.sizeOf(t.Value()).s, ", ptr @", hash, ", ptr @", eq, ", ptr ", kdrop, ", ptr ", vdrop, " }\n")
	return "@" + name
}

// eachEntry writes a loop over the live entries of the map at the
// address m, in insertion order, calling body with the addresses of
// each key and value.
func (e *Emitter) eachEntry(m string, t *types.Map, pos token.Pos, body func(k, v string)) {
	e.declare("sn_map_next", "declare i64 @sn_map_next(ptr, i64)")
	i := e.slot("i64")
	e.store(e.op("call i64 @sn_map_next(ptr "+m+", i64 0)", "i64"), i)
	cond, loop, done := e.block("entry.cond"), e.block("entry"), e.block("entry.done")
	e.jump(cond)
	e.label(cond)
	cur := e.load("i64", i)
	more := e.op("icmp sge i64 "+cur.s+", 0", "i1")
	e.branch(more.s, loop, done)
	e.label(loop)
	keys := e.load("ptr", e.gep("%sn_map", m, 0))
	vals := e.load("ptr", e.gep("%sn_map", m, 1))
	body(e.elem(e.irType(t.Key(), pos), keys.s, cur.s), e.elem(e.irType(t.Value(), pos), vals.s, cur.s))
	next := e.op("add i64 "+cur.s+", 1", "i64")
	e.store(e.op("call i64 @sn_map_next(ptr "+m+", i64 "+next.s+")", "i64"), i)
	e.jump(cond)
	e.label(done)
}

// eachElementIndexed writes a loop over the elements of the slice at
// the address s, calling body with the address and the index of each.
func (e *Emitter) eachElementIndexed(s string, elem types.Type, pos token.Pos, body func(p, i string)) {
	ptr := e.load("ptr", e.gep("%sn_slice", s, 0))
	n := e.load("i64", e.gep("%sn_slice", s, 1))
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
	e.rt("sn_slice_make", "void", value{"ptr", out}, n, c, e.sizeOf(elem))
}

// mapLit writes map[K]V{k: v}: an empty map, then one insertion per
// entry.
func (e *Emitter) mapLit(x *ast.CompositeLit, t types.Type, mt *types.Map) value {
	out := e.slot("%sn_map")
	e.store(value{"%sn_map", "zeroinitializer"}, out)
	for _, elt := range x.Elts {
		kv := elt.(*ast.KeyValueExpr)
		parts := e.operands([]ast.Expr{kv.Key, kv.Value}, []types.Type{mt.Key(), mt.Value()})
		e.mapSet(out, mt, parts[0], parts[1], x.Pos())
	}

	return e.load("%sn_map", out)
}

// sliceLit writes []T{a, b}: a slice made to size, then filled.
func (e *Emitter) sliceLit(x *ast.CompositeLit, t types.Type, st *types.Slice) value {
	if len(x.Elts) == 0 {
		return value{"%sn_slice", "zeroinitializer"}
	}

	wants := make([]types.Type, len(x.Elts))
	for i := range wants {
		wants[i] = st.Elem()
	}

	parts := e.operands(x.Elts, wants)
	n := value{"i64", strconv.Itoa(len(parts))}
	out := e.slot("%sn_slice")
	e.sliceMake(out, n, n, st.Elem(), x.Pos())
	base := e.load("ptr", e.gep("%sn_slice", out, 0))
	et := e.irType(st.Elem(), x.Pos())
	for i, p := range parts {
		e.store(p, e.elem(et, base.s, strconv.Itoa(i)))
	}

	return e.load("%sn_slice", out)
}

func (e *Emitter) stringMethod(x *ast.CallExpr, sel *ast.SelectorExpr) value {
	s := e.looked(sel.X, types.Typ[types.String])
	args := make([]value, len(x.Args))
	for i, a := range x.Args {
		if b, ok := lower.ValueType(e.typeOf(a)).Underlying().(*types.Basic); ok && b.Info()&types.IsInteger != 0 {
			args[i] = e.valueTo(a, types.Typ[types.Int]) // the count of Replace and SplitN
			continue
		}

		args[i] = e.looked(a, types.Typ[types.String])
	}

	switch sel.Sel.Name {
	case "Len":
		return e.extract(s, 1, "i64")
	case "ToUpper":
		return e.rt("sn_to_upper", "%sn_string", s)
	case "ToLower":
		return e.rt("sn_to_lower", "%sn_string", s)
	case "Trim":
		return e.rt("sn_trim", "%sn_string", s)
	case "HasPrefix":
		return e.rt("sn_has_prefix", "i1", s, args[0])
	case "HasSuffix":
		return e.rt("sn_has_suffix", "i1", s, args[0])
	case "Contains":
		return e.rt("sn_string_contains", "i1", s, args[0])
	case "Fields", "Lines", "Split", "Runes":
		out := e.slot("%sn_slice")
		name := map[string]string{"Fields": "sn_fields", "Lines": "sn_lines", "Split": "sn_split", "Runes": "sn_runes"}[sel.Sel.Name]
		e.rt(name, "void", append(append([]value{s}, args...), value{"ptr", out})...)
		return e.load("%sn_slice", out)
	case "TrimSpace":
		return e.rt("sn_trim", "%sn_string", s)
	case "TrimPrefix":
		return e.rt("sn_trim_prefix", "%sn_string", s, args[0])
	case "TrimSuffix":
		return e.rt("sn_trim_suffix", "%sn_string", s, args[0])
	case "Index":
		return e.someIndex(e.rt("sn_string_index", "i64", s, args[0]), x)
	case "LastIndex":
		return e.someIndex(e.rt("sn_string_last_index", "i64", s, args[0]), x)
	case "Count":
		return e.rt("sn_string_count", "i64", s, args[0])
	case "Replace":
		return e.rt("sn_string_replace", "%sn_string", s, args[0], args[1], args[2])
	case "ReplaceAll":
		return e.rt("sn_string_replace", "%sn_string", s, args[0], args[1], value{"i64", "9223372036854775807"})
	case "SplitN":
		out := e.slot("%sn_slice")
		e.rt("sn_split_n", "void", s, args[0], args[1], value{"ptr", out})
		return e.load("%sn_slice", out)
	case "SplitOnce":
		before, after := e.slot("%sn_string"), e.slot("%sn_string")
		has := e.rt("sn_split_once", "i1", s, args[0], value{"ptr", before}, value{"ptr", after})
		pair := e.irType(lower.ValueType(e.typeOf(x)).Underlying().(*types.Option).Elem(), x.Pos())
		tuple := e.insert(value{pair, "zeroinitializer"}, e.load("%sn_string", before), 0)
		tuple = e.insert(tuple, e.load("%sn_string", after), 1)
		return e.optionOf(e.typeOf(x), has, tuple, x.Pos())
	}

	e.unsupported(x.Pos(), "the method "+sel.Sel.Name+" on strings")
	return unit
}

// integerMethod writes the arithmetic every integer type has beside
// its operators, on recv, the integer type or one declared over it;
// false for any other method. The checked operations are LLVM's
// overflow intrinsics, one instruction and its flag; Abs and Clamp are
// LLVM's abs, min and max, which select with no branch.
func (e *Emitter) integerMethod(x *ast.CallExpr, sel *ast.SelectorExpr, recv types.Type, b *types.Basic) (value, bool) {
	name := sel.Sel.Name
	switch name {
	case "CheckedAdd", "CheckedSub", "CheckedMul", "Abs", "Clamp":
	default:
		return unit, false
	}

	ir := e.irType(recv, x.Pos())
	sign := "s"
	if b.Info()&types.IsUnsigned != 0 {
		sign = "u"
	}

	a := e.looked(sel.X, recv)
	intrinsic := func(op string, args ...value) value {
		fn := "llvm." + op + "." + ir
		var params, list []string
		for _, v := range args {
			params = append(params, ir)
			list = append(list, v.typed())
		}

		e.declare(fn, "declare "+ir+" @"+fn+"("+strings.Join(params, ", ")+")")
		return e.op("call "+ir+" @"+fn+"("+strings.Join(list, ", ")+")", ir)
	}

	switch name {
	case "Abs": // false: the minimum gives itself, as the arithmetic wraps, never poison
		fn := "llvm.abs." + ir
		e.declare(fn, "declare "+ir+" @"+fn+"("+ir+", i1)")
		return e.op("call "+ir+" @"+fn+"("+a.typed()+", i1 false)", ir), true
	case "Clamp":
		args := e.operands(x.Args, []types.Type{recv, recv})
		return intrinsic(sign+"min", intrinsic(sign+"max", a, args[0]), args[1]), true
	}

	op := map[string]string{"CheckedAdd": "add", "CheckedSub": "sub", "CheckedMul": "mul"}[name]
	fn := "llvm." + sign + op + ".with.overflow." + ir
	pair := "{ " + ir + ", i1 }"
	e.declare(fn, "declare "+pair+" @"+fn+"("+ir+", "+ir+")")
	o := e.operands(x.Args, []types.Type{recv})[0]
	r := e.op("call "+pair+" @"+fn+"("+a.typed()+", "+o.typed()+")", pair)
	over := e.extract(r, 1, "i1")
	has := e.op("xor i1 "+over.s+", true", "i1")
	return e.optionOf(e.typeOf(x), has, e.extract(r, 0, ir), x.Pos()), true
}

// byteMethod writes the character classes of a byte as the
// comparisons snello.h writes them, which are ASCII, as a byte is.
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
	case "AndThen", "OrElse", "Ok", "Err", "UnwrapOrElse":
		return e.chained(x, sel, t, held, has, val)
	}

	e.unsupported(x.Pos(), "the method "+sel.Sel.Name+" on "+t.String())
	return unit
}

// chained writes AndThen, OrElse, Ok, Err and UnwrapOrElse on the
// option or result whose flag is has and whose value, or error, is at
// val, of type t, which they take: the part each uses moves out of the
// receiver, into the function argument or the result, and the part it
// does not use is dropped. The function is called directly, with
// nothing allocated.
func (e *Emitter) chained(x *ast.CallExpr, sel *ast.SelectorExpr, t types.Type, held string, has value, val string) value {
	name := sel.Sel.Name
	heldType := e.typeOf(sel.X)
	var call func(args ...value) value
	if len(x.Args) > 0 {
		fsig := e.sub(e.typeOf(x.Args[0])).(*types.Signature)
		if inst, ok := e.info.Instances[sel.Sel]; ok {
			fsig = e.sub(inst.Type.Params()[0].Type()).(*types.Signature)
		}

		f := e.spill(e.valueTo(x.Args[0], fsig))
		pair := e.irType(fsig, x.Pos())
		fn := e.load("ptr", e.gep(pair, f, 0))
		box := e.gep(pair, f, 1)
		res := e.irType(fsig.Results(), x.Pos())
		call = func(args ...value) value {
			list := "ptr " + box
			for _, a := range args {
				list += ", " + a.typed()
			}

			return e.op("call "+res+" "+fn.s+"("+list+")", res)
		}
	}

	var valT, errT types.Type
	switch u := t.Underlying().(type) {
	case *types.Option:
		valT = u.Elem()
	case *types.Result:
		valT, errT = u.Val(), u.Err()
	}

	rt := e.typeOf(x)
	ir := e.irType(rt, x.Pos())
	out := e.slot(ir)
	e.zero(out, rt, x.Pos())
	some := func(v value) { // out holds v: an option made of it
		e.storeOption(out, lower.ValueType(rt).Underlying().(*types.Option), v, x.Pos())
	}

	drop := func(pt types.Type) { // the part not given back, moved out and dropped
		if needsDrop(pt) {
			v := e.spill(e.taken(val, pt, held, heldType, x.Pos()))
			e.dropOf(v, pt, x.Pos())
		}
	}

	yes, no, done := e.block("some"), e.block("none"), e.block("done")
	e.branch(has.s, yes, no)
	e.label(yes)
	switch name {
	case "AndThen":
		e.store(call(e.taken(val, valT, held, heldType, x.Pos())), out)
	case "OrElse", "Ok":
		some(e.taken(val, valT, held, heldType, x.Pos()))
	case "Err":
		drop(valT)
	case "UnwrapOrElse":
		e.store(e.taken(val, valT, held, heldType, x.Pos()), out)
	}

	e.jump(done)
	e.label(no)
	switch name {
	case "AndThen":
		if errT != nil {
			e.store(e.taken(val, errT, held, heldType, x.Pos()), e.gep(ir, out, 1)) // the error passed on; the flag stays false
		}
	case "OrElse":
		e.store(call(), out)
	case "Ok":
		drop(errT)
	case "Err":
		some(e.taken(val, errT, held, heldType, x.Pos()))
	case "UnwrapOrElse":
		e.store(call(e.taken(val, errT, held, heldType, x.Pos())), out)
	}

	e.jump(done)
	e.label(done)
	return e.load(ir, out)
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
