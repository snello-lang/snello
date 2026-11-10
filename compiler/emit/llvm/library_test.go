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

// A call of a function of math/bits is LLVM's intrinsic on the width,
// zero no poison for the two that count zeros, and never a call of the
// library's; a value of one is the library's function.
func TestBitOps(t *testing.T) {
	body := irFunction(t, emitSource(t, bitCalls), "sn_main")
	for _, want := range []string{
		"call i8 @llvm.ctlz.i8(i8 %", ", i1 false)", "call i64 @llvm.cttz.i64(", "call i64 @llvm.ctpop.i64(",
		"call i8 @llvm.fshl.i8(", "call i64 @llvm.bitreverse.i64(", "sub i64 8, ",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("main lacks %s:\n%s", want, body)
		}
	}

	if strings.Contains(body, "call i64 @sn_math__bits_") || strings.Contains(body, "call i8 @sn_math__bits_") {
		t.Errorf("main calls the library's function where the intrinsic belongs:\n%s", body)
	}

	if !strings.Contains(body, "sn_math__bits_OnesCount64") {
		t.Errorf("main lacks the library's function for the value of OnesCount64:\n%s", body)
	}
}
