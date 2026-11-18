// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"regexp"
	"strings"
	"testing"
)

// callThrough matches a call of a function value, the value's C name
// as the function's and the box's.
var callThrough = regexp.MustCompile(`([\w.()*]+)\.fn\(([\w.()*]+)\.box`)

// lineDirective matches a line directive of the C a backend writes, with
// the file it names.
var lineDirective = regexp.MustCompile(`^#line \d+ "([^"]*)"`)

// TestClosureLeavesNoPosition holds the C written after a closure's
// function, the backend's own helpers, to no Snello position: a drop
// function that took the closure's line would be another place a
// debugger stops at that line.
func TestClosureLeavesNoPosition(t *testing.T) {
	helpersGenerated(t, emitSource(t, `package main

import "fmt"

struct Node { v int }

func main() {
    count := 0
    add := func(k int) {
        count += k
    }
    add(2)
    var some *Node? = Some(&Node{v: 1})
    match some { Some(n) => fmt.Println(n.v), None => fmt.Println("none") }
    fmt.Println(count)
}
`))
}

// helpersGenerated fails the test for a drop function of the C out that
// a line directive places at a Snello file's line.
func helpersGenerated(t *testing.T, out string) {
	t.Helper()
	file, found := "", false
	for _, line := range strings.Split(out, "\n") {
		if m := lineDirective.FindStringSubmatch(line); m != nil {
			file = m[1]
		}

		if strings.HasPrefix(line, "static void sn_drop_") && strings.HasSuffix(line, "{") {
			found = true
			if file != "" && file != generated {
				t.Errorf("%s is at a line of %s", line, file)
			}
		}
	}

	if !found {
		t.Fatalf("no drop function in:\n%s", out)
	}
}

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
