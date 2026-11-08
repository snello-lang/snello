// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"strings"
	"testing"
)

// stringMethod writes Runes as the runtime's sn_runes into a slice of
// the result's type.
func TestStringRunes(t *testing.T) {
	body := cFunction(t, emitSource(t, `package main
func main() { rs := "añ".Runes(); print(len(rs)) }`), "static SN_UNUSED void sn_main(void)")
	if !strings.Contains(body, "sn_runes(") {
		t.Errorf("Runes is not written as sn_runes:\n%s", body)
	}
}
