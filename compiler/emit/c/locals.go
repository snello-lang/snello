// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"github.com/teak-lang/teak/compiler/ast"
	"github.com/teak-lang/teak/compiler/emit/lower"
	"github.com/teak-lang/teak/compiler/types"
)

// A local or a parameter is written under its own name, unless a local
// of the same name was declared before it in the same C function, or
// the program declares a type or trait of the name. C puts a variable in
// scope inside its own initializer, so x := x in an inner block would
// read the new x, a loop's step written after the body would reach a
// variable the body declares under the loop variable's name, and a
// local or parameter named as a struct of the program would hide the
// struct's C type from the rest of the function. Such a variable is
// given a name of its own, a temporary's number before it, which no
// name of the program reaches. A library's names are in the runtime's
// tk_ space and the program's functions, variables and constants in
// tk__, which no local reaches either.

// declareLocal returns the C name of the local name declares, and
// takes it in the function being written.
func (e *Emitter) declareLocal(name *ast.Ident) string {
	v, ok := e.info.Defs[name].(*types.Var)
	if !ok {
		return lower.Ident(name.Name)
	}

	local := e.localName(v)
	if _, renamed := e.names.renamed[v]; !renamed && (e.names.declared[local] || e.names.toplevel[local]) {
		local = e.temp() + "_" + local
		e.names.renamed[v] = local // an instance of a generic function written later takes the same name
	}

	e.names.declared[local] = true
	return local
}

// naming is how the C emitter names locals and parameters.
type naming struct {
	renamed  map[*types.Var]string // the C name of each variable given one of its own
	declared map[string]bool       // the C names taken in the function being written
	toplevel map[string]bool       // the C names the program's types and traits take at file scope
	globals  map[*types.Var]bool   // the top-level variables of the program and its libraries (varDecl), named by lower.ObjName
}

// newNaming returns the naming of the program in f: the names its types
// and traits take at file scope, each under its own name. Its
// functions, variables and constants are in the tk__ space (lower) and
// meet no local.
func newNaming(f *ast.File) naming {
	n := naming{renamed: make(map[*types.Var]string), toplevel: make(map[string]bool), globals: make(map[*types.Var]bool)}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.TypeDecl:
			n.toplevel[lower.Ident(d.Name.Name)] = true
		case *ast.TraitDecl:
			n.toplevel[lower.Ident(d.Name.Name)] = true
		}
	}

	return n
}

// localName returns the C name of the local or parameter v.
func (e *Emitter) localName(v *types.Var) string {
	if local, ok := e.names.renamed[v]; ok {
		return local
	}

	if e.names.globals[v] {
		return lower.ObjName(v)
	}

	name := lower.Ident(v.Name())
	if e.names.toplevel[name] { // the first time v is named, its declaration or the head of its function
		name = e.temp() + "_" + name
		e.names.renamed[v] = name
	}

	return name
}
