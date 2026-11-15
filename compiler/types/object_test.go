// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"testing"
	"unsafe"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/token"
)

// TestVarDefault reads a field's default through the index it holds
// into its package's table: the expression for a field that declares
// one, the same for the field of a generic struct's instance, made in
// another package's check, and nil for a field without one and for a
// variable that is no field.
func TestVarDefault(t *testing.T) {
	src := "package p\nstruct Box<T> { item T; n int = 3 }\nfunc f(x int) Box<string> { return Box<string>{item: \"a\"} }"
	f, file, err := parser.ParseFile("test.snello", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	info := &Info{}
	pkg, err := libraryConfig().Check("", file, f, info)
	if err != nil {
		t.Fatal(err)
	}

	show := func(v *Var) string {
		if x := v.Default(); x != nil {
			return ast.Source(x)
		}

		return "none"
	}

	box := pkg.Scope().Lookup("Box").Type().Underlying().(*Struct)
	sig := pkg.Scope().Lookup("f").Type().(*Signature)
	inst := sig.Results().Underlying().(*Struct)
	for _, tt := range []struct {
		what string
		v    *Var
		want string
	}{
		{"Box.item", box.fields[0], "none"},
		{"Box.n", box.fields[1], "3"},
		{"Box<string>.n", inst.fields[1], "3"},
		{"the parameter x", sig.Params()[0], "none"},
	} {
		if got := show(tt.v); got != tt.want {
			t.Errorf("%s: default %s, want %s", tt.what, got, tt.want)
		}
	}
}

// TestDefaultSizes holds a variable and a struct type to the sizes they
// had before fields had defaults: a field names its default by an index
// that fits beside the variable's flags, so the checker, which makes a
// variable for every name and parameter, makes no more bytes for them.
func TestDefaultSizes(t *testing.T) {
	if got := unsafe.Sizeof(Var{}); got > 64 {
		t.Errorf("a Var is %d bytes, want 64 at most", got)
	}

	if got := unsafe.Sizeof(Struct{}); got > 24 {
		t.Errorf("a Struct is %d bytes, want 24 at most", got)
	}
}

// TestSymbol keeps the symbol a backend writes on a type name, a
// function and a named type, "" before one is set.
func TestSymbol(t *testing.T) {
	tn := NewTypeName(token.NoPos, "Box", nil)
	fn := NewFunc(token.NoPos, "f", NewSignature(nil, nil, Unit))
	named := NewNamed(tn, NewStruct(nil), nil)
	for _, s := range []interface {
		Symbol() string
		SetSymbol(string)
	}{tn, fn, named} {
		if s.Symbol() != "" {
			t.Errorf("%T: symbol %q before one is set", s, s.Symbol())
		}

		s.SetSymbol("sn_x")
		if s.Symbol() != "sn_x" {
			t.Errorf("%T: symbol %q after SetSymbol, want sn_x", s, s.Symbol())
		}
	}
}
