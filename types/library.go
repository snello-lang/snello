// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "github.com/teak-lang/teak/token"

// The stage 1 library: the names the core programs import, with the
// signatures section 4 of _docs/design/05-standard-library.md gives
// them, and the methods section 1 predeclares on slices, maps, strings,
// options and results. Everything here is a mapping onto a Go function
// of the same meaning, debt the stage 4 runtime repays; see the
// mapping table in that document.

// libraries holds one scope per package path, built once.
var libraries = map[string]*Scope{
	"fmt": fmtPackage(),
	"os":  osPackage(),
	"io":  ioPackage(),
}

// errorResult is Result<(), error>, what a library call with nothing
// to return but failure yields.
var errorResult = NewResult(Unit, Typ[Error])

// The library's own named types, shared between packages.
var osFile = NewNamed(NewTypeName(token.NoPos, "File", nil), NewStruct(nil), []string{"Drop"})

// libraryMember returns the object named in an imported package, or nil.
func libraryMember(path, name string) Object {
	if scope := libraries[path]; scope != nil {
		return scope.Lookup(name)
	}

	return nil
}

// isLibraryPackage reports whether the import path names a package the
// stage 1 library knows.
func isLibraryPackage(path string) bool {
	return libraries[path] != nil
}

func fmtPackage() *Scope {
	s := NewScope(nil)
	s.Insert(libFunc("Println", variadic(nil, Unit)))
	s.Insert(libFunc("Printf", variadic([]*Var{param("format", ref(Typ[String]))}, Unit)))
	s.Insert(libFunc("Sprintf", variadic([]*Var{param("format", ref(Typ[String]))}, Typ[String])))
	s.Insert(libFunc("Sprint", variadic(nil, Typ[String])))
	return s
}

func osPackage() *Scope {
	s := NewScope(nil)
	osFile.AddMethod(libMethod("Close", NewVar(token.NoPos, "f", osFile), nil, errorResult))
	osFile.AddMethod(libMethod("Sync", NewVar(token.NoPos, "f", NewRef(osFile, true)), nil, errorResult))
	osFile.AddMethod(libMethod("WriteString", NewVar(token.NoPos, "f", NewRef(osFile, true)), []*Var{param("s", ref(Typ[String]))}, NewResult(Typ[Int], Typ[Error])))
	s.Insert(osFile.obj)
	s.Insert(libFunc("ReadToString", NewSignature(nil, []*Var{param("path", ref(Typ[String]))}, NewResult(Typ[String], Typ[Error]))))
	s.Insert(libFunc("Open", NewSignature(nil, []*Var{param("path", ref(Typ[String]))}, NewResult(osFile, Typ[Error]))))
	s.Insert(libFunc("Create", NewSignature(nil, []*Var{param("path", ref(Typ[String]))}, NewResult(osFile, Typ[Error]))))
	s.Insert(libFunc("MkdirTemp", NewSignature(nil, []*Var{param("dir", ref(Typ[String])), param("pattern", ref(Typ[String]))}, NewResult(Typ[String], Typ[Error]))))
	s.Insert(libFunc("RemoveAll", NewSignature(nil, []*Var{param("path", ref(Typ[String]))}, errorResult)))
	s.Insert(libFunc("Exit", NewSignature(nil, []*Var{param("code", Typ[Int])}, Typ[Never])))
	s.Insert(NewVar(token.NoPos, "Args", NewSlice(Typ[String])))
	s.Insert(NewVar(token.NoPos, "Stdin", osFile))
	s.Insert(NewVar(token.NoPos, "Stdout", osFile))
	s.Insert(NewVar(token.NoPos, "Stderr", osFile))
	return s
}

func ioPackage() *Scope {
	s := NewScope(nil)
	s.Insert(libFunc("Copy", NewSignature(nil, []*Var{param("dst", NewRef(osFile, true)), param("src", NewRef(osFile, true))}, NewResult(Typ[Int64], Typ[Error]))))
	return s
}

// builtinMethod returns the signature of a predeclared method on a
// slice, map, string, option or result, or nil. The receiver is the
// value the method is called on, its borrows already stripped.
func builtinMethod(recv Type, name string) *Signature {
	switch t := recv.Underlying().(type) {
	case *Slice:
		return sliceMethod(t, name)
	case *Map:
		return mapMethod(t, name)
	case *Basic:
		if t.kind == String {
			return stringMethod(name)
		}
	case *Option:
		return optionMethod(t.elem, name)
	case *Result:
		return resultMethod(t.val, name)
	}

	return nil
}

func sliceMethod(t *Slice, name string) *Signature {
	elem := t.elem
	switch name {
	case "Push":
		return method([]*Var{param("v", elem)}, Unit)
	case "Pop":
		return method(nil, NewOption(elem))
	case "Insert":
		return method([]*Var{param("i", Typ[Int]), param("v", elem)}, Unit)
	case "Remove":
		return method([]*Var{param("i", Typ[Int])}, elem)
	case "RemoveValue":
		return method([]*Var{param("v", ref(elem))}, Typ[Bool])
	case "Contains":
		return method([]*Var{param("v", ref(elem))}, Typ[Bool])
	case "Clear", "Reverse", "Sort":
		return method(nil, Unit)
	case "SortBy":
		less := NewSignature(nil, []*Var{param("a", ref(elem)), param("b", ref(elem))}, Typ[Bool])
		return method([]*Var{param("less", less)}, Unit)
	case "Clone":
		return method(nil, t)
	}

	return nil
}

func mapMethod(t *Map, name string) *Signature {
	switch name {
	case "Get":
		return method([]*Var{param("k", ref(t.key))}, NewOption(ref(t.value)))
	case "GetMut":
		return method([]*Var{param("k", ref(t.key))}, NewOption(NewRef(t.value, true)))
	case "Insert":
		return method([]*Var{param("k", t.key), param("v", t.value)}, Unit)
	case "Remove":
		return method([]*Var{param("k", ref(t.key))}, NewOption(t.value))
	case "Contains":
		return method([]*Var{param("k", ref(t.key))}, Typ[Bool])
	case "Clone":
		return method(nil, t)
	}

	return nil
}

// stringMethod: Fields and Split yield borrows into the string. Until
// the iterator trait exists (stage 2) they are typed as a slice of
// borrows, a type a program cannot write, which ranges as the iterator
// will.
func stringMethod(name string) *Signature {
	s := Typ[String]
	switch name {
	case "Len":
		return method(nil, Typ[Int])
	case "Clone", "ToUpper", "ToLower":
		return method(nil, s)
	case "Fields":
		return method(nil, NewSlice(ref(s)))
	case "Split":
		return method([]*Var{param("sep", ref(s))}, NewSlice(ref(s)))
	case "SplitOnce":
		return method([]*Var{param("sep", ref(s))}, NewOption(NewTuple(ref(s), ref(s))))
	case "Trim":
		return method(nil, ref(s))
	case "HasPrefix", "HasSuffix", "Contains":
		return method([]*Var{param("t", ref(s))}, Typ[Bool])
	}

	return nil
}

func optionMethod(elem Type, name string) *Signature {
	switch name {
	case "Expect":
		return method([]*Var{param("msg", ref(Typ[String]))}, elem)
	case "Unwrap":
		return method(nil, elem)
	case "UnwrapOr":
		return method([]*Var{param("v", elem)}, elem)
	case "IsSome", "IsNone":
		return method(nil, Typ[Bool])
	}

	return nil
}

func resultMethod(val Type, name string) *Signature {
	switch name {
	case "Expect":
		return method([]*Var{param("msg", ref(Typ[String]))}, val)
	case "Unwrap":
		return method(nil, val)
	case "UnwrapOr":
		return method([]*Var{param("v", val)}, val)
	case "IsOk", "IsErr":
		return method(nil, Typ[Bool])
	}

	return nil
}

func libFunc(name string, sig *Signature) *Func {
	return NewFunc(token.NoPos, name, sig)
}

func libMethod(name string, recv *Var, params []*Var, results Type) *Func {
	return NewFunc(token.NoPos, name, NewSignature(recv, params, results))
}

func method(params []*Var, results Type) *Signature {
	return NewSignature(nil, params, results)
}

func variadic(params []*Var, results Type) *Signature {
	sig := NewSignature(nil, append(params, param("args", Typ[Any])), results)
	sig.variadic = true
	return sig
}

func param(name string, t Type) *Var {
	return NewVar(token.NoPos, name, t)
}

func ref(t Type) *Ref {
	return NewRef(t, false)
}
