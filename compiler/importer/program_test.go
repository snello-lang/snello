// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package importer

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/platform"
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
