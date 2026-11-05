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
		{"%x", nil, "fmt has no verb %x"},
		{"%05d", nil, "fmt takes no 0 flag"},
		{"%#v", nil, "fmt takes no # flag"},
		{"%*d", nil, "fmt takes no * width or precision"},
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
