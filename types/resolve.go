// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"

	"github.com/teak-lang/teak/ast"
)

// collectImports declares each import's package name in the file scope.
func (c *Checker) collectImports(f *ast.File, scope *Scope) {
	for _, imp := range f.Imports {
		path := imp.Path.Value[1 : len(imp.Path.Value)-1] // strip the quotes
		name := path[strings.LastIndexByte(path, '/')+1:]

		ident := imp.Name
		if ident == nil {
			ident = &ast.Ident{NamePos: imp.Path.Pos(), Name: name}
		}

		c.declare(scope, ident, NewPkgName(imp.Pos(), ident.Name, path))
	}
}

// collectDecls declares every top-level name in the package scope, so
// that declarations can refer to each other in any order, as in Go.
// Types get a Named with no underlying type yet; variants are declared
// as unqualified names next to their enum.
func (c *Checker) collectDecls(f *ast.File) {
	c.objs = make([]Object, len(f.Decls))
	for i, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			fn := NewFunc(d.Name.Pos(), d.Name.Name, nil)
			if d.Recv == nil { // a method is attached to its type in resolveDecls
				c.declare(c.pkg.scope, d.Name, fn)
			}

			c.objs[i] = fn
		case *ast.TypeDecl:
			c.objs[i] = c.collectType(d)
		case *ast.TraitDecl:
			c.errorf(d.Pos(), "traits are not part of core Teak")
		case *ast.VarDecl:
			c.declareVars(c.pkg.scope, d, nil)
		}
	}
}

// collectType declares a type name with an empty Named behind it. An
// enum's variants are created now, with their fields filled in later,
// so that the names exist before any body is walked.
func (c *Checker) collectType(d *ast.TypeDecl) *TypeName {
	if len(d.TypeParams) > 0 {
		c.errorf(d.Pos(), "generic types are not part of core Teak")
	}

	obj := NewTypeName(d.Name.Pos(), d.Name.Name, nil)
	named := NewNamed(obj, nil, c.implNames(d.Impls))
	c.declare(c.pkg.scope, d.Name, obj)
	if e, ok := d.Def.(*ast.EnumType); ok {
		variants := make([]*Variant, len(e.Variants))
		for i, v := range e.Variants {
			variants[i] = NewVariant(v.Pos(), v.Name.Name, named, i, nil)
			c.declare(c.pkg.scope, v.Name, variants[i])
		}

		named.SetUnderlying(NewEnum(variants))
	}

	return obj
}

// declareVars declares the names of a var or const declaration with
// type t, which is nil when the type is declared later (a top-level
// declaration) or inferred (the expression checker's job).
func (c *Checker) declareVars(scope *Scope, d *ast.VarDecl, t Type) {
	for _, name := range d.Names {
		if d.Const {
			c.declare(scope, name, NewConst(name.Pos(), name.Name, t, ""))
		} else {
			c.declare(scope, name, NewVar(name.Pos(), name.Name, t))
		}
	}
}

// implNames returns the trait names after impl. Traits are a later
// stage; here they are kept by name so Drop can be found.
func (c *Checker) implNames(ts []ast.Type) []string {
	if len(ts) == 0 {
		return nil
	}

	names := make([]string, len(ts))
	for i, t := range ts {
		if n, ok := t.(*ast.NamedType); ok && n.Pkg == nil && len(n.Args) == 0 {
			names[i] = n.Name.Name
		} else {
			c.errorf(t.Pos(), "a trait name is expected after impl")
		}
	}

	return names
}

// resolveDecls gives every collected object its type: struct fields and
// enum variants, function signatures, methods on their receiver types,
// and declared variable types. Bodies are not entered.
func (c *Checker) resolveDecls(f *ast.File) {
	for i, d := range f.Decls {
		if d, ok := d.(*ast.TypeDecl); ok {
			c.resolveType(d, c.objs[i].(*TypeName).typ.(*Named))
		}
	}

	for i, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			c.resolveFuncDecl(d, c.objs[i].(*Func))
		case *ast.VarDecl:
			c.resolveVar(d)
		}
	}
}

func (c *Checker) resolveType(d *ast.TypeDecl, named *Named) {
	switch def := d.Def.(type) {
	case *ast.StructType:
		named.SetUnderlying(NewStruct(c.fields(def.Fields)))
	case *ast.EnumType:
		c.variantFields(def.Variants, named.underlying.(*Enum).variants)
	default:
		named.SetUnderlying(c.typ(def).Underlying())
	}
}

// resolveFuncDecl gives fn its signature. A method's receiver names the
// type it belongs to, as (t mut Tree); the method is attached there.
func (c *Checker) resolveFuncDecl(d *ast.FuncDecl, fn *Func) {
	if len(d.TypeParams) > 0 {
		c.errorf(d.Pos(), "generic functions are not part of core Teak")
	}

	var recv *Var
	if d.Recv != nil {
		recv = NewVar(d.Recv.Pos(), d.Recv.Name.Name, c.typ(d.Recv.Type))
	}

	fn.typ = NewSignature(recv, c.params(d.Params), c.results(d.Results))
	if d.Recv == nil {
		return
	}

	c.recordDef(d.Name, fn)
	named, ok := receiverBase(recv.typ).(*Named)
	if !ok {
		c.errorf(d.Recv.Pos(), "a receiver must be a declared type, a ref or mut of one")
		return
	}

	if old := named.AddMethod(fn); old != nil {
		c.errorf(d.Name.Pos(), "method "+named.obj.name+"."+d.Name.Name+" redeclared; first declared at "+c.lineCol(old.Pos()))
	}
}

func (c *Checker) resolveVar(d *ast.VarDecl) {
	if d.Type == nil {
		return
	}

	t := c.typ(d.Type)
	for _, name := range d.Names {
		switch obj := c.pkg.scope.Lookup(name.Name).(type) {
		case *Var:
			obj.typ = t
		case *Const:
			obj.typ = t
		}
	}
}

// isBlank reports whether e is the blank identifier _.
func isBlank(e ast.Expr) bool {
	ident, ok := e.(*ast.Ident)
	return ok && ident.Name == "_"
}

// receiverBase strips the ref or mut from a receiver type.
func receiverBase(t Type) Type {
	if r, ok := t.(*Ref); ok {
		return r.elem
	}

	return t
}
