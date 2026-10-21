// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ast

import (
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/teak-lang/teak/compiler/token"
)

// Fprint writes an indented rendering of the tree rooted at node, one
// field per line, for people reading `teak parse` output. Positions are
// printed as line:column, resolved through file, or as raw offsets when
// file is nil; nil fields, empty slices and missing positions are
// omitted.
//
// It walks the node with reflection, as go/ast.Fprint does, so that
// adding a node type needs no change here.
func Fprint(w io.Writer, node Node, file *token.File) {
	p := printer{w: w, file: file}
	p.print(reflect.ValueOf(node), 0)
	fmt.Fprintln(w)
}

// Sprint returns what Fprint would write.
func Sprint(node Node, file *token.File) string {
	var sb strings.Builder
	Fprint(&sb, node, file)
	return sb.String()
}

type printer struct {
	w    io.Writer
	file *token.File
}

var posType = reflect.TypeOf(token.NoPos)

func (p *printer) print(v reflect.Value, depth int) {
	switch v.Kind() {
	case reflect.Interface, reflect.Ptr:
		if v.IsNil() {
			fmt.Fprint(p.w, "nil")
			return
		}

		p.print(v.Elem(), depth)

	case reflect.Struct:
		fmt.Fprintf(p.w, "%s {", v.Type().Name())
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if isEmpty(f) {
				continue
			}

			fmt.Fprintf(p.w, "\n%s%s: ", indent(depth+1), v.Type().Field(i).Name)
			p.print(f, depth+1)
		}

		fmt.Fprintf(p.w, "\n%s}", indent(depth))

	case reflect.Slice:
		fmt.Fprint(p.w, "[")
		for i := 0; i < v.Len(); i++ {
			fmt.Fprintf(p.w, "\n%s", indent(depth+1))
			p.print(v.Index(i), depth+1)
		}

		fmt.Fprintf(p.w, "\n%s]", indent(depth))

	case reflect.String:
		fmt.Fprintf(p.w, "%q", v.String())

	default:
		switch x := v.Interface().(type) {
		case token.Pos:
			p.printPos(x)
		case token.Kind:
			fmt.Fprint(p.w, x.String())
		default:
			fmt.Fprint(p.w, x)
		}
	}
}

func (p *printer) printPos(pos token.Pos) {
	if p.file == nil {
		fmt.Fprint(p.w, int(pos))
		return
	}

	r := p.file.Position(pos)
	fmt.Fprintf(p.w, "%d:%d", r.Line, r.Column)
}

// isEmpty reports whether a field would print nothing useful.
func isEmpty(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Interface, reflect.Ptr:
		return v.IsNil()
	case reflect.Slice:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int:
		return v.Type() == posType && !v.Interface().(token.Pos).IsValid()
	}

	return false
}

func indent(n int) string { return strings.Repeat("  ", n) }
