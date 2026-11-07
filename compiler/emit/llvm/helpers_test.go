// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"regexp"
	"strings"
	"testing"
)

// textAt prints a trait value of Error through its table, a call
// through a pointer loaded from the table, and the stringer of a
// declared type prints its structure: a derived Error() over int
// reaches sn_int_to_string and never calls itself.
func TestTraitValueText(t *testing.T) {
	body := irFunction(t, emitError(t), "sn_main")
	if !regexp.MustCompile(`call %sn_string %t[0-9]+\(ptr %t[0-9]+\)`).MatchString(body) {
		t.Errorf("main does not print the Error value through its table:\n%s", body)
	}
}

func TestStringerOverBasic(t *testing.T) {
	body := irFunction(t, emitError(t), "sn_str_Code")
	if !strings.Contains(body, "@sn_int_to_string") || strings.Contains(body, "call %sn_string @sn_str_Code(") {
		t.Errorf("the derived message of Code does not print the number:\n%s", body)
	}
}
