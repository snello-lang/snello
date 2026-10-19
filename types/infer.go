// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"github.com/teak-lang/teak/ast"
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
}

func newInference(tparams []*TypeParam) *inference {
	return &inference{tparams, make(map[*TypeParam]Type, len(tparams))}
}

// unify matches the slot type param against the value type arg and
// binds the parameters of the inference that param mentions. A value
// passed to a bare parameter binds what it borrows, not the borrow, so
// that T is never a borrow, which a type argument cannot be; the
// assignment check then says whether the borrow may be read as the
// value. A missing or invalid arg, as an expected type may be, binds
// nothing.
func (in *inference) unify(param, arg Type) {
	if arg == nil || arg == Typ[Invalid] {
		return
	}

	switch p := param.(type) {
	case *TypeParam:
		if _, bound := in.m[p]; bound || !in.owns(p) {
			return
		}

		if r, ok := arg.(*Ref); ok {
			arg = r.elem
		}

		in.m[p] = arg
	case *Ref:
		if a, ok := arg.(*Ref); ok {
			arg = a.elem
		}

		in.unify(p.elem, arg) // an owned value is lent to a ref parameter
	case *Pointer:
		if a, ok := arg.(*Pointer); ok {
			in.unify(p.elem, a.elem)
		}
	case *Option:
		if a, ok := arg.(*Option); ok {
			in.unify(p.elem, a.elem)
		}
	case *Result:
		if a, ok := arg.(*Result); ok {
			in.unify(p.val, a.val)
			in.unify(p.err, a.err)
		}
	case *Slice:
		if a, ok := arg.(*Slice); ok {
			in.unify(p.elem, a.elem)
		}
	case *Array:
		if a, ok := arg.(*Array); ok {
			in.unify(p.elem, a.elem)
		}
	case *Map:
		if a, ok := arg.(*Map); ok {
			in.unify(p.key, a.key)
			in.unify(p.value, a.value)
		}
	case *Chan:
		if a, ok := arg.(*Chan); ok {
			in.unify(p.elem, a.elem)
		}
	case *Tuple:
		if a, ok := arg.(*Tuple); ok && len(a.elems) == len(p.elems) {
			for i := range p.elems {
				in.unify(p.elems[i], a.elems[i])
			}
		}
	case *Signature:
		if a, ok := arg.(*Signature); ok && len(a.params) == len(p.params) {
			for i := range p.params {
				in.unify(p.params[i].typ, a.params[i].typ)
			}

			in.unify(p.results, a.results)
		}
	case *Named:
		if a, ok := arg.(*Named); ok && a.Origin() == p.Origin() {
			pargs, aargs := typeArgs(p), typeArgs(a)
			for i := range pargs {
				in.unify(pargs[i], aargs[i])
			}
		}
	}
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
func (c *Checker) explicit(in *inference, args []ast.Type, name string, pos ast.Node, sig *Signature) bool {
	if len(args) > len(in.tparams) {
		c.errorf(pos.Pos(), name+" takes "+plural(len(in.tparams), "type argument"))
		return false
	}

	for i, a := range args {
		t := c.typ(a)
		if t == Typ[Invalid] {
			return false
		}

		if !(sig != nil && lentOnly(sig, in.tparams[i])) && !c.storable(t, "a type argument", a.Pos()) {
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
				continue
			}

			in.unify(slots[i], ops[i].typ)
			effect(i, ops[i])
		}
	}

	return ops
}

// complete returns the type arguments the values bound, in the
// parameters' order, for the use called name; or reports the first
// parameter left unbound and returns false. An inferred argument obeys
// the rules a written one does: it cannot hold a borrow, which the
// instance would store, and it implements the bounds of its parameter.
func (c *Checker) complete(in *inference, name string, pos ast.Node, sig *Signature) ([]Type, bool) {
	if p := in.missing(); p != nil {
		c.errorf(pos.Pos(), "cannot infer the type parameter "+p.obj.name+" of "+name+": give it, "+name+"<...>")
		return nil, false
	}

	targs := in.args()
	for i, t := range targs {
		if sig != nil && lentOnly(sig, in.tparams[i]) {
			continue // the function borrows every value of this type and keeps none, so an argument holding a borrow is safe
		}

		if !c.storable(t, "a type argument", pos.Pos()) {
			return nil, false
		}
	}

	if !c.satisfies(in.tparams, targs, pos.Pos(), name) {
		return nil, false
	}

	return targs, true
}

// lentOnly reports whether the generic function sig only borrows values
// of its type parameter p and keeps none: every parameter whose type
// mentions p is a borrow, the result does not mention p, and a mut
// parameter mentioning p is the only parameter that does, since through
// a mut borrow the body could store a value of p taken from another
// parameter into the caller's data. An instance then keeps nothing of
// p, so a type argument that holds a borrow, which a generic type's
// instance would store, is safe in it: fmt.Fprintln and io.Copy take a
// buffered writer that borrows the file underneath.
func lentOnly(sig *Signature, p *TypeParam) bool {
	mentioned, mut := 0, false
	for _, v := range sig.params {
		if !mentions(v.typ, p) {
			continue
		}

		r, ok := v.typ.(*Ref)
		if !ok {
			return false
		}

		mentioned++
		mut = mut || r.mut
	}

	if mut && mentioned > 1 {
		return false
	}

	return sig.results == nil || !mentions(sig.results, p)
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
