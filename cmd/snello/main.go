// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command snello is the Snello compiler driver.
//
// `snello build FILE` compiles a Snello program of one file to a native
// binary in the current directory, through the LLVM backend and clang,
// and `snello build DIR` a program of every .snello file in a
// directory, named after the directory's last element; the
// packages of the program's own are found through the nearest
// snello.mod above it. `snello build -debug FILE` builds it for a
// debugger, the program's own code unoptimized and described in full,
// with the runtime's checks, which stop the program on a value used
// after its release and report values never released; on macOS the
// debugging information is gathered into FILE.dSYM beside the binary.
// `snello debug FILE [ARGS]` builds it so and runs lldb, or gdb on
// Linux, on it, with Snello's formatters and tasks command loaded.
// `snello build -backend c
// FILE` builds it through the C backend, the reference implementation,
// and the system's C compiler. `snello test DIR` builds the tests of a
// package, its _test.snello files, with a generated main and runs them.
// `snello init [DIR]` makes the current directory, or DIR, the root of
// a module, writing its snello.mod with the version of this compiler.
// Four more subcommands inspect the compiler by hand: `snello lex FILE`
// prints the token stream, `snello parse FILE` the syntax tree, `snello
// check FILE|DIR` what the checker finds, and `snello emit [-debug] [-backend c]
// FILE|DIR` the IR module, or the C source, that build would compile.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/c"
	"github.com/snello-lang/snello/compiler/emit/llvm"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/scanner"
	"github.com/snello-lang/snello/compiler/testrun"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

const (
	// The commands, the first argument of every command line.
	lexCommand   = "lex"
	parseCommand = "parse"
	checkCommand = "check"
	buildCommand = "build"
	emitCommand  = "emit"
	testCommand  = "test"
	debugCommand = "debug"
	initCommand  = "init"

	// commandArg is where the command stands among the arguments, and
	// pathArgs how many arguments a command line has that names one
	// path after its command: snello check FILE.
	commandArg = 1
	pathArgs   = 3

	// The exit codes: a command that did its work, one that failed,
	// and one given wrongly, as the flag package exits.
	exitSuccess = 0
	exitFailure = 1
	exitUsage   = 2

	// The flags: the backend, a build for a debugger, and those test
	// passes on to the program it builds, each written after flagStart;
	// a test program runs each test once unless -count says otherwise.
	backendFlag  = "backend"
	debugFlag    = "debug"
	verboseFlag  = "v"
	failfastFlag = "failfast"
	shuffleFlag  = "shuffle"
	shortFlag    = "short"
	updateFlag   = "update"
	runFlag      = "run"
	benchFlag    = "bench"
	countFlag    = "count"
	timeoutFlag  = "timeout"
	flagStart    = "-"
	defaultCount = 1

	// currentDir is the directory a command works in when it is given
	// none, and mainPackage the package clause of a program.
	currentDir  = "."
	mainPackage = "main"

	// buildDirPattern names the directory a build works in, removed
	// when it ends, and binaryMode the permissions of the binary it
	// writes: run and read by all, written by its owner.
	buildDirPattern = "snello-build"
	binaryMode      = 0o755

	// newline is the token the lexer gives for the end of a line, which
	// lex prints quoted so that the listing keeps one token a line.
	newline = "\n"
)

// testBoolFlags and testStringFlags are the flags test passes on to the
// program it builds as they were given.
var (
	testBoolFlags   = []string{verboseFlag, failfastFlag, shuffleFlag, shortFlag, updateFlag}
	testStringFlags = []string{runFlag, benchFlag}
)

func main() {
	if len(os.Args) <= commandArg {
		usage()
	}

	args := os.Args[commandArg+1:]
	switch os.Args[commandArg] {
	case lexCommand:
		if len(os.Args) != pathArgs {
			usage()
		}

		os.Exit(lex(args[0]))
	case parseCommand:
		if len(os.Args) != pathArgs {
			usage()
		}

		os.Exit(parse(args[0]))
	case checkCommand:
		if len(os.Args) != pathArgs {
			usage()
		}

		os.Exit(check(args[0]))
	case buildCommand:
		path, backend, debug := buildArgs(args, true)
		os.Exit(build(path, backend, debug))
	case emitCommand:
		path, backend, debug := buildArgs(args, true)
		os.Exit(emit(path, backend, debug))
	case testCommand:
		os.Exit(test(args))
	case debugCommand:
		os.Exit(debug(args))
	case initCommand:
		os.Exit(initModule(args, os.Stderr))
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: snello (build [-debug] [-backend llvm|c] | emit [-debug] [-backend llvm|c] | check) FILE|DIR")
	fmt.Fprintln(os.Stderr, "       snello debug [-backend llvm|c] FILE|DIR [ARGS...]")
	fmt.Fprintln(os.Stderr, "       snello test [-backend llvm|c] [-v] [-run NAME] [-bench NAME] [-count N] [-failfast] [-timeout DURATION] [-shuffle] [-short] [-update] DIR")
	fmt.Fprintln(os.Stderr, "       snello init [DIR]")
	fmt.Fprintln(os.Stderr, "       snello (lex | parse) FILE")
	os.Exit(exitUsage)
}

// test builds the tests of the package in a directory into a program
// and runs it: -backend chooses the backend, and the rest of the
// flags, -v and -run NAME, go to the program, whose exit code is the
// command's. In a checkout of the compiler, std/NAME is a standard
// package, run from its sources as edited with the library's
// privileges.
func test(args []string) int {
	fs := flag.NewFlagSet(testCommand, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	backend := fs.String(backendFlag, lower.LLVMBackend, "the backend: llvm, or c for the reference backend")
	bools := map[string]*bool{}
	for _, name := range testBoolFlags {
		bools[name] = fs.Bool(name, false, "")
	}

	strs := map[string]*string{}
	for _, name := range testStringFlags {
		strs[name] = fs.String(name, "", "")
	}

	count := fs.Int(countFlag, defaultCount, "")
	timeout := fs.Duration(timeoutFlag, 0, "")

	if err := fs.Parse(args); err != nil || fs.NArg() != 1 || !isBackend(*backend) {
		usage()
	}

	var pass []string
	for _, name := range testBoolFlags {
		if *bools[name] {
			pass = append(pass, flagStart+name)
		}
	}

	for _, name := range testStringFlags {
		if *strs[name] != "" {
			pass = append(pass, flagStart+name, *strs[name])
		}
	}

	if *count != defaultCount {
		pass = append(pass, flagStart+countFlag, strconv.Itoa(*count))
	}

	if *timeout > 0 { // the program counts in whole seconds
		pass = append(pass, flagStart+timeoutFlag, strconv.Itoa(int((*timeout+time.Second-1)/time.Second)))
	}

	code, err := testrun.Run(fs.Arg(0), *backend, pass, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFailure
	}

	return code
}

// buildArgs reads the flags of build and emit: the backend, and for a
// build whether the runtime's checks are wanted.
func buildArgs(args []string, withDebug bool) (path, backend string, debug bool) {
	fs := flag.NewFlagSet(buildCommand, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&backend, backendFlag, lower.LLVMBackend, "the backend: llvm, or c for the reference backend")
	if withDebug {
		fs.BoolVar(&debug, debugFlag, false, "build for a debugger, unoptimized, with the runtime's checks")
	}

	if err := fs.Parse(args); err != nil || fs.NArg() != 1 || !isBackend(backend) {
		usage()
	}

	return fs.Arg(0), backend, debug
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
		printErrors(err)
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
func check(path string) int {
	names, dir, ok := programFiles(path)
	if !ok {
		return exitFailure
	}

	warnModule(os.Stderr, names)
	info := &types.Info{}
	var err error
	if dir && !isMain(names[0]) {
		err = importer.LoadPackage(path, info)
	} else {
		_, _, err = importer.LoadProgram(names, info)
	}

	if err != nil {
		printErrors(err)
		return exitFailure
	}

	return exitSuccess
}

// initModule writes a module file into the directory args name, the
// current one when they name none, through importer.WriteModFile, and
// says on w, standard error, which file it wrote; a module file already
// there is refused, and the command exits 1. A flag or more than one
// directory is a usage error. It creates and writes one file.
func initModule(args []string, w io.Writer) int {
	fs := flag.NewFlagSet(initCommand, flag.ContinueOnError)
	fs.SetOutput(w)
	if err := fs.Parse(args); err != nil || fs.NArg() > 1 {
		usage()
	}

	dir := currentDir
	if fs.NArg() == 1 {
		dir = fs.Arg(0)
	}

	name, err := importer.WriteModFile(dir)
	if err != nil {
		fmt.Fprintln(w, "snello "+initCommand+": "+err.Error())
		return exitFailure
	}

	fmt.Fprintln(w, "snello "+initCommand+": wrote "+name)
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

// isMain reports whether the source file's package clause says main;
// a file that does not read or parse counts as main, so that the
// program's loader reports it.
func isMain(name string) bool {
	src, err := os.ReadFile(name)
	if err != nil {
		return true
	}

	f, _, _ := parser.ParseFile(name, src)
	return f == nil || f.Name == nil || f.Name.Name == mainPackage
}

// emit prints what build would compile: the C source, or the IR
// module, for reading.
func emit(path, backend string, debug bool) int {
	src, _, ok := emitSource(path, backend, debug)
	if !ok {
		return exitFailure
	}

	os.Stdout.Write(src)
	return exitSuccess
}

// build compiles a program, a file or a directory, to a native binary
// named after it, in the current directory, through the backend asked
// for; debug asks for the runtime's checks.
func build(path, backend string, debug bool) int {
	_, isDir, ok := programFiles(path)
	if !ok {
		return exitFailure
	}

	src, bindings, ok := emitSource(path, backend, debug)
	if !ok {
		return exitFailure
	}

	dir, err := os.MkdirTemp("", buildDirPattern)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFailure
	}

	defer os.RemoveAll(dir)
	name := programName(path, isDir)
	var bin string
	var out []byte
	if backend == lower.LLVMBackend {
		bin, out, err = llvm.Build(dir, name, src, debug, bindings...)
	} else {
		bin, out, err = c.Build(dir, name, src, debug, bindings...)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n%s", backend, err, out)
		return exitFailure
	}

	built, err := os.ReadFile(bin)
	if err == nil {
		err = os.WriteFile(name, built, binaryMode)
	}

	if err == nil && debug {
		out, err = c.Bundle(bin, name) // before the build's directory, which holds the objects the bundle is gathered from, is removed
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n%s", err, out)
		return exitFailure
	}

	return exitSuccess
}

// emitSource loads a program, a file or a directory, and emits it
// through the backend, printing the errors of whichever step fails,
// with the C side of every package that binds C, for the build. A
// package that is not a program is refused as CheckMain says, since
// only check takes one. The LLVM backend emits a debug build's checks
// itself, so it is told; the C backend's are a flag of the C compiler.
func emitSource(path, backend string, debug bool) ([]byte, []lower.Binding, bool) {
	names, _, ok := programFiles(path)
	if !ok {
		return nil, nil, false
	}

	warnModule(os.Stderr, names)
	info := &types.Info{}
	imp, prog, err := importer.LoadProgram(names, info)
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
			out, err = llvm.EmitFiles(llvm.Config{Debug: debug}, prog.Files, prog.Syntax, info, libs...)
			if err == nil {
				out, _ = llvm.Targeted(out) // as build compiles it, so that emit prints the same module; without clang, the module as emitted
			}
		} else {
			out, err = c.EmitFiles(prog.Files, prog.Syntax, info, libs...)
		}
	}

	if err != nil {
		printErrors(err)
		return nil, nil, false
	}

	return out, bindings, true
}

// programFiles returns the source files of the program at path, as go
// build takes a file or a directory: the one file named, which must
// end in .snello, or every .snello file of the directory, which must
// hold one; and whether path is a directory. A failure is reported to
// stderr.
func programFiles(path string) (names []string, dir, ok bool) {
	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, false, false
	}

	if !info.IsDir() {
		if !isSource(path) {
			return nil, false, false
		}

		return []string{path}, false, true
	}

	names, err = importer.SourceFiles(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, true, false
	}

	if len(names) == 0 {
		fmt.Fprintf(os.Stderr, "%s: no %s files in the directory\n", path, importer.SourceExt)
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
	if !isSource(path) {
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
func isSource(path string) bool {
	if !strings.HasSuffix(path, importer.SourceExt) {
		fmt.Fprintf(os.Stderr, "%s: Snello source files end in %s\n", path, importer.SourceExt)
		return false
	}

	return true
}
