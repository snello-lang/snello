// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command teak is the Teak compiler driver.
//
// `teak build FILE` compiles a Teak program to a native binary next to
// it. Three more subcommands inspect the front end by hand: `teak lex
// FILE` prints the token stream, `teak parse FILE` prints the syntax
// tree, and `teak check FILE` reports what the checker finds.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/emit/c"
	"github.com/teak-lang/teak/parser"
	"github.com/teak-lang/teak/scanner"
	"github.com/teak-lang/teak/token"
	"github.com/teak-lang/teak/types"
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
	case "parse":
		if len(os.Args) != 3 {
			usage()
		}

		os.Exit(parse(os.Args[2]))
	case "check":
		if len(os.Args) != 3 {
			usage()
		}

		os.Exit(check(os.Args[2]))
	case "build":
		if len(os.Args) != 3 {
			usage()
		}

		os.Exit(build(os.Args[2]))
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: teak (build | lex | parse | check) FILE")
	os.Exit(2)
}

func lex(path string) int {
	src, ok := readSource(path)
	if !ok {
		return 1
	}

	file := token.NewFile(path, len(src))
	var s scanner.Scanner
	s.Init(file, src, func(pos token.Position, msg string) {
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

		fmt.Printf("%s\t%s\t%s\n", file.Position(pos), tok, lit)
	}

	if s.ErrorCount > 0 {
		return 1
	}

	return 0
}

func parse(path string) int {
	src, ok := readSource(path)
	if !ok {
		return 1
	}

	f, file, err := parser.ParseFile(path, src)
	ast.Fprint(os.Stdout, f, file)
	if err != nil {
		printErrors(err)
		return 1
	}

	return 0
}

// check parses and checks a file and prints the errors; it prints
// nothing and exits 0 when the file is correct.
func check(path string) int {
	src, ok := readSource(path)
	if !ok {
		return 1
	}

	f, file, err := parser.ParseFile(path, src)
	if err == nil {
		_, err = types.Check(file, f, nil)
	}

	if err != nil {
		printErrors(err)
		return 1
	}

	return 0
}

// build compiles a file to a native binary named after it, in the
// current directory, through the C backend and the system C compiler.
func build(path string) int {
	src, ok := readSource(path)
	if !ok {
		return 1
	}

	f, file, err := parser.ParseFile(path, src)
	info := &types.Info{}
	if err == nil {
		_, err = types.Check(file, f, info)
	}

	if err != nil {
		printErrors(err)
		return 1
	}

	csrc, err := c.Emit(file, f, info)
	if err != nil {
		printErrors(err)
		return 1
	}

	dir, err := os.MkdirTemp("", "teak-build")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	defer os.RemoveAll(dir)
	name := strings.TrimSuffix(filepath.Base(path), ".teak")
	bin, out, err := c.Build(dir, name, csrc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cc: %v\n%s", err, out)
		return 1
	}

	built, err := os.ReadFile(bin)
	if err == nil {
		err = os.WriteFile(name, built, 0o755)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	return 0
}

// printErrors writes each error of a list on its own line.
func printErrors(err error) {
	if list, ok := err.(scanner.ErrorList); ok {
		for _, e := range list {
			fmt.Fprintln(os.Stderr, e)
		}

		return
	}

	fmt.Fprintln(os.Stderr, err)
}

// readSource reads a Teak source file, reporting a failure to stderr.
// The name must end in .teak, as the Go tool insists on .go: it keeps a
// stray file from being compiled as Teak, and it keeps build from
// writing the binary over a source file with no extension to strip.
func readSource(path string) ([]byte, bool) {
	if !strings.HasSuffix(path, ".teak") {
		fmt.Fprintf(os.Stderr, "%s: Teak source files end in .teak\n", path)
		return nil, false
	}

	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, false
	}

	return src, true
}
