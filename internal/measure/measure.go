// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package measure runs a program for a test and reports how long it ran
// and the most memory it held, for the tests that hold the runtime's
// memory flat and for the benchmarks' table.
//
// On Linux and macOS a process's peak is read once it ended, as the
// maxrss the system keeps for it, and Linux folds into a process's
// maxrss the high-water mark of the address space it leaves when it
// calls exec. A Go process starts a child on the parent's address
// space until the child's exec, so a child of a test would report the
// test's own peak whenever that is the larger: a test that had built a
// few programs read 63 MB for one that held 3. The program is so run by
// a measurer, a small C program, as time(1) is: it forks, the child
// execs the program from the measurer's address space of a few hundred
// kilobytes, and the measurer waits for it and reports its maxrss and
// how long it ran. On Windows the program's peak working set is read
// through a handle opened as it starts, which keeps the counters past
// its end.
package measure

import "time"

// A Result is what one run of a program gave: how long it ran, and the
// most memory it held, in bytes.
type Result struct {
	Took time.Duration
	Peak int64
}
