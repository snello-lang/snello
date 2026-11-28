// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// twoLoops has two loops in one function, each with a variable of one
// name, j.
const twoLoops = `package main

import "fmt"

func main() {
    for j := 0; j < 1; j++ {
        fmt.Println("first", j)
    }
    for j := 0; j < 1; j++ {
        fmt.Println("second", j)
    }
}
`

var (
	loopVariable = regexp.MustCompile(`DILocalVariable\(name: "j", scope: !(\d+)`)
	locationNode = regexp.MustCompile(`(?m)^!(\d+) = !DILocation\(line: \d+, column: \d+, scope: !(\d+)\)`)
	jumpToCond   = regexp.MustCompile(`br label %for\.cond\.\d+, !dbg !(\d+)`)
	condBlock    = regexp.MustCompile(`(?m)^for\.cond\.\d+:\n((?:  .*\n)+)`)
	dbgOf        = regexp.MustCompile(`!dbg !(\d+)`)
)

// TestLoopScopeOneRange holds a loop's lexical block to one range of
// code in a debug build: the jumps into the loop's condition and the
// condition itself are in the block of the loop's variable, so that a
// debugger reading CodeView, which lifts a block of several ranges into
// the one around it, keeps the two loops' j apart.
func TestLoopScopeOneRange(t *testing.T) {
	out, err := emit(t, "loops", twoLoops, Config{Debug: true, OS: "windows", Arch: "amd64"})
	if err != nil {
		t.Fatal(err)
	}

	ir := string(out)
	scopes := map[string]bool{}
	for _, m := range loopVariable.FindAllStringSubmatch(ir, -1) {
		scopes[m[1]] = true
	}

	if len(scopes) != 2 {
		t.Fatalf("the two loops' j are in scopes %v, want two of their own", scopes)
	}

	scopeOf := map[string]string{}
	for _, m := range locationNode.FindAllStringSubmatch(ir, -1) {
		scopeOf[m[1]] = m[2]
	}

	var locations []string
	body := irFunction(t, ir, "sn_main") // the program's loops; the library's have scopes of their own
	for _, m := range jumpToCond.FindAllStringSubmatch(body, -1) {
		locations = append(locations, m[1])
	}

	for _, block := range condBlock.FindAllStringSubmatch(body, -1) {
		for _, m := range dbgOf.FindAllStringSubmatch(block[1], -1) {
			locations = append(locations, m[1])
		}
	}

	if len(locations) == 0 {
		t.Fatalf("no location of a loop's condition in:\n%s", ir)
	}

	for _, n := range locations {
		if !scopes[scopeOf[n]] {
			t.Errorf("a loop's condition or a jump to it is at !%s, in scope !%s, not a loop's own %v", n, scopeOf[n], strings.Join(slices.Sorted(maps.Keys(scopes)), ", "))
		}
	}
}
