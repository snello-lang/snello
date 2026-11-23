// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"io"
	"os"

	"github.com/snello-lang/snello/lsp/server"
)

// declareLSP declares lsp, which serves one editor's session of the
// Language Server Protocol on standard input and output, its logs on
// standard error, and exits with the code the protocol asks for. It
// takes the flags an editor's client passes: --stdio, which says the
// session is on standard input and output, the only one the server
// has, and --clientProcessId, the editor's process, which the server
// needs not, since the stream's end ends the session.
func declareLSP(fs *flag.FlagSet, stdout, stderr io.Writer) func(args []string) int {
	fs.Bool(stdioFlag, true, "serve on standard input and output, the only way the server serves")
	fs.Int(clientFlag, 0, "the editor's process, which the server needs not")
	return func(args []string) int { return lsp(os.Stdin, stdout, stderr) }
}

// lsp serves one session over in and out, its logs on errs, and returns
// the exit code: 0 after a shutdown and an exit, 1 after an exit alone
// or the end of the stream.
func lsp(in io.Reader, out, errs io.Writer) int {
	return server.New(in, out, errs).Run()
}
