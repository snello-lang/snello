// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package testproc starts the programs a test runs so that none outlives
// the test binary. A test binary that ends abruptly, a test's timeout
// panicking in a goroutine of its own, a crash or a kill, runs none of
// its deferred calls, and a program it started would run on, its parent
// gone, for as long as it takes, a day for one that hangs. On Linux and
// macOS the programs join a process group a guard process leads, which
// watches the test binary and kills the group once it is gone; on
// Windows the test binary is in a job that kills every process in it
// when its last handle, the binary's, closes.
package testproc

import (
	"context"
	"os/exec"
)

// Command is exec.Command for a program a test runs: the program, and
// what it starts, end with the test binary at the latest.
func Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	prepare(cmd)
	return cmd
}

// CommandContext is exec.CommandContext for a program a test runs, as
// Command is.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	prepare(cmd)
	return cmd
}
