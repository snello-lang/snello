// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package protocol

import (
	"encoding/json"
	"testing"
)

// TestEncode writes the types as the specification spells them: the
// server's capabilities, a diagnostic, diagnostics that clear a
// document, which are an empty list and not null, a hover and a
// location; the capabilities of symbols, references and rename, the
// last as true or as its options; an outline as a tree and as a list;
// a rename's edits and the answer that it may; completion's capability
// and its list, an empty one as an empty list.
func TestEncode(t *testing.T) {
	version := int32(3)
	tests := []struct {
		v    any
		want string
	}{
		{
			InitializeResult{
				Capabilities: ServerCapabilities{
					PositionEncoding: PositionEncodingUTF16,
					TextDocumentSync: &TextDocumentSyncOptions{OpenClose: true, Change: TextDocumentSyncKindIncremental, Save: &SaveOptions{}},
				},
				ServerInfo: &ServerInfo{Name: "snello", Version: "0.1.0"},
			},
			`{"capabilities":{"positionEncoding":"utf-16","textDocumentSync":{"openClose":true,"change":2,"save":{"includeText":false}}},"serverInfo":{"name":"snello","version":"0.1.0"}}`,
		},
		{
			PublishDiagnosticsParams{URI: "file:///a.snello", Version: &version, Diagnostics: []Diagnostic{{
				Range:    Range{Start: Position{Line: 1, Character: 4}, End: Position{Line: 1, Character: 7}},
				Severity: SeverityError,
				Source:   "snello",
				Message:  "undefined: x",
			}}},
			`{"uri":"file:///a.snello","version":3,"diagnostics":[{"range":{"start":{"line":1,"character":4},"end":{"line":1,"character":7}},"severity":1,"source":"snello","message":"undefined: x"}]}`,
		},
		{PublishDiagnosticsParams{URI: "file:///a.snello", Diagnostics: []Diagnostic{}}, `{"uri":"file:///a.snello","diagnostics":[]}`},
		{ServerCapabilities{HoverProvider: true, DefinitionProvider: true}, `{"hoverProvider":true,"definitionProvider":true}`},
		{
			Hover{Contents: MarkupContent{Kind: MarkupKindMarkdown, Value: "```snello\nvar n int\n```"}, Range: &Range{End: Position{Character: 1}}},
			`{"contents":{"kind":"markdown","value":"` + "```snello\\nvar n int\\n```" + `"},"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}}}`,
		},
		{Location{URI: "file:///a.snello", Range: Range{Start: Position{Line: 2}}}, `{"uri":"file:///a.snello","range":{"start":{"line":2,"character":0},"end":{"line":0,"character":0}}}`},
		{ServerCapabilities{ReferencesProvider: true, DocumentSymbolProvider: true, WorkspaceSymbolProvider: true, RenameProvider: RenameOptions{PrepareProvider: true}}, `{"referencesProvider":true,"documentSymbolProvider":true,"workspaceSymbolProvider":true,"renameProvider":{"prepareProvider":true}}`},
		{ServerCapabilities{RenameProvider: true}, `{"renameProvider":true}`},
		{
			DocumentSymbol{Name: "Kind", Kind: SymbolKindEnum, Children: []DocumentSymbol{{Name: "Round", Kind: SymbolKindEnumMember}}},
			`{"name":"Kind","kind":10,"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"selectionRange":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"children":[{"name":"Round","kind":22,"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"selectionRange":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}}}]}`,
		},
		{SymbolInformation{Name: "Round", Kind: SymbolKindEnumMember, Location: Location{URI: "file:///a.snello"}, ContainerName: "Kind"}, `{"name":"Round","kind":22,"location":{"uri":"file:///a.snello","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}}},"containerName":"Kind"}`},
		{WorkspaceEdit{Changes: map[DocumentURI][]TextEdit{"file:///a.snello": {{NewText: "Biggest"}}}}, `{"changes":{"file:///a.snello":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":"Biggest"}]}}`},
		{PrepareRenameResult{Placeholder: "Largest"}, `{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"placeholder":"Largest"}`},
		{ServerCapabilities{CompletionProvider: &CompletionOptions{TriggerCharacters: []string{".", "\""}}}, `{"completionProvider":{"triggerCharacters":[".","\""]}}`},
		{CompletionList{Items: []CompletionItem{{Label: "Area", Kind: CompletionItemKindMethod, Detail: "func (r ref Rect) Area() f64", SortText: "00000"}}}, `{"isIncomplete":false,"items":[{"label":"Area","kind":2,"detail":"func (r ref Rect) Area() f64","sortText":"00000"}]}`},
		{CompletionList{Items: []CompletionItem{}}, `{"isIncomplete":false,"items":[]}`},
	}

	for _, tt := range tests {
		got, err := json.Marshal(tt.v)
		if err != nil {
			t.Fatal(err)
		}

		if string(got) != tt.want {
			t.Errorf("%T:\n got %s\nwant %s", tt.v, got, tt.want)
		}
	}
}

// TestDecode reads what a client sends: the handshake with the position
// encodings it offers and the forms its hover renders, a change with a
// range and one without, a place a request asks about, the workspace's
// folders and the outline's and rename's capabilities, and the
// references and the rename asked for.
func TestDecode(t *testing.T) {
	var init InitializeParams
	if err := json.Unmarshal([]byte(`{"processId":12,"rootUri":null,"capabilities":{"general":{"positionEncodings":["utf-8","utf-16"]},"textDocument":{}}}`), &init); err != nil {
		t.Fatal(err)
	}

	if init.Capabilities.General == nil || len(init.Capabilities.General.PositionEncodings) != 2 || init.Capabilities.General.PositionEncodings[0] != PositionEncodingUTF8 {
		t.Errorf("the encodings offered: %+v", init.Capabilities.General)
	}

	init = InitializeParams{}
	if err := json.Unmarshal([]byte(`{"capabilities":{"textDocument":{"hover":{"dynamicRegistration":true,"contentFormat":["markdown","plaintext"]}}}}`), &init); err != nil {
		t.Fatal(err)
	}

	if d := init.Capabilities.TextDocument; d == nil || d.Hover == nil || len(d.Hover.ContentFormat) != 2 || d.Hover.ContentFormat[0] != MarkupKindMarkdown {
		t.Errorf("the hover's forms: %+v", init.Capabilities.TextDocument)
	}

	init = InitializeParams{}
	if err := json.Unmarshal([]byte(`{"rootUri":"file:///old","workspaceFolders":[{"uri":"file:///w","name":"w"}],"capabilities":{"textDocument":{"documentSymbol":{"hierarchicalDocumentSymbolSupport":true},"rename":{"prepareSupport":true}}}}`), &init); err != nil {
		t.Fatal(err)
	}

	if d := init.Capabilities.TextDocument; init.RootURI != "file:///old" || len(init.WorkspaceFolders) != 1 || init.WorkspaceFolders[0].URI != "file:///w" ||
		d == nil || d.DocumentSymbol == nil || !d.DocumentSymbol.HierarchicalDocumentSymbolSupport || d.Rename == nil || !d.Rename.PrepareSupport {
		t.Errorf("the workspace and the outline's and rename's capabilities: %+v", init)
	}

	var refs ReferenceParams
	if err := json.Unmarshal([]byte(`{"textDocument":{"uri":"file:///a.snello"},"position":{"line":1,"character":2},"context":{"includeDeclaration":true}}`), &refs); err != nil {
		t.Fatal(err)
	}

	if refs.TextDocument.URI != "file:///a.snello" || refs.Position.Character != 2 || !refs.Context.IncludeDeclaration {
		t.Errorf("the references asked for: %+v", refs)
	}

	var rename RenameParams
	if err := json.Unmarshal([]byte(`{"textDocument":{"uri":"file:///a.snello"},"position":{"line":1,"character":2},"newName":"Biggest"}`), &rename); err != nil {
		t.Fatal(err)
	}

	if rename.Position.Line != 1 || rename.NewName != "Biggest" {
		t.Errorf("the rename asked for: %+v", rename)
	}

	var at TextDocumentPositionParams
	if err := json.Unmarshal([]byte(`{"textDocument":{"uri":"file:///a.snello"},"position":{"line":3,"character":7}}`), &at); err != nil {
		t.Fatal(err)
	}

	if at.TextDocument.URI != "file:///a.snello" || at.Position != (Position{Line: 3, Character: 7}) {
		t.Errorf("the place: %+v", at)
	}

	var change DidChangeTextDocumentParams
	body := `{"textDocument":{"uri":"file:///a.snello","version":5},"contentChanges":[{"range":{"start":{"line":0,"character":2},"end":{"line":0,"character":3}},"rangeLength":1,"text":"x"},{"text":"whole"}]}`
	if err := json.Unmarshal([]byte(body), &change); err != nil {
		t.Fatal(err)
	}

	if change.TextDocument.Version != 5 || len(change.ContentChanges) != 2 || change.ContentChanges[0].Range == nil || change.ContentChanges[0].Range.Start.Character != 2 || change.ContentChanges[1].Range != nil || change.ContentChanges[1].Text != "whole" {
		t.Errorf("the change: %+v", change)
	}
}
