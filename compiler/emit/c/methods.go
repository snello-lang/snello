// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"strconv"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// The predeclared methods of slices, strings, options and results, and
// the built-ins that work on them, are lowered here onto the runtime:
// the library table of _docs/design/05-standard-library.md.
// An operation that produces a new slice writes it into a temporary
// through the runtime, since C cannot convert between the slice types
// the emitter declares.

// methodCall writes a call of a predeclared method on x.X.
func (e *Emitter) methodCall(x *ast.CallExpr, sel *ast.SelectorExpr) string {
	recv := lower.ValueType(e.typeOf(sel.X))
	name := sel.Sel.Name
	switch t := recv.Underlying().(type) {
	case *types.Slice:
		return e.sliceMethod(x, sel, t)
	case *types.Map:
		return e.mapMethod(x, sel, t)
	case *types.Chan:
		return e.standardMethod(x, sel, recv) // Clone, another handle
	case *types.Basic:
		if call := e.standardMethod(x, sel, recv); call != "" {
			return call
		}

		if t.Kind() == types.String {
			return e.stringMethod(x, sel)
		}

		if t.Kind() == types.Uint8 {
			return e.byteMethod(x, sel)
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
	"Clear": true, "Swap": true, "Reverse": true, "Sort": true, "SortBy": true, "GetMut": true,
}

func (e *Emitter) sliceMethod(x *ast.CallExpr, sel *ast.SelectorExpr, t *types.Slice) string {
	place, _ := e.receiver(sel)
	generic := "(sn_slice *)&" + place
	elem := e.cType(t.Elem(), x.Pos())
	size := "sizeof(" + elem + ")"
	switch sel.Sel.Name {
	case "Push": // with room, in place; growing, and a sub-slice's panic, the runtime's
		v := e.saved(x.Args[0], t.Elem())
		sp := e.temp()
		e.line(e.cType(t, x.Pos()) + " *" + sp + " = &" + place + ";")
		e.line("if (SN_PUSH_FAST(*" + sp + ")) {")
		e.line(sp + "->ptr[" + sp + "->len++] = " + v + ";")
		e.line("} else {")
		e.line("sn_slice_push((sn_slice *)" + sp + ", &" + v + ", " + size + ");")
		e.line("}")
		return ""
	case "Pop": // a slice of its own with an element, in place; the rest the runtime's
		out, sp := e.temp(), e.temp()
		e.line(e.cType(e.typeOf(x), x.Pos()) + " " + out + ";")
		e.line(e.cType(t, x.Pos()) + " *" + sp + " = &" + place + ";")
		e.line("if (SN_POP_FAST(*" + sp + ")) {")
		e.line(out + ".has = true;")
		e.line(out + ".val = " + sp + "->ptr[--" + sp + "->len];")
		e.line("} else {")
		e.line(out + ".has = sn_slice_pop((sn_slice *)" + sp + ", &" + out + ".val, " + size + ");")
		e.line("}")
		return out
	case "Insert":
		args := e.operands(x.Args, []types.Type{types.Typ[types.Int], t.Elem()})
		v := e.temp()
		e.line(elem + " " + v + " = " + args[1] + ";")
		e.line("sn_slice_insert(" + generic + ", " + args[0] + ", &" + v + ", " + size + ");")
		return ""
	case "Remove":
		out := e.temp()
		e.line(elem + " " + out + ";")
		e.line("sn_slice_remove(" + generic + ", " + e.valueTo(x.Args[0], types.Typ[types.Int]) + ", &" + out + ", " + size + ");")
		return out
	case "RemoveValue", "Contains":
		return e.search(sel.Sel.Name, place, e.looked(x.Args[0], t.Elem()), t, x.Pos()) // lent: a value built here is held and dropped with the statement
	case "Clear":
		e.line("sn_slice_own(" + generic + ", \"Clear\");")
		if needsDrop(t.Elem()) {
			e.line("for (int64_t i = 0; i < " + place + ".len; i++) {")
			e.line(e.dropOf(place+".ptr[i]", t.Elem(), x.Pos()))
			e.line("}")
		}

		e.line(place + ".len = 0;")
		return ""
	case "Swap": // both indexes checked, in order, then the two elements exchanged in place
		args := e.operands(x.Args, []types.Type{types.Typ[types.Int], types.Typ[types.Int]})
		a, b, tmp := e.temp(), e.temp(), e.temp()
		e.line("int64_t " + a + " = sn_index((int64_t)(" + args[0] + "), " + place + ".len);")
		e.line("int64_t " + b + " = sn_index((int64_t)(" + args[1] + "), " + place + ".len);")
		e.line(elem + " " + tmp + " = " + place + ".ptr[" + a + "];")
		e.line(place + ".ptr[" + a + "] = " + place + ".ptr[" + b + "];")
		e.line(place + ".ptr[" + b + "] = " + tmp + ";")
		return ""
	case "Reverse":
		e.line("sn_slice_reverse(" + generic + ", " + size + ");")
		return ""
	case "Clone":
		if needsDrop(t.Elem()) {
			return e.cloner(t, x.Pos()) + "(" + place + ")" // element by element, so that each copy owns its own
		}

		return e.into(t, x.Pos(), func(out string) string {
			return "sn_slice_clone(" + generic + ", " + out + ", " + size + ")"
		})
	case "SortBy":
		less := e.saved(x.Args[0], e.typeOf(x.Args[0]))
		e.line(e.sorter("sn_sortby_"+lower.Mangle(t.Elem()), e.lessAdapter(t.Elem(), x.Pos()), size) + "(" + generic + ", &" + less + ");")
		return ""
	case "Sort":
		if order := e.ordering(t.Elem(), x.Pos()); order != "NULL" {
			e.line(e.sorter("sn_sort_"+lower.Mangle(t.Elem()), order, size) + "(" + generic + ", NULL);")
		}

		return ""
	}

	e.unsupported(x.Pos(), "the method "+sel.Sel.Name+" on slices")
	return "0"
}

// lessAdapter returns the name of the function that lets the runtime
// sort call a Snello less function held in the context, written once per
// element type.
func (e *Emitter) lessAdapter(elem types.Type, pos token.Pos) string {
	name := "sn_lessfn_" + lower.Mangle(elem)
	if e.state[name] == undeclared {
		e.state[name] = defined
		ct := e.cType(elem, pos)
		less := types.NewSignature(nil, []*types.Var{types.NewVar(token.NoPos, "a", types.NewRef(elem, false)), types.NewVar(token.NoPos, "b", types.NewRef(elem, false))}, types.Typ[types.Bool])
		fn := e.cType(less, pos)
		put(&e.helpers, "static bool ", name, "(void *ctx, const void *a, const void *b) {\n\t", fn, " *f = ctx;\n\treturn f->fn(f->box, *(const ", ct, " *)a, *(const ", ct, " *)b);\n}\n\n")
	}

	return name
}

// sorter returns the name of the function that sorts a slice by less,
// one for each element type and order: the runtime's sort is inlined
// into it when the program is optimized with the runtime, and with the
// element's size and less constants there, clang compiles one sort for
// the type with the comparison inlined, as a generic sort is
// instantiated for each type.
func (e *Emitter) sorter(name, less, size string) string {
	if e.state[name] == undeclared {
		e.state[name] = defined
		head := "static void " + name + "(sn_slice *s, void *ctx)"
		put(&e.protos, head, ";\n")
		put(&e.helpers, head, " {\n\tsn_slice_sort(s, ", size, ", ", less, ", ctx);\n}\n\n")
	}

	return name
}

// ordering returns the name of the function that compares two
// elements with <, for Sort on numbers and strings, and on a type with a
// Cmp, written or derived, through it: a before b when a.Cmp(b) is
// Less, as sort.Sort orders.
func (e *Emitter) ordering(elem types.Type, pos token.Pos) string {
	name := "sn_lt_" + lower.Mangle(elem)
	n, cmp := lower.ValueType(elem).(*types.Named)
	cmp = cmp && n.Method("Cmp") != nil
	b, ok := lower.ValueType(elem).Underlying().(*types.Basic)
	if !cmp && (!ok || b.Kind() == types.Bool) {
		e.unsupported(pos, "Sort on a slice of "+elem.String()+"; use SortBy")
		return "NULL"
	}

	if e.state[name] == undeclared {
		e.state[name] = defined
		ct := e.cType(elem, pos)
		test := less("*("+ct+" const *)a", "*("+ct+" const *)b", elem)
		if cmp {
			test = e.compare("*("+ct+" const *)a", "*("+ct+" const *)b", elem, pos) + ".tag == 0" // a before b when a.Cmp(b) is Less
		} else if isString(elem) {
			test = "sn_string_cmp(*(const " + ct + " *)a, *(const " + ct + " *)b) < 0"
		}

		put(&e.helpers, "static bool ", name, "(void *ctx, const void *a, const void *b) {\n\t(void)ctx;\n\treturn ", test, ";\n}\n\n")
	}

	return name
}

// search writes RemoveValue or Contains through a generated function
// that walks the slice comparing elements with the element's equality.
func (e *Emitter) search(what, place, v string, t *types.Slice, pos token.Pos) string {
	name := "sn_" + what + "_" + lower.Mangle(t.Elem())
	if e.state[name] == undeclared {
		e.state[name] = defined
		st, elem := e.cType(t, pos), e.cType(t.Elem(), pos)
		head := "static bool " + name + "(" + st + " *s, " + elem + " v)"
		put(&e.protos, head, ";\n")
		put(&e.helpers, head, " {\n\tfor (int64_t i = 0; i < s->len; i++) {\n\t\tif (", e.equal("s->ptr[i]", "v", t.Elem(), pos), ") {\n")
		if what == "RemoveValue" { // the element leaves the slice and is dropped; v is only lent
			put(&e.helpers, "\t\t\t", elem, " out;\n\t\t\tsn_slice_remove((sn_slice *)s, i, &out, sizeof out);\n")
			if needsDrop(t.Elem()) {
				put(&e.helpers, "\t\t\t", e.dropOf("out", t.Elem(), pos), "\n")
			}
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
		e.line(e.cType(e.typeOf(x), x.Pos()) + " " + out + " = {0};")
		e.line(val + " *" + found + " = sn_map_find(&" + place + ", " + mt + ", &" + e.key(x.Args[0], t.Key()) + ");")
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
		e.line(e.cType(e.typeOf(x), x.Pos()) + " " + out + ";")
		e.line(out + ".has = sn_map_remove(&" + place + ", " + mt + ", &" + e.key(x.Args[0], t.Key()) + ", &" + out + ".val);")
		return out
	case "Contains":
		return "(sn_map_find(&" + place + ", " + mt + ", &" + e.key(x.Args[0], t.Key()) + ") != NULL)"
	case "Clone":
		if needsDrop(t.Key()) || needsDrop(t.Value()) {
			return e.cloner(t, x.Pos()) + "(" + place + ")"
		}

		out := e.temp()
		e.line(e.cType(t, x.Pos()) + " " + out + ";")
		e.line("sn_map_clone(&" + place + ", " + mt + ", &" + out + ");")
		return out
	}

	e.unsupported(x.Pos(), "the method "+sel.Sel.Name+" on maps")
	return "0"
}

// mapIndex writes m[k]: when written to, the entry's value, made with
// the zero value when the key is new; when read, the value or the zero
// value, with the map unchanged. A key the statement owns, built there
// or moved out of its variable, goes to the new entry; a key only read
// from its variable, as m[k] op= v reads it, is cloned for the new
// entry and stays its owner's.
func (e *Emitter) mapIndex(x *ast.IndexExpr, place string, t *types.Map, write bool) string {
	val := e.cType(t.Value(), x.Pos())
	if write {
		slot := e.temp()
		k := e.valueTo(x.Index, t.Key())
		if e.isRead(x.Index) && !e.info.Moves[x.Index] {
			e.line(val + " *" + slot + " = " + e.mapLentSlotter(t, x.Pos()) + "(&" + place + ", " + e.spill(k, t.Key(), x.Pos()) + ");")
		} else {
			e.line(val + " *" + slot + " = " + e.mapSlotter(t, x.Pos()) + "(&" + place + ", " + k + ");")
		}

		return "(*" + slot + ")"
	}

	mt := e.mapType(t, x.Pos())
	return "(*(const " + val + " *)sn_map_get(&" + place + ", " + mt + ", &" + e.key(x.Index, t.Key()) + ", &(" + val + ")" + zeroValue + "))"
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
	case "ToUpper":
		return "sn_to_upper(" + s + ")"
	case "ToLower":
		return "sn_to_lower(" + s + ")"
	case "Trim":
		return "sn_trim(" + s + ")"
	case "HasPrefix":
		return "sn_has_prefix(" + s + ", " + args[0] + ")"
	case "HasSuffix":
		return "sn_has_suffix(" + s + ", " + args[0] + ")"
	case "Contains":
		return "sn_string_contains(" + s + ", " + args[0] + ")"
	case "Fields":
		return e.into(e.typeOf(x), x.Pos(), func(out string) string { return "sn_fields(" + s + ", " + out + ")" })
	case "Lines":
		return e.into(e.typeOf(x), x.Pos(), func(out string) string { return "sn_lines(" + s + ", " + out + ")" })
	case "Runes":
		return e.into(e.typeOf(x), x.Pos(), func(out string) string { return "sn_runes(" + s + ", " + out + ")" })
	case "Split":
		return e.into(e.typeOf(x), x.Pos(), func(out string) string { return "sn_split(" + s + ", " + args[0] + ", " + out + ")" })
	case "SplitOnce":
		out := e.temp()
		e.line(e.cType(e.typeOf(x), x.Pos()) + " " + out + ";")
		e.line(out + ".has = sn_split_once(" + s + ", " + args[0] + ", &" + out + ".val.e0, &" + out + ".val.e1);")
		return out
	}

	e.unsupported(x.Pos(), "the method "+sel.Sel.Name+" on strings")
	return "0"
}

// byteMethod writes the character classes of a byte onto the runtime's
// tests, which are ASCII, as a byte is.
func (e *Emitter) byteMethod(x *ast.CallExpr, sel *ast.SelectorExpr) string {
	c := e.looked(sel.X, types.Typ[types.Uint8])
	switch sel.Sel.Name {
	case "IsDigit":
		return "sn_is_digit(" + c + ")"
	case "IsSpace":
		return "sn_is_space(" + c + ")"
	case "IsLetter":
		return "sn_is_letter(" + c + ")"
	case "IsUpper":
		return "sn_is_upper(" + c + ")"
	case "IsLower":
		return "sn_is_lower(" + c + ")"
	}

	e.unsupported(x.Pos(), "the method "+sel.Sel.Name+" on bytes")
	return "0"
}

// standardMethod writes the method of a standard trait on a number, a
// string or a bool, Eq, Cmp, Hash or Clone, from the comparison, hash
// and copy the backend writes for the type; "" for any other method.
func (e *Emitter) standardMethod(x *ast.CallExpr, sel *ast.SelectorExpr, recv types.Type) string {
	switch sel.Sel.Name {
	case "Eq", "Cmp":
		a, b := e.looked(sel.X, recv), e.operands(x.Args, nil)[0]
		if e.hasEffects(sel.X) || e.hasEffects(x.Args[0]) { // the comparison names each operand twice
			a, b = e.spill(a, recv, x.Pos()), e.spill(b, e.typeOf(x.Args[0]), x.Pos())
		}

		if sel.Sel.Name == "Eq" {
			return "(" + e.equal(a, b, recv, x.Pos()) + ")"
		}

		return e.compare(a, b, recv, x.Pos())
	case "Hash":
		return e.hash(e.looked(sel.X, recv), recv, x.Pos())
	case "Clone":
		return e.cloneOf(e.looked(sel.X, recv), recv, x.Pos())
	}

	return ""
}

// optionMethod writes the methods options and results share: flag is
// the field that says a value is present and absent the name of the
// other case for the panic message.
func (e *Emitter) optionMethod(x *ast.CallExpr, sel *ast.SelectorExpr, flag, absent string) string {
	place, t, held := e.consumed(sel.X)
	if held != "" {
		e.hold(held, e.typeOf(sel.X))
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
		e.line("sn_panic(" + msg.s + ");")
		e.line("}")
		return e.taken(place+".val", e.typeOf(x), held, e.typeOf(sel.X), x.Pos())
	case "UnwrapOr":
		v := e.saved(x.Args[0], e.typeOf(x))
		out := e.temp()
		e.line(e.cType(e.typeOf(x), x.Pos()) + " " + out + " = " + v + ";")
		e.line("if (" + place + "." + flag + ") {")
		e.line(e.dropIfNeeded(v, e.typeOf(x), x.Pos()))
		e.line(out + " = " + e.taken(place+".val", e.typeOf(x), held, e.typeOf(sel.X), x.Pos()) + ";")
		e.line("}")
		return out
	case "Map", "MapErr":
		return e.mapped(x, sel, place, t, held, flag)
	}

	e.unsupported(x.Pos(), "the method "+sel.Sel.Name+" on "+t.String())
	return "0"
}

// mapped writes Map and MapErr on the option or result at place, of
// type t: the function argument is called on the part it maps, moved
// out of the receiver, and the other part, when there is one, moves
// into the result as it is. The function's type comes from the instance
// the checker recorded for the call.
func (e *Emitter) mapped(x *ast.CallExpr, sel *ast.SelectorExpr, place string, t types.Type, held string, flag string) string {
	inst, ok := e.info.Instances[sel.Sel]
	if !ok {
		e.unsupported(x.Pos(), sel.Sel.Name+" without its instance")
		return "0"
	}

	fsig := e.sub(inst.Type.Params()[0].Type()).(*types.Signature)
	var f string
	if e.hasEffects(x.Args[0]) {
		f = e.saved(x.Args[0], fsig)
	} else {
		f = e.valueTo(x.Args[0], fsig)
	}

	rt := e.typeOf(x)
	out := e.temp()
	e.line(e.cType(rt, x.Pos()) + " " + out + " = {0};")

	// part writes the move of one part out of the receiver into the
	// result: through the function for the part it maps, as it is for
	// the other.
	mapsErr := sel.Sel.Name == "MapErr"
	heldType := e.typeOf(sel.X)
	part := func(field string, pt types.Type, through bool) {
		v := e.taken(place+"."+field, pt, held, heldType, x.Pos())
		if through {
			v = f + ".fn(" + f + ".box, " + v + ")"
		}

		e.line(out + "." + field + " = " + v + ";")
	}

	e.line("if (" + place + "." + flag + ") {")
	e.line(out + "." + flag + " = true;")
	part("val", valueOf(e.typeOf(sel.X)), !mapsErr)
	if _, isResult := t.Underlying().(*types.Result); isResult {
		e.line("} else {")
		part("err", errOf(e.typeOf(sel.X)), mapsErr)
	}

	e.line("}")
	return out
}

// valueOf returns the value type of an option or a result, errOf the
// error type of a result.
func valueOf(t types.Type) types.Type {
	switch u := t.Underlying().(type) {
	case *types.Option:
		return u.Elem()
	case *types.Result:
		return u.Val()
	}

	panic("valueOf: " + t.String() + " is neither an option nor a result")
}

func errOf(t types.Type) types.Type {
	return t.Underlying().(*types.Result).Err()
}

// into writes an operation whose result the runtime stores through a
// slice pointer into a temporary of the Snello type t, and returns the
// temporary. call writes the operation given the pointer.
func (e *Emitter) into(t types.Type, pos token.Pos, call func(out string) string) string {
	out := e.temp()
	e.line(e.cType(t, pos) + " " + out + ";")
	e.line(call("(sn_slice *)&"+out) + ";")
	return out
}

// saved writes x into a temporary of type t and returns its name, for
// a value the runtime takes by address.
// spill writes the C value v of type t into a temporary and returns its
// name, so that an expression naming it more than once evaluates it
// once.
func (e *Emitter) spill(v string, t types.Type, pos token.Pos) string {
	tmp := e.temp()
	e.line(e.cType(t, pos) + " " + tmp + " = " + v + ";")
	return tmp
}

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
		return place + ".ptr[sn_index((int64_t)(" + i + "), " + place + ".len)]"
	}

	if a, ok := t.Underlying().(*types.Array); ok {
		return place + ".at[sn_index((int64_t)(" + i + "), " + strconv.FormatInt(a.Len(), 10) + ")]"
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
		return "sn_substr(" + place + ", " + lo + ", " + hi + ")"
	}

	if st, ok := t.Underlying().(*types.Slice); ok {
		size := "sizeof(" + e.cType(st.Elem(), x.Pos()) + ")"
		return e.into(t, x.Pos(), func(out string) string {
			return "sn_slice_sub((sn_slice *)&" + place + ", " + lo + ", " + hi + ", " + out + ", " + size + ")"
		})
	}

	e.unsupported(x.Pos(), "slicing a value of type "+t.String())
	return "0"
}

// makeCall writes make([]T, n), make([]T, n, cap) and make(map[K]V).
func (e *Emitter) makeCall(x *ast.CallExpr) string {
	t := e.typeOf(x)
	if mt, ok := t.Underlying().(*types.Map); ok {
		out := e.temp()
		e.line(e.cType(t, x.Pos()) + " " + out + " = {0};")
		if len(x.Args) > 1 { // room for the size hint's entries, as Go makes it
			e.line("sn_map_reserve((sn_map *)&" + out + ", " + e.mapType(mt, x.Pos()) + ", " + e.valueTo(x.Args[1], types.Typ[types.Int]) + ");")
		}

		return out
	}

	if ch, ok := t.Underlying().(*types.Chan); ok {
		n := "0"
		if len(x.Args) > 1 {
			n = "(int64_t)(" + e.valueTo(x.Args[1], types.Typ[types.Int]) + ")"
		}

		return "sn_chan_make(sizeof(" + e.cType(ch.Elem(), x.Pos()) + "), " + n + ", " + e.elemDropper(ch.Elem(), x.Pos()) + ")"
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
	return e.into(t, x.Pos(), func(out string) string { return "sn_slice_make(" + out + ", " + n + ", " + c + ", " + size + ")" })
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
	out := e.into(t, x.Pos(), func(out string) string { return "sn_slice_make(" + out + ", " + n + ", " + n + ", " + size + ")" })
	for i, p := range parts {
		e.line(out + ".ptr[" + strconv.Itoa(i) + "] = " + p + ";")
	}

	return out
}

// takeCall writes take(mut place): the value, with the zero value left
// behind.
func (e *Emitter) takeCall(x *ast.CallExpr) string {
	target := x.Args[0].(*ast.UnaryExpr).X
	place := deref(e.lvalue(target), e.typeOf(target))
	t := e.typeOf(x)
	if e.hasEffects(target) { // the place is named twice below: a call in it runs once
		at := e.temp()
		e.line(e.cType(t, x.Pos()) + " *" + at + " = &" + place + ";")
		place = "(*" + at + ")"
	}

	out := e.temp()
	e.line(e.cType(t, x.Pos()) + " " + out + " = " + place + ";")
	e.line(place + " = (" + e.cType(t, x.Pos()) + ")" + zeroValue + ";")
	return out
}

// replaceCall writes replace(mut place, v): the value the place held,
// with v moved in. The place's address is taken first, once, since v
// may be a call that runs before the place is read.
func (e *Emitter) replaceCall(x *ast.CallExpr) string {
	target := x.Args[0].(*ast.UnaryExpr).X
	place := deref(e.lvalue(target), e.typeOf(target))
	t := e.typeOf(x)
	ct := e.cType(t, x.Pos())
	at := e.temp()
	e.line(ct + " *" + at + " = &" + place + ";")
	v := e.operands(x.Args[1:], []types.Type{t})[0]
	out := e.temp()
	e.line(ct + " " + out + " = *" + at + ";")
	e.line("*" + at + " = " + v + ";")
	return out
}

// lenCall writes len(x) and cap(x) on a slice, a string or a map.
func (e *Emitter) lenCall(x *ast.CallExpr, b *types.Builtin) string {
	place, t := e.scrutinee(x.Args[0])
	if _, ok := t.Underlying().(*types.Map); ok && b.ID() == types.BuiltinLen {
		return place + ".count"
	}

	if _, ok := t.Underlying().(*types.Slice); ok && b.ID() == types.BuiltinCap {
		return "sn_slice_cap((sn_slice *)&" + place + ")" // a sub-slice's room is its length
	}

	if _, ok := t.Underlying().(*types.Slice); ok || isString(t) && b.ID() == types.BuiltinLen {
		return place + "." + b.Name()
	}

	if a, ok := t.Underlying().(*types.Array); ok {
		return strconv.FormatInt(a.Len(), 10) // len and cap alike
	}

	if _, ok := t.Underlying().(*types.Chan); ok {
		return "sn_chan_" + b.Name() + "(" + place + ")"
	}

	e.unsupported(x.Pos(), b.Name()+" on "+t.String())
	return "0"
}
