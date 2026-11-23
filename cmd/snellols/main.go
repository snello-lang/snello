// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command snellols is Snello's language server. An editor starts it and
// talks to it over its standard input and output in the Language Server
// Protocol: it reports the errors of the open files' packages as the
// editor holds them, as snello check would, and answers hover and go to
// definition; its logs go to standard error. It is modeled on the Language Server Protocol specification,
// 3.17, and _docs/design/15-language-server.md has its design.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/snello-lang/snello/compiler/version"
	"github.com/snello-lang/snello/lsp/server"
)

// The arguments the command takes: the one an editor's client passes to
// say the session is on standard input and output, the only one the
// server has, the editor's process, which a client may pass and the
// server needs not, since the stream's end ends the session, and the
// one that prints the version; and the exit codes of the version and
// of an argument it does not take.
const (
	stdioFlag      = "--stdio"
	clientFlag     = "--clientProcessId"
	versionFlag    = "-version"
	versionFlagGNU = "--version"
	exitSuccess    = 0
	exitUsage      = 2
	usage          = "usage: snellols [--stdio | -version]\n\nsnellols is Snello's language server: an editor starts it and talks to it\nover its standard input and output.\n"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run serves one session over in and out with its logs on errs, or
// prints the version or the usage, and returns the exit code.
func run(args []string, in io.Reader, out, errs io.Writer) int {
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == stdioFlag, strings.HasPrefix(arg, clientFlag+"="):
		case arg == clientFlag:
			i++ // its value, the next argument
		case arg == versionFlag, arg == versionFlagGNU:
			fmt.Fprintln(out, "snellols "+version.Current)
			return exitSuccess
		default:
			fmt.Fprint(errs, usage)
			return exitUsage
		}
	}

	return server.New(in, out, errs).Run()
}
