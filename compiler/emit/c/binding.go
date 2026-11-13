// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"bytes"
	"os"
	"path/filepath"
	goruntime "runtime"

	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/token"
)

const (
	linux       = "linux"      // the system whose links bindNow is for
	mathLibrary = "-lm"        // the C library's mathematics, a library of its own on Linux
	bindNow     = "-Wl,-z,now" // binds every import as the program starts
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
		if len(b.Files) == 0 {
			continue // a package with a link line and no C of its own
		}

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

// SystemLinkFlags returns what the link of every program ends with: the
// C library's mathematics, and on Linux the binding of every function
// the program imports as it starts, not at its first call, so that no
// call runs the dynamic linker's resolver on a task's stack, which the
// bound of the stack a task can use does not count. macOS binds every
// import as the program starts already.
func SystemLinkFlags() []string {
	if goruntime.GOOS == linux {
		return []string{mathLibrary, bindNow}
	}

	return []string{mathLibrary}
}

// LinkFlags returns the -l flags of the system libraries the bindings
// name, for the link of the program.
func LinkFlags(bindings []lower.Binding) []string {
	var flags []string
	for _, l := range lower.Links(bindings) {
		flags = append(flags, "-l"+l)
	}

	return flags
}
