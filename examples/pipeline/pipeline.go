// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

// Version 1: idiomatic Go.
//
// The same three-stage pipeline. Compare line by line with pipeline.snello:
// the shape is identical. The difference is what the compiler lets you do
// wrong, shown at the bottom.

package pipeline

import (
	"fmt"
	"strings"
)

type Job struct {
	id   int
	text string
}

type Result struct {
	id    int
	words int
}

func produce(n int) <-chan Job {
	out := make(chan Job)
	go func() {
		defer close(out)
		for i := 0; i < n; i++ {
			job := Job{id: i, text: strings.Repeat("snello ", i)}
			out <- job
			// job is still usable here. Go copied the struct; the string
			// inside is shared with the receiver until the GC frees it.
		}
	}()
	return out
}

func count(jobs <-chan Job) <-chan Result {
	out := make(chan Result)
	go func() {
		defer close(out)
		for job := range jobs {
			out <- Result{id: job.id, words: len(strings.Fields(job.text))}
		} // job's string is garbage; the GC will get to it eventually
	}()
	return out
}

func merge(inputs []<-chan Result) <-chan Result {
	out := make(chan Result)
	done := make(chan struct{})
	for _, in := range inputs {
		in := in
		go func() {
			for r := range in {
				out <- r
			}
			done <- struct{}{}
		}()
	}
	go func() {
		for range inputs {
			<-done
		}
		close(out)
	}()
	return out
}

func main() {
	jobs := produce(10)
	var workers []<-chan Result
	for i := 0; i < 4; i++ {
		workers = append(workers, count(jobs))
	}
	total := 0
	for r := range merge(workers) {
		total += r.words
	}
	fmt.Println(total) // 45
}

// What Go accepts that Snello rejects. All three compile and run in Go.
//
//	m := map[string]int{}
//	go func() { m["a"] = 1 }()
//	go func() { m["b"] = 2 }()       // data race: two goroutines write one map
//
//	buf := make([]byte, 1024)
//	ch <- buf                         // sent...
//	buf[0] = 1                        // ...and still mutated by the sender
//
//	job := Job{text: "x"}
//	ch <- job
//	fmt.Println(job.text)             // used after send; fine in Go, confusing in practice
