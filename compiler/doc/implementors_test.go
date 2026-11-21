// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	"slices"
	"testing"

	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// standard returns every standard package as a source for Implementors.
func standard(t *testing.T) []Source {
	t.Helper()
	imp := importer.NewImporter(&types.Info{})
	for _, path := range importer.Paths() {
		if path == BuiltinPath {
			continue
		}

		if _, err := imp.Import(path); err != nil {
			t.Fatal(err)
		}
	}

	byPath := map[string]*Source{}
	var order []string
	for _, e := range imp.Packages() {
		if byPath[e.Path] == nil {
			byPath[e.Path] = &Source{Path: e.Path}
			order = append(order, e.Path)
		}

		byPath[e.Path].Files = append(byPath[e.Path].Files, File{e.Syntax, e.File})
	}

	var out []Source
	for _, path := range order {
		out = append(out, *byPath[path])
	}

	return out
}

func TestImplementors(t *testing.T) {
	pkgs := standard(t)
	readers := Implementors("io", "Reader", pkgs, false)
	for _, want := range []Implementor{{"strings", "Reader"}, {"bytes", "Buffer"}, {"net", "Conn"}} {
		if !slices.Contains(readers, want) {
			t.Errorf("io.Reader's implementors %v lack %v", readers, want)
		}
	}

	eq := Implementors(BuiltinPath, "Eq", pkgs, false)
	if !slices.Contains(eq, Implementor{Name: "int"}) || !slices.Contains(eq, Implementor{Name: "string"}) {
		t.Errorf("Eq's implementors %v lack the predeclared int and string", eq)
	}

	if slices.Contains(Implementors(BuiltinPath, "Copy", pkgs, false), Implementor{Name: "string"}) {
		t.Error("string listed as Copy")
	}

	if slices.Contains(Implementors(BuiltinPath, "Eq", pkgs, true), Implementor{"encoding/json", "streamState"}) {
		t.Error("another package's unexported implementor shown under -u")
	}
}

// TestImplementorsUnexported shows an unexported implementor under -u
// for the trait's own package alone.
func TestImplementorsUnexported(t *testing.T) {
	src := "package geo\n\ntrait Shape { func (s ref Self) Area() int }\n\nstruct square impl Shape { side int }\n\nstruct Circle impl Shape { r int }\n"
	file := token.NewFile("geo.snello", len(src))
	f, err := parser.Parse(file, []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	pkgs := []Source{{Path: "geo", Files: []File{{f, file}}}}
	if got, want := Implementors("geo", "Shape", pkgs, false), []Implementor{{"geo", "Circle"}}; !slices.Equal(got, want) {
		t.Errorf("without -u: %v, want %v", got, want)
	}

	if got, want := Implementors("geo", "Shape", pkgs, true), []Implementor{{"geo", "Circle"}, {"geo", "square"}}; !slices.Equal(got, want) {
		t.Errorf("with -u: %v, want %v", got, want)
	}
}
