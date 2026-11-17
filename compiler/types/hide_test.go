// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "testing"

// TestHide checks that a type parameter a generic body puts in a trait
// value or a closure takes no type argument that holds a borrow, and
// that what a body hides passes along its generic calls, through an
// argument, the receiver or a bound, and to an instance of a generic
// type whose methods hide its parameter.
func TestHide(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		{"func f(s ref string) { v := Shown{s: s}; print(HideOn(ref v).Text()) }", []string{"HideOn's T cannot hold Shown: it holds a borrow, which HideOn hides in the call of Hide at 43:56"}},
		{"func f(s ref string) { v := Shown{s: s}; print(Hide(ref v).Text()) }", []string{"Hide's T cannot hold Shown: it holds a borrow, which Hide hides in a trait value at 42:50"}},
		{"func f(s ref string) { v := View{s: s}; g := Later(ref v); print(g()) }", []string{"Later's T cannot hold View: it holds a borrow, which Later hides in a closure at 44:57"}},
		{"func f() { n := 1; print(Later(ref n)()) }", nil}, // an argument that holds no borrow hides nothing
		{"trait Hider: Show { func (s Self) Hide() Show { return Wrap<Self>{x: s} } }\nstruct HView impl Show, Hider { s ref string }\nfunc (v ref HView) Text() string { return (*v.s).Clone() }\nfunc viaBound<H: Hider>(h H) Show { return h.Hide() }\nfunc f(s ref string) { print(viaBound(HView{s: s}).Text()) }", []string{"viaBound's H cannot hold HView: it holds a borrow, which viaBound hides in the call of h.Hide"}},          // a default reached through a bound
		{"trait Hider2 { func (s Self) Hide() Show }\nstruct Box<T: Show> impl Hider2 { x T }\nfunc (b Box<T>) Hide() Show { return Wrap<T>{x: b.x} }\nfunc viaBound2<H: Hider2>(h H) Show { return h.Hide() }\nfunc f(s ref string) { print(viaBound2(Box<Shown>{x: Shown{s: s}}).Text()) }", []string{"Box's T cannot hold Shown: it holds a borrow, which Box hides in a trait value"}},                                                   // an implementation chosen by the instance
		{"struct Boxed<T: Show> { x T }\nfunc (b Boxed<T>) Hide() Show { return Wrap<T>{x: b.x} }\nfunc viaRecv<T: Show>(b Boxed<T>) Show { return b.Hide() }\nfunc f(s ref string) { print(viaRecv(Boxed<Shown>{x: Shown{s: s}}).Text()) }", []string{"viaRecv's T cannot hold Shown: it holds a borrow, which viaRecv hides in the call of b.Hide", "Boxed's T cannot hold Shown: it holds a borrow, which Boxed hides in a trait value"}}, // passed on through the receiver
	}
	for _, tt := range tests {
		expectErrors(t, prelude+passingPrelude+tt.src, tt.src, tt.want)
	}
}
