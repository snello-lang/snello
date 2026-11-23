// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
)

// writes records each write made to it, to see that a message is one.
type writes struct {
	each []string
}

// TestRead reads messages framed as an editor frames them: one whole, a
// message read a byte at a time, two in one read, one with a content
// type, blank lines between messages, and the end of the stream between
// messages.
func TestRead(t *testing.T) {
	two := frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`) + frame(`{"jsonrpc":"2.0","method":"initialized","params":{}}`)
	tests := []struct {
		name    string
		in      io.Reader
		methods []string
	}{
		{"one", strings.NewReader(frame(`{"jsonrpc":"2.0","id":"a","method":"shutdown"}`)), []string{"shutdown"}},
		{"a byte at a time", iotest.OneByteReader(strings.NewReader(two)), []string{"initialize", "initialized"}},
		{"two in one read", strings.NewReader(two), []string{"initialize", "initialized"}},
		{"a content type", strings.NewReader("Content-Type: application/vscode-jsonrpc; charset=utf-8\r\n" + frame(`{"jsonrpc":"2.0","method":"exit"}`)), []string{"exit"}},
		{"a header in lower case", strings.NewReader("content-length: 33\r\n\r\n{\"jsonrpc\":\"2.0\",\"method\":\"exit\"}"), []string{"exit"}},
		{"blank lines between messages", strings.NewReader("\r\n" + frame(`{"jsonrpc":"2.0","method":"initialized"}`) + "\r\n\n" + frame(`{"jsonrpc":"2.0","method":"exit"}`) + "\r\n"), []string{"initialized", "exit"}},
	}

	for _, tt := range tests {
		r := NewReader(tt.in)
		for _, want := range tt.methods {
			m, err := r.Read()
			if err != nil {
				t.Fatalf("%s: %v, want %s", tt.name, err, want)
			}

			if m.Method != want {
				t.Errorf("%s: method %q, want %q", tt.name, m.Method, want)
			}
		}

		if m, err := r.Read(); err != io.EOF {
			t.Errorf("%s: after the last message: %v, %v, want io.EOF", tt.name, m, err)
		}
	}
}

// TestReadKinds tells a request, a notification and a response apart,
// and keeps a request's id as it was written, a number or a string.
func TestReadKinds(t *testing.T) {
	tests := []struct {
		body                  string
		request, notification bool
		id                    string
	}{
		{`{"jsonrpc":"2.0","id":7,"method":"shutdown"}`, true, false, `7`},
		{`{"jsonrpc":"2.0","id":"x-1","method":"shutdown"}`, true, false, `"x-1"`},
		{`{"jsonrpc":"2.0","method":"exit"}`, false, true, ``},
		{`{"jsonrpc":"2.0","id":3,"result":null}`, false, false, `3`},
	}

	for _, tt := range tests {
		m, err := NewReader(strings.NewReader(frame(tt.body))).Read()
		if err != nil {
			t.Fatalf("%s: %v", tt.body, err)
		}

		if m.IsRequest() != tt.request || m.IsNotification() != tt.notification || string(m.ID) != tt.id {
			t.Errorf("%s: request %t, notification %t, id %q; want %t, %t, %q", tt.body, m.IsRequest(), m.IsNotification(), m.ID, tt.request, tt.notification, tt.id)
		}
	}
}

// TestReadErrors refuses what is not the protocol's: a body that is not
// JSON, which the stream goes on after; a message of another version;
// a header line with no colon; no Content-Length; one that is not a
// number, or past the most a body may be; and a stream that ends in a
// header or in a body.
func TestReadErrors(t *testing.T) {
	tests := []struct {
		name, in string
		code     int // the *Error's code, or 0 for another error
		want     string
	}{
		{"not JSON", frame(`{"jsonrpc":`), ParseError, "not JSON"},
		{"another version", frame(`{"jsonrpc":"1.0","method":"exit"}`), InvalidRequest, "not JSON-RPC 2.0"},
		{"no colon", "Content-Length 3\r\n\r\n{}\n", 0, "no colon"},
		{"no length", "Content-Type: x\r\n\r\n{}", 0, "no Content-Length"},
		{"a length that is not a number", "Content-Length: three\r\n\r\n{}", 0, "not a length"},
		{"a length past the most", "Content-Length: 999999999999\r\n\r\n{}", 0, "not a length"},
		{"the end in a header", "Content-Length: 2\r\n", 0, io.ErrUnexpectedEOF.Error()},
		{"the end in a body", "Content-Length: 20\r\n\r\n{}", 0, io.ErrUnexpectedEOF.Error()},
	}

	for _, tt := range tests {
		_, err := NewReader(strings.NewReader(tt.in)).Read()
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v, want an error saying %q", tt.name, err, tt.want)
			continue
		}

		var e *Error
		if code := 0; errors.As(err, &e) {
			code = e.Code
			if code != tt.code {
				t.Errorf("%s: code %d, want %d", tt.name, code, tt.code)
			}
		} else if tt.code != 0 {
			t.Errorf("%s: %v, want an *Error with code %d", tt.name, err, tt.code)
		}
	}

	r := NewReader(strings.NewReader(frame(`not json`) + frame(`{"jsonrpc":"2.0","method":"exit"}`)))
	if _, err := r.Read(); err == nil {
		t.Fatal("a body that is not JSON was read")
	}

	if m, err := r.Read(); err != nil || m.Method != "exit" {
		t.Errorf("after a body that is not JSON: %v, %v, want the next message", m, err)
	}
}

// TestWrite frames each kind of message as a reader reads it back, in
// one write: a notification, a response with a result and with none,
// and an error response, whose missing id is written as null.
func TestWrite(t *testing.T) {
	note, err := Notification("textDocument/publishDiagnostics", map[string]any{"uri": "file:///a.snello"})
	if err != nil {
		t.Fatal(err)
	}

	answer, err := Response(json.RawMessage(`4`), map[string]int{"n": 1})
	if err != nil {
		t.Fatal(err)
	}

	empty, err := Response(json.RawMessage(`"s"`), nil)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		m    *Message
		body string
	}{
		{note, `{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":{"uri":"file:///a.snello"}}`},
		{answer, `{"jsonrpc":"2.0","id":4,"result":{"n":1}}`},
		{empty, `{"jsonrpc":"2.0","id":"s","result":null}`},
		{ErrorResponse(json.RawMessage(`9`), MethodNotFound, "no such method"), `{"jsonrpc":"2.0","id":9,"error":{"code":-32601,"message":"no such method"}}`},
		{ErrorResponse(nil, ParseError, "not JSON"), `{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"not JSON"}}`},
	}

	for _, tt := range tests {
		var out writes
		if err := NewWriter(&out).Write(tt.m); err != nil {
			t.Fatal(err)
		}

		if len(out.each) != 1 || out.each[0] != frame(tt.body) {
			t.Errorf("wrote %q, want %q in one write", out.each, frame(tt.body))
		}
	}
}

// TestError writes an error with its code.
func TestError(t *testing.T) {
	if got, want := (&Error{Code: InvalidParams, Message: "bad"}).Error(), "jsonrpc error -32602: bad"; got != want {
		t.Errorf("%q, want %q", got, want)
	}
}

// Write records p as one write.
func (w *writes) Write(p []byte) (int, error) {
	w.each = append(w.each, string(bytes.Clone(p)))
	return len(p), nil
}

// frame frames body as the protocol does.
func frame(body string) string {
	return "Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body
}
