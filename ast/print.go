// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ast

import (
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/teak-lang/teak/token"
)

// Fprint writes an indented rendering of the tree rooted at node, one
// field per line, for people reading `teak parse` output. Positions are
// printed as line:column; nil fields and empty slices are omitted.
//
// It walks the node with reflection, as go/ast.Fprint does, so that
// adding a node type needs no change here.
func Fprint(w io.Writer, node Node) {
	p := printer{w: w}
	p.print(reflect.ValueOf(node), 0)
	fmt.Fprintln(w)
}

// Sprint returns what Fprint would write.
func Sprint(node Node) string {
	var sb strings.Builder
	Fprint(&sb, node)
	return sb.String()
}

type printer struct {
	w io.Writer
}

var posType = reflect.TypeOf(token.Pos{})

func (p *printer) print(v reflect.Value, depth int) {
	switch v.Kind() {
	case reflect.Interface, reflect.Ptr:
		if v.IsNil() {
			fmt.Fprint(p.w, "nil")
			return
		}

		p.print(v.Elem(), depth)

	case reflect.Struct:
		if v.Type() == posType {
			pos := v.Interface().(token.Pos)
			fmt.Fprintf(p.w, "%d:%d", pos.Line, pos.Column)
			return
		}

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
		if k, ok := v.Interface().(token.Kind); ok {
			fmt.Fprint(p.w, k.String())
			return
		}

		fmt.Fprint(p.w, v.Interface())
	}
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
	case reflect.Struct:
		return v.Type() == posType && !v.Interface().(token.Pos).IsValid()
	}

	return false
}

func indent(n int) string { return strings.Repeat("  ", n) }
