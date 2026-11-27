// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docModule is a module with a library package, geo, whose comments the
// command prints, and a name it must not.
var docModule = map[string]string{
	"snello.mod": "// the module\nsnello 0.1.0\n",
	"main.snello": `// Command shapes draws.
package main

import "geo"

func main() { geo.Area(2) }
`,
	"geo/geo.snello": `// Package geo measures.
package geo

// Area returns the area of a square of side s.
func Area(s int) int { return s * s }

func secret() int { return 1 }
`,
}

// runDoc runs snello doc with args in dir and returns its exit code and
// what it wrote.
func runDoc(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	t.Chdir(dir)
	var out, errs strings.Builder
	code := run(append([]string{docCommand}, args...), &out, &errs)
	return code, out.String(), errs.String()
}

func writeDocModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

// TestDocStandard prints a standard package, a type and a method, each
// with its comment, and nothing unexported.
func TestDocStandard(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		args   []string
		has    []string
		hasNot []string
	}{
		{[]string{"strings"}, []string{`package strings // import "strings"`, "Package strings builds", "struct Builder { ... }", "func Join("}, []string{"appendString"}},
		{[]string{"strings.Builder"}, []string{"struct Builder {", "// Has unexported fields.", "A Builder accumulates text", "func (b mut Builder) WriteString(s ref string)"}, []string{"buf []byte"}},
		{[]string{"strings.Builder.WriteString"}, []string{"func (b mut Builder) WriteString(s ref string)", "appends the bytes of s"}, nil},
		{[]string{"json"}, []string{`package json // import "encoding/json"`}, nil},
		{[]string{"-u", "strings"}, []string{"appendString"}, nil},
		{[]string{"-all", "strings"}, []string{"WriteString appends the bytes of s"}, nil},
		{[]string{"io.Reader"}, []string{"Implemented by:", "strings.Reader"}, nil},
		{[]string{"builtin.Option"}, []string{"enum Option<T> {"}, nil},
		{[]string{"builtin"}, []string{"type int int", "func len("}, nil},
		{[]string{"builtin.len"}, []string{"func len("}, nil},
		{[]string{"len"}, []string{"func len("}, nil},
	} {
		code, out, errs := runDoc(t, dir, c.args...)
		if code != exitSuccess || errs != "" {
			t.Errorf("%v: exit %d, %s", c.args, code, errs)
			continue
		}

		for _, want := range c.has {
			if !strings.Contains(out, want) {
				t.Errorf("%v: output lacks %q:\n%s", c.args, want, out)
			}
		}

		for _, unwanted := range c.hasNot {
			if strings.Contains(out, unwanted) {
				t.Errorf("%v: output has %q", c.args, unwanted)
			}
		}
	}
}

// TestDocModule prints a package of the program's module by its path
// and by its directory, and the program's own from its root.
func TestDocModule(t *testing.T) {
	mod := writeDocModule(t, docModule)
	for _, c := range []struct {
		dir  string
		args []string
		want string
	}{
		{mod, []string{"geo"}, "func Area(s int) int"},
		{mod, []string{"./geo"}, "Package geo measures."},
		{filepath.Join(mod, "geo"), nil, "Package geo measures."},
		{mod, []string{"geo.Area"}, "Area returns the area of a square"},
		{mod, nil, "Command shapes draws."},
	} {
		code, out, errs := runDoc(t, c.dir, c.args...)
		if code != exitSuccess || !strings.Contains(out, c.want) || strings.Contains(out, "secret") {
			t.Errorf("%v in %s: exit %d, %s\n%s", c.args, c.dir, code, errs, out)
		}
	}
}

// TestDocErrors holds the command to its messages and exit codes for a
// name or a package it cannot find, and a name several packages have.
func TestDocErrors(t *testing.T) {
	dir := t.TempDir()
	for args, want := range map[string]string{
		"strings.Nope": "snello doc: no Nope in package strings",
		"nopkg":        "snello doc: no package or name nopkg",
		"Reader":       "snello doc: Reader is in bufio, io and strings",
		"builtin.Nope": "snello doc: no Nope in package builtin",
	} {
		code, out, errs := runDoc(t, dir, args)
		if code != exitFailure || out != "" || strings.TrimSpace(errs) != want {
			t.Errorf("%s: exit %d, stdout %q, stderr %q, want %q", args, code, out, errs, want)
		}
	}
}

// TestDocBrokenLink warns of a link that names nothing, and prints the
// page.
func TestDocBrokenLink(t *testing.T) {
	mod := writeDocModule(t, map[string]string{
		"snello.mod":     "// the module\nsnello 0.1.0\n",
		"geo/geo.snello": "// Package geo measures, see [Nowhere].\npackage geo\n",
	})
	code, out, errs := runDoc(t, mod, "geo")
	if code != exitSuccess || !strings.Contains(out, "see [Nowhere]") || !strings.Contains(errs, "geo.snello:1:1: [Nowhere] names nothing") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errs)
	}
}

// TestDocBrokenLinkTwice warns of a name linked on two lines of one
// comment at each of the two lines, not twice at the first, and of none
// in a line of code between them.
func TestDocBrokenLinkTwice(t *testing.T) {
	mod := writeDocModule(t, map[string]string{
		"snello.mod":     "// the module\nsnello 0.1.0\n",
		"geo/geo.snello": "// Package geo measures, see [Nowhere].\n//\n//\tidx := m[Nowhere]\n//\n// And [Nowhere] once more.\npackage geo\n",
	})
	code, _, errs := runDoc(t, mod, "geo")
	want := []string{"geo.snello:1:1: [Nowhere] names nothing", "geo.snello:5:1: [Nowhere] names nothing"}
	got := strings.Split(strings.TrimSpace(errs), "\n")
	if code != exitSuccess || len(got) != len(want) || !strings.HasSuffix(got[0], want[0]) || !strings.HasSuffix(got[1], want[1]) {
		t.Errorf("exit %d, stderr %q, want lines ending %q", code, errs, want)
	}
}

// TestDocBrokenLinkBlock warns of a link on a block comment's later
// line at that line.
func TestDocBrokenLinkBlock(t *testing.T) {
	mod := writeDocModule(t, map[string]string{
		"snello.mod":     "// the module\nsnello 0.1.0\n",
		"geo/geo.snello": "/* Package geo measures.\n\n See [Nowhere]. */\npackage geo\n",
	})
	code, _, errs := runDoc(t, mod, "geo")
	if want := "geo.snello:3:1: [Nowhere] names nothing"; code != exitSuccess || !strings.Contains(errs, want) || strings.Count(errs, "names nothing") != 1 {
		t.Errorf("exit %d, stderr %q, want one warning ending %q", code, errs, want)
	}
}

// TestDocExamples shows a name's example, from the package's test file,
// on its page.
func TestDocExamples(t *testing.T) {
	mod := writeDocModule(t, map[string]string{
		"snello.mod":     "// the module\nsnello 0.1.0\n",
		"geo/geo.snello": "// Package geo measures.\npackage geo\n\n// Area returns the area of a square of side s.\nfunc Area(s int) int { return s * s }\n",
		"geo/geo_test.snello": `package geo

import "fmt"

func ExampleArea() {
	fmt.Println(Area(3))
	// Output: 9
}
`,
	})
	code, out, errs := runDoc(t, mod, "geo.Area")
	want := "Example:\n    fmt.Println(Area(3))\n\n    Output:\n    9\n"
	if code != exitSuccess || errs != "" || !strings.HasSuffix(out, want) {
		t.Errorf("exit %d, stderr %q, output:\n%s\nwant it to end:\n%s", code, errs, out, want)
	}
}

// TestDocDirCase takes a directory for a package only when its name is
// written with its case, so that a name with a directory's letters is
// looked for as a name on a system whose files ignore case.
func TestDocDirCase(t *testing.T) {
	mod := writeDocModule(t, docModule)
	if !isDirNamed(mod, "geo") || isDirNamed(mod, "Geo") || isDirNamed(mod, "nowhere") {
		t.Error("a directory taken by its letters whatever their case, or one that is not there")
	}
}
