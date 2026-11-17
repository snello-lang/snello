// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package version holds the version of Snello this compiler is, and
// reads and orders versions as Semantic Versioning 2.0 writes them:
// MAJOR.MINOR.PATCH, a pre-release after a hyphen, build metadata
// after a plus.
package version

import "strings"

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

	// digits are the characters of a number, zero the one a number
	// longer than one digit may not begin with, and identifierChars the
	// characters of a pre-release or build identifier.
	digits          = "0123456789"
	zero            = "0"
	identifierChars = digits + "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ-"
)

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
