// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/compiler/scanner"
	"github.com/snello-lang/snello/compiler/testrun"
	"github.com/snello-lang/snello/compiler/types"
	"github.com/snello-lang/snello/lsp/protocol"
)

// diagnosticSource names the tool a diagnostic comes from, as an
// editor shows it beside the message, and mainFunc the function a
// program's file declares.
const (
	diagnosticSource = "snello"
	mainFunc         = "main"
)

// A report is what one check found: the diagnostics of every file it
// reported on, by the file's path.
type report map[string][]protocol.Diagnostic

// checkAll checks every package with an open document and keeps its
// reports in place of the last; a package with no open document left
// loses its reports. A package is checked as snello check DIR checks
// it, and its tests as snello test loads them; a directory in which
// more than one file declares main holds programs of one file each, as
// snello build FILE builds them, and each file is checked alone.
func (s *Server) checkAll() {
	dirs := s.openPackages()
	for dir := range s.reports {
		if !slices.Contains(dirs, dir) {
			delete(s.reports, dir)
		}
	}

	for _, dir := range dirs {
		if programs := s.programsIn(dir); len(programs) > 1 {
			s.reports[dir] = nil
			for _, name := range programs {
				s.reports[dir] = append(s.reports[dir], s.checkProgram(name))
			}

			continue
		}

		s.reports[dir] = []report{s.checkSources(dir)}
		if tests, err := importer.TestFiles(dir, platform.Host()); err == nil && len(tests) > 0 {
			s.reports[dir] = append(s.reports[dir], s.checkTests(dir))
		}
	}
}

// openPackages returns the directories of the open documents that are
// source files, sorted, each once: the packages the editor shows.
func (s *Server) openPackages() []string {
	var dirs []string
	for _, d := range s.docs {
		if d.path != "" && strings.HasSuffix(d.path, importer.SourceExt) {
			dirs = append(dirs, filepath.Dir(d.path))
		}
	}

	slices.Sort(dirs)
	return slices.Compact(dirs)
}

// programsIn returns the source files of dir that declare main, as the
// editor holds them; none when a file's parse panics, so that the check
// of the package reports why.
func (s *Server) programsIn(dir string) []string {
	names, err := importer.SourceFiles(dir, platform.Host())
	if err != nil {
		return nil
	}

	read, _ := s.reader()
	var programs []string
	err = s.guard(func() error {
		for _, name := range names {
			src, err := read(name)
			if err != nil {
				continue
			}

			f, _, _ := parser.ParseFile(name, src)
			if f != nil && slices.ContainsFunc(f.Decls, isMain) {
				programs = append(programs, name)
			}
		}

		return nil
	})
	if err != nil {
		return nil
	}

	return programs
}

// checkProgram checks the file name alone, as snello check FILE does.
func (s *Server) checkProgram(name string) report {
	read, srcs := s.reader()
	err := s.guard(func() error {
		_, _, err := importer.LoadProgramFrom(options(read).Load([]string{name}), &types.Info{})
		return err
	})

	return s.reportOf(err, srcs, []string{name})
}

// checkSources checks the package in dir as snello check DIR does and
// reports its errors; an error that names no file is reported on the
// package's open documents.
func (s *Server) checkSources(dir string) report {
	read, srcs := s.reader()
	err := s.guard(func() error { return importer.CheckDir(dir, &types.Info{}, options(read)) })
	return s.reportOf(err, srcs, s.openIn(dir, false))
}

// checkTests checks the package in dir with its tests, as snello test
// loads them: a main package with its test files, and a library by the
// import path its tests are reached at, its own or its external test
// package's. An error that names no file is reported on the package's
// open test files.
func (s *Server) checkTests(dir string) report {
	read, srcs := s.reader()
	err := s.guard(func() error {
		plan, err := testrun.PlanDirFrom(dir, read)
		if err != nil {
			return err
		}

		if plan.Main {
			_, _, err = importer.LoadProgramFrom(options(read).Load(append(slices.Clone(plan.Files), plan.Tests...)), &types.Info{})
			return err
		}

		return importer.CheckTests(dir, plan.Path, &types.Info{}, options(read))
	})

	return s.reportOf(err, srcs, s.openIn(dir, true))
}

// reader returns the function a check reads its sources through, the
// open documents' text where a file is open and the disk elsewhere,
// and the map it records each source in, by its path, which the
// check's positions are converted against.
func (s *Server) reader() (func(name string) ([]byte, error), map[string][]byte) {
	srcs := make(map[string][]byte)
	read := func(name string) ([]byte, error) {
		if d := s.byPath[filepath.Clean(name)]; d != nil {
			srcs[name] = d.text
			return d.text, nil
		}

		src, err := os.ReadFile(name)
		if err == nil {
			srcs[name] = src
		}

		return src, err
	}

	return read, srcs
}

// guard runs a check, and turns a panic in it into an error, so that a
// fault of the checker on a program as the developer types it reports
// on the program rather than ending the server.
func (s *Server) guard(check func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			s.logf("the check panicked: %v", r)
			err = fmt.Errorf("the checker failed on this package: %v", r)
		}
	}()

	return check()
}

// openIn returns the paths of the open documents in dir: its test files
// when tests is set, and its other source files otherwise.
func (s *Server) openIn(dir string, tests bool) []string {
	var paths []string
	for path := range s.byPath {
		if filepath.Dir(path) == dir && strings.HasSuffix(path, importer.SourceExt) && importer.IsTestFile(path) == tests {
			paths = append(paths, path)
		}
	}

	slices.Sort(paths)
	return paths
}

// reportOf turns a check's error into its report: every error with a
// position becomes a diagnostic on the file it names, converted against
// the source the check read; an error with none is reported at the
// start of each of others. An error is reported once, however many
// lists name it.
func (s *Server) reportOf(err error, srcs map[string][]byte, others []string) report {
	r := make(report)
	if err == nil {
		return r
	}

	var list scanner.ErrorList
	if !errors.As(err, &list) {
		for _, path := range others {
			r.add(path, protocol.Diagnostic{Message: err.Error()})
		}

		return r
	}

	for _, e := range list {
		path := filepath.Clean(e.Pos.Filename)
		if !e.Pos.IsValid() || !filepath.IsAbs(path) {
			for _, other := range others {
				r.add(other, protocol.Diagnostic{Message: e.Error()})
			}

			continue
		}

		src, ok := srcs[e.Pos.Filename]
		if !ok {
			src, _ = os.ReadFile(path) // a file the check read through no reader of the server's: what it holds now
		}

		start := min(e.Pos.Offset, len(src))
		r.add(path, protocol.Diagnostic{
			Range:   protocol.Range{Start: positionOf(src, start, s.encoding), End: positionOf(src, wordEnd(src, start), s.encoding)},
			Message: e.Msg,
		})
	}

	return r
}

// add adds d to the diagnostics of path, as an error with its source,
// unless the same diagnostic is there already.
func (r report) add(path string, d protocol.Diagnostic) {
	d.Severity, d.Source = protocol.SeverityError, diagnosticSource
	if !slices.Contains(r[path], d) {
		r[path] = append(r[path], d)
	}
}

// options are the loader's options for every check of the server: the
// host's files, the sources through read, and the module resolved from
// the module cache alone, so that a check as the developer types never
// fetches a module or writes the lock file.
func options(read func(name string) ([]byte, error)) importer.Options {
	return importer.Options{Target: platform.Host(), Read: read, Cached: true}
}

// isMain reports whether a declaration is a program's main: a function
// named main with no receiver.
func isMain(d ast.Decl) bool {
	f, ok := d.(*ast.FuncDecl)
	return ok && f.Recv == nil && f.Name != nil && f.Name.Name == mainFunc
}
