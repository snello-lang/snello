// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package importer loads the packages a program imports, as
// go/importer serves go/types: a standard package from the copy std
// embeds in the compiler, and a package of the program's own from the
// directory its import path names under the program's module, each
// parsed and checked once.
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
	"github.com/snello-lang/snello/compiler/platform"
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
	Std    bool     // a package of the standard library, whose files are the compiler's own
}

// A compileError is why a package the program imports does not compile:
// the parser's or the checker's errors, kept whole, so that a caller
// that reads each error's position, the language server's, finds them
// through errors.As, while its message is the one a terminal shows.
type compileError struct {
	path string
	err  error
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

	// Comments reports, of a package's import path, whether its files
	// are parsed with their comments, for snello doc and the examples'
	// runner; nil, as for a build, keeps none.
	Comments func(path string) bool

	// StdDir is a directory holding the standard library's sources as
	// std/ in the compiler's repository does, read in place of the copy
	// embedded in the compiler: how snello test std/NAME in a checkout
	// runs a package's tests against the sources as edited. Empty, the
	// embedded copy is read.
	StdDir string

	// Target is the platform the program is built for, which chooses
	// the files of every package loaded, a standard package's
	// included, by their names: the host's, from NewImporter.
	Target platform.Target

	// Read reads a source file of a package by its name, in place of the
	// disk, which nil reads: the language server's answers from the
	// editor's buffers where a file is open, so that a package is checked
	// as the editor holds it.
	Read func(name string) ([]byte, error)

	// Parsed, when set, is called with each source file the importer
	// parses, the program's and every package's it imports, as it is
	// parsed and before it is checked, a file with errors too: how the
	// language server keeps the syntax of a check. A file of the
	// standard library is named by its path under std/.
	Parsed func(file *token.File, syntax *ast.File)

	fset    *token.FileSet // every file the importer parses, so that a position names one file of the program
	std     types.Config   // the standard library is checked with its privileges
	own     types.Config   // a package of the program's own without them
	info    *types.Info
	loaded  map[string]*Package
	failed  map[string]error // the packages that did not load, with why: loaded once, however many files import them
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
	imp := &Importer{Target: platform.Host(), fset: token.NewFileSet(), info: info, loaded: make(map[string]*Package), failed: make(map[string]error), roots: make(map[string]*root)}
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

	if base, ok := strings.CutSuffix(importPath, TestSuffix); ok && base != "" && !strings.HasSuffix(base, "/") {
		return imp.importTests(base, importPath, dir)
	}

	if p := imp.loaded[importPath]; p != nil && p.Std { // loaded already: the library's, which answers first
		return p.Pkg, nil
	}

	if imp.isStd(importPath) {
		return imp.loadOnce(importPath, true, func() (*types.Package, []*Package, error) {
			want := isSource
			if imp.Tests == importPath { // the package under test: its test files join it
				want = func(name string) bool { return strings.HasSuffix(name, SourceExt) }
			}

			names, srcs, err := imp.stdFiles(importPath, want)
			if err != nil {
				return nil, nil, err
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

// Error returns the package's path and its errors as one message.
func (e *compileError) Error() string {
	return "package " + e.path + " does not compile:\n" + e.err.Error()
}

// Unwrap returns the errors the package was refused for.
func (e *compileError) Unwrap() error { return e.err }

// resolveOwn finds the package of the program's own that importPath
// names from dir: its canonical name, its directory, and the module it
// is a directory of, whose links it carries.
func (imp *Importer) resolveOwn(importPath, dir string) (canonical, pkgDir string, owner *root, err error) {
	var r *root // none for a directory of the embedded library, which is not one on disk
	if n := len(imp.loading); n == 0 || !imp.loading[n-1].std {
		r = imp.rootFor(dir)
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
// testing, is tested without an import cycle. A standard package's is
// loaded with the library's privileges, from the embedded copy or from
// StdDir.
func (imp *Importer) importTests(base, importPath, dir string) (*types.Package, error) {
	if imp.isStd(base) {
		return imp.loadOnce(importPath, true, func() (*types.Package, []*Package, error) {
			names, srcs, err := imp.stdFiles(base, IsTestFile)
			if err != nil {
				return nil, nil, err
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

	return imp.loadOnce(canonical+TestSuffix, false, func() (*types.Package, []*Package, error) {
		tests, err := TestFiles(pkgDir, imp.Target)
		if err != nil {
			return nil, nil, err
		}

		if len(tests) == 0 {
			return nil, nil, errors.New("package " + importPath + " has no test files at " + pkgDir)
		}

		srcs := make([][]byte, len(tests))
		for i, name := range tests {
			if srcs[i], err = imp.readFile(name); err != nil {
				return nil, nil, err
			}
		}

		return imp.load(canonical+TestSuffix, tests, srcs, nil, owner.module.Links, &imp.own)
	})
}

// readFile reads a package's source file name through Read, or from the
// disk when there is none.
func (imp *Importer) readFile(name string) ([]byte, error) {
	return readSource(name, imp.Read)
}

// loadOnce answers an import from the packages loaded, refuses a cycle
// with the cycle written out, and otherwise loads the package named
// path with load and records it once it compiled and passed the
// checks load makes, so that a package refused is never answered as
// loaded. A package that did not load is answered with why, as it was
// the first time: loaded again for every file that imports it, a
// package deep in a chain of failing packages would be loaded as many
// times as the files above it multiply.
func (imp *Importer) loadOnce(path string, std bool, load func() (*types.Package, []*Package, error)) (*types.Package, error) {
	if p := imp.loaded[path]; p != nil {
		return p.Pkg, nil
	}

	if err := imp.failed[path]; err != nil {
		return nil, err
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
		imp.failed[path] = err
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
	files, err := SourceFiles(pkgDir, imp.Target)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}

	if len(files) == 0 {
		return nil, nil, errors.New("no package at " + pkgDir + " for import " + importPath)
	}

	if canonical == imp.Tests { // the package under test: its test files join it
		tests, err := TestFiles(pkgDir, imp.Target)
		if err != nil {
			return nil, nil, err
		}

		files = append(files, tests...)
	}

	srcs := make([][]byte, len(files))
	for i, name := range files {
		if srcs[i], err = imp.readFile(name); err != nil {
			return nil, nil, err
		}
	}

	cfiles, err := cFiles(pkgDir, imp.Target)
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

// Paths returns the import path of every package in the library,
// once each: every directory holding the file named after it, as
// isStd reads a package.
func Paths() []string {
	var paths []string
	fs.WalkDir(std.Sources, ".", func(name string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && path.Base(name) == path.Base(path.Dir(name))+SourceExt {
			paths = append(paths, path.Dir(name))
		}

		return nil
	})

	return paths
}

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

// isStd reports whether the standard library has a package at
// importPath: a directory holding the file named after it, NAME.snello,
// which every standard package has, so that a directory of the library
// that only holds packages, encoding, is not taken for one.
func (imp *Importer) isStd(importPath string) bool {
	name := importPath + "/" + path.Base(importPath) + SourceExt
	var err error
	if imp.StdDir != "" {
		_, err = os.Stat(filepath.Join(imp.StdDir, filepath.FromSlash(name)))
	} else {
		_, err = fs.Stat(std.Sources, name)
	}

	return err == nil
}

// stdFiles reads the files of the standard package at importPath that
// want takes and that are built for the importer's target, as a
// package of the program's own is read, with their names under std/.
func (imp *Importer) stdFiles(importPath string, want func(string) bool) ([]string, [][]byte, error) {
	entries, err := imp.stdDir(importPath)
	if err != nil {
		return nil, nil, err
	}

	names, _ := choose(entries, want, imp.Target)
	srcs := make([][]byte, len(names))
	for i, name := range names {
		names[i] = importPath + "/" + name
		if srcs[i], err = imp.stdFile(names[i]); err != nil {
			return nil, nil, err
		}
	}

	return names, srcs, nil
}

// load parses and checks the files of one package under conf, as the
// package named path, and returns it with one entry per file, for the
// caller to record.
func (imp *Importer) load(path string, names []string, srcs [][]byte, cfiles, links []string, conf *types.Config) (*types.Package, []*Package, error) {
	files, syntax, err := imp.parseFiles(names, srcs, imp.Comments != nil && imp.Comments(path))
	if err == nil {
		err = checkTestImports(names, syntax, conf.Standard)
	}

	var pkg *types.Package
	if err == nil {
		pkg, err = conf.CheckFiles(path, files, syntax, imp.info)
	}

	if err != nil {
		return nil, nil, &compileError{path, err}
	}

	entries := make([]*Package, len(syntax))
	for i, f := range syntax {
		entries[i] = &Package{path, files[i], f, pkg, cfiles, links, conf.Standard}
	}

	return pkg, entries, nil
}

// rootFor returns the module governing the files in dir, with the id
// its packages are named under: the program's module for a directory
// under its root, or the required module a directory is under, which
// enter recorded when the require was followed. A module file deeper
// in either tree is not looked for. A program with no module has none.
func (imp *Importer) rootFor(dir string) *root {
	if imp.Module == nil {
		return nil
	}

	own := canonDir(imp.Module.Dir)
	if r, ok := imp.roots[own]; !ok || r == nil {
		imp.roots[own] = &root{imp.Module, ""}
	}

	for d := canonDir(dir); ; d = filepath.Dir(d) {
		if r, ok := imp.roots[d]; ok {
			return r
		}

		if parent := filepath.Dir(d); parent == d {
			return imp.roots[own] // outside every tree reached: the program's own rules
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

	if sel := imp.Module.selection; sel != nil { // the versions the program selected, for the module's requires by version
		for i := range m.Requires {
			if m.Requires[i].URL != "" {
				m.Requires[i].Dir = sel.dirs[m.Requires[i].URL]
			}
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
