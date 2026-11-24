// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/compiler/scanner"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
	"github.com/snello-lang/snello/lsp/jsonrpc"
	"github.com/snello-lang/snello/lsp/protocol"
)

// What the text holds at a completion's place, a contextKind: code, a
// comment, a string, or an import's path. completionName is the name the
// server writes in place of the name being typed, for the check a
// completion runs: a name, so that the text parses wherever a name may
// stand, after a dot or where a statement begins, and one no program
// declares; sortWidth is the width of the number each item sorts by, so
// that an editor keeps the server's order; the trigger characters ask
// for a completion as they are typed, a dot for a member and a quote
// for an import's path; builtinPackage documents the predeclared names,
// in builtinFile, and is never imported; lineComment begins a comment
// that runs to its line's end; and funcWord begins a method's detail.
const (
	inCode contextKind = iota
	inComment
	inString
	inImport

	completionName = "_snello_completion_"
	sortWidth      = 5
	dotTrigger     = "."
	quoteTrigger   = "\""
	builtinPackage = "builtin"
	lineComment    = "//"
	funcWord       = "func"
)

// builtinNames are the names of the methods the language predeclares,
// on slices, maps, strings, numbers, options and results, as the
// library's documentation of them, std/builtin, declares them, sorted:
// the candidates of a value's predeclared members, each kept when the
// checker gives it a signature for the value's type.
var builtinNames = sync.OnceValue(func() []string {
	var names []string
	for key := range builtins().methods {
		names = append(names, key[strings.LastIndex(key, keyDot)+len(keyDot):])
	}

	slices.Sort(names)
	return slices.Compact(names)
})

// A context is what the text holds at a place a completion asks about:
// a comment, a string, an import's path, with the stretch of its
// quoted text, or code.
type context struct {
	kind       contextKind
	start, end int
}

// contextKind is the kind of a context.
type contextKind int

// completion answers the names that may be written at the cursor, in
// the order of their scopes and with no ranking, the same list for the
// same place every time: after a dot, the members of what is before it;
// on a name, or where one may begin, the names visible there, the
// innermost scope's first, then the keywords; on the key of a struct's
// literal, the struct's fields; in an import's path, the standard
// library's packages and the module's; in a comment, any other string,
// a number or a place no name is looked up at, nothing. The check that
// holds the file is run, with what the parser recovered of a line typed
// half way.
func (s *Server) completion(m *jsonrpc.Message) {
	var p protocol.CompletionParams
	if err := json.Unmarshal(m.Params, &p); err != nil {
		s.fail(m.ID, jsonrpc.InvalidParams, err.Error())
		return
	}

	var list protocol.CompletionList
	path := pathOf(p.TextDocument.URI)
	var text []byte
	var err error
	if d := s.docs[p.TextDocument.URI]; d != nil {
		text = d.text
	} else {
		text, err = s.read(path)
	}

	if err != nil || path == "" {
		s.reply(m.ID, sorted(list))
		return
	}

	offset := offsetOf(text, p.Position, s.encoding)
	switch ctx := contextAt(path, text, offset); ctx.kind {
	case inComment, inString:
		s.reply(m.ID, sorted(list))
		return
	case inImport:
		list.Items = s.importPaths(path, text, ctx, offset)
		s.reply(m.ID, sorted(list))
		return
	}

	start, end := wordAround(text, offset)
	if r, _ := utf8.DecodeRune(text[start:]); start < end && unicode.IsDigit(r) { // a number, which no name is
		s.reply(m.ID, sorted(list))
		return
	}

	over := map[string][]byte{path: slices.Concat(text[:start], []byte(completionName), text[end:])}
	v := s.completionCheck(path, over, importer.Place{File: path, Offset: start})
	if v != nil {
		if file := v.fileNamedPath(path); file != nil {
			list.Items = completions(v, file, file.Pos(start), start > 0 && text[start-1] == dotTrigger[0])
		}
	}

	s.reply(m.ID, sorted(list))
}

// completionCheck runs the one check of the file at path's package that
// holds the file, with the sources of over and the place at, for the
// platform the file is built for, the parser's recovery of a file that
// does not parse checked too: the program the file is, in a directory
// of programs of one file; the package's tests, for a test file; and
// the package's sources otherwise.
func (s *Server) completionCheck(path string, over map[string][]byte, at importer.Place) *view {
	dir, t := filepath.Dir(path), platformOf(path)
	var v *view
	switch programs := s.programsIn(dir, over, t); {
	case len(programs) > 1 && slices.Contains(programs, path):
		_, v = s.checkProgram(path, over, t, at)
	case importer.IsTestFile(path):
		_, v = s.checkTests(dir, over, t, at)
	default:
		_, v = s.checkSources(dir, over, t, at)
	}

	return v
}

// completions returns the items of the place pos of a file of the view's
// check, the name written over the one being typed: after a dot, the
// members of what is before it; on a struct literal's key, the struct's
// fields; else the names visible there and the keywords, after the
// struct's fields on a name in a struct's literal, a key being typed or
// a value; and nothing at a name the check does not look up, a
// declaration's.
func completions(v *view, file *token.File, pos token.Pos, dot bool) []protocol.CompletionItem {
	from := packageOf(v, file)
	var found, lead []protocol.CompletionItem
	typePlace := false // a name where a type is written, which a type or a package's name may begin
	ast.Inspect(v.syntax[file], func(n ast.Node) bool {
		if found != nil {
			return false
		}

		switch n := n.(type) {
		case *ast.SelectorExpr:
			if dot && n.Sel != nil && n.Sel.NamePos == pos {
				found = selectorMembers(v, n.X, from)
			}
		case *ast.NamedType:
			switch {
			case dot && n.Pkg != nil && n.Name != nil && n.Name.NamePos == pos:
				found = packageMembers(v.info.Uses[n.Pkg], true)
			case !dot && n.Pkg == nil && n.Name != nil && n.Name.NamePos == pos:
				typePlace = true
			}
		case *ast.CompositeLit:
			for _, e := range n.Elts {
				switch e := e.(type) {
				case *ast.KeyValueExpr:
					if id, ok := e.Key.(*ast.Ident); ok && id.NamePos == pos && !dot {
						found = fieldsOf(literalType(v, n), from)
					}
				case *ast.Ident:
					if e.NamePos == pos && !dot { // a key being typed, or a value of a literal with none
						lead = fieldsOf(literalType(v, n), from)
					}
				}
			}
		}

		return true
	})

	if found != nil || dot {
		return found
	}

	if v.info.Visible == nil {
		return lead
	}

	items := lead
	for _, obj := range v.info.Visible {
		_, isType := obj.(*types.TypeName)
		_, isPackage := obj.(*types.PkgName)
		if obj.Name() != completionName && (!typePlace || isType || isPackage) {
			items = append(items, itemOf(obj, nil))
		}
	}

	if typePlace {
		return items
	}

	for k := range token.NumKinds {
		if kind := token.Kind(k); kind.IsKeyword() && !kind.IsRefused() {
			items = append(items, protocol.CompletionItem{Label: kind.String(), Kind: protocol.CompletionItemKindKeyword})
		}
	}

	return items
}

// selectorMembers returns the items of the members of x, what is before
// a dot, as the check typed it: a package's exported names; a type's
// functions, Point.Origin, or a type parameter's, of its bounds; a
// value's fields and methods.
func selectorMembers(v *view, x ast.Expr, from *types.Package) []protocol.CompletionItem {
	var named ast.Expr = x
	if sel, ok := x.(*ast.SelectorExpr); ok {
		named = sel.Sel
	}

	if id, ok := named.(*ast.Ident); ok {
		switch obj := v.info.Uses[id].(type) {
		case *types.PkgName:
			return packageMembers(obj, false)
		case *types.TypeName:
			return typeFunctions(obj.Type(), obj, from)
		}
	}

	if te, ok := x.(*ast.TypeExpr); ok {
		if nt, ok := te.Type.(*ast.NamedType); ok {
			if tn, ok := v.info.Uses[nt.Name].(*types.TypeName); ok {
				return typeFunctions(tn.Type(), tn, from)
			}
		}
	}

	return valueMembers(v.info.Types[x], from)
}

// packageMembers returns the items of the exported names of the package
// obj names, if it names one: its types alone where a type is written.
func packageMembers(obj types.Object, typesOnly bool) []protocol.CompletionItem {
	imp, ok := obj.(*types.PkgName)
	if !ok || imp.Imported() == nil {
		return []protocol.CompletionItem{}
	}

	items := []protocol.CompletionItem{}
	scope := imp.Imported().Scope()
	for _, name := range scope.Names() {
		_, isType := scope.Lookup(name).(*types.TypeName)
		if ast.IsExported(name) && (isType || !typesOnly) {
			items = append(items, itemOf(scope.Lookup(name), nil))
		}
	}

	return items
}

// typeFunctions returns the items of the functions of the type t, which
// tn names, that a name of the type is followed by, Point.Origin: a
// type's, or a type parameter's, of its bounds; of a type of another
// package, its exported ones alone.
func typeFunctions(t types.Type, tn *types.TypeName, from *types.Package) []protocol.CompletionItem {
	var fns []*types.Func
	switch t := t.(type) {
	case *types.Named:
		fns = t.Methods()
	case *types.TypeParam:
		fns = t.Methods()
	}

	items := []protocol.CompletionItem{}
	for _, fn := range fns {
		if fn.Static() && visibleFrom(t, fn.Name(), from) {
			items = append(items, itemOf(fn, tn))
		}
	}

	return items
}

// valueMembers returns the items of the fields and methods of a value
// of type t, read in the package from, through a borrow or a pointer: a
// struct's fields and its type's methods, a type parameter's from its
// bounds, a trait value's, and the methods the language predeclares on
// its kind; a member another package declares unexported left out, and
// a function of the type, which a value does not call, too.
func valueMembers(t types.Type, from *types.Package) []protocol.CompletionItem {
	for {
		switch u := t.(type) {
		case *types.Ref:
			t = u.Elem()
			continue
		case *types.Pointer:
			t = u.Elem()
			continue
		}

		break
	}

	items := fieldsOf(t, from)
	var methods []*types.Func
	switch u := t.(type) {
	case *types.Named:
		methods = u.Methods()
	case *types.TypeParam:
		methods = u.Methods()
	case *types.Trait:
		methods = u.ValueMethods()
	}

	for _, fn := range methods {
		if !fn.Static() && !dropsByHand(t, fn) && visibleFrom(t, fn.Name(), from) {
			items = append(items, itemOf(fn, nil))
		}
	}

	if t == nil {
		return items
	}

	for _, name := range builtinNames() {
		if sig := types.BuiltinMethod(t, name); sig != nil && !slices.ContainsFunc(items, func(i protocol.CompletionItem) bool { return i.Label == name }) {
			items = append(items, protocol.CompletionItem{Label: name, Kind: protocol.CompletionItemKindMethod, Detail: funcWord + " " + name + strings.TrimPrefix(sig.String(), funcWord)})
		}
	}

	return items
}

// dropsByHand reports whether fn is the Drop of a type that implements
// Drop, which runs once, when its value goes, and which the checker
// refuses a program calling.
func dropsByHand(t types.Type, fn *types.Func) bool {
	n, ok := t.(*types.Named)
	return ok && fn.Name() == types.DropTrait.Obj().Name() && n.Implements(types.DropTrait)
}

// fieldsOf returns the items of the fields of a value of type t, read in
// the package from: a struct's, a type parameter's from its bounds, a
// trait value's; one another package declares unexported left out.
func fieldsOf(t types.Type, from *types.Package) []protocol.CompletionItem {
	var fields []*types.Var
	switch u := t.(type) {
	case *types.Named:
		if st, ok := u.Underlying().(*types.Struct); ok {
			for i := range st.NumFields() {
				fields = append(fields, st.Field(i))
			}
		}
	case *types.TypeParam:
		fields = u.Fields()
	case *types.Trait:
		fields = u.ValueFields()
	}

	items := []protocol.CompletionItem{}
	for _, f := range fields {
		if visibleFrom(t, f.Name(), from) {
			items = append(items, itemOf(f, nil))
		}
	}

	return items
}

// literalType returns the type of a composite literal: as the check
// typed it, or, for one typed half way the check gave no type, as the
// literal names it, the type its name resolves to.
func literalType(v *view, lit *ast.CompositeLit) types.Type {
	if t := v.info.Types[lit]; t != nil {
		return t
	}

	if nt, ok := lit.Type.(*ast.NamedType); ok && nt.Name != nil {
		if tn, ok := v.info.Uses[nt.Name].(*types.TypeName); ok {
			return tn.Type()
		}
	}

	return nil
}

// visibleFrom reports whether the member name of a value of type t may
// be named in the package from, by the checker's own rule: exported, or
// declared by from, types.Declarer.
func visibleFrom(t types.Type, name string, from *types.Package) bool {
	if ast.IsExported(name) {
		return true
	}

	owner := types.Declarer(t, name)
	return owner == nil || owner == from
}

// importPaths returns the items of the import paths that begin with
// what the quoted text of ctx holds up to offset, in order: every
// package of the standard library, and every package of the module of
// the file at path but its root's, each written over the quoted text.
func (s *Server) importPaths(path string, text []byte, ctx context, offset int) []protocol.CompletionItem {
	paths := slices.DeleteFunc(importer.Paths(), func(p string) bool { return p == builtinPackage })
	if m, err := importer.FindModule(filepath.Dir(path)); err == nil && m != nil {
		for _, dir := range importer.Packages(m.Dir) {
			if rel, err := filepath.Rel(m.Dir, dir); err == nil && rel != "." {
				paths = append(paths, filepath.ToSlash(rel))
			}
		}
	}

	slices.Sort(paths)
	typed := string(text[ctx.start:offset])
	r := protocol.Range{Start: positionOf(text, ctx.start, s.encoding), End: positionOf(text, ctx.end, s.encoding)}
	items := []protocol.CompletionItem{}
	for _, p := range slices.Compact(paths) {
		if strings.HasPrefix(p, typed) {
			items = append(items, protocol.CompletionItem{Label: p, Kind: protocol.CompletionItemKindModule, TextEdit: &protocol.TextEdit{Range: r, NewText: p}})
		}
	}

	return items
}

// itemOf returns the item of obj: its name, its kind, and its
// declaration as hover writes it, of a function of the type of.
func itemOf(obj types.Object, of *types.TypeName) protocol.CompletionItem {
	return protocol.CompletionItem{Label: obj.Name(), Kind: kindOf(obj), Detail: types.ObjectString(obj, of)}
}

// kindOf returns the kind of item obj is.
func kindOf(obj types.Object) protocol.CompletionItemKind {
	switch obj := obj.(type) {
	case *types.Func:
		if sig, ok := obj.Type().(*types.Signature); ok && sig.Recv() != nil {
			return protocol.CompletionItemKindMethod
		}

		return protocol.CompletionItemKindFunction
	case *types.Var:
		if obj.IsField() {
			return protocol.CompletionItemKindField
		}

		return protocol.CompletionItemKindVariable
	case *types.Const:
		return protocol.CompletionItemKindConstant
	case *types.Variant:
		return protocol.CompletionItemKindEnumMember
	case *types.PkgName:
		return protocol.CompletionItemKindModule
	case *types.Builtin:
		return protocol.CompletionItemKindFunction
	case *types.TypeName:
		switch t := obj.Type().(type) {
		case *types.Trait:
			return protocol.CompletionItemKindInterface
		case *types.TypeParam:
			return protocol.CompletionItemKindTypeParameter
		case *types.Named:
			switch t.Underlying().(type) {
			case *types.Struct:
				return protocol.CompletionItemKindStruct
			case *types.Enum:
				return protocol.CompletionItemKindEnum
			}
		}

		return protocol.CompletionItemKindClass
	}

	return protocol.CompletionItemKindVariable
}

// sorted numbers the items of list in their order, so that an editor,
// which sorts by the number, keeps the server's; a list of none is an
// empty list, never null, as the protocol asks.
func sorted(list protocol.CompletionList) protocol.CompletionList {
	if list.Items == nil {
		list.Items = []protocol.CompletionItem{}
	}

	for i := range list.Items {
		list.Items[i].SortText = fmt.Sprintf("%0*d", sortWidth, i)
	}

	return list
}

// contextAt returns what the text of the file at path holds at offset:
// a comment, a string, an import's path, or code. A place inside a
// comment, or inside a string's quotes, or at the end of one not closed
// on its line, is in it.
func contextAt(path string, text []byte, offset int) context {
	file := token.NewFile(path, len(text))
	var s scanner.Scanner
	s.Init(file, text, func(token.Position, string) {})
	s.KeepComments()
	grouped, afterImport := false, false
	found := context{kind: inCode}
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}

		start := file.Offset(pos)
		end := start + max(len(lit), len(tok.String()))
		if (tok == token.STRING || tok == token.CHAR) && start < offset && (offset < end || offset == end && !closed(lit)) {
			found = context{kind: inString, start: start + 1, end: end}
			if closed(lit) {
				found.end--
			}

			if afterImport || grouped {
				found.kind = inImport
			}
		}

		switch tok {
		case token.IMPORT:
			afterImport = true
		case token.LPAREN:
			grouped = afterImport
		case token.RPAREN:
			grouped, afterImport = false, false
		case token.IDENT, token.STRING:
		case token.SEMICOLON:
			afterImport = grouped
		default:
			afterImport, grouped = false, false
		}
	}

	for _, c := range s.Comments() {
		start := file.Offset(c.Pos)
		end := start + len(c.Text)
		if start < offset && (offset < end || offset == end && strings.HasPrefix(c.Text, lineComment)) {
			return context{kind: inComment}
		}
	}

	return found
}

// closed reports whether a string's literal ends with its quote.
func closed(lit string) bool {
	return len(lit) >= 2 && lit[len(lit)-1] == lit[0]
}

// wordAround returns the stretch of the name that covers offset in
// text, as offsets: the letters before it and after it; an empty
// stretch at offset for none.
func wordAround(text []byte, offset int) (start, end int) {
	start, end = offset, offset
	for start > 0 {
		r, size := utf8.DecodeLastRune(text[:start])
		if !isWordRune(r) {
			break
		}

		start -= size
	}

	for end < len(text) {
		r, size := utf8.DecodeRune(text[end:])
		if !isWordRune(r) {
			break
		}

		end += size
	}

	return start, end
}

// platformOf returns the platform the file at path is checked for: the
// host's when its build chooses the file, and else the first platform
// whose build does.
func platformOf(path string) platform.Target {
	name := filepath.Base(path)
	if importer.Chosen(name, platform.Host()) {
		return platform.Host()
	}

	for _, t := range platform.Targets() {
		if importer.Chosen(name, t) {
			return t
		}
	}

	return platform.Host()
}

// packageOf returns the package the file of a view declares, or nil.
func packageOf(v *view, file *token.File) *types.Package {
	for id, obj := range v.info.Defs {
		if obj != nil && obj.Pkg() != nil && file.Holds(id.NamePos) {
			return obj.Pkg()
		}
	}

	return nil
}

// fileNamedPath returns the file of the check whose path, as stdPathOf
// names it, is path, or nil.
func (v *view) fileNamedPath(path string) *token.File {
	for _, f := range v.files {
		if stdPathOf(f.Name()) == path {
			return f
		}
	}

	return nil
}
