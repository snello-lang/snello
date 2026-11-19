// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/platform"
)

// otherLinux is the Linux target that is not the host: the other
// processor's on Linux, the host's processor's on macOS.
func otherLinux() platform.Target {
	host := platform.Host()
	if host.OS != platform.Linux {
		return platform.Target{OS: platform.Linux, Arch: host.Arch}
	}

	if host.Arch == platform.AMD64 {
		return platform.Target{OS: platform.Linux, Arch: platform.ARM64}
	}

	return platform.Target{OS: platform.Linux, Arch: platform.AMD64}
}

// TestCheckTarget holds a build for another platform to what it needs:
// nothing for the host; for another Linux, a sysroot that is a
// directory, named by its variable, which a host that is not Linux must
// name, and LLVM's linker on the path; and macOS from macOS alone.
func TestCheckTarget(t *testing.T) {
	cc := Compiler()
	if _, err := CompilerID(cc); err != nil {
		t.Skip("no C compiler")
	}

	if err := CheckTarget(cc, platform.Host()); err != nil {
		t.Errorf("the host: %v", err)
	}

	target := otherLinux()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv(target.SysrootVariable(), file)
	if err := CheckTarget(cc, target); err == nil || !strings.Contains(err.Error(), target.SysrootVariable()+" names "+file+", which is not a directory") {
		t.Errorf("a sysroot that is a file: %v", err)
	}

	if platform.Host().OS != platform.Linux {
		t.Setenv(target.SysrootVariable(), "")
		if err := CheckTarget(cc, target); err == nil || !strings.Contains(err.Error(), "takes a Linux system's headers and libraries: set "+target.SysrootVariable()) {
			t.Errorf("no sysroot off Linux: %v", err)
		}
	} else {
		darwin := platform.Target{OS: platform.Darwin, Arch: platform.ARM64}
		if err := CheckTarget(cc, darwin); err == nil || !strings.Contains(err.Error(), "takes Apple's SDK and linker, which run on macOS alone") {
			t.Errorf("macOS from Linux: %v", err)
		}
	}

	path, err := exec.LookPath(cc)
	if err != nil {
		t.Fatal(err)
	}

	bare := t.TempDir() // a path holding the compiler and no linker
	if err := os.Symlink(path, filepath.Join(bare, filepath.Base(path))); err != nil {
		t.Fatal(err)
	}

	t.Setenv(target.SysrootVariable(), t.TempDir())
	t.Setenv("PATH", bare)
	bareCC := filepath.Join(bare, filepath.Base(path))
	err = CheckTarget(bareCC, target)
	if findsLinker(bareCC, lldName) { // clang's own directory holds it, which its link finds
		if err != nil {
			t.Errorf("ld.lld beside the compiler: %v", err)
		}
	} else if err == nil || !strings.Contains(err.Error(), "takes LLVM's linker, ld.lld") {
		t.Errorf("no ld.lld: %v", err)
	}
}

// TestCheckTargetCrossGCC takes a gcc built for the other Linux, which
// links with its own linker and needs no ld.lld, when the system has
// one, as Debian's gcc-x86-64-linux-gnu package installs it.
func TestCheckTargetCrossGCC(t *testing.T) {
	target := otherLinux()
	if platform.Host().OS != platform.Linux {
		t.Skip("a gcc built for another Linux runs on Linux")
	}

	cross := map[string]string{platform.AMD64: "x86_64-linux-gnu-gcc", platform.ARM64: "aarch64-linux-gnu-gcc"}[target.Arch]
	path, err := exec.LookPath(cross)
	if err != nil || !IsGCC(path) {
		t.Skip("no " + cross)
	}

	t.Setenv("PATH", filepath.Dir(path)) // no ld.lld beside it, which a gcc needs not
	if err := CheckTarget(path, target); err != nil {
		t.Errorf("%s for %s: %v", cross, target, err)
	}
}

// TestCheckTargetGCC refuses a gcc built for another target than the
// one asked for, which gcc cannot be told to build for.
func TestCheckTargetGCC(t *testing.T) {
	if !IsGCC("gcc") {
		t.Skip("no gcc")
	}

	machine, err := exec.Command("gcc", dumpMachine).Output()
	if err != nil {
		t.Fatal(err)
	}

	target := otherLinux()
	t.Setenv(target.SysrootVariable(), t.TempDir())
	if err := CheckTarget("gcc", target); err == nil || !strings.Contains(err.Error(), "gcc is gcc for "+strings.TrimSpace(string(machine))+", which builds for its own target alone") {
		t.Errorf("gcc for another target: %v", err)
	}
}

// TestTargetFlags gives clang the target's triple and the sysroot its
// variable names, gcc the sysroot alone, and the host's build nothing.
func TestTargetFlags(t *testing.T) {
	clang := toolchainClang(t)
	host := TargetFlags(clang, platform.Host())
	if want := []string{"--target=" + platform.Host().Triple()}; platform.Host().OS == platform.Darwin && !slices.Equal(host, want) {
		t.Errorf("macOS's flags: %v, want %v, which names the oldest macOS", host, want)
	} else if platform.Host().OS == platform.Linux && host != nil {
		t.Errorf("the Linux host's flags: %v", host)
	}

	target := otherLinux()
	t.Setenv(target.SysrootVariable(), "/opt/sysroot")
	if got, want := TargetFlags(clang, target), []string{"--target=" + target.Triple(), "--sysroot=/opt/sysroot"}; !slices.Equal(got, want) {
		t.Errorf("clang's flags: %v, want %v", got, want)
	}

	if IsGCC("gcc") {
		if got, want := TargetFlags("gcc", target), []string{"--sysroot=/opt/sysroot"}; !slices.Equal(got, want) {
			t.Errorf("gcc's flags: %v, want %v", got, want)
		}
	}

	if got := linkerFlags(clang, target); !slices.Equal(got, []string{useLLD}) {
		t.Errorf("another Linux's link through clang: %v, want %s", got, useLLD)
	}

	if IsGCC("gcc") {
		if got := linkerFlags("gcc", target); got != nil {
			t.Errorf("another Linux's link through gcc: %v, want its own linker", got)
		}
	}

	if got := linkerFlags(clang, platform.Host()); got != nil && platform.Host().OS != platform.Windows {
		t.Errorf("the host's link: %v", got)
	}

	if got := linkerFlags(clang, platform.Target{OS: platform.Darwin, Arch: platform.AMD64}); got != nil && platform.Host().OS == platform.Darwin {
		t.Errorf("macOS's link: %v", got)
	}
}

// TestCheckTargetWindows holds a build for Windows to what it needs:
// clang, not gcc; Microsoft's SDK and C runtime, from a sysroot that is
// a directory, which a host that is not Windows must name; and LLVM's
// COFF linker on the path.
func TestCheckTargetWindows(t *testing.T) {
	clang := toolchainClang(t)
	windows := platform.Target{OS: platform.Windows, Arch: platform.AMD64}
	if IsGCC("gcc") {
		if err := CheckTarget("gcc", windows); err == nil || !strings.Contains(err.Error(), "gcc is gcc, which builds for Windows' MinGW alone") {
			t.Errorf("gcc for Windows: %v", err)
		}
	}

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv(windows.SysrootVariable(), file)
	if err := CheckTarget(clang, windows); err == nil || !strings.Contains(err.Error(), windows.SysrootVariable()+" names "+file+", which is not a directory") {
		t.Errorf("a sysroot that is a file: %v", err)
	}

	if platform.Host().OS != platform.Windows {
		t.Setenv(windows.SysrootVariable(), "")
		if err := CheckTarget(clang, windows); err == nil || !strings.Contains(err.Error(), "takes Microsoft's SDK and C runtime: set "+windows.SysrootVariable()) {
			t.Errorf("no sysroot off Windows: %v", err)
		}
	}

	path, err := exec.LookPath(clang)
	if err != nil {
		t.Fatal(err)
	}

	bare := t.TempDir() // a path holding the compiler and no linker
	if err := os.Symlink(path, filepath.Join(bare, filepath.Base(path))); err != nil {
		t.Fatal(err)
	}

	sysroot := t.TempDir()
	t.Setenv(windows.SysrootVariable(), sysroot)
	if err := CheckTarget(clang, windows); err == nil || !strings.Contains(err.Error(), "holds no libraries for x86_64: xwin --arch x86_64 splat") {
		t.Errorf("a sysroot of another processor's libraries: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(sysroot, "crt", "lib", "x86_64"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", bare)
	bareCC := filepath.Join(bare, filepath.Base(path))
	err = CheckTarget(bareCC, windows)
	if findsLinker(bareCC, coffLinkerName) { // clang's own directory holds it, which its link finds
		if err != nil {
			t.Errorf("lld-link beside the compiler: %v", err)
		}
	} else if err == nil || !strings.Contains(err.Error(), "takes LLVM's linker, lld-link") {
		t.Errorf("no lld-link: %v", err)
	}
}

// TestFindsLinker finds a linker clang names by its path, and not one
// that is nowhere.
func TestFindsLinker(t *testing.T) {
	clang := toolchainClang(t)
	if findsLinker(clang, "snello-no-such-linker") {
		t.Error("a linker that is nowhere was found")
	}

	if _, err := exec.LookPath(lldName); err == nil && !findsLinker(clang, lldName) {
		t.Errorf("%s, on the path, was not found", lldName)
	}
}

// TestWindowsFlags gives a build for Windows the target's triple, the
// C runtime's define and, from a sysroot, the SDK's headers in the
// layout xwin writes; and its link LLVM's COFF linker and the SDK's
// libraries of the target's processor.
func TestWindowsFlags(t *testing.T) {
	sysroot := filepath.Join(t.TempDir(), "xwin")
	for _, arch := range []string{platform.AMD64, platform.ARM64} {
		target := platform.Target{OS: platform.Windows, Arch: arch}
		t.Setenv(target.SysrootVariable(), sysroot)
		if got, want := TargetFlags("clang", target), []string{"--target=" + target.Triple()}; !slices.Equal(got, want) {
			t.Errorf("%s: target flags %v, want %v", target, got, want)
		}

		compile := compileFlags(target)
		if compile[0] != noSecureWarnings || !slices.Contains(compile, filepath.Join(sysroot, "sdk", "include", "ucrt")) {
			t.Errorf("%s: compile flags %v, want %s and the SDK's headers", target, compile, noSecureWarnings)
		}

		link := linkerFlags("clang", target)
		want := "-L" + filepath.Join(sysroot, "sdk", "lib", "um", msvcArch[arch])
		if link[0] != useLLD || !slices.Contains(link, want) {
			t.Errorf("%s: link flags %v, want %s and %s", target, link, useLLD, want)
		}

		t.Setenv(target.SysrootVariable(), "")
		if got := compileFlags(target); !slices.Equal(got, []string{noSecureWarnings}) {
			t.Errorf("%s with no sysroot: compile flags %v, want the define alone, the SDK being clang's to find", target, got)
		}
	}

	if got := compileFlags(otherLinux()); got != nil {
		t.Errorf("Linux's compile flags: %v, want none", got)
	}
}

// toolchainClang returns clang, or skips the test without it.
func toolchainClang(t *testing.T) string {
	t.Helper()
	clang := "clang"
	if _, err := CompilerID(clang); err != nil || IsGCC(clang) {
		t.Skip("no clang")
	}

	return clang
}
