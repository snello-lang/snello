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
