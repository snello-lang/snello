// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ast

import (
	"testing"

	"github.com/teak-lang/teak/token"
)

// TestPrint builds the tree for
//
//	func (s ref Stack<T>) Peek() ref T? {
//	    n := s.head ?? return None
//	    return Some(ref n.value)
//	}
//
// by hand and checks the printed form, which doubles as a readable
// statement of how the node types fit together.
func TestPrint(t *testing.T) {
	// A file with four lines of 40 bytes, so line:column maps to an offset
	// the printer can resolve back.
	file := token.NewFile("test.teak", 160)
	for _, start := range []int{40, 80, 120} {
		file.AddLine(start)
	}

	at := func(line, col int) token.Pos { return file.Pos((line-1)*40 + col - 1) }
	id := func(line, col int, name string) *Ident { return &Ident{NamePos: at(line, col), Name: name} }

	decl := &FuncDecl{
		Func: at(1, 1),
		Recv: &Param{
			Name: id(1, 7, "s"),
			Type: &RefType{Ref: at(1, 9), Elem: &NamedType{
				Name: id(1, 13, "Stack"),
				Args: []Type{&NamedType{Name: id(1, 19, "T")}},
			}},
		},
		Name:    id(1, 23, "Peek"),
		Results: []Type{&OptionType{Elem: &RefType{Ref: at(1, 30), Elem: &NamedType{Name: id(1, 34, "T")}}, Question: at(1, 35)}},
		Body: &BlockStmt{
			Lbrace: at(1, 37),
			List: []Stmt{
				&AssignStmt{
					Lhs:    []Expr{id(2, 5, "n")},
					TokPos: at(2, 7),
					Tok:    token.DEFINE,
					Rhs: []Expr{&CoalesceExpr{
						X:     &SelectorExpr{X: id(2, 10, "s"), Sel: id(2, 12, "head")},
						OpPos: at(2, 17),
						Else:  &ReturnStmt{Return: at(2, 20), Results: []Expr{id(2, 27, "None")}},
					}},
				},
				&ReturnStmt{
					Return: at(3, 5),
					Results: []Expr{&CallExpr{
						Fun:    id(3, 12, "Some"),
						Lparen: at(3, 16),
						Args: []Expr{&UnaryExpr{
							OpPos: at(3, 17),
							Op:    token.REF,
							X:     &SelectorExpr{X: id(3, 21, "n"), Sel: id(3, 23, "value")},
						}},
					}},
				},
			},
			Rbrace: at(4, 1),
		},
	}

	want := `FuncDecl {
  Func: 1:1
  Recv: Param {
    Name: Ident {
      NamePos: 1:7
      Name: "s"
    }
    Type: RefType {
      Ref: 1:9
      Elem: NamedType {
        Name: Ident {
          NamePos: 1:13
          Name: "Stack"
        }
        Args: [
          NamedType {
            Name: Ident {
              NamePos: 1:19
              Name: "T"
            }
          }
        ]
      }
    }
  }
  Name: Ident {
    NamePos: 1:23
    Name: "Peek"
  }
  Results: [
    OptionType {
      Elem: RefType {
        Ref: 1:30
        Elem: NamedType {
          Name: Ident {
            NamePos: 1:34
            Name: "T"
          }
        }
      }
      Question: 1:35
    }
  ]
  Body: BlockStmt {
    Lbrace: 1:37
    List: [
      AssignStmt {
        Lhs: [
          Ident {
            NamePos: 2:5
            Name: "n"
          }
        ]
        TokPos: 2:7
        Tok: :=
        Rhs: [
          CoalesceExpr {
            X: SelectorExpr {
              X: Ident {
                NamePos: 2:10
                Name: "s"
              }
              Sel: Ident {
                NamePos: 2:12
                Name: "head"
              }
            }
            OpPos: 2:17
            Else: ReturnStmt {
              Return: 2:20
              Results: [
                Ident {
                  NamePos: 2:27
                  Name: "None"
                }
              ]
            }
          }
        ]
      }
      ReturnStmt {
        Return: 3:5
        Results: [
          CallExpr {
            Fun: Ident {
              NamePos: 3:12
              Name: "Some"
            }
            Lparen: 3:16
            Args: [
              UnaryExpr {
                OpPos: 3:17
                Op: ref
                X: SelectorExpr {
                  X: Ident {
                    NamePos: 3:21
                    Name: "n"
                  }
                  Sel: Ident {
                    NamePos: 3:23
                    Name: "value"
                  }
                }
              }
            ]
          }
        ]
      }
    ]
    Rbrace: 4:1
  }
}
`
	got := Sprint(decl, file)
	if got != want {
		t.Errorf("printed tree differs:\n got:\n%s\n want:\n%s", got, want)
	}
}

// TestPos checks the nodes whose position is taken from a child, since
// those are the ones that can point at the wrong token: a qualified type
// starts at its package, a named field at its first name.
func TestPos(t *testing.T) {
	file := token.NewFile("test.teak", 100)
	at := func(col int) token.Pos { return file.Pos(col - 1) }
	id := func(col int, name string) *Ident { return &Ident{NamePos: at(col), Name: name} }

	tests := []struct {
		name string
		node Node
		want int
	}{
		{"qualified type", &NamedType{Pkg: id(1, "http"), Name: id(6, "Request")}, 1},
		{"unqualified type", &NamedType{Name: id(6, "Request")}, 6},
		{"named field", &Field{Names: []*Ident{id(1, "w"), id(4, "h")}, Type: &NamedType{Name: id(6, "float64")}}, 1},
		{"positional field", &Field{Type: &NamedType{Name: id(6, "float64")}}, 6},
		{"named param", &Param{Name: id(1, "xs"), Type: &RefType{Ref: at(4), Elem: &NamedType{Name: id(8, "T")}}}, 1},
		{"unnamed param", &Param{Type: &NamedType{Name: id(8, "T")}}, 8},
		{"aliased import", &ImportSpec{Name: id(8, "j"), Path: &BasicLit{ValuePos: at(10), Kind: token.STRING, Value: `"json"`}}, 8},
		{"plain import", &ImportSpec{Path: &BasicLit{ValuePos: at(10), Kind: token.STRING, Value: `"json"`}}, 10},
		{"typed literal", &CompositeLit{Type: &NamedType{Name: id(1, "Node")}, Lbrace: at(5)}, 1},
		{"elided literal", &CompositeLit{Lbrace: at(5)}, 5},
	}
	for _, tt := range tests {
		if got := file.Position(tt.node.Pos()).Column; got != tt.want {
			t.Errorf("%s: Pos column %d, want %d", tt.name, got, tt.want)
		}
	}
}

// TestFamilies checks that every concrete node belongs to exactly the
// family its name suggests, which is what the sealed interfaces promise.
func TestFamilies(t *testing.T) {
	var (
		_ Decl    = (*FuncDecl)(nil)
		_ Decl    = (*TypeDecl)(nil)
		_ Decl    = (*TraitDecl)(nil)
		_ Decl    = (*VarDecl)(nil)
		_ Stmt    = (*IfBindStmt)(nil)
		_ Stmt    = (*RangeStmt)(nil)
		_ Expr    = (*BadExpr)(nil)
		_ Expr    = (*CoalesceExpr)(nil)
		_ Expr    = (*MatchExpr)(nil)
		_ Expr    = (*Ident)(nil)
		_ Type    = (*OptionType)(nil)
		_ Type    = (*RefType)(nil)
		_ Type    = (*EnumType)(nil)
		_ Pattern = (*VariantPat)(nil)
		_ Pattern = (*RangePat)(nil)
	)

	// A node outside a family must not satisfy it. These are compile-time
	// facts; the assignments above fail to build if a marker is missing,
	// and the type assertions below fail at run time if one is extra.
	var n Node = &BlockStmt{}
	if _, ok := n.(Expr); ok {
		t.Error("BlockStmt must not be an Expr")
	}

	n = &StructType{}
	if _, ok := n.(Expr); ok {
		t.Error("StructType must not be an Expr")
	}
}
