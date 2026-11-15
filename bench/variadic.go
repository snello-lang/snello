// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

package main

import (
	"fmt"
	"time"
)

func Sum(xs ...int) int {
	total := 0
	for _, x := range xs {
		total += x
	}

	return total
}

func Width(parts ...string) int {
	n := 0
	for _, p := range parts {
		n += len(p)
	}

	return n
}

func main() {
	const rounds = 20000000
	a, b, c := "alpha", "be", "c"
	words := []string{"alpha", "be", "c"}
	total := 0
	start := time.Now()
	for i := 0; i < rounds; i++ {
		total += Sum(i, 2, 3)
	}

	owned := time.Since(start)
	start = time.Now()
	for i := 0; i < rounds; i++ {
		total += Width(a, b, c) + i
	}

	lent := time.Since(start)
	start = time.Now()
	for i := 0; i < rounds; i++ {
		total += Width(words...) + i
	}

	spread := time.Since(start)
	n := int64(rounds)
	fmt.Println(total)
	fmt.Println("owned", owned.Nanoseconds()*10/n, "lent", lent.Nanoseconds()*10/n, "spread", spread.Nanoseconds()*10/n)
}
