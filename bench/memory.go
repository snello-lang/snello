// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

package main

import (
	"fmt"
	"time"
)

const rounds = 2000

type Node struct {
	left, right *Node
}

func build(depth int) *Node {
	if depth == 0 {
		return &Node{}
	}

	return &Node{left: build(depth - 1), right: build(depth - 1)}
}

func count(n *Node) int {
	total := 1
	if n.left != nil {
		total += count(n.left)
	}

	if n.right != nil {
		total += count(n.right)
	}

	return total
}

func round() int {
	return count(build(12))
}

func main() {
	live := build(20)
	total := 0
	took := make([]time.Duration, rounds)
	all := time.Now()
	for i := 0; i < rounds; i++ {
		start := time.Now()
		total += round()
		took[i] = time.Since(start)
	}

	mean := time.Since(all) / rounds
	var longest time.Duration
	slow := 0
	for _, d := range took {
		if d > longest {
			longest = d
		}

		if d > 2*mean {
			slow++
		}
	}

	fmt.Println(count(live), total)
	fmt.Println("round: mean", mean.Microseconds(), "us, longest", longest.Microseconds(), "us,", slow, "over twice the mean")
}
