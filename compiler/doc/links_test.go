// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	"reflect"
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// TestResolve resolves the forms a link takes, and refuses what names
// nothing.
func TestResolve(t *testing.T) {
	p := load(t, "strings", false)
	for name, want := range map[string]bool{
		"Builder": true, "Builder.WriteString": true, "Builder.buf": true, "NewReader": true,
		"io.Reader": true, "io.Reader.Read": true, "unicode/utf8": true, "unicode/utf8.RuneLen": true,
		"strings": true, "int": true, "Option": true,
		"Builderr": false, "Builder.Nope": false, "io.Nope": false, "nopkg.X": false, "unicode/utf8.Nope": false,
	} {
		if got := p.Resolve(name); got != want {
			t.Errorf("Resolve(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestLinksOf finds each link of a comment group at the line it is
// written on: a name linked twice at both lines, a link on a block
// comment's later line there, and nothing in code.
func TestLinksOf(t *testing.T) {
	g := &ast.CommentGroup{List: []*ast.Comment{
		{Slash: 10, Text: "// See [Builder]."},
		{Slash: 30, Text: "//"},
		{Slash: 40, Text: "//\tidx := m[Builder]"},
		{Slash: 70, Text: "//"},
		{Slash: 80, Text: "// And [Builder] once more."},
	}}
	if got, want := linksOf(g), []link{{"Builder", 10}, {"Builder", 80}}; !reflect.DeepEqual(got, want) {
		t.Errorf("line comments: linksOf = %v, want %v", got, want)
	}

	block := &ast.CommentGroup{List: []*ast.Comment{{Slash: 110, Text: "/* Then\n [Builder.Len]. */"}}}
	if got, want := linksOf(block), []link{{"Builder.Len", 110 + token.Pos(len("/* Then\n"))}}; !reflect.DeepEqual(got, want) {
		t.Errorf("a block comment: linksOf = %v, want %v", got, want)
	}
}

// TestTarget says where a link points: the package and the anchor on
// its page.
func TestTarget(t *testing.T) {
	p := load(t, "strings", false)
	for name, want := range map[string][2]string{
		"Builder":              {"strings", "Builder"},
		"Builder.WriteString":  {"strings", "Builder.WriteString"},
		"io.Reader":            {"io", "Reader"},
		"unicode/utf8.RuneLen": {"unicode/utf8", "RuneLen"},
		"unicode/utf8":         {"unicode/utf8", ""},
		"Option":               {BuiltinPath, "Option"},
	} {
		path, anchor, ok := p.Target(name)
		if !ok || path != want[0] || anchor != want[1] {
			t.Errorf("Target(%q) = %q, %q, %v, want %v", name, path, anchor, ok, want)
		}
	}
}
