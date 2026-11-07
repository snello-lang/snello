// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package testrun builds a package's tests into a program and runs it,
// which is what snello test does and what the standard library's own
// tests go through (_docs/design/09-testing.md, section 5): the tests
// are the functions named Test in the package's _test.snello files,
// found by name, and the program is the package with those files and
// a generated main that lists the tests and calls testing.Main. A
// library package is imported by the generated main under its path;
// a main package has the generated file join it, with the program's
// own main left out, since main cannot be imported.
package testrun

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/c"
	"github.com/snello-lang/snello/compiler/emit/llvm"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/types"
	"github.com/snello-lang/snello/std"
)

// Runner is the name of the generated file, which ends as a test file
// does so that it may import testing, in the package's directory so
// that its imports are answered from the package's module; it is never
// written there.
const Runner = "snello_test.snello"

// A Plan is a package's tests as found, before the program is built:
// where the package is, whether it is a main package, its source and
// test files, and the names of its tests in the order they run.
type Plan struct {
	Dir    string   // the package's directory, or "" for a standard package
	Path   string   // the import path the generated main imports it by; "" for a main package
	Main   bool     // a main package: the generated file joins it
	Files  []string // the package's sources, on disk or in the embedded library
	Tests  []string // the test files
	Names  []string // the tests, by name, sorted
	StdDir string   // for a standard package read from a checkout, the directory holding the library's sources, std/
}

// PlanDir finds the tests of the package in dir: its test files, the
// functions named Test in them, and how the generated main reaches it.
// A library package under no module is refused, since the generated
// main could not import it; a directory with no test file is refused.
func PlanDir(dir string) (*Plan, error) {
	files, err := importer.SourceFiles(dir)
	if err != nil {
		return nil, err
	}

	tests, err := importer.TestFiles(dir)
	if err != nil {
		return nil, err
	}

	if len(tests) == 0 {
		return nil, errors.New(dir + ": no _test.snello files")
	}

	p := &Plan{Dir: dir, Files: files, Tests: tests}
	var pkg string
	if p.Names, pkg, err = testNames(tests, nil); err != nil {
		return nil, err
	}

	first := tests[0]
	if len(files) > 0 {
		first = files[0]
	}

	src, err := os.ReadFile(first)
	if err != nil {
		return nil, err
	}

	f, _, err := parser.ParseFile(first, src)
	if err != nil {
		return nil, err
	}

	if f.Name.Name == "main" {
		if pkg != "main" {
			return nil, errors.New(dir + ": the tests of a main package are inside it, package main, since main cannot be imported; " + pkg + " cannot be")
		}

		p.Main = true
		return p, nil
	}

	m, err := importer.FindModule(dir)
	if err != nil {
		return nil, err
	}

	if m == nil {
		return nil, errors.New(dir + ": no snello.mod above the package; snello test needs the module to name it")
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	rel, err := filepath.Rel(m.Dir, abs)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return nil, errors.New(dir + ": the package is the module's root, which no import path names; snello test needs it in a directory under the root")
	}

	p.Path = filepath.ToSlash(rel)
	if external(pkg, path.Base(p.Path)) {
		p.Path += "_test"
	}
	return p, nil
}

// PlanStd finds the tests of the standard package at importPath, in
// the copy the compiler embeds, for the library's own tests.
func PlanStd(importPath string) (*Plan, error) {
	entries, err := std.Sources.ReadDir(importPath)
	if err != nil {
		return nil, err
	}

	p := &Plan{Path: importPath}
	var srcs [][]byte
	for _, e := range entries {
		name := importPath + "/" + e.Name()
		switch {
		case strings.HasSuffix(e.Name(), "_test.snello"):
			src, err := std.Sources.ReadFile(name)
			if err != nil {
				return nil, err
			}

			p.Tests = append(p.Tests, name)
			srcs = append(srcs, src)
		case strings.HasSuffix(e.Name(), ".snello"):
			p.Files = append(p.Files, name)
		}
	}

	if len(p.Tests) == 0 {
		return nil, errors.New(importPath + ": no _test.snello files")
	}

	var pkg string
	if p.Names, pkg, err = testNames(p.Tests, srcs); err != nil {
		return nil, err
	}

	if external(pkg, importPath) {
		p.Path += "_test"
	}

	return p, nil
}

// PlanStdDir finds the tests of the standard package in dir, a
// directory std/NAME of a checkout of the compiler, read from the
// directory so that the tests run against the sources as edited; the
// whole library is read from the checkout then, every package under
// std/. A directory that is not a standard package's is refused.
func PlanStdDir(dir string) (*Plan, error) {
	if !IsStdDir(dir) {
		return nil, errors.New(dir + " is not a standard package of the library: snello test std/NAME runs one in a checkout of the compiler, whose std/ holds every package, with the compiler built from that checkout")
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	tests, err := importer.TestFiles(abs)
	if err != nil {
		return nil, err
	}

	if len(tests) == 0 {
		return nil, errors.New(dir + ": no _test.snello files")
	}

	name := filepath.Base(abs)
	p := &Plan{Path: name, Files: []string{filepath.Join(abs, name+".snello")}, Tests: tests, StdDir: filepath.Dir(abs)}
	var pkg string
	if p.Names, pkg, err = testNames(tests, nil); err != nil {
		return nil, err
	}

	if external(pkg, name) {
		p.Path += "_test"
	}

	return p, nil
}

// IsStdDir reports whether dir is a standard package's directory in a
// checkout of the compiler: std/NAME, holding NAME.snello, for a NAME
// the embedded library has, under a std/ that holds every package of
// the embedded library, so that a program's own directory named std
// is never mistaken for the checkout and planned with the library's
// privileges.
func IsStdDir(dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}

	name := filepath.Base(abs)
	stdDir := filepath.Dir(abs)
	if filepath.Base(stdDir) != "std" {
		return false
	}

	if _, err := std.Sources.ReadFile(name + "/" + name + ".snello"); err != nil {
		return false
	}

	for _, p := range importer.Paths() { // the whole library, as the embedded copy lists it
		if _, err := os.Stat(filepath.Join(stdDir, p, path.Base(p)+".snello")); err != nil {
			return false
		}
	}

	return true
}

// testNames parses the test files, read from disk when srcs is nil,
// and returns the names of the functions named Test or Benchmark plus
// a capital with no receiver, sorted, as the tests run. One with
// parameters, results or type parameters is refused, so that a test is
// never silently not run.
func testNames(tests []string, srcs [][]byte) ([]string, string, error) {
	var names []string
	pkg := ""
	for i, name := range tests {
		var src []byte
		var err error
		if srcs != nil {
			src = srcs[i]
		} else if src, err = os.ReadFile(name); err != nil {
			return nil, "", err
		}

		f, _, err := parser.ParseFile(name, src)
		if err != nil {
			return nil, "", err
		}

		if pkg == "" {
			pkg = f.Name.Name
		} else if f.Name.Name != pkg {
			return nil, "", errors.New("the test files are of two packages, " + pkg + " and " + f.Name.Name + " (" + filepath.Base(name) + "): a package's tests are all inside it or all in its " + pkg + "_test package")
		}

		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && isTest(fn.Name.Name) {
				if len(fn.Params) != 0 || len(fn.Results) != 0 || len(fn.TypeParams) != 0 {
					return nil, "", errors.New(fn.Name.Name + " has the wrong signature: want func()")
				}

				names = append(names, fn.Name.Name)
			}
		}
	}

	sort.Strings(names)
	return names, pkg, nil
}

// external says whether the test files, of package pkg, are the
// external test package of the package named base, pkg base_test,
// which imports base as its users do: the form for a package the test
// harness itself depends on, and for testing a package through its
// exported names alone.
func external(pkg, base string) bool {
	return pkg == base+"_test"
}

// isTest reports whether name is a test's, Test and then a capital, or
// a benchmark's, Benchmark and then a capital; both are listed for
// testing.Main, which runs the benchmarks under -bench.
func isTest(name string) bool {
	for _, prefix := range []string{"Test", "Benchmark"} {
		if rest := strings.TrimPrefix(name, prefix); rest != name && len(rest) > 0 && rest[0] >= 'A' && rest[0] <= 'Z' {
			return true
		}
	}

	return false
}

// Source writes the generated main: it imports the package under test
// unless it joins it, and calls testing.Main with the package's path
// and its tests.
func (p *Plan) Source() []byte {
	var b strings.Builder
	b.WriteString("package main\n\nimport \"testing\"\n")
	prefix := ""
	if !p.Main {
		b.WriteString("import pkg " + strconv.Quote(p.Path) + "\n")
		prefix = "pkg."
	}

	label := strings.TrimSuffix(p.Path, "_test") // the package tested, whichever form its tests take
	if p.Main {
		label = path.Base(filepath.ToSlash(p.Dir))
	}

	b.WriteString("\nfunc main() {\n\ttesting.Main(" + strconv.Quote(label) + ", []testing.Test{")
	for i, name := range p.Names {
		if i > 0 {
			b.WriteString(", ")
		}

		b.WriteString("testing.Test{Name: " + strconv.Quote(name) + ", F: " + prefix + name + "}")
	}

	b.WriteString("})\n}\n")
	return []byte(b.String())
}

// Emit loads the plan's program and emits it through the backend.
func (p *Plan) Emit(backend string) ([]byte, []lower.Binding, error) {
	dir := p.Dir
	if dir == "" {
		dir = "."
	}

	info := &types.Info{}
	load := importer.Load{Tests: p.Path, StdDir: p.StdDir}
	if p.Main {
		load.Names = append(append(append([]string{}, p.Files...), p.Tests...), filepath.Join(dir, Runner))
		load.DropMain = true
		load.Main = filepath.Join(dir, Runner)
		for _, name := range load.Names[:len(load.Names)-1] {
			src, err := os.ReadFile(name)
			if err != nil {
				return nil, nil, err
			}

			load.Srcs = append(load.Srcs, src)
		}

		load.Srcs = append(load.Srcs, p.Source())
	} else {
		load.Names = []string{filepath.Join(dir, Runner)}
		load.Srcs = [][]byte{p.Source()}
	}

	imp, prog, err := importer.LoadProgramFrom(load, info)
	if err != nil {
		return nil, nil, err
	}

	pkg := prog.Pkg
	if !p.Main {
		pkg = nil
		for _, lib := range imp.Packages() {
			if lib.Path == p.Path {
				pkg = lib.Pkg
				break
			}
		}

		if pkg == nil {
			return nil, nil, errors.New("package " + p.Path + " was not loaded")
		}
	}

	for _, name := range p.Names { // every test the plan found is in the package: a method or a value under the name would have failed the generated main's build
		if _, ok := pkg.Scope().Lookup(name).(*types.Func); !ok {
			return nil, nil, errors.New(name + " is not a function of the package")
		}
	}

	var libs []lower.Library
	for _, lib := range imp.Packages() {
		libs = append(libs, lower.Library{Path: lib.Path, File: lib.File, Syntax: lib.Syntax, CFiles: lib.CFiles, Links: lib.Links})
	}

	bindings := lower.Bindings(info, libs)
	var out []byte
	if backend == "llvm" {
		out, err = llvm.EmitFiles(llvm.Config{}, prog.Files, prog.Syntax, info, libs...)
	} else {
		out, err = c.EmitFiles(prog.Files, prog.Syntax, info, libs...)
	}

	return out, bindings, err
}

// Build emits and compiles the plan's program into outDir as a release
// build through the backend, and returns the binary.
func (p *Plan) Build(backend, outDir string) (string, error) {
	src, bindings, err := p.Emit(backend)
	if err != nil {
		return "", err
	}

	name := "snello-test"
	var bin string
	var log []byte
	if backend == "llvm" {
		bin, log, err = llvm.Build(outDir, name, src, false, bindings...)
	} else {
		bin, log, err = c.Build(outDir, name, src, false, bindings...)
	}

	if err != nil {
		return "", errors.New(backend + ": " + err.Error() + "\n" + string(log))
	}

	return bin, nil
}

// Run builds the tests of the package in dir through the backend and
// runs the program with args, its output passed through, and returns
// the program's exit code: 0 when every test passed.
func Run(dir, backend string, args []string, stdout, stderr io.Writer) (int, error) {
	var p *Plan
	var err error
	if IsStdDir(dir) {
		p, err = PlanStdDir(dir)
	} else {
		p, err = PlanDir(dir)
	}

	if err != nil {
		return 1, err
	}

	tmp, err := os.MkdirTemp("", "snello-test")
	if err != nil {
		return 1, err
	}

	defer os.RemoveAll(tmp)
	bin, err := p.Build(backend, tmp)
	if err != nil {
		return 1, err
	}

	cmd := exec.Command(bin, args...)
	cmd.Dir = dir // as go test runs a package's tests in its directory, so that testdata/ is beside them
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			if code := exit.ExitCode(); code >= 0 {
				return code, nil
			}

			fmt.Fprintln(stderr, "snello test: the tests ended by a signal:", exit.String())
			return 2, nil
		}

		return 1, err
	}

	return 0, nil
}
