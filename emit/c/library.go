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

// The core library is a table in the emitter, as
// _docs/design/05-standard-library.md says: fmt's printing is lowered
// onto the runtime's printers with the format read at compile time, and
// the os and io functions onto runtime functions that return an error
// message, empty on success. The library written in Teak replaces this
// file.

// libraryFunctions maps the os and io functions onto the runtime; stores
// says whether the function has a value to store through its last
// argument.
var libraryFunctions = map[string]struct {
	runtime string
	stores  bool
}{
	"os.ReadToString": {"tk_read_to_string", true},
	"os.Open":         {"tk_open", true},
	"os.Create":       {"tk_create", true},
	"os.MkdirTemp":    {"tk_mkdir_temp", true},
	"os.RemoveAll":    {"tk_remove_all", false},
	"io.Copy":         {"tk_io_copy", true},
}

// libraryCall lowers the core library: fmt's printing becomes
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

	if lib, ok := libraryFunctions[pkg.Path()+"."+name]; ok {
		fn := e.info.Uses[x.Fun.(*ast.SelectorExpr).Sel].(*types.Func)
		args := e.args(nil, x.Args, fn.Type().(*types.Signature))
		return e.libResult(x, func(out string) string {
			if !lib.stores {
				return lib.runtime + "(" + args + ")"
			}

			return lib.runtime + "(" + args + ", " + out + ")"
		})
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

// libraryMethod lowers Close, Sync and WriteString on a file; args
// holds the receiver and the arguments, written.
func (e *Emitter) libraryMethod(x *ast.CallExpr, name, args string) string {
	switch name {
	case "Close":
		return e.libResult(x, func(out string) string { return "tk_file_close(" + args + ")" })
	case "Sync":
		return e.libResult(x, func(out string) string { return "tk_file_sync(" + args + ")" })
	case "WriteString":
		return e.libResult(x, func(out string) string { return "tk_file_write_string(" + args + ", " + out + ")" })
	}

	e.unsupported(x.Pos(), "the method "+name+" on os.File")
	return "0"
}

// libResult writes a call of a runtime function that returns an error
// message, empty on success, and stores its value through a pointer,
// as the Result the library function promises.
func (e *Emitter) libResult(x *ast.CallExpr, call func(out string) string) string {
	rt := e.info.Types[x]
	r := rt.Underlying().(*types.Result)
	val, err, out := e.temp(), e.temp(), e.temp()
	e.line(e.cType(r.Val(), x.Pos()) + " " + val + " = " + zeroValue + ";")
	e.line("tk_string " + err + " = " + call("&"+val) + ";")
	e.line(e.cType(rt, x.Pos()) + " " + out + ";")
	e.line("if (" + err + ".len == 0) {")
	e.line(out + ".ok = true;")
	e.line(out + ".val = " + val + ";")
	e.line("} else {")
	e.line(out + ".ok = false;")
	e.line(out + ".err = " + err + ";")
	e.line("}")
	return out
}
