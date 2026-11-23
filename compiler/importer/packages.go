// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package importer

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// testdataDir names a directory whose files are a test's data and hold
// no package of the module.
const testdataDir = "testdata"

// Packages returns the directories of the module rooted at root that
// hold a package of its own, sorted: every directory under the root,
// the root's included, with a file a build for some platform would
// choose, its source files or its tests, but a hidden one, one under
// _, a testdata one, and a module nested in it, which is a module of
// its own.
func Packages(root string) []string {
	var dirs []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}

		if p != root {
			if ignored(d.Name()) || d.Name() == testdataDir {
				return filepath.SkipDir
			}

			if _, err := os.Stat(filepath.Join(p, ModFile)); err == nil {
				return filepath.SkipDir
			}
		}

		if len(PackageFiles(p)) > 0 {
			dirs = append(dirs, p)
		}

		return nil
	})

	return dirs
}

// PackageFiles returns every file of the package in dir a build for
// some platform would choose, its source files and its tests, sorted,
// each with its directory: poll_linux.snello and poll_windows.snello
// both, so that what reads a package's files reads every platform's.
func PackageFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var files []string
	for _, e := range entries {
		if name := e.Name(); !e.IsDir() && !ignored(name) && strings.HasSuffix(name, SourceExt) {
			files = append(files, filepath.Join(dir, name))
		}
	}

	slices.Sort(files)
	return files
}

// Modules returns the roots of the modules a folder holds, sorted: the
// module the folder is in, if any, and every module under the folder,
// a module's nested modules among them, but none in a hidden
// directory, one under _ or a testdata one.
func Modules(folder string) []string {
	var roots []string
	if m, err := FindModule(folder); err == nil && m != nil {
		roots = append(roots, m.Dir)
	}

	filepath.WalkDir(folder, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}

		if p != folder && (ignored(d.Name()) || d.Name() == testdataDir) {
			return filepath.SkipDir
		}

		if _, err := os.Stat(filepath.Join(p, ModFile)); err == nil && !slices.Contains(roots, p) {
			roots = append(roots, p)
		}

		return nil
	})

	slices.Sort(roots)
	return roots
}
