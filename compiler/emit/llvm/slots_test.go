// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

// TestSlotWords holds slotWords to the runtime's SN_SLOT_WORDS: the
// backend writes a header word's place from the first, the runtime
// lays the header out from the second, and a difference would read or
// write a slot as a word.
func TestSlotWords(t *testing.T) {
	header, err := os.ReadFile("../../../runtime/c/snello.h")
	if err != nil {
		t.Fatal(err)
	}

	m := regexp.MustCompile(`#define SN_SLOT_WORDS (\d+)`).FindSubmatch(header)
	if m == nil {
		t.Fatal("runtime/c/snello.h does not define SN_SLOT_WORDS")
	}

	words, err := strconv.Atoi(string(m[1]))
	if err != nil {
		t.Fatal(err)
	}

	if words != slotWords {
		t.Errorf("slotWords is %d, the runtime's SN_SLOT_WORDS is %d", slotWords, words)
	}
}
