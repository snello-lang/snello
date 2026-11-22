// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"runtime"
	"strconv"
	"testing"

	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/platform"
)

// TestSectionOf writes the code section of each system as a definition
// names it, the host's for an empty name, and hostOS and hostArch give
// the host's for an empty name and the name otherwise.
func TestSectionOf(t *testing.T) {
	for _, system := range []string{platform.Darwin, platform.Linux, platform.Windows, "freebsd"} {
		if got, want := sectionOf(system), " section "+strconv.Quote(lower.CodeSection(system)); got != want {
			t.Errorf("sectionOf(%q) = %q, want %q", system, got, want)
		}
	}

	if got, want := sectionOf(""), sectionOf(runtime.GOOS); got != want {
		t.Errorf("sectionOf of no name = %q, want the host's, %q", got, want)
	}

	if hostOS("") != runtime.GOOS || hostOS(platform.Windows) != platform.Windows {
		t.Errorf("hostOS gave %q and %q", hostOS(""), hostOS(platform.Windows))
	}

	if hostArch("") != runtime.GOARCH || hostArch(platform.ARM64) != platform.ARM64 {
		t.Errorf("hostArch gave %q and %q", hostArch(""), hostArch(platform.ARM64))
	}
}
