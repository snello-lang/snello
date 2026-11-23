// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package server is Snello's language server: it answers an editor's
// messages over a stream, keeps the documents the editor has open, and
// checks their packages through the compiler's own loader and checker,
// publishing every error as a diagnostic on the file it names; it
// answers hover and definition from what the last check left, outlines
// and the workspace's symbols from the syntax, and references and
// rename from checks of the packages that can name the object. One
// goroutine reads the stream; another owns the documents and the
// results and does all the rest, so that nothing the server keeps is
// shared. It is modeled on the Language Server Protocol specification,
// 3.17, and _docs/design/15-language-server.md has its design.
package server

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"

	"github.com/snello-lang/snello/compiler/version"
	"github.com/snello-lang/snello/lsp/jsonrpc"
	"github.com/snello-lang/snello/lsp/protocol"
)

// name is the server's name, as the handshake gives it; the exit codes
// are a session's that ends after a shutdown and without one; and
// incomingPending is how many messages the reading goroutine may read
// ahead of the one handling them.
const (
	name              = "snellols"
	exitAfterShutdown = 0
	exitAbruptly      = 1
	incomingPending   = 64
)

// A Server is the state of one editor's session: the documents it has
// open, by URI and by path, the reports of the last checks, by the
// package's directory, the views they left, by the path of each file
// they checked, the diagnostics it last published, by path, and the
// requests about the documents waiting for the check of what changed.
type Server struct {
	in   *jsonrpc.Reader
	out  *jsonrpc.Writer
	logs io.Writer

	encoding        string
	markup          string
	hierarchical    bool
	prepares        bool
	documentChanges bool
	folders         []string
	initialized     bool
	shutdown        bool
	changed         bool

	docs      map[protocol.DocumentURI]*document
	byPath    map[string]*document
	reports   map[string][]report
	views     map[string]*view
	published map[string][]protocol.Diagnostic
	queries   []*jsonrpc.Message
	outlines  map[string]*outlined
}

// An incoming is a message the reading goroutine read, or the error
// that ended its reading.
type incoming struct {
	m   *jsonrpc.Message
	err error
}

// New returns a server that reads the editor's messages from in,
// writes its own to out, and writes what it has to say of itself, a
// check that panicked or a message it could not read, to logs.
func New(in io.Reader, out io.Writer, logs io.Writer) *Server {
	return &Server{
		in:        jsonrpc.NewReader(in),
		out:       jsonrpc.NewWriter(out),
		logs:      logs,
		encoding:  protocol.PositionEncodingUTF16,
		markup:    protocol.MarkupKindPlainText,
		docs:      make(map[protocol.DocumentURI]*document),
		byPath:    make(map[string]*document),
		reports:   make(map[string][]report),
		views:     make(map[string]*view),
		published: make(map[string][]protocol.Diagnostic),
	}
}

// Run serves the session until the editor sends exit or closes the
// stream, and returns the exit code the protocol asks for: 0 after a
// shutdown, 1 without one. Messages that arrive while a check runs are
// handled together after it, and every package with an open document is
// checked once after them, so that a burst of typing costs one check at
// its end; a request about a document is answered after that check, on
// the latest text, never on a check of text that came before it. The
// reading goroutine stops once the session is over, at its next message.
func (s *Server) Run() int {
	messages := make(chan incoming, incomingPending)
	over := make(chan struct{})
	defer close(over)
	go func() {
		for {
			m, err := s.in.Read()
			var bad *jsonrpc.Error
			select {
			case messages <- incoming{m, err}:
			case <-over:
				return
			}

			if err != nil && !errors.As(err, &bad) {
				return
			}
		}
	}()

	for {
		if done := s.receive(<-messages); done {
			return s.exitCode()
		}

		for drained := false; !drained; {
			select {
			case next := <-messages:
				if done := s.receive(next); done {
					return s.exitCode()
				}
			default:
				drained = true
			}
		}

		if s.changed {
			s.changed = false
			s.checkAll()
			s.publishAll()
		}

		s.answer()
	}
}

// receive handles one message the reading goroutine read, and reports
// whether the session is over: an exit, or a stream that ended or
// broke.
func (s *Server) receive(in incoming) bool {
	var bad *jsonrpc.Error
	switch {
	case errors.As(in.err, &bad):
		s.logf("a message that could not be read: %v", bad)
		s.write(jsonrpc.ErrorResponse(nil, bad.Code, bad.Message))
		return false
	case errors.Is(in.err, io.EOF):
		return true
	case in.err != nil:
		s.logf("the stream broke: %v", in.err)
		return true
	}

	return s.handle(in.m)
}

// handle answers a request or acts on a notification, and reports
// whether it was the exit. A response, which this server never asks
// for, is passed over.
func (s *Server) handle(m *jsonrpc.Message) bool {
	switch {
	case m.IsRequest():
		s.request(m)
	case m.IsNotification():
		return s.notification(m)
	}

	return false
}

// request answers a request: the handshake, the shutdown, a request
// about a document, which waits for the check of what changed before
// it, or the protocol's error for a request before the handshake, after
// the shutdown, or of a method the server does not have.
func (s *Server) request(m *jsonrpc.Message) {
	switch {
	case m.Method == protocol.MethodInitialize && s.initialized:
		s.fail(m.ID, jsonrpc.InvalidRequest, "the server is initialized already")
	case m.Method == protocol.MethodInitialize:
		s.initialize(m)
	case !s.initialized:
		s.fail(m.ID, jsonrpc.ServerNotInitialized, "the server is not initialized")
	case s.shutdown:
		s.fail(m.ID, jsonrpc.InvalidRequest, "the server is shut down")
	case m.Method == protocol.MethodShutdown:
		s.shutdown = true
		s.reply(m.ID, nil)
	case isQuery(m.Method):
		s.queries = append(s.queries, m)
	default:
		s.fail(m.ID, jsonrpc.MethodNotFound, "no method "+m.Method)
	}
}

// notification acts on a notification, and reports whether it was the
// exit. One the server does not know, and any before the handshake or
// after the shutdown but the exit, is passed over, as the protocol
// allows.
func (s *Server) notification(m *jsonrpc.Message) bool {
	if m.Method == protocol.MethodExit {
		return true
	}

	if !s.initialized || s.shutdown {
		return false
	}

	var err error
	switch m.Method {
	case protocol.MethodDidOpen:
		var p protocol.DidOpenTextDocumentParams
		if err = json.Unmarshal(m.Params, &p); err == nil {
			s.open(p.TextDocument)
		}
	case protocol.MethodDidChange:
		var p protocol.DidChangeTextDocumentParams
		if err = json.Unmarshal(m.Params, &p); err == nil {
			err = s.change(p)
		}
	case protocol.MethodDidSave:
		s.changed = true // a file saved may join its package, and a package another imports is read from the disk
	case protocol.MethodDidClose:
		var p protocol.DidCloseTextDocumentParams
		if err = json.Unmarshal(m.Params, &p); err == nil {
			s.close(p.TextDocument.URI)
		}
	}

	if err != nil {
		s.logf("%s: %v", m.Method, err)
	}

	return false
}

// initialize answers the handshake with the capabilities of what the
// server does, the position encoding it picked among those the editor
// offers, and its name and version; it writes a hover in the first form
// the editor renders that it writes, plain text when the editor names
// none.
func (s *Server) initialize(m *jsonrpc.Message) {
	var p protocol.InitializeParams
	if len(m.Params) > 0 {
		if err := json.Unmarshal(m.Params, &p); err != nil {
			s.fail(m.ID, jsonrpc.InvalidParams, err.Error())
			return
		}
	}

	if g := p.Capabilities.General; g != nil && slices.Contains(g.PositionEncodings, protocol.PositionEncodingUTF8) {
		s.encoding = protocol.PositionEncodingUTF8
	}

	if d := p.Capabilities.TextDocument; d != nil {
		if d.Hover != nil {
			if i := slices.IndexFunc(d.Hover.ContentFormat, isMarkup); i >= 0 {
				s.markup = d.Hover.ContentFormat[i]
			}
		}

		s.hierarchical = d.DocumentSymbol != nil && d.DocumentSymbol.HierarchicalDocumentSymbolSupport
		s.prepares = d.Rename != nil && d.Rename.PrepareSupport
	}

	if w := p.Capabilities.Workspace; w != nil && w.WorkspaceEdit != nil {
		s.documentChanges = w.WorkspaceEdit.DocumentChanges
	}

	for _, f := range p.WorkspaceFolders {
		s.folders = append(s.folders, pathOf(f.URI))
	}

	if len(p.WorkspaceFolders) == 0 && p.RootURI != "" {
		s.folders = append(s.folders, pathOf(p.RootURI))
	}

	var rename any = true
	if s.prepares {
		rename = protocol.RenameOptions{PrepareProvider: true}
	}

	s.initialized = true
	s.reply(m.ID, protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			PositionEncoding:        s.encoding,
			TextDocumentSync:        &protocol.TextDocumentSyncOptions{OpenClose: true, Change: protocol.TextDocumentSyncKindIncremental, Save: &protocol.SaveOptions{}},
			HoverProvider:           true,
			DefinitionProvider:      true,
			ReferencesProvider:      true,
			DocumentSymbolProvider:  true,
			WorkspaceSymbolProvider: true,
			RenameProvider:          rename,
		},
		ServerInfo: &protocol.ServerInfo{Name: name, Version: version.Current},
	})
}

// open keeps a document the editor opened, as its text and version.
func (s *Server) open(item protocol.TextDocumentItem) {
	s.close(item.URI) // an open of a document open already starts it again
	d := &document{uri: item.URI, path: pathOf(item.URI), version: item.Version, text: []byte(item.Text)}
	s.docs[d.uri] = d
	if d.path != "" {
		s.byPath[d.path] = d
	}

	s.changed = true
}

// change applies a document's changes and takes its new version.
func (s *Server) change(p protocol.DidChangeTextDocumentParams) error {
	d := s.docs[p.TextDocument.URI]
	if d == nil {
		return errors.New("a change to " + string(p.TextDocument.URI) + ", which is not open")
	}

	if err := d.apply(p.ContentChanges, s.encoding); err != nil {
		return err
	}

	d.version = p.TextDocument.Version
	s.changed = true
	return nil
}

// close forgets a document the editor closed; its file is read from
// the disk from now on.
func (s *Server) close(uri protocol.DocumentURI) {
	d := s.docs[uri]
	if d == nil {
		return
	}

	delete(s.docs, uri)
	if s.byPath[d.path] == d {
		delete(s.byPath, d.path)
	}

	s.changed = true
}

// publishAll publishes the diagnostics of every file whose diagnostics,
// the union of every report's, differ from what was last published of
// it: an empty list for a file no report names any more.
func (s *Server) publishAll() {
	now := make(map[string][]protocol.Diagnostic)
	for _, dir := range slices.Sorted(maps.Keys(s.reports)) {
		for _, r := range s.reports[dir] {
			for path, diags := range r {
				for _, d := range diags {
					if !slices.Contains(now[path], d) {
						now[path] = append(now[path], d)
					}
				}
			}
		}
	}

	paths := slices.Collect(maps.Keys(now))
	for path := range s.published {
		if _, ok := now[path]; !ok {
			paths = append(paths, path)
		}
	}

	slices.Sort(paths)
	for _, path := range paths {
		diags, ok := now[path]
		if !ok {
			s.publish(path, []protocol.Diagnostic{})
			delete(s.published, path)
			continue
		}

		slices.SortStableFunc(diags, compareDiagnostics)
		if !slices.Equal(diags, s.published[path]) {
			s.publish(path, diags)
			s.published[path] = diags
		}
	}
}

// publish sends the diagnostics of the file at path, under the URI the
// editor opened it by, or its own when it is not open, with the version
// checked when it is.
func (s *Server) publish(path string, diags []protocol.Diagnostic) {
	p := protocol.PublishDiagnosticsParams{URI: uriOf(path), Diagnostics: diags}
	if d := s.byPath[path]; d != nil {
		version := d.version
		p.URI, p.Version = d.uri, &version
	}

	m, err := jsonrpc.Notification(protocol.MethodPublishDiagnostics, p)
	if err != nil {
		s.logf("publishing %s: %v", path, err)
		return
	}

	s.write(m)
}

// reply answers the request whose id is id with result.
func (s *Server) reply(id json.RawMessage, result any) {
	m, err := jsonrpc.Response(id, result)
	if err != nil {
		s.fail(id, jsonrpc.InternalError, err.Error())
		return
	}

	s.write(m)
}

// fail answers the request whose id is id with an error.
func (s *Server) fail(id json.RawMessage, code int, message string) {
	s.write(jsonrpc.ErrorResponse(id, code, message))
}

// write writes a message to the editor; one that cannot be written is
// logged, since the stream is the only way to say so.
func (s *Server) write(m *jsonrpc.Message) {
	if err := s.out.Write(m); err != nil {
		s.logf("writing a message: %v", err)
	}
}

// exitCode is the code the session ends with: 0 after a shutdown, 1
// without one.
func (s *Server) exitCode() int {
	if s.shutdown {
		return exitAfterShutdown
	}

	return exitAbruptly
}

// logf writes a line about the server itself to its logs.
func (s *Server) logf(format string, args ...any) {
	fmt.Fprintf(s.logs, name+": "+format+"\n", args...)
}

// compareDiagnostics orders diagnostics by where they start, and then
// by message, so that a file's list is the same however its checks ran.
func compareDiagnostics(a, b protocol.Diagnostic) int {
	return cmp.Or(
		cmp.Compare(a.Range.Start.Line, b.Range.Start.Line),
		cmp.Compare(a.Range.Start.Character, b.Range.Start.Character),
		cmp.Compare(a.Message, b.Message),
	)
}
