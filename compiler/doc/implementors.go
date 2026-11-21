// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	"cmp"
	"slices"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/types"
)

// An Implementor is a type that implements a trait: its package's path,
// empty for a predeclared type, and its name.
type Implementor struct {
	Path string
	Name string
}

// A traitRef names a trait by its package's path and its name; a
// predeclared trait's path is BuiltinPath.
type traitRef struct{ path, name string }

// Implementors returns the types that implement the trait named name of
// the package at path, among pkgs, directly or through a trait that
// refines it, found from the impl written on each type, sorted by path
// and name; an unexported one only when unexported asks and it is of
// the trait's own package, whose page shows its unexported names. For a
// predeclared trait the predeclared types the checker gives it come
// first.
func Implementors(path, name string, pkgs []Source, unexported bool) []Implementor {
	want := map[traitRef]bool{{path, name}: true}
	for grown := true; grown; { // a trait that refines one wanted is wanted too, until none is added
		grown = false
		for _, src := range pkgs {
			forDecls(src, func(f *ast.File, d ast.Decl) {
				t, ok := d.(*ast.TraitDecl)
				if !ok {
					return
				}

				ref := traitRef{src.Path, t.Name.Name}
				if !want[ref] && slices.ContainsFunc(t.Refines, func(r ast.Type) bool { return want[refOf(src, f, r)] }) {
					want[ref], grown = true, true
				}
			})
		}
	}

	var out []Implementor
	for _, src := range pkgs {
		forDecls(src, func(f *ast.File, d ast.Decl) {
			if t, ok := d.(*ast.TypeDecl); ok && (unexported && src.Path == path || ast.IsExported(t.Name.Name)) &&
				slices.ContainsFunc(t.Impls, func(r ast.Type) bool { return want[refOf(src, f, r)] }) {
				out = append(out, Implementor{src.Path, t.Name.Name})
			}
		})
	}

	slices.SortFunc(out, func(a, b Implementor) int { return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Name, b.Name)) })
	return append(predeclaredImplementors(path, name), slices.Compact(out)...)
}

func forDecls(src Source, each func(*ast.File, ast.Decl)) {
	for _, f := range src.Files {
		for _, d := range f.Syntax.Decls {
			each(f.Syntax, d)
		}
	}
}

// refOf returns the trait a type written in f names: a name of the
// package, a name of a package f imports, or a predeclared name.
func refOf(src Source, f *ast.File, t ast.Type) traitRef {
	n, ok := t.(*ast.NamedType)
	if !ok {
		return traitRef{}
	}

	if n.Pkg != nil {
		for _, im := range f.Imports {
			path := strings.Trim(im.Path.Value, "\"`")
			local := path[strings.LastIndexByte(path, pathSep)+1:]
			if im.Name != nil {
				local = im.Name.Name
			}

			if local == n.Pkg.Name {
				return traitRef{path, n.Name.Name}
			}
		}

		return traitRef{}
	}

	if declares(src, n.Name.Name) {
		return traitRef{src.Path, n.Name.Name}
	}

	return traitRef{BuiltinPath, n.Name.Name}
}

func declares(src Source, name string) bool {
	for _, f := range src.Files {
		for _, d := range f.Syntax.Decls {
			if t, ok := d.(*ast.TraitDecl); ok && t.Name.Name == name {
				return true
			}
		}
	}

	return false
}

// predeclaredImplementors returns the predeclared types that implement
// a predeclared trait by their structure, as the checker decides it, in
// the order builtin declares them.
func predeclaredImplementors(path, name string) []Implementor {
	if path != BuiltinPath {
		return nil
	}

	obj, ok := types.Universe.Lookup(name).(*types.TypeName)
	b := predeclared()
	if !ok || b == nil {
		return nil
	}

	trait, ok := obj.Type().(*types.Trait)
	if !ok {
		return nil
	}

	var out []Implementor
	forDecls(b.src, func(_ *ast.File, d ast.Decl) {
		t, ok := d.(*ast.TypeDecl)
		if !ok {
			return
		}

		if tn, ok := types.Universe.Lookup(t.Name.Name).(*types.TypeName); ok && types.Implements(tn.Type(), trait) {
			out = append(out, Implementor{Name: t.Name.Name})
		}
	})

	return out
}
