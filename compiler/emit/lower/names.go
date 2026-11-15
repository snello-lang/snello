// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lower

import (
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/types"
)

// The names of the symbols a backend writes are the same in every
// backend, since the runtime declares some of them and the
// differential test compares the rest: a function, variable or
// constant of the program is in the sn__ space, a library's is
// qualified by its import path in the runtime's sn_ space, and Snello's
// main is sn_main, which the runtime's main calls (ObjName).

// symbolRoom is the room a builder of a symbol takes first, which holds
// nearly every symbol, so that one is built in one allocation and not
// in one per piece as the builder doubles.
const symbolRoom = 64

// reserved holds the identifiers an emitted program must not reuse:
// C's keywords and the libc names the C output refers to. The IR
// output has no such words, but a symbol must be one name in both,
// so the same identifiers are renamed in both.
var reserved = map[string]bool{
	"auto": true, "break": true, "case": true, "char": true, "const": true, "continue": true,
	"default": true, "do": true, "double": true, "else": true, "enum": true, "extern": true,
	"float": true, "for": true, "goto": true, "if": true, "inline": true, "int": true, "long": true,
	"register": true, "restrict": true, "return": true, "short": true, "signed": true, "sizeof": true,
	"static": true, "struct": true, "switch": true, "typedef": true, "union": true, "unsigned": true,
	"void": true, "volatile": true, "while": true, "bool": true, "true": true, "false": true,
	"main": true, "printf": true, "malloc": true, "free": true, "exit": true, "memcpy": true,
	"strlen": true, "NULL": true, "stdout": true, "stderr": true, "size_t": true,
}

// Ident writes a Snello identifier as a symbol: a reserved one, one
// starting with the runtime's prefix, or one written in capitals alone,
// gets a trailing underscore. C's headers define their constants as
// macros in capitals, SIZE_MAX and INT64_MAX in stdint.h and dozens more
// that differ by platform, and a macro would replace a local of its
// name; no list of them is complete, so every name of that form is
// spelled apart, which changes nothing but the C.
func Ident(name string) string {
	if reserved[name] || strings.HasPrefix(name, "sn_") || strings.HasPrefix(name, "SN_") || capitals(name) {
		return name + "_"
	}

	return name
}

// capitals reports whether name has two or more characters and no
// lower-case letter, the form of a C macro.
func capitals(name string) bool {
	if len(name) < 2 {
		return false
	}

	for i := 0; i < len(name); i++ {
		if c := name[i]; c >= 'a' && c <= 'z' {
			return false
		}
	}

	return true
}

// ObjName writes the symbol of a declared function, variable, type or
// constant. One a standard package declares is qualified by the
// package's import path in the runtime's sn_ space, under which the
// runtime defines the functions the package declares without a body;
// one a package of the program's own declares is qualified the same
// way in the snm_ space, which the runtime never uses, so a package
// may have any name. The program's own functions, variables and
// constants are in sn__, the space of the package with no path, as
// Go's linker writes them main.name: a symbol the program named write
// must not be libc's write, which the runtime calls, once link-time
// optimization makes the program and the runtime one unit. The
// program's types keep their names, which no linker sees.
func ObjName(obj types.Object) string {
	if obj.Pkg() == nil { // predeclared: shared by every compilation in the process, so nothing is written to it; its symbol is its name, made without allocating
		return objName(obj)
	}

	switch o := obj.(type) { // a type or a function is written at every use: its symbol is kept on it
	case *types.TypeName:
		if o.Symbol() == "" {
			o.SetSymbol(objName(obj))
		}

		return o.Symbol()
	case *types.Func:
		if o.Symbol() == "" {
			o.SetSymbol(objName(obj))
		}

		return o.Symbol()
	}

	return objName(obj)
}

// objName writes the symbol of obj as ObjName says.
func objName(obj types.Object) string {
	if p := obj.Pkg(); p != nil && p.Path() != "" {
		if p.Standard() {
			return qualified("sn_", p.Path(), obj.Name())
		}

		return qualified("snm_", p.Path(), obj.Name())
	}

	switch obj.(type) {
	case *types.Func, *types.Var, *types.Const:
		if obj.Pkg() != nil {
			return "sn__" + obj.Name()
		}
	}

	return Ident(obj.Name())
}

// PathPrefix writes the start of every symbol of a package of the
// program's own at an import path: snm_, the path with / as __, and _.
// a/b is snm_a__b_, apart from a package a_b. A path may carry a dot or
// a dash no C identifier may, my-lib, and each is written as _;
// CheckPrefixes refuses two paths that would share a prefix this way.
// StdPrefix is the standard library's form, sn_fmt_ and sn_net__http_,
// the names the runtime defines.
func PathPrefix(path string) string {
	return qualified("snm_", path, "")
}

// StdPrefix writes the start of every symbol of the standard package
// at an import path.
func StdPrefix(path string) string {
	return qualified("sn_", path, "")
}

// qualified writes a symbol in the space space of the package at path,
// the path's separators and the characters no C identifier may hold
// written as _, then name, in one block sized for all of it.
func qualified(space, path, name string) string {
	var b strings.Builder
	b.Grow(len(space) + 2*len(path) + 1 + len(name)) // a / is written as two bytes
	b.WriteString(space)
	for i := 0; i < len(path); i++ {
		switch path[i] {
		case '/':
			b.WriteString("__")
		case '.', '-', '~':
			b.WriteByte('_')
		default:
			b.WriteByte(path[i])
		}
	}

	b.WriteByte('_')
	b.WriteString(name)
	return b.String()
}

// FuncName writes the symbol of a declared function. A method may be
// called main; it has no package of its own.
func FuncName(fn *types.Func) string {
	if p := fn.Pkg(); fn.Name() == "main" && p != nil && p.Path() == "" {
		return "sn_main"
	}

	return ObjName(fn)
}

// MethodName writes the symbol of a method: the receiver's type and
// the method's name, under the runtime prefix so it collides with
// nothing the program declares.
func MethodName(recv types.Type, name string) string {
	return "sn_" + Mangle(ValueType(recv)) + "_" + name
}

// NamedName writes the symbol of a declared type: its own, or for an
// instance its name followed by its arguments. The symbol is kept on
// the type, since a backend writes it at every value of the type.
func NamedName(t *types.Named) string {
	if t.Obj().Pkg() == nil { // predeclared, as ObjName says: nothing is written to it
		return withArgs(ObjName(t.Obj()), t.TypeArgs())
	}

	if s := t.Symbol(); s != "" {
		return s
	}

	s := withArgs(ObjName(t.Obj()), t.TypeArgs())
	t.SetSymbol(s)
	return s
}

// TraitName writes the symbol of a trait used as a type as a declared
// type's is written: its name, qualified by its package, so that two
// packages may each declare a Shape, and for an instance of a generic
// trait its arguments after it, so that Iterator<int> and
// Iterator<string> have a table type each.
func TraitName(t *types.Trait) string {
	return withArgs(ObjName(t.Obj()), t.TypeArgs())
}

// withArgs writes name and each of args after it, mangled, separated
// by _: name itself, with nothing allocated, for a type that is not an
// instance of a generic one, and one builder for an instance.
func withArgs(name string, args []types.Type) string {
	if len(args) == 0 {
		return name
	}

	var b strings.Builder
	b.Grow(symbolRoom)
	b.WriteString(name)
	for _, a := range args {
		b.WriteByte('_')
		mangle(&b, a)
	}

	return b.String()
}

// Mangle writes a type as a fragment of a symbol: a declared type's
// symbol, and a type made of others with a word for its kind before
// each of them, in one allocation however deep the type.
func Mangle(t types.Type) string {
	switch t := t.(type) {
	case *types.Basic:
		return types.Typ[t.Kind()].Name()
	case *types.Named:
		return NamedName(t)
	}

	var b strings.Builder
	b.Grow(symbolRoom)
	mangle(&b, t)
	return b.String()
}

// Symbol writes a type's mangled name after prefix, in one allocation:
// the name of a function or a struct type a backend writes for the type.
func Symbol(prefix string, t types.Type) string {
	var b strings.Builder
	b.Grow(symbolRoom)
	b.WriteString(prefix)
	mangle(&b, t)
	return b.String()
}

// mangle writes t as a fragment of a symbol into b.
func mangle(b *strings.Builder, t types.Type) {
	switch t := t.(type) {
	case *types.Basic:
		b.WriteString(types.Typ[t.Kind()].Name())
	case *types.Named:
		b.WriteString(NamedName(t))
	case *types.TypeParam:
		b.WriteString(Ident(t.Obj().Name())) // never written: every type is substituted first
	case *types.Trait:
		b.WriteString(TraitName(t))
	case *types.Ref:
		if t.Mut() {
			b.WriteString("mut_")
		} else {
			b.WriteString("ref_")
		}

		mangle(b, t.Elem())
	case *types.Pointer:
		b.WriteString("ptr_")
		mangle(b, t.Elem())
	case *types.Chan: // the direction is part of the name, since a handle's drop gives up the ends it holds
		switch t.Dir() {
		case types.SendOnly:
			b.WriteString("sendchan_")
		case types.RecvOnly:
			b.WriteString("recvchan_")
		default:
			b.WriteString("chan_")
		}

		mangle(b, t.Elem())
	case *types.Option:
		b.WriteString("opt_")
		mangle(b, t.Elem())
	case *types.Result:
		b.WriteString("res_")
		mangle(b, t.Val())
		b.WriteByte('_')
		mangle(b, t.Err())
	case *types.Slice:
		b.WriteString("slice_")
		mangle(b, t.Elem())
	case *types.Array:
		b.WriteString("array_")
		b.WriteString(strconv.FormatInt(t.Len(), 10))
		b.WriteByte('_')
		mangle(b, t.Elem())
	case *types.Map:
		b.WriteString("map_")
		mangle(b, t.Key())
		b.WriteByte('_')
		mangle(b, t.Value())
	case *types.Signature:
		b.WriteString("fn")
		b.WriteString(strconv.Itoa(len(t.Params())))
		for _, p := range t.Params() {
			b.WriteByte('_')
			mangle(b, p.Type())
		}

		b.WriteByte('_')
		mangle(b, t.Results())
	case *types.Tuple:
		if t == types.Unit {
			b.WriteString("unit")
			return
		}

		b.WriteString("tup")
		b.WriteString(strconv.Itoa(t.Len()))
		for i := 0; i < t.Len(); i++ {
			b.WriteByte('_')
			mangle(b, t.At(i))
		}
	default:
		b.WriteString("unknown")
	}
}

// ValueType strips the borrows and owned pointers in front of a type,
// as the checker does for a field access or a match.
func ValueType(t types.Type) types.Type {
	for {
		switch u := t.(type) {
		case *types.Ref:
			t = u.Elem()
		case *types.Pointer:
			t = u.Elem()
		default:
			return t
		}
	}
}

// IsLocal reports whether t is the marker a library type's field has
// to keep its values in their task, which holds nothing and prints as
// nothing.
func IsLocal(t types.Type) bool {
	b, ok := t.(*types.Basic)
	return ok && b.Kind() == types.Local
}
