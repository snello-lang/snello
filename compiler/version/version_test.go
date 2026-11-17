// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package version

import "testing"

// TestParse holds Parse to SemVer's grammar: three numbers with no
// leading zero, a pre-release of identifiers whose numbers have none
// either, build metadata whose may; and nothing else.
func TestParse(t *testing.T) {
	good := []string{"0.1.0", "1.2.3", "10.20.30", "0.2.0-rc.1", "1.0.0-alpha", "1.0.0-0.3.7", "1.0.0-x.7.z.92", "1.0.0-x-y-z.--", "1.0.0+20130313144700", "1.0.0-beta+exp.sha.5114f85", "1.0.0+0.build.1-rc.10000aaa-kk-0.1", "1.0.0+01", "1.0.0+001.0a", "99999999999999999999999.0.0"}
	for _, s := range good {
		if _, ok := Parse(s); !ok {
			t.Errorf("Parse(%q) refused a SemVer version", s)
		}
	}

	bad := []string{"", "0", "0.1", "v0.1.0", "01.2.0", "0.01.0", "0.1.00", "0.1.0.0", "0.1.0-", "0.1.0-01", "0.1.0-rc..1", "0.1.0+", "0.1.0+a..b", "0.1.0-rc_1", "0.1.0 extra", "-0.1.0", "0.1.-0", "a.b.c", "1.2.3-é"}
	for _, s := range bad {
		if _, ok := Parse(s); ok {
			t.Errorf("Parse(%q) took what SemVer does not write", s)
		}
	}

	if _, ok := Parse(Current); !ok {
		t.Errorf("Current, %q, is not a SemVer version", Current)
	}

	if _, ok := Parse(First); !ok {
		t.Errorf("First, %q, is not a SemVer version", First)
	}
}

// TestCompare holds Compare to SemVer's precedence: the example list
// of item 11, each before the next, with numbers past an int's range
// and build metadata that orders nothing.
func TestCompare(t *testing.T) {
	order := []string{"0.9.0", "0.10.0", "1.0.0-0", "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.1.0", "2.0.0", "99999999999999999999998.0.0", "99999999999999999999999.0.0"}
	for i := range order {
		for j := range order {
			a, _ := Parse(order[i])
			b, _ := Parse(order[j])
			if got, want := a.Compare(b), compareInts(i, j); got != want {
				t.Errorf("%s compared with %s = %d, want %d", order[i], order[j], got, want)
			}
		}
	}

	a, _ := Parse("1.0.0+one")
	b, _ := Parse("1.0.0+two")
	if a.Compare(b) != 0 {
		t.Errorf("build metadata ordered two versions")
	}
}
