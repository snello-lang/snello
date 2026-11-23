// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"cmp"
	"encoding/json"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
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

// languageFile begins the key of a method of a trait the language
// declares, which no file holds.
const languageFile = "language "

// places matches a line and a column in a message, 3:4.
var places = regexp.MustCompile(`[0-9]+:[0-9]+`)

// An objectKey names an object across checks, each of which makes its
// own: the file it is declared in, by the name the check gives it, and
// the offset of its name there.
type objectKey struct {
	file   string
	offset int
}

// A use is a name of a checked file that declares or uses an object:
// the file's path, the name's stretch as offsets, whether it is the
// declaration, and the key of the object it names.
type use struct {
	path       string
	start, end int
	decl       bool
	key        objectKey
}

// A spot is a place of a file: its path and an offset in it.
type spot struct {
	path   string
	offset int
}

// A checked is one check of a search: the package's directory, the
// platform it was checked for, what it reported and the view it left,
// trimmed to the package's own files.
type checked struct {
	dir    string
	target platform.Target
	report report
	view   *view
}

// A search is what references and rename found of an object, its name
// and its key: the objects it looks for, by key, the object's alone or,
// for a rename of a method, every method of its dispatch, each named
// Holder.Method; every name that declares or uses one of them, each
// once, in order; the packages it checked, from where it was asked and
// for which new name; and the checks, which a rename checks its edits
// against.
type search struct {
	name    string
	key     objectKey
	keys    map[objectKey]string
	uses    []use
	dirs    map[string]bool
	from    string
	renamed string
	checks  []checked
}

// references answers every name that uses the object under the cursor,
// and its declaration when the editor asks, in the packages that can
// name it, each checked, for every platform its files differ for, as
// the request is answered.
func (s *Server) references(m *jsonrpc.Message) {
	var p protocol.ReferenceParams
	if err := json.Unmarshal(m.Params, &p); err != nil {
		s.fail(m.ID, jsonrpc.InvalidParams, err.Error())
		return
	}

	locations := []protocol.Location{}
	v, _, t := s.place(p.TextDocumentPositionParams)
	if t == nil || t.obj == nil {
		s.reply(m.ID, locations)
		return
	}

	found := s.search(v, t.obj, pathOf(p.TextDocument.URI), "")
	declared := false
	for _, u := range found.uses {
		declared = declared || u.decl
		if !u.decl || p.Context.IncludeDeclaration {
			locations = append(locations, s.locationOf(found, u))
		}
	}

	if p.Context.IncludeDeclaration && !declared { // declared where no search reaches: the standard library, or a module required
		if loc, ok, err := s.declarationOf(v, t.obj); err == nil && ok {
			locations = append(locations, loc)
		}
	}

	s.reply(m.ID, locations)
}

// prepareRename answers whether the name under the cursor may be
// renamed, with its stretch and the name, or why it may not.
func (s *Server) prepareRename(m *jsonrpc.Message) {
	v, src, t, ok := s.targetOf(m)
	if !ok {
		return
	}

	if why := s.unrenamable(v, t); why != "" {
		s.fail(m.ID, jsonrpc.RequestFailed, why)
		return
	}

	s.reply(m.ID, protocol.PrepareRenameResult{
		Range:       protocol.Range{Start: positionOf(src, t.start, s.encoding), End: positionOf(src, t.end, s.encoding)},
		Placeholder: t.obj.Name(),
	})
}

// rename answers the edits that rename the object under the cursor at
// its declaration and every use, in every package that can name it,
// each file's edits in one list, or why it may not. It refuses a name
// the language, the standard library or a module outside the editor
// declares, a package's, a new name that is no name, a package it would
// rename in that does not parse, and a method whose dispatch holds a
// method the editor does not edit; a method is renamed with every
// method it shares a dispatch with; then it checks the packages again with
// the edits in place, and refuses edits after which the checker reports
// an error it did not, with the checker's message, or after which a
// name refers to another declaration than it did.
func (s *Server) rename(m *jsonrpc.Message) {
	var p protocol.RenameParams
	if err := json.Unmarshal(m.Params, &p); err != nil {
		s.fail(m.ID, jsonrpc.InvalidParams, err.Error())
		return
	}

	v, _, t := s.place(p.TextDocumentPositionParams)
	if why := s.unrenamable(v, t); why != "" {
		s.fail(m.ID, jsonrpc.RequestFailed, why)
		return
	}

	old := t.obj.Name()
	switch {
	case token.Lookup(p.NewName) != token.IDENT:
		s.fail(m.ID, jsonrpc.RequestFailed, strconv.Quote(p.NewName)+" is a keyword, and names nothing")
		return
	case !isIdentifier(p.NewName):
		s.fail(m.ID, jsonrpc.RequestFailed, strconv.Quote(p.NewName)+" is not a name: a letter or _, then letters, digits and _")
		return
	case p.NewName == old:
		s.reply(m.ID, protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{}})
		return
	}

	found := s.search(v, t.obj, pathOf(p.TextDocument.URI), p.NewName)
	why := s.widen(found)
	if why == "" {
		why = found.untyped()
	}

	if why == "" {
		why = found.unresolved()
	}

	if why == "" {
		why = s.refused(found, s.renamed(found, p.NewName), p.NewName)
	}

	if why != "" {
		s.fail(m.ID, jsonrpc.RequestFailed, "renaming "+old+" to "+p.NewName+": "+why)
		return
	}

	s.reply(m.ID, s.workspaceEdit(found, p.NewName))
}

// unrenamable returns why the target t of a view may not be renamed, or
// "": no name, one the language declares, a package's, which is its
// directory's, or one declared outside what the editor edits: by the
// standard library, which the compiler holds, or by a module the
// editor has no file or folder of, a module required by version among
// them, which the module cache holds.
func (s *Server) unrenamable(v *view, t *target) string {
	if t == nil || t.obj == nil {
		return "there is no name here to rename"
	}

	name := t.obj.Name()
	key, ok := v.keyOf(t.obj)
	switch _, pkg := t.obj.(*types.PkgName); {
	case !ok:
		return name + " is declared by the language"
	case pkg:
		return name + " names a package, whose name is its directory's"
	}

	return s.uneditable(key, name)
}

// uneditable returns why the object of key, named name, is not the
// editor's to change, or "": the language declares it, the standard
// library, which the compiler holds, a module required by version,
// which the module cache holds, or a module the editor has no file or
// folder of.
func (s *Server) uneditable(key objectKey, name string) string {
	switch {
	case strings.HasPrefix(key.file, languageFile):
		return name + " is declared by the language"
	case !filepath.IsAbs(key.file):
		return name + " is declared by the standard library, which the compiler holds"
	}

	if cache, err := importer.ModCacheDir(); err == nil && within(cache, key.file) {
		return name + " is declared by a module required by version, which the module cache holds"
	}

	m, err := importer.FindModule(filepath.Dir(key.file))
	if err == nil && m != nil && !slices.Contains(s.moduleRoots(), m.Dir) {
		return name + " is declared by the module at " + m.Dir + ", of which the editor has no file or folder open"
	}

	return ""
}

// search finds every name that declares or uses obj, an object of v,
// in the packages that can name it whose files spell its name, or the
// name renamed, the one a rename gives it, "" for none, each checked
// for every platform its files differ for: a package that spells the
// new name alone is one the rename may change the meaning of.
func (s *Server) search(v *view, obj types.Object, from, renamed string) *search {
	found := &search{name: obj.Name(), from: from, renamed: renamed, dirs: make(map[string]bool)}
	key, ok := v.keyOf(obj)
	if !ok {
		return found
	}

	found.key = key
	found.keys = map[objectKey]string{key: obj.Name()}
	s.checkDirs(found, s.searched(key, obj.Name(), from))
	found.collect()
	return found
}

// widen makes a search for a method a search for its whole dispatch:
// every method that shares the dispatch, found from the checks, and the
// packages that can name each checked too, until the dispatch grows no
// more; it returns why the dispatch may not be renamed, a method of it
// the editor does not edit, or "".
func (s *Server) widen(found *search) string {
	for {
		members := dispatchOf(found.checks, found.name, found.keys)
		keys := slices.SortedFunc(maps.Keys(members), func(a, b objectKey) int {
			return cmp.Or(strings.Compare(a.file, b.file), cmp.Compare(a.offset, b.offset))
		})

		grown := false
		for _, k := range keys { // in order, so that a refusal names the same method every time
			label := members[k]
			if why := s.uneditable(k, label); why != "" {
				return "it shares its dispatch with " + label + ", which " + strings.TrimPrefix(why, label+" ")
			}

			if _, ok := found.keys[k]; !ok {
				found.keys[k] = label
				grown = true
				s.checkDirs(found, s.searched(k, found.name, found.from))
			}
		}

		if !grown {
			found.collect()
			return ""
		}
	}
}

// checkDirs checks each package of dirs a search has not, whose files
// spell the name or the new one, for every platform its files differ
// for, and keeps the checks, trimmed to the package's own files.
func (s *Server) checkDirs(found *search, dirs []string) {
	for _, dir := range dirs {
		if found.dirs[dir] {
			continue
		}

		found.dirs[dir] = true
		if !s.spells(dir, found.name) && (found.renamed == "" || !s.spells(dir, found.renamed)) {
			continue
		}

		for _, t := range targetsOf(dir) {
			reports, views := s.checks(dir, nil, t, importer.Place{})
			for i, cv := range views {
				cv.trim(ownFiles(dir, cv))
				found.checks = append(found.checks, checked{dir, t, reports[i], cv})
			}
		}
	}
}

// collect finds in the checks of a search every name that declares or
// uses one of its objects, each once, in order.
func (found *search) collect() {
	seen := make(map[use]bool)
	for _, c := range found.checks {
		c.view.each(func(id *ast.Ident, o types.Object, decl bool) {
			if k, ok := c.view.keyOf(o); ok {
				if _, wanted := found.keys[k]; wanted {
					file := c.view.fileOf(id.NamePos)
					start := file.Offset(id.NamePos)
					seen[use{path: stdPathOf(file.Name()), start: start, end: start + len(id.Name), decl: decl && k.offset == start && k.file == file.Name(), key: k}] = true
				}
			}
		})
	}

	found.uses = found.uses[:0]
	for u := range seen {
		found.uses = append(found.uses, u)
	}

	slices.SortFunc(found.uses, func(a, b use) int { return cmp.Or(strings.Compare(a.path, b.path), cmp.Compare(a.start, b.start)) })
}

// searched returns the packages that can name the object of key, named
// name, sorted: its own alone for a name no other package can name, one
// not exported; else its own, when it is a package of the editor's, and
// every package of the workspace's modules that imports it, directly or
// through another, each import resolved as the importer resolves it;
// and for a name declared outside them, every package of the
// workspace's modules. The package of the document asked from is among
// them, unless the document is a copy of the standard library's.
func (s *Server) searched(key objectKey, name, from string) []string {
	own := ""
	if filepath.IsAbs(key.file) {
		own = filepath.Dir(key.file)
	}

	if own != "" && !ast.IsExported(name) {
		return []string{own}
	}

	packages, importedBy := s.importGraph()
	var dirs []string
	if own != "" && (slices.Contains(packages, own) || importedBy[own] != nil) {
		dirs = []string{own}
		for i := 0; i < len(dirs); i++ {
			for _, by := range importedBy[dirs[i]] {
				if !slices.Contains(dirs, by) {
					dirs = append(dirs, by)
				}
			}
		}
	} else {
		dirs = slices.Clone(packages)
		if own != "" {
			dirs = append(dirs, own)
		}
	}

	if dir := filepath.Dir(from); !slices.Contains(dirs, dir) {
		if _, copied := stdCopyOf(dir); !copied {
			dirs = append(dirs, dir)
		}
	}

	slices.Sort(dirs)
	return slices.Compact(dirs)
}

// importGraph returns the packages of the workspace's modules and, by a
// package's directory, the packages that import it, read from every
// platform's files, each import resolved through its module as the
// importer resolves it, Module.Lookup, a require of a directory or of
// a sibling module included; the module's requirements are taken from
// the module cache alone.
func (s *Server) importGraph() ([]string, map[string][]string) {
	var packages []string
	importedBy := make(map[string][]string)
	for _, root := range s.moduleRoots() {
		m, err := importer.FindModule(root)
		if err != nil || m == nil {
			continue
		}

		m.ResolveCached() // a module the cache lacks resolves nothing, and its imports are passed over
		for _, pkg := range importer.Packages(root) {
			packages = append(packages, pkg)
			for _, path := range importer.PackageFiles(pkg) {
				src, err := s.read(path)
				if err != nil {
					continue
				}

				for _, imp := range importsOf(path, src) {
					dir, _, err := m.Lookup(imp)
					if dir = filepath.Clean(dir); err == nil && dir != "." && !slices.Contains(importedBy[dir], pkg) {
						importedBy[dir] = append(importedBy[dir], pkg)
					}
				}
			}
		}
	}

	return packages, importedBy
}

// spells reports whether a file of the package in dir, for any
// platform, has name as a name: no other file can use it.
func (s *Server) spells(dir, name string) bool {
	for _, path := range importer.PackageFiles(dir) {
		src, err := s.read(path)
		if err == nil && hasName(path, src, name) {
			return true
		}
	}

	return false
}

// untyped returns why a search cannot be trusted with a rename: a
// package it searched whose check did not type its files, since one of
// them does not parse, whose uses it does not know, named by the first
// file the check reports an error on; or "".
func (found *search) untyped() string {
	for _, c := range found.checks {
		var untyped []string
		for path, f := range c.view.byPath {
			if !c.view.typed(f) {
				untyped = append(untyped, path)
			}
		}

		if len(untyped) == 0 {
			continue
		}

		slices.Sort(untyped)
		at := untyped[0]
		for _, path := range untyped {
			if len(c.report[path]) > 0 {
				at = path
				break
			}
		}

		return relative(at) + " does not check as it stands, so the uses of " + found.name + " in its package are not known: fix it first"
	}

	return ""
}

// dispatchOf returns the methods named name in the checks that share a
// dispatch with one of the methods of keys, by key, each named
// Holder.Method, keys' own among them: a type's method that implements
// a trait's, a trait's that overrides one of a trait it refines, and so
// on, the dispatch closed over. Two methods share a dispatch when a
// type implements, or a trait refines, a trait whose method of that
// name the type or trait answers with one of them. A method of a trait
// the language declares has no file, and is keyed by languageFile and
// its trait's name.
func dispatchOf(checks []checked, name string, keys map[objectKey]string) map[objectKey]string {
	parent := make(map[objectKey]objectKey)
	var find func(objectKey) objectKey
	find = func(k objectKey) objectKey {
		if p, ok := parent[k]; ok && p != k {
			parent[k] = find(p)
			return parent[k]
		}

		return k
	}

	labels := make(map[objectKey]string)
	for k, label := range keys {
		labels[k] = label
	}

	keyOf := func(v *view, m *types.Func, holder string) objectKey {
		if k, ok := v.keyOf(m); ok {
			return k
		}

		return objectKey{file: languageFile + holder}
	}

	for _, c := range checks {
		var named []*types.TypeName
		var traits []*types.Trait
		c.view.each(func(_ *ast.Ident, o types.Object, _ bool) {
			if tn, ok := o.(*types.TypeName); ok {
				named = append(named, tn)
				if tr, ok := tn.Type().(*types.Trait); ok {
					traits = append(traits, tr)
				}
			}
		})

		for _, tr := range traits {
			m := tr.Method(name)
			if m == nil {
				continue
			}

			km := keyOf(c.view, m, tr.Obj().Name())
			if declaresMethod(tr, traits, m, c.view) {
				labels[km] = tr.Obj().Name() + "." + name
			}

			for _, tn := range named {
				var own *types.Func
				switch h := tn.Type().(type) {
				case *types.Named:
					if h.ImplementsDecl(tr) {
						own = h.Method(name)
					}
				case *types.Trait:
					if h != tr && h.RefinesDecl(tr) {
						own = h.Method(name)
					}
				}

				if own == nil {
					continue
				}

				ko := keyOf(c.view, own, tn.Name())
				if ko == km {
					continue
				}

				if _, ok := labels[ko]; !ok {
					labels[ko] = tn.Name() + "." + name
				}

				parent[find(ko)] = find(km)
			}
		}
	}

	members := make(map[objectKey]string)
	for k, label := range labels {
		for start := range keys {
			if find(k) == find(start) {
				members[k] = label
				break
			}
		}
	}

	return members
}

// declaresMethod reports whether the trait tr declares its method m,
// of the traits of one check: m is what tr answers with, and no trait
// tr refines answers with it, as one tr inherits it from would.
func declaresMethod(tr *types.Trait, traits []*types.Trait, m *types.Func, v *view) bool {
	k, _ := v.keyOf(m)
	for _, other := range traits {
		if other != tr && tr.Refines(other) {
			if om := other.Method(m.Name()); om != nil {
				if ok2, ok := v.keyOf(om); ok && ok2 == k {
					return false
				}
			}
		}
	}

	return true
}

// renamed returns the text of each file a search found a use in, every
// use renamed to name.
func (s *Server) renamed(found *search, name string) map[string][]byte {
	over := make(map[string][]byte)
	for _, u := range slices.Backward(found.uses) {
		text, ok := over[u.path]
		if !ok {
			text = slices.Clone(s.textOf(found, u.path))
		}

		over[u.path] = slices.Concat(text[:u.start], []byte(name), text[u.end:])
	}

	return over
}

// refused checks the packages of a search again with the renamed text
// of over, and returns why the rename to name is refused, or "": an
// error the checker reports that the checks before it did not, as the
// file, the line and column, and the checker's message, the first in
// the order of the files; a name that refers after the rename to
// another declaration than before it, which a name renamed into the
// scope of another of the same name would; or a dispatch the renamed
// methods would join under the new name, which a method renamed onto a
// name its trait's dispatch already uses would.
func (s *Server) refused(found *search, over map[string][]byte, name string) string {
	var again []checked
	for _, c := range found.checks {
		reports, views := s.checks(c.dir, over, c.target, importer.Place{})
		for i, cv := range views {
			cv.trim(ownFiles(c.dir, cv))
			again = append(again, checked{c.dir, c.target, reports[i], cv})
		}
	}

	if why := found.newErrors(again, name); why != "" {
		return why
	}

	if why := found.captured(again, name); why != "" {
		return why
	}

	shift := found.shifter(name)
	moved := make(map[objectKey]string)
	for k, label := range found.keys {
		moved[objectKey{k.file, shift(stdPathOf(k.file), k.offset)}] = label
	}

	var joined []string
	for k, label := range dispatchOf(again, name, moved) {
		if _, ok := moved[k]; !ok {
			joined = append(joined, label)
		}
	}

	if len(joined) == 0 {
		return ""
	}

	slices.Sort(joined)
	return "it would share a dispatch with " + strings.Join(joined, ", ") + ", which it does not now"
}

// newErrors returns the first error the checks again report that the
// checks of the search did not, or "": an error of before matches one
// of after with the same file and message, read either as it was or
// with the old name, a whole word, written as the new one, since an
// error about the renamed name says the new one and one about another
// of the old name says it still; a line and column a message holds,
// "first declared at 3:4", are no part of it, since the edits move
// them.
func (found *search) newErrors(again []checked, name string) string {
	type earlier struct {
		path, as, renamed string
		used              bool
	}

	var before []*earlier
	for _, c := range found.checks {
		for path, diags := range c.report {
			for _, d := range diags {
				before = append(before, &earlier{path, withoutPlaces(d.Message), withoutPlaces(renameWord(d.Message, found.name, name)), false})
			}
		}
	}

	type news struct {
		path      string
		line, col uint32
		message   string
	}

	var errs []news
	for _, c := range again {
		for path, diags := range c.report {
			for _, d := range diags {
				msg := withoutPlaces(d.Message)
				i := slices.IndexFunc(before, func(e *earlier) bool { return !e.used && e.path == path && (e.as == msg || e.renamed == msg) })
				if i >= 0 {
					before[i].used = true
					continue
				}

				errs = append(errs, news{path, d.Range.Start.Line, d.Range.Start.Character, d.Message})
			}
		}
	}

	if len(errs) == 0 {
		return ""
	}

	first := slices.MinFunc(errs, func(a, b news) int {
		return cmp.Or(strings.Compare(a.path, b.path), cmp.Compare(a.line, b.line), cmp.Compare(a.col, b.col))
	})

	return relative(first.path) + ":" + strconv.Itoa(int(first.line)+1) + ":" + strconv.Itoa(int(first.col)+1) + ": " + first.message
}

// unresolved returns why a search cannot be trusted with a rename: a
// name spelled as the old one in a file it checked that the check gave
// no object, as a use of a package that did not load has none, so that
// what it names is not known; or "". The package's own name, in its
// package clause, names no object.
func (found *search) unresolved() string {
	for _, c := range found.checks {
		resolved := make(map[*token.File]map[token.Pos]bool)
		c.view.each(func(id *ast.Ident, _ types.Object, _ bool) {
			if id.Name != found.name {
				return
			}

			f := c.view.fileOf(id.NamePos)
			if resolved[f] == nil {
				resolved[f] = make(map[token.Pos]bool)
			}

			resolved[f][id.NamePos] = true
		})

		paths := slices.Sorted(maps.Keys(c.view.byPath))
		for _, path := range paths {
			f := c.view.byPath[path]
			src, err := c.view.source(f.Name())
			if err != nil {
				continue
			}

			clause := token.NoPos
			if syntax := c.view.syntax[f]; syntax != nil && syntax.Name != nil {
				clause = syntax.Name.NamePos
			}

			for _, at := range namesAt(f.Name(), src, found.name) {
				if pos := f.Pos(at); pos != clause && !resolved[f][pos] {
					p := positionOf(src, at, protocol.PositionEncodingUTF8)
					return "the " + found.name + " at " + relative(path) + ":" + strconv.Itoa(int(p.Line)+1) + ":" + strconv.Itoa(int(p.Character)+1) + " names nothing the checker knows, as a use of a package that does not compile names nothing, so it cannot be renamed with the rest: fix the errors first"
				}
			}
		}
	}

	return ""
}

// shifter returns the function that moves an offset of a file by the
// rename to name: by the change in length of every renamed name before
// it in the file.
func (found *search) shifter(name string) func(path string, offset int) int {
	edits := make(map[string][]int)
	for _, u := range found.uses {
		edits[u.path] = append(edits[u.path], u.start)
	}

	return func(path string, offset int) int {
		n := 0
		for _, at := range edits[path] {
			if at < offset {
				n++
			}
		}

		return offset + n*(len(name)-len(found.name))
	}
}

// captured returns where a name of the renamed checks again refers to
// another declaration than its own did before the rename, or "": a
// renamed name must refer to the renamed object, and a name spelled as
// the new name before it must refer to what it did, each at its place
// moved by the edits before it in its file.
func (found *search) captured(again []checked, name string) string {
	shift := found.shifter(name)
	want := make(map[spot]objectKey)
	for _, u := range found.uses {
		want[spot{u.path, shift(u.path, u.start)}] = objectKey{u.key.file, shift(stdPathOf(u.key.file), u.key.offset)}
	}

	for _, c := range found.checks {
		c.view.each(func(id *ast.Ident, o types.Object, _ bool) {
			if id.Name != name {
				return
			}

			file := c.view.fileOf(id.NamePos)
			path := stdPathOf(file.Name())
			if k, ok := c.view.keyOf(o); ok {
				want[spot{path, shift(path, file.Offset(id.NamePos))}] = objectKey{k.file, shift(stdPathOf(k.file), k.offset)}
			}
		})
	}

	for _, c := range again {
		var where string
		c.view.each(func(id *ast.Ident, o types.Object, _ bool) {
			if where != "" || id.Name != name {
				return
			}

			file := c.view.fileOf(id.NamePos)
			at := spot{stdPathOf(file.Name()), file.Offset(id.NamePos)}
			k, ok := c.view.keyOf(o)
			if w, known := want[at]; known && (!ok || k != w) {
				src, _ := c.view.source(file.Name())
				p := positionOf(src, at.offset, protocol.PositionEncodingUTF8)
				where = relative(at.path) + ":" + strconv.Itoa(int(p.Line)+1) + ":" + strconv.Itoa(int(p.Character)+1)
			}
		})

		if where != "" {
			return "the " + name + " at " + where + " would refer to another declaration than it does"
		}
	}

	return ""
}

// workspaceEdit returns the edits of a rename to name: every use a
// search found, each file's in one list, in order; as documents' edits
// naming the version of each open document, for an editor that takes
// them, so that it applies none to a document that changed since.
func (s *Server) workspaceEdit(found *search, name string) protocol.WorkspaceEdit {
	byPath := make(map[string][]protocol.TextEdit)
	var paths []string
	for _, u := range found.uses {
		if byPath[u.path] == nil {
			paths = append(paths, u.path)
		}

		byPath[u.path] = append(byPath[u.path], protocol.TextEdit{Range: s.locationOf(found, u).Range, NewText: name})
	}

	if !s.documentChanges {
		edit := protocol.WorkspaceEdit{Changes: make(map[protocol.DocumentURI][]protocol.TextEdit)}
		for path, edits := range byPath {
			edit.Changes[s.uriOfPath(path)] = edits
		}

		return edit
	}

	var edit protocol.WorkspaceEdit
	for _, path := range paths {
		doc := protocol.OptionalVersionedTextDocumentIdentifier{URI: s.uriOfPath(path)}
		if d := s.byPath[path]; d != nil {
			version := d.version
			doc.Version = &version
		}

		edit.DocumentChanges = append(edit.DocumentChanges, protocol.TextDocumentEdit{TextDocument: doc, Edits: byPath[path]})
	}

	return edit
}

// textOf returns the text of the file at path as the checks of a search
// read it.
func (s *Server) textOf(found *search, path string) []byte {
	for _, c := range found.checks {
		if f := c.view.byPath[path]; f != nil {
			if src, err := c.view.source(f.Name()); err == nil {
				return src
			}
		}
	}

	src, _ := s.read(path)
	return src
}

// locationOf returns where a use is, in the protocol's positions, under
// the URI the editor opened its file by.
func (s *Server) locationOf(found *search, u use) protocol.Location {
	src := s.textOf(found, u.path)
	return protocol.Location{URI: s.uriOfPath(u.path), Range: protocol.Range{
		Start: positionOf(src, u.start, s.encoding),
		End:   positionOf(src, u.end, s.encoding),
	}}
}

// each calls f with every name of the view's own files the checker
// gave an object, the declaring ones and the using ones.
func (v *view) each(f func(id *ast.Ident, obj types.Object, decl bool)) {
	for id, obj := range v.info.Defs {
		if obj != nil {
			f(id, obj, true)
		}
	}

	for id, obj := range v.info.Uses {
		f(id, obj, false)
	}
}

// keyOf returns the key of obj, an object of the view's check; false
// for one the language declares, which no file holds.
func (v *view) keyOf(obj types.Object) (objectKey, bool) {
	if obj == nil || !obj.Pos().IsValid() {
		return objectKey{}, false
	}

	f := v.fileOf(obj.Pos())
	if f == nil {
		return objectKey{}, false
	}

	return objectKey{f.Name(), f.Offset(obj.Pos())}, true
}

// targetsOf returns the platforms the package in dir is checked for so
// that every file of it is: the host, and each other platform whose
// files differ from those of every platform before it.
func targetsOf(dir string) []platform.Target {
	host := platform.Host()
	targets := []platform.Target{host}
	seen := []string{filesKey(dir, host)}
	for _, t := range platform.Targets() {
		if k := filesKey(dir, t); !slices.Contains(seen, k) {
			seen = append(seen, k)
			targets = append(targets, t)
		}
	}

	return targets
}

// filesKey returns the files a build of the package in dir for t
// chooses, its tests among them, as one string.
func filesKey(dir string, t platform.Target) string {
	files, _ := importer.SourceFiles(dir, t)
	tests, _ := importer.TestFiles(dir, t)
	return strings.Join(append(files, tests...), "\n")
}

// importsOf returns the import paths of the file named name, src its
// text, read from its tokens alone: import "p", import name "p", and
// the paths in parentheses.
func importsOf(name string, src []byte) []string {
	file := token.NewFile(name, len(src))
	var s scanner.Scanner
	s.Init(file, src, func(token.Position, string) {})
	var paths []string
	grouped := false
	for {
		_, tok, lit := s.Scan()
		switch {
		case tok == token.EOF:
			return paths
		case tok == token.STRING:
			if p, err := strconv.Unquote(lit); err == nil {
				paths = append(paths, p)
			}
		case tok == token.IMPORT, tok == token.PACKAGE, tok == token.IDENT, tok == token.SEMICOLON:
		case tok == token.LPAREN:
			grouped = true
		case tok == token.RPAREN && grouped:
			grouped = false
		default:
			return paths // the imports are over: a declaration begins
		}
	}
}

// hasName reports whether src, the text of the file named path, has
// name as a name among its tokens.
func hasName(path string, src []byte, name string) bool {
	return len(namesAt(path, src, name)) > 0
}

// namesAt returns the offsets of the names of src, the text of the file
// named path, spelled name, in order.
func namesAt(path string, src []byte, name string) []int {
	file := token.NewFile(path, len(src))
	var s scanner.Scanner
	s.Init(file, src, func(token.Position, string) {})
	var offsets []int
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			return offsets
		}

		if tok == token.IDENT && lit == name {
			offsets = append(offsets, file.Offset(pos))
		}
	}
}

// withoutPlaces returns message with every line and column it holds,
// 3:4, written as one mark, since a rename's edits move them.
func withoutPlaces(message string) string {
	return places.ReplaceAllString(message, "#:#")
}

// renameWord returns message with every name old in it, a whole word,
// written as name: the message an error of before the rename would
// have after it.
func renameWord(message, old, name string) string {
	var b strings.Builder
	for {
		i := strings.Index(message, old)
		if i < 0 {
			b.WriteString(message)
			return b.String()
		}

		end := i + len(old)
		before, _ := utf8.DecodeLastRuneInString(message[:i])
		after, _ := utf8.DecodeRuneInString(message[end:])
		whole := (i == 0 || !isWordRune(before)) && (end == len(message) || !isWordRune(after))
		b.WriteString(message[:i])
		if whole {
			b.WriteString(name)
		} else {
			b.WriteString(old)
		}

		message = message[end:]
	}
}

// relative returns path from the working directory's module, or as it
// is when it is under none: the place a refusal names.
func relative(path string) string {
	if m, err := importer.FindModule(filepath.Dir(path)); err == nil && m != nil {
		if rel, err := filepath.Rel(m.Dir, path); err == nil {
			return filepath.ToSlash(rel)
		}
	}

	return path
}

// within reports whether path is root or under it.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && filepath.IsLocal(rel)
}

// isIdentifier reports whether name is one name to the scanner, as a
// program would declare it.
func isIdentifier(name string) bool {
	file := token.NewFile("", len(name))
	var s scanner.Scanner
	bad := false
	s.Init(file, []byte(name), func(token.Position, string) { bad = true })
	_, tok, lit := s.Scan()
	if bad || tok != token.IDENT || lit != name {
		return false
	}

	for {
		_, tok, _ := s.Scan()
		switch tok {
		case token.EOF:
			return !bad
		case token.SEMICOLON:
		default:
			return false
		}
	}
}
