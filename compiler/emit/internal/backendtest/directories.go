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

// Directories returns the programs under dir that are directories, as
// snello build DIR takes them, in name order: each a directory holding
// a .snello file, a main package with the packages of its own beside
// it. Its expected output is beside the directory, name.out, as a
// program of one file has beside its file, and the suites ask for it
// as they do for a file. No fixtures are copied beside a directory
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
