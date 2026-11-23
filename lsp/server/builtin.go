// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"bytes"
	"sync"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
	"github.com/snello-lang/snello/std"
)

// The kinds of the language's types a documented method of builtinFile
// is on, as builtinKind and typeKind name them, and keyDot, which joins
// a receiver's kind to a method's name in a key of the index.
const (
	sliceKind  = "[]T"
	bytesKind  = "[]byte"
	byteKind   = "byte"
	optionKind = "T?"
	resultKind = "Result"
	mapKind    = "map"
	chanKind   = "chan"
	sendKind   = "chan<-"
	recvKind   = "<-chan"
	keyDot     = "."
)

// builtins indexes builtinFile, read once, since the copy in the
// compiler never changes while it runs.
var builtins = sync.OnceValue(func() *builtinIndex {
	index := &builtinIndex{names: make(map[string]builtinDecl), methods: make(map[string]builtinDecl), traitMethods: make(map[string]builtinDecl)}
	src, err := std.Sources.ReadFile(builtinFile)
	if err != nil {
		return index
	}

	file := token.NewFile(builtinFile, len(src))
	f, _ := parser.ParseComments(file, src)
	if f == nil {
		return index
	}

	of := func(name *ast.Ident, decl, commented ast.Node) builtinDecl {
		start := file.Offset(name.NamePos)
		d := builtinDecl{start: start, end: start + len(name.Name), decl: lineOf(src, file.Offset(decl.Pos()))}
		if f.Docs != nil && f.Docs.Lead[commented] != nil {
			d.doc = f.Docs.Lead[commented].Text()
		}

		return d
	}

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				index.names[d.Name.Name] = of(d.Name, d, d)
			} else {
				index.methods[builtinKind(d.Recv.Type)+keyDot+d.Name.Name] = of(d.Name, d, d)
			}
		case *ast.TypeDecl:
			index.names[d.Name.Name] = of(d.Name, d, d)
		case *ast.TraitDecl:
			index.names[d.Name.Name] = of(d.Name, d, d)
			for _, m := range d.Methods {
				index.traitMethods[m.Name.Name] = of(m.Name, m, d) // a trait's method of one line shares the trait's comment
			}
		case *ast.VarDecl:
			for _, name := range d.Names {
				index.names[name.Name] = of(name, d, d)
			}
		}
	}

	return index
})

// A builtinIndex is builtinFile's declarations: the top-level names, by
// name; the methods of the language's types, by their receiver's kind
// and their name, []T.Push and string.Contains; and the methods of the
// language's traits, by name, each declared by one trait.
type builtinIndex struct {
	names, methods, traitMethods map[string]builtinDecl
}

// A builtinDecl is a name the language declares as builtinFile
// documents it: where its name is, as offsets, the line that declares
// it, and its doc comment.
type builtinDecl struct {
	start, end int
	decl, doc  string
}

// builtinOf returns the documentation of obj, an object the language
// declares, from builtinFile: a method of a trait the language declares
// by the trait's, and any other by its name; false for an object of a
// program, or one the file does not document.
func builtinOf(obj types.Object) (builtinDecl, bool) {
	if obj == nil || obj.Pos().IsValid() {
		return builtinDecl{}, false
	}

	index := builtins()
	if _, method := obj.(*types.Func); method {
		if d, ok := index.traitMethods[obj.Name()]; ok {
			return d, true
		}
	}

	d, ok := index.names[obj.Name()]
	return d, ok
}

// builtinMethodOf returns the documentation of the method name of the
// type the language declares that recv is, a borrow of one read as the
// type, from builtinFile: a []byte's own, or else a slice's; false for
// a type the file documents no such method of.
func builtinMethodOf(recv types.Type, name string) (builtinDecl, bool) {
	if r, ok := recv.(*types.Ref); ok {
		recv = r.Elem()
	}

	kind := typeKind(recv)
	if kind == "" {
		return builtinDecl{}, false
	}

	methods := builtins().methods
	if d, ok := methods[kind+keyDot+name]; ok {
		return d, true
	}

	if kind == bytesKind {
		d, ok := methods[sliceKind+keyDot+name]
		return d, ok
	}

	return builtinDecl{}, false
}

// builtinKind returns the kind of a documented method's receiver type,
// as builtinFile writes it: the language's type it is on, read through
// the receiver's mode.
func builtinKind(t ast.Type) string {
	if r, ok := t.(*ast.RefType); ok {
		t = r.Elem
	}

	switch t := t.(type) {
	case *ast.SliceType:
		if n, ok := t.Elem.(*ast.NamedType); ok && n.Pkg == nil && n.Name.Name == byteKind {
			return bytesKind
		}

		return sliceKind
	case *ast.OptionType:
		return optionKind
	case *ast.MapType:
		return mapKind
	case *ast.ChanType:
		return chanKindOf(t.Dir == ast.SendOnly, t.Dir == ast.RecvOnly)
	case *ast.NamedType:
		return t.Name.Name
	}

	return ""
}

// typeKind returns the kind of the type t as builtinKind names a
// receiver's, or "" for a type the language declares no methods of: a
// named type's methods are its own.
func typeKind(t types.Type) string {
	switch t := t.(type) {
	case *types.Slice:
		if b, ok := t.Elem().(*types.Basic); ok && b.Kind() == types.Byte {
			return bytesKind
		}

		return sliceKind
	case *types.Option:
		return optionKind
	case *types.Result:
		return resultKind
	case *types.Map:
		return mapKind
	case *types.Chan:
		return chanKindOf(t.Dir() == types.SendOnly, t.Dir() == types.RecvOnly)
	case *types.Basic:
		if t.Kind() == types.Byte {
			return byteKind
		}

		return t.Name()
	}

	return ""
}

// chanKindOf returns the kind of a channel that only sends, only
// receives, or does both.
func chanKindOf(send, recv bool) string {
	switch {
	case send:
		return sendKind
	case recv:
		return recvKind
	}

	return chanKind
}

// lineOf returns the line of src that begins at offset, or holds it,
// without its newline.
func lineOf(src []byte, offset int) string {
	end := bytes.IndexByte(src[offset:], '\n')
	if end < 0 {
		return string(src[offset:])
	}

	return string(src[offset : offset+end])
}
