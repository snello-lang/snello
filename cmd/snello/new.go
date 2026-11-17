// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/snello-lang/snello/compiler/importer"
)

const (
	// mainName names the program's file and its test's in a module new
	// starts, and newDirMode and newFileMode are the permissions of
	// what it writes: read, and the directory entered, by all, written
	// by the owner.
	mainName    = "main"
	newDirMode  = 0o755
	newFileMode = 0o644

	// changeDir is the shell's command that enters a directory, the
	// first of the commands new prints, and the rest are how a path is
	// written in it: as it is when it holds only shellSafe characters,
	// else between single quotes, a quote in it written as
	// quotedQuote, and after relativePrefix when it begins with
	// optionStart, so that cd does not read it as an option.
	changeDir      = "cd"
	shellSafe      = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_./-+:,@%"
	singleQuote    = "'"
	quotedQuote    = `'\''`
	relativePrefix = "./"
	optionStart    = "-"

	// mainSource is the program new writes, which prints a greeting,
	// and mainTestSource its test.
	mainSource = `package main

import "fmt"

// greeting returns the line main prints for name.
func greeting(name ref string) string {
	return "Hello, " + name + "!"
}

func main() {
	fmt.Println(greeting("Snello"))
}
`
	mainTestSource = `package main

import "expect"

func TestGreeting() {
	expect.Equal(ref greeting("Snello"), "Hello, Snello!")
}
`
)

// newModule starts a module in a directory it makes, dir, which must not
// exist, in one that does: the module file, written through
// importer.WriteModFile as init writes it, a program that prints a
// greeting, and its test. It says on w, standard error, what it wrote
// and the commands that enter it and build, run and test it, the path
// written as a shell reads it. A directory that exists is refused, and
// one new fails to fill is removed, so that it leaves a module whole or
// nothing. It makes one directory and writes three files.
func newModule(dir string, w io.Writer) int {
	dir = filepath.Clean(dir)
	if err := os.Mkdir(dir, newDirMode); err != nil {
		if errors.Is(err, fs.ErrExist) {
			err = errors.New(dir + " already exists")
		}

		return newFailed(w, err)
	}

	if err := writeModule(dir); err != nil {
		os.RemoveAll(dir) // the directory new made, and what it wrote in it
		return newFailed(w, err)
	}

	fmt.Fprintf(w, "%s %s: wrote %s, a module with a program and its test:\n\t%s %s\n", program, newCommand, dir, changeDir, shellPath(dir))
	for _, c := range []string{buildCommand, runCommand, testCommand} {
		fmt.Fprintf(w, "\t%s %s %s\n", program, c, currentDir)
	}

	return exitSuccess
}

// writeModule writes into dir the module file, the program and its
// test.
func writeModule(dir string) error {
	if _, err := importer.WriteModFile(dir); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(dir, mainName+importer.SourceExt), []byte(mainSource), newFileMode); err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, mainName+importer.TestFileSuffix), []byte(mainTestSource), newFileMode)
}

// shellPath returns path as a shell command line takes it: as it is
// when it holds only characters a shell reads as themselves, else
// between single quotes, and after ./ when it begins with -, which a
// command would read as an option.
func shellPath(path string) string {
	if strings.HasPrefix(path, optionStart) {
		path = relativePrefix + path
	}

	if strings.Trim(path, shellSafe) == "" {
		return path
	}

	return singleQuote + strings.ReplaceAll(path, singleQuote, quotedQuote) + singleQuote
}

// newFailed says on w why new failed, and returns exitFailure.
func newFailed(w io.Writer, err error) int {
	fmt.Fprintln(w, program+" "+newCommand+": "+err.Error())
	return exitFailure
}
