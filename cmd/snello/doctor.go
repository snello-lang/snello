// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/snello-lang/snello/compiler/toolchain"
)

const (
	// The marks a check is printed with: passed, failed so that a build
	// fails, and failed only for something beside a build; markFormat
	// lines them up, and fixIndent sets a fix under its check.
	passMark   = "ok"
	failMark   = "fail"
	noteMark   = "note"
	markFormat = "%-4s  %s: %s\n"
	fixIndent  = "      "
)

// doctor runs every check of what a build needs, prints on w one line a
// check, with the fix under a check that failed, and returns
// exitFailure when a build would fail, a check it needs having failed.
// self is the snello command, whose directory should be on the path.
// It runs the compilers on small programs, as toolchain.Checks says.
func doctor(self string, w io.Writer) int {
	code := exitSuccess
	for _, c := range toolchain.Checks(self) {
		mark := passMark
		switch {
		case !c.OK && c.Needed:
			mark, code = failMark, exitFailure
		case !c.OK:
			mark = noteMark
		}

		fmt.Fprintf(w, markFormat, mark, c.Name, c.Detail)
		if !c.OK && c.Fix != "" {
			fmt.Fprintln(w, fixIndent+c.Fix)
		}
	}

	return code
}

// self returns the path of the running snello command, its links
// followed, or its name as run when the system does not say.
func self() string {
	path, err := os.Executable()
	if err != nil {
		return os.Args[0]
	}

	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}

	return path
}
