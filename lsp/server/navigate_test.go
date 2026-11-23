// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/c"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/version"
	"github.com/snello-lang/snello/lsp/jsonrpc"
	"github.com/snello-lang/snello/lsp/protocol"
	"github.com/snello-lang/snello/std"
)

// A place is a name or a token of a file of examples/packaged, as a
// test points at it: the file, under the example, the text that leads
// to it, and the text it begins, which may be the lead's own end.
type place struct {
	file, lead, at string
}

// TestDefinition goes to the declaration of a name declared in the
// file, in another file of the package, in a package of the program's
// own, from a test file, of a method through a trait bound, which is the
// trait's method, and in a standard package, whose file is copied into
// the cache read-only and copied again when it differs, and which says
// why it cannot with the cache off; a name the language declares, and a
// place that holds none, go nowhere.
func TestDefinition(t *testing.T) {
	n := navigate(t, protocol.ClientCapabilities{})
	tests := []struct {
		name     string
		from, to place
	}{
		{"in the file", place{"main.snello", "var mine Shape = ", "Triangle"}, place{"main.snello", "struct ", "Triangle"}},
		{"in another file", place{"main.snello", "fmt.Println(", "report(ref c)"}, place{"report.snello", "func ", "report<"}},
		{"in a package of the program", place{"main.snello", "shapes.", "Largest"}, place{"shapes/area.snello", "func ", "Largest"}},
		{"a method through a bound", place{"report.snello", "s.Name(), s.", "Area()"}, place{"shapes/shape.snello", "func (s ref Self) ", "Area()"}},
		{"a variable", place{"main.snello", "fmt.Println(", "mine.Corners()"}, place{"main.snello", "var ", "mine"}},
		{"from a test file", place{"shapes/shapes_test.snello", "expect.Equal(ref ", "Describe(c.kind)"}, place{"shapes/area.snello", "func ", "Describe"}},
		{"a package's name", place{"main.snello", "fmt.Println(", "shapes.Largest"}, place{"main.snello", "import (\"fmt\"; ", "\"shapes\""}},
	}

	for _, tt := range tests {
		got := n.definition(tt.from)
		if got == nil {
			t.Errorf("%s: no definition", tt.name)
			continue
		}

		if want := n.location(tt.to); *got != want {
			t.Errorf("%s: %+v, want %+v", tt.name, *got, want)
		}
	}

	cache := t.TempDir()
	t.Setenv(c.CacheEnv, cache)
	got := n.definition(place{"main.snello", "fmt.", "Println(c.Name()"})
	copied := filepath.Join(cache, stdCopies, version.Current, "fmt", "fmt.snello")
	src, err := std.Sources.ReadFile("fmt/fmt.snello")
	if err != nil {
		t.Fatal(err)
	}

	if got == nil || got.URI != uriOf(copied) {
		t.Fatalf("a standard package's: %+v, want %s", got, uriOf(copied))
	}

	if on, err := os.ReadFile(copied); err != nil || !bytes.Equal(on, src) {
		t.Errorf("the copy of fmt.snello: %v, or it differs from the compiler's", err)
	}

	start := bytes.Index(src, []byte("func Println(")) + len("func ")
	if want := positionOf(src, start, protocol.PositionEncodingUTF16); got.Range.Start != want {
		t.Errorf("a standard package's: at %+v, want %+v", got.Range.Start, want)
	}

	if info, err := os.Stat(copied); err != nil || runtime.GOOS != windows && info.Mode().Perm() != copyMode {
		t.Errorf("the copy is not read-only: %v, %v", info.Mode(), err)
	}

	if err := os.Chmod(copied, fileMode); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(copied, []byte("stale"), fileMode); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(copied, copyMode); err != nil {
		t.Fatal(err)
	}

	n.definition(place{"main.snello", "fmt.", "Println(c.Name()"})
	if on, err := os.ReadFile(copied); err != nil || !bytes.Equal(on, src) {
		t.Errorf("a copy that differs from the compiler's is written again: %v, %q", err, on)
	}

	t.Setenv(c.CacheEnv, c.CacheOff)
	for name, at := range map[string]place{
		"a standard package's":         {"main.snello", "fmt.", "Println(c.Name()"},
		"a name the language declares": {"main.snello", "func (t ref Triangle) Corners() ", "int"},
	} {
		if m := n.call(protocol.MethodDefinition, n.params(at)); m.Error == nil || !strings.Contains(m.Error.Message, c.CacheEnv) {
			t.Errorf("%s with the cache off: %+v, want an error naming %s", name, m.Error, c.CacheEnv)
		}
	}

	if got := n.definition(place{"main.snello", "Corners() int ", "{"}); got != nil {
		t.Errorf("a place with no name: %+v, want none", *got)
	}

	n.shutdown()
}

// TestHover shows the declaration of a function, a method, a struct, a
// trait, an enum, a variant, a variable, a constant and a field as the
// checker types it, in a Snello block of Markdown, each with its doc
// comment below it; the type of an expression at its operator or its
// literal; and nothing where there is neither. A hover sent after a
// change is answered on the changed text. In plain text the declaration
// is not fenced.
func TestHover(t *testing.T) {
	n := navigate(t, protocol.ClientCapabilities{TextDocument: &protocol.TextDocumentClientCapabilities{
		Hover: &protocol.HoverClientCapabilities{ContentFormat: []string{"html", protocol.MarkupKindMarkdown}},
	}})
	tests := []struct {
		name      string
		at        place
		about     string // the text the hover is about, which begins at the place
		decl, doc string
	}{
		{"a function", place{"main.snello", "shapes.", "Largest"}, "Largest", "func Largest<A: Shape, B: Shape>(a ref A, b ref B) f64", "Largest is the larger area of two shapes lent.\n"},
		{"a method", place{"main.snello", "fmt.Println(c.", "Name()"}, "Name", "func (c ref shapes.Circle) Name() string", ""},
		{"a struct", place{"main.snello", "r := shapes.", "Rect{"}, "Rect", "struct Rect impl Shape {\n\tW f64\n\tH f64\n}", ""},
		{"a trait", place{"main.snello", "var mine ", "Shape"}, "Shape", "trait Shape {\n\tfunc (s ref Self) Corners() int\n\tfunc (s ref Self) Sides() int\n}", "Shape is the program's own trait, named as the package's is: each\nhas its own table. Triangle reaches it through Pointed, which\nreport.snello declares, so its default Sides comes from a trait of\nthis file through one of the next.\n"},
		{"an enum", place{"report.snello", "s ref S) shapes.", "Kind"}, "Kind", "enum Kind impl Copy, Clone, Eq {\n\tRound\n\tSquare\n}", "Kind says whether a shape has corners.\n"},
		{"a variant", place{"main.snello", "kindOf(ref r) == shapes.", "Square"}, "Square", "Kind.Square", ""},
		{"a variable", place{"main.snello", "fmt.Println(", "mine.Corners()"}, "mine", "var mine Shape", ""},
		{"a package variable", place{"main.snello", "shapes.Area(r), ", "reports.Load()"}, "reports", "var reports sync.Counter", "reports counts the reports made: a package variable declared in the\nsecond file of the main package and read by the first, a counter\nsince a package variable is changed only through what it holds.\n"},
		{"a constant", place{"shapes/shape.snello", "math.", "Pi"}, "Pi", "const Pi f64 = 3.141592653589793", "Pi is the ratio of a circle's circumference to its diameter, to the\nprecision an f64 holds.\n"},
		{"a field", place{"shapes/shape.snello", "math.Pi * c.", "Radius"}, "Radius", "field Radius f64", ""},
		{"an operator", place{"main.snello", "kindOf(ref r) ", "=="}, "==", "bool", ""},
		{"a literal", place{"main.snello", "Medal{points: ", "7"}, "7", "int", ""},
		{"a test file's type", place{"shapes/shapes_test.snello", "cases := []", "Case"}, "Case", "struct Case {\n\tname string\n\tkind shapes.Kind\n\twant string\n}", ""},
		{"a package's name", place{"main.snello", "c := ", "shapes.Circle"}, "shapes", "package shapes (\"shapes\")", "Package shapes is a package of the program's own, found through the\nmodule file above it: the trait and the structs are in this file,\nthe enum and the generic functions in area.snello.\n"},
		{"an import's path", place{"main.snello", "import (\"fmt\"; ", "\"shapes\""}, "\"shapes\"", "package shapes (\"shapes\")", "Package shapes is a package of the program's own, found through the\nmodule file above it: the trait and the structs are in this file,\nthe enum and the generic functions in area.snello.\n"},
		{"the package clause", place{"shapes/shape.snello", "package ", "shapes"}, "shapes", "package shapes", "Package shapes is a package of the program's own, found through the\nmodule file above it: the trait and the structs are in this file,\nthe enum and the generic functions in area.snello.\n"},
	}

	for _, tt := range tests {
		got := n.hover(tt.at)
		if got == nil {
			t.Errorf("%s: no hover", tt.name)
			continue
		}

		value := fenceOpen + tt.decl + fence
		if tt.doc != "" {
			value += "\n\n" + markdownOf(tt.doc)
		}

		if got.Contents.Kind != protocol.MarkupKindMarkdown || got.Contents.Value != value {
			t.Errorf("%s:\n got %s %q\nwant %q", tt.name, got.Contents.Kind, got.Contents.Value, value)
		}

		start := n.offset(tt.at)
		text := n.texts[tt.at.file]
		want := protocol.Range{Start: positionOf(text, start, protocol.PositionEncodingUTF16), End: positionOf(text, start+len(tt.about), protocol.PositionEncodingUTF16)}
		if got.Range == nil || *got.Range != want {
			t.Errorf("%s: about %+v, want %+v", tt.name, got.Range, want)
		}
	}

	if got := n.hover(place{"main.snello", "Corners() int ", "{"}); got != nil {
		t.Errorf("a brace: %+v, want none", *got)
	}

	renamed := strings.ReplaceAll(string(n.texts["main.snello"]), "mine", "ours")
	n.change(uriOf(n.path("main.snello")), 2, renamed)
	n.texts["main.snello"] = []byte(renamed)
	if got := n.hover(place{"main.snello", "fmt.Println(", "ours.Corners()"}); got == nil || got.Contents.Value != fenceOpen+"var ours Shape"+fence {
		t.Errorf("a hover sent after a change, before its check: %+v, want it answered on the changed text", got)
	}

	n.shutdown()

	plain := navigate(t, protocol.ClientCapabilities{})
	if got := plain.hover(place{"main.snello", "var mine Shape = ", "Triangle"}); got == nil || got.Contents.Kind != protocol.MarkupKindPlainText || got.Contents.Value != "struct Triangle impl Pointed {\n\tside f64\n}" {
		t.Errorf("in plain text: %+v", got)
	}

	plain.shutdown()
}

// TestStdCopy opens the copy of a standard file a definition went to,
// as an editor does: it reports nothing, and a hover in it is answered
// from a check of the library's own package; a copy of another version
// of the compiler's reports nothing and answers nothing.
func TestStdCopy(t *testing.T) {
	cache := t.TempDir()
	t.Setenv(c.CacheEnv, cache)
	n := navigate(t, protocol.ClientCapabilities{})
	loc := n.definition(place{"main.snello", "fmt.", "Println(c.Name()"})
	if loc == nil {
		t.Fatal("no definition in fmt")
	}

	text, err := os.ReadFile(pathOf(loc.URI))
	if err != nil {
		t.Fatal(err)
	}

	other := protocol.DocumentURI(uriOf(filepath.Join(cache, stdCopies, "0.0.1", "fmt", "fmt.snello")))
	for _, uri := range []protocol.DocumentURI{loc.URI, other} {
		n.notify(protocol.MethodDidOpen, protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: uri, Version: 1, Text: string(text)}})
	}

	var h *protocol.Hover
	n.decode(n.call(protocol.MethodHover, protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: loc.URI}, Position: loc.Range.Start}), &h)
	if h == nil || !strings.HasPrefix(h.Contents.Value, "func Println(args ...ref any)\n\n") {
		t.Errorf("a hover in the copy: %+v, want Println's declaration and its doc comment", h)
	}

	h = nil
	n.decode(n.call(protocol.MethodHover, protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: other}, Position: loc.Range.Start}), &h)
	if h != nil {
		t.Errorf("a hover in another version's copy: %+v, want none", h)
	}

	for _, m := range n.pending {
		var p protocol.PublishDiagnosticsParams
		if m.Method == protocol.MethodPublishDiagnostics && json.Unmarshal(m.Params, &p) == nil && (p.URI == loc.URI || p.URI == other) {
			t.Errorf("diagnostics on a copy of the library: %+v", p)
		}
	}

	n.shutdown()
}

// TestSyntaxError keeps a package's answers through a syntax error
// typed into one of its files: a file beside it is answered from the
// last check while its text is the same, the file with the error is
// answered with nothing, and both are answered from a check again once
// the file parses.
func TestSyntaxError(t *testing.T) {
	n := navigate(t, protocol.ClientCapabilities{})
	uri, good := uriOf(n.path("report.snello")), string(n.texts["report.snello"])
	beside, broken := place{"main.snello", "fmt.Println(", "mine.Corners()"}, place{"report.snello", "func ", "kindOf<"}
	if got := n.hover(beside); got == nil { // answered once the files as opened are checked: the last check that typed them
		t.Fatal("no hover before the error")
	}

	n.change(uri, 2, strings.Replace(good, "reports.Add(1)", "reports.Add(1", 1))
	if got := n.hover(beside); got == nil || got.Contents.Value != "var mine Shape" {
		t.Errorf("a file beside the error: %+v, want its last answer", got)
	}

	if got := n.hover(broken); got != nil {
		t.Errorf("the file with the error: %+v, want none", got)
	}

	n.change(uri, 3, good)
	if got := n.hover(broken); got == nil || !strings.HasPrefix(got.Contents.Value, "func kindOf<S: Shape>(s ref S) shapes.Kind") {
		t.Errorf("the file fixed: %+v, want kindOf's declaration", got)
	}

	n.shutdown()
}

// TestQuery answers a request whose answer panics with the request's
// error, as a check's half-typed results could have it, rather than
// ending the server.
func TestQuery(t *testing.T) {
	var out bytes.Buffer
	s := New(nil, &out, io.Discard)
	f, file := parse(t, pointSrc)
	path := filepath.Join(t.TempDir(), "point.snello")
	s.views[path] = &view{syntax: map[*token.File]*ast.File{file: f}, srcs: map[string][]byte{file.Name(): []byte(pointSrc)}, byPath: map[string]*token.File{path: file}} // no checker's information: a lookup panics
	params, err := json.Marshal(protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: uriOf(path)}, Position: protocol.Position{Line: 3, Character: 8}})
	if err != nil {
		t.Fatal(err)
	}

	s.query(&jsonrpc.Message{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: protocol.MethodHover, Params: params})
	m, err := jsonrpc.NewReader(&out).Read()
	if err != nil || m.Error == nil || m.Error.Code != jsonrpc.InternalError || string(m.ID) != "1" {
		t.Errorf("a request that panics: %+v, %v, want the internal error", m, err)
	}
}

// TestMarkdownOf escapes the marks of Markdown in a doc comment, and
// leaves a line indented as code as written.
func TestMarkdownOf(t *testing.T) {
	got := markdownOf("Max<T> is *the* larger, a_b [x].\n\tm := Max<int>(1, 2) // *\n")
	want := "Max\\<T\\> is \\*the\\* larger, a\\_b \\[x\\]\\.\n\tm := Max<int>(1, 2) // *\n"
	if got != want {
		t.Errorf("markdownOf:\n got %q\nwant %q", got, want)
	}
}

// TestStdPaths places a standard file's copy in the cache under the
// compiler's version, refuses a name that is not a path under std/ and
// any with the cache off, and tells a directory of the copies, this
// version's or another's, from one that is not.
func TestStdPaths(t *testing.T) {
	cache := t.TempDir()
	t.Setenv(c.CacheEnv, cache)
	copies := filepath.Join(cache, stdCopies, version.Current)
	abs := filepath.Join(cache, "a.snello")
	for name, want := range map[string]string{"fmt/fmt.snello": filepath.Join(copies, "fmt", "fmt.snello"), abs: abs, "../fmt.snello": ""} {
		if got := stdPathOf(name); got != want {
			t.Errorf("stdPathOf(%q) = %q, want %q", name, got, want)
		}
	}

	for dir, want := range map[string][2]any{
		filepath.Join(copies, "unicode", "utf8"):           {"unicode/utf8", true},
		filepath.Join(cache, stdCopies, "0.0.1", "fmt"):    {"", true},
		filepath.Join(cache, "elsewhere", version.Current): {"", false},
	} {
		if path, ok := stdCopyOf(dir); path != want[0] || ok != want[1] {
			t.Errorf("stdCopyOf(%q) = %q, %v, want %v", dir, path, ok, want)
		}
	}

	t.Setenv(c.CacheEnv, c.CacheOff)
	if got := stdPathOf("fmt/fmt.snello"); got != "" {
		t.Errorf("with the cache off: %q", got)
	}
}

// TestPackageDocTests hovers on the package clause of a test file,
// whose check loads the test files with the package: the package's
// comment is its source files', as snello doc gives it, and a test
// file's comment above its clause is none of it.
func TestPackageDocTests(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"lib/lib.snello":      "// Package lib does one thing.\npackage lib\n\n// F is one.\nfunc F() int { return 1 }\n",
		"lib/lib_test.snello": "// The tests of lib.\npackage lib\n\nimport \"expect\"\n\nfunc TestF() { expect.Equal(ref F(), ref 1) }\n",
	})
	n := navigateIn(t, protocol.ClientCapabilities{}, dir, "lib/lib.snello", "lib/lib_test.snello")
	if h := n.hover(place{"lib/lib_test.snello", "package ", "lib"}); h == nil || h.Contents.Value != "package lib\n\nPackage lib does one thing.\n" {
		t.Errorf("the package clause of a test file: %+v, want the package's comment alone", h)
	}

	n.shutdown()
}

// TestBuiltinNavigation hovers on and goes to the definition of names
// the language declares, which no file of the program declares: a trait
// in a bound and in an impl list, its method, a method of a slice, a
// string and an option, a built-in function, a type and a constant.
// Each hover has the comment builtinFile writes for it, and each
// definition is the name in the file's copy in the cache.
func TestBuiltinNavigation(t *testing.T) {
	t.Setenv(c.CacheEnv, t.TempDir())
	dir := writeModule(t, map[string]string{"main.snello": "package main\n\nimport \"fmt\"\n\nstruct P impl Eq { n int }\n\nfunc same<T: Eq>(a ref T, b ref T) bool { return a.Eq(b) }\n\nfunc main() {\n\txs := []int{1}\n\txs.Push(2)\n\ts := \"ab\"\n\to := xs.Pop()\n\tfmt.Println(len(xs), s.Contains(\"a\"), o.IsSome(), same(ref 1, ref 2), true)\n}\n"})
	n := navigateIn(t, protocol.ClientCapabilities{}, dir, "main.snello")
	for _, tt := range []struct {
		at        place
		name, doc string
	}{
		{place{"main.snello", "func same<T: ", "Eq>"}, "Eq", "Eq compares two values for equality"},
		{place{"main.snello", "struct P impl ", "Eq {"}, "Eq", "Eq compares two values for equality"},
		{place{"main.snello", "return a.", "Eq(b)"}, "Eq", "Eq compares two values for equality"},
		{place{"main.snello", "xs.", "Push(2)"}, "Push", "Push adds v at the end of the slice"},
		{place{"main.snello", "s.", "Contains"}, "Contains", "Contains reports whether the string holds t"},
		{place{"main.snello", "o.", "IsSome"}, "IsSome", "IsSome reports whether there is a value"},
		{place{"main.snello", "fmt.Println(", "len("}, "len", "len returns the length of x"},
		{place{"main.snello", "xs := []", "int{1}"}, "int", "int is the signed integer of 64 bits"},
		{place{"main.snello", "same(ref 1, ref 2), ", "true"}, "true", "true and false are the two values of bool"},
	} {
		if h := n.hover(tt.at); h == nil || !strings.Contains(h.Contents.Value, tt.doc) {
			t.Errorf("hover on %s: %+v, want its comment, %q", tt.at.at, h, tt.doc)
		}

		loc := n.definition(tt.at)
		if loc == nil || !strings.HasSuffix(filepath.ToSlash(pathOf(loc.URI)), builtinFile) {
			t.Errorf("definition of %s: %+v, want the name in the copy of %s", tt.at.at, loc, builtinFile)
			continue
		}

		text, err := os.ReadFile(pathOf(loc.URI))
		if err != nil {
			t.Fatal(err)
		}

		start, end := offsetOf(text, loc.Range.Start, protocol.PositionEncodingUTF16), offsetOf(text, loc.Range.End, protocol.PositionEncodingUTF16)
		if got := string(text[start:end]); got != tt.name {
			t.Errorf("definition of %s: at %q, want %q", tt.at.at, got, tt.name)
		}
	}

	n.shutdown()
}

// packagedFiles are the files of examples/packaged.
var packagedFiles = []string{"main.snello", "report.snello", "shapes/shape.snello", "shapes/area.snello", "shapes/shapes_test.snello"}

// A navigation is a session over examples/packaged with its files open.
type navigation struct {
	*session
	dir   string
	texts map[string][]byte
}

// navigate starts a session offering caps, and opens every file of
// examples/packaged.
func navigate(t *testing.T, caps protocol.ClientCapabilities) *navigation {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "examples", "packaged"))
	if err != nil {
		t.Fatal(err)
	}

	return navigateIn(t, caps, dir, packagedFiles...)
}

// navigateIn starts a session offering caps, and opens the files names
// of the directory dir, by their paths under it.
func navigateIn(t *testing.T, caps protocol.ClientCapabilities, dir string, names ...string) *navigation {
	t.Helper()
	n := &navigation{session: startSession(t), dir: dir, texts: map[string][]byte{}}
	n.initialize(caps)
	for _, name := range names {
		text, err := os.ReadFile(n.path(name))
		if err != nil {
			t.Fatal(err)
		}

		n.texts[name] = text
		n.notify(protocol.MethodDidOpen, protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: uriOf(n.path(name)), Version: 1, Text: string(text)}})
	}

	return n
}

// definition asks where the name at p is declared.
func (n *navigation) definition(p place) *protocol.Location {
	n.t.Helper()
	var loc *protocol.Location
	n.ask(protocol.MethodDefinition, p, &loc)
	return loc
}

// hover asks for the hover of p.
func (n *navigation) hover(p place) *protocol.Hover {
	n.t.Helper()
	var h *protocol.Hover
	n.ask(protocol.MethodHover, p, &h)
	return h
}

// ask sends the request of method about p and reads its answer into
// result.
func (n *navigation) ask(method string, p place, result any) {
	n.t.Helper()
	n.decode(n.call(method, n.params(p)), result)
}

// decode reads the answer m into result, failing on an error.
func (n *navigation) decode(m *jsonrpc.Message, result any) {
	n.t.Helper()
	if m.Error != nil {
		n.t.Fatalf("%v", m.Error)
	}

	if err := json.Unmarshal(m.Result, result); err != nil {
		n.t.Fatal(err)
	}
}

// params are the parameters of a request about p.
func (n *navigation) params(p place) protocol.TextDocumentPositionParams {
	n.t.Helper()
	return protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uriOf(n.path(p.file))},
		Position:     n.position(p),
	}
}

// location is where a definition at p is: the stretch of its token, a
// name or an import's path.
func (n *navigation) location(p place) protocol.Location {
	n.t.Helper()
	text := n.texts[p.file]
	start, end, ok := tokenAt(p.file, text, n.offset(p))
	if !ok {
		n.t.Fatalf("no token at %+v", p)
	}

	return protocol.Location{URI: uriOf(n.path(p.file)), Range: protocol.Range{
		Start: positionOf(text, start, protocol.PositionEncodingUTF16),
		End:   positionOf(text, end, protocol.PositionEncodingUTF16),
	}}
}

// position is where p is, in UTF-16.
func (n *navigation) position(p place) protocol.Position {
	n.t.Helper()
	return positionOf(n.texts[p.file], n.offset(p), protocol.PositionEncodingUTF16)
}

// offset is the offset of p in its file: the end of its lead, which its
// text begins.
func (n *navigation) offset(p place) int {
	n.t.Helper()
	text := string(n.texts[p.file])
	i := strings.Index(text, p.lead+p.at)
	if i < 0 {
		n.t.Fatalf("no %q in %s", p.lead+p.at, p.file)
	}

	return i + len(p.lead)
}

// path is the path of a file of the example.
func (n *navigation) path(name string) string {
	return filepath.Join(n.dir, filepath.FromSlash(name))
}
