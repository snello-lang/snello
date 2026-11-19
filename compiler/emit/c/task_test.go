// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import "testing"

// TestSpawnedLiteralLeavesNoPosition holds the C written after the
// function of a spawned function literal to no Snello position, as a
// closure's.
func TestSpawnedLiteralLeavesNoPosition(t *testing.T) {
	helpersGenerated(t, emitSource(t, `package main

import "fmt"

struct Node { v int }

func main() {
    done := make(chan int)
    spawn func(out chan int) {
        out <- 1
    }(done.Clone())
    fmt.Println(<-done)
    var some *Node? = Some(&Node{v: 1})
    match some { Some(n) => fmt.Println(n.v), None => fmt.Println("none") }
}
`))
}
