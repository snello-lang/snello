// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/snello-lang/snello/lsp/protocol"
)

// TestCompletion completes, in examples/packaged, the members after a
// dot: a struct's fields and its type's methods after a value of
// another package's type, and a package's exported names after its
// name; the names on a bare name, the locals of the blocks around it,
// the parameters, the package's names and the imports, and none
// declared further on or in another block; the import paths in an
// import's string; keywords; and nothing in a string or a comment. The
// list is the same for the same place every time.
func TestCompletion(t *testing.T) {
	n := navigate(t, protocol.ClientCapabilities{})
	main := string(n.texts["main.snello"])
	tests := []struct {
		name         string
		edit, at     string // the text written after r := shapes.Rect{W: 2, H: 3}, and what the cursor is after in it
		want, refuse []string
	}{
		{"after a value's dot", "\n\tr.", "\tr.", []string{"W", "H", "Name", "Area"}, []string{"Radius", "Describe"}},
		{"after a package's dot", "\n\tshapes.", "\tshapes.", []string{"Area", "Circle", "Describe", "Kind", "Largest", "Rect", "Round", "Shape", "Square"}, []string{"Name"}},
		{"on a bare name", "\n\tlater := 1\n\t{\n\t\tblock := 2\n\t\t_ = block\n\t}\n\tk", "\n\tk", []string{"c", "r", "later", "shapes", "fmt", "kindOf", "report", "Triangle", "main", "len", "int", "for", "return"}, []string{"block"}},
		{"before a later local", "\n\tk\n\tlater := 1\n\t_ = later", "\n\tk", []string{"c", "r"}, []string{"later"}},
		{"in a string", "\n\tfmt.Println(\"r.", "\"r.", nil, []string{"W", "c"}},
		{"in a comment", "\n\t// r.", "// r.", nil, []string{"W", "c"}},
	}

	anchor := "r := shapes.Rect{W: 2, H: 3}"
	for i, tt := range tests {
		text := strings.Replace(main, anchor, anchor+tt.edit, 1)
		got := n.complete("main.snello", text, int32(i+2), strings.Index(text, anchor)+len(anchor)+strings.Index(tt.edit, tt.at)+len(tt.at))
		for _, w := range tt.want {
			if !slices.Contains(got, w) {
				t.Errorf("%s: no %s in %q", tt.name, w, got)
			}
		}

		for _, r := range tt.refuse {
			if slices.Contains(got, r) {
				t.Errorf("%s: %s in %q", tt.name, r, got)
			}
		}

		if tt.want == nil && len(got) != 0 {
			t.Errorf("%s: %q, want nothing", tt.name, got)
		}

		if again := n.complete("main.snello", text, int32(i+100), strings.Index(text, anchor)+len(anchor)+strings.Index(tt.edit, tt.at)+len(tt.at)); !slices.Equal(got, again) {
			t.Errorf("%s: another list the second time:\n%q\n%q", tt.name, got, again)
		}
	}

	paths := strings.Replace(main, `import ("fmt"; "shapes")`, `import ("fmt"; "shapes"; "str")`, 1)
	got := n.complete("main.snello", paths, 200, strings.Index(paths, `"str`)+len(`"str`))
	if !slices.Contains(got, "strings") || !slices.Contains(got, "strconv") || slices.Contains(got, "fmt") {
		t.Errorf("in an import's path begun str: %q, want the standard packages that begin with it", got)
	}

	got = n.complete("main.snello", strings.Replace(main, `import ("fmt"; "shapes")`, `import ("fmt"; "shapes"; "")`, 1), 201, strings.Index(main, `"shapes")`)+len(`"shapes"; "`))
	if !slices.Contains(got, "shapes") || !slices.Contains(got, "fmt") || slices.Contains(got, "builtin") {
		t.Errorf("in an import's empty path: %q, want the module's packages and the standard library's but builtin", got)
	}

	n.shutdown()
}

// TestCompletionBound completes the members of a value of a type
// parameter, from its bound, on the traits example.
func TestCompletionBound(t *testing.T) {
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("..", "..", "examples", "traits", "traits.snello"))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "traits.snello"), src, fileMode); err != nil {
		t.Fatal(err)
	}

	n := navigateIn(t, protocol.ClientCapabilities{}, dir, "traits.snello")
	text := strings.Replace(string(src), "sum += x.Area()", "sum += x.", 1)
	got := n.complete("traits.snello", text, 2, strings.Index(text, "sum += x.")+len("sum += x."))
	for _, w := range []string{"Area", "Perimeter", "Name", "Describe"} {
		if !slices.Contains(got, w) {
			t.Errorf("no %s among the members of a T bound by Shape: %q", w, got)
		}
	}

	n.shutdown()
}

// TestCompletionCases completes, on a module of its own, each place the
// first review of completion found wrong, holding each to its answer:
// a line typed half way, the methods the language predeclares, a type
// written with its package, a function of another package's type and of
// a generic one's instance, a type parameter's value and its name, the
// checker's own rule of what another package's type opens, a struct
// literal's key, a declaration's name, a number and a char literal, no
// keyword the parser refuses, no Drop, which runs on its own, and only
// types and packages where a type is written; and an empty answer is a
// list.
func TestCompletionCases(t *testing.T) {
	const lib = `package lib

trait Tagged {
	var tag int
	func (t ref Self) Shown() int { return 1 }
}

trait Maker {
	func New() Self
	func (s ref Self) Made() int
}

struct Point { X int; Y int }

func Point.Origin() Point { return Point{X: 0, Y: 0} }

struct Box<T> { v T }

func Box<T>.Of(v T) Box<T> { return Box<T>{v: v} }
`
	const main = `package main

import "lib"

struct Mine impl lib.Tagged { n int }

struct Held impl Drop { n int }

func (h mut Held) Drop() {}

func use<T: lib.Maker>(x ref T) int { return x.Made() }

func main() {
	r := lib.Point{X: 1, Y: 2}
	xs := []int{1}
	s := "text"
	o := xs.Pop()
	m := Mine{n: 2}
	_ = r.X + xs.Len() + s.Len() + m.n + o.UnwrapOr(0)
	HERE
}
`
	dir := writeModule(t, map[string]string{"lib/lib.snello": lib, "main.snello": strings.Replace(main, "HERE", "", 1)})
	n := navigateIn(t, protocol.ClientCapabilities{}, dir, "main.snello")
	tests := []struct {
		name, edit, at string // what is written at HERE, and what the cursor is after in it
		want, refuse   []string
	}{
		{"an if typed half way", "if r", "if r", []string{"r", "xs", "lib", "main"}, nil},
		{"a call typed half way", "print(r", "print(r", []string{"r", "xs"}, nil},
		{"a dot in an if typed half way", "if r.", "if r.", []string{"X", "Y"}, nil},
		{"a slice's predeclared methods", "xs.", "xs.", []string{"Push", "Pop", "Contains"}, nil},
		{"a string's", "s.", "s.", []string{"Len", "ToUpper"}, nil},
		{"an option's", "o.", "o.", []string{"Unwrap", "UnwrapOr", "IsSome"}, nil},
		{"a type with its package", "var p lib.", "var p lib.", []string{"Point", "Box", "Tagged"}, []string{"Origin"}},
		{"a function of another package's type", "p := lib.Point.", "lib.Point.", []string{"Origin"}, []string{"X", "Y"}},
		{"a function of a generic type's instance", "b := lib.Box<int>.", "lib.Box<int>.", []string{"Of"}, nil},
		{"a field another package's trait gives", "m.", "m.", []string{"n", "Shown"}, []string{"tag"}},
		{"a struct literal's key", "q := lib.Point{X: 1, Y", "X: 1, Y", []string{"X", "Y", "r"}, nil},
		{"a declaration's name", "fresh := 1", "fresh", nil, []string{"r"}},
		{"a number", "k := 12", "12", nil, []string{"r"}},
		{"a char literal", "c := 'a", "'a", nil, []string{"r"}},
		{"no refused keyword", "f", "\n\tf", []string{"for", "match"}, []string{"switch"}},
		{"no Drop by hand", "h := Held{n: 1}\n\th.", "h.", []string{"n"}, []string{"Drop"}},
		{"a name where a type is written", "var w Mi", "var w Mi", []string{"Mine", "Held", "lib", "int"}, []string{"r", "xs", "main"}},
	}

	for i, tt := range tests {
		text := strings.Replace(main, "HERE", tt.edit, 1)
		at := strings.Index(text, "\n\t"+tt.edit) + 2 + strings.Index(tt.edit, strings.TrimPrefix(tt.at, "\n\t")) + len(strings.TrimPrefix(tt.at, "\n\t"))
		got := n.complete("main.snello", text, int32(i+2), at)
		for _, w := range tt.want {
			if !slices.Contains(got, w) {
				t.Errorf("%s: no %s in %q", tt.name, w, got)
			}
		}

		for _, r := range tt.refuse {
			if slices.Contains(got, r) {
				t.Errorf("%s: %s in %q", tt.name, r, got)
			}
		}

		if tt.want == nil && len(got) != 0 {
			t.Errorf("%s: %q, want nothing", tt.name, got)
		}
	}

	for name, tt := range map[string]struct {
		edit         string
		want, refuse string
	}{
		"a type parameter's value": {"return x.", "Made", "New"},
		"a type parameter's name":  {"return T.", "New", "Made"},
	} {
		text := strings.Replace(strings.Replace(main, "HERE", "", 1), "return x.Made()", tt.edit, 1)
		got := n.complete("main.snello", text, 50, strings.Index(text, tt.edit)+len(tt.edit))
		if !slices.Contains(got, tt.want) || slices.Contains(got, tt.refuse) {
			t.Errorf("%s: %q, want %s and no %s", name, got, tt.want, tt.refuse)
		}
	}

	text := strings.Replace(main, "HERE", "// r.", 1)
	n.change(uriOf(n.path("main.snello")), 99, text)
	m := n.call(protocol.MethodCompletion, protocol.CompletionParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uriOf(n.path("main.snello"))},
		Position:     positionOf([]byte(text), strings.Index(text, "// r.")+len("// r."), protocol.PositionEncodingUTF16),
	}})
	if !strings.Contains(string(m.Result), `"items":[]`) {
		t.Errorf("an empty answer: %s, want an empty list of items", m.Result)
	}

	n.shutdown()
}

// complete writes text as the document file's, at version, and asks
// for the completion at offset, returning the items' labels in order.
func (n *navigation) complete(file, text string, version int32, offset int) []string {
	n.t.Helper()
	n.change(uriOf(n.path(file)), version, text)
	var list protocol.CompletionList
	pos := positionOf([]byte(text), offset, protocol.PositionEncodingUTF16)
	n.decode(n.call(protocol.MethodCompletion, protocol.CompletionParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: uriOf(n.path(file))}, Position: pos}}), &list)
	var labels []string
	for i, item := range list.Items {
		labels = append(labels, item.Label)
		if want := fmt.Sprintf("%05d", i); item.SortText != want {
			n.t.Errorf("item %d sorts as %q, want %q", i, item.SortText, want)
		}
	}

	return labels
}
