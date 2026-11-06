// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package importer loads the packages a program imports, as
// go/importer serves go/types: a standard package from the copy std
// embeds in the compiler, and a package of the program's own from the
// directory its import path names under the program's module
// (_docs/design/08-packages.md), each parsed and checked once.
package importer

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/scanner"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
	"github.com/snello-lang/snello/std"
)

// A Package is one source file of a library package as loaded: its
// import path, the file and its syntax, and the package the checker
// built from all of its files. A package of several files is several
// entries, in the order of their names, each the backends' unit.
type Package struct {
	Path   string
	File   *token.File
	Syntax *ast.File
	Pkg    *types.Package
}

// An Importer loads the packages a program imports, each once,
// checking them into the Info the program is checked with, so that one
// Info describes the whole program. It is the types.Importer of the
// Config the program is checked under. Module is the module the
// program is in, found by FindModule, or nil for a program with none,
// which imports the standard library alone.
type Importer struct {
	Module *Module

	std     types.Config // the standard library is checked with its privileges
	own     types.Config // a package of the program's own without them
	info    *types.Info
	loaded  map[string]*Package
	loading []loading          // the packages being loaded, outermost first, for naming a cycle
	modules map[string]*Module // the module of each directory asked about
	order   []*Package         // as the loads finished: a package after the ones it imports
}

// A loading is a package whose imports are being loaded: its path, and
// whether it is the standard library's, whose imports the standard
// library alone answers.
type loading struct {
	path string
	std  bool
}

// NewImporter returns an importer that checks the packages it loads
// into info.
func NewImporter(info *types.Info) *Importer {
	imp := &Importer{info: info, loaded: make(map[string]*Package), modules: make(map[string]*Module)}
	imp.std = types.Config{Importer: imp, Standard: true}
	imp.own = types.Config{Importer: imp}
	return imp
}

// Import loads the package at importPath as a file of the program's
// own directory would, and returns it, or says why it could not: no
// package has the path, the imports form a cycle, or the package's
// source has errors.
func (imp *Importer) Import(importPath string) (*types.Package, error) {
	dir := ""
	if imp.Module != nil {
		dir = imp.Module.Dir
	}

	return imp.ImportFrom(importPath, dir)
}

// ImportFrom loads the package at importPath for a file in dir: the
// standard library answers first, then the module dir is in, then that
// module's require lines. A path none of them has is refused naming
// the three.
func (imp *Importer) ImportFrom(importPath, dir string) (*types.Package, error) {
	if p := imp.loaded[importPath]; p != nil {
		return p.Pkg, nil
	}

	for i, l := range imp.loading {
		if l.path == importPath {
			cycle := make([]string, 0, len(imp.loading)-i+1)
			for _, l := range imp.loading[i:] {
				cycle = append(cycle, l.path)
			}

			return nil, errors.New("import cycle: " + strings.Join(append(cycle, importPath), " imports "))
		}
	}

	if importPath == "builtin" {
		return nil, errors.New("package builtin is not imported: its names are predeclared")
	}

	if err := checkImportPath(importPath); err != nil {
		return nil, err
	}

	name := importPath + "/" + path.Base(importPath) + ".snello"
	if src, err := std.Sources.ReadFile(name); err == nil {
		return imp.load(importPath, []string{name}, [][]byte{src}, &imp.std)
	}

	var m *Module
	var err error
	if n := len(imp.loading); n > 0 && imp.loading[n-1].std {
		m = nil // dir is a directory of the embedded library, not one on disk
	} else if m, err = imp.moduleFor(dir); err != nil {
		return nil, err
	}

	if m == nil {
		return nil, errors.New("package " + importPath + " is not in the standard library")
	}

	pkgDir, req, ok := m.PackageDir(importPath)
	if !ok {
		return nil, errors.New("package " + importPath + " is not in the standard library, in module " + m.Path + " or in its require lines")
	}

	if req != nil {
		if err := imp.checkRequired(req); err != nil {
			return nil, err
		}
	}

	files, err := sourceFiles(pkgDir)
	if err != nil || len(files) == 0 {
		return nil, errors.New("no package at " + pkgDir + " for import " + importPath)
	}

	srcs := make([][]byte, len(files))
	for i, name := range files {
		if srcs[i], err = os.ReadFile(name); err != nil {
			return nil, err
		}
	}

	pkg, err := imp.load(importPath, files, srcs, &imp.own)
	if err != nil {
		return nil, err
	}

	if pkg.Name() == "main" {
		return nil, errors.New("import " + strconv.Quote(importPath) + " is a program, not an importable package")
	}

	return pkg, nil
}

// Packages returns the packages loaded so far, each after the ones it
// imports, for the backend to compile with the program.
func (imp *Importer) Packages() []*Package { return imp.order }

// Paths returns the import path of every package in the library.
func Paths() []string {
	var paths []string
	fs.WalkDir(std.Sources, ".", func(name string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(name, ".snello") {
			paths = append(paths, path.Dir(name))
		}

		return nil
	})

	return paths
}

// load parses and checks the files of one package under conf, as the
// package at importPath, and records them. The files are parsed into
// one FileSet, so that an error in any of them names its file.
func (imp *Importer) load(importPath string, names []string, srcs [][]byte, conf *types.Config) (*types.Package, error) {
	imp.loading = append(imp.loading, loading{importPath, conf.Standard})
	defer func() { imp.loading = imp.loading[:len(imp.loading)-1] }()
	fset := token.NewFileSet()
	syntax := make([]*ast.File, len(names))
	var errs scanner.ErrorList
	for i, name := range names {
		var err error
		if syntax[i], err = parser.Parse(fset.AddFile(name, len(srcs[i])), srcs[i]); err != nil {
			errs = append(errs, err.(scanner.ErrorList)...)
		}
	}

	var pkg *types.Package
	var err error = errs.Err()
	if err == nil {
		pkg, err = conf.CheckFiles(importPath, fset.Files(), syntax, imp.info)
	}

	if err != nil {
		return nil, errors.New("package " + importPath + " does not compile:\n" + err.Error())
	}

	for i, f := range syntax {
		p := &Package{importPath, fset.Files()[i], f, pkg}
		if i == 0 {
			imp.loaded[importPath] = p
		}

		imp.order = append(imp.order, p)
	}

	return pkg, nil
}

// moduleFor returns the module governing the files in dir: the one
// named by the nearest snello.mod in dir or above it, which lets a
// required module bring its own require lines, or the program's when
// there is none.
func (imp *Importer) moduleFor(dir string) (*Module, error) {
	if dir == "" || dir == "." || imp.Module == nil {
		return imp.Module, nil
	}

	if m, ok := imp.modules[dir]; ok {
		return m, nil
	}

	m, err := FindModule(dir)
	if err != nil {
		return nil, err
	}

	if m == nil || m.Dir == imp.Module.Dir {
		m = imp.Module
	}

	imp.modules[dir] = m
	return m, nil
}

// checkRequired reads the module file of a required directory, when it
// has one of its own, which must name the path the require line gave,
// as the go.mod of a replacement directory must name its module.
func (imp *Importer) checkRequired(r *Require) error {
	m, err := imp.moduleFor(r.Dir)
	if err != nil {
		return err
	}

	if m.Dir == r.Dir && m.Path != r.Path {
		return errors.New(filepath.Join(m.Dir, ModFile) + ": module path is " + m.Path + ", but the directory is required as " + r.Path)
	}

	return nil
}

// sourceFiles returns the .snello files of a directory, in name order.
func sourceFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".snello") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}

	sort.Strings(files)
	return files, nil
}
