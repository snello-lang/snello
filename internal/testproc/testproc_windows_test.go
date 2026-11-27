// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testproc

import (
	"syscall"
	"testing"
)

const (
	// synchronize and terminate are the rights the test asks of a
	// process it looks at and ends.
	synchronize = 0x00100000
	terminate   = 0x0001

	// waitObject0 is what WaitForSingleObject returns for a process
	// that has ended.
	waitObject0 = 0
)

// TestPrepare leaves a command as exec makes it: the job holds the
// programs.
func TestPrepare(t *testing.T) {
	if cmd := Command("cmd", "/c", "exit"); cmd.SysProcAttr != nil {
		t.Fatalf("SysProcAttr = %+v, want none", cmd.SysProcAttr)
	}
}

// alive reports whether the process pid still runs.
func alive(pid int) bool {
	h, err := syscall.OpenProcess(synchronize, false, uint32(pid))
	if err != nil {
		return false
	}

	defer syscall.CloseHandle(h)
	ended, _ := syscall.WaitForSingleObject(h, 0)
	return ended != waitObject0
}

// kill ends the process pid, which a failing test leaves.
func kill(pid int) {
	if h, err := syscall.OpenProcess(terminate, false, uint32(pid)); err == nil {
		syscall.TerminateProcess(h, 1)
		syscall.CloseHandle(h)
	}
}
