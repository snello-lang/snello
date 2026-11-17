// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "testing"

// TestUnifyBorrows holds inference to its rule for a borrow: a value
// passed to a bare type parameter binds what it borrows, and a borrow
// met inside a type binds as itself, a slice's element, an option's
// value or what a ref parameter lends alike.
func TestUnifyBorrows(t *testing.T) {
	view := &Ref{elem: Typ[String]}
	tests := []struct {
		name      string
		slot      func(p *TypeParam) Type
		arg, want Type
	}{
		{"a bare parameter", func(p *TypeParam) Type { return p }, view, Typ[String]},
		{"a slice's element", func(p *TypeParam) Type { return &Slice{elem: p} }, &Slice{elem: view}, view},
		{"an option's value", func(p *TypeParam) Type { return &Option{elem: p} }, &Option{elem: view}, view},
		{"behind a ref parameter", func(p *TypeParam) Type { return &Ref{elem: p} }, &Ref{elem: view}, view},
		{"a value lent to a ref parameter", func(p *TypeParam) Type { return &Ref{elem: p} }, Typ[Int], Typ[Int]},
	}
	for _, tt := range tests {
		p := NewTypeParam(NewTypeName(0, "T", nil), 0)
		in := newInference([]*TypeParam{p})
		in.unify(tt.slot(p), tt.arg)
		if got := in.m[p]; got == nil || !Identical(got, tt.want) {
			t.Errorf("%s: T bound to %v, want %v", tt.name, got, tt.want)
		}
	}
}
