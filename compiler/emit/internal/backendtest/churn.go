// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package backendtest

import (
	"os"
	"runtime"
	"syscall"
)

// Churn makes a million channels and drops each, waits a million times
// in a select whose other case is ready before its After fires, and
// drops a thousand Ticks unread. Every channel is freed by its last
// handle, and an After or a Tick whose channel no one holds any more
// is taken out of the timers at once rather than at its deadline, so
// its memory stays flat: ChurnLimit bytes at the peak, where a channel
// kept past its last handle, or an After kept until its second passed,
// would hold a million blocks of about two hundred bytes. It prints
// ChurnOut, and a debug build of it reports no block left.
const Churn = `package main

import ("fmt"; "time")

func main() {
	sum := 0
	for i := 0; i < 1000000; i++ {
		ch := make(chan int, 1)
		in := ch.Clone()
		in <- i
		sum += <-ch ?? 0
	}

	work := make(chan int, 1)
	took := 0
	for i := 0; i < 1000000; i++ {
		work <- i
		select {
		case v := <-work:
			took += 1
		case <-time.After(time.Second):
		}
	}

	for i := 0; i < 1000; i++ {
		tick := time.Tick(time.Hour)
		_ = tick
	}

	fired := 0
	for i := 0; i < 100; i++ {
		select {
		case <-time.After(time.Microsecond):
			fired += 1
		}
	}

	fmt.Println(sum, took, fired)
}
`

// ChurnOut is what Churn prints.
const ChurnOut = "499999500000 1000000 100\n"

// ChurnLimit is the most memory Churn may hold at its peak.
const ChurnLimit = 64 << 20

// Peak returns the most memory the process that ended in state held,
// in bytes.
func Peak(state *os.ProcessState) int64 {
	peak := state.SysUsage().(*syscall.Rusage).Maxrss // bytes on macOS, kilobytes on Linux
	if runtime.GOOS != "darwin" {
		peak *= 1024
	}

	return peak
}
