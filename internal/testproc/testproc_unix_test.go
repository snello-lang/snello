// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !windows

package testproc

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestPrepare puts a program in the guard's process group, which the
// guard leads.
func TestPrepare(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true command")
	}

	cmd := Command("true")
	if group == 0 {
		t.Fatal("the guard did not start")
	}

	if a := cmd.SysProcAttr; a == nil || !a.Setpgid || a.Pgid != group {
		t.Fatalf("SysProcAttr = %+v, want the group %d", a, group)
	}

	if err := cmd.Run(); err != nil { // a program joins the guard's group and runs
		t.Fatal(err)
	}
}

// TestGuardRestarts starts a new guard for the next program when the
// guard was killed, so that no program joins a group nobody watches.
func TestGuardRestarts(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true command")
	}

	Command("true")
	old := group
	if old == 0 {
		t.Fatal("the guard did not start")
	}

	_ = syscall.Kill(old, syscall.SIGKILL)
	for deadline := time.Now().Add(goneWithin); alive(old) && !zombie(old); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the guard %d did not end", old)
		}
	}

	cmd := Command("true")
	if group == 0 || group == old {
		t.Fatalf("group = %d after the guard %d was killed, want a new guard's", group, old)
	}

	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
}

// TestHelperEscaper is the helper and the escaper TestOutlivesItsGroup
// runs, and nothing when run as a test: the helper starts the escaper
// through Command, prints the pid the escaper prints and ends at once;
// the escaper starts a sleeper in a process group of its own, as a
// debugger starts the program it debugs, prints its pid and waits.
func TestHelperEscaper(t *testing.T) {
	switch os.Getenv(roleEnv) {
	case "escaper-helper":
		cmd := Command(os.Args[0], "-test.run=^TestHelperEscaper$")
		cmd.Env = append(os.Environ(), roleEnv+"=escaper")
		out, err := cmd.StdoutPipe()
		if err != nil || cmd.Start() != nil {
			os.Exit(2)
		}

		line, err := bufio.NewReader(out).ReadString('\n')
		if err != nil {
			os.Exit(2)
		}

		fmt.Print(line)
		os.Exit(0) // ends without waiting, as a binary a timeout or a kill ends
	case "escaper":
		sleeper := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
		sleeper.Env = append(os.Environ(), roleEnv+"=sleeper")
		sleeper.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := sleeper.Start(); err != nil {
			os.Exit(2)
		}

		fmt.Println(sleeper.Process.Pid)
		time.Sleep(sleepFor)
		os.Exit(0)
	}
}

// TestOutlivesItsGroup wants a program that a program a test ran started
// in a process group of its own gone soon after the test binary: the
// guard ends what its group's programs started, wherever they went.
func TestOutlivesItsGroup(t *testing.T) {
	wantGone(t, helperPid(t, "^TestHelperEscaper$", "escaper-helper"))
}

// wantListed wants the test binary among procs, with its parent, its
// group and as running.
func wantListed(t *testing.T, procs []process) {
	t.Helper()
	want := process{os.Getpid(), os.Getppid(), syscall.Getpgrp(), false}
	for _, p := range procs {
		if p.pid == want.pid {
			if p != want {
				t.Fatalf("processes() has %+v, want %+v", p, want)
			}

			return
		}
	}

	t.Fatalf("processes() does not list the test binary, %d", want.pid)
}

// alive reports whether the process pid still runs, or has ended and
// not been collected.
func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// zombie reports whether the process pid, a child of the test binary,
// has ended, without collecting it.
func zombie(pid int) bool {
	var info syscall.Rusage
	var status syscall.WaitStatus
	got, _ := syscall.Wait4(pid, &status, syscall.WNOHANG|syscall.WNOWAIT, &info)
	return got == pid
}

// kill ends the process pid, which a failing test leaves.
func kill(pid int) { _ = syscall.Kill(pid, syscall.SIGKILL) }
