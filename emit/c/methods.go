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
// the library table of _docs/design/05-standard-library.md.
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
	case *types.Map:
		return e.mapMethod(x, sel, t)
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

// mutating names the predeclared methods that change their receiver.
var mutating = map[string]bool{
	"Push": true, "Pop": true, "Insert": true, "Remove": true, "RemoveValue": true,
	"Clear": true, "Reverse": true, "Sort": true, "SortBy": true, "GetMut": true,
}

func (e *Emitter) sliceMethod(x *ast.CallExpr, sel *ast.SelectorExpr, t *types.Slice) string {
	place, _ := e.receiver(sel)
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
		if needsDrop(t.Elem()) {
			return e.cloner(t, x.Pos()) + "(" + place + ")" // element by element, so that each copy owns its own
		}

		return e.into(t, x.Pos(), func(out string) string {
			return "tk_slice_clone(" + generic + ", " + out + ", " + size + ")"
		})
	case "SortBy":
		less := e.saved(x.Args[0], e.info.Types[x.Args[0]])
		e.line("tk_slice_sort(" + generic + ", " + size + ", " + e.lessAdapter(t.Elem(), x.Pos()) + ", &" + less + ");")
		return ""
	case "Sort":
		e.line("tk_slice_sort(" + generic + ", " + size + ", " + e.ordering(t.Elem(), x.Pos()) + ", NULL);")
		return ""
	}

	e.unsupported(x.Pos(), "the method "+sel.Sel.Name+" on slices")
	return "0"
}

// lessAdapter returns the name of the function that lets the runtime
// sort call a Teak less function held in the context, written once per
// element type.
func (e *Emitter) lessAdapter(elem types.Type, pos token.Pos) string {
	name := "tk_lessfn_" + mangle(elem)
	if e.state[name] == undeclared {
		e.state[name] = defined
		ct := e.cType(elem, pos)
		less := types.NewSignature(nil, []*types.Var{types.NewVar(token.NoPos, "a", types.NewRef(elem, false)), types.NewVar(token.NoPos, "b", types.NewRef(elem, false))}, types.Typ[types.Bool])
		fn := e.cType(less, pos)
		put(&e.helpers, "static bool ", name, "(void *ctx, const void *a, const void *b) {\n\t", fn, " *f = ctx;\n\treturn f->fn(f->env, *(const ", ct, " *)a, *(const ", ct, " *)b);\n}\n\n")
	}

	return name
}

// ordering returns the name of the function that compares two
// elements with <, for Sort on numbers and strings.
func (e *Emitter) ordering(elem types.Type, pos token.Pos) string {
	name := "tk_lt_" + mangle(elem)
	b, ok := valueType(elem).Underlying().(*types.Basic)
	if !ok || b.Kind() == types.Bool {
		e.unsupported(pos, "Sort on a slice of "+elem.String()+"; use SortBy")
		return "NULL"
	}

	if e.state[name] == undeclared {
		e.state[name] = defined
		ct := e.cType(elem, pos)
		test := "*(const " + ct + " *)a < *(const " + ct + " *)b"
		if isString(elem) {
			test = "tk_string_cmp(*(const " + ct + " *)a, *(const " + ct + " *)b) < 0"
		}

		put(&e.helpers, "static bool ", name, "(void *ctx, const void *a, const void *b) {\n\t(void)ctx;\n\treturn ", test, ";\n}\n\n")
	}

	return name
}

// search writes RemoveValue or Contains through a generated function
// that walks the slice comparing elements with the element's equality.
func (e *Emitter) search(what, place, v string, t *types.Slice, pos token.Pos) string {
	name := "tk_" + what + "_" + mangle(t.Elem())
	if e.state[name] == undeclared {
		e.state[name] = defined
		st, elem := e.cType(t, pos), e.cType(t.Elem(), pos)
		head := "static bool " + name + "(" + st + " *s, " + elem + " v)"
		put(&e.protos, head, ";\n")
		put(&e.helpers, head, " {\n\tfor (int64_t i = 0; i < s->len; i++) {\n\t\tif (", e.equal("s->ptr[i]", "v", t.Elem(), pos), ") {\n")
		if what == "RemoveValue" {
			e.helpers.WriteString("\t\t\ttk_slice_remove((tk_slice *)s, i, &v, sizeof v);\n")
		}

		e.helpers.WriteString("\t\t\treturn true;\n\t\t}\n\t}\n\n\treturn false;\n}\n\n")
	}

	return name + "(&" + place + ", " + v + ")"
}

// receiver writes the place a predeclared method is called on, made
// when the method changes it and it is a new map element.
func (e *Emitter) receiver(sel *ast.SelectorExpr) (string, types.Type) {
	if mutating[sel.Sel.Name] {
		return e.changed(sel.X)
	}

	return e.scrutinee(sel.X)
}

func (e *Emitter) mapMethod(x *ast.CallExpr, sel *ast.SelectorExpr, t *types.Map) string {
	place, _ := e.receiver(sel)
	mt := e.mapType(t, x.Pos())
	val := e.cType(t.Value(), x.Pos())
	switch sel.Sel.Name {
	case "Get", "GetMut":
		out, found := e.temp(), e.temp()
		e.line(e.cType(e.info.Types[x], x.Pos()) + " " + out + " = {0};")
		e.line(val + " *" + found + " = tk_map_find(&" + place + ", " + mt + ", &" + e.key(x.Args[0], t.Key()) + ");")
		e.line("if (" + found + " != NULL) {")
		e.line(out + ".has = true;")
		if sel.Sel.Name == "Get" {
			e.line(out + ".val = *" + found + ";")
		} else {
			e.line(out + ".val = " + found + ";")
		}

		e.line("}")
		return out
	case "Insert":
		args := e.operands(x.Args, []types.Type{t.Key(), t.Value()})
		e.mapStore(place, t, args[0], args[1], x.Pos())
		return ""
	case "Remove":
		out := e.temp()
		e.line(e.cType(e.info.Types[x], x.Pos()) + " " + out + ";")
		e.line(out + ".has = tk_map_remove(&" + place + ", " + mt + ", &" + e.key(x.Args[0], t.Key()) + ", &" + out + ".val);")
		return out
	case "Contains":
		return "(tk_map_find(&" + place + ", " + mt + ", &" + e.key(x.Args[0], t.Key()) + ") != NULL)"
	case "Clone":
		if needsDrop(t.Key()) || needsDrop(t.Value()) {
			return e.cloner(t, x.Pos()) + "(" + place + ")"
		}

		out := e.temp()
		e.line(e.cType(t, x.Pos()) + " " + out + ";")
		e.line("tk_map_clone(&" + place + ", " + mt + ", &" + out + ");")
		return out
	}

	e.unsupported(x.Pos(), "the method "+sel.Sel.Name+" on maps")
	return "0"
}

// mapIndex writes m[k]: when written to, the entry's value, made with
// the zero value when the key is new, which then owns the key; when
// read, the value or the zero value, with the map unchanged.
func (e *Emitter) mapIndex(x *ast.IndexExpr, place string, t *types.Map, write bool) string {
	val := e.cType(t.Value(), x.Pos())
	if write {
		slot := e.temp()
		e.line(val + " *" + slot + " = " + e.mapSlotter(t, x.Pos()) + "(&" + place + ", " + e.valueTo(x.Index, t.Key()) + ");")
		return "(*" + slot + ")"
	}

	mt := e.mapType(t, x.Pos())
	return "(*(const " + val + " *)tk_map_get(&" + place + ", " + mt + ", &" + e.key(x.Index, t.Key()) + ", &(" + val + ")" + zeroValue + "))"
}

// key writes a map key the operation only looks up, in a temporary
// the runtime takes by address; a key built here is held for the
// statement and dropped with it.
func (e *Emitter) key(x ast.Expr, t types.Type) string {
	v := e.saved(x, t)
	if e.lent(x, nil) {
		e.hold(v, t)
	}

	return v
}

// looked writes x, of type t, where the operation only looks at its
// value, as a ref parameter would: a value built here is held for the
// statement and dropped with it.
func (e *Emitter) looked(x ast.Expr, t types.Type) string {
	return e.operands([]ast.Expr{x}, []types.Type{types.NewRef(t, false)})[0]
}

// mapStore writes the entry for key k in the map at place, with v as
// its value; an entry already there drops its old value.
func (e *Emitter) mapStore(place string, t *types.Map, k, v string, pos token.Pos) {
	e.line(e.mapSetter(t, pos) + "(&" + place + ", " + k + ", " + v + ");")
}

// mapLit writes map[K]V{k: v}: an empty map, then one insertion per
// entry.
func (e *Emitter) mapLit(x *ast.CompositeLit, t types.Type, mt *types.Map) string {
	out := e.temp()
	e.line(e.cType(t, x.Pos()) + " " + out + " = {0};")
	for _, elt := range x.Elts {
		kv := elt.(*ast.KeyValueExpr)
		parts := e.operands([]ast.Expr{kv.Key, kv.Value}, []types.Type{mt.Key(), mt.Value()})
		e.mapStore(out, mt, parts[0], parts[1], x.Pos())
	}

	return out
}

func (e *Emitter) stringMethod(x *ast.CallExpr, sel *ast.SelectorExpr) string {
	s := e.looked(sel.X, types.Typ[types.String])
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
	place, t, held := e.consumed(sel.X)
	if held != "" {
		e.hold(held, e.info.Types[sel.X])
	}

	switch sel.Sel.Name {
	case "IsSome", "IsOk":
		return place + "." + flag
	case "IsNone", "IsErr":
		return "(!" + place + "." + flag + ")"
	case "Expect", "Unwrap":
		msg := text{literal(sel.Sel.Name + " on " + absent), false}
		if sel.Sel.Name == "Expect" {
			msg = text{e.looked(x.Args[0], types.Typ[types.String]), false}
		}

		if r, ok := t.Underlying().(*types.Result); ok {
			s, owned := e.toText(place+".err", r.Err(), x.Pos(), false)
			msg = join(join(msg, text{literal(": "), false}), text{s, owned})
		}

		e.line("if (!" + place + "." + flag + ") {")
		e.line("tk_panic(" + msg.s + ");")
		e.line("}")
		return e.taken(place+".val", e.info.Types[x], held, e.info.Types[sel.X], x.Pos())
	case "UnwrapOr":
		v := e.saved(x.Args[0], e.info.Types[x])
		out := e.temp()
		e.line(e.cType(e.info.Types[x], x.Pos()) + " " + out + " = " + v + ";")
		e.line("if (" + place + "." + flag + ") {")
		e.line(e.dropIfNeeded(v, e.info.Types[x], x.Pos()))
		e.line(out + " = " + e.taken(place+".val", e.info.Types[x], held, e.info.Types[sel.X], x.Pos()) + ";")
		e.line("}")
		return out
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

// index writes x[i] on a slice or a string, with the index checked, or
// on a map; write says whether the element is about to be assigned.
func (e *Emitter) index(x *ast.IndexExpr, write bool) string {
	place, t := e.scrutinee(x.X)
	if m, ok := t.Underlying().(*types.Map); ok {
		return e.mapIndex(x, place, m, write)
	}

	var i string
	if e.hasEffects(x.Index) { // the place may be written twice, as by take or +=
		i = e.saved(x.Index, types.Typ[types.Int])
	} else {
		i = e.valueTo(x.Index, types.Typ[types.Int]) // through any borrow of the index
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
		lo = "(int64_t)(" + e.valueTo(x.Low, types.Typ[types.Int]) + ")"
	}

	if x.High != nil {
		hi = "(int64_t)(" + e.valueTo(x.High, types.Typ[types.Int]) + ")"
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

// makeCall writes make([]T, n), make([]T, n, cap) and make(map[K]V).
func (e *Emitter) makeCall(x *ast.CallExpr) string {
	t := e.info.Types[x]
	if _, ok := t.Underlying().(*types.Map); ok {
		return "(" + e.cType(t, x.Pos()) + "){0}"
	}

	st, ok := t.Underlying().(*types.Slice)
	if !ok {
		e.unsupported(x.Pos(), "make of "+t.String())
		return "0"
	}

	sizes := e.operands(x.Args[1:], []types.Type{types.Typ[types.Int], types.Typ[types.Int]}[:len(x.Args)-1])
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
	target := x.Args[0].(*ast.UnaryExpr).X
	place := deref(e.lvalue(target), e.info.Types[target])
	t := e.info.Types[x]
	out := e.temp()
	e.line(e.cType(t, x.Pos()) + " " + out + " = " + place + ";")
	e.line(place + " = (" + e.cType(t, x.Pos()) + ")" + zeroValue + ";")
	return out
}

// lenCall writes len(x) and cap(x) on a slice, a string or a map.
func (e *Emitter) lenCall(x *ast.CallExpr, b *types.Builtin) string {
	place, t := e.scrutinee(x.Args[0])
	if _, ok := t.Underlying().(*types.Map); ok && b.ID() == types.BuiltinLen {
		return place + ".count"
	}

	if _, ok := t.Underlying().(*types.Slice); ok || isString(t) && b.ID() == types.BuiltinLen {
		return place + "." + b.Name()
	}

	e.unsupported(x.Pos(), b.Name()+" on "+t.String())
	return "0"
}
