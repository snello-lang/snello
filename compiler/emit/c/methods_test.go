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

// replaceCall takes the place's address once, reads the old value out
// through it and writes the new one in, so the place is never empty and
// a call in the value runs before the place is read.
func TestReplace(t *testing.T) {
	body := cFunction(t, emitSource(t, `package main
struct Box { name string }
func main() { b := Box{name: "a".Clone()}; old := replace(mut b.name, "b".Clone()); print(old, b.name) }`), "static SN_UNUSED void sn_main(void)")
	at := strings.Index(body, " = &b.name;")
	if at < 0 {
		t.Fatalf("main takes no address of the place:\n%s", body)
	}

	start := strings.LastIndex(body[:at], "*") + 1
	p := body[start:at] // the pointer's name
	read, write := strings.Index(body, " = *"+p+";"), strings.Index(body, "*"+p+" = sn_string_clone(")
	if read < at || write < read {
		t.Errorf("main does not read the place through %s and then write the new value:\n%s", p, body)
	}
}

// A channel of the unit value is made with an element size of zero: C
// gives the unit a byte, which a ring of done signals would hold one of
// per slot, a line every sender writes. Any other element keeps its
// size.
func TestMakeChannelOfUnit(t *testing.T) {
	body := cFunction(t, emitSource(t, `package main
func main() { done := make(chan (), 4); n := make(chan int, 4); done <- (); n <- 1; <-done; <-n }`), "static SN_UNUSED void sn_main(void)")
	if !strings.Contains(body, "sn_chan_make(0, (int64_t)(4LL)") {
		t.Errorf("the channel of the unit value is not made with an element size of zero:\n%s", body)
	}

	if !strings.Contains(body, "sn_chan_make(sizeof(sn_int), (int64_t)(4LL)") {
		t.Errorf("the channel of ints is not made with its element's size:\n%s", body)
	}
}
