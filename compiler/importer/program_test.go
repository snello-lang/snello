// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package importer

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/types"
)

// TestLoadProgram loads a main package of two files with a package of
// the program's own beside it, found through the module file, as
// snello build DIR does; a program of one file; two files that disagree
// on the package clause, refused with both named; and what CheckMain
// says of a program under another package clause, a library, which
// has no main, a main package without main, and one whose main is a
// variable.
func TestLoadProgram(t *testing.T) {
	root := writeTree(t, map[string]string{
		"snello.mod":           "module example.com/app\n",
		"shapes/shape.snello":  shapesSrc,
		"shapes/area.snello":   areaSrc,
		"nomain/a.snello":      "package main\nfunc f() {}\n",
		"named/a.snello":       "package named\nfunc main() {}\n",
		"varmain/a.snello":     "package main\nvar main int\n",
		"cmd/tool/main.snello": "package main\nimport (\"fmt\"; \"example.com/app/shapes\")\nfunc main() { c := shapes.Circle{Radius: 1}; r := shapes.Rect{W: 1, H: 2}; fmt.Println(shapes.Largest(ref c, ref r) + extra()) }\n",
		"cmd/tool/more.snello": "package main\nfunc extra() f64 { return 1 }\n",
		"one/main.snello":      "package main\nfunc main() {}\n",
		"mixed/a.snello":       "package main\nfunc main() {}\n",
		"mixed/b.snello":       "package other\n",
	})

	for _, dir := range []string{"cmd/tool", "one"} {
		names, err := SourceFiles(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}

		imp, prog, err := LoadProgram(names, &types.Info{})
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}

		if len(prog.Files) != len(names) || len(prog.Syntax) != len(names) || imp.Module == nil || imp.Module.Path != "example.com/app" {
			t.Errorf("%s: loaded %d files of %d, module %v", dir, len(prog.Files), len(names), imp.Module)
		}
	}

	names, err := SourceFiles(filepath.Join(root, "mixed"))
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := LoadProgram(names, &types.Info{}); err == nil || !strings.Contains(err.Error(), "found packages main (a.snello) and other (b.snello) in mixed") {
		t.Errorf("mixed: got %v", err)
	}

	if _, _, err := LoadProgram([]string{filepath.Join(root, "one", "nosuch.snello")}, &types.Info{}); err == nil {
		t.Error("a file that does not exist loaded")
	}

	if _, _, err := LoadProgram(nil, &types.Info{}); err == nil {
		t.Error("no files loaded")
	}

	for _, c := range []struct{ dir, want string }{
		{"cmd/tool", ""},
		{"named", ""}, // a program under another package clause, as the examples are
		{"shapes", "function main is undeclared in package shapes"},
		{"nomain", "function main is undeclared in the main package"},
		{"varmain", "cannot declare main - must be func"},
	} {
		names, err := SourceFiles(filepath.Join(root, c.dir))
		if err != nil {
			t.Fatal(err)
		}

		_, prog, err := LoadProgram(names, &types.Info{})
		if err != nil {
			t.Fatalf("%s: %v", c.dir, err)
		}

		if got := prog.CheckMain(); (got == nil) != (c.want == "") || got != nil && got.Error() != c.want {
			t.Errorf("%s: CheckMain says %v, want %q", c.dir, got, c.want)
		}
	}
}
