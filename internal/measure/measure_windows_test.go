// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package measure

import (
	"os"
	"strings"
	"testing"
)

// TestRunKilled gives an error for a program ended by force, which
// Windows reports as the exit code TerminateProcess was given, 1.
func TestRunKilled(t *testing.T) {
	_, _, _, err := helper(t, New(t), modeKill)
	if err == nil || !strings.HasSuffix(err.Error(), "exit status 1") {
		t.Errorf("err %v, want one ending exit status 1", err)
	}
}

// killSelf ends the helper by force, as os.Process.Kill does.
func killSelf() {
	self, err := os.FindProcess(os.Getpid())
	if err == nil {
		self.Kill()
	}

	select {}
}
