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

// verbCalls prints with each verb past %v, %d, %s, %q, %f and %t, with
// the zero flag where it pads and where it does not, with %g and %x on
// types that print as their String, and with Fprint.
const verbCalls = `package main
import ("fmt"; "os")
type Heat f64 impl fmt.Stringer
func (h ref Heat) String() string { return "hot" }
type Tag string impl fmt.Stringer
func (t ref Tag) String() string { return "TAG" }
func main() {
	var n i8 = -5
	var u u8 = 200
	bs := []u8{1, 255}
	s := "hi"
	heat := Heat(1.5)
	tag := Tag("ab")
	fmt.Printf("%x %X %o %b %c %.4x\n", n, u, n, u, u, n)
	fmt.Printf("%x %X\n", s, bs)
	fmt.Printf("%e %.2e %g %.3g\n", 2.5, 2.5, f32(2.5), 2.5)
	fmt.Printf("%05d %-05d %06.3d %08.3f %5d\n", n, n, n, 2.5, n)
	fmt.Printf("%g %x\n", heat, tag)
	fmt.Fprint(mut os.Stdout(), n, s)
}
`

// A verb is a call of the runtime's function for it: an integer in a
// base as its magnitude after a sign, widened by its own signedness;
// the bytes of a string and of a []u8 in hexadecimal; a float with an
// exponent, six digits unless the format gives a precision; and %g
// without one as the shortest text of the float, of its own width,
// never a String method's. %x of a type that prints as its String is
// the bytes of what String returns, as in Go. The zero flag pads with
// sn_pad_zero, but not under the minus flag or on an integer with a
// precision, and Fprint writes its text with the writer's Write.
func TestVerbs(t *testing.T) {
	body := irFunction(t, emitSource(t, verbCalls), "sn_main")
	for _, want := range []string{
		"sext i8 %", "zext i8 %", ", i64 16, i1 zeroext false)", ", i64 16, i1 zeroext true)", ", i64 8, i1 zeroext false)", ", i64 2, i1 zeroext false)",
		"@sn_hex_bytes(", "@sn_f32_to_string(", "@sn_sn_os_File_Write(",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("main lacks %s:\n%s", want, body)
		}
	}

	for call, last := range map[string][]string{ // each call's precision or digits, its last argument
		"@sn_f64_e(": {", i64 6)", ", i64 2)"}, "@sn_f64_g(": {", i64 3)"}, "@sn_f64_fixed(": {", i64 3)"}, "@sn_int_digits(": {", i64 4)", ", i64 3)"},
	} {
		var got []string
		for _, line := range strings.Split(body, "\n") {
			if i := strings.LastIndex(line, ", i64 "); i >= 0 && strings.Contains(line, "call void "+call) {
				got = append(got, line[i:])
			}
		}

		if strings.Join(got, " ") != strings.Join(last, " ") {
			t.Errorf("the calls of %s end in %q, want %q:\n%s", call, got, last, body)
		}
	}

	for name, want := range map[string]int{
		"@sn_int_base(": 3, "@sn_uint_base(": 2, "@sn_char(": 1, "@sn_int_digits(": 2, "@sn_hex(": 2, "@sn_f64_e(": 2, "@sn_f64_g(": 1,
		"@sn_pad_zero(": 2, "@sn_pad(": 3, "@sn_f64_to_string(": 1,
	} {
		if n := strings.Count(body, "call void "+name); n != want {
			t.Errorf("main calls %s %d times, want %d:\n%s", name, n, want, body)
		}
	}

	if n := strings.Count(body, "_String("); n != 1 || !strings.Contains(body, "call %sn_string @sn_Tag_String(") {
		t.Errorf("main calls a String method %d times, want once, for the %%x of the tag and not for the %%g of the heat:\n%s", n, body)
	}
}
