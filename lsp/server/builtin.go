// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"bytes"
	"path/filepath"
	"strings"
	"sync"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/doc"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
	"github.com/snello-lang/snello/std"
)

// builtinFile is the file of the standard library that documents the
// names the language declares, which is never compiled; the kinds of
// the language's types a documented method of it is on, as builtinKind
// and typeKind name them; integerKind, the integer type whose comment
// documents the methods every integer type has; keyDot, which joins a
// receiver's kind to a method's name in a key of the index; and
// optionName, the enum T? is written for.
const (
	builtinFile = "builtin/builtin.snello"
	sliceKind   = "[]T"
	bytesKind   = "[]byte"
	byteKind    = "byte"
	optionKind  = "T?"
	resultKind  = "Result"
	mapKind     = "map"
	chanKind    = "chan"
	sendKind    = "chan<-"
	recvKind    = "<-chan"
	integerKind = "int"
	keyDot      = "."
	optionName  = "Option"
)

// builtins is the index of the compiler's copy of builtinFile, read
// once, since the copy never changes while the compiler runs.
var builtins = sync.OnceValue(func() *builtinIndex {
	src, err := std.Sources.ReadFile(builtinFile)
	if err != nil {
		return indexBuiltin(nil)
	}

	return indexBuiltin(src)
})

// A builtinIndex is builtinFile's declarations: the top-level names and
// the variants of its enums, by name; the methods of the language's
// types, by their receiver's kind and their name, []T.Push and
// string.Contains; the methods of the language's traits, by name, each
// declared by one trait; and the forms the language writes with its own
// syntax, by their name, as the documentation reads them, doc.Forms.
// Every entry is also in all, in the order of the file, for a place in
// the file to find the entry whose name it is on.
type builtinIndex struct {
	names, methods, traitMethods, forms map[string]builtinDecl
	all                                 []builtinDecl
}

// A builtinDecl is a name the language declares as builtinFile
// documents it: where its name is, as offsets, its declaration as
// written, and its doc comment; and the document it was found in, for
// one asked about in a builtinFile the editor has open, or "" for the
// compiler's copy.
type builtinDecl struct {
	start, end      int
	decl, doc, path string
}

// builtinOf returns the documentation of obj, an object the language
// declares, from builtinFile: a method of a trait the language declares
// by the trait's, and any other, a variant of its enums among them, by
// its name; false for an object of a program, or one the file does not
// document.
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

// builtinMethodOf returns the documentation of the method name the
// checker gives recv, a value of a type the language declares methods
// of, read through its borrows and owned pointers, as a call reads it,
// and a type declared over such a type, from builtinFile: a slice's,
// or else a []byte's own, as the checker tries them; for an integer,
// int's, which documents every integer type's; and a method of a
// standard trait a number or a bool has, by the trait's. False for a
// method the checker does not give the type.
func builtinMethodOf(recv types.Type, name string) (builtinDecl, bool) {
	for {
		if r, ok := recv.(*types.Ref); ok {
			recv = r.Elem()
		} else if p, ok := recv.(*types.Pointer); ok {
			recv = p.Elem()
		} else {
			break
		}
	}

	if recv == nil || types.BuiltinMethod(recv, name) == nil {
		return builtinDecl{}, false
	}

	index := builtins()
	kind := typeKind(recv.Underlying())
	kinds := []string{kind}
	switch {
	case kind == bytesKind:
		kinds = []string{sliceKind, bytesKind}
	case isInteger(recv.Underlying()):
		kinds = append(kinds, integerKind)
	}

	for _, k := range kinds {
		if d, ok := index.methods[k+keyDot+name]; ok {
			return d, true
		}
	}

	d, ok := index.traitMethods[name]
	return d, ok
}

// builtinFormOf returns the documentation of the form the syntax node n
// writes, from builtinFile: a type the language writes with its own
// syntax, map[K]V or []T, T? by Option's, and a borrow, ref T or mut T,
// in a type or at a call; false for any other node.
func builtinFormOf(n ast.Node) (builtinDecl, bool) {
	index := builtins()
	name := ""
	switch n := n.(type) {
	case *ast.SliceType:
		name = doc.FormSlice
	case *ast.ArrayType:
		name = doc.FormArray
	case *ast.MapType:
		name = doc.FormMap
	case *ast.ChanType:
		name = doc.FormChan
	case *ast.PointerType:
		name = doc.FormPointer
	case *ast.RefType:
		name = doc.FormRef
		if n.Mut {
			name = doc.FormMut
		}
	case *ast.UnaryExpr:
		switch n.Op {
		case token.REF:
			name = doc.FormRef
		case token.MUT:
			name = doc.FormMut
		}
	case *ast.OptionType:
		d, ok := index.names[optionName]
		return d, ok
	}

	d, ok := index.forms[name]
	return d, ok
}

// builtinAt returns the documentation the text src of builtinFile gives
// the name at offset: the entry whose name it is, or else the entry of
// the name it uses, bool in a signature; false for a place on no name
// the file documents.
func builtinAt(src []byte, offset int) (builtinDecl, bool) {
	index := indexBuiltin(src)
	for _, d := range index.all {
		if d.start <= offset && offset <= d.end {
			return d, true
		}
	}

	start, end, ok := tokenAt(builtinFile, src, offset)
	if !ok {
		return builtinDecl{}, false
	}

	d, ok := index.names[string(src[start:end])]
	return d, ok
}

// isBuiltinFile reports whether path is builtinFile: the one in a
// checkout of the compiler, std/builtin/builtin.snello, or its copy in
// the cache for this version of the compiler. Neither is checked, since
// nothing compiles it, so its places are answered from its text.
func isBuiltinFile(path string) bool {
	if filepath.Base(path) != filepath.Base(builtinFile) {
		return false
	}

	dir := filepath.Dir(path)
	if p, ok := stdCopyOf(dir); ok {
		return p == builtinPackage
	}

	_, p, ok := importer.StdPackage(dir)
	return ok && p == builtinPackage
}

// indexBuiltin indexes src, the text of builtinFile: each declaration
// by its name, its comment its own, a trait's method's the trait's when
// it has none, a constant's the comment of the group of constants on
// consecutive lines it is in, and a variant's its enum's when it has
// none.
func indexBuiltin(src []byte) *builtinIndex {
	index := &builtinIndex{names: make(map[string]builtinDecl), methods: make(map[string]builtinDecl), traitMethods: make(map[string]builtinDecl), forms: make(map[string]builtinDecl)}
	file := token.NewFile(builtinFile, len(src))
	f, _ := parser.ParseComments(file, src)
	if f == nil {
		return index
	}

	comment := func(n ast.Node) string {
		if f.Docs != nil && f.Docs.Lead[n] != nil {
			return f.Docs.Lead[n].Text()
		}

		return ""
	}

	entry := func(name *ast.Ident, decl, doc string) builtinDecl {
		start := file.Offset(name.NamePos)
		d := builtinDecl{start: start, end: start + len(name.Name), decl: decl, doc: doc}
		index.all = append(index.all, d)
		return d
	}

	group, groupEnd := "", 0 // the comment of the group of constants the last one was in, and its line
	for _, decl := range f.Decls {
		line := lineOf(src, file.Offset(decl.Pos()))
		switch d := decl.(type) {
		case *ast.FuncDecl:
			var b strings.Builder
			ast.WriteSignature(&b, d)
			if d.Recv == nil {
				index.names[d.Name.Name] = entry(d.Name, b.String(), comment(d))
			} else {
				index.methods[builtinKind(d.Recv.Type)+keyDot+d.Name.Name] = entry(d.Name, b.String(), comment(d))
			}
		case *ast.TypeDecl:
			index.names[d.Name.Name] = entry(d.Name, line, comment(d))
			if enum, ok := d.Def.(*ast.EnumType); ok {
				for _, v := range enum.Variants {
					index.names[v.Name.Name] = entry(v.Name, strings.TrimSpace(lineOf(src, file.Offset(v.Name.NamePos))), cmpOr(comment(v), comment(d)))
				}
			}
		case *ast.TraitDecl:
			index.names[d.Name.Name] = entry(d.Name, line, comment(d))
			for _, m := range d.Methods {
				var b strings.Builder
				ast.WriteSignature(&b, m)
				index.traitMethods[m.Name.Name] = entry(m.Name, b.String(), cmpOr(comment(m), comment(d)))
			}
		case *ast.VarDecl:
			at := file.Position(d.Pos()).Line
			own := comment(d)
			if own == "" && at == groupEnd+1 {
				own = group
			}

			group, groupEnd = own, at
			for _, name := range d.Names {
				index.names[name.Name] = entry(name, line, own)
			}
		}
	}

	for _, form := range doc.Forms(f) {
		start := file.Offset(form.Pos)
		d := builtinDecl{start: start, end: start + len(form.Written), decl: form.Written, doc: form.Doc}
		index.forms[form.Name] = d
		index.all = append(index.all, d)
	}

	return index
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

// typeKind returns the kind of the type t, an underlying type, as
// builtinKind names a receiver's, or "" for a type the language
// declares no methods of.
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

// isInteger reports whether t, an underlying type, is an integer type.
func isInteger(t types.Type) bool {
	b, ok := t.(*types.Basic)
	return ok && b.Info()&types.IsInteger != 0
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

// cmpOr returns a, or b when a is empty.
func cmpOr(a, b string) string {
	if a != "" {
		return a
	}

	return b
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
