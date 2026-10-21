// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lower

import (
	"strconv"
	"strings"

	"github.com/teak-lang/teak/compiler/types"
)

// The names of the symbols a backend writes are the same in every
// backend, since the runtime declares some of them and the
// differential test compares the rest: a function of the program
// keeps its name, a library's is qualified by its import path in the
// runtime's tk_ space, and Teak's main is tk_main, which the
// runtime's main calls.

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

// Ident writes a Teak identifier as a symbol: a reserved one, or one
// starting with the runtime's prefix, gets a trailing underscore.
func Ident(name string) string {
	if reserved[name] || strings.HasPrefix(name, "tk_") || strings.HasPrefix(name, "TK_") {
		return name + "_"
	}

	return name
}

// ObjName writes the symbol of a declared function, type or constant:
// its own name, or for one a library package declares the name
// qualified by the package's import path, in the runtime's tk_ space,
// which no name of a program reaches.
func ObjName(obj types.Object) string {
	if p := obj.Pkg(); p != nil && p.Path() != "" {
		return "tk_" + strings.ReplaceAll(p.Path(), "/", "__") + "_" + obj.Name() // net/http is net__http, apart from a package net_http
	}

	return Ident(obj.Name())
}

// FuncName writes the symbol of a declared function. A method may be
// called main; it has no package of its own.
func FuncName(fn *types.Func) string {
	if p := fn.Pkg(); fn.Name() == "main" && p != nil && p.Path() == "" {
		return "tk_main"
	}

	return ObjName(fn)
}

// MethodName writes the symbol of a method: the receiver's type and
// the method's name, under the runtime prefix so it collides with
// nothing the program declares.
func MethodName(recv types.Type, name string) string {
	return "tk_" + Mangle(ValueType(recv)) + "_" + name
}

// NamedName writes the symbol of a declared type: its own, or for an
// instance its name followed by its arguments.
func NamedName(t *types.Named) string {
	name := ObjName(t.Obj())
	for _, a := range t.TypeArgs() {
		name += "_" + Mangle(a)
	}

	return name
}

// Mangle writes a type as a fragment of a symbol.
func Mangle(t types.Type) string {
	switch t := t.(type) {
	case *types.Basic:
		return types.Typ[t.Kind()].Name()
	case *types.Named:
		return NamedName(t)
	case *types.TypeParam:
		return Ident(t.Obj().Name()) // never written: every type is substituted first
	case *types.Trait:
		return Ident(t.Obj().Name())
	case *types.Ref:
		if t.Mut() {
			return "mut_" + Mangle(t.Elem())
		}

		return "ref_" + Mangle(t.Elem())
	case *types.Pointer:
		return "ptr_" + Mangle(t.Elem())
	case *types.Chan:
		return "chan_" + Mangle(t.Elem())
	case *types.Option:
		return "opt_" + Mangle(t.Elem())
	case *types.Result:
		return "res_" + Mangle(t.Val()) + "_" + Mangle(t.Err())
	case *types.Slice:
		return "slice_" + Mangle(t.Elem())
	case *types.Array:
		return "array_" + strconv.FormatInt(t.Len(), 10) + "_" + Mangle(t.Elem())
	case *types.Map:
		return "map_" + Mangle(t.Key()) + "_" + Mangle(t.Value())
	case *types.Signature:
		parts := make([]string, len(t.Params()))
		for i, p := range t.Params() {
			parts[i] = Mangle(p.Type())
		}

		return "fn" + strconv.Itoa(len(parts)) + "_" + strings.Join(append(parts, Mangle(t.Results())), "_")
	case *types.Tuple:
		if t == types.Unit {
			return "unit"
		}

		parts := make([]string, t.Len())
		for i := range parts {
			parts[i] = Mangle(t.At(i))
		}

		return "tup" + strconv.Itoa(t.Len()) + "_" + strings.Join(parts, "_")
	}

	return "unknown"
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
