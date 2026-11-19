// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !windows

package measure

import (
	"strings"
	"syscall"
	"testing"
)

// TestRunSignal gives an error naming the signal that ended a program.
func TestRunSignal(t *testing.T) {
	_, _, _, err := helper(t, New(t), modeKill)
	if err == nil || !strings.HasSuffix(err.Error(), "signal 9") {
		t.Errorf("err %v, want one ending signal 9", err)
	}
}

// killSelf ends the helper with SIGKILL.
func killSelf() {
	syscall.Kill(syscall.Getpid(), syscall.SIGKILL)
	select {}
}
