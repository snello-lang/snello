// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"
)

// TestPrintf checks the formats of fmt's Printf, Sprintf and Fprintf
// against their arguments, as go vet's printf check does: each verb
// takes the types Go's fmt prints with it, the arguments are as many
// as the verbs, the format is a literal, and fmt takes only its verbs,
// the minus flag and the zero flag on a number.
func TestPrintf(t *testing.T) {
	tests := []struct {
		src  string
		want string // a part of the one error, or "" for none
	}{
		{`fmt.Printf("%d %s %v %q %f %t %%", 1, "s", 2.5, "q", 1.5, true)`, ""},
		{`fmt.Printf("%d", "x")`, `fmt.Printf format %d takes an integer, not string`},
		{`fmt.Printf("%s", 42)`, `fmt.Printf format %s takes a string, not int`},
		{`fmt.Printf("%d", 2.5)`, `fmt.Printf format %d takes an integer, not f64`},
		{`fmt.Printf("%f", 1)`, `fmt.Printf format %f takes a float, not int`},
		{`fmt.Printf("%t", 1)`, `fmt.Printf format %t takes a bool, not int`},
		{`fmt.Printf("%q", 2.5)`, `fmt.Printf format %q takes a string or a rune, not f64`},
		{`fmt.Printf("%q %q", 'A', byte(66))`, ""}, // an integer quotes as its rune
		{`fmt.Printf("[%d]", 1, 2, "three")`, `fmt.Printf call has 3 arguments, but its format reads 1`},
		{`fmt.Printf("%d %d", 1)`, `fmt.Printf format %d reads argument 2, but the call has 1`},
		{`fmt.Printf("50%")`, `the format ends in a verb with no letter`},
		{`fmt.Printf("y%5", 3)`, `the format ends in a verb with no letter`},
		{`fmt.Printf("%x %X %o %b %c %e %g", 255, u8(255), -8, 5, 'A', 2.5, f32(2.5))`, ""},
		{`fmt.Printf("%x %X %x", "hi", bs, name)`, ""}, // the bytes of a string, of a []u8 and of a type declared over string
		{`fmt.Printf("%x", 2.5)`, `fmt.Printf format %x takes an integer, a string or a []u8, not f64`},
		{`fmt.Printf("%X", e)`, `fmt.Printf format %X takes an integer, a string or a []u8, not E`}, // the bytes of a string, not of what a type prints as
		{`fmt.Printf("%x", ns)`, `fmt.Printf format %x takes an integer, a string or a []u8, not []int`},
		{`fmt.Printf("%.4x %.4X %.3o %.8b", 255, 255, 8, 5)`, ""},
		{`fmt.Printf("%.2x", "hi")`, `fmt.Printf format %x takes no precision on string`},
		{`fmt.Printf("%.2X", bs)`, `fmt.Printf format %X takes no precision on []u8`},
		{`fmt.Printf("%o", "x")`, `fmt.Printf format %o takes an integer, not string`},
		{`fmt.Printf("%b", 2.5)`, `fmt.Printf format %b takes an integer, not f64`},
		{`fmt.Printf("%c", "x")`, `fmt.Printf format %c takes an integer, not string`},
		{`fmt.Printf("%.2c", 'A')`, `%c takes no precision`},
		{`fmt.Printf("%e", 1)`, `fmt.Printf format %e takes a float, not int`},
		{`fmt.Printf("%g", "x")`, `fmt.Printf format %g takes a float, not string`},
		{`fmt.Printf("%.3e %.3g %12.3e", 2.5, 2.5, 2.5)`, ""},
		{`fmt.Printf("%E", 2.5)`, `fmt has no verb %E`},
		{`fmt.Printf("%05d %02x %02X %08b %03o %08.3f %012e %08g %05v %05v", 1, 2, 3, 4, 5, 1.5, 2.5, 3.5, 7, 4.5)`, ""},
		{`fmt.Printf("%05s", "x")`, `fmt takes the 0 flag on a number only, not with %s`},
		{`fmt.Printf("%05v", "x")`, `fmt.Printf format %v takes the 0 flag on a number, not on string`},
		{`fmt.Printf("%05v", true)`, `fmt.Printf format %v takes the 0 flag on a number, not on bool`},
		{`fmt.Printf("%04x", "hi")`, `fmt.Printf format %x takes the 0 flag on a number, not on string`},
		{`fmt.Printf("%04X", bs)`, `fmt.Printf format %X takes the 0 flag on a number, not on []u8`},
		{`fmt.Printf("%#x", 1)`, `fmt takes no # flag`},
		{`fmt.Printf("%.99999999999999999999f", 2.5)`, `fmt takes a width and a precision of 1000000 at most`},
		{`fmt.Printf("%05v", c)`, `fmt.Printf format %v takes the 0 flag on a number, not on Color`},
		{`fmt.Printf("%05v", e)`, `fmt.Printf format %v takes the 0 flag on a number, not on E`},
		{`fmt.Printf("%05v", lv)`, `fmt.Printf format %v takes the 0 flag on a number, not on Level, which prints as its String`}, // a number that prints as text
		{`fmt.Printf("%08v", cel)`, `fmt.Printf format %v takes the 0 flag on a number, not on Celsius, which prints as its String`},
		{`fmt.Printf("%05d %05x %08g %08e %08f %g %x", lv, lv, cel, cel, cel, cel, name)`, ""}, // the number itself, whatever its String
		{`fmt.Printf("%+d", 1)`, `fmt takes the + flag on %v of an error only`},
		{`fmt.Printf("%+v", 1)`, `fmt.Printf format %+v takes an error, not int`},
		{`fmt.Printf("%+.3v", e)`, `fmt.Printf format %+v takes no precision`},
		{`fmt.Printf("%+8v|%-+8v", e, ref e)`, ``},
		{`fmt.Printf("%*d", 3, 1)`, `fmt takes no * width or precision`},
		{`fmt.Printf("%.2s %.3d %.1v %.4v %8.3f %-6s|", "abc", 7, 2.5, "text", 1.5, "l")`, ""},
		{`fmt.Printf("%.2v", 7)`, `%.2v takes a precision on a string or a float, not on int`},
		{`fmt.Printf("%.2q", 'A')`, `%q takes no precision on rune`},
		{`s := "%d"; fmt.Printf(s, 1)`, `fmt.Printf takes its format as a string literal or a constant`}, // a variable's text is only known when the program runs
		{`s := fmt.Sprintf("%d", "x"); print(s)`, `fmt.Sprintf format %d takes an integer, not string`},
		{`fmt.Fprintf(mut w, "%s %d", "a", 1)`, ""},
		{`fmt.Fprintf(mut w, "%s", 1)`, `fmt.Fprintf format %s takes a string, not int`},
		{`fmt.Printf("%s %v %q", e, e, e)`, ""}, // an Error type prints as its message
		{`fmt.Printf("%s %q %v", c, c, c)`, ""}, // a Stringer prints as its String
		{`fmt.Printf("%s", p)`, `fmt.Printf format %s takes a string, not Plain`},
		{`fmt.Printf("%s %q %v", er, er, er)`, ""}, // the predeclared error prints as its message
		{`fmt.Printf("%s %q %v", t, t, t)`, ""},    // a type parameter bounded by Stringer
		{`fmt.Printf("%s %q %v", u, u, u)`, ""},    // or by Error
		{`fmt.Printf("%v", v)`, ""},                // any type parameter prints with %v
		{`fmt.Printf("%s", v)`, `fmt.Printf format %s takes a string, not V`},
		{`fmt.Printf("%s %q", sh, fa)`, ""},                                     // a trait value whose trait refines Stringer or Error
		{`fmt.Printf("%s", n)`, `fmt.Printf format %s takes a string, not int`}, // the verb sees through a borrow
		{`fmt.Printf("%s", q)`, `fmt.Printf format %s takes a string, not int`}, // and through a pointer
		{`fmt.Printf("%s", fmt.Sprintf("%d", 1))`, ""},
		{`fmt.Printf("%s", fmt.Sprintf("%d", "x"))`, `fmt.Sprintf format %d takes an integer, not string`},
		{"fmt.Printf(`%d\\n`, 1)", ""}, // a raw string is a literal too
		{`fmt.Fprintf(mut w, "%s %d", "a")`, `fmt.Fprintf format %d reads argument 2, but the call has 1`},
		{`const f = "%d"; fmt.Printf(f, 1)`, ""}, // a constant is read as its literal
		{`const f = "%d"; fmt.Printf(f, "x")`, `fmt.Printf format %d takes an integer, not string`},
		{`const f = "%d %d"; fmt.Printf((f), 1)`, `fmt.Printf format %d reads argument 2, but the call has 1`},
		{`const k = 3; fmt.Printf(k, 1)`, `cannot use k (int) as ref string in argument`}, // one error: the type, not the constant
	}
	for _, tt := range tests {
		src := "package p\nimport (\"fmt\"; \"io\")\n" +
			"struct W impl io.Writer { n int }\nfunc (w mut W) Write(p ref []byte) Result<int, io.Error> { return Ok(len(p)) }\n" +
			"struct E impl Error { m string }\nfunc (e ref E) Error() string { return e.m.Clone() }\n" +
			"struct Color impl fmt.Stringer { n int }\nfunc (c ref Color) String() string { return \"red\" }\n" +
			"struct Plain { n int }\n" +
			"trait Shape: fmt.Stringer { func (s ref Self) Area() f64 }\ntrait Fault: Error { func (f ref Self) Code() int }\n" +
			"type Name string\n" +
			"type Level int impl fmt.Stringer\nfunc (l ref Level) String() string { return \"low\" }\n" +
			"type Celsius f64 impl fmt.Stringer\nfunc (c ref Celsius) String() string { return \"hot\" }\n" +
			"func f<T: fmt.Stringer, U: Error, V>(w W, e E, c Color, p Plain, er error, t T, u U, v V, sh ref Shape, fa ref Fault, n ref int, q *int, bs []u8, ns []int, name Name, lv Level, cel Celsius) { " + tt.src + " }\n"
		got := checkErrors(t, src)
		switch {
		case tt.want == "" && len(got) > 0:
			t.Errorf("%s: got %q, want no error", tt.src, got)
		case tt.want != "" && (len(got) != 1 || !strings.Contains(got[0], tt.want)):
			t.Errorf("%s: got %q, want one error containing %q", tt.src, got, tt.want)
		}
	}
}
