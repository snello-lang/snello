// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package c emits C source for a checked core Teak program and
// provides the runtime it links against. It is the first backend
// (_docs/design/01-language-design.md, "Backend: C first, LLVM
// later"); LLVM IR replaces it and keeps the runtime.
//
// The lowering: numbers are C integers and doubles, a string is the
// runtime's tk_string view, a struct is a C struct, an enum a tagged
// union, an option a struct with a flag, a Result a flag over a union,
// a tuple a struct. A ref T is passed as a T and a mut T or a *T as a
// T*. A function is a static C function and a method one named after
// its type. match, ?, ?? and if expressions become statements that
// leave their value in a temporary, and fmt's printing is a sequence
// of runtime calls chosen by the argument types. Constructs the
// emitter does not handle yet are reported as errors, never emitted
// wrong. The emitter never decides anything about ownership; it reads
// the types the checker recorded.
package c

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/teak-lang/teak/compiler/ast"
	"github.com/teak-lang/teak/compiler/emit/lower"
	"github.com/teak-lang/teak/compiler/token"
	"github.com/teak-lang/teak/compiler/types"
)

// An Emitter writes one program. C needs a type complete before a
// value of it is declared and a function declared before a call, so
// declarations, type definitions, prototypes and helper functions go
// into sections of their own, written out before the function bodies.
type Emitter struct {
	info     *types.Info
	file     *token.File
	decls    bytes.Buffer // forward declarations of struct types
	types    bytes.Buffer // type definitions, each after the ones it holds by value
	protos   bytes.Buffer // function prototypes
	helpers  bytes.Buffer // generated functions, such as the printers of composite types
	body     bytes.Buffer // function definitions
	boxWords int          // the words a function value's box has: the program's largest closure of copies, at least minBoxWords
	tmp      int
	used     map[types.Object]bool
	state    map[string]int                  // of each C type and helper name: undeclared, declared, defining, defined
	seen     map[string]bool                 // the unsupported constructs reported so far
	result   types.Type                      // the result type of the function being emitted
	loops    []string                        // the loops open at this point: the label continue jumps to, or empty for C's continue
	captured map[*types.Var]string           // inside a closure: how each captured variable is reached
	closures int                             // closures written so far, numbering their functions
	tasks    int                             // spawn statements written so far, numbering their trampolines
	prologue []string                        // declarations to put at the top of the function being written
	pre      map[ast.Expr]string             // expressions already evaluated into temporaries, for defer
	defers   []deferred                      // the deferred calls of the function being written, in order
	subst    map[*types.TypeParam]types.Type // the type arguments of the instance being written (mono.go)
	generics map[*types.Func]*ast.FuncDecl   // the generic functions and methods, written per instance
	files    map[*ast.FuncDecl]*token.File   // the source file of each generic declaration, for the positions in an instance's errors
	external map[*types.Var]bool             // the library variables the runtime defines, read through their runtime declaration
	statics  []owned                         // the top-level variables owning something, dropped when main returns
	done     map[string]bool                 // the instances asked for, by C name
	work     []instance                      // the instances still to write
	frames   []frame                         // the scopes open at this point, with the variables to drop at their end (drop.go)
	temps    []owned                         // the temporaries of the statement being written, dropped at its end
	guards   map[*types.Named]bool           // whether dropping a declared type runs a Drop method, which a panic must still run
	errors   []string
}

// A deferred call is a flag that says the defer statement ran and the
// lines that make the call.
type deferred struct {
	flag string
	code string
}

// Emit writes the C program for a checked file and the library
// packages it imports, each after the ones it imports, into one C
// file with the library names qualified by their import paths. info
// must hold the Defs, Uses and Types the checker recorded for all of
// them; the files resolve the positions in error messages.
func Emit(file *token.File, f *ast.File, info *types.Info, libraries ...lower.Library) ([]byte, error) {
	e := &Emitter{
		info:     info,
		file:     file,
		used:     make(map[types.Object]bool, len(info.Uses)),
		state:    make(map[string]int),
		seen:     make(map[string]bool),
		pre:      make(map[ast.Expr]string),
		guards:   make(map[*types.Named]bool),
		generics: make(map[*types.Func]*ast.FuncDecl),
		files:    make(map[*ast.FuncDecl]*token.File),
		external: make(map[*types.Var]bool),
		done:     make(map[string]bool),
	}
	e.state["tk_slice_u8"] = defined // the runtime defines []byte in teak.h, as the library's bodiless functions take it
	for _, obj := range info.Uses {
		e.used[obj] = true
	}

	units := append(libraries, lower.Library{File: file, Syntax: f})
	for _, u := range units {
		e.file = u.File
		for _, d := range u.Syntax.Decls {
			switch d := d.(type) {
			case *ast.TypeDecl:
				if len(d.TypeParams) == 0 {
					e.typeDecl(d) // an instance of a generic type is declared when a value of it is first written
				}
			case *ast.FuncDecl:
				e.generic(d, info.Defs[d.Name].(*types.Func)) // noted before any body can call it
			case *ast.TraitDecl:
				for _, m := range d.Methods {
					if fn, ok := info.Defs[m.Name].(*types.Func); ok && m.Body != nil {
						e.generics[fn], e.files[m] = m, u.File // a default method is written once per implementing type
					}
				}
			case *ast.VarDecl:
				e.varDecl(d, u.Syntax != f)
			}
		}
	}

	for _, u := range units {
		e.file = u.File
		for _, d := range u.Syntax.Decls {
			if d, ok := d.(*ast.FuncDecl); ok && e.generics[info.Defs[d.Name].(*types.Func)] == nil {
				e.funcDecl(d)
			}
		}
	}

	e.instances()
	finish := "\ttk_main();\n"
	if len(e.statics) > 0 {
		finish += "\ttk_statics_drop();\n"
		var b strings.Builder
		for _, s := range e.statics {
			put(&b, "\t", e.dropOf(s.name, s.typ, token.NoPos), "\n")
		}

		put(&e.helpers, "static void tk_statics_drop(void) {\n", b.String(), "}\n\n") // after the droppers it asks for
	}

	if len(e.errors) > 0 {
		return nil, &Error{e.errors}
	}

	var out bytes.Buffer
	put(&out, "// Emitted by the Teak compiler from ", file.Name(), ". Do not edit.\n\n#include \"teak.h\"\n\n")
	put(&out, "#define TK_BOX_WORDS ", strconv.Itoa(max(e.boxWords, minBoxWords)), " // every function value carries this many words for a closure's copies\n\n")
	out.Write(e.decls.Bytes())
	out.WriteString("\n")
	out.Write(e.types.Bytes())
	out.Write(e.protos.Bytes())
	out.WriteString("\n")
	out.Write(e.helpers.Bytes())
	out.Write(e.body.Bytes())
	out.WriteString("int main(int argc, char **argv) {\n\ttk_init(argc, argv);\n" + finish + "\treturn tk_finish();\n}\n")
	return out.Bytes(), nil
}

// An Error lists the constructs the emitter cannot lower yet, one per
// line with the position of its first use.
type Error struct {
	Unsupported []string
}

func (e *Error) Error() string {
	return strings.Join(e.Unsupported, "\n")
}

// unsupported records a construct the backend does not handle yet,
// once per construct: the first position is enough to act on.
func (e *Emitter) unsupported(pos token.Pos, what string) {
	if e.seen[what] {
		return
	}

	e.seen[what] = true
	e.errors = append(e.errors, e.file.Position(pos).String()+": the C backend does not support "+what+" yet")
}

// typeDecl declares a type: a struct or enum is forward-declared here
// and defined when first used, so types may refer to each other in
// any order; any other declared type is a typedef written at once.
func (e *Emitter) typeDecl(d *ast.TypeDecl) {
	named := e.info.Defs[d.Name].(*types.TypeName).Type().(*types.Named)
	switch named.Underlying().(type) {
	case *types.Struct, *types.Enum:
		name := lower.NamedName(named)
		e.state[name] = declared
		put(&e.decls, "typedef struct ", name, " ", name, ";\n")
	default:
		e.define(named, d.Pos())
	}
}

// varDecl writes a top-level variable or constant as a static C
// variable, in the prototypes section so that a declared type it has is
// defined first. The value must be a literal: a number, character,
// boolean or string, a negated number, or a struct of such, since
// anything else would need a computation, which C does not run before
// main. A library variable declared without a value is the runtime's,
// defined in its header under the variable's name, as a function
// without a body is; a program's variable without a value starts at
// zero. A variable that may come to own something, by assignment, is
// dropped when main returns, so that a debug build reports what the
// program itself leaked.
func (e *Emitter) varDecl(d *ast.VarDecl, library bool) {
	for i, name := range d.Names {
		obj := e.info.Defs[name]
		if len(d.Values) == 0 && library {
			e.external[obj.(*types.Var)] = true
			continue
		}

		value := zeroValue
		if i < len(d.Values) {
			if value = e.staticValue(d.Values[i], obj.Type()); value == "" {
				e.unsupported(name.Pos(), "a top-level var or const whose value is not a literal")
				return
			}
		}

		qualifier := "static TK_UNUSED " // a library's streams may go unused by a program
		if d.Const {
			qualifier += "const "
		} else if needsDrop(obj.Type()) {
			e.statics = append(e.statics, owned{lower.ObjName(obj), obj.Type()})
		}

		put(&e.protos, qualifier, e.cType(obj.Type(), name.Pos()), " ", lower.ObjName(obj), " = ", value, ";\n")
	}
}

// staticValue writes x as a C initializer a static variable of type t
// can have: a number, character, boolean or string literal, the
// negation of a number, or a struct literal of such, with the live flag
// of a Drop type set. A string is written as the fields of a tk_string
// over the C literal, since the TK_STR macro is a compound literal,
// which C does not allow at file scope. It is empty for anything else.
func (e *Emitter) staticValue(x ast.Expr, t types.Type) string {
	switch x := x.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return e.literal(x)
		}

		if s, err := strconv.Unquote(x.Value); err == nil {
			return "{(const uint8_t *)" + cString(s) + ", " + strconv.Itoa(len(s)) + ", 0}"
		}
	case *ast.Ident:
		if x.Name == "true" || x.Name == "false" {
			return x.Name
		}
	case *ast.UnaryExpr:
		if v := e.staticValue(x.X, t); x.Op == token.SUB && v != "" {
			if minMagnitude(v) {
				return "INT64_MIN" // the one magnitude C cannot negate as a signed literal
			}

			return "-" + v
		}
	case *ast.ParenExpr:
		return e.staticValue(x.X, t)
	case *ast.CompositeLit:
		st, ok := t.Underlying().(*types.Struct)
		if !ok {
			return ""
		}

		var parts []string
		for _, elt := range x.Elts {
			kv := elt.(*ast.KeyValueExpr)
			key := kv.Key.(*ast.Ident)
			v := e.staticValue(kv.Value, fieldType(st, key.Name))
			if v == "" {
				return ""
			}

			parts = append(parts, "."+lower.Ident(key.Name)+" = "+v)
		}

		if hasDrop(t) {
			parts = append(parts, ".tk_live = true")
		}

		if len(parts) == 0 {
			return zeroValue
		}

		return "{" + strings.Join(parts, ", ") + "}"
	}

	return ""
}

// funcDecl writes a function: a prototype in the prototypes section
// and the definition in the body. Teak's main becomes tk_main, which
// the C main calls; a method is a function named after its type whose
// first parameter is the receiver. A library function declared without
// a body is the runtime's, declared in its header under the same name,
// so nothing is written for it.
func (e *Emitter) funcDecl(d *ast.FuncDecl) {
	if d.Body == nil {
		return
	}

	fn := e.info.Defs[d.Name].(*types.Func)
	sig := fn.Type().(*types.Signature)
	name := lower.FuncName(fn)
	if recv := sig.Recv(); recv != nil {
		name = lower.MethodName(recv.Type(), d.Name.Name)
	}

	e.writeFunction(name, sig, d.Body, d.Pos())
}

// writeFunction writes a function or method under the given C name:
// its prototype, its head with the receiver as the first parameter,
// and its body.
func (e *Emitter) writeFunction(name string, sig *types.Signature, body *ast.BlockStmt, pos token.Pos) {
	params := sig.Params()
	if recv := sig.Recv(); recv != nil {
		params = append([]*types.Var{recv}, params...)
	}

	head := "static TK_UNUSED " + e.resultType(sig, pos) + " " + name + "(" + e.params(params, pos) + ")"
	put(&e.protos, head, ";\n")
	e.line(head + " {")
	e.function(sig, body)
	e.line("")
}

// function writes the body of a function or closure whose head was
// just written: a (void) for each parameter nothing refers to, since
// the C compiler is asked to treat an unused one as an error, the
// statements, and for a body that falls off its end the deferred calls
// and then the drops of its parameters and locals, and the
// declarations the body asked for at the top. The caller's own state
// is kept.
func (e *Emitter) function(sig *types.Signature, body *ast.BlockStmt) {
	outerResult, outerLoops, outerPrologue, outerDefers := e.result, e.loops, e.prologue, e.defers
	outerFrames, outerTemps := e.frames, e.temps
	e.result, e.loops, e.prologue, e.defers, e.frames, e.temps = sig.Results(), nil, nil, nil, nil, nil
	e.push(false)
	start := e.body.Len()
	if recv := sig.Recv(); recv != nil {
		if !e.used[recv] {
			e.line("(void)" + lower.Ident(recv.Name()) + ";")
		}

		e.own(lower.Ident(recv.Name()), recv.Type())
	}

	for _, p := range sig.Params() {
		if p.Name() == "" {
			continue
		}

		if !e.used[p] {
			e.line("(void)" + lower.Ident(p.Name()) + ";")
		}

		e.own(lower.Ident(p.Name()), p.Type())
	}

	e.stmts(body.List)
	if sig.Results() == types.Unit {
		e.runDefers()
		e.pop(body.Rbrace)
	} else {
		e.frames = e.frames[:len(e.frames)-1] // the body ended with a statement that left, which dropped everything
	}

	e.line("}")
	if len(e.prologue) > 0 {
		rest := append([]byte(nil), e.body.Bytes()[start:]...)
		e.body.Truncate(start)
		put(&e.body, strings.Join(e.prologue, "\n"), "\n")
		e.body.Write(rest)
	}

	e.result, e.loops, e.prologue, e.defers = outerResult, outerLoops, outerPrologue, outerDefers
	e.frames, e.temps = outerFrames, outerTemps
}

func (e *Emitter) params(params []*types.Var, pos token.Pos) string {
	if len(params) == 0 {
		return "void"
	}

	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = e.cType(p.Type(), pos)
		if p.Name() != "" {
			parts[i] += " " + lower.Ident(p.Name())
		}
	}

	return strings.Join(parts, ", ")
}

func (e *Emitter) resultType(sig *types.Signature, pos token.Pos) string {
	if sig.Results() == types.Unit {
		return "void"
	}

	return e.cType(sig.Results(), pos)
}

func basicType(t *types.Basic) string {
	switch t.Kind() {
	case types.Bool:
		return "bool"
	case types.String, types.Error:
		return "tk_string" // an error is its message for now
	case types.Int8:
		return "int8_t"
	case types.Int16:
		return "int16_t"
	case types.Int32:
		return "int32_t"
	case types.Int64:
		return "int64_t"
	case types.Int:
		return "tk_int"
	case types.Uint8:
		return "uint8_t"
	case types.Uint16:
		return "uint16_t"
	case types.Uint32:
		return "uint32_t"
	case types.Uint64:
		return "uint64_t"
	case types.Uint:
		return "tk_uint"
	case types.Float32:
		return "float"
	case types.Float64:
		return "double"
	case types.Never:
		return "void"
	case types.Local:
		return "uint8_t" // holds nothing, but C has no empty type
	}

	return "void"
}

// A writer is a section or a builder generated C goes into.
type writer interface {
	WriteString(s string) (int, error)
}

// put writes the parts of one piece of generated C, each on its own,
// which is what a buffer is for.
func put(w writer, parts ...string) {
	for _, p := range parts {
		w.WriteString(p)
	}
}

// line writes one line of the current section. Indentation is not
// tracked; the output is for the C compiler, and a reader has the Teak
// source.
func (e *Emitter) line(s string) {
	e.body.WriteString(s)
	e.body.WriteByte('\n')
}

// temp returns a fresh temporary name.
func (e *Emitter) temp() string {
	e.tmp++
	return "tk_t" + strconv.Itoa(e.tmp)
}
