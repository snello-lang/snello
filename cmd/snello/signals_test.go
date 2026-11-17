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
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// helperEnv names what the test binary, run again as a helper process,
// is to do: hold a work directory until a signal ends it, or raise a
// signal on itself.
const helperEnv = "SNELLO_SIGNALS_HELPER"

// helperDirEnv names the program a helper process runs as run does.
const helperDirEnv = "SNELLO_SIGNALS_PROGRAM"

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
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGQUIT} {
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

// TestRaise ends a process by an interrupt it raises on itself, as a
// shell must see it end.
func TestRaise(t *testing.T) {
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

// helper returns the command that runs the test binary as a helper
// process doing what.
func helper(t *testing.T, what string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), helperEnv+"="+what, "TMPDIR="+t.TempDir())
	return cmd
}
