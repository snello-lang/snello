// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testproc

import "testing"

// TestProcesses lists the test binary itself, with its parent and its
// group, as running.
func TestProcesses(t *testing.T) {
	wantListed(t, processes())
}
