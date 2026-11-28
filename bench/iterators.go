// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

package main

import (
	"fmt"
	"iter"
	"slices"
	"time"
)

const items = 1000000
const rounds = 20

// The timings are printed in tenths of a nanosecond an item.
const tenthsPerNanosecond = 10

// filter, mapped and fold are the adapters over Go's iterators, as
// the standard library's iter package shapes them: range over a
// function, each adapter a function returning one.
func filter[T any](seq iter.Seq[T], keep func(T) bool) iter.Seq[T] {
	return func(yield func(T) bool) {
		for x := range seq {
			if keep(x) && !yield(x) {
				return
			}
		}
	}
}

func mapped[T, U any](seq iter.Seq[T], f func(T) U) iter.Seq[U] {
	return func(yield func(U) bool) {
		for x := range seq {
			if !yield(f(x)) {
				return
			}
		}
	}
}

func fold[T, A any](seq iter.Seq[T], init A, f func(A, T) A) A {
	acc := init
	for x := range seq {
		acc = f(acc, x)
	}

	return acc
}

// backward gives the elements of xs from the last to the first, as
// slices.Backward does without the index.
func backward(xs []int) iter.Seq[int] {
	return func(yield func(int) bool) {
		for _, x := range slices.Backward(xs) {
			if !yield(x) {
				return
			}
		}
	}
}

// count gives the numbers from 0 up to end.
func count(end int) iter.Seq[int] {
	return func(yield func(int) bool) {
		for i := 0; i < end; i++ {
			if !yield(i) {
				return
			}
		}
	}
}

func even(x int) bool { return x%2 == 0 }

func square(x int) int { return x * x }

func add(total int, x int) int { return total + x }

func main() {
	xs := make([]int, 0, items)
	for i := 0; i < items; i++ {
		xs = append(xs, i)
	}

	// A round discarded, so that the first section timed does not pay for
	// the cold cache and the clock's ramp.
	warm := fold(mapped(filter(slices.Values(xs), even), square), 0, add)
	chain := 0
	start := time.Now()
	for r := 0; r < rounds; r++ {
		chain += fold(mapped(filter(slices.Values(xs), even), square), 0, add)
	}

	chainTime := time.Since(start)
	loop := 0
	start = time.Now()
	for r := 0; r < rounds; r++ {
		for _, x := range xs {
			if x%2 == 0 {
				loop += x * x
			}
		}
	}

	loopTime := time.Since(start)
	rev := 0
	start = time.Now()
	for r := 0; r < rounds; r++ {
		rev += fold(mapped(filter(backward(xs), even), square), 0, add)
	}

	revTime := time.Since(start)
	counted := 0
	start = time.Now()
	for r := 0; r < rounds; r++ {
		counted += fold(mapped(filter(count(len(xs)), even), square), 0, add)
	}

	countedTime := time.Since(start)
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
	fmt.Println(warm, chain, loop, rev, counted, plain)
	fmt.Println("slice chain", chainTime.Nanoseconds()*tenthsPerNanosecond/n, "slice loop", loopTime.Nanoseconds()*tenthsPerNanosecond/n, "rev chain", revTime.Nanoseconds()*tenthsPerNanosecond/n, "count chain", countedTime.Nanoseconds()*tenthsPerNanosecond/n, "count loop", plainTime.Nanoseconds()*tenthsPerNanosecond/n)
}
