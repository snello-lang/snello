// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command snello is the Snello compiler driver.
//
// `snello build FILE` compiles a Snello program to a native binary in the
// current directory, through the LLVM backend and clang; `snello build
// -debug FILE` builds it with the runtime's checks, which stop the
// program on a value used after its release and report values never
// released; `snello build -backend c FILE` builds it through the C
// backend, the reference implementation, and the system's C compiler.
// Four more subcommands inspect the compiler by hand: `snello lex FILE`
// prints the token stream, `snello parse FILE` the syntax tree, `snello
// check FILE` what the checker finds, and `snello emit [-backend c]
// FILE` the IR module, or the C source, that build would compile.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/c"
	"github.com/snello-lang/snello/compiler/emit/llvm"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/scanner"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
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
		path, backend, debug := buildArgs(os.Args[2:], true)
		os.Exit(build(path, backend, debug))
	case "emit":
		path, backend, _ := buildArgs(os.Args[2:], false)
		os.Exit(emit(path, backend))
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: snello (build [-debug] [-backend llvm|c] | emit [-backend llvm|c] | lex | parse | check) FILE")
	os.Exit(2)
}

// buildArgs reads the flags of build and emit: the backend, and for a
// build whether the runtime's checks are wanted.
func buildArgs(args []string, withDebug bool) (path, backend string, debug bool) {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&backend, "backend", "llvm", "the backend: llvm, or c for the reference backend")
	if withDebug {
		fs.BoolVar(&debug, "debug", false, "build with the runtime's checks")
	}

	if err := fs.Parse(args); err != nil || fs.NArg() != 1 || backend != "c" && backend != "llvm" {
		usage()
	}

	return fs.Arg(0), backend, debug
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
		conf := types.Config{Importer: importer.NewImporter(&types.Info{})}
		_, err = conf.Check("", file, f, nil)
	}

	if err != nil {
		printErrors(err)
		return 1
	}

	return 0
}

// emit prints what build would compile: the C source, or the IR
// module, for reading.
func emit(path, backend string) int {
	src, ok := emitSource(path, backend, false)
	if !ok {
		return 1
	}

	os.Stdout.Write(src)
	return 0
}

// build compiles a file to a native binary named after it, in the
// current directory, through the backend asked for; debug asks for
// the runtime's checks.
func build(path, backend string, debug bool) int {
	src, ok := emitSource(path, backend, debug)
	if !ok {
		return 1
	}

	dir, err := os.MkdirTemp("", "snello-build")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	defer os.RemoveAll(dir)
	name := strings.TrimSuffix(filepath.Base(path), ".snello")
	var bin string
	var out []byte
	if backend == "llvm" {
		bin, out, err = llvm.Build(dir, name, src, debug)
	} else {
		bin, out, err = c.Build(dir, name, src, debug)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n%s", backend, err, out)
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

// emitSource parses, checks and emits a file through the backend,
// printing the errors of whichever step fails. The LLVM backend emits
// a debug build's checks itself, so it is told; the C backend's are a
// flag of the C compiler.
func emitSource(path, backend string, debug bool) ([]byte, bool) {
	src, ok := readSource(path)
	if !ok {
		return nil, false
	}

	f, file, err := parser.ParseFile(path, src)
	info := &types.Info{}
	imp := importer.NewImporter(info)
	if err == nil {
		conf := types.Config{Importer: imp}
		_, err = conf.Check("", file, f, info)
	}

	var out []byte
	if err == nil {
		if backend == "llvm" {
			out, err = llvm.Emit(llvm.Config{Debug: debug}, file, f, info, libraries(imp)...)
		} else {
			out, err = c.Emit(file, f, info, libraries(imp)...)
		}
	}

	if err != nil {
		printErrors(err)
		return nil, false
	}

	return out, true
}

// libraries returns the packages the importer loaded, as the backends
// take them.
func libraries(imp *importer.Importer) []lower.Library {
	pkgs := imp.Packages()
	libs := make([]lower.Library, len(pkgs))
	for i, p := range pkgs {
		libs[i] = lower.Library{File: p.File, Syntax: p.Syntax}
	}

	return libs
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

// readSource reads a Snello source file, reporting a failure to stderr.
// The name must end in .snello, as the Go tool insists on .go: it keeps a
// stray file from being compiled as Snello, and it keeps build from
// writing the binary over a source file with no extension to strip.
func readSource(path string) ([]byte, bool) {
	if !strings.HasSuffix(path, ".snello") {
		fmt.Fprintf(os.Stderr, "%s: Snello source files end in .snello\n", path)
		return nil, false
	}

	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, false
	}

	return src, true
}
