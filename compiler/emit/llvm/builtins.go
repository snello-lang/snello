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

// The predeclared functions, len, cap, make, take, panic, min, max,
// close and print, and the element access they sit beside: indexing
// with its check, slicing, and the conversions between a string and
// its bytes.

// index writes x[i] on a slice, a string or an array, with the index
// checked, or on a map, and returns the element's address; write says
// whether the element is about to be assigned.
func (e *Emitter) index(x *ast.IndexExpr, write bool) string {
	p, t := e.scrutinee(x.X)
	if m, ok := t.Underlying().(*types.Map); ok {
		return e.mapIndex(x, p, m, write)
	}

	i := e.valueTo(x.Index, types.Typ[types.Int])
	switch u := t.Underlying().(type) {
	case *types.Slice:
		base := e.load("ptr", e.gep("%sn_slice", p, 0))
		n := e.load("i64", e.gep("%sn_slice", p, 1))
		return e.elem(e.irType(u.Elem(), x.Pos()), base.s, e.checkIndex(i, n).s)
	case *types.Basic:
		if isString(u) {
			base := e.load("ptr", e.gep("%sn_string", p, 0))
			n := e.load("i64", e.gep("%sn_string", p, 1))
			return e.elem("i8", base.s, e.checkIndex(i, n).s)
		}
	case *types.Array:
		return e.elem(e.irType(u.Elem(), x.Pos()), p, e.checkIndex(i, value{"i64", strconv.FormatInt(u.Len(), 10)}).s)
	}

	e.unsupported(x.Pos(), "indexing a value of type "+t.String())
	return "null"
}

// checkIndex writes the check of an index against a length, as
// sn_index does: an index at or past the length, or negative, which is
// past any length as an unsigned number, ends the program.
func (e *Emitter) checkIndex(i, n value) value {
	e.declare("sn_bounds", "declare void @sn_bounds(i64, i64)")
	ok, bad := e.block("index"), e.block("index.bad")
	inRange := e.op("icmp ult "+i.typed()+", "+n.s, "i1")
	e.branch(inRange.s, ok, bad)
	e.label(bad)
	e.inst("call void @sn_bounds(" + i.typed() + ", " + n.typed() + ")")
	e.inst("unreachable")
	e.fn.terminated = true
	e.label(ok)
	return i
}

// sliceExpr writes x[lo:hi], a view of part of a slice or string.
func (e *Emitter) sliceExpr(x *ast.SliceExpr) value {
	p, t := e.scrutinee(x.X)
	lo := value{"i64", "0"}
	if x.Low != nil {
		lo = e.valueTo(x.Low, types.Typ[types.Int])
	}

	hi := e.load("i64", e.gep("%sn_slice", p, 1)) // a string's length is at the same place
	if x.High != nil {
		hi = e.valueTo(x.High, types.Typ[types.Int])
	}

	if isString(t) {
		return e.rt("sn_substr", "%sn_string", e.load("%sn_string", p), lo, hi)
	}

	if st, ok := t.Underlying().(*types.Slice); ok {
		out := e.slot("%sn_slice")
		e.rt("sn_slice_sub", "void", value{"ptr", p}, lo, hi, value{"ptr", out}, e.sizeOf(st.Elem()))
		return e.load("%sn_slice", out)
	}

	e.unsupported(x.Pos(), "slicing a value of type "+t.String())
	return unit
}

// stringToBytes writes []byte(s): a copy of the bytes the string
// holds, as the runtime's inline conversion makes one; no bytes are an
// empty slice, not a view of static data.
func (e *Emitter) stringToBytes(s value) value {
	out := e.slot("%sn_slice")
	e.store(value{"%sn_slice", "zeroinitializer"}, out)
	n := e.extract(s, 1, "i64")
	empty := e.op("icmp eq "+n.typed()+", 0", "i1")
	copy, done := e.block("copy"), e.block("done")
	e.branch(empty.s, done, copy)
	e.label(copy)
	e.store(e.rt("sn_string_clone", "%sn_string", s), out) // the same three words, read back as a slice
	e.jump(done)
	e.label(done)
	return e.load("%sn_slice", out)
}

// bytesToString writes string(b): a copy of the bytes as an owned
// string.
func (e *Emitter) bytesToString(b value) value {
	view := e.insert(value{"%sn_string", "zeroinitializer"}, e.extract(b, 0, "ptr"), 0)
	view = e.insert(view, e.extract(b, 1, "i64"), 1)
	return e.rt("sn_string_clone", "%sn_string", view)
}

// builtinCall writes a call of a predeclared function.
func (e *Emitter) builtinCall(x *ast.CallExpr, b *types.Builtin) value {
	switch b.ID() {
	case types.BuiltinLen, types.BuiltinCap:
		return e.lenCall(x, b)
	case types.BuiltinMake:
		return e.makeCall(x)
	case types.BuiltinTake:
		target := x.Args[0].(*ast.UnaryExpr).X
		p := e.placeAt(e.lvalue(target), e.typeOf(target))
		t := e.typeOf(x)
		v := e.load(e.irType(t, x.Pos()), p)
		e.zero(p, t, x.Pos())
		return v
	case types.BuiltinPanic:
		v := e.operands(x.Args, nil)[0]
		e.panic(e.built(e.textOf(v, e.typeOf(x.Args[0]), x.Pos(), false)))
		return unit
	case types.BuiltinMin, types.BuiltinMax:
		return e.minCall(x, b)
	case types.BuiltinClose:
		e.rt("sn_chan_close", "void", e.chanOperand(x.Args[0]))
		return unit
	case types.BuiltinPrint:
		parts := e.operands(x.Args, nil)
		for i, a := range x.Args {
			s := e.textOf(parts[i], e.typeOf(a), a.Pos(), false)
			if s.owned {
				e.callWords("sn_eprint_owned", s.v)
			} else {
				e.callWords("sn_eprint_string", s.v)
			}
		}

		return unit
	}

	e.unsupported(x.Pos(), b.Name()+" on this type")
	return unit
}

// minCall writes min(a, b, ...) and max(a, b, ...) as selects over the
// arguments.
func (e *Emitter) minCall(x *ast.CallExpr, b *types.Builtin) value {
	t := lower.ValueType(e.typeOf(x))
	parts := e.operands(x.Args, nil)
	for i, a := range x.Args {
		parts[i] = e.derefTo(parts[i], e.typeOf(a), t) // read through a borrow of a number
	}

	if isFloat(t) { // NaN wins, and -0 is below +0, as Go's specification has min and max on floats
		fn := "sn_f64_" + b.Name()
		out := parts[0]
		for _, p := range parts[1:] {
			a, q := out, p
			if out.typ == "float" {
				a, q = e.op("fpext "+a.typed()+" to double", "double"), e.op("fpext "+q.typed()+" to double", "double")
			}

			r := e.rt(fn, "double", a, q)
			if out.typ == "float" {
				r = e.op("fptrunc "+r.typed()+" to float", "float")
			}

			out = r
		}

		return out
	}

	op := token.LSS
	if b.ID() == types.BuiltinMax {
		op = token.GTR
	}

	name, _ := compare(op, isFloat(t), isUnsigned(t))
	cmp := "icmp"
	if isFloat(t) {
		cmp = "fcmp"
	}

	out := parts[0]
	for _, p := range parts[1:] {
		better := e.op(cmp+" "+name+" "+p.typed()+", "+out.s, "i1")
		out = e.op("select i1 "+better.s+", "+p.typed()+", "+out.typed(), out.typ)
	}

	return out
}

// lenCall writes len(x) and cap(x) on a slice, a string, an array, a
// map or a channel.
func (e *Emitter) lenCall(x *ast.CallExpr, b *types.Builtin) value {
	p, t := e.scrutinee(x.Args[0])
	switch u := t.Underlying().(type) {
	case *types.Map:
		if b.ID() == types.BuiltinLen {
			return e.load("i64", e.gep("%sn_map", p, 5)) // count: the live entries
		}
	case *types.Slice:
		if b.ID() == types.BuiltinLen {
			return e.load("i64", e.gep("%sn_slice", p, 1))
		}

		return e.rt("sn_slice_cap", "i64", value{"ptr", p}) // a sub-slice's room is its length
	case *types.Basic:
		if isString(u) && b.ID() == types.BuiltinLen {
			return e.load("i64", e.gep("%sn_string", p, 1))
		}
	case *types.Array:
		return value{"i64", strconv.FormatInt(u.Len(), 10)} // len and cap alike
	case *types.Chan:
		return e.rt("sn_chan_"+b.Name(), "i64", e.load("ptr", p))
	}

	e.unsupported(x.Pos(), b.Name()+" on "+t.String())
	return value{"i64", "0"}
}

// makeCall writes make([]T, n), make([]T, n, cap), make(map[K]V) and
// make(chan T, n).
func (e *Emitter) makeCall(x *ast.CallExpr) value {
	t := e.typeOf(x)
	switch u := t.Underlying().(type) {
	case *types.Map:
		if len(x.Args) == 1 {
			return value{"%sn_map", "zeroinitializer"}
		}

		out := e.slot("%sn_map") // room for the size hint's entries, as Go makes it
		e.store(value{"%sn_map", "zeroinitializer"}, out)
		e.rt("sn_map_reserve", "void", value{"ptr", out}, value{"ptr", e.mapType(u, x.Pos())}, e.valueTo(x.Args[1], types.Typ[types.Int]))
		return e.load("%sn_map", out)
	case *types.Chan:
		n := value{"i64", "0"}
		if len(x.Args) > 1 {
			n = e.valueTo(x.Args[1], types.Typ[types.Int])
		}

		drop := value{"ptr", "null"} // what the last handle drops each value still in the channel with
		if needsDrop(u.Elem()) {
			drop.s = "@" + e.dropper(u.Elem(), x.Pos())
		}

		return e.rt("sn_chan_make", "ptr", e.sizeOf(u.Elem()), n, drop)
	case *types.Slice:
		wants := []types.Type{types.Typ[types.Int], types.Typ[types.Int]}[:len(x.Args)-1]
		sizes := e.operands(x.Args[1:], wants)
		n, c := value{"i64", "0"}, value{"i64", "0"}
		if len(sizes) > 0 {
			n, c = sizes[0], sizes[0]
		}

		if len(sizes) > 1 {
			c = sizes[1]
		}

		out := e.slot("%sn_slice")
		e.sliceMake(out, n, c, u.Elem(), x.Pos())
		return e.load("%sn_slice", out)
	}

	e.unsupported(x.Pos(), "make of "+t.String())
	return unit
}
