// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	_ "embed"
	"html/template"
	"io"
	"strings"
	"sync"

	"github.com/snello-lang/snello/compiler/ast"
)

// Where a page lives and how it links: the route of a package's page,
// what begins an anchor, the end of a package's first sentence, the
// name the pages are of, the titles of a page's sections, and how the
// icon is carried to a browser's tab.
const (
	PackageRoute   = "/pkg/"
	anchorMark     = "#"
	sentenceEnd    = ". "
	siteName       = "Snello"
	indexPageTitle = "Packages"
	typesTitle     = "Types"
	funcsTitle     = "Functions"
	valuesTitle    = "Constants and variables"
	AssetRoute     = "/assets/"
)

// The groups a sidebar and the index list the packages in, by where a
// package comes from: the language's predeclared names, the library the
// compiler carries, the libraries that ship beside it, the module's own
// packages, and the modules it requires.
const (
	LanguageGroup     = "Language"
	StandardGroup     = "Standard library"
	LibrariesGroup    = "Libraries"
	ModuleGroup       = "This module"
	DependenciesGroup = "Dependencies"
)

// The pages' files, built into the compiler: the templates, and the
// style, the script and the brand's icon, the S on its blue tile, which
// the server serves beside the pages under AssetRoute; the icon a copy
// of _docs/brand/icon.svg under the license header, which a test holds
// it to.
var (
	//go:embed assets/pages.html
	pageTemplates string

	//go:embed assets/style.css
	pageStyle string

	//go:embed assets/script.js
	pageScript string

	//go:embed assets/icon.svg
	icon string
)

// pages is the templates, parsed the first time a page is written, so
// that a command that writes none, every command but snello doc -http,
// does not parse them as it starts.
var pages = sync.OnceValue(func() *template.Template {
	return template.Must(template.New("pages").Parse(pageTemplates))
})

// A Nav is what every page's sidebar shows: the version of Snello and
// the packages the pages are served for, in their groups.
type Nav struct {
	Version string
	Groups  []NavGroup
}

// A NavGroup is the packages of one origin, LanguageGroup or one of the
// others, which the sidebar and the index list under its title; a group
// with no package is not listed.
type NavGroup struct {
	Title string
	Paths []string
}

// An IndexEntry is a package on the index: its path, its first
// sentence, its page, and its shown names, for the search.
type IndexEntry struct {
	Path     string
	Synopsis string
	Href     string
	Names    []Link
}

// A Link is a text and where it points.
type Link struct {
	Text string
	Href string
}

// The views a page is written from, which the templates escape.
type (
	navView struct {
		Site, Version string
		Groups        []navGroupView
	}

	navGroupView struct {
		Title    string
		Packages []navPackage
	}

	navPackage struct {
		Path, Href string
		Current    bool
		Names      []Link
	}

	indexGroupView struct {
		Title   string
		Entries []IndexEntry
	}

	pageView struct {
		Nav           navView
		Title, Clause string
		Path          string
		Doc           []blockView
		Examples      []exampleView
		Summary       []summaryView
		Sections      []sectionView
		Index         []indexGroupView // the index's
	}

	summaryView struct {
		Title string
		Rows  []summaryRow
	}

	summaryRow struct {
		Text, Href, Synopsis string
	}

	sectionView struct {
		Title string
		Decls []declView
	}

	declView struct {
		Anchor, Text string
		More         []string // the anchors of a group's other names
		Doc          []blockView
		Examples     []exampleView
		Members      []declView // a type's functions and methods
		Implementors []Link
	}

	blockView struct {
		Kind  string // "p", "h", "ul" or "pre"
		Spans []Link
		Items [][]Link
		Code  string
	}

	exampleView struct {
		Suffix, Code, Output string
	}
)

// An asset is a file the pages load beside them: its content and its
// type.
type asset struct {
	content     string
	contentType string
}

// assets are the files the server serves under AssetRoute, by name.
var assets = map[string]asset{
	"style.css": {pageStyle, "text/css; charset=utf-8"},
	"script.js": {pageScript, "text/javascript; charset=utf-8"},
	"icon.svg":  {icon, "image/svg+xml"},
}

// Asset returns the file the pages load at AssetRoute and name, and its
// type; ok is false for a name the pages have none of.
func Asset(name string) (content, contentType string, ok bool) {
	a, ok := assets[name]
	return a.content, a.contentType, ok
}

// PageHref returns where the page of the package at path is, with the
// anchor of a name on it when anchor is not "".
func PageHref(path, anchor string) string {
	var b strings.Builder
	put(&b, PackageRoute, path)
	if anchor != "" {
		put(&b, anchorMark, anchor)
	}

	return b.String()
}

// Index returns the package's entry on the index: its path, its first
// sentence and its shown names.
func (p *Package) Index() IndexEntry {
	e := IndexEntry{Path: p.Path, Synopsis: Synopsis(p.Doc), Href: PageHref(p.Path, "")}
	for _, row := range p.shownNames() {
		e.Names = append(e.Names, Link{row.Text, row.Href})
	}

	return e
}

// WriteIndex writes the index of the packages, each under nav's group,
// with nav's sidebar.
func WriteIndex(w io.Writer, nav Nav, entries []IndexEntry) error {
	byPath := map[string]IndexEntry{}
	for _, e := range entries {
		byPath[e.Path] = e
	}

	v := pageView{Nav: nav.view("", nil), Title: indexPageTitle}
	for _, g := range nav.Groups {
		group := indexGroupView{Title: g.Title}
		for _, path := range g.Paths {
			if e, ok := byPath[path]; ok {
				group.Entries = append(group.Entries, e)
			}
		}

		if len(group.Entries) > 0 {
			v.Index = append(v.Index, group)
		}
	}

	return pages().ExecuteTemplate(w, "index", v)
}

// WriteHTML writes the package's page, with nav's sidebar, from the
// model the terminal prints: a summary of its names with their first
// sentences, then each declaration as written under a header bar, with
// its comment and its links, its examples, and a type's functions and
// methods and a trait's implementors under it.
func (p *Package) WriteHTML(w io.Writer, nav Nav) error {
	var clause strings.Builder
	put(&clause, "package ", p.Name)
	v := pageView{Nav: nav.view(p.Path, p.shownNames()), Title: p.Path, Clause: clause.String(), Path: p.Path, Doc: p.blocks(p.Doc), Examples: p.examples("")}
	if v.Title == "" {
		v.Title = p.Name
	}

	var types, funcs, values sectionView
	var typeRows, funcRows, valueRows []summaryRow
	for _, t := range p.Types {
		var b strings.Builder
		p.WriteTypeDecl(&b, t)
		d := declView{Anchor: t.Name, Text: b.String(), Doc: p.blocks(t.Doc), Examples: p.examples(t.Name)}
		for _, fn := range t.functions() {
			d.Members = append(d.Members, p.funcView(fn))
		}

		for _, impl := range t.Implementors {
			d.Implementors = append(d.Implementors, implementorLink(impl))
		}

		types.Decls = append(types.Decls, d)
		typeRows = append(typeRows, summaryRow{t.Name, PageHref(p.Path, t.Name), Synopsis(t.Doc)})
	}

	for _, fn := range p.Funcs {
		d := p.funcView(fn)
		funcs.Decls = append(funcs.Decls, d)
		funcRows = append(funcRows, summaryRow{d.Anchor, PageHref(p.Path, d.Anchor), Synopsis(fn.Doc)})
	}

	for _, group := range append(append([]*Values{}, p.Consts...), p.Vars...) {
		var names []string
		for _, d := range group.Decls {
			for _, n := range d.Names {
				names = append(names, n.Name)
				valueRows = append(valueRows, summaryRow{n.Name, PageHref(p.Path, n.Name), Synopsis(group.Doc)})
			}
		}

		values.Decls = append(values.Decls, declView{Anchor: names[0], More: names[1:], Text: p.valuesText(group), Doc: p.blocks(group.Doc)})
	}

	for _, s := range []struct {
		title   string
		rows    []summaryRow
		section sectionView
	}{{typesTitle, typeRows, types}, {funcsTitle, funcRows, funcs}, {valuesTitle, valueRows, values}} {
		if len(s.rows) == 0 {
			continue
		}

		v.Summary = append(v.Summary, summaryView{s.title, s.rows})
		s.section.Title = s.title
		v.Sections = append(v.Sections, s.section)
	}

	return pages().ExecuteTemplate(w, "package", v)
}

// view returns the sidebar: every group that has packages, and in it
// every package, the current one with its names under it.
func (n Nav) view(current string, names []summaryRow) navView {
	v := navView{Site: siteName, Version: n.Version}
	for _, g := range n.Groups {
		if len(g.Paths) == 0 {
			continue
		}

		group := navGroupView{Title: g.Title}
		for _, path := range g.Paths {
			pkg := navPackage{Path: path, Href: PageHref(path, ""), Current: path == current}
			if pkg.Current {
				for _, row := range names {
					pkg.Names = append(pkg.Names, Link{row.Text, row.Href})
				}
			}

			group.Packages = append(group.Packages, pkg)
		}

		v.Groups = append(v.Groups, group)
	}

	return v
}

// shownNames returns the package's shown names as the sidebar and the index
// list them: its types, each with its functions and methods, then its
// functions, constants and variables.
func (p *Package) shownNames() []summaryRow {
	var out []summaryRow
	add := func(name string) { out = append(out, summaryRow{Text: name, Href: PageHref(p.Path, name)}) }
	for _, t := range p.Types {
		add(t.Name)
		for _, fn := range t.functions() {
			add(funcAnchor(fn.Decl))
		}
	}

	for _, fn := range p.Funcs {
		add(funcAnchor(fn.Decl))
	}

	for _, group := range append(append([]*Values{}, p.Consts...), p.Vars...) {
		for _, d := range group.Decls {
			for _, n := range d.Names {
				if p.Shown(n.Name) {
					add(n.Name)
				}
			}
		}
	}

	return out
}

// implementorLink returns a trait's implementor as a link to its page:
// its package's path and its name, or a predeclared type's name alone.
func implementorLink(impl Implementor) Link {
	if impl.Path == "" {
		return Link{impl.Name, PageHref(BuiltinPath, impl.Name)}
	}

	var text strings.Builder
	put(&text, impl.Path, string(memberSep), impl.Name)
	return Link{text.String(), PageHref(impl.Path, impl.Name)}
}

// anchorSafe writes a type with no name as an anchor holds it: a
// channel's direction as a word, and no spaces.
var anchorSafe = strings.NewReplacer("<-chan", "recv-chan", "chan<-", "send-chan", " ", "")

// funcAnchor returns a function's anchor on its package's page: its
// name; a method's or a function of a type's, its type's name and its
// own, Builder.Len; a method of a type with no name, as builtin has,
// the type as written, a channel's direction as a word and no spaces,
// and its name, []T.Push or recv-chanT.Clone.
func funcAnchor(fn *ast.FuncDecl) string {
	var b strings.Builder
	switch {
	case fn.Recv != nil:
		if owner := baseName(fn.Recv.Type); owner != "" {
			b.WriteString(owner)
		} else {
			t := fn.Recv.Type
			if r, ok := t.(*ast.RefType); ok {
				t = r.Elem
			}

			b.WriteString(anchorSafe.Replace(ast.SourceType(t)))
		}

		b.WriteByte(memberSep)
	case fn.Of != nil:
		put(&b, fn.Of.Name.Name, string(memberSep))
	}

	b.WriteString(fn.Name.Name)
	return b.String()
}

// valuesText returns a group of constants or variables as written, each
// with its line comment.
func (p *Package) valuesText(group *Values) string {
	var b strings.Builder
	for i, d := range group.Decls {
		if i > 0 {
			b.WriteString(lineSep)
		}

		p.WriteValueDecl(&b, d)
		if c := p.LineComment(d); c != "" {
			put(&b, lineNote, c)
		}
	}

	return b.String()
}

// funcView returns a function's declaration with its comment and its
// examples, at its anchor.
func (p *Package) funcView(fn *Func) declView {
	var sig strings.Builder
	ast.WriteSignature(&sig, fn.Decl)
	anchor := funcAnchor(fn.Decl)
	return declView{Anchor: anchor, Text: sig.String(), Doc: p.blocks(fn.Doc), Examples: p.examples(anchor)}
}

// examples returns the examples of a name, "" for the package's.
func (p *Package) examples(name string) []exampleView {
	var out []exampleView
	for _, e := range p.Examples[name] {
		out = append(out, exampleView{e.Suffix, e.Code, e.Output})
	}

	return out
}

// blocks returns a comment's blocks as a page shows them: paragraphs and
// list items joined into one flow each, their links pointing at the
// pages they name, and the links that name nothing left as written.
func (p *Package) blocks(text string) []blockView {
	var out []blockView
	for _, block := range Parse(text) {
		switch block := block.(type) {
		case *Paragraph:
			out = append(out, blockView{Kind: "p", Spans: p.links(block.Lines)})
		case *Heading:
			out = append(out, blockView{Kind: "h", Spans: p.links([]string{block.Text})})
		case *List:
			view := blockView{Kind: "ul"}
			for _, item := range block.Items {
				view.Items = append(view.Items, p.links(item.Lines))
			}

			out = append(out, view)
		case *Code:
			out = append(out, blockView{Kind: "pre", Code: strings.Join(block.Lines, lineSep)})
		}
	}

	return out
}

// links returns lines as one flow of text and links, a space between
// lines.
func (p *Package) links(lines []string) []Link {
	var out []Link
	for i, line := range lines {
		if i > 0 {
			out = append(out, Link{Text: wordSep})
		}

		for _, s := range Spans(line) {
			switch {
			case s.URL != "":
				out = append(out, Link{s.Text, s.URL})
			case s.Name != "":
				if path, anchor, ok := p.Target(s.Name); ok {
					out = append(out, Link{s.Name, PageHref(path, anchor)})
				} else {
					out = append(out, Link{Text: s.Text})
				}
			default:
				out = append(out, Link{Text: s.Text})
			}
		}
	}

	return out
}

// Synopsis returns a comment's first sentence, up to its first period
// and space, or its first paragraph when it has none.
func Synopsis(doc string) string {
	first, _, _ := strings.Cut(doc, lineSep+lineSep)
	first = strings.Join(strings.Fields(first), wordSep)
	if i := strings.Index(first, sentenceEnd); i >= 0 {
		return first[:i+1]
	}

	return first
}
