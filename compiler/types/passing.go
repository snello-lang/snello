// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// A generic call passes borrows along. A type argument may hold a
// borrow, and a generic body, checked once, cannot see it; but a
// function generic in T makes no value of T, so each one it returns or
// stores came from its arguments (Reynolds' parametricity). The call
// is followed from outside: its result holds what every argument
// holding a borrow holds, and each argument lent mut takes what the
// other arguments of the same type parameter hold, as an assignment's
// target takes what its value holds. An instance whose arguments hold
// a borrow keeps none in a map, a channel or a pointer, which the
// graph does not follow.

// A passedArg is an argument of a generic call: the expression, with a
// ref or mut in front of it taken off when the parameter is declared a
// borrow, since what the lent place holds is what the call is given,
// and the parameter's type as declared and in the call. For a bare T
// that stands for a borrow, ref x is the value passed, and stays.
type passedArg struct {
	x          ast.Expr
	decl, inst Type
}

// A passedTo says what the result of a generic call holds: what each of
// args holds, and, when only, nothing else, since the declared result
// holds no borrow; otherwise also the borrow the rule for returned
// borrows ties it to (callSource).
type passedTo struct {
	args     []ast.Expr
	lent     []ast.Expr // of args, those given to a parameter declared T and a borrow in the call, whose value the result may borrow itself
	only     bool
	all      []passedArg  // every argument, with its declared and instance types, for the stores the call makes
	borrowed []*TypeParam // the type parameters whose arguments hold a borrow
}

// A binding is a type parameter of a declaration and what it stands for
// in a call.
type binding struct {
	p   *TypeParam
	arg Type
}

// passBorrows follows a call of fn, whose signature in the call is
// inst, when a type argument holds a borrow: it refuses an instance
// that would keep one in owned data the checker does not follow,
// records the arguments whose borrows the result holds
// (Checker.passed), and gives each argument lent mut what the others it
// may receive hold. It reports whether the result holds a borrow only
// through its type arguments, which then borrows no temporary an
// argument lends.
func (c *Checker) passBorrows(e *ast.CallExpr, fn *Func, inst *Signature) bool {
	decl := genericSig(fn)
	if decl == nil {
		return false
	}

	var borrowed []*TypeParam
	for _, b := range bindings(decl, inst) {
		if holdsBorrow(b.arg) {
			borrowed = append(borrowed, b.p)
			c.pendingHides = append(c.pendingHides, pendingHide{b.p, b.arg, e.Lparen, exprText(e.Fun)})
		}
	}

	if len(borrowed) == 0 {
		return false
	}

	c.ownedParts(inst, e.Lparen)
	args := passedArgs(e, decl, inst)
	only := !holdsBorrow(decl.results)
	passed := passedTo{only: only, all: args, borrowed: borrowed}
	if only || reachesAny(decl.results, borrowed) {
		for _, a := range args {
			if holdsBorrow(a.inst) && !isFuncLit(a.x) { // a closure given to the call borrows nothing it keeps, and returns no borrow of what it captures
				passed.args = append(passed.args, a.x)
				if isBorrow(a.inst) && !isBorrow(a.decl) {
					passed.lent = append(passed.lent, a.x)                                                                                                   // a T taken as a borrow, pick<ref string>(owned()): the result may be the borrow of it; a parameter declared ref T lends it as argEffects sees, the result holding what it holds
					if _, kind := c.placeOf(a.x); kind == notPlace && !isBorrow(c.info.Types[unparen(a.x)]) && !c.static(a.x) && c.temporaryOf(a.x) == nil { // a part of a temporary already names the whole, mkh() for mkh().name
						c.markTemporary(a.x, a.x) // a value made for the call and lent, which argEffects could not see from T as declared
					}
				}
			}
		}
	}

	if c.passed == nil {
		c.passed = make(map[*ast.CallExpr]passedTo)
	}

	c.passed[e] = passed
	for _, p := range borrowed {
		c.storesInto(e, args, p)
	}

	return only
}

// passStores records what the arguments of the generic call e lent mut
// take of what the others hold, as the typing found them.
func (c *Checker) passStores(e *ast.CallExpr) {
	passed := c.passed[e]
	for _, p := range passed.borrowed {
		c.storesInto(e, passed.all, p)
	}
}

// storesInto gives each argument of the call e lent mut, whose
// parameter may receive a value of the type parameter p, what the other
// arguments that may bring one hold; and refuses an argument that holds
// a mut borrow inside it beside one that brings a borrow, since the
// store would go through a borrow the call cannot name.
func (c *Checker) storesInto(e *ast.CallExpr, args []passedArg, p *TypeParam) {
	var givers []passedArg
	for _, a := range args {
		if mayGive(a.decl, p, nil) && !isFuncLit(a.x) && len(c.borrowSources(a.x)) > 0 {
			givers = append(givers, a)
		}
	}

	for _, a := range args {
		if !mayGive(a.decl, p, nil) {
			continue
		}

		var others []passedArg
		for _, g := range givers {
			if g.x != a.x {
				others = append(others, g)
			}
		}

		if len(others) == 0 {
			continue
		}

		if r, ok := a.decl.(*Ref); ok && r.mut {
			c.storeArg(e, a, others)
		} else if HoldsMutBorrow(a.inst) {
			c.errorf(a.x.Pos(), "cannot pass "+exprText(a.x)+" to "+exprText(e.Fun)+" beside "+exprText(others[0].x)+": "+exprText(a.x)+" holds a mut borrow, through which "+exprText(e.Fun)+" may store what "+exprText(others[0].x)+" holds")
		}
	}
}

// storeArg makes the place a lent mut, which a generic call may store
// a value of the others' type parameter into, hold what each of them
// holds. Only a place a variable owns can, as for Push on a slice of
// borrows: through a borrow the holder is another function's.
func (c *Checker) storeArg(e *ast.CallExpr, a passedArg, others []passedArg) {
	pl, kind := through(c.placeOf(a.x))
	if kind == notPlace {
		return // a temporary, dropped with the statement
	}

	if kind != placeOwned {
		c.errorf(a.x.Pos(), "cannot keep a borrow in "+exprText(a.x)+": "+exprText(e.Fun)+" may store what "+exprText(others[0].x)+" holds in it, where the checker cannot follow it")
		return
	}

	for _, o := range others {
		if tmp := c.heldTemporary(o.x); tmp != nil {
			c.errorf(o.x.Pos(), "cannot keep a borrow of a temporary: "+temporaryText(tmp)+" is dropped when the statement ends")
			continue
		}

		c.holdValue(pl.v, pl.path, placeType(pl), o.x, o.x.Pos()) // what the argument holds, not the argument, which may be a temporary lent to the call
	}
}

// genericSig returns the declared signature fn was made from by
// substitution, or fn's own when it has type parameters of its own:
// what a call of fn is matched against. Nil when fn is not generic.
func genericSig(fn *Func) *Signature {
	root := fn.Origin()
	decl, ok := root.typ.(*Signature)
	if !ok || root == fn && decl.tparams == nil {
		return nil
	}

	return decl
}

// passedArgs lists the receiver and the arguments of the call e with
// their parameters' types as declared and in the call. A variadic
// any parameter, which takes anything, mentions no type parameter.
func passedArgs(e *ast.CallExpr, decl, inst *Signature) []passedArg {
	var args []passedArg
	if sel, ok := e.Fun.(*ast.SelectorExpr); ok && decl.recv != nil && inst.recv != nil {
		args = append(args, passedArg{sel.X, decl.recv.typ, inst.recv.typ})
	}

	kind, each := decl.variadicArg()
	instKind, instEach := inst.variadicArg()
	spread := e.Ellipsis.IsValid()
	for i, x := range e.Args {
		if i >= decl.fixed() && kind == VariadicAny {
			break
		}

		_, d, _, _ := argSlot(decl, i, spread, kind, each)
		_, t, _, _ := argSlot(inst, i, spread, instKind, instEach)
		if u, ok := unparen(x).(*ast.UnaryExpr); ok && (u.Op == token.REF || u.Op == token.MUT) && isBorrow(d) {
			x = u.X
		}

		args = append(args, passedArg{x, d, t})
	}

	return args
}

// isFuncLit reports whether e is a closure written out.
func isFuncLit(e ast.Expr) bool {
	_, ok := unparen(e).(*ast.FuncLit)
	return ok
}

// bindings matches a declared signature against the signature it has
// in a call and returns what each type parameter stands for, in the
// order the parameters are met: the receiver's, the parameters', the
// result's.
func bindings(decl, inst *Signature) []binding {
	var bs []binding
	if decl.recv != nil && inst.recv != nil {
		bs = bind(decl.recv.typ, inst.recv.typ, bs)
	}

	for i, p := range decl.params {
		if i < len(inst.params) {
			bs = bind(p.typ, inst.params[i].typ, bs)
		}
	}

	return bind(decl.results, inst.results, bs)
}

// bind matches the declared type d against t, the same type with
// arguments in place of type parameters, and appends what each type
// parameter met for the first time stands for.
func bind(d, t Type, bs []binding) []binding {
	if d == nil || t == nil {
		return bs
	}

	switch d := d.(type) {
	case *TypeParam:
		for _, b := range bs {
			if b.p == d {
				return bs
			}
		}

		return append(bs, binding{d, t})
	case *Ref:
		if t, ok := t.(*Ref); ok {
			return bind(d.elem, t.elem, bs)
		}
	case *Pointer:
		if t, ok := t.(*Pointer); ok {
			return bind(d.elem, t.elem, bs)
		}
	case *Option:
		if t, ok := t.(*Option); ok {
			return bind(d.elem, t.elem, bs)
		}
	case *Slice:
		if t, ok := t.(*Slice); ok {
			return bind(d.elem, t.elem, bs)
		}
	case *Array:
		if t, ok := t.(*Array); ok {
			return bind(d.elem, t.elem, bs)
		}
	case *Chan:
		if t, ok := t.(*Chan); ok {
			return bind(d.elem, t.elem, bs)
		}
	case *Result:
		if t, ok := t.(*Result); ok {
			return bind(d.err, t.err, bind(d.val, t.val, bs))
		}
	case *Map:
		if t, ok := t.(*Map); ok {
			return bind(d.value, t.value, bind(d.key, t.key, bs))
		}
	case *Tuple:
		if t, ok := t.(*Tuple); ok && len(t.elems) == len(d.elems) {
			for i := range d.elems {
				bs = bind(d.elems[i], t.elems[i], bs)
			}
		}
	case *Signature:
		if t, ok := t.(*Signature); ok && len(t.params) == len(d.params) {
			for i := range d.params {
				bs = bind(d.params[i].typ, t.params[i].typ, bs)
			}

			return bind(d.results, t.results, bs)
		}
	case *Named:
		if t, ok := t.(*Named); ok {
			da, ta := typeArgs(d), typeArgs(t)
			for i := range min(len(da), len(ta)) {
				bs = bind(da[i], ta[i], bs)
			}
		}
	}

	return bs
}

// reachesAny reports whether a value of type t may hold a value of one
// of the type parameters ps.
func reachesAny(t Type, ps []*TypeParam) bool {
	for _, p := range ps {
		if mayGive(t, p, nil) {
			return true
		}
	}

	return false
}

// mayGive reports whether a value of type t may hold or give a value of
// the type parameter p: t mentions p, or a type parameter in t has a
// bound that does, so that the body reaches p through the bound's
// methods; Self reaches its trait's parameters. seen ends a walk that
// comes back to a parameter.
func mayGive(t Type, p *TypeParam, seen []*TypeParam) bool {
	found := false
	walkTypes(t, func(t Type) {
		q, ok := t.(*TypeParam)
		if found || !ok {
			return
		}

		if q == p {
			found = true
			return
		}

		for _, s := range seen {
			if s == q {
				return
			}
		}

		seen = append(seen, q)
		for _, b := range q.bounds {
			if b != nil && boundReaches(b, p, seen) {
				found = true
				return
			}
		}
	})

	return found
}

// boundReaches reports whether the bound b gives a value of p: one of
// its arguments reaches p, or, for the trait itself as Self's bound,
// p is one of its parameters; and so for the traits it refines.
func boundReaches(b *Trait, p *TypeParam, seen []*TypeParam) bool {
	for _, a := range b.targs {
		if mayGive(a, p, seen) {
			return true
		}
	}

	if b.targs == nil {
		for _, q := range b.tparams {
			if q == p {
				return true
			}
		}
	}

	for _, r := range b.refines {
		if r != nil && boundReaches(r, p, seen) {
			return true
		}
	}

	return false
}
