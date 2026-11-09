// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"regexp"
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

// A deferred call is a thunk over a record whose first member is the
// flag: cleared in the entry block, so that a return a defer statement
// did not reach skips the call, set where the statement runs, and the
// record registered on the guard stack.
func TestDeferRecord(t *testing.T) {
	out := emitSource(t, `package main
import "fmt"
func work(on bool) {
	if on {
		defer fmt.Println("deferred")
	}
	fmt.Println("body")
}
func main() { work(false) }`)
	body := irFunction(t, out, "sn__work")
	entry := body[:strings.Index(body, "\n  br ")]
	m := regexp.MustCompile(`(%s\d+) = alloca \{ i1, `).FindStringSubmatch(entry)
	if m == nil {
		t.Fatalf("no record in the entry block:\n%s", body)
	}

	rec := m[1]
	for _, want := range []string{
		"store i1 false, ptr " + rec + ", align 8", // cleared at the entry
	} {
		if !strings.Contains(entry, want) {
			t.Errorf("the entry block lacks %q:\n%s", want, body)
		}
	}

	for _, want := range []string{
		"store i1 true, ptr " + rec + ", align 8",
		"call void @sn_guard(ptr " + rec + ", ptr @sn_defer_",
		"call void @sn_unguard(ptr " + rec + ")",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("work lacks %q:\n%s", want, body)
		}
	}

	if !strings.Contains(out, "define internal void @sn_defer_") {
		t.Error("no thunk written")
	}
}
