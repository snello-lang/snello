// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"strconv"
	"strings"

	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/token"
	"github.com/teak-lang/teak/types"
)

// expr writes an expression and returns its C text. An expression that
// needs statements, such as a match or a ?, writes them first and
// returns the temporary holding its value; one that is a statement in
// C, such as a print, writes its statements and returns an empty
// string.
func (e *Emitter) expr(x ast.Expr) string {
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
		return e.index(x)
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
		e.unsupported(x.Pos(), "closures")
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
	s := e.expr(x)
	have := e.info.Types[x]
	if want == nil || have == nil {
		return s
	}

	d := depth(have) - depth(want)
	for ; d > 0; d-- {
		s = "(*" + s + ")"
	}

	if d < 0 {
		s = "&" + s
	}

	return s
}

// place writes x dereferenced to the value it denotes, for a field
// access or a match on it.
func (e *Emitter) place(x ast.Expr) string {
	return deref(e.expr(x), e.info.Types[x])
}

func (e *Emitter) ident(x *ast.Ident) string {
	switch obj := e.info.Uses[x].(type) {
	case *types.Const:
		if obj.Pos() == token.NoPos {
			return obj.Name() // true, false: C has them with stdbool.h
		}
	case *types.Func:
		if obj.Name() == "main" {
			return "tk_main"
		}
	case *types.Variant:
		return e.variantValue(e.info.Types[x], obj, nil, x.Pos())
	}

	return ident(x.Name)
}

// variantValue writes a value of an enum, option or Result type built
// by a variant: a compound literal with the tag and the payload.
func (e *Emitter) variantValue(t types.Type, v *types.Variant, args []string, pos token.Pos) string {
	ct := e.cType(t, pos)
	switch u := valueType(t).Underlying().(type) {
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
		e.unsupported(x.Pos(), pkg.Name()+"."+x.Sel.Name)
		return "0"
	}

	if _, ok := e.info.Uses[x.Sel].(*types.Var); !ok {
		e.unsupported(x.Pos(), "methods as values")
		return "0"
	}

	return e.place(x.X) + "." + ident(x.Sel.Name)
}

// tuple writes (a, b) as a struct literal and () as the unit value.
func (e *Emitter) tuple(x *ast.TupleExpr) string {
	if len(x.Elts) == 0 {
		return "TK_UNIT"
	}

	t := e.info.Types[x].(*types.Tuple)
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
	t := e.info.Types[x]
	if st, ok := t.Underlying().(*types.Slice); ok {
		return e.sliceLit(x, t, st)
	}

	if _, ok := t.Underlying().(*types.Struct); !ok {
		e.unsupported(x.Pos(), "literals of type "+t.String())
		return "0"
	}

	if len(x.Elts) == 0 {
		return "(" + e.cType(t, x.Pos()) + "){0}"
	}

	values := make([]ast.Expr, len(x.Elts))
	wants := make([]types.Type, len(x.Elts))
	names := make([]string, len(x.Elts))
	for i, elt := range x.Elts {
		kv := elt.(*ast.KeyValueExpr)
		key := kv.Key.(*ast.Ident)
		values[i] = kv.Value
		wants[i] = e.info.Uses[key].Type()
		names[i] = ident(key.Name)
	}

	parts := e.operands(values, wants)
	for i := range parts {
		parts[i] = "." + names[i] + " = " + parts[i]
	}

	return "(" + e.cType(t, x.Pos()) + "){" + strings.Join(parts, ", ") + "}"
}

// literal writes a number in decimal, a character as its code, and a
// string as a view of a C string literal with its length computed
// here, so the runtime never counts bytes.
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

		return strconv.FormatUint(n, 10)
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
			b.WriteString("\\" + strconv.FormatInt(int64(c)>>6, 8) + strconv.FormatInt(int64(c)>>3&7, 8) + strconv.FormatInt(int64(c)&7, 8))
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
		return e.valueTo(x.X, e.info.Types[x])
	case token.MUL:
		s := e.expr(x.X)
		if r, ok := e.info.Types[x.X].(*types.Ref); ok && !r.Mut() {
			return s // a ref is the value itself
		}

		return "(*" + s + ")"
	case token.AND:
		elem := e.info.Types[x].(*types.Pointer).Elem()
		cell := e.temp()
		e.line(e.cType(elem, x.Pos()) + " *" + cell + " = tk_alloc(sizeof(" + e.cType(elem, x.Pos()) + "));")
		e.line("*" + cell + " = " + e.valueTo(x.X, elem) + ";")
		return cell
	case token.ARROW:
		e.unsupported(x.OpPos, "channels")
		return "0"
	case token.XOR:
		return "(~" + e.expr(x.X) + ")"
	case token.SUB, token.NOT:
		return "(" + x.Op.String() + e.expr(x.X) + ")"
	}

	e.unsupported(x.OpPos, "the operator "+x.Op.String())
	return "0"
}

// binary writes x op y. Strings have no operators in C: + is a
// concatenation and the comparisons go through the runtime. A right
// side that needs statements is written under the test of the left
// side, so && and || still stop short.
func (e *Emitter) binary(x *ast.BinaryExpr) string {
	t := valueType(e.info.Types[x.X])
	if (x.Op == token.LAND || x.Op == token.LOR) && needsHoist(x.Y) {
		r := e.temp()
		e.line("bool " + r + " = " + e.valueTo(x.X, t) + ";")
		if x.Op == token.LAND {
			e.line("if (" + r + ") {")
		} else {
			e.line("if (!" + r + ") {")
		}

		e.line(r + " = " + e.valueTo(x.Y, t) + ";")
		e.line("}")
		return r
	}

	sides := e.operands([]ast.Expr{x.X, x.Y}, []types.Type{t, t})
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
			return "tk_concat(" + l + ", " + r + ")"
		}

		return "(tk_string_cmp(" + l + ", " + r + ") " + x.Op.String() + " 0)"
	}

	if x.Op == token.AND_NOT {
		return "(" + l + " & ~" + r + ")"
	}

	return "(" + l + " " + x.Op.String() + " " + r + ")"
}

// call writes a conversion, a built-in, a variant constructor, a
// library function, a method, or a call of a function of the program.
func (e *Emitter) call(x *ast.CallExpr) string {
	if id, ok := x.Fun.(*ast.Ident); ok {
		switch obj := e.info.Uses[id].(type) {
		case *types.Builtin:
			return e.builtinCall(x, obj)
		case *types.TypeName:
			return "((" + e.cType(obj.Type(), x.Pos()) + ")(" + e.expr(x.Args[0]) + "))"
		case *types.Func:
			return e.ident(id) + "(" + e.args(nil, x.Args, obj.Type().(*types.Signature)) + ")"
		case *types.Variant:
			return e.variantValue(e.info.Types[x], obj, e.operands(x.Args, e.variantFields(e.info.Types[x], obj)), x.Pos())
		}
	}

	sel, ok := x.Fun.(*ast.SelectorExpr)
	if !ok {
		e.unsupported(x.Pos(), "calling a value")
		return "0"
	}

	if pkg := e.packageOf(sel.X); pkg != nil {
		return e.libraryCall(x, pkg, sel.Sel.Name)
	}

	fn, ok := e.info.Uses[sel.Sel].(*types.Func)
	if !ok {
		return e.methodCall(x, sel)
	}

	sig := fn.Type().(*types.Signature)
	return methodName(sig.Recv().Type(), sel.Sel.Name) + "(" + e.args(sel.X, x.Args, sig) + ")"
}

// args writes the arguments of a call, the receiver first when there
// is one, each converted to its parameter's type.
func (e *Emitter) args(recv ast.Expr, args []ast.Expr, sig *types.Signature) string {
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

	return strings.Join(e.operands(exprs, wants), ", ")
}

// variantFields lists the types a variant's arguments must have.
func (e *Emitter) variantFields(t types.Type, v *types.Variant) []types.Type {
	switch u := valueType(t).Underlying().(type) {
	case *types.Option:
		return []types.Type{u.Elem()}
	case *types.Result:
		if v == types.OkVariant {
			return []types.Type{u.Val()}
		}

		return []types.Type{u.Err()}
	}

	fields := v.Fields()
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
		return "tk_panic(" + e.toString(x.Args[0]) + ")"
	case types.BuiltinPrint:
		parts := e.evaluated(x.Args)
		for i, a := range x.Args {
			e.line("tk_eprint_string(" + e.text(parts[i], a) + ");")
		}

		return ""
	}

	e.unsupported(x.Pos(), b.Name()+" on this type")
	return "0"
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

// libraryCall lowers the stage 1 library: fmt's printing becomes
// runtime calls chosen by each argument's type, and the format of
// Printf and Sprintf is read here, so the runtime has no formatter.
func (e *Emitter) libraryCall(x *ast.CallExpr, pkg *types.PkgName, name string) string {
	switch pkg.Path() + "." + name {
	case "fmt.Println":
		parts := e.evaluated(x.Args)
		for i, a := range x.Args {
			if i > 0 {
				e.line("tk_print_sep();")
			}

			e.line("tk_print_string(" + e.text(parts[i], a) + ");")
		}

		e.line("tk_print_nl();")
		return ""
	case "fmt.Sprint":
		return e.sprint(x.Args)
	case "fmt.Sprintf":
		return e.sprintf(x)
	case "fmt.Printf":
		e.line("tk_print_string(" + e.sprintf(x) + ");")
		return ""
	case "os.Exit":
		return "tk_exit((int)(" + e.expr(x.Args[0]) + "))"
	}

	e.unsupported(x.Pos(), pkg.Name()+"."+name)
	return "0"
}

// toString writes the runtime call that turns a value into a string.
func (e *Emitter) toString(a ast.Expr) string {
	return e.text(e.expr(a), a)
}

// text writes the conversion of v, the C text of a, to a tk_string.
func (e *Emitter) text(v string, a ast.Expr) string {
	return e.toText(v, e.info.Types[a], a.Pos(), false)
}

// sprint joins the arguments as Sprint does: a space between two
// operands when neither is a string.
func (e *Emitter) sprint(args []ast.Expr) string {
	parts := e.operands(args, nil)
	out := ""
	for i, a := range args {
		if i > 0 && !isString(valueType(e.info.Types[a])) && !isString(valueType(e.info.Types[args[i-1]])) {
			out = concat(out, literal(" "))
		}

		out = concat(out, e.text(parts[i], a))
	}

	if out == "" {
		return literal("")
	}

	return out
}

// sprintf reads the format at compile time and writes the
// concatenation it stands for. Verbs: %d, %s, %v, %q, %%.
func (e *Emitter) sprintf(x *ast.CallExpr) string {
	lit, ok := x.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		e.unsupported(x.Args[0].Pos(), "a format that is not a literal")
		return literal("")
	}

	format, _ := strconv.Unquote(lit.Value)
	args := x.Args[1:]
	parts := e.operands(args, nil)
	out, text := "", ""
	flush := func() {
		if text != "" {
			out = concat(out, literal(text))
			text = ""
		}
	}

	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 >= len(format) {
			text += string(format[i])
			continue
		}

		i++
		verb := format[i]
		if verb == '%' {
			text += "%"
			continue
		}

		if len(args) == 0 {
			e.unsupported(lit.Pos(), "a format with more verbs than arguments")
			return literal("")
		}

		flush()
		a, v := args[0], parts[0]
		args, parts = args[1:], parts[1:]
		switch verb {
		case 'd', 's', 'v':
			out = concat(out, e.text(v, a))
		case 'q':
			out = concat(out, "tk_quote("+e.text(v, a)+")")
		default:
			e.unsupported(lit.Pos(), "the format verb %"+string(verb))
		}
	}

	flush()
	if out == "" {
		return literal("")
	}

	return out
}

func concat(a, b string) string {
	if a == "" {
		return b
	}

	return "tk_concat(" + a + ", " + b + ")"
}
