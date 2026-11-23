// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/snello-lang/snello/compiler/scanner"
	"github.com/snello-lang/snello/lsp/jsonrpc"
	"github.com/snello-lang/snello/lsp/protocol"
)

const (
	// patience is how long a test waits for the server's next message
	// before it fails, far past a check's few milliseconds; a
	// line past every test's text, which a change to the whole of it
	// ends at; how many messages a test keeps before it gives up on the
	// one it waits for; and the permissions a test writes a file with.
	patience    = 30 * time.Second
	pastTheEnd  = 1000
	pendingMost = 100
	fileMode    = 0o644

	// The module the sessions edit: a program of two files and a library.
	mainValid = "package main\n\nimport (\"fmt\"; \"lib\")\n\nfunc main() {\n\tfmt.Println(greet(), lib.Name())\n}\n"
	otherSrc  = "package main\n\nfunc greet() string {\n\treturn \"hi\"\n}\n"
	libValid  = "package lib\n\n// Name is a name.\nfunc Name() string {\n\treturn \"lib\"\n}\n"

	mainSyntax    = "package main\n\nimport (\"fmt\"; \"lib\")\n\nfunc main() {\n\tfmt.Println(greet(), lib.Name()\n}\n"
	mainUndefined = "package main\n\nimport (\"fmt\"; \"lib\")\n\nfunc main() {\n\tfmt.Println(\"é😀\", missing, lib.Name())\n}\n"
	otherBroken   = "package main\n\nfunc greet() string {\n\treturn 1\n}\n"
	libBroken     = "package lib\n\n// Name is a name.\nfunc Name() string {\n\treturn 1\n}\n"
	savedBroken   = "package main\n\nfunc saved() int {\n\treturn \"no\"\n}\n"
)

// A session is a test's side of a server over pipes: it writes the
// editor's messages, and reads the server's on a goroutine of its own,
// keeping the diagnostics that arrive while it waits for an answer.
type session struct {
	t       *testing.T
	in      *io.PipeWriter
	writer  *jsonrpc.Writer
	from    chan *jsonrpc.Message
	done    chan int
	pending []*jsonrpc.Message
	id      int
}

// TestSession edits a module as a developer would, over the protocol:
// a syntax error typed into an unsaved buffer, then a type error after
// characters past ASCII, then the fix, each reported as the compiler
// reports it and cleared when fixed; an error in a file of the package
// not open is read from the disk and reported on that file; a library
// opened in the editor is checked as it stands there, its error on its
// file and on the import that reaches it; and closing it clears both.
func TestSession(t *testing.T) {
	dir := writeModule(t, map[string]string{"main.snello": mainValid, "other.snello": otherSrc, "lib/lib.snello": libValid})
	mainPath, otherPath, libPath := filepath.Join(dir, "main.snello"), filepath.Join(dir, "other.snello"), filepath.Join(dir, "lib", "lib.snello")
	c := startSession(t)
	result := c.initialize(nil)
	if result.Capabilities.PositionEncoding != protocol.PositionEncodingUTF16 || result.ServerInfo == nil || result.ServerInfo.Name != name {
		t.Errorf("the handshake's answer: %+v", result)
	}

	if sync := result.Capabilities.TextDocumentSync; sync == nil || !sync.OpenClose || sync.Change != protocol.TextDocumentSyncKindIncremental {
		t.Errorf("the document sync offered: %+v", sync)
	}

	mainURI := uriOf(mainPath)
	c.notify(protocol.MethodDidOpen, protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: mainURI, LanguageID: "snello", Version: 1, Text: mainSyntax}})
	expect(t, c.diagnostics(mainURI), mainSyntax, compilerErrorsOf(t, map[string]string{"main.snello": mainSyntax, "other.snello": otherSrc, "lib/lib.snello": libValid}, "main.snello"), protocol.PositionEncodingUTF16)

	c.change(mainURI, 2, mainUndefined)
	published := c.publication(mainURI)
	if published.Version == nil || *published.Version != 2 {
		t.Errorf("the version published: %v, want 2, the version checked", published.Version)
	}

	got := published.Diagnostics
	expect(t, got, mainUndefined, compilerErrorsOf(t, map[string]string{"main.snello": mainUndefined, "other.snello": otherSrc, "lib/lib.snello": libValid}, "main.snello"), protocol.PositionEncodingUTF16)
	if len(got) == 1 && (got[0].Range.Start.Character != 20 || got[0].Range.End.Character != 27) {
		t.Errorf("missing after é and an emoji: %+v, want characters 20 to 27 in UTF-16, é one unit and the emoji two", got[0].Range)
	}

	c.change(mainURI, 3, mainValid)
	if got := c.diagnostics(mainURI); len(got) != 0 {
		t.Errorf("after the fix: %+v, want none", got)
	}

	write(t, otherPath, otherBroken)
	c.change(mainURI, 4, mainValid)
	otherURI := uriOf(otherPath)
	expect(t, c.diagnostics(otherURI), otherBroken, compilerErrorsOf(t, map[string]string{"main.snello": mainValid, "other.snello": otherBroken, "lib/lib.snello": libValid}, "other.snello"), protocol.PositionEncodingUTF16)

	write(t, otherPath, otherSrc)
	c.change(mainURI, 5, mainValid)
	if got := c.diagnostics(otherURI); len(got) != 0 {
		t.Errorf("a file not open, fixed on the disk: %+v, want none", got)
	}

	savedPath := filepath.Join(dir, "saved.snello")
	write(t, savedPath, savedBroken) // a file new to the package, written as the editor saves it
	c.notify(protocol.MethodDidSave, protocol.DidSaveTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: mainURI}})
	if got := c.diagnostics(uriOf(savedPath)); len(got) != 1 || !strings.Contains(got[0].Message, "cannot use") {
		t.Errorf("a file the save brought into the package: %+v", got)
	}

	if err := os.Remove(savedPath); err != nil {
		t.Fatal(err)
	}

	c.notify(protocol.MethodDidSave, protocol.DidSaveTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: mainURI}})
	if got := c.diagnostics(uriOf(savedPath)); len(got) != 0 {
		t.Errorf("a file removed, after a save: %+v, want none", got)
	}

	libURI := uriOf(libPath)
	c.notify(protocol.MethodDidOpen, protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: libURI, LanguageID: "snello", Version: 1, Text: libBroken}})
	libDiags, mainDiags := c.diagnostics(libURI), c.diagnostics(mainURI)
	if len(libDiags) != 1 || libDiags[0].Range.Start.Line != 4 || !strings.Contains(libDiags[0].Message, "cannot use") {
		t.Errorf("the library as the editor holds it: %+v", libDiags)
	}

	if len(mainDiags) != 1 || mainDiags[0].Range.Start.Line != 2 || !strings.Contains(mainDiags[0].Message, "package lib does not compile") {
		t.Errorf("the import of the broken library: %+v", mainDiags)
	}

	c.notify(protocol.MethodDidClose, protocol.DidCloseTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: libURI}})
	if got := c.diagnostics(libURI); len(got) != 0 {
		t.Errorf("the library, closed: %+v, want none", got)
	}

	if got := c.diagnostics(mainURI); len(got) != 0 {
		t.Errorf("the program, with the library read from the disk again: %+v, want none", got)
	}

	if code := c.shutdown(); code != exitAfterShutdown {
		t.Errorf("exit after shutdown: code %d, want %d", code, exitAfterShutdown)
	}
}

// TestSessionUTF8 agrees on UTF-8 when the editor offers it, and counts
// a diagnostic's character in bytes.
func TestSessionUTF8(t *testing.T) {
	dir := writeModule(t, map[string]string{"main.snello": mainValid, "other.snello": otherSrc, "lib/lib.snello": libValid})
	c := startSession(t)
	result := c.initialize(&protocol.GeneralClientCapabilities{PositionEncodings: []string{protocol.PositionEncodingUTF8, protocol.PositionEncodingUTF16}})
	if result.Capabilities.PositionEncoding != protocol.PositionEncodingUTF8 {
		t.Errorf("offered UTF-8: %q", result.Capabilities.PositionEncoding)
	}

	uri := uriOf(filepath.Join(dir, "main.snello"))
	c.notify(protocol.MethodDidOpen, protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: uri, Version: 1, Text: mainUndefined}})
	got := c.diagnostics(uri)
	if len(got) != 1 || got[0].Range.Start.Character != 23 || got[0].Range.End.Character != 30 {
		t.Errorf("missing after é and an emoji: %+v, want characters 23 to 30 in UTF-8, é two bytes and the emoji four", got)
	}

	c.shutdown()
}

// TestSessionErrors answers what the protocol says a server answers: a
// request before the handshake, a second handshake, a method the server
// does not have, a message that is not JSON, and a request after the
// shutdown; a notification it does not know is passed over; an exit
// with no shutdown ends with 1, and so does the end of the stream.
func TestSessionErrors(t *testing.T) {
	c := startSession(t)
	if m := c.call(protocol.MethodShutdown, nil); m.Error == nil || m.Error.Code != jsonrpc.ServerNotInitialized {
		t.Errorf("a request before the handshake: %+v", m.Error)
	}

	c.initialize(nil)
	if m := c.call(protocol.MethodInitialize, protocol.InitializeParams{}); m.Error == nil || m.Error.Code != jsonrpc.InvalidRequest {
		t.Errorf("a second handshake: %+v", m.Error)
	}

	c.notify("$/cancelRequest", map[string]int{"id": 1})
	c.notify("workspace/didChangeConfiguration", map[string]any{})
	if m := c.call("textDocument/hover", map[string]any{}); m.Error == nil || m.Error.Code != jsonrpc.MethodNotFound {
		t.Errorf("a method the server does not have: %+v", m.Error)
	}

	if len(c.pending) != 0 {
		t.Errorf("notifications to pass over were answered: %+v", c.pending)
	}

	if _, err := c.in.Write([]byte("Content-Length: 9\r\n\r\nnot json!")); err != nil {
		t.Fatal(err)
	}

	if m := c.next(); m.Error == nil || m.Error.Code != jsonrpc.ParseError || string(m.ID) != "null" {
		t.Errorf("a message that is not JSON: %+v", m)
	}

	if m := c.call(protocol.MethodShutdown, nil); m.Error != nil {
		t.Errorf("the shutdown: %+v", m.Error)
	}

	if m := c.call("textDocument/definition", map[string]any{}); m.Error == nil || m.Error.Code != jsonrpc.InvalidRequest {
		t.Errorf("a request after the shutdown: %+v", m.Error)
	}

	c.notify(protocol.MethodExit, nil)
	if code := c.wait(); code != exitAfterShutdown {
		t.Errorf("exit after shutdown: %d", code)
	}

	abrupt := startSession(t)
	abrupt.initialize(nil)
	abrupt.notify(protocol.MethodExit, nil)
	if code := abrupt.wait(); code != exitAbruptly {
		t.Errorf("exit with no shutdown: %d, want %d", code, exitAbruptly)
	}

	closed := startSession(t)
	closed.initialize(nil)
	closed.in.Close()
	if code := closed.wait(); code != exitAbruptly {
		t.Errorf("the end of the stream: %d, want %d", code, exitAbruptly)
	}
}

// startSession starts a server over pipes.
func startSession(t *testing.T) *session {
	t.Helper()
	toServer, in := io.Pipe()
	out, fromServer := io.Pipe()
	c := &session{t: t, in: in, writer: jsonrpc.NewWriter(in), from: make(chan *jsonrpc.Message, 64), done: make(chan int, 1)}
	go func() {
		c.done <- New(toServer, fromServer, io.Discard).Run()
		fromServer.Close()
	}()

	go func() {
		r := jsonrpc.NewReader(out)
		for {
			m, err := r.Read()
			if err != nil {
				close(c.from)
				return
			}

			c.from <- m
		}
	}()

	t.Cleanup(func() { in.Close(); out.Close() })
	return c
}

// initialize performs the handshake, offering the position encodings
// general gives, and returns the server's answer.
func (c *session) initialize(general *protocol.GeneralClientCapabilities) protocol.InitializeResult {
	c.t.Helper()
	m := c.call(protocol.MethodInitialize, protocol.InitializeParams{Capabilities: protocol.ClientCapabilities{General: general}})
	if m.Error != nil {
		c.t.Fatalf("the handshake: %v", m.Error)
	}

	var result protocol.InitializeResult
	if err := json.Unmarshal(m.Result, &result); err != nil {
		c.t.Fatal(err)
	}

	c.notify(protocol.MethodInitialized, map[string]any{})
	return result
}

// change replaces the whole text of the document at uri, as one change
// of the range from its start to past its end.
func (c *session) change(uri protocol.DocumentURI, version int32, text string) {
	c.notify(protocol.MethodDidChange, protocol.DidChangeTextDocumentParams{
		TextDocument:   protocol.VersionedTextDocumentIdentifier{URI: uri, Version: version},
		ContentChanges: []protocol.TextDocumentContentChangeEvent{{Range: &protocol.Range{End: protocol.Position{Line: pastTheEnd}}, Text: text}},
	})
}

// notify sends a notification.
func (c *session) notify(method string, params any) {
	c.t.Helper()
	m, err := jsonrpc.Notification(method, params)
	if err != nil {
		c.t.Fatal(err)
	}

	if err := c.writer.Write(m); err != nil {
		c.t.Fatal(err)
	}
}

// call sends a request and returns its answer, keeping what arrives
// before it.
func (c *session) call(method string, params any) *jsonrpc.Message {
	c.t.Helper()
	c.id++
	id := json.RawMessage(strconv.Itoa(c.id))
	body, err := json.Marshal(params)
	if err != nil {
		c.t.Fatal(err)
	}

	if err := c.writer.Write(&jsonrpc.Message{JSONRPC: "2.0", ID: id, Method: method, Params: body}); err != nil {
		c.t.Fatal(err)
	}

	for {
		m := c.next()
		if !m.IsNotification() && string(m.ID) == string(id) {
			return m
		}

		c.pending = append(c.pending, m)
	}
}

// diagnostics returns the next diagnostics the server publishes for
// uri, from those kept or those to come.
func (c *session) diagnostics(uri protocol.DocumentURI) []protocol.Diagnostic {
	c.t.Helper()
	return c.publication(uri).Diagnostics
}

// publication returns the next publication of diagnostics for uri,
// with the version it names, from those kept or those to come.
func (c *session) publication(uri protocol.DocumentURI) protocol.PublishDiagnosticsParams {
	c.t.Helper()
	for {
		var m *jsonrpc.Message
		if len(c.pending) > 0 {
			m, c.pending = c.pending[0], c.pending[1:]
		} else {
			m = c.next()
		}

		if m.Method != protocol.MethodPublishDiagnostics {
			continue
		}

		var p protocol.PublishDiagnosticsParams
		if err := json.Unmarshal(m.Params, &p); err != nil {
			c.t.Fatal(err)
		}

		if p.URI == uri {
			return p
		}

		c.pending = append(c.pending, m) // another file's, which a later call may ask for
		if len(c.pending) > pendingMost {
			c.t.Fatalf("no diagnostics for %s among %d messages", uri, len(c.pending))
		}
	}
}

// next returns the server's next message.
func (c *session) next() *jsonrpc.Message {
	c.t.Helper()
	select {
	case m, ok := <-c.from:
		if !ok {
			c.t.Fatal("the server ended its stream")
		}

		return m
	case <-time.After(patience):
		c.t.Fatal("no message from the server")
	}

	return nil
}

// shutdown shuts the server down and has it exit, and returns its exit
// code.
func (c *session) shutdown() int {
	c.t.Helper()
	if m := c.call(protocol.MethodShutdown, nil); m.Error != nil || string(m.Result) != "null" {
		c.t.Errorf("the shutdown's answer: %+v", m)
	}

	c.notify(protocol.MethodExit, nil)
	return c.wait()
}

// wait returns the server's exit code once it has ended.
func (c *session) wait() int {
	c.t.Helper()
	select {
	case code := <-c.done:
		return code
	case <-time.After(patience):
		c.t.Fatal("the server did not end")
	}

	return -1
}

// expect checks diagnostics against the compiler's errors for the same
// text: the same messages, each starting where the compiler's position
// falls in the text, in enc.
func expect(t *testing.T, got []protocol.Diagnostic, text string, want scanner.ErrorList, enc string) {
	t.Helper()
	if len(want) == 0 || len(got) != len(want) {
		t.Fatalf("diagnostics %+v, want the compiler's %v", got, want)
	}

	for i, e := range want {
		start := positionOf([]byte(text), e.Pos.Offset, enc)
		if got[i].Message != e.Msg || got[i].Range.Start != start || got[i].Severity != protocol.SeverityError || got[i].Source != diagnosticSource {
			t.Errorf("diagnostic %d: %+v, want %q at %v", i, got[i], e.Msg, start)
		}
	}
}

// compilerErrorsOf writes the module files into a directory of its own
// and returns the errors snello check reports for it on the file named.
func compilerErrorsOf(t *testing.T, files map[string]string, file string) scanner.ErrorList {
	t.Helper()
	dir := writeModule(t, files)
	var on scanner.ErrorList
	for _, e := range compilerErrors(t, dir) {
		if filepath.Base(e.Pos.Filename) == file {
			on = append(on, e)
		}
	}

	return on
}

// write writes src to the file at path.
func write(t *testing.T, path, src string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(src), fileMode); err != nil {
		t.Fatal(err)
	}
}
