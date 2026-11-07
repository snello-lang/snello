// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ast

import (
	"strings"

	"github.com/snello-lang/snello/compiler/token"
)

// Source writes an expression as a program writes it, on one line: the
// text a test's failure shows for the expression that failed, which
// the compiler hands the expect functions (_docs/design/09-testing.md,
// section 4), since the syntax tree keeps no source text. A function
// literal, an if or a match is abridged to its head, with { ... } for
// its body, as a line can hold.
func Source(e Expr) string {
	var b strings.Builder
	writeExpr(&b, e)
	return b.String()
}

// SourceType writes a type as a program writes it.
func SourceType(t Type) string {
	var b strings.Builder
	writeType(&b, t)
	return b.String()
}

func writeExpr(b *strings.Builder, e Expr) {
	switch x := e.(type) {
	case nil:
		b.WriteString("?")
	case *Ident:
		b.WriteString(x.Name)
	case *BasicLit:
		b.WriteString(x.Value)
	case *ParenExpr:
		b.WriteString("(")
		writeExpr(b, x.X)
		b.WriteString(")")
	case *TupleExpr:
		b.WriteString("(")
		writeExprs(b, x.Elts)
		b.WriteString(")")
	case *SelectorExpr:
		writeExpr(b, x.X)
		b.WriteString(".")
		b.WriteString(x.Sel.Name)
	case *IndexExpr:
		writeExpr(b, x.X)
		b.WriteString("[")
		writeExpr(b, x.Index)
		b.WriteString("]")
	case *SliceExpr:
		writeExpr(b, x.X)
		b.WriteString("[")
		if x.Low != nil {
			writeExpr(b, x.Low)
		}

		b.WriteString(":")
		if x.High != nil {
			writeExpr(b, x.High)
		}

		b.WriteString("]")
	case *CallExpr:
		writeExpr(b, x.Fun)
		if len(x.TypeArgs) > 0 {
			b.WriteString("<")
			for i, t := range x.TypeArgs {
				if i > 0 {
					b.WriteString(", ")
				}

				writeType(b, t)
			}

			b.WriteString(">")
		}

		b.WriteString("(")
		writeExprs(b, x.Args)
		b.WriteString(")")
	case *UnaryExpr:
		switch x.Op {
		case token.REF, token.MUT:
			b.WriteString(x.Op.String() + " ")
		default:
			b.WriteString(x.Op.String())
		}

		writeExpr(b, x.X)
	case *BinaryExpr:
		writeExpr(b, x.X)
		b.WriteString(" " + x.Op.String() + " ")
		writeExpr(b, x.Y)
	case *CoalesceExpr:
		writeExpr(b, x.X)
		b.WriteString(" ?? ")
		switch y := x.Else.(type) {
		case Expr:
			writeExpr(b, y)
		case *ReturnStmt:
			b.WriteString("return")
			if len(y.Results) > 0 {
				b.WriteString(" ")
				writeExprs(b, y.Results)
			}
		case *BranchStmt:
			b.WriteString(y.Tok.String())
		default:
			b.WriteString("{ ... }")
		}
	case *TypeExpr:
		writeType(b, x.Type)
	case *TryExpr:
		writeExpr(b, x.X)
		b.WriteString("?")
	case *CompositeLit:
		if x.Type != nil {
			writeType(b, x.Type)
		}

		b.WriteString("{")
		writeExprs(b, x.Elts)
		b.WriteString("}")
	case *KeyValueExpr:
		writeExpr(b, x.Key)
		b.WriteString(": ")
		writeExpr(b, x.Value)
	case *FuncLit:
		b.WriteString("func(")
		writeParams(b, x.Type.Params)
		b.WriteString(")")
		writeResults(b, x.Type.Results)
		b.WriteString(" { ... }")
	case *IfExpr:
		b.WriteString("if ")
		writeExpr(b, x.Cond)
		b.WriteString(" { ... }")
	case *MatchExpr:
		b.WriteString("match ")
		writeExpr(b, x.X)
		b.WriteString(" { ... }")
	default:
		b.WriteString("...")
	}
}

func writeExprs(b *strings.Builder, es []Expr) {
	for i, e := range es {
		if i > 0 {
			b.WriteString(", ")
		}

		writeExpr(b, e)
	}
}

func writeParams(b *strings.Builder, ps []*Param) {
	for i, p := range ps {
		if i > 0 {
			b.WriteString(", ")
		}

		if p.Name != nil {
			b.WriteString(p.Name.Name + " ")
		}

		writeType(b, p.Type)
	}
}

func writeResults(b *strings.Builder, rs []Type) {
	switch len(rs) {
	case 0:
	case 1:
		b.WriteString(" ")
		writeType(b, rs[0])
	default:
		b.WriteString(" (")
		for i, r := range rs {
			if i > 0 {
				b.WriteString(", ")
			}

			writeType(b, r)
		}

		b.WriteString(")")
	}
}

func writeType(b *strings.Builder, t Type) {
	switch x := t.(type) {
	case nil:
		b.WriteString("?")
	case *NamedType:
		if x.Pkg != nil {
			b.WriteString(x.Pkg.Name + ".")
		}

		b.WriteString(x.Name.Name)
		if len(x.Args) > 0 {
			b.WriteString("<")
			for i, a := range x.Args {
				if i > 0 {
					b.WriteString(", ")
				}

				writeType(b, a)
			}

			b.WriteString(">")
		}
	case *SelfType:
		b.WriteString("Self")
	case *PointerType:
		b.WriteString("*")
		writeType(b, x.Elem)
	case *OptionType:
		writeType(b, x.Elem)
		b.WriteString("?")
	case *RefType:
		if x.Mut {
			b.WriteString("mut ")
		} else {
			b.WriteString("ref ")
		}

		writeType(b, x.Elem)
	case *SliceType:
		b.WriteString("[]")
		writeType(b, x.Elem)
	case *ArrayType:
		b.WriteString("[")
		writeExpr(b, x.Len)
		b.WriteString("]")
		writeType(b, x.Elem)
	case *MapType:
		b.WriteString("map[")
		writeType(b, x.Key)
		b.WriteString("]")
		writeType(b, x.Value)
	case *ChanType:
		switch x.Dir {
		case SendOnly:
			b.WriteString("chan<- ")
		case RecvOnly:
			b.WriteString("<-chan ")
		default:
			b.WriteString("chan ")
		}

		writeType(b, x.Elem)
	case *FuncType:
		b.WriteString("func(")
		writeParams(b, x.Params)
		b.WriteString(")")
		writeResults(b, x.Results)
	case *TupleType:
		b.WriteString("(")
		for i, e := range x.Elems {
			if i > 0 {
				b.WriteString(", ")
			}

			writeType(b, e)
		}

		b.WriteString(")")
	default:
		b.WriteString("...")
	}
}
