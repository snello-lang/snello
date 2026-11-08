// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "testing"

// The predeclared string method Runes gives an owned slice of runes and
// reads its receiver.
func TestStringRunes(t *testing.T) {
	sig := builtinMethod(Typ[String], "Runes")
	if sig == nil {
		t.Fatal("string has no method Runes")
	}

	if got := sig.String(); got != "func() []rune" {
		t.Errorf("Runes is %s, want func() []rune", got)
	}

	if r, ok := sig.Recv().Type().(*Ref); !ok || r.Mut() {
		t.Errorf("Runes takes %s, want a ref of the string", sig.Recv().Type())
	}
}
