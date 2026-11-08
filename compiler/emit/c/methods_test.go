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
