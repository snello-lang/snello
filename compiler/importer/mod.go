// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package importer

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// ModFile is the name of the module file, as go.mod is Go's.
const ModFile = "snello.mod"

// A Module is a directory tree of packages, marked by a file snello.mod
// at its root (_docs/design/08-packages.md): the directory holding the
// file, and its require lines, each naming a dependency and the
// directory on disk that holds it. The counterpart of Go's
// modfile.File with the module path and the versions left out: a
// module has no name, since an import names a directory under the
// root, and a dependency is known by the name its require line gives.
type Module struct {
	Dir      string
	Requires []Require
	Links    []string // the system libraries its link lines name, -lNAME at the link of a program that imports a package of the module
}

// A Require is one require line: the name a program imports the
// dependency by, the first element of every import path into it, and
// the directory holding it, made absolute.
type Require struct {
	Name string
	Dir  string
}

// FindModule returns the module of the nearest snello.mod in dir or
// in a directory above it, as the Go tool finds go.mod, or nil when
// there is none. An error is a module file that does not read, or a
// directory of the root that a standard package's name would shadow.
func FindModule(dir string) (*Module, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	for {
		name := filepath.Join(dir, ModFile)
		if src, err := os.ReadFile(name); err == nil {
			m, err := ParseModFile(name, src)
			if err != nil {
				return nil, err
			}

			if err := m.checkShadows(); err != nil {
				return nil, err
			}

			return m, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, nil
		}

		dir = parent
	}
}

// ParseModFile reads a module file, named for its errors, whose
// directory is the module's root. The file holds require and link
// directives, one a line, with // comments, and may be empty, since
// marking the root is its first job. Go's module directive is refused
// with what takes its place.
func ParseModFile(name string, src []byte) (*Module, error) {
	m := &Module{Dir: filepath.Dir(name)}
	for i, line := range strings.Split(string(src), "\n") {
		if j := strings.Index(line, "//"); j >= 0 {
			line = line[:j]
		}

		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		at := name + ":" + strconv.Itoa(i+1) + ": "
		switch fields[0] {
		case "require":
			if len(fields) != 3 {
				return nil, errors.New(at + "require needs a name and a directory")
			}

			if err := checkRequireName(fields[1]); err != nil {
				return nil, errors.New(at + err.Error())
			}

			for _, r := range m.Requires {
				if r.Name == fields[1] {
					return nil, errors.New(at + "a second require of " + fields[1])
				}
			}

			dir := fields[2]
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(m.Dir, dir)
			}

			m.Requires = append(m.Requires, Require{fields[1], dir})
		case "link":
			if len(fields) != 2 {
				return nil, errors.New(at + "link needs a library name")
			}

			if strings.HasPrefix(fields[1], "-") || strings.ContainsAny(fields[1], "/\\") {
				return nil, errors.New(at + "link names a library the system's linker finds, as -l does, not a flag or a path")
			}

			if !slices.Contains(m.Links, fields[1]) {
				m.Links = append(m.Links, fields[1])
			}
		case "module":
			return nil, errors.New(at + "a module has no name: the file marks the root, and an import names a directory under it")
		default:
			return nil, errors.New(at + "unknown directive " + strconv.Quote(fields[0]))
		}
	}

	return m, nil
}

// Lookup returns the directory the import path names: the directory
// at that place under the module's root, when there is one spelled as
// the path spells it, or the directory a require line gives for the
// path's first element, with the line. A path both answer is refused
// as ambiguous, and one neither answers gives nothing.
func (m *Module) Lookup(path string) (dir string, req *Require, err error) {
	own := ""
	if exists(m.Dir, path) {
		own = filepath.Join(m.Dir, filepath.FromSlash(path))
	}

	first, rest, _ := strings.Cut(path, "/")
	for i := range m.Requires {
		if m.Requires[i].Name != first {
			continue
		}

		if own != "" {
			return "", nil, errors.New("import " + strconv.Quote(path) + " is both a directory of the module and under the required " + first)
		}

		return filepath.Join(m.Requires[i].Dir, filepath.FromSlash(rest)), &m.Requires[i], nil
	}

	return own, nil, nil
}

// exists reports whether the directory path names under root exists
// with each element spelled as the path spells it, read from the
// directory's entries rather than asked of the file system, which on a
// case-insensitive one would answer Shapes for shapes and let one
// directory load twice under two paths, as the go command refuses a
// case-insensitive import collision.
func exists(root, path string) bool {
	dir := root
	for _, elem := range strings.Split(path, "/") {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return false
		}

		found := false
		for _, e := range entries {
			if e.Name() == elem && e.IsDir() {
				found = true
				break
			}
		}

		if !found {
			return false
		}

		dir = filepath.Join(dir, elem)
	}

	return true
}

// checkShadows refuses a directory of the root that holds a package
// under a standard package's name, since the standard library answers
// an import first and the directory could never be reached.
func (m *Module) checkShadows() error {
	entries, err := os.ReadDir(m.Dir)
	if err != nil {
		return err
	}

	for _, e := range entries {
		if !e.IsDir() || !standard[e.Name()] {
			continue
		}

		if files, err := SourceFiles(filepath.Join(m.Dir, e.Name())); err == nil && len(files) > 0 {
			return errors.New(filepath.Join(m.Dir, ModFile) + ": the directory " + e.Name() + " shadows the standard package " + e.Name() + "; rename it")
		}
	}

	return nil
}

// standard holds the first element of every standard package's path,
// which no require line and no directory of a module may take.
var standard = func() map[string]bool {
	names := make(map[string]bool)
	for _, p := range Paths() {
		first, _, _ := strings.Cut(p, "/")
		names[first] = true
	}

	return names
}()

// checkRequireName says what is wrong with the name a require line
// gives: it is one element, the first of every import path into the
// dependency, since the rest of a path names a directory under it, and
// it must not be a standard package's, which the standard library
// would answer first.
func checkRequireName(name string) error {
	if err := checkImportPath(name); err != nil {
		return err
	}

	if strings.Contains(name, "/") {
		return errors.New("a required name is one element, and " + strconv.Quote(name) + " has a /: the rest of a path names a directory under the dependency")
	}

	if standard[name] {
		return errors.New(name + " is a standard package")
	}

	return nil
}

// checkImportPath says what is wrong with an import path: an element
// that is empty, or that is . or .., as go/build refuses them, or a
// character outside letters, digits, . - and _, as the go command
// refuses, which keeps ~ for the importer's own ids.
func checkImportPath(path string) error {
	for _, elem := range strings.Split(path, "/") {
		switch elem {
		case "":
			return errors.New("malformed import path " + strconv.Quote(path) + ": empty path element")
		case ".", "..":
			return errors.New("malformed import path " + strconv.Quote(path) + ": a relative path; name the package by its directory under the module")
		}

		for _, c := range elem {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
				return errors.New("malformed import path " + strconv.Quote(path) + ": invalid character " + strconv.QuoteRune(c))
			}
		}
	}

	return nil
}
