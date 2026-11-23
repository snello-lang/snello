// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"io"
	"strconv"
	"strings"
	"testing"
)

// TestLSP takes the flags an editor's client passes, and serves a
// session that shuts down and exits with 0, and one that exits with no
// shutdown with 1, as the protocol asks.
func TestLSP(t *testing.T) {
	for _, args := range [][]string{{"--stdio"}, {"--stdio", "--clientProcessId", "4242"}, {"--clientProcessId=4242"}, nil} {
		fs := newFlagSet(lookup(lspCommand), io.Discard)
		declareLSP(fs, io.Discard, io.Discard)
		if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
			t.Errorf("snello lsp %q: %v, %d arguments left", args, err, fs.NArg())
		}
	}

	session := frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{}}}`) +
		frame(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`) +
		frame(`{"jsonrpc":"2.0","method":"exit"}`)
	var out, errs bytes.Buffer
	if code := lsp(strings.NewReader(session), &out, &errs); code != exitSuccess {
		t.Errorf("a session that shuts down and exits: code %d; wrote %q", code, out.String())
	}

	if !strings.Contains(out.String(), `"name":"snello"`) || !strings.Contains(out.String(), `"id":2,"result":null`) {
		t.Errorf("the session's answers: %q", out.String())
	}

	if code := lsp(strings.NewReader(frame(`{"jsonrpc":"2.0","method":"exit"}`)), &out, &errs); code != exitFailure {
		t.Errorf("an exit with no shutdown: code %d, want %d", code, exitFailure)
	}
}

// frame frames a message's body as the protocol writes it on a stream.
func frame(body string) string {
	return "Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body
}
