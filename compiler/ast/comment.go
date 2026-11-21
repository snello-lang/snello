// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ast

import (
	"strings"

	"github.com/snello-lang/snello/compiler/token"
)

// The markers that open and close a comment, which Text takes off.
const (
	lineMarker  = "//"
	blockOpen   = "/*"
	blockClose  = "*/"
	commentLine = "\n"
)

// A Comment is one comment, // or /* */, as written.
type Comment struct {
	Slash token.Pos // the first / of its marker
	Text  string
}

// A CommentGroup is comments on consecutive lines with no token
// between.
type CommentGroup struct {
	List []*Comment
}

// Docs is what a file parsed with its comments keeps: every comment
// group in the order of the source, and the doc comment of the file's
// package, of each declaration, field, variant and trait method, and
// the line comment of each field, variant, constant and variable.
type Docs struct {
	Groups []*CommentGroup
	Lead   map[Node]*CommentGroup
	Line   map[Node]*CommentGroup
}

// Pos is where the group begins.
func (g *CommentGroup) Pos() token.Pos { return g.List[0].Slash }

// Text returns the group's text with the comment markers taken off,
// the space after a // too, each line's trailing space trimmed, blank
// lines at either end dropped and a run of blank lines made one, so
// that a comment reads as its writer meant, whatever its markers.
func (g *CommentGroup) Text() string {
	var lines []string
	for _, c := range g.List {
		text := c.Text
		if strings.HasPrefix(text, lineMarker) {
			text = strings.TrimPrefix(text[len(lineMarker):], " ")
		} else {
			text = strings.TrimSuffix(strings.TrimPrefix(text, blockOpen), blockClose)
		}

		for line := range strings.SplitSeq(text, commentLine) {
			lines = append(lines, strings.TrimRight(line, " \t\r"))
		}
	}

	var b strings.Builder
	blank := false
	for _, line := range lines {
		if line == "" {
			blank = b.Len() > 0
			continue
		}

		if blank {
			b.WriteString(commentLine)
			blank = false
		}

		b.WriteString(line)
		b.WriteString(commentLine)
	}

	return b.String()
}
