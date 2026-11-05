// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"path/filepath"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
)

// collectImports declares each import's package name in the file
// scope. A path is loaded through the Config's Importer; one it cannot
// find is reported here, once, with the name declared all the same so
// that its members are not reported again. Without an importer every
// member is undefined.
func (c *Checker) collectImports(f *ast.File, scope *Scope) {
	for _, imp := range f.Imports {
		path := imp.Path.Value[1 : len(imp.Path.Value)-1] // strip the quotes
		name := path[strings.LastIndexByte(path, '/')+1:]
		pkg := NewPkgName(imp.Pos(), name, path)
		if c.conf.Importer != nil {
			var loaded *Package
			var err error
			if from, ok := c.conf.Importer.(ImporterFrom); ok {
				loaded, err = from.ImportFrom(path, filepath.Dir(c.file.Name()))
			} else {
				loaded, err = c.conf.Importer.Import(path)
			}

			if err != nil {
				c.errorf(imp.Path.Pos(), err.Error())
				pkg.missing = true
			} else {
				pkg.pkg = loaded
				pkg.name = loaded.name
			}
		}

		ident := imp.Name
		if ident == nil {
			ident = &ast.Ident{NamePos: imp.Path.Pos(), Name: pkg.name}
		} else {
			pkg.name = ident.Name
		}

		c.declare(scope, ident, pkg)
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
			c.objs[i] = c.collectTrait(d)
		case *ast.VarDecl:
			c.declareVars(c.pkg.scope, d, nil)
		}
	}
}

// collectType declares a type name with an empty Named behind it. An
// enum's variants are created now, with their fields filled in later,
// so that the names exist before any body is walked.
func (c *Checker) collectType(d *ast.TypeDecl) *TypeName {
	obj := NewTypeName(d.Name.Pos(), d.Name.Name, nil)
	named := NewNamed(obj, nil, nil) // the impl list resolves with the other trait lists
	named.SetTypeParams(c.typeParams(d.TypeParams))
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

// typeParams creates the type parameters of a generic declaration, in
// order; nil when there are none. Their bounds are resolved once every
// trait is declared (resolveBounds), and the names are declared in a
// scope of their own when the declaration resolves.
func (c *Checker) typeParams(ps []*ast.TypeParam) []*TypeParam {
	if len(ps) == 0 {
		return nil
	}

	tparams := make([]*TypeParam, len(ps))
	seen := NewScope(nil)
	for i, p := range ps {
		obj := NewTypeName(p.Name.Pos(), p.Name.Name, nil)
		tparams[i] = NewTypeParam(obj, i)
		if seen.Insert(obj) != nil {
			c.errorf(p.Name.Pos(), "duplicate type parameter "+p.Name.Name)
		}
	}

	return tparams
}

// openTypeParams opens a scope declaring the given type parameters
// under the given names, for resolving a generic declaration or a
// method's signature and body, and records each name's definition.
func (c *Checker) openTypeParams(names []*ast.Ident, tparams []*TypeParam) *Scope {
	scope := c.openScope()
	for i, name := range names {
		obj := tparams[i].obj
		if name.Name != obj.name {
			obj = NewTypeName(name.Pos(), name.Name, tparams[i]) // a method may name a parameter differently
		}

		scope.Insert(obj)
		c.recordDef(name, obj)
	}

	return scope
}

// declareVars declares the names of a var or const declaration with
// type t, which is nil when the type is declared later (a top-level
// declaration) or inferred (the expression checker's job). A constant
// keeps the text of the literal it is given, which is what a format
// that names it reads.
func (c *Checker) declareVars(scope *Scope, d *ast.VarDecl, t Type) {
	for i, name := range d.Names {
		if d.Const {
			text := ""
			if i < len(d.Values) {
				if lit, ok := unparen(d.Values[i]).(*ast.BasicLit); ok {
					text = lit.Value
				}
			}

			c.declare(scope, name, NewConst(name.Pos(), name.Name, t, text))
		} else {
			c.declare(scope, name, NewVar(name.Pos(), name.Name, t))
		}
	}
}

// resolveDecls gives every collected object its type: the trait lists
// first, then struct fields and enum variants, trait methods, function
// signatures, methods on their receiver types, and declared variable
// types; last, with every method attached, each type is checked
// against the traits it implements. Bodies are not entered.
func (c *Checker) resolveDecls(f *ast.File) {
	c.resolveTraitLists(f)
	var named []*Named
	for i, d := range f.Decls {
		if d, ok := d.(*ast.TypeDecl); ok {
			n := c.objs[i].(*TypeName).typ.(*Named)
			c.resolveType(d, n)
			named = append(named, n)
		}
	}

	c.checkTypeCycles(named)
	c.resolvePending()
	c.resolvePendingBounds()
	for i, d := range f.Decls {
		switch d := d.(type) {
		case *ast.TraitDecl:
			c.resolveTrait(d, c.objs[i].(*TypeName).typ.(*Trait))
		case *ast.FuncDecl:
			c.resolveFuncDecl(d, c.objs[i].(*Func))
		case *ast.VarDecl:
			c.resolveVar(d)
		}
	}

	c.fillTraitInstances()
	c.checkRefinements(f)
	c.checkImpls(f)
}

func (c *Checker) resolveType(d *ast.TypeDecl, named *Named) {
	if named.tparams != nil {
		c.openTypeParams(paramNames(d.TypeParams), named.tparams)
		defer func() {
			c.closeScope()
			named.complete()
		}()
	}

	switch def := d.Def.(type) {
	case *ast.StructType:
		own := c.fields(def.Fields)
		fields := append(own, c.traitFields(named, own, d.Name.Pos())...) // a trait's fields are stored inline, after the type's own
		named.SetUnderlying(NewStruct(fields))
	case *ast.EnumType:
		c.variantFields(def.Variants, named.underlying.(*Enum).variants)
		c.refuseTraitFields(named, d.Name.Pos())
	default:
		named.SetUnderlying(c.typ(def).Underlying())
		c.refuseTraitFields(named, d.Name.Pos())
	}
}

// resolveFuncDecl gives fn its signature. A method's receiver names the
// type it belongs to, as (t mut Tree); the method is attached there.
//
// A method may have type parameters of its own beside the ones its
// receiver names, on a generic type or not: they are in a scope inside
// the receiver's, so the method's signature and body see both.
func (c *Checker) resolveFuncDecl(d *ast.FuncDecl, fn *Func) {
	if d.Recv != nil {
		scope, ok := c.receiverParams(d.Recv.Type)
		if !ok {
			fn.typ = NewSignature(nil, nil, Unit)
			c.badMethods[d] = true // reported once; its signature and body would only repeat the error
			return
		}

		if scope != nil {
			c.typeScopes[d] = scope
			defer c.closeScope()
		}
	}

	var tparams []*TypeParam
	if len(d.TypeParams) > 0 {
		tparams = c.typeParams(d.TypeParams)
		c.typeScopes[d] = c.resolveBounds(d.TypeParams, tparams)
		defer c.closeScope()
	}

	var recv *Var
	if d.Recv != nil {
		recv = NewVar(d.Recv.Pos(), d.Recv.Name.Name, c.typ(d.Recv.Type))
	}

	params, variadic := c.params(d.Params)
	sig := NewSignature(recv, params, c.results(d.Results))
	sig.tparams = tparams
	sig.variadic = variadic
	fn.typ = sig
	c.checkSources(d.Name.Name, fn.typ.(*Signature), d.Name.Pos())
	if d.Body == nil && !c.pkg.std {
		c.errorf(d.Name.Pos(), "missing function body") // the standard library may leave one to the runtime
	}

	if d.Recv == nil {
		return
	}

	c.recordDef(d.Name, fn)
	named, ok := receiverBase(recv.typ).(*Named)
	if ok && named.obj.pkg != c.pkg {
		c.errorf(d.Recv.Pos(), "cannot define new methods on non-local type "+named.String()) // Go's rule and message
		return
	}

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

// receiverParams opens the scope of the type parameters a method's
// receiver names, (l mut List<T>), when its type is generic, and
// returns it; it returns nil, with no scope open, for any other
// receiver. Each argument of the receiver type must be a plain name,
// as it declares the parameter in that position; a name that is
// already a type, List<int>, is refused rather than shadowing it,
// unless it is the declaration's own name for that parameter. ok is
// false after an error, which leaves the method out.
func (c *Checker) receiverParams(t ast.Type) (scope *Scope, ok bool) {
	if r, ok := t.(*ast.RefType); ok {
		t = r.Elem
	}

	n, isNamed := t.(*ast.NamedType)
	if !isNamed || n.Pkg != nil || len(n.Args) == 0 {
		return nil, true
	}

	tn, isType := c.pkg.scope.Lookup(n.Name.Name).(*TypeName)
	if !isType {
		return nil, true
	}

	named, isNamed := tn.typ.(*Named)
	if !isNamed || named.tparams == nil {
		return nil, true // namedType reports the arguments of a type that takes none
	}

	if len(n.Args) != len(named.tparams) {
		c.errorf(n.Pos(), n.Name.Name+" takes "+plural(len(named.tparams), "type argument")+": "+named.String())
		return nil, false
	}

	names := make([]*ast.Ident, len(n.Args))
	for i, arg := range n.Args {
		if names[i] = c.freshName(arg, named.tparams[i]); names[i] == nil {
			c.errorf(arg.Pos(), "a method's receiver names the type's parameters: "+named.String())
			return nil, false
		}
	}

	return c.openTypeParams(names, named.tparams), true
}

// freshName returns the name a receiver argument declares for the
// parameter p: a plain name that is not already a type, or is p's own
// name, which a package type may share; or nil.
func (c *Checker) freshName(arg ast.Type, p *TypeParam) *ast.Ident {
	a, ok := arg.(*ast.NamedType)
	if !ok || a.Pkg != nil || len(a.Args) > 0 {
		return nil
	}

	if a.Name.Name == p.obj.name {
		return a.Name
	}

	if _, obj := c.scope.LookupParent(a.Name.Name); obj != nil {
		if _, isType := obj.(*TypeName); isType {
			return nil
		}
	}

	return a.Name
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
