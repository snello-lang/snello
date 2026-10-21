// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

package main

import (
	"fmt"
	"time"
)

func nothing(done chan struct{}) {
	done <- struct{}{}
}

func echo(in chan int, out chan int) {
	for v := range in {
		out <- v
	}
}

func main() {
	start := time.Now()
	done := make(chan struct{}, 100000)
	for i := 0; i < 100000; i++ {
		go nothing(done)
	}

	for i := 0; i < 100000; i++ {
		<-done
	}

	spawned := time.Since(start)
	in := make(chan int)
	out := make(chan int)
	go echo(in, out)
	start = time.Now()
	for i := 0; i < 500000; i++ {
		in <- i
		<-out
	}

	switched := time.Since(start)
	close(in)
	fmt.Println("spawn", spawned.Nanoseconds()/100000, "ns/op switch", switched.Nanoseconds()/1000000, "ns/op")
}
