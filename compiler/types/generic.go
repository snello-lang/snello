// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

// Generic types, after go/types. A declaration with type parameters,
// struct List<T> { items []T }, is a Named holding its parameters; its
// fields and methods are written in terms of them. A use with type
// arguments, List<int>, is an instance: another Named that points back
// at the declaration and holds the arguments. Each argument list is
// instantiated once and the instance kept on the declaration, so two
// uses of List<int> are the same pointer and identity stays a pointer
// comparison. An instance's underlying type and methods are the
// declaration's with every parameter replaced by its argument,
// computed on first use: the declaration may not be resolved yet when
// the instance is created, and a type that refers to itself, *Node<T>,
// would otherwise be expanded forever.

// A TypeParam is a type parameter of a generic declaration, T in
// struct List<T>. Inside the declaration it is an owned type the
// checker knows nothing about but its bounds: a value of it moves,
// unless a Copy bound says it copies, and it has the methods its
// bounds declare.
type TypeParam struct {
	obj     *TypeName
	index   int
	bounds  []*Trait // the traits after the colon; a nil entry stands for one that failed to resolve
	methods []*Func  // the bounds' methods as this parameter sees them, made on first use
	fields  []*Var   // the bounds' fields as this parameter sees them, made on first use
}

// NewTypeParam returns the index'th type parameter of a declaration,
// without bounds yet, and sets obj's type to it.
func NewTypeParam(obj *TypeName, index int) *TypeParam {
	t := &TypeParam{obj: obj, index: index}
	obj.typ = t
	return t
}

// Obj returns the type parameter's name.
func (t *TypeParam) Obj() *TypeName { return t.obj }

// Index returns the parameter's position in its declaration's list.
func (t *TypeParam) Index() int { return t.index }

// Bounds returns the traits the parameter is bounded by.
func (t *TypeParam) Bounds() []*Trait { return t.bounds }

// HasBound reports whether the parameter is bounded by trait, or by a
// trait refining it.
func (t *TypeParam) HasBound(trait *Trait) bool {
	for _, b := range t.bounds {
		if b != nil && b.Refines(trait) {
			return true
		}
	}

	return false
}

func (t *TypeParam) Underlying() Type { return t }
func (t *TypeParam) String() string   { return t.obj.name }

// setBounds gives the parameter the traits after its colon.
func (t *TypeParam) setBounds(bounds []*Trait) { t.bounds = bounds }

// boundMethod returns the method with the given name that a bound of
// the parameter declares, with the parameter in place of the trait's
// Self, made once and kept; or nil.
func (t *TypeParam) boundMethod(name string) *Func {
	for _, m := range t.methods {
		if m.name == name {
			return m
		}
	}

	for _, b := range t.bounds {
		if b == nil {
			continue
		}

		fn, owner := b.lookup(name)
		if fn == nil {
			continue
		}

		m := substFunc(fn, map[*TypeParam]Type{owner.self: t})
		t.methods = append(t.methods, m)
		return m
	}

	return nil
}

// boundField returns the field with the given name that a bound of the
// parameter declares, with the parameter in place of the trait's Self,
// made once and kept; or nil.
func (t *TypeParam) boundField(name string) *Var {
	if f := fieldNamed(t.fields, name); f != nil {
		return f
	}

	for _, b := range t.bounds {
		if b == nil {
			continue
		}

		f, owner := b.field(name)
		if f == nil {
			continue
		}

		v := substVar(f, map[*TypeParam]Type{owner.self: t})
		t.fields = append(t.fields, v)
		return v
	}

	return nil
}

// TypeParams returns the type parameters of a generic declaration, or
// nil for a type that is not generic and for an instance.
func (n *Named) TypeParams() []*TypeParam { return n.tparams }

// Origin returns the generic declaration an instance was made from,
// or n itself.
func (n *Named) Origin() *Named {
	if n.orig != nil {
		return n.orig
	}

	return n
}

// TypeArgs returns the type arguments of an instance, or nil.
func (n *Named) TypeArgs() []Type { return n.targs }

// SetTypeParams gives a declaration its type parameters.
func (n *Named) SetTypeParams(tparams []*TypeParam) { n.tparams = tparams }

// complete marks a generic declaration as resolved, after which its
// instances may expand.
func (n *Named) complete() { n.ready = true }

// Instantiate returns the instance of the generic declaration orig for
// the given type arguments, one per parameter: the same Named for the
// same arguments. The declaration's own parameters as arguments give
// orig itself, which is what List<T> means inside List's methods.
func Instantiate(orig *Named, targs []Type) *Named {
	if isOwnParams(orig, targs) {
		return orig
	}

	for _, inst := range orig.instances {
		if identicalLists(inst.targs, targs) {
			return inst
		}
	}

	inst := &Named{obj: orig.obj, orig: orig, targs: targs}
	orig.instances = append(orig.instances, inst)
	return inst
}

func isOwnParams(orig *Named, targs []Type) bool {
	for i, t := range targs {
		if t != orig.tparams[i] {
			return false
		}
	}

	return true
}

// expand computes an instance's underlying type from its declaration's,
// once the declaration is resolved. An enum gets variants of its own,
// whose parent is the instance, so that a pattern on List<int> binds
// ints.
func (n *Named) expand() {
	if n.underlying != nil || n.orig == nil || !n.orig.ready {
		return
	}

	m := n.substitution()
	if e, ok := n.orig.underlying.(*Enum); ok {
		variants := make([]*Variant, len(e.variants))
		for i, v := range e.variants {
			variants[i] = NewVariant(v.pos, v.name, n, v.index, substVars(v.fields, m))
		}

		n.underlying = NewEnum(variants)
		return
	}

	n.underlying = subst(n.orig.underlying, m)
}

// substitution maps an instance's declaration's parameters to its
// arguments.
func (n *Named) substitution() map[*TypeParam]Type {
	m := make(map[*TypeParam]Type, len(n.targs))
	for i, p := range n.orig.tparams {
		m[p] = n.targs[i]
	}

	return m
}

// instanceMethod returns the method of an instance: the declaration's,
// with the parameters replaced in its signature, made once and kept.
func (n *Named) instanceMethod(name string) *Func {
	for _, m := range n.methods {
		if m.name == name {
			return m
		}
	}

	fn := n.orig.Method(name)
	if fn == nil {
		return nil
	}

	m := substFunc(fn, n.substitution())
	n.methods = append(n.methods, m)
	return m
}

// Subst returns t with every type parameter in m replaced by its
// argument, for a backend writing one copy of a generic declaration
// per instance. A type without parameters comes back as itself.
func Subst(t Type, m map[*TypeParam]Type) Type { return subst(t, m) }

// Subst returns t with the instance's arguments in place of its
// declaration's parameters; t itself for a declaration.
func (n *Named) Subst(t Type) Type {
	if n.orig == nil {
		return t
	}

	return subst(t, n.substitution())
}

func subst(t Type, m map[*TypeParam]Type) Type {
	switch t := t.(type) {
	case *TypeParam:
		if r, ok := m[t]; ok {
			return r
		}
	case *Named:
		switch {
		case t.orig != nil:
			return Instantiate(t.orig, substList(t.targs, m))
		case t.tparams != nil:
			return Instantiate(t, substList(paramTypes(t.tparams), m))
		}
	case *Pointer:
		return NewPointer(subst(t.elem, m))
	case *Ref:
		return NewRef(subst(t.elem, m), t.mut)
	case *Option:
		return NewOption(subst(t.elem, m))
	case *Result:
		return NewResult(subst(t.val, m), subst(t.err, m))
	case *Slice:
		return NewSlice(subst(t.elem, m))
	case *Array:
		return NewArray(subst(t.elem, m), t.len)
	case *Map:
		return NewMap(subst(t.key, m), subst(t.value, m))
	case *Chan:
		return NewChan(t.dir, subst(t.elem, m))
	case *Tuple:
		if t == Unit {
			return t
		}

		return NewTuple(substList(t.elems, m)...)
	case *Signature:
		var recv *Var
		if t.recv != nil {
			recv = substVar(t.recv, m)
		}

		s := NewSignature(recv, substVars(t.params, m), subst(t.results, m))
		s.variadic = t.variadic
		if len(t.tparams) > 0 {
			if _, bound := m[t.tparams[0]]; !bound {
				s.tparams = t.tparams // a generic method of an instance keeps its own parameters
			}
		}

		return s
	case *Struct:
		return NewStruct(substVars(t.fields, m))
	}

	return t
}

// paramTypes returns type parameters as a list of types.
func paramTypes(tparams []*TypeParam) []Type {
	ts := make([]Type, len(tparams))
	for i, p := range tparams {
		ts[i] = p
	}

	return ts
}

func substList(ts []Type, m map[*TypeParam]Type) []Type {
	out := make([]Type, len(ts))
	for i, t := range ts {
		out[i] = subst(t, m)
	}

	return out
}

func substVars(vs []*Var, m map[*TypeParam]Type) []*Var {
	if vs == nil {
		return nil
	}

	out := make([]*Var, len(vs))
	for i, v := range vs {
		out[i] = substVar(v, m)
	}

	return out
}

// substVar copies a variable, a field or a parameter, with its type
// substituted; whether a parameter keeps its argument carries over.
func substVar(v *Var, m map[*TypeParam]Type) *Var {
	return &Var{object: object{v.name, subst(v.typ, m), v.pos, v.pkg}, field: v.field, kept: v.kept}
}

// substFunc copies a method with its signature substituted, remembering
// the method it was made from: an instance's method from the generic
// declaration's, or a type parameter's from its bound's.
func substFunc(fn *Func, m map[*TypeParam]Type) *Func {
	return &Func{object: object{fn.name, subst(fn.typ, m), fn.pos, fn.pkg}, origin: fn, derived: fn.derived, trait: fn.trait}
}
