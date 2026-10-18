// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package c emits C source for a checked core Teak program and
// provides the runtime it links against. It is the backend of stages 1
// to 3 (_docs/design/01-language-design.md, "Backend: C first, LLVM
// later"); stage 4 replaces it with LLVM IR and keeps the runtime.
//
// The lowering so far: numbers are C integers and doubles, a string is
// the runtime's tk_string view, a ref T is passed as a T and a mut T as
// a T*, a function is a static C function, and fmt's printing is a
// sequence of runtime calls chosen by the argument types. Constructs
// the emitter does not handle yet are reported as errors, never
// emitted wrong. The emitter never decides anything about ownership;
// it reads the types the checker recorded.
package c

import (
	"bytes"
	"embed"
	"strconv"
	"strings"

	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/token"
	"github.com/teak-lang/teak/types"
)

//go:embed runtime/teak.h runtime/teak.c
var runtimeFiles embed.FS

// Runtime returns the runtime's header and source, to be written next
// to the emitted program and compiled with it.
func Runtime() (header, source []byte) {
	header, _ = runtimeFiles.ReadFile("runtime/teak.h")
	source, _ = runtimeFiles.ReadFile("runtime/teak.c")
	return header, source
}

// An Emitter writes one program. Function bodies go into body;
// prototypes and type definitions, which C needs before use, go into
// their own sections and are written out first.
type Emitter struct {
	info   *types.Info
	file   *token.File
	types  bytes.Buffer // typedefs
	protos bytes.Buffer // function prototypes
	body   bytes.Buffer // function definitions
	tmp    int
	used   map[types.Object]bool
	result types.Type // the result type of the function being emitted
	errors []string
}

// cReserved holds the identifiers an emitted program must not reuse:
// C's keywords and the libc and runtime names the output refers to. A
// Teak identifier among them gets a trailing underscore.
var cReserved = map[string]bool{
	"auto": true, "break": true, "case": true, "char": true, "const": true, "continue": true,
	"default": true, "do": true, "double": true, "else": true, "enum": true, "extern": true,
	"float": true, "for": true, "goto": true, "if": true, "inline": true, "int": true, "long": true,
	"register": true, "restrict": true, "return": true, "short": true, "signed": true, "sizeof": true,
	"static": true, "struct": true, "switch": true, "typedef": true, "union": true, "unsigned": true,
	"void": true, "volatile": true, "while": true, "bool": true, "true": true, "false": true,
	"main": true, "printf": true, "malloc": true, "free": true, "exit": true, "memcpy": true,
	"strlen": true, "NULL": true, "stdout": true, "stderr": true, "size_t": true,
}

// Emit writes the C program for a checked file. info must hold the
// Defs, Uses and Types the checker recorded for f; file resolves the
// positions in error messages.
func Emit(file *token.File, f *ast.File, info *types.Info) ([]byte, error) {
	e := &Emitter{info: info, file: file, used: make(map[types.Object]bool, len(info.Uses))}
	for _, obj := range info.Uses {
		e.used[obj] = true
	}

	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.TypeDecl:
			e.typeDecl(d)
		case *ast.FuncDecl:
			e.funcDecl(d)
		case *ast.VarDecl:
			e.unsupported(d.Pos(), "top-level var and const")
		}
	}

	if len(e.errors) > 0 {
		return nil, &Error{e.errors}
	}

	var out bytes.Buffer
	out.WriteString("// Emitted by the Teak compiler from " + file.Name() + ". Do not edit.\n\n#include \"teak.h\"\n\n")
	out.Write(e.types.Bytes())
	out.Write(e.protos.Bytes())
	out.WriteString("\n")
	out.Write(e.body.Bytes())
	out.WriteString("int main(void) {\n\ttk_main();\n\treturn 0;\n}\n")
	return out.Bytes(), nil
}

// An Error lists the constructs the emitter cannot lower yet, one per
// line with its position.
type Error struct {
	Unsupported []string
}

func (e *Error) Error() string {
	return strings.Join(e.Unsupported, "\n")
}

// unsupported records a construct the backend does not handle yet.
func (e *Emitter) unsupported(pos token.Pos, what string) {
	e.errors = append(e.errors, e.file.Position(pos).String()+": the C backend does not support "+what+" yet")
}

func (e *Emitter) typeDecl(d *ast.TypeDecl) {
	named := e.info.Defs[d.Name].(*types.TypeName).Type().(*types.Named)
	switch named.Underlying().(type) {
	case *types.Basic:
		e.types.WriteString("typedef " + e.cType(named.Underlying(), d.Pos()) + " " + ident(d.Name.Name) + ";\n")
	default:
		e.unsupported(d.Pos(), "struct and enum declarations")
	}
}

// funcDecl writes a function: a prototype in the prototypes section
// and the definition in the body. Teak's main becomes tk_main, which
// the C main calls.
func (e *Emitter) funcDecl(d *ast.FuncDecl) {
	fn := e.info.Defs[d.Name].(*types.Func)
	sig := fn.Type().(*types.Signature)
	if sig.Recv() != nil {
		e.unsupported(d.Pos(), "methods")
		return
	}

	name := ident(d.Name.Name)
	if d.Name.Name == "main" {
		name = "tk_main"
	}

	head := "static TK_UNUSED " + e.resultType(sig, d.Pos()) + " " + name + "(" + e.params(sig, d.Pos()) + ")"
	e.protos.WriteString(head + ";\n")
	e.line(head + " {")
	for _, p := range sig.Params() {
		if p.Name() != "" && !e.used[p] {
			e.line("(void)" + ident(p.Name()) + ";")
		}
	}

	outer := e.result
	e.result = sig.Results()
	e.stmts(d.Body.List)
	e.result = outer
	e.line("}")
	e.line("")
}

func (e *Emitter) params(sig *types.Signature, pos token.Pos) string {
	if len(sig.Params()) == 0 {
		return "void"
	}

	parts := make([]string, len(sig.Params()))
	for i, p := range sig.Params() {
		parts[i] = e.cType(p.Type(), pos)
		if p.Name() != "" {
			parts[i] += " " + ident(p.Name())
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

// cType writes the C type for a Teak type; a kind the backend does not
// lower yet is reported and written as void so the output still reads.
func (e *Emitter) cType(t types.Type, pos token.Pos) string {
	switch t := t.(type) {
	case *types.Basic:
		return basicType(t)
	case *types.Named:
		if _, ok := t.Underlying().(*types.Basic); ok {
			return ident(t.Obj().Name())
		}
	case *types.Ref:
		if t.Mut() {
			return e.cType(t.Elem(), pos) + " *"
		}

		return e.cType(t.Elem(), pos)
	}

	e.unsupported(pos, "values of type "+t.String())
	return "void"
}

func basicType(t *types.Basic) string {
	switch t.Kind() {
	case types.Bool:
		return "bool"
	case types.String:
		return "tk_string"
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
	}

	return "void"
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

// ident writes a Teak identifier as a C identifier.
func ident(name string) string {
	if cReserved[name] {
		return name + "_"
	}

	return name
}
