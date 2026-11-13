// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	goruntime "runtime"
	"slices"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/emit/internal/backendtest"
	"github.com/snello-lang/snello/compiler/emit/lower"
)

// TestHeader writes the header of the bound example's package and
// checks the prototype of each form that crosses: a number, a view of
// a string and of bytes, a buffer, an out-parameter and a bool.
func TestHeader(t *testing.T) {
	_, info, libs, err := backendtest.LoadDirectory(backendtest.Examples + "/bound")
	if err != nil {
		t.Fatal(err)
	}

	bindings := lower.Bindings(info, libs)
	var tally *lower.Binding
	for i := range bindings {
		if bindings[i].Path == "tally" {
			tally = &bindings[i]
		}
	}

	if tally == nil || tally.Prefix != "snm_tally" || tally.Header != "tally.snello.h" || len(tally.Links) != 1 || tally.Links[0] != "m" {
		t.Fatalf("bindings = %+v, want tally with its module's link m", bindings)
	}

	if flags := LinkFlags(bindings); len(flags) != 1 || flags[0] != "-lm" {
		t.Errorf("link flags %v, want -lm once for the two packages of one module", flags)
	}

	header := string(Header(*tally))
	for _, want := range []string{
		"#include \"snello.h\"",
		"sn_int snm_tally_open(sn_int start);",
		"sn_int snm_tally_Live(void);",
		"sn_int snm_tally_add(sn_int handle, sn_int n);",
		"void snm_tally_release(sn_int handle);",
		"sn_int snm_tally_Sum(sn_slice_u8 xs);",
		"sn_int snm_tally_Fill(sn_slice_u8 * buf, uint8_t b);",
		"bool snm_tally_Parse(sn_string s, sn_int * n);",
	} {
		if !strings.Contains(header, want) {
			t.Errorf("header lacks %q:\n%s", want, header)
		}
	}
}

// TestSystemLinkFlags checks every link ends with the mathematics, and
// binds every import as the program starts where the system would wait
// for its first call.
func TestSystemLinkFlags(t *testing.T) {
	flags := SystemLinkFlags()
	if !slices.Contains(flags, mathLibrary) {
		t.Errorf("flags %v, want %s", flags, mathLibrary)
	}

	if now := slices.Contains(flags, bindNow); now != (goruntime.GOOS == linux) {
		t.Errorf("flags %v on %s: %s is for Linux alone", flags, goruntime.GOOS, bindNow)
	}
}
