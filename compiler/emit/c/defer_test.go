// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"regexp"
	"strings"
	"testing"
)

// A deferred call is a thunk over a record: the statement saves the
// arguments and a mut receiver's address, sets the flag and registers
// the record on the guard stack; a return takes each guard off before
// calling its thunk, last deferred first.
func TestDeferThunk(t *testing.T) {
	out := emitSource(t, `package main
import "fmt"
struct Counter { n int }
func (c mut Counter) Report() { fmt.Println(c.n) }
func main() {
	c := Counter{n: 1}
	defer c.Report()
	defer fmt.Println("done")
	c.n = 2
}`)
	body := wholeFunction(t, out, "static SN_UNUSED void sn_main(void)")
	guards := regexp.MustCompile(`sn_guard\(&(sn_t\d+), (sn_defer_\d+)\);`).FindAllStringSubmatch(body, -1)
	if len(guards) != 2 {
		t.Fatalf("want two deferred calls registered, got %d:\n%s", len(guards), body)
	}

	report, done := guards[0], guards[1]
	for _, want := range []string{
		report[1] + ".r = &",            // the receiver lent mutably, by its address
		report[1] + ".on = true;",       // the flag, set where the statement runs
		"sn_unguard(&" + done[1] + ");", // the guard off before the call
		done[2] + "(&" + done[1] + ");", // the call through the thunk
		"struct sn_drec_",               // the record, at the top
	} {
		if !strings.Contains(body, want) {
			t.Errorf("main lacks %q:\n%s", want, body)
		}
	}

	unguard := strings.Index(body, "sn_unguard(&"+done[1]+");")
	if call := strings.Index(body, done[2]+"(&"+done[1]+");"); call < unguard {
		t.Errorf("the thunk is called before its guard comes off:\n%s", body)
	}

	if strings.Index(body, done[2]+"(&") > strings.Index(body, report[2]+"(&") {
		t.Errorf("the deferred calls do not run last first:\n%s", body)
	}

	thunk := cFunction(t, out, "static void "+report[2]+"(void *sn_p)")
	if !strings.Contains(thunk, "(*sn_d->r)") {
		t.Errorf("the thunk does not reach the receiver through the record:\n%s", thunk)
	}

	thunk = cFunction(t, out, "static void "+done[2]+"(void *sn_p)")
	if !strings.Contains(thunk, "sn_d->a0") {
		t.Errorf("the thunk does not read the saved argument from the record:\n%s", thunk)
	}
}

// wholeFunction returns the function written as head, through the
// blocks inside it, up to the blank line after its closing brace.
func wholeFunction(t *testing.T, out, head string) string {
	t.Helper()
	i := strings.Index(out, head+" {\n")
	if i < 0 {
		t.Fatalf("no function written as %q", head)
	}

	body := out[i:]
	return body[:strings.Index(body, "\n}\n\n")]
}
