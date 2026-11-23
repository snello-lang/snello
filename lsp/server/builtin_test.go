// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/types"
	"github.com/snello-lang/snello/std"
)

// TestBuiltinIndex indexes builtinFile whole: every method a receiver
// declares, by its receiver's kind, and the names and the traits'
// methods, each where its name is and with its comment, and finds a
// method of a type of the language by the type's kind, a []byte's own
// or a slice's.
func TestBuiltinIndex(t *testing.T) {
	src, err := std.Sources.ReadFile(builtinFile)
	if err != nil {
		t.Fatal(err)
	}

	index := builtins()
	if want := strings.Count(string(src), "\nfunc ("); len(index.methods) != want {
		t.Errorf("%d methods indexed, the file declares %d", len(index.methods), want)
	}

	all := map[string]map[string]builtinDecl{"name": index.names, "method": index.methods, "trait method": index.traitMethods}
	for kind, keys := range map[string][]string{
		"name":         {"Eq", "Ord", "len", "int", "string", "true", "Option", "Result"},
		"method":       {"[]T.Push", "[]byte.Equal", "byte.IsDigit", "int.Abs", "string.Contains", "T?.IsSome", "Result.Map", "map.Get", "chan.Open", "chan<-.Clone", "<-chan.Clone"},
		"trait method": {"Eq", "Cmp", "Hash", "Clone"},
	} {
		for _, key := range keys {
			d, ok := all[kind][key]
			name := key[strings.LastIndex(key, keyDot)+1:]
			if !ok || string(src[d.start:d.end]) != name || d.doc == "" || !strings.Contains(d.decl, name) {
				t.Errorf("the %s %s: %+v, %v, want its name's place, its line and its comment", kind, key, d, ok)
			}
		}
	}

	bytes := types.NewSlice(types.Typ[types.Byte])
	for _, tt := range []struct {
		recv       types.Type
		name, decl string
	}{
		{bytes, "Equal", "func (b ref []byte) Equal(o ref []byte) bool"},
		{bytes, "Push", "func (xs mut []T) Push(v T)"},
		{types.NewRef(types.Typ[types.String], false), "Contains", "func (s ref string) Contains(t ref string) bool"},
	} {
		if d, ok := builtinMethodOf(tt.recv, tt.name); !ok || d.decl != tt.decl {
			t.Errorf("%s of %s: %q, %v, want %q", tt.name, tt.recv, d.decl, ok, tt.decl)
		}
	}

	if _, ok := builtinMethodOf(types.Typ[types.Bool], "Push"); ok {
		t.Error("a method of bool the file does not document is found")
	}
}
