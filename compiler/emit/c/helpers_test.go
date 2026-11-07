// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"strings"
	"testing"
)

// toText prints a trait value of Error through its table, inside an
// Err(...) as anywhere, and the stringer of a declared type prints its
// structure: a derived Error() over int reaches sn_int_to_string and
// never calls itself.
func TestTraitValueText(t *testing.T) {
	out := emitError(t)
	if body := cFunction(t, out, "static SN_UNUSED void sn_main(void)"); !strings.Contains(body, "sn_msg_Error(") {
		t.Errorf("main does not print the Error value through sn_msg_Error:\n%s", body)
	}

	if !strings.Contains(out, "sn_quote_owned(sn_msg_Error(v.err))") {
		t.Error("an Error value inside an Err(...) is not printed, quoted, through its table")
	}
}

func TestStringerOverBasic(t *testing.T) {
	body := cFunction(t, emitError(t), "static sn_string sn_str_Code(Code v)")
	if !strings.Contains(body, "sn_int_to_string") || strings.Count(body, "sn_str_Code(") > 1 {
		t.Errorf("the derived message of Code does not print the number:\n%s", body)
	}
}
