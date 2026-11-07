// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package importer

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/parser"
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

// CheckMain reports why the program cannot be built, in go build's
// words: main is not declared in its package, or is declared as
// something other than a function. The package may have any name,
// since a program is the package that declares main, and the examples
// name theirs after what they show, where Go asks for main. snello
// check takes a package without main, as go vet does, and does not
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
// snello.mod above the directory. It returns the importer, which holds
// the packages it loaded, and the program, or the error of the first
// step that failed: no file named, a module file or a source that does
// not read, or the errors of the parser or the checker.
func LoadProgram(names []string, info *types.Info) (*Importer, *Program, error) {
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

	return LoadProgramFrom(Load{Names: names, Srcs: srcs}, info)
}

// A Load is a program given as sources, named as files for the
// messages and for the module they are found under: what a test build
// gives the loader, with the generated main in memory. Tests is the
// path of the package whose test files are loaded with it, or "";
// DropMain leaves the program's own main out, for a main package
// tested with a generated one, which is the file named by Main and
// keeps its.
type Load struct {
	Names    []string
	Srcs     [][]byte
	Tests    string
	DropMain bool
	Main     string
	StdDir   string // the standard library's sources read from a directory, as Importer.StdDir
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
	var err error
	if imp.Module, err = FindModule(filepath.Dir(l.Names[0])); err != nil {
		return nil, nil, err
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
// under no module is checked as a program.
func LoadPackage(dir string, info *types.Info) error {
	m, err := FindModule(dir)
	if err != nil {
		return err
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}

	if m == nil || abs == m.Dir {
		names, err := SourceFiles(dir)
		if err != nil {
			return err
		}

		_, _, err = LoadProgram(names, info)
		return err
	}

	rel, err := filepath.Rel(m.Dir, abs)
	if err != nil {
		return err
	}

	imp := NewImporter(info)
	imp.Module = m
	_, err = imp.ImportFrom(filepath.ToSlash(rel), m.Dir)
	return err
}

// SourceFiles returns the .snello files of dir, sorted by name, each
// with its directory: the package the directory holds, and none when
// it holds no package. A test file, NAME_test.snello, is not among
// them: it is part of the package only when the package is tested
// (_docs/design/09-testing.md, section 1).
func SourceFiles(dir string) ([]string, error) {
	return filesOf(dir, func(name string) bool { return strings.HasSuffix(name, ".snello") && !IsTestFile(name) })
}

// TestFiles returns the test files of dir, NAME_test.snello, sorted by
// name, each with its directory.
func TestFiles(dir string) ([]string, error) {
	return filesOf(dir, IsTestFile)
}

// IsTestFile reports whether a file's name is a test file's.
func IsTestFile(name string) bool {
	return strings.HasSuffix(name, "_test.snello")
}

func filesOf(dir string, want func(string) bool) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && want(e.Name()) {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}

	sort.Strings(files)
	return files, nil
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
			if path, err := strconv.Unquote(spec.Path.Value); err == nil && (path == "testing" || path == "expect" || strings.HasSuffix(path, "_test")) {
				return errors.New(names[i] + ": package " + path + " is imported by test files alone")
			}
		}
	}

	return nil
}

// cFiles returns the .c and .h files of dir, sorted by name, each with
// its directory: the C side of the package the directory holds, which
// the backends compile with the program.
func cFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && (strings.HasSuffix(e.Name(), ".c") || strings.HasSuffix(e.Name(), ".h")) {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}

	sort.Strings(files)
	return files, nil
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
