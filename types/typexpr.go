// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strconv"

	"github.com/teak-lang/teak/ast"
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
		return NewRef(c.typ(e.Elem), e.Mut)
	case *ast.SliceType:
		if elem := c.typ(e.Elem); c.pkg.path != "" || c.storable(elem, "a slice", e.Pos()) {
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
		return NewStruct(c.fields(e.Fields))
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

// namedType resolves a name to a type. Result<T, E> is the one name
// written with type arguments that the language itself knows, unless
// the program declares a Result of its own, which shadows it as any
// declaration shadows a predeclared name. A qualified name, io.Error,
// is looked up in the imported package.
func (c *Checker) namedType(e *ast.NamedType) Type {
	if _, declared := c.scope.LookupParent("Result"); e.Pkg == nil && e.Name.Name == "Result" && declared == nil {
		if len(e.Args) != 2 {
			c.errorf(e.Pos(), "Result takes two type arguments: Result<T, E>")
			return Typ[Invalid]
		}

		return NewResult(c.typ(e.Args[0]), c.typ(e.Args[1]))
	}

	var obj Object
	if e.Pkg != nil {
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

	if tn.typ == Typ[Local] && c.pkg.path == "" {
		c.errorf(e.Pos(), "local is a field of the standard library's types that stay in their task; a program does not write it")
		return Typ[Invalid]
	}

	if tr, ok := tn.typ.(*Trait); ok && tr.tparams != nil {
		c.errorf(e.Pos(), tr.obj.name+" is a generic trait and is not a type yet: bound a type parameter by it, I: "+tr.obj.name+"<...>")
		return Typ[Invalid]
	}

	named, _ := tn.typ.(*Named)
	if named == nil || named.tparams == nil {
		if len(e.Args) > 0 {
			c.errorf(e.Pos(), e.Name.Name+" is not generic: write "+e.Name.Name)
			return Typ[Invalid]
		}

		return tn.typ
	}

	return c.instance(e, named)
}

// instance resolves the arguments of a generic type's name, List<int>,
// one per parameter, and returns the instance. An argument cannot hold
// a borrow, which the instance's fields would then store, and must
// implement the bounds of its parameter.
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
		if targs[i] == Typ[Invalid] || !c.storable(targs[i], "a type argument", arg.Pos()) {
			return Typ[Invalid]
		}
	}

	if !c.satisfies(named.tparams, targs, e.Pos(), e.Name.Name) {
		return Typ[Invalid]
	}

	return Instantiate(named, targs)
}

// plural writes a count of things: 1 type argument, 2 type arguments.
func plural(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}

	return strconv.Itoa(n) + " " + thing + "s"
}

// arrayLen reads an array length, which the grammar limits to an
// integer literal.
func (c *Checker) arrayLen(e ast.Expr) int64 {
	if lit, ok := e.(*ast.BasicLit); ok {
		if n, err := strconv.ParseInt(lit.Value, 0, 64); err == nil && n >= 0 {
			return n
		}
	}

	c.errorf(e.Pos(), "array length must be a non-negative integer literal")
	return 0
}

// params turns parameter declarations into variables. A function body
// declares them in its scope later (funcBody); a function type never
// does.
// params resolves a parameter list and reports whether its last
// parameter is variadic. A variadic parameter is ...ref any, the type
// of fmt's arguments, and only a library package declares one, since
// the compiler writes the function that takes it; a program's own is
// not supported yet.
func (c *Checker) params(ps []*ast.Param) (vars []*Var, variadic bool) {
	if len(ps) == 0 {
		return nil, false
	}

	vars = make([]*Var, len(ps))
	for i, p := range ps {
		var t Type
		switch {
		case !p.Dots.IsValid():
			t = c.typ(p.Type)
		case c.pkg.path == "":
			c.errorf(p.Dots, "a variadic parameter is not supported yet")
			t = Typ[Invalid]
		default:
			t = c.variadicType(p.Type)
			variadic = true
		}

		name := ""
		if p.Name != nil {
			name = p.Name.Name
		}

		vars[i] = NewVar(p.Pos(), name, t)
	}

	return vars, variadic
}

// variadicType resolves the type of a variadic parameter, which is any,
// written ref any or any: the compiler takes each argument by its own
// type. Another type is reported.
func (c *Checker) variadicType(t ast.Type) Type {
	if r, ok := t.(*ast.RefType); ok && !r.Mut {
		t = r.Elem
	}

	if n, ok := t.(*ast.NamedType); ok && n.Pkg == nil && n.Name.Name == "any" && len(n.Args) == 0 {
		c.recordUse(n.Name, Universe.Lookup("any"))
		return Typ[Any]
	}

	c.errorf(t.Pos(), "a variadic parameter is ...ref any; the compiler takes each argument by its type")
	return Typ[Invalid]
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

// fields turns struct fields into variables; a field with several
// names, `x, y f64`, becomes one variable per name.
func (c *Checker) fields(fs []*ast.Field) []*Var {
	var vars []*Var
	seen := NewScope(nil)
	for _, f := range fs {
		t := c.typ(f.Type)
		for _, name := range f.Names {
			v := NewField(name.Pos(), name.Name, t)
			if old := seen.Insert(v); old != nil {
				c.errorf(name.Pos(), "duplicate field "+name.Name)
			}

			c.recordDef(name, v)
			vars = append(vars, v)
		}
	}

	return vars
}

// variantFields resolves the field types of each variant declaration
// into the variant objects, which were created when the names were.
func (c *Checker) variantFields(vs []*ast.Variant, variants []*Variant) {
	for i, v := range vs {
		fields, _ := c.params(v.Fields)
		for j, f := range fields {
			f.field = true
			if v.Fields[j].Name != nil {
				c.recordDef(v.Fields[j].Name, f)
			}
		}

		variants[i].fields = fields
	}
}
