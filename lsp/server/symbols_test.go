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
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/lsp/jsonrpc"
	"github.com/snello-lang/snello/lsp/protocol"
)

// kindNames names the kinds of symbol for a test's reading.
var kindNames = map[protocol.SymbolKind]string{
	protocol.SymbolKindClass:      "class",
	protocol.SymbolKindMethod:     "method",
	protocol.SymbolKindField:      "field",
	protocol.SymbolKindEnum:       "enum",
	protocol.SymbolKindInterface:  "trait",
	protocol.SymbolKindFunction:   "func",
	protocol.SymbolKindVariable:   "var",
	protocol.SymbolKindConstant:   "const",
	protocol.SymbolKindEnumMember: "variant",
	protocol.SymbolKindStruct:     "struct",
}

// TestOutline writes the outline of a file: its traits with their
// fields and methods, its structs with their fields and then their
// methods, an enum with its variants, its functions and its variables,
// in the order written, with the kinds the protocol has; a method of a
// type of another file named with its type; and each symbol's stretch
// from its first token to its last, its name's on its name.
func TestOutline(t *testing.T) {
	tests := []struct {
		path, src, want string
	}{
		{
			path: "../../examples/traits/traits.snello",
			want: "Shape trait(Area method, Perimeter method, Name method, Describe method), Named trait(name field, Name method), Logged trait(Describe method), " +
				"Circle struct(r field, Area method, Perimeter method), Rect struct(w field, h field, Area method, Perimeter method, Describe method), " +
				"TotalArea func, PrintAll func, main func",
		},
		{path: "../../examples/packaged/shapes/area.snello", want: "Kind enum(Round variant, Square variant), Describe func, Area func, Largest func"},
		{path: "../../examples/packaged/report.snello", want: "reports var, report func, kindOf func, Pointed trait, Ranked trait"},
		{path: "methods.snello", src: "package p\n\nconst Tau f64 = 6.28\n\nfunc (r ref Rect) Area() f64 { return 0 }\n\ntype Celsius f64\n", want: "Tau const, Rect.Area method, Celsius class"},
	}

	for _, tt := range tests {
		src := []byte(tt.src)
		if tt.src == "" {
			var err error
			if src, err = os.ReadFile(tt.path); err != nil {
				t.Fatal(err)
			}
		}

		if got := treeOf(outline(tt.path, src, protocol.PositionEncodingUTF16)); got != tt.want {
			t.Errorf("%s:\n got %s\nwant %s", filepath.Base(tt.path), got, tt.want)
		}
	}

	src, err := os.ReadFile("../../examples/traits/traits.snello")
	if err != nil {
		t.Fatal(err)
	}

	symbols := outline("traits.snello", src, protocol.PositionEncodingUTF16)
	circle := symbols[3]
	describe := symbols[4].Children[4]
	for name, tt := range map[string]struct {
		got        protocol.DocumentSymbol
		from, upTo string // the text the stretch begins with and ends with
	}{
		"Circle":          {circle, "struct Circle impl Shape, Named, Logged {", "    r f64\n}"},
		"Circle's r":      {circle.Children[0], "    r f64", "r f64"},
		"Circle's Area":   {circle.Children[1], "func (c ref Circle) Area()", "math.Pi * c.r * c.r }"},
		"Rect's Describe": {describe, "func (r ref Rect) Describe()", `{ return "a rectangle" }`},
	} {
		start := strings.Index(string(src), tt.from) + len(tt.from) - len(strings.TrimLeft(tt.from, " "))
		end := strings.Index(string(src)[start:], tt.upTo) + start + len(tt.upTo)
		want := protocol.Range{Start: positionOf(src, start, protocol.PositionEncodingUTF16), End: positionOf(src, end, protocol.PositionEncodingUTF16)}
		if tt.got.Range != want {
			t.Errorf("%s: %+v, want %+v", name, tt.got.Range, want)
		}

		if sel := tt.got.SelectionRange; sel.Start.Line != sel.End.Line || int(sel.End.Character-sel.Start.Character) != len(tt.got.Name) {
			t.Errorf("%s: its name at %+v", name, sel)
		}
	}
}

// TestDocumentSymbol answers the outline of a document as a tree for a
// client that shows one, of a document never saved too, and as a list,
// each member with its type's name, for one that does not.
func TestDocumentSymbol(t *testing.T) {
	tree := navigate(t, protocol.ClientCapabilities{TextDocument: &protocol.TextDocumentClientCapabilities{
		DocumentSymbol: &protocol.DocumentSymbolClientCapabilities{HierarchicalDocumentSymbolSupport: true},
	}})
	shape := protocol.DocumentSymbolParams{TextDocument: protocol.TextDocumentIdentifier{URI: uriOf(tree.path("shapes/shape.snello"))}}
	var symbols []protocol.DocumentSymbol
	tree.decode(tree.call(protocol.MethodDocumentSymbol, shape), &symbols)
	if got := treeOf(symbols); got != "Shape trait(Name method, Area method), Circle struct(Radius field, Name method, Area method), Rect struct(W field, H field, Name method, Area method)" {
		t.Errorf("the tree: %s", got)
	}

	untitled := protocol.DocumentURI("untitled:Untitled-1")
	tree.notify(protocol.MethodDidOpen, protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: untitled, Version: 1, Text: "package p\n\nfunc Draft() {}\n"}})
	symbols = nil
	tree.decode(tree.call(protocol.MethodDocumentSymbol, protocol.DocumentSymbolParams{TextDocument: protocol.TextDocumentIdentifier{URI: untitled}}), &symbols)
	if got := treeOf(symbols); got != "Draft func" {
		t.Errorf("a document never saved: %q", got)
	}

	tree.shutdown()

	params := protocol.DocumentSymbolParams{TextDocument: protocol.TextDocumentIdentifier{URI: uriOf(tree.path("shapes/area.snello"))}}
	list := navigate(t, protocol.ClientCapabilities{})
	var infos []protocol.SymbolInformation
	list.decode(list.call(protocol.MethodDocumentSymbol, params), &infos)
	var got []string
	for _, info := range infos {
		got = append(got, info.Name+" in "+info.ContainerName)
		if info.Location.URI != params.TextDocument.URI {
			t.Errorf("%s in %s", info.Name, info.Location.URI)
		}
	}

	if want := []string{"Kind in ", "Round in Kind", "Square in Kind", "Describe in ", "Area in ", "Largest in "}; !slices.Equal(got, want) {
		t.Errorf("the list: %q, want %q", got, want)
	}

	list.shutdown()
}

// TestWorkspaceSymbol finds the declarations of every file of the
// module, the open ones and the rest, by the letters of a query in
// order, the best first, each with its package or its type; and those
// of the module of the folder the editor names, with no file open, and
// of every module under a folder that is in none.
func TestWorkspaceSymbol(t *testing.T) {
	n := navigate(t, protocol.ClientCapabilities{})
	tests := []struct {
		query, first, container, file string
	}{
		{"Largest", "Largest", "shapes", "shapes/area.snello"},
		{"kind", "Kind", "shapes", "shapes/area.snello"},
		{"lgst", "Largest", "shapes", "shapes/area.snello"},
		{"radius", "Radius", "Circle", "shapes/shape.snello"},
		{"TestArea", "TestArea", "shapes", "shapes/shapes_test.snello"},
		{"Medal", "Medal", "main", "main.snello"},
	}

	for _, tt := range tests {
		var got []protocol.SymbolInformation
		n.decode(n.call(protocol.MethodWorkspaceSymbol, protocol.WorkspaceSymbolParams{Query: tt.query}), &got)
		if len(got) == 0 || got[0].Name != tt.first || got[0].ContainerName != tt.container || got[0].Location.URI != uriOf(n.path(tt.file)) {
			t.Errorf("%q: %+v, want %s in %s, in %s first", tt.query, got, tt.first, tt.container, tt.file)
		}
	}

	var none []protocol.SymbolInformation
	n.decode(n.call(protocol.MethodWorkspaceSymbol, protocol.WorkspaceSymbolParams{Query: "zzzq"}), &none)
	if none == nil || len(none) != 0 {
		t.Errorf("no match: %+v, want an empty list", none)
	}

	n.shutdown()

	folder := &navigation{session: startSession(t), dir: n.dir}
	m := folder.call(protocol.MethodInitialize, protocol.InitializeParams{WorkspaceFolders: []protocol.WorkspaceFolder{{URI: uriOf(n.dir), Name: "packaged"}}})
	if m.Error != nil {
		t.Fatal(m.Error)
	}

	var got []protocol.SymbolInformation
	folder.decode(folder.call(protocol.MethodWorkspaceSymbol, protocol.WorkspaceSymbolParams{Query: "Largest"}), &got)
	if len(got) == 0 || got[0].Name != "Largest" {
		t.Errorf("from the workspace's folder, with no file open: %+v", got)
	}

	folder.shutdown()

	above := t.TempDir()
	for name, src := range map[string]string{"a/" + importer.ModFile: "snello 0.1.0\n", "a/a.snello": "package main\n\nfunc Alpha() {}\n", "b/" + importer.ModFile: "snello 0.1.0\n", "b/b.snello": "package main\n\nfunc Beta() {}\n"} {
		path := filepath.Join(above, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(src), fileMode); err != nil {
			t.Fatal(err)
		}
	}

	both := &navigation{session: startSession(t), dir: above}
	if m := both.call(protocol.MethodInitialize, protocol.InitializeParams{RootURI: uriOf(above)}); m.Error != nil {
		t.Fatal(m.Error)
	}

	for _, name := range []string{"Alpha", "Beta"} {
		got = nil
		both.decode(both.call(protocol.MethodWorkspaceSymbol, protocol.WorkspaceSymbolParams{Query: name}), &got)
		if len(got) != 1 || got[0].Name != name {
			t.Errorf("%s from a folder above two modules: %+v", name, got)
		}
	}

	both.shutdown()
}

// TestMatches matches a query's letters in order, in lower case as
// the search gives both, and ranks the name that is the query first,
// then one that begins with it, then one that holds it, then the rest.
func TestMatches(t *testing.T) {
	for _, tt := range []struct {
		name, query string
		want        bool
	}{
		{"Largest", "lgst", true},
		{"largest", "LGST", true},
		{"Lag", "lgst", false},
		{"TotalArea", "ta", true},
		{"Area", "ta", false},
	} {
		if got := matches(strings.ToLower(tt.name), strings.ToLower(tt.query)); got != tt.want {
			t.Errorf("matches(%q, %q) = %v, want %v", tt.name, tt.query, got, tt.want)
		}
	}

	names := []string{"area", "totalarea", "areas", "anyrealarea", "aerial"}
	slices.SortStableFunc(names, func(a, b string) int { return rank(a, "area") - rank(b, "area") })
	if want := []string{"area", "areas", "totalarea", "anyrealarea", "aerial"}; !slices.Equal(names, want) {
		t.Errorf("ranked %q, want %q", names, want)
	}

	if !matches("Anything", "") {
		t.Error("an empty query matches nothing")
	}
}

// treeOf writes an outline as names and kinds, each symbol's children
// in parentheses after it.
func treeOf(symbols []protocol.DocumentSymbol) string {
	var parts []string
	for _, sym := range symbols {
		part := sym.Name + " " + kindNames[sym.Kind]
		if len(sym.Children) > 0 {
			part += "(" + treeOf(sym.Children) + ")"
		}

		parts = append(parts, part)
	}

	return strings.Join(parts, ", ")
}

// BenchmarkWorkspaceSymbol searches a module of thirty packages of
// eight files of forty functions each, 9,600 symbols, for a query of
// one letter, which every symbol matches, the first search reading
// every file and the rest reading none: what an answer of every match
// costs where a search matches the most.
func BenchmarkWorkspaceSymbol(b *testing.B) {
	files := map[string]string{}
	for p := range 30 {
		for f := range 8 {
			var src strings.Builder
			fmt.Fprintf(&src, "package p%d\n\n", p)
			for g := range 40 {
				fmt.Fprintf(&src, "func F%d_%d() {}\n\n", f, g)
			}

			files["p"+strconv.Itoa(p)+"/f"+strconv.Itoa(f)+".snello"] = src.String()
		}
	}

	dir := writeModule(b, files)
	s := New(nil, io.Discard, io.Discard)
	s.folders = []string{dir}
	params, err := json.Marshal(protocol.WorkspaceSymbolParams{Query: "f"})
	if err != nil {
		b.Fatal(err)
	}

	var out countingWriter
	s.out = jsonrpc.NewWriter(&out)
	for b.Loop() {
		out.n = 0
		s.workspaceSymbol(&jsonrpc.Message{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: protocol.MethodWorkspaceSymbol, Params: params})
	}

	b.ReportMetric(float64(out.n), "bytes/answer")
}

// A countingWriter counts what is written to it.
type countingWriter struct{ n int }

// Write counts p.
func (w *countingWriter) Write(p []byte) (int, error) {
	w.n += len(p)
	return len(p), nil
}
