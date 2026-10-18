// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parser

import (
	"strings"
	"testing"

	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/token"
)

// TestTypes parses each type and prints it back in canonical form. For
// most inputs the canonical form is the input itself; where it differs,
// the difference shows how the parser grouped the type.
func TestTypes(t *testing.T) {
	tests := []struct{ src, want string }{
		// Named, qualified, generic.
		{"int", "int"},
		{"http.Request", "http.Request"},
		{"List<T>", "List<T>"},
		{"arena.Index<Node>", "arena.Index<Node>"},
		{"map[K]Index<entry<K, V>>", "map[K]Index<entry<K, V>>"},
		{"Arc<sync.RwLock<map[string]Entry>>", "Arc<sync.RwLock<map[string]Entry>>"},
		{"Self", "Self"},

		// Where ? binds: outside pointers and borrows, inside containers.
		{"*Node<T>?", "(*Node<T>)?"},
		{"ref T?", "(ref T)?"},
		{"mut Tree", "mut Tree"},
		{"[]int?", "[](int)?"},
		{"[]*Node?", "[](*Node)?"},
		{"ref []T", "ref []T"},
		{"map[string]Value?", "map[string](Value)?"},
		{"*Node<K, V>?", "(*Node<K, V>)?"},
		{"(ref string, ref string)?", "((ref string, ref string))?"},
		{"[16]float64", "[16]float64"},

		// Channels.
		{"chan Job", "chan Job"},
		{"chan<- Result<int, string>", "chan<- Result<int, string>"},
		{"<-chan struct{}", "<-chan struct{}"},

		// Functions: unnamed, named, grouped, results.
		{"func(T) U", "func(T) U"},
		{"func(x int) int", "func(x int) int"},
		{"func(from, to NodeID, w int)", "func(from, to NodeID, w int)"},
		{"func(A, B,)", "func(A, B)"}, // trailing comma, as on a multi-line list
		{"func(a, b int,)", "func(a, b int)"},
		{"func(ref T) bool", "func(ref T) bool"},
		{"func(xs ref []T, keep func(ref T) bool) List<T>", "func(xs ref []T, keep func(ref T) bool) List<T>"},
		{"func() (*Node, *Node?)", "func() (*Node, (*Node)?)"},
		{"func(T?)", "func((T)?)"},
		{"func(pkg.T, List<T>)", "func(pkg.T, List<T>)"},

		// Tuples and structs.
		{"(A, B)", "(A, B)"},
		{"(A)", "A"},
		{"struct{}", "struct{}"},
		{"struct { id int; next *Node? }", "struct{id int; next (*Node)?}"},
		{"struct { w, h float64; Point }", "struct{w, h float64; Point}"},
	}
	for _, tt := range tests {
		typ, errs := parseTypeString(tt.src)
		if len(errs) > 0 {
			t.Errorf("%q: unexpected errors: %v", tt.src, errs)
			continue
		}

		if got := typeString(typ); got != tt.want {
			t.Errorf("%q: parsed as %q, want %q", tt.src, got, tt.want)
		}
	}
}

func TestTypeErrors(t *testing.T) {
	tests := []struct{ src, want string }{
		{"", "expected type, found 'EOF'"},
		{"123", "expected type, found '123'"},
		{"List<T", "expected '>', found 'newline'"}, // the scanner ends the line for us
		{"map[K", "expected ']', found 'newline'"},
		{"[x]int", "expected array length, found 'x'"},
		{"<-chan<- T", "expected 'chan', found 'T'"},
		{"func(", "expected ')', found 'EOF'"},
		{"struct { a int\ntype", "expected field or '}', found 'type'"},
		{"struct { a int b }", "expected ';' or newline, found 'b'"},
	}
	for _, tt := range tests {
		_, errs := parseTypeString(tt.src)
		if len(errs) == 0 || !strings.Contains(errs[0], tt.want) {
			t.Errorf("%q: got %v, want an error containing %q", tt.src, errs, tt.want)
		}
	}
}

// parseTypeString parses src as a type and returns it with the error
// messages, so types can be tested before declarations exist.
func parseTypeString(src string) (ast.Type, []string) {
	var p Parser
	p.init("test.teak", []byte(src))
	typ := p.parseType()
	if p.tok != token.EOF && p.tok != token.SEMICOLON {
		p.errorExpected(p.pos, "end of type")
	}

	var errs []string
	for _, e := range p.errors {
		errs = append(errs, e.Error())
	}

	return typ, errs
}

// typeString renders a type in canonical source form, with parentheses
// around the operand of ? so the grouping is visible.
func typeString(typ ast.Type) string {
	switch t := typ.(type) {
	case *ast.NamedType:
		s := t.Name.Name
		if t.Pkg != nil {
			s = t.Pkg.Name + "." + s
		}

		if len(t.Args) > 0 {
			s += "<" + typeList(t.Args, ", ") + ">"
		}

		return s
	case *ast.SelfType:
		return "Self"
	case *ast.PointerType:
		return "*" + typeString(t.Elem)
	case *ast.OptionType:
		return "(" + typeString(t.Elem) + ")?"
	case *ast.RefType:
		if t.Mut {
			return "mut " + typeString(t.Elem)
		}

		return "ref " + typeString(t.Elem)
	case *ast.SliceType:
		return "[]" + typeString(t.Elem)
	case *ast.ArrayType:
		return "[" + t.Len.(*ast.BasicLit).Value + "]" + typeString(t.Elem)
	case *ast.MapType:
		return "map[" + typeString(t.Key) + "]" + typeString(t.Value)
	case *ast.ChanType:
		switch t.Dir {
		case ast.SendOnly:
			return "chan<- " + typeString(t.Elem)
		case ast.RecvOnly:
			return "<-chan " + typeString(t.Elem)
		}

		return "chan " + typeString(t.Elem)
	case *ast.FuncType:
		s := "func(" + paramList(t.Params) + ")"
		switch len(t.Results) {
		case 0:
		case 1:
			s += " " + typeString(t.Results[0])
		default:
			s += " (" + typeList(t.Results, ", ") + ")"
		}

		return s
	case *ast.TupleType:
		return "(" + typeList(t.Elems, ", ") + ")"
	case *ast.StructType:
		return "struct{" + fieldList(t.Fields) + "}"
	}

	return "?"
}

func fieldList(fields []*ast.Field) string {
	var parts []string
	for _, f := range fields {
		parts = append(parts, fieldString(f))
	}

	return strings.Join(parts, "; ")
}

func fieldString(f *ast.Field) string {
	var names []string
	for _, n := range f.Names {
		names = append(names, n.Name)
	}

	s := typeString(f.Type)
	if len(names) > 0 {
		s = strings.Join(names, ", ") + " " + s
	}

	return s
}

func typeList(types []ast.Type, sep string) string {
	var parts []string
	for _, t := range types {
		parts = append(parts, typeString(t))
	}

	return strings.Join(parts, sep)
}

// paramList renders parameters, regrouping consecutive names that share
// a type, so (from, to NodeID) prints as written.
func paramList(params []*ast.Param) string {
	var parts []string
	for i := 0; i < len(params); i++ {
		p := params[i]
		if p.Name == nil {
			parts = append(parts, typeString(p.Type))
			continue
		}

		names := []string{p.Name.Name}
		for i+1 < len(params) && params[i+1].Name != nil && params[i+1].Type == p.Type {
			i++
			names = append(names, params[i].Name.Name)
		}

		parts = append(parts, strings.Join(names, ", ")+" "+typeString(p.Type))
	}

	return strings.Join(parts, ", ")
}
