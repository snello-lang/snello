// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package platform

import (
	"runtime"
	"testing"
)

func TestBuilds(t *testing.T) {
	linuxARM := Target{OS: Linux, Arch: ARM64}
	darwinAMD := Target{OS: Darwin, Arch: AMD64}
	tests := []struct {
		name          string
		linux, darwin bool // built for linux/arm64, for darwin/amd64
	}{
		{"poll.snello", true, true},
		{"poll_linux.snello", true, false},
		{"poll_darwin.snello", false, true},
		{"poll_arm64.snello", true, false},
		{"poll_amd64.snello", false, true},
		{"poll_linux_arm64.snello", true, false},
		{"poll_linux_amd64.snello", false, false},
		{"poll_darwin_amd64.snello", false, true},
		{"poll_linux_test.snello", true, false},
		{"poll_darwin_arm64_test.snello", false, false},
		{"shim_linux.c", true, false},
		{"shim_darwin.h", false, true},
		{"linux.snello", true, true},          // no first element: a name, not a platform
		{"linux_test.snello", true, true},     // the same
		{"string_builder.snello", true, true}, // a last word that is no platform's
		{"poll_linx.snello", true, true},      // a misspelt system is a word
		{"arm64_linux.snello", true, false},   // the first element is cut: the name ends in linux
		{"poll_arm64_linux.snello", true, false},
		{"poll_test.snello", true, true},
		{"poll_.snello", true, true},
	}

	for _, tt := range tests {
		for _, c := range []struct {
			target Target
			want   bool
		}{{linuxARM, tt.linux}, {darwinAMD, tt.darwin}} {
			if got := c.target.Builds(tt.name); got != c.want {
				t.Errorf("%s for %s: built %v, want %v", tt.name, c.target, got, c.want)
			}
		}
	}
}

// TestBuildsAnotherPlatform leaves out a file named for a system or a
// processor Snello does not build for, for every target, and its C.
func TestBuildsAnotherPlatform(t *testing.T) {
	for _, name := range []string{"poll_windows.snello", "poll_386.snello", "poll_freebsd_amd64.snello", "poll_linux_riscv64.snello", "poll_ios_test.snello", "shim_windows.c", "cpu_ppc64le.c"} {
		for _, target := range []Target{{OS: Darwin, Arch: AMD64}, {OS: Darwin, Arch: ARM64}, {OS: Linux, Arch: AMD64}, {OS: Linux, Arch: ARM64}} {
			if target.Builds(name) {
				t.Errorf("%s built for %s", name, target)
			}
		}
	}
}

func TestHost(t *testing.T) {
	h := Host()
	if h.OS != runtime.GOOS || h.Arch != runtime.GOARCH {
		t.Errorf("Host() = %v, want %s/%s", h, runtime.GOOS, runtime.GOARCH)
	}

	if got, want := (Target{OS: Linux, Arch: ARM64}).String(), "linux/arm64"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
