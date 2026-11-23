// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/version"
)

// TestRun prints the version and the usage, takes the argument an
// editor's client passes, and serves a session that shuts down and
// exits, with the exit code the protocol asks for.
func TestRun(t *testing.T) {
	var out, errs bytes.Buffer
	if code := run([]string{"-version"}, strings.NewReader(""), &out, &errs); code != 0 || out.String() != "snellols "+version.Current+"\n" {
		t.Errorf("-version: %d, %q", code, out.String())
	}

	out.Reset()
	if code := run([]string{"-x"}, strings.NewReader(""), &out, &errs); code != exitUsage || !strings.Contains(errs.String(), "usage: snellols") {
		t.Errorf("an argument it does not take: %d, %q", code, errs.String())
	}

	session := frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{}}}`) +
		frame(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`) +
		frame(`{"jsonrpc":"2.0","method":"exit"}`)
	out.Reset()
	if code := run([]string{"--stdio", "--clientProcessId", "4242"}, strings.NewReader(session), &out, &errs); code != 0 {
		t.Errorf("a session that shuts down and exits: code %d, want 0; wrote %q", code, out.String())
	}

	if !strings.Contains(out.String(), `"name":"snellols"`) || !strings.Contains(out.String(), `"id":2,"result":null`) {
		t.Errorf("the session's answers: %q", out.String())
	}

	if code := run([]string{"--clientProcessId=4242"}, strings.NewReader(frame(`{"jsonrpc":"2.0","method":"exit"}`)), &out, &errs); code != 1 {
		t.Errorf("an exit with no shutdown: code %d, want 1", code)
	}
}

func frame(body string) string {
	return "Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body
}
