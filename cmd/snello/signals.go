// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const (
	// signalExitBase is added to the number of a signal to give the
	// exit code of a command it ended, when the command cannot end by
	// the signal itself, as a shell reports a process a signal ended.
	signalExitBase = 128

	// raiseWait is how long raise waits to be ended by the signal it
	// sent itself before it exits with the signal's code instead.
	raiseWait = time.Second
)

// endingSignals are the signals that end a command unless it catches
// them: an interrupt and a quit, which the terminal sends to every
// process of the command line, a hangup and a termination.
var endingSignals = []os.Signal{os.Interrupt, syscall.SIGQUIT, syscall.SIGHUP, syscall.SIGTERM}

// A workDir is the temporary directory a command builds in, guarded
// against the signals that would end the command, by one goroutine,
// until it is removed. While the command builds, such a signal removes
// the directory and then ends the command as the signal would have, so
// that an interrupted build leaves nothing behind. Once the command
// hands it the program it runs from the directory, a termination or a
// hangup is passed on to the program, so that it is never left running
// without the command, and an interrupt or a quit, which the terminal
// sends the program as well, is the program's to answer: the command
// waits for it to end and then removes the directory. The signals are
// caught and not ignored, since an ignored signal stays ignored in the
// program the command starts, and a caught one does not.
type workDir struct {
	path    string
	signals chan os.Signal
	program chan *os.Process
	done    chan struct{}
	once    sync.Once
}

// newWorkDir makes a temporary directory named after pattern and starts
// guarding it. It makes one directory and starts one goroutine, which
// remove ends.
func newWorkDir(pattern string) (*workDir, error) {
	path, err := os.MkdirTemp("", pattern)
	if err != nil {
		return nil, err
	}

	w := &workDir{path: path, signals: make(chan os.Signal, 1), program: make(chan *os.Process, 1), done: make(chan struct{})}
	signal.Notify(w.signals, endingSignals...)
	go w.guard()
	return w, nil
}

// hand gives the directory's guard the program p the command has
// started from it, which the signals then go to as workDir says.
func (w *workDir) hand(p *os.Process) {
	w.program <- p
}

// remove stops the guard and removes the directory; it may be called
// more than once.
func (w *workDir) remove() {
	w.once.Do(func() {
		signal.Stop(w.signals)
		close(w.done)
		os.RemoveAll(w.path)
	})
}

// guard answers the signals that reach the command until remove: before
// a program is handed to it by removing the directory and ending the
// command, after by passing a termination or a hangup on to the program.
func (w *workDir) guard() {
	var p *os.Process
	for {
		select {
		case p = <-w.program:
		case s := <-w.signals:
			if p == nil {
				os.RemoveAll(w.path)
				raise(s)
			}

			if s == syscall.SIGTERM || s == syscall.SIGHUP {
				p.Signal(s)
			}
		case <-w.done:
			return
		}
	}
}

// raise ends the command by s, as s's default action would, so that the
// shell that ran it sees a command a signal ended and, for an
// interrupt, ends a script too rather than go on to its next line. An
// interrupt, a hangup and a termination end it so; any other signal
// ends it with signalExitBase and the signal's number, as does one that
// has not ended it within raiseWait.
func raise(s os.Signal) {
	sig, ok := s.(syscall.Signal)
	if !ok {
		os.Exit(exitFailure)
	}

	if sig == syscall.SIGINT || sig == syscall.SIGHUP || sig == syscall.SIGTERM {
		signal.Reset(sig)
		syscall.Kill(os.Getpid(), sig)
		time.Sleep(raiseWait)
	}

	os.Exit(signalExitBase + int(sig))
}
