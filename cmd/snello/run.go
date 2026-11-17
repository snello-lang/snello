// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
)

// runProgram builds the program at path, a file or a directory, through
// backend, runs it with args, the arguments after its path, on the
// command's stdin, stdout and stderr, and returns its exit code: the
// program's own, signalExitBase and the signal's number for a program a
// signal ended, which it returns too for the command to end by, or
// exitFailure for a build that failed or a program that did not start.
// The binary's directory is removed when the program ends, and the
// signals that reach the command while it runs are answered as workDir
// says, so that run leaves no file behind however the program ends and
// the program never runs on without the command. debug asks for the
// runtime's checks.
func runProgram(path, backend string, debug bool, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, os.Signal) {
	bin, _, work, ok := compile(path, backend, debug, stderr)
	if !ok {
		return exitFailure, nil
	}

	defer work.remove()
	cmd := exec.Command(bin, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	work.start()
	if err := cmd.Start(); err != nil {
		work.hand(nil)
		fmt.Fprintln(stderr, err)
		return exitFailure, nil
	}

	work.hand(cmd.Process)
	err := cmd.Wait()
	if sig, ok := signalOf(err); ok {
		return signalExitBase + int(sig), sig
	}

	return exitCode(err, stderr), nil
}

// exitCode returns the exit code of a program whose run ended with err:
// 0 for none, the program's own code, or signalExitBase and the
// signal's number for a program a signal ended; a program that could not
// be waited for is reported on stderr, with exitFailure.
func exitCode(err error, stderr io.Writer) int {
	if err == nil {
		return exitSuccess
	}

	if sig, ok := signalOf(err); ok {
		return signalExitBase + int(sig)
	}

	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}

	return exit.ExitCode()
}

// signalOf returns the signal that ended a program whose run ended with
// err, and whether one did.
func signalOf(err error) (syscall.Signal, bool) {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return 0, false
	}

	status, ok := exit.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return 0, false
	}

	return status.Signal(), true
}
