// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/emit/internal/backendtest"
	"github.com/snello-lang/snello/compiler/emit/lower"
)

// TestHeader writes the header of the bound example's package and
// checks the prototype of each form that crosses: a number, a view of
// a string and of bytes, a buffer, an out-parameter and a bool.
func TestHeader(t *testing.T) {
	_, info, libs, err := backendtest.LoadDirectory("../../../examples/run/bound")
	if err != nil {
		t.Fatal(err)
	}

	bindings := lower.Bindings(info, libs)
	if len(bindings) != 1 || bindings[0].Path != "tally" || bindings[0].Prefix != "snm_tally" || bindings[0].Header != "tally.snello.h" {
		t.Fatalf("bindings = %+v, want one for tally", bindings)
	}

	header := string(Header(bindings[0]))
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
