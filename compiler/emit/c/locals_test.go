// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"regexp"
	"strings"
	"testing"
)

// TestLocalNames holds a local's C name to its Snello name, which a
// debugger prints it under, but where C cannot say what Snello does: two
// sibling loops' variable of one name are each that name, and so is a
// name a block's value declared once the block has ended; a local that
// shadows one still in scope, which C would read in its own initializer,
// is given a name of its own; a local named as a type of the program
// is too.
func TestLocalNames(t *testing.T) {
	out := emitSource(t, `package main

import "fmt"

struct Point { x int }

func main() {
    for j := 0; j < 2; j++ {
        fmt.Println(j)
    }

    for j := 0; j < 2; j++ {
        fmt.Println(j)
    }

    x := 1
    if x > 0 {
        x := x + 1
        fmt.Println(x)
    }

    y := if x > 0 { z := 2; z } else { 3 }
    z := y + 1
    Point := 5
    fmt.Println(x, y, z, Point)
}
`)
	head := "static SN_UNUSED void sn_main(void) {"
	start := strings.Index(out, head)
	if start < 0 {
		t.Fatalf("no function written as %q", head)
	}

	body := out[start:] // to the next function's head, past the inner blocks' braces, which are written at the left too
	if end := strings.Index(body[len(head):], "static "); end >= 0 {
		body = body[:len(head)+end]
	}

	if n := strings.Count(body, "sn_int j = "); n != 2 {
		t.Errorf("the two loops' j are written as j %d times, want 2:\n%s", n, body)
	}

	for _, want := range []*regexp.Regexp{
		regexp.MustCompile(`sn_int sn_t\d+_x = \(x \+ 1LL\);`), // the shadowing x, whose initializer reads the outer one
		regexp.MustCompile(`sn_int z = \(y \+ 1LL\);`),         // z, free again after the block that declared its own
		regexp.MustCompile(`sn_int sn_t\d+_Point = 5`),         // a local named as the program's type
	} {
		if !want.MatchString(body) {
			t.Errorf("sn_main lacks %s:\n%s", want, body)
		}
	}
}
