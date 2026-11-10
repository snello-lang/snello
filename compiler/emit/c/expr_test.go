// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"strings"
	"testing"
)

// conversion writes string(r) and string(rs) as the runtime's
// sn_string_from_rune and sn_string_from_runes, the slice passed as the
// runtime's slice of any element.
func TestRuneConversions(t *testing.T) {
	body := cFunction(t, emitSource(t, `package main
func main() { var r rune = 'é'; rs := "añ".Runes(); print(string(r), string(rs)) }`), "static SN_UNUSED void sn_main(void)")
	for _, want := range []string{"sn_string_from_rune(", "sn_string_from_runes((sn_slice){"} {
		if !strings.Contains(body, want) {
			t.Errorf("main lacks %q:\n%s", want, body)
		}
	}
}

// Arithmetic on an integer narrower than 64 bits is written with a
// conversion to its type, so its result is the type's wherever it is
// read; arithmetic on an int is C's own, with none. A shift is the
// function of its type, which settles a count of the width or more, in
// an expression and in an assignment alike.
func TestNarrowArithmetic(t *testing.T) {
	out := emitSource(t, `package main
func main() {
	var a i8 = 1
	var u u32 = 2
	n := 3
	print(a + a > a, u * u, -a, ^u, n + n, n - n, u << u, n >> n)
	u <<= u
}`)
	body := cFunction(t, out, "static SN_UNUSED void sn_main(void)")
	for _, want := range []string{"((int8_t)(a + a))", "((uint32_t)(u * u))", "((int8_t)(-a))", "((uint32_t)(~u))", "(n + n)", "(n - n)", "sn_shl_u32(u, u)", "sn_shr_int(n, n)", "= sn_shl_u32(*"} {
		if !strings.Contains(body, want) {
			t.Errorf("main lacks %q:\n%s", want, body)
		}
	}

	if strings.Contains(body, ")(n + n)") {
		t.Errorf("main converts a sum of two ints, which is the width C adds in:\n%s", body)
	}

	for _, want := range []string{
		"static SN_UNUSED uint32_t sn_shl_u32(uint32_t a, uint32_t n) {\n\treturn (uint32_t)n >= 32 ? 0 : (uint32_t)((uint64_t)a << n);",
		"static SN_UNUSED sn_int sn_shr_int(sn_int a, sn_int n) {\n\treturn (uint64_t)n >= 64 ? (sn_int)(a < 0 ? -1 : 0) : (sn_int)(a >> n);",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the program lacks the shift function %q:\n%s", want, out)
		}
	}
}

// A literal beside an f32 is written as a C float, so the expression is
// worked out in floats and not in doubles; one beside an f64 is a
// double. An and-not assignment is C's and with the complement, C
// having no such operator. A float converted to an integer goes through
// the function of the integer type, which settles a float out of its
// range; an integer converted to an integer is C's own conversion.
func TestFloatsInC(t *testing.T) {
	out := emitSource(t, `package main
func main() {
	var f f32 = 1.5
	g := 2.5
	var u u8 = 255
	u &^= 15
	print(f * 0.1, f + 16777216.0, g * 0.1, u8(g), i64(f), int(u))
}`)
	body := cFunction(t, out, "static SN_UNUSED void sn_main(void)")
	for _, want := range []string{"(f * 0.1f)", "(f + 1.6777216e+07f)", "(g * 0.1)", "u &= ~(15ULL);", "sn_trunc_u8(g)", "sn_trunc_i64(f)", "((sn_int)(u))"} {
		if !strings.Contains(body, want) {
			t.Errorf("main lacks %q:\n%s", want, body)
		}
	}

	for _, want := range []string{
		"static SN_UNUSED uint8_t sn_trunc_u8(double x) {\n\tif (x != x) {\n\t\treturn 0;\n\t}\n\n\tif (x <= 0.0) {\n\t\treturn 0;\n\t}\n\n\tif (x >= 0x1p8) {\n\t\treturn UINT8_MAX;\n\t}\n\n\treturn (uint8_t)x;\n}",
		"static SN_UNUSED int64_t sn_trunc_i64(double x) {\n\tif (x != x) {\n\t\treturn 0;\n\t}\n\n\tif (x <= -0x1p63) {\n\t\treturn INT64_MIN;\n\t}\n\n\tif (x >= 0x1p63) {\n\t\treturn INT64_MAX;\n\t}\n\n\treturn (int64_t)x;\n}",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the program lacks the conversion function %q:\n%s", want, out)
		}
	}
}
