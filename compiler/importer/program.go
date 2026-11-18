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

// A Program is the main package as loaded: its source files and their
// syntax, checked as the program with the packages they import, which
// the backends compile with it, and the package the checker made of
// it, which CheckMain looks at.
type Program struct {
	Files  []*token.File
	Syntax []*ast.File
	Pkg    *types.Package
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
	if len(names) == 0 {
		return nil, nil, errors.New("no source files to load")
	}

	srcs := make([][]byte, len(names))
	var err error
	for i, name := range names {
		if srcs[i], err = os.ReadFile(name); err != nil {
			return nil, nil, err
		}
	}

	return LoadProgramFrom(Load{Names: names, Srcs: srcs, Target: t}, info)
}

// A Load is a program given as sources, named as files for the
// messages and for the module they are found under: what a test build
// gives the loader, with the generated main in memory. Tests is the
// path of the package whose test files are loaded with it, or "";
// DropMain leaves the program's own main out, for a main package
// tested with a generated one, which is the file named by Main and
// keeps its; Standalone loads the program in no module, wherever its
// files are named, as a standard package's tests are.
type Load struct {
	Names      []string
	Srcs       [][]byte
	Tests      string
	DropMain   bool
	Main       string
	StdDir     string // the standard library's sources read from a directory, as Importer.StdDir
	Standalone bool
	Target     platform.Target // the platform the program is built for, which chooses its packages' files; the zero Target is the host
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
	if l.Target != (platform.Target{}) {
		imp.Target = l.Target
	}
	if !l.Standalone {
		var err error
		if imp.Module, err = FindModule(filepath.Dir(l.Names[0])); err != nil {
			return nil, nil, err
		}
	}

	fset, syntax, err := parseFiles(l.Names, l.Srcs)
	if err != nil {
		return nil, nil, err
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

	conf := types.Config{Importer: imp}
	pkg, err := conf.CheckFiles("", fset.Files(), syntax, info)
	if err != nil {
		return nil, nil, err
	}

	return imp, &Program{fset.Files(), syntax, pkg}, nil
}

// LoadPackage checks the package in dir, a directory under a module's
// root other than the root itself, as an import of the program would:
// under a library's rules, named by its directory under the root, with
// the packages it imports. It is what snello check runs on a directory
// whose package is not main, since a library checked as a program would
// be refused its binding to C. A directory that is a module's root or
// under no module is checked as a program. Its files and its imports'
// are chosen for the target t.
func LoadPackage(dir string, t platform.Target, info *types.Info) error {
	m, err := FindModule(dir)
	if err != nil {
		return err
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}

	if m == nil || abs == m.Dir {
		names, err := SourceFiles(dir, t)
		if err != nil {
			return err
		}

		_, _, err = LoadProgram(names, t, info)
		return err
	}

	rel, err := filepath.Rel(m.Dir, abs)
	if err != nil {
		return err
	}

	imp := NewImporter(info)
	imp.Module = m
	imp.Target = t
	_, err = imp.ImportFrom(filepath.ToSlash(rel), m.Dir)
	return err
}

// SourceFiles returns the .snello files of dir built for t, sorted by
// name, each with its directory: the package the directory holds, and
// none when it holds no package. A file named for another platform,
// poll_linux.snello on macOS, is left out, as platform.Builds reads the
// names, and so is one whose name begins with a dot or an underscore; a directory whose every
// file is left out for another platform is refused, since it holds no
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

// parseFiles parses the sources named into one FileSet, so that a
// position names its file, and returns the files, their syntax and the
// errors of every file together.
func parseFiles(names []string, srcs [][]byte) (*token.FileSet, []*ast.File, error) {
	fset := token.NewFileSet()
	syntax := make([]*ast.File, len(names))
	var errs scanner.ErrorList
	for i, name := range names {
		var err error
		if syntax[i], err = parser.Parse(fset.AddFile(name, len(srcs[i])), srcs[i]); err != nil {
			errs = append(errs, err.(scanner.ErrorList)...)
		}
	}

	return fset, syntax, errs.Err()
}
