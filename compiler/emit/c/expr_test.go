// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"strings"
	"testing"
)

// conversion writes string(r) and string(rs) as the runtime's
// sn_string_from_rune and sn_string_from_runes, the slice passed as the
// runtime's slice of any element.
func TestRuneConversions(t *testing.T) {
	body := cFunction(t, emitSource(t, `package main
func main() { var r rune = 'é'; rs := "añ".Runes(); print(string(r), string(rs)) }`), "static SN_UNUSED void sn_main(void)")
	for _, want := range []string{"sn_string_from_rune(", "sn_string_from_runes((sn_slice){"} {
		if !strings.Contains(body, want) {
			t.Errorf("main lacks %q:\n%s", want, body)
		}
	}
}
