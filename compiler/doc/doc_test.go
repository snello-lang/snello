// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	"slices"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// load builds the documentation of a standard package as snello doc
// loads it: checked through the importer with its comments kept, its
// links resolved against what the importer can load.
func load(t *testing.T, path string, unexported bool) *Package {
	t.Helper()
	imp := importer.NewImporter(&types.Info{})
	imp.Comments = func(p string) bool { return p == path }
	pkg, err := imp.Import(path)
	if err != nil {
		t.Fatal(err)
	}

	src := Source{Path: path, Pkg: pkg, Unexported: unexported, Import: func(p string) *types.Package {
		pkg, _ := imp.Import(p)
		return pkg
	}}
	for _, e := range imp.Packages() {
		if e.Path == path {
			src.Files = append(src.Files, File{e.Syntax, e.File})
		}
	}

	return New(src)
}

// TestNewStrings holds the model of strings to what its source says:
// its comment, its types with their methods and makers, and nothing
// unexported.
func TestNewStrings(t *testing.T) {
	p := load(t, "strings", false)
	if p.Name != "strings" || !strings.HasPrefix(p.Doc, "Package strings builds and transforms text.") {
		t.Errorf("package %s with comment %q", p.Name, p.Doc)
	}

	b := p.Type("Builder")
	if b == nil || !strings.HasPrefix(b.Doc, "A Builder accumulates text") {
		t.Fatalf("Builder: %+v", b)
	}

	if !slices.ContainsFunc(b.Methods, func(f *Func) bool { return f.Decl.Name.Name == "WriteString" }) {
		t.Error("Builder lacks its method WriteString")
	}

	r := p.Type("Reader")
	if r == nil || !slices.ContainsFunc(r.Makers, func(f *Func) bool { return f.Decl.Name.Name == "NewReader" }) {
		t.Error("NewReader is not under Reader, the type it makes")
	}

	for _, fn := range p.Funcs {
		if !ast.IsExported(fn.Decl.Name.Name) {
			t.Errorf("unexported %s shown", fn.Decl.Name.Name)
		}
	}

	if p.Func("appendString") != nil {
		t.Error("appendString shown without -u")
	}

	if load(t, "strings", true).Func("appendString") == nil {
		t.Error("appendString not shown with -u")
	}
}

// TestValueGroups holds os's constants to the groups they are written
// in: the flags of OpenFile one group, its comment the group's.
func TestValueGroups(t *testing.T) {
	p := load(t, "os", false)
	group := p.ValuesOf("O_WRONLY")
	if group == nil || group != p.ValuesOf("O_RDONLY") || !strings.Contains(group.Doc, "OpenFile") {
		t.Fatalf("O_WRONLY's group: %+v", group)
	}

	if len(group.Decls) < 3 {
		t.Errorf("the flags' group holds %d constants", len(group.Decls))
	}
}

// TestStandardLinks holds every standard package's comments to links
// that name something, as the design promises.
func TestStandardLinks(t *testing.T) {
	for _, path := range importer.Paths() {
		if path == "builtin" {
			continue
		}

		for _, b := range load(t, path, true).Broken {
			t.Errorf("%s: %s: [%s] names nothing", path, b.Pos, b.Name)
		}
	}
}

// TestForms finds the forms a file documents: a comment attached to no
// declaration that begins with a form, in the order written, with the
// place its form is written; a comment that begins with one but is a
// declaration's, or a word that is no form, documents none.
func TestForms(t *testing.T) {
	src := "package builtin\n\n// map[K]V is a map.\n\n// ref T is a borrow.\n\n// map[K]V is this function's own.\nfunc f() {}\n\n// mapping is no form.\n"
	file := token.NewFile("builtin.snello", len(src))
	f, err := parser.ParseComments(file, []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	forms := Forms(f)
	if len(forms) != 2 || forms[0].Name != FormMap || forms[0].Written != "map[K]V" || forms[0].Doc != "map[K]V is a map.\n" || forms[1].Name != FormRef {
		t.Fatalf("forms: %+v, want the map's and the borrow's, in order", forms)
	}

	if at := file.Offset(forms[0].Pos); src[at:at+len(forms[0].Written)] != forms[0].Written {
		t.Errorf("the map's form is at %d, %q", at, src[at:])
	}

	p, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, form := range p.Forms {
		names = append(names, form.Name)
	}

	if want := []string{FormSlice, FormArray, FormMap, FormChan, FormPointer, FormRef, FormMut}; !slices.Equal(names, want) {
		t.Errorf("the builtin package documents the forms %v, want %v", names, want)
	}
}
