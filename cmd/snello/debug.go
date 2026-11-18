// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/emit/c"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/std"
)

const (
	// supportDir is the directory of the cache the debugger's support is
	// written into, each version in a directory named by a hash of it
	// (hashChars of the hash's hex), holding the script, scriptName, and
	// the standard library's sources, under stdDir; supportTemp names
	// the directory of the system's temporary one a debugger's session
	// writes its own copy into when the cache is off.
	supportDir  = "debug"
	scriptName  = "snello.py"
	jsName      = "snello.js"
	stdDir      = "std"
	hashChars   = 16
	supportTemp = "snello-debug-"

	// lldb is the debugger macOS ships, gdb the one Linux's
	// distributions do, and cdb Windows' SDK does, which reads the PDB a
	// Windows debug build writes; darwin names macOS and windows
	// Windows.
	lldb    = "lldb"
	gdb     = "gdb"
	cdb     = "cdb"
	darwin  = "darwin"
	windows = "windows"

	// cdb's arguments: where sources are looked for, the support's
	// directory, whose files the debugger finds by the end of a path the
	// debugging information names, std/os/os.snello, and then the
	// places sourcePathVar names, parted by cdbPathSeparator; and the
	// commands run first, which load the script and read the source's
	// lines, parted by cdbSeparator. A source path is an option and not
	// a command, since the command takes the rest of the line as its
	// places. cdbQuote encloses a path with spaces. programFilesX86 is
	// the variable naming the directory the SDK installs its debuggers
	// under, a directory a processor in windowsKitDebuggers.
	cdbSourcePath    = "-srcpath"
	sourcePathVar    = "_NT_SOURCE_PATH"
	cdbPathSeparator = ";"
	cdbCommand       = "-c"
	cdbScriptLoad    = ".scriptload "
	cdbLines         = ".lines -e"
	cdbSeparator     = "; "
	cdbQuote         = `"`
	programFilesX86  = "ProgramFiles(x86)"
	executable       = ".exe"

	// lldb's arguments: a command run first, the end of its own
	// options, and the commands that load the script and map the
	// standard library's sources.
	lldbCommand   = "-o"
	lldbProgram   = "--"
	lldbImport    = "command script import "
	lldbSourceMap = "settings set target.source-map "

	// gdb's: a script to read, a command to run, the program and its
	// arguments, and the command that maps the sources.
	gdbScript         = "-x"
	gdbCommand        = "-ex"
	gdbProgram        = "--args"
	gdbSubstitutePath = "set substitute-path "

	// argSeparator parts the words of a debugger command, stdPathEnd
	// ends the standard library's path prefix in the debugging
	// information, tempSeparator parts a support directory's name from
	// the suffix of its temporary copy, and supportMode and
	// supportFileMode are the permissions of the support's directories
	// and files: read, and a directory entered, by all, written by
	// their owner.
	argSeparator    = " "
	stdPathEnd      = "/"
	tempSeparator   = "."
	supportMode     = 0o755
	supportFileMode = 0o644
)

// debuggerScript is Snello's support for lldb and gdb: the formatters
// that show a value as the program writes it, and the tasks command.
//
//go:embed snello.py
var debuggerScript []byte

// windowsScript is Snello's support for cdb and WinDbg: the tasks
// command; the values are shown by the visualizers the build embeds in
// the PDB.
//
//go:embed snello.js
var windowsScript []byte

// windowsKitDebuggers is where Windows' SDK installs its debuggers,
// under the program files' directory, in one directory a processor,
// named by kitProcessors.
var (
	windowsKitDebuggers = []string{"Windows Kits", "10", "Debuggers"}
	kitProcessors       = map[string]string{"amd64": "x64", "arm64": "arm64"}
)

// debug builds the program at path, a file or a directory, as build
// -debug does through backend, and runs the platform's debugger on the
// binary, with args, the arguments after the program's path, for the
// program, Snello's script loaded and the standard library's
// sources where the debugger looks for them: the command loads the
// script, since neither debugger loads one a binary names unless the
// user has trusted its place. It returns the debugger's exit code.
func debug(path, backend string, args []string) int {
	_, isDir, ok := programFiles(path, platform.Host(), os.Stderr)
	if !ok {
		return exitFailure
	}

	name := platformDebugger()
	debugger, found := debuggerPath(name)
	if !found { // before a build there is no debugger to run
		where := "on the path"
		if name == cdb {
			where += " or in Windows' SDK"
		}

		fmt.Fprintf(os.Stderr, "%s %s: %s is not %s\n", program, debugCommand, name, where)
		return exitFailure
	}

	if code := build(path, backend, platform.Host(), true); code != 0 {
		return code
	}

	support, done, err := debugSupport()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFailure
	}

	defer done()
	cmdArgs := debuggerCommand(name, support, currentDir+string(filepath.Separator)+programName(path, isDir), args)
	cmd := exec.Command(debugger, cmdArgs...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}

		fmt.Fprintln(os.Stderr, err)
		return exitFailure
	}

	return exitSuccess
}

// platformDebugger is the debugger of the system the compiler runs on:
// lldb on macOS, gdb on Linux, and cdb on Windows.
func platformDebugger() string {
	switch goruntime.GOOS {
	case darwin:
		return lldb
	case windows:
		return cdb
	}

	return gdb
}

// debuggerPath returns the path of the debugger name, and whether it is
// there: the one on the path, or for cdb the one Windows' SDK installs
// for the processor the command runs on, outside the path.
func debuggerPath(name string) (string, bool) {
	if found, err := exec.LookPath(name); err == nil {
		return found, true
	}

	programFiles := os.Getenv(programFilesX86)
	processor, known := kitProcessors[goruntime.GOARCH]
	if name != cdb || programFiles == "" || !known { // never a path relative to the working directory
		return "", false
	}

	parts := append([]string{programFiles}, windowsKitDebuggers...)
	kit := filepath.Join(append(parts, processor, cdb+executable)...)
	if _, err := os.Stat(kit); err != nil {
		return "", false
	}

	return kit, true
}

// debuggerCommand is the arguments of the debugger name: bin, run with
// args, the script in support loaded, and the standard library's
// files, which the debugging information names under lower.StdSources,
// read from support's copy: by that prefix mapped to it for lldb and
// gdb, and for cdb, which maps no prefix, by the end of the path, found
// under the support's directory.
func debuggerCommand(name, support, bin string, args []string) []string {
	if name == cdb {
		return append(append(cdbArgs(support), bin), args...)
	}

	script := filepath.Join(support, scriptName)
	prefix := strings.TrimSuffix(lower.StdSources, stdPathEnd)
	sources := filepath.Join(support, stdDir)
	if name == lldb {
		return append([]string{lldbCommand, lldbImport + strconv.Quote(script), lldbCommand, lldbSourceMap + prefix + argSeparator + strconv.Quote(sources), lldbProgram, bin}, args...)
	}

	return append([]string{gdbScript, script, gdbCommand, gdbSubstitutePath + prefix + argSeparator + strconv.Quote(sources), gdbProgram, bin}, args...)
}

// cdbArgs returns cdb's arguments before the program: the support's
// directory first among the places sources are looked for, the user's
// own after it, and the commands run first, which load the script and
// read the source's lines, then more.
func cdbArgs(support string, more ...string) []string {
	places := support
	if own := os.Getenv(sourcePathVar); own != "" {
		places += cdbPathSeparator + own
	}

	commands := append([]string{cdbScriptLoad + cdbQuote + filepath.Join(support, jsName) + cdbQuote, cdbLines}, more...)
	return []string{cdbSourcePath, places, cdbCommand, strings.Join(commands, cdbSeparator)}
}

// debugSupport returns the directory holding the script and the
// standard library's sources, and what to call when the debugger is
// done with it. In the cache directory it is written once for each
// version of them, under a hash of their content, and whole, into a
// directory of its own renamed into place, so that a debugger never
// reads half of one; the cache is the user's own. With the cache off
// it is written for this session alone into a directory the system
// makes for it, which only the user may write, and removed after, so
// that no file another user placed under a name known in advance is
// ever run as the script.
func debugSupport() (dir string, done func(), err error) {
	base, ok := c.CacheDir()
	if !ok {
		tmp, err := os.MkdirTemp("", supportTemp)
		if err != nil {
			return "", nil, err
		}

		if err := writeSupport(tmp); err != nil {
			os.RemoveAll(tmp)
			return "", nil, err
		}

		return tmp, func() { os.RemoveAll(tmp) }, nil
	}

	sum := sha256.New()
	sum.Write(debuggerScript)
	sum.Write(windowsScript)
	err = fs.WalkDir(std.Sources, currentDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		src, err := std.Sources.ReadFile(path)
		sum.Write([]byte(path))
		sum.Write(src)
		return err
	})
	if err != nil {
		return "", nil, err
	}

	parent := filepath.Join(base, supportDir)
	dir = filepath.Join(parent, hex.EncodeToString(sum.Sum(nil))[:hashChars])
	keep := func() {} // the cache's copy stays for the next session
	if _, err := os.Stat(filepath.Join(dir, scriptName)); err == nil {
		return dir, keep, nil
	}

	if err := os.MkdirAll(parent, supportMode); err != nil {
		return "", nil, err
	}

	tmp, err := os.MkdirTemp(parent, filepath.Base(dir)+tempSeparator)
	if err != nil {
		return "", nil, err
	}

	defer os.RemoveAll(tmp) // left only when the rename did not take it
	if err := writeSupport(tmp); err != nil {
		return "", nil, err
	}

	if err := os.Rename(tmp, dir); err != nil {
		if _, serr := os.Stat(filepath.Join(dir, scriptName)); serr != nil {
			return "", nil, err
		}
	}

	return dir, keep, nil // ours, or another build's of the same content
}

// writeSupport writes the script and the standard library's sources
// into dir.
func writeSupport(dir string) error {
	for name, script := range map[string][]byte{scriptName: debuggerScript, jsName: windowsScript} {
		if err := os.WriteFile(filepath.Join(dir, name), script, supportFileMode); err != nil {
			return err
		}
	}

	return fs.WalkDir(std.Sources, currentDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		to := filepath.Join(dir, stdDir, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(to, supportMode)
		}

		src, err := std.Sources.ReadFile(path)
		if err != nil {
			return err
		}

		return os.WriteFile(to, src, supportFileMode)
	})
}
