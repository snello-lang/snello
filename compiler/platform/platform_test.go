// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package platform

import (
	"runtime"
	"testing"
)

// TestBuilds holds each file name to the targets it is built for: a
// system's, a processor's, both, every Unix's, and every target's for a
// name with no platform word.
func TestBuilds(t *testing.T) {
	linuxARM := Target{OS: Linux, Arch: ARM64}
	darwinAMD := Target{OS: Darwin, Arch: AMD64}
	windowsAMD := Target{OS: Windows, Arch: AMD64}
	tests := []struct {
		name                   string
		linux, darwin, windows bool // built for linux/arm64, for darwin/amd64, for windows/amd64
	}{
		{"poll.snello", true, true, true},
		{"poll_linux.snello", true, false, false},
		{"poll_darwin.snello", false, true, false},
		{"poll_windows.snello", false, false, true},
		{"poll_unix.snello", true, true, false},
		{"poll_unix_arm64.snello", true, false, false},
		{"poll_unix_test.snello", true, true, false},
		{"poll_windows_amd64.snello", false, false, true},
		{"poll_arm64.snello", true, false, false},
		{"poll_amd64.snello", false, true, true},
		{"poll_linux_arm64.snello", true, false, false},
		{"poll_linux_amd64.snello", false, false, false},
		{"poll_darwin_amd64.snello", false, true, false},
		{"poll_linux_test.snello", true, false, false},
		{"poll_darwin_arm64_test.snello", false, false, false},
		{"shim_linux.c", true, false, false},
		{"shim_unix.c", true, true, false},
		{"shim_darwin.h", false, true, false},
		{"linux.snello", true, true, true},          // no first element: a name, not a platform
		{"unix.snello", true, true, true},           // the same
		{"linux_test.snello", true, true, true},     // the same
		{"string_builder.snello", true, true, true}, // a last word that is no platform's
		{"poll_linx.snello", true, true, true},      // a misspelt system is a word
		{"arm64_linux.snello", true, false, false},  // the first element is cut: the name ends in linux
		{"poll_arm64_linux.snello", true, false, false},
		{"poll_test.snello", true, true, true},
		{"poll_.snello", true, true, true},
	}

	for _, tt := range tests {
		for _, c := range []struct {
			target Target
			want   bool
		}{{linuxARM, tt.linux}, {darwinAMD, tt.darwin}, {windowsAMD, tt.windows}} {
			if got := c.target.Builds(tt.name); got != c.want {
				t.Errorf("%s for %s: built %v, want %v", tt.name, c.target, got, c.want)
			}
		}
	}
}

// TestBuildsAnotherPlatform leaves out a file named for a system or a
// processor Snello does not build for, for every target, and its C.
func TestBuildsAnotherPlatform(t *testing.T) {
	for _, name := range []string{"poll_freebsd.snello", "poll_386.snello", "poll_freebsd_amd64.snello", "poll_linux_riscv64.snello", "poll_ios_test.snello", "shim_plan9.c", "cpu_ppc64le.c"} {
		for _, target := range Targets() {
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

func TestParse(t *testing.T) {
	for _, target := range Targets() {
		if got, err := Parse(target.String()); err != nil || got != target {
			t.Errorf("Parse(%q) = %v, %v", target, got, err)
		}
	}

	for _, c := range []struct{ in, want string }{
		{"linux", `malformed target "linux": a target is SYSTEM/PROCESSOR, linux/arm64`},
		{"linux/", `malformed target "linux/": a target is SYSTEM/PROCESSOR, linux/arm64`},
		{"/arm64", `malformed target "/arm64": a target is SYSTEM/PROCESSOR, linux/arm64`},
		{"linux/arm64/v8", `malformed target "linux/arm64/v8": a target is SYSTEM/PROCESSOR, linux/arm64`},
		{"freebsd/amd64", "unknown target freebsd/amd64: the targets are darwin/amd64, darwin/arm64, linux/amd64, linux/arm64, windows/amd64 and windows/arm64"},
		{"linux/386", "unknown target linux/386: the targets are darwin/amd64, darwin/arm64, linux/amd64, linux/arm64, windows/amd64 and windows/arm64"},
	} {
		if _, err := Parse(c.in); err == nil || err.Error() != c.want {
			t.Errorf("Parse(%q): got %v, want %q", c.in, err, c.want)
		}
	}
}

func TestTriple(t *testing.T) {
	want := map[Target]string{
		{OS: Darwin, Arch: AMD64}:  "x86_64-apple-macosx14.4",
		{OS: Darwin, Arch: ARM64}:  "arm64-apple-macosx14.4",
		{OS: Linux, Arch: AMD64}:   "x86_64-unknown-linux-gnu",
		{OS: Linux, Arch: ARM64}:   "aarch64-unknown-linux-gnu",
		{OS: Windows, Arch: AMD64}: "x86_64-pc-windows-msvc",
		{OS: Windows, Arch: ARM64}: "aarch64-pc-windows-msvc",
	}

	if len(Targets()) != len(want) {
		t.Fatalf("Targets() = %v, want the four of %v", Targets(), want)
	}

	for _, target := range Targets() {
		if got := target.Triple(); got != want[target] {
			t.Errorf("%s: Triple() = %q, want %q", target, got, want[target])
		}
	}
}

func TestSysroot(t *testing.T) {
	target := Target{OS: Linux, Arch: AMD64}
	if got, want := target.SysrootVariable(), "SNELLO_SYSROOT_linux_amd64"; got != want {
		t.Errorf("SysrootVariable() = %q, want %q", got, want)
	}

	t.Setenv("SNELLO_SYSROOT_linux_amd64", "/opt/sysroot/amd64")
	t.Setenv("SNELLO_SYSROOT_linux_arm64", "")
	if got := target.Sysroot(); got != "/opt/sysroot/amd64" {
		t.Errorf("Sysroot() = %q, want the variable's", got)
	}

	if got := (Target{OS: Linux, Arch: ARM64}).Sysroot(); got != "" {
		t.Errorf("an unset variable's Sysroot() = %q, want none", got)
	}
}

func TestExecutable(t *testing.T) {
	if got := (Target{OS: Windows, Arch: AMD64}).Executable("hello"); got != "hello.exe" {
		t.Errorf("windows: %q, want hello.exe", got)
	}

	if got := (Target{OS: Linux, Arch: AMD64}).Executable("hello"); got != "hello" {
		t.Errorf("linux: %q, want hello", got)
	}
}
