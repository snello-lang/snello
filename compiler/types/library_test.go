// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "testing"

// The predeclared string method Runes gives an owned slice of runes and
// reads its receiver.
func TestStringRunes(t *testing.T) {
	sig := builtinMethod(Typ[String], "Runes")
	if sig == nil {
		t.Fatal("string has no method Runes")
	}

	if got := sig.String(); got != "func() []rune" {
		t.Errorf("Runes is %s, want func() []rune", got)
	}

	if r, ok := sig.Recv().Type().(*Ref); !ok || r.Mut() {
		t.Errorf("Runes takes %s, want a ref of the string", sig.Recv().Type())
	}
}

// TestByteSliceMethods holds the signatures a []byte has of the
// string's text methods: the searches and tests, the trims and splits
// lending views of the receiver, the case methods giving owned slices.
// A slice of any other element has none of them.
func TestByteSliceMethods(t *testing.T) {
	bytes := NewSlice(Typ[Uint8])
	tests := []struct{ name, want string }{
		{"Find", "func(ref []u8) int?"},
		{"FindLast", "func(ref []u8) int?"},
		{"Count", "func(ref []u8) int"},
		{"HasPrefix", "func(ref []u8) bool"},
		{"HasSuffix", "func(ref []u8) bool"},
		{"Trim", "func() ref []u8"},
		{"TrimSpace", "func() ref []u8"},
		{"TrimPrefix", "func(ref []u8) ref []u8"},
		{"TrimSuffix", "func(ref []u8) ref []u8"},
		{"ToUpper", "func() []u8"},
		{"ToLower", "func() []u8"},
		{"Fields", "func() []ref []u8"},
		{"Lines", "func() []ref []u8"},
		{"Split", "func(ref []u8) []ref []u8"},
		{"SplitN", "func(ref []u8, int) []ref []u8"},
		{"SplitOnce", "func(ref []u8) (ref []u8, ref []u8)?"},
		{"SplitAfter", "func(ref []u8) []ref []u8"},
		{"SplitAfterN", "func(ref []u8, int) []ref []u8"},
		{"ContainsAny", "func(ref []u8) bool"},
		{"IndexAny", "func(ref []u8) int?"},
		{"LastIndexAny", "func(ref []u8) int?"},
		{"TrimLeft", "func(ref []u8) ref []u8"},
		{"TrimRight", "func(ref []u8) ref []u8"},
		{"TrimAny", "func(ref []u8) ref []u8"},
		{"CutPrefix", "func(ref []u8) (ref []u8)?"},
		{"CutSuffix", "func(ref []u8) (ref []u8)?"},
	}
	for _, tt := range tests {
		sig := builtinMethod(bytes, tt.name)
		if sig == nil {
			t.Errorf("[]byte has no method %s", tt.name)
			continue
		}

		if got := sig.String(); got != tt.want {
			t.Errorf("%s is %s, want %s", tt.name, got, tt.want)
		}

		if r, ok := sig.Recv().Type().(*Ref); !ok || r.Mut() {
			t.Errorf("%s takes %s, want a ref of the slice", tt.name, sig.Recv().Type())
		}
	}

	if sig := builtinMethod(bytes, "Runes"); sig != nil {
		t.Errorf("[]byte has Runes: %s", sig)
	}

	if sig := builtinMethod(NewSlice(Typ[Int]), "Trim"); sig != nil {
		t.Errorf("[]int has Trim: %s", sig)
	}

	if sig := builtinMethod(bytes, "IndexByte"); sig != nil {
		t.Errorf("[]byte has IndexByte, which Index is: %s", sig)
	}
}

// TestStringTextMethods holds the signatures of the string's searches
// for any of a set of runes and for a byte, its splits keeping the
// separator, its trims with a cutset and its cuts, which give the rest
// as an option of a borrow.
func TestStringTextMethods(t *testing.T) {
	tests := []struct{ name, want string }{
		{"ContainsAny", "func(ref string) bool"},
		{"IndexAny", "func(ref string) int?"},
		{"LastIndexAny", "func(ref string) int?"},
		{"IndexByte", "func(byte) int?"},
		{"LastIndexByte", "func(byte) int?"},
		{"SplitAfter", "func(ref string) []ref string"},
		{"SplitAfterN", "func(ref string, int) []ref string"},
		{"TrimLeft", "func(ref string) ref string"},
		{"TrimRight", "func(ref string) ref string"},
		{"TrimAny", "func(ref string) ref string"},
		{"CutPrefix", "func(ref string) ref string?"},
		{"CutSuffix", "func(ref string) ref string?"},
		{"Bytes", "func() ref []byte"},
	}
	for _, tt := range tests {
		sig := builtinMethod(Typ[String], tt.name)
		if sig == nil {
			t.Errorf("string has no method %s", tt.name)
			continue
		}

		if got := sig.String(); got != tt.want {
			t.Errorf("%s is %s, want %s", tt.name, got, tt.want)
		}

		if r, ok := sig.Recv().Type().(*Ref); !ok || r.Mut() {
			t.Errorf("%s takes %s, want a ref of the string", tt.name, sig.Recv().Type())
		}
	}
}
