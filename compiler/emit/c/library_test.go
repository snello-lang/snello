// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"strings"
	"testing"
)

// verb writes %+v as a call of the chain function of the argument's
// type: the struct with a written Source, the type over int, and the
// trait value.
func TestVerbPlus(t *testing.T) {
	body := cFunction(t, emitError(t), "static SN_UNUSED void sn_main(void)")
	for _, want := range []string{"sn_chain_Wrapped(", "sn_chain_Code(", "sn_chain_Error("} {
		if !strings.Contains(body, want) {
			t.Errorf("main lacks the call %s for its %%+v:\n%s", want, body)
		}
	}
}

// bitCalls calls a function of math/bits of each kind and keeps one as
// a value.
const bitCalls = `package main
import ("fmt"; "math/bits")
func main() {
	var a u8 = 1
	var d u64 = 1
	f := bits.OnesCount64
	fmt.Println(bits.LeadingZeros8(a), bits.TrailingZeros64(d), bits.OnesCount64(d), bits.Len8(a), bits.RotateLeft8(a, -1), bits.Reverse64(d), f(d))
}
`

// A call of a function of math/bits is the runtime header's function
// over the compiler's builtin, written in place, and never a call of
// the library's; a value of one is the library's function.
func TestBitOps(t *testing.T) {
	body := cFunction(t, emitSource(t, bitCalls), "static SN_UNUSED void sn_main(void)")
	for _, want := range []string{"sn_bits_LeadingZeros8(", "sn_bits_TrailingZeros64(", "sn_bits_OnesCount64(", "sn_bits_Len8(", "sn_bits_RotateLeft8(", "sn_bits_Reverse64("} {
		if !strings.Contains(body, want) {
			t.Errorf("main lacks %s:\n%s", want, body)
		}
	}

	if n := strings.Count(body, "sn_math__bits_"); n != 1 || !strings.Contains(body, "sn_math__bits_OnesCount64") {
		t.Errorf("main names the library's functions %d times, want once, for the value of OnesCount64:\n%s", n, body)
	}
}
