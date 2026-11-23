// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"errors"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/snello-lang/snello/lsp/protocol"
)

// The scheme of a document that is a file; the host of a file URI that
// names this machine; what begins a path of a share on a network,
// //host/share on Windows; and the separator of a Windows drive from its
// path, file:///C:/dir/a.snello.
const (
	fileScheme     = "file"
	localHost      = "localhost"
	sharePrefix    = "//"
	driveSeparator = ':'
	windows        = "windows"
)

// A document is a file the editor has open, as the editor holds it: its
// URI, its path on disk, or "" for a document that is no file, its
// version and its text.
type document struct {
	uri     protocol.DocumentURI
	path    string
	version int32
	text    []byte
}

// apply applies changes to the document's text in their order, each
// range in the text as the changes before it left it, its positions
// counted in enc; a change with no range replaces the whole text. The
// changes apply whole or not at all: a range that ends before it starts
// leaves the text as it was.
func (d *document) apply(changes []protocol.TextDocumentContentChangeEvent, enc string) error {
	text := d.text
	for _, c := range changes {
		if c.Range == nil {
			text = []byte(c.Text)
			continue
		}

		start, end := offsetOf(text, c.Range.Start, enc), offsetOf(text, c.Range.End, enc)
		if end < start {
			return errors.New("a change whose range ends before it starts")
		}

		next := make([]byte, 0, len(text)-(end-start)+len(c.Text))
		text = append(append(append(next, text[:start]...), c.Text...), text[end:]...)
	}

	d.text = text
	return nil
}

// pathOf returns the path on disk of the file a URI names, or "" for a
// URI that names no file of this machine, an editor's unsaved buffer
// among them. On Windows a drive is written in a URI's path after a
// slash, /C:/dir, and loses it, and a URI with a host names a share on
// a network, //host/share; elsewhere a host but this machine's names no
// file here.
func pathOf(uri protocol.DocumentURI) string {
	u, err := url.Parse(string(uri))
	if err != nil || u.Scheme != fileScheme || u.Path == "" {
		return ""
	}

	p := u.Path
	if u.Host != "" && u.Host != localHost {
		if runtime.GOOS != windows {
			return ""
		}

		p = sharePrefix + u.Host + p
	} else if runtime.GOOS == windows && isDrivePath(p) {
		p = p[1:]
	}

	return filepath.Clean(filepath.FromSlash(p))
}

// uriOf returns the URI of the file at path, an absolute path, each
// element escaped as a URI's path is: a Windows share, //host/share,
// with its host as the URI's.
func uriOf(path string) protocol.DocumentURI {
	p := filepath.ToSlash(path)
	host := ""
	if rest, ok := strings.CutPrefix(p, sharePrefix); ok {
		host, p, _ = strings.Cut(rest, "/")
		p = "/" + p
	} else if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}

	return protocol.DocumentURI((&url.URL{Scheme: fileScheme, Host: host, Path: p}).String())
}

// isDrivePath reports whether a URI's path begins with a Windows drive
// after its slash, /C:/dir.
func isDrivePath(p string) bool {
	return len(p) >= 3 && p[0] == '/' && p[2] == driveSeparator && (p[1] >= 'a' && p[1] <= 'z' || p[1] >= 'A' && p[1] <= 'Z')
}
