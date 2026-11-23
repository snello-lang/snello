// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/doc"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/scanner"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
	"github.com/snello-lang/snello/std"
)

// posType is the type of a position, which a node's fields that hold
// where its own tokens are have; formTokens are the tokens a form the
// language writes with its own syntax is written with, a type, map[K]V,
// ref T, T? by its last and a channel's direction, <-chan T and
// chan<- T, and an operator, x?, x ?? y, <-ch, &x, a match arm's =>, a
// variadic's ... and an attribute's @, a pattern's | and ..=, and an
// arithmetic operator, a comparison and a compound assignment, which a
// standard trait is behind, whose node a place on them is looked for
// at; chanTokens those of a
// channel's type, which begins with either; attrToken the one an
// attribute begins with, whose node is kept beside the syntax tree;
// patternTokens those that join a pattern's parts, which no node holds;
// superWord the keyword a trait's method calls the next definition with,
// which is parsed as a name; and fieldsFunc and variantsFunc the
// functions that give a type's parts, whose members their comments
// document.
var (
	posType       = reflect.TypeOf(token.NoPos)
	formTokens    = []string{"map", "chan", "<-", "ref", "mut", "[", "*", "?", "??", "&", "=>", "...", "@", "|", "..=", "+", "-", "/", "%", "==", "!=", "<", "<=", ">", ">=", "+=", "-=", "*=", "/=", "%="}
	chanTokens    = []string{"chan", "<-"}
	attrToken     = "@"
	patternTokens = []string{"|", "..="}
	superWord     = "super"
	fieldsFunc    = "fields"
	variantsFunc  = "variants"
)

// A view is what one check leaves for the requests on its package's
// files: every file the check parsed, the program's and its imports',
// whose positions are apart, so that a position names its file; the
// sources the check read, by name, against which its positions are
// converted; and, once the check is kept, the checker's information and
// the syntax of the package's own files alone, by path, the type each
// function of a type belongs to, which the function does not keep, and
// the package name each import of its own files declares.
// What the check knew of its imports' insides is let go, since no
// request reads it: a hover reads a declaration's comment from its file
// again.
type view struct {
	info    *types.Info
	files   []*token.File
	syntax  map[*token.File]*ast.File
	srcs    map[string][]byte
	byPath  map[string]*token.File
	owners  map[*types.Func]*types.TypeName
	imports map[*ast.ImportSpec]*types.PkgName
}

// A target is what a place in a document holds: the name there, with
// the object it declares or uses, or the expression whose token is
// there, with its type, or the package an import's path or the package
// clause names, or a method the language declares of one of its types,
// which no object stands for, as builtinFile documents it; and the
// stretch of the document it covers, as offsets.
type target struct {
	obj        types.Object
	typ        types.Type
	pkg        *types.Package
	builtin    *builtinDecl
	start, end int
}

// A packageFile is a source file of a package: its name, its text, the
// offset of the name its package clause gives, and the comment above
// the clause.
type packageFile struct {
	name   string
	src    []byte
	clause int
	doc    string
}

// newView returns the view of a check that reads its sources into
// srcs.
func newView(srcs map[string][]byte) *view {
	return &view{info: &types.Info{}, syntax: make(map[*token.File]*ast.File), srcs: srcs, byPath: make(map[string]*token.File)}
}

// parsed keeps a file the check parsed, with its syntax.
func (v *view) parsed(file *token.File, syntax *ast.File) {
	v.files = append(v.files, file)
	v.syntax[file] = syntax
}

// typed reports whether the check typed the file, which a file that
// does not parse, or one of a package beside it that does not, never
// is: its declarations' names are defined, or it declares nothing.
func (v *view) typed(file *token.File) bool {
	f := v.syntax[file]
	if f == nil {
		return false
	}

	for _, d := range f.Decls {
		if names := namesOf(d); len(names) > 0 && names[0] != nil {
			_, ok := v.info.Defs[names[0]]
			return ok
		}
	}

	return true
}

// trim keeps of the check what the requests on its own files read: the
// checker's information on their names and expressions, their syntax,
// by path as pathOf names them, and the type each function of a type of
// any file belongs to; the rest goes.
func (v *view) trim(own map[string]*token.File) {
	for _, f := range v.files {
		syntax := v.syntax[f]
		if syntax == nil {
			continue
		}

		for _, d := range syntax.Decls {
			if d, ok := d.(*ast.FuncDecl); ok && d.Of != nil {
				fn, _ := v.info.Defs[d.Name].(*types.Func)
				tn, _ := v.info.Uses[d.Of.Name].(*types.TypeName)
				if fn != nil && tn != nil {
					if v.owners == nil {
						v.owners = make(map[*types.Func]*types.TypeName)
					}

					v.owners[fn] = tn
				}
			}
		}
	}

	imported := make(map[token.Pos]*types.PkgName) // every import's package name, by the import's position, a name its file never uses included
	for _, obj := range v.info.Defs {
		if pn, ok := obj.(*types.PkgName); ok {
			imported[pn.Pos()] = pn
		}
	}

	v.imports = make(map[*ast.ImportSpec]*types.PkgName)
	for _, f := range own {
		if syntax := v.syntax[f]; syntax != nil {
			for _, spec := range syntax.Imports {
				if pn := imported[spec.Pos()]; pn != nil {
					v.imports[spec] = pn
				}
			}
		}
	}

	kept := &types.Info{Defs: make(map[*ast.Ident]types.Object), Uses: make(map[*ast.Ident]types.Object), Types: make(map[ast.Expr]types.Type)}
	syntax := make(map[*token.File]*ast.File, len(own))
	for path, f := range own {
		v.byPath[path] = f
		syntax[f] = v.syntax[f]
		keep := func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				if obj, ok := v.info.Defs[id]; ok {
					kept.Defs[id] = obj
				}

				if obj, ok := v.info.Uses[id]; ok {
					kept.Uses[id] = obj
				}
			}

			if x, ok := n.(ast.Expr); ok {
				if t, ok := v.info.Types[x]; ok {
					kept.Types[x] = t
				}
			}

			return true
		}

		ast.Inspect(v.syntax[f], keep)
		if v.syntax[f] != nil {
			for _, attrs := range v.syntax[f].Attrs { // the attributes, kept beside the tree, which Inspect does not walk
				for _, a := range attrs {
					ast.Inspect(a.X, keep)
				}
			}
		}
	}

	v.info, v.syntax = kept, syntax
}

// fileOf returns the file of the check that holds pos, or nil.
func (v *view) fileOf(pos token.Pos) *token.File {
	for _, f := range v.files {
		if f.Holds(pos) {
			return f
		}
	}

	return nil
}

// source returns the text of the file named name as the check read it:
// a source read through the server, a file of the standard library from
// the copy in the compiler, by its path under std/, and any other from
// the disk.
func (v *view) source(name string) ([]byte, error) {
	if src, ok := v.srcs[name]; ok {
		return src, nil
	}

	if !filepath.IsAbs(name) {
		return std.Sources.ReadFile(name)
	}

	return os.ReadFile(name)
}

// at returns what the file at offset holds: the name there, or the
// innermost expression one of whose own tokens is there; nil for
// neither, or a file the check did not parse.
func (v *view) at(file *token.File, src []byte, offset int) *target {
	syntax := v.syntax[file]
	if syntax == nil {
		return nil
	}

	id := identAt(syntax, file, offset)
	if id == nil || offset == file.Offset(id.NamePos)+len(id.Name) { // on no name's letters: a form's, a keyword's or an operator's token, ? of int?, before the name it may follow
		if start, end, ok := tokenAt(file.Name(), src, offset); ok {
			if d, ok := v.tokenForm(syntax, file, string(src[start:end]), file.Pos(start)); ok {
				return &target{builtin: &d, start: start, end: end}
			}
		}
	}

	if id != nil {
		start := file.Offset(id.NamePos)
		if id == syntax.Name {
			return &target{pkg: v.ownPackage(file, syntax), start: start, end: start + len(id.Name)}
		}

		if id.Name == superWord && selectorOf(syntax, id) == nil {
			if d, ok := builtins().forms[superWord]; ok { // super.M(args): the checker records the receiver's use
				return &target{builtin: &d, start: start, end: start + len(id.Name)}
			}
		}

		obj := v.info.Defs[id]
		if obj == nil {
			obj = v.info.Uses[id]
		}

		if obj == nil {
			obj = v.paramOf(syntax, id)
		}

		t := &target{obj: obj, typ: v.info.Types[id], start: start, end: start + len(id.Name)}
		if obj == nil {
			if sel := selectorOf(syntax, id); sel != nil {
				if d, ok := builtinMethodOf(v.info.Types[sel.X], id.Name); ok {
					t.builtin = &d
				} else if d, ok := v.partMember(sel); ok {
					t.builtin = &d
				}
			} else if isLanguageType(syntax, id) {
				if d, ok := builtins().names[id.Name]; ok { // Result<T, E>, which is in no scope
					t.builtin = &d
				}
			} else if id.Name == doc.FormBlank || token.Lookup(id.Name).IsKeyword() {
				if d, ok := builtins().forms[id.Name]; ok { // the blank name, and super, which is parsed as a name
					t.builtin = &d
				}
			}
		}

		return t
	}

	start, end, ok := tokenAt(file.Name(), src, offset)
	if !ok {
		return nil
	}

	for _, spec := range syntax.Imports {
		if file.Offset(spec.Path.Pos()) == start {
			pn := v.imports[spec]
			if pn == nil || pn.Imported() == nil {
				return nil
			}

			return &target{obj: pn, pkg: pn.Imported(), start: start, end: end}
		}
	}

	if x, ok := owner(syntax, file.Pos(start)).(ast.Expr); ok && v.info.Types[x] != nil {
		return &target{typ: v.info.Types[x], start: start, end: end}
	}

	return nil
}

// declaration returns the declaration of obj as the checker types it,
// and its doc comment, or its line comment when it has none: the
// comment of the node the object's name is declared in, read from the
// file it is declared in, parsed again with its comments.
func (v *view) declaration(obj types.Object) (decl, doc string) {
	decl = types.ObjectString(obj, v.typeOf(obj))
	file := v.fileOf(obj.Pos())
	if file == nil {
		d, _ := builtinOf(obj) // a name the language declares, documented in builtinFile
		return decl, d.doc
	}

	src, err := v.source(file.Name())
	if err != nil {
		return decl, ""
	}

	again := token.NewFile(file.Name(), len(src))
	f, _ := parser.ParseComments(again, src)
	if f == nil || f.Docs == nil {
		return decl, ""
	}

	offset := file.Offset(obj.Pos())
	for _, comments := range []map[ast.Node]*ast.CommentGroup{f.Docs.Lead, f.Docs.Line} {
		for n, g := range comments {
			if declares(n, again, offset) {
				return decl, g.Text()
			}
		}
	}

	return decl, ""
}

// packageDoc returns the doc comment of pkg, a package the check
// loaded, as snello doc gives it: the comment above the package clause
// of each of its source files, in the order of their names, joined.
func (v *view) packageDoc(pkg *types.Package) string {
	var docs []string
	for _, f := range v.packageFiles(pkg) {
		if f.doc != "" {
			docs = append(docs, f.doc)
		}
	}

	return strings.Join(docs, "\n")
}

// packageFiles returns the source files of pkg, a package the check
// loaded, in the order of their names, a test file left out: the files
// of the check in the directory of its declarations whose clause names
// it, each parsed again for its comments.
func (v *view) packageFiles(pkg *types.Package) []packageFile {
	dirs := make(map[string]bool)
	for _, name := range pkg.Scope().Names() {
		if f := v.fileOf(pkg.Scope().Lookup(name).Pos()); f != nil {
			dirs[filepath.Dir(f.Name())] = true
		}
	}

	var names []string
	for _, f := range v.files {
		if dirs[filepath.Dir(f.Name())] && !importer.IsTestFile(f.Name()) {
			names = append(names, f.Name())
		}
	}

	slices.Sort(names)
	var files []packageFile
	for _, name := range slices.Compact(names) {
		src, err := v.source(name)
		if err != nil {
			continue
		}

		file := token.NewFile(name, len(src))
		f, _ := parser.ParseComments(file, src)
		if f == nil || f.Name == nil || f.Name.Name != pkg.Name() {
			continue
		}

		pf := packageFile{name: name, src: src, clause: file.Offset(f.Name.NamePos)}
		if f.Docs != nil && f.Docs.Lead[f] != nil {
			pf.doc = f.Docs.Lead[f].Text()
		}

		files = append(files, pf)
	}

	return files
}

// tokenForm returns the documentation of the form the token text at
// pos is part of: an attribute's, from its @; the node of a form's or
// an operator's token, a channel's type from either of its tokens, with
// the form as the place writes it; or else a keyword's, by the keyword;
// false for any other token.
func (v *view) tokenForm(syntax *ast.File, file *token.File, text string, pos token.Pos) (builtinDecl, bool) {
	if text == attrToken && isAttr(syntax, pos) {
		d, ok := builtins().forms[doc.FormAttribute]
		return d, ok
	}

	if slices.Contains(formTokens, text) {
		n := owner(syntax, pos)
		switch {
		case slices.Contains(chanTokens, text):
			if ch := chanTypeAt(syntax, pos); ch != nil {
				n = ch
			}
		case slices.Contains(patternTokens, text):
			if patternAt(syntax, pos) {
				d, ok := builtins().forms[doc.FormPattern]
				return d, ok
			}
		}

		if d, ok := v.formAt(n); ok {
			return d, true
		}
	}

	if !token.Lookup(text).IsKeyword() && text != doc.FormBlank {
		return builtinDecl{}, false
	}

	d, ok := builtins().forms[text] // a keyword's, or the blank name's, a pattern's _ among them
	return d, ok
}

// formAt returns the documentation of the form the syntax node n
// writes, builtinFormOf's, with the form as the place writes it: a
// type's text, and the type of an expression an operator gives, &x or
// x?, in place of the file's.
func (v *view) formAt(n ast.Node) (builtinDecl, bool) {
	d, ok := builtinFormOf(n)
	if !ok {
		return d, false
	}

	switch n := n.(type) {
	case ast.Type:
		d.decl = ast.SourceType(n)
	case *ast.UnaryExpr, *ast.TryExpr, *ast.CoalesceExpr, *ast.BinaryExpr:
		if t := v.info.Types[n.(ast.Expr)]; t != nil {
			d.decl = t.String()
		}
	}

	return d, true
}

// isAttr reports whether an attribute's @ is at pos, among the
// attributes the syntax keeps beside its tree.
func isAttr(syntax *ast.File, pos token.Pos) bool {
	for _, attrs := range syntax.Attrs {
		for _, a := range attrs {
			if a.At == pos {
				return true
			}
		}
	}

	return false
}

// paramOf returns the parameter, or the receiver, id names in the
// declaration of a function without a body, one the runtime or C
// provides, or a trait's required method, whose parameters the checker
// declares no object for: the one its signature holds; nil for any
// other name.
func (v *view) paramOf(syntax *ast.File, id *ast.Ident) types.Object {
	var found types.Object
	ast.Inspect(syntax, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || found != nil {
			return found == nil
		}

		if fd.Body != nil {
			return true
		}

		fn, _ := v.info.Defs[fd.Name].(*types.Func)
		if fn == nil {
			return true
		}

		sig, _ := fn.Type().(*types.Signature)
		if sig == nil {
			return true
		}

		if fd.Recv != nil && fd.Recv.Name == id && sig.Recv() != nil {
			found = sig.Recv()
		}

		for i, p := range fd.Params {
			if p.Name == id && i < len(sig.Params()) {
				found = sig.Params()[i]
			}
		}

		return found == nil
	})

	return found
}

// partMember returns the documentation of a member of a part of a type,
// f.name, f.type or f.default of a field fields(T) gives, or of a
// variant variants(T) gives: the comment of the function that gives
// the part, which says what each member is.
func (v *view) partMember(sel *ast.SelectorExpr) (builtinDecl, bool) {
	x, ok := sel.X.(*ast.Ident)
	if !ok {
		return builtinDecl{}, false
	}

	part, ok := v.info.Uses[x].(*types.Part)
	if !ok {
		return builtinDecl{}, false
	}

	giver := fieldsFunc
	if part.IsVariant() {
		giver = variantsFunc
	}

	d, ok := builtins().names[giver]
	return d, ok
}

// patternAt reports whether pos is between the parts of a pattern of
// alternatives, where a | joins two, or of a range, where ..= joins its
// ends.
func patternAt(syntax *ast.File, pos token.Pos) bool {
	found := false
	ast.Inspect(syntax, func(n ast.Node) bool {
		switch p := n.(type) {
		case *ast.OrPat:
			for i := 1; i < len(p.Alts); i++ {
				found = found || p.Alts[i-1].Pos() < pos && pos < p.Alts[i].Pos()
			}
		case *ast.RangePat:
			found = found || p.Lo.Pos() < pos && pos < p.Hi.Pos()
		}

		return !found
	})

	return found
}

// isLanguageType reports whether id names a type in a type, written
// with no package: Result<T, E>, a type the language declares in no
// scope.
func isLanguageType(syntax *ast.File, id *ast.Ident) bool {
	found := false
	ast.Inspect(syntax, func(n ast.Node) bool {
		if nt, ok := n.(*ast.NamedType); ok && nt.Name == id && nt.Pkg == nil {
			found = true
		}

		return !found
	})

	return found
}

// chanTypeAt returns the channel type whose arrow or keyword is at pos,
// <-chan T's chan as well as its arrow, chan<- T's arrow as well as its
// chan, or nil for an arrow that sends or receives.
func chanTypeAt(syntax *ast.File, pos token.Pos) *ast.ChanType {
	var found *ast.ChanType
	ast.Inspect(syntax, func(n ast.Node) bool {
		if ch, ok := n.(*ast.ChanType); ok && ch.Begin <= pos && pos < ch.Elem.Pos() {
			found = ch
		}

		return true
	})

	return found
}

// selectorOf returns the selector expression whose selected name is id,
// x.id, or nil for a name selected from nothing.
func selectorOf(syntax *ast.File, id *ast.Ident) *ast.SelectorExpr {
	var found *ast.SelectorExpr
	ast.Inspect(syntax, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel == id {
			found = sel
		}

		return found == nil
	})

	return found
}

// ownPackage returns the package of file, syntax its tree, as one of
// its top-level names says, or for a file that declares none, the one
// that holds only the package's comment, as a file beside it of the
// same package clause says; nil for none.
func (v *view) ownPackage(file *token.File, syntax *ast.File) *types.Package {
	if pkg := v.declaredPackage(syntax); pkg != nil {
		return pkg
	}

	for other, s := range v.syntax {
		if s != nil && s != syntax && s.Name.Name == syntax.Name.Name && filepath.Dir(other.Name()) == filepath.Dir(file.Name()) {
			if pkg := v.declaredPackage(s); pkg != nil {
				return pkg
			}
		}
	}

	return nil
}

// declaredPackage returns the package of the file syntax, as one of its
// top-level names says, or nil for a file that declares none.
func (v *view) declaredPackage(syntax *ast.File) *types.Package {
	var pkg *types.Package
	ast.Inspect(syntax, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && pkg == nil {
			if obj := v.info.Defs[id]; obj != nil {
				pkg = obj.Pkg()
			}
		}

		return pkg == nil
	})

	return pkg
}

// typeOf returns the type a function of a type belongs to, Point of
// func Point.Parse, which the function does not keep: the type its
// declaration names, which trim kept; nil for any other object.
func (v *view) typeOf(obj types.Object) *types.TypeName {
	fn, ok := obj.(*types.Func)
	if !ok {
		return nil
	}

	return v.owners[fn.Origin()]
}

// identAt returns the name of syntax at offset: the one whose letters
// cover it, or else the one that ends there, where a cursor after a
// name stands; nil for none.
func identAt(syntax *ast.File, file *token.File, offset int) *ast.Ident {
	var within, ending *ast.Ident
	visit := func(n ast.Node) bool {
		if within != nil {
			return false
		}

		if id, ok := n.(*ast.Ident); ok && id.NamePos.IsValid() {
			switch start := file.Offset(id.NamePos); {
			case start <= offset && offset < start+len(id.Name):
				within = id
			case offset == start+len(id.Name) && ending == nil:
				ending = id
			}
		}

		return true
	}

	ast.Inspect(syntax, visit)
	for _, attrs := range syntax.Attrs { // the attributes, kept beside the tree, which Inspect does not walk
		for _, a := range attrs {
			ast.Inspect(a.X, visit)
		}
	}

	if within != nil {
		return within
	}

	return ending
}

// tokenAt returns the stretch of the token of src at offset, as
// offsets, scanning src as the file named name.
func tokenAt(name string, src []byte, offset int) (start, end int, ok bool) {
	file := token.NewFile(name, len(src))
	var s scanner.Scanner
	s.Init(file, src, func(token.Position, string) {})
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			return 0, 0, false
		}

		start = file.Offset(pos)
		if start > offset {
			return 0, 0, false
		}

		size := len(lit)
		if size == 0 {
			size = len(tok.String())
		}

		if offset < start+size {
			return start, start + size, true
		}
	}
}

// owner returns the innermost node of syntax that records one of its
// own tokens at pos, the operator of a binary expression or the
// parenthesis of a call; nil for none.
func owner(syntax *ast.File, pos token.Pos) ast.Node {
	var found ast.Node
	ast.Inspect(syntax, func(n ast.Node) bool {
		v := reflect.ValueOf(n).Elem()
		for i := range v.NumField() {
			if f := v.Field(i); f.Type() == posType && token.Pos(f.Int()) == pos {
				found = n
				break
			}
		}

		return true
	})

	return found
}

// declares reports whether the node n of file declares the name at
// offset: a declaration's, a field's or a variant's name.
func declares(n ast.Node, file *token.File, offset int) bool {
	for _, id := range namesOf(n) {
		if id != nil && file.Offset(id.NamePos) == offset {
			return true
		}
	}

	return false
}

// namesOf returns the names a node declares: a declaration's, a field's
// or a variant's; none for any other node.
func namesOf(n ast.Node) []*ast.Ident {
	switch n := n.(type) {
	case *ast.FuncDecl:
		return []*ast.Ident{n.Name}
	case *ast.TypeDecl:
		return []*ast.Ident{n.Name}
	case *ast.TraitDecl:
		return []*ast.Ident{n.Name}
	case *ast.Variant:
		return []*ast.Ident{n.Name}
	case *ast.VarDecl:
		return n.Names
	case *ast.Field:
		return n.Names
	}

	return nil
}
