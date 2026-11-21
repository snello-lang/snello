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
// the compiler hands the expect functions, since the syntax tree keeps
// no source text. A function literal, an if or a match is abridged to
// its head, with { ... } for its body, as a line can hold.
func Source(e Expr) string {
	var b strings.Builder
	WriteExpr(&b, e)
	return b.String()
}

// SourceType writes a type as a program writes it.
func SourceType(t Type) string {
	var b strings.Builder
	WriteType(&b, t)
	return b.String()
}

// WriteSignature writes a function's declaration without its body to
// b, as a program writes it: its receiver or the type it is a function
// of, its name, its type parameters with their bounds, its parameters
// with their names, modes and ..., and its results.
func WriteSignature(b *strings.Builder, fd *FuncDecl) {
	b.WriteString("func ")
	switch {
	case fd.Recv != nil:
		b.WriteString("(")
		WriteParams(b, []*Param{fd.Recv})
		b.WriteString(") ")
	case fd.Of != nil:
		WriteType(b, fd.Of)
		b.WriteString(".")
	}

	b.WriteString(fd.Name.Name)
	WriteTypeParams(b, fd.TypeParams)
	b.WriteString("(")
	WriteParams(b, fd.Params)
	b.WriteString(")")
	writeResults(b, fd.Results)
}

// WriteTypeParams writes type parameters with their bounds to b as a
// program writes them, <K: Hash + Eq, V>, or nothing when there are
// none.
func WriteTypeParams(b *strings.Builder, tps []*TypeParam) {
	if len(tps) == 0 {
		return
	}

	b.WriteString("<")
	for i, tp := range tps {
		if i > 0 {
			b.WriteString(", ")
		}

		b.WriteString(tp.Name.Name)
		for j, bound := range tp.Bounds {
			if j == 0 {
				b.WriteString(": ")
			} else {
				b.WriteString(" + ")
			}

			WriteType(b, bound)
		}
	}

	b.WriteString(">")
}

// WriteExpr writes an expression to b as Source returns it.
func WriteExpr(b *strings.Builder, e Expr) {
	switch x := e.(type) {
	case nil:
		b.WriteString("?")
	case *Ident:
		b.WriteString(x.Name)
	case *BasicLit:
		b.WriteString(x.Value)
	case *ParenExpr:
		b.WriteString("(")
		WriteExpr(b, x.X)
		b.WriteString(")")
	case *TupleExpr:
		b.WriteString("(")
		writeExprs(b, x.Elts)
		b.WriteString(")")
	case *SelectorExpr:
		WriteExpr(b, x.X)
		b.WriteString(".")
		b.WriteString(x.Sel.Name)
	case *IndexExpr:
		WriteExpr(b, x.X)
		b.WriteString("[")
		WriteExpr(b, x.Index)
		b.WriteString("]")
	case *SliceExpr:
		WriteExpr(b, x.X)
		b.WriteString("[")
		if x.Low != nil {
			WriteExpr(b, x.Low)
		}

		b.WriteString(":")
		if x.High != nil {
			WriteExpr(b, x.High)
		}

		b.WriteString("]")
	case *CallExpr:
		WriteExpr(b, x.Fun)
		if len(x.TypeArgs) > 0 {
			b.WriteString("<")
			for i, t := range x.TypeArgs {
				if i > 0 {
					b.WriteString(", ")
				}

				WriteType(b, t)
			}

			b.WriteString(">")
		}

		b.WriteString("(")
		writeExprs(b, x.Args)
		if x.Ellipsis.IsValid() {
			b.WriteString("...")
		}

		b.WriteString(")")
	case *UnaryExpr:
		switch x.Op {
		case token.REF, token.MUT:
			b.WriteString(x.Op.String())
			b.WriteString(" ")
		default:
			b.WriteString(x.Op.String())
		}

		WriteExpr(b, x.X)
	case *BinaryExpr:
		WriteExpr(b, x.X)
		b.WriteString(" ")
		b.WriteString(x.Op.String())
		b.WriteString(" ")
		WriteExpr(b, x.Y)
	case *SpawnExpr:
		b.WriteString("spawn ")
		WriteExpr(b, x.Call)
	case *CoalesceExpr:
		WriteExpr(b, x.X)
		b.WriteString(" ?? ")
		switch y := x.Else.(type) {
		case Expr:
			WriteExpr(b, y)
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
		WriteType(b, x.Type)
	case *TryExpr:
		WriteExpr(b, x.X)
		b.WriteString("?")
	case *CompositeLit:
		if x.Type != nil {
			WriteType(b, x.Type)
		}

		b.WriteString("{")
		writeExprs(b, x.Elts)
		b.WriteString("}")
	case *KeyValueExpr:
		WriteExpr(b, x.Key)
		b.WriteString(": ")
		WriteExpr(b, x.Value)
	case *PartExpr:
		WriteExpr(b, x.X)
		b.WriteString(".[")
		b.WriteString(x.Part.Name)
		b.WriteString("]")
	case *PartsElt:
		b.WriteString("for ")
		b.WriteString(x.Var.Name)
		b.WriteString(" := range ")
		WriteExpr(b, x.X)
		b.WriteString(" { ")
		if x.Key != nil {
			b.WriteString("[")
			b.WriteString(x.Key.Name)
			b.WriteString("]: ")
		}

		WriteExpr(b, x.Value)
		b.WriteString(" }")
	case *FuncLit:
		b.WriteString("func(")
		WriteParams(b, x.Type.Params)
		b.WriteString(")")
		writeResults(b, x.Type.Results)
		b.WriteString(" { ... }")
	case *IfExpr:
		b.WriteString("if ")
		WriteExpr(b, x.Cond)
		b.WriteString(" { ... }")
	case *MatchExpr:
		b.WriteString("match ")
		WriteExpr(b, x.X)
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

		WriteExpr(b, e)
	}
}

// WriteParams writes a parameter list to b without its parentheses, as
// a program writes it: each name, mode, ... and type.
func WriteParams(b *strings.Builder, ps []*Param) {
	for i, p := range ps {
		if i > 0 {
			b.WriteString(", ")
		}

		if p.Name != nil {
			b.WriteString(p.Name.Name)
			b.WriteString(" ")
		}

		if p.Dots.IsValid() {
			b.WriteString("...")
		}

		WriteType(b, p.Type)
	}
}

func writeResults(b *strings.Builder, rs []Type) {
	switch len(rs) {
	case 0:
	case 1:
		b.WriteString(" ")
		WriteType(b, rs[0])
	default:
		b.WriteString(" (")
		for i, r := range rs {
			if i > 0 {
				b.WriteString(", ")
			}

			WriteType(b, r)
		}

		b.WriteString(")")
	}
}

// WriteType writes a type to b as SourceType returns it.
func WriteType(b *strings.Builder, t Type) {
	switch x := t.(type) {
	case nil:
		b.WriteString("?")
	case *NamedType:
		if x.Pkg != nil {
			b.WriteString(x.Pkg.Name)
			b.WriteString(".")
		}

		b.WriteString(x.Name.Name)
		if len(x.Args) > 0 {
			b.WriteString("<")
			for i, a := range x.Args {
				if i > 0 {
					b.WriteString(", ")
				}

				WriteType(b, a)
			}

			b.WriteString(">")
		}
	case *SelfType:
		b.WriteString("Self")
	case *PointerType:
		b.WriteString("*")
		WriteType(b, x.Elem)
	case *OptionType:
		WriteType(b, x.Elem)
		b.WriteString("?")
	case *RefType:
		if x.Mut {
			b.WriteString("mut ")
		} else {
			b.WriteString("ref ")
		}

		WriteType(b, x.Elem)
	case *SliceType:
		b.WriteString("[]")
		WriteType(b, x.Elem)
	case *ArrayType:
		b.WriteString("[")
		WriteExpr(b, x.Len)
		b.WriteString("]")
		WriteType(b, x.Elem)
	case *MapType:
		b.WriteString("map[")
		WriteType(b, x.Key)
		b.WriteString("]")
		WriteType(b, x.Value)
	case *ChanType:
		switch x.Dir {
		case SendOnly:
			b.WriteString("chan<- ")
		case RecvOnly:
			b.WriteString("<-chan ")
		default:
			b.WriteString("chan ")
		}

		WriteType(b, x.Elem)
	case *FuncType:
		b.WriteString("func(")
		WriteParams(b, x.Params)
		b.WriteString(")")
		writeResults(b, x.Results)
	case *TupleType:
		b.WriteString("(")
		for i, e := range x.Elems {
			if i > 0 {
				b.WriteString(", ")
			}

			WriteType(b, e)
		}

		b.WriteString(")")
	default:
		b.WriteString("...")
	}
}
