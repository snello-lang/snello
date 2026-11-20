// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parser

import (
	"sort"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/scanner"
	"github.com/snello-lang/snello/compiler/token"
)

// ParseComments parses src as Parse does, and keeps the file's comments
// in File.Docs: every comment group, and the doc comment and line
// comment of each declaration, field, variant and trait method: a
// group of comments ending on the line above a node, the first
// beginning its line, is its doc comment, and a comment after a token
// on a member's line its line comment. What snello doc and the examples' runner read; a
// build parses with Parse, which keeps none.
func ParseComments(file *token.File, src []byte) (*ast.File, error) {
	var p Parser
	p.init(file, src, true)
	f := p.parseFile()
	f.Docs = attach(file, f, p.scanner.Comments())
	p.errors.Sort()

	return f, p.errors.Err()
}

// A commentIndex finds the comment groups around a node by their
// lines.
type commentIndex struct {
	file   *token.File
	groups []*ast.CommentGroup
	owns   []bool // whether each group's first comment begins its line
	docs   *ast.Docs
}

// attach groups the comments and gives each node the group that is its
// doc comment: the group that ends on the line above the node and whose
// first comment begins its line; and to each field, variant, constant
// and variable the group that is its line comment: the group that
// begins after it, on its line, after a token.
func attach(file *token.File, f *ast.File, comments []scanner.Comment) *ast.Docs {
	ix := commentIndex{file: file, docs: &ast.Docs{Lead: map[ast.Node]*ast.CommentGroup{}, Line: map[ast.Node]*ast.CommentGroup{}}}
	ix.group(comments)
	ix.lead(f, f.Package)
	for _, d := range f.Decls {
		ix.lead(d, d.Pos())
		switch d := d.(type) {
		case *ast.TypeDecl:
			ix.members(d.Def, d.Pos())
		case *ast.TraitDecl:
			for _, field := range d.Fields {
				ix.member(field, d.Pos())
			}

			for _, m := range d.Methods {
				if ix.lineOf(m.Pos()) != ix.lineOf(d.Pos()) {
					ix.lead(m, m.Pos())
				}
			}
		case *ast.VarDecl:
			ix.line(d)
		}
	}

	return ix.docs
}

func (ix *commentIndex) members(def ast.Type, owner token.Pos) {
	switch def := def.(type) {
	case *ast.StructType:
		for _, field := range def.Fields {
			ix.member(field, owner)
		}
	case *ast.EnumType:
		for _, v := range def.Variants {
			ix.member(v, owner)
		}
	}
}

// member gives a field or a variant its comments; one written on its
// type's line, a body of one line, has none of its own, since the
// comment above is the type's and one after the body follows its brace.
func (ix *commentIndex) member(n ast.Node, owner token.Pos) {
	if ix.lineOf(n.Pos()) == ix.lineOf(owner) {
		return
	}

	ix.lead(n, n.Pos())
	ix.line(n)
}

// group makes the comment groups: a comment that follows a token on its
// line stands alone, and comments that begin their lines join while
// each begins on the line after the one before ends.
func (ix *commentIndex) group(comments []scanner.Comment) {
	end := 0 // the line the last comment ends on
	for _, c := range comments {
		first := ix.lineOf(c.Pos)
		joins := len(ix.groups) > 0 && c.OwnsLine && ix.owns[len(ix.groups)-1] && first == end+1
		if joins {
			g := ix.groups[len(ix.groups)-1]
			g.List = append(g.List, &ast.Comment{Slash: c.Pos, Text: c.Text})
		} else {
			ix.groups = append(ix.groups, &ast.CommentGroup{List: []*ast.Comment{{Slash: c.Pos, Text: c.Text}}})
			ix.owns = append(ix.owns, c.OwnsLine)
		}

		end = ix.lineOf(c.Pos + token.Pos(len(c.Text)-1))
	}

	ix.docs.Groups = ix.groups
}

// lineOf returns the line pos is on.
func (ix *commentIndex) lineOf(pos token.Pos) int { return ix.file.Position(pos).Line }

// before returns the index of the last group that begins before pos, or
// -1.
func (ix *commentIndex) before(pos token.Pos) int {
	return sort.Search(len(ix.groups), func(i int) bool { return ix.groups[i].Pos() >= pos }) - 1
}

func (ix *commentIndex) lead(n ast.Node, pos token.Pos) {
	i := ix.before(pos)
	if i < 0 || !ix.owns[i] {
		return
	}

	g := ix.groups[i]
	last := g.List[len(g.List)-1]
	if ix.lineOf(last.Slash+token.Pos(len(last.Text)-1)) == ix.lineOf(pos)-1 {
		ix.docs.Lead[n] = g
	}
}

func (ix *commentIndex) line(n ast.Node) {
	i := ix.before(n.Pos()) + 1
	if i < len(ix.groups) && !ix.owns[i] && ix.lineOf(ix.groups[i].Pos()) == ix.lineOf(n.Pos()) {
		ix.docs.Line[n] = ix.groups[i]
	}
}
