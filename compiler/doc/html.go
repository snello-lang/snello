// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	_ "embed"
	"encoding/json"
	"html/template"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/snello-lang/snello/compiler/ast"
)

// Where a page lives and how it links: the route of a package's page
// and of the files it loads, what begins an anchor, the end of a
// package's first sentence, the name the pages are of, the titles of a
// page's sections and their anchors, which hold a hyphen so that no
// name's anchor is one, and the kinds of a comment's blocks.
const (
	PackageRoute   = "/pkg/"
	AssetRoute     = "/assets/"
	anchorMark     = "#"
	sentenceEnd    = ". "
	siteName       = "Snello"
	indexPageTitle = "Snello Documentation"
	overviewTitle  = "Overview"
	topicsTitle    = "Topics"
	typesTitle     = "Types"
	funcsTitle     = "Functions"
	valuesTitle    = "Constants and variables"
	overviewAnchor = "section-overview"
	topicsAnchor   = "section-topics"
	typesAnchor    = "section-types"
	funcsAnchor    = "section-functions"
	valuesAnchor   = "section-values"
	paragraphKind  = "p"
	headingKind    = "h"
	listKind       = "ul"
	codeKind       = "pre"
)

// The kinds of a name, which the sidebar, the topics and the search
// show it with, as the keyword that declares it: the style gives each
// its badge.
const (
	kindPackage = "package"
	kindStruct  = "struct"
	kindEnum    = "enum"
	kindTrait   = "trait"
	kindType    = "type"
	kindFunc    = "func"
	kindMethod  = "method"
	kindConst   = "const"
	kindVar     = "var"
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
	Names    []Name
}

// A Name is a name of a package as the topics, the sidebar and the
// search list it: its text, its anchor's link, its comment's first
// sentence, and its kind, the keyword that declares it, "method" for a
// method.
type Name struct {
	Text, Href, Synopsis, Kind string
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
		Group         string // the title of the group the page is in, "" for the index
		Groups        []navGroupView
	}

	tocEntry struct {
		Text, Href string
		Sub        bool // a declaration under a section
	}

	navGroupView struct {
		Title    string
		Packages []navPackage
	}

	navPackage struct {
		Path, Href string
		Current    bool
		Names      []Name
	}

	indexGroupView struct {
		Title   string
		Entries []IndexEntry
	}

	pageView struct {
		Nav           navView
		Title, Clause string
		Path          string
		Abstract      []Link // the first sentence, with its links
		TOC           []tocEntry
		Doc           []blockView
		Examples      []exampleView
		Topics        []topicsView
		Sections      []sectionView
		Index         []indexGroupView // the index's
	}

	topicsView struct {
		Title string
		Rows  []Name
	}

	sectionView struct {
		Title, Anchor string
		Decls         []declView
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
		Kind  string // paragraphKind, headingKind, listKind or codeKind
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
	return IndexEntry{Path: p.Path, Synopsis: Synopsis(p.Doc), Href: PageHref(p.Path, ""), Names: p.shownNames()}
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
// model the terminal prints: its title and first sentence, its comment
// and examples as its overview, its names by kind with their first
// sentences as its topics, then each declaration as written, with its
// comment and its links, its examples, and a type's functions and
// methods and a trait's implementors under it.
func (p *Package) WriteHTML(w io.Writer, nav Nav) error {
	var clause strings.Builder
	put(&clause, "package ", p.Name)
	abstract := p.links([]string{firstSentence(p.Doc)})
	v := pageView{Nav: nav.view(p.Path, p.shownNames()), Title: p.Path, Clause: clause.String(), Path: p.Path, Abstract: abstract,
		Doc: withoutAbstract(p.blocks(p.Doc), abstract), Examples: p.examples("")}
	if v.Title == "" {
		v.Title = p.Name
	}

	if len(v.Doc) > 0 || len(v.Examples) > 0 {
		v.TOC = append(v.TOC, tocEntry{Text: overviewTitle, Href: anchorMark + overviewAnchor})
	}

	var types, funcs, values sectionView
	var typeRows, funcRows, valueRows []Name
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
		typeRows = append(typeRows, Name{t.Name, PageHref(p.Path, t.Name), Synopsis(t.Doc), t.kind()})
	}

	for _, fn := range p.Funcs {
		d := p.funcView(fn)
		funcs.Decls = append(funcs.Decls, d)
		funcRows = append(funcRows, Name{d.Anchor, PageHref(p.Path, d.Anchor), Synopsis(fn.Doc), kindFunc})
	}

	for _, group := range p.values() {
		var names []string
		for _, d := range group.Decls {
			for _, n := range d.Names {
				names = append(names, n.Name)
				valueRows = append(valueRows, Name{n.Name, PageHref(p.Path, n.Name), Synopsis(group.Doc), p.valueKind(group)})
			}
		}

		values.Decls = append(values.Decls, declView{Anchor: names[0], More: names[1:], Text: p.valuesText(group), Doc: p.blocks(group.Doc)})
	}

	for _, s := range []struct {
		title, anchor string
		rows          []Name
		section       sectionView
	}{{typesTitle, typesAnchor, typeRows, types}, {funcsTitle, funcsAnchor, funcRows, funcs}, {valuesTitle, valuesAnchor, valueRows, values}} {
		if len(s.rows) == 0 {
			continue
		}

		v.Topics = append(v.Topics, topicsView{s.title, s.rows})
		s.section.Title, s.section.Anchor = s.title, s.anchor
		v.Sections = append(v.Sections, s.section)
	}

	if len(v.Topics) > 0 {
		v.TOC = append(v.TOC, tocEntry{Text: topicsTitle, Href: anchorMark + topicsAnchor})
	}

	for _, section := range v.Sections {
		v.TOC = append(v.TOC, tocEntry{Text: section.Title, Href: anchorMark + section.Anchor})
		for _, d := range section.Decls {
			v.TOC = append(v.TOC, tocEntry{d.Anchor, anchorMark + d.Anchor, true})
		}
	}

	return pages().ExecuteTemplate(w, "package", v)
}

// view returns the frame of a page: every group that has packages, as
// the sidebar's tree, the current package open with its names under
// it, and the title of the group it is in.
func (n Nav) view(current string, names []Name) navView {
	v := navView{Site: siteName, Version: n.Version}
	for _, g := range n.Groups {
		if len(g.Paths) == 0 {
			continue
		}

		group := navGroupView{Title: g.Title}
		for _, path := range g.Paths {
			pkg := navPackage{Path: path, Href: PageHref(path, ""), Current: path == current}
			if pkg.Current {
				pkg.Names = names
				v.Group = g.Title
			}

			group.Packages = append(group.Packages, pkg)
		}

		v.Groups = append(v.Groups, group)
	}

	return v
}

// shownNames returns the package's shown names as the sidebar and the
// search list them: its types, each with its functions and methods,
// then its functions, constants and variables.
func (p *Package) shownNames() []Name {
	var out []Name
	add := func(name, doc, kind string) {
		out = append(out, Name{name, PageHref(p.Path, name), Synopsis(doc), kind})
	}
	for _, t := range p.Types {
		add(t.Name, t.Doc, t.kind())
		for _, fn := range t.functions() {
			add(funcAnchor(fn.Decl), fn.Doc, funcKind(fn.Decl))
		}
	}

	for _, fn := range p.Funcs {
		add(funcAnchor(fn.Decl), fn.Doc, kindFunc)
	}

	for _, group := range p.values() {
		for _, d := range group.Decls {
			for _, n := range d.Names {
				if p.Shown(n.Name) {
					add(n.Name, group.Doc, p.valueKind(group))
				}
			}
		}
	}

	return out
}

// values returns the package's groups of constants, then of variables.
func (p *Package) values() []*Values {
	return append(append([]*Values{}, p.Consts...), p.Vars...)
}

// valueKind returns the kind of a group of the package's, a constant's
// or a variable's.
func (p *Package) valueKind(group *Values) string {
	if slices.Contains(p.Consts, group) {
		return kindConst
	}

	return kindVar
}

// kind returns the keyword that declares the type: struct, enum, trait,
// or type for any other.
func (t *Type) kind() string {
	switch d := t.Decl.(type) {
	case *ast.TraitDecl:
		return kindTrait
	case *ast.TypeDecl:
		switch d.Def.(type) {
		case *ast.StructType:
			return kindStruct
		case *ast.EnumType:
			return kindEnum
		}
	}

	return kindType
}

// funcKind returns a function's kind: a method, or a function, one of
// a type's among them.
func funcKind(fn *ast.FuncDecl) string {
	if fn.Recv != nil {
		return kindMethod
	}

	return kindFunc
}

// A searchItem is one entry of the search: a package or a name, with
// its page, its first sentence and its kind, in short keys, since the
// search loads every one.
type searchItem struct {
	Text     string `json:"t"`
	Href     string `json:"h"`
	Synopsis string `json:"s,omitempty"`
	Kind     string `json:"k"`
}

// WriteSearch writes what the filter looks through, as JSON: every
// package and every shown name of each, with its page, its first
// sentence and its kind.
func WriteSearch(w io.Writer, entries []IndexEntry) error {
	var items []searchItem
	for _, e := range entries {
		items = append(items, searchItem{e.Path, e.Href, e.Synopsis, kindPackage})
		for _, n := range e.Names {
			var text strings.Builder
			put(&text, e.Path, string(memberSep), n.Text)
			items = append(items, searchItem{text.String(), n.Href, n.Synopsis, n.Kind})
		}
	}

	return json.NewEncoder(w).Encode(items)
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
			out = append(out, blockView{Kind: paragraphKind, Spans: p.links(block.Lines)})
		case *Heading:
			out = append(out, blockView{Kind: headingKind, Spans: p.links([]string{block.Text})})
		case *List:
			view := blockView{Kind: listKind}
			for _, item := range block.Items {
				view.Items = append(view.Items, p.links(item.Lines))
			}

			out = append(out, view)
		case *Code:
			out = append(out, blockView{Kind: codeKind, Code: strings.Join(block.Lines, lineSep)})
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

// withoutAbstract returns a comment's blocks without its first
// sentence, which the page shows under its title: the first paragraph
// when that sentence is all of it.
func withoutAbstract(blocks []blockView, abstract []Link) []blockView {
	if len(blocks) == 0 || blocks[0].Kind != paragraphKind || len(abstract) == 0 {
		return blocks
	}

	if flowText(blocks[0].Spans) != flowText(abstract) {
		return blocks
	}

	return blocks[1:]
}

// flowText returns a flow of text and links as its words read, one
// space between each two.
func flowText(flow []Link) string {
	var text strings.Builder
	for _, s := range flow {
		text.WriteString(s.Text)
	}

	return strings.Join(strings.Fields(text.String()), wordSep)
}

// Synopsis returns a comment's first sentence as plain text, a link's
// name without its brackets.
func Synopsis(doc string) string {
	var text strings.Builder
	for _, s := range Spans(firstSentence(doc)) {
		if s.Name != "" {
			text.WriteString(s.Name)
		} else {
			text.WriteString(s.Text)
		}
	}

	return text.String()
}

// firstSentence returns a comment's first sentence as written, up to
// its first period and space, or its first paragraph when it has none,
// on one line.
func firstSentence(doc string) string {
	first, _, _ := strings.Cut(doc, lineSep+lineSep)
	first = strings.Join(strings.Fields(first), wordSep)
	if i := strings.Index(first, sentenceEnd); i >= 0 {
		return first[:i+1]
	}

	return first
}
