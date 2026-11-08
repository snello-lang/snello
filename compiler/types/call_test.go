// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"
)

// The conversions to a string: from its bytes, from a rune, a character
// literal typed as a rune, and from a []rune, borrowed operands too; an
// int is not a rune, so string(65) is refused rather than meaning "A".
func TestStringConversions(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		{"func f(r rune) string { return string(r) }", nil},
		{"func f() string { return string('é') }", nil},
		{"func f(rs []rune) string { return string(rs) }", nil},
		{"func f(rs ref []rune) string { return string(rs) }", nil},
		{"func f(b ref []byte) string { return string(b) }", nil},
		{"func f(s ref string) []byte { return []byte(s) }", nil},
		{"func f(n int) string { return string(n) }", []string{"cannot convert int to string"}},
		{"func f() string { return string(65) }", []string{"cannot convert"}},
		{"func f(xs []int) string { return string(xs) }", []string{"cannot convert []int to string"}},
		{"func f(s string) []rune { return []rune(s) }", []string{"cannot convert string to []rune"}}, // a string's runes are s.Runes()
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
