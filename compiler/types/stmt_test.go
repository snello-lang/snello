// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"
)

// A range over a string: what its variables get, and that the string
// is lent for the loop, not consumed by it.
func TestRangeString(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		{"func f(s string) { for i, r := range s { var a int = i; var b rune = r; print(a, b) }; print(s) }", nil}, // the offset, the rune, and s after the loop
		{"func f(s string) { for i := range s { var a int = i; print(a) } }", nil},
		{"func f(s ref string) { for _, r := range s { var b rune = r; print(b) } }", nil},
		{"func f(s string) { for _, r := range s[1:] { print(r) } }", nil},
		{"func f(s string) { for _, r := range s { var b byte = r; print(b) } }", []string{"cannot use r (rune) as byte in declaration"}},
		{"func f(s string) { for _, r := range s { s = \"x\"; print(r) } }", []string{"cannot assign to s: s is borrowed by the range loop"}},
		{"func f(s string) { for _, r := range s { print(r); keep(s); break } }", []string{"cannot move s: s is borrowed by the range loop"}},
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+tt.src)
		if len(got) != len(tt.want) {
			t.Errorf("%s:\n  got %d errors %q\n  want %d %q", tt.src, len(got), got, len(tt.want), tt.want)
			continue
		}

		for i := range got {
			if !strings.Contains(got[i], tt.want[i]) {
				t.Errorf("%s:\n  got %q\n  want it to contain %q", tt.src, got[i], tt.want[i])
			}
		}
	}
}
