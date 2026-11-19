// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package version holds the version of Snello this compiler is, and
// reads and orders versions as Semantic Versioning 2.0 writes them:
// MAJOR.MINOR.PATCH, a pre-release after a hyphen, build metadata
// after a plus. A release build stamps the channel it is published on
// into the binary with the linker:
//
//	go build -ldflags "-X github.com/snello-lang/snello/compiler/version.channel=stable" ./cmd/snello
package version

import (
	"runtime"
	"runtime/debug"
	"strings"
)

const (
	// Current is the version of the release this compiler is built
	// for, set at the start of each release's work: the version a
	// module's snello line is checked against.
	Current = "0.1.0"

	// First is the first release, which a module file without a
	// snello line is read as written for, so that a new release never
	// changes what such a module means.
	First = "0.1.0"

	// buildStart begins a version's build metadata, preReleaseStart its
	// pre-release, and separator joins its numbers and the identifiers
	// of each; numbers is how many numbers it has, MAJOR.MINOR.PATCH.
	buildStart      = "+"
	preReleaseStart = "-"
	separator       = "."
	numbers         = 3

	// initialMajor is the major number of a version before 1.0.0, the
	// initial development, whose minor releases may break what an
	// earlier one promised; initialMinor the minor number of one before
	// 0.1.0, whose every patch release may.
	initialMajor = "0"
	initialMinor = "0"

	// digits are the characters of a number, zero the one a number
	// longer than one digit may not begin with, and identifierChars the
	// characters of a pre-release or build identifier.
	digits          = "0123456789"
	zero            = "0"
	identifierChars = digits + "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ-"

	// devel is the channel of a build from source, which no release
	// stamped; revisionSetting and modifiedSetting are the settings of
	// the build information that hold the commit it was built from and
	// whether the tree had changes, which modifiedSuffix marks, as git
	// describe --dirty does; shortCommit is how many hex digits of the
	// commit are shown, git's short form.
	devel           = "devel"
	revisionSetting = "vcs.revision"
	modifiedSetting = "vcs.modified"
	modifiedTrue    = "true"
	modifiedSuffix  = "-dirty"
	shortCommit     = 7

	// commandName opens the line Describe writes, wordSeparator parts its
	// words, commitOpen and commitClose hold the commit, and
	// platformSeparator parts the system from the processor,
	// darwin/arm64.
	commandName       = "snello"
	wordSeparator     = " "
	commitOpen        = "("
	commitClose       = ")"
	platformSeparator = "/"
)

// channel is the channel a release build is published on, stable or
// nightly, stamped in by the linker's -X flag; empty in a build from
// source, which Describe names devel.
var channel string

// A Version is a version as SemVer writes it, read by Parse: its
// three numbers and its pre-release identifiers, each kept as the
// digits or the word written, and no build metadata, which orders
// nothing.
type Version struct {
	major, minor, patch string
	pre                 []string
}

// Parse reads s as a Semantic Versioning 2.0 version, 0.1.0 or
// 0.2.0-rc.1+build.5, and reports whether it is one: three numbers
// with no leading zero, then a pre-release of identifiers of letters,
// digits and hyphens, a numeric one with no leading zero, then build
// metadata of the same characters, any of which may start with a zero.
// It allocates the list of pre-release identifiers.
//
//	v, ok := version.Parse("0.2.0-rc.1")
func Parse(s string) (Version, bool) {
	core, build, hasBuild := strings.Cut(s, buildStart)
	if hasBuild && !identifiers(build, false) {
		return Version{}, false
	}

	core, pre, hasPre := strings.Cut(core, preReleaseStart)
	if hasPre && !identifiers(pre, true) {
		return Version{}, false
	}

	nums := strings.Split(core, separator)
	if len(nums) != numbers {
		return Version{}, false
	}

	for _, n := range nums {
		if !number(n) {
			return Version{}, false
		}
	}

	v := Version{major: nums[0], minor: nums[1], patch: nums[2]}
	if hasPre {
		v.pre = strings.Split(pre, separator)
	}

	return v, true
}

// Describe returns the line snello version prints: the compiler's
// version, the commit it was built from, the channel a release stamped
// or devel, and the system and processor it runs on, snello 0.1.0
// (8477985) stable darwin/arm64. The commit is read from the build
// information Go records, marked -dirty when the tree had changes, and
// left out when there is none, in a binary built outside a checkout. It
// reads the build information once a call.
//
//	fmt.Println(version.Describe())
func Describe() string {
	revision, modified := "", false
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			switch s.Key {
			case revisionSetting:
				revision = s.Value
			case modifiedSetting:
				modified = s.Value == modifiedTrue
			}
		}
	}

	return describe(channel, revision, modified, runtime.GOOS, runtime.GOARCH)
}

// Compare orders v and w by SemVer's precedence, -1, 0 or 1: by the
// major, minor and patch numbers, then a pre-release before the release
// it precedes, and two pre-releases identifier by identifier, numbers
// numerically and before words, words in ASCII order, and a shorter list
// first when the longer begins with it. Build metadata orders nothing.
// It allocates nothing, and reads no number into an integer, so a
// version of any length compares.
func (v Version) Compare(w Version) int {
	for _, p := range [][2]string{{v.major, w.major}, {v.minor, w.minor}, {v.patch, w.patch}} {
		if c := compareNumbers(p[0], p[1]); c != 0 {
			return c
		}
	}

	switch {
	case len(v.pre) == 0 && len(w.pre) == 0:
		return 0
	case len(v.pre) == 0:
		return 1
	case len(w.pre) == 0:
		return -1
	}

	for i := 0; i < len(v.pre) && i < len(w.pre); i++ {
		if c := compareIdentifiers(v.pre[i], w.pre[i]); c != 0 {
			return c
		}
	}

	return compareInts(len(v.pre), len(w.pre))
}

// Release reports whether v is a release, with no pre-release
// identifier: 0.2.0 and not 0.2.0-rc.1.
func (v Version) Release() bool {
	return len(v.pre) == 0
}

// Compatible reports whether a program built against v builds against
// w, by Semantic Versioning's promise, which its first number that is
// not zero carries: the same major version from 1.0.0; before it, when
// a minor release may break what an earlier one promised, the same
// minor version; and before 0.1.0 the same patch version.
func (v Version) Compatible(w Version) bool {
	switch {
	case v.major != w.major:
		return false
	case v.major != initialMajor:
		return true
	case v.minor != w.minor:
		return false
	default:
		return v.minor != initialMinor || v.patch == w.patch
	}
}

// describe writes Describe's line from its parts: the channel, empty
// for a build from source; the commit's full hash, empty when unknown,
// and whether the tree had changes; and the system and processor.
func describe(channel, revision string, modified bool, goos, goarch string) string {
	if channel == "" {
		channel = devel
	}

	line := commandName + wordSeparator + Current + wordSeparator
	if revision != "" {
		commit := revision[:min(len(revision), shortCommit)]
		if modified {
			commit += modifiedSuffix
		}

		line += commitOpen + commit + commitClose + wordSeparator
	}

	return line + channel + wordSeparator + goos + platformSeparator + goarch
}

// identifiers reports whether s is a list of identifiers joined by
// dots, each of letters, digits and hyphens and not empty; numeric
// says that one of digits alone is a number, with no leading zero, as
// a pre-release's is and build metadata's is not.
func identifiers(s string, numeric bool) bool {
	for id := range strings.SplitSeq(s, separator) {
		if id == "" || strings.Trim(id, identifierChars) != "" {
			return false
		}

		if numeric && isDigits(id) && !number(id) {
			return false
		}
	}

	return true
}

// number reports whether s is a number as SemVer writes one: digits,
// and no leading zero but in 0 itself.
func number(s string) bool {
	return isDigits(s) && (s == zero || !strings.HasPrefix(s, zero))
}

// isDigits reports whether s is one or more decimal digits.
func isDigits(s string) bool {
	return s != "" && strings.Trim(s, digits) == ""
}

// compareNumbers orders two numbers written with no leading zero: the
// one of fewer digits first, and else digit by digit.
func compareNumbers(a, b string) int {
	if c := compareInts(len(a), len(b)); c != 0 {
		return c
	}

	return strings.Compare(a, b)
}

// compareIdentifiers orders two pre-release identifiers: numbers
// numerically and before words, words in ASCII order.
func compareIdentifiers(a, b string) int {
	an, bn := isDigits(a), isDigits(b)
	switch {
	case an && bn:
		return compareNumbers(a, b)
	case an:
		return -1
	case bn:
		return 1
	}

	return strings.Compare(a, b)
}

// compareInts orders two ints, -1, 0 or 1.
func compareInts(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}

	return 0
}
