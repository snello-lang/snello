// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file reads the formats of fmt's printing functions: the grammar
// of Go's fmt, of which Snello's fmt takes the verbs %v, %d, %x, %X,
// %o, %b, %c, %s, %q, %f, %e, %g and %t, a width, a precision, the
// minus flag, the zero flag on a number and the plus flag on %v of an
// error, which prints its chain. The checker checks a format against
// its arguments with it, as go vet's printf check does, and both
// backends lower a format with it, so the three read one grammar.

package types

import (
	"strconv"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// FormatText returns the text of the format e that fmt's printing
// functions take, read when the program is built: a string literal,
// or a constant declared as one, found through info. It reports false
// for any other expression, whose text is only known when the program
// runs.
func FormatText(info *Info, e ast.Expr) (string, bool) {
	var obj Object
	switch x := unparen(e).(type) {
	case *ast.Ident:
		obj = info.Uses[x]
	case *ast.SelectorExpr:
		obj = info.Uses[x.Sel]
	}

	return formatText(e, obj)
}

// formatText reads the format e, a string literal or the constant obj
// that e names.
func formatText(e ast.Expr, obj Object) (string, bool) {
	text := ""
	if lit, ok := unparen(e).(*ast.BasicLit); ok {
		if lit.Kind != token.STRING {
			return "", false
		}

		text = lit.Value
	} else if k, ok := obj.(*Const); ok && k.typ != nil && isString(k.typ) {
		text = k.value
	} else {
		return "", false
	}

	s, err := strconv.Unquote(text)
	return s, err == nil
}

// A FormatPiece is a run of text a format prints as it is, or a verb that
// prints the next argument.
type FormatPiece struct {
	Text  string // the text, when Verb is 0
	Verb  byte   // one of FormatVerbs
	Minus bool   // pad on the right, %-6s
	Zero  bool   // pad a number with zeros after its sign, %06d; never set with Minus, which overrides it
	Plus  bool   // print an error's chain, %+v
	Width int    // the width, or -1
	Prec  int    // the precision, or -1
}

// FormatVerbs are the verbs Snello's fmt prints.
const FormatVerbs = "bcdefgoqstvxX"

// integerVerbs are the verbs that print an integer's digits, in one
// base or another, and take a precision as the least of them.
const integerVerbs = "bdoxX"

// ZeroPads reports whether the piece's width is made up with zeros
// rather than spaces: the zero flag, but for an integer with a
// precision, whose digits the precision has already made up, as in Go.
func (p FormatPiece) ZeroPads() bool {
	return p.Zero && !(p.Prec >= 0 && formatHasByte(integerVerbs, p.Verb))
}

// An FormatError says what is wrong with a format.
type FormatError struct {
	Msg string
}

func (e *FormatError) Error() string { return e.Msg }

// ParseFormat reads a format into its pieces, adjacent text joined into one
// and %% read as a percent sign, or returns what is wrong with it: a
// verb fmt does not print, a flag or a * it does not take, or a % that
// ends the format.
func ParseFormat(format string) ([]FormatPiece, error) {
	var pieces []FormatPiece
	var text []byte // the bytes as they are: a byte above 0x7f is part of a UTF-8 sequence, not a rune
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			text = append(text, format[i])
			continue
		}

		i++
		p := FormatPiece{Width: -1, Prec: -1}
		for ; i < len(format) && isFormatFlag(format[i]); i++ {
			switch format[i] {
			case '-':
				p.Minus = true
			case '+':
				p.Plus = true
			case '0':
				p.Zero = true
			default:
				return nil, &FormatError{"fmt takes no " + string(format[i]) + " flag: " + formatQuoted(format)}
			}
		}

		i, p.Width = formatNumber(format, i)
		if i < len(format) && format[i] == '.' {
			i, p.Prec = formatNumber(format, i+1)
			p.Prec = max(p.Prec, 0) // %.f is a precision of zero, as in Go
		}

		if p.Width > FormatMost || p.Prec > FormatMost {
			return nil, &FormatError{"fmt takes a width and a precision of " + strconv.Itoa(FormatMost) + " at most: " + formatQuoted(format)}
		}

		if i >= len(format) {
			return nil, &FormatError{"the format ends in a verb with no letter: " + formatQuoted(format)}
		}

		switch c := format[i]; {
		case c == '%' && p.Width < 0 && p.Prec < 0 && !p.Minus && !p.Zero:
			text = append(text, '%')
			continue
		case c == '*':
			return nil, &FormatError{"fmt takes no * width or precision: " + formatQuoted(format)}
		case c < 0x80 && formatHasByte(FormatVerbs, c):
			p.Verb = c
		default:
			return nil, &FormatError{"fmt has no verb %" + string(rune(c)) + ": " + formatQuoted(format)}
		}

		if p.Plus && p.Verb != 'v' {
			return nil, &FormatError{"fmt takes the + flag on %v of an error only: " + formatQuoted(format)}
		}

		if p.Zero && formatHasByte("cqst", p.Verb) {
			return nil, &FormatError{"fmt takes the 0 flag on a number only, not with %" + string(p.Verb) + ": " + formatQuoted(format)}
		}

		p.Zero = p.Zero && !p.Minus

		if len(text) > 0 {
			pieces = append(pieces, FormatPiece{Text: string(text)})
			text = text[:0]
		}

		pieces = append(pieces, p)
	}

	if len(text) > 0 {
		pieces = append(pieces, FormatPiece{Text: string(text)})
	}

	return pieces, nil
}

// FormatCount returns how many arguments the pieces print.
func FormatCount(pieces []FormatPiece) int {
	n := 0
	for _, p := range pieces {
		if p.Verb != 0 {
			n++
		}
	}

	return n
}

func isFormatFlag(c byte) bool { return c == '-' || c == '+' || c == '#' || c == ' ' || c == '0' }

// FormatMost is the largest width and the largest precision a format
// takes: a text of a million characters is past any table or number a
// program prints, and a bound keeps what the runtime allocates for one
// verb within reason.
const FormatMost = 1000000

// formatNumber reads the decimal number at i, returning where it ends
// and its value, or -1 when there is none. A number past FormatMost
// reads as one past it, however many digits it has, so that none wraps
// into a small one.
func formatNumber(s string, i int) (int, int) {
	n := -1
	for ; i < len(s) && '0' <= s[i] && s[i] <= '9'; i++ {
		n = min(max(n, 0)*10+int(s[i]-'0'), FormatMost+1)
	}

	return i, n
}

func formatHasByte(s string, c byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return true
		}
	}

	return false
}

func formatQuoted(s string) string { return strconv.Quote(s) }
