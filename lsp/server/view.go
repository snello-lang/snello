// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/scanner"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
	"github.com/snello-lang/snello/std"
)

// posType is the type of a position, which a node's fields that hold
// where its own tokens are have.
var posType = reflect.TypeOf(token.NoPos)

// A view is what one check leaves for the requests on its package's
// files: every file the check parsed, the program's and its imports',
// whose positions are apart, so that a position names its file; the
// sources the check read, by name, against which its positions are
// converted; and, once the check is kept, the checker's information and
// the syntax of the package's own files alone, by path, and the type
// each function of a type belongs to, which the function does not keep.
// What the check knew of its imports' insides is let go, since no
// request reads it: a hover reads a declaration's comment from its file
// again.
type view struct {
	info   *types.Info
	files  []*token.File
	syntax map[*token.File]*ast.File
	srcs   map[string][]byte
	byPath map[string]*token.File
	owners map[*types.Func]*types.TypeName
}

// A target is what a place in a document holds: the name there, with
// the object it declares or uses, or the expression whose token is
// there, with its type, or the package an import's path or the package
// clause names, or a method the language declares of one of its types,
// which no object stands for, as builtinFile documents it; and the
// stretch of the document it covers, as offsets.
type target struct {
	obj        types.Object
	typ        types.Type
	pkg        *types.Package
	builtin    *builtinDecl
	start, end int
}

// newView returns the view of a check that reads its sources into
// srcs.
func newView(srcs map[string][]byte) *view {
	return &view{info: &types.Info{}, syntax: make(map[*token.File]*ast.File), srcs: srcs, byPath: make(map[string]*token.File)}
}

// parsed keeps a file the check parsed, with its syntax.
func (v *view) parsed(file *token.File, syntax *ast.File) {
	v.files = append(v.files, file)
	v.syntax[file] = syntax
}

// typed reports whether the check typed the file, which a file that
// does not parse, or one of a package beside it that does not, never
// is: its declarations' names are defined, or it declares nothing.
func (v *view) typed(file *token.File) bool {
	f := v.syntax[file]
	if f == nil {
		return false
	}

	for _, d := range f.Decls {
		if names := namesOf(d); len(names) > 0 && names[0] != nil {
			_, ok := v.info.Defs[names[0]]
			return ok
		}
	}

	return true
}

// trim keeps of the check what the requests on its own files read: the
// checker's information on their names and expressions, their syntax,
// by path as pathOf names them, and the type each function of a type of
// any file belongs to; the rest goes.
func (v *view) trim(own map[string]*token.File) {
	for _, f := range v.files {
		syntax := v.syntax[f]
		if syntax == nil {
			continue
		}

		for _, d := range syntax.Decls {
			if d, ok := d.(*ast.FuncDecl); ok && d.Of != nil {
				fn, _ := v.info.Defs[d.Name].(*types.Func)
				tn, _ := v.info.Uses[d.Of.Name].(*types.TypeName)
				if fn != nil && tn != nil {
					if v.owners == nil {
						v.owners = make(map[*types.Func]*types.TypeName)
					}

					v.owners[fn] = tn
				}
			}
		}
	}

	kept := &types.Info{Defs: make(map[*ast.Ident]types.Object), Uses: make(map[*ast.Ident]types.Object), Types: make(map[ast.Expr]types.Type)}
	syntax := make(map[*token.File]*ast.File, len(own))
	for path, f := range own {
		v.byPath[path] = f
		syntax[f] = v.syntax[f]
		keep := func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				if obj, ok := v.info.Defs[id]; ok {
					kept.Defs[id] = obj
				}

				if obj, ok := v.info.Uses[id]; ok {
					kept.Uses[id] = obj
				}
			}

			if x, ok := n.(ast.Expr); ok {
				if t, ok := v.info.Types[x]; ok {
					kept.Types[x] = t
				}
			}

			return true
		}

		ast.Inspect(v.syntax[f], keep)
		if v.syntax[f] != nil {
			for _, attrs := range v.syntax[f].Attrs { // the attributes, kept beside the tree, which Inspect does not walk
				for _, a := range attrs {
					ast.Inspect(a.X, keep)
				}
			}
		}
	}

	v.info, v.syntax = kept, syntax
}

// fileOf returns the file of the check that holds pos, or nil.
func (v *view) fileOf(pos token.Pos) *token.File {
	for _, f := range v.files {
		if f.Holds(pos) {
			return f
		}
	}

	return nil
}

// source returns the text of the file named name as the check read it:
// a source read through the server, a file of the standard library from
// the copy in the compiler, by its path under std/, and any other from
// the disk.
func (v *view) source(name string) ([]byte, error) {
	if src, ok := v.srcs[name]; ok {
		return src, nil
	}

	if !filepath.IsAbs(name) {
		return std.Sources.ReadFile(name)
	}

	return os.ReadFile(name)
}

// at returns what the file at offset holds: the name there, or the
// innermost expression one of whose own tokens is there; nil for
// neither, or a file the check did not parse.
func (v *view) at(file *token.File, src []byte, offset int) *target {
	syntax := v.syntax[file]
	if syntax == nil {
		return nil
	}

	if id := identAt(syntax, file, offset); id != nil {
		start := file.Offset(id.NamePos)
		if id == syntax.Name {
			return &target{pkg: v.ownPackage(syntax), start: start, end: start + len(id.Name)}
		}

		obj := v.info.Defs[id]
		if obj == nil {
			obj = v.info.Uses[id]
		}

		t := &target{obj: obj, typ: v.info.Types[id], start: start, end: start + len(id.Name)}
		if sel := selectorOf(syntax, id); obj == nil && sel != nil {
			if d, ok := builtinMethodOf(v.info.Types[sel.X], id.Name); ok {
				t.builtin = &d
			}
		}

		return t
	}

	start, end, ok := tokenAt(file.Name(), src, offset)
	if !ok {
		return nil
	}

	for _, spec := range syntax.Imports {
		if file.Offset(spec.Path.Pos()) == start {
			pn := v.importOf(spec)
			if pn == nil {
				return nil
			}

			return &target{obj: pn, pkg: pn.Imported(), start: start, end: end}
		}
	}

	if x, ok := owner(syntax, file.Pos(start)).(ast.Expr); ok && v.info.Types[x] != nil {
		return &target{typ: v.info.Types[x], start: start, end: end}
	}

	return nil
}

// declaration returns the declaration of obj as the checker types it,
// and its doc comment, or its line comment when it has none: the
// comment of the node the object's name is declared in, read from the
// file it is declared in, parsed again with its comments.
func (v *view) declaration(obj types.Object) (decl, doc string) {
	decl = types.ObjectString(obj, v.typeOf(obj))
	file := v.fileOf(obj.Pos())
	if file == nil {
		d, _ := builtinOf(obj) // a name the language declares, documented in builtinFile
		return decl, d.doc
	}

	src, err := v.source(file.Name())
	if err != nil {
		return decl, ""
	}

	again := token.NewFile(file.Name(), len(src))
	f, _ := parser.ParseComments(again, src)
	if f == nil || f.Docs == nil {
		return decl, ""
	}

	offset := file.Offset(obj.Pos())
	for _, comments := range []map[ast.Node]*ast.CommentGroup{f.Docs.Lead, f.Docs.Line} {
		for n, g := range comments {
			if declares(n, again, offset) {
				return decl, g.Text()
			}
		}
	}

	return decl, ""
}

// packageDoc returns the doc comment of pkg, a package the check
// loaded, as snello doc gives it: the comment above the package clause
// of each of its source files, in the order of their names, joined, a
// test file's left out. Its files are the files of the check in the
// directory of its declarations.
func (v *view) packageDoc(pkg *types.Package) string {
	dirs := make(map[string]bool)
	for _, name := range pkg.Scope().Names() {
		if f := v.fileOf(pkg.Scope().Lookup(name).Pos()); f != nil {
			dirs[filepath.Dir(f.Name())] = true
		}
	}

	var names []string
	for _, f := range v.files {
		if dirs[filepath.Dir(f.Name())] && !importer.IsTestFile(f.Name()) {
			names = append(names, f.Name())
		}
	}

	slices.Sort(names)
	var docs []string
	for _, name := range slices.Compact(names) {
		src, err := v.source(name)
		if err != nil {
			continue
		}

		f, _ := parser.ParseComments(token.NewFile(name, len(src)), src)
		if f == nil || f.Name == nil || f.Name.Name != pkg.Name() || f.Docs == nil {
			continue
		}

		if g := f.Docs.Lead[f]; g != nil {
			docs = append(docs, g.Text())
		}
	}

	return strings.Join(docs, "\n")
}

// selectorOf returns the selector expression whose selected name is id,
// x.id, or nil for a name selected from nothing.
func selectorOf(syntax *ast.File, id *ast.Ident) *ast.SelectorExpr {
	var found *ast.SelectorExpr
	ast.Inspect(syntax, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel == id {
			found = sel
		}

		return found == nil
	})

	return found
}

// ownPackage returns the package of the file syntax, as one of its
// top-level names says, or nil for a file that declares none.
func (v *view) ownPackage(syntax *ast.File) *types.Package {
	var pkg *types.Package
	ast.Inspect(syntax, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && pkg == nil {
			if obj := v.info.Defs[id]; obj != nil {
				pkg = obj.Pkg()
			}
		}

		return pkg == nil
	})

	return pkg
}

// importOf returns the package name the import spec declares, as the
// file's uses of it record it, or nil for an import the file never
// uses, whose error the check reports.
func (v *view) importOf(spec *ast.ImportSpec) *types.PkgName {
	for _, obj := range v.info.Uses {
		if pn, ok := obj.(*types.PkgName); ok && pn.Pos() == spec.Pos() && pn.Imported() != nil {
			return pn
		}
	}

	return nil
}

// typeOf returns the type a function of a type belongs to, Point of
// func Point.Parse, which the function does not keep: the type its
// declaration names, which trim kept; nil for any other object.
func (v *view) typeOf(obj types.Object) *types.TypeName {
	fn, ok := obj.(*types.Func)
	if !ok {
		return nil
	}

	return v.owners[fn.Origin()]
}

// identAt returns the name of syntax at offset: the one whose letters
// cover it, or else the one that ends there, where a cursor after a
// name stands; nil for none.
func identAt(syntax *ast.File, file *token.File, offset int) *ast.Ident {
	var within, ending *ast.Ident
	ast.Inspect(syntax, func(n ast.Node) bool {
		if within != nil {
			return false
		}

		if id, ok := n.(*ast.Ident); ok && id.NamePos.IsValid() {
			switch start := file.Offset(id.NamePos); {
			case start <= offset && offset < start+len(id.Name):
				within = id
			case offset == start+len(id.Name) && ending == nil:
				ending = id
			}
		}

		return true
	})

	if within != nil {
		return within
	}

	return ending
}

// tokenAt returns the stretch of the token of src at offset, as
// offsets, scanning src as the file named name.
func tokenAt(name string, src []byte, offset int) (start, end int, ok bool) {
	file := token.NewFile(name, len(src))
	var s scanner.Scanner
	s.Init(file, src, func(token.Position, string) {})
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			return 0, 0, false
		}

		start = file.Offset(pos)
		if start > offset {
			return 0, 0, false
		}

		size := len(lit)
		if size == 0 {
			size = len(tok.String())
		}

		if offset < start+size {
			return start, start + size, true
		}
	}
}

// owner returns the innermost node of syntax that records one of its
// own tokens at pos, the operator of a binary expression or the
// parenthesis of a call; nil for none.
func owner(syntax *ast.File, pos token.Pos) ast.Node {
	var found ast.Node
	ast.Inspect(syntax, func(n ast.Node) bool {
		v := reflect.ValueOf(n).Elem()
		for i := range v.NumField() {
			if f := v.Field(i); f.Type() == posType && token.Pos(f.Int()) == pos {
				found = n
				break
			}
		}

		return true
	})

	return found
}

// declares reports whether the node n of file declares the name at
// offset: a declaration's, a field's or a variant's name.
func declares(n ast.Node, file *token.File, offset int) bool {
	for _, id := range namesOf(n) {
		if id != nil && file.Offset(id.NamePos) == offset {
			return true
		}
	}

	return false
}

// namesOf returns the names a node declares: a declaration's, a field's
// or a variant's; none for any other node.
func namesOf(n ast.Node) []*ast.Ident {
	switch n := n.(type) {
	case *ast.FuncDecl:
		return []*ast.Ident{n.Name}
	case *ast.TypeDecl:
		return []*ast.Ident{n.Name}
	case *ast.TraitDecl:
		return []*ast.Ident{n.Name}
	case *ast.Variant:
		return []*ast.Ident{n.Name}
	case *ast.VarDecl:
		return n.Names
	case *ast.Field:
		return n.Names
	}

	return nil
}
