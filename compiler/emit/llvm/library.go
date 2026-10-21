// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"strconv"

	"github.com/teak-lang/teak/compiler/ast"
	"github.com/teak-lang/teak/compiler/emit/lower"
	"github.com/teak-lang/teak/compiler/token"
	"github.com/teak-lang/teak/compiler/types"
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
		e.rt("tk_exit", "void", code)
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
	}

	e.unsupported(x.Pos(), key)
	return unit
}

// print writes a text to standard output, freed after when built here.
func (e *Emitter) print(s text) {
	if s.owned {
		e.callWords("tk_print_owned", s.v)
	} else {
		e.callWords("tk_print_string", s.v)
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
	view := e.insert(value{"%tk_slice", "zeroinitializer"}, e.extract(s, 0, "ptr"), 0)
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
			part = text{e.rt("tk_pad", "%tk_string", part.v, value{"i64", strconv.Itoa(p.Width)}, value{"i1", strconv.FormatBool(p.Minus)}, value{"i1", strconv.FormatBool(part.owned)}), true}
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

		return text{e.rt("tk_truncate", "%tk_string", s.v, prec, value{"i1", strconv.FormatBool(s.owned)}), s.owned}
	}

	switch {
	case p.Verb == 'f':
		digits := max(p.Prec, 0)
		if p.Prec < 0 {
			digits = 6 // as Go prints %f
		}

		return text{e.rt("tk_f64_fixed", "%tk_string", float(), value{"i32", strconv.Itoa(digits)}), true}
	case p.Verb == 'v' && p.Prec >= 0 && isFloat(vt):
		return text{e.rt("tk_f64_g", "%tk_string", float(), prec), true}
	case p.Verb == 'q' && isInteger(vt):
		return text{e.rt("tk_quote_rune", "%tk_string", e.widen(e.derefTo(v, t, vt), !isUnsigned(vt))), true}
	case p.Verb == 'q':
		s := truncated(e.textOf(v, t, a.Pos(), false))
		if s.owned {
			return text{e.rt("tk_quote_owned", "%tk_string", s.v), true}
		}

		return text{e.rt("tk_quote", "%tk_string", s.v), true}
	case p.Verb == 'd': // the number, never a String method's text, as Go's %d prints it
		toString, signed := "tk_int_to_string", true
		if isUnsigned(vt) {
			toString, signed = "tk_uint_to_string", false
		}

		digits := e.rt(toString, "%tk_string", e.widen(e.derefTo(v, t, vt), signed))

		if p.Prec >= 0 {
			digits = e.rt("tk_int_digits", "%tk_string", digits, prec)
		}

		return text{digits, true}
	}

	return truncated(e.textOf(v, t, a.Pos(), false))
}
