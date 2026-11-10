// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package llvm writes a checked Snello program as LLVM IR, which clang
// turns into a native binary over the C runtime the C backend uses,
// linked unchanged. The layouts and the release points are those of
// _docs/design/07-lowering.md, shared with the C backend through
// emit/lower; what differs is the text, and the three layouts the note
// gives the IR alone. The emitter reads the types, moves and captures
// the checker recorded and decides nothing about ownership. A
// construct it does not lower yet is an error naming it, never silent
// wrong code.
//
// Every local and parameter lives in a stack slot allocated at the top
// of its function and is read and written through it; the optimizer
// promotes the slots to registers, which is the form LLVM's own
// front ends use. A borrow is the address of the place it lends.
// Expressions compute into registers named in order; a composite
// value is an aggregate register, built with insertvalue and read with
// extractvalue, and a place is a pointer, its parts reached with
// getelementptr. Control flow is blocks and branches: a statement that
// needs a block opens one under a label of its own, and a block
// always ends in a terminator, since the IR allows no instruction
// after one and no block without one.
package llvm

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

// A Config says how the program is built: with the runtime's checks,
// as snello build -debug asks, and for which processor, since a call
// into the C runtime follows the processor's calling convention
// (abi.go).
type Config struct {
	Debug bool
	Arch  string // amd64 or arm64; the host's when empty
	OS    string // darwin or linux; the host's when empty
}

// An Emitter writes one program as one IR module. The named types,
// the constants, the declarations of the runtime functions called and
// the generated helper functions go into sections of their own,
// written out before the program's functions: the IR reads a module
// in any order, a reader does not.
type Emitter struct {
	cfg        Config
	abi        abi
	info       *types.Info
	file       *token.File
	types      bytes.Buffer                    // the named types of the module
	consts     bytes.Buffer                    // string literals and tables, as private constants
	decls      map[string]string               // the runtime functions and globals used, by name, each declared once
	helpers    bytes.Buffer                    // generated functions: droppers, printers, comparers, adapters
	funcs      bytes.Buffer                    // the program's function definitions
	globalDefs bytes.Buffer                    // the program's top-level variables
	globals    map[*types.Var]bool             // the top-level variables, reached as globals
	localConst map[*types.Const]string         // the constants declared in a function, each a global of its own
	fn         *function                       // the function being written
	strs       int                             // string constants written so far
	closures   int                             // closures written so far, numbering their functions
	seq        int                             // values and deferred calls registered so far, ordering what a return releases
	tasks      int                             // spawn statements written so far, numbering their trampolines
	boxWords   int                             // the words a function value's box has: the program's largest closure of copies, at least minBoxWords
	boxNeed    int                             // the words a closure of copies written in this pass needed beyond boxWords, which the next pass gives every box
	state      map[string]bool                 // the named types and helpers written so far
	seen       map[string]bool                 // the unsupported constructs reported so far
	used       map[types.Object]bool           // the objects the program refers to
	guards     map[*types.Named]bool           // whether dropping a value of a declared type runs a Drop method (drop.go)
	subst      map[*types.TypeParam]types.Type // the type arguments of the instance being written (mono.go)
	generics   map[*types.Func]*ast.FuncDecl   // the generic functions and methods, written per instance
	files      map[*ast.FuncDecl]*token.File   // the source file of each generic declaration, for the positions in an instance's errors
	external   map[*types.Var]bool             // the library variables the runtime defines
	runtime    map[string]bool                 // the library functions the runtime defines, by name, called under its convention
	statics    []owned                         // the top-level variables owning something, dropped when main returns
	lazy       map[*types.Var]bool             // the top-level variables made on first use, since their initializer is a call; reached through a getter
	lazies     []owned                         // the lazy variables owning something, dropped when main returns if they were made
	making     *types.Var                      // the lazy variable whose initializer is being written, which names the cell itself
	done       map[string]bool                 // the instances asked for, by name
	work       []instance                      // the instances still to write
	errors     []string
}

// A function being written: its instructions; the allocas of its
// locals and temporaries, written at the top of the entry block; the
// slot of each variable; the loops open at this point, with the labels
// break and continue jump to; the frames of the scopes open, with the
// variables to drop at their end (drop.go); the temporaries of the
// statement being written; the deferred calls; the expressions a
// defer evaluated early; how a captured variable is reached inside a
// closure; the signature; a counter naming registers and blocks; and
// whether the current block has ended in a terminator.
type function struct {
	body       bytes.Buffer
	allocas    bytes.Buffer
	vars       map[*types.Var]string
	loops      []loop
	frames     []frame
	temps      []owned
	defers     []deferred
	pre        map[ast.Expr]string
	captured   map[*types.Var]string
	sig        *types.Signature
	tmp        int
	terminated bool
}

// A loop open at this point: the label continue jumps to, the one
// break jumps to, and the index of its body's frame.
type loop struct {
	next, end string
	frame     int
}

// A deferred call: the record its defer statement saved into, whose
// first member is the flag saying the statement ran; the function that
// makes the call from the record; and when the statement registered
// it.
type deferred struct {
	rec   string
	thunk string
	seq   int
}

// Emit writes the IR module for a checked file, a program of one, and
// the library packages it imports; EmitFiles takes a main package of
// several files.
func Emit(cfg Config, file *token.File, f *ast.File, info *types.Info, libraries ...lower.Library) ([]byte, error) {
	return EmitFiles(cfg, []*token.File{file}, []*ast.File{f}, info, libraries...)
}

// EmitFiles writes the IR module for a checked main package, its files
// parsed into one FileSet, and the library packages it imports, each
// after the ones it imports. info must hold the Defs, Uses and Types
// the checker recorded for all of them; the files resolve the positions
// in error messages.
//
// The box of a function value has one size in the whole module, since
// it is part of every function type, and the size is found before any
// type is written, from the closures the checker recorded. A closure in
// a generic function copies values of its instance's types, which are
// known only as the instance is written; when one needs a larger box
// than the module was written with, the module is written again with
// that size, which settles in a pass or two.
func EmitFiles(cfg Config, files []*token.File, fs []*ast.File, info *types.Info, libraries ...lower.Library) ([]byte, error) {
	target, err := targetABI(cfg.Arch)
	if err != nil {
		return nil, err
	}

	if err := lower.CheckPrefixes(libraries); err != nil {
		return nil, err
	}

	words := minBoxWords
	for pass := 0; ; pass++ {
		out, need, err := emitPass(cfg, target, words, files, fs, info, libraries)
		if err != nil || need == 0 {
			return out, err
		}

		if pass == maxPasses { // a box that must hold more than itself, which no Copy value is
			return nil, &Error{[]string{files[0].Name() + ": the LLVM backend does not support a closure whose copies need a box of " + strconv.Itoa(need) + " words yet"}}
		}

		words = need
	}
}

// maxPasses bounds the writes of a module: the box size grows to the
// largest closure of copies, which takes one more pass per generic
// instance nesting another, and a few suffice.
const maxPasses = 8

// emitPass writes the module once with boxes of at least words words, and
// returns it with the larger size a closure needed, or zero when every
// closure fit.
func emitPass(cfg Config, target abi, words int, files []*token.File, fs []*ast.File, info *types.Info, libraries []lower.Library) ([]byte, int, error) {
	e := &Emitter{
		cfg:        cfg,
		abi:        target,
		info:       info,
		file:       files[0],
		decls:      make(map[string]string),
		state:      make(map[string]bool),
		seen:       make(map[string]bool),
		used:       make(map[types.Object]bool, len(info.Uses)),
		guards:     make(map[*types.Named]bool),
		generics:   make(map[*types.Func]*ast.FuncDecl),
		files:      make(map[*ast.FuncDecl]*token.File),
		external:   make(map[*types.Var]bool),
		runtime:    make(map[string]bool),
		done:       make(map[string]bool),
		globals:    make(map[*types.Var]bool),
		lazy:       make(map[*types.Var]bool),
		localConst: make(map[*types.Const]string),
		boxWords:   words,
	}
	for _, obj := range info.Uses {
		e.used[obj] = true
	}

	e.sizeBoxes()
	units := append([]lower.Library(nil), libraries...)
	for i, f := range fs {
		units = append(units, lower.Library{File: files[i], Syntax: f})
	}

	for i, u := range units {
		e.file = u.File
		for _, d := range u.Syntax.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				fn := info.Defs[d.Name].(*types.Func)
				if d.Body == nil {
					e.runtime[lower.FuncName(fn)] = true // the runtime's, called as C is called
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

	e.file = files[0]
	e.instances()
	e.mainFunc()
	if len(e.errors) > 0 {
		return nil, 0, &Error{e.errors}
	}

	if e.boxNeed > e.boxWords {
		return nil, e.boxNeed, nil
	}

	sources := make([]string, len(files))
	for i, f := range files {
		sources[i] = f.Name()
	}

	var out bytes.Buffer
	put(&out, "; Emitted by the Snello compiler from ", strings.Join(sources, ", "), ". Do not edit.\n")
	put(&out, "source_filename = ", strconv.Quote(files[0].Name()), "\n")
	put(&out, "target triple = ", strconv.Quote(triple(cfg.Arch, cfg.OS)), "\n\n")
	put(&out, "%sn_string = type { ptr, i64, i64 }\n%sn_slice = type { ptr, i64, i64 }\n%sn_map = type { ptr, ptr, ptr, ptr, i64, i64, i64, i64 }\n%sn_trait = type { ptr, ptr }\n%sn_map_type = type { i64, i64, ptr, ptr, ptr, ptr }\n%sn_case = type { ptr, ptr, i8, i8 }\n")
	out.Write(e.types.Bytes())
	out.WriteString("\n")
	out.Write(e.consts.Bytes())
	names := make([]string, 0, len(e.decls))
	for name := range e.decls {
		names = append(names, name)
	}

	sort.Strings(names)
	for _, name := range names {
		put(&out, e.decls[name], "\n")
	}

	out.WriteString("\n")
	out.Write(e.globalDefs.Bytes())
	out.Write(e.helpers.Bytes())
	out.Write(e.funcs.Bytes())
	out.WriteString(emptyGroup) // every function's attributes, which Targeted fills with the target's (target.go)
	return out.Bytes(), 0, nil
}

// mainFunc writes the module's main: the runtime set up, the program's
// main, the top-level variables dropped, and the runtime's verdict.
func (e *Emitter) mainFunc() {
	e.declare("sn_init", "declare void @sn_init(i32, ptr)")
	e.declare("sn_finish", "declare i32 @sn_finish()")
	e.fn = &function{vars: make(map[*types.Var]string), pre: make(map[ast.Expr]string)}
	e.inst("call void @sn_init(i32 %argc, ptr %argv)")
	e.inst("call void @sn_main()")
	for i := len(e.statics) - 1; i >= 0; i-- {
		e.dropOf(e.statics[i].slot, e.statics[i].typ, token.NoPos)
	}

	for i := len(e.lazies) - 1; i >= 0; i-- { // a lazy variable is dropped when it was made
		state := e.op("load atomic i32, ptr "+e.lazies[i].slot+"_lazy acquire, align 4", "i32")
		made := e.op("icmp eq i32 "+state.s+", 2", "i1")
		drop, skip := e.block("drop"), e.block("skip")
		e.branch(made.s, drop, skip)
		e.label(drop)
		e.dropOf(e.lazies[i].slot, e.lazies[i].typ, token.NoPos)
		e.jump(skip)
		e.label(skip)
	}

	code := e.op("call i32 @sn_finish()", "i32")
	e.inst("ret i32 " + code.s)
	put(&e.funcs, "define i32 @main(i32 %argc, ptr %argv)", funcAttrs, " {\nentry:\n")
	e.funcs.Write(e.fn.allocas.Bytes())
	e.funcs.Write(e.fn.body.Bytes())
	e.funcs.WriteString("}\n")
	e.fn = nil
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
	e.errors = append(e.errors, e.file.Position(pos).String()+": the LLVM backend does not support "+what+" yet")
}

// declare records the declaration of a runtime function or an
// external global the module uses, once.
func (e *Emitter) declare(name, decl string) {
	e.decls[name] = decl
}

// varDecl writes a top-level variable or constant as a global of the
// module. The value must be a literal: a number, character, boolean
// or string, a negated number, or a struct of such, since anything
// else would need a computation, which the module does not run before
// main. A library variable declared without a value is the runtime's,
// defined in it under the variable's name; a program's variable
// without a value starts at zero. A variable that may come to own
// something, by assignment, is dropped when main returns, so that a
// debug build reports what the program itself leaked.
func (e *Emitter) varDecl(d *ast.VarDecl, library bool) {
	for i, name := range d.Names {
		obj := e.info.Defs[name]
		if v, ok := obj.(*types.Var); ok {
			e.globals[v] = true
		}

		t := e.irType(obj.Type(), name.Pos())
		if library && i >= len(d.Values) {
			e.external[obj.(*types.Var)] = true
			e.declare(lower.ObjName(obj), "@"+lower.ObjName(obj)+" = external global "+t)
			continue
		}

		value := zero(t)
		if i < len(d.Values) {
			if value = e.staticValue(d.Values[i], obj.Type()); value == "" {
				if d.Const {
					e.unsupported(name.Pos(), "a top-level const whose value is not a literal")
					return
				}

				e.lazyVar(obj.(*types.Var), name, d.Values[i], t)
				continue
			}
		}

		qualifier := "global "
		if d.Const {
			qualifier = "constant "
		} else if needsDrop(obj.Type()) {
			e.statics = append(e.statics, owned{slot: "@" + lower.ObjName(obj), typ: obj.Type()})
		}

		put(&e.globalDefs, "@", lower.ObjName(obj), " = internal ", qualifier, t, " ", value, "\n")
	}
}

// lazyVar writes a top-level variable whose initializer is a call, or
// anything else that is not a literal: the cell, zero until made; the
// runtime's lazy beside it; the initializer as a function of its own,
// an assignment to the cell written as any statement is; and the
// getter every use of the variable goes through, which makes it on
// the first (_docs/design/03-ownership-across-tasks.md, package
// variables).
func (e *Emitter) lazyVar(v *types.Var, name *ast.Ident, value ast.Expr, t string) {
	e.lazy[v] = true
	g := "@" + lower.ObjName(v)
	cell := &ast.Ident{NamePos: name.NamePos, Name: name.Name}
	e.info.Uses[cell] = v
	e.info.Types[cell] = v.Type()
	body := &ast.BlockStmt{List: []ast.Stmt{&ast.AssignStmt{Lhs: []ast.Expr{cell}, TokPos: name.NamePos, Tok: token.ASSIGN, Rhs: []ast.Expr{value}}}}
	e.making = v
	e.writeFunction(lower.ObjName(v)+"_init", types.NewSignature(nil, nil, nil), body, name.Pos())
	e.making = nil
	e.declare("sn_lazy_make", "declare void @sn_lazy_make(ptr, ptr, ptr)")
	put(&e.globalDefs, g, " = internal global ", t, " zeroinitializer\n", g, "_lazy = internal global { i32, ptr } zeroinitializer\n")
	put(&e.funcs, "define internal ptr ", g, "_get()", funcAttrs, " {\nentry:\n  %state = load atomic i32, ptr ", g, "_lazy acquire, align 4\n  %unmade = icmp ne i32 %state, 2\n  br i1 %unmade, label %make, label %done\nmake:\n  call void @sn_lazy_make(ptr ", g, "_lazy, ptr ", g, "_init, ptr ", e.constBytes(name.Name), ")\n  br label %done\ndone:\n  ret ptr ", g, "\n}\n\n")
	if needsDrop(v.Type()) {
		e.lazies = append(e.lazies, owned{slot: g, typ: v.Type()})
	}
}

// staticValue writes x as a constant a global of type t can have: a
// number, character, boolean or string literal, the negation of a
// number, or a struct literal of such, with the live flag of a Drop
// type set. It is empty for anything else.
func (e *Emitter) staticValue(x ast.Expr, t types.Type) string {
	switch x := x.(type) {
	case *ast.BasicLit:
		return e.literal(x).s
	case *ast.Ident:
		if x.Name == "true" || x.Name == "false" {
			return x.Name
		}
	case *ast.UnaryExpr:
		if lit, ok := x.X.(*ast.BasicLit); ok && x.Op == token.SUB {
			v := e.literal(lit)
			if isFloat(t) {
				f, _ := strconv.ParseFloat(lit.Value, 64)
				return hexFloat(-f, bits(t))
			}

			n, _ := strconv.ParseInt(v.s, 10, 64)
			return strconv.FormatInt(-n, 10)
		}
	case *ast.ParenExpr:
		return e.staticValue(x.X, t)
	case *ast.CompositeLit:
		st, ok := t.Underlying().(*types.Struct)
		if !ok {
			return ""
		}

		parts := make([]string, st.NumFields())
		for i := range parts {
			parts[i] = e.irType(st.Field(i).Type(), x.Pos()) + " " + zero(e.irType(st.Field(i).Type(), x.Pos()))
		}

		for _, elt := range x.Elts {
			kv := elt.(*ast.KeyValueExpr)
			key := kv.Key.(*ast.Ident)
			i := fieldIndex(st, key.Name)
			v := e.staticValue(kv.Value, st.Field(i).Type())
			if v == "" {
				return ""
			}

			parts[i] = e.irType(st.Field(i).Type(), x.Pos()) + " " + v
		}

		if hasDrop(t) {
			parts = append(parts, "i1 true")
		}

		return "{ " + strings.Join(parts, ", ") + " }"
	}

	return ""
}

// funcDecl writes a function: its parameters stored into slots of
// their own at the top, its body, and the drops and terminator the
// body's end needs. Snello's main becomes sn_main, which the module's
// main calls after the runtime is set up; a method is a function named
// after its type whose first parameter is the receiver; a function
// declared without a body is the runtime's and needs nothing written.
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

// writeFunction writes a function or method under the given name: its
// definition with the receiver as the first parameter, and its body.
func (e *Emitter) writeFunction(name string, sig *types.Signature, body *ast.BlockStmt, pos token.Pos) {
	params := sig.Params()
	if recv := sig.Recv(); recv != nil {
		params = append([]*types.Var{recv}, params...)
	}

	e.writeBody(name, sig, params, nil, nil, body, pos)
}

// writeBody writes a function under name whose Snello parameters are
// params, after any leading IR parameters written as declared, such as
// a closure's environment. prologue, when not nil, writes instructions
// before the parameters are stored and returns how the body reaches
// each captured variable. A function is written whole into its own
// text, so one can be written while another is being written.
func (e *Emitter) writeBody(name string, sig *types.Signature, params []*types.Var, leading []string, prologue func() map[*types.Var]string, body *ast.BlockStmt, pos token.Pos) {
	outer := e.fn
	e.fn = &function{vars: make(map[*types.Var]string), pre: make(map[ast.Expr]string), sig: sig}
	list := append([]string(nil), leading...)
	for i, p := range params {
		list = append(list, e.irType(p.Type(), pos)+" %a"+strconv.Itoa(i))
	}

	var env map[*types.Var]string
	if prologue != nil {
		env = prologue()
	}

	e.function(sig, params, env, body)
	put(&e.funcs, "define internal ", e.resultType(sig.Results(), pos), " @", name, "(", strings.Join(list, ", "), ")", funcAttrs, " {\nentry:\n")
	e.funcs.Write(e.fn.allocas.Bytes())
	e.funcs.Write(e.fn.body.Bytes())
	e.funcs.WriteString("}\n\n")
	e.fn = outer
}

// function writes the body of a function or closure into the function
// being written: the parameters moved into slots of their own, the
// statements, and for a body that falls off its end the drops of its
// parameters and locals and the deferred calls, last registered first,
// or the mark of code the checker proved unreachable. env, when not
// nil, is the closure's environment, reached as closure.go says.
func (e *Emitter) function(sig *types.Signature, params []*types.Var, env map[*types.Var]string, body *ast.BlockStmt) {
	e.fn.captured = env
	e.push(false)
	for i, p := range params {
		if p.Name() == "" || p.Name() == "_" {
			continue
		}

		slot := e.local(p)
		e.inst("store " + e.irType(p.Type(), p.Pos()) + " %a" + strconv.Itoa(i) + ", ptr " + slot + ", align 8")
		e.own(slot, e.sub(p.Type()))
	}

	e.stmts(body.List)
	if e.fn.terminated {
		e.fn.frames = e.fn.frames[:len(e.fn.frames)-1] // the body ended with a statement that left, which dropped everything
		return
	}

	if sig.Results() == types.Unit {
		e.leaveFunction(body.Rbrace)
		e.fn.frames = e.fn.frames[:len(e.fn.frames)-1]
		e.inst("ret void")
	} else {
		e.inst("unreachable")
	}

	e.fn.terminated = true
}

// resultType writes the IR type a function returns: void for a Snello
// function of no result.
func (e *Emitter) resultType(t types.Type, pos token.Pos) string {
	if t == types.Unit {
		return "void"
	}

	return e.irType(t, pos)
}

// local gives the variable v a stack slot and returns its address.
func (e *Emitter) local(v *types.Var) string {
	slot := e.slot(e.irType(v.Type(), v.Pos()))
	e.fn.vars[v] = slot
	return slot
}

// inst writes one instruction of the function being written. An
// instruction after a terminator, which the checker allows as code
// after a return, opens a block of its own.
func (e *Emitter) inst(s string) {
	if e.fn.terminated {
		e.label(e.block("unreachable"))
	}

	put(&e.fn.body, "  ", s, "\n")
}

// block returns a fresh block label with the given stem.
func (e *Emitter) block(stem string) string {
	e.fn.tmp++
	return stem + "." + strconv.Itoa(e.fn.tmp)
}

// label opens the block named by a label returned by block.
func (e *Emitter) label(name string) {
	put(&e.fn.body, name, ":\n")
	e.fn.terminated = false
}

// jump ends the current block with an unconditional branch.
func (e *Emitter) jump(to string) {
	e.inst("br label %" + to)
	e.fn.terminated = true
}

// branch ends the current block with a conditional branch on the i1 c.
func (e *Emitter) branch(c, then, els string) {
	e.inst("br i1 " + c + ", label %" + then + ", label %" + els)
	e.fn.terminated = true
}

// temp returns a fresh register name.
func (e *Emitter) temp() string {
	e.fn.tmp++
	return "%t" + strconv.Itoa(e.fn.tmp)
}

// op writes an instruction computing into a fresh register of the IR
// type t.
func (e *Emitter) op(s, t string) value {
	r := e.temp()
	e.inst(r + " = " + s)
	return value{t, r}
}

// slot returns the address of a fresh stack slot of the IR type t,
// allocated at the top of the function.
func (e *Emitter) slot(t string) string {
	e.fn.tmp++
	name := "%s" + strconv.Itoa(e.fn.tmp)
	put(&e.fn.allocas, "  ", name, " = alloca ", t, ", align 8\n")
	return name
}

// spill stores v into a fresh slot and returns the slot's address.
func (e *Emitter) spill(v value) string {
	slot := e.slot(v.typ)
	e.store(v, slot)
	return slot
}

// load reads the value of IR type t at the address p.
func (e *Emitter) load(t, p string) value {
	return e.op("load "+t+", ptr "+p+", align 8", t)
}

// store writes v at the address p.
func (e *Emitter) store(v value, p string) {
	e.inst("store " + v.typed() + ", ptr " + p + ", align 8")
}

// gep returns the address of member i of the aggregate of IR type t
// at the address p.
func (e *Emitter) gep(t, p string, i int) string {
	return e.op("getelementptr inbounds "+t+", ptr "+p+", i32 0, i32 "+strconv.Itoa(i), "ptr").s
}

// elem returns the address of element i, an i64, of the array of
// elements of IR type t starting at the address p.
func (e *Emitter) elem(t, p, i string) string {
	return e.op("getelementptr inbounds "+t+", ptr "+p+", i64 "+i, "ptr").s
}

// extract reads member i of the aggregate value v, of IR type t.
func (e *Emitter) extract(v value, i int, t string) value {
	return e.op("extractvalue "+v.typed()+", "+strconv.Itoa(i), t)
}

// insert returns the aggregate value agg with member i set to v.
func (e *Emitter) insert(agg, v value, i int) value {
	return e.op("insertvalue "+agg.typed()+", "+v.typed()+", "+strconv.Itoa(i), agg.typ)
}

// put writes its pieces to b.
func put(b *bytes.Buffer, pieces ...string) {
	for _, p := range pieces {
		b.WriteString(p)
	}
}
