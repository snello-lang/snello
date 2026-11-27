// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/snello-lang/snello/lsp/jsonrpc"
	"github.com/snello-lang/snello/lsp/protocol"
)

// runnablesOfSession asks a session for the runnables of the document
// at path, or of the workspace for "", each written kind name args.
func runnablesOfSession(n *navigation, path string) []string {
	n.t.Helper()
	var p protocol.RunnablesParams
	if path != "" {
		p.TextDocument = &protocol.TextDocumentIdentifier{URI: uriOf(path)}
	}

	var got []protocol.Runnable
	n.decode(n.call(protocol.MethodRunnables, p), &got)
	var out []string
	for _, r := range got {
		out = append(out, string(r.Kind)+" "+r.Name+" "+strings.Join(r.Args, " "))
	}

	return out
}

// TestRunnables answers the programs and tests of a document and of the
// workspace, each with the arguments that run it alone, its stretch and
// its name's; an edited buffer as it stands, and a file that no longer
// parses as it last did; and announces the request.
func TestRunnables(t *testing.T) {
	n := navigate(t, protocol.ClientCapabilities{})
	shapes := n.path("shapes")
	main := []string{"main main run " + n.dir}
	tests := []string{
		"test TestDescribe test -run ^TestDescribe$ " + shapes,
		"test TestArea test -run ^TestArea$ " + shapes,
		"test TestLargest test -run ^TestLargest$ " + shapes,
		"example ExampleDescribe test -run ^ExampleDescribe$ " + shapes,
	}

	if got := runnablesOfSession(n, n.path("main.snello")); !slices.Equal(got, main) {
		t.Errorf("main.snello: %q, want %q", got, main)
	}

	if got := runnablesOfSession(n, n.path("shapes/shapes_test.snello")); !slices.Equal(got, tests) {
		t.Errorf("shapes_test.snello: %q, want %q", got, tests)
	}

	if got := runnablesOfSession(n, n.path("shapes/area.snello")); len(got) != 0 {
		t.Errorf("a library's source file: %q, want none", got)
	}

	all := runnablesOfSession(n, "")
	for _, want := range append(main, tests...) {
		if !slices.Contains(all, want) {
			t.Errorf("the workspace: %q has no %q", all, want)
		}
	}

	var found []protocol.Runnable
	n.decode(n.call(protocol.MethodRunnables, protocol.RunnablesParams{TextDocument: &protocol.TextDocumentIdentifier{URI: uriOf(n.path("shapes/shapes_test.snello"))}}), &found)
	area := found[1]
	text := string(n.texts["shapes/shapes_test.snello"])
	start := strings.Index(text, "func TestArea")
	if want := n.location(place{"shapes/shapes_test.snello", "func ", "TestArea"}); area.SelectionRange != want.Range || area.Location.URI != want.URI || area.Package != "shapes" || area.Dir != shapes {
		t.Errorf("TestArea: %+v, its name at %+v", area, want.Range)
	}

	if area.Location.Range.Start != positionOf([]byte(text), start, protocol.PositionEncodingUTF16) {
		t.Errorf("TestArea's stretch begins at %+v, want its func", area.Location.Range.Start)
	}

	edited := strings.Replace(text, "func TestLargest()", "func TestSmallest() {}\n\nfunc TestLargest()", 1)
	n.change(uriOf(n.path("shapes/shapes_test.snello")), 2, edited)
	if got := runnablesOfSession(n, n.path("shapes/shapes_test.snello")); !slices.Contains(got, "test TestSmallest test -run ^TestSmallest$ "+shapes) {
		t.Errorf("the edited buffer: %q has no TestSmallest", got)
	}

	n.change(uriOf(n.path("shapes/shapes_test.snello")), 3, edited+"\nfunc TestBroken( {")
	if got := runnablesOfSession(n, n.path("shapes/shapes_test.snello")); !slices.Contains(got, "test TestSmallest test -run ^TestSmallest$ "+shapes) || len(got) != len(tests)+1 {
		t.Errorf("a buffer that does not parse: %q, want what it held when it last parsed", got)
	}

	n.shutdown()
}

// TestRunnablesKinds answers a benchmark by a pattern that names no test,
// a test of a package that does not compile as any other, a program of
// one file among others by its file, and the one program of its
// directory by the directory until a second joins it; a test file for
// another platform holds none; and announces the request in the
// capabilities.
func TestRunnablesKinds(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"main.snello":         "package main\n\nfunc main() {}\n",
		"lib/lib.snello":      "package lib\n\nfunc Two() int { return \"two\" }\n",
		"lib/lib_test.snello": "package lib\n\nimport \"testing\"\n\nfunc TestTwo() {}\n\nfunc BenchmarkTwo() {\n\tfor testing.Loop() {}\n}\n\nfunc helper() {}\n",
	})

	n := navigateIn(t, protocol.ClientCapabilities{}, dir, "main.snello", "lib/lib_test.snello")
	lib := n.path("lib")
	want := []string{
		"test TestTwo test -run ^TestTwo$ " + lib,
		"benchmark BenchmarkTwo test -run ^$ -bench ^BenchmarkTwo$ " + lib,
	}

	if got := runnablesOfSession(n, n.path("lib/lib_test.snello")); !slices.Equal(got, want) {
		t.Errorf("lib_test.snello: %q, want %q", got, want)
	}

	n.shutdown()
	tree, err := filepath.Abs(filepath.Join("..", "..", "examples", "tree"))
	if err != nil {
		t.Fatal(err)
	}

	m := navigateIn(t, protocol.ClientCapabilities{}, tree, "tree-arena.snello")
	if got := runnablesOfSession(m, m.path("tree-arena.snello")); !slices.Equal(got, []string{"main main run " + m.path("tree-arena.snello")}) {
		t.Errorf("a program of one file among others: %q, want it run by its file", got)
	}

	m.shutdown()
	other := "windows"
	if runtime.GOOS == other {
		other = "linux"
	}

	progs := writeModule(t, map[string]string{
		"p/a.snello":                    "package main\n\nfunc main() {}\n",
		"p/p_" + other + "_test.snello": "package main\n\nfunc TestElsewhere() {}\n",
	})
	o := navigateIn(t, protocol.ClientCapabilities{}, progs, "p/a.snello")
	dirP := o.path("p")
	if got := runnablesOfSession(o, o.path("p/a.snello")); !slices.Equal(got, []string{"main main run " + dirP}) {
		t.Errorf("the one program of its directory: %q, want it run by its directory", got)
	}

	if got := runnablesOfSession(o, o.path("p/p_"+other+"_test.snello")); len(got) != 0 {
		t.Errorf("a test file for another platform: %q, want none", got)
	}

	if err := os.WriteFile(o.path("p/b.snello"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := runnablesOfSession(o, o.path("p/a.snello")); !slices.Equal(got, []string{"main main run " + o.path("p/a.snello")}) {
		t.Errorf("once a second program is beside it: %q, want it run by its file", got)
	}

	o.shutdown()
	s := startSession(t)
	if caps := s.initialize(protocol.ClientCapabilities{}).Capabilities; caps.Experimental == nil || !caps.Experimental.Runnables {
		t.Errorf("capabilities %+v: runnables not announced", caps.Experimental)
	}

	s.shutdown()
}

// BenchmarkRunnables answers the runnables of a module of thirty
// packages, each a test file of forty tests and a source file, the
// first answer reading every file and the rest reading none: what the
// editor's tree of tests costs to keep.
func BenchmarkRunnables(b *testing.B) {
	files := map[string]string{}
	for p := range 30 {
		pkg := "p" + strconv.Itoa(p)
		var src strings.Builder
		fmt.Fprintf(&src, "package %s\n\n", pkg)
		for g := range 40 {
			fmt.Fprintf(&src, "func Test%d() {}\n\n", g)
		}

		files[pkg+"/"+pkg+"_test.snello"] = src.String()
		files[pkg+"/"+pkg+".snello"] = "package " + pkg + "\n\nfunc F() {}\n"
	}

	dir := writeModule(b, files)
	s := New(nil, io.Discard, io.Discard)
	s.folders = []string{filepath.Clean(dir)}
	var out countingWriter
	s.out = jsonrpc.NewWriter(&out)
	for b.Loop() {
		out.n = 0
		s.runnables(&jsonrpc.Message{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: protocol.MethodRunnables})
	}

	b.ReportMetric(float64(out.n), "bytes/answer")
}
