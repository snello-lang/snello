// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"testing"

	"github.com/snello-lang/snello/lsp/protocol"
)

// TestPositions maps byte offsets to positions and back in each
// encoding: ASCII, a character of two bytes, one of four, which is two
// units in UTF-16, a tab, a line that ends with a carriage return, and
// positions past a line and past the text.
func TestPositions(t *testing.T) {
	const text = "ab\n" + // line 0
		"é x\n" + // line 1: é is two bytes, one unit
		"😀y\n" + // line 2: the emoji is four bytes, two UTF-16 units
		"\tz\r\n" + // line 3: a tab, and a carriage return before the newline
		"end"
	tests := []struct {
		offset      int
		utf8, utf16 protocol.Position
	}{
		{0, pos(0, 0), pos(0, 0)},
		{2, pos(0, 2), pos(0, 2)},
		{3, pos(1, 0), pos(1, 0)},
		{5, pos(1, 2), pos(1, 1)}, // after é
		{6, pos(1, 3), pos(1, 2)}, // at x
		{8, pos(2, 0), pos(2, 0)},
		{12, pos(2, 4), pos(2, 2)}, // at y, after the emoji
		{14, pos(3, 0), pos(3, 0)},
		{15, pos(3, 1), pos(3, 1)}, // at z, after the tab
		{19, pos(4, 1), pos(4, 1)},
	}

	for _, tt := range tests {
		for enc, want := range map[string]protocol.Position{protocol.PositionEncodingUTF8: tt.utf8, protocol.PositionEncodingUTF16: tt.utf16} {
			if got := positionOf([]byte(text), tt.offset, enc); got != want {
				t.Errorf("positionOf(%d, %s) = %v, want %v", tt.offset, enc, got, want)
			}

			if got := offsetOf([]byte(text), want, enc); got != tt.offset {
				t.Errorf("offsetOf(%v, %s) = %d, want %d", want, enc, got, tt.offset)
			}
		}
	}

	clamped := []struct {
		p    protocol.Position
		want int
	}{
		{pos(0, 99), 2},  // past its line: the line's end
		{pos(3, 99), 16}, // past a line that ends in \r\n: before the \r
		{pos(9, 0), len(text)},
		{pos(2, 1), 8}, // in the middle of the emoji's two units: its start
	}

	for _, tt := range clamped {
		if got := offsetOf([]byte(text), tt.p, protocol.PositionEncodingUTF16); got != tt.want {
			t.Errorf("offsetOf(%v) = %d, want %d", tt.p, got, tt.want)
		}
	}

	if got := positionOf([]byte(text), 999, protocol.PositionEncodingUTF16); got != pos(4, 3) {
		t.Errorf("an offset past the text: %v, want the text's end", got)
	}
}

// TestWordEnd covers the word at an offset: an identifier with a letter
// past ASCII, a number, an operator, and a line's and the text's end.
func TestWordEnd(t *testing.T) {
	const text = "x := naïve_1 + 42\nz"
	tests := []struct{ offset, want int }{
		{0, 1},                 // x
		{2, 3},                 // :
		{5, 13},                // naïve_1, ï of two bytes
		{14, 15},               // +
		{16, 18},               // 42
		{18, 18},               // the newline: a point
		{len(text), len(text)}, // the text's end: a point
		{len(text) + 5, len(text)},
	}

	for _, tt := range tests {
		if got := wordEnd([]byte(text), tt.offset); got != tt.want {
			t.Errorf("wordEnd(%d) = %d, want %d", tt.offset, got, tt.want)
		}
	}
}

func pos(line, char uint32) protocol.Position {
	return protocol.Position{Line: line, Character: char}
}
