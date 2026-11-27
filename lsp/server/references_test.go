// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/emit/c"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/toolchain"
	"github.com/snello-lang/snello/compiler/types"
	"github.com/snello-lang/snello/internal/testproc"
	"github.com/snello-lang/snello/lsp/jsonrpc"
	"github.com/snello-lang/snello/lsp/protocol"
)

// TestReferences finds every use of an object across the packages that
// can name it: the trait of package shapes from the program, in the
// program's two files and in the package's, apart from the program's
// own trait of the same name, which the package never names; a local
// variable in its function alone; and the declaration only when asked.
func TestReferences(t *testing.T) {
	n := navigate(t, protocol.ClientCapabilities{})
	theirs := []place{
		{"main.snello", "var theirs shapes.", "Shape"},
		{"report.snello", "report<S: shapes.", "Shape"},
		{"report.snello", "kindOf<S: shapes.", "Shape"},
		{"shapes/area.snello", "Area<S: ", "Shape"},
		{"shapes/area.snello", "Largest<A: ", "Shape"},
		{"shapes/area.snello", "B: ", "Shape"},
		{"shapes/shape.snello", "trait ", "Shape"},
		{"shapes/shape.snello", "struct Circle impl ", "Shape"},
		{"shapes/shape.snello", "struct Rect impl ", "Shape"},
	}
	mine := []place{
		{"main.snello", "trait ", "Shape {\n\tfunc (s ref Self) Corners"},
		{"main.snello", "var mine ", "Shape"},
		{"report.snello", "trait Pointed: ", "Shape"},
	}
	local := []place{
		{"main.snello", "var ", "mine Shape"},
		{"main.snello", "fmt.Println(", "mine.Corners()"},
		{"main.snello", "fmt.Println(mine.Corners(), ", "mine.Sides()"},
	}

	tests := []struct {
		name string
		from place
		decl bool
		want []place
	}{
		{"the package's trait", theirs[0], true, theirs},
		{"the package's trait, from its declaration", theirs[6], true, theirs},
		{"the program's trait", mine[1], true, mine},
		{"a local", local[2], true, local},
		{"a local, its declaration left out", local[2], false, local[1:]},
	}

	for _, tt := range tests {
		var got []protocol.Location
		n.decode(n.call(protocol.MethodReferences, protocol.ReferenceParams{TextDocumentPositionParams: n.params(tt.from), Context: protocol.ReferenceContext{IncludeDeclaration: tt.decl}}), &got)
		var want []protocol.Location
		for _, p := range tt.want {
			want = append(want, n.location(p))
		}

		slices.SortFunc(got, compareLocations)
		slices.SortFunc(want, compareLocations)
		if !slices.Equal(got, want) {
			t.Errorf("%s:\n got %+v\nwant %+v", tt.name, got, want)
		}
	}

	var none []protocol.Location
	n.decode(n.call(protocol.MethodReferences, protocol.ReferenceParams{TextDocumentPositionParams: n.params(place{"main.snello", "Corners() int ", "{"})}), &none)
	if none == nil || len(none) != 0 {
		t.Errorf("a place with no name: %+v, want an empty list", none)
	}

	n.shutdown()
}

// TestRename renames a function of package shapes across the module,
// at its declaration and its uses in the program and in the package's
// tests, and the renamed program builds and prints what it printed; it
// renames a local in its function alone; and it refuses, with why, a
// rename to a name declared beside it, in the checker's words, to a
// keyword and to no name, and a rename of a name of the standard
// library, of the language and of a package.
func TestRename(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "..", "examples", "packaged"))); err != nil {
		t.Fatal(err)
	}

	n := navigateIn(t, protocol.ClientCapabilities{TextDocument: &protocol.TextDocumentClientCapabilities{Rename: &protocol.RenameClientCapabilities{PrepareSupport: true}}}, dir, packagedFiles...)
	largest := place{"main.snello", "fmt.Println(shapes.", "Largest"}
	var prepared protocol.PrepareRenameResult
	n.decode(n.call(protocol.MethodPrepareRename, n.params(largest)), &prepared)
	if want := n.location(largest).Range; prepared.Placeholder != "Largest" || prepared.Range != want {
		t.Errorf("prepareRename: %+v, want Largest at %+v", prepared, want)
	}

	for name, tt := range map[string]struct {
		at          place
		method, new string
		why         string
	}{
		"a name beside it":            {largest, protocol.MethodRename, "Area", "Area redeclared in this block"},
		"a keyword":                   {largest, protocol.MethodRename, "func", "is a keyword"},
		"no name":                     {largest, protocol.MethodRename, "9lives", "is not a name"},
		"the standard library's name": {place{"main.snello", "fmt.", "Println(c.Name()"}, protocol.MethodPrepareRename, "", "standard library"},
		"the language's name":         {place{"main.snello", "Corners() ", "int {"}, protocol.MethodPrepareRename, "", "declared by the language"},
		"a package's name":            {place{"main.snello", "fmt.Println(", "shapes.Largest"}, protocol.MethodPrepareRename, "", "names a package"},
	} {
		var params any = n.params(tt.at)
		if tt.method == protocol.MethodRename {
			params = protocol.RenameParams{TextDocumentPositionParams: n.params(tt.at), NewName: tt.new}
		}

		if m := n.call(tt.method, params); m.Error == nil || m.Error.Code != jsonrpc.RequestFailed || !strings.Contains(m.Error.Message, tt.why) {
			t.Errorf("%s: %+v, want a refusal saying %q", name, m.Error, tt.why)
		}
	}

	local := n.rename(place{"main.snello", "fmt.Println(", "mine.Corners()"}, "ours")
	if len(local.Changes) != 1 || len(local.Changes[uriOf(n.path("main.snello"))]) != 3 {
		t.Errorf("a local: %+v, want three edits of main.snello", local.Changes)
	}

	edit := n.rename(largest, "Biggest")
	for _, file := range []string{"main.snello", "shapes/area.snello", "shapes/shapes_test.snello"} {
		uri := uriOf(n.path(file))
		edits := edit.Changes[uri]
		if len(edits) != 1 {
			t.Errorf("%s: %+v, want one edit", file, edits)
			continue
		}

		renamed := applyEdits(t, n.texts[file], edits)
		if err := os.WriteFile(n.path(file), renamed, fileMode); err != nil {
			t.Fatal(err)
		}
	}

	if len(edit.Changes) != 3 {
		t.Errorf("edits of %d files, want 3", len(edit.Changes))
	}

	n.shutdown()

	want, err := os.ReadFile(filepath.Join(dir, "packaged.out"))
	if err != nil {
		t.Fatal(err)
	}

	if got := buildAndRun(t, dir); string(got) != string(want) {
		t.Errorf("the renamed program prints:\n%s\nwant:\n%s", got, want)
	}
}

// TestRenameRefusals renames a method with its whole dispatch, from any
// method of it: a trait's default with the methods that override it, a
// trait's method with the type's that implements it; and refuses a
// rename that would compile and change what the program does, or that
// cannot know every use: a method whose dispatch holds a trait's
// method the language declares, a name renamed into the scope of
// another of the new name, which would capture its uses, and a rename
// of a name a package with a file that does not parse uses, whose uses
// are not known.
func TestRenameRefusals(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"main.snello": `package main

import "fmt"

trait Shape {
	func (s ref Self) Area() f64
	func (s ref Self) Describe() string { return "a shape" }
}

trait Logged: Shape {
	func (s ref Self) Describe() string { return "[logged] " + super.Describe() }
}

struct Rect impl Logged { w f64 }

func (r ref Rect) Area() f64 { return r.w }

func (r ref Rect) Describe() string { return "a rectangle" }

struct Free { n int }

func (f ref Free) Area() f64 { return 1.0 }

struct P impl Clone { n int }

func (p ref P) Clone() P { return P{n: p.n} }

func main() {
	y := 10
	if y > 0 {
		x := 2
		fmt.Println(x + y)
	}

	var s Shape = Rect{w: 2.0}
	fmt.Println(s.Describe(), s.Area(), Free{n: 1}.Area())
}
`,
		"lib/lib.snello":    "package lib\n\nfunc Foo() int { return 1 }\n",
		"app/app.snello":    "package main\n\nimport \"lib\"\n\nfunc main() { lib.Foo() }\n",
		"app/broken.snello": "package main\n\nfunc wip() int { return lib.Foo( }\n",
	})

	n := navigateIn(t, protocol.ClientCapabilities{}, dir, "main.snello", "lib/lib.snello")
	for name, tt := range map[string]struct {
		at          place
		new, refuse string
	}{
		"a method of the language's trait": {place{"main.snello", "func (p ref P) ", "Clone()"}, "Duplicate", "it shares its dispatch with Clone.Clone, which is declared by the language"},
		"a package that does not parse":    {place{"lib/lib.snello", "func ", "Foo"}, "Baz", "app/broken.snello does not check as it stands"},
	} {
		if m := n.call(protocol.MethodRename, protocol.RenameParams{TextDocumentPositionParams: n.params(tt.at), NewName: tt.new}); m.Error == nil || !strings.Contains(m.Error.Message, tt.refuse) {
			t.Errorf("%s: %+v, want a refusal saying %q", name, m.Error, tt.refuse)
		}
	}

	if m := n.call(protocol.MethodRename, protocol.RenameParams{TextDocumentPositionParams: n.params(place{"main.snello", "\t\t", "x := 2"}), NewName: "y"}); m.Error == nil || !strings.Contains(m.Error.Message, "would refer to another declaration") {
		t.Errorf("a capture: %+v, want a refusal", m.Error)
	}

	if edit := n.rename(place{"main.snello", "func (f ref Free) ", "Area()"}, "Size"); len(edit.Changes[uriOf(n.path("main.snello"))]) != 2 {
		t.Errorf("a method of a type that implements no trait: %+v, want its declaration and its call", edit.Changes)
	}

	for name, tt := range map[string]struct {
		at    place
		edits int
	}{
		"a default and its overrides, from the default":          {place{"main.snello", "func (s ref Self) ", "Describe() string { return \"a shape\""}, 5},
		"a default and its overrides, from an override":          {place{"main.snello", "func (r ref Rect) ", "Describe()"}, 5},
		"a trait's method and its implementation, from the type": {place{"main.snello", "func (r ref Rect) ", "Area()"}, 3},
	} {
		edit := n.rename(tt.at, "Renamed")
		if got := len(edit.Changes[uriOf(n.path("main.snello"))]); got != tt.edits {
			t.Errorf("%s: %d edits, want %d: every method of the dispatch, and every call", name, got, tt.edits)
		}
	}

	n.shutdown()
}

// TestRenameOverride refuses a rename that would make a method share a
// dispatch it did not, and so override where it did not: a type's
// method renamed to the name of its trait's default, and the default
// renamed to the name of the type's method, in one package and across
// two, where the package of the type never spells the old name; and
// renames a type's method with its trait's of another package.
func TestRenameOverride(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"main.snello": `package main

import ("fmt"; "lib")

trait Shape {
	func (s ref Self) Describe() string { return "default" }
}

struct Circle impl Shape { r f64 }

func (c ref Circle) Explain() string { return "circle" }

struct Mine impl lib.Shaped { n int }

func (m ref Mine) Explain() string { return "mine" }

func (m ref Mine) Size() int { return m.n }

func main() {
	var s Shape = Circle{r: 1.0}
	var o lib.Shaped = Mine{n: 1}
	fmt.Println(s.Describe(), o.Describe(), o.Size())
}
`,
		"lib/lib.snello": "package lib\n\ntrait Shaped {\n\tfunc (s ref Self) Describe() string { return \"default\" }\n\tfunc (s ref Self) Size() int\n}\n",
	})

	n := navigateIn(t, protocol.ClientCapabilities{}, dir, "main.snello", "lib/lib.snello")
	for name, tt := range map[string]struct {
		at      place
		new     string
		partner string
	}{
		"a method onto its trait's default": {place{"main.snello", "func (c ref Circle) ", "Explain"}, "Describe", "Shape.Describe"},
		"a default onto a type's method":    {place{"main.snello", "func (s ref Self) ", "Describe"}, "Explain", "Circle.Explain"},
		"a default of another package's":    {place{"lib/lib.snello", "func (s ref Self) ", "Describe"}, "Explain", "Mine.Explain"},
	} {
		if m := n.call(protocol.MethodRename, protocol.RenameParams{TextDocumentPositionParams: n.params(tt.at), NewName: tt.new}); m.Error == nil || !strings.Contains(m.Error.Message, "share a dispatch with "+tt.partner) {
			t.Errorf("%s: %+v, want a refusal naming %s", name, m.Error, tt.partner)
		}
	}

	edit := n.rename(place{"main.snello", "func (m ref Mine) ", "Size()"}, "Count")
	if got, lib := len(edit.Changes[uriOf(n.path("main.snello"))]), len(edit.Changes[uriOf(n.path("lib/lib.snello"))]); got != 2 || lib != 1 {
		t.Errorf("a type's method with its trait's of another package: %d edits of main.snello and %d of lib.snello, want 2 and 1", got, lib)
	}

	n.shutdown()
}

// TestRenameGeneric renames a method of a generic trait with the
// methods of the types that implement an instance of it, from either
// side, and refuses one that implements an instance of a trait of the
// standard library, naming its method.
func TestRenameGeneric(t *testing.T) {
	dir := writeModule(t, map[string]string{"main.snello": `package main

import ("fmt"; "iter")

trait Box<T> {
	func (b ref Self) Get() T
}

struct IntBox impl Box<int> { n int }

func (b ref IntBox) Get() int { return b.n }

struct Count impl iter.Iterator<int> { n int }

func (c mut Count) Next() int? {
	if c.n == 0 {
		return None
	}

	c.n -= 1
	return Some(c.n)
}

func main() {
	var b Box<int> = IntBox{n: 1}
	fmt.Println(b.Get())
}
`})

	n := navigateIn(t, protocol.ClientCapabilities{}, dir, "main.snello")
	for name, at := range map[string]place{
		"from the trait's method": {"main.snello", "func (b ref Self) ", "Get"},
		"from the type's method":  {"main.snello", "func (b ref IntBox) ", "Get"},
	} {
		if got := len(n.rename(at, "Fetch").Changes[uriOf(n.path("main.snello"))]); got != 3 {
			t.Errorf("%s: %d edits, want the trait's method, the type's and the call", name, got)
		}
	}

	m := n.call(protocol.MethodRename, protocol.RenameParams{TextDocumentPositionParams: n.params(place{"main.snello", "func (c mut Count) ", "Next"}), NewName: "Advance"})
	if m.Error == nil || !strings.Contains(m.Error.Message, "shares its dispatch with Iterator.Next, which is declared by the standard library") {
		t.Errorf("a method of a standard trait's instance: %+v", m.Error)
	}

	n.shutdown()
}

// TestRenameBroken refuses a rename where a package that does not
// compile leaves a use of the name with no object, and renames beside
// an error of before that names the old name of another declaration,
// which the rename leaves as it was.
func TestRenameBroken(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"lib/lib.snello":     "package lib\n\nfunc Foo() int { return 1 }\n\nvar x int = \"s\"\n",
		"app/app.snello":     "package main\n\nimport \"lib\"\n\nfunc main() { lib.Foo() }\n",
		"shape/shape.snello": "package shape\n\ntrait Shape {\n\tfunc (s ref Self) Describe() string\n}\n\nstruct Color impl Shape { n int }\n\nfunc (c ref Color) Describe() int { return 1 }\n",
		"user/user.snello":   "package main\n\nimport \"shape\"\n\nstruct Mine { n int }\n\nfunc (m ref Mine) Describe() int { return m.n }\n\nfunc main() { _ = Mine{n: 1}.Describe() }\n\nfunc use(s ref shape.Shape) {}\n",
	})

	n := navigateIn(t, protocol.ClientCapabilities{}, dir, "lib/lib.snello", "user/user.snello")
	if m := n.call(protocol.MethodRename, protocol.RenameParams{TextDocumentPositionParams: n.params(place{"lib/lib.snello", "func ", "Foo"}), NewName: "Bar"}); m.Error == nil || !strings.Contains(m.Error.Message, "app/app.snello") {
		t.Errorf("a use a package that does not compile leaves with no object: %+v, want a refusal naming app/app.snello", m.Error)
	}

	if edit := n.rename(place{"user/user.snello", "func (m ref Mine) ", "Describe"}, "Explain"); len(edit.Changes[uriOf(n.path("user/user.snello"))]) != 2 {
		t.Errorf("beside an error of before about another Describe: %+v, want two edits", edit.Changes)
	}

	n.shutdown()
}

// TestRenameScope renames what the editor edits and nothing it does
// not: a local of a program in no module; a name used by files for
// other platforms, each renamed too; a name of a module required as a
// directory inside the module, whose uses in the module are found
// through the importer's resolution; with an error of before, which
// holds the old name inside another word, left as it was; and as
// documents' edits with their versions for an editor that takes them.
func TestRenameScope(t *testing.T) {
	alone := t.TempDir()
	if err := os.WriteFile(filepath.Join(alone, "main.snello"), []byte("package main\n\nimport \"fmt\"\n\nfunc main() {\n\tsum := 1\n\tfmt.Println(sum)\n}\n"), fileMode); err != nil {
		t.Fatal(err)
	}

	n := navigateIn(t, protocol.ClientCapabilities{}, alone, "main.snello")
	if edit := n.rename(place{"main.snello", "\t", "sum := 1"}, "total"); len(edit.Changes[uriOf(n.path("main.snello"))]) != 2 {
		t.Errorf("a program in no module: %+v, want two edits", edit.Changes)
	}

	n.shutdown()

	dir := writeModule(t, map[string]string{
		"main.snello":            "package main\n\nimport (\"fmt\"; \"lib\"; \"vend\")\n\nfunc main() {\n\tfmt.Println(lib.Foo(), vend.Hello())\n}\n",
		"broken.snello":          "package main\n\nfunc typed() int {\n\ty := 1\n\treturn \"y\"\n}\n",
		"lib/lib.snello":         "package lib\n\nfunc Foo() int { return 1 }\n",
		"lib/use_darwin.snello":  "package lib\n\nfunc mine() int { return Foo() }\n",
		"lib/use_linux.snello":   "package lib\n\nfunc mine() int { return Foo() }\n",
		"lib/use_windows.snello": "package lib\n\nfunc mine() int { return Foo() }\n",
		"third/vend/snello.mod":  "snello 0.1.0\n",
		"third/vend/vend.snello": "package vend\n\nfunc Hello() int { return 2 }\n",
		"lib/inner/inner.snello": "package inner\n",
	})

	if err := os.WriteFile(filepath.Join(dir, importer.ModFile), []byte("snello 0.1.0\nrequire vend ./third/vend\n"), fileMode); err != nil {
		t.Fatal(err)
	}

	caps := protocol.ClientCapabilities{Workspace: &protocol.WorkspaceClientCapabilities{WorkspaceEdit: &protocol.WorkspaceEditClientCapabilities{DocumentChanges: true}}}
	n = navigateIn(t, caps, dir, "main.snello", "broken.snello", "third/vend/vend.snello")
	var refs []protocol.Location
	n.decode(n.call(protocol.MethodReferences, protocol.ReferenceParams{TextDocumentPositionParams: n.params(place{"third/vend/vend.snello", "func ", "Hello"})}), &refs)
	if len(refs) != 1 || refs[0].URI != uriOf(n.path("main.snello")) {
		t.Errorf("a required directory's name: %+v, want its use in main.snello", refs)
	}

	edit := n.rename(place{"main.snello", "fmt.Println(lib.", "Foo()"}, "Fooed")
	var files []string
	for _, change := range edit.DocumentChanges {
		files = append(files, filepath.Base(pathOf(change.TextDocument.URI)))
		if open := change.TextDocument.URI == uriOf(n.path("main.snello")); open != (change.TextDocument.Version != nil) {
			t.Errorf("%s: version %v, want one for an open document alone", change.TextDocument.URI, change.TextDocument.Version)
		}
	}

	slices.Sort(files)
	if want := []string{"lib.snello", "main.snello", "use_darwin.snello", "use_linux.snello", "use_windows.snello"}; !slices.Equal(files, want) || edit.Changes != nil {
		t.Errorf("a name used for every platform: %q, want %q as documents' edits", files, want)
	}

	if m := n.call(protocol.MethodRename, protocol.RenameParams{TextDocumentPositionParams: n.params(place{"broken.snello", "\t", "y := 1"}), NewName: "z"}); m.Error != nil {
		t.Errorf("a rename beside an error that holds the name in a word: %v", m.Error)
	}

	n.shutdown()
}

// TestReferencesFromStd finds the uses of a name of the standard
// library from the copy of its file a definition opened, in the
// workspace's modules, its declaration in the copy among them.
func TestReferencesFromStd(t *testing.T) {
	t.Setenv(c.CacheEnv, t.TempDir())
	n := navigate(t, protocol.ClientCapabilities{})
	loc := n.definition(place{"main.snello", "fmt.", "Println(c.Name()"})
	if loc == nil {
		t.Fatal("no definition in fmt")
	}

	text, err := os.ReadFile(pathOf(loc.URI))
	if err != nil {
		t.Fatal(err)
	}

	n.notify(protocol.MethodDidOpen, protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: loc.URI, Version: 1, Text: string(text)}})
	var refs []protocol.Location
	params := protocol.ReferenceParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: loc.URI}, Position: loc.Range.Start}, Context: protocol.ReferenceContext{IncludeDeclaration: true}}
	n.decode(n.call(protocol.MethodReferences, params), &refs)
	inMain, declared := 0, false
	for _, r := range refs {
		inMain += boolInt(r.URI == uriOf(n.path("main.snello")))
		declared = declared || r == *loc
	}

	if inMain != strings.Count(string(n.texts["main.snello"]), "fmt.Println(") || !declared {
		t.Errorf("Println from its copy: %d in main.snello, declared %v, of %+v", inMain, declared, refs)
	}

	n.shutdown()
}

// TestReferencesInStdCheckout asks, from a file of a standard package
// of the checkout, for the references of a name another package of the
// library exports: they reach its declaration and the packages of the
// library that use it, and a rename of it is refused, since the
// programs outside the checkout use it through the compiler's own copy.
func TestReferencesInStdCheckout(t *testing.T) {
	checkout, err := filepath.Abs(stdCheckoutDir)
	if err != nil {
		t.Fatal(err)
	}

	n := navigateIn(t, protocol.ClientCapabilities{}, checkout, "deque/deque.snello")
	at := place{"deque/deque.snello", "fmt.", "Sprint(v)"}
	var refs []protocol.Location
	n.decode(n.call(protocol.MethodReferences, protocol.ReferenceParams{TextDocumentPositionParams: n.params(at), Context: protocol.ReferenceContext{IncludeDeclaration: true}}), &refs)
	packages := map[string]bool{}
	for _, r := range refs {
		packages[filepath.Base(filepath.Dir(pathOf(r.URI)))] = true
	}

	for _, want := range []string{"fmt", "deque", "set"} {
		if !packages[want] {
			t.Errorf("Sprint's references reach no file of %s: %+v", want, refs)
		}
	}

	if m := n.call(protocol.MethodRename, protocol.RenameParams{TextDocumentPositionParams: n.params(at), NewName: "Sprint2"}); m.Error == nil || !strings.Contains(m.Error.Message, "Sprint is exported by the standard library") {
		t.Errorf("a rename of Sprint from the checkout: %+v, want a refusal", m.Error)
	}

	n.shutdown()
}

// TestNames reads a file's imports from its tokens, finds a name among
// its tokens, renames a whole word of a message and no part of one,
// tells a name from a keyword and from no name, and checks a package
// for every platform its files differ for.
func TestNames(t *testing.T) {
	src := []byte("package main\n\nimport (\"fmt\"; j \"encoding/json\")\nimport \"lib\"\n\nfunc main() { fmt.Println(\"lib\") }\n")
	if got := importsOf("a.snello", src); !slices.Equal(got, []string{"fmt", "encoding/json", "lib"}) {
		t.Errorf("importsOf = %q", got)
	}

	if !hasName("a.snello", src, "Println") || hasName("a.snello", src, "lib") || hasName("a.snello", src, "Print") {
		t.Error("hasName finds a string, or a part of a name, or misses a name")
	}

	if got := renameWord("cannot use y of type y_y as y", "y", "z"); got != "cannot use z of type y_y as z" {
		t.Errorf("renameWord = %q", got)
	}

	for name, want := range map[string]bool{"total": true, "_x9": true, "é": true, "9x": false, "a b": false, "a.b": false, "": false, "func": false} {
		if got := isIdentifier(name) && token.Lookup(name) == token.IDENT; got != want {
			t.Errorf("%q a name: %v, want %v", name, got, want)
		}
	}

	dir := t.TempDir()
	for _, name := range []string{"lib.snello", "poll_linux.snello", "poll_windows.snello"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package lib\n"), fileMode); err != nil {
			t.Fatal(err)
		}
	}

	if targets := targetsOf(dir); targets[0] != platform.Host() || len(targets) != 3 {
		t.Errorf("targetsOf: %v, want the host and one platform for each other set of files", targets)
	}
}

// boolInt is 1 for true and 0 for false.
func boolInt(b bool) int {
	if b {
		return 1
	}

	return 0
}

// BenchmarkReferences finds the uses of a function of the bottom of a
// chain of ten packages, each importing the one below and every one
// using the function, eight files of forty functions each, as
// references does, and checks a rename of it as rename does.
func BenchmarkReferences(b *testing.B) {
	const depth, width, funcs = 10, 8, 40
	files := map[string]string{"main.snello": "package main\n\nimport \"p9\"\n\nfunc main() { p9.F0_0(1) }\n"}
	for p := range depth {
		for f := range width {
			var src strings.Builder
			fmt.Fprintf(&src, "package p%d\n\n", p)
			if p > 0 {
				fmt.Fprintf(&src, "import \"p0\"\n\n")
			}

			for g := range funcs {
				body := "x + " + strconv.Itoa(g)
				if p > 0 && g == 0 {
					body = "p0.Base(x)"
				}

				fmt.Fprintf(&src, "func F%d_%d(x int) int { return %s }\n\n", f, g, body)
			}

			if p == 0 && f == 0 {
				src.WriteString("func Base(x int) int { return x }\n")
			}

			if p > 0 && f == 0 {
				fmt.Fprintf(&src, "func Up(x int) int { return p%d.F0_1(x) }\n", p-1)
			}

			files[fmt.Sprintf("p%d/f%d.snello", p, f)] = src.String()
		}
	}

	dir := writeModule(b, files)
	path := filepath.Join(dir, "p0", "f0.snello")
	text, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}

	s := New(nil, io.Discard, io.Discard)
	s.open(protocol.TextDocumentItem{URI: uriOf(path), Version: 1, Text: string(text)})
	s.checkAll()
	v := s.views[path]
	t := v.at(v.byPath[path], text, strings.Index(string(text), "func Base")+len("func "))
	if t == nil || t.obj == nil {
		b.Fatal("no Base")
	}

	b.Run("references", func(b *testing.B) {
		for b.Loop() {
			if found := s.search(v, t.obj, path, ""); len(found.uses) != (depth-1)*width+1 { // the first function of each file above the bottom, and the declaration
				b.Fatalf("%d uses, want %d", len(found.uses), (depth-1)*width+1)
			}
		}
	})

	b.Run("rename", func(b *testing.B) {
		for b.Loop() {
			found := s.search(v, t.obj, path, "Root")
			if why := s.refused(found, s.renamed(found, "Root"), "Root"); why != "" {
				b.Fatal(why)
			}
		}
	})
}

// rename asks for the edits that rename the name at p to name.
func (n *navigation) rename(p place, name string) protocol.WorkspaceEdit {
	n.t.Helper()
	var edit protocol.WorkspaceEdit
	n.decode(n.call(protocol.MethodRename, protocol.RenameParams{TextDocumentPositionParams: n.params(p), NewName: name}), &edit)
	return edit
}

// applyEdits applies the edits of one file to its text, the last first,
// as an editor applies a list of them at once.
func applyEdits(t *testing.T, text []byte, edits []protocol.TextEdit) []byte {
	t.Helper()
	slices.SortFunc(edits, func(a, b protocol.TextEdit) int {
		return cmp.Or(cmp.Compare(b.Range.Start.Line, a.Range.Start.Line), cmp.Compare(b.Range.Start.Character, a.Range.Start.Character))
	})

	for _, e := range edits {
		start, end := offsetOf(text, e.Range.Start, protocol.PositionEncodingUTF16), offsetOf(text, e.Range.End, protocol.PositionEncodingUTF16)
		text = slices.Concat(text[:start], []byte(e.NewText), text[end:])
	}

	return text
}

// buildAndRun builds the program in dir through the C backend and
// returns what it prints; a machine without a C compiler skips it.
func buildAndRun(t *testing.T, dir string) []byte {
	t.Helper()
	names, err := importer.SourceFiles(dir, platform.Host())
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{}
	imp, prog, err := importer.LoadProgram(names, platform.Host(), info)
	if err != nil {
		t.Fatalf("the renamed program: %v", err)
	}

	var libs []lower.Library
	for _, lib := range imp.Packages() {
		libs = append(libs, lower.Library{Path: lib.Path, File: lib.File, Syntax: lib.Syntax, CFiles: lib.CFiles, Links: lib.Links, Std: lib.Std})
	}

	src, err := c.EmitFiles(prog.Files, prog.Syntax, info, libs...)
	if err != nil {
		t.Fatal(err)
	}

	bin, log, err := c.Build(t.TempDir(), "renamed", src, platform.Host(), false, lower.Bindings(info, libs)...)
	if missing := (*toolchain.MissingError)(nil); errors.As(err, &missing) {
		t.Skipf("no C compiler: %v", err)
	}

	if err != nil {
		t.Fatalf("%v\n%s", err, log)
	}

	out, err := testproc.Command(bin).Output()
	if err != nil {
		t.Fatal(err)
	}

	return out
}

// compareLocations orders locations by document and then by place.
func compareLocations(a, b protocol.Location) int {
	return cmp.Or(
		strings.Compare(string(a.URI), string(b.URI)),
		cmp.Compare(a.Range.Start.Line, b.Range.Start.Line),
		cmp.Compare(a.Range.Start.Character, b.Range.Start.Character),
	)
}
