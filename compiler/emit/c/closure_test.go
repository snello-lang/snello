// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"regexp"
	"testing"
)

// callThrough matches a call of a function value, the value's C name
// as the function's and the box's.
var callThrough = regexp.MustCompile(`([\w.()*]+)\.fn\(([\w.()*]+)\.box`)

// TestCalledThroughLocal holds every call of a function value to a call
// through a local copy of it, whether the value is a struct's field, a
// parameter or an option's mapping function: the box's address the
// function receives is then the copy's, and the place that holds the
// value does not escape into a function the C compiler cannot see.
func TestCalledThroughLocal(t *testing.T) {
	out := emitSource(t, `package main

import "fmt"

struct Holder { f func(int) int }

func twice(x int) int { return x * 2 }

func apply(f func(int) int, x int) int { return f(x) }

func main() {
    h := Holder{f: twice}
    o := Some(3)
    fmt.Println(h.f(1), apply(twice, 2), o.Map(h.f))
}
`)
	calls := callThrough.FindAllStringSubmatch(out, -1)
	if len(calls) < 3 {
		t.Fatalf("found %d calls of a function value, want the field's, the parameter's and the option's:\n%s", len(calls), out)
	}

	local := regexp.MustCompile(`^sn_t\d+$`)
	for _, m := range calls {
		if m[1] != m[2] || !local.MatchString(m[1]) {
			t.Errorf("a function value is called through %q, with the box of %q, not through a local copy", m[1], m[2])
		}
	}
}
