// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"strings"
	"testing"
)

// toText prints a trait value of Error through its table, inside an
// Err(...) as anywhere, and the stringer of a declared type prints its
// structure: a derived Error() over int reaches sn_int_to_string and
// never calls itself.
func TestTraitValueText(t *testing.T) {
	out := emitError(t)
	if body := cFunction(t, out, "static SN_UNUSED void sn_main(void)"); !strings.Contains(body, "sn_msg_Error(") {
		t.Errorf("main does not print the Error value through sn_msg_Error:\n%s", body)
	}

	if !strings.Contains(out, "sn_quoted_owned(sn_msg_Error(v.err))") {
		t.Error("an Error value inside an Err(...) is not printed, quoted, through its table")
	}
}

func TestStringerOverBasic(t *testing.T) {
	body := cFunction(t, emitError(t), "static sn_string sn_str_Code(Code v)")
	if !strings.Contains(body, "sn_int_to_string") || strings.Count(body, "sn_str_Code(") > 1 {
		t.Errorf("the derived message of Code does not print the number:\n%s", body)
	}
}

// formatterSrc prints a type listing fmt.Formatter under a width, inside
// an option, through a trait value and as a derived Error().
const formatterSrc = `package main
import "fmt"
struct Money impl fmt.Formatter { cents int }
func (m ref Money) Format(f mut fmt.State, verb rune) { f.Pad("$") }
struct Fail impl Error, fmt.Formatter { code int }
func (e ref Fail) Format(f mut fmt.State, verb rune) { f.WriteString("fail") }
func main() {
	m := Money{cents: 1}
	fmt.Printf("%8v %d\n", m, m)
	fmt.Println(Some(m))
	var tv fmt.Formatter = Money{cents: 2}
	fmt.Println(tv)
	e := Fail{code: 3}
	fmt.Println(e.Error())
}`

// A Formatter prints through one function per type that builds the
// fmt.State with the verb's width, precision and flags, calls Format on
// the value and the State, and moves the State's buffer into the text;
// the call passes the verb and -1 for a width or precision not given,
// and the text is not padded again. A trait value calls Format through
// its table, and a derived Error() is the %v text Format writes.
func TestFormatter(t *testing.T) {
	out := emitSource(t, formatterSrc)
	body := cFunction(t, out, "static sn_string sn_format_Money(Money v, int32_t verb, int64_t width, int64_t prec, bool minus, bool zero)")
	for _, want := range []string{"f.width = (sn_opt_int){.has = true, .val = width}", "f.minus = minus", "Format(v, &f, verb);", "return sn_bytes_into_string(f.out);"} {
		if !strings.Contains(body, want) {
			t.Errorf("the formatter of Money lacks %q:\n%s", want, body)
		}
	}

	main := cFunction(t, out, "static SN_UNUSED void sn_main(void)")
	for _, want := range []string{"sn_format_Money(m, 118, 8, -1, false, false)", "sn_format_Money(m, 100, -1, -1, false, false)"} {
		if !strings.Contains(main, want) {
			t.Errorf("main does not print Money through its Format as %q:\n%s", want, main)
		}
	}

	if !strings.Contains(out, "sn_quoted_owned(sn_format_Money(") {
		t.Error("a Money inside an option is not printed, quoted, through its Format")
	}

	if strings.Contains(main, "sn_pad(sn_format_Money(") {
		t.Errorf("main pads what a Format wrote under its width:\n%s", main)
	}

	if !strings.Contains(out, "v.tab->Format(v.data, &f, verb);") {
		t.Error("a trait value of Formatter does not call Format through its table")
	}

	if msg := cFunction(t, out, "static sn_string sn_msg_Fail(Fail v)"); !strings.Contains(msg, "sn_format_Fail(v, 118, -1, -1, false, false)") {
		t.Errorf("the derived Error() of a Formatter is not what its Format writes for %%v:\n%s", msg)
	}
}
