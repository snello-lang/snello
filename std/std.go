// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package std holds the standard library's Teak sources, embedded in
// the compiler so that a build needs no library path, and loads them
// for a program that imports them, as go/importer serves go/types.
// Each package is one directory holding one file named after it,
// math/math.teak, written in Teak; a function it declares without a
// body is the runtime's, under the C name the backend gives the
// function (_docs/design/05-standard-library.md).
package std

import (
	"embed"
	"errors"
	"io/fs"
	"path"
	"strings"

	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/parser"
	"github.com/teak-lang/teak/token"
	"github.com/teak-lang/teak/types"
)

//go:embed */*.teak
var sources embed.FS

// A Package is a library package as loaded: its import path, its
// source file and syntax, and the package the checker built from them.
type Package struct {
	Path   string
	File   *token.File
	Syntax *ast.File
	Pkg    *types.Package
}

// An Importer loads the library packages a program imports, each once,
// checking them into the Info the program is checked with, so that one
// Info describes the whole program. It is the types.Importer of the
// Config the program is checked under.
type Importer struct {
	conf    types.Config
	info    *types.Info
	loaded  map[string]*Package
	loading map[string]bool
	order   []*Package // as the loads finished: a package after the ones it imports
}

// NewImporter returns an importer that checks the packages it loads
// into info.
func NewImporter(info *types.Info) *Importer {
	imp := &Importer{info: info, loaded: make(map[string]*Package), loading: make(map[string]bool)}
	imp.conf.Importer = imp
	return imp
}

// Import loads the package at importPath and returns it, or says why
// it could not: the library has no such package, the imports form a
// cycle, or the package's source has errors, which are the compiler's
// own to fix.
func (imp *Importer) Import(importPath string) (*types.Package, error) {
	if p := imp.loaded[importPath]; p != nil {
		return p.Pkg, nil
	}

	if imp.loading[importPath] {
		return nil, errors.New("import cycle not allowed: " + importPath)
	}

	if importPath == "builtin" {
		return nil, errors.New("package builtin is not imported: its names are predeclared")
	}

	name := importPath + "/" + path.Base(importPath) + ".teak"
	src, err := sources.ReadFile(name)
	if err != nil {
		return nil, errors.New("package " + importPath + " is not in the standard library")
	}

	imp.loading[importPath] = true
	defer delete(imp.loading, importPath)
	f, file, err := parser.ParseFile(name, src)
	var pkg *types.Package
	if err == nil {
		pkg, err = imp.conf.Check(importPath, file, f, imp.info)
	}

	if err != nil {
		return nil, errors.New("package " + importPath + " does not compile:\n" + err.Error())
	}

	p := &Package{importPath, file, f, pkg}
	imp.loaded[importPath] = p
	imp.order = append(imp.order, p)
	return pkg, nil
}

// Packages returns the packages loaded so far, each after the ones it
// imports, for the backend to compile with the program.
func (imp *Importer) Packages() []*Package { return imp.order }

// Paths returns the import path of every package in the library.
func Paths() []string {
	var paths []string
	fs.WalkDir(sources, ".", func(name string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(name, ".teak") {
			paths = append(paths, path.Dir(name))
		}

		return nil
	})

	return paths
}
