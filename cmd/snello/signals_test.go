// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	// helperEnv names what the test binary, run again as a helper
	// process, is to do: hold a work directory until a signal ends it,
	// raise a signal on itself, or another of TestHelperProcess's cases;
	// helperDirEnv names the program a helper runs as run does; and
	// startWindowRounds is how many times the start window is
	// signalled into.
	helperEnv         = "SNELLO_SIGNALS_HELPER"
	helperDirEnv      = "SNELLO_SIGNALS_PROGRAM"
	startWindowRounds = 20

	// ignoredWait is how long a process is given to be ended by a
	// signal it was started ignoring, which it never is.
	ignoredWait = 300 * time.Millisecond
)

// TestWorkDir holds a work directory to its removal: there until it,
// gone after, and removed twice without harm.
func TestWorkDir(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	w, err := newWorkDir(buildDirPattern)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(w.path); err != nil {
		t.Fatalf("the work directory is not there: %v", err)
	}

	w.remove()
	w.remove()
	if _, err := os.Stat(w.path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the work directory is left after its release: %v", err)
	}
}

// TestWorkDirSignal ends a process holding a work directory with each
// signal that ends a command: the directory is removed, and the process
// ends by an interrupt, a hangup or a termination as their default
// would, and by a quit with its code, no goroutine dump.
func TestWorkDirSignal(t *testing.T) {
	needSignals(t)
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGQUIT} {
		if !slices.Contains(caughtSignals, os.Signal(sig)) {
			continue // ignored by whatever started the tests, and so by the helper, which TestIgnoredSignal covers
		}

		cmd := helper(t, "hold")
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}

		var said strings.Builder
		cmd.Stderr = &said
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		dir, err := bufio.NewReader(out).ReadString('\n')
		if err != nil {
			t.Fatalf("%v: the helper said no directory: %v", sig, err)
		}

		dir = strings.TrimSpace(dir)
		cmd.Process.Signal(sig)
		err = cmd.Wait()
		if _, serr := os.Stat(dir); !errors.Is(serr, os.ErrNotExist) {
			t.Errorf("%v: the work directory is left: %v", sig, serr)
		}

		got, ended := signalOf(err)
		switch {
		case sig == syscall.SIGQUIT:
			if ended || exitCode(err, &said) != signalExitBase+int(sig) || strings.Contains(said.String(), "goroutine") {
				t.Errorf("%v: ended by %v (%v) and said %q, want code %d", sig, got, ended, said.String(), signalExitBase+int(sig))
			}
		case !ended || got != sig:
			t.Errorf("%v: ended by %v, %v", sig, got, err)
		}
	}
}

// TestWorkDirForward ends, with a termination, a process that runs a
// program from its work directory: the termination is passed on to the
// program, the process outlives it and reports the program's end, and
// the directory is removed.
func TestWorkDirForward(t *testing.T) {
	needSignals(t)
	cmd := helper(t, "forward")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	dir, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("the helper said no directory: %v", err)
	}

	cmd.Process.Signal(syscall.SIGTERM)
	err = cmd.Wait()
	if _, ended := signalOf(err); ended || exitCode(err, io.Discard) != signalExitBase+int(syscall.SIGTERM) {
		t.Errorf("the helper ended with %v, want it to report the program's termination", err)
	}

	if _, serr := os.Stat(strings.TrimSpace(dir)); !errors.Is(serr, os.ErrNotExist) {
		t.Errorf("the work directory is left: %v", serr)
	}
}

// TestRunForward ends, with a termination, the command running a
// program as run does: the program gets the termination, and the
// command outlives it, reports its end and leaves no file behind.
func TestRunForward(t *testing.T) {
	needSignals(t)
	if testing.Short() {
		t.Skip("builds a program")
	}

	root := writePrograms(t, map[string]string{"sleeps/main.snello": "package main\n\nimport (\"fmt\"; \"time\")\n\nfunc main() {\n\tfmt.Println(\"ready\")\n\ttime.Sleep(60 * time.Second)\n}\n"})
	tmp := t.TempDir()
	cmd := helper(t, "run")
	cmd.Env = append(cmd.Env, "TMPDIR="+tmp, helperDirEnv+"="+filepath.Join(root, "sleeps"))
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	if line, err := bufio.NewReader(out).ReadString('\n'); err != nil || line != "ready\n" {
		cmd.Process.Kill()
		t.Fatalf("the program said %q, %v", line, err)
	}

	cmd.Process.Signal(syscall.SIGTERM)
	err = cmd.Wait()
	if _, ended := signalOf(err); ended || exitCode(err, io.Discard) != signalExitBase+int(syscall.SIGTERM) {
		t.Errorf("the command ended with %v, want it to report the program's termination", err)
	}

	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Errorf("run left %s in the temporary directory", entries[0].Name())
	}
}

// TestStartWindow signals a process between telling its work
// directory's guard that a program is starting and handing it the
// program, round after round: the guard holds the termination and
// passes it on to the program once it runs, and the process lives on;
// and a start that fails ends the process by the signal it held, with
// the directory removed.
func TestStartWindow(t *testing.T) {
	needSignals(t)
	if err := helper(t, "window").Run(); err != nil {
		t.Errorf("a signal in the start window: %v", err)
	}

	cmd := helper(t, "failedstart")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	dir, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("the helper said no directory: %v", err)
	}

	err = cmd.Wait()
	if sig, ok := signalOf(err); !ok || sig != syscall.SIGTERM {
		t.Errorf("a failed start with a signal held ended with %v, want the termination", err)
	}

	if _, serr := os.Stat(strings.TrimSpace(dir)); !errors.Is(serr, os.ErrNotExist) {
		t.Errorf("the work directory is left: %v", serr)
	}
}

// TestIgnoredSignal starts a process holding a work directory with a
// hangup ignored, as nohup starts a command: the hangup leaves it
// running and its directory in place, since a command started ignoring
// a signal goes on ignoring it, and a termination still ends it,
// directory removed.
func TestIgnoredSignal(t *testing.T) {
	needSignals(t)
	hold := helper(t, "hold")
	cmd := exec.Command("sh", "-c", `trap "" HUP; exec "$0" "$@"`, hold.Path)
	cmd.Args = append(cmd.Args, hold.Args[1:]...)
	cmd.Env = hold.Env
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	dir, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("the helper said no directory: %v", err)
	}

	dir = strings.TrimSpace(dir)
	cmd.Process.Signal(syscall.SIGHUP)
	time.Sleep(ignoredWait)
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Errorf("a hangup ended a command started ignoring it: %v", err)
	}

	if _, err := os.Stat(dir); err != nil {
		t.Errorf("a hangup removed the work directory of a command started ignoring it: %v", err)
	}

	cmd.Process.Signal(syscall.SIGTERM)
	err = cmd.Wait()
	if sig, ok := signalOf(err); !ok || sig != syscall.SIGTERM {
		t.Errorf("a termination ended it with %v", err)
	}

	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the work directory is left: %v", err)
	}
}

// TestRaise ends a process by an interrupt it raises on itself, as a
// shell must see it end.
func TestRaise(t *testing.T) {
	needSignals(t)
	err := helper(t, "raise").Run()
	if sig, ok := signalOf(err); !ok || sig != syscall.SIGINT {
		t.Errorf("the helper ended with %v, want an interrupt", err)
	}
}

// TestHelperProcess is the helper process the signal tests run, which
// does what helperEnv names and nothing when run as a test.
func TestHelperProcess(t *testing.T) {
	switch os.Getenv(helperEnv) {
	case "forward":
		w, err := newWorkDir(buildDirPattern)
		if err != nil {
			os.Exit(exitFailure)
		}

		program := exec.Command("sleep", "60")
		w.start()
		if err := program.Start(); err != nil {
			os.Exit(exitFailure)
		}

		w.hand(program.Process)
		os.Stdout.WriteString(w.path + "\n")
		err = program.Wait()
		w.remove()
		os.Exit(exitCode(err, io.Discard))
	case "run":
		code, _ := runProgram(os.Getenv(helperDirEnv), "llvm", false, nil, nil, os.Stdout, os.Stderr)
		os.Exit(code)
	case "window":
		for range startWindowRounds {
			w, err := newWorkDir(buildDirPattern)
			if err != nil {
				os.Exit(exitFailure)
			}

			w.start()
			signalSelf(syscall.SIGTERM) // before the program exists: held, then passed on
			program := exec.Command("sleep", "60")
			if err := program.Start(); err != nil {
				os.Exit(exitFailure)
			}

			w.hand(program.Process)
			if sig, ok := signalOf(program.Wait()); !ok || sig != syscall.SIGTERM {
				os.Exit(exitFailure)
			}

			w.remove()
		}

		os.Exit(exitSuccess)
	case "failedstart":
		w, err := newWorkDir(buildDirPattern)
		if err != nil {
			os.Exit(exitFailure)
		}

		os.Stdout.WriteString(w.path + "\n")
		w.start()
		signalSelf(syscall.SIGTERM)
		time.Sleep(100 * time.Millisecond) // the signal reaches the guard, which holds it
		w.hand(nil)
		time.Sleep(time.Minute)
		os.Exit(exitFailure)
	case "raise":
		raise(syscall.SIGINT)
	case "hold":
		w, err := newWorkDir(buildDirPattern)
		if err != nil {
			os.Exit(exitFailure)
		}

		os.Stdout.WriteString(w.path + "\n")
		time.Sleep(time.Minute)
		os.Exit(exitFailure)
	}
}

// TestSignalSelf holds signalSelf to delivering a signal to the command
// itself where the system has signals, and to sending none on Windows.
func TestSignalSelf(t *testing.T) {
	if goruntime.GOOS == windows {
		if signalSelf(syscall.SIGINT) {
			t.Error("signalSelf sent a signal on Windows, which has none")
		}

		return
	}

	got := make(chan os.Signal, 1)
	signal.Notify(got, syscall.SIGHUP)
	defer signal.Stop(got)
	if !signalSelf(syscall.SIGHUP) {
		t.Fatal("signalSelf could not send SIGHUP")
	}

	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Error("SIGHUP never came")
	}
}

// needSignals skips a test of the signals a terminal and a shell send,
// which Windows has none of.
func needSignals(t *testing.T) {
	t.Helper()
	if goruntime.GOOS == windows {
		t.Skip("Windows has no signals")
	}
}

// helper returns the command that runs the test binary as a helper
// process doing what.
func helper(t *testing.T, what string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), helperEnv+"="+what, "TMPDIR="+t.TempDir())
	return cmd
}
