// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package importer

import (
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/compiler/scanner"
	"github.com/snello-lang/snello/compiler/token"
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
		"snello.mod":           "// the root of the module\nsnello 0.1.0\nlink m\n",
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
		names, err := SourceFiles(filepath.Join(root, dir), platform.Host())
		if err != nil {
			t.Fatal(err)
		}

		imp, prog, err := LoadProgram(names, platform.Host(), &types.Info{})
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

			if p.Std != (p.Path != "shapes") { // fmt and what it imports are the standard library's; shapes is the module's
				t.Errorf("%s: package %s marked as the standard library's: %v", dir, p.Path, p.Std)
			}
		}
	}

	names, err := SourceFiles(filepath.Join(root, "mixed"), platform.Host())
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := LoadProgram(names, platform.Host(), &types.Info{}); err == nil || !strings.Contains(err.Error(), "found packages main (a.snello) and other (b.snello) in mixed") {
		t.Errorf("mixed: got %v", err)
	}

	if _, _, err := LoadProgram([]string{filepath.Join(root, "one", "nosuch.snello")}, platform.Host(), &types.Info{}); err == nil {
		t.Error("a file that does not exist loaded")
	}

	if _, _, err := LoadProgram(nil, platform.Host(), &types.Info{}); err == nil {
		t.Error("no files loaded")
	}

	for _, c := range []struct{ dir, want string }{
		{"cmd/tool", ""},
		{"named", ""}, // a program under another package clause, as the examples are
		{"shapes", "function main is undeclared in package shapes"},
		{"nomain", "function main is undeclared in the main package"},
		{"varmain", "cannot declare main - must be func"},
	} {
		names, err := SourceFiles(filepath.Join(root, c.dir), platform.Host())
		if err != nil {
			t.Fatal(err)
		}

		_, prog, err := LoadProgram(names, platform.Host(), &types.Info{})
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
		"viatests/main.snello":      "package main\n\nimport \"tested_test\"\n\nfunc main() { tested_test.TestN() }\n",
		"wrongpkg/w.snello":         "package wrongpkg\n",
		"wrongpkg/w_test.snello":    "package other\n",
	})

	files, err := SourceFiles(filepath.Join(root, "tested"), platform.Host())
	if err != nil || len(files) != 1 || !strings.HasSuffix(files[0], "tested.snello") {
		t.Fatalf("SourceFiles of tested: %v, %v; a test file must not be among them", files, err)
	}

	tests, err := TestFiles(filepath.Join(root, "tested"), platform.Host())
	if err != nil || len(tests) != 1 || !strings.HasSuffix(tests[0], "tested_test.snello") {
		t.Fatalf("TestFiles of tested: %v, %v", tests, err)
	}

	// A program importing tested loads it without its test file, so the
	// test file's import of expect is never seen.
	names, _ := SourceFiles(filepath.Join(root, "app"), platform.Host())
	imp, _, err := LoadProgram(names, platform.Host(), &types.Info{})
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

	names, _ = SourceFiles(filepath.Join(root, "prog"), platform.Host())
	if _, _, err := LoadProgram(names, platform.Host(), &types.Info{}); err == nil || !strings.Contains(err.Error(), "main.snello: package expect is imported by test files alone") {
		t.Errorf("prog: got %v", err)
	}

	// A dependency's file is held to the rule as well, so that no
	// program reaches testing through a package it imports.
	names, _ = SourceFiles(filepath.Join(root, "viaharness"), platform.Host())
	if _, _, err := LoadProgram(names, platform.Host(), &types.Info{}); err == nil || !strings.Contains(err.Error(), "harness.snello: package testing is imported by test files alone") {
		t.Errorf("viaharness: got %v", err)
	}

	// An external test package is a test file's to import as well: a
	// program importing one would carry the harness.
	names, _ = SourceFiles(filepath.Join(root, "viatests"), platform.Host())
	if _, _, err := LoadProgram(names, platform.Host(), &types.Info{}); err == nil || !strings.Contains(err.Error(), "main.snello: package tested_test is imported by test files alone") {
		t.Errorf("viatests: got %v", err)
	}

	// A main package under test: its own main dropped, the generated one kept.
	names, _ = SourceFiles(filepath.Join(root, "app"), platform.Host())
	tests, _ = TestFiles(filepath.Join(root, "app"), platform.Host())
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

// TestLoadComments holds a Load that asks for comments to the program's
// own files keeping theirs, and one that does not to keeping none.
func TestLoadComments(t *testing.T) {
	src := []byte("// Package main runs.\npackage main\n\nfunc main() {}\n")
	for _, keep := range []bool{true, false} {
		_, prog, err := LoadProgramFrom(Load{Names: []string{"main.snello"}, Srcs: [][]byte{src}, Standalone: true, Comments: keep}, &types.Info{})
		if err != nil {
			t.Fatal(err)
		}

		if got := prog.Syntax[0].Docs != nil; got != keep {
			t.Errorf("Comments %v: comments kept %v", keep, got)
		}
	}
}

// TestLoadStandalone loads a program in no module, as a standard
// package's tests are, though its files are named under a directory
// whose module file does not read: Standalone leaves the module unread,
// and without it the file is what the load reports.
func TestLoadStandalone(t *testing.T) {
	root := writeTree(t, map[string]string{"snello.mod": "exclude x\n"})
	name := filepath.Join(root, "main.snello")
	src := []byte("package main\n\nimport \"strings\"\n\nfunc main() { _ = strings.ToUpper(\"x\") }\n")
	imp, _, err := LoadProgramFrom(Load{Names: []string{name}, Srcs: [][]byte{src}, Standalone: true}, &types.Info{})
	if err != nil {
		t.Fatalf("a standalone load: %v", err)
	}

	if imp.Module != nil {
		t.Errorf("a standalone load read the module %+v", imp.Module)
	}

	if _, _, err := LoadProgramFrom(Load{Names: []string{name}, Srcs: [][]byte{src}}, &types.Info{}); err == nil || !strings.Contains(err.Error(), `unknown directive "exclude"`) {
		t.Errorf("a load in the module: got %v, want its file's error", err)
	}
}

// TestPlatformFiles chooses a package's files by their names for the
// platform the program is built for: the program's own directory, a
// package of the module with its test file and its C side, a standard
// package read from a directory, with its own tests and its external
// test package when it is under test; a file for a platform Snello does
// not build for, and one whose name begins with a dot or an underscore,
// is left out; and a directory whose every file is for another
// platform says so.
func TestPlatformFiles(t *testing.T) {
	root := writeTree(t, map[string]string{
		"snello.mod":                      "snello 0.1.0\n",
		"app/main.snello":                 "package main\nimport (\"poll\"; \"plat\"; \"windows\"; \"cwin\")\nfunc main() { _ = poll.Name() + width() + plat.Name() + windows.Name() + cwin.Name() }\n",
		"app/width_arm64.snello":          "package main\nfunc width() string { return \"arm64\" }\n",
		"app/width_amd64.snello":          "package main\nfunc width() string { return \"amd64\" }\n",
		"app/.#main.snello":               "an editor's lock, no source\n",
		"app/_draft.snello":               "not a package's file\n",
		"poll/poll.snello":                "package poll\nfunc Name() string { return name() }\n",
		"poll/poll_linux.snello":          "package poll\nfunc name() string { return \"linux\" }\n",
		"poll/poll_darwin.snello":         "package poll\nfunc name() string { return \"darwin\" }\n",
		"poll/poll_linux_test.snello":     "package poll\nfunc TestLinux() {}\n",
		"poll/shim.h":                     "",
		"poll/shim_linux.c":               "",
		"poll/shim_darwin_arm64.c":        "",
		"windows/windows.snello":          "package windows\nfunc Name() string { return \"\" }\n",
		"windows/os_windows.snello":       "this file is never compiled\n",
		"cwin/cwin.snello":                "package cwin\nfunc Name() string { return \"\" }\n",
		"cwin/shim_windows.c":             "",
		"cwin/cpu_ppc64le.c":              "",
		"onlylinux/a_linux.snello":        "package onlylinux\n",
		"std/plat/plat.snello":            "package plat\nfunc Name() string { return name() }\n",
		"std/plat/plat_linux.snello":      "package plat\nfunc name() string { return \"linux\" }\n",
		"std/plat/plat_darwin.snello":     "package plat\nfunc name() string { return \"darwin\" }\n",
		"std/plat/plat_linux_test.snello": "package plat\nfunc TestLinux() { _ = name() }\n",
		"std/plat/ext_darwin_test.snello": "package plat_test\nimport \"plat\"\nfunc TestDarwin() { _ = plat.Name() }\n",
	})

	for _, c := range []struct {
		target          platform.Target
		app, poll, plat []string
		cfiles          []string
		pollTests       int
		platTests       []string // the plat package's own files under test, then its external test package's
	}{
		{platform.Target{OS: platform.Linux, Arch: platform.ARM64}, []string{"main.snello", "width_arm64.snello"}, []string{"poll.snello", "poll_linux.snello"}, []string{"plat.snello", "plat_linux.snello"}, []string{"shim.h", "shim_linux.c"}, 1, []string{"plat.snello", "plat_linux.snello", "plat_linux_test.snello"}},
		{platform.Target{OS: platform.Darwin, Arch: platform.ARM64}, []string{"main.snello", "width_arm64.snello"}, []string{"poll.snello", "poll_darwin.snello"}, []string{"plat.snello", "plat_darwin.snello"}, []string{"shim.h", "shim_darwin_arm64.c"}, 0, []string{"plat.snello", "plat_darwin.snello", "ext_darwin_test.snello"}},
		{platform.Target{OS: platform.Darwin, Arch: platform.AMD64}, []string{"main.snello", "width_amd64.snello"}, []string{"poll.snello", "poll_darwin.snello"}, []string{"plat.snello", "plat_darwin.snello"}, []string{"shim.h"}, 0, []string{"plat.snello", "plat_darwin.snello", "ext_darwin_test.snello"}},
	} {
		names, err := SourceFiles(filepath.Join(root, "app"), c.target)
		if err != nil {
			t.Fatal(err)
		}

		if got := bases(names); !slices.Equal(got, c.app) {
			t.Errorf("%s: the program's files are %v, want %v", c.target, got, c.app)
		}

		files := loadPlatform(t, Load{Names: names, Target: c.target, StdDir: filepath.Join(root, "std")})
		if !slices.Equal(files["poll"], c.poll) || !slices.Equal(files["plat"], c.plat) || !slices.Equal(files["windows"], []string{"windows.snello"}) {
			t.Errorf("%s: loaded poll %v, plat %v and windows %v, want %v, %v and windows.snello", c.target, files["poll"], files["plat"], files["windows"], c.poll, c.plat)
		}

		if got := files["poll C"]; !slices.Equal(got, c.cfiles) {
			t.Errorf("%s: the C side of poll is %v, want %v", c.target, got, c.cfiles)
		}

		if got := files["cwin C"]; len(got) != 0 {
			t.Errorf("%s: the C side of cwin is %v, want none of another platform's", c.target, got)
		}

		if tests, err := TestFiles(filepath.Join(root, "poll"), c.target); err != nil || len(tests) != c.pollTests {
			t.Errorf("%s: the test files of poll are %v, %v, want %d", c.target, bases(tests), err, c.pollTests)
		}

		runner, under := "import \"plat\"\nfunc main() { _ = plat.Name() }\n", "plat" // a generated runner, under test files' rules, and the package it tests
		if c.target.OS == platform.Darwin {
			runner, under = "import \"plat_test\"\nfunc main() { plat_test.TestDarwin() }\n", "plat_test"
		}

		tested := loadPlatform(t, Load{Names: []string{filepath.Join(root, "runner_test.snello")}, Srcs: [][]byte{[]byte("package main\n" + runner)}, Tests: under, Target: c.target, StdDir: filepath.Join(root, "std"), Standalone: true})
		if got := append(tested["plat"], tested["plat_test"]...); !slices.Equal(got, c.platTests) {
			t.Errorf("%s: plat under test loaded %v, want %v", c.target, got, c.platTests)
		}
	}

	host := platform.Target{OS: platform.Darwin, Arch: platform.ARM64}
	if _, err := SourceFiles(filepath.Join(root, "onlylinux"), host); err == nil || err.Error() != "every .snello file in "+filepath.Join(root, "onlylinux")+" is for another platform than darwin/arm64" {
		t.Errorf("onlylinux: got %v", err)
	}

	imp := NewImporter(&types.Info{})
	imp.Target = host
	if imp.Module, _ = FindModule(root); imp.Module == nil {
		t.Fatal("no module found")
	}

	if _, err := imp.Import("onlylinux"); err == nil || !strings.Contains(err.Error(), "is for another platform than darwin/arm64") {
		t.Errorf("import onlylinux: got %v", err)
	}
}

// loadPlatform loads the program l names and returns the base names of
// each package's files by its path, and of its C side by the path and
// " C".
func loadPlatform(t *testing.T, l Load) map[string][]string {
	t.Helper()
	for _, name := range l.Names[len(l.Srcs):] {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}

		l.Srcs = append(l.Srcs, src)
	}

	imp, _, err := LoadProgramFrom(l, &types.Info{})
	if err != nil {
		t.Fatalf("%s: %v", l.Target, err)
	}

	files := map[string][]string{}
	for _, p := range imp.Packages() {
		files[p.Path] = append(files[p.Path], filepath.Base(p.File.Name()))
		files[p.Path+" C"] = bases(p.CFiles)
	}

	return files
}

// TestChosen holds a directory's file to the platform and to the names
// no package holds: an editor's lock or a tool's file, beginning with a
// dot or an underscore.
func TestChosen(t *testing.T) {
	linux := platform.Target{OS: platform.Linux, Arch: platform.AMD64}
	for name, want := range map[string]bool{
		"poll.snello":         true,
		"poll_linux.snello":   true,
		"poll_darwin.snello":  false,
		"poll_windows.snello": false,
		".#poll.snello":       false,
		"_poll.snello":        false,
		"poll_test.snello":    true,
	} {
		if got := Chosen(name, linux); got != want {
			t.Errorf("Chosen(%q) = %v, want %v", name, got, want)
		}
	}
}

// bases returns the base names of the paths given.
func bases(paths []string) []string {
	var names []string
	for _, p := range paths {
		names = append(names, filepath.Base(p))
	}

	return names
}

// TestRead loads a program whose sources come from a function, as the
// language server gives the editor's buffers: the program's own file and
// a file of a package it imports are read through it in place of the
// disk, whose copies compile while the buffers do not.
func TestRead(t *testing.T) {
	root := writeTree(t, map[string]string{
		ModFile:          "snello 0.1.0\n",
		"main.snello":    "package main\n\nimport \"lib\"\n\nfunc main() {\n\tlib.F()\n}\n",
		"lib/lib.snello": "package lib\n\n// F does nothing.\nfunc F() {}\n",
	})
	mainPath, libPath := filepath.Join(root, "main.snello"), filepath.Join(root, "lib", "lib.snello")
	buffers := map[string]string{libPath: "package lib\n\n// F does nothing.\nfunc F() int {}\n"}
	read := func(name string) ([]byte, error) {
		if src, ok := buffers[name]; ok {
			return []byte(src), nil
		}

		return os.ReadFile(name)
	}

	if _, _, err := LoadProgram([]string{mainPath}, platform.Host(), &types.Info{}); err != nil {
		t.Fatalf("the program on the disk: %v", err)
	}

	if _, _, err := LoadProgramFrom(Load{Names: []string{mainPath}, Read: read}, &types.Info{}); err == nil || !strings.Contains(err.Error(), "package lib does not compile") {
		t.Errorf("the library's buffer, through an import: %v", err)
	}

	buffers[mainPath] = "package main\n\nfunc main() {\n\tundefined()\n}\n"
	delete(buffers, libPath)
	if _, _, err := LoadProgramFrom(Load{Names: []string{mainPath}, Read: read}, &types.Info{}); err == nil || !strings.Contains(err.Error(), "undefined") {
		t.Errorf("the program's own buffer: %v", err)
	}
}

// TestParsed gives Parsed every file a load parses, the program's, a
// package of its module's and the standard library's by its path under
// std/, a file with a syntax error too, and gives no two files of one
// load a position in common, so that a position names its file.
func TestParsed(t *testing.T) {
	root := writeTree(t, map[string]string{
		ModFile:          "snello 0.1.0\n",
		"main.snello":    "package main\n\nimport (\"lib\"; \"strings\")\n\nfunc main() {\n\tlib.F(strings.ToUpper(\"a\"))\n}\n",
		"lib/lib.snello": "package lib\n\nfunc F(s string) {}\n",
	})
	mainPath, libPath := filepath.Join(root, "main.snello"), filepath.Join(root, "lib", "lib.snello")
	var files []*token.File
	parsed := func(file *token.File, syntax *ast.File) {
		if syntax == nil {
			t.Errorf("%s parsed to no tree", file.Name())
		}

		files = append(files, file)
	}

	if _, _, err := LoadProgramFrom(Load{Names: []string{mainPath}, Parsed: parsed}, &types.Info{}); err != nil {
		t.Fatal(err)
	}

	var names []string
	for i, f := range files {
		names = append(names, f.Name())
		for _, g := range files[:i] {
			if f.Holds(g.Pos(0)) || g.Holds(f.Pos(0)) {
				t.Errorf("%s and %s share positions", f.Name(), g.Name())
			}
		}
	}

	for _, want := range []string{mainPath, libPath, "strings/strings.snello"} {
		if !slices.Contains(names, want) {
			t.Errorf("parsed %q, want %s among them", names, want)
		}
	}

	files = nil
	broken := writeTree(t, map[string]string{"main.snello": "package main\n\nfunc main( {\n"})
	if _, _, err := LoadProgramFrom(Load{Names: []string{filepath.Join(broken, "main.snello")}, Parsed: parsed}, &types.Info{}); err == nil || len(files) != 1 {
		t.Errorf("a file with a syntax error: %v, parsed %d files, want an error and the file", err, len(files))
	}
}

// TestFailedOnce loads a package that does not compile once, however
// many files import it: in a chain of packages of three files each, all
// importing the one below, whose bottom does not compile, every file is
// parsed once, and the error reaches the program.
func TestFailedOnce(t *testing.T) {
	const depth, width = 6, 3
	files := map[string]string{ModFile: "snello 0.1.0\n", "main.snello": "package main\n\nimport \"p5\"\n\nfunc main() { p5.F0() }\n"}
	for p := range depth {
		for f := range width {
			src := "package p" + strconv.Itoa(p) + "\n\n"
			body := "{}"
			if p > 0 {
				src += "import \"p" + strconv.Itoa(p-1) + "\"\n\n"
				body = "{ p" + strconv.Itoa(p-1) + ".F0() }"
			} else if f == 0 {
				body = "{ return 1 }" // the error at the bottom
			}

			files["p"+strconv.Itoa(p)+"/f"+strconv.Itoa(f)+".snello"] = src + "func F" + strconv.Itoa(f) + "() " + body + "\n"
		}
	}

	parsed := map[string]int{}
	root := writeTree(t, files)
	count := func(file *token.File, _ *ast.File) {
		if strings.HasPrefix(file.Name(), root) { // the program's own files; the library's, fmt loaded into every program among them, are not the chain's
			parsed[file.Name()]++
		}
	}

	_, _, err := LoadProgramFrom(Load{Names: []string{filepath.Join(root, "main.snello")}, Parsed: count}, &types.Info{})
	if err == nil || !strings.Contains(err.Error(), "package p0 does not compile") {
		t.Errorf("the chain: %v, want p0's error", err)
	}

	if len(parsed) != depth*width+1 {
		t.Errorf("parsed %d files, want every one of the %d", len(parsed), depth*width+1)
	}

	for name, n := range parsed {
		if n != 1 {
			t.Errorf("%s parsed %d times, want once", name, n)
		}
	}
}

// TestAt records the names visible at the place At names, a name of
// the program's own file and one of a package of the module it imports,
// which is parsed and checked after the program's file; the name there
// resolves, and the record is made as it does.
func TestAt(t *testing.T) {
	root := writeTree(t, map[string]string{
		ModFile:          "snello 0.1.0\n",
		"main.snello":    "package main\n\nimport \"lib\"\n\nfunc main() {\n\tmine := 1\n\t_ = mine + lib.F()\n}\n",
		"lib/lib.snello": "package lib\n\nfunc F() int {\n\tlibs := 2\n\treturn libs\n}\n",
	})

	for file, at := range map[string]string{"main.snello": "mine + ", "lib/lib.snello": "libs\n}"} {
		path := filepath.Join(root, filepath.FromSlash(file))
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		info := &types.Info{}
		if _, _, err := LoadProgramFrom(Load{Names: []string{filepath.Join(root, "main.snello")}, At: Place{File: path, Offset: strings.Index(string(src), at)}}, info); err != nil {
			t.Fatal(err)
		}

		want := strings.Fields(at)[0]
		if !slices.ContainsFunc(info.Visible, func(o types.Object) bool { return o.Name() == want }) {
			t.Errorf("at %s in %s: %d names visible, want %s among them", want, file, len(info.Visible), want)
		}
	}
}

// TestRecovered checks a file that does not parse, a line typed half
// way, as the parser recovered it when a load asks, recording the names
// visible at the place and reporting the parse's error; a load that
// does not ask stops at the parse, and records nothing.
func TestRecovered(t *testing.T) {
	const src = "package main\n\nfunc main() {\n\tmine := 1\n\tif min\n}\n"
	root := writeTree(t, map[string]string{"main.snello": src})
	path := filepath.Join(root, "main.snello")
	for _, recovered := range []bool{false, true} {
		info := &types.Info{}
		_, _, err := LoadProgramFrom(Load{Names: []string{path}, At: Place{File: path, Offset: strings.Index(src, "min\n")}, Recovered: recovered}, info)
		if err == nil || !strings.Contains(err.Error(), "expected") {
			t.Errorf("recovered %v: %v, want the parse's error", recovered, err)
		}

		if got := slices.ContainsFunc(info.Visible, func(o types.Object) bool { return o.Name() == "mine" }); got != recovered {
			t.Errorf("recovered %v: mine visible %v, want %v", recovered, got, recovered)
		}
	}
}

// TestRecoveredNoPanic checks, as a load that asks checks a file that
// does not parse, every example's file and every feature program the
// backends are tested on, each cut in the two ways a file being
// typed is: ended part way, and with a stretch taken out, at places
// spread over the file, a completion's place at the cut; the check
// reports the parse's errors and never panics, whatever the parser left.
func TestRecoveredNoPanic(t *testing.T) {
	const cuts, stretch = 16, 5
	examples, err := filepath.Glob(filepath.Join("..", "..", "examples", "*", "*.snello"))
	if err != nil || len(examples) == 0 {
		t.Fatalf("no examples: %v", err)
	}

	features, err := filepath.Glob(filepath.Join("..", "emit", "internal", "backendtest", "testdata", "*.snello"))
	if err != nil || len(features) == 0 {
		t.Fatalf("no feature programs: %v", err)
	}

	paths := append(examples, features...)

	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		for i := 1; i <= cuts; i++ {
			at := len(src) * i / (cuts + 1)
			for form, text := range map[string][]byte{
				"ended":   src[:at],
				"cut out": slices.Concat(src[:at], src[min(at+stretch, len(src)):]),
			} {
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Errorf("%s %s at %d: the check panicked: %v\n%s", filepath.Base(path), form, at, r, debug.Stack())
						}
					}()

					LoadProgramFrom(Load{Names: []string{path}, Srcs: [][]byte{text}, Standalone: true, Recovered: true, At: Place{File: path, Offset: at}}, &types.Info{})
				}()
			}
		}
	}
}

// TestUnexportedMembers checks that a trait's unexported members are
// its package's alone. A program may not name them, by any road: a
// field or default its own type gets from the trait or one it refines,
// a value of the trait, a type parameter it bounds, a generic type's
// instance, or super. It may not stand in for them: its type may not
// override an unexported default or answer an unexported requirement,
// a generic trait's and a function of the type among them, nor may a
// trait of its own or a default of another package; an unexported
// default may not answer the program's requirement either, nor a trait
// of the program redeclare an unexported field, and two defaults where
// one is unexported are refused, with no way out to offer. Each
// mistake gives one message: a trait refused for an unexported
// requirement is not asked for its exported ones, and is refused once
// however many listed traits reach it; a requirement no answer may
// meet is the one mistake, whatever the type declares or inherits for
// it; and a mistake told where it was made leaves the trait's other
// requirements asked for. The program may inherit the default, take
// the trait's package's own default as the answer, and use, override
// and answer the exported members.
func TestUnexportedMembers(t *testing.T) {
	const lib = "package lib\n\ntrait Tagged {\n\tvar tag int\n\tfunc (t ref Self) hidden() int { return 1 }\n\tfunc (t ref Self) Shown() int { return 2 }\n}\n\ntrait Labeled: Tagged {}\n\nstruct Box<T> { v T }\n\nfunc Box<T>.Of(v T) Box<T> { return Box<T>{v: v} }\n\ntrait Sealed {\n\tfunc (s ref Self) secret() int { return 1 }\n\tfunc (s ref Self) Peek() int { return s.secret() }\n}\n\ntrait Want {\n\tfunc (s ref Self) want() int\n}\n\nstruct Inner impl Sealed, Want { n int }\n\nfunc (i ref Inner) secret() int { return 2 }\n\nfunc (i ref Inner) want() int { return i.n }\n\ntrait A {\n\tfunc (s ref Self) f() int\n\tfunc (s ref Self) Get() int { return s.f() }\n}\n\ntrait B: A {\n\tfunc (s ref Self) f() int { return 7 }\n}\n\ntrait Other {\n\tfunc (s ref Self) secret() int { return 3 }\n}\n\ntrait It<T> {\n\tfunc (s ref Self) step() T\n}\n\ntrait Maker {\n\tfunc make() Self\n}\n\ntrait Three {\n\tfunc (s ref Self) a() int\n\tfunc (s ref Self) b() int\n\tfunc (s ref Self) c() int\n}\n\ntrait Sized {\n\tfunc (s ref Self) Size() int\n}\n\ntrait Mixed {\n\tfunc (s ref Self) Size() int\n\tfunc (s ref Self) want() int\n}\n\ntrait Named {\n\tfunc (s ref Self) Name() string\n}\n\ntrait Sealed2: Named {\n\tfunc (s ref Self) want() int\n}\n\ntrait GiveW {\n\tfunc (s ref Self) want() int { return 7 }\n}\n\ntrait TaggedTwice: Tagged {\n\tvar tag int\n}\n"
	const lib2 = "package lib2\n\ntrait Give {\n\tfunc (s ref Self) want() int { return 7 }\n}\n\ntrait Hide {\n\tfunc (s ref Self) secret() int { return 4 }\n}\n\ntrait Need2 {\n\tfunc (s ref Self) want() int\n\tfunc (s ref Self) Use2() int { return s.want() }\n}\n"
	tests := []struct {
		name, body, want string // want holds the messages expected, one a line, in the order of their positions
	}{
		{"a field the trait gives", "struct Mine impl lib.Tagged { n int }\n\nfunc main() { m := Mine{n: 2}; _ = m.tag }\n", "tag of Mine is not exported"},
		{"a field the trait gives, in a literal", "struct Mine impl lib.Tagged { n int }\n\nfunc main() { m := Mine{tag: 1, n: 2}; _ = m.n }\n", "tag of Mine is not exported"},
		{"an inherited default", "struct Mine impl lib.Tagged { n int }\n\nfunc main() { m := Mine{n: 2}; _ = m.hidden() }\n", "hidden of Mine is not exported"},
		{"through a trait value", "func use(x ref lib.Tagged) int { return x.hidden() }\n\nfunc main() {}\n", "hidden of Tagged is not exported"},
		{"a field through a trait value", "func use(x ref lib.Tagged) int { return x.tag }\n\nfunc main() {}\n", "tag of Tagged is not exported"},
		{"through a bound", "func use<T: lib.Tagged>(x ref T) int { return x.hidden() }\n\nfunc main() {}\n", "hidden of T is not exported"},
		{"a refined trait's default", "struct Mine impl lib.Labeled { n int }\n\nfunc main() { m := Mine{n: 2}; _ = m.hidden() }\n", "hidden of Mine is not exported"},
		{"a generic instance's field", "func main() { b := lib.Box<int>.Of(1); _ = b.v }\n", "v of lib.Box<int> is not exported"},
		{"an override of an unexported default", "struct Mine impl lib.Sealed { n int }\n\nfunc (m ref Mine) secret() int { return 99 }\n\nfunc main() { m := Mine{n: 1}; _ = m.Peek() }\n", "Mine.secret would override Sealed.secret, which is not exported, so only lib's types override it"},
		{"an unexported method required", "struct Mine impl lib.Want { n int }\n\nfunc (m ref Mine) want() int { return m.n }\n\nfunc main() {}\n", "Mine cannot implement Want: Want requires want, which is not exported, so only lib's traits and types answer it"},
		{"an unexported requirement beside an exported one", "struct Mine impl lib.Mixed { n int }\n\nfunc main() {}\n", "Mine cannot implement Mixed: Mixed requires want, which is not exported"},
		{"an unexported requirement over a refined trait's exported one", "struct Mine impl lib.Sealed2 { n int }\n\nfunc main() {}\n", "Mine cannot implement Sealed2: Sealed2 requires want, which is not exported"},
		{"a type of the program taking a trait's stand-in", "trait W2: lib.Want {\n\tfunc (s ref Self) want() int { return 5 }\n}\n\nstruct Mine impl W2 { n int }\n\nfunc main() {}\n", "W2.want would stand for Want.want, which is not exported"},
		{"a trait standing in for an unexported field", "trait P: lib.Tagged {\n\tvar tag int\n}\n\nstruct Mine impl P { n int }\n\nfunc main() {}\n", "P.tag would stand for Tagged.tag, which is not exported, so only lib's traits and types declare it"},
		{"a field named twice within the library", "struct Mine impl lib.TaggedTwice { n int }\n\nfunc main() {}\n", "Mine gets a field tag from both TaggedTwice and Tagged"},
		{"a sealed trait reached through two listed traits", "trait W3: lib.Want {}\n\nstruct Mine impl lib.Want, W3 { n int }\n\nfunc (m ref Mine) want() int { return m.n }\n\nfunc main() {}\n", "Mine cannot implement Want: Want requires want, which is not exported"},
		{"the package's default in an ambiguity", "trait Give {\n\tfunc (s ref Self) want() int { return 5 }\n}\n\nstruct Mine impl lib.Want, lib.GiveW, Give { n int }\n\nfunc main() {}\n", "Mine cannot implement both Give and GiveW: each gives it a default want, and lib's is not exported, so Mine cannot declare its own to choose"},
		{"a refused override beside a missing method", "struct Mine impl lib.Mixed, lib.GiveW { n int }\n\nfunc (m ref Mine) want() int { return 1 }\n\nfunc main() {}\n", "Mine does not implement Mixed: missing method Size() int\nMine.want would override GiveW.want, which is not exported"},
		{"two defaults of the program's under a sealed requirement", "trait X {\n\tfunc (s ref Self) want() int { return 1 }\n}\n\ntrait Y {\n\tfunc (s ref Self) want() int { return 2 }\n}\n\nstruct Mine impl lib.Want, X, Y { n int }\n\nfunc main() {}\n", "Mine cannot implement Want: Want requires want, which is not exported, so only lib's traits and types answer it"},
		{"a method of the type's beside a default of the program's, under a sealed requirement", "trait X {\n\tfunc (s ref Self) want() int { return 1 }\n}\n\nstruct Mine impl lib.Want, X { n int }\n\nfunc (m ref Mine) want() string { return \"\" }\n\nfunc main() {}\n", "Mine cannot implement Want: Want requires want, which is not exported"},
		{"a field of the type's own beside a stand-in's", "trait P: lib.Tagged {\n\tvar tag int\n}\n\nstruct Mine impl P { tag int }\n\nfunc main() {}\n", "P.tag would stand for Tagged.tag, which is not exported\nfield tag is also a field of P"},
		{"a default answering the program's requirement under a sealed one", "trait X {\n\tfunc (s ref Self) want() int { return 1 }\n}\n\ntrait Need {\n\tfunc (s ref Self) want() int\n}\n\nstruct Mine impl lib.Want, X, Need { n int }\n\nfunc main() {}\n", "Mine cannot implement Want: Want requires want, which is not exported"},
		{"a third package's own default answering its requirement under a sealed one", "import \"lib2\"\n\nstruct Mine impl lib.Want, lib2.Need2, lib2.Give { n int }\n\nfunc main() {}\n", "Mine cannot implement Want: Want requires want, which is not exported"},
		{"the package's default answering it while another package's requirement is sealed", "import \"lib2\"\n\nstruct Mine impl lib.Want, lib.GiveW, lib2.Need2 { n int }\n\nfunc main() {}\n", "Mine cannot implement Need2: Need2 requires want, which is not exported, so only lib2's traits and types answer it"},
		{"a requirement of three unexported methods", "struct Mine impl lib.Three { n int }\n\nfunc main() {}\n", "Mine cannot implement Three: Three requires a, which is not exported"},
		{"another package's default answering", "trait Give {\n\tfunc (s ref Self) want() int { return 42 }\n}\n\nstruct Mine impl lib.Want, Give { n int }\n\nfunc main() {}\n", "Mine cannot implement Want: Want requires want, which is not exported, so only lib's traits and types answer it"},
		{"a third package's default answering", "import \"lib2\"\n\nstruct Mine impl lib.Want, lib2.Give { n int }\n\nfunc main() {}\n", "Mine cannot implement Want: Want requires want, which is not exported"},
		{"another package's default, through a trait refining both", "trait Give {\n\tfunc (s ref Self) want() int { return 5 }\n}\n\ntrait Both: lib.Want, Give {}\n\nstruct Mine impl Both { n int }\n\nfunc main() {}\n", "Mine cannot implement Both: Want requires want, which is not exported"},
		{"another package's default answering a generic trait", "trait Give {\n\tfunc (s ref Self) step() int { return 11 }\n}\n\nstruct Mine impl lib.It<int>, Give { n int }\n\nfunc main() {}\n", "Mine cannot implement It<int>: It<int> requires step, which is not exported"},
		{"an unexported default answering the program's requirement", "trait Need {\n\tfunc (s ref Self) secret() int\n}\n\nstruct Mine impl lib.Sealed, Need { n int }\n\nfunc main() {}\n", "Mine cannot implement Need: Need requires secret, and the one Mine inherits is Sealed's, which is not exported, so it answers only lib's traits"},
		{"super from the type's own method", "struct Mine impl lib.Sealed { n int }\n\nfunc (m ref Mine) Call() int { return super.secret() }\n\nfunc main() {}\n", "secret of Sealed is not exported"},
		{"super from a trait of the program", "trait T: lib.Sealed {\n\tfunc (s ref Self) Call() int { return super.secret() }\n}\n\nfunc main() {}\n", "secret of Sealed is not exported"},
		{"an override of the package's default answering its requirement", "struct Mine impl lib.B { n int }\n\nfunc (m ref Mine) f() int { return 1 }\n\nfunc main() {}\n", "Mine.f would override B.f, which is not exported, so only lib's types override it"},
		{"a trait standing in for an unexported default", "trait Mine2: lib.Sealed {\n\tfunc (s ref Self) secret() int { return 99 }\n}\n\nfunc main() {}\n", "Mine2.secret would stand for Sealed.secret, which is not exported, so only lib's traits and types declare it"},
		{"a trait standing in for an unexported required method", "trait W2: lib.Want {\n\tfunc (s ref Self) want() int { return 5 }\n}\n\nfunc main() {}\n", "W2.want would stand for Want.want, which is not exported, so only lib's traits and types declare it"},
		{"the package's own default answering", "struct Mine impl lib.B { n int }\n\nfunc main() { m := Mine{n: 1}; _ = m.Get() }\n", ""},
		{"the package's own default, through a trait of the program", "trait C: lib.B {}\n\nstruct Mine impl C { n int }\n\nfunc main() { m := Mine{n: 1}; _ = m.Get() }\n", ""},
		{"two defaults, one sealed", "struct Mine impl lib.Sealed, lib.Other { n int }\n\nfunc main() {}\n", "Mine cannot implement both Other and Sealed: each gives it a default secret, and lib's are not exported, so Mine cannot declare its own to choose"},
		{"two defaults, sealed by two packages", "import \"lib2\"\n\nstruct Mine impl lib.Sealed, lib2.Hide { n int }\n\nfunc main() {}\n", "Mine cannot implement both Hide and Sealed: each gives it a default secret, and lib2's and lib's are not exported"},
		{"a generic trait's instance", "struct Mine impl lib.It<int> { n int }\n\nfunc (m ref Mine) step() int { return m.n }\n\nfunc main() {}\n", "Mine cannot implement It<int>: It<int> requires step, which is not exported"},
		{"an unexported function of the type", "struct Mine impl lib.Maker { n int }\n\nfunc Mine.make() Mine { return Mine{n: 0} }\n\nfunc main() {}\n", "Mine cannot implement Maker: Maker requires make, which is not exported"},
		{"an unexported default inherited", "struct Mine impl lib.Sealed { n int }\n\nfunc main() { m := Mine{n: 1}; _ = m.Peek() }\n", ""},
		{"the exported ones", "struct Mine impl lib.Tagged { n int }\n\nfunc use<T: lib.Tagged>(x ref T) int { return x.Shown() }\n\nfunc main() { m := Mine{n: 2}; _ = m.Shown() + use(ref m) }\n", ""},
		{"the exported ones overridden and answered", "struct Mine impl lib.Tagged, lib.Sized { n int }\n\nfunc (m ref Mine) Shown() int { return 5 }\n\nfunc (m ref Mine) Size() int { return m.n }\n\nfunc main() {}\n", ""},
	}

	for _, tt := range tests {
		root := writeTree(t, map[string]string{ModFile: "snello 0.1.0\n", "lib/lib.snello": lib, "lib2/lib2.snello": lib2, "main.snello": "package main\n\nimport \"lib\"\n\n" + tt.body})
		_, _, err := LoadProgramFrom(Load{Names: []string{filepath.Join(root, "main.snello")}}, &types.Info{})
		if tt.want == "" {
			if err != nil {
				t.Errorf("%s: %v, want none", tt.name, err)
			}

			continue
		}

		var list scanner.ErrorList
		wants := strings.Split(tt.want, "\n")
		if !errors.As(err, &list) || len(list) != len(wants) { // one mistake, one message
			t.Errorf("%s: %v, want only %q", tt.name, err, wants)
			continue
		}

		for i, want := range wants {
			if !strings.Contains(list[i].Msg, want) {
				t.Errorf("%s: %s, want %q", tt.name, list[i].Msg, want)
			}
		}
	}
}

// TestCheckDir checks a directory as snello check DIR does: a main
// package as a program, whose errors are the checker's own list; a
// library under its module's rules, as an import, whose errors come as
// its package's that does not compile, each with its position; a
// library's buffer read through the options; and a directory of no
// source file refused.
func TestCheckDir(t *testing.T) {
	root := writeTree(t, map[string]string{
		ModFile:            "snello 0.1.0\n",
		"main.snello":      "package main\n\nimport \"lib\"\n\nfunc main() {\n\tlib.F()\n}\n",
		"lib/lib.snello":   "package lib\n\n// F does nothing.\nfunc F() {}\n",
		"bad/bad.snello":   "package main\n\nfunc main() {\n\tmissing()\n}\n",
		"empty/README.txt": "no package here\n",
	})
	libDir := filepath.Join(root, "lib")
	for _, dir := range []string{root, libDir} {
		if err := CheckDir(dir, &types.Info{}, Options{}); err != nil {
			t.Errorf("%s: %v", dir, err)
		}
	}

	var list scanner.ErrorList
	err := CheckDir(filepath.Join(root, "bad"), &types.Info{}, Options{})
	if !errors.As(err, &list) || strings.HasPrefix(err.Error(), "package ") {
		t.Errorf("a main package's error: %v, want the checker's list itself", err)
	}

	libPath := filepath.Join(libDir, "lib.snello")
	read := func(name string) ([]byte, error) {
		if name == libPath {
			return []byte("package lib\n\n// F does nothing.\nfunc F() int {}\n"), nil
		}

		return os.ReadFile(name)
	}

	err = CheckDir(libDir, &types.Info{}, Options{Read: read, Cached: true})
	if !errors.As(err, &list) || len(list) != 1 || list[0].Pos.Filename != libPath || list[0].Pos.Line != 4 || !strings.HasPrefix(err.Error(), "package lib does not compile:\n") {
		t.Errorf("a library's buffer: %v, want its package's error with the line in its file", err)
	}

	if err := CheckDir(filepath.Join(root, "empty"), &types.Info{}, Options{}); err == nil || !strings.Contains(err.Error(), "no .snello files") {
		t.Errorf("a directory of no source file: %v", err)
	}
}

// TestCheckTests checks a library's tests by the import path they are
// reached at: its own, whose test files join it, and its external test
// package's; an error keeps its test file and its line; a package under
// no module is refused, since its tests could not name it.
func TestCheckTests(t *testing.T) {
	root := writeTree(t, map[string]string{
		ModFile:               "snello 0.1.0\n",
		"lib/lib.snello":      "package lib\n\n// F is one.\nfunc F() int {\n\treturn 1\n}\n",
		"lib/lib_test.snello": "package lib\n\nimport \"expect\"\n\nfunc TestF() {\n\texpect.Equal(ref F(), ref 1)\n}\n",
		"ext/ext.snello":      "package ext\n\n// G is two.\nfunc G() int {\n\treturn 2\n}\n",
		"ext/ext_test.snello": "package ext_test\n\nimport (\"expect\"; \"ext\")\n\nfunc TestG() {\n\texpect.Equal(ref ext.G(), ref missing)\n}\n",
	})

	if err := CheckTests(filepath.Join(root, "lib"), "lib", &types.Info{}, Options{}); err != nil {
		t.Errorf("a library's own tests: %v", err)
	}

	err := CheckTests(filepath.Join(root, "ext"), "ext"+TestSuffix, &types.Info{}, Options{Cached: true})
	var list scanner.ErrorList
	if !errors.As(err, &list) || len(list) != 1 || filepath.Base(list[0].Pos.Filename) != "ext_test.snello" || list[0].Pos.Line != 6 || list[0].Msg != "undefined: missing" {
		t.Errorf("an external test package's error: %v, want undefined: missing at line 6 of its file", err)
	}

	if err := CheckTests(t.TempDir(), "lib", &types.Info{}, Options{}); err == nil || !strings.Contains(err.Error(), "no "+ModFile) {
		t.Errorf("a package under no module: %v", err)
	}
}

// TestCheckStdCheckout checks a standard package of the checkout as
// snello check and the language server check it: as the library's,
// whose functions the runtime provides and whose fields only it
// writes, with nothing reported; a file's text read through the
// options, its error on its line in the file named where it is, the
// package's own error and not the import's; the package's tests; and
// builtin, documentation nothing compiles, with nothing to check but
// its tests.
func TestCheckStdCheckout(t *testing.T) {
	dir := filepath.Join("..", "..", "std", "rc")
	if err := CheckDir(dir, &types.Info{}, Options{}); err != nil {
		t.Errorf("%s: %v", dir, err)
	}

	edited := filepath.Join(dir, "rc.snello")
	read := func(name string) ([]byte, error) {
		src, err := os.ReadFile(name)
		if name == edited {
			src = append(src, "\nfunc broken() int { return \"x\" }\n"...)
		}

		return src, err
	}

	var list scanner.ErrorList
	err := CheckDir(dir, &types.Info{}, Options{Read: read})
	lines := strings.Count(readFile(t, edited), "\n")
	if !errors.As(err, &list) || len(list) != 1 || list[0].Pos.Filename != edited || list[0].Pos.Line != lines+2 || strings.HasPrefix(err.Error(), "package ") {
		t.Errorf("an edited file of the checkout: %v, want its error at line %d of %s alone", err, lines+2, edited)
	}

	if err := CheckTests(dir, "rc_test", &types.Info{}, Options{}); err != nil {
		t.Errorf("%s's tests: %v", dir, err)
	}

	builtin := filepath.Join("..", "..", "std", builtinPath)
	if err := CheckDir(builtin, &types.Info{}, Options{}); err != nil {
		t.Errorf("builtin, which nothing compiles: %v, want nothing checked", err)
	}

	if err := CheckTests(builtin, builtinPath+TestSuffix, &types.Info{}, Options{}); err != nil {
		t.Errorf("builtin's tests: %v", err)
	}
}
