// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"strings"
	"testing"
)

// rangeString decodes each rune with the runtime's one decoder and
// moves on by its width in the loop's step, where a continue goes.
func TestRangeString(t *testing.T) {
	out := emitSource(t, `package main
func main() {
	s := "añb"
	for i, r := range s { if r == 'b' { continue }; print(i, r) }
}`)
	body := cFunction(t, out, "static SN_UNUSED void sn_main(void)")
	for _, want := range []string{
		"sn_utf8_decode((const uint8_t *)",
		" += sn_t", // the step adds the width
		"int32_t sn_t",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the loop lacks %q:\n%s", want, body)
		}
	}

	if strings.Contains(body, "++)") {
		t.Errorf("the loop steps a byte at a time:\n%s", body)
	}
}
