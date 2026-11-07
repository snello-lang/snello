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
	if p := obj.Pkg(); p != nil && p.Path() != "" {
		if p.Standard() {
			return StdPrefix(p.Path()) + obj.Name()
		}

		return PathPrefix(p.Path()) + obj.Name()
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
// CheckPrefixes refuses two paths that would share a prefix this way
// (_docs/design/08-packages.md). StdPrefix is the standard library's
// form, sn_fmt_ and sn_net__http_, the names the runtime defines.
func PathPrefix(path string) string {
	return prefix("snm_", path)
}

// StdPrefix writes the start of every symbol of the standard package
// at an import path.
func StdPrefix(path string) string {
	return prefix("sn_", path)
}

func prefix(space, path string) string {
	var b strings.Builder
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
