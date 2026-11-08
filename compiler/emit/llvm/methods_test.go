// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"strings"
	"testing"
)

// stringMethod writes Runes as a call of the runtime's sn_runes into a
// slice.
func TestStringRunes(t *testing.T) {
	body := irFunction(t, emitSource(t, `package main
func main() { rs := "añ".Runes(); print(len(rs)) }`), "sn_main")
	if !strings.Contains(body, "call void @sn_runes(") {
		t.Errorf("Runes is not written as sn_runes:\n%s", body)
	}
}
