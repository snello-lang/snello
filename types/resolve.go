// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"

	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/token"
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

// resolveBodies walks every function body, declaring parameters and
// locals as they appear and resolving each use to its object.
func (c *Checker) resolveBodies(f *ast.File) {
	for i, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			c.funcBody(c.objs[i].(*Func).typ.(*Signature), d.Recv, d.Params, d.Body)
		case *ast.VarDecl:
			c.exprs(d.Values)
		}
	}
}

// funcBody opens the function scope, declares the receiver and the
// parameters under their names with the variables the signature already
// holds, and walks the statements.
func (c *Checker) funcBody(sig *Signature, recv *ast.Param, params []*ast.Param, body *ast.BlockStmt) {
	if body == nil {
		return
	}

	scope := c.openScope()
	if recv != nil {
		c.declare(scope, recv.Name, sig.recv)
	}

	for i, p := range params {
		if p.Name != nil {
			c.declare(scope, p.Name, sig.params[i])
		}
	}

	c.stmts(body.List)
	c.closeScope()
}

func (c *Checker) stmts(list []ast.Stmt) {
	for _, s := range list {
		c.stmt(s)
	}
}

func (c *Checker) block(b *ast.BlockStmt) {
	c.openScope()
	c.stmts(b.List)
	c.closeScope()
}

func (c *Checker) stmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.BlockStmt:
		c.block(s)
	case *ast.DeclStmt:
		c.exprs(s.Decl.Values) // the right side cannot see the new names
		var t Type
		if s.Decl.Type != nil {
			t = c.typ(s.Decl.Type)
		}

		c.declareVars(c.scope, s.Decl, t)
	case *ast.ExprStmt:
		c.expr(s.X)
	case *ast.SendStmt:
		c.expr(s.Chan)
		c.expr(s.Value)
	case *ast.IncDecStmt:
		c.expr(s.X)
	case *ast.AssignStmt:
		c.assign(s)
	case *ast.SpawnStmt:
		c.expr(s.Call)
	case *ast.DeferStmt:
		c.expr(s.Call)
	case *ast.ReturnStmt:
		c.exprs(s.Results)
	case *ast.IfStmt:
		c.ifStmt(s)
	case *ast.IfBindStmt:
		c.ifBind(s)
	case *ast.ForStmt:
		c.openScope()
		c.stmtOrNil(s.Init)
		c.exprOrNil(s.Cond)
		c.stmtOrNil(s.Post)
		c.block(s.Body)
		c.closeScope()
	case *ast.RangeStmt:
		c.rangeStmt(s)
	case *ast.SwitchStmt:
		c.openScope()
		c.stmtOrNil(s.Init)
		c.exprOrNil(s.Tag)
		for _, cc := range s.Body {
			c.openScope()
			c.exprs(cc.List)
			c.stmts(cc.Body)
			c.closeScope()
		}

		c.closeScope()
	case *ast.SelectStmt:
		for _, cc := range s.Body {
			c.openScope()
			c.stmtOrNil(cc.Comm)
			c.stmts(cc.Body)
			c.closeScope()
		}
	}
}

// assign resolves an assignment. With :=, a name not yet declared in
// the current scope is declared, one already there is assigned, and at
// least one must be new, as in Go.
func (c *Checker) assign(s *ast.AssignStmt) {
	c.exprs(s.Rhs) // the right side cannot see the new names
	if s.Tok != token.DEFINE {
		for _, lhs := range s.Lhs {
			if !isBlank(lhs) { // _ = x discards x
				c.expr(lhs)
			}
		}

		return
	}

	declared := false
	for _, lhs := range s.Lhs {
		ident, ok := lhs.(*ast.Ident)
		if !ok {
			c.errorf(lhs.Pos(), "non-name on the left side of :=")
			continue
		}

		if isBlank(ident) {
			continue // _ := x declares nothing
		}

		if old := c.scope.Lookup(ident.Name); old != nil {
			c.recordUse(ident, old)
			continue
		}

		c.declare(c.scope, ident, NewVar(ident.Pos(), ident.Name, nil))
		declared = true
	}

	if !declared {
		c.errorf(s.TokPos, "no new variables on the left side of :=")
	}
}

func (c *Checker) ifStmt(s *ast.IfStmt) {
	c.openScope()
	c.stmtOrNil(s.Init)
	c.expr(s.Cond)
	c.block(s.Body)
	c.stmtOrNil(s.Else)
	c.closeScope()
}

// ifBind resolves `if pat := value {`: the value first, then the body
// with the pattern's names bound.
func (c *Checker) ifBind(s *ast.IfBindStmt) {
	c.expr(s.Value)
	c.openScope()
	c.pattern(s.Pat)
	c.block(s.Body)
	c.closeScope()
	c.stmtOrNil(s.Else)
}

func (c *Checker) rangeStmt(s *ast.RangeStmt) {
	c.expr(s.X)
	c.openScope()
	if s.Tok == token.DEFINE {
		c.rangeVar(s.Key)
		c.rangeVar(s.Value)
	} else {
		c.exprOrNil(s.Key)
		c.exprOrNil(s.Value)
	}

	c.block(s.Body)
	c.closeScope()
}

func (c *Checker) rangeVar(e ast.Expr) {
	if e == nil {
		return
	}

	if ident, ok := e.(*ast.Ident); ok {
		c.declare(c.scope, ident, NewVar(ident.Pos(), ident.Name, nil))
		return
	}

	c.errorf(e.Pos(), "non-name on the left side of range :=")
}

func (c *Checker) stmtOrNil(s ast.Stmt) {
	if s != nil {
		c.stmt(s)
	}
}

func (c *Checker) exprOrNil(e ast.Expr) {
	if e != nil {
		c.expr(e)
	}
}

func (c *Checker) exprs(es []ast.Expr) {
	for _, e := range es {
		c.expr(e)
	}
}

// expr resolves the names in an expression. A selector's field or
// method name needs the type of its operand and is left to the
// expression checker; a composite literal's keys are field names and
// are left to it too.
func (c *Checker) expr(e ast.Expr) {
	switch e := e.(type) {
	case *ast.Ident:
		if isBlank(e) {
			c.errorf(e.Pos(), "cannot use _ as a value")
		} else {
			c.lookup(e)
		}
	case *ast.ParenExpr:
		c.expr(e.X)
	case *ast.TupleExpr:
		c.exprs(e.Elts)
	case *ast.SelectorExpr:
		c.expr(e.X)
	case *ast.IndexExpr:
		c.expr(e.X)
		c.expr(e.Index)
	case *ast.SliceExpr:
		c.expr(e.X)
		c.exprOrNil(e.Low)
		c.exprOrNil(e.High)
	case *ast.CallExpr:
		c.expr(e.Fun)
		for _, t := range e.TypeArgs {
			c.typ(t)
		}

		c.exprs(e.Args)
	case *ast.UnaryExpr:
		c.expr(e.X)
	case *ast.BinaryExpr:
		c.expr(e.X)
		c.expr(e.Y)
	case *ast.CoalesceExpr:
		c.expr(e.X)
		c.node(e.Else)
	case *ast.TypeExpr:
		c.typ(e.Type)
	case *ast.TryExpr:
		c.expr(e.X)
	case *ast.CompositeLit:
		c.compositeLit(e)
	case *ast.KeyValueExpr:
		c.expr(e.Key)
		c.expr(e.Value)
	case *ast.FuncLit:
		c.funcLit(e)
	case *ast.IfExpr:
		c.ifExpr(e)
	case *ast.MatchExpr:
		c.matchExpr(e)
	}
}

func (c *Checker) compositeLit(e *ast.CompositeLit) {
	if e.Type != nil {
		c.typ(e.Type)
	}

	for _, elt := range e.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			if _, isName := kv.Key.(*ast.Ident); !isName {
				c.expr(kv.Key) // a map key; a bare name is a field
			}

			c.expr(kv.Value)
			continue
		}

		c.expr(elt)
	}
}

// funcLit resolves a closure: the signature's types, then the body in
// its own scope with the parameters declared. Captured names resolve
// through the enclosing scopes as any name does.
func (c *Checker) funcLit(e *ast.FuncLit) {
	sig := c.typ(e.Type).(*Signature)
	c.funcBody(sig, nil, e.Type.Params, e.Body)
}

func (c *Checker) ifExpr(e *ast.IfExpr) {
	c.expr(e.Cond)
	c.block(e.Then)
	c.node(e.Else)
}

// matchExpr resolves the value, then each arm in its own scope with the
// pattern's bindings declared before the guard and the body.
func (c *Checker) matchExpr(e *ast.MatchExpr) {
	c.expr(e.X)
	for _, arm := range e.Arms {
		c.openScope()
		c.pattern(arm.Pat)
		c.exprOrNil(arm.Guard)
		c.node(arm.Body)
		c.closeScope()
	}
}

// node resolves whatever an arm body or an else may be: an expression,
// a block, or a statement.
func (c *Checker) node(n ast.Node) {
	switch n := n.(type) {
	case ast.Expr:
		c.expr(n)
	case ast.Stmt:
		c.stmt(n)
	}
}

// pattern declares the names a pattern binds in the current scope. A
// bare name that resolves to a variant, such as None or Closed, is a
// use of that variant; any other bare name is a new binding. This is
// the rule the syntax conventions leave to the checker.
func (c *Checker) pattern(p ast.Pattern) {
	switch p := p.(type) {
	case *ast.IdentPat:
		_, obj := c.scope.LookupParent(p.Name.Name)
		if v, ok := obj.(*Variant); ok {
			c.recordUse(p.Name, v)
			return
		}

		c.declare(c.scope, p.Name, NewVar(p.Name.Pos(), p.Name.Name, nil))
	case *ast.TuplePat:
		for _, elt := range p.Elts {
			c.pattern(elt)
		}
	case *ast.VariantPat:
		c.expr(p.Path)
		for _, arg := range p.Args {
			c.pattern(arg)
		}
	case *ast.OrPat:
		for _, alt := range p.Alts {
			c.pattern(alt)
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
