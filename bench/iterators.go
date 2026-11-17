// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

package main

import (
	"fmt"
	"time"
)

const items = 1000000
const rounds = 20

// The timings are printed in tenths of a nanosecond an item.
const tenthsPerNanosecond = 10

func main() {
	xs := make([]int, 0, items)
	for i := 0; i < items; i++ {
		xs = append(xs, i)
	}

	loop := 0
	start := time.Now()
	for r := 0; r < rounds; r++ {
		for _, x := range xs {
			if x%2 == 0 {
				loop += x * x
			}
		}
	}

	loopTime := time.Since(start)
	plain := 0
	end := len(xs)
	start = time.Now()
	for r := 0; r < rounds; r++ {
		for i := 0; i < end; i++ {
			if i%2 == 0 {
				plain += i * i
			}
		}
	}

	plainTime := time.Since(start)
	n := int64(items * rounds)
	fmt.Println(loop, loop, plain, plain)
	fmt.Println("slice loop", loopTime.Nanoseconds()*tenthsPerNanosecond/n, "count loop", plainTime.Nanoseconds()*tenthsPerNanosecond/n)
}
