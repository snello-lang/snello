// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	"cmp"
	"slices"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// Resolve reports whether a link's name names something, as Target
// finds it.
func (p *Package) Resolve(name string) bool {
	_, _, ok := p.Target(name)
	return ok
}

// Target returns where a link's name points: the import path of the
// package that declares it, BuiltinPath for a predeclared name, and its
// anchor on that package's page, a name or a type's member, Builder or
// Builder.Len, or "" for the package itself. It looks in the package's
// own declarations and their members, then in a package by its path or
// by the name a file imports it under, then among the predeclared
// names; ok is false when the name names nothing.
func (p *Package) Target(name string) (path, anchor string, ok bool) {
	if i := strings.LastIndexByte(name, pathSep); i >= 0 {
		end, members := len(name), "" // the path ends at the first dot after its last slash
		if dot := strings.IndexByte(name[i+1:], memberSep); dot >= 0 {
			end, members = i+1+dot, name[i+2+dot:]
		}

		return name[:end], members, p.resolveIn(p.load(name[:end]), members)
	}

	first, rest, _ := strings.Cut(name, string(memberSep))
	if _, ok := p.decls[first]; ok {
		return p.Path, name, rest == "" || !strings.Contains(rest, string(memberSep)) && p.members[first][rest]
	}

	if path, ok := p.names[first]; ok {
		return path, rest, p.resolveIn(p.load(path), rest)
	}

	if pkg := p.load(first); pkg != nil {
		return first, rest, p.resolveIn(pkg, rest)
	}

	if p.Path != BuiltinPath {
		if b := predeclared(); b != nil {
			if path, anchor, ok := b.Target(name); ok {
				return path, anchor, true
			}
		}
	}

	return BuiltinPath, name, rest == "" && types.Universe.Lookup(first) != nil
}

func (p *Package) load(path string) *types.Package {
	if p.src.Import == nil {
		return nil
	}

	return p.src.Import(path)
}

// resolveIn reports whether members, a name and perhaps a member of it
// joined by a dot, names something in pkg; none names the package.
func (p *Package) resolveIn(pkg *types.Package, members string) bool {
	if pkg == nil {
		return false
	}

	if members == "" {
		return true
	}

	first, rest, _ := strings.Cut(members, string(memberSep))
	obj := pkg.Scope().Lookup(first)
	if obj == nil || rest == "" {
		return obj != nil
	}

	tn, ok := obj.(*types.TypeName)
	return ok && !strings.Contains(rest, string(memberSep)) && hasMember(tn.Type(), rest)
}

// hasMember reports whether a type has a method, a field or a variant
// named name.
func hasMember(t types.Type, name string) bool {
	if n, ok := t.(*types.Named); ok {
		if n.Method(name) != nil {
			return true
		}

		t = n.Underlying()
	}

	switch u := t.(type) {
	case *types.Struct:
		for i := range u.NumFields() {
			if u.Field(i).Name() == name {
				return true
			}
		}
	case *types.Enum:
		for i := range u.NumVariants() {
			if u.Variant(i).Name() == name {
				return true
			}
		}
	case *types.Trait:
		if u.Method(name) != nil {
			return true
		}

		return slices.ContainsFunc(u.ValueFields(), func(v *types.Var) bool { return v.Name() == name })
	}

	return false
}

// checkLinks resolves every link of every comment of the package, and
// records those that name nothing, in the order of the source, each at
// the line that holds it.
func (p *Package) checkLinks() {
	seen := map[*ast.CommentGroup]bool{}
	for _, groups := range []map[ast.Node]*ast.CommentGroup{p.lead, p.line} {
		for _, g := range groups {
			if seen[g] {
				continue
			}

			seen[g] = true
			for _, l := range linksOf(g) {
				if !p.Resolve(l.name) {
					p.Broken = append(p.Broken, Broken{p.position(l.at), l.name})
				}
			}
		}
	}

	slices.SortFunc(p.Broken, func(a, b Broken) int {
		return cmp.Or(cmp.Compare(a.Pos.Filename, b.Pos.Filename), cmp.Compare(a.Pos.Line, b.Pos.Line), cmp.Compare(a.Name, b.Name))
	})
}

// A link is a name a comment links to and where its line begins.
type link struct {
	name string
	at   token.Pos
}

// linksOf returns the links of a comment group, outside its code, each
// with the line it is written on: the group's own lines are parsed as
// its text is, so a line of code is skipped by the rule that makes it
// code, and a link on a block comment's later line is placed there.
func linksOf(g *ast.CommentGroup) []link {
	lines, at := g.Lines()
	var out []link
	for _, b := range parseLines(lines) {
		if _, code := b.Block.(*Code); code {
			continue
		}

		for i := b.start; i < b.end; i++ {
			for _, s := range Spans(lines[i]) {
				if s.Name != "" {
					out = append(out, link{s.Name, at[i]})
				}
			}
		}
	}

	return out
}

// position returns the file and line of at.
func (p *Package) position(at token.Pos) (pos token.Position) {
	for _, f := range p.src.Files {
		if f.Tok.Holds(at) {
			return f.Tok.Position(at)
		}
	}

	return pos
}
