// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/doc"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
	"github.com/snello-lang/snello/std"
)

// TestBuiltinIndex indexes builtinFile whole: every method a receiver
// declares, by its receiver's kind, and every method of a trait, each
// name declared by one trait; every entry where its name is, with its
// comment, a constant's the comment of its group; and finds a method of
// a type of the language by the type's kind, through borrows and
// owned pointers, on a type declared over it, a slice's before a
// []byte's own, an integer's by int's, and a standard trait's by the
// trait's.
func TestBuiltinIndex(t *testing.T) {
	src, err := std.Sources.ReadFile(builtinFile)
	if err != nil {
		t.Fatal(err)
	}

	index := builtins()
	if want := strings.Count(string(src), "\nfunc ("); len(index.methods) != want {
		t.Errorf("%d methods indexed, the file declares %d", len(index.methods), want)
	}

	f, err := parser.ParseComments(token.NewFile(builtinFile, len(src)), src)
	if err != nil {
		t.Fatal(err)
	}

	declared := 0
	for _, d := range f.Decls {
		if trait, ok := d.(*ast.TraitDecl); ok {
			declared += len(trait.Methods)
		}
	}

	if len(index.traitMethods) != declared {
		t.Errorf("%d methods of traits indexed, the file declares %d: a name declared by two traits", len(index.traitMethods), declared)
	}

	for _, d := range index.all {
		if d.doc == "" {
			t.Errorf("%q at %d has no comment", src[d.start:d.end], d.start)
		}
	}

	all := map[string]map[string]builtinDecl{"name": index.names, "method": index.methods, "trait method": index.traitMethods}
	wanted := map[string][]string{
		"name":         {"Eq", "Ord", "len", "int", "string", "true", "false", "Option", "Result", "Some", "None", "Ok", "Err"},
		"method":       {"[]T.Push", "[]byte.Equal", "byte.IsDigit", "int.Abs", "string.Contains", "T?.IsSome", "Result.Map", "map.Get", "chan.Open", "chan<-.Clone", "<-chan.Clone"},
		"trait method": {"Eq", "Cmp", "Hash", "Clone", "Source"},
	}
	for _, kind := range slices.Sorted(maps.Keys(wanted)) {
		for _, key := range wanted[kind] {
			d, ok := all[kind][key]
			name := key[strings.LastIndex(key, keyDot)+1:]
			if !ok || string(src[d.start:d.end]) != name || d.doc == "" || !strings.Contains(d.decl, name) {
				t.Errorf("the %s %s: %+v, %v, want its name's place, its declaration and its comment", kind, key, d, ok)
			}
		}
	}

	if d := index.traitMethods["Source"]; !strings.HasPrefix(d.doc, "Source gives") {
		t.Errorf("Source's comment: %q, want its own, not its trait's", d.doc)
	}

	byteSlice := types.NewSlice(types.Typ[types.Byte])
	path := types.NewNamed(types.NewTypeName(token.NoPos, "Path", nil), types.Typ[types.String], nil)
	for _, tt := range []struct {
		recv       types.Type
		name, decl string
	}{
		{byteSlice, "Equal", "func (b ref []byte) Equal(o ref []byte) bool"},
		{byteSlice, "Push", "func (xs mut []T) Push(v T)"},
		{types.NewRef(types.Typ[types.String], false), "Contains", "func (s ref string) Contains(t ref string) bool"},
		{types.NewPointer(types.NewSlice(types.Typ[types.Int])), "Push", "func (xs mut []T) Push(v T)"},
		{path, "Contains", "func (s ref string) Contains(t ref string) bool"},
		{types.Typ[types.Int32], "CheckedAdd", "func (x ref int) CheckedAdd(o int) int?"},
		{types.Typ[types.Int], "Eq", "func (s ref Self) Eq(o ref Self) bool"},
	} {
		if d, ok := builtinMethodOf(tt.recv, tt.name); !ok || d.decl != tt.decl {
			t.Errorf("%s of %s: %q, %v, want %q", tt.name, tt.recv, d.decl, ok, tt.decl)
		}
	}

	if _, ok := builtinMethodOf(types.Typ[types.Bool], "Push"); ok {
		t.Error("a method the checker does not give bool is found")
	}
}

// TestBuiltinAt finds, in the text of builtinFile, the entry whose name
// a place is on, the declaration of a name a signature uses, the form a
// declaration's keyword or operator writes, map, ?, ... and <-, and
// nothing on a word that names nothing the file declares.
func TestBuiltinAt(t *testing.T) {
	src, err := std.Sources.ReadFile(builtinFile)
	if err != nil {
		t.Fatal(err)
	}

	text := string(src)
	for _, tt := range []struct {
		lead, at, decl string
	}{
		{"trait ", "Eq {", "trait Eq { func (s ref Self) Eq(o ref Self) bool }"},
		{"Eq(o ref Self) ", "bool }", "type bool bool"},
		{"// ", "map[K]V is a map", "map[K]V"},
		{"func (m ref ", "map[K]V) Get", "map[K]V"},
		{"func (o T", "?) Unwrap", "enum Option<T> {"},
		{"func print(args ", "...ref any)", "xs ...T"},
		{"func (ch ref ", "<-chan T) Clone", "chan T"},
	} {
		offset := strings.Index(text, tt.lead+tt.at)
		if offset < 0 {
			t.Fatalf("no %q in %s", tt.lead+tt.at, builtinFile)
		}

		if d, ok := builtinAt(src, offset+len(tt.lead)); !ok || d.decl != tt.decl {
			t.Errorf("at %q: %q, %v, want %q", tt.at, d.decl, ok, tt.decl)
		}
	}

	if d, ok := builtinAt(src, strings.Index(text, "Package builtin documents")); ok {
		t.Errorf("a word of the package's comment: %+v, want nothing", d)
	}
}

// TestIsBuiltinFile knows builtinFile in a checkout of the compiler and
// in the cache's copies, of this version and of another, and no other
// file.
func TestIsBuiltinFile(t *testing.T) {
	checkout, err := filepath.Abs(filepath.Join(stdCheckoutDir, filepath.FromSlash(builtinFile)))
	if err != nil {
		t.Fatal(err)
	}

	cache := t.TempDir()
	t.Setenv("SNELLO_CACHE", cache)
	copied := stdPathOf(builtinFile)
	other := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(copied))), "0.0.1", filepath.FromSlash(builtinFile))
	for path, want := range map[string]bool{
		checkout: true,
		copied:   true,
		other:    true,
		filepath.Join(filepath.Dir(checkout), "builtin_test.snello"): false,
		filepath.Join(t.TempDir(), "builtin", "builtin.snello"):      false,
	} {
		if got := isBuiltinFile(path); got != want {
			t.Errorf("%s: %v, want %v", path, got, want)
		}
	}
}

// TestBuiltinFormOf finds the comment of each form the syntax writes:
// the types the language writes with its own syntax and the borrows,
// in a type and at a call, and an option's by Option's; and none for
// a name.
func TestBuiltinFormOf(t *testing.T) {
	src := "package p\n\nfunc f(a []int, b [2]int, c map[int]int, d chan int, e *int, g ref int, h mut int, k int?) { f(a, b, c, d, e, ref g, mut h, k) }\n"
	f, _, err := parser.ParseFile("p.snello", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	found := make(map[string]bool)
	ast.Inspect(f, func(n ast.Node) bool {
		if d, ok := builtinFormOf(n); ok {
			if _, name := n.(*ast.Ident); name {
				t.Errorf("a name %v has a form's comment", n)
			}

			found[d.doc] = true
		}

		return true
	})

	for _, name := range []string{doc.FormSlice, doc.FormArray, doc.FormMap, doc.FormChan, doc.FormPointer, doc.FormRef, doc.FormMut} {
		if d := builtins().forms[name]; d.doc == "" || !found[d.doc] {
			t.Errorf("no comment found for the form %s", name)
		}
	}

	if !found[builtins().names[optionName].doc] {
		t.Error("no comment found for an option, Option's")
	}
}
