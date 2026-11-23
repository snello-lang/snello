// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/scanner"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/lsp/jsonrpc"
	"github.com/snello-lang/snello/lsp/protocol"
)

// memberSeparator joins a member's name to its type's where the type
// is not in the outline beside it.
const memberSeparator = "."

// An outlined is the outline of a file a search of the workspace read,
// as a list, each name in lower case beside it, and the stamp of the
// text it was read from: the open document's version, or the file's
// time and size on the disk.
type outlined struct {
	stamp   string
	symbols []protocol.SymbolInformation
	lower   []string
}

// A span is the stretch of a token, as offsets.
type span struct {
	start, end int
}

// An outliner writes the outline of one file: its syntax, its text,
// the stretches of its tokens, and the encoding positions are written
// in.
type outliner struct {
	file     *token.File
	src      []byte
	tokens   []span
	encoding string
}

// documentSymbol answers the outline of a document: its declarations,
// a type's members and methods under it, as a tree, or as a list with
// each member's type named for a client that shows no tree.
func (s *Server) documentSymbol(m *jsonrpc.Message) {
	var p protocol.DocumentSymbolParams
	if err := json.Unmarshal(m.Params, &p); err != nil {
		s.fail(m.ID, jsonrpc.InvalidParams, err.Error())
		return
	}

	path := pathOf(p.TextDocument.URI)
	var src []byte
	if d := s.docs[p.TextDocument.URI]; d != nil { // an open document, a file's or one never saved
		src = d.text
	} else {
		var err error
		if src, err = s.read(path); err != nil {
			s.fail(m.ID, jsonrpc.RequestFailed, err.Error())
			return
		}
	}

	symbols := outline(cmp.Or(path, string(p.TextDocument.URI)), src, s.encoding)
	if s.hierarchical {
		s.reply(m.ID, symbols)
		return
	}

	list := []protocol.SymbolInformation{}
	for _, sym := range symbols {
		list = flatten(list, sym, p.TextDocument.URI, "")
	}

	s.reply(m.ID, list)
}

// workspaceSymbol answers the declarations of every file of the
// workspace whose names hold the query's letters in order, whatever
// their case, the best matches first: a name that is the query, then
// one that begins with it, then one that holds it, then the shortest;
// every match is answered, and the editor shows what fits.
// The workspace is every package of its modules, and the package of an
// open document under no module. A file's outline is read again only
// when its text changed since the search before.
func (s *Server) workspaceSymbol(m *jsonrpc.Message) {
	var p protocol.WorkspaceSymbolParams
	if err := json.Unmarshal(m.Params, &p); err != nil {
		s.fail(m.ID, jsonrpc.InvalidParams, err.Error())
		return
	}

	type match struct {
		sym   protocol.SymbolInformation
		lower string
	}

	query := strings.ToLower(p.Query)
	var found []match
	outlines := make(map[string]*outlined)
	for _, dir := range s.workspacePackages() {
		for _, path := range importer.PackageFiles(dir) {
			o := s.outlineOf(path)
			if o == nil {
				continue
			}

			outlines[path] = o
			for i, sym := range o.symbols {
				if matches(o.lower[i], query) {
					found = append(found, match{sym, o.lower[i]})
				}
			}
		}
	}

	s.outlines = outlines // a file no longer in the workspace is forgotten
	slices.SortStableFunc(found, func(a, b match) int {
		return cmp.Or(
			cmp.Compare(rank(a.lower, query), rank(b.lower, query)),
			cmp.Compare(len(a.lower), len(b.lower)),
			strings.Compare(a.sym.Name, b.sym.Name),
		)
	})

	symbols := make([]protocol.SymbolInformation, 0, len(found))
	for _, f := range found {
		symbols = append(symbols, f.sym)
	}

	s.reply(m.ID, symbols)
}

// outlineOf returns the outline of the file at path as a list, from the
// search before when its text has not changed since, or read again;
// nil for a file that does not read.
func (s *Server) outlineOf(path string) *outlined {
	stamp := ""
	if d := s.byPath[path]; d != nil {
		stamp = "open " + strconv.Itoa(int(d.version))
	} else if info, err := os.Stat(path); err == nil {
		stamp = info.ModTime().String() + " " + strconv.FormatInt(info.Size(), 10)
	}

	if o := s.outlines[path]; o != nil && o.stamp == stamp && stamp != "" {
		return o
	}

	src, err := s.read(path)
	if err != nil {
		return nil
	}

	o := &outlined{stamp: stamp}
	for _, sym := range outline(path, src, s.encoding) {
		o.symbols = flatten(o.symbols, sym, s.uriOfPath(path), packageName(src))
	}

	for _, sym := range o.symbols {
		o.lower = append(o.lower, strings.ToLower(sym.Name))
	}

	return o
}

// workspacePackages returns the packages of the workspace: every
// package of its modules, and the package of an open document that is
// in no module.
func (s *Server) workspacePackages() []string {
	var dirs []string
	for _, root := range s.moduleRoots() {
		dirs = append(dirs, importer.Packages(root)...)
	}

	for _, d := range s.docs {
		if d.path == "" {
			continue
		}

		if m, err := importer.FindModule(filepath.Dir(d.path)); err == nil && m == nil {
			dirs = append(dirs, filepath.Dir(d.path))
		}
	}

	slices.Sort(dirs)
	return slices.Compact(dirs)
}

// moduleRoots returns the roots of the modules the workspace holds,
// sorted, each once: the module of each open document, and those of
// each folder the editor has open, the one it is in and every one under
// it, looked for again at each request, so that a module made since is
// found.
func (s *Server) moduleRoots() []string {
	var roots []string
	add := func(dir string) {
		if m, err := importer.FindModule(dir); err == nil && m != nil && !slices.Contains(roots, m.Dir) {
			roots = append(roots, m.Dir)
		}
	}

	for _, d := range s.docs {
		if d.path != "" {
			add(filepath.Dir(d.path))
		}
	}

	for _, folder := range s.folders {
		for _, root := range importer.Modules(folder) {
			if !slices.Contains(roots, root) {
				roots = append(roots, root)
			}
		}
	}

	slices.Sort(roots)
	return roots
}

// uriOfPath returns the URI of the file at path: the one the editor
// opened it by, or its own.
func (s *Server) uriOfPath(path string) protocol.DocumentURI {
	if d := s.byPath[path]; d != nil {
		return d.uri
	}

	return uriOf(path)
}

// outline returns the outline of the file at path, src its text: its
// declarations in order, each with its whole stretch and its name's,
// a struct's fields, an enum's variants and a trait's fields and
// methods under it, and the methods and functions of a type the file
// declares under the type; one of a type declared elsewhere is named
// with the type, Rect.Area.
func outline(path string, src []byte, enc string) []protocol.DocumentSymbol {
	file := token.NewFile(path, len(src))
	f, _ := parser.Parse(file, src)
	if f == nil {
		return []protocol.DocumentSymbol{}
	}

	o := &outliner{file: file, src: src, tokens: spans(path, src), encoding: enc}
	symbols := []protocol.DocumentSymbol{}
	types := make(map[string]int)
	type method struct {
		decl       *ast.FuncDecl
		start, end int
	}

	var methods []method
	for i, d := range f.Decls {
		limit := len(src)
		if i+1 < len(f.Decls) {
			limit = file.Offset(f.Decls[i+1].Pos())
		}

		start := file.Offset(d.Pos())
		end := o.endBefore(start, limit)
		switch d := d.(type) {
		case *ast.FuncDecl:
			if ownerName(d) != "" {
				methods = append(methods, method{d, start, end})
				continue
			}

			symbols = append(symbols, o.symbol(d.Name, protocol.SymbolKindFunction, start, end))
		case *ast.TypeDecl:
			types[d.Name.Name] = len(symbols)
			symbols = append(symbols, o.typeSymbol(d, start, end))
		case *ast.TraitDecl:
			types[d.Name.Name] = len(symbols)
			symbols = append(symbols, o.traitSymbol(d, start, end))
		case *ast.VarDecl:
			kind := protocol.SymbolKindVariable
			if d.Const {
				kind = protocol.SymbolKindConstant
			}

			for _, name := range d.Names {
				symbols = append(symbols, o.symbol(name, kind, start, end))
			}
		}
	}

	for _, m := range methods {
		kind := protocol.SymbolKindMethod
		if m.decl.Recv == nil { // a function of a type, Point.Origin, which takes no value of it
			kind = protocol.SymbolKindFunction
		}

		sym := o.symbol(m.decl.Name, kind, m.start, m.end)
		if at, ok := types[ownerName(m.decl)]; ok {
			symbols[at].Children = append(symbols[at].Children, sym)
			continue
		}

		sym.Name = ownerName(m.decl) + memberSeparator + sym.Name
		symbols = append(symbols, sym)
	}

	slices.SortStableFunc(symbols, func(a, b protocol.DocumentSymbol) int {
		return cmp.Or(cmp.Compare(a.Range.Start.Line, b.Range.Start.Line), cmp.Compare(a.Range.Start.Character, b.Range.Start.Character))
	})

	return symbols
}

// typeSymbol returns the symbol of a type's declaration: a struct with
// its fields, an enum with its variants, or any other type.
func (o *outliner) typeSymbol(d *ast.TypeDecl, start, end int) protocol.DocumentSymbol {
	var members []ast.Node
	kind := protocol.SymbolKindClass
	switch def := d.Def.(type) {
	case *ast.StructType:
		kind = protocol.SymbolKindStruct
		for _, f := range def.Fields {
			members = append(members, f)
		}
	case *ast.EnumType:
		kind = protocol.SymbolKindEnum
		for _, v := range def.Variants {
			members = append(members, v)
		}
	}

	sym := o.symbol(d.Name, kind, start, end)
	sym.Children = o.members(members, start, end)
	return sym
}

// traitSymbol returns the symbol of a trait's declaration, with its
// fields and methods in the order written.
func (o *outliner) traitSymbol(d *ast.TraitDecl, start, end int) protocol.DocumentSymbol {
	var members []ast.Node
	for _, f := range d.Fields {
		members = append(members, f)
	}

	for _, m := range d.Methods {
		members = append(members, m)
	}

	slices.SortFunc(members, func(a, b ast.Node) int { return cmp.Compare(a.Pos(), b.Pos()) })
	sym := o.symbol(d.Name, protocol.SymbolKindInterface, start, end)
	sym.Children = o.members(members, start, end)
	return sym
}

// members returns the symbols of a declaration's members, in order,
// the declaration from start to end: each member's stretch reaches its
// last token before the next member, and the last one's before the
// declaration's own last token, its closing brace.
func (o *outliner) members(members []ast.Node, start, end int) []protocol.DocumentSymbol {
	closing := o.lastTokenBefore(start, end)
	var symbols []protocol.DocumentSymbol
	for i, n := range members {
		from := o.file.Offset(n.Pos())
		limit := closing
		if i+1 < len(members) {
			limit = o.file.Offset(members[i+1].Pos())
		}

		to := o.endBefore(from, limit)
		kind := protocol.SymbolKindField
		switch n.(type) {
		case *ast.Variant:
			kind = protocol.SymbolKindEnumMember
		case *ast.FuncDecl:
			kind = protocol.SymbolKindMethod
		}

		for _, name := range namesOf(n) {
			if name != nil {
				symbols = append(symbols, o.symbol(name, kind, from, to))
			}
		}
	}

	return symbols
}

// symbol returns the symbol of a name of the kind given, its
// declaration from start to end.
func (o *outliner) symbol(name *ast.Ident, kind protocol.SymbolKind, start, end int) protocol.DocumentSymbol {
	at := o.file.Offset(name.NamePos)
	return protocol.DocumentSymbol{
		Name:           name.Name,
		Kind:           kind,
		Range:          o.rangeOf(start, max(end, at+len(name.Name))),
		SelectionRange: o.rangeOf(at, at+len(name.Name)),
	}
}

// rangeOf returns the stretch from start to end in the protocol's
// positions.
func (o *outliner) rangeOf(start, end int) protocol.Range {
	return protocol.Range{Start: positionOf(o.src, start, o.encoding), End: positionOf(o.src, end, o.encoding)}
}

// endBefore returns where the last token that begins at start or after
// and before limit ends; start for none.
func (o *outliner) endBefore(start, limit int) int {
	if t, ok := o.lastToken(start, limit); ok {
		return t.end
	}

	return start
}

// lastTokenBefore returns where the last token that begins at start or
// after and before end begins; end for none.
func (o *outliner) lastTokenBefore(start, end int) int {
	if t, ok := o.lastToken(start, end); ok {
		return t.start
	}

	return end
}

// lastToken returns the last token that begins at start or after and
// before limit; false for none.
func (o *outliner) lastToken(start, limit int) (span, bool) {
	i, _ := slices.BinarySearchFunc(o.tokens, limit, func(t span, limit int) int { return cmp.Compare(t.start, limit) })
	if i == 0 || o.tokens[i-1].start < start {
		return span{}, false
	}

	return o.tokens[i-1], true
}

// spans returns the stretches of the tokens of src in order, leaving
// out the semicolons the scanner puts at a line's end, which are no
// part of what a line declares.
func spans(name string, src []byte) []span {
	file := token.NewFile(name, len(src))
	var s scanner.Scanner
	s.Init(file, src, func(token.Position, string) {})
	var tokens []span
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			return tokens
		}

		if tok == token.SEMICOLON {
			continue
		}

		size := len(lit)
		if size == 0 {
			size = len(tok.String())
		}

		start := file.Offset(pos)
		tokens = append(tokens, span{start, start + size})
	}
}

// flatten appends sym and the symbols under it to list, each with the
// name of the symbol it is under, or container for one under none.
func flatten(list []protocol.SymbolInformation, sym protocol.DocumentSymbol, uri protocol.DocumentURI, container string) []protocol.SymbolInformation {
	list = append(list, protocol.SymbolInformation{Name: sym.Name, Kind: sym.Kind, Location: protocol.Location{URI: uri, Range: sym.Range}, ContainerName: container})
	for _, child := range sym.Children {
		list = flatten(list, child, uri, sym.Name)
	}

	return list
}

// ownerName returns the name of the type a method or a function of a
// type belongs to, Rect of func (r ref Rect) Area and of func
// Rect.Square; "" for a function of none.
func ownerName(d *ast.FuncDecl) string {
	var t ast.Type
	switch {
	case d.Recv != nil:
		t = d.Recv.Type
	case d.Of != nil:
		return d.Of.Name.Name
	default:
		return ""
	}

	for {
		switch u := t.(type) {
		case *ast.RefType:
			t = u.Elem
		case *ast.PointerType:
			t = u.Elem
		case *ast.NamedType:
			return u.Name.Name
		default:
			return ""
		}
	}
}

// packageName returns the name a file's package clause gives, or "".
func packageName(src []byte) string {
	file := token.NewFile("", len(src))
	var s scanner.Scanner
	s.Init(file, src, func(token.Position, string) {})
	if _, tok, _ := s.Scan(); tok != token.PACKAGE {
		return ""
	}

	if _, tok, lit := s.Scan(); tok == token.IDENT {
		return lit
	}

	return ""
}

// matches reports whether name holds the letters of query in order;
// both are in lower case, so that the match takes no case, and every
// name matches an empty query.
func matches(name, query string) bool {
	rest := []rune(query)
	for _, r := range name {
		if len(rest) > 0 && r == rest[0] {
			rest = rest[1:]
		}
	}

	return len(rest) == 0
}

// rank orders a name against a query, lower first: the query itself,
// then a name that begins with it, then one that holds it, then any
// other that matches; both are in lower case.
func rank(name, query string) int {
	switch {
	case name == query:
		return 0
	case strings.HasPrefix(name, query):
		return 1
	case strings.Contains(name, query):
		return 2
	}

	return 3
}
