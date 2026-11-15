// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"slices"
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// The questions a type answers. A reflecting function is checked once;
// what depends on the type it is used with, which arm of a match on a
// type it takes and which fields a loop over its parts reaches, is asked
// here, after the package's bodies are checked, for each type a generic
// function is used with: from every call of the package whose type
// arguments are all known, the walk follows the calls of generic
// functions inside each body, with the types each is given, through the
// arm each type takes and the parts each loop gives. A mistake is
// reported at the call the program wrote, with the chain that led to it.

// maxChain bounds a chain of generic calls the walk follows, so that a
// program it cannot finish is refused, at the call the chain began
// from, rather than walked for ever.
const maxChain = 256

// A reachStep is one generic function on the chain the walk followed,
// with the type arguments it was given.
type reachStep struct {
	fn    *Func
	targs []Type
}

// A reachWalk is one walk: the tables and package it asks about, where
// it began, the chain it is on, the instances already asked about, and
// the mistakes it found, which reach reports. It holds no Checker, so
// that a check that never walks keeps its Checker off the heap.
type reachWalk struct {
	info  *Info
	pkg   *Package
	root  token.Pos
	chain []reachStep
	seen  map[*Func][][]Type // the type arguments each generic function was asked about with, compared by identity
	bound map[*Part]reachPart
	deep  token.Pos // the call a chain past maxChain began from, reported once
	errs  []reachErr
}

// A reachErr is a mistake the walk found, at the call it began from.
type reachErr struct {
	pos token.Pos
	msg string
}

// report notes a mistake found at the call the walk began from.
func (w *reachWalk) report(msg string) {
	w.errs = append(w.errs, reachErr{w.root, msg})
}

// A reachPart is the part a loop around the walk's place stands for in
// the copy being walked: a variant, whose fields a loop inside reaches.
type reachPart struct {
	variant *Variant
}

// reach walks from each call of the package's own code whose type
// arguments are all known.
func (c *Checker) reach(fs []*ast.File) {
	if c.info == nil || c.info.Arms == nil || len(c.errors) > 0 { // the questions are asked of a program that checked and reflects: every reflecting function matches on a type, and the tables are the build's
		return
	}

	w := &reachWalk{info: c.info, pkg: c.pkg, seen: make(map[*Func][][]Type)}
	for k, f := range fs { // the bodies of the functions with their types known, in the order written, so that a mistake is reported at the first call that leads to it, the same in every check
		for i, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Body != nil {
				if fn, ok := c.fileState[k].objs[i].(*Func); ok && fn.decl == nil {
					w.body(fd.Body, nil, c.pkg)
				}
			}
		}
	}

	for _, e := range w.errs {
		c.errorf(e.pos, e.msg)
	}
}

// anyParam reports whether a type of ts holds a type parameter.
func anyParam(ts []Type) bool {
	for _, t := range ts {
		if holdsTypeParam(t) {
			return true
		}
	}

	return false
}

// call walks fn, called by the name id, with recv the type of what it is
// called on, inside a body whose instance has the substitution sub: its
// type arguments, the call's own and its receiver's, the generic type's
// or Self of a trait's default, with sub in place, once they are all
// known. A call met outside any generic body begins the chain there.
func (w *reachWalk) call(id *ast.Ident, fn *Func, recv Type, sub map[*TypeParam]Type) {
	origin := fn.Origin()
	if origin.decl == nil {
		return
	}

	osig := origin.typ.(*Signature)
	targs := make([]Type, 0, len(osig.tparams)+2)
	if inst, ok := w.info.Instances[id]; ok {
		for _, t := range inst.TypeArgs {
			targs = append(targs, Subst(t, sub))
		}
	}

	params := osig.tparams
	if osig.recv != nil { // the receiver's: a generic type's arguments, or the type that inherits a default
		if recv == nil && fn.typ.(*Signature).recv != nil {
			recv = fn.typ.(*Signature).recv.typ
		}

		bound, args := receiverBinds(pointee(osig.recv.typ), pointee(Subst(recv, sub)))
		params = append(slices.Clip(params), bound...) // a copy when it grows: the declaration's own list is left alone
		targs = append(targs, args...)
	} else if origin.static && recv != nil { // G<bool>.Kind: the instance before the dot gives its type's parameters
		if n, ok := pointee(Subst(recv, sub)).(*Named); ok && n.orig != nil {
			params = append(slices.Clip(params), n.orig.tparams...)
			targs = append(targs, n.targs...)
		}
	}

	if len(params) == 0 || len(targs) != len(params) || anyParam(targs) {
		return
	}

	if len(w.chain) == 0 {
		w.root = id.Pos()
	}

	w.instance(origin, params, targs)
}

// receiverBinds returns the type parameters a method's declared
// receiver want binds, and the arguments the receiver have gives them:
// the parameters of a generic type's declaration, Box<T>, or Self of a
// trait's default; none when have does not fit want.
func receiverBinds(want, have Type) ([]*TypeParam, []Type) {
	switch want := want.(type) {
	case *Named:
		bound := want.tparams // the declaration's receiver, Box<T>: its arguments are its own parameters
		if len(want.targs) > 0 {
			bound = nil
			for _, a := range want.targs {
				if tp, ok := a.(*TypeParam); ok {
					bound = append(bound, tp)
				}
			}
		}

		if n, ok := have.(*Named); ok && len(n.targs) == len(bound) {
			return bound, n.targs
		}
	case *TypeParam:
		return []*TypeParam{want}, []Type{have}
	}

	return nil, nil
}

// instance asks the questions of fn's body for its type parameters
// params given targs, once for each set, and of the generic functions it
// calls in turn.
func (w *reachWalk) instance(origin *Func, params []*TypeParam, targs []Type) {
	decl := origin.decl
	if decl == nil {
		return
	}

	for _, seen := range w.seen[origin] {
		if identicalTypes(seen, targs) {
			return
		}
	}

	w.seen[origin] = append(w.seen[origin], targs)
	if w.grows(origin, targs) {
		return
	}

	if len(w.chain) >= maxChain {
		if w.deep != w.root {
			w.deep = w.root
			var b strings.Builder
			w.step(&b, w.chain[0])
			b.WriteString(", ..., ")
			w.step(&b, w.chain[len(w.chain)-1])
			b.WriteString(", ")
			w.step(&b, reachStep{origin, targs})
			w.report(b.String() + ": the calls of generic functions go deeper than " + strconv.Itoa(maxChain) + ", more than the compiler follows")
		}

		return
	}

	sub := make(map[*TypeParam]Type, len(targs))
	for i, p := range params {
		sub[p] = targs[i]
	}

	w.chain = append(w.chain, reachStep{origin, targs})
	w.body(decl.Body, sub, origin.pkg)
	w.chain = w.chain[:len(w.chain)-1]
}

// grows reports, and refuses, a call of fn whose types have grown twice
// along the chain: fn given a type that holds the type an earlier call
// of fn was given, which holds the type a call before that was given,
// f<int>, f<[]int>, f<[][]int>. A recursive type steps into a larger type
// once and comes back to one already asked about, Value to []Value to
// Value; a chain that grows twice grows without end.
func (w *reachWalk) grows(fn *Func, targs []Type) bool {
	var smaller []reachStep // the earlier calls of fn whose types the new ones hold
	for _, s := range w.chain {
		if s.fn == fn && strictlyHolds(targs, s.targs) {
			smaller = append(smaller, s)
		}
	}

	for i, a := range smaller {
		for _, b := range smaller[i+1:] {
			if strictlyHolds(b.targs, a.targs) {
				w.report(w.where() + fn.name + "<" + typesKey(targs) + "> holds " + fn.name + "<" + typesKey(b.targs) + ">, which holds " + fn.name + "<" + typesKey(a.targs) + ">; the types grow without end")
				return true
			}
		}
	}

	return false
}

// strictlyHolds reports whether some type of ts holds, and differs
// from, the type of us at the same place.
func strictlyHolds(ts, us []Type) bool {
	for i, t := range ts {
		if i < len(us) && !Identical(t, us[i]) && contains(t, us[i]) {
			return true
		}
	}

	return false
}

// contains reports whether t holds u.
func contains(t, u Type) bool {
	found := false
	walkTypes(t, func(t Type) {
		found = found || Identical(t, u)
	})

	return found
}

// body walks a body for the substitution sub, the type arguments of the
// instance being asked about, in a function of the package pkg.
func (w *reachWalk) body(n ast.Node, sub map[*TypeParam]Type, pkg *Package) {
	ast.Inspect(n, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.MatchExpr:
			if x.OnType {
				w.typeArm(x, sub, pkg)
				return false
			}

			if len(x.Arms) == 1 {
				if p, ok := x.Arms[0].Pat.(*ast.PartsPat); ok {
					w.walkParts(p.Var, sub, pkg, func(sub map[*TypeParam]Type) { w.body(x.Arms[0].Body, sub, pkg) })
					return false
				}
			}
		case *ast.RangeStmt:
			if key, ok := x.Key.(*ast.Ident); ok && w.walkParts(key, sub, pkg, func(sub map[*TypeParam]Type) { w.body(x.Body, sub, pkg) }) {
				return false
			}
		case *ast.PartsElt:
			if w.walkParts(x.Var, sub, pkg, func(sub map[*TypeParam]Type) { w.body(x.Value, sub, pkg) }) {
				return false
			}
		case *ast.SelectorExpr: // a method, called on a value whose type gives its receiver's type arguments
			if fn, ok := w.info.Uses[x.Sel].(*Func); ok {
				w.call(x.Sel, fn, w.info.Types[x.X], sub)
				w.body(x.X, sub, pkg)
				return false
			}
		case *ast.Ident:
			if fn, ok := w.info.Uses[x].(*Func); ok {
				w.call(x, fn, nil, sub)
			}
		}

		return true
	})
}

// typeArm walks the arm of a match on a type that the instance's type
// takes, with what its shape binds, or refuses the type no arm takes.
func (w *reachWalk) typeArm(x *ast.MatchExpr, sub map[*TypeParam]Type, pkg *Package) {
	tp := w.matched(x)
	if tp == nil {
		return
	}

	t := Subst(tp, sub)
	if holdsTypeParam(t) {
		return
	}

	arms := make([]TypeArm, len(x.Arms))
	for i, arm := range x.Arms {
		arms[i] = w.info.Arms[arm]
		if arms[i].Type != nil {
			arms[i].Type = Subst(arms[i].Type, sub) // an arm may name the function's other type parameters, []U
		}
	}

	i, binds := ChooseArm(arms, t)
	if i < 0 {
		w.report(w.where() + "no arm of the match on " + tp.obj.name + " takes " + t.String())
		return
	}

	if tv, ok := t.(*Trait); ok && isTraitArm(arms[i]) { // a trait value stands for its trait in the arm only when a value can reach what the trait has
		if what, why := arms[i].Type.(*Trait).unreachable(); what != "" {
			w.report(w.where() + "a value of " + tv.String() + " takes the arm " + arms[i].Type.String() + ", whose " + what + " " + why)
			return
		}
	}

	if len(binds) > 0 {
		s := make(map[*TypeParam]Type, len(sub)+len(binds))
		for p, a := range sub {
			s[p] = a
		}

		for p, a := range binds {
			s[p] = a
		}

		sub = s
	}

	w.body(x.Arms[i].Body, sub, pkg)
}

// matched returns the type parameter a match on a type is on: the one
// its name stands for, or the type of a field, f.type.
func (w *reachWalk) matched(x *ast.MatchExpr) *TypeParam {
	switch m := x.X.(type) {
	case *ast.Ident:
		if tn, ok := w.info.Uses[m].(*TypeName); ok {
			tp, _ := tn.typ.(*TypeParam)
			return tp
		}
	case *ast.SelectorExpr:
		if id, ok := m.X.(*ast.Ident); ok {
			if p, ok := w.info.Uses[id].(*Part); ok {
				tp, _ := p.typ.(*TypeParam)
				return tp
			}
		}
	}

	return nil
}

// walkParts walks a loop over a type's parts, the one key names, once
// for each part of the instance's type, after asking whether the
// reflecting function may reach it; it reports whether key names such
// a loop's part.
func (w *reachWalk) walkParts(key *ast.Ident, sub map[*TypeParam]Type, pkg *Package, each func(map[*TypeParam]Type)) bool {
	part, _ := w.info.Defs[key].(*Part)
	if part == nil {
		return false
	}

	of := Subst(part.of, sub)
	if holdsTypeParam(of) {
		return true
	}

	named, _ := of.(*Named)
	var opener *Package // the package that opened the type by listing Reflect, which opens the parts it declared
	if named != nil && implements(named, ReflectTrait) {
		opener = named.obj.pkg
	}

	switch {
	case part.variant:
		en, ok := of.Underlying().(*Enum)
		if !ok {
			return true
		}

		for _, v := range en.variants {
			if v.pkg != pkg && v.pkg != opener && !ast.IsExported(v.name) {
				w.report(w.where() + "variant " + v.name + " of " + of.String() + " is not exported; export it, or list Reflect on " + of.String())
				return true
			}

			w.bind(part, v, func() { each(sub) })
		}
	case part.in != nil:
		v := w.bound[part.in].variant
		if v == nil {
			return true
		}

		for _, f := range v.fields { // reached as the variant is, by position, as a pattern of another package binds them
			if !w.reachable(f, of, pkg, f.pkg) {
				return true
			}

			each(withPart(sub, part, f.typ))
		}
	default:
		st, ok := of.Underlying().(*Struct)
		if !ok {
			return true
		}

		for _, f := range st.fields {
			if !w.reachable(f, of, pkg, opener) {
				return true
			}

			each(withPart(sub, part, f.typ))
		}
	}

	return true
}

// reachable reports, and refuses when not, whether a reflecting function
// may reach the field f of the type t: one its own package, pkg,
// declared, an exported one, or one the package that declared it opened, opener,
// by listing Reflect on the type; and one that holds no borrow. A type
// declared over another package's struct opens none of its fields. A
// variant's fields are open where the variant is.
func (w *reachWalk) reachable(f *Var, t Type, pkg, opener *Package) bool {
	if f.pkg != pkg && f.pkg != opener && !ast.IsExported(f.name) {
		if n, ok := t.(*Named); ok && n.obj.pkg != f.pkg { // a type declared over another package's struct: only that package opens its fields
			w.report(w.where() + "field " + f.name + " of " + t.String() + " is not exported by " + f.pkg.name + ", which declares it; a type declared over its struct opens none of its fields")
			return false
		}

		w.report(w.where() + "field " + f.name + " of " + t.String() + " is not exported; export it, or list Reflect on " + t.String())
		return false
	}

	if holdsBorrow(f.typ) {
		w.report(w.where() + "field " + f.name + " of " + t.String() + " holds a borrow, " + f.typ.String() + ", which no type argument holds")
		return false
	}

	return true
}

// bind walks with the variant v standing for the part p.
func (w *reachWalk) bind(p *Part, v *Variant, walk func()) {
	if w.bound == nil {
		w.bound = make(map[*Part]reachPart)
	}

	old, had := w.bound[p]
	w.bound[p] = reachPart{v}
	walk()
	if had {
		w.bound[p] = old
	} else {
		delete(w.bound, p)
	}
}

// withPart returns sub with t in place of the part's type, f.type.
func withPart(sub map[*TypeParam]Type, p *Part, t Type) map[*TypeParam]Type {
	s := make(map[*TypeParam]Type, len(sub)+1)
	for q, a := range sub {
		s[q] = a
	}

	if tp, ok := p.typ.(*TypeParam); ok {
		s[tp] = t
	}

	return s
}

// where writes the chain of generic calls the walk is on, for a
// message: json.Marshal<Server>, write<chan int>: .
func (w *reachWalk) where() string {
	var b strings.Builder
	for i, s := range w.chain {
		if i > 0 {
			b.WriteString(", ")
		}

		w.step(&b, s)
	}

	if b.Len() > 0 {
		b.WriteString(": ")
	}

	return b.String()
}

// step writes one call of a chain, its package named when it is not
// the walk's, and a method's receiver type before its name:
// json.writeValue<int>, Box.Kind<bool>, refl.Kit.Pick<bool>.
func (w *reachWalk) step(b *strings.Builder, s reachStep) {
	pkg, recv := s.fn.pkg, receiverNamed(s.fn)
	if recv != nil {
		pkg = recv.obj.pkg // a method is declared in its type's package, and has none of its own
	}

	if pkg != nil && pkg != w.pkg && pkg.name != "" {
		b.WriteString(pkg.name)
		b.WriteString(".")
	}

	if recv != nil {
		b.WriteString(recv.obj.name)
		b.WriteString(".")
	}

	b.WriteString(s.fn.name)
	b.WriteString("<")
	b.WriteString(typesKey(s.targs))
	b.WriteString(">")
}

// receiverNamed returns the named type a method is declared on, or nil
// for a function.
func receiverNamed(fn *Func) *Named {
	sig, ok := fn.typ.(*Signature)
	if !ok || sig.recv == nil {
		return nil
	}

	n, ok := stripRef(sig.recv.typ).(*Named)
	if !ok || n.obj == nil {
		return nil
	}

	return n
}

// typesKey writes types for a key and a message: int, []string.
func typesKey(ts []Type) string {
	var b strings.Builder
	for i, t := range ts {
		if i > 0 {
			b.WriteString(", ")
		}

		b.WriteString(t.String())
	}

	return b.String()
}

// genericDecl reports whether fn is a generic function or a method of
// a generic type, whose body the walk asks about for each type it is
// used with.
func genericDecl(fn *Func) bool {
	sig := fn.typ.(*Signature)
	if sig.tparams != nil {
		return true
	}

	if sig.recv != nil {
		if n, ok := pointee(sig.recv.typ).(*Named); ok && n.tparams != nil {
			return true
		}
	}

	return false
}
