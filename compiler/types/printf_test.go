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
// as the verbs, the format is a literal, and fmt takes only its verbs
// and the minus flag.
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
		{`fmt.Printf("%x", 1)`, `fmt has no verb %x`},
		{`fmt.Printf("%05d", 1)`, `fmt takes no 0 flag`},
		{`fmt.Printf("%+d", 1)`, `fmt takes no + flag`},
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
			"func f<T: fmt.Stringer, U: Error, V>(w W, e E, c Color, p Plain, er error, t T, u U, v V, sh ref Shape, fa ref Fault, n ref int, q *int) { " + tt.src + " }\n"
		got := checkErrors(t, src)
		switch {
		case tt.want == "" && len(got) > 0:
			t.Errorf("%s: got %q, want no error", tt.src, got)
		case tt.want != "" && (len(got) != 1 || !strings.Contains(got[0], tt.want)):
			t.Errorf("%s: got %q, want one error containing %q", tt.src, got, tt.want)
		}
	}
}
