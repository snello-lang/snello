// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"slices"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// Reflection at compile time. A generic function asks which type its
// type parameter is with a match on it, match T { int => ..., []E =>
// ..., struct => ... }, and an arm that matched a kind reaches the
// type's parts, for f := range fields(T). The function is checked once,
// as any generic function: an arm that matched a type is checked with
// the parameter read as that type, a shape's names, E in []E, are type
// parameters of the arm's own, and a part's type is one of the loop's
// own. The backends keep, for each type the function is compiled for,
// the one arm the type matches, and write each loop out once per part.

// typeMatch checks a match on a type parameter. An arm listing several
// types is first made one arm for each, with a copy of its body, so
// that each is checked, and its types recorded, for its own type. The
// arms are paths as a match's are: one of them is compiled for a type.
func (c *Checker) typeMatch(e *ast.MatchExpr, expected Type) operand {
	tp := c.typeParamOf(e.X)
	splitAlternatives(e)
	result := operand{value, Typ[Never], nil, nil}
	f := c.forkAt()
	for _, arm := range e.Arms {
		c.openScope()
		known := c.matchArm(arm, tp)
		want := c.refine(expected) // what the arm knows of the type parameter, in the type the match is to have
		v := c.node(arm.Body, want)
		if x, ok := arm.Body.(ast.Expr); ok {
			c.pin(x, v.typ)
		}

		c.maybeMove(v, v.typ)
		if expected != nil && v.mode != invalid && v.typ != Typ[Never] {
			c.assign(v, want, arm.Arrow, "match arm")
			v.typ = expected // the arm's value, read outside it, where one arm is compiled for each type
		}

		result = c.unify(result, v, arm.Arrow, "match")
		c.forget(known)
		c.endScope(nodeEnd(arm.Body))
		c.branchEnd(&f)
	}

	c.joinAt(&f, true)
	return result
}

// typeParamOf returns the type parameter a match on a type names, or
// nil, reported, when the name is not one an arm can learn more of.
func (c *Checker) typeParamOf(x ast.Expr) *TypeParam {
	if sel, ok := x.(*ast.SelectorExpr); ok { // match f.type: the type of a field a loop gives
		if p := c.partNamed(sel.X); p != nil && !p.variant {
			return p.typ.(*TypeParam)
		}

		c.errorf(sel.Pos(), exprText(sel)+" is no field's type: match f.type takes the f of for f := range fields(T)")
		return nil
	}

	id := x.(*ast.Ident) // the parser reads match T as one on a type only for a name or a field's type
	obj := c.lookup(id)
	if obj == nil {
		return nil
	}

	tn, _ := obj.(*TypeName)
	if tn == nil {
		c.errorf(id.Pos(), id.Name+" is a value here, which hides the type parameter: a match on a type is on a type parameter, match "+id.Name+" with no value of that name in scope")
		return nil
	}

	tp, ok := tn.typ.(*TypeParam)
	if !ok {
		c.errorf(id.Pos(), id.Name+" is not a type parameter: a match on a type asks which type a generic function was given")
		return nil
	}

	if t, known := c.refined[tp]; known {
		c.errorf(id.Pos(), id.Name+" is known here to be "+t.String()+": a match on it would take one arm")
		return nil
	}

	return tp
}

// splitAlternatives makes each arm of e that lists several types, i8 |
// i16, one arm for each type, the first with the body written and the
// others with copies of it.
func splitAlternatives(e *ast.MatchExpr) {
	n := 0
	for _, arm := range e.Arms {
		if p, ok := arm.Pat.(*ast.TypePat); ok && len(p.Types) > 1 {
			n += len(p.Types) - 1
		}
	}

	if n == 0 {
		return
	}

	arms := make([]*ast.MatchArm, 0, len(e.Arms)+n)
	for _, arm := range e.Arms {
		p, ok := arm.Pat.(*ast.TypePat)
		if !ok || len(p.Types) < 2 {
			arms = append(arms, arm)
			continue
		}

		for i, t := range p.Types {
			body := arm.Body
			if i > 0 {
				body = ast.Clone(arm.Body)
			}

			arms = append(arms, &ast.MatchArm{Pat: &ast.TypePat{Types: []ast.Type{t}}, Arrow: arm.Arrow, Body: body})
		}
	}

	e.Arms = arms
}

// An armKnown is what an arm of a match on a type made known of the
// type parameter tp, a type or a kind, with what was known of it before,
// which forget puts back. It is a value, holding nothing of the Checker,
// so that knowing it keeps the Checker off the heap.
type armKnown struct {
	tp      *TypeParam // nil when the arm made nothing known
	kind    bool
	had     bool
	oldType Type
	oldKind string
}

// matchArm records what the arm matches the type parameter tp as, and
// makes it known while the arm's body is checked: a type, read in place
// of tp, after its shape's names are declared as type parameters of the
// arm's own; a kind, which lets the body reach tp's parts. It returns
// what forget puts back.
func (c *Checker) matchArm(arm *ast.MatchArm, tp *TypeParam) armKnown {
	var ta TypeArm
	var known armKnown
	switch p := arm.Pat.(type) {
	case *ast.TypePat:
		if p.Kind != "" {
			ta.Kind = p.Kind
			if tp != nil {
				known = armKnown{tp: tp, kind: true}
				known.oldKind, known.had = c.kinds[tp]
				if c.kinds == nil {
					c.kinds = make(map[*TypeParam]string)
				}

				c.kinds[tp] = p.Kind
			}

			break
		}

		ta.Binds = c.shapeNames(p.Types[0])
		ta.Type = c.typ(p.Types[0])
		if tp != nil && ta.Type != Typ[Invalid] && !Identical(ta.Type, tp) && contains(ta.Type, tp) {
			c.errorf(p.Types[0].Pos(), "no type is "+ta.Type.String()+": it holds "+tp.obj.name+", the type the match is on; a shape names its parts with names of its own")
			ta.Type = Typ[Invalid]
		}

		if tp != nil && ta.Type != Typ[Invalid] {
			c.armFits(tp, ta.Type, p.Types[0].Pos())
			known = armKnown{tp: tp}
			known.oldType, known.had = c.refined[tp]
			if c.refined == nil {
				c.refined = make(map[*TypeParam]Type)
			}

			c.refined[tp] = ta.Type
		}
	case *ast.WildcardPat:
	}

	if c.info != nil {
		if c.info.Arms == nil {
			c.info.Arms = make(map[*ast.MatchArm]TypeArm)
		}

		c.info.Arms[arm] = ta
	}

	return known
}

// forget puts back what was known of a type parameter before an arm
// made more known, as matchArm returned it.
func (c *Checker) forget(k armKnown) {
	switch {
	case k.tp == nil:
	case k.kind && k.had:
		c.kinds[k.tp] = k.oldKind
	case k.kind:
		delete(c.kinds, k.tp)
	case k.had:
		c.refined[k.tp] = k.oldType
	default:
		delete(c.refined, k.tp)
	}
}

// shapeNames declares, in the arm's scope, each name of the shape t
// that names no type, E in []E and K and V in map[K]V, as a type
// parameter of the arm's own, which the shape binds, and returns them.
// A map's key is bound with Hash, which a map asks of its key, so that
// the shape can be written; any key a map has has it.
func (c *Checker) shapeNames(t ast.Type) []*TypeParam {
	var binds []*TypeParam
	var walk func(t ast.Type, key bool)
	walk = func(t ast.Type, key bool) {
		switch t := t.(type) {
		case *ast.NamedType:
			if t.Pkg == nil && len(t.Args) == 0 {
				if _, obj := c.scope.LookupParent(t.Name.Name); obj == nil {
					tn := NewTypeName(t.Name.Pos(), t.Name.Name, nil)
					p := NewTypeParam(tn, len(binds))
					if key {
						p.bounds = []*Trait{HashTrait}
					}

					binds = append(binds, p)
					c.scope.Insert(tn)
					return
				}
			}

			for _, a := range t.Args {
				walk(a, false)
			}
		case *ast.OptionType:
			walk(t.Elem, false)
		case *ast.SliceType:
			walk(t.Elem, false)
		case *ast.PointerType:
			walk(t.Elem, false)
		case *ast.MapType:
			walk(t.Key, true)
			walk(t.Value, false)
		}
	}

	walk(t, false)
	return binds
}

// armFits refuses an arm whose type the type parameter tp can never
// be: one without tp's bounds.
func (c *Checker) armFits(tp *TypeParam, t Type, pos token.Pos) {
	for _, b := range tp.bounds {
		if b != nil && !implements(t, b) && !mentionsParam(t) {
			c.errorf(pos, tp.obj.name+" is never "+t.String()+": "+t.String()+" does not implement "+b.String())
			return
		}
	}
}

// mentionsParam reports whether t holds a type parameter, a shape's
// name, whose type argument may give t a bound it does not show.
func mentionsParam(t Type) bool {
	return holdsTypeParam(t)
}

// refine returns t as an arm of a match on a type reads it: with what
// the arm matched in place of the type parameter it knows of. Outside
// such an arm, t itself.
func (c *Checker) refine(t Type) Type {
	if len(c.refined) == 0 || t == nil { // outside an arm, as nearly every name is: small enough to inline
		return t
	}

	return c.refineInArm(t)
}

// refineInArm is refine inside an arm of a match on a type: what the arm
// matched in place of the type parameter, to a fixed point.
func (c *Checker) refineInArm(t Type) Type {
	for range c.refined { // an arm inside an arm: T is map[K]V, and K is string; each pass places one level
		u := Subst(t, c.refined)
		if Identical(u, t) {
			break
		}

		t = u
	}

	return t
}

// ChooseArm returns the index of the first of arms, those of a match on
// a type, that the type t takes, with what the arm's shape binds, E of
// []E, given t; -1 when none does. A type takes an arm of its own type,
// and of no other: a type declared over int takes no int arm, since the
// arm reads the parameter as int, and what it builds from it would be
// of another type than the instance's; a shape takes a type of its
// form, binding its names; a kind takes any struct or enum; and an arm
// with neither takes any type. The checker asks it for each type a reflecting function is
// used with, and the backends ask it again when they compile the
// function for that type, so the two agree.
func ChooseArm(arms []TypeArm, t Type) (int, map[*TypeParam]Type) {
	for i, a := range arms {
		switch {
		case a.Kind != "":
			if kindOf(t) == a.Kind {
				return i, nil
			}
		case a.Type == nil:
			return i, nil
		case len(a.Binds) == 0:
			if Identical(t, a.Type) {
				return i, nil
			}
		default:
			binds := make(map[*TypeParam]Type, len(a.Binds))
			if unifyShape(a.Type, t, a.Binds, binds) {
				return i, binds
			}
		}
	}

	return -1, nil
}

// kindOf returns "struct" or "enum" for a declared struct or enum type,
// or "".
func kindOf(t Type) string {
	n, ok := t.(*Named)
	if !ok {
		return ""
	}

	switch n.Underlying().(type) {
	case *Struct:
		return "struct"
	case *Enum:
		return "enum"
	}

	return ""
}

// unifyShape reports whether t has the form of the shape s, whose type
// parameters params stand for any type, and records in binds what each
// stands for in t. A parameter met twice must stand for one type.
func unifyShape(s, t Type, params []*TypeParam, binds map[*TypeParam]Type) bool {
	if p, ok := s.(*TypeParam); ok && slices.Contains(params, p) {
		if had, ok := binds[p]; ok {
			return Identical(had, t)
		}

		binds[p] = t
		return true
	}

	switch s := s.(type) {
	case *Option:
		u, ok := t.(*Option)
		return ok && unifyShape(s.elem, u.elem, params, binds)
	case *Slice:
		u, ok := t.(*Slice)
		return ok && unifyShape(s.elem, u.elem, params, binds)
	case *Pointer:
		u, ok := t.(*Pointer)
		return ok && unifyShape(s.elem, u.elem, params, binds)
	case *Map:
		u, ok := t.(*Map)
		return ok && unifyShape(s.key, u.key, params, binds) && unifyShape(s.value, u.value, params, binds)
	case *Named:
		u, ok := t.(*Named)
		if !ok || s.Origin() != u.Origin() || len(s.targs) != len(u.targs) {
			return false
		}

		for i := range s.targs {
			if !unifyShape(s.targs[i], u.targs[i], params, binds) {
				return false
			}
		}

		return true
	}

	return Identical(s, t)
}

// partsCall returns the call and the built-in when x is fields(...) or
// variants(...), the parts a loop goes through; nil otherwise, a name
// that shadows them included.
func (c *Checker) partsCall(x ast.Expr) (*ast.CallExpr, *Builtin) {
	call, ok := x.(*ast.CallExpr)
	if !ok {
		return nil, nil
	}

	id, ok := call.Fun.(*ast.Ident)
	if !ok {
		return nil, nil
	}

	_, obj := c.scope.LookupParent(id.Name)
	b, ok := obj.(*Builtin)
	if !ok || b.id != BuiltinFields && b.id != BuiltinVariants {
		return nil, nil
	}

	c.recordUse(id, b)
	return call, b
}

// partsLoop checks for f := range fields(T), or variants(T): the loop
// names one part, a field or a variant, and its body is checked once,
// with the part's type a type parameter of the loop's own. It is a loop
// for the move and borrow passes, since its body is written out once
// per part, and one that no break or continue leaves.
func (c *Checker) partsLoop(s *ast.RangeStmt, call *ast.CallExpr, b *Builtin) {
	c.openScope()
	defer c.closeScope()
	of, in := c.partsOf(call, b)
	key, _ := s.Key.(*ast.Ident)
	if key == nil || s.Value != nil || s.Tok != token.DEFINE {
		c.errorf(s.For, "a loop over a type's parts names one part: for f := range "+b.name+"(T)")
	} else if of != nil {
		part := &Part{object: object{key.Name, nil, key.Pos(), c.pkg}, of: of, in: in, variant: b.id == BuiltinVariants}
		if !part.variant {
			part.typ = NewTypeParam(NewTypeName(key.Pos(), key.Name+".type", nil), 0)
		}

		c.scope.Insert(part)
		c.recordDef(key, part)
	}

	c.enterRegion(true, false, s.For)
	r := &c.regions[len(c.regions)-1]
	r.parts = true
	r.floor, r.end = c.scope.parent, s.Body.Rbrace
	c.flowLoopBody()
	c.block(s.Body)
	c.leaveRegion()
}

// partsOf returns what fields(x) or variants(x) goes through: a type
// parameter an arm matched as a struct, for fields, or as an enum, for
// variants; or a variant a loop gives, for its fields, with the enum's
// type parameter. nil, reported, for anything else.
func (c *Checker) partsOf(call *ast.CallExpr, b *Builtin) (Type, *Part) {
	want := "struct"
	if b.id == BuiltinVariants {
		want = "enum"
	}

	if len(call.Args) != 1 {
		c.errorf(call.Lparen, b.name+" takes one type: "+b.name+"(T)")
		return nil, nil
	}

	id, ok := call.Args[0].(*ast.Ident)
	if ok {
		switch obj := c.lookup(id).(type) {
		case *TypeName:
			if tp, ok := obj.typ.(*TypeParam); ok && c.kinds[tp] == want {
				return tp, nil
			}
		case *Part:
			if obj.variant && b.id == BuiltinFields {
				return obj.of, obj
			}
		case nil:
			return nil, nil
		}
	}

	c.errorf(call.Args[0].Pos(), b.name+" takes a type parameter that an arm "+want+" of a match on it matched, "+b.name+"(T)"+variantFields(b))
	return nil, nil
}

func variantFields(b *Builtin) string {
	if b.id == BuiltinFields {
		return ", or a variant a loop gives, fields(c)"
	}

	return ""
}

// partNamed returns the part x names, or nil.
func (c *Checker) partNamed(x ast.Expr) *Part {
	id, ok := x.(*ast.Ident)
	if !ok {
		return nil
	}

	_, obj := c.scope.LookupParent(id.Name)
	p, _ := obj.(*Part)
	if p != nil {
		c.recordUse(id, p)
	}

	return p
}

// partSelector checks what a part gives: a field's name, f.name, a
// constant string in each copy of the loop; its default, f.default, an
// option of its type; a variant's name, c.name. Its type, f.type, is a
// type and no value.
func (c *Checker) partSelector(e *ast.SelectorExpr, p *Part) operand {
	switch {
	case e.Sel.Name == "name":
		return operand{value, Typ[String], nil, nil}
	case e.Sel.Name == "default" && !p.variant:
		return operand{value, NewOption(p.typ), nil, nil}
	case e.Sel.Name == "type" && !p.variant:
		c.errorf(e.Pos(), p.name+".type is a type, written where a type is: f<"+p.name+".type>(...)")
	default:
		c.errorf(e.Sel.Pos(), p.name+" is a "+partKind(p)+" of a type, which gives "+partGives(p)+", not "+e.Sel.Name)
	}

	return invalidOperand
}

func partKind(p *Part) string {
	if p.variant {
		return "variant"
	}

	return "field"
}

func partGives(p *Part) string {
	if p.variant {
		return "its name, " + p.name + ".name, and its fields, fields(" + p.name + ")"
	}

	return p.name + ".name, " + p.name + ".type and " + p.name + ".default"
}

// partExpr checks v.[f]: the field f of v, whose type is the type
// parameter whose fields the loop gives f from. It is lent: changed
// through a mut place, read through any, and never moved, since the
// type may implement Drop, whose Drop reads every field.
func (c *Checker) partExpr(e *ast.PartExpr) operand {
	x := c.expr(e.X, nil)
	p := c.partNamed(e.Part)
	if x.mode == invalid {
		return invalidOperand
	}

	if p == nil {
		c.errorf(e.Part.Pos(), e.Part.Name+" is no part a loop gives: v.[f] reaches the field f of for f := range fields(T)")
		return invalidOperand
	}

	base, m := deref(x)
	if p.in != nil && !c.holdsVariant(e.X, p.in) {
		c.errorf(e.Lbrack, "v.[f] reaches a field of the variant "+p.in.name+" in the arm of a match written for it, of the value the match is on")
		return invalidOperand
	}

	if p.variant || !Identical(base, p.of) {
		c.errorf(e.Lbrack, "v.[f] reaches a field of a value of the type whose fields f is one of, "+p.of.String()+", not of "+base.String())
		return invalidOperand
	}

	if m == variable || m == value {
		m = mutBorrowed // lent, never moved
	}

	c.sawParts = true
	return operand{m, c.refine(p.typ), nil, nil} // in an arm of match f.type, the field reads as the type the arm matched
}

// selectParts replaces each v.[f] of the checked body with a selector,
// v.f, whose field a backend names in each copy of the loop it writes,
// f's field in that copy; the selector keeps the type the checker gave
// v.[f], f.type, and the part it stands for is recorded for the
// backends. Every place a backend writes a field then writes this one.
func (c *Checker) selectParts(body *ast.BlockStmt) {
	ast.Rewrite(body, func(x ast.Expr) ast.Expr {
		pe, ok := x.(*ast.PartExpr)
		if !ok {
			return x
		}

		p, _ := c.info.Uses[pe.Part].(*Part)
		if p == nil {
			return x
		}

		sel := &ast.SelectorExpr{X: pe.X, Sel: &ast.Ident{NamePos: pe.Part.Pos(), Name: pe.Part.Name}}
		c.info.Types[sel] = c.info.Types[pe]
		if c.info.Parts == nil {
			c.info.Parts = make(map[*Part][]*ast.SelectorExpr)
		}

		c.info.Parts[p] = append(c.info.Parts[p], sel)
		return sel
	})
}

// partsLit checks T{ for f := range fields(T) { [f]: value } }, the
// value of a type an arm matched as a struct built from its fields: the
// loop is the literal's one element, its value is checked once, against
// the field's type, and is taken by the literal, as a field's value is.
// It is a loop for the move and borrow passes, since the backends write
// the value once per field.
func (c *Checker) partsLit(e *ast.CompositeLit, tp *TypeParam) operand {
	elt, ok := e.Elts[0].(*ast.PartsElt)
	if len(e.Elts) != 1 || !ok || elt.Key == nil {
		c.errorf(e.Lbrace, "a value of "+tp.obj.name+" is built from its fields, "+tp.obj.name+"{ for f := range fields("+tp.obj.name+") { [f]: value } }, one loop and nothing beside it")
		c.looseElements(e.Elts)
		return invalidOperand
	}

	c.openScope()
	defer c.closeScope()
	var part *Part
	if call, b := c.partsCall(elt.X); call == nil || b.id != BuiltinFields {
		c.errorf(elt.X.Pos(), "a value is built from fields("+tp.obj.name+")")
	} else if of, in := c.partsOf(call, b); of != nil && in == nil {
		if of != Type(tp) {
			c.errorf(elt.X.Pos(), "a value of "+tp.obj.name+" is built from fields("+tp.obj.name+"), not of "+of.String())
		}

		part = &Part{object: object{elt.Var.Name, NewTypeParam(NewTypeName(elt.Var.Pos(), elt.Var.Name+".type", nil), 0), elt.Var.Pos(), c.pkg}, of: tp}
		c.scope.Insert(part)
		c.recordDef(elt.Var, part)
	}

	if part != nil && elt.Key.Name != part.name {
		c.errorf(elt.Key.Pos(), "each field is given by the loop's part, ["+part.name+"]: "+elt.Key.Name)
	} else if part != nil {
		c.recordUse(elt.Key, part)
	}

	c.enterRegion(true, false, elt.For)
	r := &c.regions[len(c.regions)-1]
	r.parts = true
	r.floor, r.end = c.scope.parent, nodeEnd(elt.Value)
	c.flowLoopBody()
	if part != nil {
		v := c.expr(elt.Value, part.typ)
		c.assign(v, part.typ, elt.Value.Pos(), "field "+part.name)
		c.maybeMove(v, part.typ)
		c.kept(v, "stored", elt.Value.Pos())
	} else {
		c.expr(elt.Value, nil)
	}

	c.leaveRegion()
	return operand{value, tp, nil, nil}
}

// heldVariant reports whether the name id is the variable a match
// written once per variant around it is on.
func (c *Checker) heldVariant(id *ast.Ident) bool {
	if len(c.held) == 0 {
		return false
	}

	v, _ := c.resolve(id.Name).(*Var)
	for _, h := range c.held {
		if h == v {
			return true
		}
	}

	return false
}

// holdsVariant reports whether x is the value a match whose arm is
// written once per variant matched, in the arm for the variant.
func (c *Checker) holdsVariant(x ast.Expr, variant *Part) bool {
	id, ok := unparen(x).(*ast.Ident)
	if !ok {
		return false
	}

	v, _ := c.resolve(id.Name).(*Var)
	return v != nil && c.held[variant] == v
}

// variantMatch checks match v { for c := range variants(T) => body }:
// one arm, written out once for each variant of the type an arm matched
// T as an enum, so that the match covers them all; in each, v holds the
// variant c, whose fields v.[f] reaches through fields(c). v is a
// variable, read or lent, never taken apart: the arm binds nothing.
func (c *Checker) variantMatch(e *ast.MatchExpr, expected Type) operand {
	pat := e.Arms[0].Pat.(*ast.PartsPat)
	if len(e.Arms) != 1 {
		c.errorf(e.Arms[1].Pat.Pos(), "a match written once for each variant has that arm alone, which covers every variant")
	}

	x := c.expr(e.X, nil)
	held, _ := c.resolve(identName(e.X)).(*Var)
	if _, isIdent := unparen(e.X).(*ast.Ident); !isIdent || held == nil {
		c.errorf(e.X.Pos(), "a match written once for each variant is on a variable, whose fields the arm reaches with v.[f]")
	}

	c.openScope()
	defer c.closeScope()
	var part *Part
	if call, b := c.partsCall(pat.X); call == nil || b.id != BuiltinVariants {
		c.errorf(pat.X.Pos(), "the arm is written once for each variant, for c := range variants(T)")
	} else if of, _ := c.partsOf(call, b); of != nil {
		if base, _ := deref(x); x.mode != invalid && !Identical(base, of) {
			c.errorf(e.X.Pos(), "the match is on a value of "+of.String()+", whose variants the arm goes through, not of "+base.String())
		}

		part = &Part{object: object{pat.Var.Name, nil, pat.Var.Pos(), c.pkg}, of: of, variant: true}
		c.scope.Insert(part)
		c.recordDef(pat.Var, part)
	}

	if part != nil && held != nil {
		if c.held == nil {
			c.held = make(map[*Part]*Var)
		}

		c.held[part] = held
		defer delete(c.held, part)
	}

	holder := c.holdMatched(e.X, pat.For)
	c.enterRegion(true, false, pat.For)
	r := &c.regions[len(c.regions)-1]
	r.parts = true
	r.floor, r.end = c.scope.parent, nodeEnd(e.Arms[0].Body)
	c.flowLoopBody()
	v := c.node(e.Arms[0].Body, expected)
	if holder != nil {
		c.flowUse(holder, nodeEnd(e.Arms[0].Body)) // held to the end of the arm
	}

	c.leaveRegion()
	return v
}

// holdMatched lends the variable a match written once per variant is on,
// and what it borrows, to a holder for the arm: v.[f] reads the fields
// of the variant the arm is written for, so nothing may give v another
// value, lend it mut or move it while the arm runs.
func (c *Checker) holdMatched(x ast.Expr, pos token.Pos) *Var {
	p, kind := c.placeOf(x)
	if kind == notPlace {
		return nil
	}

	holder := NewVar(pos, "the arm written for each variant", Typ[Invalid])
	c.flowAssign(holder, "")
	c.flowLoan(borrow{holder: holder, place: p, mut: false, pos: pos})
	for _, src := range c.borrowSources(x) {
		if q, kind := through(c.placeOf(src)); kind != notPlace {
			c.flowLoan(borrow{holder: holder, place: q, mut: false, pos: pos})
		}
	}

	return holder
}

// variantBuild checks c(for f := range fields(c) { value }), the
// variant c of an enum type built from its fields, each value checked
// once against its field's type and taken by the variant.
func (c *Checker) variantBuild(e *ast.CallExpr, p *Part) operand {
	if !p.variant {
		c.errorf(e.Fun.Pos(), p.name+" is a field: a variant a loop gives is built, c(for f := range fields(c) { value })")
		c.exprs(e.Args)
		return invalidOperand
	}

	elt, ok := firstPartsElt(e.Args)
	if !ok || elt.Key != nil {
		c.errorf(e.Lparen, "the variant "+p.name+" is built from its fields, "+p.name+"(for f := range fields("+p.name+") { value }), one loop and nothing beside it")
		c.exprs(e.Args)
		return invalidOperand
	}

	c.openScope()
	defer c.closeScope()
	var field *Part
	if call, b := c.partsCall(elt.X); call == nil || b.id != BuiltinFields {
		c.errorf(elt.X.Pos(), "a variant is built from fields("+p.name+")")
	} else if _, in := c.partsOf(call, b); in != p {
		c.errorf(elt.X.Pos(), "the variant "+p.name+" is built from its own fields, fields("+p.name+")")
	} else {
		field = &Part{object: object{elt.Var.Name, NewTypeParam(NewTypeName(elt.Var.Pos(), elt.Var.Name+".type", nil), 0), elt.Var.Pos(), c.pkg}, of: p.of, in: p}
		c.scope.Insert(field)
		c.recordDef(elt.Var, field)
	}

	c.enterRegion(true, false, elt.For)
	r := &c.regions[len(c.regions)-1]
	r.parts = true
	r.floor, r.end = c.scope.parent, nodeEnd(elt.Value)
	c.flowLoopBody()
	if field != nil {
		v := c.expr(elt.Value, field.typ)
		c.assign(v, field.typ, elt.Value.Pos(), "field "+field.name)
		c.maybeMove(v, field.typ)
		c.kept(v, "stored", elt.Value.Pos())
	} else {
		c.expr(elt.Value, nil)
	}

	c.leaveRegion()
	return operand{value, p.of, nil, nil}
}

// firstPartsElt returns the one argument of a call when it is a loop
// over a type's parts.
func firstPartsElt(args []ast.Expr) (*ast.PartsElt, bool) {
	if len(args) != 1 {
		return nil, false
	}

	elt, ok := args[0].(*ast.PartsElt)
	return elt, ok
}

// identName returns the name x is, or "".
func identName(x ast.Expr) string {
	if id, ok := unparen(x).(*ast.Ident); ok {
		return id.Name
	}

	return ""
}
