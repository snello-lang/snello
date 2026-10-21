// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "github.com/teak-lang/teak/compiler/token"

// Two kinds of declaration describe no type a program can hold, and
// both are refused once every declaration is resolved, before anything
// asks what a type contains, since the question would never end.
//
// A generic type that refers to itself with a growing argument,
// struct S<T> { s *S<S<T>>? }, would need S<int>, then S<S<int>>, and
// so on without end, and each instance is a type of its own when the
// program is compiled. The check is Go's for generic functions: a
// graph from each type parameter to the parameters it is passed to,
// where passing it wrapped, S<T> as the argument of S, grows it; a
// growing edge on a cycle is refused.
//
// A type that contains itself by value, struct R { r R? }, has no
// finite size: only a pointer, slice or map in between breaks the
// chain, as in Go.

// A paramEdge records that a type parameter is passed to another, by
// the instance inst; grows says it is passed wrapped.
type paramEdge struct {
	to    *TypeParam
	grows bool
	inst  *Named
}

// A callEdge records that a call inside a generic body passes a type
// parameter of that body, from, to a parameter of the callee, to:
// Deep(W{v: x}, n) inside Deep<T> passes T to T wrapped in W. The
// same rule as for types applies: a growing edge on a cycle would
// need a copy of the function for each of an endless chain of types.
type callEdge struct {
	from, to *TypeParam
	grows    bool
	pos      token.Pos
	what     string
}

// recordCall adds the edges of a call of a generic function, method or
// type with the given parameters and arguments, for the cycle check at
// the end; what names the callee for the message.
func (c *Checker) recordCall(tparams []*TypeParam, targs []Type, pos token.Pos, what string) {
	for i, arg := range targs {
		walkTypes(arg, func(t Type) {
			if p, ok := t.(*TypeParam); ok {
				c.callEdges = append(c.callEdges, callEdge{p, tparams[i], arg != Type(p), pos, what + "<" + typeList(targs) + ">"})
			}
		})
	}
}

// checkCallCycles reports each call whose argument grows a parameter
// that, through the calls it leads to, comes back to itself.
func (c *Checker) checkCallCycles() {
	edges := make(map[*TypeParam][]paramEdge, len(c.callEdges))
	for _, e := range c.callEdges {
		edges[e.from] = append(edges[e.from], paramEdge{e.to, e.grows, nil})
	}

	for _, e := range c.callEdges {
		if e.grows && reaches(e.to, e.from, edges) {
			c.errorf(e.pos, "the call "+e.what+" grows the type parameter "+e.from.obj.name+" each time around: it would need instances without end")
		}
	}
}

// checkTypeCycles reports the declarations among decls that refer to
// themselves with a growing argument or contain themselves by value.
// A generic declaration refused for growing is left unresolved, so
// that nothing expands its instances.
func (c *Checker) checkTypeCycles(decls []*Named) {
	edges := make(map[*TypeParam][]paramEdge)
	for _, d := range decls {
		if d.tparams != nil {
			paramEdges(d.underlying, d.tparams, edges)
		}
	}

	for _, d := range decls {
		if inst := growingInstance(d, edges); inst != nil {
			c.errorf(d.obj.pos, d.String()+" refers to itself as "+inst.String()+", whose arguments grow with each instance: it would need instances without end")
			d.ready = false
		}
	}

	state := make(map[*Named]int8) // 1 while being walked, 2 when done
	reported := make(map[*Named]bool)
	for _, d := range decls {
		if d.tparams == nil || d.ready {
			c.containsSelf(d, state, reported)
		}
	}
}

// paramEdges adds an edge for every type parameter in tparams that t
// passes, as part of an argument, to a generic type.
func paramEdges(t Type, tparams []*TypeParam, edges map[*TypeParam][]paramEdge) {
	walkTypes(t, func(t Type) {
		n, ok := t.(*Named)
		if !ok || n.orig == nil {
			return
		}

		for i, arg := range n.targs {
			for _, p := range tparams {
				if mentions(arg, p) {
					edges[p] = append(edges[p], paramEdge{n.orig.tparams[i], arg != Type(p), n})
				}
			}
		}
	})
}

// growingInstance returns the instance through which one of d's
// parameters reaches itself wrapped, or nil.
func growingInstance(d *Named, edges map[*TypeParam][]paramEdge) *Named {
	for _, p := range d.tparams {
		for _, e := range edges[p] {
			if e.grows && reaches(e.to, p, edges) {
				return e.inst
			}
		}
	}

	return nil
}

// reaches reports whether the parameter from is passed, through any
// chain of instances, to the parameter to.
func reaches(from, to *TypeParam, edges map[*TypeParam][]paramEdge) bool {
	seen := map[*TypeParam]bool{from: true}
	work := []*TypeParam{from}
	for len(work) > 0 {
		p := work[len(work)-1]
		work = work[:len(work)-1]
		if p == to {
			return true
		}

		for _, e := range edges[p] {
			if !seen[e.to] {
				seen[e.to] = true
				work = append(work, e.to)
			}
		}
	}

	return false
}

// mentions reports whether t mentions the type parameter p.
func mentions(t Type, p *TypeParam) bool {
	found := false
	walkTypes(t, func(t Type) {
		found = found || t == Type(p)
	})

	return found
}

// walkTypes calls f on t and on every type written inside it, down to
// named types, whose arguments it walks but whose definitions it does
// not enter. A generic declaration named inside its own definition
// stands for itself with its own parameters, which are its arguments.
func walkTypes(t Type, f func(Type)) {
	f(t)
	switch t := t.(type) {
	case *Named:
		for _, a := range typeArgs(t) {
			walkTypes(a, f)
		}
	case *Pointer:
		walkTypes(t.elem, f)
	case *Ref:
		walkTypes(t.elem, f)
	case *Option:
		walkTypes(t.elem, f)
	case *Result:
		walkTypes(t.val, f)
		walkTypes(t.err, f)
	case *Slice:
		walkTypes(t.elem, f)
	case *Array:
		walkTypes(t.elem, f)
	case *Map:
		walkTypes(t.key, f)
		walkTypes(t.value, f)
	case *Chan:
		walkTypes(t.elem, f)
	case *Tuple:
		for _, e := range t.elems {
			walkTypes(e, f)
		}
	case *Signature:
		for _, p := range t.params {
			walkTypes(p.typ, f)
		}

		walkTypes(t.results, f)
	case *Struct:
		for _, v := range t.fields {
			walkTypes(v.typ, f)
		}
	case *Enum:
		for _, v := range t.variants {
			for _, fv := range v.fields {
				walkTypes(fv.typ, f)
			}
		}
	}
}

// containsSelf walks the named types n holds by value, depth first,
// and reports each declaration found again while it is being walked.
// An instance is walked through its expanded definition, so that
// struct W { b Box<W> } is found when Box holds its argument by value.
func (c *Checker) containsSelf(n *Named, state map[*Named]int8, reported map[*Named]bool) {
	switch state[n] {
	case 1:
		if d := n.Origin(); !reported[d] {
			reported[d] = true
			c.errorf(d.obj.pos, "invalid recursive type "+d.obj.name+": it contains itself; hold it through a pointer, *"+d.String())
		}

		return
	case 2:
		return
	}

	state[n] = 1
	if u := n.Underlying(); u != nil {
		byValue(u, func(m *Named) { c.containsSelf(m, state, reported) })
	}

	state[n] = 2
}

// byValue calls f on every named type t holds by value: its fields,
// variants, elements and options, but not what a pointer, slice, map,
// channel, borrow or function holds.
func byValue(t Type, f func(*Named)) {
	switch t := t.(type) {
	case *Named:
		f(t)
	case *Option:
		byValue(t.elem, f)
	case *Result:
		byValue(t.val, f)
		byValue(t.err, f)
	case *Array:
		byValue(t.elem, f)
	case *Tuple:
		for _, e := range t.elems {
			byValue(e, f)
		}
	case *Struct:
		for _, v := range t.fields {
			byValue(v.typ, f)
		}
	case *Enum:
		for _, v := range t.variants {
			for _, fv := range v.fields {
				byValue(fv.typ, f)
			}
		}
	}
}
