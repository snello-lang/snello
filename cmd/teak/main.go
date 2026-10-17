// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command teak is the Teak compiler driver.
//
// Stage 0 provides one subcommand, `teak lex FILE`, which prints the
// token stream. It exists so the lexer can be inspected by hand while the
// parser is being written.
package main

import (
	"fmt"
	"os"

	"github.com/teak-lang/teak/scanner"
	"github.com/teak-lang/teak/token"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	switch os.Args[1] {
	case "lex":
		if len(os.Args) != 3 {
			usage()
		}
		os.Exit(lex(os.Args[2]))
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: teak lex FILE")
	os.Exit(2)
}

func lex(path string) int {
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	var s scanner.Scanner
	s.Init(path, src, func(pos token.Pos, msg string) {
		fmt.Fprintf(os.Stderr, "%s: %s\n", pos, msg)
	})

	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if lit == "\n" {
			lit = `"\n"`
		}
		fmt.Printf("%s\t%s\t%s\n", pos, tok, lit)
	}

	if s.ErrorCount > 0 {
		return 1
	}
	return 0
}
