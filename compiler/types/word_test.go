// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "testing"

// TestWord checks that an implementation whose trait declares a value
// of a type parameter in its result returns only borrows stored inside
// its receiver and the parameters the trait declares with Self or a
// type parameter, reached through ref borrows: never a borrow of what
// the receiver owns, a reborrow through a mut, or a parameter of a
// concrete type; and that a borrow reached through a ref field, through
// a local that borrows the receiver, is accepted.
func TestWord(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		{"struct Own impl It<mut []int> { xs []int }\nfunc (o mut Own) Next() (mut []int)? { return Some(mut o.xs) }", []string{"cannot return a borrow of o.xs: o owns it, and It<mut []int>.Next returns only what o borrows"}},
		{"struct Bad impl It<ref string> { xs mut []string }\nfunc (b mut Bad) Next() ref string? { return Some(ref b.xs[0]) }", []string{"cannot return a borrow through b.xs: it is a mut borrow, and It<ref string>.Next returns only what is reached through ref borrows"}},
		{"struct Slot impl It<ref int> { n int }\nfunc (s mut Slot) Next() ref int? { x := ref s.n; return Some(x) }", []string{"cannot return a borrow of s.n: s owns it, and It<ref int>.Next returns only what s borrows"}},
		{"trait Of { func Of(s ref string) Self }\nstruct Viewed impl Of { s ref string }\nfunc Viewed.Of(s ref string) Viewed { return Viewed{s: s} }", []string{"cannot return a borrow of s: Of.Of declares s with neither Self nor a type parameter of the trait, and returns only what such parameters borrow"}},
		{"trait Pair<T> { func (p mut Self) Get() (T, ref int) }\nstruct Own2 impl Pair<ref []int> { xs []int; r ref int }\nfunc (o mut Own2) Get() (ref []int, ref int) { return (ref o.xs, o.r) }", []string{"cannot return a borrow of o.xs: o owns it, and Pair<ref []int>.Get returns only what o borrows"}}, // a type parameter beside a declared borrow
		{"struct Inner { own []int; r ref []int }\nstruct Walked impl It<ref int> { io Inner }\nfunc (v mut Walked) Next() ref int? { x := ref v.io; return Some(ref x.r[0]) }", nil},                                                                                                                             // through a ref field of a part the receiver owns
		{"struct Inner { own []int; r ref []int }\nstruct Walked impl It<ref int> { in ref Inner }\nfunc (v mut Walked) Next() ref int? { x := ref v.in; return Some(ref (*x).own[0]) }", nil},                                                                                                                    // through a ref field read into a local
		{"struct Inner { own []int; r ref []int }\nstruct Walked impl It<ref int> { io Inner }\nfunc (v mut Walked) Next() ref int? { x := ref v.io; return Some(ref x.own[0]) }", []string{"cannot return a borrow of v.io.own: v owns it"}},
		{"struct Inner { own []int; r ref []int }\nstruct Walked impl It<ref int> { in ref Inner }\nfunc (v mut Walked) Next() ref int? { x := v.in; y := ref x.own; return Some(ref y[0]) }", nil}, // a ref field read into a local
		{"trait Peeks<T> { func (p ref Self) Peek() ref T? }\nstruct Stack impl Peeks<int> { items []int }\nfunc (s ref Stack) Peek() ref int? { return Some(ref s.items[0]) }", nil},               // behind a declared borrow, tied to the receiver
		{"func firstOf(xs ref []int) ref int? { return None }\nstruct Behind impl It<ref int> { xs mut []int }\nfunc (b mut Behind) Next() ref int? { r := ref b.xs; return firstOf(*r) }", []string{"cannot return a borrow through b.xs: it is a mut borrow"}},
		{"func firstOf(xs ref []int) ref int? { return None }\nstruct Owning impl It<ref int> { items []int }\nfunc (b mut Owning) Next() ref int? { r := ref b.items; return firstOf(*r) }", []string{"cannot return a borrow of b.items: b owns it"}},
		{"func firstOf(xs ref []int) ref int? { return None }\nstruct Behind impl It<ref int> { xs mut []int }\nfunc (b mut Behind) Next() ref int? { r := ref b.xs; y := firstOf(*r); return y }", []string{"mut borrow"}}, // only what * leaves of the lend reaches y
		{"func firstOf(xs ref []int) ref int? { return None }\nstruct Owning impl It<ref int> { items []int }\nfunc (b mut Owning) Next() ref int? { r := ref b.items; y := firstOf(*r); return y }", []string{"b owns it"}},
		{"struct Mixed impl It<ref int> { d ref []int; scratch mut []int }\nfunc (b mut Mixed) Next() ref int? { x := ref b.d; return Some(ref (*x)[0]) }", nil}, // through a ref field beside a mut one
		{"struct Owned impl It<ref int> { items []int }\nfunc (b mut Owned) Next() ref int? { x := ref b.items; return Some(ref (*x)[0]) }", []string{"cannot return a borrow of b.items: b owns it"}},
		{"struct Stored impl It<ref int> { r ref int }\nfunc (v mut Stored) Next() ref int? { x := v.r; return Some(x) }", nil}, // a borrow read out of a ref field, through a local
	}
	for _, tt := range tests {
		expectErrors(t, prelude+passingPrelude+tt.src, tt.src, tt.want)
	}
}
