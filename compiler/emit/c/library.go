// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"path/filepath"
	"strconv"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// The functions the library declares without a body and the compiler
// writes are listed in emit/lower, with the reason for each; this file
// writes them in C: fmt's printing as the runtime's printers chosen by
// the type of each argument, os.Exit as the runtime's exit, and the
// primitives of rc and sync as the pointer itself.

// libraryCall writes the call x of the lowered function key.
func (e *Emitter) libraryCall(x *ast.CallExpr, key string) string {
	switch key {
	case "fmt.Println": // the line built whole and written at once, so two tasks' lines do not interleave
		e.line("sn_print_owned(" + e.sprintln(x.Args) + ");")
		return ""
	case "fmt.Sprint":
		return e.sprint(x.Args)
	case "fmt.Sprintf":
		return e.sprintf(x.Args[0], x.Args[1:])
	case "fmt.Printf":
		e.line("sn_print_owned(" + e.sprintf(x.Args[0], x.Args[1:]) + ");")
		return ""
	case "fmt.Fprintln":
		e.fprint(x, e.sprintln(x.Args[1:]))
		return ""
	case "fmt.Fprintf":
		e.fprint(x, e.sprintf(x.Args[1], x.Args[2:]))
		return ""
	case "os.Exit":
		return "sn_exit((int)(" + e.expr(x.Args[0]) + "))"
	case "rc.alias", "sync.alias", "sync.inside": // a second copy of the pointer a borrow lends, owned by the caller, or the cell it points at as a mut: a ref borrow of a pointer is the pointer in C, and so is a mut of what it points at
		t := e.typeOf(x.Args[0])
		if r, ok := t.(*types.Ref); ok {
			t = r.Elem()
		}

		return e.operands(x.Args, []types.Type{types.NewRef(t, false)})[0]
	case "rc.leak", "sync.leak": // the pointer moves in and nothing frees it
		e.line("(void)" + e.operands(x.Args, []types.Type{e.typeOf(x.Args[0])})[0] + ";")
		return ""
	case "testing.Logf", "expect.Failf": // the format read at compile time as fmt's are, the line handed to the unformatted function with the place of this call
		fn := e.info.Uses[x.Fun.(*ast.SelectorExpr).Sel].(*types.Func)
		e.locate(x, fn)
		plain := "Log"
		if key == "expect.Failf" {
			plain = "Fail"
		}

		tmp := e.temp()
		e.line("sn_string " + tmp + " = " + e.sprintf(x.Args[0], x.Args[1:]) + ";")
		e.line(lower.ObjName(fn.Pkg().Scope().Lookup(plain)) + "(" + tmp + ");")
		e.line("sn_string_drop(&" + tmp + ");")
		return ""
	case "testing.Catch": // the function runs under the runtime's mark, called through an adapter as the sort's less is
		f := e.saved(x.Args[0], e.typeOf(x.Args[0]))
		out := e.temp()
		e.line(e.cType(e.typeOf(x), x.Pos()) + " " + out + " = {.has = false};")
		e.line(out + ".has = sn_catch(" + e.catchAdapter(lower.ValueType(e.typeOf(x.Args[0])), x.Pos()) + ", &" + f + ", &" + out + ".val);")
		return out
	}

	e.unsupported(x.Pos(), key)
	return "0"
}

// locate hands the runtime, before a call of a located function, the
// place of the call for the running task, file:line, with the call's
// text and its parts for an assertion: the type of what Equal
// compares, or the operands of a condition with their values, when
// they are simple enough to read twice (_docs/design/09-testing.md,
// section 4).
func (e *Emitter) locate(x *ast.CallExpr, fn *types.Func) {
	mode := lower.Located(fn)
	if mode == "" {
		return
	}

	pos := e.file.Position(x.Pos())
	where := filepath.Base(pos.Filename) + ":" + strconv.Itoa(pos.Line)
	if mode != "call" {
		e.line("sn_testing_line(" + literal(where) + ");")
		return
	}

	where += ": " + ast.Source(x)
	e.line("sn_testing_call(" + literal(where) + ", " + e.parts(x, fn.Name()) + ");")
}

// parts writes the parts of an assertion as lower.Parts decides them:
// the type compared, or the operands with their values, "n is 2,
// limit is 3", or nothing.
func (e *Emitter) parts(x *ast.CallExpr, name string) string {
	typed, operands := lower.Parts(e.info, x, name)
	if typed {
		return literal(lower.ValueType(e.typeOf(x.Args[0])).String())
	}

	if len(operands) == 0 {
		return literal("")
	}

	var pieces []piece
	for i, operand := range operands {
		if i > 0 {
			pieces = append(pieces, piece{lit: ", "})
		}

		pieces = append(pieces, piece{lit: ast.Source(operand) + " is "}, piece{text: e.text(e.expr(operand), operand)})
	}

	return e.concat(pieces)
}

// catchAdapter returns the name of the function that lets sn_catch
// call a Snello function value held in the context, written once.
func (e *Emitter) catchAdapter(sig types.Type, pos token.Pos) string {
	name := "sn_catchfn_" + lower.Mangle(sig)
	if e.state[name] == undeclared {
		e.state[name] = defined
		put(&e.helpers, "static void ", name, "(void *ctx) {\n\t", e.cType(sig, pos), " *f = ctx;\n\tf->fn(f->box);\n}\n\n")
	}

	return name
}

// toString writes the runtime call that turns a value into a string.
func (e *Emitter) toString(a ast.Expr) string {
	return e.text(e.expr(a), a).s
}

// text writes the conversion of v, the C text of a, to a sn_string.
func (e *Emitter) text(v string, a ast.Expr) text {
	return e.textOf(v, e.typeOf(a), a.Pos(), false)
}

// sprint joins the arguments as Sprint does: a space between two
// operands when neither is a string. The result is built here; a
// string argument is cloned, so the result is one owned string.
func (e *Emitter) sprint(args []ast.Expr) string {
	parts := e.operands(args, nil)
	var pieces []piece
	for i, a := range args {
		if i > 0 && !isString(lower.ValueType(e.typeOf(a))) && !isString(lower.ValueType(e.typeOf(args[i-1]))) {
			pieces = append(pieces, piece{lit: " "})
		}

		pieces = append(pieces, piece{text: e.text(parts[i], a)})
	}

	return e.concat(pieces)
}

// sprintln joins the arguments as Println prints them: a space between
// every two, and a newline at the end.
func (e *Emitter) sprintln(args []ast.Expr) string {
	parts := e.operands(args, nil)
	var pieces []piece
	for i, a := range args {
		if i > 0 {
			pieces = append(pieces, piece{lit: " "})
		}

		pieces = append(pieces, piece{text: e.text(parts[i], a)})
	}

	return e.concat(append(pieces, piece{lit: "\n"}))
}

// fprint writes the owned string s to the writer x.Args[0] names, a mut
// borrow of a type implementing io.Writer, through its Write method
// over a view of the string's bytes; the result is dropped, as
// Fprintln has none.
func (e *Emitter) fprint(x *ast.CallExpr, s string) {
	w := x.Args[0]
	named, ok := lower.ValueType(e.typeOf(w)).(*types.Named)
	if !ok {
		e.unsupported(w.Pos(), "a writer of type "+e.typeOf(w).String())
		return
	}

	name, sig := e.methodNameOf(nil, named, "Write", x.Pos())
	if sig == nil {
		return
	}

	recv := e.expr(w)
	tmp := e.temp()
	e.line("sn_string " + tmp + " = " + s + ";")
	bytes := "(" + e.cType(types.NewSlice(types.Typ[types.Uint8]), x.Pos()) + "){(uint8_t *)" + tmp + ".ptr, " + tmp + ".len, 0}"
	e.discard(name+"("+recv+", "+bytes+")", sig.Results(), x.Pos())
	e.line("sn_string_drop(&" + tmp + ");")
}

// built writes a text as a string the caller owns: a clone when it
// was only lent.
func built(t text) string {
	switch {
	case t.s == "":
		return literal("")
	case !t.owned:
		return "sn_string_clone(" + t.s + ")"
	}

	return t.s
}

// sprintf reads the format at compile time, with the grammar the
// checker checked it with, and writes the concatenation it stands for;
// verb writes each argument's text.
func (e *Emitter) sprintf(f ast.Expr, args []ast.Expr) string {
	format, ok := types.FormatText(e.info, f)
	pieces, err := types.ParseFormat(format)
	if !ok || err != nil || types.FormatCount(pieces) != len(args) {
		e.unsupported(f.Pos(), "a format the checker refuses") // the checker reported it
		return literal("")
	}

	parts := e.operands(args, nil)
	var out []piece
	for _, p := range pieces {
		if p.Verb == 0 {
			out = append(out, piece{lit: p.Text})
			continue
		}

		part := e.verb(p, args[0], parts[0])
		args, parts = args[1:], parts[1:]
		if p.Width >= 0 {
			part = text{"sn_pad(" + part.s + ", " + strconv.Itoa(p.Width) + ", " + strconv.FormatBool(p.Minus) + ", " + strconv.FormatBool(part.owned) + ")", true}
		}

		out = append(out, piece{text: part})
	}

	return e.concat(out)
}

// verb writes the text of the argument a, whose C value is v, as the
// verb of p prints it, with its precision; the checker has made sure
// the verb fits the argument's type.
func (e *Emitter) verb(p types.FormatPiece, a ast.Expr, v string) text {
	t := e.typeOf(a)
	info := types.BasicInfo(0)
	if b, ok := lower.ValueType(t).Underlying().(*types.Basic); ok {
		info = b.Info()
	}

	prec := strconv.Itoa(p.Prec)
	truncated := func(s text) text {
		if p.Prec < 0 {
			return s
		}

		return text{"sn_truncate(" + s.s + ", " + prec + ", " + strconv.FormatBool(s.owned) + ")", s.owned}
	}

	switch {
	case p.Verb == 'v' && p.Plus: // an error's chain
		return text{e.chainer(lower.ValueType(t), a.Pos()) + "(" + deref(v, t) + ")", true}
	case p.Verb == 'f':
		digits := max(p.Prec, 0)
		if p.Prec < 0 {
			digits = 6 // as Go prints %f
		}

		return text{"sn_f64_fixed(" + deref(v, t) + ", " + strconv.Itoa(digits) + ")", true}
	case p.Verb == 'v' && p.Prec >= 0 && info&types.IsFloat != 0:
		return text{"sn_f64_g(" + deref(v, t) + ", " + prec + ")", true}
	case p.Verb == 'q' && info&types.IsInteger != 0:
		return text{"sn_quote_rune((int64_t)(" + deref(v, t) + "))", true}
	case p.Verb == 'q':
		s := truncated(e.text(v, a))
		if s.owned {
			return text{"sn_quote_owned(" + s.s + ")", true}
		}

		return text{"sn_quote(" + s.s + ")", true}
	case p.Verb == 'd': // the number, never a String method's text, as Go's %d prints it
		digits := "sn_int_to_string((int64_t)(" + deref(v, t) + "))"
		if info&types.IsUnsigned != 0 {
			digits = "sn_uint_to_string((uint64_t)(" + deref(v, t) + "))"
		}

		if p.Prec >= 0 {
			digits = "sn_int_digits(" + digits + ", " + prec + ")"
		}

		return text{digits, true}
	}

	return truncated(e.text(v, a))
}
