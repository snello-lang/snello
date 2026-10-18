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
// is a statement in C, such as a print, writes its statements and
// returns an empty string.
func (e *Emitter) expr(x ast.Expr) string {
	switch x := x.(type) {
	case *ast.Ident:
		return e.ident(x)
	case *ast.BasicLit:
		return e.literal(x)
	case *ast.ParenExpr:
		return "(" + e.expr(x.X) + ")"
	case *ast.CallExpr:
		return e.call(x)
	case *ast.UnaryExpr:
		return e.unary(x)
	case *ast.BinaryExpr:
		return e.binary(x)
	case *ast.IndexExpr:
		if isString(readType(e.info.Types[x.X])) {
			return e.valueTo(x.X, types.Typ[types.String]) + ".ptr[" + e.expr(x.Index) + "]"
		}
	}

	e.unsupported(x.Pos(), "this kind of expression")
	return "0"
}

// valueTo writes x where a value of type want is expected, with the
// dereference the checker allowed silently when a mut borrow is read
// as its value.
func (e *Emitter) valueTo(x ast.Expr, want types.Type) string {
	s := e.expr(x)
	if want == nil {
		return s
	}

	have := e.info.Types[x]
	if have == nil {
		return s
	}

	for d := cDepth(have) - cDepth(want); d > 0; d-- {
		s = "(*" + s + ")"
	}

	return s
}

// cDepth is the number of C pointers in front of the value of a Teak
// type: one per mut borrow, none for a ref borrow.
func cDepth(t types.Type) int {
	if r, ok := t.(*types.Ref); ok {
		if r.Mut() {
			return 1 + cDepth(r.Elem())
		}

		return cDepth(r.Elem())
	}

	return 0
}

// readType is what an operator reads from an operand type: the value
// behind any borrow.
func readType(t types.Type) types.Type {
	if r, ok := t.(*types.Ref); ok {
		return r.Elem()
	}

	return t
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
	}

	return ident(x.Name)
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

		return "TK_STR(" + cString(s) + ", " + strconv.Itoa(len(s)) + ")"
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

func (e *Emitter) unary(x *ast.UnaryExpr) string {
	switch x.Op {
	case token.REF:
		return e.expr(x.X)
	case token.MUT:
		return "&" + e.expr(x.X)
	case token.MUL:
		return "(*" + e.expr(x.X) + ")"
	case token.XOR:
		return "(~" + e.expr(x.X) + ")"
	case token.SUB, token.NOT:
		return "(" + x.Op.String() + e.expr(x.X) + ")"
	}

	e.unsupported(x.OpPos, "the operator "+x.Op.String())
	return "0"
}

// binary writes x op y. Strings have no operators in C: + is a
// concatenation and the comparisons go through the runtime.
func (e *Emitter) binary(x *ast.BinaryExpr) string {
	t := readType(e.info.Types[x.X])
	if yt := e.info.Types[x.Y]; yt != nil && cDepth(yt) < cDepth(e.info.Types[x.X]) {
		t = readType(yt)
	}

	l, r := e.valueTo(x.X, t), e.valueTo(x.Y, t)
	if isString(t) {
		switch x.Op {
		case token.ADD:
			return "tk_concat(" + l + ", " + r + ")"
		case token.EQL:
			return "tk_string_eq(" + l + ", " + r + ")"
		case token.NEQ:
			return "(!tk_string_eq(" + l + ", " + r + "))"
		default:
			return "(tk_string_cmp(" + l + ", " + r + ") " + x.Op.String() + " 0)"
		}
	}

	op := x.Op.String()
	if x.Op == token.AND_NOT {
		return "(" + l + " & ~" + r + ")"
	}

	return "(" + l + " " + op + " " + r + ")"
}

// needsHoist reports whether writing x produces statements. Nothing
// does yet; the next slice's ?, ?? and match will.
func needsHoist(x ast.Expr) bool {
	return false
}

// call writes a conversion, a built-in, a library function, or a call
// of a function of the program.
func (e *Emitter) call(x *ast.CallExpr) string {
	if id, ok := x.Fun.(*ast.Ident); ok {
		switch obj := e.info.Uses[id].(type) {
		case *types.Builtin:
			return e.builtinCall(x, obj)
		case *types.TypeName:
			return "((" + e.cType(obj.Type(), x.Pos()) + ")(" + e.expr(x.Args[0]) + "))"
		case *types.Func:
			return e.ident(id) + "(" + e.args(x.Args, obj.Type().(*types.Signature)) + ")"
		case *types.Variant:
			e.unsupported(x.Pos(), "variants")
			return "0"
		}
	}

	if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
		if pkg := e.packageOf(sel.X); pkg != nil {
			return e.libraryCall(x, pkg, sel.Sel.Name)
		}
	}

	e.unsupported(x.Pos(), "this kind of call")
	return "0"
}

func (e *Emitter) args(args []ast.Expr, sig *types.Signature) string {
	parts := make([]string, len(args))
	for i, a := range args {
		var want types.Type
		if i < len(sig.Params()) {
			want = sig.Params()[i].Type()
		}

		parts[i] = e.valueTo(a, want)
	}

	return strings.Join(parts, ", ")
}

func (e *Emitter) builtinCall(x *ast.CallExpr, b *types.Builtin) string {
	switch b.ID() {
	case types.BuiltinLen:
		if isString(readType(e.info.Types[x.Args[0]])) {
			return e.valueTo(x.Args[0], types.Typ[types.String]) + ".len"
		}
	case types.BuiltinPanic:
		return "tk_panic(" + e.toString(x.Args[0]) + ")"
	case types.BuiltinPrint:
		for _, a := range x.Args {
			e.line("tk_eprint_string(" + e.toString(a) + ");")
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
		for i, a := range x.Args {
			if i > 0 {
				e.line("tk_print_sep();")
			}

			e.printValue(a)
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

// printValue writes the runtime call that prints one value.
func (e *Emitter) printValue(a ast.Expr) {
	kind, v := e.printable(a)
	if kind == "" {
		return
	}

	e.line("tk_print_" + kind + "(" + v + ");")
}

// toString writes the runtime call that turns a value into a string.
func (e *Emitter) toString(a ast.Expr) string {
	kind, v := e.printable(a)
	switch kind {
	case "":
		return `TK_STR("", 0)`
	case "string":
		return v
	}

	return "tk_" + kind + "_to_string(" + v + ")"
}

// printable sorts a value into what the runtime can print, string,
// int, uint, f64 or bool, and writes it cast to the runtime's type for
// that kind. A value the runtime cannot print is reported and yields
// an empty kind.
func (e *Emitter) printable(a ast.Expr) (kind, value string) {
	t := readType(e.info.Types[a])
	v := e.valueTo(a, t)
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		e.unsupported(a.Pos(), "printing values of type "+t.String())
		return "", ""
	}

	switch {
	case b.Kind() == types.String:
		return "string", v
	case b.Kind() == types.Bool:
		return "bool", v
	case b.Info()&types.IsFloat != 0:
		return "f64", "(double)(" + v + ")"
	case b.Info()&types.IsUnsigned != 0:
		return "uint", "(uint64_t)(" + v + ")"
	case b.Info()&types.IsInteger != 0:
		return "int", "(int64_t)(" + v + ")"
	}

	e.unsupported(a.Pos(), "printing values of type "+t.String())
	return "", ""
}

// sprint joins the arguments as Sprint does: a space between two
// operands when neither is a string.
func (e *Emitter) sprint(args []ast.Expr) string {
	out := ""
	for i, a := range args {
		s := e.toString(a)
		if i > 0 && !isString(readType(e.info.Types[a])) && !isString(readType(e.info.Types[args[i-1]])) {
			out = concat(out, `TK_STR(" ", 1)`)
		}

		out = concat(out, s)
	}

	if out == "" {
		return `TK_STR("", 0)`
	}

	return out
}

// sprintf reads the format at compile time and writes the
// concatenation it stands for. Verbs: %d, %s, %v, %q, %%.
func (e *Emitter) sprintf(x *ast.CallExpr) string {
	lit, ok := x.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		e.unsupported(x.Args[0].Pos(), "a format that is not a literal")
		return `TK_STR("", 0)`
	}

	format, _ := strconv.Unquote(lit.Value)
	args := x.Args[1:]
	out, text := "", ""
	flush := func() {
		if text != "" {
			out = concat(out, "TK_STR("+cString(text)+", "+strconv.Itoa(len(text))+")")
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
			return `TK_STR("", 0)`
		}

		flush()
		a := args[0]
		args = args[1:]
		switch verb {
		case 'd', 's', 'v':
			out = concat(out, e.toString(a))
		case 'q':
			out = concat(out, "tk_quote("+e.toString(a)+")")
		default:
			e.unsupported(lit.Pos(), "the format verb %"+string(verb))
		}
	}

	flush()
	if out == "" {
		return `TK_STR("", 0)`
	}

	return out
}

func concat(a, b string) string {
	if a == "" {
		return b
	}

	return "tk_concat(" + a + ", " + b + ")"
}
