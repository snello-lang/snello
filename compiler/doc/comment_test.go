// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []Block
	}{
		{"a paragraph", "One line.\nTwo lines.\n", []Block{&Paragraph{[]string{"One line.", "Two lines."}}}},
		{"two paragraphs", "One.\n\nTwo.\n", []Block{&Paragraph{[]string{"One."}}, &Paragraph{[]string{"Two."}}}},
		{"a heading", "Intro.\n\n# Use\n\nText.\n", []Block{&Paragraph{[]string{"Intro."}}, &Heading{"Use"}, &Paragraph{[]string{"Text."}}}},
		{"a # line in a paragraph is text", "One.\n# not a heading\n", []Block{&Paragraph{[]string{"One.", "# not a heading"}}}},
		{"code keeps its inner indentation", "Use it:\n\n\tif x {\n\t\ty()\n\t}\n", []Block{
			&Paragraph{[]string{"Use it:"}}, &Code{[]string{"if x {", "\ty()", "}"}},
		}},
		{"a list whose item continues on an indented line", "Rules:\n\n  - one,\n    still one\n  - two\n", []Block{
			&Paragraph{[]string{"Rules:"}}, &List{[]*Item{{"-", []string{"one,", "still one"}}, {"-", []string{"two"}}}},
		}},
		{"a numbered list", "Steps:\n 1. first\n 2)\tsecond\n", []Block{&Paragraph{[]string{"Steps:"}}, &List{[]*Item{{"1.", []string{"first"}}, {"2)", []string{"second"}}}}}},
		{"indentation every line shares is no code", " Block doc,\n on two lines.\n", []Block{&Paragraph{[]string{"Block doc,", "on two lines."}}}},
		{"a number at a line's start, not indented, is text", "fi by fi and ² by / 2.\n2. The forms.\n", []Block{
			&Paragraph{[]string{"fi by fi and ² by / 2.", "2. The forms."}},
		}},
	}

	for _, tt := range tests {
		if got := Parse(tt.text); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: Parse = %#v, want %#v", tt.name, got, tt.want)
		}
	}
}

func TestSpans(t *testing.T) {
	tests := []struct {
		line string
		want []Span
	}{
		{"See [Builder] for more.", []Span{{Text: "See "}, {Text: "[Builder]", Name: "Builder"}, {Text: " for more."}}},
		{"A [io.Reader], [Builder.Len] and [unicode/utf8.RuneLen].", []Span{
			{Text: "A "}, {Text: "[io.Reader]", Name: "io.Reader"}, {Text: ", "}, {Text: "[Builder.Len]", Name: "Builder.Len"},
			{Text: " and "}, {Text: "[unicode/utf8.RuneLen]", Name: "unicode/utf8.RuneLen"}, {Text: "."},
		}},
		{"v.[f] the field of v", []Span{{Text: "v.[f] the field of v"}}},
		{"the bytes in [0, n)", []Span{{Text: "the bytes in [0, n)"}}},
		{"a[i] is not a link", []Span{{Text: "a[i] is not a link"}}},
		{"(see https://go.dev/doc/comment).", []Span{{Text: "(see "}, {Text: "https://go.dev/doc/comment", URL: "https://go.dev/doc/comment"}, {Text: ")."}}},
		{"[strings]", []Span{{Text: "[strings]", Name: "strings"}}},
	}

	for _, tt := range tests {
		if got := Spans(tt.line); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Spans(%q) = %#v, want %#v", tt.line, got, tt.want)
		}
	}
}
