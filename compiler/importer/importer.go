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
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
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
	CFiles []string // the .c and .h files of the package's directory, its C side; none for a standard package, whose C side is the runtime
	Links  []string // the system libraries the package's module links, from its link lines
}

// An Importer loads the packages a program imports, each once,
// checking them into the Info the program is checked with, so that one
// Info describes the whole program. It is the types.Importer of the
// Config the program is checked under. Module is the module the
// program is in, found by FindModule, or nil for a program with none,
// which imports the standard library alone.
type Importer struct {
	Module *Module

	Tests string // the path of the package under test, whose test files are loaded with it, or ""

	// StdDir is a directory holding the standard library's sources as
	// std/ in the compiler's repository does, read in place of the copy
	// embedded in the compiler: how snello test std/NAME in a checkout
	// runs a package's tests against the sources as edited. Empty, the
	// embedded copy is read.
	StdDir string

	std     types.Config // the standard library is checked with its privileges
	own     types.Config // a package of the program's own without them
	info    *types.Info
	loaded  map[string]*Package
	loading []loading        // the packages being loaded, outermost first, for naming a cycle
	roots   map[string]*root // the modules reached so far, by directory
	order   []*Package       // as the loads finished: a package after the ones it imports
}

// A loading is a package whose imports are being loaded: its path, and
// whether it is the standard library's, whose imports the standard
// library alone answers.
type loading struct {
	path string
	std  bool
}

// A root is a module the importer has reached, with the id the paths
// of its packages carry: none for the program's module, and for a
// required module the require name that first reached it, dep, with a
// numeral after a ~ when a module reached earlier took that name,
// dep~2. A module is one whatever route reaches it, since the roots
// are keyed by directory: a dependency two dependencies require is
// loaded once and its types are one type, where a name made of the
// route, b/d and c/d, loaded it twice. The id is what keeps the
// program's util and a dependency's util two packages.
type root struct {
	module *Module
	id     string
}

// NewImporter returns an importer that checks the packages it loads
// into info.
func NewImporter(info *types.Info) *Importer {
	imp := &Importer{info: info, loaded: make(map[string]*Package), roots: make(map[string]*root)}
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
// standard library answers first, then the directories of the module
// dir is in, then that module's require lines. A package of a required
// module is named by the module's id and its directory under it,
// dep/util, so that the program's util and the dependency's are two
// packages with two symbol prefixes, and the same package whichever
// require line reaches its module. A path none of the three has is
// refused naming the three. A path ending in _test names the external
// test package of the package before it, importTests, which only a
// test file may import; the suffix is reserved, as Go reserves it.
func (imp *Importer) ImportFrom(importPath, dir string) (*types.Package, error) {
	if importPath == "builtin" {
		return nil, errors.New("package builtin is not imported: its names are predeclared")
	}

	if err := checkImportPath(importPath); err != nil {
		return nil, err
	}

	if base, ok := strings.CutSuffix(importPath, "_test"); ok && base != "" && !strings.HasSuffix(base, "/") {
		return imp.importTests(base, importPath, dir)
	}

	name := importPath + "/" + path.Base(importPath) + ".snello"
	if src, err := imp.stdFile(name); err == nil {
		return imp.loadOnce(importPath, true, func() (*types.Package, []*Package, error) {
			names, srcs := []string{name}, [][]byte{src}
			if imp.Tests == importPath { // the package under test: its test files join it
				entries, _ := imp.stdDir(importPath)
				for _, e := range entries {
					if IsTestFile(e.Name()) {
						test, err := imp.stdFile(importPath + "/" + e.Name())
						if err != nil {
							return nil, nil, err
						}

						names, srcs = append(names, importPath+"/"+e.Name()), append(srcs, test)
					}
				}
			}

			return imp.load(importPath, names, srcs, nil, nil, &imp.std)
		})
	}

	canonical, pkgDir, owner, err := imp.resolveOwn(importPath, dir)
	if err != nil {
		return nil, err
	}

	return imp.loadOnce(canonical, false, func() (*types.Package, []*Package, error) {
		return imp.loadDir(canonical, importPath, pkgDir, owner.module.Links)
	})
}

// resolveOwn finds the package of the program's own that importPath
// names from dir: its canonical name, its directory, and the module it
// is a directory of, whose links it carries.
func (imp *Importer) resolveOwn(importPath, dir string) (canonical, pkgDir string, owner *root, err error) {
	var r *root
	if n := len(imp.loading); n > 0 && imp.loading[n-1].std {
		r = nil // dir is a directory of the embedded library, not one on disk
	} else if r, err = imp.rootFor(dir); err != nil {
		return "", "", nil, err
	}

	if r == nil {
		return "", "", nil, errors.New("package " + importPath + " is not in the standard library, and no snello.mod above the program marks a module")
	}

	pkgDir, req, err := r.module.Lookup(importPath)
	if err != nil {
		return "", "", nil, err
	}

	if pkgDir == "" {
		return "", "", nil, errors.New("package " + importPath + " is not in the standard library, in the module's directories or in its require lines")
	}

	canonical = importPath
	if r.id != "" {
		canonical = r.id + "/" + importPath
	}

	owner = r // the module the package is a directory of, whose links it carries
	if req != nil {
		if owner, err = imp.enter(req); err != nil {
			return "", "", nil, err
		}

		canonical = owner.id
		if inner := strings.TrimPrefix(strings.TrimPrefix(importPath, req.Name), "/"); inner != "" {
			canonical += "/" + inner
		}
	}

	return canonical, pkgDir, owner, nil
}

// importTests loads the external test package of base, named
// base_test: the test files of base's directory whose package clause
// is base_test, a package of their own that imports base as its users
// do, so that a package the test harness itself depends on, sync under
// testing, is tested without an import cycle (_docs/design/09-testing.md,
// section 1). A standard package's is loaded with the library's
// privileges, from the embedded copy or from StdDir.
func (imp *Importer) importTests(base, importPath, dir string) (*types.Package, error) {
	if _, err := imp.stdFile(base + "/" + path.Base(base) + ".snello"); err == nil {
		return imp.loadOnce(importPath, true, func() (*types.Package, []*Package, error) {
			entries, err := imp.stdDir(base)
			if err != nil {
				return nil, nil, err
			}

			var names []string
			var srcs [][]byte
			for _, e := range entries {
				if IsTestFile(e.Name()) {
					src, err := imp.stdFile(base + "/" + e.Name())
					if err != nil {
						return nil, nil, err
					}

					names, srcs = append(names, base+"/"+e.Name()), append(srcs, src)
				}
			}

			if len(names) == 0 {
				return nil, nil, errors.New("package " + importPath + " has no test files under " + base)
			}

			return imp.load(importPath, names, srcs, nil, nil, &imp.std)
		})
	}

	canonical, pkgDir, owner, err := imp.resolveOwn(base, dir)
	if err != nil {
		return nil, errors.New("package " + importPath + " names the tests of " + base + ", and " + err.Error())
	}

	return imp.loadOnce(canonical+"_test", false, func() (*types.Package, []*Package, error) {
		tests, err := TestFiles(pkgDir)
		if err != nil {
			return nil, nil, err
		}

		if len(tests) == 0 {
			return nil, nil, errors.New("package " + importPath + " has no test files at " + pkgDir)
		}

		srcs := make([][]byte, len(tests))
		for i, name := range tests {
			if srcs[i], err = os.ReadFile(name); err != nil {
				return nil, nil, err
			}
		}

		return imp.load(canonical+"_test", tests, srcs, nil, owner.module.Links, &imp.own)
	})
}

// loadOnce answers an import from the packages loaded, refuses a cycle
// with the cycle written out, and otherwise loads the package named
// path with load and records it once it compiled and passed the
// checks load makes, so that a package refused is never answered from
// the cache.
func (imp *Importer) loadOnce(path string, std bool, load func() (*types.Package, []*Package, error)) (*types.Package, error) {
	if p := imp.loaded[path]; p != nil {
		return p.Pkg, nil
	}

	for i, l := range imp.loading {
		if l.path == path {
			cycle := make([]string, 0, len(imp.loading)-i+1)
			for _, l := range imp.loading[i:] {
				cycle = append(cycle, l.path)
			}

			return nil, errors.New("import cycle: " + strings.Join(append(cycle, path), " imports "))
		}
	}

	imp.loading = append(imp.loading, loading{path, std})
	defer func() { imp.loading = imp.loading[:len(imp.loading)-1] }()
	pkg, entries, err := load()
	if err != nil {
		return nil, err
	}

	imp.loaded[path] = entries[0]
	imp.order = append(imp.order, entries...)
	return pkg, nil
}

// loadDir loads the package of the program's own at pkgDir, imported
// as importPath and named canonical: its .snello files, its C side
// when it has one, the libraries its module links, and the refusals of
// a program, of a bound package with no .c file to define what it
// declares, and of a file named as the header the compiler writes.
func (imp *Importer) loadDir(canonical, importPath, pkgDir string, links []string) (*types.Package, []*Package, error) {
	files, err := SourceFiles(pkgDir)
	if err != nil || len(files) == 0 {
		return nil, nil, errors.New("no package at " + pkgDir + " for import " + importPath)
	}

	if canonical == imp.Tests { // the package under test: its test files join it
		tests, err := TestFiles(pkgDir)
		if err != nil {
			return nil, nil, err
		}

		files = append(files, tests...)
	}

	srcs := make([][]byte, len(files))
	for i, name := range files {
		if srcs[i], err = os.ReadFile(name); err != nil {
			return nil, nil, err
		}
	}

	cfiles, err := cFiles(pkgDir)
	if err != nil {
		return nil, nil, err
	}

	hasC := false
	for _, f := range cfiles {
		if strings.HasSuffix(f, ".c") {
			hasC = true
		}

		if filepath.Base(f) == path.Base(importPath)+".snello.h" {
			return nil, nil, errors.New("package " + importPath + ": " + filepath.Base(f) + " is the header the compiler writes for the package; rename it")
		}
	}

	pkg, entries, err := imp.load(canonical, files, srcs, cfiles, links, &imp.own)
	if err != nil {
		return nil, nil, err
	}

	if pkg.Name() == "main" {
		return nil, nil, errors.New("import " + strconv.Quote(importPath) + " is a program, not an importable package")
	}

	if name := bound(entries); name != "" && !hasC {
		return nil, nil, errors.New("package " + importPath + " declares " + name + " without a body and has no .c file beside it to define it")
	}

	return pkg, entries, nil
}

// bound returns the name of a function the package declares without a
// body, in any of its files, or "" when it has none.
func bound(entries []*Package) string {
	for _, p := range entries {
		for _, d := range p.Syntax.Decls {
			if d, ok := d.(*ast.FuncDecl); ok && d.Body == nil {
				return d.Name.Name
			}
		}
	}

	return ""
}

// Packages returns the packages loaded so far, each after the ones it
// imports, for the backend to compile with the program.
func (imp *Importer) Packages() []*Package { return imp.order }

// stdFile reads a file of the standard library by its path under std/,
// from StdDir when one is set and from the embedded copy otherwise.
func (imp *Importer) stdFile(name string) ([]byte, error) {
	if imp.StdDir != "" {
		return os.ReadFile(filepath.Join(imp.StdDir, filepath.FromSlash(name)))
	}

	return std.Sources.ReadFile(name)
}

// stdDir lists a standard package's directory, as stdFile reads it.
func (imp *Importer) stdDir(importPath string) ([]fs.DirEntry, error) {
	if imp.StdDir != "" {
		return os.ReadDir(filepath.Join(imp.StdDir, filepath.FromSlash(importPath)))
	}

	return fs.ReadDir(std.Sources, importPath)
}

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
// package named path, and returns it with one entry per file, for the
// caller to record. The files are parsed into one FileSet, so that an
// error in any of them names its file.
func (imp *Importer) load(path string, names []string, srcs [][]byte, cfiles, links []string, conf *types.Config) (*types.Package, []*Package, error) {
	fset, syntax, err := parseFiles(names, srcs)
	if err == nil {
		err = checkTestImports(names, syntax, conf.Standard)
	}

	var pkg *types.Package
	if err == nil {
		pkg, err = conf.CheckFiles(path, fset.Files(), syntax, imp.info)
	}

	if err != nil {
		return nil, nil, errors.New("package " + path + " does not compile:\n" + err.Error())
	}

	entries := make([]*Package, len(syntax))
	for i, f := range syntax {
		entries[i] = &Package{path, fset.Files()[i], f, pkg, cfiles, links}
	}

	return pkg, entries, nil
}

// rootFor returns the module governing the files in dir, with the id
// its packages are named under: the program's module for a directory
// under its root, or the required module a directory is under, which
// enter recorded when the require was followed. A module file deeper
// in either tree is not looked for (section 7 of the design note). A
// program with no module has none.
func (imp *Importer) rootFor(dir string) (*root, error) {
	if imp.Module == nil {
		return nil, nil
	}

	own := canonDir(imp.Module.Dir)
	if r, ok := imp.roots[own]; !ok || r == nil {
		imp.roots[own] = &root{imp.Module, ""}
	}

	for d := canonDir(dir); ; d = filepath.Dir(d) {
		if r, ok := imp.roots[d]; ok {
			return r, nil
		}

		if parent := filepath.Dir(d); parent == d {
			return imp.roots[own], nil // outside every tree reached: the program's own rules
		}
	}
}

// canonDir is the one spelling of a directory the roots are keyed by:
// absolute, with symbolic links resolved, so that two require lines
// naming one directory two ways reach one module.
func canonDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}

	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}

	return abs
}

// enter returns the module a require line leads to, recording it the
// first time under the id its packages are named by: the module its
// directory's snello.mod marks, with its own require lines, or the
// directory alone when it has none.
func (imp *Importer) enter(req *Require) (*root, error) {
	dir := canonDir(req.Dir)
	if r, ok := imp.roots[dir]; ok {
		return r, nil
	}

	m := &Module{Dir: req.Dir}
	if src, err := os.ReadFile(filepath.Join(req.Dir, ModFile)); err == nil {
		if m, err = ParseModFile(filepath.Join(req.Dir, ModFile), src); err != nil {
			return nil, err
		}

		if err := m.checkShadows(); err != nil {
			return nil, err
		}
	}

	id := req.Name
	for n := 2; imp.taken(id); n++ {
		id = req.Name + "~" + strconv.Itoa(n) // ~ is no character of an import path, so the id names no directory and no require
	}

	r := &root{m, id}
	imp.roots[dir] = r
	return r, nil
}

// taken reports whether a module reached already carries the id.
func (imp *Importer) taken(id string) bool {
	for _, r := range imp.roots {
		if r != nil && r.id == id {
			return true
		}
	}

	return false
}
