// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"encoding/json"
	"path/filepath"
	"slices"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/compiler/testrun"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/lsp/jsonrpc"
	"github.com/snello-lang/snello/lsp/protocol"
)

// The arguments of the snello command a runnable is run with: a program
// is run, a test is run by a pattern that names it alone, and a
// benchmark by one that names no test and the benchmark alone.
const (
	runArg    = "run"
	testArg   = "test"
	runFlag   = "-run"
	benchFlag = "-bench"
	noTests   = "^$"
)

// A runnableFile is what a search for runnables found in a file, and the
// stamp of the text it was read from.
type runnableFile struct {
	stamp string
	found []protocol.Runnable
}

// runnables answers snello/runnables: the programs and tests of a
// document, or of every package of the workspace when none is named,
// as snello finds them on this platform, each with the arguments that
// run it alone; a file for another platform holds none. A file that
// does not parse answers what it held when it last did.
func (s *Server) runnables(m *jsonrpc.Message) {
	var p protocol.RunnablesParams
	if len(m.Params) > 0 {
		if err := json.Unmarshal(m.Params, &p); err != nil {
			s.fail(m.ID, jsonrpc.InvalidParams, err.Error())
			return
		}
	}

	var files []string
	if p.TextDocument != nil {
		if path := pathOf(p.TextDocument.URI); path != "" && slices.Contains(hostFiles(filepath.Dir(path)), path) {
			files = append(files, path)
		}
	} else {
		for _, dir := range s.workspacePackages() {
			files = append(files, hostFiles(dir)...)
		}
	}

	found := []protocol.Runnable{}
	for _, path := range files {
		for _, r := range s.runnablesOf(path) {
			if r.Kind == protocol.RunnableMain { // by the programs beside it now, which a change to another file may change
				r.Args = s.programArgs(path, r.Dir)
			}

			found = append(found, r)
		}
	}

	s.reply(m.ID, found)
}

// hostFiles returns the source and test files of the package in dir
// that a build on this platform takes, which is what snello runs.
func hostFiles(dir string) []string {
	sources, _ := importer.SourceFiles(dir, platform.Host())
	tests, _ := importer.TestFiles(dir, platform.Host())
	files := append(sources, tests...)
	for i, f := range files {
		files[i] = filepath.Clean(f)
	}

	return files
}

// programArgs are the arguments that run the program whose main the
// file at path declares: by its directory, or by the file in a
// directory of programs of one file each.
func (s *Server) programArgs(path, dir string) []string {
	if len(s.programsIn(filepath.Dir(path), nil, platform.Host())) > 1 {
		return []string{runArg, path}
	}

	return []string{runArg, dir}
}

// runnablesOf returns the runnables of the file at path, from the search
// before when its text has not changed since, or read again.
func (s *Server) runnablesOf(path string) []protocol.Runnable {
	stamp := s.stampOf(path)
	last := s.runnableFiles[path]
	if last != nil && last.stamp == stamp && stamp != "" {
		return last.found
	}

	src, err := s.read(path)
	if err != nil {
		return nil
	}

	file := token.NewFile(path, len(src))
	f, err := parser.ParseComments(file, src)
	if err != nil && last != nil {
		return last.found // the file as it last parsed, until it parses again
	}

	if f == nil {
		return nil
	}

	dir := filepath.Dir(path)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}

	var found []protocol.Runnable
	for _, r := range testrun.FileRunnables(path, f) {
		found = append(found, s.runnable(r, f, file, src, dir))
	}

	if s.runnableFiles == nil {
		s.runnableFiles = make(map[string]*runnableFile)
	}

	s.runnableFiles[path] = &runnableFile{stamp: stamp, found: found}
	return found
}

// runnable writes r, found in the file f with text src, as the protocol
// gives it: where it is, and the arguments that run a test alone.
func (s *Server) runnable(r testrun.Runnable, f *ast.File, file *token.File, src []byte, dir string) protocol.Runnable {
	name := r.Decl.Name
	at := file.Offset(name.NamePos)
	end := at + len(name.Name)
	if body := r.Decl.Body; body != nil && body.Rbrace.IsValid() {
		end = file.Offset(body.Rbrace) + 1 // through the closing brace
	}

	whole := protocol.Range{Start: positionOf(src, file.Offset(r.Decl.Pos()), s.encoding), End: positionOf(src, end, s.encoding)}
	out := protocol.Runnable{
		Kind:           protocol.RunnableKind(r.Kind),
		Name:           name.Name,
		Location:       protocol.Location{URI: s.uriOfPath(file.Name()), Range: whole},
		SelectionRange: protocol.Range{Start: positionOf(src, at, s.encoding), End: positionOf(src, at+len(name.Name), s.encoding)},
		Package:        f.Name.Name,
		Dir:            dir,
	}

	switch r.Kind {
	case testrun.KindMain: // its arguments depend on the files beside it, and are written when it is answered
	case testrun.KindBenchmark:
		out.Args = []string{testArg, runFlag, noTests, benchFlag, testrun.Only(name.Name), dir}
	default:
		out.Args = []string{testArg, runFlag, testrun.Only(name.Name), dir}
	}

	return out
}
