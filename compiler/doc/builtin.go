// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package doc

import (
	"sync"

	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/std"
)

// BuiltinPath is the name snello doc gives the predeclared names, and
// builtinFile the file that documents them, which is never compiled.
const (
	BuiltinPath = "builtin"
	builtinFile = "builtin/builtin.snello"
)

// The documentation of the predeclared names, read once, which every
// package's links resolve a name of the language against: Option and
// Result are the language's own and in no scope.
var (
	predeclaredOnce sync.Once
	predeclaredDoc  *Package
)

func predeclared() *Package {
	predeclaredOnce.Do(func() { predeclaredDoc, _ = Builtin() })
	return predeclaredDoc
}

// Builtin returns the documentation of the predeclared names: the file
// that documents them parsed with its comments and never checked, since
// it declares what the compiler provides and is not compiled.
func Builtin() (*Package, error) {
	src, err := std.Sources.ReadFile(builtinFile)
	if err != nil {
		return nil, err
	}

	file := token.NewFile(builtinFile, len(src))
	f, err := parser.ParseComments(file, src)
	if err != nil {
		return nil, err
	}

	return New(Source{Path: BuiltinPath, Files: []File{{f, file}}}), nil
}
