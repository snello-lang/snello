// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"slices"
	"strconv"
	"strings"

	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/token"
	"github.com/teak-lang/teak/types"
)

// Every value is freed where the checker said its owner dies. The
// emitter keeps a frame per scope it writes, holding the owned
// variables declared there that have something to release, and writes
// their drops, last declared first, where the scope ends: at the
// closing brace, before a return, break or continue, and at the end
// of a match arm. A move empties the place it moves from, so a place
// is dropped exactly once whatever path reaches the end of its scope:
// the drop of an emptied place does nothing. A value that no variable
// holds, such as a string built for a print, is a temporary of the
// statement that made it and is dropped when the statement ends. A
// type with a Drop method carries a flag saying it is live, set when
// a value is built and cleared when it moves, so the method runs once.

// A frame is a scope being written: the owned variables declared in
// it that need dropping, in order; whether it is a loop body, which a
// break or continue leaves; and how many statement temporaries were
// pending when it opened.
type frame struct {
	vars []owned
	loop bool
	mark int
}

// An owned value the emitter will drop: its C text and its Teak type.
type owned struct {
	name string
	typ  types.Type
}

// needsDrop reports whether a value of type t releases something when
// it is dropped: a string that may own its bytes, a slice, map or
// owned pointer, a file, a type with a Drop method, or a type holding
// one of these. Borrows, numbers, function values and channel handles
// do not.
func needsDrop(t types.Type) bool {
	switch t := t.(type) {
	case *types.Basic:
		return isString(t)
	case *types.Named:
		if t == types.OSFile || t.Implements("Drop") {
			return true
		}
	case *types.Pointer, *types.Slice, *types.Map:
		return true
	}

	return anyPart(t, needsDrop)
}

// guarded reports whether dropping a value of type t runs a Drop
// method, its own or one of a part, which a panic must still run: such
// a value is registered with the runtime while it is owned. The answer
// for a declared type is kept, as a type may hold a pointer to itself.
func (e *Emitter) guarded(t types.Type) bool {
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

// anyPart reports whether f holds for a part of a value of type t: the
// underlying type of a declared type, what a pointer, slice or map
// holds, or the elements and fields of a composite.
func anyPart(t types.Type, f func(types.Type) bool) bool {
	switch t := t.(type) {
	case *types.Named:
		return f(t.Underlying())
	case *types.Pointer:
		return f(t.Elem())
	case *types.Slice:
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
	return ok && n != types.OSFile && n.Implements("Drop")
}

// dropOf writes the statement that drops the value at the C place of
// type t: the runtime's own drop for a string or a file, a generated
// function for anything else.
func (e *Emitter) dropOf(place string, t types.Type, pos token.Pos) string {
	switch {
	case isString(t):
		return "tk_string_drop(&" + place + ");"
	case t == types.OSFile:
		return "tk_file_drop(&" + place + ");"
	}

	return e.dropper(t, pos) + "(&" + place + ");"
}

// dropper returns the name of the function dropping a value of type t
// through a pointer, writing it on first use: a Drop method first,
// when the value is live, then each part that owns something, and the
// memory a pointer, slice or map holds.
func (e *Emitter) dropper(t types.Type, pos token.Pos) string {
	name := "tk_drop_" + mangle(t)
	if e.state[name] != undeclared {
		return name
	}

	e.state[name] = defined
	head := "static void " + name + "(" + e.cType(t, pos) + " *v)"
	put(&e.protos, head, ";\n")
	var b strings.Builder
	put(&b, head, " {\n")
	if hasDrop(t) {
		put(&b, "\tif (!v->tk_live) {\n\t\treturn; // moved away, or never built\n\t}\n\n\tv->tk_live = false;\n\t", methodName(t, "Drop"), "(v);\n")
	}

	switch u := t.Underlying().(type) {
	case *types.Basic:
		b.WriteString("\ttk_string_drop(v);\n")
	case *types.Struct:
		if t == types.OSFile {
			b.WriteString("\ttk_file_drop(v);\n")
			break
		}

		for i := 0; i < u.NumFields(); i++ {
			f := u.Field(i)
			if needsDrop(f.Type()) {
				put(&b, "\t", e.dropOf("v->"+ident(f.Name()), f.Type(), pos), "\n")
			}
		}
	case *types.Enum:
		b.WriteString("\tswitch (v->tag) {\n")
		for i := 0; i < u.NumVariants(); i++ {
			variant := u.Variant(i)
			put(&b, "\tcase ", strconv.Itoa(i), ":\n")
			for j, f := range variant.Fields() {
				if needsDrop(f.Type()) {
					put(&b, "\t\t", e.dropOf("v->"+variantMember(variant)+"."+fieldName(f, j), f.Type(), pos), "\n")
				}
			}

			b.WriteString("\t\tbreak;\n")
		}

		b.WriteString("\t}\n")
	case *types.Option:
		put(&b, "\tif (v->has) {\n\t\t", e.dropOf("v->val", u.Elem(), pos), "\n\t}\n")
	case *types.Result:
		put(&b, "\tif (v->ok) {\n\t\t", e.dropIfNeeded("v->val", u.Val(), pos), "\n\t} else {\n\t\t", e.dropIfNeeded("v->err", u.Err(), pos), "\n\t}\n")
	case *types.Tuple:
		for i := 0; i < u.Len(); i++ {
			if needsDrop(u.At(i)) {
				put(&b, "\t", e.dropOf("v->e"+strconv.Itoa(i), u.At(i), pos), "\n")
			}
		}
	case *types.Pointer:
		put(&b, "\tif (*v != NULL) {\n\t\t", e.dropIfNeeded("TK_DEREF(*v)", u.Elem(), pos), "\n\t\ttk_release((void **)v);\n\t}\n")
	case *types.Slice:
		if needsDrop(u.Elem()) {
			put(&b, "\tfor (int64_t i = 0; i < v->len; i++) {\n\t\t", e.dropOf("v->ptr[i]", u.Elem(), pos), "\n\t}\n\n")
		}

		b.WriteString("\ttk_slice_free((tk_slice *)v);\n")
	case *types.Map:
		put(&b, "\ttk_map_free((tk_map *)v, ", e.mapType(u, pos), ");\n")
	default:
		e.unsupported(pos, "dropping values of type "+t.String())
	}

	b.WriteString("}\n\n")
	e.helpers.WriteString(b.String())
	return name
}

// dropIfNeeded is dropOf, or nothing for a type that owns nothing.
func (e *Emitter) dropIfNeeded(place string, t types.Type, pos token.Pos) string {
	if !needsDrop(t) {
		return "(void)0;"
	}

	return e.dropOf(place, t, pos)
}

// zero writes the zero value of type t as a C value, what a place
// holds once its value moved away.
func (e *Emitter) zero(t types.Type, pos token.Pos) string {
	return "(" + e.cType(t, pos) + ")" + zeroValue
}

// push opens a frame for a scope; loop says a break or continue leaves
// it.
func (e *Emitter) push(loop bool) {
	e.frames = append(e.frames, frame{loop: loop, mark: len(e.temps)})
}

// pop closes the innermost frame, writing the drops of its variables.
func (e *Emitter) pop(pos token.Pos) {
	n := len(e.frames) - 1
	e.dropVars(e.frames[n].vars, pos)
	e.frames = e.frames[:n]
}

// own records a variable just declared in the innermost frame, when
// its type has something to release and it owns its value.
func (e *Emitter) own(name string, t types.Type) {
	if _, borrow := t.(*types.Ref); borrow || !needsDrop(t) || len(e.frames) == 0 {
		return
	}

	f := &e.frames[len(e.frames)-1]
	f.vars = append(f.vars, owned{name, t})
	e.guard(name, t)
}

// hold records a temporary of the statement being written, dropped
// when the statement ends.
func (e *Emitter) hold(name string, t types.Type) {
	if needsDrop(t) {
		e.temps = append(e.temps, owned{name, t})
		e.guard(name, t)
	}
}

// guard registers an owned value with the runtime when its drop runs
// a Drop method, so that a panic runs it too.
func (e *Emitter) guard(name string, t types.Type) {
	if e.guarded(t) {
		e.line("tk_guard(&" + name + ", " + e.dropFn(t, token.NoPos) + ");")
	}
}

func (e *Emitter) dropVars(vars []owned, pos token.Pos) {
	for _, v := range slices.Backward(vars) {
		e.line(e.dropOf(v.name, v.typ, pos))
		if e.guarded(v.typ) {
			e.line("tk_unguard(&" + v.name + ");")
		}
	}
}

// dropTemps writes the drops of the temporaries recorded since mark
// and forgets them.
func (e *Emitter) dropTemps(mark int, pos token.Pos) {
	e.dropVars(e.temps[mark:], pos)
	e.temps = e.temps[:mark]
}

// leave writes the drops a jump out of the frames from index from
// runs: the temporaries pending since those frames opened, then their
// variables, innermost first. The frames stay open for the code after
// the jump.
func (e *Emitter) leave(from int, pos token.Pos) {
	if from >= len(e.frames) {
		return
	}

	e.dropVars(e.temps[e.frames[from].mark:], pos)
	for _, f := range slices.Backward(e.frames[from:]) {
		e.dropVars(f.vars, pos)
	}
}

// loopFrame is the index of the innermost loop body frame, which a
// break or continue leaves.
func (e *Emitter) loopFrame() int {
	for i, f := range slices.Backward(e.frames) {
		if f.loop {
			return i
		}
	}

	return len(e.frames)
}

// scoped writes what f writes and drops the temporaries it made at
// once, for code inside a C block the statement's own drops cannot
// reach.
func (e *Emitter) scoped(pos token.Pos, f func()) {
	mark := len(e.temps)
	f()
	e.dropTemps(mark, pos)
}

// boolean writes a condition into a bool temporary when evaluating it
// makes temporaries, which are then dropped before the condition is
// used, so that a test inside a block owns nothing.
func (e *Emitter) boolean(x ast.Expr) string {
	mark := len(e.temps)
	s := e.expr(x)
	if len(e.temps) == mark {
		return s
	}

	v := e.temp()
	e.line("bool " + v + " = " + s + ";")
	e.dropTemps(mark, x.Pos())
	return v
}

// moved writes a read of the place x that moves its value away: the
// value goes into a temporary and the place is emptied, so its drop
// later does nothing.
func (e *Emitter) moved(x ast.Expr) string {
	t := e.info.Types[x]
	place := e.rawPlace(x)
	tmp := e.temp()
	e.line(e.cType(t, x.Pos()) + " " + tmp + " = " + place + ";")
	e.line(place + " = " + e.zero(t, x.Pos()) + ";")
	return tmp
}

// rawPlace writes a place expression as a C lvalue, without the move
// that expr may make of it.
func (e *Emitter) rawPlace(x ast.Expr) string {
	switch x := x.(type) {
	case *ast.Ident:
		return e.ident(x)
	case *ast.ParenExpr:
		return "(" + e.rawPlace(x.X) + ")"
	case *ast.SelectorExpr:
		return e.selector(x)
	case *ast.IndexExpr:
		return e.index(x, false)
	case *ast.UnaryExpr:
		return e.unary(x)
	}

	return e.expr(x)
}

// isRead reports whether x reads a place someone else owns, so that
// its value is lent to the expression around it, never a temporary to
// drop.
func (e *Emitter) isRead(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.Ident:
		_, ok := e.info.Uses[x].(*types.Var)
		return ok
	case *ast.ParenExpr:
		return e.isRead(x.X)
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

// discard writes an expression as a statement: its value of type t is
// dropped when it owns something, else cast to void, as the C
// compiler warns about an unused value.
func (e *Emitter) discard(v string, t types.Type, pos token.Pos) {
	if !needsDrop(t) {
		e.line("(void)(" + v + ");")
		return
	}

	tmp := e.temp()
	e.line(e.cType(t, pos) + " " + tmp + " = " + v + ";")
	e.line(e.dropOf(tmp, t, pos))
}

// cloner returns the name of the function copying a value of type t
// part by part, writing it on first use, for a slice or map whose
// elements own something: a bitwise copy would be dropped twice.
func (e *Emitter) cloner(t types.Type, pos token.Pos) string {
	name := "tk_clone_" + mangle(t)
	if e.state[name] != undeclared {
		return name
	}

	e.state[name] = defined
	ct := e.cType(t, pos)
	head := "static " + ct + " " + name + "(" + ct + " v)"
	put(&e.protos, head, ";\n")
	var b strings.Builder
	put(&b, head, " {\n\t", ct, " out = ", zeroValue, ";\n")
	switch u := t.Underlying().(type) {
	case *types.Basic:
		b.WriteString("\tout = tk_string_clone(v);\n")
	case *types.Struct:
		for i := 0; i < u.NumFields(); i++ {
			f := ident(u.Field(i).Name())
			put(&b, "\tout.", f, " = ", e.cloneOf("v."+f, u.Field(i).Type(), pos), ";\n")
		}
	case *types.Option:
		put(&b, "\tout.has = v.has;\n\tif (v.has) {\n\t\tout.val = ", e.cloneOf("v.val", u.Elem(), pos), ";\n\t}\n")
	case *types.Tuple:
		for i := 0; i < u.Len(); i++ {
			f := "e" + strconv.Itoa(i)
			put(&b, "\tout.", f, " = ", e.cloneOf("v."+f, u.At(i), pos), ";\n")
		}
	case *types.Pointer:
		put(&b, "\tif (v != NULL) {\n\t\tout = tk_alloc(sizeof *out);\n\t\t*out = ", e.cloneOf("(*v)", u.Elem(), pos), ";\n\t}\n")
	case *types.Slice:
		b.WriteString("\ttk_slice_make((tk_slice *)&out, v.len, v.len, sizeof *v.ptr);\n")
		put(&b, "\tfor (int64_t i = 0; i < v.len; i++) {\n\t\tout.ptr[i] = ", e.cloneOf("v.ptr[i]", u.Elem(), pos), ";\n\t}\n")
	case *types.Map:
		k, val := "(("+e.cType(u.Key(), pos)+" *)v.keys)[i]", "(("+e.cType(u.Value(), pos)+" *)v.vals)[i]"
		b.WriteString("\tfor (int64_t i = tk_map_next((tk_map *)&v, 0); i >= 0; i = tk_map_next((tk_map *)&v, i + 1)) {\n")
		put(&b, "\t\t", e.mapSetter(u, pos), "(&out, ", e.cloneOf(k, u.Key(), pos), ", ", e.cloneOf(val, u.Value(), pos), ");\n\t}\n")
	default:
		e.unsupported(pos, "cloning values of type "+t.String())
	}

	b.WriteString("\treturn out;\n}\n\n")
	e.helpers.WriteString(b.String())
	return name
}

// cloneOf writes the copy of the C value v of type t: itself when it
// owns nothing.
func (e *Emitter) cloneOf(v string, t types.Type, pos token.Pos) string {
	switch {
	case !needsDrop(t):
		return v
	case isString(t):
		return "tk_string_clone(" + v + ")"
	}

	return e.cloner(t, pos) + "(" + v + ")"
}

// mapSlotter returns the name of the function giving the value slot
// for a key in a map of type t, writing it on first use: the key is
// taken, kept by a new entry and dropped when an entry already has it.
func (e *Emitter) mapSlotter(t *types.Map, pos token.Pos) string {
	name := "tk_mapslot_" + mangle(t)
	if e.state[name] != undeclared {
		return name
	}

	e.state[name] = defined
	mt, k, v := e.mapType(t, pos), e.cType(t.Key(), pos), e.cType(t.Value(), pos)
	head := "static " + v + " *" + name + "(" + e.cType(t, pos) + " *m, " + k + " k)"
	put(&e.protos, head, ";\n")
	var b strings.Builder
	put(&b, head, " {\n\t", v, " *found = tk_map_find((tk_map *)m, ", mt, ", &k);\n\tif (found != NULL) {\n")
	put(&b, "\t\t", e.dropIfNeeded("k", t.Key(), pos), "\n\t\treturn found;\n\t}\n\n")
	put(&b, "\treturn tk_map_slot((tk_map *)m, ", mt, ", &k);\n}\n\n")
	e.helpers.WriteString(b.String())
	return name
}

// mapSetter returns the name of the function storing a key and value
// in a map of type t, writing it on first use: an entry already there
// takes the new value and drops the old one.
func (e *Emitter) mapSetter(t *types.Map, pos token.Pos) string {
	name := "tk_mapset_" + mangle(t)
	if e.state[name] != undeclared {
		return name
	}

	e.state[name] = defined
	k, v := e.cType(t.Key(), pos), e.cType(t.Value(), pos)
	head := "static void " + name + "(" + e.cType(t, pos) + " *m, " + k + " k, " + v + " v)"
	put(&e.protos, head, ";\n")
	put(&e.helpers, head, " {\n\t", v, " *slot = ", e.mapSlotter(t, pos), "(m, k);\n\t", e.dropIfNeeded("(*slot)", t.Value(), pos), "\n\t*slot = v;\n}\n\n")
	return name
}

// dropFn returns the name of a function dropping a value through a
// void pointer, for the map descriptor, or NULL when the type owns
// nothing.
func (e *Emitter) dropFn(t types.Type, pos token.Pos) string {
	if !needsDrop(t) {
		return "NULL"
	}

	name := "tk_dropfn_" + mangle(t)
	if e.state[name] == undeclared {
		e.state[name] = defined
		ct := e.cType(t, pos)
		put(&e.helpers, "static void ", name, "(void *p) {\n\t", e.dropOf("(*("+ct+" *)p)", t, pos), "\n}\n\n")
	}

	return name
}
