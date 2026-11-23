// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"bytes"
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
	"github.com/snello-lang/snello/compiler/token"
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
// reports in place of the last, and the views of its files; a package
// with no open document left loses its reports. A package is checked
// as snello check DIR checks it, and its tests as snello test loads
// them; a directory in which more than one file declares main holds
// programs of one file each, as snello build FILE builds them, and each
// file is checked alone. A copy of the standard library's, which a
// definition opened, is the library's package and reports nothing: it
// is answered from a check of the package in the compiler, and a copy
// of another version's from none.
func (s *Server) checkAll() {
	dirs := s.openPackages()
	for dir := range s.reports {
		if !slices.Contains(dirs, dir) {
			delete(s.reports, dir)
		}
	}

	last := s.views
	s.views = make(map[string]*view)
	for _, dir := range dirs {
		if path, ok := stdCopyOf(dir); ok {
			s.reports[dir] = nil
			if path != "" {
				s.keep(dir, s.checkStd(path), last)
			}

			continue
		}

		var views []*view
		s.reports[dir], views = s.checks(dir, nil, platform.Host())
		for _, v := range views {
			s.keep(dir, v, last)
		}
	}
}

// checks checks the package in dir as checkAll does, for the platform
// t, with the sources of over in place of the files they name, and
// returns what each check reported and the view it left, untrimmed:
// the package's sources and its tests, or each of the programs of one
// file it holds.
func (s *Server) checks(dir string, over map[string][]byte, t platform.Target) ([]report, []*view) {
	var reports []report
	var views []*view
	add := func(r report, v *view) {
		reports, views = append(reports, r), append(views, v)
	}

	if programs := s.programsIn(dir, over, t); len(programs) > 1 {
		for _, name := range programs {
			add(s.checkProgram(name, over, t))
		}

		return reports, views
	}

	add(s.checkSources(dir, over, t))
	if tests, err := importer.TestFiles(dir, t); err == nil && len(tests) > 0 {
		add(s.checkTests(dir, over, t))
	}

	return reports, views
}

// keep trims v to the files of dir its check parsed that no check
// before it parsed, and makes it their view: a package's files are
// answered from the check of its sources, and its test files from the
// check of its tests. A file the check did not type, since it or a file
// beside it does not parse, keeps its view of the last checks while its
// text is the one that view read, so that a syntax error darkens the
// file it is in alone.
func (s *Server) keep(dir string, v *view, last map[string]*view) {
	own := make(map[string]*token.File)
	for path, f := range ownFiles(dir, v) {
		if s.views[path] == nil {
			own[path] = f
		}
	}

	v.trim(own)
	for path, f := range own {
		old := last[path]
		delete(last, path) // the last view of the file is let go as soon as it is replaced, so that a round of checks holds one round's views
		if old != nil && !v.typed(f) && sameText(old, v, path) {
			s.views[path] = old
			continue
		}

		s.views[path] = v
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

// programsIn returns the source files of dir for the platform t that
// declare main, as the editor holds them or as over gives them; none
// when a file's parse panics, so that the check of the package reports
// why.
func (s *Server) programsIn(dir string, over map[string][]byte, t platform.Target) []string {
	names, err := importer.SourceFiles(dir, t)
	if err != nil {
		return nil
	}

	var programs []string
	err = s.guard(func() error {
		for _, name := range names {
			src, ok := over[name]
			if !ok {
				var err error
				if src, err = s.read(name); err != nil {
					continue
				}
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
func (s *Server) checkProgram(name string, over map[string][]byte, t platform.Target) (report, *view) {
	v, o := s.begin(over, t)
	err := s.guard(func() error {
		_, _, err := importer.LoadProgramFrom(o.Load([]string{name}), v.info)
		return err
	})

	return s.reportOf(err, v.srcs, []string{name}), v
}

// checkSources checks the package in dir as snello check DIR does and
// reports its errors; an error that names no file is reported on the
// package's open documents.
func (s *Server) checkSources(dir string, over map[string][]byte, t platform.Target) (report, *view) {
	v, o := s.begin(over, t)
	err := s.guard(func() error { return importer.CheckDir(dir, v.info, o) })
	return s.reportOf(err, v.srcs, s.openIn(dir, false)), v
}

// checkTests checks the package in dir with its tests, as snello test
// loads them: a main package with its test files, and a library by the
// import path its tests are reached at, its own or its external test
// package's. An error that names no file is reported on the package's
// open test files.
func (s *Server) checkTests(dir string, over map[string][]byte, t platform.Target) (report, *view) {
	v, o := s.begin(over, t)
	err := s.guard(func() error {
		plan, err := testrun.PlanDirFrom(dir, o.Read)
		if err != nil {
			return err
		}

		if plan.Main {
			_, _, err = importer.LoadProgramFrom(o.Load(append(slices.Clone(plan.Files), plan.Tests...)), v.info)
			return err
		}

		return importer.CheckTests(dir, plan.Path, v.info, o)
	})

	return s.reportOf(err, v.srcs, s.openIn(dir, true)), v
}

// begin starts a check: its view, and the loader's options it checks
// with, the same for every check of the server: the files of t, the
// sources read as read reads them, or from over, which a rename checks
// its edits through, and recorded in the view, every file parsed kept
// in the view, and the module resolved from the module cache alone, so
// that a check as the developer types never fetches a module or writes
// the lock file.
func (s *Server) begin(over map[string][]byte, t platform.Target) (*view, importer.Options) {
	v := newView(make(map[string][]byte))
	read := func(name string) ([]byte, error) {
		src, ok := over[filepath.Clean(name)]
		var err error
		if !ok {
			src, err = s.read(name)
		}

		if err == nil {
			v.srcs[name] = src
		}

		return src, err
	}

	return v, importer.Options{Target: t, Read: read, Cached: true, Parsed: v.parsed}
}

// ownFiles returns the files of dir a check parsed, by path, as
// stdPathOf names them.
func ownFiles(dir string, v *view) map[string]*token.File {
	own := make(map[string]*token.File)
	for _, f := range v.files {
		if path := stdPathOf(f.Name()); filepath.Dir(path) == dir {
			own[path] = f
		}
	}

	return own
}

// checkStd checks the standard package at path, the package in the
// compiler a copy of its files stands for, and returns its view.
func (s *Server) checkStd(path string) *view {
	v, o := s.begin(nil, platform.Host())
	s.guard(func() error {
		imp := importer.NewImporter(v.info)
		imp.Target, imp.Parsed = o.Target, o.Parsed
		_, err := imp.Import(path)
		return err
	})

	return v
}

// sameText reports whether the views a and b read the same text for
// the file at path.
func sameText(a, b *view, path string) bool {
	f := a.byPath[path]
	if f == nil {
		return false
	}

	old, err := a.source(f.Name())
	if err != nil {
		return false
	}

	now, err := b.source(f.Name())
	return err == nil && bytes.Equal(old, now)
}

// read reads a source as every check of the server does: the open
// document's text where the file is open, and the disk elsewhere.
func (s *Server) read(name string) ([]byte, error) {
	if d := s.byPath[filepath.Clean(name)]; d != nil {
		return d.text, nil
	}

	return os.ReadFile(name)
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

// isMain reports whether a declaration is a program's main: a function
// named main with no receiver.
func isMain(d ast.Decl) bool {
	f, ok := d.(*ast.FuncDecl)
	return ok && f.Recv == nil && f.Name != nil && f.Name.Name == mainFunc
}
