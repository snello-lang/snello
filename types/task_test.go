// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"
)

// TestTaskBoundaries checks what crosses into a spawned task and onto
// a channel: a spawned closure moves what it captures, every capture,
// argument and receiver is Send, a channel carries only Send values,
// close and range take the channel directions into account.
func TestTaskBoundaries(t *testing.T) {
	const prelude = "package main\nimport \"rc\"\n" +
		"struct W { r rc.Rc<int> }\nfunc (w W) Run() {}\n" +
		"trait Shape { func (s ref Self) Area() f64 }\ntrait Job: Send { func (j ref Self) Run() }\n" +
		"struct C impl Shape, Job { r f64 }\nfunc (c ref C) Area() f64 { return c.r }\nfunc (c ref C) Run() {}\n"
	tests := []struct {
		src  string
		want []string
	}{
		// A spawned closure moves what it captures; a Copy value is copied in.
		{"func f() { names := []string{\"a\"}; spawn func() { print(names) }(); print(names) }", []string{"use of moved value names"}},
		{"func f() { n := 1; spawn func() { print(n) }(); print(n) }", nil},
		{"func f() { names := []string{\"a\"}; for i := 0; i < 2; i++ { spawn func() { print(names) }() } }", []string{"names moved in a previous iteration of the loop"}},
		{"func f() { names := []string{\"a\"}; g := func() { spawn func() { print(names) }() }; g(); print(names) }", []string{"use of moved value names"}},
		{"func f() { names := []string{\"a\"}; g := func() { print(len(names)) }; spawn g() }", []string{"closure g captures names by borrow and cannot be spawned"}},
		{"func f() { names := []string{\"a\"}; g := func() { spawn func() { print(names) }() }; names = []string{\"b\"}; names.Push(\"c\"); g() }", nil}, // g owns what the spawn moved: it holds no borrow of names
		{"func f(xs ref []int) { spawn func() { print(len(xs)) }() }", []string{"cannot spawn with xs: ref []int is a borrow"}},
		{"func f() { names := []string{\"a\"}; spawn func() { r := ref names; print(len(names)); print(len(r)) }() }", nil}, // the body's borrows are of the task's own value
		{"func f() { names := []string{\"a\"}; r := ref names; spawn func() { print(len(names)) }(); print(len(r)) }", []string{"cannot move names: names is borrowed by r at 10:36, used at 10:100"}},

		// Send at the spawn: captures, arguments, the receiver, a trait value, a type parameter.
		{"func f() { r := rc.New(1); spawn func() { print(r.Get()) }() }", []string{"cannot spawn with r: rc.Rc<int> stays in its task"}},
		{"func show(r rc.Rc<int>) {}\nfunc f() { r := rc.New(1); spawn show(r.Clone()) }", []string{"cannot spawn with r.Clone(): rc.Rc<int> stays in its task"}},
		{"func f() { w := W{r: rc.New(1)}; spawn w.Run() }", []string{"cannot spawn with w.Run: rc.Rc<int> stays in its task"}},
		{"func f() { var s Shape = C{r: 1.0}; spawn func() { print(s.Area()) }() }", []string{"cannot spawn with s: Shape does not refine Send"}},
		{"func f() { var j Job = C{r: 1.0}; spawn func() { j.Run() }() }", nil},
		{"func f<T>(v T) { spawn func() { v2 := v; print(1) }() }", []string{"cannot spawn with v: T is not bounded by Send"}},
		{"func f<T: Send>(v T) { spawn func() { v2 := v; print(1) }() }", nil},

		// A channel carries only Send values, decided where the type is written.
		{"func f() { ch := make(chan rc.Rc<int>); print(cap(ch)) }", []string{"a channel cannot carry rc.Rc<int>: rc.Rc<int> stays in its task"}},
		{"func f() { ch := make(chan ref string); print(cap(ch)) }", []string{"a channel cannot carry a borrow"}},
		{"func f<T>(v T) { ch := make(chan T); print(cap(ch)) }", []string{"a channel cannot carry T: T is not bounded by Send"}},
		{"func f<T: Send>(v T) { ch := make(chan T); print(cap(ch)) }", nil},
		{"struct Q { in <-chan rc.Rc<int> }", []string{"a channel cannot carry rc.Rc<int>: rc.Rc<int> stays in its task"}}, // asked before the declarations were resolved

		// close and range, by channel direction.
		{"func f(ch chan int, out chan<- int) { close(ch); close(out) }", nil},
		{"func f(in <-chan int) { close(in) }", []string{"cannot close a receive-only channel"}},
		{"func f() { close(1) }", []string{"cannot close a value of type int"}},
		{"func f(ch chan int) { close(ch, ch) }", []string{"close takes one channel"}},
		{"func f(in <-chan int, ch chan int) { for v := range in { print(v) }; for range ch {} }", nil},
		{"func f(out chan<- int) { for v := range out { print(v) } }", []string{"cannot range over a value of type chan<- int"}},
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+tt.src)
		if len(got) != len(tt.want) {
			t.Errorf("%q: got %d errors %q, want %q", tt.src, len(got), got, tt.want)
			continue
		}

		for i := range got {
			if !strings.Contains(got[i], tt.want[i]) {
				t.Errorf("%q: error %q does not contain %q", tt.src, got[i], tt.want[i])
			}
		}
	}
}
