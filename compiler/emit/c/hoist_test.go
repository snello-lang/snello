// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"strings"
	"testing"
)

// try hands the error to passed: wrapped in the variant the checker
// chose, boxed as a trait value, or as it is.
func TestTryPasses(t *testing.T) {
	out := emitError(t)
	tests := []struct{ head, want, what string }{
		{"static SN_UNUSED sn_res_int_AppError sn__parse(sn_string text)", ".err = (AppError){.tag = 0, .Parse = {.cause = ", "? wraps a strconv.Error in Parse, the one variant holding it"},
		{"static SN_UNUSED sn_res_int_Error sn__parseAny(sn_string text)", ".err = sn_sn_strconv_Error_as_Error(", "? boxes a strconv.Error into the Error value the function returns"},
	}
	for _, tt := range tests {
		if body := cFunction(t, out, tt.head); !strings.Contains(body, tt.want) {
			t.Errorf("%s: %q lacks %q:\n%s", tt.what, tt.head, tt.want, body)
		}
	}
}

// An operand is saved before the operands after it when one of them
// moves a variable it reads, and not when they only read: a slice of a
// string beside a slice moved in is no reason to save the slice.
func TestOperandsSaved(t *testing.T) {
	out := emitSource(t, `package main

func count(xs []int, s ref string) int { return len(xs) + len(s) }

func sliced(xs []int, s string, at int) int { return count(xs, s[at:]) }

func moved(v string) (string, string) { return (v.Clone(), v) }

func main() {}
`)
	if body := cFunction(t, out, "static SN_UNUSED sn_int sn__sliced(sn_slice_int xs, sn_string s, sn_int at)"); strings.Count(body, "sn_slice_int sn_t") != 1 {
		t.Errorf("xs is saved again before s[at:], which moves nothing:\n%s", body)
	}

	body := cFunction(t, out, "static SN_UNUSED sn_tup2_string_string sn__moved(sn_string v)")
	clone, empty := strings.Index(body, "= sn_string_clone(v);"), strings.Index(body, "v = (sn_string){0};")
	if clone < 0 || empty < 0 || clone > empty {
		t.Errorf("v.Clone() is not saved before v is moved out:\n%s", body)
	}
}
