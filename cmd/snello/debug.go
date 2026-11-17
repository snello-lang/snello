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
	stdDir      = "std"
	hashChars   = 16
	supportTemp = "snello-debug-"

	// lldb is the debugger macOS ships, and gdb the one Linux's
	// distributions do; darwin names macOS.
	lldb   = "lldb"
	gdb    = "gdb"
	darwin = "darwin"

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

// debug builds the program at path, a file or a directory, as build
// -debug does through backend, and runs the platform's debugger on the
// binary, with args, the arguments after the program's path, for the
// program, Snello's script loaded and the standard library's
// sources where the debugger looks for them: the command loads the
// script, since neither debugger loads one a binary names unless the
// user has trusted its place. It returns the debugger's exit code.
func debug(path, backend string, args []string) int {
	_, isDir, ok := programFiles(path, os.Stderr)
	if !ok {
		return exitFailure
	}

	if name := platformDebugger(); !onPath(name) { // before a build there is no debugger to run
		fmt.Fprintf(os.Stderr, "%s %s: %s is not on the path\n", program, debugCommand, name)
		return exitFailure
	}

	if code := build(path, backend, true); code != 0 {
		return code
	}

	support, done, err := debugSupport()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFailure
	}

	defer done()
	name, cmdArgs := debuggerCommand(support, currentDir+string(filepath.Separator)+programName(path, isDir), args)

	cmd := exec.Command(name, cmdArgs...)
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
// lldb on macOS, gdb on Linux.
func platformDebugger() string {
	if goruntime.GOOS == darwin {
		return lldb
	}

	return gdb
}

// onPath reports whether the command name can be run.
func onPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// debuggerCommand is the platform's debugger and its arguments: bin,
// run with args, the script in support loaded, and the standard
// library's files, which the debugging information names under
// lower.StdSources, read from support's copy.
func debuggerCommand(support, bin string, args []string) (string, []string) {
	script := filepath.Join(support, scriptName)
	prefix := strings.TrimSuffix(lower.StdSources, stdPathEnd)
	sources := filepath.Join(support, stdDir)
	if platformDebugger() == lldb {
		return lldb, append([]string{lldbCommand, lldbImport + strconv.Quote(script), lldbCommand, lldbSourceMap + prefix + argSeparator + strconv.Quote(sources), lldbProgram, bin}, args...)
	}

	return gdb, append([]string{gdbScript, script, gdbCommand, gdbSubstitutePath + prefix + argSeparator + strconv.Quote(sources), gdbProgram, bin}, args...)
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
	if err := os.WriteFile(filepath.Join(dir, scriptName), debuggerScript, supportFileMode); err != nil {
		return err
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
