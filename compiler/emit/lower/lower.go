// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package lower holds the decisions every backend makes the same way,
// as _docs/design/07-lowering.md records them: what a backend takes
// in, which library functions the compiler writes in place of a body,
// and in time the names of symbols and the predicates over types that
// say what a value needs when it is dropped. A backend walks the
// checked syntax tree on its own and writes its own text; what it
// must agree on with the others lives here, so that the differential
// test between backends compares two walks of one set of rules.
package lower

import (
	"errors"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// A Library is a package written in Snello that the program imports, as
// the importer loaded it: its import path, its source file and its
// syntax, checked into the program's Info. It is compiled with the
// program, into the same output, its names qualified by its import path.
type Library struct {
	Path   string
	File   *token.File
	Syntax *ast.File
}

// CheckPrefixes refuses two libraries whose import paths would share
// one symbol prefix, example.com/a.b and example.com/a_b, since their
// names could then collide in the output; no real program meets it
// (_docs/design/08-packages.md).
func CheckPrefixes(libraries []Library) error {
	paths := make(map[string]string, len(libraries))
	for _, lib := range libraries {
		prefix := PathPrefix(lib.Path)
		if other, ok := paths[prefix]; ok && other != lib.Path {
			return errors.New("import paths " + other + " and " + lib.Path + " would share one symbol prefix")
		}

		paths[prefix] = lib.Path
	}

	return nil
}

// lowered names the library functions the compiler writes, by import
// path and name: fmt's printing is written by the type of each
// argument, with the format read at compile time, so the runtime has
// no formatter; os.Exit is the runtime's exit, since a function cannot
// say it never returns yet; and the primitives of rc and sync are the
// pointer itself, since a second owner of one pointer, and a mut
// reached through a ref, are what the language refuses and the
// reference count and the lock are what make them safe there.
var lowered = map[string]bool{
	"fmt.Println": true, "fmt.Printf": true, "fmt.Sprint": true, "fmt.Sprintf": true,
	"fmt.Fprintln": true, "fmt.Fprintf": true, "os.Exit": true,
	"rc.alias": true, "rc.leak": true, "sync.alias": true, "sync.leak": true, "sync.inside": true,
}

// Key returns the key of fn among the lowered functions, path.Name, or
// the empty string for a function the compiler does not write.
func Key(fn *types.Func) string {
	if fn.Pkg() == nil {
		return ""
	}

	if key := fn.Pkg().Path() + "." + fn.Name(); lowered[key] {
		return key
	}

	return ""
}
