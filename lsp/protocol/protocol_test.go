// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package protocol

import (
	"encoding/json"
	"testing"
)

// TestEncode writes the types as the specification spells them: the
// server's capabilities, a diagnostic, and diagnostics that clear a
// document, which are an empty list and not null.
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
				ServerInfo: &ServerInfo{Name: "snellols", Version: "0.1.0"},
			},
			`{"capabilities":{"positionEncoding":"utf-16","textDocumentSync":{"openClose":true,"change":2,"save":{"includeText":false}}},"serverInfo":{"name":"snellols","version":"0.1.0"}}`,
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
// encodings it offers, and a change with a range and one without.
func TestDecode(t *testing.T) {
	var init InitializeParams
	if err := json.Unmarshal([]byte(`{"processId":12,"rootUri":null,"capabilities":{"general":{"positionEncodings":["utf-8","utf-16"]},"textDocument":{}}}`), &init); err != nil {
		t.Fatal(err)
	}

	if init.Capabilities.General == nil || len(init.Capabilities.General.PositionEncodings) != 2 || init.Capabilities.General.PositionEncodings[0] != PositionEncodingUTF8 {
		t.Errorf("the encodings offered: %+v", init.Capabilities.General)
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
