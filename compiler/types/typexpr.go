// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strconv"

	"github.com/snello-lang/snello/compiler/ast"
)

// typ turns a type expression into a Type, resolving every name in it.
// An error yields Typ[Invalid] after one report, so the use of a bad
// type is never reported twice.
func (c *Checker) typ(e ast.Type) Type {
	switch e := e.(type) {
	case *ast.NamedType:
		return c.namedType(e)
	case *ast.PointerType:
		if elem := c.typ(e.Elem); c.storable(elem, "a pointer", e.Pos()) {
			return NewPointer(elem)
		}
	case *ast.OptionType:
		return NewOption(c.typ(e.Elem))
	case *ast.RefType:
		elem := c.typ(e.Elem)
		if e.Mut && isBorrow(elem) { // a ref of a borrow is a borrow of its target (NewRef); a mut of one would widen a ref, or give two mut borrows of one value
			c.errorf(e.Pos(), "cannot write mut of a borrow type: a borrow is not borrowed mut; write mut of what it borrows, or ref of it, which is the borrow itself")
			return Typ[Invalid]
		}

		return NewRef(elem, e.Mut)
	case *ast.SliceType:
		if elem := c.typ(e.Elem); c.pkg.std || c.storable(elem, "a slice", e.Pos()) {
			return NewSlice(elem) // a library may write the slice of borrows a string's Fields yields, which the iterator trait will replace
		}
	case *ast.ArrayType:
		if elem := c.typ(e.Elem); c.storable(elem, "an array", e.Pos()) {
			return NewArray(elem, c.arrayLen(e.Len))
		}
	case *ast.MapType:
		key, value := c.typ(e.Key), c.typ(e.Value)
		if c.storable(key, "a map", e.Pos()) && c.storable(value, "a map", e.Pos()) && c.keyable(key, e.Key.Pos()) {
			return NewMap(key, value)
		}
	case *ast.ChanType:
		if elem := c.typ(e.Elem); c.storable(elem, "a channel", e.Pos()) {
			return NewChan(ChanDir(e.Dir), elem)
		}
	case *ast.FuncType:
		params, variadic := c.params(e.Params)
		sig := NewSignature(nil, params, c.results(e.Results))
		sig.variadic = variadic
		return sig
	case *ast.TupleType:
		elems := make([]Type, len(e.Elems))
		for i, t := range e.Elems {
			elems[i] = c.typ(t)
		}

		return NewTuple(elems...)
	case *ast.StructType:
		return NewStruct(c.fields(e.Fields, true))
	case *ast.EnumType:
		variants := make([]*Variant, len(e.Variants))
		for i, v := range e.Variants {
			variants[i] = NewVariant(v.Pos(), v.Name.Name, nil, i, nil)
		}

		c.variantFields(e.Variants, variants)
		return NewEnum(variants)
	case *ast.SelfType:
		if c.self != nil {
			return c.self
		}

		c.errorf(e.Pos(), "Self is only defined inside a trait")
	}

	return Typ[Invalid]
}

// languageResult reports whether e names the language's own Result,
// which no program has declared one of its own in place of.
func (c *Checker) languageResult(e *ast.NamedType) bool {
	if e.Pkg != nil || e.Name.Name != "Result" {
		return false
	}

	_, declared := c.scope.LookupParent("Result")
	return declared == nil
}

// namedType resolves a name to a type. Result<T, E> is the one name
// written with type arguments that the language itself knows, unless
// the program declares a Result of its own, which shadows it as any
// declaration shadows a predeclared name. A qualified name, io.Error,
// is looked up in the imported package.
func (c *Checker) namedType(e *ast.NamedType) Type {
	if c.languageResult(e) {
		if len(e.Args) != 2 {
			c.errorf(e.Pos(), "Result takes two type arguments: Result<T, E>")
			return Typ[Invalid]
		}

		return NewResult(c.typ(e.Args[0]), c.typ(e.Args[1]))
	}

	var obj Object
	if e.Pkg != nil {
		if p := c.partNamed(e.Pkg); p != nil && e.Name.Name == "type" && !p.variant {
			return p.typ // f.type, the type of the field f a loop gives
		}

		obj = c.qualified(e.Pkg, e.Name)
	} else {
		obj = c.lookup(e.Name)
	}

	if obj == nil {
		return Typ[Invalid]
	}

	tn, ok := obj.(*TypeName)
	if !ok {
		c.errorf(e.Pos(), e.Name.Name+" is not a type")
		return Typ[Invalid]
	}

	if tn.typ == Typ[Any] {
		c.errorf(e.Pos(), "any is only the type of a variadic parameter, ...ref any; a value of any type is a trait value")
		return Typ[Invalid]
	}

	if tn.typ == Typ[Local] && !c.pkg.std {
		c.errorf(e.Pos(), "local is a field of the standard library's types that stay in their task; a program does not write it")
		return Typ[Invalid]
	}

	if tr, ok := tn.typ.(*Trait); ok && tr.tparams != nil {
		return c.traitValueType(tr, e)
	}

	named, _ := tn.typ.(*Named)
	if named == nil || named.tparams == nil {
		if len(e.Args) > 0 {
			c.errorf(e.Pos(), e.Name.Name+" is not generic: write "+e.Name.Name)
			return Typ[Invalid]
		}

		return c.refine(tn.typ)
	}

	return c.instance(e, named)
}

// traitValueType resolves an instance of a generic trait written as a
// type, Iterator<int>: the type of a trait value of that instance. Its
// arguments cannot hold a borrow, which the value would hide.
func (c *Checker) traitValueType(tr *Trait, e *ast.NamedType) Type {
	if len(e.Args) == 0 {
		c.errorf(e.Pos(), tr.obj.name+" is a generic trait: as a type it takes its arguments, "+tr.obj.name+"<...>")
		return Typ[Invalid]
	}

	inst := c.traitArgs(tr, e)
	if inst == nil {
		return Typ[Invalid]
	}

	for i, a := range inst.targs {
		if holdsBorrow(a) {
			c.errorf(e.Args[i].Pos(), "a trait value cannot hold a borrow: "+inst.String()+" would hide what "+a.String()+" borrows, which a bound may take instead")
			return Typ[Invalid]
		}
	}

	return inst
}

// instance resolves the arguments of a generic type's name, List<int>,
// one per parameter, and returns the instance. An argument must
// implement the bounds of its parameter, and one that holds a borrow
// makes an instance that holds it, which keeps it in no owned data the
// checker does not follow (ownedParts).
func (c *Checker) instance(e *ast.NamedType, named *Named) Type {
	if len(e.Args) != len(named.tparams) {
		c.errorf(e.Pos(), e.Name.Name+" takes "+plural(len(named.tparams), "type argument")+": "+named.String())
		for _, arg := range e.Args {
			c.typ(arg)
		}

		return Typ[Invalid]
	}

	targs := make([]Type, len(e.Args))
	for i, arg := range e.Args {
		targs[i] = c.typ(arg)
		if targs[i] == Typ[Invalid] {
			return Typ[Invalid]
		}
	}

	if !c.satisfies(named.tparams, targs, e.Pos(), e.Name.Name) || named.refused { // a refused declaration was reported where it is declared
		return Typ[Invalid]
	}

	inst := Instantiate(named, targs)
	c.ownedParts(inst, e.Pos())
	return inst
}

// plural writes a count of things: 1 type argument, 2 type arguments.
func plural(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}

	return strconv.Itoa(n) + " " + thing + "s"
}

// params turns parameter declarations into variables, and reports
// whether the last is variadic. A function body declares them in its
// scope later (funcBody); a function type never does.
func (c *Checker) params(ps []*ast.Param) (vars []*Var, variadic bool) {
	if len(ps) == 0 {
		return nil, false
	}

	vars = make([]*Var, len(ps))
	for i, p := range ps {
		var t Type
		if p.Dots.IsValid() {
			t = c.variadicType(p.Type)
			variadic = t != Typ[Invalid]
		} else {
			t = c.typ(p.Type)
		}

		name := ""
		if p.Name != nil {
			name = p.Name.Name
		}

		vars[i] = NewVar(p.Pos(), name, t)
		vars[i].lent = p.Dots.IsValid() && isBorrow(t)
	}

	return vars, variadic
}

// variadicType resolves the type of a variadic parameter, as the
// function's body sees it: ...T is an owned []T, the arguments moved
// into it, and ...ref T a ref []T, the arguments lent for the call
// alone. A library's ...ref any, or ...any, is the printing functions'
// any, whose arguments the compiler takes each by its own type. A
// ...mut T would lend each argument mut, which no slice can hold apart,
// and is refused.
func (c *Checker) variadicType(t ast.Type) Type {
	elem, lent := t, false
	if r, ok := t.(*ast.RefType); ok {
		if r.Mut {
			c.errorf(t.Pos(), "a variadic parameter is ...T or ...ref T, not ...mut T: pass a mut slice instead, xs mut []T")
			return Typ[Invalid]
		}

		elem, lent = r.Elem, true
	}

	if n, ok := elem.(*ast.NamedType); ok && n.Pkg == nil && n.Name.Name == "any" && len(n.Args) == 0 && c.resolve("any") == Universe.Lookup("any") {
		if !c.pkg.std {
			c.errorf(t.Pos(), "...ref any is the library's printing functions' alone: a variadic parameter names the type of its arguments, ...T or ...ref T")
			return Typ[Invalid]
		}

		c.recordUse(n.Name, Universe.Lookup("any"))
		return Typ[Any]
	}

	e := c.typ(elem)
	if e == Typ[Invalid] {
		return e
	}

	if !c.storable(e, "a variadic parameter", t.Pos()) {
		return Typ[Invalid]
	}

	if lent {
		return NewRef(NewSlice(e), false)
	}

	return NewSlice(e)
}

// results turns a result list into Unit, one type, or a tuple.
func (c *Checker) results(rs []ast.Type) Type {
	switch len(rs) {
	case 0:
		return Unit
	case 1:
		return c.typ(rs[0])
	}

	elems := make([]Type, len(rs))
	for i, r := range rs {
		elems[i] = c.typ(r)
	}

	return NewTuple(elems...)
}

// fields turns struct or trait fields into variables; a field with
// several names, `x, y f64`, becomes one variable per name. A struct
// field's default, one per name, goes in the package's table of
// defaults, which the variable names by index, and is checked once the
// constants are (checkDefaults); a field of a type that holds a type
// parameter, an embedded one and a trait's take none.
func (c *Checker) fields(fs []*ast.Field, defaults bool) []*Var {
	var vars []*Var
	seen := NewScope(nil)
	for _, f := range fs {
		t := c.typ(f.Type)
		given := f.Defaults != nil && c.defaultsFit(f, t, defaults)

		for i, name := range f.Names {
			v := NewField(name.Pos(), name.Name, t)
			v.pkg = c.pkg // the package that declares the field decides who may name it, whatever type is declared over the struct
			if old := seen.Insert(v); old != nil {
				c.errorf(name.Pos(), "duplicate field "+name.Name)
			}

			if given {
				c.pkg.defaults = append(c.pkg.defaults, f.Defaults.Values[i])
				v.def = int32(len(c.pkg.defaults))
				c.fieldDefaults = append(c.fieldDefaults, pendingDefault{x: f.Defaults.Values[i], typ: t, file: c.fileIndex})
			}

			c.recordDef(name, v)
			vars = append(vars, v)
		}
	}

	return vars
}

// defaultsFit reports whether the defaults written after a field may
// be its defaults, and refuses them when not: one for each name, on a
// struct's field with names, of a type that holds no type parameter.
func (c *Checker) defaultsFit(f *ast.Field, t Type, defaults bool) bool {
	d := f.Defaults
	switch {
	case !defaults:
		c.errorf(d.Assign, "a trait field takes no default: the type that implements the trait gives the field its value")
	case len(f.Names) == 0:
		c.errorf(d.Assign, "an embedded field takes no default")
	case len(d.Values) != len(f.Names):
		c.errorf(d.Assign, "a default for each field: "+plural(len(f.Names), "name")+" but "+plural(len(d.Values), "default"))
	case holdsTypeParam(t):
		c.errorf(d.Assign, "a field of type "+t.String()+" takes no default: no constant has every type a type parameter stands for")
	default:
		return true
	}

	return false
}

// holdsTypeParam reports whether t is a type parameter or holds one.
func holdsTypeParam(t Type) bool {
	held := false
	walkTypes(t, func(t Type) {
		_, ok := t.(*TypeParam)
		held = held || ok
	})

	return held
}

// variantFields resolves the field types of each variant declaration
// into the variant objects, which were created when the names were.
func (c *Checker) variantFields(vs []*ast.Variant, variants []*Variant) {
	for i, v := range vs {
		fields, variadic := c.params(v.Fields)
		if variadic {
			c.errorf(v.Fields[len(v.Fields)-1].Dots, "a variant's field is not variadic: it holds one value, a slice for many, []T")
		}

		for j, f := range fields {
			f.field = true
			if v.Fields[j].Name != nil {
				c.recordDef(v.Fields[j].Name, f)
			}
		}

		variants[i].fields = fields
	}
}
