// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parser

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// docsSrc has a comment for every place a doc comment or a line comment
// goes, and comments in places none does.
const docsSrc = `// Copyright: not the package's, a blank line away.

// Package shapes draws.
package shapes

// Pi is a constant.
const Pi = 3.14 // pi's line

const Tau = 6.28 // tau's line

// Area is a function.
func Area() int {
	// inside a body: nobody's
	return 0
}

// A Point is a struct.
struct Point {
	// X is a field.
	X int // x's line
	y int
}

// Shape is an enum.
enum Shape {
	// Circle is a variant.
	Circle(r int) // circle's line
	Square
}

// Drawable is a trait.
trait Drawable {
	// Draw is a method.
	func Draw()
}

// Sized is a trait of one line, whose comment is not its method's.
trait Sized { func Size() int }

// Color is an enum of one line, whose trailing comment is no variant's.
enum Color { Red; Green } // both

// A gap leaves this to nobody.

func Lone() {}
`

func TestParseComments(t *testing.T) {
	file := token.NewFile("shapes.snello", len(docsSrc))
	f, err := ParseComments(file, []byte(docsSrc))
	if err != nil {
		t.Fatal(err)
	}

	decl := map[string]ast.Node{}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			decl[d.Name.Name] = d
		case *ast.VarDecl:
			decl[d.Names[0].Name] = d
		case *ast.TypeDecl:
			decl[d.Name.Name] = d
			switch def := d.Def.(type) {
			case *ast.StructType:
				for _, field := range def.Fields {
					decl[field.Names[0].Name] = field
				}
			case *ast.EnumType:
				for _, v := range def.Variants {
					decl[v.Name.Name] = v
				}
			}
		case *ast.TraitDecl:
			decl[d.Name.Name] = d
			for _, m := range d.Methods {
				decl[m.Name.Name] = m
			}
		}
	}

	leads := map[string]string{
		"package": "Package shapes draws.", "Pi": "Pi is a constant.", "Tau": "", "Area": "Area is a function.",
		"Point": "A Point is a struct.", "X": "X is a field.", "y": "", "Shape": "Shape is an enum.",
		"Circle": "Circle is a variant.", "Square": "", "Drawable": "Drawable is a trait.", "Draw": "Draw is a method.", "Lone": "",
		"Sized": "Sized is a trait of one line, whose comment is not its method's.", "Size": "",
	}
	lines := map[string]string{"Pi": "pi's line", "Tau": "tau's line", "X": "x's line", "Circle": "circle's line", "y": "", "Square": "", "Red": "", "Green": ""}
	for name, want := range leads {
		n := decl[name]
		if name == "package" {
			n = f
		}

		if got := text(f.Docs.Lead[n]); got != want {
			t.Errorf("%s: doc comment %q, want %q", name, got, want)
		}
	}

	for name, want := range lines {
		if got := text(f.Docs.Line[decl[name]]); got != want {
			t.Errorf("%s: line comment %q, want %q", name, got, want)
		}
	}

	if len(f.Docs.Groups) != 19 {
		t.Errorf("%d groups, want every comment's 19", len(f.Docs.Groups))
	}
}

// TestParseKeepsNoComments holds a build's parse to keeping none.
func TestParseKeepsNoComments(t *testing.T) {
	f, err := Parse(token.NewFile("shapes.snello", len(docsSrc)), []byte(docsSrc))
	if err != nil {
		t.Fatal(err)
	}

	if f.Docs != nil {
		t.Error("Parse kept comments")
	}
}

func text(g *ast.CommentGroup) string {
	if g == nil {
		return ""
	}

	return strings.TrimSpace(g.Text())
}
