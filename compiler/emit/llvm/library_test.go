// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"strings"
	"testing"
)

// verb writes %+v as a call of the chain function of the argument's
// type: the struct with a written Source, the type over int, and the
// trait value.
func TestVerbPlus(t *testing.T) {
	body := irFunction(t, emitError(t), "sn_main")
	for _, want := range []string{"call %sn_string @sn_chain_Wrapped(ptr", "call %sn_string @sn_chain_Code(ptr", "call %sn_string @sn_chain_Error(ptr"} {
		if !strings.Contains(body, want) {
			t.Errorf("main lacks the call %s for its %%+v:\n%s", want, body)
		}
	}
}
