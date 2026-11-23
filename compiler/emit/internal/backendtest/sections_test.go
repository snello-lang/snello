// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package backendtest

import (
	"testing"

	"github.com/snello-lang/snello/compiler/platform"
)

// TestMainOpen reads main's section from each system's listing: in the
// code section, in the plain text section, and absent.
func TestMainOpen(t *testing.T) {
	tests := []struct {
		name, system, listing string
		want                  bool
	}{
		{"macOS, open", platform.Darwin, "0000000100002d74 (__TEXT,__sn_code) external _main\n0000000100000ac0 (__TEXT,__text) external _snm_tally_Sum\n", true},
		{"macOS, closed", platform.Darwin, "0000000100002d74 (__TEXT,__text) external _main\n0000000100000ac0 (__TEXT,__sn_code) external _domain\n", false},
		{"macOS, none", platform.Darwin, "", false},
		{"Linux, open", platform.Linux, "main                |00000000000059b8|   T  |              FUNC|0000000000000040|     |.sn_code\n", true},
		{"Linux, closed", platform.Linux, "main                |00000000000033c0|   T  |              FUNC|0000000000000040|     |.text\nmainly              |00000000000059b8|   T  |              FUNC|0000000000000040|     |.sn_code\n", false},
		{"Linux, none", platform.Linux, "sn_main             |00000000000059b8|   T  |              FUNC|0000000000000040|     |.sn_code\n", false},
	}

	for _, tt := range tests {
		if got := mainOpen(tt.listing, tt.system); got != tt.want {
			t.Errorf("%s: %t, want %t", tt.name, got, tt.want)
		}
	}
}
