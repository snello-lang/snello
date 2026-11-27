// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ast

import (
	"testing"

	"github.com/snello-lang/snello/compiler/token"
)

func TestCommentGroupText(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  string
	}{
		{"line comments", []string{"// A Builder builds.", "// Its zero value works."}, "A Builder builds.\nIts zero value works.\n"},
		{"a paragraph break", []string{"// One.", "//", "// Two."}, "One.\n\nTwo.\n"},
		{"blank lines at the ends and a run of them", []string{"//", "// One.", "//", "//", "// Two.", "//"}, "One.\n\nTwo.\n"},
		{"code keeps its tab", []string{"// Use it:", "//", "//\tb.WriteString(s)"}, "Use it:\n\n\tb.WriteString(s)\n"},
		{"a block comment keeps its space", []string{"/* One.\n   Two. */"}, " One.\n   Two.\n"},
		{"trailing space", []string{"// One.  "}, "One.\n"},
		{"only one space after the marker goes", []string{"//  indented"}, " indented\n"},
	}

	for _, tt := range tests {
		g := &CommentGroup{}
		for _, line := range tt.lines {
			g.List = append(g.List, &Comment{Text: line})
		}

		if got := g.Text(); got != tt.want {
			t.Errorf("%s: Text() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestCommentGroupLines gives each line as Text reads it, blank lines
// kept, and where it begins: a line comment at its /, a block
// comment's first line at its / and each later line at its own start.
func TestCommentGroupLines(t *testing.T) {
	g := &CommentGroup{List: []*Comment{
		{Slash: 10, Text: "// One."},
		{Slash: 20, Text: "//"},
		{Slash: 30, Text: "/* Two.\n   Three. */"},
	}}
	lines, at := g.Lines()
	wantLines := []string{"One.", "", " Two.", "   Three."}
	wantAt := []token.Pos{10, 20, 30, 30 + token.Pos(len("/* Two.\n"))}
	if len(lines) != len(wantLines) || len(at) != len(wantAt) {
		t.Fatalf("Lines() = %q at %v, want %q at %v", lines, at, wantLines, wantAt)
	}

	for i := range wantLines {
		if lines[i] != wantLines[i] || at[i] != wantAt[i] {
			t.Errorf("line %d: %q at %d, want %q at %d", i, lines[i], at[i], wantLines[i], wantAt[i])
		}
	}
}

func TestCommentGroupPos(t *testing.T) {
	g := &CommentGroup{List: []*Comment{{Slash: 7, Text: "// a"}, {Slash: 12, Text: "// b"}}}
	if g.Pos() != 7 {
		t.Errorf("Pos() = %d, want 7", g.Pos())
	}
}
