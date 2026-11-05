// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package importer

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ModFile is the name of the module file, as go.mod is Go's.
const ModFile = "snello.mod"

// A Module is a directory tree of packages under one import path, as
// its snello.mod names it (_docs/design/08-packages.md): the module
// path, the directory holding the file, and the require lines, each
// naming an import path and the directory on disk that holds it. The
// counterpart of Go's modfile.File, with versions left out.
type Module struct {
	Path     string
	Dir      string
	Requires []Require
}

// A Require is one require line: the import path it names and the
// directory holding that path's packages, made absolute.
type Require struct {
	Path string
	Dir  string
}

// FindModule returns the module of the nearest snello.mod in dir or
// in a directory above it, as the Go tool finds go.mod, or nil when
// there is none. An error is a module file that does not read.
func FindModule(dir string) (*Module, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	for {
		name := filepath.Join(dir, ModFile)
		if src, err := os.ReadFile(name); err == nil {
			return ParseModFile(name, src)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, nil
		}

		dir = parent
	}
}

// ParseModFile reads a module file, named for its errors, whose
// directory is the module's. The file holds a module directive and
// require directives, one a line, with // comments.
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
		case "module":
			if m.Path != "" {
				return nil, errors.New(at + "a second module directive")
			}

			if len(fields) != 2 {
				return nil, errors.New(at + "module needs a path")
			}

			if err := checkModulePath(fields[1]); err != nil {
				return nil, errors.New(at + err.Error())
			}

			m.Path = fields[1]
		case "require":
			if len(fields) != 3 {
				return nil, errors.New(at + "require needs an import path and a directory")
			}

			if err := checkModulePath(fields[1]); err != nil {
				return nil, errors.New(at + err.Error())
			}

			dir := fields[2]
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(m.Dir, dir)
			}

			m.Requires = append(m.Requires, Require{fields[1], dir})
		default:
			return nil, errors.New(at + "unknown directive " + strconv.Quote(fields[0]))
		}
	}

	if m.Path == "" {
		return nil, errors.New(name + ": no module directive")
	}

	return m, nil
}

// PackageDir returns the directory the import path names under the
// module, or under one of its require lines, which is returned with
// it; neither having the path gives false.
func (m *Module) PackageDir(path string) (dir string, req *Require, ok bool) {
	if rest, ok := under(path, m.Path); ok {
		return filepath.Join(m.Dir, filepath.FromSlash(rest)), nil, true
	}

	for i := range m.Requires {
		if rest, ok := under(path, m.Requires[i].Path); ok {
			return filepath.Join(m.Requires[i].Dir, filepath.FromSlash(rest)), &m.Requires[i], true
		}
	}

	return "", nil, false
}

// under reports whether path is root or below it, with the rest of the
// path after root.
func under(path, root string) (string, bool) {
	if path == root {
		return "", true
	}

	if strings.HasPrefix(path, root+"/") {
		return path[len(root)+1:], true
	}

	return "", false
}

// checkModulePath says what is wrong with a module path: its first
// element must have a dot, as Go asks of a path a registry serves, so
// that it is never a standard package's and its symbols are never the
// runtime's; and it must be an import path.
func checkModulePath(path string) error {
	if err := checkImportPath(path); err != nil {
		return err
	}

	if !strings.Contains(path[:strings.IndexByte(path+"/", '/')], ".") {
		return errors.New("malformed module path " + strconv.Quote(path) + ": missing dot in first path element")
	}

	return nil
}

// checkImportPath says what is wrong with an import path: an element
// that is empty, or that is . or .., as go/build refuses them.
func checkImportPath(path string) error {
	for _, elem := range strings.Split(path, "/") {
		switch elem {
		case "":
			return errors.New("malformed import path " + strconv.Quote(path) + ": empty path element")
		case ".", "..":
			return errors.New("malformed import path " + strconv.Quote(path) + ": a relative path; name the package by its path under the module")
		}
	}

	return nil
}
