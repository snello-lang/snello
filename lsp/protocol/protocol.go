// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package protocol holds the types of the Language Server Protocol the
// server uses, and only those: the handshake, the document
// notifications and the diagnostics. Each type and field is named as
// the specification names it and encoded as its JSON spells it, so that
// a reader can hold the two side by side. It is modeled on the Language
// Server Protocol specification, 3.17.
package protocol

// The methods of the protocol the server answers or sends.
const (
	MethodInitialize         = "initialize"
	MethodInitialized        = "initialized"
	MethodShutdown           = "shutdown"
	MethodExit               = "exit"
	MethodDidOpen            = "textDocument/didOpen"
	MethodDidChange          = "textDocument/didChange"
	MethodDidSave            = "textDocument/didSave"
	MethodDidClose           = "textDocument/didClose"
	MethodPublishDiagnostics = "textDocument/publishDiagnostics"

	// The units a position's character counts in, which the client
	// offers and the server picks: UTF-16 code units, the protocol's
	// default, or UTF-8 bytes.
	PositionEncodingUTF16 = "utf-16"
	PositionEncodingUTF8  = "utf-8"
)

// A DocumentURI names a document, file:///home/me/a.snello for a file.
type DocumentURI string

// A Position is a place in a document: a line and a character, both
// from 0, the character counted in the units agreed at the handshake.
type Position struct {
	Line      uint32 `json:"line"`
	Character uint32 `json:"character"`
}

// A Range is the stretch of a document from Start up to End, which it
// does not include.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// InitializeParams is what the client says of itself at the handshake;
// the server reads its capabilities alone.
type InitializeParams struct {
	Capabilities ClientCapabilities `json:"capabilities"`
}

// ClientCapabilities is what the client can do; the server reads the
// position encodings it offers alone.
type ClientCapabilities struct {
	General *GeneralClientCapabilities `json:"general,omitempty"`
}

// GeneralClientCapabilities holds the position encodings the client
// offers, in its order of preference.
type GeneralClientCapabilities struct {
	PositionEncodings []string `json:"positionEncodings,omitempty"`
}

// InitializeResult is the server's answer to the handshake.
type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
	ServerInfo   *ServerInfo        `json:"serverInfo,omitempty"`
}

// ServerCapabilities is what the server does, and nothing it does not.
type ServerCapabilities struct {
	PositionEncoding string                   `json:"positionEncoding,omitempty"`
	TextDocumentSync *TextDocumentSyncOptions `json:"textDocumentSync,omitempty"`
}

// TextDocumentSyncOptions says which document notifications the server
// takes and how a change is sent.
type TextDocumentSyncOptions struct {
	OpenClose bool                 `json:"openClose"`
	Change    TextDocumentSyncKind `json:"change"`
	Save      *SaveOptions         `json:"save,omitempty"`
}

// TextDocumentSyncKind is how a change is sent: not at all, as the
// whole text, or as the ranges that changed.
type TextDocumentSyncKind int

const (
	TextDocumentSyncKindNone TextDocumentSyncKind = iota
	TextDocumentSyncKindFull
	TextDocumentSyncKindIncremental
)

// SaveOptions says whether a save sends the document's text.
type SaveOptions struct {
	IncludeText bool `json:"includeText"`
}

// ServerInfo names the server and its version.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// TextDocumentItem is a document as the client opens it, with its text.
type TextDocumentItem struct {
	URI        DocumentURI `json:"uri"`
	LanguageID string      `json:"languageId"`
	Version    int32       `json:"version"`
	Text       string      `json:"text"`
}

// TextDocumentIdentifier names a document.
type TextDocumentIdentifier struct {
	URI DocumentURI `json:"uri"`
}

// VersionedTextDocumentIdentifier names a document at a version.
type VersionedTextDocumentIdentifier struct {
	URI     DocumentURI `json:"uri"`
	Version int32       `json:"version"`
}

// DidOpenTextDocumentParams is a document the client opened.
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// DidChangeTextDocumentParams is a document the client changed: its
// changes, applied in their order, and its version after them.
type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

// TextDocumentContentChangeEvent is one change: the text that replaces
// the range, or, with no range, the whole document's new text.
type TextDocumentContentChangeEvent struct {
	Range *Range `json:"range,omitempty"`
	Text  string `json:"text"`
}

// DidSaveTextDocumentParams is a document the client saved.
type DidSaveTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// DidCloseTextDocumentParams is a document the client closed.
type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// PublishDiagnosticsParams is every diagnostic of a document, which
// replaces what the server published of it before: an empty list
// clears them.
type PublishDiagnosticsParams struct {
	URI         DocumentURI  `json:"uri"`
	Version     *int32       `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// A Diagnostic is one error or warning on a range of a document.
type Diagnostic struct {
	Range    Range              `json:"range"`
	Severity DiagnosticSeverity `json:"severity,omitempty"`
	Source   string             `json:"source,omitempty"`
	Message  string             `json:"message"`
}

// DiagnosticSeverity is how serious a diagnostic is.
type DiagnosticSeverity int

const (
	SeverityError DiagnosticSeverity = iota + 1
	SeverityWarning
	SeverityInformation
	SeverityHint
)
