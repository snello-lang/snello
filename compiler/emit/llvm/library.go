// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

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
// writes them in IR: fmt's printing as the runtime's printers over the
// text of each argument, os.Exit as the runtime's exit, and the
// primitives of rc and sync as the pointer itself.

// libraryCall writes the call x of the lowered function key.
func (e *Emitter) libraryCall(x *ast.CallExpr, key string) value {
	switch key {
	case "fmt.Println": // the line built whole and written at once, so two tasks' lines do not interleave
		e.print(text{e.sprintln(x.Args, x.Pos()), true})
		return unit
	case "fmt.Sprint":
		return e.sprint(x.Args, x.Pos())
	case "fmt.Sprintf":
		return e.sprintf(x.Args[0], x.Args[1:])
	case "fmt.Printf":
		e.print(text{e.sprintf(x.Args[0], x.Args[1:]), true})
		return unit
	case "fmt.Fprintln":
		e.fprint(x, e.sprintln(x.Args[1:], x.Pos()))
		return unit
	case "fmt.Fprintf":
		e.fprint(x, e.sprintf(x.Args[1], x.Args[2:]))
		return unit
	case "os.Exit":
		code := e.op("trunc "+e.expr(x.Args[0]).typed()+" to i32", "i32")
		e.rt("sn_exit", "void", code)
		e.inst("unreachable")
		e.fn.terminated = true
		return unit
	case "rc.alias", "sync.alias", "sync.inside": // a second copy of the pointer a borrow lends, owned by the caller, or the cell it points at as a mut: both are the pointer read through the borrow
		t := e.typeOf(x.Args[0])
		if r, ok := t.(*types.Ref); ok {
			t = r.Elem()
		}

		return e.load("ptr", e.operands(x.Args, []types.Type{types.NewRef(t, false)})[0].s)
	case "rc.leak", "sync.leak": // the pointer moves in and nothing frees it
		e.operands(x.Args, []types.Type{e.typeOf(x.Args[0])})
		return unit
	case "testing.Logf", "expect.Failf": // the format read at compile time as fmt's are, the line handed to the unformatted function with the place of this call
		fn := e.info.Uses[x.Fun.(*ast.SelectorExpr).Sel].(*types.Func)
		e.locate(x, fn)
		plain := "Log"
		if key == "expect.Failf" {
			plain = "Fail"
		}

		line := e.sprintf(x.Args[0], x.Args[1:])
		target := fn.Pkg().Scope().Lookup(plain).(*types.Func)
		e.inst("call void @" + lower.ObjName(target) + "(" + line.typed() + ")") // a ref string is the three words themselves
		e.dropOf(e.spill(line), types.Typ[types.String], x.Pos())
		return unit
	case "testing.Catch": // the function runs under the runtime's mark, called through an adapter as the sort's less is
		sig := lower.ValueType(e.typeOf(x.Args[0]))
		f := e.spill(e.operands(x.Args, []types.Type{e.typeOf(x.Args[0])})[0])
		str := e.irType(types.Typ[types.String], x.Pos())
		msg := e.slot(str)
		e.store(value{str, "zeroinitializer"}, msg) // the payload of a None, as the C side's {.has = false} leaves it
		has := e.rt("sn_catch", "i1", value{"ptr", "@" + e.catchAdapter(sig, x.Pos())}, value{"ptr", f}, value{"ptr", msg})
		out := e.insert(value{e.irType(e.typeOf(x), x.Pos()), "zeroinitializer"}, has, 0)
		return e.insert(out, e.load(str, msg), 1)
	}

	e.unsupported(x.Pos(), key)
	return unit
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
		e.rt("sn_testing_line", "void", e.constString(where))
		return
	}

	where += ": " + ast.Source(x)
	e.rt("sn_testing_call", "void", e.constString(where), e.parts(x, fn.Name()))
}

// parts writes the parts of an assertion as lower.Parts decides them:
// the type compared, or the operands with their values, "n is 2,
// limit is 3", or nothing.
func (e *Emitter) parts(x *ast.CallExpr, name string) value {
	typed, operands := lower.Parts(e.info, x, name)
	if typed {
		return e.constString(lower.ValueType(e.typeOf(x.Args[0])).String())
	}

	if len(operands) == 0 {
		return e.constString("")
	}

	var pieces []piece
	for i, operand := range operands {
		if i > 0 {
			pieces = append(pieces, piece{lit: ", "})
		}

		pieces = append(pieces, piece{lit: ast.Source(operand) + " is "}, piece{text: e.textOf(e.expr(operand), e.typeOf(operand), operand.Pos(), false)})
	}

	return e.concat(pieces, x.Pos())
}

// catchAdapter returns the name of the function that lets sn_catch
// call a Snello function value held in the context, written once.
func (e *Emitter) catchAdapter(sig types.Type, pos token.Pos) string {
	name := "sn_catchfn_" + lower.Mangle(sig)
	if e.state[name] {
		return name
	}

	e.state[name] = true
	pair := e.irType(sig, pos)
	e.helper(name, "void", []string{"ptr %ctx"}, func() {
		fn := e.load("ptr", e.gep(pair, "%ctx", 0))
		e.inst("call void " + fn.s + "(ptr " + e.gep(pair, "%ctx", 1) + ")")
		e.inst("ret void")
		e.fn.terminated = true
	})
	return name
}

// print writes a text to standard output, freed after when built here.
func (e *Emitter) print(s text) {
	if s.owned {
		e.callWords("sn_print_owned", s.v)
	} else {
		e.callWords("sn_print_string", s.v)
	}
}

// sprint joins the arguments as Sprint does: a space between two
// operands when neither is a string. The result is built here; a
// string argument is cloned, so the result is one owned string.
func (e *Emitter) sprint(args []ast.Expr, pos token.Pos) value {
	parts := e.operands(args, nil)
	var pieces []piece
	for i, a := range args {
		if i > 0 && !isString(lower.ValueType(e.typeOf(a))) && !isString(lower.ValueType(e.typeOf(args[i-1]))) {
			pieces = append(pieces, piece{lit: " "})
		}

		pieces = append(pieces, piece{text: e.textOf(parts[i], e.typeOf(a), a.Pos(), false)})
	}

	return e.concat(pieces, pos)
}

// sprintln joins the arguments as Println prints them: a space between
// every two, and a newline at the end.
func (e *Emitter) sprintln(args []ast.Expr, pos token.Pos) value {
	parts := e.operands(args, nil)
	var pieces []piece
	for i, a := range args {
		if i > 0 {
			pieces = append(pieces, piece{lit: " "})
		}

		pieces = append(pieces, piece{text: e.textOf(parts[i], e.typeOf(a), a.Pos(), false)})
	}

	return e.concat(append(pieces, piece{lit: "\n"}), pos)
}

// fprint writes the owned string s to the writer x.Args[0] names, a mut
// borrow of a type implementing io.Writer, through its Write method
// over a view of the string's bytes; the result is dropped, as
// Fprintln has none.
func (e *Emitter) fprint(x *ast.CallExpr, s value) {
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
	tmp := e.spill(s)
	view := e.insert(value{"%sn_slice", "zeroinitializer"}, e.extract(s, 0, "ptr"), 0)
	view = e.insert(view, e.extract(s, 1, "i64"), 1)
	r := e.callValues(name, sig, []types.Type{sig.Recv().Type(), sig.Params()[0].Type()}, []value{recv, view}, x.Pos()) // a ref []byte is the view itself
	e.discard(r, sig.Results(), x.Pos())
	e.dropOf(tmp, types.Typ[types.String], x.Pos())
}

// sprintf reads the format at compile time and writes the
// concatenation it stands for. Verbs: %d, %s, %v, %q, %%, and %f with
// a precision, %.2f, for a float; a width pads with spaces on the left,
// %6d, or on the right after a minus, %-6s.
func (e *Emitter) sprintf(f ast.Expr, args []ast.Expr) value {
	format, ok := types.FormatText(e.info, f)
	pieces, err := types.ParseFormat(format)
	if !ok || err != nil || types.FormatCount(pieces) != len(args) {
		e.unsupported(f.Pos(), "a format the checker refuses") // the checker reported it
		return e.constString("")
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
			part = text{e.rt("sn_pad", "%sn_string", part.v, value{"i64", strconv.Itoa(p.Width)}, value{"i1", strconv.FormatBool(p.Minus)}, value{"i1", strconv.FormatBool(part.owned)}), true}
		}

		out = append(out, piece{text: part})
	}

	return e.concat(out, f.Pos())
}

// verb writes the text of the argument a, whose value is v, as the verb
// of p prints it, with its precision; the checker has made sure the
// verb fits the argument's type.
func (e *Emitter) verb(p types.FormatPiece, a ast.Expr, v value) text {
	t := e.typeOf(a)
	vt := lower.ValueType(t)
	prec := value{"i64", strconv.Itoa(p.Prec)}
	float := func() value {
		f := e.derefTo(v, t, vt)
		if f.typ == "float" {
			f = e.op("fpext "+f.typed()+" to double", "double")
		}

		return f
	}

	truncated := func(s text) text {
		if p.Prec < 0 {
			return s
		}

		return text{e.rt("sn_truncate", "%sn_string", s.v, prec, value{"i1", strconv.FormatBool(s.owned)}), s.owned}
	}

	switch {
	case p.Verb == 'f':
		digits := max(p.Prec, 0)
		if p.Prec < 0 {
			digits = 6 // as Go prints %f
		}

		return text{e.rt("sn_f64_fixed", "%sn_string", float(), value{"i32", strconv.Itoa(digits)}), true}
	case p.Verb == 'v' && p.Prec >= 0 && isFloat(vt):
		return text{e.rt("sn_f64_g", "%sn_string", float(), prec), true}
	case p.Verb == 'q' && isInteger(vt):
		return text{e.rt("sn_quote_rune", "%sn_string", e.widen(e.derefTo(v, t, vt), !isUnsigned(vt))), true}
	case p.Verb == 'q':
		s := truncated(e.textOf(v, t, a.Pos(), false))
		if s.owned {
			return text{e.rt("sn_quote_owned", "%sn_string", s.v), true}
		}

		return text{e.rt("sn_quote", "%sn_string", s.v), true}
	case p.Verb == 'd': // the number, never a String method's text, as Go's %d prints it
		toString, signed := "sn_int_to_string", true
		if isUnsigned(vt) {
			toString, signed = "sn_uint_to_string", false
		}

		digits := e.rt(toString, "%sn_string", e.widen(e.derefTo(v, t, vt), signed))

		if p.Prec >= 0 {
			digits = e.rt("sn_int_digits", "%sn_string", digits, prec)
		}

		return text{digits, true}
	}

	return truncated(e.textOf(v, t, a.Pos(), false))
}
