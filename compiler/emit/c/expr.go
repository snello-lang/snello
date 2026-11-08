// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"math"
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// expr writes an expression and returns its C text. An expression that
// needs statements, such as a match or a ?, writes them first and
// returns the temporary holding its value; one that is a statement in
// C, such as a print, writes its statements and returns an empty
// string.
func (e *Emitter) expr(x ast.Expr) string {
	if v, ok := e.pre[x]; ok {
		return v // evaluated at a defer statement
	}

	if e.info.Moves[x] && needsDrop(e.typeOf(x)) {
		return e.moved(x) // the value leaves the place, which is emptied
	}

	switch x := x.(type) {
	case *ast.Ident:
		return e.ident(x)
	case *ast.BasicLit:
		return e.literal(x)
	case *ast.ParenExpr:
		return "(" + e.expr(x.X) + ")"
	case *ast.TupleExpr:
		return e.tuple(x)
	case *ast.SelectorExpr:
		return e.selector(x)
	case *ast.IndexExpr:
		return e.index(x, false)
	case *ast.SliceExpr:
		return e.sliceExpr(x)
	case *ast.CallExpr:
		return e.call(x)
	case *ast.UnaryExpr:
		return e.unary(x)
	case *ast.BinaryExpr:
		return e.binary(x)
	case *ast.CompositeLit:
		return e.compositeLit(x)
	case *ast.FuncLit:
		return e.funcLit(x)
	case *ast.CoalesceExpr:
		return e.coalesce(x)
	case *ast.TryExpr:
		return e.try(x)
	case *ast.IfExpr:
		return e.ifExpr(x)
	case *ast.MatchExpr:
		return e.matchExpr(x)
	default:
		e.unsupported(x.Pos(), "this kind of expression")
	}

	return "0"
}

// valueTo writes x where a value of type want is expected: with the
// dereferences the checker allowed silently, as when a mut borrow or
// an owned pointer is read as its value, or with the address taken
// when a place is passed where a mut borrow is wanted.
func (e *Emitter) valueTo(x ast.Expr, want types.Type) string {
	have := e.typeOf(x)
	if want == nil || have == nil {
		return e.expr(x)
	}

	if tr, ok := want.(*types.Trait); ok {
		if n, ok := have.(*types.Named); ok {
			return e.box(e.expr(x), n, tr, x.Pos()) // a value becoming a trait value
		}
	}

	if depth(have) < depth(want) {
		if !e.isPlace(x) { // a value built here and lent mut lives in a temporary for the statement
			tmp := e.spill(e.expr(x), have, x.Pos())
			if needsDrop(have) {
				e.hold(tmp, have)
			}

			return "&" + tmp
		}

		return "&" + e.lvalue(x)
	}

	return derefTo(e.expr(x), have, want)
}

// lvalue writes x as a place about to be written or lent mutably: as
// expr does, except that a map element is made when its key is new.
func (e *Emitter) lvalue(x ast.Expr) string {
	switch x := x.(type) {
	case *ast.ParenExpr:
		return "(" + e.lvalue(x.X) + ")"
	case *ast.IndexExpr:
		return e.index(x, true)
	case *ast.SelectorExpr:
		if f, ok := e.info.Uses[x.Sel].(*types.Var); ok && e.packageOf(x.X) == nil {
			base := e.typeOf(x.X)
			e.define(lower.ValueType(base), x.Pos()) // reached through a pointer, the struct may be declared and not yet defined
			if !e.isPlace(x.X) {
				return field(e.place(x.X), base, f) // a value built here: the field of the temporary holding it
			}

			return field(deref(e.lvalue(x.X), base), base, f)
		}
	}

	return e.expr(x)
}

// place writes x dereferenced to the value it denotes, for a field
// access or a match on it; a value built here is held in a temporary
// and dropped when the statement ends.
func (e *Emitter) place(x ast.Expr) string {
	if e.isPlace(x) {
		return deref(e.expr(x), e.typeOf(x))
	}

	place, _ := e.scrutinee(x)
	return place
}

func (e *Emitter) ident(x *ast.Ident) string {
	switch obj := e.info.Uses[x].(type) {
	case *types.Const:
		if obj.Pos() == token.NoPos {
			return obj.Name() // true, false: C has them with stdbool.h
		}

		return lower.ObjName(obj)
	case *types.Func:
		return e.funcValue(obj, x.Pos()) // a function named outside a call is a value
	case *types.Var:
		if s, ok := e.captured[obj]; ok {
			return s
		}

		if e.lazy[obj] && e.making != obj {
			return "(*" + lower.ObjName(obj) + "_get())" // made on first use; its own initializer names the cell
		}

		return e.localName(obj) // the variable's own name: super stands for the receiver
	case *types.Variant:
		return e.variantValue(e.typeOf(x), obj, nil, x.Pos())
	}

	return lower.Ident(x.Name)
}

// variantValue writes a value of an enum, option or Result type built
// by a variant: a compound literal with the tag and the payload.
func (e *Emitter) variantValue(t types.Type, v *types.Variant, args []string, pos token.Pos) string {
	ct := e.cType(t, pos)
	switch u := lower.ValueType(t).Underlying().(type) {
	case *types.Option:
		if v == types.SomeVariant {
			return "(" + ct + "){.has = true, .val = " + args[0] + "}"
		}

		return "(" + ct + "){.has = false}"
	case *types.Result:
		if v == types.OkVariant {
			return "(" + ct + "){.ok = true, .val = " + args[0] + "}"
		}

		return "(" + ct + "){.ok = false, .err = " + args[0] + "}"
	case *types.Enum:
		tag := ".tag = " + strconv.Itoa(v.Index())
		if hasDrop(lower.ValueType(t)) {
			tag += ", .sn_live = true"
		}

		if len(args) == 0 {
			return "(" + ct + "){" + tag + "}"
		}

		fields := make([]string, len(args))
		for i, f := range u.Variant(v.Index()).Fields() {
			fields[i] = "." + fieldName(f, i) + " = " + args[i]
		}

		return "(" + ct + "){" + tag + ", ." + variantMember(v) + " = {" + strings.Join(fields, ", ") + "}}"
	}

	e.unsupported(pos, "values of type "+t.String())
	return "0"
}

// selector writes a field access through any borrows and pointers in
// front of the struct. Package members and methods are only reached
// through calls.
func (e *Emitter) selector(x *ast.SelectorExpr) string {
	if pkg := e.packageOf(x.X); pkg != nil {
		switch obj := e.info.Uses[x.Sel].(type) { // a member of a package written in Snello
		case *types.Func:
			if lower.Key(obj) != "" {
				e.unsupported(x.Pos(), "a function the compiler provides, "+pkg.Name()+"."+x.Sel.Name+", as a value")
				return "0"
			}

			if lower.Located(obj) != "" {
				e.unsupported(x.Pos(), pkg.Name()+"."+x.Sel.Name+" as a value: it is called, so that a failure names its line")
				return "0"
			}

			return e.funcValue(obj, x.Pos())
		case *types.Variant:
			return e.variantValue(e.typeOf(x), obj, nil, x.Pos())
		case *types.Var:
			if e.lazy[obj] {
				return "(*" + lower.ObjName(obj) + "_get())"
			}

			if e.external[obj] {
				return "(*(" + e.cType(e.typeOf(x), x.Pos()) + " *)&" + lower.ObjName(obj) + ")"
			}
		}

		return lower.ObjName(e.info.Uses[x.Sel])
	}

	f, ok := e.info.Uses[x.Sel].(*types.Var)
	if !ok {
		e.unsupported(x.Pos(), "methods as values")
		return "0"
	}

	e.define(lower.ValueType(e.typeOf(x.X)), x.Pos()) // reached through a pointer, the struct may be declared and not yet defined
	return field(e.place(x.X), e.typeOf(x.X), f)
}

// tuple writes (a, b) as a struct literal and () as the unit value.
func (e *Emitter) tuple(x *ast.TupleExpr) string {
	if len(x.Elts) == 0 {
		return "SN_UNIT"
	}

	t := e.typeOf(x).(*types.Tuple)
	wants := make([]types.Type, t.Len())
	for i := range wants {
		wants[i] = t.At(i)
	}

	parts := e.operands(x.Elts, wants)
	for i := range parts {
		parts[i] = ".e" + strconv.Itoa(i) + " = " + parts[i]
	}

	return "(" + e.cType(t, x.Pos()) + "){" + strings.Join(parts, ", ") + "}"
}

// compositeLit writes a struct literal; fields left out are zero, as
// a C compound literal leaves them.
func (e *Emitter) compositeLit(x *ast.CompositeLit) string {
	t := e.typeOf(x)
	if st, ok := t.Underlying().(*types.Slice); ok {
		return e.sliceLit(x, t, st)
	}

	if mt, ok := t.Underlying().(*types.Map); ok {
		return e.mapLit(x, t, mt)
	}

	if at, ok := t.Underlying().(*types.Array); ok { // the elements given, the rest zero, as C fills an aggregate
		if len(x.Elts) == 0 {
			return "(" + e.cType(t, x.Pos()) + "){0}"
		}

		wants := make([]types.Type, len(x.Elts))
		for i := range wants {
			wants[i] = at.Elem()
		}

		return "(" + e.cType(t, x.Pos()) + "){.at = {" + strings.Join(e.operands(x.Elts, wants), ", ") + "}}"
	}

	if _, ok := t.Underlying().(*types.Struct); !ok {
		e.unsupported(x.Pos(), "literals of type "+t.String())
		return "0"
	}

	live := ""
	if hasDrop(t) {
		live = ".sn_live = true"
	}

	if len(x.Elts) == 0 {
		if live == "" {
			return "(" + e.cType(t, x.Pos()) + "){0}"
		}

		return "(" + e.cType(t, x.Pos()) + "){" + live + "}"
	}

	st := t.Underlying().(*types.Struct)
	values := make([]ast.Expr, len(x.Elts))
	wants := make([]types.Type, len(x.Elts))
	names := make([]string, len(x.Elts))
	for i, elt := range x.Elts {
		kv := elt.(*ast.KeyValueExpr)
		key := kv.Key.(*ast.Ident)
		values[i] = kv.Value
		wants[i] = fieldType(st, key.Name) // the literal's own type's field, with any type arguments in place
		names[i] = lower.Ident(key.Name)
	}

	parts := e.operands(values, wants)
	for i := range parts {
		parts[i] = "." + names[i] + " = " + parts[i]
	}

	if live != "" {
		parts = append(parts, live)
	}

	return "(" + e.cType(t, x.Pos()) + "){" + strings.Join(parts, ", ") + "}"
}

// literal writes a number in decimal, a character as its code, and a
// string as a view of a C string literal with its length computed
// here, so the runtime never counts bytes. An integer carries a suffix
// that makes it 64 bits, unsigned when its type is: C gives a bare
// literal the narrowest signed type that holds it, so 570 * 31556952
// would overflow an int where Snello computes in the literal's type, and
// a literal past the signed range would be refused.
func (e *Emitter) literal(x *ast.BasicLit) string {
	switch x.Kind {
	case token.CHAR:
		r, _, _, err := strconv.UnquoteChar(x.Value[1:len(x.Value)-1], '\'')
		if err != nil {
			e.unsupported(x.Pos(), "this character literal")
		}

		return strconv.Itoa(int(r))
	case token.STRING:
		s, err := strconv.Unquote(x.Value)
		if err != nil {
			e.unsupported(x.Pos(), "this string literal")
		}

		return literal(s)
	case token.INT:
		n, err := strconv.ParseUint(x.Value, 0, 64) // base 0 reads 0x, 0b, 0o and _
		if err != nil {
			e.unsupported(x.Pos(), "this integer literal")
			return "0"
		}

		digits := strconv.FormatUint(n, 10)
		var info types.BasicInfo // a pattern's literal has no type of its own: its size decides
		if t := e.typeOf(x); t != nil {
			if b, ok := lower.ValueType(t).Underlying().(*types.Basic); ok {
				info = b.Info()
			}
		}

		switch {
		case info&types.IsFloat != 0:
			return digits // a float written as an integer, 3 for 3.0, converts as it is
		case info&types.IsUnsigned != 0 || n > math.MaxInt64:
			return digits + "ULL"
		}

		return digits + "LL"
	case token.FLOAT:
		f, err := strconv.ParseFloat(x.Value, 64)
		if err != nil {
			e.unsupported(x.Pos(), "this float literal")
			return "0"
		}

		s := strconv.FormatFloat(f, 'g', -1, 64)
		if !strings.ContainsAny(s, ".e") {
			s += ".0" // 1.0 must stay a double in C, or 1.0 / 3.0 divides integers
		}

		return s
	}

	return x.Value
}

// minMagnitude reports whether v, an integer literal as literal
// writes it, is 2^63: negated, the smallest i64, which C cannot spell
// as a negated literal.
func minMagnitude(v string) bool {
	return strings.TrimRight(v, "UL") == "9223372036854775808"
}

// cString writes bytes as a C string literal, escaping everything that
// is not plain printable ASCII as three-digit octal, which cannot run
// into a following digit the way \x can, and ? so that no trigraph
// forms.
func cString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' || c == '\\' || c == '?': // ??= is a trigraph in C11
			b.WriteByte('\\')
			b.WriteByte(c)
		case c >= ' ' && c < 127:
			b.WriteByte(c)
		default:
			put(&b, "\\", strconv.FormatInt(int64(c)>>6, 8), strconv.FormatInt(int64(c)>>3&7, 8), strconv.FormatInt(int64(c)&7, 8))
		}
	}

	b.WriteByte('"')
	return b.String()
}

// unary writes a prefix operator. A borrow is the value or its
// address, as valueTo decides; * strips one borrow or pointer; &x moves
// x into a heap cell the owned pointer points at.
func (e *Emitter) unary(x *ast.UnaryExpr) string {
	switch x.Op {
	case token.REF, token.MUT:
		if t := e.typeOf(x.X); !e.isPlace(x.X) && needsDrop(t) {
			if _, ok := t.(*types.Ref); !ok { // a value built here and lent: held for the statement and dropped with it
				tmp := e.spill(e.expr(x.X), t, x.Pos())
				e.hold(tmp, t)
				if x.Op == token.MUT {
					return "&" + tmp
				}

				return tmp
			}
		}

		return e.valueTo(x.X, e.typeOf(x))
	case token.MUL:
		s := e.expr(x.X)
		if _, owned := e.typeOf(x.X).(*types.Pointer); owned && !e.isPlace(x.X) { // a pointer built here owns its block: held, and freed when the statement ends
			held := e.temp()
			e.line(e.cType(e.typeOf(x.X), x.Pos()) + " " + held + " = " + s + ";")
			e.hold(held, e.typeOf(x.X))
			s = held
		}

		switch r := e.typeOf(x.X).(type) {
		case *types.Ref:
			if !r.Mut() {
				return s // a ref is the value itself
			}
		case *types.Pointer:
			return "SN_DEREF(" + s + ")"
		}

		return "(*" + s + ")"
	case token.AND:
		elem := e.typeOf(x).(*types.Pointer).Elem()
		cell := e.temp()
		e.line(e.cType(elem, x.Pos()) + " *" + cell + " = sn_alloc(sizeof(" + e.cType(elem, x.Pos()) + "));")
		e.line("*" + cell + " = " + e.valueTo(x.X, elem) + ";")
		return cell
	case token.ARROW:
		opt := e.typeOf(x)
		elem := lower.ValueType(opt).(*types.Option).Elem()
		ch := e.looked(x.X, lower.ValueType(e.typeOf(x.X)))
		got, ok := e.temp(), e.temp()
		e.line(e.cType(elem, x.Pos()) + " " + got + " = " + e.zero(elem, x.Pos()) + ";")
		e.line("bool " + ok + " = sn_chan_recv(" + ch + ", &" + got + ");")
		return "(" + e.cType(opt, x.Pos()) + "){.has = " + ok + ", .val = " + got + "}"
	case token.XOR:
		return "(~" + e.expr(x.X) + ")"
	case token.SUB:
		if call := e.operatorCall(lower.ValueType(e.typeOf(x.X)), x.Op, true, x.X, nil, x.Pos()); call != "" {
			return call
		}

		if lit, ok := x.X.(*ast.BasicLit); ok && lit.Kind == token.INT && minMagnitude(e.literal(lit)) {
			return "INT64_MIN" // the one magnitude C cannot negate as a signed literal
		}

		return "(-" + e.expr(x.X) + ")"
	case token.NOT:
		return "(!" + e.expr(x.X) + ")"
	}

	e.unsupported(x.OpPos, "the operator "+x.Op.String())
	return "0"
}

// operatorCall writes an operator on a value of a declared type that
// lists the operator's trait as a call of the trait's method, the left
// operand the receiver: a + b is Add(a, b). It returns "" when t has
// no such method, so the operator is written as C's.
func (e *Emitter) operatorCall(t types.Type, op token.Kind, unary bool, recv ast.Expr, args []ast.Expr, pos token.Pos) string {
	fn := types.OperatorMethod(t, op, unary)
	if fn == nil {
		return ""
	}

	name, sig := e.methodNameOf(nil, t.(*types.Named), fn.Name(), pos)
	if sig == nil {
		return "0"
	}

	return name + "(" + e.args(recv, args, sig) + ")"
}

// binary writes x op y. Strings have no operators in C: + is a
// concatenation and the comparisons go through the runtime. A right
// side of && or || that needs statements, or calls anything, is
// written under the test of the left side, so the operators still stop
// short, and the temporaries it built are dropped there, on the one
// path that built them.
func (e *Emitter) binary(x *ast.BinaryExpr) string {
	t := lower.ValueType(e.typeOf(x.X))
	if call := e.operatorCall(t, x.Op, false, x.X, []ast.Expr{x.Y}, x.Pos()); call != "" {
		return call
	}

	if (x.Op == token.LAND || x.Op == token.LOR) && (needsHoist(x.Y) || e.hasEffects(x.Y)) {
		r := e.temp()
		e.line("bool " + r + " = " + e.valueTo(x.X, t) + ";")
		if x.Op == token.LAND {
			e.line("if (" + r + ") {")
		} else {
			e.line("if (!" + r + ") {")
		}

		mark := len(e.temps)
		e.line(r + " = " + e.valueTo(x.Y, t) + ";")
		e.dropTemps(mark, x.Pos())
		e.line("}")
		return r
	}

	if parts := lower.Addends(x, e.typeOf); len(parts) > 2 { // a + b + c: one allocation for the whole
		wants := make([]types.Type, len(parts))
		for i := range wants {
			wants[i] = t
		}

		vals := e.operands(parts, wants)
		pieces := make([]piece, len(parts))
		for i, v := range vals {
			pieces[i] = piece{text: text{v, !e.isRead(parts[i])}}
		}

		return e.concat(pieces)
	}

	var want types.Type = t
	if x.Op != token.ADD {
		want = types.NewRef(t, false) // a comparison only looks at its sides: one built here is held for the statement
	}

	sides := e.operands([]ast.Expr{x.X, x.Y}, []types.Type{want, want})
	l, r := sides[0], sides[1]
	if x.Op == token.EQL || x.Op == token.NEQ {
		if _, basic := t.Underlying().(*types.Basic); !basic || isString(t) {
			if x.Op == token.NEQ {
				return "(!" + e.equal(l, r, t, x.Pos()) + ")"
			}

			return "(" + e.equal(l, r, t, x.Pos()) + ")"
		}
	}

	if isString(t) {
		if x.Op == token.ADD {
			return join(text{l, !e.isRead(x.X)}, text{r, !e.isRead(x.Y)}).s // a side built here is freed after the join
		}

		return "(sn_string_cmp(" + l + ", " + r + ") " + x.Op.String() + " 0)"
	}

	if x.Op == token.AND_NOT {
		return "(" + l + " & ~" + r + ")"
	}

	if divides(x.Op, t) {
		return e.divider(x.Op, t, x.Pos()) + "(" + l + ", " + r + ")"
	}

	return "(" + l + " " + x.Op.String() + " " + r + ")"
}

// divides reports whether op on values of type t is an integer
// division or remainder, which the language defines where C does not.
func divides(op token.Kind, t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsInteger != 0 && (op == token.QUO || op == token.REM)
}

// divider returns the name of the function dividing two integers of
// type t, or taking the remainder, writing it on first use: a divisor
// of zero panics, and the smallest number divided by minus one wraps
// to itself with a remainder of zero, as _docs/design/07-lowering.md
// settles for both backends. C leaves both undefined, and the
// processors disagree: x86-64 traps where arm64 goes on.
func (e *Emitter) divider(op token.Kind, t types.Type, pos token.Pos) string {
	name := "sn_quo_" + lower.Mangle(t)
	if op == token.REM {
		name = "sn_rem_" + lower.Mangle(t)
	}

	if e.state[name] != undeclared {
		return name
	}

	e.state[name] = defined
	ct := e.cType(t, pos)
	var b strings.Builder
	put(&b, "static SN_UNUSED ", ct, " ", name, "(", ct, " a, ", ct, " b) {\n")
	put(&b, "\tif (b == 0) {\n\t\tsn_panic(", literal("integer divide by zero"), ");\n\t}\n\n")
	if t.Underlying().(*types.Basic).Info()&types.IsUnsigned == 0 {
		overflow := "(" + ct + ")(0 - a)" // the wrapping negation, which is the smallest number itself when a is
		if op == token.REM {
			overflow = "0"
		}

		put(&b, "\tif (b == -1) {\n\t\treturn ", overflow, ";\n\t}\n\n")
	}

	put(&b, "\treturn a ", op.String(), " b;\n}\n\n")
	put(&e.protos, "static SN_UNUSED ", ct, " ", name, "(", ct, " a, ", ct, " b);\n")
	e.helpers.WriteString(b.String())
	return name
}

// call writes a conversion, a built-in, a variant constructor, a
// library function, a method, or a call of a function of the program.
func (e *Emitter) call(x *ast.CallExpr) string {
	if _, ok := x.Fun.(*ast.TypeExpr); ok { // a conversion to a type written out, []byte(s)
		return e.conversion(x.Args[0], e.typeOf(x))
	}

	if id, ok := x.Fun.(*ast.Ident); ok {
		switch obj := e.info.Uses[id].(type) {
		case *types.Builtin:
			return e.builtinCall(x, obj)
		case *types.TypeName:
			return e.conversion(x.Args[0], e.typeOf(x)) // the call's type: a type parameter's name stands for the type it was given
		case *types.Func:
			if key := lower.Key(obj); key != "" {
				return e.libraryCall(x, key) // a library calling a function of its own the compiler writes
			}

			e.locate(x, obj)
			return e.funcCall(x, id, obj)
		case *types.Variant:
			return e.variantValue(e.typeOf(x), obj, e.operands(x.Args, e.variantFields(e.typeOf(x), obj)), x.Pos())
		}
	}

	sel, ok := x.Fun.(*ast.SelectorExpr)
	if !ok {
		if sig, ok := lower.ValueType(e.typeOf(x.Fun)).(*types.Signature); ok {
			return e.callValue(x, sig)
		}

		e.unsupported(x.Pos(), "calling a value of type "+e.typeOf(x.Fun).String())
		return "0"
	}

	if pkg := e.packageOf(sel.X); pkg != nil {
		if fn, ok := e.info.Uses[sel.Sel].(*types.Func); ok {
			if key := lower.Key(fn); key != "" {
				return e.libraryCall(x, key) // a function the library declares without a body and the compiler writes
			}

			e.locate(x, fn)
		}

		if v, ok := e.info.Uses[sel.Sel].(*types.Variant); ok {
			return e.variantValue(e.typeOf(x), v, e.operands(x.Args, e.variantFields(e.typeOf(x), v)), x.Pos())
		}

		if _, ok := e.info.Uses[sel.Sel].(*types.TypeName); ok { // a conversion to a library's type, time.Duration(n)
			return e.conversion(x.Args[0], e.typeOf(x))
		}

		return e.funcCall(x, sel.Sel, e.info.Uses[sel.Sel].(*types.Func))
	}

	fn, ok := e.info.Uses[sel.Sel].(*types.Func)
	if !ok {
		if sig, ok := lower.ValueType(e.typeOf(x.Fun)).(*types.Signature); ok && e.info.Uses[sel.Sel] != nil {
			return e.callValue(x, sig) // a field holding a function value
		}

		return e.methodCall(x, sel)
	}

	sig := fn.Type().(*types.Signature)
	if id, ok := sel.X.(*ast.Ident); ok && id.Name == "super" {
		return e.superCall(x, sel, fn)
	}

	switch lower.ValueType(sig.Recv().Type()).(type) {
	case *types.TypeParam:
		return e.boundCall(x, sel, sig.Recv().Type())
	case *types.Trait:
		return e.traitCall(x, sel, sig.Recv().Type(), sig)
	}

	name, sig := e.methodNameOf(sel.Sel, lower.ValueType(sig.Recv().Type()).(*types.Named), sel.Sel.Name, x.Pos())
	if sig == nil {
		return "0"
	}

	return name + "(" + e.args(sel.X, x.Args, sig) + ")"
}

// superCall writes super.M(args): the next definition of M in the
// implementing type's resolution order, after the trait whose default
// is being written, resolved for that type and called directly with
// the method's own receiver; fn is the definition the checker typed
// the call by.
func (e *Emitter) superCall(x *ast.CallExpr, sel *ast.SelectorExpr, fn *types.Func) string {
	recv := e.sub(fn.Type().(*types.Signature).Recv().Type())
	named, ok := lower.ValueType(recv).(*types.Named)
	if !ok {
		e.unsupported(x.Pos(), "super on "+recv.String())
		return "0"
	}

	target := named.SuperMethod(fn)
	if target == nil {
		e.unsupported(x.Pos(), "super."+sel.Sel.Name+" on "+named.String())
		return "0"
	}

	name, sig := e.callInstance(nil, target, named)
	return name + "(" + e.args(sel.X, x.Args, sig) + ")"
}

// boundCall writes a call of a method a bound declares, x.Area() with
// x of a type parameter bounded by Shape, whose receiver type is paramRecv.
// The instance being written has a type argument in the parameter's
// place, and the call goes to that type's own method, directly, as the
// checker found the trait's method on the type when it checked the
// impl list.
func (e *Emitter) boundCall(x *ast.CallExpr, sel *ast.SelectorExpr, paramRecv types.Type) string {
	recv := e.sub(paramRecv)
	if _, ok := lower.ValueType(recv).(*types.Trait); ok { // the type argument is a trait: its values dispatch
		return e.traitCall(x, sel, recv, e.sub(e.info.Uses[sel.Sel].(*types.Func).Type()).(*types.Signature))
	}

	named, ok := lower.ValueType(recv).(*types.Named)
	if !ok || named.Method(sel.Sel.Name) == nil && isPredeclaredKind(named.Underlying()) {
		return e.methodCall(x, sel) // a predeclared type's own method, Cmp on an int or Clone on a string, which a type declared over one has as its own
	}

	name, sig := e.methodNameOf(sel.Sel, named, sel.Sel.Name, x.Pos())
	if sig == nil {
		return "0"
	}

	return name + "(" + e.args(sel.X, x.Args, sig) + ")"
}

// args writes the arguments of a call, the receiver first when there
// is one, each converted to its parameter's type.
func (e *Emitter) args(recv ast.Expr, args []ast.Expr, sig *types.Signature) string {
	_, values := e.callOperands(recv, args, sig)
	return strings.Join(values, ", ")
}

// callOperands evaluates a call's receiver, when there is one, and its
// arguments, each converted to the type the signature wants, and
// returns those types with the values.
func (e *Emitter) callOperands(recv ast.Expr, args []ast.Expr, sig *types.Signature) ([]types.Type, []string) {
	wants := make([]types.Type, 0, len(args)+1)
	exprs := make([]ast.Expr, 0, len(args)+1)
	if recv != nil {
		wants = append(wants, sig.Recv().Type())
		exprs = append(exprs, recv)
	}

	for i, a := range args {
		var want types.Type
		if i < len(sig.Params()) {
			want = sig.Params()[i].Type()
		}

		wants = append(wants, want)
		exprs = append(exprs, a)
	}

	return wants, e.operands(exprs, wants)
}

// minCall writes min(a, b, ...) and max(a, b, ...) as nested
// conditionals over the arguments, each evaluated once.
func (e *Emitter) minCall(x *ast.CallExpr, b *types.Builtin) string {
	t := e.typeOf(x)
	parts := e.operands(x.Args, nil)
	for i, a := range x.Args {
		if e.hasEffects(a) { // named twice below
			parts[i] = e.spill(parts[i], t, x.Pos())
		}
	}

	if isFloat(t) { // NaN wins, and -0 is below +0, as Go's specification has min and max on floats
		out := parts[0]
		for _, p := range parts[1:] {
			out = "sn_f64_" + b.Name() + "(" + out + ", " + p + ")"
		}

		return "((" + e.cType(t, x.Pos()) + ")" + out + ")"
	}

	op := "<"
	if b.ID() == types.BuiltinMax {
		op = ">"
	}

	out := parts[0]
	for _, p := range parts[1:] {
		out = "((" + p + " " + op + " " + out + ") ? " + p + " : " + out + ")"
	}

	return out
}

// variantFields lists the types a variant's arguments must have.
func (e *Emitter) variantFields(t types.Type, v *types.Variant) []types.Type {
	switch u := lower.ValueType(t).Underlying().(type) {
	case *types.Option:
		return []types.Type{u.Elem()}
	case *types.Result:
		if v == types.OkVariant {
			return []types.Type{u.Val()}
		}

		return []types.Type{u.Err()}
	}

	fields := variantOf(t, v).Fields()
	ts := make([]types.Type, len(fields))
	for i, f := range fields {
		ts[i] = f.Type()
	}

	return ts
}

func (e *Emitter) builtinCall(x *ast.CallExpr, b *types.Builtin) string {
	switch b.ID() {
	case types.BuiltinLen, types.BuiltinCap:
		return e.lenCall(x, b)
	case types.BuiltinMake:
		return e.makeCall(x)
	case types.BuiltinTake:
		return e.takeCall(x)
	case types.BuiltinPanic:
		return "sn_panic(" + e.toString(x.Args[0]) + ")"
	case types.BuiltinMin, types.BuiltinMax:
		return e.minCall(x, b)
	case types.BuiltinClose:
		return "sn_chan_close(" + e.looked(x.Args[0], lower.ValueType(e.typeOf(x.Args[0]))) + ")"
	case types.BuiltinPrint:
		parts := e.evaluated(x.Args)
		for i, a := range x.Args {
			if s := e.text(parts[i], a); s.owned {
				e.line("sn_eprint_owned(" + s.s + ");")
			} else {
				e.line("sn_eprint_string(" + s.s + ");")
			}
		}

		return ""
	}

	e.unsupported(x.Pos(), b.Name()+" on this type")
	return "0"
}

// conversion writes T(x): a C cast between numbers, or between a
// string and its bytes a copy, since the checker reads the operand and
// leaves it to its owner; a temporary built for it is dropped with the
// statement.
func (e *Emitter) conversion(x ast.Expr, to types.Type) string {
	from := lower.ValueType(e.typeOf(x))
	switch {
	case isString(from) && isByteSlice(to):
		return "sn_string_to_bytes(" + e.looked(x, from) + ")"
	case isByteSlice(from) && isString(to):
		return "sn_bytes_to_string(" + e.looked(x, from) + ")"
	case isRuneSlice(from) && isString(to):
		t := e.spill(e.looked(x, from), from, x.Pos())
		return "sn_string_from_runes((sn_slice){" + t + ".ptr, " + t + ".len, " + t + ".cap})"
	}

	v := e.operands([]ast.Expr{x}, nil)[0]
	if isRune(from) && isString(to) {
		return "sn_string_from_rune(" + v + ")"
	}

	return "((" + e.cType(to, x.Pos()) + ")(" + v + "))"
}

// isRune reports whether t is rune, i32, or a type declared over it.
func isRune(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Kind() == types.Int32
}

// isRuneSlice reports whether t is []rune.
func isRuneSlice(t types.Type) bool {
	s, ok := t.Underlying().(*types.Slice)
	return ok && isRune(s.Elem())
}

// isByteSlice reports whether t is []byte.
func isByteSlice(t types.Type) bool {
	s, ok := t.Underlying().(*types.Slice)
	if !ok {
		return false
	}

	b, ok := s.Elem().Underlying().(*types.Basic)
	return ok && b.Kind() == types.Uint8
}

// funcCall writes the call x of the declared function fn, named by
// id: its instance for a generic function, else the function itself.
func (e *Emitter) funcCall(x *ast.CallExpr, id *ast.Ident, fn *types.Func) string {
	if e.generics[fn] != nil {
		name, sig := e.callInstance(id, fn, nil)
		return name + "(" + e.args(nil, x.Args, sig) + ")"
	}

	return lower.FuncName(fn) + "(" + e.args(nil, x.Args, fn.Type().(*types.Signature)) + ")"
}

// packageOf returns the package an identifier names, or nil.
func (e *Emitter) packageOf(x ast.Expr) *types.PkgName {
	if id, ok := x.(*ast.Ident); ok {
		if pkg, ok := e.info.Uses[id].(*types.PkgName); ok {
			return pkg
		}
	}

	return nil
}

// fieldType returns the type of the named field of st.
func fieldType(st *types.Struct, name string) types.Type {
	for i := 0; i < st.NumFields(); i++ {
		if f := st.Field(i); f.Name() == name {
			return f.Type()
		}
	}

	return nil
}

// isPredeclaredKind reports whether t is a predeclared kind, a number,
// bool, string, slice, map, array, tuple or option, whose standard
// methods a type declared over it has as its own, time.Duration's Eq
// and Cmp or a named slice's Clone; a struct or an enum has what it
// lists.
func isPredeclaredKind(t types.Type) bool {
	switch t.(type) {
	case *types.Struct, *types.Enum:
		return false
	}

	return true
}
