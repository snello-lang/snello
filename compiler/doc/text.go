// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	"io"
	"slices"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
)

// How a page is laid out in the terminal: the indentation of a comment
// under its declaration and of a member under its type, a body cut
// short, the note for a struct's hidden fields, what joins names and
// lists, a trait's implementors' heading and the width they wrap at.
const (
	indent        = "    "
	codeIndent    = "    "
	abridged      = " { ... }"
	hiddenFields  = "// Has unexported fields."
	hiddenMethods = "// Has unexported methods."
	nameSep       = ", "
	wordSep       = " "
	lineSep       = "\n"
	importNote    = " // import \""
	importEnd     = "\""
	lineNote      = " // "
	commentMark   = "//"
	headingLead   = "# "
	itemLead      = "  "
	attrMark      = " @"
	implementedBy = "Implemented by:"
	exampleHead   = "Example"
	outputHead    = "Output:"
	lineWidth     = 80
)

// WritePackage writes the overview of the package: its clause, its
// comment, the forms the builtin package documents, and a line for each
// name, a type's functions and methods under it; with all, every form
// and name with its comment instead.
func (p *Package) WritePackage(w io.Writer, all bool) {
	var b strings.Builder
	put(&b, "package ", p.Name)
	if p.Path != "" {
		put(&b, importNote, p.Path, importEnd)
	}

	b.WriteString(lineSep)
	if p.Doc != "" {
		b.WriteString(lineSep)
		p.writeComment(&b, p.Doc, "")
	}

	if all {
		p.writeExamples(&b, "")
	}

	if len(p.Forms) > 0 && !all {
		b.WriteString(lineSep)
	}

	for _, form := range p.Forms {
		p.writeForm(&b, form, all)
	}

	for _, group := range p.Consts {
		p.writeValues(&b, group, all)
	}

	for _, group := range p.Vars {
		p.writeValues(&b, group, all)
	}

	if len(p.Funcs) > 0 && !all {
		b.WriteString(lineSep)
	}

	for _, fn := range p.Funcs {
		p.writeFunc(&b, fn, all)
	}

	for _, t := range p.Types {
		b.WriteString(lineSep)
		if all {
			p.writeType(&b, t, true)
			continue
		}

		p.WriteTypeHeader(&b, t)
		put(&b, abridged, lineSep)
		for _, fn := range t.functions() {
			b.WriteString(indent)
			ast.WriteSignature(&b, fn.Decl)
			b.WriteString(lineSep)
		}
	}

	io.WriteString(w, b.String())
}

// WriteName writes the page of a name of the package: a type, with its
// declaration, comment, functions and methods; a function, a constant
// or a variable with its group; or a type's member, Builder.Len. It
// reports whether the package has the name.
func (p *Package) WriteName(w io.Writer, name string) bool {
	var b strings.Builder
	if !p.writeName(&b, name) {
		return false
	}

	io.WriteString(w, strings.TrimPrefix(b.String(), lineSep)) // a page begins with its declaration, whatever it is
	return true
}

func (p *Package) writeName(b *strings.Builder, name string) bool {
	if !p.writeDecl(b, name) {
		return false
	}

	p.writeExamples(b, name)
	return true
}

func (p *Package) writeDecl(b *strings.Builder, name string) bool {
	if owner, member, ok := strings.Cut(name, string(memberSep)); ok {
		return p.writeMember(b, owner, member)
	}

	if form := p.form(name); form != nil {
		p.writeForm(b, form, true)
		return true
	}

	if t := p.Type(name); t != nil {
		p.writeType(b, t, false)
		return true
	}

	if fns := p.funcsNamed(name); len(fns) > 0 {
		for _, fn := range fns {
			p.writeFunc(b, fn, true) // builtin has a method of a name on several types, Len on a string, a slice and a map
		}

		return true
	}

	if group := p.ValuesOf(name); group != nil {
		p.writeValues(b, group, true)
		return true
	}

	return false
}

// writeExamples writes the examples of a name, "" for the package's:
// each headed with its suffix when it has one, its code, and what it
// prints.
func (p *Package) writeExamples(b *strings.Builder, name string) {
	for _, e := range p.Examples[name] {
		put(b, lineSep, exampleHead)
		if e.Suffix != "" {
			put(b, wordSep, "(", e.Suffix, ")")
		}

		put(b, ":", lineSep)
		writeIndented(b, e.Code)
		if e.Output != "" {
			put(b, lineSep, indent, outputHead, lineSep)
			writeIndented(b, e.Output)
		}
	}
}

// writeIndented writes text's lines, each after indent, a blank one
// with none.
func writeIndented(b *strings.Builder, text string) {
	for line := range strings.SplitSeq(text, lineSep) {
		if line != "" {
			put(b, indent, line)
		}

		b.WriteString(lineSep)
	}
}

func (p *Package) writeMember(b *strings.Builder, owner, member string) bool {
	t := p.Type(owner)
	if t == nil {
		return false
	}

	for _, fn := range append(slices.Clone(t.Funcs), t.Methods...) { // its makers are the package's, not its members
		if fn.Decl.Name.Name == member {
			p.writeFunc(b, fn, true)
			return true
		}
	}

	for _, m := range p.shownMembers(t) {
		if m.name == member {
			p.writeShownMember(b, m)
			b.WriteString(lineSep)
			if doc := p.Comment(m.node); doc != "" {
				p.writeComment(b, doc, indent)
			}

			return true
		}
	}

	return false
}

// writeType writes a type's declaration, its comment, and what belongs
// to it: with their comments in the whole package's page, else as a
// list of signatures.
func (p *Package) writeType(b *strings.Builder, t *Type, inPackage bool) {
	p.WriteTypeDecl(b, t)
	b.WriteString(lineSep)
	if t.Doc != "" {
		p.writeComment(b, t.Doc, indent)
	}

	fns := t.functions()
	if !inPackage && len(fns) > 0 {
		b.WriteString(lineSep)
	}

	for _, fn := range fns {
		p.writeFunc(b, fn, inPackage)
	}

	if len(t.Implementors) > 0 {
		put(b, lineSep, implementedBy, lineSep)
		writeImplementors(b, t.Implementors, indent)
	}
}

// functions returns what belongs to a type in the order its page lists
// it: the functions that make it, its functions of the type, its
// methods.
func (t *Type) functions() []*Func {
	out := make([]*Func, 0, len(t.Makers)+len(t.Funcs)+len(t.Methods))
	out = append(out, t.Makers...)
	out = append(out, t.Funcs...)
	return append(out, t.Methods...)
}

// writeForm writes a form as written; with its comment, after a blank
// line, when withDoc asks.
func (p *Package) writeForm(b *strings.Builder, form *Form, withDoc bool) {
	if withDoc {
		b.WriteString(lineSep)
	}

	put(b, form.Written, lineSep)
	if withDoc {
		p.writeComment(b, form.Doc, indent)
	}
}

// form returns the form of the builtin package named name, by its
// name, map, or as its comment writes it, map[K]V; nil for none.
func (p *Package) form(name string) *Form {
	for _, form := range p.Forms {
		if name == form.Name || name == form.Written {
			return form
		}
	}

	return nil
}

// writeFunc writes a function's signature; with its comment, after a
// blank line, when withDoc asks.
func (p *Package) writeFunc(b *strings.Builder, fn *Func, withDoc bool) {
	if withDoc {
		b.WriteString(lineSep)
	}

	ast.WriteSignature(b, fn.Decl)
	b.WriteString(lineSep)
	if withDoc && fn.Doc != "" {
		p.writeComment(b, fn.Doc, indent)
	}
}

func (p *Package) writeValues(b *strings.Builder, group *Values, withDoc bool) {
	b.WriteString(lineSep)
	for _, d := range group.Decls {
		p.WriteValueDecl(b, d)
		if c := p.LineComment(d); c != "" && withDoc {
			put(b, lineNote, c)
		}

		b.WriteString(lineSep)
	}

	if withDoc && group.Doc != "" {
		p.writeComment(b, group.Doc, indent)
	}
}

// writeComment writes a comment's blocks, each line after prefix, the
// links that name something shown without their brackets.
func (p *Package) writeComment(b *strings.Builder, text, prefix string) {
	for i, block := range Parse(text) {
		if i > 0 {
			b.WriteString(lineSep)
		}

		switch block := block.(type) {
		case *Paragraph:
			for _, line := range block.Lines {
				b.WriteString(prefix)
				p.writeLinkText(b, line)
				b.WriteString(lineSep)
			}
		case *Heading:
			put(b, prefix, headingLead)
			p.writeLinkText(b, block.Text)
			b.WriteString(lineSep)
		case *List:
			for _, item := range block.Items {
				for j, line := range item.Lines {
					put(b, prefix, itemLead)
					if j == 0 {
						put(b, item.Marker, wordSep)
					} else {
						for range len(item.Marker) + len(wordSep) { // under the item's text
							b.WriteString(wordSep)
						}
					}

					p.writeLinkText(b, line)
					b.WriteString(lineSep)
				}
			}
		case *Code:
			for _, line := range block.Lines {
				if line != "" {
					put(b, prefix, codeIndent, line)
				}

				b.WriteString(lineSep)
			}
		}
	}
}

// writeLinkText writes a line with the brackets of its links that
// resolve taken off, and those that do not left as written.
func (p *Package) writeLinkText(b *strings.Builder, line string) {
	for _, s := range Spans(line) {
		if s.Name != "" && p.Resolve(s.Name) {
			b.WriteString(s.Name)
		} else {
			b.WriteString(s.Text)
		}
	}
}

// WriteTypeHeader writes a type's declaration up to its body: the
// keyword, its name and type parameters, what it implements or refines,
// and its attributes; a defined type whole.
func (p *Package) WriteTypeHeader(b *strings.Builder, t *Type) {
	switch d := t.Decl.(type) {
	case *ast.TypeDecl:
		switch d.Def.(type) {
		case *ast.StructType:
			b.WriteString("struct ")
		case *ast.EnumType:
			b.WriteString("enum ")
		default:
			put(b, "type ", d.Name.Name)
			ast.WriteTypeParams(b, d.TypeParams)
			b.WriteString(wordSep)
			ast.WriteType(b, d.Def)
			return
		}

		b.WriteString(d.Name.Name)
		ast.WriteTypeParams(b, d.TypeParams)
		if len(d.Impls) > 0 {
			b.WriteString(" impl ")
			writeTypeList(b, d.Impls)
		}

		p.writeAttrs(b, d, true)
	case *ast.TraitDecl:
		put(b, "trait ", d.Name.Name)
		ast.WriteTypeParams(b, d.TypeParams)
		if len(d.Refines) > 0 {
			b.WriteString(": ")
			writeTypeList(b, d.Refines)
		}
	}
}

// WriteTypeDecl writes a type's whole declaration as its page shows it:
// a struct with its shown fields, an enum with its variants, a trait
// with its fields and methods, each member with its comment and line
// comment.
func (p *Package) WriteTypeDecl(b *strings.Builder, t *Type) {
	p.WriteTypeHeader(b, t)
	if d, ok := t.Decl.(*ast.TypeDecl); ok {
		switch d.Def.(type) {
		case *ast.StructType, *ast.EnumType:
		default:
			return // a defined type has no body
		}
	}

	members, hidden := p.shownMembers(t), p.hiddenNote(t)
	if len(members) == 0 && hidden == "" {
		b.WriteString(" {}")
		return
	}

	put(b, " {", lineSep)
	for _, m := range members {
		if doc := p.Comment(m.node); doc != "" {
			for line := range strings.SplitSeq(strings.TrimRight(doc, lineSep), lineSep) {
				put(b, indent, commentMark)
				if line != "" {
					put(b, wordSep, line)
				}

				b.WriteString(lineSep)
			}
		}

		b.WriteString(indent)
		p.writeShownMember(b, m)
		if c := p.LineComment(m.node); c != "" {
			put(b, lineNote, c)
		}

		b.WriteString(lineSep)
	}

	if hidden != "" {
		put(b, indent, hidden, lineSep)
	}

	b.WriteString("}")
}

// WriteValueDecl writes a constant's or a variable's declaration as
// written.
func (p *Package) WriteValueDecl(b *strings.Builder, d *ast.VarDecl) {
	if d.Const {
		b.WriteString("const ")
	} else {
		b.WriteString("var ")
	}

	for i, n := range d.Names {
		if i > 0 {
			b.WriteString(nameSep)
		}

		b.WriteString(n.Name)
	}

	if d.Type != nil {
		b.WriteString(wordSep)
		ast.WriteType(b, d.Type)
	}

	if len(d.Values) > 0 {
		b.WriteString(" = ")
		for i, v := range d.Values {
			if i > 0 {
				b.WriteString(nameSep)
			}

			ast.WriteExpr(b, v)
		}
	}
}

// A shownMember is a field, a variant or a trait's method a type's
// declaration shows: its name, the first of a field's shown names or an
// embedded field's type, and its node.
type shownMember struct {
	name string
	node ast.Node
}

// shownMembers returns what a type's declaration shows inside its body:
// the shown fields of a struct, the variants of an enum, and the fields
// and methods of a trait.
func (p *Package) shownMembers(t *Type) []shownMember {
	var out []shownMember
	fields := func(fs []*ast.Field) {
		for _, f := range fs {
			if name, ok := p.fieldName(f); ok {
				out = append(out, shownMember{name, f})
			}
		}
	}

	switch d := t.Decl.(type) {
	case *ast.TypeDecl:
		switch def := d.Def.(type) {
		case *ast.StructType:
			fields(def.Fields)
		case *ast.EnumType:
			for _, v := range def.Variants {
				if p.Shown(v.Name.Name) {
					out = append(out, shownMember{v.Name.Name, v})
				}
			}
		}
	case *ast.TraitDecl:
		fields(d.Fields)
		for _, m := range d.Methods {
			if p.Shown(m.Name.Name) {
				out = append(out, shownMember{m.Name.Name, m})
			}
		}
	}

	return out
}

// fieldName returns the name a field is shown and looked up by: its
// first shown name, or an embedded field's type; ok is false when none
// of its names is shown.
func (p *Package) fieldName(f *ast.Field) (string, bool) {
	if len(f.Names) == 0 { // embedded: named by its type, shown as the type is
		name := ast.SourceType(f.Type)
		return name, p.Shown(name[strings.LastIndexByte(name, memberSep)+1:])
	}

	for _, n := range f.Names {
		if p.Shown(n.Name) {
			return n.Name, true
		}
	}

	return "", false
}

// writeShownMember writes a member as written: a field with its shown
// names, its type, its defaults and its attributes; a variant with its
// fields and attributes; a trait's method's signature.
func (p *Package) writeShownMember(b *strings.Builder, m shownMember) {
	switch n := m.node.(type) {
	case *ast.Field:
		p.writeField(b, n)
	case *ast.Variant:
		b.WriteString(n.Name.Name)
		if len(n.Fields) > 0 {
			b.WriteString("(")
			ast.WriteParams(b, n.Fields)
			b.WriteString(")")
		}

		p.writeAttrs(b, n, false)
	case *ast.FuncDecl:
		ast.WriteSignature(b, n)
	}
}

func (p *Package) writeField(b *strings.Builder, f *ast.Field) {
	first := true
	for _, n := range f.Names {
		if p.Shown(n.Name) {
			if !first {
				b.WriteString(nameSep)
			}

			b.WriteString(n.Name)
			first = false
		}
	}

	if len(f.Names) > 0 {
		b.WriteString(wordSep)
	}

	ast.WriteType(b, f.Type)
	if f.Defaults != nil {
		first = true
		for i, n := range f.Names {
			if !p.Shown(n.Name) || i >= len(f.Defaults.Values) {
				continue
			}

			if first {
				b.WriteString(" = ")
			} else {
				b.WriteString(nameSep)
			}

			ast.WriteExpr(b, f.Defaults.Values[i])
			first = false
		}
	}

	p.writeAttrs(b, f, false)
}

// hiddenNote returns the note a type's body ends with when its page
// does not show all of it: a struct's or a trait's fields, a trait's
// methods; "" when it shows everything.
func (p *Package) hiddenNote(t *Type) string {
	var fields []*ast.Field
	switch d := t.Decl.(type) {
	case *ast.TypeDecl:
		if def, ok := d.Def.(*ast.StructType); ok {
			fields = def.Fields
		}
	case *ast.TraitDecl:
		for _, m := range d.Methods {
			if !p.Shown(m.Name.Name) {
				return hiddenMethods
			}
		}

		fields = d.Fields
	}

	for _, f := range fields {
		for _, n := range f.Names {
			if !p.Shown(n.Name) {
				return hiddenFields
			}
		}
	}

	return ""
}

// writeAttrs writes the attributes on a node, each after a space;
// before a body, a composite literal goes in parentheses, as written
// there.
func (p *Package) writeAttrs(b *strings.Builder, n ast.Node, beforeBody bool) {
	for _, a := range p.Attrs(n) {
		b.WriteString(attrMark)
		_, literal := a.X.(*ast.CompositeLit)
		if literal && beforeBody {
			b.WriteString("(")
		}

		ast.WriteExpr(b, a.X)
		if literal && beforeBody {
			b.WriteString(")")
		}
	}
}

func writeTypeList(b *strings.Builder, ts []ast.Type) {
	for i, t := range ts {
		if i > 0 {
			b.WriteString(nameSep)
		}

		ast.WriteType(b, t)
	}
}

// writeImplementors writes a trait's implementors parted by commas,
// each as its package's path and its name, on lines of at most
// lineWidth after prefix.
func writeImplementors(b *strings.Builder, impls []Implementor, prefix string) {
	b.WriteString(prefix)
	width := len(prefix)
	for i, impl := range impls {
		size := len(impl.Name)
		if impl.Path != "" {
			size += len(impl.Path) + 1 // and its dot
		}

		if i < len(impls)-1 {
			size++ // its comma
		}

		if width > len(prefix) && width+len(wordSep)+size > lineWidth {
			put(b, lineSep, prefix)
			width = len(prefix)
		} else if width > len(prefix) {
			b.WriteString(wordSep)
			width += len(wordSep)
		}

		if impl.Path != "" {
			put(b, impl.Path, string(memberSep))
		}

		b.WriteString(impl.Name)
		if i < len(impls)-1 {
			b.WriteString(",")
		}

		width += size
	}

	b.WriteString(lineSep)
}

// put writes each of parts to b in turn, joining nothing, so that a
// line is written in its pieces and no piece is made to be copied.
func put(b *strings.Builder, parts ...string) {
	for _, s := range parts {
		b.WriteString(s)
	}
}
