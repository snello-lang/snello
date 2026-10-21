// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

package main

import "fmt"

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

func main() {
	total := 0
	for i := 0; i < 20; i++ {
		total += count(build(16))
	}

	fmt.Println(total)
}
