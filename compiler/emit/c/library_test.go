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
	body := cFunction(t, emitSource(t, verbCalls), "static SN_UNUSED void sn_main(void)")
	for _, want := range []string{
		"sn_int_base((int64_t)(n), 16, false)", "sn_uint_base((uint64_t)(u), 16, true)", "sn_int_base((int64_t)(n), 8, false)",
		"sn_uint_base((uint64_t)(u), 2, false)", "sn_char((int64_t)(u))", "sn_int_digits(sn_int_base((int64_t)(n), 16, false), 4)",
		"sn_hex(s, false, false)", "sn_hex(sn_Tag_String(tag), false, true)", "sn_hex_bytes((sn_slice){", ".cap}, true)",
		", 6);", ", 2);", "sn_f32_to_string(", "sn_f64_g(2.5, 3)", "sn_f64_to_string((double)(heat))",
		"sn_pad_zero(sn_int_to_string((int64_t)(n)), 5, true)", "sn_pad(sn_int_to_string((int64_t)(n)), 5, true, true)",
		"sn_pad(sn_int_digits(sn_int_to_string((int64_t)(n)), 3), 6, false, true)", "sn_pad_zero(sn_f64_fixed(2.5, 3), 8, true)",
		"sn_sn_os_File_Write(",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("main lacks %s:\n%s", want, body)
		}
	}

	for name, want := range map[string]int{"sn_hex(": 2, "sn_f64_e(": 2, "sn_pad_zero(": 2, "_String(": 1} {
		if n := strings.Count(body, name); n != want {
			t.Errorf("main calls %s %d times, want %d:\n%s", name, n, want, body)
		}
	}
}
