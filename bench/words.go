// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

package main

import "fmt"

func main() {
	counts := map[string]int{}
	seed := 42
	for i := 0; i < 2000000; i++ {
		seed = (seed*1103515245 + 12345) % 2147483648
		word := fmt.Sprint("w", seed%5000)
		counts[word] += 1
	}

	best := 0
	for _, n := range counts {
		if n > best {
			best = n
		}
	}

	fmt.Println(len(counts), best)
}
