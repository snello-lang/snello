// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/compiler/scanner"
	"github.com/snello-lang/snello/compiler/types"
	"github.com/snello-lang/snello/lsp/protocol"
)

// examplesDir is where the example programs are, from this package's
// directory, and pendingExample the one that waits for a library and
// so does not compile.
const (
	examplesDir    = "../../examples"
	pendingExample = "cli-grep"
)

// TestExamples opens every source file of every package under
// examples/ and checks it as the editor would: a package the compiler
// accepts, its tests included, has no diagnostic, and the pending
// example has the errors snello check reports, message for message.
func TestExamples(t *testing.T) {
	dirs := sourceDirs(t, examplesDir)
	if len(dirs) < 30 {
		t.Fatalf("only %d packages under %s: the sweep no longer finds them", len(dirs), examplesDir)
	}

	for _, dir := range dirs {
		s := New(strings.NewReader(""), io.Discard, io.Discard)
		for _, name := range sourceNames(t, dir) {
			src, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}

			s.open(protocol.TextDocumentItem{URI: uriOf(name), Text: string(src)})
		}

		s.checkAll()
		got := messagesOf(s.reports)
		if filepath.Base(dir) != pendingExample {
			if len(got) > 0 {
				t.Errorf("%s: %d diagnostics, want none:\n%s", dir, len(got), strings.Join(got, "\n"))
			}

			continue
		}

		var want []string
		for _, e := range compilerErrors(t, dir) {
			want = append(want, e.Msg)
		}

		slices.Sort(want)
		if len(want) == 0 || !slices.Equal(got, want) {
			t.Errorf("%s: diagnostics %q, want the compiler's %q", dir, got, want)
		}
	}
}

// TestTests checks a package's test files as snello test loads them:
// an error in a test file of the package, and one in a test file of its
// external test package, are reported on their files, and the package's
// sources, which compile, have none.
func TestTests(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"lib/lib.snello":        "package lib\n\n// Double doubles n.\nfunc Double(n int) int {\n\treturn n * 2\n}\n",
		"lib/lib_test.snello":   "package lib\n\nimport \"expect\"\n\nfunc TestDouble() {\n\texpect.Equal(ref Double(2), ref missing)\n}\n",
		"user/user.snello":      "package user\n\n// One is one.\nfunc One() int {\n\treturn 1\n}\n",
		"user/user_test.snello": "package user_test\n\nimport (\"expect\"; \"user\")\n\nfunc TestOne() {\n\texpect.Equal(ref user.One(), ref absent)\n}\n",
	})

	for _, tt := range []struct{ file, want string }{
		{"lib/lib_test.snello", "undefined: missing"},
		{"user/user_test.snello", "undefined: absent"},
	} {
		s := New(strings.NewReader(""), io.Discard, io.Discard)
		path := filepath.Join(dir, filepath.FromSlash(tt.file))
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		s.open(protocol.TextDocumentItem{URI: uriOf(path), Text: string(src)})
		s.checkAll()
		diags := union(s.reports)
		if len(diags[path]) != 1 || diags[path][0].Message != tt.want {
			t.Errorf("%s: %+v, want one diagnostic, %q", tt.file, diags, tt.want)
		}

		source := filepath.Join(filepath.Dir(path), strings.TrimSuffix(filepath.Base(path), importer.TestFileSuffix)+importer.SourceExt)
		if len(diags[source]) > 0 {
			t.Errorf("%s: its package's source has diagnostics: %+v", tt.file, diags[source])
		}
	}
}

// TestPlatforms checks an open file of another platform than the host's
// for its own platform: an error of its build alone is reported with
// the platform before its message, and one every build has, in a file
// every build chooses, once and with none; a package with no open file
// of another platform is checked for the host alone.
func TestPlatforms(t *testing.T) {
	other := "windows"
	if runtime.GOOS == other {
		other = "linux"
	}

	dir := writeModule(t, map[string]string{
		"lib/lib.snello":                      "package lib\n\nfunc Shared() int { return \"every build\" }\n",
		"lib/sys_" + other + ".snello":        "package lib\n\nfunc Own() int { return missing }\n",
		"lib/sys_" + runtime.GOOS + ".snello": "package lib\n\nfunc Own() int { return 1 }\n",
	})

	s := New(nil, io.Discard, io.Discard)
	own := filepath.Join(dir, "lib", "sys_"+other+".snello")
	src, err := os.ReadFile(own)
	if err != nil {
		t.Fatal(err)
	}

	s.open(protocol.TextDocumentItem{URI: uriOf(own), Version: 1, Text: string(src)})
	s.checkAll()
	var first platform.Target
	for _, tgt := range platform.Targets() {
		if tgt.OS == other {
			first = tgt
			break
		}
	}

	all := union(s.reports)
	if got := all[own]; len(got) != 1 || got[0].Message != "on "+first.String()+": undefined: missing" {
		t.Errorf("the file of another platform: %+v, want its error with %s before it", got, first)
	}

	if got := all[filepath.Join(dir, "lib", "lib.snello")]; len(got) != 1 || strings.HasPrefix(got[0].Message, "on ") {
		t.Errorf("an error every build has: %+v, want it once, with no platform", got)
	}

	s.close(uriOf(own))
	if targets := s.platformsOf(filepath.Join(dir, "lib")); len(targets) != 0 {
		t.Errorf("with no file of another platform open: %v, want the host alone", targets)
	}
}

// TestReportOf reports an error that names no file on each open
// document given, and an error of the checker's panic in place of the
// panic.
func TestReportOf(t *testing.T) {
	s := New(strings.NewReader(""), io.Discard, io.Discard)
	r := s.reportOf(errors.New("no module file"), nil, []string{"/a.snello", "/b.snello"})
	if len(r) != 2 || r["/a.snello"][0].Message != "no module file" || r["/b.snello"][0].Range != (protocol.Range{}) {
		t.Errorf("an error with no file: %+v", r)
	}

	err := s.guard(func() error { panic("an index out of range") })
	if err == nil || !strings.Contains(err.Error(), "an index out of range") {
		t.Errorf("a panic in the check: %v", err)
	}

	list := scanner.ErrorList{{Msg: "no position"}}
	r = s.reportOf(list, nil, []string{"/a.snello"})
	if len(r["/a.snello"]) != 1 || r["/a.snello"][0].Message != "no position" {
		t.Errorf("an error of the list with no position: %+v", r)
	}
}

// BenchmarkCheck checks every package under examples/ as the server
// checks the packages with an open document after a change, each with
// its tests: its time per operation is a keystroke's with all of them
// open, its ns/package a keystroke's with one, and -benchmem gives
// their allocations.
func BenchmarkCheck(b *testing.B) {
	s := New(strings.NewReader(""), io.Discard, io.Discard)
	dirs := sourceDirs(b, examplesDir)
	for _, dir := range dirs {
		for _, name := range sourceNames(b, dir) {
			src, err := os.ReadFile(name)
			if err != nil {
				b.Fatal(err)
			}

			s.open(protocol.TextDocumentItem{URI: uriOf(name), Text: string(src)})
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		s.checkAll()
	}

	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(dirs)), "ns/package")
}

// sourceDirs returns every directory under root that holds a source
// file, sorted.
func sourceDirs(t testing.TB, root string) []string {
	t.Helper()
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, importer.SourceExt) {
			dirs = append(dirs, filepath.Dir(path))
		}

		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	slices.Sort(dirs)
	return slices.Compact(dirs)
}

// sourceNames returns the absolute paths of the source and test files
// of dir built for this machine.
func sourceNames(t testing.TB, dir string) []string {
	t.Helper()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}

	files, err := importer.SourceFiles(abs, platform.Host())
	if err != nil {
		t.Fatal(err)
	}

	tests, err := importer.TestFiles(abs, platform.Host())
	if err != nil {
		t.Fatal(err)
	}

	return append(files, tests...)
}

// compilerErrors returns the errors snello check DIR reports.
func compilerErrors(t *testing.T, dir string) scanner.ErrorList {
	t.Helper()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}

	err = importer.CheckDir(abs, &types.Info{}, importer.Options{})
	var list scanner.ErrorList
	if err != nil && !errors.As(err, &list) {
		t.Fatalf("%s: %v, want a list of errors", dir, err)
	}

	return list
}

// writeModule writes a module of files, by their slashed paths under
// its root, into a new directory, and returns the directory.
func writeModule(t testing.TB, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files[importer.ModFile] = "snello 0.1.0\n"
	for name, src := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

// union returns every report's diagnostics, by path.
func union(reports map[string][]report) map[string][]protocol.Diagnostic {
	all := make(map[string][]protocol.Diagnostic)
	for _, rs := range reports {
		for _, r := range rs {
			for path, diags := range r {
				all[path] = append(all[path], diags...)
			}
		}
	}

	return all
}

// messagesOf returns the messages of every report's diagnostics,
// sorted.
func messagesOf(reports map[string][]report) []string {
	var messages []string
	for _, diags := range union(reports) {
		for _, d := range diags {
			messages = append(messages, d.Message)
		}
	}

	slices.Sort(messages)
	return messages
}
