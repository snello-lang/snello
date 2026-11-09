// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package backendtest

import (
	"os"
	"path/filepath"

	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/types"
)

// Shared is the folder of the programs of one file both backends'
// tests build and run, each with its expected output beside it, as a
// path from a backend's package; Examples is the folder of the example
// programs, whose programs of several packages the backends' tests
// build too.
const (
	Shared   = "../internal/backendtest/testdata"
	Examples = "../../../examples"
)

// Directories returns the programs under dir that are directories, as
// snello build DIR takes them, in name order: each a directory holding
// a snello.mod and a .snello file, a main package with the packages of
// its own beside it; a folder of separate programs of one file holds
// no snello.mod. Its expected output is inside the directory, named
// after it, name.out, and the suites ask for it as they do for a
// program of one file. No fixtures are copied beside a directory
// program's binary yet, since none reads a file.
func Directories(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		if _, err := os.Stat(filepath.Join(dir, e.Name(), "snello.mod")); err != nil {
			continue
		}

		files, err := importer.SourceFiles(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}

		if len(files) > 0 {
			dirs = append(dirs, filepath.Join(dir, e.Name()))
		}
	}

	return dirs, nil
}

// LoadDirectory loads the program in dir as snello build DIR does, and
// returns it with the Info the checker filled and the packages it
// imports as the backends take them.
func LoadDirectory(dir string) (*importer.Program, *types.Info, []lower.Library, error) {
	names, err := importer.SourceFiles(dir)
	if err != nil {
		return nil, nil, nil, err
	}

	info := &types.Info{}
	imp, prog, err := importer.LoadProgram(names, info)
	if err != nil {
		return nil, nil, nil, err
	}

	pkgs := imp.Packages()
	libs := make([]lower.Library, len(pkgs))
	for i, p := range pkgs {
		libs[i] = lower.Library{Path: p.Path, File: p.File, Syntax: p.Syntax, CFiles: p.CFiles, Links: p.Links}
	}

	return prog, info, libs, nil
}
