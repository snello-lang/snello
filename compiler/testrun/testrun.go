// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package testrun builds a package's tests into a program and runs it,
// which is what snello test does and what the standard library's own
// tests go through: the tests are the functions named Test in the
// package's _test.snello files, found by name, and the program is the
// package with those files and
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
	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/compiler/toolchain"
	"github.com/snello-lang/snello/compiler/types"
	"github.com/snello-lang/snello/std"
)

// Runner is the name of the generated file, which ends as a test file
// does so that it may import testing, in the package's directory so
// that its imports are answered from the package's module; it is never
// written there.
const Runner = "snello" + importer.TestFileSuffix

// mainPackage is the package clause of a program, whose tests are
// inside it.
const mainPackage = "main"

// A Plan is a package's tests as found, before the program is built:
// where the package is, whether it is a main package, its source and
// test files, and the names of its tests in the order they run.
type Plan struct {
	Dir      string    // the package's directory, or "" for a standard package
	Path     string    // the import path the generated main imports it by; "" for a main package
	Main     bool      // a main package: the generated file joins it
	Files    []string  // the package's sources, on disk or in the embedded library
	Tests    []string  // the test files
	Names    []string  // the tests, by name, sorted
	Examples []Example // the examples, by name, sorted; the generated main lists those that run
	StdDir   string    // for a standard package read from a checkout, the directory holding the library's sources, std/
}

// PlanDir finds the tests of the package in dir: its test files, the
// functions named Test in them, and how the generated main reaches it.
// A library package under no module is refused, since the generated
// main could not import it; a directory with no test file is refused.
func PlanDir(dir string) (*Plan, error) {
	return PlanDirFrom(dir, nil)
}

// PlanDirFrom is PlanDir with every file read through read, or from the
// disk when read is nil: how the language server plans the tests of a
// package as the editor holds its files. A standard package's directory
// in a checkout of the compiler is planned as PlanStdDirFrom plans it.
func PlanDirFrom(dir string, read func(name string) ([]byte, error)) (*Plan, error) {
	if _, _, std := importer.StdPackage(dir); std {
		return PlanStdDirFrom(dir, read)
	}

	if read == nil {
		read = os.ReadFile
	}

	files, err := importer.SourceFiles(dir, platform.Host())
	if err != nil {
		return nil, err
	}

	tests, err := importer.TestFiles(dir, platform.Host())
	if err != nil {
		return nil, err
	}

	if len(tests) == 0 {
		return nil, errors.New(dir + ": no _test.snello files")
	}

	p := &Plan{Dir: dir, Files: files, Tests: tests}
	srcs := make([][]byte, len(tests))
	for i, name := range tests {
		if srcs[i], err = read(name); err != nil {
			return nil, err
		}
	}

	var pkg string
	if p.Names, p.Examples, pkg, err = testNames(tests, srcs); err != nil {
		return nil, err
	}

	first := tests[0]
	if len(files) > 0 {
		first = files[0]
	}

	src, err := read(first)
	if err != nil {
		return nil, err
	}

	f, _, err := parser.ParseFile(first, src)
	if err != nil {
		return nil, err
	}

	if f.Name.Name == mainPackage {
		if pkg != mainPackage {
			return nil, errors.New(dir + ": the tests of a main package are inside it, package main, since main cannot be imported; " + pkg + " cannot be")
		}

		p.Main = true
		return p, p.checkExamples(read)
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
	if external(pkg, p.Path) {
		p.Path += importer.TestSuffix
	}

	return p, p.checkExamples(read)
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
		if e.IsDir() || !importer.Chosen(e.Name(), platform.Host()) {
			continue
		}

		name := importPath + "/" + e.Name()
		switch {
		case strings.HasSuffix(e.Name(), importer.TestFileSuffix):
			src, err := std.Sources.ReadFile(name)
			if err != nil {
				return nil, err
			}

			p.Tests = append(p.Tests, name)
			srcs = append(srcs, src)
		case strings.HasSuffix(e.Name(), importer.SourceExt):
			p.Files = append(p.Files, name)
		}
	}

	if len(p.Tests) == 0 {
		return nil, errors.New(importPath + ": no _test.snello files")
	}

	var pkg string
	if p.Names, p.Examples, pkg, err = testNames(p.Tests, srcs); err != nil {
		return nil, err
	}

	if err := p.checkExamples(std.Sources.ReadFile); err != nil {
		return nil, err
	}

	if external(pkg, importPath) {
		p.Path += importer.TestSuffix
	}

	return p, nil
}

// PlanStdDir finds the tests of the standard package in dir, a
// directory std/NAME of a checkout of the compiler, read from the
// directory so that the tests run against the sources as edited; the
// whole library is read from the checkout then, every package under
// std/. A directory that is not a standard package's is refused.
func PlanStdDir(dir string) (*Plan, error) {
	return PlanStdDirFrom(dir, nil)
}

// PlanStdDirFrom is PlanStdDir with every file read through read, or
// from the disk when read is nil, as PlanDirFrom reads them.
func PlanStdDirFrom(dir string, read func(name string) ([]byte, error)) (*Plan, error) {
	if read == nil {
		read = os.ReadFile
	}

	stdDir, importPath, ok := importer.StdPackage(dir)
	if !ok {
		return nil, errors.New(dir + " is not a standard package of the library: snello test std/NAME runs one in a checkout of the compiler, whose std/ holds every package, with the compiler built from that checkout")
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	tests, err := importer.TestFiles(abs, platform.Host())
	if err != nil {
		return nil, err
	}

	if len(tests) == 0 {
		return nil, errors.New(dir + ": no _test.snello files")
	}

	files, err := importer.SourceFiles(abs, platform.Host())
	if err != nil {
		return nil, err
	}

	p := &Plan{Path: importPath, Files: files, Tests: tests, StdDir: stdDir}
	srcs := make([][]byte, len(tests))
	for i, name := range tests {
		if srcs[i], err = read(name); err != nil {
			return nil, err
		}
	}

	var pkg string
	if p.Names, p.Examples, pkg, err = testNames(tests, srcs); err != nil {
		return nil, err
	}

	if err := p.checkExamples(read); err != nil {
		return nil, err
	}

	if external(pkg, importPath) {
		p.Path += importer.TestSuffix
	}

	return p, nil
}

// testNames parses the test files, read from disk when srcs is nil,
// with their comments, and returns the names of the functions named
// Test or Benchmark plus a capital with no receiver, sorted, as the
// tests run, and the examples, sorted. One
// with parameters, results or type parameters is refused, so that a
// test or an example is never silently not run.
func testNames(tests []string, srcs [][]byte) ([]string, []Example, string, error) {
	var names []string
	var examples []Example
	pkg := ""
	for i, name := range tests {
		var src []byte
		var err error
		if srcs != nil {
			src = srcs[i]
		} else if src, err = os.ReadFile(name); err != nil {
			return nil, nil, "", err
		}

		f, err := parseWithComments(name, src)
		if err != nil {
			return nil, nil, "", err
		}

		found, err := examplesOf(f)
		if err != nil {
			return nil, nil, "", err
		}

		examples = append(examples, found...)
		if pkg == "" {
			pkg = f.Name.Name
		} else if f.Name.Name != pkg {
			return nil, nil, "", errors.New("the test files are of two packages, " + pkg + " and " + f.Name.Name + " (" + filepath.Base(name) + "): a package's tests are all inside it or all in its " + externalOf(pkg, f.Name.Name) + " package")
		}

		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && isTest(fn.Name.Name) {
				if len(fn.Params) != 0 || len(fn.Results) != 0 || len(fn.TypeParams) != 0 {
					return nil, nil, "", errors.New(fn.Name.Name + wrongSignature)
				}

				names = append(names, fn.Name.Name)
			}
		}
	}

	sort.Strings(names)
	sort.Slice(examples, func(i, j int) bool { return examples[i].Name < examples[j].Name })
	return names, examples, pkg, nil
}

// checkExamples refuses an example whose name names nothing the package
// declares, reading the package's files with read.
func (p *Plan) checkExamples(read func(string) ([]byte, error)) error {
	if len(p.Examples) == 0 {
		return nil
	}

	files := map[string][]byte{}
	for _, name := range p.Files {
		src, err := read(name)
		if err != nil {
			return err
		}

		files[name] = src
	}

	return checkExampleNames(p.Examples, p.label(), files)
}

// external says whether the test files, of package pkg, are the
// external test package of the package at importPath, whose name is
// its last element: pkg utf8_test for unicode/utf8, which imports the
// package as its users do. It is the form for a package the test
// harness itself depends on, and for testing a package through its
// exported names alone.
func external(pkg, importPath string) bool {
	return pkg == path.Base(importPath)+importer.TestSuffix
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
		b.WriteString("import pkg ")
		b.WriteString(strconv.Quote(p.Path))
		b.WriteString("\n")
		prefix = "pkg."
	}

	b.WriteString("\nfunc main() {\n\ttesting.Main(")
	b.WriteString(strconv.Quote(p.label()))
	b.WriteString(", []testing.Test{")
	for i, name := range p.Names {
		if i > 0 {
			b.WriteString(", ")
		}

		b.WriteString("testing.Test{Name: ")
		b.WriteString(strconv.Quote(name))
		b.WriteString(", F: ")
		b.WriteString(prefix)
		b.WriteString(name)
		b.WriteString("}")
	}

	b.WriteString("}, []testing.Example{")
	listed := 0
	for _, e := range p.Examples {
		if !e.Runs {
			continue
		}

		if listed > 0 {
			b.WriteString(", ")
		}

		listed++
		b.WriteString("testing.Example{Name: ")
		b.WriteString(strconv.Quote(e.Name))
		b.WriteString(", F: ")
		b.WriteString(prefix)
		b.WriteString(e.Name)
		b.WriteString(", Output: ")
		b.WriteString(strconv.Quote(e.Output))
		b.WriteString(", Unordered: ")
		b.WriteString(strconv.FormatBool(e.Unordered))
		b.WriteString("}")
	}

	b.WriteString("})\n}\n")
	return []byte(b.String())
}

// label is the name the run reports the package by: the package tested,
// whichever form its tests take, or a main package's directory.
func (p *Plan) label() string {
	if p.Main {
		return mainLabel(p.Dir)
	}

	return strings.TrimSuffix(p.Path, importer.TestSuffix)
}

// Emit loads the plan's program and emits it through the backend, for
// a debugger when debug says so.
func (p *Plan) Emit(backend string, debug bool) ([]byte, []lower.Binding, error) {
	dir := p.Dir
	if dir == "" {
		dir = "."
	}

	info := &types.Info{}
	load := importer.Load{Tests: p.Path, StdDir: p.StdDir, Standalone: p.Dir == ""} // a standard package's tests are in no module, wherever they are run from
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
		libs = append(libs, lower.Library{Path: lib.Path, File: lib.File, Syntax: lib.Syntax, CFiles: lib.CFiles, Links: lib.Links, Std: lib.Std})
	}

	bindings := lower.Bindings(info, libs)
	var out []byte
	if backend == lower.LLVMBackend {
		out, err = llvm.EmitFiles(llvm.Config{Debug: debug}, prog.Files, prog.Syntax, info, libs...)
	} else {
		out, err = c.EmitFiles(prog.Files, prog.Syntax, info, libs...) // the C backend's checks are a flag of the C compiler, which Build gives
	}

	return out, bindings, err
}

// Build emits and compiles the plan's program into outDir through the
// backend, a release build or one for a debugger, and returns the
// binary.
func (p *Plan) Build(backend, outDir string, debug bool) (string, error) {
	src, bindings, err := p.Emit(backend, debug)
	if err != nil {
		return "", err
	}

	name := "snello-test"
	var bin string
	var log []byte
	if backend == lower.LLVMBackend {
		bin, log, err = llvm.Build(outDir, name, src, platform.Host(), debug, bindings...)
	} else {
		bin, log, err = c.Build(outDir, name, src, platform.Host(), debug, bindings...)
	}

	if missing := (*toolchain.MissingError)(nil); errors.As(err, &missing) {
		return "", missing // what to do, which needs no backend's name before it
	}

	if err != nil {
		return "", errors.New(backend + ": " + err.Error() + "\n" + string(log))
	}

	return bin, nil
}

// A Work is the directory a caller guards for a test program to be
// built in and run from: Dir is its path, Starting says the program is
// about to start from it, and Started hands over the program, or nil
// for a start that failed, so that what would end the caller is passed
// on to the program and the directory goes however the caller ends.
// The caller removes it. With none, the program is built in a
// directory of the runner's own, removed when it returns.
type Work interface {
	Dir() string
	Starting()
	Started(p *os.Process)
}

// A Command makes the command that runs the program a run builds:
// exec.Command for the snello command, and for a test the command that
// ends the program with the test binary (internal/testproc).
type Command func(name string, args ...string) *exec.Cmd

// Run builds the tests of the package in dir through the backend, in
// work, and runs the program with args through command, its output
// passed through, and returns the program's exit code: 0 when every
// test passed.
func Run(dir, backend string, args []string, stdout, stderr io.Writer, work Work, command Command) (int, error) {
	if w := importer.ModuleWarning(dir); w != "" {
		fmt.Fprintln(stderr, w) // before anything else, so that a build that fails still says it
	}

	p, err := PlanDir(dir)
	if err != nil {
		return 1, err
	}

	return p.run(dir, backend, args, stdout, stderr, work, command)
}

// run builds the plan's program through the backend in work, or in a
// directory of its own, and runs it in dir with args through command,
// its output passed through, and returns its exit code.
func (p *Plan) run(dir, backend string, args []string, stdout, stderr io.Writer, work Work, command Command) (int, error) {
	tmp, done, err := workDir(work)
	if err != nil {
		return 1, err
	}

	defer done()
	bin, err := p.Build(backend, tmp, false)
	if err != nil {
		return 1, err
	}

	cmd := command(bin, args...)
	cmd.Dir = dir // a package's tests run in its directory, so that testdata/ is beside them
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if work != nil {
		work.Starting()
	}

	err = cmd.Start()
	if work != nil {
		if err != nil {
			work.Started(nil)
		} else {
			work.Started(cmd.Process)
		}
	}

	if err == nil {
		err = cmd.Wait()
	}

	if err != nil {
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

// BuildTest builds the tests of the package in dir through the backend
// into a program at out, which runs them when it is run in dir with the
// flags snello test passes it; debug builds it for a debugger, with
// the information a debugger reads beside it, as snello build -debug
// writes a program's.
func BuildTest(dir, backend, out string, debug bool, stderr io.Writer, work Work) error {
	if w := importer.ModuleWarning(dir); w != "" {
		fmt.Fprintln(stderr, w)
	}

	p, err := PlanDir(dir)
	if err != nil {
		return err
	}

	tmp, done, err := workDir(work)
	if err != nil {
		return err
	}

	defer done()
	bin, err := p.Build(backend, tmp, debug)
	if err != nil {
		return err
	}

	built, err := os.ReadFile(bin)
	if err == nil {
		err = os.WriteFile(out, built, 0o755)
	}

	if err == nil && debug {
		var log []byte
		if log, err = c.Bundle(bin, out, platform.Host()); err != nil { // before the build's directory, which holds what the bundle is gathered from, goes
			err = errors.New(err.Error() + "\n" + string(log))
		}
	}

	return err
}

// workDir returns the directory a test program is built in, work's or
// one of the runner's own, and what removes the runner's own.
func workDir(work Work) (string, func(), error) {
	if work != nil {
		return work.Dir(), func() {}, nil
	}

	tmp, err := os.MkdirTemp("", "snello-test")
	if err != nil {
		return "", nil, err
	}

	return tmp, func() { os.RemoveAll(tmp) }, nil
}

// mainLabel names the tests of the main package in dir on their ok or
// FAIL line: the directory's own name, read from its absolute path so
// that a directory given as . or .. is named too.
func mainLabel(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}

	return filepath.Base(dir)
}

// externalOf names, for a hint, the external test package of the
// package whose test files were found in the packages a and b: the
// first of the two that is not itself external, made external.
func externalOf(a, b string) string {
	switch {
	case !strings.HasSuffix(a, importer.TestSuffix):
		return a + importer.TestSuffix
	case !strings.HasSuffix(b, importer.TestSuffix):
		return b + importer.TestSuffix
	}

	return a
}
