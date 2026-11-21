// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import "testing"

// TestResolve resolves the forms a link takes, and refuses what names
// nothing.
func TestResolve(t *testing.T) {
	p := load(t, "strings", false)
	for name, want := range map[string]bool{
		"Builder": true, "Builder.WriteString": true, "Builder.buf": true, "NewReader": true,
		"io.Reader": true, "io.Reader.Read": true, "unicode/utf8": true, "unicode/utf8.RuneLen": true,
		"strings": true, "int": true, "Option": true,
		"Builderr": false, "Builder.Nope": false, "io.Nope": false, "nopkg.X": false, "unicode/utf8.Nope": false,
	} {
		if got := p.Resolve(name); got != want {
			t.Errorf("Resolve(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestHoldsLink finds a name in a link's brackets, and not where it is
// text.
func TestHoldsLink(t *testing.T) {
	for _, c := range []struct {
		text, name string
		want       bool
	}{
		{"// see [Builder].", "Builder", true},
		{"// a Builder and [Builder.Len]", "Builder.Len", true},
		{"// a Builder", "Builder", false},
		{"// [Builders]", "Builder", false},
	} {
		if got := holdsLink(c.text, c.name); got != c.want {
			t.Errorf("holdsLink(%q, %q) = %v, want %v", c.text, c.name, got, c.want)
		}
	}
}

// TestTarget says where a link points: the package and the anchor on
// its page.
func TestTarget(t *testing.T) {
	p := load(t, "strings", false)
	for name, want := range map[string][2]string{
		"Builder":              {"strings", "Builder"},
		"Builder.WriteString":  {"strings", "Builder.WriteString"},
		"io.Reader":            {"io", "Reader"},
		"unicode/utf8.RuneLen": {"unicode/utf8", "RuneLen"},
		"unicode/utf8":         {"unicode/utf8", ""},
		"Option":               {BuiltinPath, "Option"},
	} {
		path, anchor, ok := p.Target(name)
		if !ok || path != want[0] || anchor != want[1] {
			t.Errorf("Target(%q) = %q, %q, %v, want %v", name, path, anchor, ok, want)
		}
	}
}
