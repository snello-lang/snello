// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"strings"
	"testing"
)

// rangeString decodes each rune with the runtime's one decoder and
// adds its width in the block after the body, where a continue goes.
func TestRangeString(t *testing.T) {
	out := emitSource(t, `package main
func main() {
	s := "añb"
	for i, r := range s { if r == 'b' { continue }; print(i, r) }
}`)
	body := irFunction(t, out, "sn_main")
	for _, want := range []string{
		"call i32 @sn_utf8_decode(ptr",
		"range.post",
		"alloca i32",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the loop lacks %q:\n%s", want, body)
		}
	}

	if !strings.Contains(out, "declare i32 @sn_utf8_decode(ptr, i64, ptr)") {
		t.Error("the decoder is not declared")
	}
}
