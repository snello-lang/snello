// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package protocol holds the types of the Language Server Protocol the
// server uses, and only those: the handshake, the document
// notifications, the diagnostics, hover and definition, symbols,
// references and rename. Each type and field is named as
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
	MethodHover              = "textDocument/hover"
	MethodDefinition         = "textDocument/definition"
	MethodDocumentSymbol     = "textDocument/documentSymbol"
	MethodWorkspaceSymbol    = "workspace/symbol"
	MethodReferences         = "textDocument/references"
	MethodPrepareRename      = "textDocument/prepareRename"
	MethodRename             = "textDocument/rename"

	// The units a position's character counts in, which the client
	// offers and the server picks: UTF-16 code units, the protocol's
	// default, or UTF-8 bytes.
	PositionEncodingUTF16 = "utf-16"
	PositionEncodingUTF8  = "utf-8"

	// The forms a hover's text is written in, which the client says it
	// renders: Markdown, or plain text, which every client renders.
	MarkupKindMarkdown  = "markdown"
	MarkupKindPlainText = "plaintext"
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

// InitializeParams is what the client says of itself at the handshake:
// its capabilities, and the folders it has open, the workspace, as the
// folders of 3.6 and on or the one root of before.
type InitializeParams struct {
	Capabilities     ClientCapabilities `json:"capabilities"`
	RootURI          DocumentURI        `json:"rootUri,omitempty"`
	WorkspaceFolders []WorkspaceFolder  `json:"workspaceFolders,omitempty"`
}

// A WorkspaceFolder is a folder the client has open.
type WorkspaceFolder struct {
	URI  DocumentURI `json:"uri"`
	Name string      `json:"name"`
}

// ClientCapabilities is what the client can do; the server reads the
// position encodings it offers, the forms its hover renders, and what
// it can do with documents and the workspace.
type ClientCapabilities struct {
	General      *GeneralClientCapabilities      `json:"general,omitempty"`
	TextDocument *TextDocumentClientCapabilities `json:"textDocument,omitempty"`
	Workspace    *WorkspaceClientCapabilities    `json:"workspace,omitempty"`
}

// WorkspaceClientCapabilities is what the client can do with the
// workspace; the server reads the form of the edits it applies.
type WorkspaceClientCapabilities struct {
	WorkspaceEdit *WorkspaceEditClientCapabilities `json:"workspaceEdit,omitempty"`
}

// WorkspaceEditClientCapabilities says whether the client takes a
// workspace's edits as documents' edits, each naming the version of the
// document it was made for, which a client refuses to apply to another.
type WorkspaceEditClientCapabilities struct {
	DocumentChanges bool `json:"documentChanges,omitempty"`
}

// TextDocumentClientCapabilities is what the client can do with a
// document; the server reads its hover's, its outline's and its
// rename's.
type TextDocumentClientCapabilities struct {
	Hover          *HoverClientCapabilities          `json:"hover,omitempty"`
	DocumentSymbol *DocumentSymbolClientCapabilities `json:"documentSymbol,omitempty"`
	Rename         *RenameClientCapabilities         `json:"rename,omitempty"`
}

// DocumentSymbolClientCapabilities says whether the client shows an
// outline as a tree, of DocumentSymbol, or as a list, of
// SymbolInformation.
type DocumentSymbolClientCapabilities struct {
	HierarchicalDocumentSymbolSupport bool `json:"hierarchicalDocumentSymbolSupport,omitempty"`
}

// RenameClientCapabilities says whether the client asks whether a name
// may be renamed before it asks for the new name.
type RenameClientCapabilities struct {
	PrepareSupport bool `json:"prepareSupport,omitempty"`
}

// HoverClientCapabilities holds the forms the client renders a hover's
// text in, in its order of preference.
type HoverClientCapabilities struct {
	ContentFormat []string `json:"contentFormat,omitempty"`
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
	PositionEncoding   string                   `json:"positionEncoding,omitempty"`
	TextDocumentSync   *TextDocumentSyncOptions `json:"textDocumentSync,omitempty"`
	HoverProvider      bool                     `json:"hoverProvider,omitempty"`
	DefinitionProvider bool                     `json:"definitionProvider,omitempty"`
	ReferencesProvider bool                     `json:"referencesProvider,omitempty"`

	DocumentSymbolProvider  bool `json:"documentSymbolProvider,omitempty"`
	WorkspaceSymbolProvider bool `json:"workspaceSymbolProvider,omitempty"`

	// RenameProvider is true, or RenameOptions for a client that asks
	// before it renames.
	RenameProvider any `json:"renameProvider,omitempty"`
}

// RenameOptions says the server answers prepareRename.
type RenameOptions struct {
	PrepareProvider bool `json:"prepareProvider"`
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

// TextDocumentPositionParams is a place in a document a request asks
// about: what hover and definition are asked with.
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// Hover is the answer to a hover: its text, and the stretch of the
// document it is about.
type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

// MarkupContent is text in one of the forms of MarkupKind.
type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// A Location is a stretch of a document: where a definition is.
type Location struct {
	URI   DocumentURI `json:"uri"`
	Range Range       `json:"range"`
}

// DocumentSymbolParams names the document whose outline is asked for.
type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// A SymbolKind is what a symbol is, as an editor marks it.
type SymbolKind int

// The kinds of symbol the server gives, by the protocol's numbers.
const (
	SymbolKindClass      SymbolKind = 5
	SymbolKindMethod     SymbolKind = 6
	SymbolKindField      SymbolKind = 8
	SymbolKindEnum       SymbolKind = 10
	SymbolKindInterface  SymbolKind = 11
	SymbolKindFunction   SymbolKind = 12
	SymbolKindVariable   SymbolKind = 13
	SymbolKindConstant   SymbolKind = 14
	SymbolKindEnumMember SymbolKind = 22
	SymbolKindStruct     SymbolKind = 23
)

// A DocumentSymbol is a declaration of a document's outline: its name,
// kind, the stretch of its whole declaration and that of its name, and
// the declarations under it.
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           SymbolKind       `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

// SymbolInformation is a declaration as a list of symbols gives it: its
// name, kind and place, and the name of what it is declared in.
type SymbolInformation struct {
	Name          string     `json:"name"`
	Kind          SymbolKind `json:"kind"`
	Location      Location   `json:"location"`
	ContainerName string     `json:"containerName,omitempty"`
}

// WorkspaceSymbolParams is the text the symbols of the workspace are
// looked for by.
type WorkspaceSymbolParams struct {
	Query string `json:"query"`
}

// ReferenceParams is a place whose name's references are asked for,
// and whether its declaration is among them.
type ReferenceParams struct {
	TextDocumentPositionParams
	Context ReferenceContext `json:"context"`
}

// ReferenceContext says whether the declaration is a reference.
type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

// RenameParams is a place whose name is renamed, and the new name.
type RenameParams struct {
	TextDocumentPositionParams
	NewName string `json:"newName"`
}

// PrepareRenameResult is the stretch of the name a rename would change,
// and the name the editor offers to change.
type PrepareRenameResult struct {
	Range       Range  `json:"range"`
	Placeholder string `json:"placeholder"`
}

// A WorkspaceEdit is the edits of a rename, each document's applied at
// once: by document, or as documents' edits with their versions.
type WorkspaceEdit struct {
	Changes         map[DocumentURI][]TextEdit `json:"changes,omitempty"`
	DocumentChanges []TextDocumentEdit         `json:"documentChanges,omitempty"`
}

// A TextDocumentEdit is the edits of one document, made for the version
// it names, or for the file on the disk when it names none.
type TextDocumentEdit struct {
	TextDocument OptionalVersionedTextDocumentIdentifier `json:"textDocument"`
	Edits        []TextEdit                              `json:"edits"`
}

// OptionalVersionedTextDocumentIdentifier names a document and the
// version an edit was made for, null for a document the editor does
// not have open.
type OptionalVersionedTextDocumentIdentifier struct {
	URI     DocumentURI `json:"uri"`
	Version *int32      `json:"version"`
}

// A TextEdit replaces a stretch of a document with text.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}
