// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ast

import (
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/snello-lang/snello/compiler/token"
)

// Fprint writes an indented rendering of the tree rooted at node, one
// field per line, for people reading `snello parse` output. Positions are
// printed as line:column, resolved through file, or as raw offsets when
// file is nil; nil fields, empty slices and missing positions are
// omitted. What a File keeps beside its tree is printed on the node it
// belongs to: its doc comment as Doc, first, its line comment as
// Comment and its attributes as Attrs, last.
//
// It walks the node with reflection, as go/ast.Fprint does, so that
// adding a node type needs no change here.
func Fprint(w io.Writer, node Node, file *token.File) {
	p := printer{w: w, file: file}
	if f, ok := node.(*File); ok {
		p.attrs, p.docs = f.Attrs, f.Docs
	}

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
	w     io.Writer
	file  *token.File
	attrs map[Node][]*Attr
	docs  *Docs
}

// The types of a position and of the tables a File keeps beside its
// tree, which are printed on their nodes rather than as fields.
var (
	posType   = reflect.TypeOf(token.NoPos)
	attrsType = reflect.TypeOf(map[Node][]*Attr(nil))
	docsType  = reflect.TypeOf((*Docs)(nil))
)

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
		n := p.nodeOf(v)
		if p.docs != nil && p.docs.Lead[n] != nil {
			p.field("Doc", reflect.ValueOf(p.docs.Lead[n].Text()), depth+1)
		}

		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if isEmpty(f) || f.Type() == attrsType || f.Type() == docsType {
				continue
			}

			p.field(v.Type().Field(i).Name, f, depth+1)
		}

		if p.docs != nil && p.docs.Line[n] != nil {
			p.field("Comment", reflect.ValueOf(p.docs.Line[n].Text()), depth+1)
		}

		if attrs := p.attrs[n]; len(attrs) > 0 {
			p.field("Attrs", reflect.ValueOf(attrs), depth+1)
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

// field writes one field of a struct, its name and its value, on a
// line of its own.
func (p *printer) field(name string, v reflect.Value, depth int) {
	fmt.Fprintf(p.w, "\n%s%s: ", indent(depth), name)
	p.print(v, depth)
}

// nodeOf returns the node a struct is, as the tables beside the tree
// key it, or nil for a struct that is no node or is not reached through
// a pointer.
func (p *printer) nodeOf(v reflect.Value) Node {
	if !v.CanAddr() {
		return nil
	}

	n, _ := v.Addr().Interface().(Node)
	return n
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
