// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseFormat(t *testing.T) {
	tests := []struct {
		format string
		want   []FormatPiece
		err    string
	}{
		{"plain", []FormatPiece{{Text: "plain"}}, ""},
		{"%d%%", []FormatPiece{{Verb: 'd', Width: -1, Prec: -1}, {Text: "%"}}, ""},
		{"x=%-6.2f°C", []FormatPiece{{Text: "x="}, {Verb: 'f', Minus: true, Width: 6, Prec: 2}, {Text: "°C"}}, ""}, // a UTF-8 byte stays a byte
		{"%.f", []FormatPiece{{Verb: 'f', Width: -1, Prec: 0}}, ""},
		{"%8.3d|", []FormatPiece{{Verb: 'd', Width: 8, Prec: 3}, {Text: "|"}}, ""},
		{"50%", nil, "the format ends in a verb with no letter"},
		{"%x%X%o%b%c%e%g", []FormatPiece{
			{Verb: 'x', Width: -1, Prec: -1}, {Verb: 'X', Width: -1, Prec: -1}, {Verb: 'o', Width: -1, Prec: -1}, {Verb: 'b', Width: -1, Prec: -1},
			{Verb: 'c', Width: -1, Prec: -1}, {Verb: 'e', Width: -1, Prec: -1}, {Verb: 'g', Width: -1, Prec: -1},
		}, ""},
		{"%E", nil, "fmt has no verb %E"},
		{"%U", nil, "fmt has no verb %U"},
		{"%05d|%02x|%08.3f|%0v", []FormatPiece{
			{Verb: 'd', Zero: true, Width: 5, Prec: -1}, {Text: "|"}, {Verb: 'x', Zero: true, Width: 2, Prec: -1}, {Text: "|"},
			{Verb: 'f', Zero: true, Width: 8, Prec: 3}, {Text: "|"}, {Verb: 'v', Zero: true, Width: -1, Prec: -1},
		}, ""},
		{"%-05d|%0-5d", []FormatPiece{{Verb: 'd', Minus: true, Width: 5, Prec: -1}, {Text: "|"}, {Verb: 'd', Minus: true, Width: 5, Prec: -1}}, ""}, // the minus flag overrides the zero
		{"%05s", nil, "fmt takes the 0 flag on a number only, not with %s"},
		{"%05q", nil, "fmt takes the 0 flag on a number only, not with %q"},
		{"%05c", nil, "fmt takes the 0 flag on a number only, not with %c"},
		{"%05t", nil, "fmt takes the 0 flag on a number only, not with %t"},
		{"%0%", nil, "fmt has no verb %%"},
		{"%1000000d|%.1000000f", []FormatPiece{{Verb: 'd', Width: 1000000, Prec: -1}, {Text: "|"}, {Verb: 'f', Width: -1, Prec: 1000000}}, ""},
		{"%1000001d", nil, "fmt takes a width and a precision of 1000000 at most"},
		{"%.1000001f", nil, "fmt takes a width and a precision of 1000000 at most"},
		{"%.4294967293f", nil, "fmt takes a width and a precision of 1000000 at most"},           // past an int of 32 bits
		{"%.99999999999999999999f", nil, "fmt takes a width and a precision of 1000000 at most"}, // and past one of 64, which wrapped to 9
		{"%18446744073709551621d", nil, "fmt takes a width and a precision of 1000000 at most"},
		{"%#v", nil, "fmt takes no # flag"},
		{"%*d", nil, "fmt takes no * width or precision"},
		{"%+v|%-+8v", []FormatPiece{{Verb: 'v', Plus: true, Width: -1, Prec: -1}, {Text: "|"}, {Verb: 'v', Plus: true, Minus: true, Width: 8, Prec: -1}}, ""}, // the plus flag, an error's chain, beside the minus
		{"%+d", nil, "fmt takes the + flag on %v of an error only"},
		{"%+s", nil, "fmt takes the + flag on %v of an error only"},
		{"%+q", nil, "fmt takes the + flag on %v of an error only"},
	}
	for _, tt := range tests {
		got, err := ParseFormat(tt.format)
		if tt.err != "" {
			if err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Errorf("%q: got %v, want an error containing %q", tt.format, err, tt.err)
			}

			continue
		}

		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%q: got %+v, %v; want %+v", tt.format, got, err, tt.want)
		}
	}
}

// TestZeroPads checks which pieces make up their width with zeros: one
// with the zero flag, but for an integer verb with a precision, whose
// digits the precision has made up already, as Go pads %06.3d with
// spaces.
func TestZeroPads(t *testing.T) {
	tests := []struct {
		format string
		want   bool
	}{
		{"%05d", true},
		{"%5d", false},
		{"%-05d", false},
		{"%06.3d", false},
		{"%06.3x", false},
		{"%06.3X", false},
		{"%06.3o", false},
		{"%06.3b", false},
		{"%08.3f", true},
		{"%08.3e", true},
		{"%08.3g", true},
		{"%08.3v", true},
	}
	for _, tt := range tests {
		pieces, err := ParseFormat(tt.format)
		if err != nil || len(pieces) != 1 {
			t.Fatalf("%q: %+v, %v", tt.format, pieces, err)
		}

		if got := pieces[0].ZeroPads(); got != tt.want {
			t.Errorf("%q: got %t, want %t", tt.format, got, tt.want)
		}
	}
}
