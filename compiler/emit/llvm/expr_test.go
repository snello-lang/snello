// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"strings"
	"testing"
)

// try hands the error to passed: wrapped in the variant the checker
// chose, boxed as a trait value, or as it is; its failure is marked
// unlikely, so the path that goes on is laid straight.
func TestTryPasses(t *testing.T) {
	out := emitError(t)
	tests := []struct{ name, want, what string }{
		{"sn__parse", "call i1 @llvm.expect.i1(i1 ", "? marks its failure unlikely"},
		{"sn__parse", "store i32 0, ptr", "? wraps a strconv.Error in Parse, the one variant holding it"},
		{"sn__parseAny", "@sn_sn_strconv_Error_as_Error_tab", "? boxes a strconv.Error into the Error value the function returns"},
	}
	for _, tt := range tests {
		if body := irFunction(t, out, tt.name); !strings.Contains(body, tt.want) {
			t.Errorf("%s: @%s lacks %q:\n%s", tt.what, tt.name, tt.want, body)
		}
	}
}

// An index of a type narrower than a length is widened to the length's
// 64 bits before it is checked, by its sign, and a bound of a slice
// expression the same. A float converted to an integer is the IR's
// saturating conversion, which settles a float out of the type's range,
// from the float's own type to the integer's.
func TestNarrowIndexAndConversion(t *testing.T) {
	body := irFunction(t, emitSource(t, `package main
func main() {
	xs := []int{1, 2, 3}
	var u u8 = 1
	var s i16 = 2
	var f f32 = 1.5
	g := 2.5
	print(xs[u], xs[s], len(xs[u:s]), u8(g), i64(f), u32(f))
}`), "sn_main")
	for _, want := range []string{
		"zext i8 %", " to i64", "sext i16 %", "icmp ult i64 %",
		"call i8 @llvm.fptoui.sat.i8.double(double %", "call i64 @llvm.fptosi.sat.i64.float(float %", "call i32 @llvm.fptoui.sat.i32.float(float %",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("main lacks %q:\n%s", want, body)
		}
	}

	for _, plain := range []string{"fptosi float", "fptoui double", "icmp ult i8", "icmp ult i16"} {
		if strings.Contains(body, plain) {
			t.Errorf("main holds %q, an index compared in its own width or a conversion left undefined out of range:\n%s", plain, body)
		}
	}
}

// A conversion of a []byte to a string copies the bytes of a place,
// which keeps them, and moves the bytes of a temporary, a call's result
// or a literal, into the string.
func TestBytesIntoString(t *testing.T) {
	out := emitSource(t, `package main
func built() []byte { return []byte("built") }
func main() {
	b := []byte("abc")
	s := string(take(mut b))
	u := string(b)
	v := string(built())
	print(s, u, v)
}`)
	body := irFunction(t, out, "sn_main")
	if strings.Count(body, "@sn_bytes_into_string(") != 2 || strings.Count(body, "@sn_string_clone(") < 1 {
		t.Errorf("the temporaries do not move and the place does not copy:\n%s", body)
	}
}
