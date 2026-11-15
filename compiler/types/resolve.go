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
			if d.Recv == nil && d.Of == nil { // a method, and a function of a type, is attached to its type in resolveDecls
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
// is given its value when its declaration is checked.
func (c *Checker) declareVars(scope *Scope, d *ast.VarDecl, t Type) {
	for _, name := range d.Names {
		if d.Const {
			c.declare(scope, name, NewConst(name.Pos(), name.Name, t, Constant{}))
		} else {
			v := NewVar(name.Pos(), name.Name, t)
			v.global = scope == c.pkg.scope // its type is checked for Send in initializersOf, once every type is complete
			c.declare(scope, name, v)
		}
	}
}

// globalType refuses a package variable whose type cannot be shared
// by every task, as a spawn refuses to carry it: a package variable is
// reached from any task.
func (c *Checker) globalType(name *ast.Ident, t Type) {
	if t == nil || t == Typ[Invalid] {
		return // reported already
	}

	if why := NotSend(t); why != "" {
		c.errorf(name.Pos(), name.Name+" cannot be a package variable: "+why)
	}
}

// resolveTypes gives each declared type of a file its struct fields,
// enum variants or underlying type, and appends them to named for the
// cycle check over the whole package. The order of the phases over a
// package's files is checkFiles's: the trait lists come first, the
// types next, and the signatures after every type of every file.
func (c *Checker) resolveTypes(f *ast.File, named []*Named) []*Named {
	for i, d := range f.Decls {
		if d, ok := d.(*ast.TypeDecl); ok {
			n := c.objs[i].(*TypeName).typ.(*Named)
			if p := c.early[n]; p != nil {
				if p.done {
					named = append(named, n) // resolved already, where a constant or another such type needed it
					continue
				}

				p.checking = true
			}

			c.resolveType(d, n)
			if p := c.early[n]; p != nil {
				p.checking, p.done = false, true
			}

			named = append(named, n)
		}
	}

	return named
}

// A pendingType is a type declared over another type, neither a struct
// nor an enum nor generic, which may be resolved before its turn: the
// declaration, the file it is in, and how far it has gone.
type pendingType struct {
	decl     *ast.TypeDecl
	file     int
	checking bool
	done     bool
}

// collectEarly notes every type of the package's files declared over
// another type, type Celsius f64, which is all a constant's type or a
// conversion in a constant expression can be besides a predeclared
// type.
func (c *Checker) collectEarly(fs []*ast.File) {
	var named []*Named
	var pending []pendingType
	for i, f := range fs {
		for j, d := range f.Decls {
			d, ok := d.(*ast.TypeDecl)
			if !ok || d.TypeParams != nil {
				continue
			}

			switch d.Def.(type) {
			case *ast.StructType, *ast.EnumType:
				continue
			}

			if tn, ok := c.fileState[i].objs[j].(*TypeName); ok {
				if n, ok := tn.typ.(*Named); ok {
					named = append(named, n)
					pending = append(pending, pendingType{decl: d, file: i})
				}
			}
		}
	}

	if len(named) == 0 {
		return
	}

	c.early = make(map[*Named]*pendingType, len(named)) // made once every record is in place, so that none moves
	for i, n := range named {
		c.early[n] = &pending[i]
	}
}

// resolveEarly resolves t now when it is a type declared over another
// that has not had its turn, in the file that declares it, so that a
// constant, which may be needed by an array length before every type is
// resolved, and a type declared over a later one see its underlying
// type. A type met again while it is resolved is left to the check of
// recursive types.
func (c *Checker) resolveEarly(t Type) {
	n, ok := t.(*Named)
	if !ok {
		return
	}

	p := c.early[n]
	if p == nil || p.done || p.checking {
		return
	}

	p.checking = true
	at := c.here()
	c.enter(p.file)
	c.resolveType(p.decl, n)
	c.restore(at)
	p.checking, p.done = false, true
}

// resolveSignatures gives each trait of a file its methods, each
// function its signature, each method its place on its receiver type,
// and each declared variable its type. Bodies are not entered.
func (c *Checker) resolveSignatures(f *ast.File) {
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
		own := c.fields(def.Fields, true)
		fields := append(own, c.traitFields(named, own, d.Name.Pos())...) // a trait's fields are stored inline, after the type's own
		named.SetUnderlying(NewStruct(fields))
	case *ast.EnumType:
		c.variantFields(def.Variants, named.underlying.(*Enum).variants)
		c.refuseTraitFields(named, d.Name.Pos())
	default:
		t := c.typ(def)
		c.resolveEarly(t) // type A B before type B f64
		named.SetUnderlying(t.Underlying())
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
	var owner *Scope     // the type parameters the function's type names, G<A> of func G<A>.Of
	var ownerName string // G
	var ofType Type      // that type, read before the function's own type parameters are in scope
	if d.Of != nil {     // func Point.Parse: a function of the type, which names its parameters as a receiver does
		fn.static = true
		if c.refuseOwner(d) {
			fn.typ = NewSignature(nil, nil, Unit)
			c.badMethods[d] = true
			return
		}

		scope, ok := c.receiverParams(d.Of, "a function of a type")
		if !ok {
			fn.typ = NewSignature(nil, nil, Unit)
			c.badMethods[d] = true
			return
		}

		if scope != nil {
			c.typeScopes[d] = scope
			owner, ownerName = scope, d.Of.Name.Name
			defer c.closeScope()
		}

		ofType = c.typ(d.Of)
	}

	var recv *Var
	if d.Recv != nil {
		scope, ok := c.receiverParams(d.Recv.Type, "a method's receiver")
		if !ok {
			fn.typ = NewSignature(nil, nil, Unit)
			c.badMethods[d] = true // reported once; its signature and body would only repeat the error
			return
		}

		if scope != nil {
			c.typeScopes[d] = scope
			owner, ownerName = scope, receiverName(d.Recv.Type)
			defer c.closeScope()
		}

		recv = NewVar(d.Recv.Pos(), d.Recv.Name.Name, c.typ(d.Recv.Type))
	}

	var tparams []*TypeParam
	if len(d.TypeParams) > 0 {
		for _, tp := range d.TypeParams {
			if owner != nil && owner.Lookup(tp.Name.Name) != nil { // its own would hide the type's in the signature and body
				c.errorf(tp.Name.Pos(), tp.Name.Name+" is already a type parameter of "+ownerName+": name "+d.Name.Name+"'s own another way")
			}
		}

		tparams = c.typeParams(d.TypeParams)
		c.typeScopes[d] = c.resolveBounds(d.TypeParams, tparams)
		defer c.closeScope()
	}

	params, variadic := c.params(d.Params)
	sig := NewSignature(recv, params, c.results(d.Results))
	sig.tparams = tparams
	sig.variadic = variadic
	fn.typ = sig
	c.checkSources(d.Name.Name, fn.typ.(*Signature), d.Name.Pos())
	if c.pkg.path == "" && d.Recv == nil && d.Name.Name == "main" && (len(params) > 0 || sig.results != Unit || tparams != nil) {
		c.errorf(d.Name.Pos(), "func main must have no type parameters, no parameters and no results: the program calls it with none and reads os.Args for its arguments")
	}

	if d.Body == nil {
		switch {
		case c.pkg.std: // the runtime defines it
		case c.pkg.path == "":
			c.errorf(d.Name.Pos(), "missing function body") // a program binds C through a package, not in its own file
		default:
			c.checkBinding(d, sig) // a package's binding to C
		}
	}

	if d.Of != nil {
		c.attachFunc(d, fn, ofType)
		return
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

// attachFunc attaches a function of a type, func Point.Parse, to t, the
// type it names, a declared type of this package, beside its methods,
// whose names it shares: a type has one member of each name.
func (c *Checker) attachFunc(d *ast.FuncDecl, fn *Func, t Type) {
	c.recordDef(d.Name, fn)
	if t == Typ[Invalid] {
		return // reported where the type is written
	}

	named, ok := t.(*Named)
	if !ok {
		c.errorf(d.Of.Pos(), "a function of a type is declared on a declared type: func Point."+d.Name.Name+"(...)")
		return
	}

	c.recordUse(d.Of.Name, named.obj) // the type it belongs to, which a backend names it after

	if old := named.AddMethod(fn); old != nil {
		c.errorf(d.Name.Pos(), named.obj.name+"."+d.Name.Name+" redeclared; first declared at "+c.lineCol(old.Pos()))
	}
}

func (c *Checker) resolveVar(d *ast.VarDecl) {
	if d.Type == nil {
		return
	}

	if d.Const {
		return // its type was given with its value (checkConst)
	}

	t := c.typ(d.Type)
	for _, name := range d.Names {
		if v, ok := c.pkg.scope.Lookup(name.Name).(*Var); ok {
			v.typ = t
		}
	}
}

// refuseOwner refuses, once, a function of a type declared on what can
// have none: a trait, a generic one too, func Show.Default(), whose
// functions belong to the types that implement it; and a declared type
// of the language, func Result<T, E>.Of(), which no package adds to. It
// reports whether it did.
func (c *Checker) refuseOwner(d *ast.FuncDecl) bool {
	if c.languageResult(d.Of) {
		c.errorf(d.Of.Pos(), "cannot define new functions on non-local type Result")
		return true
	}

	_, obj := c.scope.LookupParent(d.Of.Name.Name)
	tn, ok := obj.(*TypeName)
	if !ok {
		return false
	}

	switch t := tn.typ.(type) {
	case *Trait:
		c.errorf(d.Of.Pos(), t.obj.name+" is a trait: it lists the functions it requires, func "+d.Name.Name+"(...), and each type that implements it declares its own")
		return true
	case *Named:
		if tn.pkg != c.pkg {
			c.errorf(d.Of.Pos(), "cannot define new functions on non-local type "+t.obj.name)
			return true
		}
	}

	return false
}

// receiverName returns the name of the type a receiver's type t names,
// List of (l mut List<T>).
func receiverName(t ast.Type) string {
	if r, ok := t.(*ast.RefType); ok {
		t = r.Elem
	}

	if n, ok := t.(*ast.NamedType); ok {
		return n.Name.Name
	}

	return ""
}

// receiverParams opens the scope of the type parameters a method's
// receiver names, (l mut List<T>), or a function of a type names,
// func List<T>.Of, when its type is generic, and returns it; it returns
// nil, with no scope open, for any other type. Each argument of the
// type must be a plain name, as it declares the parameter in that
// position; a name that is already a type, List<int>, is refused rather
// than shadowing it, unless it is the declaration's own name for that
// parameter, and what says which declaration it is in the message. ok
// is false after an error, which leaves the function out.
func (c *Checker) receiverParams(t ast.Type, what string) (scope *Scope, ok bool) {
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
			c.errorf(arg.Pos(), what+" names the type's parameters: "+named.String())
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
