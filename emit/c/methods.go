// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"strconv"

	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/token"
	"github.com/teak-lang/teak/types"
)

// The predeclared methods of slices, strings, options and results, and
// the built-ins that work on them, are lowered here onto the runtime:
// the stage 1 library table of _docs/design/05-standard-library.md.
// An operation that produces a new slice writes it into a temporary
// through the runtime, since C cannot convert between the slice types
// the emitter declares.

// methodCall writes a call of a predeclared method on x.X.
func (e *Emitter) methodCall(x *ast.CallExpr, sel *ast.SelectorExpr) string {
	recv := valueType(e.info.Types[sel.X])
	name := sel.Sel.Name
	switch t := recv.Underlying().(type) {
	case *types.Slice:
		return e.sliceMethod(x, sel, t)
	case *types.Basic:
		if t.Kind() == types.String {
			return e.stringMethod(x, sel)
		}
	case *types.Option:
		return e.optionMethod(x, sel, "has", "None")
	case *types.Result:
		return e.optionMethod(x, sel, "ok", "Err")
	}

	e.unsupported(x.Pos(), "the method "+name+" on "+recv.String())
	return "0"
}

func (e *Emitter) sliceMethod(x *ast.CallExpr, sel *ast.SelectorExpr, t *types.Slice) string {
	place, _ := e.scrutinee(sel.X)
	generic := "(tk_slice *)&" + place
	elem := e.cType(t.Elem(), x.Pos())
	size := "sizeof(" + elem + ")"
	switch sel.Sel.Name {
	case "Push":
		v := e.saved(x.Args[0], t.Elem())
		e.line("tk_slice_push(" + generic + ", &" + v + ", " + size + ");")
		return ""
	case "Pop":
		out := e.temp()
		e.line(e.cType(e.info.Types[x], x.Pos()) + " " + out + ";")
		e.line(out + ".has = tk_slice_pop(" + generic + ", &" + out + ".val, " + size + ");")
		return out
	case "Insert":
		args := e.operands(x.Args, []types.Type{types.Typ[types.Int], t.Elem()})
		v := e.temp()
		e.line(elem + " " + v + " = " + args[1] + ";")
		e.line("tk_slice_insert(" + generic + ", " + args[0] + ", &" + v + ", " + size + ");")
		return ""
	case "Remove":
		out := e.temp()
		e.line(elem + " " + out + ";")
		e.line("tk_slice_remove(" + generic + ", " + e.valueTo(x.Args[0], types.Typ[types.Int]) + ", &" + out + ", " + size + ");")
		return out
	case "RemoveValue", "Contains":
		return e.search(sel.Sel.Name, place, e.valueTo(x.Args[0], t.Elem()), t, x.Pos())
	case "Clear":
		e.line(place + ".len = 0;")
		return ""
	case "Reverse":
		e.line("tk_slice_reverse(" + generic + ", " + size + ");")
		return ""
	case "Clone":
		return e.into(t, x.Pos(), func(out string) string {
			return "tk_slice_clone(" + generic + ", " + out + ", " + size + ")"
		})
	}

	e.unsupported(x.Pos(), "the method "+sel.Sel.Name+" on slices")
	return "0"
}

// search writes RemoveValue or Contains through a generated function
// that walks the slice comparing elements with the element's equality.
func (e *Emitter) search(what, place, v string, t *types.Slice, pos token.Pos) string {
	name := "tk_" + what + "_" + mangle(t.Elem())
	if e.state[name] == undeclared {
		e.state[name] = defined
		st, elem := e.cType(t, pos), e.cType(t.Elem(), pos)
		head := "static bool " + name + "(" + st + " *s, " + elem + " v)"
		e.protos.WriteString(head + ";\n")
		e.helpers.WriteString(head + " {\n\tfor (int64_t i = 0; i < s->len; i++) {\n\t\tif (" + e.equal("s->ptr[i]", "v", t.Elem(), pos) + ") {\n")
		if what == "RemoveValue" {
			e.helpers.WriteString("\t\t\ttk_slice_remove((tk_slice *)s, i, &v, sizeof v);\n")
		}

		e.helpers.WriteString("\t\t\treturn true;\n\t\t}\n\t}\n\n\treturn false;\n}\n\n")
	}

	return name + "(&" + place + ", " + v + ")"
}

func (e *Emitter) stringMethod(x *ast.CallExpr, sel *ast.SelectorExpr) string {
	s := e.valueTo(sel.X, types.Typ[types.String])
	args := e.operands(x.Args, nil)
	switch sel.Sel.Name {
	case "Len":
		return s + ".len"
	case "Clone":
		return "tk_string_clone(" + s + ")"
	case "ToUpper":
		return "tk_to_upper(" + s + ")"
	case "ToLower":
		return "tk_to_lower(" + s + ")"
	case "Trim":
		return "tk_trim(" + s + ")"
	case "HasPrefix":
		return "tk_has_prefix(" + s + ", " + args[0] + ")"
	case "HasSuffix":
		return "tk_has_suffix(" + s + ", " + args[0] + ")"
	case "Contains":
		return "tk_string_contains(" + s + ", " + args[0] + ")"
	case "Fields":
		return e.into(e.info.Types[x], x.Pos(), func(out string) string { return "tk_fields(" + s + ", " + out + ")" })
	case "Split":
		return e.into(e.info.Types[x], x.Pos(), func(out string) string { return "tk_split(" + s + ", " + args[0] + ", " + out + ")" })
	case "SplitOnce":
		out := e.temp()
		e.line(e.cType(e.info.Types[x], x.Pos()) + " " + out + ";")
		e.line(out + ".has = tk_split_once(" + s + ", " + args[0] + ", &" + out + ".val.e0, &" + out + ".val.e1);")
		return out
	}

	e.unsupported(x.Pos(), "the method "+sel.Sel.Name+" on strings")
	return "0"
}

// optionMethod writes the methods options and results share: flag is
// the field that says a value is present and absent the name of the
// other case for the panic message.
func (e *Emitter) optionMethod(x *ast.CallExpr, sel *ast.SelectorExpr, flag, absent string) string {
	place, t := e.scrutinee(sel.X)
	switch sel.Sel.Name {
	case "IsSome", "IsOk":
		return place + "." + flag
	case "IsNone", "IsErr":
		return "(!" + place + "." + flag + ")"
	case "Expect", "Unwrap":
		msg := literal(sel.Sel.Name + " on " + absent)
		if sel.Sel.Name == "Expect" {
			msg = e.valueTo(x.Args[0], types.Typ[types.String])
		}

		if r, ok := t.Underlying().(*types.Result); ok {
			msg = concat(concat(msg, literal(": ")), e.toText(place+".err", r.Err(), x.Pos(), false))
		}

		e.line("if (!" + place + "." + flag + ") {")
		e.line("tk_panic(" + msg + ");")
		e.line("}")
		return place + ".val"
	case "UnwrapOr":
		v := e.saved(x.Args[0], e.info.Types[x])
		return "(" + place + "." + flag + " ? " + place + ".val : " + v + ")"
	}

	e.unsupported(x.Pos(), "the method "+sel.Sel.Name+" on "+t.String())
	return "0"
}

// into writes an operation whose result the runtime stores through a
// slice pointer into a temporary of the Teak type t, and returns the
// temporary. call writes the operation given the pointer.
func (e *Emitter) into(t types.Type, pos token.Pos, call func(out string) string) string {
	out := e.temp()
	e.line(e.cType(t, pos) + " " + out + ";")
	e.line(call("(tk_slice *)&"+out) + ";")
	return out
}

// saved writes x into a temporary of type t and returns its name, for
// a value the runtime takes by address.
func (e *Emitter) saved(x ast.Expr, t types.Type) string {
	v := e.temp()
	e.line(e.cType(t, x.Pos()) + " " + v + " = " + e.valueTo(x, t) + ";")
	return v
}

// index writes x[i] on a slice or a string, with the index checked.
func (e *Emitter) index(x *ast.IndexExpr) string {
	place, t := e.scrutinee(x.X)
	var i string
	if hasEffects(x.Index) { // the place may be written twice, as by take or +=
		i = e.saved(x.Index, e.info.Types[x.Index])
	} else {
		i = e.expr(x.Index)
	}

	if _, ok := t.Underlying().(*types.Slice); ok || isString(t) {
		return place + ".ptr[tk_index((int64_t)(" + i + "), " + place + ".len)]"
	}

	e.unsupported(x.Pos(), "indexing a value of type "+t.String())
	return "0"
}

// sliceExpr writes x[lo:hi], a view of part of a slice or string.
func (e *Emitter) sliceExpr(x *ast.SliceExpr) string {
	place, t := e.scrutinee(x.X)
	lo, hi := "0", place+".len"
	if x.Low != nil {
		lo = "(int64_t)(" + e.expr(x.Low) + ")"
	}

	if x.High != nil {
		hi = "(int64_t)(" + e.expr(x.High) + ")"
	}

	if isString(t) {
		return "tk_substr(" + place + ", " + lo + ", " + hi + ")"
	}

	if st, ok := t.Underlying().(*types.Slice); ok {
		size := "sizeof(" + e.cType(st.Elem(), x.Pos()) + ")"
		return e.into(t, x.Pos(), func(out string) string {
			return "tk_slice_sub((tk_slice *)&" + place + ", " + lo + ", " + hi + ", " + out + ", " + size + ")"
		})
	}

	e.unsupported(x.Pos(), "slicing a value of type "+t.String())
	return "0"
}

// makeCall writes make([]T, n) and make([]T, n, cap).
func (e *Emitter) makeCall(x *ast.CallExpr) string {
	t := e.info.Types[x]
	st, ok := t.Underlying().(*types.Slice)
	if !ok {
		e.unsupported(x.Pos(), "make of "+t.String())
		return "0"
	}

	sizes := e.operands(x.Args[1:], nil)
	n, c := "0", ""
	if len(sizes) > 0 {
		n = "(int64_t)(" + sizes[0] + ")"
		c = n
	}

	if len(sizes) > 1 {
		c = "(int64_t)(" + sizes[1] + ")"
	}

	size := "sizeof(" + e.cType(st.Elem(), x.Pos()) + ")"
	return e.into(t, x.Pos(), func(out string) string { return "tk_slice_make(" + out + ", " + n + ", " + c + ", " + size + ")" })
}

// sliceLit writes []T{a, b}: a slice made to size, then filled.
func (e *Emitter) sliceLit(x *ast.CompositeLit, t types.Type, st *types.Slice) string {
	if len(x.Elts) == 0 {
		return "(" + e.cType(t, x.Pos()) + "){0}"
	}

	wants := make([]types.Type, len(x.Elts))
	for i := range wants {
		wants[i] = st.Elem()
	}

	parts := e.operands(x.Elts, wants)
	n, size := strconv.Itoa(len(parts)), "sizeof("+e.cType(st.Elem(), x.Pos())+")"
	out := e.into(t, x.Pos(), func(out string) string { return "tk_slice_make(" + out + ", " + n + ", " + n + ", " + size + ")" })
	for i, p := range parts {
		e.line(out + ".ptr[" + strconv.Itoa(i) + "] = " + p + ";")
	}

	return out
}

// takeCall writes take(mut place): the value, with the zero value left
// behind.
func (e *Emitter) takeCall(x *ast.CallExpr) string {
	place := e.place(x.Args[0].(*ast.UnaryExpr).X)
	t := e.info.Types[x]
	out := e.temp()
	e.line(e.cType(t, x.Pos()) + " " + out + " = " + place + ";")
	e.line(place + " = (" + e.cType(t, x.Pos()) + ")" + e.zeroValue(t) + ";")
	return out
}

// lenCall writes len(x) and cap(x) on a slice or a string.
func (e *Emitter) lenCall(x *ast.CallExpr, b *types.Builtin) string {
	place, t := e.scrutinee(x.Args[0])
	if _, ok := t.Underlying().(*types.Slice); ok || isString(t) && b.ID() == types.BuiltinLen {
		return place + "." + b.Name()
	}

	e.unsupported(x.Pos(), b.Name()+" on "+t.String())
	return "0"
}
