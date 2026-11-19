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
	"sort"
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/parser"
)

// What the commands that change a module's dependencies write and
// read: a require line; the carriage return that ends a line written on
// Windows before its \n, kept on each line that has it; the blanks that
// part a comment from a line; and where an import of a required name
// is.
const (
	requireLineStart  = requireDirective + fieldSeparator
	carriageReturn    = "\r"
	crlf              = carriageReturn + lineEnd
	blanks            = " \t"
	positionSeparator = ":"
)

// SetRequire returns the text of a module file, src, with name required
// from the repository at url at version: the version of name's line
// changed, its comment kept, when name is required from url already,
// and else a line added after the last require line, or at the end. A
// name required by a directory, or from another repository, is refused,
// since what to do with the other line is the user's to say. Each
// line's end, \n or \r\n, is kept, and a line added ends as the file's
// first line that ends does.
func SetRequire(src, name, url, version string) (string, error) {
	cr := lineEndOf(src)
	lines := strings.Split(src, lineEnd)
	last := -1
	for i, line := range lines {
		fields := directive(line)
		if len(fields) < 2 || fields[0] != requireDirective {
			continue
		}

		last = i
		if fields[1] != name {
			continue
		}

		if len(fields) < requireFields {
			return "", errors.New(lineAt(ModFile, i) + "require needs a name and a directory, or a name, a repository's URL and a version")
		}

		if len(fields) != requireVersionFields || fields[2] != url {
			return "", errors.New(name + " is required already, from " + fields[2] + ": remove it first")
		}

		body, ended := strings.CutSuffix(line, carriageReturn)
		lines[i] = requireLine(name, url, version) + commentOf(body)
		if ended {
			lines[i] += carriageReturn
		}

		return strings.Join(lines, lineEnd), nil
	}

	added := requireLine(name, url, version) + cr
	if last >= 0 {
		lines = slices.Insert(lines, last+1, added)
		return strings.Join(lines, lineEnd), nil
	}

	src = strings.TrimRight(src, crlf)
	if src != "" {
		src += cr + lineEnd + cr + lineEnd
	}

	return src + added + lineEnd, nil
}

// DropRequire returns the text of a module file, src, without the
// require lines of names, refusing a name it does not require. Where
// the removed lines leave two runs of blank lines meeting, one blank
// line of them is closed up, and blank lines they leave at the end of
// the file are; the rest of the file is kept as it is, each line's end
// among it.
func DropRequire(src string, names []string) (string, error) {
	drop := map[string]bool{}
	for _, n := range names {
		drop[n] = true
	}

	body, ended := strings.CutSuffix(src, lineEnd)
	var kept []string
	removed := false
	lastRemoved := -1 // where in kept the last removed line was
	for _, line := range strings.Split(body, lineEnd) {
		fields := directive(line)
		if len(fields) >= 2 && fields[0] == requireDirective && drop[fields[1]] {
			delete(drop, fields[1])
			removed = true
			lastRemoved = len(kept)
			continue
		}

		if removed && isBlank(line) && (len(kept) == 0 || isBlank(kept[len(kept)-1])) {
			removed = false
			continue
		}

		removed = false
		kept = append(kept, line)
	}

	for _, n := range names {
		if drop[n] {
			return "", errors.New(n + " is not required by " + ModFile)
		}
	}

	if lastRemoved >= 0 && !slices.ContainsFunc(kept[lastRemoved:], func(line string) bool { return !isBlank(line) }) {
		for len(kept) > 0 && isBlank(kept[len(kept)-1]) {
			kept = kept[:len(kept)-1]
		}
	}

	out := strings.Join(kept, lineEnd)
	if ended && out != "" {
		out += lineEnd
	}

	return out, nil
}

// Rewrite makes src the module file of the module rooted at dir when
// it holds: it is read, and its dependencies fetched, selected and
// locked as a build does, before the file is written, through a file
// renamed into place; on any failure the module file is left as it
// was, and the lock file too. It returns the module read.
func Rewrite(dir, src string) (*Module, error) {
	name := filepath.Join(dir, ModFile)
	m, err := ParseModFile(name, []byte(src))
	if err != nil {
		return nil, err
	}

	lockName := filepath.Join(dir, LockFile)
	lock, lockErr := os.ReadFile(lockName)
	if lockErr != nil && !errors.Is(lockErr, os.ErrNotExist) {
		return nil, lockErr
	}

	if err := m.checkShadows(); err != nil {
		return nil, err
	}

	if err := m.Resolve(); err != nil {
		return nil, err
	}

	if err := replaceFile(name, src, modFileMode); err != nil {
		var restoreErr error
		if lockErr == nil {
			restoreErr = replaceFile(lockName, string(lock), lockFileMode)
		} else {
			restoreErr = removeLock(dir)
		}

		return nil, errors.Join(err, restoreErr)
	}

	return m, nil
}

// ImportsOf returns where the packages under the module rooted at dir
// import the required name, a path that is name or begins with it,
// each as a file and a line, sorted. It looks wider than a build
// reaches rather than narrower, since an import it misses is a build
// broken by removing the name, and one it finds too many only asks for
// a look: every source file the build would choose for some platform,
// test files among them, in every directory but a hidden one, a module
// nested under the root and a directory under _ included, and the
// imports a file that does not parse still holds.
func ImportsOf(dir, name string) ([]string, error) {
	var where []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if p != dir && strings.HasPrefix(d.Name(), hiddenPrefix) {
				return filepath.SkipDir
			}

			return nil
		}

		if !strings.HasSuffix(d.Name(), SourceExt) || ignored(d.Name()) {
			return nil
		}

		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}

		f, file, _ := parser.ParseFile(p, src)

		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err == nil && (path == name || strings.HasPrefix(path, name+pathSeparator)) {
				where = append(where, p+positionSeparator+strconv.Itoa(file.Position(imp.Path.Pos()).Line))
			}
		}

		return nil
	})

	sort.Strings(where)
	return where, err
}

// lineEndOf returns what ends a line of the file src before its \n:
// a carriage return when its first line that ends has one, as a
// checkout on Windows writes it, and else nothing.
func lineEndOf(src string) string {
	first, _, found := strings.Cut(src, lineEnd)
	if found && strings.HasSuffix(first, carriageReturn) {
		return carriageReturn
	}

	return ""
}

// commentOf returns the comment that ends line, with the blanks before
// it, or "" when it has none: a comment begins a word, as directive
// reads one.
func commentOf(line string) string {
	for i := 0; i < len(line); i++ {
		if strings.HasPrefix(line[i:], commentStart) && (i == 0 || strings.ContainsRune(blanks, rune(line[i-1]))) {
			return line[len(strings.TrimRight(line[:i], blanks)):]
		}
	}

	return ""
}

// isBlank reports whether line holds nothing but blanks.
func isBlank(line string) bool {
	return strings.Trim(line, blanks+crlf) == ""
}

// requireLine returns the require line of name from url at version.
func requireLine(name, url, version string) string {
	return requireLineStart + name + fieldSeparator + url + fieldSeparator + version
}
