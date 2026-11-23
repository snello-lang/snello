// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package doc builds what snello doc shows of a package: its comment,
// its constants and variables in the groups they are written in, its
// functions, and its types with the functions that make them, their
// functions of the type and their methods, from the package's syntax
// parsed with its comments and the checker's view of it. The links its
// comments hold are resolved, and those that name nothing are reported.
package doc

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// resultType is the name of the predeclared Result, whose first
// argument a function that makes a type returns.
const resultType = "Result"

// The names of the forms the language writes with its own syntax, which
// the builtin package documents: snello doc builtin map asks for one,
// and a page links to it by it.
const (
	FormSlice   = "slice"
	FormArray   = "array"
	FormMap     = "map"
	FormChan    = "chan"
	FormPointer = "pointer"
	FormRef     = "ref"
	FormMut     = "mut"
)

// builtinForms are the forms the builtin package documents, each by its
// name and the text its comment begins with, and formMark the text a
// comment's line begins with, before what the comment says.
var (
	builtinForms = []struct{ name, written string }{
		{FormSlice, "[]T"},
		{FormArray, "[N]T"},
		{FormMap, "map[K]V"},
		{FormChan, "chan T"},
		{FormPointer, "*T"},
		{FormRef, "ref T"},
		{FormMut, "mut T"},
	}
	formMark = len("// ")
)

// A Source is a package as loaded for its documentation.
type Source struct {
	Path       string                           // its import path; "builtin" for the predeclared names, "" for a program's main
	Files      []File                           // its files, parsed with their comments
	Pkg        *types.Package                   // the checked package; nil for builtin, which is parsed and never checked
	Import     func(path string) *types.Package // loads the package at path, which a link may name, or nil when none has it
	Tests      []TestFile                       // its test files, parsed with their comments, whose examples its pages show
	Unexported bool                             // the unexported names too
}

// A File is one source file of a package: its syntax and its positions.
type File struct {
	Syntax *ast.File
	Tok    *token.File
}

// A TestFile is a test file of a package, with its source, which an
// example's code is cut from.
type TestFile struct {
	Syntax *ast.File
	Tok    *token.File
	Src    []byte
}

// A Package is what snello doc shows of a package.
type Package struct {
	Name     string
	Path     string
	Doc      string
	Forms    []*Form // the forms the builtin package documents
	Consts   []*Values
	Vars     []*Values
	Funcs    []*Func
	Types    []*Type
	Examples map[string][]*Example // by the name each documents: "" for the package, Area, Point.Moved
	Broken   []Broken              // the links that name nothing

	src     Source
	lead    map[ast.Node]*ast.CommentGroup
	line    map[ast.Node]*ast.CommentGroup
	attrs   map[ast.Node][]*ast.Attr
	names   map[string]string          // the package names its files import, to their paths
	decls   map[string]ast.Decl        // every top-level name, shown or not
	members map[string]map[string]bool // every member of each type: its fields, variants, methods and functions of the type
}

// A Form is a type or a borrow the language writes with its own
// syntax, map[K]V or ref T, which no declaration can name, as the
// builtin package documents it: by its name, the form as its comment
// begins with it, the comment, and where the form is written in the
// comment.
type Form struct {
	Name, Written, Doc string
	Pos                token.Pos
}

// Values is a group of constants or variables, written on consecutive
// lines: the comment above the first is the group's.
type Values struct {
	Doc   string
	Decls []*ast.VarDecl
}

// A Func is a function or a method with its comment.
type Func struct {
	Decl *ast.FuncDecl
	Doc  string
}

// A Type is a struct, an enum, a defined type or a trait, with what
// belongs to it.
type Type struct {
	Name         string
	Decl         ast.Decl // an *ast.TypeDecl or an *ast.TraitDecl
	Doc          string
	Makers       []*Func       // the package's functions that return it
	Funcs        []*Func       // its functions of the type, func Point.Parse
	Methods      []*Func       // its methods
	Implementors []Implementor // for a trait, the types that implement it; the caller's to fill
}

// A Broken is a link that names nothing: where its comment is and what
// it names.
type Broken struct {
	Pos  token.Position
	Name string
}

// New builds a package's documentation from its files.
func New(src Source) *Package {
	p := &Package{Path: src.Path, src: src, Examples: map[string][]*Example{}, lead: map[ast.Node]*ast.CommentGroup{}, line: map[ast.Node]*ast.CommentGroup{},
		attrs: map[ast.Node][]*ast.Attr{}, names: map[string]string{}, decls: map[string]ast.Decl{}, members: map[string]map[string]bool{}}
	var docs []string
	for _, f := range src.Files {
		p.Name = f.Syntax.Name.Name
		if d := f.Syntax.Docs; d != nil {
			maps.Copy(p.lead, d.Lead)
			maps.Copy(p.line, d.Line)
		}

		maps.Copy(p.attrs, f.Syntax.Attrs)
		for _, im := range f.Syntax.Imports {
			path := strings.Trim(im.Path.Value, "\"`")
			name := path[strings.LastIndexByte(path, '/')+1:]
			if im.Name != nil {
				name = im.Name.Name
			}

			p.names[name] = path
		}

		if g := p.lead[f.Syntax]; g != nil {
			docs = append(docs, g.Text())
		}
	}

	p.Doc = strings.Join(docs, lineSep)
	if src.Path == BuiltinPath {
		for _, f := range src.Files {
			p.Forms = append(p.Forms, Forms(f.Syntax)...)
		}
	}
	byName := map[string]*Type{}
	var funcs []*Func
	for _, f := range src.Files {
		var group *Values
		groupLine := 0
		for _, d := range f.Syntax.Decls {
			v, isValue := d.(*ast.VarDecl)
			if !isValue {
				group = nil
			}

			switch d := d.(type) {
			case *ast.VarDecl:
				line := f.Tok.Position(d.Pos()).Line
				if group == nil || group.Decls[0].Const != v.Const || line != groupLine+1 {
					group = &Values{Doc: p.text(d)}
					if v.Const {
						p.Consts = append(p.Consts, group)
					} else {
						p.Vars = append(p.Vars, group)
					}
				}

				group.Decls = append(group.Decls, d)
				groupLine = line
				for _, n := range d.Names {
					p.decls[n.Name] = d
				}
			case *ast.FuncDecl:
				funcs = append(funcs, &Func{d, p.text(d)})
				switch {
				case d.Recv != nil:
					p.member(baseName(d.Recv.Type), d.Name.Name)
				case d.Of != nil:
					p.member(d.Of.Name.Name, d.Name.Name)
				default:
					p.decls[d.Name.Name] = d
				}
			case *ast.TypeDecl:
				byName[d.Name.Name] = &Type{Name: d.Name.Name, Decl: d, Doc: p.text(d)}
				p.decls[d.Name.Name] = d
				switch def := d.Def.(type) {
				case *ast.StructType:
					for _, field := range def.Fields {
						for _, n := range field.Names {
							p.member(d.Name.Name, n.Name)
						}
					}
				case *ast.EnumType:
					for _, v := range def.Variants {
						p.member(d.Name.Name, v.Name.Name)
					}
				}
			case *ast.TraitDecl:
				byName[d.Name.Name] = &Type{Name: d.Name.Name, Decl: d, Doc: p.text(d)}
				p.decls[d.Name.Name] = d
				for _, field := range d.Fields {
					for _, n := range field.Names {
						p.member(d.Name.Name, n.Name)
					}
				}

				for _, m := range d.Methods {
					p.member(d.Name.Name, m.Name.Name)
				}
			}
		}
	}

	for _, fn := range funcs {
		owner := ""
		switch {
		case fn.Decl.Recv != nil:
			owner = baseName(fn.Decl.Recv.Type)
		case fn.Decl.Of != nil:
			owner = fn.Decl.Of.Name.Name
		}

		if !p.Shown(fn.Decl.Name.Name) {
			continue
		}

		t := byName[owner]
		switch {
		case owner != "" && t != nil && p.Shown(owner):
			if fn.Decl.Recv != nil {
				t.Methods = append(t.Methods, fn)
			} else {
				t.Funcs = append(t.Funcs, fn)
			}
		case owner != "":
		case fn.Decl.Recv != nil || fn.Decl.Of != nil:
			p.Funcs = append(p.Funcs, fn) // a method of a type with no name, a slice's in builtin
		case len(fn.Decl.Results) > 0 && byName[made(fn.Decl.Results[0])] != nil && p.Shown(made(fn.Decl.Results[0])):
			made := byName[made(fn.Decl.Results[0])]
			made.Makers = append(made.Makers, fn)
		default:
			p.Funcs = append(p.Funcs, fn)
		}
	}

	for name, t := range byName {
		if p.Shown(name) {
			p.Types = append(p.Types, t)
		}
	}

	byFuncName := func(a, b *Func) int { return cmp.Compare(a.Decl.Name.Name, b.Decl.Name.Name) }
	slices.SortFunc(p.Types, func(a, b *Type) int { return cmp.Compare(a.Name, b.Name) })
	slices.SortFunc(p.Funcs, byFuncName)
	for _, t := range p.Types {
		slices.SortFunc(t.Makers, byFuncName)
		slices.SortFunc(t.Funcs, byFuncName)
		slices.SortFunc(t.Methods, byFuncName)
	}

	p.Consts = p.shownValues(p.Consts)
	p.Vars = p.shownValues(p.Vars)
	p.addExamples(src.Tests)
	p.checkLinks()
	return p
}

// Forms returns the forms the file f documents: each of its comments
// attached to no declaration that begins with a form the language
// writes with its own syntax and a space, map[K]V is a map, in the
// order they are written.
func Forms(f *ast.File) []*Form {
	if f.Docs == nil {
		return nil
	}

	attached := make(map[*ast.CommentGroup]bool)
	for _, comments := range []map[ast.Node]*ast.CommentGroup{f.Docs.Lead, f.Docs.Line} {
		for _, g := range comments {
			attached[g] = true
		}
	}

	var forms []*Form
	for _, g := range f.Docs.Groups {
		if attached[g] {
			continue
		}

		text := g.Text()
		for _, form := range builtinForms {
			if strings.HasPrefix(text, form.written+" ") {
				forms = append(forms, &Form{Name: form.name, Written: form.written, Doc: text, Pos: g.Pos() + token.Pos(formMark)})
			}
		}
	}

	return forms
}

// member records that the type named owner has a member named name.
func (p *Package) member(owner, name string) {
	if p.members[owner] == nil {
		p.members[owner] = map[string]bool{}
	}

	p.members[owner][name] = true
}

// Shown reports whether a name is on the package's pages: an exported
// one, or any when the unexported are asked for; every name of builtin,
// whose predeclared names are written in lower case and are everyone's.
func (p *Package) Shown(name string) bool {
	return p.src.Unexported || p.Path == BuiltinPath || ast.IsExported(name)
}

// LineComment returns the comment at the end of a field's, a variant's,
// a constant's or a variable's line, or "".
func (p *Package) LineComment(n ast.Node) string {
	if g := p.line[n]; g != nil {
		return strings.TrimSpace(g.Text())
	}

	return ""
}

// Comment returns a field's, a variant's or a trait method's comment,
// or "".
func (p *Package) Comment(n ast.Node) string { return p.text(n) }

// Attrs returns the attributes written on a node.
func (p *Package) Attrs(n ast.Node) []*ast.Attr { return p.attrs[n] }

// Type returns the type of the package named name, or nil.
func (p *Package) Type(name string) *Type {
	for _, t := range p.Types {
		if t.Name == name {
			return t
		}
	}

	return nil
}

// Func returns the function of the package named name, among those not
// placed under a type and the makers of its types, or nil.
func (p *Package) Func(name string) *Func {
	if fns := p.funcsNamed(name); len(fns) > 0 {
		return fns[0]
	}

	return nil
}

// funcsNamed returns every function named name among the package's own
// and the makers of its types: several in builtin, a method of a name
// on each of several types with no name.
func (p *Package) funcsNamed(name string) []*Func {
	var out []*Func
	for _, fn := range p.Funcs {
		if fn.Decl.Name.Name == name {
			out = append(out, fn)
		}
	}

	for _, t := range p.Types {
		for _, fn := range t.Makers {
			if fn.Decl.Name.Name == name {
				out = append(out, fn)
			}
		}
	}

	return out
}

// ValuesOf returns the group that declares name, or nil.
func (p *Package) ValuesOf(name string) *Values {
	for _, group := range slices.Concat(p.Consts, p.Vars) {
		for _, d := range group.Decls {
			for _, n := range d.Names {
				if n.Name == name {
					return group
				}
			}
		}
	}

	return nil
}

func (p *Package) text(n ast.Node) string {
	if g := p.lead[n]; g != nil {
		return g.Text()
	}

	return ""
}

// shownValues keeps the groups that declare a name shown, each with the
// declarations that do.
func (p *Package) shownValues(groups []*Values) []*Values {
	var out []*Values
	for _, group := range groups {
		decls := slices.DeleteFunc(slices.Clone(group.Decls), func(d *ast.VarDecl) bool {
			return !slices.ContainsFunc(d.Names, func(n *ast.Ident) bool { return p.Shown(n.Name) })
		})
		if len(decls) > 0 {
			out = append(out, &Values{group.Doc, decls})
		}
	}

	return out
}

// baseName returns the name of the type a receiver is, through its
// mode: Builder for mut Builder.
func baseName(t ast.Type) string {
	switch t := t.(type) {
	case *ast.RefType:
		return baseName(t.Elem)
	case *ast.NamedType:
		if t.Pkg == nil {
			return t.Name.Name
		}
	}

	return ""
}

// made returns the name of the type of the package a function makes
// when it returns r: T, *T, T? or Result<T, E>.
func made(r ast.Type) string {
	switch r := r.(type) {
	case *ast.PointerType:
		return made(r.Elem)
	case *ast.OptionType:
		return made(r.Elem)
	case *ast.NamedType:
		if r.Pkg != nil {
			return ""
		}

		if r.Name.Name == resultType && len(r.Args) == 2 {
			return made(r.Args[0])
		}

		return r.Name.Name
	}

	return ""
}
