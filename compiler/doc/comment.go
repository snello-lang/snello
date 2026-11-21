// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// What a doc comment's text is read by: a heading's mark, the marks of
// a list's items, the schemes of a link to the web, and the bracket
// and separators of a link to a declaration.
const (
	headingMark   = "# "
	httpsScheme   = "https://"
	httpScheme    = "http://"
	linkOpen      = '['
	linkClose     = ']'
	memberSep     = '.'
	pathSep       = '/'
	urlTrailing   = ".,:;?!)'\""
	numberedMarks = ".)"
	bulletMarks   = "-*+•"
)

// A Block is one piece of a doc comment: a *Paragraph, a *Heading, a
// *List or a *Code.
type Block interface{ block() }

// A Paragraph is lines of text, as written.
type Paragraph struct{ Lines []string }

// A Heading is a line # Heading, alone in its paragraph.
type Heading struct{ Text string }

// A List is items, each its marker and its lines, the first after the
// marker.
type List struct{ Items []*Item }

// An Item is one entry of a list.
type Item struct {
	Marker string
	Lines  []string
}

// A Code is lines shown as written, their common indentation taken off.
type Code struct{ Lines []string }

func (*Paragraph) block() {}
func (*Heading) block()   {}
func (*List) block()      {}
func (*Code) block()      {}

// Parse reads a doc comment's text, as ast.CommentGroup.Text gives it,
// into blocks: once the indentation every line shares is taken off,
// unindented lines are a paragraph, a lone # line between blank ones a
// heading, and a span of indented lines a list when its first line
// begins with a marker and code otherwise.
func Parse(text string) []Block {
	lines := dedent(strings.Split(strings.TrimRight(text, lineSep), lineSep))
	var blocks []Block
	for i := 0; i < len(lines); {
		line := lines[i]
		switch {
		case strings.TrimSpace(line) == "":
			i++
		case !indented(line):
			start := i
			for i < len(lines) && strings.TrimSpace(lines[i]) != "" && !indented(lines[i]) {
				i++
			}

			if text, ok := strings.CutPrefix(line, headingMark); ok && i-start == 1 && (start == 0 || blank(lines[start-1])) && (i == len(lines) || blank(lines[i])) {
				blocks = append(blocks, &Heading{strings.TrimSpace(text)})
			} else {
				blocks = append(blocks, &Paragraph{lines[start:i]})
			}
		default:
			start := i
			for i < len(lines) && (blank(lines[i]) || indented(lines[i])) {
				i++
			}

			end := i
			for end > start && blank(lines[end-1]) {
				end--
			}

			blocks = append(blocks, indentedBlock(lines[start:end]))
		}
	}

	return blocks
}

// indentedBlock reads a span of indented lines: a list when its first
// line begins with a marker, each marked line an item and the others
// continuing the item before them, and code otherwise.
func indentedBlock(lines []string) Block {
	if _, _, ok := cutMarker(strings.TrimSpace(lines[0])); !ok {
		return &Code{dedent(lines)}
	}

	list := &List{}
	for _, line := range lines {
		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}

		if marker, rest, ok := cutMarker(text); ok {
			list.Items = append(list.Items, &Item{marker, []string{rest}})
			continue
		}

		item := list.Items[len(list.Items)-1]
		item.Lines = append(item.Lines, text)
	}

	return list
}

// cutMarker splits a list item's marker from its text: a bullet, or a
// number and . or ), and a space.
func cutMarker(text string) (marker, rest string, ok bool) {
	n := 0
	for n < len(text) && text[n] >= '0' && text[n] <= '9' {
		n++
	}

	switch {
	case n > 0 && n < len(text) && strings.IndexByte(numberedMarks, text[n]) >= 0:
		n++
	case n == 0 && text != "":
		r, size := utf8.DecodeRuneInString(text)
		if !strings.ContainsRune(bulletMarks, r) {
			return "", "", false
		}

		n = size
	default:
		return "", "", false
	}

	if n >= len(text) || text[n] != ' ' && text[n] != '\t' {
		return "", "", false
	}

	return text[:n], strings.TrimSpace(text[n:]), true
}

// dedent takes the lines' common leading space off.
func dedent(lines []string) []string {
	common := -1
	for _, line := range lines {
		if blank(line) {
			continue
		}

		n := len(line) - len(strings.TrimLeft(line, " \t"))
		if common < 0 || n < common {
			common = n
		}
	}

	if common <= 0 { // no line has text, or one starts at the margin: nothing to take off
		return lines
	}

	out := make([]string, len(lines))
	for i, line := range lines {
		if len(line) >= common {
			out[i] = line[common:]
		}
	}

	return out
}

func indented(line string) bool { return line != "" && (line[0] == ' ' || line[0] == '\t') }

func blank(line string) bool { return strings.TrimSpace(line) == "" }

// A Span is a piece of a line: text, a link to a declaration, which
// Name names as written in the brackets, or a link to the web, URL.
type Span struct {
	Text string
	Name string
	URL  string
}

// Spans splits a line of a paragraph, a heading or a list into text and
// links: a link is a name in brackets whose [ follows a
// space, punctuation other than a dot or the start of the line and
// whose ] precedes a space, punctuation or the end, so that v.[f] and
// [0, n) stay text; and an http or https URL up to a space, its
// trailing punctuation left out.
func Spans(line string) []Span {
	var spans []Span
	text := 0 // where the text not yet in a span begins
	emit := func(end int, link Span) {
		if end > text {
			spans = append(spans, Span{Text: line[text:end]})
		}

		spans = append(spans, link)
	}

	for i := 0; i < len(line); {
		if strings.HasPrefix(line[i:], httpsScheme) || strings.HasPrefix(line[i:], httpScheme) {
			end := i
			for end < len(line) && line[end] != ' ' && line[end] != '\t' {
				end++
			}

			for end > i && strings.IndexByte(urlTrailing, line[end-1]) >= 0 {
				end--
			}

			emit(i, Span{Text: line[i:end], URL: line[i:end]})
			text, i = end, end
			continue
		}

		if line[i] == linkOpen && opensLink(line, i) {
			if j := strings.IndexByte(line[i:], linkClose); j > 0 {
				name := line[i+1 : i+j]
				if closesLink(line, i+j+1) && isLinkName(name) {
					emit(i, Span{Text: line[i : i+j+1], Name: name})
					text, i = i+j+1, i+j+1
					continue
				}
			}
		}

		i++
	}

	if text < len(line) {
		spans = append(spans, Span{Text: line[text:]})
	}

	return spans
}

func opensLink(line string, i int) bool {
	if i == 0 {
		return true
	}

	r, _ := utf8.DecodeLastRuneInString(line[:i])
	return r != memberSep && (unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r))
}

func closesLink(line string, i int) bool {
	if i == len(line) {
		return true
	}

	r, _ := utf8.DecodeRuneInString(line[i:])
	return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
}

// isLinkName reports whether s is a name a link may hold: a package's
// path, its last element and a name, or names joined by dots.
func isLinkName(s string) bool {
	if s == "" {
		return false
	}

	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == pathSep || r == memberSep }) {
		if !isIdent(part) {
			return false
		}
	}

	first, last := s[0], s[len(s)-1]
	return first != memberSep && first != pathSep && last != memberSep && last != pathSep &&
		!strings.Contains(s, "..") && !strings.Contains(s, "//")
}

func isIdent(s string) bool {
	for i, r := range s {
		if !unicode.IsLetter(r) && r != '_' && (i == 0 || !unicode.IsDigit(r)) {
			return false
		}
	}

	return s != ""
}
