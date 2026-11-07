// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

package main

import (
	"fmt"
	"runtime"
)

const rounds = 409600 // 1600 batches
const slots = 256
const batch = 256

func size(i int) int {
	return 16 + (i*7%16)*16
}

func churn(id int, out chan [][]byte, in chan [][]byte, done chan int) {
	ring := make([][]byte, slots)
	made := 0
	for i := 0; i < rounds; i++ {
		n := size(i + id)
		ring[i%slots] = make([]byte, n)
		made += n
	}

	for i := 0; i < rounds/batch; i++ {
		blocks := make([][]byte, 0, batch)
		for j := 0; j < batch; j++ {
			n := size(i*batch + j + id)
			blocks = append(blocks, make([]byte, n))
			made += n
		}

		out <- blocks
		<-in
	}

	close(out)
	for range in {
	}

	done <- made
}

func main() {
	workers := runtime.NumCPU()
	chans := make([]chan [][]byte, 0, workers)
	for i := 0; i < workers; i++ {
		chans = append(chans, make(chan [][]byte, 4))
	}

	done := make(chan int, workers)
	for i := 0; i < workers; i++ {
		go churn(i, chans[i], chans[(i+1)%workers], done)
	}

	total := 0
	for i := 0; i < workers; i++ {
		total += <-done
	}

	fmt.Println(workers, "tasks allocated", total, "bytes")
}
