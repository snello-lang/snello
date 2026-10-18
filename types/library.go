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

// OSFile is the library's os.File type, shared between packages and
// recognized by the C emitter.
var OSFile = NewNamed(NewTypeName(token.NoPos, "File", nil), NewStruct(nil), []string{"Drop"})

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
	OSFile.AddMethod(libMethod("Close", NewVar(token.NoPos, "f", OSFile), nil, errorResult))
	OSFile.AddMethod(libMethod("Sync", NewVar(token.NoPos, "f", NewRef(OSFile, true)), nil, errorResult))
	OSFile.AddMethod(libMethod("WriteString", NewVar(token.NoPos, "f", NewRef(OSFile, true)), []*Var{param("s", ref(Typ[String]))}, NewResult(Typ[Int], Typ[Error])))
	s.Insert(OSFile.obj)
	s.Insert(libFunc("ReadToString", NewSignature(nil, []*Var{param("path", ref(Typ[String]))}, NewResult(Typ[String], Typ[Error]))))
	s.Insert(libFunc("Open", NewSignature(nil, []*Var{param("path", ref(Typ[String]))}, NewResult(OSFile, Typ[Error]))))
	s.Insert(libFunc("Create", NewSignature(nil, []*Var{param("path", ref(Typ[String]))}, NewResult(OSFile, Typ[Error]))))
	s.Insert(libFunc("MkdirTemp", NewSignature(nil, []*Var{param("dir", ref(Typ[String])), param("pattern", ref(Typ[String]))}, NewResult(Typ[String], Typ[Error]))))
	s.Insert(libFunc("RemoveAll", NewSignature(nil, []*Var{param("path", ref(Typ[String]))}, errorResult)))
	s.Insert(libFunc("Exit", NewSignature(nil, []*Var{param("code", Typ[Int])}, Typ[Never])))
	s.Insert(NewVar(token.NoPos, "Args", NewSlice(Typ[String])))
	s.Insert(NewVar(token.NoPos, "Stdin", OSFile))
	s.Insert(NewVar(token.NoPos, "Stdout", OSFile))
	s.Insert(NewVar(token.NoPos, "Stderr", OSFile))
	return s
}

func ioPackage() *Scope {
	s := NewScope(nil)
	s.Insert(libFunc("Copy", NewSignature(nil, []*Var{param("dst", NewRef(OSFile, true)), param("src", NewRef(OSFile, true))}, NewResult(Typ[Int64], Typ[Error]))))
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
		return optionMethod(recv, t.elem, name)
	case *Result:
		return resultMethod(recv, t.val, name)
	}

	return nil
}

// Each predeclared method says what it does to its receiver, as a
// declared method's receiver does: reads lends it, changes borrows it
// mut, and takes consumes it, as Expect does the option it opens.
func sliceMethod(t *Slice, name string) *Signature {
	elem := t.elem
	switch name {
	case "Push":
		return changes([]*Var{param("v", elem)}, Unit)
	case "Pop":
		return changes(nil, NewOption(elem))
	case "Insert":
		return changes([]*Var{param("i", Typ[Int]), param("v", elem)}, Unit)
	case "Remove":
		return changes([]*Var{param("i", Typ[Int])}, elem)
	case "RemoveValue":
		return changes([]*Var{param("v", ref(elem))}, Typ[Bool])
	case "Contains":
		return reads([]*Var{param("v", ref(elem))}, Typ[Bool])
	case "Clear", "Reverse", "Sort":
		return changes(nil, Unit)
	case "SortBy":
		less := NewSignature(nil, []*Var{param("a", ref(elem)), param("b", ref(elem))}, Typ[Bool])
		return changes([]*Var{param("less", less)}, Unit)
	case "Clone":
		return reads(nil, t)
	}

	return nil
}

func mapMethod(t *Map, name string) *Signature {
	switch name {
	case "Get":
		return reads([]*Var{param("k", ref(t.key))}, NewOption(ref(t.value)))
	case "GetMut":
		return changes([]*Var{param("k", ref(t.key))}, NewOption(NewRef(t.value, true)))
	case "Insert":
		return changes([]*Var{param("k", t.key), param("v", t.value)}, Unit)
	case "Remove":
		return changes([]*Var{param("k", ref(t.key))}, NewOption(t.value))
	case "Contains":
		return reads([]*Var{param("k", ref(t.key))}, Typ[Bool])
	case "Clone":
		return reads(nil, t)
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
		return reads(nil, Typ[Int])
	case "Clone", "ToUpper", "ToLower":
		return reads(nil, s)
	case "Fields":
		return reads(nil, NewSlice(ref(s)))
	case "Split":
		return reads([]*Var{param("sep", ref(s))}, NewSlice(ref(s)))
	case "SplitOnce":
		return reads([]*Var{param("sep", ref(s))}, NewOption(NewTuple(ref(s), ref(s))))
	case "Trim":
		return reads(nil, ref(s))
	case "HasPrefix", "HasSuffix", "Contains":
		return reads([]*Var{param("t", ref(s))}, Typ[Bool])
	}

	return nil
}

func optionMethod(t, elem Type, name string) *Signature {
	switch name {
	case "Expect":
		return takes(t, []*Var{param("msg", ref(Typ[String]))}, elem)
	case "Unwrap":
		return takes(t, nil, elem)
	case "UnwrapOr":
		return takes(t, []*Var{param("v", elem)}, elem)
	case "IsSome", "IsNone":
		return reads(nil, Typ[Bool])
	}

	return nil
}

func resultMethod(t, val Type, name string) *Signature {
	switch name {
	case "Expect":
		return takes(t, []*Var{param("msg", ref(Typ[String]))}, val)
	case "Unwrap":
		return takes(t, nil, val)
	case "UnwrapOr":
		return takes(t, []*Var{param("v", val)}, val)
	case "IsOk", "IsErr":
		return reads(nil, Typ[Bool])
	}

	return nil
}

func libFunc(name string, sig *Signature) *Func {
	return NewFunc(token.NoPos, name, sig)
}

func libMethod(name string, recv *Var, params []*Var, results Type) *Func {
	return NewFunc(token.NoPos, name, NewSignature(recv, params, results))
}

// The receivers of the methods that lend are shared: the checker reads
// only whether the borrow is mut, and the emitter lowers these methods
// by name. A method that takes its receiver names the type, which the
// move pass needs.
var (
	refReceiver = param("", NewRef(Typ[Any], false))
	mutReceiver = param("", NewRef(Typ[Any], true))
)

func reads(params []*Var, results Type) *Signature {
	return NewSignature(refReceiver, params, results)
}

func changes(params []*Var, results Type) *Signature {
	return NewSignature(mutReceiver, params, results)
}

func takes(recv Type, params []*Var, results Type) *Signature {
	return NewSignature(param("", recv), params, results)
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
