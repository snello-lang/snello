// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"regexp"
	"strings"
	"testing"
)

// textAt prints a trait value of Error through its table, a call
// through a pointer loaded from the table, and the stringer of a
// declared type prints its structure: a derived Error() over int
// reaches sn_int_to_string and never calls itself.
func TestTraitValueText(t *testing.T) {
	body := irFunction(t, emitError(t), "sn_main")
	if !regexp.MustCompile(`call %sn_string %t[0-9]+\(ptr %t[0-9]+\)`).MatchString(body) {
		t.Errorf("main does not print the Error value through its table:\n%s", body)
	}
}

func TestStringerOverBasic(t *testing.T) {
	body := irFunction(t, emitError(t), "sn_str_Code")
	if !strings.Contains(body, "@sn_int_to_string") || strings.Contains(body, "call %sn_string @sn_str_Code(") {
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
	body := irFunction(t, out, "sn_format_Money")
	for _, want := range []string{"icmp sge i64 %width, 0", "Format(ptr %v, ptr %", "i32 %verb)", "@sn_bytes_into_string("} {
		if !strings.Contains(body, want) {
			t.Errorf("the formatter of Money lacks %q:\n%s", want, body)
		}
	}

	main := irFunction(t, out, "sn_main")
	for _, want := range []string{"i32 118, i64 8, i64 -1, i1 false, i1 false)", "i32 100, i64 -1, i64 -1, i1 false, i1 false)"} {
		if !regexp.MustCompile(`call %sn_string @sn_format_Money\(ptr %[a-z0-9.]+, ` + regexp.QuoteMeta(want)).MatchString(main) {
			t.Errorf("main does not print Money through its Format as %q:\n%s", want, main)
		}
	}

	if strings.Contains(main, "@sn_pad(") {
		t.Errorf("main pads what a Format wrote under its width:\n%s", main)
	}

	if trait := irFunction(t, out, "sn_format_sn_fmt_Formatter"); !regexp.MustCompile(`call void %t[0-9]+\(ptr %t[0-9]+, ptr %s[0-9]+, i32 %verb\)`).MatchString(trait) {
		t.Errorf("a trait value of Formatter does not call Format through its table:\n%s", trait)
	}

	if msg := irFunction(t, out, "sn_msg_Fail"); !strings.Contains(msg, "call %sn_string @sn_format_Fail(ptr %v, i32 118, i64 -1, i64 -1, i1 false, i1 false)") {
		t.Errorf("the derived Error() of a Formatter is not what its Format writes for %%v:\n%s", msg)
	}
}
