// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"strings"
	"testing"
)

// try hands the error to passed: wrapped in the variant the checker
// chose, boxed as a trait value, or as it is.
func TestTryPasses(t *testing.T) {
	out := emitError(t)
	tests := []struct{ name, want, what string }{
		{"sn__parse", "store i32 0, ptr", "? wraps a strconv.Error in Parse, the one variant holding it"},
		{"sn__parseAny", "@sn_sn_strconv_Error_as_Error_tab", "? boxes a strconv.Error into the Error value the function returns"},
	}
	for _, tt := range tests {
		if body := irFunction(t, out, tt.name); !strings.Contains(body, tt.want) {
			t.Errorf("%s: @%s lacks %q:\n%s", tt.what, tt.name, tt.want, body)
		}
	}
}
