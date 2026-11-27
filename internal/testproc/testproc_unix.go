// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !windows

package testproc

import (
	"io"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"sync"
	"syscall"
)

const (
	// guardEnv, set in the guard's environment, marks the test binary
	// run as the guard, which runs no test.
	guardEnv = "SNELLO_TESTPROC_GUARD"

	// lifelineFD is the descriptor the guard reads its lifeline from,
	// the first after the standard three, as os/exec numbers ExtraFiles.
	lifelineFD = 3

	// listings bounds how many times the guard lists the processes
	// before it kills what it found: one per generation of processes
	// started under one another, far more than a test's programs have,
	// so that the guard ends whatever a listing shows.
	listings = 32
)

var (
	// mu guards the guard's state below.
	mu sync.Mutex

	// group is the process group the guard leads, which every program a
	// test runs joins; 0 when no guard runs, and the programs run as
	// exec runs them.
	group int

	// lifeline is the write end of the pipe the guard reads, held open
	// for the binary's life: its close, when the binary ends however it
	// ends, is the guard's end of file.
	lifeline *os.File
)

func init() {
	if os.Getenv(guardEnv) != "" {
		guard() // never returns
	}
}

// prepare puts cmd in the guard's process group, starting a guard when
// none runs, the first time or after one died.
func prepare(cmd *exec.Cmd) {
	mu.Lock()
	defer mu.Unlock()
	if group != 0 && exited(group) {
		group, lifeline = 0, nil // a guard ended early, a signal sent to it or its group: a new one guards from here
	}

	if group == 0 {
		startGuard()
	}

	if group == 0 {
		return
	}

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}

	cmd.SysProcAttr.Setpgid, cmd.SysProcAttr.Pgid = true, group
}

// exited reports whether the guard pid has ended, collecting it: a guard
// not collected would keep its group's number, which programs would go
// on joining with no guard watching.
func exited(pid int) bool {
	var status syscall.WaitStatus
	got, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	return got == pid || err != nil
}

// startGuard starts the guard: the test binary again, with guardEnv
// set, as the leader of a process group of its own, reading the read
// end of a pipe whose write end the binary keeps.
func startGuard() {
	exe, err := os.Executable()
	if err != nil {
		return
	}

	r, w, err := os.Pipe()
	if err != nil {
		return
	}

	g := exec.Command(exe)
	g.Env = append(os.Environ(), guardEnv+"=1")
	g.ExtraFiles = []*os.File{r}
	g.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err = g.Start()
	r.Close()
	if err != nil {
		w.Close()
		return
	}

	group, lifeline = g.Process.Pid, w
}

// guard waits for the end of its lifeline, the test binary gone however
// it went, then ends every program the binary ran, what they started
// and itself. The signals that end a process are ignored, so that one
// sent to the group, or a terminal's interrupt, leaves the guard to its
// work.
func guard() {
	signal.Ignore(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	io.Copy(io.Discard, os.NewFile(lifelineFD, "lifeline")) // the binary writes nothing: this returns when its end closes
	end(os.Getpid())
	os.Exit(0)
}

// end kills the process group the guard self leads and every process
// descended from one of its members, in whatever group: a debugger's
// server and the program it debugs each move to a group of their own.
// Each is stopped as it is found, so that none starts another or ends
// and leaves its children to the system, and the processes are listed
// again until a listing finds none more, at most listings times; then
// all are killed.
func end(self int) {
	var found []int
	for i, more := 0, true; more && i < listings; i++ {
		more = false
		for _, p := range processes() {
			if p.pid == self || p.zombie || slices.Contains(found, p.pid) || p.pgid != self && !slices.Contains(found, p.ppid) {
				continue // a zombie has ended and starts nothing, and its number may go to another process once it is collected
			}

			_ = syscall.Kill(p.pid, syscall.SIGSTOP)
			found, more = append(found, p.pid), true
		}
	}

	for _, pid := range found {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}

	_ = syscall.Kill(-self, syscall.SIGKILL)
}

// A process is one the system runs: its id, its parent's and its
// group's, and whether it has ended and waits to be collected.
type process struct {
	pid, ppid, pgid int
	zombie          bool
}
