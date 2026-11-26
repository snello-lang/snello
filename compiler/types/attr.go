// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
)

// The places an attribute is written, and the trait that limits an
// attribute to each: one that lists none of them may be written on any.
const (
	onField = iota // a struct's field or a variant's
	onVariant
	onType
)

var attrPlaces = [...]struct {
	name  string
	trait *Trait
}{
	onField:   {"a field", FieldAttributeTrait},
	onVariant: {"a variant", VariantAttributeTrait},
	onType:    {"a type", TypeAttributeTrait},
}

// An Attribute is one attribute written on a field, a variant or a
// type: its type, which lists Attribute, and the constant expression
// that gives its value, or nil for a struct with no fields written by
// its name, whose value is its only one.
type Attribute struct {
	Type  *Named
	Value ast.Expr
}

// TypeAttr returns the attribute of type t written on the declaration
// of n, and whether one is; an instance of a generic type has its
// declaration's.
func (info *Info) TypeAttr(n *Named, t Type) (Attribute, bool) {
	return info.attr(n.obj, t)
}

// FieldAttr returns the attribute of type t written on the i'th field of
// the struct n, or of the struct its instance's declaration declares.
func (info *Info) FieldAttr(n *Named, i int, t Type) (Attribute, bool) {
	if n.orig != nil {
		n = n.orig
	}

	st, ok := n.underlying.(*Struct)
	if !ok || i >= len(st.fields) {
		return Attribute{}, false
	}

	return info.attr(st.fields[i], t)
}

// VariantAttr returns the attribute of type t written on the variant v,
// or on the variant of the enum's declaration an instance's is.
func (info *Info) VariantAttr(v *Variant, t Type) (Attribute, bool) {
	return info.attr(declaredVariant(v), t)
}

// VariantFieldAttr returns the attribute of type t written on the i'th
// field of the variant v.
func (info *Info) VariantFieldAttr(v *Variant, i int, t Type) (Attribute, bool) {
	v = declaredVariant(v)
	if i >= len(v.fields) {
		return Attribute{}, false
	}

	return info.attr(v.fields[i], t)
}

// declaredVariant returns the variant of the enum's declaration that v,
// a variant of an instance of a generic enum or of the declaration, is.
func declaredVariant(v *Variant) *Variant {
	if v.parent == nil || v.parent.orig == nil {
		return v
	}

	if en, ok := v.parent.orig.underlying.(*Enum); ok && v.index < len(en.variants) {
		return en.variants[v.index]
	}

	return v
}

// attr returns the attribute of type t written on the declaration obj.
func (info *Info) attr(obj Object, t Type) (Attribute, bool) {
	for _, a := range info.Attrs[obj] {
		if Identical(a.Type, t) {
			return a, true
		}
	}

	return Attribute{}, false
}

// checkAttrs checks the attributes written in the file f, declaration
// by declaration in the order written, so that each mistake is reported
// once and in order, and records each for the backends.
func (c *Checker) checkAttrs(f *ast.File) {
	if f.Attrs == nil {
		return
	}

	for i, d := range f.Decls {
		d, ok := d.(*ast.TypeDecl)
		if !ok {
			continue
		}

		tn, _ := c.objs[i].(*TypeName)
		if tn == nil {
			continue
		}

		c.attrsOn(tn, onType, f.Attrs[d])
		switch def := d.Def.(type) {
		case *ast.StructType:
			st, _ := tn.typ.Underlying().(*Struct)
			at := 0 // the struct's field the ast field's first name declares
			for _, af := range def.Fields {
				if len(af.Names) == 0 && len(f.Attrs[af]) > 0 { // an embedded field is no field of its own
					c.errorf(f.Attrs[af][0].At, "an embedded field takes no attribute")
					continue
				}

				for j := range af.Names {
					if st != nil && at+j < len(st.fields) {
						c.attrsOn(st.fields[at+j], onField, f.Attrs[af])
					}
				}

				at += len(af.Names)
			}
		case *ast.EnumType:
			en, _ := tn.typ.Underlying().(*Enum)
			for k, av := range def.Variants {
				if en == nil || k >= len(en.variants) {
					continue
				}

				v := en.variants[k]
				c.attrsOn(v, onVariant, f.Attrs[av])
				for j, param := range av.Fields { // a variant's fields, one each, take attributes as a struct's do
					if j < len(v.fields) {
						c.attrsOn(v.fields[j], onField, f.Attrs[param])
					}
				}
			}
		}
	}
}

// attrsOn checks the attributes attrs written on the declaration obj, a
// type's name, a field or a variant, and records them by it: each a
// constant of a type that lists Attribute, no two of one type. A
// declaration is its own key, so two packages' declarations at the same
// offset of their files never share attributes.
func (c *Checker) attrsOn(obj Object, place int, attrs []*ast.Attr) {
	if len(attrs) == 0 {
		return
	}

	list := make([]Attribute, 0, len(attrs))
	for _, a := range attrs {
		at, ok := c.attribute(a.X)
		if !ok {
			continue
		}

		if !fits(at.Type, place) {
			c.errorf(a.At, at.Type.String()+" is not written on "+attrPlaces[place].name+": it lists "+placesOf(at.Type))
			continue
		}

		for _, seen := range list {
			if Identical(seen.Type, at.Type) {
				c.errorf(a.At, at.Type.String()+" is given twice")
				ok = false
				break
			}
		}

		if ok {
			list = append(list, at)
		}
	}

	if len(list) == 0 {
		return
	}

	if c.info.Attrs == nil {
		c.info.Attrs = make(map[Object][]Attribute)
	}

	c.info.Attrs[obj] = list
}

// attribute checks the expression of one attribute: a constant of a
// declared type that lists Attribute, or the name of such a struct
// with no fields, which stands for its one value.
func (c *Checker) attribute(x ast.Expr) (Attribute, bool) {
	obj, isName := c.nameObject(x)
	switch obj := obj.(type) {
	case *TypeName:
		n, _ := obj.typ.(*Named)
		if n == nil || !isAttribute(n) {
			c.errorf(x.Pos(), exprText(x)+" is not an attribute: it does not list Attribute")
			return Attribute{}, false
		}

		if st, ok := n.Underlying().(*Struct); !ok || len(st.fields) > 0 {
			c.errorf(x.Pos(), n.String()+" is a type: write a value of it, the attribute, as a constant")
			return Attribute{}, false
		}

		return Attribute{Type: n}, true
	case *Const: // a constant's name, a struct constant among them, stands for its value
		if obj.typ == nil || obj.typ == Typ[Invalid] {
			return Attribute{}, false // its value had an error, reported where it is declared
		}

		c.expr(x, nil) // records the name's type, which a backend writes the constant by
		return c.attributeOf(x, obj.typ)
	case nil:
		if isName {
			return Attribute{}, false // reported: the name refers to nothing
		}
	}

	if call, ok := x.(*ast.CallExpr); ok { // @Undef(1): the name is the mistake, not the constant
		if fun, isName := c.nameObject(call.Fun); isName && fun == nil {
			return Attribute{}, false
		}
	}

	op, _ := c.constValue(x, nil, "attribute")
	if op.mode == invalid {
		return Attribute{}, false
	}

	return c.attributeOf(x, op.typ)
}

// attributeOf is the attribute x gives, a constant of type t, when t is
// an attribute's type.
func (c *Checker) attributeOf(x ast.Expr, t Type) (Attribute, bool) {
	n, ok := t.(*Named)
	if !ok || !isAttribute(n) {
		c.errorf(x.Pos(), "a value of type "+t.String()+" is not an attribute: an attribute's type lists Attribute")
		return Attribute{}, false
	}

	return Attribute{Type: n, Value: x}, true
}

// nameObject returns what x refers to when x is a name or a package's
// member, and whether it is one. A name that refers to nothing, and a
// member its package does not export, are reported, once, and give nil;
// every other use is recorded.
func (c *Checker) nameObject(x ast.Expr) (Object, bool) {
	switch x := x.(type) {
	case *ast.Ident:
		obj := c.resolve(x.Name)
		if obj == nil {
			c.errorf(x.Pos(), "undefined: "+x.Name)
			return nil, true
		}

		c.recordUse(x, obj)
		return obj, true
	case *ast.SelectorExpr:
		id, ok := x.X.(*ast.Ident)
		if !ok {
			return nil, false
		}

		pkg, ok := c.resolve(id.Name).(*PkgName)
		if !ok {
			return nil, false
		}

		c.recordUse(id, pkg)
		return c.member(pkg, x.Sel), true
	}

	return nil, false
}

// fits reports whether the attribute type n may be written on place:
// one of the places it lists, or any place when it lists none.
func fits(n *Named, place int) bool {
	limited := false
	for i, p := range attrPlaces {
		if n.Implements(p.trait) {
			if i == place {
				return true
			}

			limited = true
		}
	}

	return !limited
}

// placesOf returns the traits among FieldAttribute, VariantAttribute and
// TypeAttribute that n lists, joined for a message.
func placesOf(n *Named) string {
	var names []string
	for _, p := range attrPlaces {
		if n.Implements(p.trait) {
			names = append(names, p.trait.obj.name)
		}
	}

	return strings.Join(names, " and ")
}

// isAttribute reports whether n lists Attribute.
func isAttribute(n *Named) bool {
	return implements(n, AttributeTrait)
}

// attrCall checks attr(x, A): the attribute of type A written on x, a
// part a loop over fields or variants gives or a type parameter an arm
// struct or enum matched, an A? that is a constant in each copy and
// each instance.
func (c *Checker) attrCall(e *ast.CallExpr) operand {
	if len(e.Args) != 2 {
		c.errorf(e.Lparen, "attr takes what an attribute is written on and the attribute's type: attr(f, json.Name)")
		c.exprs(e.Args)
		return invalidOperand
	}

	obj, isName := c.nameObject(e.Args[1]) // a type, named where a value would be written
	if isName && obj == nil {
		return invalidOperand // reported
	}

	tn, _ := obj.(*TypeName)
	var n *Named
	if tn != nil {
		n, _ = tn.typ.(*Named)
	}

	if n == nil || !isAttribute(n) {
		c.errorf(e.Args[1].Pos(), exprText(e.Args[1])+" is not an attribute's type: attr's second argument is a type that lists Attribute")
		return invalidOperand
	}

	if !c.attrSubject(e.Args[0]) {
		if obj, isName := c.nameObject(e.Args[0]); isName && (obj == nil || obj.Type() == Typ[Invalid]) {
			return invalidOperand // reported, where it is used or declared
		}

		c.errorf(e.Args[0].Pos(), "attr reads the attributes of a part a loop over fields or variants gives, attr(f, ...), or of a type parameter a struct or enum arm matched, attr(T, ...)")
		return invalidOperand
	}

	return operand{value, NewOption(n), nil, nil}
}

// attrSubject reports whether x is what attr reads attributes from: a
// part a loop gives, or a type parameter whose arm took a struct or an
// enum.
func (c *Checker) attrSubject(x ast.Expr) bool {
	if c.partNamed(x) != nil {
		return true
	}

	id, ok := x.(*ast.Ident)
	if !ok {
		return false
	}

	_, obj := c.scope.LookupParent(id.Name)
	tn, ok := obj.(*TypeName)
	if !ok {
		return false
	}

	tp, ok := tn.typ.(*TypeParam)
	if !ok || c.kinds[c.armParam(tp)] == "" {
		return false
	}

	c.recordUse(id, tn)
	return true
}
