// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"strings"
	"testing"
)

// methodNameOf hands a derived Error() to the type's stringer and a
// derived Source() to the function giving None, so the table of a
// type used as an Error reaches both.
func TestDerivedErrorMethods(t *testing.T) {
	out := emitError(t)
	tests := []struct{ name, want string }{
		{"sn_AppError_as_Error_Error", "call %sn_string @sn_str_AppError(ptr %p)"},
		{"sn_AppError_as_Error_Source", "call { i1, %sn_trait } @sn_src_AppError(ptr %p)"},
		{"sn_Wrapped_as_Error_Error", "call %sn_string @sn_Wrapped_Error(ptr %p)"}, // a written method, as before
		{"sn_Wrapped_as_Error_Source", "call { i1, %sn_trait } @sn_Wrapped_Source(ptr %p)"},
	}
	for _, tt := range tests {
		if body := irFunction(t, out, tt.name); !strings.Contains(body, tt.want) {
			t.Errorf("@%s lacks %q:\n%s", tt.name, tt.want, body)
		}
	}

	if !strings.Contains(out, "@sn_Wrapped_as_Error_tab = internal constant [3 x ptr] [ptr @sn_Wrapped_as_Error_drop, ptr @sn_Wrapped_as_Error_Error, ptr @sn_Wrapped_as_Error_Source]") {
		t.Error("the Error table is not the drop, then Error, then Source")
	}
}
