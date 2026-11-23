// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/snello-lang/snello/lsp/protocol"
)

// TestApply applies changes as an editor sends them: at a line's start,
// middle and end, across lines, after a character past ASCII counted in
// UTF-16, several in one notification, each on the text the one before
// left, and the whole text replaced.
func TestApply(t *testing.T) {
	const text = "let é = 1\nfunc f() {}\n"
	tests := []struct {
		name    string
		changes []protocol.TextDocumentContentChangeEvent
		want    string
	}{
		{"insert at a line's start", []protocol.TextDocumentContentChangeEvent{edit(1, 0, 1, 0, "pub ")}, "let é = 1\npub func f() {}\n"},
		{"replace after é", []protocol.TextDocumentContentChangeEvent{edit(0, 8, 0, 9, "2")}, "let é = 2\nfunc f() {}\n"},
		{"delete at a line's end", []protocol.TextDocumentContentChangeEvent{edit(1, 9, 1, 11, "")}, "let é = 1\nfunc f() \n"},
		{"across lines", []protocol.TextDocumentContentChangeEvent{edit(0, 9, 1, 4, ";\nfn")}, "let é = 1;\nfn f() {}\n"},
		{"two in order", []protocol.TextDocumentContentChangeEvent{edit(0, 0, 0, 3, "var"), edit(0, 0, 0, 3, "VAR")}, "VAR é = 1\nfunc f() {}\n"},
		{"the whole text", []protocol.TextDocumentContentChangeEvent{{Text: "package main\n"}}, "package main\n"},
	}

	for _, tt := range tests {
		d := &document{text: []byte(text)}
		if err := d.apply(tt.changes, protocol.PositionEncodingUTF16); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}

		if string(d.text) != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, d.text, tt.want)
		}
	}

	d := &document{text: []byte(text)}
	changes := []protocol.TextDocumentContentChangeEvent{edit(0, 0, 0, 3, "var"), edit(1, 4, 0, 2, "x")}
	if err := d.apply(changes, protocol.PositionEncodingUTF16); err == nil || !strings.Contains(err.Error(), "ends before it starts") || string(d.text) != text {
		t.Errorf("a range that ends before it starts, after a good change: %v, text %q, want the text as it was", err, d.text)
	}
}

// TestPaths maps a file's URI to its path and back, with an escaped
// character, reads the forms an editor writes, a drive escaped and a
// host of this machine, a share on a network on Windows alone, and
// refuses a URI that names no file of this machine.
func TestPaths(t *testing.T) {
	path := filepath.Join(string(filepath.Separator)+"home", "me", "my code", "a.snello")
	if runtime.GOOS == windows {
		path = `C:\home\me\my code\a.snello`
	}

	uri := uriOf(path)
	if !strings.HasPrefix(string(uri), "file:///") || !strings.Contains(string(uri), "my%20code") {
		t.Errorf("uriOf(%q) = %q", path, uri)
	}

	if got := pathOf(uri); got != path {
		t.Errorf("pathOf(%q) = %q, want %q", uri, got, path)
	}

	forms := map[protocol.DocumentURI]string{
		"file://localhost/home/me/a.snello": filepath.FromSlash("/home/me/a.snello"),
		"file:///c%3A/x/a.snello":           filepath.FromSlash("/c:/x/a.snello"), // a drive means nothing here: a path
		"file://server/share/a.snello":      "",                                   // another machine's
	}
	if runtime.GOOS == windows {
		forms = map[protocol.DocumentURI]string{
			"file:///c%3A/x/a.snello":      `c:\x\a.snello`,
			"file:///C:/x/a.snello":        `C:\x\a.snello`,
			"file://server/share/a.snello": `\\server\share\a.snello`,
		}
	}

	for u, want := range forms {
		if got := pathOf(u); got != want {
			t.Errorf("pathOf(%q) = %q, want %q", u, got, want)
		}

		if want != "" && runtime.GOOS == windows && pathOf(uriOf(want)) != want {
			t.Errorf("%q does not survive its URI, %q", want, uriOf(want))
		}
	}

	for _, u := range []protocol.DocumentURI{"untitled:Untitled-1", "", "file://"} {
		if got := pathOf(u); got != "" {
			t.Errorf("pathOf(%q) = %q, want none", u, got)
		}
	}

	if !isDrivePath("/c:/x") || !isDrivePath("/C:/x") || isDrivePath("/cd/x") || isDrivePath("/1:/x") {
		t.Error("isDrivePath does not tell a drive from a directory")
	}
}

func edit(line, char, endLine, endChar uint32, text string) protocol.TextDocumentContentChangeEvent {
	return protocol.TextDocumentContentChangeEvent{Range: &protocol.Range{Start: pos(line, char), End: pos(endLine, endChar)}, Text: text}
}
