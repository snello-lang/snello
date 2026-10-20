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

// The functions the library declares without a body and the compiler
// writes, as _docs/design/05-standard-library.md says: fmt's printing
// is lowered onto the runtime's printers by the type of each argument,
// with the format read at compile time, so the runtime has no
// formatter; os.Exit onto the runtime's exit, since a function cannot
// say it never returns yet; and the primitives of rc and sync onto the
// pointer itself, since a second owner of one pointer, and a mut
// reached through a ref, are what the language refuses and the
// reference count and the lock are what make them safe there.

// lowered names the library functions the compiler writes, by import
// path and name.
var lowered = map[string]bool{
	"fmt.Println": true, "fmt.Printf": true, "fmt.Sprint": true, "fmt.Sprintf": true,
	"fmt.Fprintln": true, "fmt.Fprintf": true, "os.Exit": true,
	"rc.alias": true, "rc.leak": true, "sync.alias": true, "sync.leak": true, "sync.inside": true,
}

// loweredKey returns the key of fn in lowered, path.Name, or the empty
// string for a function the compiler does not write.
func loweredKey(fn *types.Func) string {
	if fn.Pkg() == nil {
		return ""
	}

	if key := fn.Pkg().Path() + "." + fn.Name(); lowered[key] {
		return key
	}

	return ""
}

// libraryCall writes the call x of the lowered function key.
func (e *Emitter) libraryCall(x *ast.CallExpr, key string) string {
	switch key {
	case "fmt.Println":
		parts := e.evaluated(x.Args)
		for i, a := range x.Args {
			if i > 0 {
				e.line("tk_print_sep();")
			}

			if s := e.text(parts[i], a); s.owned {
				e.line("tk_print_owned(" + s.s + ");")
			} else {
				e.line("tk_print_string(" + s.s + ");")
			}
		}

		e.line("tk_print_nl();")
		return ""
	case "fmt.Sprint":
		return e.sprint(x.Args)
	case "fmt.Sprintf":
		return e.sprintf(x.Args[0], x.Args[1:])
	case "fmt.Printf":
		e.line("tk_print_owned(" + e.sprintf(x.Args[0], x.Args[1:]) + ");")
		return ""
	case "fmt.Fprintln":
		e.fprint(x, e.sprintln(x.Args[1:]))
		return ""
	case "fmt.Fprintf":
		e.fprint(x, e.sprintf(x.Args[1], x.Args[2:]))
		return ""
	case "os.Exit":
		return "tk_exit((int)(" + e.expr(x.Args[0]) + "))"
	case "rc.alias", "sync.alias", "sync.inside": // a second copy of the pointer a borrow lends, owned by the caller, or the cell it points at as a mut: a ref borrow of a pointer is the pointer in C, and so is a mut of what it points at
		t := e.typeOf(x.Args[0])
		if r, ok := t.(*types.Ref); ok {
			t = r.Elem()
		}

		return e.operands(x.Args, []types.Type{types.NewRef(t, false)})[0]
	case "rc.leak", "sync.leak": // the pointer moves in and nothing frees it
		e.line("(void)" + e.operands(x.Args, []types.Type{e.typeOf(x.Args[0])})[0] + ";")
		return ""
	}

	e.unsupported(x.Pos(), key)
	return "0"
}

// toString writes the runtime call that turns a value into a string.
func (e *Emitter) toString(a ast.Expr) string {
	return e.text(e.expr(a), a).s
}

// text writes the conversion of v, the C text of a, to a tk_string.
func (e *Emitter) text(v string, a ast.Expr) text {
	return e.textOf(v, e.typeOf(a), a.Pos(), false)
}

// sprint joins the arguments as Sprint does: a space between two
// operands when neither is a string. The result is built here; a
// string argument is cloned, so the result is one owned string.
func (e *Emitter) sprint(args []ast.Expr) string {
	parts := e.operands(args, nil)
	var out text
	for i, a := range args {
		if i > 0 && !isString(valueType(e.typeOf(a))) && !isString(valueType(e.typeOf(args[i-1]))) {
			out = join(out, text{literal(" "), false})
		}

		out = join(out, e.text(parts[i], a))
	}

	return built(out)
}

// sprintln joins the arguments as Println prints them: a space between
// every two, and a newline at the end.
func (e *Emitter) sprintln(args []ast.Expr) string {
	parts := e.operands(args, nil)
	texts := make([]text, len(args))
	for i, a := range args {
		texts[i] = e.text(parts[i], a)
	}

	return built(joined("", "\n", " ", texts))
}

// fprint writes the owned string s to the writer x.Args[0] names, a mut
// borrow of a type implementing io.Writer, through its Write method
// over a view of the string's bytes; the result is dropped, as
// Fprintln has none.
func (e *Emitter) fprint(x *ast.CallExpr, s string) {
	w := x.Args[0]
	named, ok := valueType(e.typeOf(w)).(*types.Named)
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
	e.line("tk_string " + tmp + " = " + s + ";")
	bytes := "(" + e.cType(types.NewSlice(types.Typ[types.Uint8]), x.Pos()) + "){(uint8_t *)" + tmp + ".ptr, " + tmp + ".len, 0}"
	e.discard(name+"("+recv+", "+bytes+")", sig.Results(), x.Pos())
	e.line("tk_string_drop(&" + tmp + ");")
}

// built writes a text as a string the caller owns: a clone when it
// was only lent.
func built(t text) string {
	switch {
	case t.s == "":
		return literal("")
	case !t.owned:
		return "tk_string_clone(" + t.s + ")"
	}

	return t.s
}

// sprintf reads the format at compile time and writes the
// concatenation it stands for. Verbs: %d, %s, %v, %q, %%, and %f with
// a precision, %.2f, for a float; a width pads with spaces on the left,
// %6d, or on the right after a minus, %-6s.
func (e *Emitter) sprintf(f ast.Expr, args []ast.Expr) string {
	lit, ok := f.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		e.unsupported(f.Pos(), "a format that is not a literal")
		return literal("")
	}

	format, _ := strconv.Unquote(lit.Value)
	parts := e.operands(args, nil)
	var out text
	plain := ""
	flush := func() {
		if plain != "" {
			out = join(out, text{literal(plain), false})
			plain = ""
		}
	}

	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 >= len(format) {
			plain += string(format[i])
			continue
		}

		i++
		left, width := false, -1
		if format[i] == '-' {
			left = true
			i++
		}

		if i < len(format) && format[i] == '0' {
			e.unsupported(lit.Pos(), "the 0 flag in a format")
		}

		for ; i < len(format) && '0' <= format[i] && format[i] <= '9'; i++ {
			width = max(width, 0)*10 + int(format[i]-'0')
		}

		if i >= len(format) {
			break
		}

		precision := -1
		if format[i] == '.' {
			precision = 0
			for i++; i < len(format) && '0' <= format[i] && format[i] <= '9'; i++ {
				precision = precision*10 + int(format[i]-'0')
			}

			if i >= len(format) {
				break
			}
		}

		verb := format[i]
		if verb == '%' {
			plain += "%"
			continue
		}

		if len(args) == 0 {
			e.unsupported(lit.Pos(), "a format with more verbs than arguments")
			return literal("")
		}

		flush()
		a, v := args[0], parts[0]
		args, parts = args[1:], parts[1:]
		var part text
		switch verb {
		case 'd', 's', 'v':
			part = e.text(v, a)
		case 'f':
			if !isFloat(e.typeOf(a)) {
				e.unsupported(a.Pos(), "%f on a value of type "+e.typeOf(a).String())
			}

			if precision < 0 {
				precision = 6 // as Go prints %f
			}

			part = text{"tk_f64_fixed(" + v + ", " + strconv.Itoa(precision) + ")", true}
		case 'q':
			s := e.text(v, a)
			if s.owned { // the quote reads a string it does not keep
				tmp := e.temp()
				e.line("tk_string " + tmp + " = " + s.s + ";")
				e.hold(tmp, types.Typ[types.String])
				s.s = tmp
			}

			part = text{"tk_quote(" + s.s + ")", true}
		default:
			e.unsupported(lit.Pos(), "the format verb %"+string(verb))
			continue
		}

		if width >= 0 {
			part = text{"tk_pad(" + part.s + ", " + strconv.Itoa(width) + ", " + strconv.FormatBool(left) + ", " + strconv.FormatBool(part.owned) + ")", true}
		}

		out = join(out, part)
	}

	flush()
	return built(out)
}
