// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"github.com/snello-lang/snello/compiler/ast"
)

// Inference finds the type arguments of a generic use from the values
// it is given, after go/types: a call Map(l, f), a variant Leaf(1), or
// a struct literal Pair{a: 1, b: 2.0}. Each value is matched against
// the type of the slot it fills, parameter or field, and every type
// parameter met in that type is bound to what the value's type has in
// its place. The first binding of a parameter stands; a later value
// that disagrees is reported by the ordinary assignment check, once the
// arguments are known.
//
// The values are evaluated in source order, except the ones that take
// their type from context, a literal or None, which go last with the
// slot's type as far as it is known by then, so that F(xs, 1) with xs a
// []f64 makes the 1 an f64. A value's move or borrow happens when it is
// evaluated, as in any call, so F(s, s) still reports the second use of
// a moved s; only the type check waits for the arguments.

// An inference holds the type parameters of one generic use and what
// each is bound to so far.
type inference struct {
	tparams []*TypeParam
	m       map[*TypeParam]Type
	outer   []binding // for a method made from a trait's default or a generic type's method, what the parameters of those stand for, which its own may name in their bounds
	invalid bool      // a value it would learn from was invalid, and reported
}

func newInference(tparams []*TypeParam) *inference {
	return &inference{tparams: tparams, m: make(map[*TypeParam]Type, len(tparams))}
}

// unify matches the slot type param against the value type arg and
// binds the parameters of the inference that param mentions. A value
// passed to a bare parameter binds what it borrows, not the borrow, as
// a borrow passed to a value parameter is read as the value; the
// assignment check then says whether the borrow may be read as the
// value. Inside a type a parameter binds what stands in its place, a
// borrow too: []T against a []ref string binds T to ref string, and
// Iterator<V> against an Iterator<ref int> V to ref int. A missing or
// invalid arg, as an expected type may be, binds nothing.
func (in *inference) unify(param, arg Type) {
	in.match(param, arg, false)
}

// match is unify, exact once inside a type: a bare parameter there
// binds the borrow it meets.
func (in *inference) match(param, arg Type, exact bool) {
	if arg == nil || arg == Typ[Invalid] {
		return
	}

	switch param.(type) {
	case *Pointer, *Option, *Result, *Slice, *Array, *Map, *Chan, *Tuple, *Signature:
		if a, ok := arg.(*Named); ok {
			arg = a.Underlying() // a declared type against a type of no name, by its underlying type, as it is assignable to one: an Ints against []T binds T to int
		}
	}

	switch p := param.(type) {
	case *TypeParam:
		if _, bound := in.m[p]; bound || !in.owns(p) {
			return
		}

		if r, ok := arg.(*Ref); ok && !exact {
			arg = r.elem
		}

		in.m[p] = arg
	case *Ref:
		if a, ok := arg.(*Ref); ok {
			arg = a.elem
		}

		in.match(p.elem, arg, true) // an owned value is lent to a ref parameter
	case *Pointer:
		if a, ok := arg.(*Pointer); ok {
			in.match(p.elem, a.elem, true)
		}
	case *Option:
		if a, ok := arg.(*Option); ok {
			in.match(p.elem, a.elem, true)
		}
	case *Result:
		if a, ok := arg.(*Result); ok {
			in.match(p.val, a.val, true)
			in.match(p.err, a.err, true)
		}
	case *Slice:
		if a, ok := arg.(*Slice); ok {
			in.match(p.elem, a.elem, true)
		}
	case *Array:
		if a, ok := arg.(*Array); ok {
			in.match(p.elem, a.elem, true)
		}
	case *Map:
		if a, ok := arg.(*Map); ok {
			in.match(p.key, a.key, true)
			in.match(p.value, a.value, true)
		}
	case *Chan:
		if a, ok := arg.(*Chan); ok {
			in.match(p.elem, a.elem, true)
		}
	case *Tuple:
		if a, ok := arg.(*Tuple); ok && len(a.elems) == len(p.elems) {
			for i := range p.elems {
				in.match(p.elems[i], a.elems[i], true)
			}
		}
	case *Signature:
		if a, ok := arg.(*Signature); ok && len(a.params) == len(p.params) {
			for i := range p.params {
				in.match(p.params[i].typ, a.params[i].typ, true)
			}

			in.match(p.results, a.results, true)
		}
	case *Named:
		if a, ok := arg.(*Named); ok && a.Origin() == p.Origin() {
			pargs, aargs := typeArgs(p), typeArgs(a)
			for i := range pargs {
				in.match(pargs[i], aargs[i], true)
			}
		}
	case *Trait:
		if p.orig == nil || p.targs == nil {
			return
		}

		if inst := implementedInstance(arg, p.orig); inst != nil { // an Iterator<int>, or a Range that implements it
			for i := range p.targs {
				in.match(p.targs[i], inst.targs[i], true)
			}
		}
	}
}

// fromBounds binds what the bounds of a parameter say of the type bound
// to it: with I bound to Range and the bound I: Iterator<T>, T is bound
// to int, read off the one instance of Iterator that Range implements.
// It goes round until nothing new is bound, since what one bound gives
// may be what another's parameter needed.
func (in *inference) fromBounds() {
	for grew := true; grew; {
		grew = false
		for _, p := range in.tparams {
			t, ok := in.m[p]
			if !ok {
				continue
			}

			for _, b := range p.bounds {
				if b == nil || b.targs == nil {
					continue
				}

				if inst := implementedInstance(t, b.orig); inst != nil {
					before := len(in.m)
					for i := range b.targs {
						in.match(b.targs[i], inst.targs[i], true)
					}

					grew = grew || len(in.m) > before
				}
			}
		}
	}
}

// unmet returns why p could not be read off a bound: a parameter bound
// to a type that implements no instance of a bound mentioning p, "Blob
// implements no Iterator"; "" otherwise.
func (in *inference) unmet(p *TypeParam) string {
	for _, q := range in.tparams {
		t, ok := in.m[q]
		if !ok {
			continue
		}

		for _, b := range q.bounds {
			if b != nil && b.targs != nil && mentions(b, p) && implementedInstance(t, b.orig) == nil {
				return t.String() + " implements no " + b.obj.name
			}
		}
	}

	return ""
}

// implementedInstance returns the instance of the generic trait orig
// that t implements, or nil: through the traits a declared type lists,
// with a generic type's arguments in place, the bounds of a type
// parameter, or a trait value's own trait, each with the traits they
// refine. A type implements a generic trait at one argument, so the
// first found is the one.
func implementedInstance(t Type, orig *Trait) *Trait {
	var listed []*Trait
	var m map[*TypeParam]Type
	switch t := t.(type) {
	case *Named:
		listed = t.Origin().impls
		if t.orig != nil {
			m = t.substitution()
		}
	case *TypeParam:
		listed = t.bounds
	case *Trait:
		listed = []*Trait{t}
	}

	for _, l := range listed {
		if l == nil {
			continue
		}

		for r := range l.lineage() {
			if r != nil && r.orig == orig && r.targs != nil {
				if m != nil {
					return substInstance(r, m) // MapIter<Range, int, string> lists Iterator<U>: Iterator<string>
				}

				return r
			}
		}
	}

	return nil
}

// typeArgs returns the arguments a named type stands for: an
// instance's, or a generic declaration's own parameters, which is what
// its name means inside its own definition.
func typeArgs(n *Named) []Type {
	if n.orig == nil && n.tparams != nil {
		return paramTypes(n.tparams)
	}

	return n.targs
}

func (in *inference) owns(p *TypeParam) bool {
	for _, q := range in.tparams {
		if q == p {
			return true
		}
	}

	return false
}

// known reports whether t mentions no parameter of the inference that
// is still unbound, so that it can serve as a hint.
func (in *inference) known(t Type) bool {
	for _, p := range in.tparams {
		if _, bound := in.m[p]; !bound && mentions(t, p) {
			return false
		}
	}

	return true
}

// apply returns t with the bound parameters in place.
func (in *inference) apply(t Type) Type { return subst(t, in.m) }

// missing returns the first parameter still unbound, or nil.
func (in *inference) missing() *TypeParam {
	for _, p := range in.tparams {
		if _, bound := in.m[p]; !bound {
			return p
		}
	}

	return nil
}

// args returns the bound arguments, in the parameters' order.
func (in *inference) args() []Type {
	targs := make([]Type, len(in.tparams))
	for i, p := range in.tparams {
		targs[i] = in.m[p]
	}

	return targs
}

// explicit binds the type arguments written at a generic use, in
// order, F<int>(x); fewer than the parameters is allowed, and the rest
// are inferred. It reports false after an error.
func (c *Checker) explicit(in *inference, args []ast.Type, name string, pos ast.Node) bool {
	if len(args) > len(in.tparams) {
		c.errorf(pos.Pos(), name+" takes "+plural(len(in.tparams), "type argument"))
		return false
	}

	for i, a := range args {
		t := c.typ(a)
		if t == Typ[Invalid] {
			return false
		}

		in.m[in.tparams[i]] = t
	}

	return true
}

// evaluate checks the values for slots of the given types: in source
// order, the values that take their type from context last. Each is
// matched against its slot when it is evaluated, and effect then runs
// the moves and borrows that its slot's shape implies, for every value
// that checked.
func (c *Checker) evaluate(in *inference, values []ast.Expr, slots []Type, effect func(i int, op operand)) []operand {
	ops := make([]operand, len(values))
	for _, late := range []bool{false, true} {
		for i, v := range values {
			if contextTyped(v) != late {
				continue
			}

			var hint Type
			if in.known(slots[i]) {
				hint = paramHint(in.apply(slots[i]))
			}

			ops[i] = c.expr(v, hint)
			if ops[i].mode == invalid {
				in.invalid = true
				continue
			}

			in.unify(slots[i], ops[i].typ)
			effect(i, ops[i])
		}

		in.fromBounds() // before a literal takes a type: Sum(mut r, 0) with r an Iterator<i64> makes the 0 an i64
	}

	return ops
}

// complete returns the type arguments the values bound, in the
// parameters' order, for the use called name; or reports the first
// parameter left unbound and returns false. An inferred argument obeys
// the rules a written one does: it implements the bounds of its
// parameter.
func (c *Checker) complete(in *inference, name string, pos ast.Node) ([]Type, bool) {
	in.fromBounds()
	if p := in.missing(); p != nil {
		if in.invalid { // what would have bound p was reported already
			return nil, false
		}

		if why := in.unmet(p); why != "" {
			c.errorf(pos.Pos(), "cannot infer the type parameter "+p.obj.name+" of "+name+": "+why)
			return nil, false
		}

		c.errorf(pos.Pos(), "cannot infer the type parameter "+p.obj.name+" of "+name+": give it, "+name+"<...>")
		return nil, false
	}

	targs := in.args()
	if !c.satisfiesIn(in.tparams, targs, in.outer, pos.Pos(), name) {
		return nil, false
	}

	return targs, true
}

// contextTyped reports whether e takes its type from the context it is
// used in, as a literal, None, or a composite literal without a type
// does, so that it is evaluated after the values whose types it may
// need.
func contextTyped(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.BasicLit:
		return true
	case *ast.ParenExpr:
		return contextTyped(e.X)
	case *ast.UnaryExpr:
		return contextTyped(e.X)
	case *ast.Ident:
		return e.Name == "None"
	case *ast.CompositeLit:
		return e.Type == nil
	}

	return false
}
