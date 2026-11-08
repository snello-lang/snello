// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"strings"
	"testing"
)

// The program the Error trait's helpers are written for: a derived
// Error() and Source() on an enum, a struct and a type over int, a
// written Source, ? into a variant and into a trait value, %+v, and
// a trait value printed. What it prints is checked by the error
// programs of backendtest, which both backends run; this test reads
// the C written for it, so that each helper of error.go is seen in
// the output under its own name.
const errorSrc = `package main
import ("fmt"; "strconv")
enum AppError impl Error, Clone { Parse(cause strconv.Error); Missing(key string) }
struct Pair impl Error { key string; n int }
type Code int impl Error
struct Wrapped impl Error { cause AppError }
func (e ref Wrapped) Error() string { return "wrapped" }
func (e ref Wrapped) Source() Error? { return Some(e.cause.Clone()) }
func parse(text ref string) Result<int, AppError> { n := strconv.Atoi(text)?; return Ok(n) }
func parseAny(text ref string) Result<int, Error> { n := strconv.Atoi(text)?; return Ok(n) }
func main() {
	q := "q"
	fmt.Println(parse(ref q), parseAny(ref q), Pair{key: "k", n: 1}, Code(7))
	w := Wrapped{cause: Missing("port")}
	fmt.Printf("%+v %+v\n", w, Code(7))
	var boxed Error = w
	fmt.Println(boxed)
	fmt.Printf("%+v\n", boxed)
}
`

func emitError(t *testing.T) string {
	t.Helper()
	return emitSource(t, errorSrc)
}

// The helpers error.go writes: a derived Source, the chain behind %+v
// on a declared type and on a trait value, and a trait value's message
// through its table. The rest of what the program needs is tested
// beside the file that writes it.
func TestSourcer(t *testing.T) {
	out := emitError(t)
	for _, head := range []string{
		"static sn_opt_Error sn_src_AppError(AppError v)",
		"static sn_opt_Error sn_src_Code(Code v)",
		"static sn_opt_Error sn_src_sn_strconv_Error(sn_strconv_Error v)", // the library's type, derived as any
	} {
		if body := cFunction(t, out, head); !strings.Contains(body, "return (sn_opt_Error){.has = false};") {
			t.Errorf("%q does not give None:\n%s", head, body)
		}
	}
}

func TestChainer(t *testing.T) {
	out := emitError(t)
	tests := []struct{ head, want, what string }{
		{"static sn_string sn_chain_Wrapped(Wrapped v)", "sn_string out = sn_Wrapped_Error(v);", "the chain starts with the type's own message"},
		{"static sn_string sn_chain_Wrapped(Wrapped v)", "sn_opt_Error s = sn_Wrapped_Source(v);", "and asks the type for its source"},
		{"static sn_string sn_chain_Wrapped(Wrapped v)", "while (s.has) {", "walking down the sources"},
		{"static sn_string sn_chain_Wrapped(Wrapped v)", "s.val.tab->Error(s.val.data), 3);", "each source's message, joined after a colon and a space"},
		{"static sn_string sn_chain_Wrapped(Wrapped v)", "sn_opt_Error next = s.val.tab->Source(s.val.data);", "each source asked for its own through its table"},
		{"static sn_string sn_chain_Wrapped(Wrapped v)", "sn_drop_Error(&s.val);", "and dropped once asked"},
		{"static sn_string sn_chain_Code(Code v)", "sn_string out = sn_str_Code(v);", "%+v on a derived message starts with the printed form"},
		{"static sn_string sn_chain_Code(Code v)", "sn_opt_Error s = sn_src_Code(v);", "and with the derived source"},
		{"static sn_string sn_chain_Error(Error v)", "sn_string out = v.tab->Error(v.data);", "%+v on a trait value starts through its table"},
		{"static sn_string sn_chain_Error(Error v)", "sn_opt_Error s = v.tab->Source(v.data);", "and asks it for its source the same way"},
	}
	for _, tt := range tests {
		if body := cFunction(t, out, tt.head); !strings.Contains(body, tt.want) {
			t.Errorf("%s: %q lacks %q:\n%s", tt.what, tt.head, tt.want, body)
		}
	}
}

func TestTraitText(t *testing.T) {
	body := cFunction(t, emitError(t), "static sn_string sn_msg_Error(Error v)")
	if !strings.Contains(body, "return v.tab->Error(v.data);") {
		t.Errorf("a trait value of Error does not print through its table:\n%s", body)
	}
}
