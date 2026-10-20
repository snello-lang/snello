// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package types declares the data types of Teak and the checker that
// assigns one to every expression. It is modeled on go/types: a Type
// interface with one struct per kind of type, objects for the named
// entities of a program, and scopes that map names to objects.
//
// This file holds the types. Compared with go/types, Teak adds the
// owned pointer *T, the borrows ref T and mut T, the optional T?, the
// built-in Result<T, E>, tuples, and enums, and drops interfaces, whose
// place traits take (trait.go).
package types

import (
	"strconv"
	"strings"
)

// BasicKind identifies a predeclared basic type.
type BasicKind int

const (
	Invalid BasicKind = iota // the type of an expression with an error

	Bool
	String
	Error

	Int8
	Int16
	Int32
	Int64
	Int

	Uint8
	Uint16
	Uint32
	Uint64
	Uint

	Float32
	Float64

	// Any and Never are internal: not names in the universe. Any is the
	// parameter type of the printing functions and accepts every value;
	// Never is the type of an expression that does not return, such as
	// panic("x") or a ?? else that returns, and converts to any type.
	Any
	Never

	// Local is the one type that is not Send, written only by the
	// standard library, as a field that holds nothing: a type with such
	// a field stays in the task that made it, as rc.Rc does (send.go).
	Local

	// Aliases: byte is u8 and rune is i32, as their names are only
	// spellings. Identical compares kinds, so the alias and its type
	// are the same type.
	Byte = Uint8
	Rune = Int32
)

// BasicInfo is a set of flags describing the properties of a basic type.
type BasicInfo int

const (
	IsBoolean BasicInfo = 1 << iota
	IsInteger
	IsUnsigned
	IsFloat
	IsString

	IsNumeric = IsInteger | IsFloat
)

// copyState caches whether a named type is Copy; see Named.isCopy.
type copyState int8

const (
	copyUnknown copyState = iota
	copyNo
	copyYes
)

// ChanDir is the direction of a channel type.
type ChanDir int

const (
	SendRecv ChanDir = iota // chan T
	SendOnly                // chan<- T
	RecvOnly                // <-chan T
)

// Typ holds the one instance of each basic type, indexed by kind.
var Typ = [...]*Basic{
	Invalid: {Invalid, 0, "invalid type"},
	Bool:    {Bool, IsBoolean, "bool"},
	String:  {String, IsString, "string"},
	Error:   {Error, 0, "error"},
	Int8:    {Int8, IsInteger, "i8"},
	Int16:   {Int16, IsInteger, "i16"},
	Int32:   {Int32, IsInteger, "i32"},
	Int64:   {Int64, IsInteger, "i64"},
	Int:     {Int, IsInteger, "int"},
	Uint8:   {Uint8, IsInteger | IsUnsigned, "u8"},
	Uint16:  {Uint16, IsInteger | IsUnsigned, "u16"},
	Uint32:  {Uint32, IsInteger | IsUnsigned, "u32"},
	Uint64:  {Uint64, IsInteger | IsUnsigned, "u64"},
	Uint:    {Uint, IsInteger | IsUnsigned, "uint"},
	Float32: {Float32, IsFloat, "f32"},
	Float64: {Float64, IsFloat, "f64"},
	Any:     {Any, 0, "any"},
	Never:   {Never, 0, "never"},
	Local:   {Local, 0, "local"},
}

// aliases are the basic types that share a kind with an entry of Typ
// under another name.
var aliases = [...]*Basic{
	{Byte, IsInteger | IsUnsigned, "byte"},
	{Rune, IsInteger, "rune"},
}

// Unit is the empty tuple (), the type of a function with no results
// and the value inside Ok(()).
var Unit = &Tuple{}

// A Type is a Teak type.
type Type interface {
	// Underlying returns the underlying type of a type: the type itself
	// for every kind but Named, as in go/types.
	Underlying() Type

	// String returns the type as it is written in a Teak program.
	String() string
}

// A Basic is a predeclared basic type: bool, string, error, or a number.
type Basic struct {
	kind BasicKind
	info BasicInfo
	name string
}

// A Pointer is an owned heap pointer *T.
type Pointer struct {
	elem Type
}

// A Ref is a borrow: ref T, or mut T when Mut is set.
type Ref struct {
	elem Type
	mut  bool
}

// An Option is the optional type T?, an enum with the variants Some(T)
// and None that the language knows.
type Option struct {
	elem Type
}

// A Result is the type Result<T, E>, an enum with the variants Ok(T)
// and Err(E) that the language knows.
type Result struct {
	val Type
	err Type
}

// A Slice is the type []T.
type Slice struct {
	elem Type
}

// An Array is the type [N]T.
type Array struct {
	len  int64
	elem Type
}

// A Map is the type map[K]V.
type Map struct {
	key   Type
	value Type
}

// A Chan is a channel type.
type Chan struct {
	dir  ChanDir
	elem Type
}

// A Tuple is a list of types: the results of a function with several,
// or the type of a tuple value such as (n, right). The empty tuple is
// Unit.
type Tuple struct {
	elems []Type
}

// A Signature is a function type. Each parameter's type says how it is
// passed: a *Ref for ref and mut, any other type for a move or copy.
type Signature struct {
	recv     *Var // nil for a function
	params   []*Var
	results  Type // Unit, a single type, or a *Tuple of several
	variadic bool // the last parameter takes any number of arguments
	tparams  []*TypeParam
}

// A Struct is an unnamed struct type. Most structs are the underlying
// type of a Named; struct{} on a channel is the unnamed case.
type Struct struct {
	fields []*Var
}

// An Enum is the underlying type of an enum declaration.
type Enum struct {
	variants []*Variant
}

// A Named is a type with a name: a struct or enum declaration, or
// `type Name T`. Two Named types are identical only if they are the
// same declaration, or the same instance of a generic one. The
// underlying type is set after creation so that a declaration can
// refer to itself through a pointer. See generic.go for the fields of
// a generic declaration and its instances.
type Named struct {
	obj         *TypeName
	underlying  Type
	impls       []*Trait // the traits after impl; a nil entry stands for one that failed to resolve
	order       []*Trait // the traits consulted for a method, the most specific first (trait.go); super will walk it
	methods     []*Func  // a declaration's own and inherited methods, or the instance methods made so far
	copyState   copyState
	borrowState copyState // whether the type holds a borrow; see Named.holdsBorrow
	sendState   copyState // whether a value may cross a task, and why not; see Named.notSend
	sendWhy     string

	tparams   []*TypeParam // a generic declaration's parameters
	instances []*Named     // a generic declaration's instances, one per argument list
	ready     bool         // a generic declaration is resolved, so its instances can expand
	orig      *Named       // an instance's declaration
	targs     []Type       // an instance's arguments
}

// NewPointer returns the type *elem.
func NewPointer(elem Type) *Pointer { return &Pointer{elem} }

// NewRef returns the type ref elem, or mut elem.
func NewRef(elem Type, mut bool) *Ref { return &Ref{elem, mut} }

// NewOption returns the type elem?.
func NewOption(elem Type) *Option { return &Option{elem} }

// NewResult returns the type Result<val, err>.
func NewResult(val, err Type) *Result { return &Result{val, err} }

// NewSlice returns the type []elem.
func NewSlice(elem Type) *Slice { return &Slice{elem} }

// NewArray returns the type [len]elem.
func NewArray(elem Type, len int64) *Array { return &Array{len, elem} }

// NewMap returns the type map[key]value.
func NewMap(key, value Type) *Map { return &Map{key, value} }

// NewChan returns a channel type.
func NewChan(dir ChanDir, elem Type) *Chan { return &Chan{dir, elem} }

// NewTuple returns the tuple of the given types; with none it returns Unit.
func NewTuple(elems ...Type) *Tuple {
	if len(elems) == 0 {
		return Unit
	}

	return &Tuple{elems}
}

// NewSignature returns a function type. results is Unit for none, the
// type itself for one, and a *Tuple for several.
func NewSignature(recv *Var, params []*Var, results Type) *Signature {
	if results == nil {
		results = Unit
	}

	return &Signature{recv, params, results, false, nil}
}

// NewStruct returns a struct type with the given fields.
func NewStruct(fields []*Var) *Struct { return &Struct{fields} }

// NewEnum returns an enum type with the given variants.
func NewEnum(variants []*Variant) *Enum { return &Enum{variants} }

// NewNamed returns a named type for obj with the given underlying type,
// which may be nil until SetUnderlying is called, implementing the
// given traits, and sets obj's type to it if obj has none yet.
func NewNamed(obj *TypeName, underlying Type, impls []*Trait) *Named {
	t := &Named{obj: obj, underlying: underlying, impls: impls}
	if obj.typ == nil {
		obj.typ = t
	}

	return t
}

// Kind returns the kind of a basic type.
func (b *Basic) Kind() BasicKind { return b.kind }

// Info returns the properties of a basic type.
func (b *Basic) Info() BasicInfo { return b.info }

// Name returns the name of a basic type.
func (b *Basic) Name() string { return b.name }

// Elem returns the pointed-to type.
func (p *Pointer) Elem() Type { return p.elem }

// Elem returns the borrowed type.
func (r *Ref) Elem() Type { return r.elem }

// Mut reports whether the borrow is mut rather than ref.
func (r *Ref) Mut() bool { return r.mut }

// Elem returns the type inside the option.
func (o *Option) Elem() Type { return o.elem }

// Val returns the type inside Ok.
func (r *Result) Val() Type { return r.val }

// Err returns the type inside Err.
func (r *Result) Err() Type { return r.err }

// Elem returns the element type.
func (s *Slice) Elem() Type { return s.elem }

// Len returns the array length.
func (a *Array) Len() int64 { return a.len }

// Elem returns the element type.
func (a *Array) Elem() Type { return a.elem }

// Key returns the key type.
func (m *Map) Key() Type { return m.key }

// Value returns the value type.
func (m *Map) Value() Type { return m.value }

// Dir returns the channel direction.
func (c *Chan) Dir() ChanDir { return c.dir }

// Elem returns the element type.
func (c *Chan) Elem() Type { return c.elem }

// Len returns the number of elements.
func (t *Tuple) Len() int { return len(t.elems) }

// At returns the i'th element type.
func (t *Tuple) At(i int) Type { return t.elems[i] }

// Recv returns the receiver, or nil for a function.
func (s *Signature) Recv() *Var { return s.recv }

// Params returns the parameters.
func (s *Signature) Params() []*Var { return s.params }

// Results returns the result type: Unit, one type, or a *Tuple.
func (s *Signature) Results() Type { return s.results }

// Variadic reports whether the last parameter takes any number of
// arguments, as the printing functions do.
func (s *Signature) Variadic() bool { return s.variadic }

// TypeParams returns a generic function's type parameters, or nil.
func (s *Signature) TypeParams() []*TypeParam { return s.tparams }

// NumFields returns the number of fields.
func (s *Struct) NumFields() int { return len(s.fields) }

// Field returns the i'th field.
func (s *Struct) Field(i int) *Var { return s.fields[i] }

// NumVariants returns the number of variants.
func (e *Enum) NumVariants() int { return len(e.variants) }

// Variant returns the i'th variant.
func (e *Enum) Variant(i int) *Variant { return e.variants[i] }

// Obj returns the type name of a named type.
func (n *Named) Obj() *TypeName { return n.obj }

// SetUnderlying sets the underlying type of a named type declared
// before its definition was known.
func (n *Named) SetUnderlying(t Type) { n.underlying = t }

// AddMethod attaches a method declared on the type and returns nil, or
// returns the method already declared under that name.
func (n *Named) AddMethod(f *Func) *Func {
	if old := n.Method(f.name); old != nil {
		return old
	}

	n.methods = append(n.methods, f)
	return nil
}

// Method returns the method with the given name, or nil. An instance's
// method has the declaration's signature with the arguments in place
// of the parameters.
func (n *Named) Method(name string) *Func {
	if n.orig != nil {
		return n.instanceMethod(name)
	}

	for _, m := range n.methods {
		if m.name == name {
			return m
		}
	}

	return nil
}

// Implements reports whether the declaration listed trait after impl,
// or a trait refining it; an instance implements what its declaration
// does.
func (n *Named) Implements(trait *Trait) bool {
	for _, t := range n.Origin().impls {
		if t != nil && t.Refines(trait) {
			return true
		}
	}

	return false
}

func (b *Basic) Underlying() Type     { return b }
func (p *Pointer) Underlying() Type   { return p }
func (r *Ref) Underlying() Type       { return r }
func (o *Option) Underlying() Type    { return o }
func (r *Result) Underlying() Type    { return r }
func (s *Slice) Underlying() Type     { return s }
func (a *Array) Underlying() Type     { return a }
func (m *Map) Underlying() Type       { return m }
func (c *Chan) Underlying() Type      { return c }
func (t *Tuple) Underlying() Type     { return t }
func (s *Signature) Underlying() Type { return s }
func (s *Struct) Underlying() Type    { return s }
func (e *Enum) Underlying() Type      { return e }
func (n *Named) Underlying() Type     { n.expand(); return n.underlying }

func (b *Basic) String() string     { return b.name }
func (p *Pointer) String() string   { return "*" + optionParens(p.elem) }
func (r *Ref) String() string       { return refString(r) }
func (o *Option) String() string    { return optionString(o) }
func (r *Result) String() string    { return "Result<" + r.val.String() + ", " + r.err.String() + ">" }
func (s *Slice) String() string     { return "[]" + s.elem.String() }
func (a *Array) String() string     { return "[" + strconv.FormatInt(a.len, 10) + "]" + a.elem.String() }
func (m *Map) String() string       { return "map[" + m.key.String() + "]" + m.value.String() }
func (c *Chan) String() string      { return chanString(c) }
func (t *Tuple) String() string     { return "(" + typeList(t.elems) + ")" }
func (s *Signature) String() string { return signatureString(s) }
func (s *Struct) String() string    { return structString(s) }
func (e *Enum) String() string      { return enumString(e) }
func (n *Named) String() string     { return namedString(n) }

// namedString writes a named type, with its arguments when it is an
// instance and its parameters when it is a generic declaration:
// List<int>, List<T>.
func namedString(n *Named) string {
	name := n.obj.name
	if p := n.obj.pkg; p != nil && p.path != "" {
		name = p.name + "." + name // a library's type reads as the program writes it
	}

	switch {
	case n.targs != nil:
		return name + "<" + typeList(n.targs) + ">"
	case n.tparams != nil:
		return name + "<" + typeList(paramTypes(n.tparams)) + ">"
	}

	return name
}

func refString(r *Ref) string {
	if r.mut {
		return "mut " + optionParens(r.elem)
	}

	return "ref " + optionParens(r.elem)
}

// optionParens writes t, parenthesized when it is an option, since ?
// binds outside a pointer or borrow: *T? is an optional pointer, and a
// pointer to an optional is written *(T?).
func optionParens(t Type) string {
	if _, ok := t.(*Option); ok {
		return "(" + t.String() + ")"
	}

	return t.String()
}

// optionString writes elem? with the parentheses the grammar needs: ?
// binds outside a pointer or borrow and inside a container, so a
// container, map, channel or function inside an option is written
// ([]T)? (see _docs/design/00-syntax-conventions.md).
func optionString(o *Option) string {
	switch o.elem.(type) {
	case *Slice, *Array, *Map, *Chan, *Signature:
		return "(" + o.elem.String() + ")?"
	}

	return o.elem.String() + "?"
}

func chanString(c *Chan) string {
	switch c.dir {
	case SendOnly:
		return "chan<- " + c.elem.String()
	case RecvOnly:
		return "<-chan " + c.elem.String()
	}

	return "chan " + c.elem.String()
}

func signatureString(s *Signature) string {
	var b strings.Builder
	b.WriteString("func(")
	for i, p := range s.params {
		if i > 0 {
			b.WriteString(", ")
		}

		if s.variadic && i == len(s.params)-1 {
			b.WriteString("...")
		}

		b.WriteString(p.typ.String())
	}

	b.WriteString(")")
	if t, ok := s.results.(*Tuple); !ok || t.Len() > 0 {
		b.WriteString(" ")
		b.WriteString(s.results.String())
	}

	return b.String()
}

func structString(s *Struct) string {
	var b strings.Builder
	b.WriteString("struct{")
	for i, f := range s.fields {
		if i > 0 {
			b.WriteString("; ")
		}

		b.WriteString(f.name)
		b.WriteString(" ")
		b.WriteString(f.typ.String())
	}

	b.WriteString("}")
	return b.String()
}

func enumString(e *Enum) string {
	var b strings.Builder
	b.WriteString("enum{")
	for i, v := range e.variants {
		if i > 0 {
			b.WriteString("; ")
		}

		b.WriteString(v.name)
		if len(v.fields) > 0 {
			b.WriteString("(")
			for j, f := range v.fields {
				if j > 0 {
					b.WriteString(", ")
				}

				b.WriteString(f.typ.String())
			}

			b.WriteString(")")
		}
	}

	b.WriteString("}")
	return b.String()
}

func typeList(ts []Type) string {
	var b strings.Builder
	for i, t := range ts {
		if i > 0 {
			b.WriteString(", ")
		}

		b.WriteString(t.String())
	}

	return b.String()
}
