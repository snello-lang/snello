// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"
)

// TestDefaultLiterals checks the struct literals that leave out fields
// with defaults: a keyed one leaves out any of them, a positional one
// only the last, and a struct without defaults keeps the rule that a
// positional literal gives every field. A keyed literal names a field
// once.
func TestDefaultLiterals(t *testing.T) {
	const decls = "struct Size { w int; h int = 1; d int = 2 }\nstruct Mid { a int = 1; b int; c int = 3 }\nstruct Plain { a int; b int }\n"
	tests := []struct {
		src  string
		want []string
	}{
		{"func f() Size { return Size{} }", nil},
		{"func f() Size { return Size{d: 5} }", nil},
		{"func f() Size { return Size{3} }", nil},
		{"func f() Size { return Size{3, 4} }", nil},
		{"func f() Size { return Size{3, 4, 5} }", nil},
		{"func f() Mid { return Mid{1, 2} }", nil}, // c, the last, declares one
		{"func f() Mid { return Mid{1} }", []string{"a positional struct literal gives every field but the last ones that declare a default: Mid has 3 fields, not 1"}}, // b declares none
		{"func f() Size { return Size{1, 2, 3, 4} }", []string{"a positional struct literal gives every field but the last ones that declare a default: Size has 3 fields, not 4"}},
		{"func f() Plain { return Plain{1} }", []string{"a positional struct literal gives every field: Plain has 2 fields, not 1"}},
		{"func f() Size { return Size{w: 1, h: 2, w: 3} }", []string{"duplicate field w in a Size literal"}},
		{"func f() Plain { return Plain{a: 1, b: 2, a: 3, b: 4} }", []string{"duplicate field a in a Plain literal", "duplicate field b in a Plain literal"}},
		{"struct Box<T> { v T; n int }\nfunc f() Box<int> { return Box{v: 1, n: 2, v: 3} }", []string{"duplicate field v in a Box<int> literal"}}, // the type inferred
	}
	for _, tt := range tests {
		got := checkErrors(t, "package p\n"+decls+tt.src)
		if len(got) != len(tt.want) {
			t.Errorf("%s:\n  got %d errors %q\n  want %d %q", tt.src, len(got), got, len(tt.want), tt.want)
			continue
		}

		for i := range got {
			if !strings.Contains(got[i], tt.want[i]) {
				t.Errorf("%s:\n  got  %q\n  want %q", tt.src, got[i], tt.want[i])
			}
		}
	}
}
