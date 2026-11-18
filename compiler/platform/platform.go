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
	"runtime"
	"strings"
)

const (
	// Darwin and Linux are the systems Snello builds for, AMD64 and
	// ARM64 the processors.
	Darwin = "darwin"
	Linux  = "linux"
	AMD64  = "amd64"
	ARM64  = "arm64"

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

// Host returns the platform the compiler runs on.
func Host() Target {
	return Target{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

// String writes the target as system/processor, linux/arm64.
func (t Target) String() string {
	return t.OS + targetSeparator + t.Arch
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
