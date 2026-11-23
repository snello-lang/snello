// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"bytes"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/snello-lang/snello/lsp/protocol"
)

// The byte that ends a line, and the one before it that ends a line on
// Windows, which the protocol does not count as a character of the
// line.
const (
	newline        = '\n'
	carriageReturn = '\r'
)

// offsetOf returns the byte offset in text of the position p, whose
// character counts in enc. A character past the end of its line is
// taken as the line's end, and a line past the last as the text's end,
// as the protocol says.
func offsetOf(text []byte, p protocol.Position, enc string) int {
	start := 0
	for line := uint32(0); line < p.Line; line++ {
		i := bytes.IndexByte(text[start:], newline)
		if i < 0 {
			return len(text)
		}

		start += i + 1
	}

	end := lineEnd(text, start)
	return start + byteOffset(text[start:end], p.Character, enc)
}

// positionOf returns the position of the byte offset in text, its
// character counted in enc. An offset past the text is taken as its
// end.
func positionOf(text []byte, offset int, enc string) protocol.Position {
	offset = min(max(offset, 0), len(text))
	start := bytes.LastIndexByte(text[:offset], newline) + 1
	line := bytes.Count(text[:start], []byte{newline})
	return protocol.Position{Line: uint32(line), Character: units(text[start:offset], enc)}
}

// wordEnd returns the end of the word that starts at offset in text: an
// identifier, a keyword or a number runs to its last letter, digit or
// underscore; any other character is a word of its own; a line's end
// and the text's end are words of none, so that a diagnostic there is
// a point.
func wordEnd(text []byte, offset int) int {
	if offset < 0 || offset >= len(text) {
		return min(max(offset, 0), len(text))
	}

	r, size := utf8.DecodeRune(text[offset:])
	if r == newline || r == carriageReturn {
		return offset
	}

	if !isWordRune(r) {
		return offset + size
	}

	end := offset
	for end < len(text) {
		r, size := utf8.DecodeRune(text[end:])
		if !isWordRune(r) {
			break
		}

		end += size
	}

	return end
}

// lineEnd returns the offset in text where the line starting at start
// ends: its newline, and the carriage return before it, are not of the
// line.
func lineEnd(text []byte, start int) int {
	end := len(text)
	if i := bytes.IndexByte(text[start:], newline); i >= 0 {
		end = start + i
	}

	if end > start && text[end-1] == carriageReturn {
		end--
	}

	return end
}

// units counts the characters of line in enc: its bytes in UTF-8, its
// code points in UTF-32, and in UTF-16 one unit for a code point of the
// Basic Multilingual Plane and two for one beyond it. A byte that is
// not UTF-8 counts as the one replacement character an editor shows.
func units(line []byte, enc string) uint32 {
	if enc == protocol.PositionEncodingUTF8 {
		return uint32(len(line))
	}

	n := uint32(0)
	for len(line) > 0 {
		r, size := utf8.DecodeRune(line)
		line = line[size:]
		n += runeUnits(r, enc)
	}

	return n
}

// byteOffset returns the byte offset in line of its character char,
// counted in enc; a character past the line is the line's end, and one
// in the middle of a code point is the code point's start.
func byteOffset(line []byte, char uint32, enc string) int {
	if enc == protocol.PositionEncodingUTF8 {
		return min(int(char), len(line))
	}

	offset, n := 0, uint32(0)
	for offset < len(line) {
		r, size := utf8.DecodeRune(line[offset:])
		if n+runeUnits(r, enc) > char {
			break
		}

		n += runeUnits(r, enc)
		offset += size
	}

	return offset
}

// runeUnits counts the units of the code point r in enc, UTF-16's or
// UTF-32's: two in UTF-16 for a code point past the Basic Multilingual
// Plane, one otherwise.
func runeUnits(r rune, enc string) uint32 {
	if n := utf16.RuneLen(r); enc == protocol.PositionEncodingUTF16 && n > 0 {
		return uint32(n)
	}

	return 1
}

// isWordRune reports whether r continues a word: a letter, a digit or
// an underscore.
func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
