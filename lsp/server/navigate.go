// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/snello-lang/snello/compiler/emit/c"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
	"github.com/snello-lang/snello/compiler/version"
	"github.com/snello-lang/snello/lsp/jsonrpc"
	"github.com/snello-lang/snello/lsp/protocol"
)

// stdCopies is the directory of the cache the standard library's
// sources are copied to, one directory for each version of the
// compiler, for an editor to open a definition in; the copies are
// written with copyDirMode and copyMode, read-only, since an edit to
// one changes nothing, and made writable, copyWritable, to be replaced.
// The fence opens and closes a Markdown block of Snello code, and
// markdownMarks are the characters Markdown reads as marks, which a doc
// comment's text escapes.
const (
	stdCopies     = "std"
	copyDirMode   = 0o755
	copyMode      = 0o444
	copyWritable  = 0o644
	fenceOpen     = "```snello\n"
	fence         = "\n```"
	markdownMarks = "\\`*_{}[]<>()#+-.!|~"
	markdownCode  = "\t"
	escape        = '\\'
)

// answer answers the requests about the documents that waited for the
// check, in the order they came.
func (s *Server) answer() {
	for _, m := range s.queries {
		s.query(m)
	}

	s.queries = nil
}

// query answers one request about a document, and turns a panic in it
// into the request's error, so that a fault on a check's half-typed
// results answers the request rather than ending the server.
func (s *Server) query(m *jsonrpc.Message) {
	defer func() {
		if r := recover(); r != nil {
			s.logf("%s panicked: %v", m.Method, r)
			s.fail(m.ID, jsonrpc.InternalError, fmt.Sprintf("the server failed on this request: %v", r))
		}
	}()

	switch m.Method {
	case protocol.MethodHover:
		s.hover(m)
	case protocol.MethodDefinition:
		s.definition(m)
	case protocol.MethodDocumentSymbol:
		s.documentSymbol(m)
	case protocol.MethodWorkspaceSymbol:
		s.workspaceSymbol(m)
	case protocol.MethodReferences:
		s.references(m)
	case protocol.MethodPrepareRename:
		s.prepareRename(m)
	case protocol.MethodRename:
		s.rename(m)
	case protocol.MethodCompletion:
		s.completion(m)
	}
}

// isQuery reports whether method is a request the server answers from
// its documents' checks, after the check of what changed before it.
func isQuery(method string) bool {
	switch method {
	case protocol.MethodHover, protocol.MethodDefinition, protocol.MethodDocumentSymbol, protocol.MethodWorkspaceSymbol,
		protocol.MethodReferences, protocol.MethodPrepareRename, protocol.MethodRename, protocol.MethodCompletion:
		return true
	}

	return false
}

// hover answers a hover: the declaration of the name under the cursor,
// as the checker types it, with its doc comment below it; or the type of
// the expression one of whose tokens is under it; or nothing.
func (s *Server) hover(m *jsonrpc.Message) {
	v, src, t, ok := s.targetOf(m)
	if !ok {
		return
	}

	if t == nil || t.obj == nil && t.typ == nil {
		s.reply(m.ID, nil)
		return
	}

	var text, doc string
	if t.obj != nil {
		text, doc = v.declaration(t.obj)
	} else {
		text = t.typ.String()
	}

	if text == "" {
		s.reply(m.ID, nil)
		return
	}

	if s.markup == protocol.MarkupKindMarkdown {
		text, doc = fenceOpen+text+fence, markdownOf(doc)
	}

	if doc != "" {
		text += "\n\n" + doc
	}

	r := protocol.Range{Start: positionOf(src, t.start, s.encoding), End: positionOf(src, t.end, s.encoding)}
	s.reply(m.ID, protocol.Hover{Contents: protocol.MarkupContent{Kind: s.markup, Value: text}, Range: &r})
}

// definition answers a definition: where the name under the cursor is
// declared, in its package's file or, for the standard library's, in
// its copy in the cache; or nothing, for a name the language declares
// or no name.
func (s *Server) definition(m *jsonrpc.Message) {
	v, _, t, ok := s.targetOf(m)
	if !ok {
		return
	}

	if t == nil || t.obj == nil {
		s.reply(m.ID, nil)
		return
	}

	loc, found, err := s.declarationOf(v, t.obj)
	switch {
	case err != nil:
		s.fail(m.ID, jsonrpc.InternalError, err.Error())
	case !found:
		s.reply(m.ID, nil)
	default:
		s.reply(m.ID, loc)
	}
}

// declarationOf returns where obj, an object of v, is declared: the
// token of its name, in its file or in the cache's copy of the
// standard library's; false for an object the language declares.
func (s *Server) declarationOf(v *view, obj types.Object) (protocol.Location, bool, error) {
	if !obj.Pos().IsValid() {
		return protocol.Location{}, false, nil
	}

	file := v.fileOf(obj.Pos())
	if file == nil {
		return protocol.Location{}, false, nil
	}

	src, err := v.source(file.Name())
	if err != nil {
		return protocol.Location{}, false, err
	}

	path, err := pathOfFile(file, src)
	if err != nil {
		return protocol.Location{}, false, err
	}

	start := file.Offset(obj.Pos())
	_, end, ok := tokenAt(file.Name(), src, start)
	if !ok {
		end = start
	}

	return protocol.Location{URI: s.uriOfPath(path), Range: protocol.Range{
		Start: positionOf(src, start, s.encoding),
		End:   positionOf(src, end, s.encoding),
	}}, true, nil
}

// targetOf reads a request about a place in a document, and returns the
// view of the document's last check, its text as the check read it and
// what the place holds, as place does; ok is false when the request is
// refused, as it is answered already.
func (s *Server) targetOf(m *jsonrpc.Message) (v *view, src []byte, t *target, ok bool) {
	var p protocol.TextDocumentPositionParams
	if err := json.Unmarshal(m.Params, &p); err != nil {
		s.fail(m.ID, jsonrpc.InvalidParams, err.Error())
		return nil, nil, nil, false
	}

	v, src, t = s.place(p)
	return v, src, t, true
}

// place returns the view of the last check of the document p names,
// its text as the check read it, and what the place p names holds; a
// nil target for nothing or a document no check has read.
func (s *Server) place(p protocol.TextDocumentPositionParams) (*view, []byte, *target) {
	path := pathOf(p.TextDocument.URI)
	v := s.views[path]
	if v == nil || v.byPath[path] == nil {
		return nil, nil, nil
	}

	file := v.byPath[path]
	src, err := v.source(file.Name())
	if err != nil {
		return nil, nil, nil
	}

	return v, src, v.at(file, src, offsetOf(src, p.Position, s.encoding))
}

// pathOfFile returns the path of a file a check read, src its text: its
// own, or for a file of the standard library, which the compiler holds
// and the disk does not, its copy in the cache, written when the cache
// has none of this version or one that differs.
func pathOfFile(file *token.File, src []byte) (string, error) {
	if filepath.IsAbs(file.Name()) {
		return file.Name(), nil
	}

	path := stdPathOf(file.Name())
	if path == "" {
		return "", errors.New("no cache to copy the standard library's " + file.Name() + " to: " + c.CacheEnv + " is " + c.CacheOff)
	}

	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, src) {
		return path, nil
	}

	return path, writeWhole(path, src)
}

// stdPathOf returns the path of the file a check names name: the name,
// a path, or for a file of the standard library, named by its path
// under std/, the path of its copy for this version of the compiler in
// the cache; "" for a library's file with the cache off.
func stdPathOf(name string) string {
	if filepath.IsAbs(name) {
		return name
	}

	cache, ok := c.CacheDir()
	if !ok || !fs.ValidPath(name) {
		return ""
	}

	return filepath.Join(cache, stdCopies, version.Current, filepath.FromSlash(name))
}

// stdCopyOf reports whether dir is a directory of the standard
// library's copies in the cache, and the import path of the package it
// is a copy of, "" for a copy of another version's.
func stdCopyOf(dir string) (path string, ok bool) {
	cache, ok := c.CacheDir()
	if !ok {
		return "", false
	}

	rel, err := filepath.Rel(filepath.Join(cache, stdCopies), dir)
	if err != nil || !filepath.IsLocal(rel) {
		return "", false
	}

	at, path, _ := strings.Cut(filepath.ToSlash(rel), "/")
	if at != version.Current {
		return "", true
	}

	return path, true
}

// markdownOf returns a doc comment's text as Markdown shows it as
// written: every mark Markdown reads escaped, but in a line indented
// as code, which Markdown shows as written.
func markdownOf(doc string) string {
	var b strings.Builder
	for line := range strings.SplitAfterSeq(doc, "\n") {
		if strings.HasPrefix(line, markdownCode) {
			b.WriteString(line)
			continue
		}

		for _, r := range line {
			if strings.ContainsRune(markdownMarks, r) {
				b.WriteRune(escape)
			}

			b.WriteRune(r)
		}
	}

	return b.String()
}

// writeWhole writes src to path through a file beside it that takes its
// place once written, so that no reader, another server's or an
// editor's, finds a file half written.
func writeWhole(path string, src []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), copyDirMode); err != nil {
		return err
	}

	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}

	_, err = f.Write(src)
	if err == nil {
		err = f.Chmod(copyMode)
	}

	if closeErr := f.Close(); err == nil {
		err = closeErr
	}

	if err == nil {
		os.Chmod(path, copyWritable) // a copy there, read-only, which a rename on Windows does not replace
		err = os.Rename(f.Name(), path)
	}

	if err != nil {
		os.Remove(f.Name())
	}

	return err
}

// isMarkup reports whether kind is a form the server writes a hover in.
func isMarkup(kind string) bool {
	return kind == protocol.MarkupKindMarkdown || kind == protocol.MarkupKindPlainText
}
