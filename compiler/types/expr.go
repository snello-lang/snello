// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// expr checks e and records its type. expected is the type the context
// asks for, or nil: it gives a literal its type, None and Some their
// element, and an elided composite literal its type. It is a hint, not
// a check; the caller decides whether the result fits.
func (c *Checker) expr(e ast.Expr, expected Type) operand {
	op := c.exprInternal(e, expected)

	if op.typ == Typ[Invalid] {
		return invalidOperand // reported where the type went wrong; never again
	}

	if op.mode == typeOperand && unparen(e) != c.callee && unparen(e) != c.wantType { // a type stands only before the parentheses of a conversion, or where make wants one
		c.errorf(e.Pos(), exprText(e)+" (type) is not an expression")
		return invalidOperand
	}

	if op.mode != invalid {
		c.recordType(e, op.typ)
		op.expr = e
	}

	return op
}

func (c *Checker) exprInternal(e ast.Expr, expected Type) operand {
	switch e := e.(type) {
	case *ast.Ident:
		return c.ident(e, expected)
	case *ast.BasicLit:
		return operand{value, c.literal(e, expected), nil, nil}
	case *ast.ParenExpr:
		return c.expr(e.X, expected)
	case *ast.TupleExpr:
		return c.tuple(e, expected)
	case *ast.SelectorExpr:
		return c.selector(e)
	case *ast.IndexExpr:
		return c.index(e)
	case *ast.SliceExpr:
		return c.sliceExpr(e)
	case *ast.CallExpr:
		return c.call(e, expected)
	case *ast.UnaryExpr:
		return c.unary(e, expected)
	case *ast.BinaryExpr:
		return c.binary(e, expected)
	case *ast.CoalesceExpr:
		return c.coalesce(e)
	case *ast.SpawnExpr:
		return c.spawnExpr(e)
	case *ast.TypeExpr:
		return operand{typeOperand, c.typ(e.Type), nil, nil}
	case *ast.TryExpr:
		return c.try(e)
	case *ast.CompositeLit:
		return c.compositeLit(e, expected)
	case *ast.KeyValueExpr:
		c.errorf(e.Pos(), "key: value is only allowed inside a composite literal")
	case *ast.FuncLit:
		return c.funcLit(e)
	case *ast.IfExpr:
		op := c.ifExpr(e, expected)
		c.lentBranches(e, op, "if") // once, at the head of an else-if chain, whose every branch eachBranch walks
		return op
	case *ast.PartExpr:
		return c.partExpr(e)
	case *ast.MatchExpr:
		if e.OnType {
			return c.typeMatch(e, expected)
		}

		if len(e.Arms) > 0 {
			if _, ok := e.Arms[0].Pat.(*ast.PartsPat); ok {
				return c.variantMatch(e, expected)
			}
		}

		return c.matchExpr(e, expected)
	}

	return invalidOperand
}

// ident resolves a name and says what kind of thing it is.
func (c *Checker) ident(e *ast.Ident, expected Type) operand {
	if isBlank(e) {
		c.errorf(e.Pos(), "cannot use _ as a value")
		return invalidOperand
	}

	if e.Name == "super" {
		c.errorf(e.Pos(), "super must be followed by a method: super.Method()")
		return invalidOperand
	}

	obj := c.lookup(e)
	if obj == nil {
		return invalidOperand
	}

	switch obj := obj.(type) {
	case *Var:
		if obj.typ == nil {
			if c.uninferred[obj] { // a package variable whose value, which gives its type, is checked after this
				c.errorf(e.Pos(), e.Name+" is used before its type is known: declare "+e.Name+" before this, or with its type")
			}

			return invalidOperand // declared with an error
		}

		return operand{variable, c.refine(obj.typ), obj, nil}
	case *Const:
		c.checkConst(obj) // a top-level constant is checked the first time it is needed
		if obj.typ == nil {
			return invalidOperand
		}

		return operand{value, obj.typ, obj, nil}
	case *Part:
		c.errorf(e.Pos(), e.Name+" is a "+partKind(obj)+" of a type, known to the compiler alone: it gives "+partGives(obj))
		return invalidOperand
	case *Func:
		if sig, ok := obj.typ.(*Signature); ok && sig.tparams != nil && c.callee != e {
			c.errorf(e.Pos(), "cannot use the generic function "+obj.name+" without calling it")
			return invalidOperand
		}

		if c.callee != e {
			c.keepValueOf(obj, e.Pos()) // a value: it may keep no parameter
		}

		return operand{value, obj.typ, obj, nil}
	case *TypeName:
		return operand{typeOperand, c.refine(obj.typ), obj, nil} // a type parameter an arm of a match on a type knows, read as what the arm matched
	case *Builtin:
		c.errorf(e.Pos(), obj.name+" must be called")
	case *Variant:
		return c.constantVariant(e, obj, expected)
	case *PkgName:
		c.errorf(e.Pos(), "use of package "+obj.name+" without a selector")
	}

	return invalidOperand
}

// constantVariant types a variant written without arguments: Closed,
// None. One with fields is a constructor and must be called.
func (c *Checker) constantVariant(e *ast.Ident, v *Variant, expected Type) operand {
	if v.parent == nil && v != NoneVariant {
		c.errorf(e.Pos(), v.name+" must be called: "+v.name+"(x)")
		return invalidOperand
	}

	if v == NoneVariant {
		if expected == Typ[Invalid] {
			return invalidOperand
		}

		if expected != nil {
			if _, ok := expected.Underlying().(*Option); ok {
				return operand{value, expected, v, nil}
			}
		}

		c.errorf(e.Pos(), "cannot tell the type of None here; the context gives it no type")
		return invalidOperand
	}

	if len(v.fields) > 0 {
		c.errorf(e.Pos(), v.name+" has fields and must be written "+v.name+"(...)")
		return invalidOperand
	}

	t := v.parent
	if t.tparams != nil {
		n, ok := expected.(*Named) // a variant without fields has nothing to infer from but its context
		if !ok || n.Origin() != t {
			if expected != Typ[Invalid] {
				c.errorf(e.Pos(), "cannot infer the type parameter "+t.tparams[0].obj.name+" of "+v.name+": the context gives it no type")
			}

			return invalidOperand
		}

		t = n
	}

	return operand{value, t, v, nil}
}

// selector checks x.Sel: a package member, a field, a method of a
// declared type, or a predeclared method. Fields and methods are found
// through any borrow or owned pointer, so n.value works whether n is a
// Node, a ref Node, or a *Node.
func (c *Checker) selector(e *ast.SelectorExpr) operand {
	if ident, ok := e.X.(*ast.Ident); ok {
		if ident.Name == "super" {
			return c.superCall(e)
		}

		if _, obj := c.scope.LookupParent(ident.Name); obj != nil {
			if pkg, ok := obj.(*PkgName); ok {
				c.recordUse(ident, pkg)
				return c.packageMember(e, pkg)
			}

			if p, ok := obj.(*Part); ok {
				c.recordUse(ident, p)
				return c.partSelector(e, p)
			}
		}
	}

	wantType := c.wantType
	c.wantType = unparen(e.X) // Point.Parse: a type before a function of it
	x := c.base(e.X)
	c.wantType = wantType
	if x.mode == invalid {
		return invalidOperand
	}

	if x.mode == typeOperand {
		return c.typeFunc(e, x.typ)
	}

	base, m := deref(x)
	if base == Typ[Invalid] {
		return invalidOperand // reported where the type went wrong, a ref of it among them
	}

	if e.Sel.Name == "Drop" && implements(base, DropTrait) {
		c.errorf(e.Sel.Pos(), dropByHand(e.X))
		return invalidOperand
	}

	if named, ok := base.(*Named); ok {
		if st, ok := named.Underlying().(*Struct); ok {
			if f := fieldNamed(st.fields, e.Sel.Name); f != nil {
				if !c.visible(named, e.Sel) {
					return invalidOperand
				}

				c.recordUse(e.Sel, f)
				return operand{m, f.typ, f, nil}
			}
		}

		if fn := named.Method(e.Sel.Name); fn != nil {
			if !c.visible(named, e.Sel) {
				return invalidOperand
			}

			if fn.static {
				c.errorf(e.Sel.Pos(), fn.name+" is a function of "+named.obj.name+", which takes no value of it: call "+named.obj.name+"."+fn.name+"(...)")
				return invalidOperand
			}

			c.recordUse(e.Sel, fn)
			sig := fn.typ.(*Signature)
			if sig.tparams != nil && c.callee != ast.Expr(e) { // the selector itself, which call unparenthesized
				c.errorf(e.Sel.Pos(), "cannot use the generic method "+fn.name+" without calling it")
				return invalidOperand
			}

			c.receiver(e, x, sig)
			if c.callee != ast.Expr(e) {
				c.keepValueOf(fn, e.Sel.Pos()) // a method as a value: it may keep no parameter
			}

			return operand{value, fn.typ, fn, nil}
		}
	}

	if sig := builtinMethod(base, e.Sel.Name); sig != nil {
		if sl, ok := base.Underlying().(*Slice); ok {
			if tr := elementBound(e.Sel.Name); tr != nil && !elementHas(sl.elem, tr) {
				c.errorf(e.Sel.Pos(), e.Sel.Name+" needs the elements to be "+tr.obj.name+": "+sl.elem.String()+" is not")
				return invalidOperand
			}
		}

		if m, ok := base.Underlying().(*Map); ok && e.Sel.Name == "Clone" {
			for _, part := range []Type{m.key, m.value} {
				if !elementHas(part, CloneTrait) {
					c.errorf(e.Sel.Pos(), "Clone needs the keys and values to be Clone: "+part.String()+" is not")
					return invalidOperand
				}
			}
		}

		c.receiver(e, x, sig)
		return operand{value, sig, nil, nil}
	}

	// A trait value, or a type parameter with bounds: the traits' fields
	// and methods, with the value's type for Self.
	var f *Var
	var fn *Func
	var param *TypeParam // the parameter whose bounds were searched, for the message
	switch t := base.(type) {
	case *Trait:
		var mentionsSelf bool
		if f, mentionsSelf = t.valueField(e.Sel.Name); mentionsSelf {
			c.errorf(e.Sel.Pos(), "cannot reach "+e.Sel.Name+" through "+t.String()+": its type mentions Self")
			return invalidOperand
		}

		if f == nil {
			var why string
			if fn, why = t.valueMethod(e.Sel.Name); why != "" {
				c.errorf(e.Sel.Pos(), "cannot call "+e.Sel.Name+" through "+t.String()+": "+why)
				return invalidOperand
			}
		}
	case *TypeParam:
		if f = t.boundField(e.Sel.Name); f == nil {
			fn = t.boundMethod(e.Sel.Name)
		}

		if fn != nil && fn.static {
			c.errorf(e.Sel.Pos(), fn.name+" is a function of "+t.obj.name+", which takes no value of it: call "+t.obj.name+"."+fn.name+"(...)")
			return invalidOperand
		}

		param = t
	}

	if (f != nil || fn != nil) && !c.visible(base, e.Sel) {
		return invalidOperand
	}

	if f != nil {
		c.recordUse(e.Sel, f)
		return operand{m, f.typ, f, nil}
	}

	if fn != nil {
		c.recordUse(e.Sel, fn)
		c.receiver(e, x, fn.typ.(*Signature))
		if c.callee != ast.Expr(e) {
			c.keepValueOf(fn, e.Sel.Pos()) // a method as a value: it may keep no parameter
		}

		return operand{value, fn.typ, fn, nil}
	}

	msg := e.Sel.Name + " is not a field or method of " + x.typ.String()
	if param != nil {
		msg += ": no bound of " + param.obj.name + " declares it"
	}

	c.errorf(e.Sel.Pos(), msg)
	return invalidOperand
}

// receiver checks the receiver of a method against what the method
// asks of it: a value receiver takes it (takeValue); a mut receiver
// needs a place that can be changed, which a value reached through a
// ref is not, nor a constant. The borrow the call creates is checked in
// call, after the arguments.
func (c *Checker) receiver(e *ast.SelectorExpr, x operand, sig *Signature) {
	r, ok := sig.recv.typ.(*Ref)
	if !ok {
		c.takeValue(e.X, x, sig.recv.typ)
		return
	}

	if r.mut && x.mode == borrowed {
		c.errorf(e.X.Pos(), "cannot borrow "+exprText(e.X)+" as mut through a ref "+c.throughWhat(e.X))
	} else if r.mut && c.unchangeable(e.X, x) {
		c.errorf(e.X.Pos(), e.Sel.Name+" lends its receiver mut, which "+needsPlace(e.X))
	} else if r.mut && isBorrow(x.typ) && !isMut(x.typ) { // the receiver is itself a ref borrow
		c.errorf(e.X.Pos(), "cannot borrow "+exprText(e.X)+" as mut: "+exprText(e.X)+" is a ref "+c.throughWhat(e.X))
	}
}

// typeFunc checks T.Name, a function of the type t, func Point.Parse,
// or of the type a type parameter stands for, T.Parse with T bounded by
// a trait that requires it: a value of the function's signature, which
// a call calls directly.
func (c *Checker) typeFunc(e *ast.SelectorExpr, t Type) operand {
	var fn *Func
	switch t := t.(type) {
	case *Named:
		if _, bare := unparen(e.X).(*ast.Ident); bare && t.tparams != nil { // G.Count: no instance to call it on
			c.errorf(e.X.Pos(), t.obj.name+" is generic: name an instance, "+t.obj.name+"<...>."+e.Sel.Name)
			return invalidOperand
		}

		fn = t.Method(e.Sel.Name)
		if fn != nil && !c.visible(t, e.Sel) {
			return invalidOperand
		}
	case *TypeParam:
		fn = t.boundMethod(e.Sel.Name)
		if fn != nil && !c.visible(t, e.Sel) {
			return invalidOperand
		}
	}

	switch {
	case fn == nil:
		c.errorf(e.Sel.Pos(), noFunction(t, e.Sel.Name))
		return invalidOperand
	case !fn.static:
		c.errorf(e.Sel.Pos(), e.Sel.Name+" is a method of "+t.String()+": call it on a value of it")
		return invalidOperand
	}

	if fn.typ.(*Signature).tparams != nil && c.callee != ast.Expr(e) { // the selector itself, which call unparenthesized
		c.errorf(e.Sel.Pos(), "cannot use the generic function "+fn.name+" without calling it")
		return invalidOperand
	}

	c.recordUse(e.Sel, fn)
	return operand{value, fn.typ, fn, nil}
}

// noFunction says that t has no function name, and, when name is one of
// its variants, how a variant is written.
func noFunction(t Type, name string) string {
	msg := t.String() + " has no function " + name
	if en, ok := t.Underlying().(*Enum); ok {
		for _, v := range en.variants {
			if v.name == name {
				return msg + ": " + name + " is a variant of it, written by its name, " + name
			}
		}
	}

	return msg
}

// packageMember resolves pkg.Name as a value: a function, variable,
// constant or type of the package.
func (c *Checker) packageMember(e *ast.SelectorExpr, pkg *PkgName) operand {
	obj := c.member(pkg, e.Sel)
	if obj == nil {
		return invalidOperand
	}

	switch obj := obj.(type) {
	case *Func:
		if c.callee != ast.Expr(e) {
			c.keepValueOf(obj, e.Pos()) // a value: it may keep no parameter
		}

		return operand{value, obj.typ, obj, nil}
	case *Var:
		return operand{variable, obj.typ, obj, nil}
	case *Const:
		return operand{value, obj.typ, obj, nil}
	case *TypeName:
		return operand{typeOperand, obj.typ, obj, nil}
	case *Variant:
		return c.constantVariant(e.Sel, obj, nil) // io.NotExist: a variant named through its package
	}

	c.errorf(e.Sel.Pos(), "cannot use "+pkg.name+"."+e.Sel.Name+" as a value")
	return invalidOperand
}

// visible reports whether the field or method sel of a value of type t
// may be named here: always inside the package that declares it,
// Declarer's, and elsewhere only when the name is exported. A name that
// is not visible is reported.
func (c *Checker) visible(t Type, sel *ast.Ident) bool {
	if ast.IsExported(sel.Name) {
		return true
	}

	if owner := Declarer(t, sel.Name); owner == nil || owner == c.pkg {
		return true
	}

	c.errorf(sel.Pos(), sel.Name+" of "+t.String()+" is not exported")
	return false
}

// Declarer returns the package that declares the member name of a value
// of type t, the one package whose code may name it when it is not
// exported; nil for a member the language declares, or none. A struct's
// field is the package's that declared the struct, so that a type
// declared over another package's struct does not open its fields, or
// that declared the trait that gives it; a method is the type's
// package's, or, inherited from a trait's default, the trait's; a
// member a trait value or a type parameter's bound has is the
// package's of the trait that declares it.
func Declarer(t Type, name string) *Package {
	switch t := t.(type) {
	case *Named:
		if st, ok := t.Underlying().(*Struct); ok {
			if f := fieldNamed(st.fields, name); f != nil {
				if f.pkg != nil {
					return f.pkg
				}

				return t.obj.pkg
			}
		}

		if fn := t.Method(name); fn != nil {
			if fn.trait != nil {
				return fn.trait.origin().obj.pkg
			}

			return t.obj.pkg
		}
	case *Trait:
		return traitDeclarer([]*Trait{t}, name)
	case *TypeParam:
		return traitDeclarer(t.bounds, name)
	}

	return nil
}

// traitDeclarer returns the package of the trait, among traits and the
// traits they refine, that declares the member name, or nil.
func traitDeclarer(traits []*Trait, name string) *Package {
	for _, tr := range traits {
		if tr == nil {
			continue
		}

		if f, owner := tr.field(name); f != nil {
			return owner.origin().obj.pkg
		}

		if fn, owner := tr.lookup(name); fn != nil {
			return owner.origin().obj.pkg
		}
	}

	return nil
}

// qualified resolves pkg.Name written as a type, a trait or a pattern,
// and records the use of the package name; nil after reporting.
func (c *Checker) qualified(pkgIdent, sel *ast.Ident) Object {
	obj := c.lookup(pkgIdent)
	if obj == nil {
		return nil
	}

	pkg, ok := obj.(*PkgName)
	if !ok {
		c.errorf(pkgIdent.Pos(), pkgIdent.Name+" is not a package")
		return nil
	}

	return c.member(pkg, sel)
}

// packageVariant returns the variant pkg.Name names when the callee of
// a call is one, recording the uses; nil for anything else, which the
// call then resolves as a value and reports if it must.
func (c *Checker) packageVariant(fun ast.Expr) *Variant {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil
	}

	_, obj := c.scope.LookupParent(id.Name)
	pkg, ok := obj.(*PkgName)
	if !ok || pkg.pkg == nil || !ast.IsExported(sel.Sel.Name) {
		return nil
	}

	v, ok := pkg.pkg.scope.Lookup(sel.Sel.Name).(*Variant)
	if !ok {
		return nil
	}

	c.recordUse(id, pkg)
	c.recordUse(sel.Sel, v)
	return v
}

// member resolves a name in an imported package and records the use:
// only an exported name is visible, as in Go. A member of a package the
// importer did not find is not reported: the import was.
func (c *Checker) member(pkg *PkgName, sel *ast.Ident) Object {
	if pkg.missing {
		return nil
	}

	var obj Object
	if pkg.pkg != nil {
		obj = pkg.pkg.scope.Lookup(sel.Name)
	}

	if obj != nil && !ast.IsExported(sel.Name) {
		c.errorf(sel.Pos(), "name "+sel.Name+" not exported by package "+pkg.name)
		return nil
	}

	if obj == nil {
		c.errorf(sel.Pos(), "undefined: "+pkg.name+"."+sel.Name)
		return nil
	}

	c.recordUse(sel, obj)
	return obj
}

// base evaluates what a selector or an index reaches into: a chain of
// places, whose whole is the use its reader checks, or a value made
// there, a call, which is read for its value as the pointer under *p is,
// whatever place the whole is part of.
func (c *Checker) base(x ast.Expr) operand {
	if !isPlaceChain(x) {
		return c.operandOf(x, nil)
	}

	wasInner := c.inner
	c.inner = true
	op := c.expr(x, nil)
	c.inner = wasInner
	return op
}

func (c *Checker) index(e *ast.IndexExpr) operand {
	wasInner := c.inner // the index is itself reached into, m[k].f
	x := c.base(e.X)
	if x.mode == invalid {
		return invalidOperand
	}

	base, m := deref(x)
	switch t := base.Underlying().(type) {
	case *Slice:
		c.intIndex(e.Index)
		return operand{m, t.elem, nil, nil}
	case *Array:
		c.intIndex(e.Index)
		return operand{m, t.elem, nil, nil}
	case *Map:
		if c.noUse { // m[k] = v: the map keeps the key, taken as it is evaluated, before the value
			c.assign(c.operandOf(e.Index, t.key), t.key, e.Index.Pos(), "map key")
			if wasInner { // m[k].f = v writes part of an entry a missing key makes whole from the zero value
				c.zeroWrite(e, t)
			}
		} else {
			c.fits(c.operandOf(e.Index, t.key), t.key, e.Index.Pos(), "map key")
			c.zeroRead(e, t)
		}

		return operand{m, t.value, nil, nil}
	case *Basic:
		if t.kind == String {
			c.intIndex(e.Index)
			c.stringByte = e
			return operand{value, aliases[0], nil, nil} // a byte, by that name
		}
	}

	c.errorf(e.Pos(), "cannot index a value of type "+x.typ.String())
	return invalidOperand
}

// intIndex checks an index expression, which may be of any integer
// type, including a declared one such as NodeID.
func (c *Checker) intIndex(e ast.Expr) {
	op := c.operandOf(e, Typ[Int])
	if op.mode != invalid && !hasInfo(valueType(op), IsInteger) {
		c.errorf(e.Pos(), "index must be an integer, not "+op.typ.String())
	}
}

// mutableView reports whether e is a slice of a value mut may lend:
// one owned, or reached through a mut borrow, not through a ref.
func (c *Checker) mutableView(e ast.Expr) bool {
	s, ok := unparen(e).(*ast.SliceExpr)
	return ok && !c.readOnly[s]
}

// sliceExpr checks x[lo:hi], which lends the piece: a ref string or a
// ref []T, never mut itself; mut x[lo:hi] lends it for changing when x
// is owned or reached through a mut borrow (mutableView).
func (c *Checker) sliceExpr(e *ast.SliceExpr) operand {
	x := c.expr(e.X, nil)
	if x.mode == invalid {
		return invalidOperand
	}

	if e.Low != nil {
		c.intIndex(e.Low)
	}

	if e.High != nil {
		c.intIndex(e.High)
	}

	base, m := deref(x)
	if m == borrowed {
		if c.readOnly == nil {
			c.readOnly = make(map[*ast.SliceExpr]bool)
		}

		c.readOnly[e] = true
	}

	switch t := base.Underlying().(type) {
	case *Slice:
		return operand{value, NewRef(base, false), nil, nil} // a view of the slice's own type, a declared one's methods with it, which passes for a read where a view of its underlying type is wanted; it never owns or grows the elements, so it is never mut
	case *Basic:
		if t.kind == String {
			if _, declared := base.(*Named); declared {
				return operand{value, NewRef(base, false), nil, nil} // a view of a declared string type is one, string(n[1:]) where a ref string is wanted
			}

			return operand{value, NewRef(Typ[String], false), nil, nil} // a constant's untyped string is a string
		}
	}

	c.errorf(e.Pos(), "cannot slice a value of type "+x.typ.String())
	return invalidOperand
}

// unchangeable reports whether x, checked as the operand op, can never
// change, so that lending it mut is a mistake: a constant, by its name,
// written as a literal or computed from constants, or a byte of a
// string. What is written through such a borrow goes to a copy and is
// lost. A call's result is a value made for the statement and may be
// lent: os.Stderr() is the stream itself.
func (c *Checker) unchangeable(x ast.Expr, op operand) bool {
	if op.mode != value {
		return false // a place, or what a borrow reaches
	}

	if _, ok := op.obj.(*Const); ok {
		return true
	}

	return unparen(x) == c.stringByte || c.constantExpr(x)
}

// constantExpr reports whether x is made of constants alone: a literal,
// the name of a constant, of this package or another, an operator over
// constants, -1 or (limit * 2), or a conversion of a constant,
// int(limit), which makes no value of its own.
func (c *Checker) constantExpr(x ast.Expr) bool {
	switch x := unparen(x).(type) {
	case *ast.BasicLit:
		return true
	case *ast.Ident:
		_, ok := c.resolve(x.Name).(*Const)
		return ok
	case *ast.SelectorExpr:
		_, ok := c.packageObject(x).(*Const)
		return ok
	case *ast.UnaryExpr:
		return (x.Op == token.SUB || x.Op == token.NOT || x.Op == token.XOR) && c.constantExpr(x.X)
	case *ast.BinaryExpr:
		return c.constantExpr(x.X) && c.constantExpr(x.Y)
	case *ast.CallExpr:
		return len(x.Args) == 1 && c.isTypeName(x.Fun) && c.constantExpr(x.Args[0])
	}

	return false
}

// isTypeName reports whether x names a type, T or pkg.T, so that a call
// of it is a conversion.
func (c *Checker) isTypeName(x ast.Expr) bool {
	switch x := unparen(x).(type) {
	case *ast.Ident:
		_, ok := c.resolve(x.Name).(*TypeName)
		return ok
	case *ast.SelectorExpr:
		_, ok := c.packageObject(x).(*TypeName)
		return ok
	}

	return false
}

// packageObject returns what pkg.Name names in another package, or nil
// when x is not a name of an imported package's.
func (c *Checker) packageObject(x *ast.SelectorExpr) Object {
	id, ok := x.X.(*ast.Ident)
	if !ok {
		return nil
	}

	pkg, ok := c.resolve(id.Name).(*PkgName)
	if !ok || pkg.pkg == nil {
		return nil
	}

	return pkg.pkg.scope.Lookup(x.Sel.Name)
}

// needsPlace ends the refusal of mut of x, which unchangeable decided.
func needsPlace(x ast.Expr) string {
	return "needs a place, a variable or a field, element or *p of one: " + exprText(x) + " is not one"
}

func (c *Checker) unary(e *ast.UnaryExpr, expected Type) operand {
	switch e.Op {
	case token.REF, token.MUT:
		forBuiltin := c.placeFor
		c.placeFor = false // a mut inside the place is lent for itself
		x := c.expr(e.X, paramHint(expected))
		if x.mode == invalid {
			return invalidOperand
		}

		if e.Op == token.MUT && x.mode == borrowed {
			c.errorf(e.X.Pos(), "cannot borrow "+exprText(e.X)+" as mut through a ref "+c.throughWhat(e.X))
			return invalidOperand
		}

		if e.Op == token.MUT && !forBuiltin && c.unchangeable(e.X, x) { // take and replace refuse every value that is not a place, by their own name
			c.errorf(e.X.Pos(), "mut "+needsPlace(e.X))
			return invalidOperand
		}

		if e.Op == token.MUT && isBorrow(x.typ) && !isMut(x.typ) && !c.mutableView(e.X) { // a borrow of a borrow is a borrow of the value, which a ref may not change
			if s, ok := unparen(e.X).(*ast.SliceExpr); ok {
				c.errorf(e.X.Pos(), "cannot borrow "+exprText(s)+" as mut: it is a slice of "+exprText(s.X)+", reached through a ref "+c.throughWhat(s.X))
			} else {
				c.errorf(e.X.Pos(), "cannot borrow "+exprText(e.X)+" as mut: "+exprText(e.X)+" is a ref "+c.throughWhat(e.X))
			}

			return invalidOperand
		}

		t := x.typ
		if r, ok := t.(*Ref); ok {
			t = r.elem // a borrow of a borrow is a borrow of the value
		}

		c.lentMode = x.mode
		if isMut(x.typ) {
			c.lentMode = mutBorrowed // a mut borrow lent on lends the place it borrows
		}

		return operand{value, NewRef(t, e.Op == token.MUT), nil, nil}
	case token.AND:
		var hint Type
		if p, ok := underlyingPointer(expected); ok {
			hint = p.elem
		}

		x := c.expr(e.X, hint)
		if x.mode == invalid {
			return invalidOperand
		}

		return operand{value, NewPointer(valueType(x)), nil, nil}
	case token.MUL:
		x := c.operandOf(e.X, nil) // reaching through p reads p, even to write what it holds
		if x.mode == invalid {
			return invalidOperand
		}

		switch t := x.typ.(type) {
		case *Ref:
			m := borrowed
			if t.mut {
				m = mutBorrowed
			}

			return operand{m, t.elem, nil, nil}
		case *Pointer:
			return operand{variable, t.elem, nil, nil}
		}

		c.errorf(e.OpPos, "cannot dereference a value of type "+x.typ.String())
		return invalidOperand
	case token.ARROW:
		x := c.expr(e.X, nil)
		if x.mode == invalid {
			return invalidOperand
		}

		if ch, ok := readType(x).Underlying().(*Chan); ok { // every operation on a channel reads the handle through a borrow
			if ch.dir == SendOnly {
				c.errorf(e.OpPos, "cannot receive from a send-only channel")
				return invalidOperand
			}

			return operand{value, NewOption(ch.elem), nil, nil}
		}

		c.errorf(e.OpPos, "cannot receive from a value of type "+x.typ.String())
		return invalidOperand
	}

	if lit, ok := e.X.(*ast.BasicLit); ok && e.Op == token.SUB {
		c.negated = lit
	}

	x := c.expr(e.X, expected)
	c.negated = nil
	if x.mode == invalid {
		return invalidOperand
	}

	t := readType(x)
	if fn := OperatorMethod(t, e.Op, true); fn != nil {
		sig := fn.typ.(*Signature)
		c.takeValue(e.X, x, sig.recv.typ)
		return operand{value, sig.results, nil, nil}
	}

	var need BasicInfo
	switch e.Op {
	case token.SUB:
		need = IsNumeric
	case token.NOT:
		need = IsBoolean
	case token.XOR:
		need = IsInteger
	}

	if !hasInfo(t, need) {
		c.errorf(e.OpPos, "operator "+e.Op.String()+" is not defined on "+x.typ.String())
		return invalidOperand
	}

	return operand{value, t, nil, nil}
}

// binary checks x op y. The two sides must have the same type; a
// literal on one side takes the other side's type, so v*10 is f64 when
// v is. A value reached through a borrow of a Copy type is read as the
// value, so n.key < k works through a ref Node.
func (c *Checker) binary(e *ast.BinaryExpr, expected Type) operand {
	var x, y operand
	if isLiteral(e.X) && !isLiteral(e.Y) {
		y = c.expr(e.Y, expected)
		x = c.expr(e.X, hintOf(y))
	} else {
		x = c.expr(e.X, expected)
		if x.mode == invalid {
			return invalidOperand
		}

		if fn := OperatorMethod(readType(x), e.Op, false); fn != nil {
			return c.operatorCall(e, x, fn)
		}

		if e.Op == token.LAND || e.Op == token.LOR {
			y = c.expr(e.Y, hintOf(x))
		} else {
			y = c.expr(e.Y, hintOf(x))
		}
	}

	if x.mode == invalid || y.mode == invalid {
		return invalidOperand
	}

	xt, yt := readType(x), readType(y)
	if !Identical(xt, yt) {
		c.errorf(e.OpPos, "mismatched types "+xt.String()+" and "+yt.String()+" in "+e.Op.String())
		return invalidOperand
	}

	var need BasicInfo
	result := xt
	switch e.Op {
	case token.LAND, token.LOR:
		need = IsBoolean
	case token.EQL, token.NEQ:
		result = Typ[Bool]
		if !comparable(xt) {
			c.errorf(e.OpPos, "values of type "+xt.String()+" cannot be compared with "+e.Op.String())
			return invalidOperand
		}

		return operand{value, result, nil, nil}
	case token.LSS, token.LEQ, token.GTR, token.GEQ:
		need = IsNumeric | IsString
		result = Typ[Bool]
	case token.ADD:
		need = IsNumeric | IsString
	case token.SUB, token.MUL, token.QUO:
		need = IsNumeric
	default: // %, &, |, ^, <<, >>, &^
		need = IsInteger
	}

	if !hasInfo(xt, need) {
		c.errorf(e.OpPos, "operator "+e.Op.String()+" is not defined on "+xt.String())
		return invalidOperand
	}

	return operand{value, result, nil, nil}
}

// operatorCall checks a + b as a call of the operator's method on the
// left operand, a.Add(b): the receiver is taken by value, so it moves
// unless its type is Copy, as a value receiver does, and before the
// right operand is read, as a receiver is taken before the arguments,
// so a + a on a value that moves is a use after the move. The right
// operand takes its hint from the method's parameter, so v * 2 is a
// float when the method wants one, and must fit it; the result is the
// method's.
func (c *Checker) operatorCall(e *ast.BinaryExpr, x operand, fn *Func) operand {
	sig := fn.typ.(*Signature)
	c.takeValue(e.X, x, sig.recv.typ)
	param := sig.params[0].typ
	if y := c.expr(e.Y, param); y.mode != invalid {
		c.assign(y, param, e.Y.Pos(), "the right operand of "+e.Op.String())
	}

	return operand{value, sig.results, nil, nil}
}

// takeValue checks the operand xe, whose value a method takes as its
// receiver, by value, as recv: an owned value moves unless its type is
// Copy, and a borrow of a value that is not Copy cannot give the value
// up, since it is not the borrower's to give. The operand is read
// first, as any use is.
func (c *Checker) takeValue(xe ast.Expr, x operand, recv Type) {
	if isBorrow(x.typ) && !IsCopy(readType(x)) {
		c.errorf(xe.Pos(), "cannot move "+exprText(xe)+" out of a borrow")
		return
	}

}

// coalesce checks x ?? else. x is an option or a Result, possibly
// through a borrow, in which case the result is the same borrow of the
// inside: cur ?? ... on a mut (*Node?) yields a mut *Node, and s.head
// ?? ... through a ref receiver yields a ref *Node. The else is a value
// of the inside's type, or a block or statement that leaves.
func (c *Checker) coalesce(e *ast.CoalesceExpr) operand {
	x := c.expr(e.X, nil)
	if x.mode == invalid {
		c.node(e.Else, nil)
		return invalidOperand
	}

	inner := x.typ
	through := borrowOf(x.mode) // a place reached through a borrow
	if r, ok := inner.(*Ref); ok {
		through = r // a borrow variable
		inner = r.elem
	}

	var result Type
	switch t := inner.Underlying().(type) {
	case *Option:
		result = t.elem
	case *Result:
		result = t.val
	default:
		c.errorf(e.OpPos, "?? needs an option or a Result on its left, not "+x.typ.String())
		c.node(e.Else, nil)
		return invalidOperand
	}

	if through != nil && (isBorrow(x.typ) || !IsCopy(result)) { // a Copy value reached through a borrow is read, not lent
		result = NewRef(result, through.mut)
	}

	c.elseValue(e.Else, result)
	return operand{value, result, nil, nil} // a value the ?? makes, the option's moved out or the else's, which a borrow of it would borrow as a temporary; never the option's place
}

// elseValue checks the right side of ?? or an else: an expression of
// the given type, or a block or statement that diverges.
func (c *Checker) elseValue(n ast.Node, t Type) {
	switch n := n.(type) {
	case ast.Expr:
		c.assign(c.expr(n, t), t, n.Pos(), "else value")
	case *ast.BlockStmt:
		v := c.blockValue(n, t)
		if v.mode != invalid && !isNever(v.typ) {
			c.errorf(n.Pos(), "the block after ?? must return, break or continue, or end in a value of type "+t.String())
		}
	case ast.Stmt:
		c.stmt(n)
	}
}

// try checks x?: the function's result type says which kind x must be.
func (c *Checker) try(e *ast.TryExpr) operand {
	x := c.expr(e.X, nil)
	if x.mode == invalid {
		return invalidOperand
	}

	if c.sig == nil {
		c.errorf(e.Question, "? is only allowed inside a function")
		return invalidOperand
	}

	xt, _ := deref(x)
	switch want := c.sig.results.Underlying().(type) {
	case *Option:
		if o, ok := xt.Underlying().(*Option); ok {
			return operand{value, o.elem, nil, nil}
		}

		if _, ok := xt.Underlying().(*Result); ok {
			c.errorf(e.Question, "cannot use ? on a Result in a function returning an option")
			return invalidOperand
		}
	case *Result:
		if r, ok := xt.Underlying().(*Result); ok {
			if !Identical(r.err, want.err) {
				c.passError(e, r.err, want.err)
			}

			return operand{value, r.val, nil, nil}
		}

		if _, ok := xt.Underlying().(*Option); ok {
			c.errorf(e.Question, "cannot use ? on an option in a function returning a Result")
			return invalidOperand
		}
	default:
		c.errorf(e.Question, "? is only allowed in a function returning an option or a Result")
		return invalidOperand
	}

	c.errorf(e.Question, "? needs an option or a Result on its left, not "+x.typ.String())
	return invalidOperand
}

// passError settles how x? hands an error of type from to a function
// returning one of a different type to: as a trait value when to is a
// trait from implements, or wrapped in the one variant of to's enum
// whose only field has type from, which Info.Wraps records for the
// backends. Two such variants are ambiguous, and anything else is
// refused with the reason.
func (c *Checker) passError(e *ast.TryExpr, from, to Type) {
	head := "? cannot pass on an error of type " + from.String() + " from a function returning " + c.sig.results.String()
	if tr, ok := to.(*Trait); ok {
		if !convertsToTrait(from, tr) {
			c.errorf(e.Question, head+traitReason(from, tr))
		}

		c.hide(from, e.Question, "a trait value")
		return
	}

	if _, ok := to.(*TypeParam); ok {
		c.errorf(e.Question, head+": "+to.String()+" is a type parameter")
		return
	}

	n, _ := to.(*Named)
	if n == nil {
		c.errorf(e.Question, head)
		return
	}

	en, ok := n.Underlying().(*Enum)
	if !ok {
		c.errorf(e.Question, head+": "+n.String()+" is not an enum")
		return
	}

	var holders []*Variant
	for _, v := range en.variants {
		if len(v.fields) == 1 && Identical(v.fields[0].typ, from) {
			holders = append(holders, v)
		}
	}

	switch len(holders) {
	case 1:
		c.info.Wraps[e] = holders[0]
	case 0:
		c.errorf(e.Question, head+": no variant of "+n.String()+" holds "+article(from.String()))
	default:
		where := ""
		for i, v := range holders {
			if i > 0 {
				where += " and "
			}

			where += "in " + v.name
		}

		c.errorf(e.Question, head+": "+n.String()+" holds "+article(from.String())+" "+where+"; convert it")
	}
}

// article prefixes a type's name with a or an, for a message that
// speaks of one value of it.
func article(name string) string {
	if len(name) > 0 && isVowel(name[0]) {
		return "an " + name
	}

	return "a " + name
}

func isVowel(c byte) bool { c |= 0x20; return c == 'a' || c == 'e' || c == 'i' || c == 'o' || c == 'u' }

// funcLit checks a closure: its body is checked as a function body with
// the closure's own signature, and names it captures resolve through
// the enclosing scopes like any name.
func (c *Checker) funcLit(e *ast.FuncLit) operand {
	sig := c.typ(e.Type).(*Signature)
	outer, loops := c.sig, c.loops
	c.sig, c.loops = sig, nil                                                             // the body is a function of its own: no loop around the closure is open in it
	c.enclosing = append(c.enclosing, outer)                                              // a parameter of the function around the closure may be kept from inside it
	c.keepValues = append(c.keepValues, keepValue{sig: sig, pos: e.Pos(), closure: true}) // a closure is a value: it may keep no parameter
	c.funcBody(sig, nil, e.Type.Params, e.Body)
	c.enclosing = c.enclosing[:len(c.enclosing)-1]
	c.sig, c.loops = outer, loops
	return operand{value, sig, nil, nil}
}

// closureAccesses records what the making of the closure e does to what
// it captures: a borrow, ref or mut, or a move of each, at the closure's
// position, and a read of each when it runs.
func (c *Checker) closureAccesses(e *ast.FuncLit) {
	for _, cap := range c.captured[e] {
		b := cap.borrow(nil, e.Pos())
		what := borrowAccess(cap.mode == byMut)
		switch cap.mode {
		case byCopy:
			continue
		case byMove:
			what = moveAccess // made here, the closure owns what it moves
		}

		c.access(&ast.Ident{NamePos: e.Pos(), Name: cap.v.name}, b.place, what, e.Pos()) // made here, the closure borrows what it captures; named for the message
	}

	for _, cap := range c.captured[e] {
		if cap.mode != byCopy {
			c.flowUse(cap.v, e.Pos()) // the closure reads what it captures when it runs: a borrow variable captured keeps what it views lent, so capturing the viewed place mut collides, as it does outside
		}
	}
}

// ifExpr checks if c { a } else { b } as an expression: both branches
// yield a value of one type, and a branch that leaves fits any type.
func (c *Checker) ifExpr(e *ast.IfExpr, expected Type) operand {
	c.condition(e.Cond)
	a := c.blockValue(e.Then, expected)
	var b operand
	switch els := e.Else.(type) {
	case *ast.BlockStmt:
		b = c.blockValue(els, expected)
	case *ast.IfExpr:
		b = c.ifExpr(els, expected)
	}

	return c.unify(a, b, e.If, "if")
}

// lentBranches refuses a branch of e, an if or a match, that gives an
// owned value where e gives a borrow, result being e's operand: an if
// or a match moves the value of the branch it takes into its result,
// and a value moved into a borrow no one owns, so nothing drops it and
// nothing holds it, a borrow of a value that is gone. A branch that is
// a borrow, a literal or a constant, which lives for the whole program,
// or one that never yields gives a borrow, and a value that only holds
// one, a struct with a ref field, is owned; a nested if or match is a
// branch of the type it gives, having checked its own branches. what
// names e, for the message.
func (c *Checker) lentBranches(e ast.Expr, result operand, what string) {
	if result.mode == invalid || !isBorrow(result.typ) {
		return
	}

	eachBranch(e, func(v ast.Expr) {
		t := c.info.Types[unparen(v)]
		if t != nil && !isBorrow(t) && !isNever(t) && !c.static(v) { // a value that holds a borrow is still owned: it is moved, the borrow inside it with it
			fix := "make it a variable first and lend that"
			if _, kind := c.placeOf(v); kind != notPlace {
				fix = "lend it, ref " + exprText(v)
			}

			c.errorf(v.Pos(), "the "+what+" gives a borrow, and this branch an owned value, which it would move into its result and never drop: "+fix+", or give every branch an owned value")
		}
	})
}

// blockValue checks a block whose last statement may be an expression
// giving the block's value, as in an if expression or a match arm. A
// block that returns, breaks or continues has the type never; one that
// ends in an ordinary statement is ().
func (c *Checker) blockValue(b *ast.BlockStmt, expected Type) operand {
	c.openScope()
	defer c.closeScope()
	if len(b.List) == 0 {
		return operand{value, Unit, nil, nil}
	}

	last := len(b.List) - 1
	c.stmts(b.List[:last])
	if es, ok := b.List[last].(*ast.ExprStmt); ok {
		op := c.expr(es.X, expected)
		op.expr = nil // moved here: what takes the block's value takes a value made here
		return op
	}

	if c.stmt(b.List[last]) {
		return operand{value, Typ[Never], nil, nil}
	}

	return operand{value, Unit, nil, nil}
}

// unify gives two branches one type: a branch that never yields fits
// the other, and otherwise the two must be assignable one way.
func (c *Checker) unify(a, b operand, pos token.Pos, what string) operand {
	switch {
	case a.mode == invalid || b.mode == invalid:
		return invalidOperand
	case isNever(a.typ):
		return b
	case isNever(b.typ):
		return a
	case assignable(b, valueType(a)):
		return operand{value, valueType(a), nil, nil}
	case assignable(a, valueType(b)):
		return operand{value, valueType(b), nil, nil}
	}

	c.errorf(pos, what+" branches have different types: "+a.typ.String()+" and "+b.typ.String())
	return invalidOperand
}

// dropByHand is the message for a program reaching a type's Drop as a
// method: Drop runs when its value goes, once, and the language runs
// it, so a call by hand, a method value or a deferred call of it would
// run it a second time on a value whose resources are gone. Dropping
// a value early is moving it into the blank identifier.
func dropByHand(x ast.Expr) string {
	return "cannot call Drop by hand: it runs once, when " + exprText(x) + " goes; to drop it now, write _ = " + exprText(x)
}
