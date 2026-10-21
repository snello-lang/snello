// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"os/exec"
	"testing"

	"github.com/teak-lang/teak/compiler/emit/internal/backendtest"
)

// TestChannelsFlat runs backendtest.Churn and checks that its memory
// stays flat and that a debug build of it reports no block left.
func TestChannelsFlat(t *testing.T) {
	if testing.Short() {
		t.Skip("a million channels")
	}

	cmd := exec.Command(release(t, "churn", backendtest.Churn))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if string(out) != backendtest.ChurnOut {
		t.Fatalf("printed %q, want %q", out, backendtest.ChurnOut)
	}

	if peak := backendtest.Peak(cmd.ProcessState); peak > backendtest.ChurnLimit {
		t.Errorf("peak memory %d MB, want under %d MB: channels or timers are kept past their last handle", peak>>20, backendtest.ChurnLimit>>20)
	}

	if stdout, stderr, code := run(t, "churn", backendtest.Churn, true); code != 0 || stderr != "" {
		t.Errorf("debug build: exit %d\n%s%s", code, stdout, stderr)
	}
}
