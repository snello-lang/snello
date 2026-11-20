// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/doc"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
	"github.com/snello-lang/snello/std"
)

const (
	// docArgs is what doc takes: a package, a name, or both.
	docArgs = "[PACKAGE|NAME|PACKAGE.NAME]"

	// The flags of doc.
	allFlag         = "all"
	unexportedFlag  = "u"
	allUsage        = "every name with its comment, not only the overview"
	unexportedUsage = "the unexported names too"

	// The beginning of an argument that names a directory and not an
	// import path, which is also how a hidden directory's name begins,
	// and what parts an import path's elements and a name's.
	dirPrefix     = "."
	ignoredPrefix = "_"
	testdataDir   = "testdata"
	parentDir     = ".."
	pathSeparator = "/"
	nameSeparator = "."
)

// declareDoc declares doc's flags and returns its work: the package or
// the name its argument gives, printed on stdout, the links that name
// nothing warned of on stderr.
func declareDoc(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
	all := fs.Bool(allFlag, false, allUsage)
	unexported := fs.Bool(unexportedFlag, false, unexportedUsage)
	serve := &httpAddr{}
	fs.Var(serve, httpFlag, httpUsage)
	return func(args []string) int {
		if serve.set {
			if len(args) > 0 {
				fmt.Fprintf(stderr, "%s %s: -http serves every package and takes no %s\n", program, docCommand, args[0])
				return exitUsage
			}

			return serveDoc(serve.addr, *unexported, stdout, stderr)
		}

		arg := ""
		if len(args) > 0 {
			arg = args[0]
		}

		l, err := newDocLoader(*unexported)
		if err != nil {
			fmt.Fprintf(stderr, "%s %s: %v\n", program, docCommand, err)
			return exitFailure
		}

		p, name, err := l.find(arg)
		if err != nil {
			fmt.Fprintf(stderr, "%s %s: %v\n", program, docCommand, err)
			return exitFailure
		}

		for _, b := range p.Broken {
			fmt.Fprintf(stderr, "%s: [%s] names nothing\n", b.Pos, b.Name)
		}

		if name == "" {
			p.WritePackage(stdout, *all)
			return exitSuccess
		}

		if t := p.Type(name); t != nil && isTrait(t) {
			t.Implementors = doc.Implementors(p.Path, name, l.everyPackage(), *unexported)
		}

		if !p.WriteName(stdout, name) {
			fmt.Fprintf(stderr, "%s %s: no %s in package %s\n", program, docCommand, name, p.Path)
			return exitFailure
		}

		return exitSuccess
	}
}

// A docLoader loads the packages doc prints, every one with its
// comments, through an importer of the current directory's module.
type docLoader struct {
	imp        *importer.Importer
	module     *importer.Module
	dir        string // the directory imports are found from: the module's, or the current one
	unexported bool
	sources    map[string]doc.Source // the packages loaded, by the path asked for
}

func newDocLoader(unexported bool) (*docLoader, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	m, err := importer.FindModule(cwd)
	if err != nil {
		return nil, err
	}

	if m != nil {
		if err := m.Resolve(); err != nil {
			return nil, err
		}
	}

	return loaderFor(m, cwd, unexported), nil
}

// loaderFor returns a loader of the module m, already resolved, or of
// no module, its imports found from cwd then: a fresh importer, so that
// what it loads is the packages as they are on disk now.
func loaderFor(m *importer.Module, cwd string, unexported bool) *docLoader {
	l := &docLoader{dir: cwd, unexported: unexported, module: m, sources: map[string]doc.Source{}}
	if m != nil {
		l.dir = m.Dir
	}

	l.imp = importer.NewImporter(&types.Info{})
	l.imp.Module = m
	l.imp.Comments = func(string) bool { return true }
	return l
}

// find returns the package an argument names and the name in it, or ""
// for the package itself: a directory; builtin; an import path, or the
// last element of one standard package's path, with a name after a dot;
// or a name alone, in the current directory's package, else in the one
// standard package that has it.
func (l *docLoader) find(arg string) (*doc.Package, string, error) {
	if arg == "" || strings.HasPrefix(arg, dirPrefix) || filepath.IsAbs(arg) {
		return l.dirPackage(arg)
	}

	pkgPart, name := splitDocArg(arg)
	if pkgPart == doc.BuiltinPath {
		p, err := doc.Builtin()
		return l.named(p, name, err)
	}

	if l.isPackage(pkgPart) {
		p, err := l.load(pkgPart)
		return l.named(p, name, err)
	}

	switch paths := l.byLastElement(pkgPart); len(paths) {
	case 0:
	case 1:
		p, err := l.load(paths[0])
		return l.named(p, name, err)
	default:
		return nil, "", fmt.Errorf("%s is the last element of %s: name one by its path", pkgPart, joinNames(paths))
	}

	return l.search(arg)
}

// named checks that the package has the name asked for.
func (l *docLoader) named(p *doc.Package, name string, err error) (*doc.Package, string, error) {
	if err != nil || name == "" || has(p, name) {
		return p, name, err
	}

	return nil, "", fmt.Errorf("no %s in package %s", name, p.Path)
}

// search looks for a name with no package in the current directory's
// package, then in every standard package, and is an error naming each
// package that has it when several do.
func (l *docLoader) search(name string) (*doc.Package, string, error) {
	if p, _, err := l.dirPackage(currentDir); err == nil && has(p, name) {
		return p, name, nil
	}

	var found []*doc.Package
	for _, path := range importer.Paths() { // builtin among them
		var p *doc.Package
		var err error
		if path == doc.BuiltinPath {
			p, err = doc.Builtin()
		} else {
			p, err = l.load(path)
		}

		if err == nil && has(p, name) {
			found = append(found, p)
		}
	}

	switch len(found) {
	case 0:
		return nil, "", fmt.Errorf("no package or name %s", name)
	case 1:
		return found[0], name, nil
	}

	paths := make([]string, len(found))
	for i, p := range found {
		paths[i] = p.Path
	}

	return nil, "", fmt.Errorf("%s is in %s", name, joinNames(paths))
}

// joinNames joins names as a sentence lists them: a, b and c.
func joinNames(names []string) string {
	if len(names) == 1 {
		return names[0]
	}

	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// dirPackage loads the package in dir: by its path under its module's
// root, as an import loads it, or, at the root or under no module, as a
// program.
func (l *docLoader) dirPackage(dir string) (*doc.Package, string, error) {
	if dir == "" {
		dir = currentDir
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, "", err
	}

	if l.module != nil {
		rel, err := filepath.Rel(l.module.Dir, abs)
		if err == nil && rel != currentDir && !strings.HasPrefix(rel, parentDir) { // under the root, not the root itself
			p, err := l.load(filepath.ToSlash(rel))
			return p, "", err
		}
	}

	names, err := importer.SourceFiles(abs, platform.Host())
	if err != nil {
		return nil, "", err
	}

	srcs := make([][]byte, len(names))
	for i, name := range names {
		if srcs[i], err = os.ReadFile(name); err != nil {
			return nil, "", err
		}
	}

	_, prog, err := importer.LoadProgramFrom(importer.Load{Names: names, Srcs: srcs, Comments: true}, &types.Info{})
	if err != nil {
		return nil, "", err
	}

	src := doc.Source{Pkg: prog.Pkg, Unexported: l.unexported, Import: l.importPkg, Tests: testFilesIn(abs)}
	for i, f := range prog.Syntax {
		src.Files = append(src.Files, doc.File{Syntax: f, Tok: prog.Files[i]})
	}

	return doc.New(src), "", nil
}

// load loads the package at an import path with its comments.
func (l *docLoader) load(path string) (*doc.Package, error) {
	pkg, err := l.imp.ImportFrom(path, l.dir)
	if err != nil {
		return nil, err
	}

	src := doc.Source{Path: path, Pkg: pkg, Unexported: l.unexported, Import: l.importPkg, Tests: l.testFiles(path)}
	for _, e := range l.imp.Packages() {
		if e.Pkg == pkg {
			src.Files = append(src.Files, doc.File{Syntax: e.Syntax, Tok: e.File})
		}
	}

	l.sources[path] = src
	return doc.New(src), nil
}

// testFiles returns the test files of the package at an import path,
// parsed with their comments for the examples a page shows: a standard
// package's from the library the compiler embeds, a package of the
// module's, or of a dependency's, from its directory.
func (l *docLoader) testFiles(path string) []doc.TestFile {
	if slices.Contains(importer.Paths(), path) {
		entries, err := std.Sources.ReadDir(path)
		if err != nil {
			return nil
		}

		var out []doc.TestFile
		for _, e := range entries {
			if name := path + pathSeparator + e.Name(); importer.IsTestFile(e.Name()) && importer.Chosen(e.Name(), platform.Host()) {
				if src, err := std.Sources.ReadFile(name); err == nil {
					out = appendTestFile(out, name, src)
				}
			}
		}

		return out
	}

	if l.module == nil {
		return nil
	}

	dir := filepath.Join(l.module.Dir, filepath.FromSlash(path))
	first, rest, _ := strings.Cut(path, pathSeparator)
	for _, r := range l.module.Requires {
		if r.Name == first {
			dir = filepath.Join(r.Dir, filepath.FromSlash(rest))
		}
	}

	return testFilesIn(dir)
}

// testFilesIn returns the test files of the package in dir, parsed with
// their comments.
func testFilesIn(dir string) []doc.TestFile {
	names, err := importer.TestFiles(dir, platform.Host())
	if err != nil {
		return nil
	}

	var out []doc.TestFile
	for _, name := range names {
		if src, err := os.ReadFile(name); err == nil {
			out = appendTestFile(out, name, src)
		}
	}

	return out
}

// appendTestFile parses a test file with its comments and appends it;
// one that does not parse shows no examples.
func appendTestFile(out []doc.TestFile, name string, src []byte) []doc.TestFile {
	file := token.NewFile(name, len(src))
	f, err := parser.ParseComments(file, src)
	if err != nil {
		return out
	}

	return append(out, doc.TestFile{Syntax: f, Tok: file, Src: src})
}

// importPkg loads the package a link names, or nil when none has its
// path.
func (l *docLoader) importPkg(path string) *types.Package {
	pkg, err := l.imp.ImportFrom(path, l.dir)
	if err != nil {
		return nil
	}

	return pkg
}

// isPackage reports whether path is a standard package or one of the
// module's: a directory under its root, or a dependency's name and a
// directory under it.
func (l *docLoader) isPackage(path string) bool {
	if slices.Contains(importer.Paths(), path) {
		return true
	}

	if l.module == nil {
		return false
	}

	first, _, _ := strings.Cut(path, pathSeparator)
	for _, r := range l.module.Requires {
		if r.Name == first {
			return true
		}
	}

	return isDirNamed(l.module.Dir, path)
}

// isDirNamed reports whether path, of elements parted by slashes, names
// a directory under root with each element's case as written, so that
// geo is no Geo on a system whose files ignore case.
func isDirNamed(root, path string) bool {
	dir := root
	for elem := range strings.SplitSeq(path, pathSeparator) {
		entries, err := os.ReadDir(dir)
		if err != nil || !slices.ContainsFunc(entries, func(e os.DirEntry) bool { return e.IsDir() && e.Name() == elem }) {
			return false
		}

		dir = filepath.Join(dir, elem)
	}

	return true
}

// byLastElement returns the standard packages whose path ends in elem,
// encoding/json for json.
func (l *docLoader) byLastElement(elem string) []string {
	var found []string
	for _, path := range importer.Paths() {
		if rest, ok := strings.CutSuffix(path, elem); ok && strings.HasSuffix(rest, pathSeparator) {
			found = append(found, path)
		}
	}

	return found
}

// everyPackage returns every standard package and every package of the
// module, for the implementors of a trait: each loaded once.
func (l *docLoader) everyPackage() []doc.Source {
	paths := slices.Clone(importer.Paths())
	if l.module != nil {
		paths = append(paths, l.modulePaths()...)
	}

	var out []doc.Source
	for _, path := range paths {
		if path == doc.BuiltinPath {
			continue
		}

		if _, ok := l.sources[path]; !ok {
			if _, err := l.load(path); err != nil {
				continue // a package that does not compile implements nothing here
			}
		}

		out = append(out, l.sources[path])
	}

	return out
}

// modulePaths returns the import paths of the module's packages: each
// directory under its root that holds a package for the host, the root
// itself, a program, aside.
func (l *docLoader) modulePaths() []string {
	var paths []string
	filepath.WalkDir(l.module.Dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}

		if path != l.module.Dir && (strings.HasPrefix(d.Name(), dirPrefix) || strings.HasPrefix(d.Name(), ignoredPrefix) || d.Name() == testdataDir) {
			return filepath.SkipDir // a hidden directory, one a build leaves out, or test data
		}

		if names, err := importer.SourceFiles(path, platform.Host()); path != l.module.Dir && err == nil && len(names) > 0 {
			rel, _ := filepath.Rel(l.module.Dir, path)
			paths = append(paths, filepath.ToSlash(rel))
		}

		return nil
	})

	return paths
}

// splitDocArg splits an argument into a package and a name: the name is
// what follows the first dot after the last slash, so that
// unicode/utf8.RuneLen is utf8's RuneLen and strings.Builder.Len is the
// method of strings' Builder.
func splitDocArg(arg string) (pkgPart, name string) {
	slash := strings.LastIndex(arg, pathSeparator)
	dot := strings.Index(arg[slash+1:], nameSeparator)
	if dot < 0 {
		return arg, ""
	}

	return arg[:slash+1+dot], arg[slash+1+dot+1:]
}

// has reports whether a package's pages have a name.
func has(p *doc.Package, name string) bool { return p.WriteName(io.Discard, name) }

// isTrait reports whether a type of the page is a trait.
func isTrait(t *doc.Type) bool {
	_, ok := t.Decl.(*ast.TraitDecl)
	return ok
}
