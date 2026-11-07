// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package importer

import (
	"os"
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
		"snello.mod":           "// the root of the module\nlink m\n",
		"shapes/shape.snello":  shapesSrc,
		"shapes/area.snello":   areaSrc,
		"nomain/a.snello":      "package main\nfunc f() {}\n",
		"named/a.snello":       "package named\nfunc main() {}\n",
		"varmain/a.snello":     "package main\nvar main int\n",
		"cmd/tool/main.snello": "package main\nimport (\"fmt\"; \"shapes\")\nfunc main() { c := shapes.Circle{Radius: 1}; r := shapes.Rect{W: 1, H: 2}; fmt.Println(shapes.Largest(ref c, ref r) + extra()) }\n",
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

		if len(prog.Files) != len(names) || len(prog.Syntax) != len(names) || imp.Module == nil || imp.Module.Dir != root {
			t.Errorf("%s: loaded %d files of %d, module %v", dir, len(prog.Files), len(names), imp.Module)
		}

		for _, p := range imp.Packages() {
			if p.Path == "shapes" && (len(p.Links) != 1 || p.Links[0] != "m") {
				t.Errorf("%s: the module's link line did not reach package shapes: %v", dir, p.Links)
			}
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

// TestTestFiles checks the rules of a test file: it is part of its
// package only when the package is under test, and testing and expect
// are imported by test files alone, from a program's file and from a
// package's; the standard library is exempt, since expect imports
// testing.
func TestTestFiles(t *testing.T) {
	root := writeTree(t, map[string]string{
		"snello.mod":                "",
		"tested/tested.snello":      "package tested\n\nfunc N() int { return 1 }\n",
		"tested/tested_test.snello": "package tested\n\nimport \"expect\"\n\nfunc TestN() { expect.Equal(ref N(), ref 1) }\n",
		"harness/harness.snello":    "package harness\n\nimport \"testing\"\n\nfunc F() string? { return testing.Catch(func() {}) }\n",
		"app/main.snello":           "package main\n\nimport (\"fmt\"; \"tested\")\n\nfunc main() { fmt.Println(tested.N()) }\n",
		"app/main_test.snello":      "package main\n\nimport \"expect\"\n\nfunc TestMain() { expect.True(true) }\n",
		"prog/main.snello":          "package main\n\nimport \"expect\"\n\nfunc main() { expect.True(true) }\n",
		"viaharness/main.snello":    "package main\n\nimport (\"fmt\"; \"harness\")\n\nfunc main() { fmt.Println(harness.F().IsNone()) }\n",
		"wrongpkg/w.snello":         "package wrongpkg\n",
		"wrongpkg/w_test.snello":    "package other\n",
	})

	files, err := SourceFiles(filepath.Join(root, "tested"))
	if err != nil || len(files) != 1 || !strings.HasSuffix(files[0], "tested.snello") {
		t.Fatalf("SourceFiles of tested: %v, %v; a test file must not be among them", files, err)
	}

	tests, err := TestFiles(filepath.Join(root, "tested"))
	if err != nil || len(tests) != 1 || !strings.HasSuffix(tests[0], "tested_test.snello") {
		t.Fatalf("TestFiles of tested: %v, %v", tests, err)
	}

	// A program importing tested loads it without its test file, so the
	// test file's import of expect is never seen.
	names, _ := SourceFiles(filepath.Join(root, "app"))
	imp, _, err := LoadProgram(names, &types.Info{})
	if err != nil {
		t.Fatalf("app: %v", err)
	}

	for _, p := range imp.Packages() {
		if IsTestFile(p.File.Name()) {
			t.Errorf("app loaded the test file %s", p.File.Name())
		}
	}

	// The package under test loads its test file, and the file's
	// imports with it.
	info := &types.Info{}
	imp = NewImporter(info)
	imp.Module, _ = FindModule(root)
	imp.Tests = "tested"
	if _, err := imp.ImportFrom("tested", root); err != nil {
		t.Fatalf("tested under test: %v", err)
	}

	loaded := map[string]bool{}
	for _, p := range imp.Packages() {
		loaded[filepath.Base(p.File.Name())] = true
	}

	if !loaded["tested_test.snello"] || !loaded["expect.snello"] {
		t.Errorf("tested under test loaded %v, want its test file and expect", loaded)
	}

	// The rules refused.
	for _, c := range []struct{ path, want string }{
		{"harness", "harness.snello: package testing is imported by test files alone"},
		{"wrongpkg", "found packages wrongpkg (w.snello) and other (w_test.snello)"},
	} {
		imp := NewImporter(&types.Info{})
		imp.Module, _ = FindModule(root)
		imp.Tests = c.path
		if _, err := imp.ImportFrom(c.path, root); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.path, err, c.want)
		}
	}

	names, _ = SourceFiles(filepath.Join(root, "prog"))
	if _, _, err := LoadProgram(names, &types.Info{}); err == nil || !strings.Contains(err.Error(), "main.snello: package expect is imported by test files alone") {
		t.Errorf("prog: got %v", err)
	}

	// A dependency's file is held to the rule as well, so that no
	// program reaches testing through a package it imports.
	names, _ = SourceFiles(filepath.Join(root, "viaharness"))
	if _, _, err := LoadProgram(names, &types.Info{}); err == nil || !strings.Contains(err.Error(), "harness.snello: package testing is imported by test files alone") {
		t.Errorf("viaharness: got %v", err)
	}

	// A main package under test: its own main dropped, the generated one kept.
	names, _ = SourceFiles(filepath.Join(root, "app"))
	tests, _ = TestFiles(filepath.Join(root, "app"))
	runner := filepath.Join(root, "app", "snello_test.snello")
	load := Load{Names: append(append(names, tests...), runner), DropMain: true, Main: runner}
	for _, name := range load.Names[:len(load.Names)-1] {
		src, _ := os.ReadFile(name)
		load.Srcs = append(load.Srcs, src)
	}

	load.Srcs = append(load.Srcs, []byte("package main\n\nfunc main() { TestMain() }\n"))
	_, prog, err := LoadProgramFrom(load, &types.Info{})
	if err != nil {
		t.Fatalf("app under test: %v", err)
	}

	if err := prog.CheckMain(); err != nil {
		t.Errorf("app under test has no main: %v", err)
	}
}
