// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package importer

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestSetRequire adds a require line after the last one, or at the end
// of a file with none, changes the version of a line already requiring
// the name from the URL, its comment and the blanks before it kept,
// keeps each line's end, \n or \r\n, in a file of one or of both,
// ending a line it adds as the file's first line ends, and refuses a
// name required by a directory, from another repository, or by a line
// too short to read.
func TestSetRequire(t *testing.T) {
	const url = "https://example.com/re"
	for _, c := range []struct{ src, want, err string }{
		{"snello 0.1.0\n", "snello 0.1.0\n\nrequire re " + url + " 0.2.0\n", ""},
		{"", "require re " + url + " 0.2.0\n", ""},
		{"snello 0.1.0\n\nrequire a ../a\nlink m\n", "snello 0.1.0\n\nrequire a ../a\nrequire re " + url + " 0.2.0\nlink m\n", ""},
		{"require re " + url + " 0.1.0 // the regexps\n", "require re " + url + " 0.2.0 // the regexps\n", ""},
		{"require re " + url + " 0.1.0\t// the regexps\n", "require re " + url + " 0.2.0\t// the regexps\n", ""},
		{"snello 0.1.0\r\n\r\nrequire a ../a\r\n", "snello 0.1.0\r\n\r\nrequire a ../a\r\nrequire re " + url + " 0.2.0\r\n", ""},
		{"snello 0.1.0\r\n", "snello 0.1.0\r\n\r\nrequire re " + url + " 0.2.0\r\n", ""},
		{"snello 0.1.0\r\nrequire a ../a\nrequire re " + url + " 0.1.0\n", "snello 0.1.0\r\nrequire a ../a\nrequire re " + url + " 0.2.0\n", ""},
		{"snello 0.1.0\nrequire re " + url + " 0.1.0 // r\r\n", "snello 0.1.0\nrequire re " + url + " 0.2.0 // r\r\n", ""},
		{"snello 0.1.0\r\nrequire a ../a\n", "snello 0.1.0\r\nrequire a ../a\nrequire re " + url + " 0.2.0\r\n", ""},
		{"snello 0.1.0\nrequire a ../a\r\n", "snello 0.1.0\nrequire a ../a\r\nrequire re " + url + " 0.2.0\n", ""},
		{"require re ../re\n", "", "re is required already, from ../re: remove it first"},
		{"snello 0.1.0\nrequire re\n", "", "snello.mod:2: require needs a name and a directory, or a name, a repository's URL and a version"},
		{"require re https://example.com/other 0.1.0\n", "", "re is required already, from https://example.com/other: remove it first"},
	} {
		got, err := SetRequire(c.src, "re", url, "0.2.0")
		if c.err != "" {
			if err == nil || err.Error() != c.err {
				t.Errorf("%q: %v, want %q", c.src, err, c.err)
			}

			continue
		}

		if err != nil || got != c.want {
			t.Errorf("%q: %q, %v, want %q", c.src, got, err, c.want)
		}
	}
}

// TestDropRequire takes the require lines of the names out, closes up
// one blank line where a removal makes two runs of them meet, whichever
// side of it they were, and those it leaves at the end, and no others,
// keeps each line's end in a file of one or of both, and refuses a name
// the file does not require.
func TestDropRequire(t *testing.T) {
	src := "snello 0.1.0\n\nrequire a ../a\nrequire re https://example.com/re 0.2.0\nlink m\n"
	for _, c := range []struct{ src, want string }{
		{src, "snello 0.1.0\n\nrequire a ../a\nlink m\n"},
		{"snello 0.1.0\n\nrequire re https://example.com/re 0.2.0\n", "snello 0.1.0\n"},
		{"snello 0.1.0\n\nrequire re https://example.com/re 0.2.0\n\nlink m\n\n\nlink n\n", "snello 0.1.0\n\nlink m\n\n\nlink n\n"},
		{"snello 0.1.0\r\n\r\nrequire a ../a\r\nrequire re https://example.com/re 0.2.0\r\n", "snello 0.1.0\r\n\r\nrequire a ../a\r\n"},
		{"require re https://example.com/re 0.2.0\n", ""},
		{"x\n\nrequire re https://example.com/re 0.2.0\n\n\ny\n", "x\n\n\ny\n"},
		{"x\nrequire re https://example.com/re 0.2.0\n\n\ny\n", "x\n\n\ny\n"},
		{"snello 0.1.0\n\nrequire re https://example.com/re 0.2.0\n\n", "snello 0.1.0\n"},
		{"snello 0.1.0\r\nrequire re https://example.com/re 0.2.0\nrequire a ../a\n", "snello 0.1.0\r\nrequire a ../a\n"},
		{"snello 0.1.0\n\nrequire re https://example.com/re 0.2.0\r\n", "snello 0.1.0\n"},
	} {
		if got, err := DropRequire(c.src, []string{"re"}); err != nil || got != c.want {
			t.Errorf("dropping re from %q: %q, %v, want %q", c.src, got, err, c.want)
		}
	}

	if _, err := DropRequire(src, []string{"nosuch"}); err == nil || err.Error() != "nosuch is not required by snello.mod" {
		t.Errorf("dropping a name not required: %v", err)
	}
}

// TestRewrite writes a module file and locks what it requires; one
// whose requirement cannot be fetched or that a module file refuses
// leaves both files as they were; and one whose last write fails puts
// the lock file back as it was, or removes the one it wrote.
func TestRewrite(t *testing.T) {
	modCache(t)
	_, url := gitRepo(t, map[string]string{"lib.snello": greetLib}, "0.1.0")
	dir := writeTree(t, map[string]string{ModFile: "snello 0.1.0\n"})
	m, err := Rewrite(dir, "snello 0.1.0\n\nrequire greet "+url+" 0.1.0\n")
	if err != nil || len(m.Requires) != 1 || m.Requires[0].Dir == "" {
		t.Fatalf("a rewrite requiring a tag: %+v, %v", m, err)
	}

	if _, err := os.Stat(filepath.Join(dir, LockFile)); err != nil {
		t.Errorf("no lock file after the rewrite: %v", err)
	}

	before := readFile(t, filepath.Join(dir, ModFile))
	lock := readFile(t, filepath.Join(dir, LockFile))
	for _, src := range []string{before + "require other " + url + " 9.9.9\n", before + "require fmt " + url + " 0.1.0\n"} {
		if _, err := Rewrite(dir, src); err == nil {
			t.Fatalf("a rewrite to %q succeeded", src)
		}

		if got := readFile(t, filepath.Join(dir, ModFile)); got != before {
			t.Errorf("a failed rewrite left snello.mod %q, want %q", got, before)
		}

		if got := readFile(t, filepath.Join(dir, LockFile)); got != lock {
			t.Errorf("a failed rewrite left snello.lock %q, want %q", got, lock)
		}
	}

	// A module file that is a directory holding a file cannot be
	// replaced, so the last write fails after the lock file is written;
	// the lock file kept records a module the rewrite does not reach,
	// which the lock file written drops.
	const kept = "https://example.com/gone 1.0.0 0123456789abcdef0123456789abcdef01234567 h1:x\n"
	for _, locked := range []bool{true, false} {
		dir := writeTree(t, map[string]string{ModFile + "/held": ""})
		if locked {
			writeFiles(t, dir, map[string]string{LockFile: kept})
		}

		if _, err := Rewrite(dir, "snello 0.1.0\n\nrequire greet "+url+" 0.1.0\n"); err == nil {
			t.Fatal("a rewrite over a directory succeeded")
		}

		got, err := os.ReadFile(filepath.Join(dir, LockFile))
		if locked && string(got) != kept || !locked && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("after a failed last write, with a lock file %v: %q, %v", locked, got, err)
		}
	}
}

// TestImportsOf finds every import of a required name or a path under
// it, with its file and line, wherever a build could compile one: in
// source and test files, aliased, in a module nested under the root, in
// a directory under _, and in a file that does not parse; and none in
// a hidden directory, in an ignored file, or of a longer name.
func TestImportsOf(t *testing.T) {
	dir := writeTree(t, map[string]string{
		ModFile:                 "snello 0.1.0\n",
		"main.snello":           "package main\n\nimport \"greet\"\n\nfunc main() { greet.Hello() }\n",
		"tool/tool_test.snello": "package tool\n\nimport (\n\t\"testing\"\n\tg \"greet/fmt\"\n)\n",
		"other/other.snello":    "package other\n\nimport \"greeting\"\n",
		"nested/snello.mod":     "snello 0.1.0\n",
		"nested/n.snello":       "package n\n\nimport \"greet\"\n",
		"_gen/gen.snello":       "package gen\n\nimport \"greet\"\n",
		"broken/broken.snello":  "package broken\n\nimport \"greet\"\nfunc {",
		".hidden/h.snello":      "package h\n\nimport \"greet\"\n",
		"lib/_skip.snello":      "package lib\n\nimport \"greet\"\n",
	})
	want := []string{
		filepath.Join(dir, "_gen", "gen.snello") + ":3",
		filepath.Join(dir, "broken", "broken.snello") + ":3",
		filepath.Join(dir, "main.snello") + ":3",
		filepath.Join(dir, "nested", "n.snello") + ":3",
		filepath.Join(dir, "tool", "tool_test.snello") + ":5",
	}
	slices.Sort(want)
	if got, err := ImportsOf(dir, "greet"); err != nil || !slices.Equal(got, want) {
		t.Errorf("ImportsOf = %v, %v, want %v", got, err, want)
	}
}
