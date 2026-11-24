// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package c emits C source for a checked core Snello program and
// provides the runtime it links against. It is the first backend and
// the reference one; the LLVM backend writes IR in its place and keeps
// the runtime.
//
// The lowering: numbers are C integers and doubles, a string is the
// runtime's sn_string view, a struct is a C struct, an enum a tagged
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
	"sort"
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

const (
	// generated is the file name the lines of no Snello position are
	// given, after a function's body: the helpers and the trampolines the
	// backend writes, which the trace of a panic does not show.
	generated = "<generated>"

	// entry is the symbol of the program's entry, which calls main, and
	// generatedLine the line the generated file's lines are at: a line,
	// as the program's own lines have one, where line zero is the
	// optimizer's mark of code merged from two lines.
	entry         = "main"
	generatedLine = 1
)

// An Emitter writes one program. C needs a type complete before a
// value of it is declared and a function declared before a call, so
// declarations, type definitions, prototypes and helper functions go
// into sections of their own, written out before the function bodies.
type Emitter struct {
	info     *types.Info
	file     *token.File
	stdFiles map[*token.File]string // the standard library's files, which the line tables name under lower.SourceName's directory
	quoted   map[*token.File]string // each file's name as the directives write it, quoted once a file
	at       token.Pos              // the Snello position the lines being written are at, for the directive each line carries
	within   string                 // the name the program gives the function being written, for its closures' names
	nclosure int                    // closures of the function being written, counted for their names
	traced   []lower.TraceName      // every function written, its symbol and the name the program gives it
	mainAt   token.Position         // where the program declares main, which the entry is written at
	mainFile *token.File            // and the file it is in
	decls    bytes.Buffer           // forward declarations of struct types
	types    bytes.Buffer           // type definitions, each after the ones it holds by value
	protos   bytes.Buffer           // function prototypes
	helpers  bytes.Buffer           // generated functions, such as the printers of composite types
	body     bytes.Buffer           // function definitions
	scratch  []byte                 // the statements saveBefore holds aside, reused so that holding them allocates nothing
	boxWords int                    // the words a function value's box has: the program's largest closure of copies, at least minBoxWords
	tmp      int
	used     map[types.Object]bool
	state    map[string]int                  // of each C type and helper name: undeclared, declared, defining, defined
	seen     map[string]bool                 // the unsupported constructs reported so far
	result   types.Type                      // the result type of the function being emitted
	loops    []string                        // the loops open at this point: the label continue jumps to, or empty for C's continue
	jumped   map[string]bool                 // the labels a continue has jumped to, the ones written
	lazy     map[*types.Var]bool             // the top-level variables made on first use, since their initializer is a call; reached through a getter
	consts   map[*types.Const]string         // the constants declared in a function, each a static of the file under a name of its own
	lazies   []owned                         // the lazy variables owning something, dropped when main returns if they were made
	making   *types.Var                      // the lazy variable whose initializer is being written, which names the cell itself
	captured map[*types.Var]string           // inside a closure: how each captured variable is reached
	names    naming                          // the C names of locals and parameters (locals.go)
	closures int                             // closures written so far, numbering their functions
	tasks    int                             // spawn statements written so far, numbering their trampolines
	prologue []string                        // declarations to put at the top of the function being written
	pre      map[ast.Expr]string             // expressions already evaluated into temporaries, for defer
	defers   []deferred                      // the deferred calls of the function being written, in order
	seq      int                             // values and deferred calls registered so far, ordering what a return releases
	subst    map[*types.TypeParam]types.Type // the type arguments of the instance being written (mono.go)
	partCopy map[*types.Part]lower.PartCopy  // the copy of each loop over a type's parts being written, its field (stmt.go)
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

// A deferred call is the record its defer statement saved into, whose
// flag says the statement ran; the function that makes the call from
// the record; and when the statement registered it.
type deferred struct {
	rec   string
	thunk string
	seq   int
}

// Emit writes the C program for a checked file, a program of one, and
// the library packages it imports; EmitFiles takes a main package of
// several files.
func Emit(file *token.File, f *ast.File, info *types.Info, libraries ...lower.Library) ([]byte, error) {
	return EmitFiles([]*token.File{file}, []*ast.File{f}, info, libraries...)
}

// EmitFiles writes the C program for a checked main package, its files
// parsed into one FileSet, and the library packages it imports, each
// after the ones it imports, into one C file with the library names
// qualified by their import paths. info must hold the Defs, Uses and
// Types the checker recorded for all of them; the files resolve the
// positions in error messages.
func EmitFiles(files []*token.File, fs []*ast.File, info *types.Info, libraries ...lower.Library) ([]byte, error) {
	if err := lower.CheckPrefixes(libraries); err != nil {
		return nil, err
	}

	e := newEmitter(info, files[0], fs)
	e.stdFiles, e.quoted = lower.StdFiles(libraries), make(map[*token.File]string)
	for _, obj := range info.Uses {
		e.used[obj] = true
	}

	units := append([]lower.Library(nil), libraries...)
	for i, f := range fs {
		units = append(units, lower.Library{File: files[i], Syntax: f})
	}

	for i, u := range units {
		e.file = u.File
		for _, d := range u.Syntax.Decls {
			switch d := d.(type) {
			case *ast.TypeDecl:
				if len(d.TypeParams) == 0 {
					e.typeDecl(d) // an instance of a generic type is declared when a value of it is first written
				}
			case *ast.FuncDecl:
				fn := info.Defs[d.Name].(*types.Func)
				if d.Body == nil && len(u.CFiles) > 0 {
					e.prototype(fn, d.Pos()) // a package's binding to C: the prototype the runtime's header gives a standard package's
				}

				e.generic(d, fn) // noted before any body can call it
			case *ast.TraitDecl:
				for _, m := range d.Methods {
					if fn, ok := info.Defs[m.Name].(*types.Func); ok && m.Body != nil {
						e.generics[fn], e.files[m] = m, u.File // a default method is written once per implementing type
					}
				}
			case *ast.VarDecl:
				e.varDecl(d, i < len(libraries))
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
	finish := "\tsn_main();\n"
	if len(e.statics) > 0 || len(e.lazies) > 0 {
		finish += "\tsn_statics_drop();\n"
		var b strings.Builder
		for _, s := range e.statics {
			put(&b, "\t", e.dropOf(s.name, s.typ, token.NoPos), "\n")
		}

		for _, s := range e.lazies {
			put(&b, "\tif (sn_lazy_ready(&", s.name, "_lazy)) {\n\t\t", e.dropOf(s.name, s.typ, token.NoPos), "\n\t}\n")
		}

		put(&e.helpers, "static void sn_statics_drop(void) {\n", b.String(), "}\n\n") // after the droppers it asks for
	}

	if len(e.errors) > 0 {
		return nil, &Error{e.errors}
	}

	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Name()
	}

	var out bytes.Buffer
	put(&out, "// Emitted by the Snello compiler from ", strings.Join(names, ", "), ". Do not edit.\n\n#include \"snello.h\"\n\nSN_OPEN_CODE // the program's own code, which a task may be preempted in\n\n")
	put(&out, "#define SN_BOX_WORDS ", strconv.Itoa(max(e.boxWords, minBoxWords)), " // every function value carries this many words for a closure's copies\n\n")
	e.stackBounds(&out)
	out.Write(e.decls.Bytes())
	out.WriteString("\n")
	out.Write(e.types.Bytes())
	out.Write(e.protos.Bytes())
	out.WriteString("\n")
	out.Write(e.helpers.Bytes())
	out.Write(e.body.Bytes())
	e.traceNames(&out)
	// The entry is main's, at main's position and under its name: the optimizer writes main into it, and a trace names the entry's code main.
	put(&out, "#line ", strconv.Itoa(e.mainAt.Line), " ", e.directiveName(e.mainFile), "\nint main(int argc, char **argv) {\n#line ", strconv.Itoa(generatedLine), " ", cString(generated), "\n\tsn_init(argc, argv);\n", finish, "\treturn sn_finish();\n}\n")
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
		if e.state[name] == undeclared { // a constant before the declaration may have defined the struct already, which declaring it again would write twice
			e.state[name] = declared
		}

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
		if d.Const && name.Name == "_" {
			continue // its value was checked, and nothing reads it
		}

		obj := e.info.Defs[name]
		if v, ok := obj.(*types.Var); ok {
			e.names.globals[v] = true
		}

		if len(d.Values) == 0 && library {
			e.external[obj.(*types.Var)] = true
			continue
		}

		value := zeroValue
		if i < len(d.Values) {
			if value = e.staticValue(d.Values[i], obj.Type()); value == "" {
				if d.Const {
					e.unsupported(name.Pos(), "a top-level const whose value is not a literal")
					return
				}

				e.lazyVar(obj.(*types.Var), name, d.Values[i])
				continue
			}
		}

		qualifier := "static SN_UNUSED " // a library's streams may go unused by a program
		if d.Const {
			qualifier += "const "
		} else if needsDrop(obj.Type()) {
			e.statics = append(e.statics, owned{name: lower.ObjName(obj), typ: obj.Type()})
		}

		put(&e.protos, qualifier, e.cType(obj.Type(), name.Pos()), " ", lower.ObjName(obj), " = ", value, ";\n")
	}
}

// lazyVar writes a top-level variable whose initializer is a call, or
// anything else that is not a literal: the cell, zero until made; the
// runtime's lazy beside it; the initializer as a function of its own,
// an assignment to the cell written as any statement is; and the
// getter every use of the variable goes through, which makes it on
// the first.
func (e *Emitter) lazyVar(v *types.Var, name *ast.Ident, value ast.Expr) {
	e.lazy[v] = true
	cname := lower.ObjName(v)
	ct := e.cType(v.Type(), name.Pos())
	cell := &ast.Ident{NamePos: name.NamePos, Name: name.Name}
	e.info.Uses[cell] = v
	e.info.Types[cell] = v.Type()
	body := &ast.BlockStmt{List: []ast.Stmt{&ast.AssignStmt{Lhs: []ast.Expr{cell}, TokPos: name.NamePos, Tok: token.ASSIGN, Rhs: []ast.Expr{value}}}}
	e.making = v
	e.writeFunction(lower.LazyInit(cname), name.Name+".init", types.NewSignature(nil, nil, nil), body, name.Pos())
	e.making = nil
	put(&e.protos, "static SN_UNUSED ", ct, " ", cname, ";\nstatic sn_lazy ", cname, "_lazy;\n")
	put(&e.protos, "static inline SN_UNUSED ", ct, " *", cname, "_get(void) {\n\tif (!sn_lazy_ready(&", cname, "_lazy)) {\n\t\tsn_lazy_make(&", cname, "_lazy, ", lower.LazyInit(cname), ", ", cString(name.Name), ");\n\t}\n\n\treturn &", cname, ";\n}\n\n")
	if needsDrop(v.Type()) {
		e.lazies = append(e.lazies, owned{name: cname, typ: v.Type()})
	}
}

// staticValue writes x as a C initializer a static variable of type t
// can have: a constant's value as the checker folded it, a number,
// character, boolean or string literal, the negation of a number, or a
// struct literal of such, with the live flag
// of a Drop type set. A string is written as the fields of a sn_string
// over the C literal, since the SN_STR macro is a compound literal,
// which C does not allow at file scope. It is empty for anything else.
func (e *Emitter) staticValue(x ast.Expr, t types.Type) string {
	switch x := lower.Folded(e.info, x).(type) {
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

		for _, i := range lower.LeftOut(st, x) { // a field left out takes its default
			v := e.staticValue(st.Default(i), st.Field(i).Type())
			if v == "" {
				return ""
			}

			parts = append(parts, "."+lower.Ident(st.Field(i).Name())+" = "+v)
		}

		if hasDrop(t) {
			parts = append(parts, ".sn_live = true")
		}

		if len(parts) == 0 {
			return zeroValue
		}

		return "{" + strings.Join(parts, ", ") + "}"
	}

	return ""
}

// funcDecl writes a function: a prototype in the prototypes section
// and the definition in the body. Snello's main becomes sn_main, which
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
	} else if owner := lower.TypeOwner(e.info, d); owner != nil { // func Point.Parse: named after its type, as a method is
		name = lower.MethodName(owner, d.Name.Name)
	}

	e.writeFunction(name, lower.DisplayName(fn, lower.RecvType(sig), nil), sig, d.Body, d.Pos())
}

// newEmitter makes an emitter for a program whose main package is the
// files fs, positions resolved through file, or for the header of a
// package that binds C, which has no program and no Info.
func newEmitter(info *types.Info, file *token.File, fs []*ast.File) *Emitter {
	e := &Emitter{
		info:     info,
		file:     file,
		used:     make(map[types.Object]bool),
		state:    make(map[string]int),
		seen:     make(map[string]bool),
		pre:      make(map[ast.Expr]string),
		guards:   make(map[*types.Named]bool),
		generics: make(map[*types.Func]*ast.FuncDecl),
		files:    make(map[*ast.FuncDecl]*token.File),
		external: make(map[*types.Var]bool),
		lazy:     make(map[*types.Var]bool),
		consts:   make(map[*types.Const]string),
		done:     make(map[string]bool),
		names:    newNaming(fs),
		jumped:   make(map[string]bool),
	}
	e.state["sn_slice_u8"] = defined // the runtime defines []byte in snello.h, as the library's bodiless functions take it
	return e
}

// prototype writes the prototype of a function a package binds to C,
// under its derived symbol, from its Snello signature: what the
// package's header declares too, so the C side's definition is checked
// against it there.
func (e *Emitter) prototype(fn *types.Func, pos token.Pos) {
	sig := fn.Type().(*types.Signature)
	put(&e.protos, e.resultType(sig, pos), " ", lower.FuncName(fn), "(", e.params(sig.Params(), pos), ");\n")
}

// writeFunction writes a function or method under the given C name,
// and the name the program gives it: its prototype, its head with the
// receiver as the first parameter, and its body, at their Snello
// positions.
func (e *Emitter) writeFunction(name, display string, sig *types.Signature, body *ast.BlockStmt, pos token.Pos) {
	params := sig.Params()
	if recv := sig.Recv(); recv != nil {
		params = append([]*types.Var{recv}, params...)
	}

	head := "static SN_UNUSED " + e.resultType(sig, pos) + " " + name + "(" + e.params(params, pos) + ")"
	put(&e.protos, head, ";\n")
	e.traced = append(e.traced, lower.TraceName{Symbol: name, Name: display})
	if name == lower.MainName {
		e.mainAt, e.mainFile = e.file.Position(pos), e.file // of this file, which the entry is written after
		e.traced = append(e.traced, lower.TraceName{Symbol: entry, Name: display})
	}
	outerWithin, outerClosures := e.within, e.nclosure
	e.within, e.nclosure, e.at = display, 0, pos
	e.line(head + " {")
	e.function(sig, body)
	e.endPositions()
	e.line("")
	e.within, e.nclosure = outerWithin, outerClosures
}

// directiveName is the name the file f is given in the directives,
// quoted as C writes a string, the standard library's under its
// directory (lower.SourceName): made once a file, since every line of
// it names it.
func (e *Emitter) directiveName(f *token.File) string {
	if f == nil { // a program of no main, which only a test emits
		return cString(generated)
	}

	q, ok := e.quoted[f]
	if !ok {
		q = cString(lower.SourceName(f.Name(), e.stdFiles[f]))
		e.quoted[f] = q
	}

	return q
}

// function writes the body of a function or closure whose head was
// just written: a (void) for each parameter nothing refers to, since
// the C compiler is asked to treat an unused one as an error, the
// statements, and for a body that falls off its end the drops of its
// parameters and locals and the deferred calls, last registered first,
// and the declarations the body asked for at the top. The caller's own
// state is kept.
func (e *Emitter) function(sig *types.Signature, body *ast.BlockStmt) {
	outerResult, outerLoops, outerPrologue, outerDefers := e.result, e.loops, e.prologue, e.defers
	outerFrames, outerTemps, outerDeclared := e.frames, e.temps, e.names.declared
	e.result, e.loops, e.prologue, e.defers, e.frames, e.temps = sig.Results(), nil, nil, nil, nil, nil
	e.names.declared = make(map[string]int)
	e.push(false)
	start := e.body.Len()
	if recv := sig.Recv(); recv != nil {
		e.names.declared[e.localName(recv)]++
		if !e.used[recv] {
			e.line("(void)" + e.localName(recv) + ";")
		}

		e.own(e.localName(recv), recv.Type())
	}

	framed, stays := lower.FramedVariadic(e.info, sig, body)
	for _, p := range sig.Params() {
		if p.Name() == "" {
			continue
		}

		e.names.declared[e.localName(p)]++
		if !e.used[p] {
			e.line("(void)" + e.localName(p) + ";")
		}

		if p == framed && !stays { // the body may keep the slice past the call: a buffer of its own, before anything else
			e.line("sn_slice_unframe((sn_slice *)&" + e.localName(p) + ", sizeof *" + e.localName(p) + ".ptr);")
		}

		e.own(e.localName(p), p.Type())
	}

	e.stmts(body.List)
	if sig.Results() == types.Unit {
		e.leaveFunction(body.Rbrace)
		e.frames = e.frames[:len(e.frames)-1]
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
	e.frames, e.temps, e.names.declared = outerFrames, outerTemps, outerDeclared
}

func (e *Emitter) params(params []*types.Var, pos token.Pos) string {
	if len(params) == 0 {
		return "void"
	}

	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = e.cType(p.Type(), pos)
		if p.Name() != "" {
			parts[i] += " " + e.localName(p)
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

// line writes one line of the current section, under a directive that
// gives the C compiler the Snello file and line it is at, when it is
// at one, so that the line tables it writes name the Snello source and
// its messages do too. Each line takes a directive of its own, since
// the compiler counts lines on from a directive, and a statement's C
// is several lines at one Snello line. Indentation is not tracked; the
// output is for the C compiler, and a reader has the Snello source.
func (e *Emitter) line(s string) {
	e.lineParts(s)
}

// lineParts is line for a line written in parts, which it writes one
// after another with nothing joined first.
func (e *Emitter) lineParts(parts ...string) {
	if e.at.IsValid() {
		p := e.file.Position(e.at)
		put(&e.body, "#line ", strconv.Itoa(p.Line), " ", e.directiveName(e.file), "\n")
	}

	put(&e.body, parts...)
	e.body.WriteByte('\n')
}

// addFunction appends a function written apart, a closure's, a spawned
// literal's or a deferred call's thunk, whose lines are at Snello
// positions, to the helpers, and ends the positions after it, so that
// what the helpers write next, a drop function, is the backend's own
// and no Snello line's: a debugger would stop at that line in it too.
func (e *Emitter) addFunction(fn []byte) {
	e.helpers.Write(fn)
	put(&e.helpers, "#line ", strconv.Itoa(generatedLine), " ", cString(generated), "\n\n")
}

// endPositions ends the lines at a Snello position: the lines after a
// function's body are the backend's own.
func (e *Emitter) endPositions() {
	e.at = token.NoPos
	e.line("#line " + strconv.Itoa(generatedLine) + " " + cString(generated))
}

// traceNames writes the table of names: every function written, in
// order of symbol, and the count, as snello.h declares them.
func (e *Emitter) traceNames(out *bytes.Buffer) {
	sort.Slice(e.traced, func(i, j int) bool { return e.traced[i].Symbol < e.traced[j].Symbol })
	out.WriteString("const sn_trace_name sn_trace_names[] = {\n")
	for _, n := range e.traced {
		put(out, "\t{", cString(n.Symbol), ", ", cString(n.Name), "},\n")
	}

	if len(e.traced) == 0 {
		out.WriteString("\t{\"\", \"\"},\n") // an array has at least one element
	}

	put(out, "};\nconst int64_t sn_trace_names_count = ", strconv.Itoa(len(e.traced)), ";\n")
}

// temp returns a fresh temporary name.
func (e *Emitter) temp() string {
	e.tmp++
	return "sn_t" + strconv.Itoa(e.tmp)
}
