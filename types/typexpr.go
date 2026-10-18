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
		return NewPointer(c.typ(e.Elem))
	case *ast.OptionType:
		return NewOption(c.typ(e.Elem))
	case *ast.RefType:
		return NewRef(c.typ(e.Elem), e.Mut)
	case *ast.SliceType:
		return NewSlice(c.typ(e.Elem))
	case *ast.ArrayType:
		return NewArray(c.typ(e.Elem), c.arrayLen(e.Len))
	case *ast.MapType:
		return NewMap(c.typ(e.Key), c.typ(e.Value))
	case *ast.ChanType:
		return NewChan(ChanDir(e.Dir), c.typ(e.Elem))
	case *ast.FuncType:
		return NewSignature(nil, c.params(e.Params), c.results(e.Results))
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
		c.errorf(e.Pos(), "Self is only defined inside a trait")
	}

	return Typ[Invalid]
}

// namedType resolves a name to a type. Result<T, E> is the one name
// written with type arguments in core Teak, and the language knows it.
// A qualified name such as io.Error belongs to the library, which
// stage 1 maps in the emitter; until then it is an invalid type and no
// error, so the core programs check.
func (c *Checker) namedType(e *ast.NamedType) Type {
	if e.Pkg == nil && e.Name.Name == "Result" {
		if len(e.Args) != 2 {
			c.errorf(e.Pos(), "Result takes two type arguments: Result<T, E>")
			return Typ[Invalid]
		}

		return NewResult(c.typ(e.Args[0]), c.typ(e.Args[1]))
	}

	if len(e.Args) > 0 {
		c.errorf(e.Pos(), "generic types are not part of core Teak")
		return Typ[Invalid]
	}

	if e.Pkg != nil {
		c.lookup(e.Pkg)
		return Typ[Invalid]
	}

	obj := c.lookup(e.Name)
	if obj == nil {
		return Typ[Invalid]
	}

	tn, ok := obj.(*TypeName)
	if !ok {
		c.errorf(e.Pos(), e.Name.Name+" is not a type")
		return Typ[Invalid]
	}

	return tn.typ
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
func (c *Checker) params(ps []*ast.Param) []*Var {
	if len(ps) == 0 {
		return nil
	}

	vars := make([]*Var, len(ps))
	for i, p := range ps {
		t := c.typ(p.Type)
		name := ""
		if p.Name != nil {
			name = p.Name.Name
		}

		vars[i] = NewVar(p.Pos(), name, t)
	}

	return vars
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
		fields := c.params(v.Fields)
		for j, f := range fields {
			f.field = true
			if v.Fields[j].Name != nil {
				c.recordDef(v.Fields[j].Name, f)
			}
		}

		variants[i].fields = fields
	}
}
