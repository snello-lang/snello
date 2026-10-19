// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "github.com/teak-lang/teak/token"

// BuiltinID identifies a predeclared function.
type BuiltinID int

const (
	BuiltinLen BuiltinID = iota
	BuiltinCap
	BuiltinMake
	BuiltinTake
	BuiltinPanic
	BuiltinPrint
	BuiltinMin
	BuiltinMax
)

// An Object is a named entity of a program: a type name, a variable,
// a constant, a function, a variant, a built-in function, or an
// imported package. Modeled on go/types.Object.
type Object interface {
	// Name returns the object's name as written in the program.
	Name() string

	// Type returns the object's type, or nil for a package name.
	Type() Type

	// Pos returns where the object is declared, or token.NoPos for a
	// predeclared one.
	Pos() token.Pos

	// Pkg returns the package whose top-level scope declares the
	// object, or nil for a local, a field, a method or a predeclared
	// one.
	Pkg() *Package

	isObject()
	setPkg(*Package)
}

// object is the part every Object has.
type object struct {
	name string
	typ  Type
	pos  token.Pos
	pkg  *Package
}

// A TypeName names a type: a struct, enum or type declaration, or a
// predeclared type.
type TypeName struct {
	object
}

// A Var is a variable: a local, a parameter, a receiver, or a field.
type Var struct {
	object
	field   bool
	lastUse token.Pos // the last use seen so far, for the borrow pass; an assignment to the whole variable is not one
	kept    bool      // a parameter whose argument the callee keeps: Push stores its element
}

// A Const is a named constant; true and false, and const declarations.
type Const struct {
	object
	value string // the literal's text, typed by context
}

// A Func is a function or a method; its type is a *Signature. A method
// of an instance of a generic type has its declaration's method as its
// origin.
type Func struct {
	object
	origin  *Func
	derived bool   // written by the compiler from the type's fields, for a standard trait the type listed without the method
	trait   *Trait // for a default method a type inherited: the trait, or instance of one, it came from
}

// FromTrait returns the trait a type inherited the default method from,
// an instance of a generic trait with its arguments, or nil for a
// method the type declared itself.
func (f *Func) FromTrait() *Trait { return f.trait }

// Derived reports whether the method was derived from its type's
// fields rather than declared: Eq, Cmp, Hash or Clone of a type that
// listed the trait and wrote no method.
func (f *Func) Derived() bool { return f.derived }

// A Variant is one case of an enum, as an object so that its name
// resolves: Closed, Some(x). Parent is the enum type, or nil for the
// variants of the built-in Option and Result, whose types depend on
// the context they are used in.
type Variant struct {
	object
	parent *Named
	fields []*Var
	index  int
}

// A Builtin is a predeclared function such as len or take. It has no
// type; the checker types each call by its id.
type Builtin struct {
	object
	id BuiltinID
}

// A PkgName is an imported package, the name before the dot: a
// package written in Teak, loaded by the importer, or one of the
// runtime mappings the core library table holds, which has no Package.
// An import the importer could not find is reported once, at the
// import, and its members are then not reported again.
type PkgName struct {
	object
	path    string
	pkg     *Package
	missing bool
}

// NewTypeName returns a type name; its type is set by NewNamed, or is
// the predeclared type.
func NewTypeName(pos token.Pos, name string, typ Type) *TypeName {
	return &TypeName{object{name, typ, pos, nil}}
}

// NewVar returns a local, parameter or receiver.
func NewVar(pos token.Pos, name string, typ Type) *Var {
	return &Var{object: object{name, typ, pos, nil}}
}

// NewField returns a struct or variant field.
func NewField(pos token.Pos, name string, typ Type) *Var {
	return &Var{object: object{name, typ, pos, nil}, field: true}
}

// NewConst returns a named constant with the given literal text.
func NewConst(pos token.Pos, name string, typ Type, value string) *Const {
	return &Const{object{name, typ, pos, nil}, value}
}

// NewFunc returns a function or method.
func NewFunc(pos token.Pos, name string, sig *Signature) *Func {
	return &Func{object: object{name, sig, pos, nil}}
}

// Origin returns the declared method this one was made from by
// substitution, through any number of steps: the generic declaration's
// method behind an instance's, the trait's default behind an inherited
// method; or fn itself.
func (fn *Func) Origin() *Func {
	for fn.origin != nil {
		fn = fn.origin
	}

	return fn
}

// NewVariant returns the index'th variant of parent with the given fields.
func NewVariant(pos token.Pos, name string, parent *Named, index int, fields []*Var) *Variant {
	v := &Variant{object{name, nil, pos, nil}, parent, fields, index}
	if parent != nil { // a nil *Named must not become a non-nil Type
		v.typ = parent
	}

	return v
}

// NewBuiltin returns a predeclared function.
func NewBuiltin(name string, id BuiltinID) *Builtin {
	return &Builtin{object{name, nil, token.NoPos, nil}, id}
}

// NewPkgName returns an imported package name.
func NewPkgName(pos token.Pos, name, path string) *PkgName {
	return &PkgName{object: object{name, nil, pos, nil}, path: path}
}

func (o *object) Name() string   { return o.name }
func (o *object) Type() Type     { return o.typ }
func (o *object) Pos() token.Pos { return o.pos }
func (o *object) isObject()      {}

// IsField reports whether the variable is a struct or variant field.
func (v *Var) IsField() bool { return v.field }

// Value returns the constant's literal text.
func (c *Const) Value() string { return c.value }

// Parent returns the enum the variant belongs to, or nil for Some,
// None, Ok and Err.
func (v *Variant) Parent() *Named { return v.parent }

// Fields returns the variant's fields.
func (v *Variant) Fields() []*Var { return v.fields }

// Index returns the variant's position in its enum.
func (v *Variant) Index() int { return v.index }

// ID returns which built-in function this is.
func (b *Builtin) ID() BuiltinID { return b.id }

// Path returns the import path.
func (p *PkgName) Path() string { return p.path }

// Imported returns the package written in Teak the name stands for,
// or nil for a package of the core library table.
func (p *PkgName) Imported() *Package { return p.pkg }

func (o *object) Pkg() *Package     { return o.pkg }
func (o *object) setPkg(p *Package) { o.pkg = p }
