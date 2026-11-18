// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package platform names the systems and processors a Snello program
// is built for, and reads a file's name for the platform it is
// written for: a file whose name ends in a system, a processor or
// both, _linux, _arm64 or _darwin_arm64, is built only for them. It is
// modeled on Go's internal/platform, which lists the ports, on
// internal/syslist, which lists the names a file may carry, and on
// go/build's reading of a file's name.
package platform

import (
	"errors"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

const (
	// Darwin and Linux are the systems Snello builds for, AMD64 and
	// ARM64 the processors.
	Darwin = "darwin"
	Linux  = "linux"
	AMD64  = "amd64"
	ARM64  = "arm64"

	// MacOSMajor and MacOSMinor are the oldest macOS a program built for
	// darwin runs on, 14.4, the first with the waits on an address the
	// runtime's scheduler and locks are built on: every macOS build
	// names it, as its deployment target, so that a binary built with a
	// later SDK still runs on it.
	MacOSMajor = 14
	MacOSMinor = 4

	// sysrootVariable begins the name of the environment variable that
	// names the system files of a target other than the host, followed
	// by the target's system and processor, SNELLO_SYSROOT_linux_amd64,
	// so that one environment names every target's.
	sysrootVariable = "SNELLO_SYSROOT_"

	// separator parts the elements of a file's name, extension starts
	// its extension, testSuffix is the last element of a test file's
	// name, which is not a platform's, and targetSeparator parts a
	// target's system from its processor when written.
	separator       = "_"
	extension       = "."
	testSuffix      = "test"
	targetSeparator = "/"
)

var (
	// systems are the systems Snello builds for and architectures the
	// processors, each in order, for Targets and the messages that list
	// them.
	systems       = []string{Darwin, Linux}
	architectures = []string{AMD64, ARM64}

	// knownSystems and knownArchitectures are the names a file's name
	// may end in to say what it is for: every system and processor of
	// the list the package is modeled on, so that a file for one Snello
	// does not build for is left out like any other platform's, and a
	// name that only ends in a word is not.
	knownSystems = set(
		"aix", "android", Darwin, "dragonfly", "freebsd", "hurd", "illumos", "ios", "js", Linux,
		"nacl", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows", "zos",
	)
	knownArchitectures = set(
		"386", AMD64, "amd64p32", "arm", "armbe", ARM64, "arm64be", "loong64", "mips", "mipsle",
		"mips64", "mips64le", "mips64p32", "mips64p32le", "ppc", "ppc64", "ppc64le", "riscv",
		"riscv64", "s390", "s390x", "sparc", "sparc64", "wasm",
	)
)

// A Target is the platform a program is built for: its system and its
// processor.
type Target struct {
	OS   string
	Arch string
}

// Targets returns every platform Snello builds for, each system's
// processors in turn: darwin/amd64, darwin/arm64, linux/amd64 and
// linux/arm64.
func Targets() []Target {
	var all []Target
	for _, system := range systems {
		for _, arch := range architectures {
			all = append(all, Target{OS: system, Arch: arch})
		}
	}

	return all
}

// Parse reads a target written as system/processor, linux/arm64, and
// refuses one Snello does not build for, naming the targets it does.
func Parse(s string) (Target, error) {
	system, arch, ok := strings.Cut(s, targetSeparator)
	t := Target{OS: system, Arch: arch}
	if !ok || system == "" || arch == "" || strings.Contains(arch, targetSeparator) {
		return Target{}, errors.New("malformed target " + strconv.Quote(s) + ": a target is SYSTEM/PROCESSOR, linux/arm64")
	}

	if !slices.Contains(Targets(), t) {
		names := make([]string, 0, len(Targets()))
		for _, t := range Targets() {
			names = append(names, t.String())
		}

		return Target{}, errors.New("unknown target " + s + ": the targets are " + strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1])
	}

	return t, nil
}

// Host returns the platform the compiler runs on.
func Host() Target {
	return Target{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

// String writes the target as system/processor, linux/arm64.
func (t Target) String() string {
	return t.OS + targetSeparator + t.Arch
}

// Triple returns the target triple clang and LLVM name the platform
// by, the processor, the vendor and the system, with the oldest macOS
// a darwin program runs on: arm64-apple-macosx14.4 for darwin/arm64,
// x86_64-unknown-linux-gnu for linux/amd64.
func (t Target) Triple() string {
	cpu := "x86_64"
	if t.Arch == ARM64 {
		cpu = "aarch64"
	}

	if t.OS == Darwin {
		if t.Arch == ARM64 {
			cpu = "arm64"
		}

		return cpu + "-apple-macosx" + strconv.Itoa(MacOSMajor) + "." + strconv.Itoa(MacOSMinor)
	}

	return cpu + "-unknown-linux-gnu"
}

// Sysroot returns the directory holding the target's system files, its
// C library's headers and libraries, which a build for a target other
// than the host links against: the directory the environment variable
// SNELLO_SYSROOT_<system>_<processor> names, or "" when it is not set,
// for the C compiler's own, which a system's packages for building
// for another processor fill.
func (t Target) Sysroot() string {
	return os.Getenv(t.SysrootVariable())
}

// SysrootVariable returns the name of the environment variable that
// names the target's system files, SNELLO_SYSROOT_linux_amd64.
func (t Target) SysrootVariable() string {
	return sysrootVariable + t.OS + separator + t.Arch
}

// Builds reports whether the file named name, a base name, is built
// for t: after its extension is cut, and its first element and a last
// _test with it, a name ending in a system and a processor,
// _darwin_arm64, is built for that platform alone, one ending in a
// system or a processor, _linux or _amd64, for every platform of it,
// and any other name for every platform. A name with no _, or whose
// platform word is its first element, linux.snello, is built for every
// platform, so that a system named later does not change what an
// existing file means. A name for a system or a processor Snello does
// not build for, poll_windows.snello, is built for no target, so that
// one package holds the files of every system, those of a system
// Snello comes to build for later among them.
func (t Target) Builds(name string) bool {
	base, _, _ := strings.Cut(name, extension)
	_, rest, found := strings.Cut(base, separator)
	if !found {
		return true
	}

	if r, ok := strings.CutSuffix(rest, separator+testSuffix); ok { // a name that is only its first element and _test keeps the word, which is no platform's
		rest = r
	}

	last, before := rest, "" // the last element, and the one before it when there is one
	if i := strings.LastIndex(rest, separator); i >= 0 {
		last, before = rest[i+1:], rest[:i]
		if j := strings.LastIndex(before, separator); j >= 0 {
			before = before[j+1:]
		}
	}

	system, arch := "", ""
	switch {
	case before != "" && knownSystems[before] && knownArchitectures[last]:
		system, arch = before, last
	case knownSystems[last]:
		system = last
	case knownArchitectures[last]:
		arch = last
	}

	return (system == "" || system == t.OS) && (arch == "" || arch == t.Arch)
}

// set returns the words given as a set.
func set(words ...string) map[string]bool {
	s := make(map[string]bool, len(words))
	for _, w := range words {
		s[w] = true
	}

	return s
}
