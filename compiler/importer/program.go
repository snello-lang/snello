// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package importer

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/compiler/scanner"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// mainPackage is the name of the package a program is.
const mainPackage = "main"

// A Program is the main package as loaded: its source files and their
// syntax, checked as the program with the packages they import, which
// the backends compile with it, and the package the checker made of
// it, which CheckMain looks at.
type Program struct {
	Files  []*token.File
	Syntax []*ast.File
	Pkg    *types.Package
}

// A Place is a place in a source file: the file's name, as the loader
// is given it, and a byte offset in its text.
type Place struct {
	File   string
	Offset int
}

// Options say how a check reads a program for a caller that only reads
// it, the language server as the developer types. Target chooses every
// package's files by their names, the zero Target the host's. Read
// reads every source, the package's and its imports', in place of the
// disk, which nil reads. Cached resolves the module's requirements from
// the module cache and the lock file alone, as Module.ResolveCached
// says: nothing fetched, nothing written. Parsed is called with each
// file the check parses, as Importer.Parsed says; At is the place a
// completion asks about, as Importer.At says; and Recovered checks a
// file that does not parse as the parser recovered it, as
// Importer.Recovered says.
type Options struct {
	Target    platform.Target
	Read      func(name string) ([]byte, error)
	Cached    bool
	Parsed    func(file *token.File, syntax *ast.File)
	At        Place
	Recovered bool
}

// CheckMain reports why the program cannot be built: main is not
// declared in its package, or is declared as something other than a
// function. The package may have any name, since a program is the
// package that declares main, and the examples name theirs after what
// they show. snello check takes a package without main, and does not
// ask.
func (p *Program) CheckMain() error {
	switch p.Pkg.Scope().Lookup("main").(type) {
	case *types.Func:
		return nil
	case nil:
		in := "package " + p.Pkg.Name()
		if p.Pkg.Name() == "main" {
			in = "the main package"
		}

		return errors.New("function main is undeclared in " + in)
	default:
		return errors.New("cannot declare main - must be func")
	}
}

// LoadProgram parses and checks the main package in the source files
// named, which lie in one directory, as snello build does: snello build
// DIR names every .snello file of the directory and snello build FILE
// the one file. The packages they import are found through the nearest
// snello.mod above the directory, each package's files chosen for the
// target t. It returns the importer, which holds
// the packages it loaded, and the program, or the error of the first
// step that failed: no file named, a module file or a source that does
// not read, or the errors of the parser or the checker.
func LoadProgram(names []string, t platform.Target, info *types.Info) (*Importer, *Program, error) {
	return LoadProgramFrom(Load{Names: names, Target: t}, info)
}

// A Load is a program given as sources, named as files for the
// messages and for the module they are found under: what a test build
// gives the loader, with the generated main in memory. Tests is the
// path of the package whose test files are loaded with it, or "";
// DropMain leaves the program's own main out, for a main package
// tested with a generated one, which is the file named by Main and
// keeps its; Standalone loads the program in no module, wherever its
// files are named, as a standard package's tests are. With no Srcs, the
// sources are read through Read, the program's and every package's it
// imports, or from the disk when Read is nil; Cached resolves the
// module as Options.Cached says, Parsed is called with each file
// parsed, as Importer.Parsed says, At is the place a completion asks
// about, as Importer.At says, and Recovered checks a file that does not
// parse as the parser recovered it, as Importer.Recovered says.
type Load struct {
	Names      []string
	Srcs       [][]byte
	Tests      string
	DropMain   bool
	Main       string
	StdDir     string // the standard library's sources read from a directory, as Importer.StdDir
	Standalone bool
	Target     platform.Target // the platform the program is built for, which chooses its packages' files; the zero Target is the host
	Comments   bool            // the program's own files parsed with their comments, for snello doc and the examples' runner
	Read       func(name string) ([]byte, error)
	Cached     bool
	Parsed     func(file *token.File, syntax *ast.File)
	At         Place
	Recovered  bool
}

// LoadProgramFrom loads a program from its sources, as LoadProgram
// loads it from its files.
func LoadProgramFrom(l Load, info *types.Info) (*Importer, *Program, error) {
	if len(l.Names) == 0 {
		return nil, nil, errors.New("no source files to load")
	}

	imp := NewImporter(info)
	imp.Tests = l.Tests
	imp.StdDir = l.StdDir
	imp.Read = l.Read
	imp.Parsed = l.Parsed
	imp.At = l.At
	imp.Recovered = l.Recovered
	if l.Srcs == nil {
		l.Srcs = make([][]byte, len(l.Names))
		for i, name := range l.Names {
			var err error
			if l.Srcs[i], err = imp.readFile(name); err != nil {
				return nil, nil, err
			}
		}
	}

	if l.Target != (platform.Target{}) {
		imp.Target = l.Target
	}
	if !l.Standalone {
		var err error
		if imp.Module, err = FindModule(filepath.Dir(l.Names[0])); err != nil {
			return nil, nil, err
		}

		if imp.Module != nil {
			if err := resolve(imp.Module, l.Cached); err != nil {
				return nil, nil, err
			}
		}
	}

	files, syntax, parseErr := imp.parseFiles(l.Names, l.Srcs, l.Comments)
	if parseErr != nil && !l.Recovered {
		return nil, nil, parseErr
	}

	if err := checkTestImports(l.Names, syntax, false); err != nil {
		return nil, nil, err
	}

	if l.DropMain {
		for i, f := range syntax {
			if l.Names[i] == l.Main {
				continue
			}

			f.Decls = slices.DeleteFunc(f.Decls, func(d ast.Decl) bool {
				fn, ok := d.(*ast.FuncDecl)
				return ok && fn.Recv == nil && fn.Name.Name == "main"
			})
		}
	}

	conf := types.Config{Importer: imp, At: imp.at}
	pkg, err := conf.CheckFiles("", files, syntax, info)
	if parseErr != nil {
		err = parseErr // a file that does not parse reports its syntax errors alone
	}

	if err != nil {
		return nil, nil, err
	}

	return imp, &Program{files, syntax, pkg}, nil
}

// CheckDir checks the package in dir as snello check DIR does: as a
// program when the package of its first file is main, or the directory
// is a module's root or under none; and otherwise under a library's
// rules, named by its directory under the root, with the packages it
// imports, since a library checked as a program would be refused its
// binding to C. A standard package's directory in a checkout of the
// compiler is checked as the library's package, with the library read
// from the checkout. It is what the command and the language server
// both check a directory with, so that the two agree on every package;
// o says how the program is read.
func CheckDir(dir string, info *types.Info, o Options) error {
	o = o.withTarget()
	if stdDir, path, ok := StdPackage(dir); ok {
		return checkStd(stdDir, path, "", info, o)
	}

	names, err := SourceFiles(dir, o.Target)
	if err != nil {
		return err
	}

	if len(names) == 0 {
		return errors.New(dir + ": no " + SourceExt + " files in the directory")
	}

	if isMainFile(names[0], o.Read) {
		_, _, err := LoadProgramFrom(o.Load(names), info)
		return err
	}

	return loadPackage(dir, names, info, o)
}

// CheckTests checks the tests of the library package in dir as snello
// test loads them, by path, the import path they are reached by: the
// package's own, whose test files join it, or its external test
// package's, NAME_test, which imports it. The tests of a standard
// package in a checkout of the compiler are checked with the library
// read from the checkout. o says how the program is read. An error
// keeps the file and position it names, so that the language server
// reports it on the test file it is in.
func CheckTests(dir, path string, info *types.Info, o Options) error {
	if stdDir, _, ok := StdPackage(dir); ok {
		return checkStd(stdDir, path, path, info, o.withTarget())
	}

	m, err := FindModule(dir)
	if err != nil {
		return err
	}

	if m == nil {
		return errors.New(dir + ": no " + ModFile + " above the package, which its tests need to name it")
	}

	return importIn(m, path, path, info, o.withTarget())
}

// Load returns the load of the program of the files named, read as o
// says.
func (o Options) Load(names []string) Load {
	return Load{Names: names, Target: o.Target, Read: o.Read, Cached: o.Cached, Parsed: o.Parsed, At: o.At, Recovered: o.Recovered}
}

// SourceFiles returns the .snello files of dir built for t, sorted by
// name, each with its directory: the package the directory holds, and
// none when it holds no package. A file named for another platform,
// poll_linux.snello on macOS, is left out, as platform.Builds reads the
// names, and so is one whose name begins with a dot or an underscore; a
// directory whose every file is left out for another platform is
// refused, since it holds no
// package for t. A test file, NAME_test.snello, is not among them: it
// is part of the package only when the package is tested.
func SourceFiles(dir string, t platform.Target) ([]string, error) {
	files, skipped, err := filesOf(dir, isSource, t)
	if err == nil && len(files) == 0 && skipped {
		err = errors.New("every " + SourceExt + " file in " + dir + " is for another platform than " + t.String())
	}

	return files, err
}

// TestFiles returns the test files of dir built for t, NAME_test.snello,
// sorted by name, each with its directory.
func TestFiles(dir string, t platform.Target) ([]string, error) {
	files, _, err := filesOf(dir, IsTestFile, t)
	return files, err
}

// IsTestFile reports whether a file's name is a test file's.
func IsTestFile(name string) bool {
	return strings.HasSuffix(name, TestFileSuffix)
}

// Chosen reports whether the file named name, a base name, is a file of
// its directory's package in a build for t: not one ignored passes
// over, and built for t by its name.
func Chosen(name string, t platform.Target) bool {
	return !ignored(name) && t.Builds(name)
}

// isMainFile reports whether the file name, read through read or from
// the disk, is of the main package; a file that does not read or parse
// is taken for one, so that the program's own load reports why.
func isMainFile(name string, read func(name string) ([]byte, error)) bool {
	src, err := readSource(name, read)
	if err != nil {
		return true
	}

	f, _, _ := parser.ParseFile(name, src)
	return f == nil || f.Name == nil || f.Name.Name == mainPackage
}

// loadPackage checks the package in dir, whose source files are names,
// as CheckDir does when the package is not main.
func loadPackage(dir string, names []string, info *types.Info, o Options) error {
	m, err := FindModule(dir)
	if err != nil {
		return err
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}

	if m == nil || abs == m.Dir {
		_, _, err = LoadProgramFrom(o.Load(names), info)
		return err
	}

	rel, err := filepath.Rel(m.Dir, abs)
	if err != nil {
		return err
	}

	return importIn(m, filepath.ToSlash(rel), "", info, o)
}

// importIn resolves the module m as o says and loads the package at the
// import path through an importer of the module, whose package under
// test is tests, or none for "".
func importIn(m *Module, path, tests string, info *types.Info, o Options) error {
	if err := resolve(m, o.Cached); err != nil {
		return err
	}

	imp := NewImporter(info)
	imp.Module, imp.Target, imp.Read, imp.Parsed, imp.At, imp.Recovered, imp.Tests = m, o.Target, o.Read, o.Parsed, o.At, o.Recovered, tests
	_, err := imp.ImportFrom(path, m.Dir)
	return err
}

// checkStd checks the standard package at path with the library's
// privileges, the whole library read from stdDir, the std/ directory of
// a checkout of the compiler, as snello test std/NAME reads it; tests
// is the path of the package whose test files join it, or "". The
// package builtin is documentation the compiler's own tables answer
// for, which nothing compiles and its tests hold to the checker, so it
// has nothing to check; its test package is checked as any other.
func checkStd(stdDir, path, tests string, info *types.Info, o Options) error {
	if path == builtinPath {
		return nil
	}

	imp := NewImporter(info)
	imp.StdDir, imp.Tests = stdDir, tests
	imp.Target, imp.Read, imp.Parsed, imp.At, imp.Recovered = o.Target, o.Read, o.Parsed, o.At, o.Recovered
	_, err := imp.Import(path)
	if ce, ok := errors.AsType[*compileError](err); ok && ce.path == path {
		return ce.err // the package's own errors, as a check of its directory reports them
	}

	return err
}

// withTarget returns o with the host's target where it names none.
func (o Options) withTarget() Options {
	if o.Target == (platform.Target{}) {
		o.Target = platform.Host()
	}

	return o
}

// resolve resolves the module's requirements, from the module cache
// alone when cached is set.
func resolve(m *Module, cached bool) error {
	if cached {
		return m.ResolveCached()
	}

	return m.Resolve()
}

// readSource reads a source file name through read, or from the disk
// when read is nil.
func readSource(name string, read func(name string) ([]byte, error)) ([]byte, error) {
	if read != nil {
		return read(name)
	}

	return os.ReadFile(name)
}

// filesOf returns the files of dir that want takes and that are built
// for t, sorted by name, each with its directory, and whether a file
// want takes was left out for naming another platform.
func filesOf(dir string, want func(string) bool, t platform.Target) ([]string, bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, false, err
	}

	names, skipped := choose(entries, want, t)
	for i, name := range names {
		names[i] = filepath.Join(dir, name)
	}

	return names, skipped, nil
}

// choose returns the names of the files among entries that want takes
// and that are built for t, sorted, and whether one want takes was
// left out for naming another platform. A name ignored says is no
// file of the package is passed over.
func choose(entries []fs.DirEntry, want func(string) bool, t platform.Target) ([]string, bool) {
	var names []string
	skipped := false
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || ignored(name) || !want(name) {
			continue
		}

		if t.Builds(name) {
			names = append(names, name)
		} else {
			skipped = true
		}
	}

	sort.Strings(names)
	return names, skipped
}

// ignored reports whether a file's name begins with a dot or an
// underscore, an editor's lock or a tool's own file, which is no file
// of a package.
func ignored(name string) bool {
	return strings.HasPrefix(name, hiddenPrefix) || strings.HasPrefix(name, ignoredPrefix)
}

// isSource reports whether a file's name is a source file's of the
// package, not a test file's.
func isSource(name string) bool {
	return strings.HasSuffix(name, SourceExt) && !IsTestFile(name)
}

// checkTestImports refuses an import of testing or expect, or of an
// external test package, NAME_test, from a file that is not a test
// file, so that nothing of a test reaches a program's binary; the
// standard library's own files are exempt, since expect is built on
// testing. The generated runner is a test file by its name, and so the
// one importer of a test package.
func checkTestImports(names []string, syntax []*ast.File, standard bool) error {
	if standard {
		return nil
	}

	for i, f := range syntax {
		if f == nil || IsTestFile(names[i]) {
			continue
		}

		for _, spec := range f.Imports {
			if path, err := strconv.Unquote(spec.Path.Value); err == nil && (path == testingPackage || path == expectPackage || strings.HasSuffix(path, TestSuffix)) {
				return errors.New(names[i] + ": package " + path + " is imported by test files alone")
			}
		}
	}

	return nil
}

// cFiles returns the .c and .h files of dir built for t, sorted by
// name, each with its directory: the C side of the package the
// directory holds, which the backends compile with the program, chosen
// by their names as the package's .snello files are, shim_linux.c.
func cFiles(dir string, t platform.Target) ([]string, error) {
	files, _, err := filesOf(dir, isC, t)
	return files, err
}

// isC reports whether a file's name is a C source's or a C header's.
func isC(name string) bool {
	return strings.HasSuffix(name, ".c") || strings.HasSuffix(name, ".h")
}

// parseFiles parses the sources named into the importer's FileSet, so
// that a position names one file of the program, and returns the files,
// their syntax and the errors of every file together, each file given
// to Parsed as it is parsed, and the file At names given its position
// for every check after; comments asks for the files' comments.
func (imp *Importer) parseFiles(names []string, srcs [][]byte, comments bool) ([]*token.File, []*ast.File, error) {
	parse := parser.Parse
	if comments {
		parse = parser.ParseComments
	}

	files := make([]*token.File, len(names))
	syntax := make([]*ast.File, len(names))
	var errs scanner.ErrorList
	for i, name := range names {
		files[i] = imp.fset.AddFile(name, len(srcs[i]))
		var err error
		if syntax[i], err = parse(files[i], srcs[i]); err != nil {
			errs = append(errs, err.(scanner.ErrorList)...)
		}

		if imp.Parsed != nil {
			imp.Parsed(files[i], syntax[i])
		}

		if name == imp.At.File && imp.At.File != "" {
			imp.at = files[i].Pos(imp.At.Offset)
			imp.own.At, imp.std.At = imp.at, imp.at
		}
	}

	return files, syntax, errs.Err()
}
