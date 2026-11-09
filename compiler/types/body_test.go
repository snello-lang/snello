// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"testing"

	"github.com/snello-lang/snello/compiler/token"
)

// TestHasMutParam checks which signatures could store into the caller's
// places: those with a receiver or parameter that is, or carries, a mut
// borrow.
func TestHasMutParam(t *testing.T) {
	c := &Checker{}
	str := Typ[String]
	refStr, mutStr := NewRef(str, false), NewRef(str, true)
	holdsMut := NewStruct([]*Var{NewField(token.NoPos, "src", mutStr)})
	param := func(name string, typ Type) *Var { return NewVar(token.Pos(1), name, typ) }
	tests := []struct {
		name string
		sig  *Signature
		want bool
	}{
		{"none", NewSignature(nil, nil, nil), false},
		{"owned", NewSignature(nil, []*Var{param("s", str)}, nil), false},
		{"ref param", NewSignature(nil, []*Var{param("s", refStr)}, nil), false},
		{"mut param", NewSignature(nil, []*Var{param("s", mutStr)}, nil), true},
		{"mut receiver", NewSignature(param("s", mutStr), nil, nil), true},
		{"ref receiver", NewSignature(param("s", refStr), []*Var{param("n", str)}, nil), false},
		{"mut in a field", NewSignature(nil, []*Var{param("w", holdsMut)}, nil), true},
		{"mut in an option", NewSignature(nil, []*Var{param("o", NewOption(mutStr))}, nil), true},
		{"mut after others", NewSignature(nil, []*Var{param("a", str), param("b", refStr), param("c", mutStr)}, nil), true},
	}
	for _, tt := range tests {
		if got := c.hasMutParam(tt.sig); got != tt.want {
			t.Errorf("%s: hasMutParam = %v, want %v", tt.name, got, tt.want)
		}
	}
}
