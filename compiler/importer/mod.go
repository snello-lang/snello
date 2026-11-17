// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package importer

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/version"
)

const (
	// SourceExt ends the name of every Snello source file, TestSuffix
	// the name of a package of tests outside the package it tests, and
	// TestFileSuffix the name of a test file.
	SourceExt      = ".snello"
	TestSuffix     = "_test"
	TestFileSuffix = TestSuffix + SourceExt

	// testingPackage and expectPackage are the packages only test
	// files import.
	testingPackage = "testing"
	expectPackage  = "expect"

	// ModFile is the name of the module file, and modFileMode the
	// permissions WriteModFile gives one: read by all, written by its
	// owner.
	ModFile     = "snello.mod"
	modFileMode = 0o644

	// The directives of a module file: the version of Snello the
	// module is written for, a dependency, a system library.
	versionDirective = "snello"
	requireDirective = "require"
	linkDirective    = "link"

	// The words of each directive's line, its name among them: snello
	// VERSION, require NAME DIR, link NAME.
	versionFields = 2
	requireFields = 3
	linkFields    = 2

	// lineEnd ends a line of a module file, fieldSeparator parts the
	// words of a line WriteModFile writes, and commentStart begins a
	// comment that runs to the end of its line.
	lineEnd        = "\n"
	fieldSeparator = " "
	commentStart   = "//"

	// pathSeparator joins the elements of an import path, whatever
	// the system's separator is.
	pathSeparator = "/"

	// flagStart begins a linker flag and filePathChars are the
	// separators of a file's path, which a link line's library name
	// holds neither of.
	flagStart     = "-"
	filePathChars = "/\\"

	// currentDir and parentDir are the elements of a relative path,
	// and importPathChars the characters an element of an import path
	// may hold, which keeps ~ for the importer's own ids.
	currentDir      = "."
	parentDir       = ".."
	importPathChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-_"
)

// A Module is a directory tree of packages, marked by a file snello.mod
// at its root: the directory holding the file, the version of Snello it
// is written for, and its require lines, each naming a dependency and
// the directory on disk that holds it. A module has no name, since an
// import names a directory under the root, and a dependency is known
// by the name its require line gives.
type Module struct {
	Dir      string
	Version  string // the version of Snello the module is written for, as its snello line writes it, or "" for a file with no line, read as written for version.First
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

// standard holds the first element of every standard package's path,
// which no require line and no directory of a module may take.
var standard = func() map[string]bool {
	names := make(map[string]bool)
	for _, p := range Paths() {
		first, _, _ := strings.Cut(p, pathSeparator)
		names[first] = true
	}

	return names
}()

// FindModule returns the module of the nearest snello.mod in dir or
// in a directory above it, the nearest first, or nil when
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
// directory is the module's root. The file holds snello, require and
// link directives, one a line, with // comments, and may be empty,
// since marking the root is its first job. A snello line names the
// version of Snello the module is written for, a SemVer version, and
// a module written for a later version than this compiler is refused
// before any other line is read, since a later release may have
// written a line this compiler does not know, and what to say then is
// that the compiler is too old. Any other word that begins a line is
// an unknown directive.
func ParseModFile(name string, src []byte) (*Module, error) {
	lines := strings.Split(string(src), lineEnd)
	if err := checkLater(name, lines); err != nil {
		return nil, err
	}

	m := &Module{Dir: filepath.Dir(name)}
	for i, line := range lines {
		fields := directive(line)
		if len(fields) == 0 {
			continue
		}

		at := lineAt(name, i)
		switch fields[0] {
		case versionDirective:
			if m.Version != "" {
				return nil, errors.New(at + "a second snello line")
			}

			if len(fields) != versionFields {
				return nil, errors.New(at + "snello needs one version, snello " + version.First)
			}

			if err := checkVersion(fields[1]); err != nil {
				return nil, errors.New(at + err.Error())
			}

			m.Version = fields[1]
		case requireDirective:
			if len(fields) != requireFields {
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
		case linkDirective:
			if len(fields) != linkFields {
				return nil, errors.New(at + "link needs a library name")
			}

			if strings.HasPrefix(fields[1], flagStart) || strings.ContainsAny(fields[1], filePathChars) {
				return nil, errors.New(at + "link names a library the system's linker finds, as -l does, not a flag or a path")
			}

			if !slices.Contains(m.Links, fields[1]) {
				m.Links = append(m.Links, fields[1])
			}
		default:
			return nil, errors.New(at + "unknown directive " + strconv.Quote(fields[0]))
		}
	}

	return m, nil
}

// ModuleWarning returns what a command says, before it loads the
// program in dir, of the module the program is in when its file has no
// snello line, which is read as written for the first release: the
// file's path, the version it is read as and the line to add, so that
// the module's author adds it. It returns "" for a module whose file
// has the line, a program in no module, and a module file that does not
// read, which the load reports. It reads the module file.
func ModuleWarning(dir string) string {
	m, err := FindModule(dir)
	if err != nil || m == nil || m.Version != "" {
		return ""
	}

	return "warning: " + filepath.Join(m.Dir, ModFile) + " has no snello line: it is read as snello " + version.First + ", the first release; add the line \"snello " + version.First + "\""
}

// WriteModFile starts a module in dir: it writes the module file there,
// holding the snello line of this compiler's version, and returns the
// file's absolute path. A module file already in dir is refused and
// left as it is, a file another command or person creates at the same
// moment included, since the file is created only when none exists; so
// is anything else under the file's name, a directory or a link to
// nothing, which is in its way. A file it fails to write is removed.
// Every command that starts a module writes its file through it. It
// creates and writes one file.
//
//	path, err := importer.WriteModFile(".")
func WriteModFile(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}

	name := filepath.Join(dir, ModFile)
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, modFileMode)
	if errors.Is(err, fs.ErrExist) {
		if info, serr := os.Stat(name); serr == nil && info.Mode().IsRegular() {
			return "", errors.New(name + " already exists: the directory is a module's root")
		}

		return "", errors.New(name + " is in the way: something that is not a module file has its name")
	}

	if err != nil {
		return "", err
	}

	_, err = f.WriteString(versionDirective + fieldSeparator + version.Current + lineEnd)
	if cerr := f.Close(); err == nil {
		err = cerr
	}

	if err != nil {
		os.Remove(name) // a file half written is no module file
		return "", err
	}

	return name, nil
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

	first, rest, _ := strings.Cut(path, pathSeparator)
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
// directory load twice under two paths.
func exists(root, path string) bool {
	dir := root
	for _, elem := range strings.Split(path, pathSeparator) {
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

// directive returns the words of a line of a module file, its comment
// left out: none for a blank line or a comment alone. It allocates the
// list of words.
func directive(line string) []string {
	if j := strings.Index(line, commentStart); j >= 0 {
		line = line[:j]
	}

	return strings.Fields(line)
}

// checkLater refuses a module file, named for the message, whose
// snello line, the first line that begins with snello, names a later
// version than this compiler's, with that line, whatever the file's
// other lines hold. Any other snello line, and a first one that is not
// a version alone, are the strict reading's to report, since no release
// would read the file then. It splits the lines up to the first snello
// line into words.
func checkLater(name string, lines []string) error {
	current, _ := version.Parse(version.Current) // Current is a SemVer version, which the version package's tests hold it to
	for i, line := range lines {
		fields := directive(line)
		if len(fields) == 0 || fields[0] != versionDirective {
			continue
		}

		if len(fields) == versionFields {
			if v, ok := version.Parse(fields[1]); ok && v.Compare(current) > 0 {
				return errors.New(lineAt(name, i) + "this module needs snello " + fields[1] + "; this is snello " + version.Current + ": upgrade the compiler")
			}
		}

		return nil
	}

	return nil
}

// lineAt returns the start of a message about the line at index i of
// the module file name: the file and the line's number, from 1.
func lineAt(name string, i int) string {
	return name + ":" + strconv.Itoa(i+1) + ": "
}

// checkVersion says what is wrong with the version a snello line
// names: a spelling Semantic Versioning 2.0 does not have. A later
// version than this compiler's was refused before, by checkLater. It
// allocates the version's pre-release identifiers.
func checkVersion(s string) error {
	if _, ok := version.Parse(s); !ok {
		return errors.New("malformed version " + strconv.Quote(s) + ": a version is MAJOR.MINOR.PATCH, snello " + version.First)
	}

	return nil
}

// checkRequireName says what is wrong with the name a require line
// gives: it is one element, the first of every import path into the
// dependency, since the rest of a path names a directory under it, and
// it must not be a standard package's, which the standard library
// would answer first.
func checkRequireName(name string) error {
	if err := checkImportPath(name); err != nil {
		return err
	}

	if strings.Contains(name, pathSeparator) {
		return errors.New("a required name is one element, and " + strconv.Quote(name) + " has a /: the rest of a path names a directory under the dependency")
	}

	if standard[name] {
		return errors.New(name + " is a standard package")
	}

	return nil
}

// checkImportPath says what is wrong with an import path: an element
// that is empty, or that is . or .., or a character outside letters,
// digits, . - and _, which keeps ~ for the importer's own ids.
func checkImportPath(path string) error {
	for _, elem := range strings.Split(path, pathSeparator) {
		switch elem {
		case "":
			return errors.New("malformed import path " + strconv.Quote(path) + ": empty path element")
		case currentDir, parentDir:
			return errors.New("malformed import path " + strconv.Quote(path) + ": a relative path; name the package by its directory under the module")
		}

		for _, c := range elem {
			if !strings.ContainsRune(importPathChars, c) {
				return errors.New("malformed import path " + strconv.Quote(path) + ": invalid character " + strconv.QuoteRune(c))
			}
		}
	}

	return nil
}
