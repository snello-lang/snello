// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command snello is the Snello compiler driver. `snello help` lists its
// commands, each with a line, and `snello help COMMAND` a command's
// flags and arguments, read from the table in commands.go the command
// line is parsed with.
//
// `snello build FILE` compiles a Snello program of one file to a native
// binary in the current directory, through the LLVM backend and clang,
// and `snello build DIR` a program of every .snello file in a
// directory, named after the directory's last element; the packages of
// the program's own are found through the nearest snello.mod above it.
// `snello build -debug FILE` builds it for a debugger, the program's own
// code unoptimized and described in full, with the runtime's checks,
// which stop the program on a value used after its release and report
// values never released; on macOS the debugging information is gathered
// into FILE.dSYM beside the binary. `snello debug FILE [ARGS]` builds it
// so and runs lldb, or gdb on Linux, on it, with Snello's formatters and
// tasks command loaded. `snello build -backend c FILE` builds it through
// the C backend, the reference implementation, and the system's C
// compiler. `snello test DIR` builds the tests of a package, its
// _test.snello files, with a generated main and runs them. `snello init
// [DIR]` makes the current directory, or DIR, the root of a module,
// writing its snello.mod with the version of this compiler, and
// `snello version` prints that version, the commit, the channel and
// the platform. Three more commands inspect the compiler by hand:
// `snello lex FILE` prints the token stream, `snello parse FILE` the
// syntax tree, and `snello emit FILE|DIR` the IR module, or with
// -backend c the C source, that build would compile; `snello check
// FILE|DIR` prints what the checker finds. `snello new DIR` starts a
// module in a new directory, with a program and its test, and `snello
// run FILE|DIR [ARGS]` builds a program, runs it with ARGS, passes its
// exit code on and leaves no file behind.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/c"
	"github.com/snello-lang/snello/compiler/emit/llvm"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/compiler/scanner"
	"github.com/snello-lang/snello/compiler/testrun"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/toolchain"
	"github.com/snello-lang/snello/compiler/types"
)

const (
	// currentDir is the directory a command works in when it is given
	// none.
	currentDir = "."

	// buildDirPattern names the directory a build works in, removed
	// when it ends, and binaryMode the permissions of the binary it
	// writes: run and read by all, written by its owner.
	buildDirPattern = "snello-build"
	binaryMode      = 0o755

	// newline is the token the lexer gives for the end of a line, which
	// lex prints quoted so that the listing keeps one token a line.
	newline = "\n"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// runTests builds the tests of the package in dir into a program and
// runs it through the backend, passing pass, the flags meant for the
// program, whose exit code is the command's. In a checkout of the
// compiler, std/NAME is a standard package, run from its sources as
// edited with the library's privileges.
func runTests(dir, backend string, pass []string, stdout, stderr io.Writer) int {
	code, err := testrun.Run(dir, backend, pass, stdout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}

	return code
}

func lex(path string) int {
	src, ok := readSource(path)
	if !ok {
		return exitFailure
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

		if lit == newline {
			lit = strconv.Quote(newline)
		}

		fmt.Printf("%s\t%s\t%s\n", file.Position(pos), tok, lit)
	}

	if s.ErrorCount > 0 {
		return exitFailure
	}

	return exitSuccess
}

func parse(path string) int {
	src, ok := readSource(path)
	if !ok {
		return exitFailure
	}

	f, file, err := parser.ParseFile(path, src)
	ast.Fprint(os.Stdout, f, file)
	if err != nil {
		printErrors(os.Stderr, err)
		return exitFailure
	}

	return exitSuccess
}

// check parses and checks a program, a file or a directory, and prints
// the errors; it prints nothing on standard output and exits 0 when the
// program is correct, and warns on standard error of a module file with
// no snello line.
// A directory whose package is not main is checked as the library it
// is, so that its binding to C is not refused as a program's would be.
func check(path string, target platform.Target) int {
	names, dir, ok := programFiles(path, target, os.Stderr)
	if !ok {
		return exitFailure
	}

	warnModule(os.Stderr, names)
	info := &types.Info{}
	var err error
	if dir {
		err = importer.CheckDir(path, info, importer.Options{Target: target})
	} else {
		_, _, err = importer.LoadProgram(names, target, info)
	}

	if err != nil {
		printErrors(os.Stderr, err)
		return exitFailure
	}

	return exitSuccess
}

// initModule writes a module file into dir through
// importer.WriteModFile, and says on w, standard error, which file it
// wrote; a module file already there is refused, and the command exits
// 1. It creates and writes one file.
func initModule(dir string, w io.Writer) int {
	name, err := importer.WriteModFile(dir)
	if err != nil {
		fmt.Fprintln(w, program+" "+initCommand+": "+err.Error())
		return exitFailure
	}

	fmt.Fprintln(w, program+" "+initCommand+": wrote "+name)
	return exitSuccess
}

// warnModule prints to w, standard error, before the program is
// loaded, the warning for the module of the program whose files are
// names when its file has no snello line, so that a build that fails
// still says it.
func warnModule(w io.Writer, names []string) {
	if msg := importer.ModuleWarning(filepath.Dir(names[0])); msg != "" {
		fmt.Fprintln(w, msg)
	}
}

// emit prints what build would compile: the C source, or the IR
// module, for reading.
func emit(path, backend string, target platform.Target, debug bool) int {
	src, _, ok := emitSource(path, backend, target, debug, os.Stderr)
	if !ok {
		return exitFailure
	}

	os.Stdout.Write(src)
	return exitSuccess
}

// build compiles a program, a file or a directory, to a native binary
// named after it, in the current directory, through the backend asked
// for; debug asks for the runtime's checks.
func build(path, backend string, target platform.Target, debug bool) int {
	bin, name, work, ok := compile(path, backend, target, debug, os.Stderr)
	if !ok {
		return exitFailure
	}

	defer work.remove()
	var out []byte
	built, err := os.ReadFile(bin)
	if err == nil {
		err = os.WriteFile(name, built, binaryMode)
	}

	if err == nil && debug {
		out, err = c.Bundle(bin, name, target) // before the build's directory, which holds the objects the bundle is gathered from, is removed
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n%s", err, out)
		return exitFailure
	}

	return exitSuccess
}

// compile builds the program at path, a file or a directory, through
// the backend, and returns the binary's path, in the build's work
// directory, which its remove removes, and the name a build gives it,
// printing to stderr the errors of whichever step fails; debug asks for
// the runtime's checks. The directory is made once the program has
// loaded, and is guarded as workDir says, so that a build leaves
// nothing behind however it ends.
func compile(path, backend string, target platform.Target, debug bool, stderr io.Writer) (bin, name string, work *workDir, ok bool) {
	_, isDir, ok := programFiles(path, target, stderr)
	if !ok {
		return "", "", nil, false
	}

	src, bindings, ok := emitSource(path, backend, target, debug, stderr)
	if !ok {
		return "", "", nil, false
	}

	work, err := newWorkDir(buildDirPattern)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return "", "", nil, false
	}

	var out []byte
	if backend == lower.LLVMBackend {
		bin, out, err = llvm.Build(work.path, programName(path, isDir), src, target, debug, bindings...)
	} else {
		bin, out, err = c.Build(work.path, programName(path, isDir), src, target, debug, bindings...)
	}

	if err != nil {
		work.remove()
		var missing *toolchain.MissingError
		if errors.As(err, &missing) {
			fmt.Fprintln(stderr, missing) // what to do, which needs no backend's name before it
		} else {
			fmt.Fprintf(stderr, "%s: %v\n%s", backend, err, out)
		}

		return "", "", nil, false
	}

	return bin, filepath.Base(bin), work, true // the target's name for it, name.exe for Windows
}

// emitSource loads a program, a file or a directory, and emits it
// through the backend, printing the errors of whichever step fails,
// with the C side of every package that binds C, for the build. A
// package that is not a program is refused as CheckMain says, since
// only check takes one. The LLVM backend emits a debug build's checks
// itself, so it is told; the C backend's are a flag of the C compiler.
// The errors and the module's warning go to stderr.
func emitSource(path, backend string, target platform.Target, debug bool, stderr io.Writer) ([]byte, []lower.Binding, bool) {
	names, _, ok := programFiles(path, target, stderr)
	if !ok {
		return nil, nil, false
	}

	warnModule(stderr, names)
	info := &types.Info{}
	imp, prog, err := importer.LoadProgram(names, target, info)
	if err == nil {
		if err = prog.CheckMain(); err != nil {
			err = errors.New(path + ": " + err.Error())
		}
	}

	var out []byte
	var bindings []lower.Binding
	if err == nil {
		libs := libraries(imp)
		bindings = lower.Bindings(info, libs)
		if backend == lower.LLVMBackend {
			out, err = llvm.EmitFiles(llvm.Config{Debug: debug, Arch: target.Arch, OS: target.OS}, prog.Files, prog.Syntax, info, libs...)
			if err == nil {
				out, _ = llvm.Targeted(out) // as build compiles it, so that emit prints the same module; without clang, the module as emitted
			}
		} else {
			out, err = c.EmitFiles(prog.Files, prog.Syntax, info, libs...)
		}
	}

	if err != nil {
		printErrors(stderr, err)
		return nil, nil, false
	}

	return out, bindings, true
}

// programFiles returns the source files of the program at path, as go
// build takes a file or a directory: the one file named, which must
// end in .snello, or every .snello file of the directory, which must
// hold one; and whether path is a directory. A failure is reported to
// stderr.
func programFiles(path string, target platform.Target, stderr io.Writer) (names []string, dir, ok bool) {
	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return nil, false, false
	}

	if !info.IsDir() {
		if !isSource(path, stderr) {
			return nil, false, false
		}

		return []string{path}, false, true
	}

	names, err = importer.SourceFiles(path, target)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return nil, true, false
	}

	if len(names) == 0 {
		fmt.Fprintf(stderr, "%s: no %s files in the directory\n", path, importer.SourceExt)
		return nil, true, false
	}

	return names, true, true
}

// programName names the binary of a build: the last element of path,
// which for a directory given as . or .. comes
// from its absolute path, without the .snello extension of a file, or
// of a directory so named, so that the binary is never taken for a
// source file by the next build.
func programName(path string, dir bool) string {
	if dir {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}

	return strings.TrimSuffix(filepath.Base(path), importer.SourceExt)
}

// libraries returns the packages the importer loaded, as the backends
// take them.
func libraries(imp *importer.Importer) []lower.Library {
	pkgs := imp.Packages()
	libs := make([]lower.Library, len(pkgs))
	for i, p := range pkgs {
		libs[i] = lower.Library{Path: p.Path, File: p.File, Syntax: p.Syntax, CFiles: p.CFiles, Links: p.Links, Std: p.Std}
	}

	return libs
}

// isBackend reports whether name is a backend's: llvm, or c.
func isBackend(name string) bool {
	return name == lower.LLVMBackend || name == lower.CBackend
}

// printErrors writes each error of a list on its own line.
func printErrors(w io.Writer, err error) {
	if list, ok := err.(scanner.ErrorList); ok {
		for _, e := range list {
			fmt.Fprintln(w, e)
		}

		return
	}

	fmt.Fprintln(w, err)
}

// readSource reads a Snello source file, reporting a failure to stderr.
// The name must end in .snello, as the Go tool insists on .go: it keeps a
// stray file from being compiled as Snello, and it keeps build from
// writing the binary over a source file with no extension to strip.
func readSource(path string) ([]byte, bool) {
	if !isSource(path, os.Stderr) {
		return nil, false
	}

	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, false
	}

	return src, true
}

// isSource reports whether path names a Snello source file, and says
// so to stderr when it does not.
func isSource(path string, stderr io.Writer) bool {
	if !strings.HasSuffix(path, importer.SourceExt) {
		fmt.Fprintf(stderr, "%s: Snello source files end in %s\n", path, importer.SourceExt)
		return false
	}

	return true
}
