// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/token"
)

// Header writes the header the compiler gives a package that binds C
// (_docs/design/08-packages.md, section 6): the runtime's header, and
// the prototype of every bound function under its derived symbol, from
// the Snello signature. The package's C includes it, so the C compiler
// checks each definition against its declaration, as cgo's export
// header lets it.
func Header(b lower.Binding) []byte {
	e := newEmitter(nil, token.NewFile(b.Header, 0), nil)
	for _, fn := range b.Funcs {
		e.prototype(fn, token.NoPos)
	}

	var out bytes.Buffer
	put(&out, "// Written by the Snello compiler for package ", b.Path, ": the C side defines these under these names. Do not edit.\n\n#include \"snello.h\"\n\n")
	out.Write(e.decls.Bytes())
	out.Write(e.types.Bytes())
	out.Write(e.protos.Bytes())
	return out.Bytes()
}

// BindingSources writes the C side of every binding under dir, each
// in a directory of its own named by its symbol prefix, with the header
// Header writes beside its files, and returns the paths of the .c files
// to compile, in order. The .h files a package brings are copied too,
// for its .c files to include.
func BindingSources(dir string, bindings []lower.Binding) ([]string, error) {
	var srcs []string
	for _, b := range bindings {
		sub := filepath.Join(dir, b.Prefix)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			return nil, err
		}

		if err := os.WriteFile(filepath.Join(sub, b.Header), Header(b), 0o644); err != nil {
			return nil, err
		}

		for _, file := range b.Files {
			data, err := os.ReadFile(file)
			if err != nil {
				return nil, err
			}

			dst := filepath.Join(sub, filepath.Base(file))
			if err := os.WriteFile(dst, data, 0o644); err != nil {
				return nil, err
			}

			if filepath.Ext(dst) == ".c" {
				srcs = append(srcs, dst)
			}
		}
	}

	return srcs, nil
}
