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
// process of the command line, a hangup and a termination; and
// caughtSignals those of them the command answers, read once as it
// starts: the ones it was not started ignoring. A command started with
// one ignored is to go on ignoring it, and so is the program it runs,
// which inherits the ignoring: a shell's background job its interrupt
// and quit, a command under nohup its hangup.
var (
	endingSignals = []os.Signal{os.Interrupt, syscall.SIGQUIT, syscall.SIGHUP, syscall.SIGTERM}
	caughtSignals = notIgnored(endingSignals)
)

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
	path     string
	signals  chan os.Signal
	starting chan struct{}
	program  chan *os.Process
	done     chan struct{}
	once     sync.Once
}

// newWorkDir makes a temporary directory named after pattern and starts
// guarding it. It makes one directory and starts one goroutine, which
// remove ends.
func newWorkDir(pattern string) (*workDir, error) {
	path, err := os.MkdirTemp("", pattern)
	if err != nil {
		return nil, err
	}

	w := &workDir{path: path, signals: make(chan os.Signal, 1), starting: make(chan struct{}), program: make(chan *os.Process), done: make(chan struct{})}
	if len(caughtSignals) > 0 { // with none, Notify would relay every signal
		signal.Notify(w.signals, caughtSignals...)
	}
	go w.guard()
	return w, nil
}

// start tells the directory's guard that the command is about to start
// a program from it, and returns once the guard knows: from then on the
// guard holds the signals that reach the command, until hand gives it
// the program or says the start failed, so that a signal is never taken
// for one that came while the command built, with a program already
// running, nor lost between the start and the hand.
func (w *workDir) start() {
	w.starting <- struct{}{}
}

// hand gives the directory's guard the program p the command started
// from it, or nil for a start that failed, and returns once the guard
// has it: the signals held since start go to the program, every one,
// since a program that did not exist yet when the terminal sent one did
// not receive it; with no program, the first ends the command as it
// would have while the command built.
func (w *workDir) hand(p *os.Process) {
	w.program <- p
}

// Dir, Starting and Started are the directory's path, start and hand,
// by the names the test runner asks for a directory it is to build a
// test program in and run it from.
func (w *workDir) Dir() string           { return w.path }
func (w *workDir) Starting()             { w.start() }
func (w *workDir) Started(p *os.Process) { w.hand(p) }

// remove stops the guard and removes the directory; it may be called
// more than once.
func (w *workDir) remove() {
	w.once.Do(func() {
		signal.Stop(w.signals)
		close(w.done)
		os.RemoveAll(w.path)
	})
}

// guard answers the signals that reach the command until remove: while
// the command builds by removing the directory and ending the command,
// while it starts a program by holding them, and once the program runs
// by passing a termination or a hangup on to it, the rest being the
// program's own from the terminal.
func (w *workDir) guard() {
	var p *os.Process
	var held []os.Signal
	starting := false
	for {
		select {
		case <-w.starting:
			starting = true
		case p = <-w.program:
			starting = false
			if p == nil && len(held) > 0 {
				w.end(held[0])
			}

			for _, s := range held {
				p.Signal(s)
			}

			held = nil
		case s := <-w.signals:
			switch {
			case p != nil:
				if s == syscall.SIGTERM || s == syscall.SIGHUP {
					p.Signal(s)
				}
			case starting:
				held = append(held, s)
			default:
				w.end(s)
			}
		case <-w.done:
			return
		}
	}
}

// end removes the directory and ends the command by s, for a signal that
// came while no program ran from the directory.
func (w *workDir) end(s os.Signal) {
	os.RemoveAll(w.path)
	raise(s)
}

// notIgnored returns the signals of sigs the process was not started
// ignoring, which it may catch.
func notIgnored(sigs []os.Signal) []os.Signal {
	var out []os.Signal
	for _, s := range sigs {
		if !signal.Ignored(s) {
			out = append(out, s)
		}
	}

	return out
}

// raise ends the command by s, as s's default action would, so that the
// shell that ran it sees a command a signal ended and, for an
// interrupt, ends a script too rather than go on to its next line. An
// interrupt, a hangup and a termination end it so where the system has
// signals; any other signal, every one on Windows, ends it with
// signalExitBase and the signal's number, as does one that has not
// ended it within raiseWait.
func raise(s os.Signal) {
	sig, ok := s.(syscall.Signal)
	if !ok {
		os.Exit(exitFailure)
	}

	if sig == syscall.SIGINT || sig == syscall.SIGHUP || sig == syscall.SIGTERM {
		signal.Reset(sig)
		if signalSelf(sig) {
			time.Sleep(raiseWait)
		}
	}

	os.Exit(signalExitBase + int(sig))
}

// signalSelf sends sig to the command itself and reports whether it
// could: never on Windows, which has no signals to send, as its
// process's Signal says, so that raise ends the command by its exit
// code there at once.
func signalSelf(sig syscall.Signal) bool {
	self, err := os.FindProcess(os.Getpid())
	return err == nil && self.Signal(sig) == nil
}
