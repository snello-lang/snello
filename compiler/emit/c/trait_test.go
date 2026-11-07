// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"strings"
	"testing"
)

// methodNameOf hands a derived Error() to the type's stringer and a
// derived Source() to the function giving None, so the table of a
// type used as an Error reaches both.
func TestDerivedErrorMethods(t *testing.T) {
	out := emitError(t)
	tests := []struct{ head, want string }{
		{"static sn_string sn_AppError_as_Error_Error(void *sn_p)", "return sn_str_AppError(*p);"},
		{"static sn_opt_Error sn_AppError_as_Error_Source(void *sn_p)", "return sn_src_AppError(*p);"},
		{"static sn_string sn_Wrapped_as_Error_Error(void *sn_p)", "return sn_Wrapped_Error(*p);"}, // a written method, as before
		{"static sn_opt_Error sn_Wrapped_as_Error_Source(void *sn_p)", "return sn_Wrapped_Source(*p);"},
	}
	for _, tt := range tests {
		if body := cFunction(t, out, tt.head); !strings.Contains(body, tt.want) {
			t.Errorf("%q lacks %q:\n%s", tt.head, tt.want, body)
		}
	}

	if !strings.Contains(out, "static const sn_Error_tab sn_AppError_as_Error_tab = {sn_AppError_as_Error_drop, sn_AppError_as_Error_Error, sn_AppError_as_Error_Source};") {
		t.Error("the Error table is not the drop, then Error, then Source")
	}
}
