// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"iter"
	"slices"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// Traits take the place go/types gives interfaces, with one difference
// at the root: a type implements a trait because its declaration says
// so, struct Circle impl Shape, not because its methods happen to
// match. A trait's methods are declared with Self as the receiver's
// type, and Self is a type parameter of the trait, so that asking
// whether Circle implements Shape is the same substitution an instance
// of a generic type goes through: put Circle in for Self and compare
// each required method with the one Circle declares. A type parameter
// bounded by a trait, T: Shape, sees the trait's methods with itself in
// for Self, which is how x.Area() checks on an x of type T; the backend
// writes the call against the type argument's own method, one copy per
// instance, as it writes everything else in a generic body.

// A Trait is a declared trait, or one of the standard traits the
// language predeclares.
type Trait struct {
	obj      *TypeName
	self     *TypeParam // Self: the implementing type, in the method signatures and the fields
	refines  []*Trait   // the traits after the colon; a nil entry stands for one that failed to resolve
	order    []*Trait   // the traits refined, directly or not, the most specific first (linearize)
	fields   []*Var     // the fields every implementor stores, written with var
	required []*Func    // the methods an implementor declares, receivers of type Self
	defaults []*Func    // the methods with a body, which an implementor gets
	standard bool       // predeclared: Copy, Drop and the others of the universe

	// An operator trait with the type of the other operand written,
	// Mul<f64>: orig is Mul, rhs is f64, and the trait's one method takes
	// an rhs. The checker keeps the instances it makes (stdtraits.go).
	orig *Trait
	rhs  Type

	// A generic trait, Iterator<T>: tparams are its parameters, in scope
	// for its members. An instance, Iterator<int>, has orig and targs,
	// and its members are the declaration's with the arguments in place,
	// filled once the declaration's methods are resolved (traitInstance,
	// fillTraitInstances). Instances are kept per check, as the
	// operators' are, so identity is a pointer comparison.
	tparams   []*TypeParam
	targs     []Type
	resolved  bool     // the methods are attached: an instance made now is filled at once
	instances []*Trait // a generic declaration's instances, one per argument list (instantiateTrait)

	// A trait used as a type: the methods and fields a value of it has,
	// with the trait itself for Self, made on first use.
	valueMethods []*Func
	valueFields  []*Var
}

// A pendingBound is a check of type arguments against their bounds
// asked while the declarations were being resolved, when whether a
// type is Copy cannot be answered yet.
type pendingBound struct {
	tparams []*TypeParam
	targs   []Type
	pos     token.Pos
	name    string
}

// A traitDefault is a default method whose body is checked with the
// function bodies, once, with Self standing for any implementing type.
type traitDefault struct {
	trait *Trait
	tdecl *ast.TraitDecl
	decl  *ast.FuncDecl
	fn    *Func
}

// NewTrait returns a trait named by obj with no methods yet, and sets
// obj's type to it. Its Self is bounded by the trait itself.
func NewTrait(obj *TypeName) *Trait {
	t := &Trait{obj: obj}
	t.self = NewTypeParam(NewTypeName(token.NoPos, "Self", nil), 0)
	t.self.setBounds([]*Trait{t})
	obj.typ = t
	return t
}

// Obj returns the trait's name.
func (t *Trait) Obj() *TypeName { return t.obj }

// Method returns the trait's method with the given name, required or
// default, its own or one of a trait it refines, or nil.
func (t *Trait) Method(name string) *Func {
	fn, _ := t.lookup(name)
	return fn
}

// Refines reports whether t is other or refines it, directly or
// through the traits in between: a type implementing t implements
// other as well. The walk needs no record of the traits seen, since a
// cycle among the declarations is cut when they resolve, before
// anything asks (checkRefinementCycles).
func (t *Trait) Refines(other *Trait) bool {
	if t == other || sameInstance(t, other) {
		return true
	}

	for _, r := range t.refines {
		if r != nil && r.Refines(other) {
			return true
		}
	}

	return false
}

func (t *Trait) Underlying() Type { return t }

func (t *Trait) String() string {
	if t.rhs != nil {
		return t.obj.name + "<" + t.rhs.String() + ">"
	}

	if t.targs != nil {
		return t.obj.name + "<" + typeList(t.targs) + ">"
	}

	return t.obj.name
}

// TypeParams returns a generic trait's parameters, TypeArgs an
// instance's arguments; nil otherwise.
func (t *Trait) TypeParams() []*TypeParam { return t.tparams }
func (t *Trait) TypeArgs() []Type         { return t.targs }

// lookup returns the method with the given name and the trait that
// declares it, whose Self the signature is written over.
func (t *Trait) lookup(name string) (*Func, *Trait) {
	for _, m := range t.required {
		if m.name == name {
			return m, t
		}
	}

	for _, m := range t.defaults {
		if m.name == name {
			return m, t
		}
	}

	for _, r := range t.refines {
		if r == nil {
			continue
		}

		if fn, owner := r.lookup(name); fn != nil {
			return fn, owner
		}
	}

	return nil, nil
}

// field returns the field with the given name that t or a trait it
// refines declares, and the trait that declares it, whose Self the
// field's type is written over; or nil.
func (t *Trait) field(name string) (*Var, *Trait) {
	if f := fieldNamed(t.fields, name); f != nil {
		return f, t
	}

	for _, r := range t.refines {
		if r == nil {
			continue
		}

		if f, owner := r.field(name); f != nil {
			return f, owner
		}
	}

	return nil, nil
}

// reaches reports whether other is among the traits t refines, with
// the traits seen so far, so that a cycle among the declarations ends
// the walk instead of continuing it.
func (t *Trait) reaches(other *Trait, seen map[*Trait]bool) bool {
	seen[t] = true
	for _, r := range t.refines {
		if r == nil {
			continue
		}

		if r == other || (!seen[r] && r.reaches(other, seen)) {
			return true
		}
	}

	return false
}

// eachRequired calls f for every method an implementor of t must
// declare, with the trait that declares it: t's own, then those of the
// traits it refines, leaving out the traits in seen and adding the
// ones it visits, so that a trait reached twice is walked once.
func (t *Trait) eachRequired(f func(owner *Trait, m *Func), seen map[*Trait]bool) {
	if seen[t] {
		return
	}

	seen[t] = true
	for _, m := range t.required {
		f(t, m)
	}

	for _, r := range t.refines {
		if r != nil {
			r.eachRequired(f, seen)
		}
	}
}

// standardTrait returns a predeclared trait with the given name; its
// methods, if any, are attached when the universe is built.
func standardTrait(name string) *Trait {
	t := NewTrait(NewTypeName(token.NoPos, name, nil))
	t.standard = true
	return t
}

// collectTrait declares a trait's name with an empty trait behind it,
// so that impl lists, bounds and refinements can name it in any order.
func (c *Checker) collectTrait(d *ast.TraitDecl) *TypeName {
	obj := NewTypeName(d.Name.Pos(), d.Name.Name, nil)
	tr := NewTrait(obj)
	c.declare(c.pkg.scope, d.Name, obj)
	tr.tparams = c.typeParams(d.TypeParams)
	return obj
}

// resolveTraitLists resolves the names every declaration lists before
// its body: the traits a type implements, the traits a trait refines,
// the bounds of a type's parameters, and the fields of each trait;
// orderTraitLists then gives each type its resolution order. They are
// needed before any type is resolved, since a field of type
// Copied<string> asks whether string satisfies Copied's bound, a
// type's Copy-ness asks whether it implements Drop, and a struct's
// fields include its traits'.
func (c *Checker) resolveTraitLists(f *ast.File) {
	for i, d := range f.Decls {
		switch d := d.(type) {
		case *ast.TypeDecl:
			n := c.objs[i].(*TypeName).typ.(*Named)
			if len(d.TypeParams) == 0 {
				n.impls = c.implList(d)
				continue
			}

			c.resolveBounds(d.TypeParams, n.tparams) // the parameters in scope for the bounds and the impl list, as in impl Mul<T>
			n.impls = c.implList(d)
			c.closeScope()
		case *ast.TraitDecl:
			tr := c.objs[i].(*TypeName).typ.(*Trait)
			if tr.tparams != nil {
				c.resolveBounds(d.TypeParams, tr.tparams) // in scope for the refinements and the fields
			}

			tr.refines = c.traitList(d.Refines, "after the colon")
			c.self = tr.self
			tr.fields = c.fields(d.Fields, false)
			c.self = nil
			if tr.tparams != nil {
				c.closeScope()
			}
		}
	}

}

// orderTraitLists draws the resolution order of each type and trait f
// declares from the traits it lists, once every file's lists are
// resolved and every instance has its refinements, so that a trait
// another file declares, refined or refining, is in the order whatever
// the order of the files.
func (c *Checker) orderTraitLists(f *ast.File) {
	for i, d := range f.Decls {
		switch d := d.(type) {
		case *ast.TypeDecl:
			if len(d.Impls) > 0 {
				n := c.objs[i].(*TypeName).typ.(*Named)
				n.order = linearize(n.impls)
			}
		case *ast.TraitDecl:
			if len(d.Refines) > 0 {
				tr := c.objs[i].(*TypeName).typ.(*Trait)
				tr.order = linearize(tr.refines)
			}
		}
	}
}

// traitFields returns the fields the type n gets from its traits, in
// its resolution order with each trait's fields once, their types
// written with n for Self; own are the fields n declares itself. A
// field named twice, by the type and a trait or by two traits, is
// refused: each name must mean one place. A field holding Self in
// owned data, []Self, is asked again whether it may hold n, since the
// question was asked of Self when the trait resolved and n may hold a
// borrow.
func (c *Checker) traitFields(n *Named, own []*Var, pos token.Pos) []*Var {
	var fields []*Var
	var from map[string]*Trait // the trait each field came from
	for _, t := range n.order {
		m := map[*TypeParam]Type{t.self: n}
		for _, f := range t.fields {
			if dup := fieldNamed(own, f.name); dup != nil {
				c.errorf(dup.pos, "field "+f.name+" is also a field of "+t.obj.name)
				continue
			}

			if first := from[f.name]; first != nil {
				c.errorf(pos, n.obj.name+" gets a field "+f.name+" from both "+first.obj.name+" and "+t.obj.name)
				continue
			}

			if from == nil {
				from = make(map[string]*Trait)
			}

			from[f.name] = t
			v := substVar(f, m)
			if mentions(f.typ, t.self) {
				c.storableParts(v.typ, pos)
			}

			fields = append(fields, v)
		}
	}

	return fields
}

// refuseTraitFields reports a type that is not a struct and so has
// nowhere to store the fields of a trait it lists.
func (c *Checker) refuseTraitFields(n *Named, pos token.Pos) {
	for _, t := range n.order {
		if len(t.fields) > 0 {
			c.errorf(pos, n.obj.name+" is not a struct and cannot carry the field "+t.fields[0].name+" of "+t.obj.name)
			return
		}
	}
}

// checkRefinementCycles refuses a trait that refines itself, directly
// or through others, and cuts its refinements so that every later walk
// over the traits ends.
func (c *Checker) checkRefinementCycles(f *ast.File) {
	for i, d := range f.Decls {
		if d, ok := d.(*ast.TraitDecl); ok {
			tr := c.objs[i].(*TypeName).typ.(*Trait)
			if tr.reaches(tr, make(map[*Trait]bool)) {
				c.errorf(d.Name.Pos(), "trait "+tr.obj.name+" refines itself")
				tr.refines = nil
			}
		}
	}
}

// implList resolves the traits a type declaration lists after impl.
// Send among them is refused, since a type is Send by its structure
// and never by listing it (send.go); the entry is left nil, as a trait
// that failed to resolve is.
func (c *Checker) implList(d *ast.TypeDecl) []*Trait {
	impls := c.traitList(d.Impls, "after impl")
	for i, tr := range impls {
		if tr == SendTrait {
			c.errorf(d.Impls[i].Pos(), "Send is not listed: a type is Send when every field is")
			impls[i] = nil
		}
	}

	return impls
}

// traitList resolves a list of trait names, an impl list, a refinement
// list or the bounds of one parameter, to the traits; where says which
// for the message. An entry that is not a trait, or repeats one, is
// reported and left nil, so that the positions of the list still line
// up with the names.
func (c *Checker) traitList(ts []ast.Type, where string) []*Trait {
	if len(ts) == 0 {
		return nil
	}

	traits := make([]*Trait, len(ts))
	for i, t := range ts {
		n, ok := t.(*ast.NamedType)
		if !ok {
			c.errorf(t.Pos(), "a trait name is expected "+where)
			continue
		}

		var obj Object
		if n.Pkg != nil {
			obj = c.qualified(n.Pkg, n.Name)
		} else {
			obj = c.lookupTrait(n.Name)
		}

		if obj == nil {
			continue
		}

		tr := traitOf(obj)
		if tr == nil {
			c.errorf(t.Pos(), n.Name.Name+" is not a trait")
			continue
		}

		if len(n.Args) > 0 {
			if tr = c.traitArgs(tr, n); tr == nil {
				continue
			}
		} else if tr.tparams != nil {
			c.errorf(t.Pos(), tr.obj.name+" takes "+plural(len(tr.tparams), "type argument")+": "+tr.obj.name+"<...>")
			continue
		}

		if listsOrigin(traits[:i], tr) {
			switch {
			case tr.targs == nil: // an operator trait: a type has one Mul, whatever its argument
				c.errorf(t.Pos(), n.Name.Name+" listed twice")
			case slices.Contains(traits[:i], tr):
				c.errorf(t.Pos(), tr.String()+" listed twice")
			default:
				c.errorf(t.Pos(), tr.String()+" listed with another instance of "+n.Name.Name+": a type implements a generic trait at one argument")
			}

			continue
		}

		traits[i] = tr
	}

	return traits
}

// lookupTrait resolves a name where a trait is expected: the nearest
// trait of that name, past any type that shadows it, so that a package
// may declare a type named Error and list the standard trait Error on
// it, as io does. A name with no trait behind it resolves as any name
// does, and the caller reports what it found.
func (c *Checker) lookupTrait(ident *ast.Ident) Object {
	for s := c.scope; s != nil; s = s.parent {
		if obj := s.Lookup(ident.Name); obj != nil && traitOf(obj) != nil {
			c.recordUse(ident, obj)
			return obj
		}
	}

	return c.lookup(ident)
}

// traitArgs applies the type arguments written after a trait's name:
// only a binary operator trait takes one, the type of the other
// operand, Mul<f64>; any other trait with arguments is not supported
// yet. It returns the trait for that argument, or nil after reporting.
func (c *Checker) traitArgs(tr *Trait, n *ast.NamedType) *Trait {
	if tr.tparams != nil {
		if len(n.Args) != len(tr.tparams) {
			c.errorf(n.Pos(), tr.obj.name+" takes "+plural(len(tr.tparams), "type argument"))
			return nil
		}

		targs := make([]Type, len(n.Args))
		for i, a := range n.Args {
			if targs[i] = c.typ(a); targs[i] == Typ[Invalid] {
				return nil
			}
		}

		return c.traitInstance(tr, targs)
	}

	if !binaryOperator(tr) {
		c.errorf(n.Pos(), tr.obj.name+" takes no type arguments")
		return nil
	}

	if len(n.Args) != 1 {
		c.errorf(n.Pos(), tr.obj.name+" takes one type argument, the type of the other operand")
		return nil
	}

	rhs := c.typ(n.Args[0])
	if rhs == Typ[Invalid] {
		return nil // reported where it was written; a nil entry stands for it
	}

	return c.operatorInstance(tr, rhs)
}

// traitInstance returns the generic trait tr with the given arguments
// (instantiateTrait), its refinements substituted through the check's
// own operator instances.
func (c *Checker) traitInstance(tr *Trait, targs []Type) *Trait {
	return instantiateTrait(tr, targs, c.substTrait)
}

// A traitSubst puts a substitution's arguments in place in a trait: the
// checker's keeps the operator instances it makes, and substInstance,
// with no check at hand, makes its own.
type traitSubst func(t *Trait, m map[*TypeParam]Type) *Trait

// instantiateTrait returns the generic trait orig with the arguments
// targs, one instance per argument list, kept on the declaration as a
// generic type's instances are, so that two are the same instance when
// they are the same pointer, wherever each was made: by the checker, or
// by a substitution in generic code. Its members are the declaration's
// with the arguments in place, filled now when the declaration's
// methods are resolved, and when they are otherwise (resolveTrait),
// since an impl list or a bound may name an instance before the trait's
// methods are known. Self is shared with the declaration, as the
// operator instances share theirs.
func instantiateTrait(orig *Trait, targs []Type, sub traitSubst) *Trait {
	for _, inst := range orig.instances {
		if identicalTypes(inst.targs, targs) {
			return inst
		}
	}

	inst := &Trait{obj: orig.obj, self: orig.self, standard: orig.standard, orig: orig, targs: targs}
	orig.instances = append(orig.instances, inst)
	if orig.resolved {
		inst.fill(sub)
	} else {
		inst.fillRefines(sub) // nothing yet while the declaration's refinements are being resolved: fillTraitRefines gives them
	}

	return inst
}

// twoInstances finds two instances of one generic trait, with different
// arguments, among the traits listed and those they refine: what a
// type or a type parameter would implement at two arguments, which it
// may not, since a type implements a generic trait at one argument.
// Each comes with the trait listed it was reached through, itself when
// listed.
func twoInstances(listed []*Trait) (a, viaA, b, viaB *Trait) {
	var seen map[*Trait][2]*Trait // a declaration's first instance, and what it was reached through, made at the first instance: most lists have none
	for _, l := range listed {
		if l == nil {
			continue
		}

		for t := range l.lineage() {
			if t == nil || t.orig == nil || t.targs == nil {
				continue
			}

			first, ok := seen[t.orig]
			if !ok {
				if seen == nil {
					seen = make(map[*Trait][2]*Trait)
				}

				seen[t.orig] = [2]*Trait{t, l}
				continue
			}

			if first[0] != t {
				return first[0], first[1], t, l
			}
		}
	}

	return nil, nil, nil, nil
}

// twoInstancesMessage says that who implements a and b, two instances
// of one generic trait, naming the trait listed each came through; a
// trait refines them, which verb says.
func twoInstancesMessage(who, verb string, a, viaA, b, viaB *Trait) string {
	reached := func(t, via *Trait) string {
		if t == via {
			return t.String()
		}

		return "through " + via.String() + ", " + t.String()
	}

	return who + " " + verb + " " + reached(a, viaA) + " and " + reached(b, viaB) + ": a type implements a generic trait at one argument"
}

// fill gives an instance the members of its declaration with the
// arguments in place, and its refinements, when fillTraitRefines has
// not given them already.
func (inst *Trait) fill(sub traitSubst) {
	tr := inst.orig
	m := inst.substitution()
	for _, fn := range tr.required {
		inst.required = append(inst.required, substFunc(fn, m))
	}

	for _, fn := range tr.defaults {
		inst.defaults = append(inst.defaults, substFunc(fn, m))
	}

	for _, f := range tr.fields {
		inst.fields = append(inst.fields, substVar(f, m))
	}

	inst.fillRefines(sub)
	inst.resolved = true
}

// fillRefines gives an instance the refinements of its declaration with
// the arguments in place, once.
func (inst *Trait) fillRefines(sub traitSubst) {
	if inst.order != nil || len(inst.orig.refines) == 0 {
		return
	}

	m := inst.substitution()
	for _, r := range inst.orig.refines {
		inst.refines = append(inst.refines, sub(r, m))
	}

	inst.order = linearize(inst.refines)
}

// fillTraitRefines gives the instances made so far of the generic
// traits f declares their refinements, once the declarations' are
// resolved and before a type's resolution order is drawn from the
// instances it lists.
func (c *Checker) fillTraitRefines(f *ast.File) {
	for i, d := range f.Decls {
		if _, ok := d.(*ast.TraitDecl); !ok {
			continue
		}

		if tr, ok := c.objs[i].(*TypeName).typ.(*Trait); ok {
			for _, inst := range tr.instances {
				inst.fillRefines(c.substTrait)
			}
		}
	}
}

// substitution maps an instance's declaration parameters to its
// arguments.
func (inst *Trait) substitution() map[*TypeParam]Type {
	m := make(map[*TypeParam]Type, len(inst.orig.tparams))
	for i, p := range inst.orig.tparams {
		m[p] = inst.targs[i]
	}

	return m
}

// attached marks a declaration's methods resolved, and fills the
// instances made before they were.
func (c *Checker) attached(tr *Trait) {
	tr.resolved = true
	for _, inst := range tr.instances {
		if !inst.resolved {
			inst.fill(c.substTrait)
		}
	}
}

// substTrait returns the trait t with the substitution m applied to its
// arguments: another instance of the same declaration, or t itself when
// it names no parameter of m.
func (c *Checker) substTrait(t *Trait, m map[*TypeParam]Type) *Trait {
	switch {
	case t == nil:
		return nil
	case t.rhs != nil:
		if rhs := subst(t.rhs, m); rhs != t.rhs {
			return c.operatorInstance(t.origin(), rhs)
		}
	case t.targs != nil:
		if targs := substList(t.targs, m); !identicalTypes(targs, t.targs) {
			return c.traitInstance(t.orig, targs)
		}
	}

	return t
}

// substInstance returns the trait t with the substitution m applied to
// its arguments, as the checker's substTrait does, with no check at
// hand: in a generic function's instance, or in a predicate. An
// operator instance made here is the checker's list's equal, not its
// pointer, which Refines and listsOrigin compare by what they hold.
func substInstance(t *Trait, m map[*TypeParam]Type) *Trait {
	switch {
	case t == nil:
		return nil
	case t.rhs != nil:
		if rhs := subst(t.rhs, m); rhs != t.rhs {
			return newOperatorInstance(t.origin(), rhs)
		}
	case t.targs != nil:
		for i, a := range t.targs {
			if subst(a, m) != a { // the list is made only when an argument changes, as most instances in a generic body do not
				targs := substList(t.targs, m)
				if !identicalTypes(targs[i:], t.targs[i:]) { // those before i are unchanged, as the loop found
					return instantiateTrait(t.orig, targs, substInstance)
				}

				break
			}
		}
	}

	return t
}

// listsOrigin reports whether the traits list tr or another instance
// of the same operator trait: a type has one Mul method, so one Mul.
func listsOrigin(traits []*Trait, tr *Trait) bool {
	for _, t := range traits {
		if t != nil && t.origin() == tr.origin() {
			return true
		}
	}

	return false
}

// valueMethod returns the method with the given name a value of the
// trait type t has, with t for Self, made once and kept; nil when the
// trait has no such method. why says why a method the trait has cannot
// be called through a trait value (valueGap): it is not kept, so the
// question is asked again at each such call.
func (t *Trait) valueMethod(name string) (fn *Func, why string) {
	for _, m := range t.valueMethods {
		if m.name == name {
			return m, ""
		}
	}

	def, owner := t.lookup(name)
	if def == nil {
		return nil, ""
	}

	if why := valueGap(def, owner); why != "" {
		return nil, why
	}

	fn = substFunc(def, map[*TypeParam]Type{owner.self: t})
	t.valueMethods = append(t.valueMethods, fn)
	return fn, ""
}

// valueGap returns why the method m of the trait owner cannot be called
// through a trait value, or "" when it can. A trait value calls through
// a table with one function for each method, made for the value's own
// type, which the caller does not know: a method whose parameters or
// results are written with Self needs that type, and one with type
// parameters of its own is a function for each type argument, which no
// one entry holds.
func valueGap(m *Func, owner *Trait) string {
	sig := m.typ.(*Signature)
	switch {
	case m.static:
		return "it is a function of the type, which takes no value: call it on a type, T." + m.name + "(...)"
	case mentions(sig, owner.self): // the signature's parameters and results, not the receiver
		return "its signature mentions Self"
	case len(sig.tparams) > 0:
		return "it has type parameters, compiled for each type, which a trait value's table cannot hold; take the value as a bound"
	}

	return ""
}

// valueField returns the field with the given name a value of the trait
// type t has, with t for Self, made once and kept; nil when the trait
// has no such field. mentionsSelf reports a field whose type is
// written with Self, which cannot be reached through a trait value.
func (t *Trait) valueField(name string) (v *Var, mentionsSelf bool) {
	if f := fieldNamed(t.valueFields, name); f != nil {
		return f, false
	}

	f, owner := t.field(name)
	if f == nil {
		return nil, false
	}

	if mentions(f.typ, owner.self) {
		return nil, true
	}

	v = substVar(f, map[*TypeParam]Type{owner.self: t})
	t.valueFields = append(t.valueFields, v)
	return v, false
}

// ValueMethods returns the methods a value of the trait type has, the
// trait's own first and then those of the traits it refines in their
// order, each name once and without the methods written with Self,
// which a trait value cannot call; for the backend's table of them.
func (t *Trait) ValueMethods() []*Func {
	var fns []*Func
	for tr := range t.lineage() {
		for _, m := range tr.required {
			fns = t.addValueMethod(fns, m.name)
		}

		for _, m := range tr.defaults {
			fns = t.addValueMethod(fns, m.name)
		}
	}

	return fns
}

// addValueMethod appends the method name of a value of t to fns, unless
// it is already there or a trait value cannot call it.
func (t *Trait) addValueMethod(fns []*Func, name string) []*Func {
	if fn, _ := t.valueMethod(name); fn != nil && !slices.Contains(fns, fn) {
		fns = append(fns, fn)
	}

	return fns
}

// ValueFields returns the fields a value of the trait type has, in the
// same order as ValueMethods and without the fields written with Self.
func (t *Trait) ValueFields() []*Var {
	var fields []*Var
	for tr := range t.lineage() {
		for _, f := range tr.fields {
			if v, _ := t.valueField(f.name); v != nil && fieldNamed(fields, v.name) == nil {
				fields = append(fields, v)
			}
		}
	}

	return fields
}

// lineage yields t followed by the traits it refines, in their order,
// with no list made for it.
func (t *Trait) lineage() iter.Seq[*Trait] {
	return func(yield func(*Trait) bool) {
		if !yield(t) {
			return
		}

		for _, r := range t.order {
			if !yield(r) {
				return
			}
		}
	}
}

// traitOf returns the trait obj names, or nil for any other object.
func traitOf(obj Object) *Trait {
	if tn, ok := obj.(*TypeName); ok {
		tr, _ := tn.typ.(*Trait)
		return tr
	}

	return nil
}

// resolveBounds gives each type parameter the traits after its colon,
// with the parameters themselves in scope, so a bound may name one, as
// T: Mul<T> does. The scope is left open and returned for the caller to
// close, since the declaration's signature or impl list reads it too.
func (c *Checker) resolveBounds(ps []*ast.TypeParam, tparams []*TypeParam) *Scope {
	scope := c.openTypeParams(paramNames(ps), tparams)
	for i, p := range ps {
		bounds := c.traitList(p.Bounds, "as a bound")
		if a, viaA, b, viaB := twoInstances(bounds); a != nil {
			c.errorf(p.Name.Pos(), twoInstancesMessage(p.Name.Name, "implements", a, viaA, b, viaB))
		}

		tparams[i].setBounds(bounds)
	}

	return scope
}

// paramNames lists the names of declared type parameters.
func paramNames(ps []*ast.TypeParam) []*ast.Ident {
	names := make([]*ast.Ident, len(ps))
	for i, p := range ps {
		names[i] = p.Name
	}

	return names
}

// resolveTrait gives a trait its methods: each declared method's
// signature is resolved with Self standing for the implementing type.
// A method with a body is a default, which the implementors get and
// whose body is checked with the function bodies; one without is
// required of them. Generic traits and methods are decided but not
// built yet, and say so; the members of a generic trait are left
// alone, since they name its parameters.
func (c *Checker) resolveTrait(d *ast.TraitDecl, tr *Trait) {
	if tr.tparams != nil {
		c.openTypeParams(paramNames(d.TypeParams), tr.tparams)
		defer c.closeScope()
	}

	defer c.attached(tr)
	c.self = tr.self
	defer func() { c.self = nil }()
	seen := NewScope(nil)
	for _, m := range d.Methods {
		fn := c.traitMethod(m)
		if fn == nil {
			continue
		}

		if old := seen.Insert(fn); old != nil {
			c.errorf(m.Name.Pos(), "method "+tr.obj.name+"."+m.Name.Name+" redeclared; first declared at "+c.lineCol(old.Pos()))
			continue
		}

		if m.Body != nil {
			tr.defaults = append(tr.defaults, fn)
			c.defaults = append(c.defaults, traitDefault{tr, d, m, fn})
			continue
		}

		tr.required = append(tr.required, fn)
	}
}

// checkDefaultBodies checks the body of every default method, once,
// as a function whose receiver is of type Self: inside it a value of
// Self has the trait's methods and nothing else, and moves unless the
// trait refines Copy.
func (c *Checker) checkDefaultBodies() {
	for _, d := range c.defaults {
		sig := d.fn.typ.(*Signature)
		if d.trait.tparams != nil {
			c.openTypeParams(paramNames(d.tdecl.TypeParams), d.trait.tparams)
		}

		if sig.tparams != nil {
			c.openTypeParams(paramNames(d.decl.TypeParams), sig.tparams)
		}

		c.self = d.trait.self
		c.checkBody(sig, d.decl.Recv, d.decl.Params, d.decl.Body)
		c.self = nil
		d.fn.decl = d.decl // a default is generic over Self, the type that inherits it
		if sig.tparams != nil {
			c.closeScope()
		}

		if d.trait.tparams != nil {
			c.closeScope()
		}
	}
}

// superCall checks super.M, the next definition of M in the resolution
// order after the method being checked: inside a trait's default, the
// first default for M among the traits it refines, in their order;
// inside a type's own method, the first default for M among the type's
// traits. The receiver is the method's own, captured when the call is
// inside a closure, and super stands for it to the move and borrow
// passes, so super.M(args) is checked as recv.M(args) with M found
// differently. Which definition is next depends on the implementing
// type, so the backend resolves it again per type (Named.SuperMethod);
// the one found here gives the call its type.
func (c *Checker) superCall(e *ast.SelectorExpr) operand {
	if c.recv == nil {
		c.errorf(e.X.Pos(), "super is only defined inside a method")
		return invalidOperand
	}

	if c.callee != ast.Expr(e) {
		c.errorf(e.X.Pos(), "super."+e.Sel.Name+" must be called")
		return invalidOperand
	}

	if e.Sel.Name == "Drop" { // a Drop runs once, by the language, as the selector says
		c.errorf(e.Sel.Pos(), dropByHand(e.X))
		return invalidOperand
	}

	recv := c.recv
	base := receiverBase(recv.typ)
	var order []*Trait        // the traits that may define the next M, most specific first
	var none string           // what has no definition, for the message
	switch t := base.(type) { // a method's receiver is a declared type or Self, nothing else
	case *TypeParam:
		order, none = t.bounds[0].order, "no trait refined by "+t.bounds[0].obj.name
	case *Named:
		order, none = t.order, "no trait of "+t.obj.name
	}

	def, owner := nextDefault(order, nil, e.Sel.Name)
	if def == nil {
		c.errorf(e.Sel.Pos(), none+" defines "+e.Sel.Name+" with a body")
		return invalidOperand
	}

	fn := substFunc(def, map[*TypeParam]Type{owner.self: base})
	c.recordUse(e.X.(*ast.Ident), recv)
	c.recordType(e.X, recv.typ) // the backend converts the receiver as for any call
	c.recordUse(e.Sel, fn)
	c.flowUse(recv, e.X.Pos())
	c.capture(recv, byRef)
	c.receiver(e, operand{variable, recv.typ, recv, e.X}, fn.typ.(*Signature)) // with the expression, so a value receiver moves
	return operand{value, fn.typ, fn, nil}
}

// SuperMethod returns the method a super call, checked as fn, calls on
// the implementing type n: the first default for fn's name in n's
// resolution order after the trait whose default the call is in, or
// from the start when the call is in n's own method; with n for Self.
// It is nil only when the checker did not see n implement the trait.
func (n *Named) SuperMethod(fn *Func) *Func {
	var after *Trait
	if p, ok := receiverBase(fn.typ.(*Signature).recv.typ).(*TypeParam); ok {
		after = p.bounds[0]
	}

	def, owner := nextDefault(n.Origin().order, after, fn.name)
	if def == nil {
		return nil
	}

	return substFunc(def, map[*TypeParam]Type{owner.self: n})
}

// nextDefault returns the first default named name among the traits of
// order that come after the trait after, or among all of them when
// after is nil, and the trait declaring it; or nil.
func nextDefault(order []*Trait, after *Trait, name string) (*Func, *Trait) {
	for _, t := range order {
		if after != nil {
			if t == after {
				after = nil
			}

			continue
		}

		for _, d := range t.defaults {
			if d.name == name {
				return d, t
			}
		}
	}

	return nil, nil
}

// checkRefinements checks that a trait declaring a method a trait it
// refines also declares gives it the same signature over Self, once
// every trait has its methods. A type implementing the refining trait
// implements the refined one too, and a call through either must mean
// the same method; the fault is the trait's and is reported there, once.
func (c *Checker) checkRefinements(f *ast.File) {
	for i, d := range f.Decls {
		d, ok := d.(*ast.TraitDecl)
		if !ok || len(d.Refines) == 0 {
			continue
		}

		tr := c.objs[i].(*TypeName).typ.(*Trait)
		if a, viaA, b, viaB := twoInstances(tr.refines); a != nil {
			c.errorf(d.Name.Pos(), twoInstancesMessage(tr.obj.name, "refines", a, viaA, b, viaB)) // no type could implement it
			continue
		}

		for _, fn := range tr.required {
			c.checkRedeclared(tr, fn)
		}

		for _, fn := range tr.defaults {
			c.checkRedeclared(tr, fn)
		}
	}
}

// checkRedeclared reports the trait tr's method fn when a trait tr
// refines, directly or not, declares the same method with another
// signature. Every declaration counts, not only the first a lookup
// would find: a type's order may put any of them next to fn's.
func (c *Checker) checkRedeclared(tr *Trait, fn *Func) {
	hsig := fn.typ.(*Signature)
	if hasInvalid(hsig) {
		return
	}

	for _, owner := range tr.order {
		want := ownMethod(owner, fn.name)
		if want == nil {
			continue
		}

		wsig := subst(want.typ, map[*TypeParam]Type{owner.self: tr.self}).(*Signature)
		if !sameMethod(hsig, wsig) {
			c.errorf(fn.pos, "method "+tr.obj.name+"."+fn.name+" has the wrong signature for "+owner.obj.name+": have "+receiverText(hsig)+methodText(fn.name, hsig)+", want "+receiverText(wsig)+methodText(fn.name, wsig))
			return
		}
	}
}

// ownMethod returns the method named name that t itself
// declares, required or default, or nil; the traits t refines are not
// consulted.
func ownMethod(t *Trait, name string) *Func {
	for _, m := range t.required {
		if m.name == name {
			return m
		}
	}

	for _, m := range t.defaults {
		if m.name == name {
			return m
		}
	}

	return nil
}

// traitMethod resolves the signature of a method declared in a trait,
// whose receiver must be Self, ref Self or mut Self; nil after an
// error.
func (c *Checker) traitMethod(m *ast.FuncDecl) *Func {
	if m.Recv == nil { // func Parse(text ref string) Result<Self, E>: a function of the implementing type
		return c.traitFunc(m)
	}

	recvType := m.Recv.Type
	if r, ok := recvType.(*ast.RefType); ok {
		recvType = r.Elem
	}

	if _, ok := recvType.(*ast.SelfType); !ok {
		c.errorf(m.Recv.Pos(), "a trait method's receiver is Self, ref Self or mut Self")
		return nil
	}

	var tparams []*TypeParam
	if len(m.TypeParams) > 0 { // a default with parameters of its own, Map<U>; a required method cannot have them (sameMethod)
		tparams = c.typeParams(m.TypeParams)
		c.resolveBounds(m.TypeParams, tparams)
		defer c.closeScope()
		if m.Body == nil {
			c.errorf(m.Name.Pos(), "a required method cannot have type parameters: an implementor's method has none")
		}
	}

	recv := NewVar(m.Recv.Pos(), m.Recv.Name.Name, c.typ(m.Recv.Type))
	params, variadic := c.params(m.Params)
	sig := NewSignature(recv, params, c.results(m.Results))
	sig.tparams = tparams
	sig.variadic = variadic
	fn := NewFunc(m.Name.Pos(), m.Name.Name, sig)
	c.recordDef(m.Name, fn)
	c.checkSources(m.Name.Name, sig, m.Name.Pos())
	return fn
}

// funcKind names what fn is, for a message: a method, or a function of
// a type.
func funcKind(fn *Func) string {
	if fn.static {
		return "a function of the type"
	}

	return "a method"
}

// traitFunc resolves a function a trait declares without a receiver,
// a function of the type that implements it, Self read as that type:
// required, or a default with a body.
func (c *Checker) traitFunc(m *ast.FuncDecl) *Func {
	if m.Of != nil {
		c.errorf(m.Of.Pos(), "a trait's function is of the type that implements it: func "+m.Name.Name+"(...), with Self for the type")
	}

	if m.Body != nil {
		c.errorf(m.Name.Pos(), "a trait's function has no default body: each type that implements the trait writes its own, func Point."+m.Name.Name+"(...)")
	}

	if len(m.TypeParams) > 0 {
		c.errorf(m.Name.Pos(), "a required function cannot have type parameters: an implementor's function has none")
	}

	var tparams []*TypeParam
	if len(m.TypeParams) > 0 {
		tparams = c.typeParams(m.TypeParams)
		c.resolveBounds(m.TypeParams, tparams)
		defer c.closeScope()
	}

	params, variadic := c.params(m.Params)
	sig := NewSignature(nil, params, c.results(m.Results))
	sig.tparams = tparams
	sig.variadic = variadic
	fn := NewFunc(m.Name.Pos(), m.Name.Name, sig)
	fn.static = true
	c.recordDef(m.Name, fn)
	c.checkSources(m.Name.Name, sig, m.Name.Pos())
	return fn
}

// checkImpls checks every type against the traits its declaration
// lists, once all methods are attached: the type inherits the default
// methods its traits provide, in its resolution order, and then each
// required method of each trait, and of the traits those refine, must
// be declared on the type or inherited, with the signature the trait
// gives it, Self read as the type. A trait the list reaches twice,
// named and refined by another, is checked once.
func (c *Checker) checkImpls(f *ast.File) {
	for i, d := range f.Decls {
		d, ok := d.(*ast.TypeDecl)
		if !ok || len(d.Impls) == 0 {
			continue
		}

		n := c.objs[i].(*TypeName).typ.(*Named)
		if a, viaA, b, viaB := twoInstances(n.impls); a != nil {
			c.errorf(d.Name.Pos(), twoInstancesMessage(n.obj.name, "implements", a, viaA, b, viaB))
			continue // one mistake, one message: the methods of both are not asked for
		}

		c.inherit(n, d.Name.Pos())
		seen := make(map[*Trait]bool)
		for j, t := range d.Impls {
			listed := n.impls[j]
			if listed == nil {
				continue
			}

			if (listed == DropTrait || slices.Contains(listed.order, DropTrait)) && !composite(n) { // a value is known live by a flag among its fields, which no other type has
				u, by := n.Underlying().String(), ""
				if listed != DropTrait {
					by = ", which " + listed.obj.name + " requires"
				}

				c.errorf(t.Pos(), n.obj.name+" cannot implement Drop"+by+": it is declared over "+u+", and only a struct or an enum has a Drop; hold the "+u+" in a field, struct "+n.obj.name+" { v "+u+" }")
				continue // one mistake, one message: the methods Drop asks for are not asked again
			}

			if listed.Refines(AttributeTrait) && (n.tparams != nil || !constType(n)) { // an attribute is a constant, which only these types have
				c.errorf(t.Pos(), n.obj.name+" cannot be an attribute: an attribute is a constant, a number, a string, a bool or a struct of them, and not generic")
				continue
			}

			c.deriveListed(n, listed, t.Pos()) // before the methods are required, since a derived one answers
			listed.eachRequired(func(owner *Trait, want *Func) {
				c.checkRequired(n, listed, owner, want, t.Pos())
			}, seen)
		}
	}
}

// inherit gives n the default methods of its traits. The traits are
// consulted in n's resolution order, and the first to provide a method
// the type does not declare itself wins; a type's own method overrides
// every default. A second default for the same name from a trait the
// winner does not refine is an ambiguity the type must settle by
// declaring the method, and a default the type does not take must
// still agree with the method it has. Each name is reported once.
func (c *Checker) inherit(n *Named, pos token.Pos) {
	from := make(map[string]*Trait) // the trait each inherited method came from, or nil for a name reported
	for _, t := range n.order {
		for _, def := range t.defaults {
			winner, seen := from[def.name]
			switch {
			case seen && winner == nil:
				continue
			case seen && !winner.Refines(t):
				c.errorf(pos, n.obj.name+" inherits "+def.name+" from both "+winner.obj.name+" and "+t.obj.name+": declare "+n.obj.name+"."+def.name+" to choose")
				from[def.name] = nil
			case seen:
				// a default behind the winner's, in a trait it refines, with the same signature (checkRefinements)
			case n.Method(def.name) != nil:
				if !c.checkSignature(n, t, def, n.Method(def.name), pos) {
					from[def.name] = nil
				}
			default:
				m := substFunc(def, map[*TypeParam]Type{t.self: n})
				m.trait = t // the instance it came from, whose arguments the backend's copy needs
				n.methods = append(n.methods, m)
				from[def.name] = t
			}
		}
	}
}

// linearize returns the order in which a type's traits are consulted
// for a method: the impl list from right to left, each trait followed
// by the traits it refines, and a trait reached more than once kept at
// its last place, so that a trait always comes before the traits it
// refines, wherever the list names them.
func linearize(impls []*Trait) []*Trait {
	var seq []*Trait
	for i := len(impls) - 1; i >= 0; i-- {
		if impls[i] != nil {
			seq = impls[i].appendLinear(seq)
		}
	}

	last := make(map[*Trait]int, len(seq))
	for i, t := range seq {
		last[t] = i
	}

	order := make([]*Trait, 0, len(last))
	for i, t := range seq {
		if last[t] == i {
			order = append(order, t)
		}
	}

	return order
}

// appendLinear appends t and then, right to left, the traits it refines
// with theirs.
func (t *Trait) appendLinear(seq []*Trait) []*Trait {
	seq = append(seq, t)
	for i := len(t.refines) - 1; i >= 0; i-- {
		if t.refines[i] != nil {
			seq = t.refines[i].appendLinear(seq)
		}
	}

	return seq
}

// checkRequired checks that n has the method want of the trait owner,
// listed on n directly or as a trait that listed refines, declared or
// inherited, with the signature it asks for, Self read as n. A missing
// method is reported at the name in the impl list.
func (c *Checker) checkRequired(n *Named, listed, owner *Trait, want *Func, pos token.Pos) {
	have := n.Method(want.name)
	if have == nil {
		wsig := subst(want.typ, map[*TypeParam]Type{owner.self: n}).(*Signature)
		msg := n.obj.name + " does not implement " + listed.String() + ": missing method " + methodText(want.name, wsig)
		if want.static {
			msg = n.obj.name + " does not implement " + listed.String() + ": missing function " + n.obj.name + "." + methodText(want.name, wsig)
		}
		if owner != listed {
			msg += ", required by " + owner.String()
		}

		c.errorf(pos, msg)
		return
	}

	c.checkSignature(n, owner, want, have, pos)
}

// checkSignature checks that n's method have has the signature the
// trait owner's declaration want asks for, Self read as n, and reports
// false after reporting a wrong one: a method of n's own at the
// method, an inherited one at pos. A method whose own signature failed
// to resolve was reported there and is not compared.
func (c *Checker) checkSignature(n *Named, owner *Trait, want, have *Func, pos token.Pos) bool {
	wsig := subst(want.typ, map[*TypeParam]Type{owner.self: n}).(*Signature)
	hsig := have.typ.(*Signature)
	if hasInvalid(hsig) {
		return true
	}

	if have.static != want.static { // a method takes a value of the type, a function of it none: neither stands for the other
		c.errorf(have.pos, n.obj.name+"."+have.name+" is "+funcKind(have)+", and "+owner.String()+" wants "+funcKind(want))
		return false
	}

	if sameMethod(hsig, wsig) {
		c.keepImpls = append(c.keepImpls, keepImpl{have, want}) // it may keep no more than the trait's declaration
		return true
	}

	if have.origin == nil {
		pos = have.pos
	}

	c.errorf(pos, "method "+n.obj.name+"."+have.name+" has the wrong signature for "+owner.String()+": have "+receiverText(hsig)+methodText(have.name, hsig)+", want "+receiverText(wsig)+methodText(want.name, wsig))
	return false
}

// hasInvalid reports whether a signature mentions the invalid type,
// after an error in its declaration.
func hasInvalid(sig *Signature) bool {
	found := false
	note := func(t Type) { found = found || t == Typ[Invalid] }
	if sig.recv != nil {
		walkTypes(sig.recv.typ, note)
	}

	walkTypes(sig, note)
	return found
}

// sameMethod reports whether a declared method's signature is the one a
// trait asks for: the same receiver, borrowed the same way, the same
// parameters and results, and no type parameters of its own, which a
// trait method cannot have.
func sameMethod(have, want *Signature) bool {
	sameRecv := have.recv == nil && want.recv == nil || have.recv != nil && want.recv != nil && Identical(have.recv.typ, want.recv.typ) // none, for a function of the type
	return sameRecv && have.tparams == nil && have.variadic == want.variadic && identicalVars(have.params, want.params) && Identical(have.results, want.results)
}

// satisfies checks each type argument against the bounds of its
// parameter, for the use called name, and reports the first that does
// not implement one; while the declarations are being resolved the
// question waits, since whether a type is Copy is not known yet.
func (c *Checker) satisfies(tparams []*TypeParam, targs []Type, pos token.Pos, name string) bool {
	if !anyBounds(tparams) {
		return true
	}

	if !c.declared {
		c.pendingBounds = append(c.pendingBounds, pendingBound{tparams, targs, pos, name})
		return true
	}

	for i, p := range tparams {
		for _, b := range p.bounds {
			if b == nil {
				continue
			}

			b = c.boundFor(b, tparams, targs) // Mul<T> asks for Mul<int> of an int
			if !implements(targs[i], b) {
				arg := targs[i].String()
				c.errorf(pos, "cannot use "+arg+" as the type parameter "+p.obj.name+" of "+name+": "+arg+" does not implement "+b.String())
				return false
			}

			if _, isTrait := targs[i].(*Trait); isTrait {
				if what, why := b.unreachable(); what != "" {
					c.errorf(pos, "cannot use "+targs[i].String()+" as the type parameter "+p.obj.name+" of "+name+": the "+what+" of "+b.obj.name+" "+why)
					return false
				}
			}
		}
	}

	return true
}

// unreachable returns the first method or field of the trait t, or of
// a trait it refines, that a trait value cannot reach, as "method
// Same" or "field kids", and why, "mentions Self" or "has type
// parameters"; or "" when there is none. A trait value stands for a
// type parameter bounded by t only when there is none, since the body
// may reach any of them through the parameter, and a trait value
// cannot.
func (t *Trait) unreachable() (what, why string) {
	for tr := range t.lineage() {
		for _, ms := range [][]*Func{tr.required, tr.defaults} {
			for _, m := range ms {
				switch {
				case m.static:
					return "function " + m.name, "takes no value, and a trait value has no type to call it on"
				case mentions(m.typ, tr.self):
					return "method " + m.name, "mentions Self"
				case len(m.typ.(*Signature).tparams) > 0:
					return "method " + m.name, "has type parameters"
				}
			}
		}

		for _, f := range tr.fields {
			if mentions(f.typ, tr.self) {
				return "field " + f.name, "mentions Self"
			}
		}
	}

	return "", ""
}

// boundFor returns the bound b with the type arguments in place of the
// parameters in its right operand: Own<T: Mul<T>> asks an int for
// Mul<int>. Any other bound names no parameter and is returned as it
// is.
func (c *Checker) boundFor(b *Trait, tparams []*TypeParam, targs []Type) *Trait {
	if b.rhs == nil && b.targs == nil {
		return b
	}

	m := make(map[*TypeParam]Type, len(tparams))
	for i, p := range tparams {
		m[p] = targs[i]
	}

	return c.substTrait(b, m)
}

// anyBounds reports whether any of the parameters has a bound to check.
func anyBounds(tparams []*TypeParam) bool {
	for _, p := range tparams {
		if len(p.bounds) > 0 {
			return true
		}
	}

	return false
}

// resolvePendingBounds answers the bound questions asked before the
// declarations were resolved.
func (c *Checker) resolvePendingBounds() {
	for _, p := range c.pendingBounds {
		c.satisfies(p.tparams, p.targs, p.pos, p.name)
	}

	c.pendingBounds = nil
}

// implements reports whether a value of type t may stand where trait
// is required: a type parameter implements what its bounds do, a trait
// value its trait and those it refines, the predeclared types the
// standard traits their structure gives them (stdtraits.go), and a
// declared type the traits it lists.
func implements(t Type, trait *Trait) bool {
	if ownArgs(trait) {
		trait = trait.orig // Iterator<T> inside trait Iterator<T> is the trait itself, which Self implements
	}

	switch t := t.(type) {
	case *TypeParam:
		return t.HasBound(trait)
	case *Trait:
		return t.Refines(trait)
	}

	if trait.standard {
		if ok, decided := standardImplements(t, trait); decided {
			return ok
		}
	}

	n, ok := t.(*Named)
	return ok && (n.Implements(trait) || listsOperator(n, trait))
}

// sameInstance reports whether two instances of one trait were made
// with the same arguments, in this check or another's: a library's
// Iterator<int> and a program's are one trait.
func sameInstance(a, b *Trait) bool {
	if a.orig == nil || a.orig != b.orig || !identicalTypes(a.targs, b.targs) {
		return false
	}

	return a.rhs == nil && b.rhs == nil || a.rhs != nil && b.rhs != nil && Identical(a.rhs, b.rhs)
}

// ownArgs reports whether the instance names its declaration's own
// parameters as its arguments, Iterator<T> written inside trait
// Iterator<T>, which is the declaration under another spelling.
func ownArgs(t *Trait) bool {
	if t.targs == nil {
		return false
	}

	for i, a := range t.targs {
		if a != Type(t.orig.tparams[i]) {
			return false
		}
	}

	return true
}

// methodText writes a method as a trait lists it, without the
// receiver: Area() f64.
func methodText(name string, sig *Signature) string {
	return name + strings.TrimPrefix(sig.String(), "func")
}

// receiverText writes a method's receiver type in parentheses, for a
// message comparing two methods: (ref Circle) .
func receiverText(sig *Signature) string {
	if sig.recv == nil {
		return ""
	}

	return "(" + sig.recv.typ.String() + ") "
}
