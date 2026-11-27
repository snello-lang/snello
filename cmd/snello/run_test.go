// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/internal/testproc"
)

// panicExit is the code a Snello program that panics exits with.
const panicExit = 2

// TestRunProgram runs programs as snello run does, through both
// backends: the arguments after the path and the input reach the
// program; its exit code is the command's, a panic's with its trace; a
// build that fails is the command's failure; a debug build runs; and
// nothing is left behind, in the directory the command runs in or the
// temporary one.
func TestRunProgram(t *testing.T) {
	root := writePrograms(t, map[string]string{
		"args/main.snello":   "package main\n\nimport (\"fmt\"; \"os\")\n\nfunc main() { fmt.Println(os.Args[1:]) }\n",
		"exits/main.snello":  "package main\n\nimport \"os\"\n\nfunc main() { os.Exit(3) }\n",
		"panics/main.snello": "package main\n\nfunc main() {\n\tpanic(\"boom\")\n}\n",
		"lines/main.snello":  "package main\n\nimport (\"bufio\"; \"fmt\"; \"os\")\n\nfunc main() {\n\tin := os.Stdin()\n\ts := bufio.NewScanner(mut in)\n\tn := 0\n\tfor s.Scan() { n += 1 }\n\tfmt.Println(n)\n}\n",
		"broken/main.snello": "package main\n\nfunc main() { x }\n",
	})
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	here := t.TempDir()
	t.Chdir(here)
	for _, backend := range []string{"llvm", "c"} {
		var out, errs strings.Builder
		if code, sig := runProgram(filepath.Join(root, "args"), backend, false, []string{"a", "-b"}, nil, &out, &errs, testproc.Command); code != exitSuccess || sig != nil || out.String() != "[a -b]\n" {
			t.Errorf("%s: args exited %d, %v, printed %q, said %q", backend, code, sig, out.String(), errs.String())
		}

		if code, _ := runProgram(filepath.Join(root, "exits"), backend, false, nil, nil, &out, &errs, testproc.Command); code != 3 {
			t.Errorf("%s: a program exiting 3 gave %d", backend, code)
		}

		errs.Reset()
		if code, _ := runProgram(filepath.Join(root, "panics"), backend, false, nil, nil, &out, &errs, testproc.Command); code != panicExit || !strings.Contains(errs.String(), "boom") || !strings.Contains(errs.String(), "main.snello:4") {
			t.Errorf("%s: a panic gave %d and said %q, want %d and its trace", backend, code, errs.String(), panicExit)
		}

		out.Reset()
		if code, _ := runProgram(filepath.Join(root, "lines"), backend, false, nil, strings.NewReader("a\nbb\nccc\n"), &out, &errs, testproc.Command); code != exitSuccess || out.String() != "3\n" {
			t.Errorf("%s: a program reading its input exited %d and printed %q", backend, code, out.String())
		}

		errs.Reset()
		if code, _ := runProgram(filepath.Join(root, "broken"), backend, false, nil, nil, &out, &errs, testproc.Command); code != exitFailure || !strings.Contains(errs.String(), "undefined: x") {
			t.Errorf("%s: a program that does not compile gave %d and said %q", backend, code, errs.String())
		}
	}

	var out strings.Builder
	if code, _ := runProgram(filepath.Join(root, "args"), "llvm", true, []string{"d"}, nil, &out, io.Discard, testproc.Command); code != exitSuccess || out.String() != "[d]\n" {
		t.Errorf("a debug build exited %d and printed %q", code, out.String())
	}

	for _, dir := range []string{tmp, here} {
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("run left %d entries in %s, %s first", len(entries), dir, entries[0].Name())
		}
	}
}

// TestRunByVersion runs a program that requires a library from a git
// repository at a tag, through both backends: the library is fetched
// into the module cache, the program builds against it and prints what
// it returns, and snello.lock beside the module file records it. A
// machine without git skips the test.
func TestRunByVersion(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git here")
	}

	t.Setenv(importer.ModCacheEnv, t.TempDir())
	repo := t.TempDir()
	gitIn(t, repo, "init", "--quiet", "--initial-branch", "main")
	if err := os.WriteFile(filepath.Join(repo, "greet.snello"), []byte("package greet\n\nfunc Hello() string { return \"hello from a tag\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	gitIn(t, repo, "add", "--all")
	gitIn(t, repo, "commit", "--quiet", "--message", "the library")
	gitIn(t, repo, "tag", "v0.1.0")
	prog := writePrograms(t, map[string]string{
		"app/" + importer.ModFile: "snello 0.1.0\n\nrequire greet " + fileURL(repo) + " 0.1.0\n",
		"app/main.snello":         "package main\n\nimport (\"fmt\"; \"greet\")\n\nfunc main() { fmt.Println(greet.Hello()) }\n",
	})
	app := filepath.Join(prog, "app")
	t.Chdir(t.TempDir())
	for _, backend := range []string{"llvm", "c"} {
		var out, errs strings.Builder
		if code, _ := runProgram(app, backend, false, nil, nil, &out, &errs, testproc.Command); code != exitSuccess || out.String() != "hello from a tag\n" {
			t.Errorf("%s: exited %d, printed %q, said %q", backend, code, out.String(), errs.String())
		}
	}

	if lock, err := os.ReadFile(filepath.Join(app, importer.LockFile)); err != nil || !strings.Contains(string(lock), " 0.1.0 ") || !strings.Contains(string(lock), " "+importer.HashPrefix) {
		t.Errorf("snello.lock: %q, %v", lock, err)
	}
}

// gitIn runs git in dir with an identity for its commits and no
// configuration of the machine's or the user's.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=snello-test", "GIT_AUTHOR_EMAIL=test@snello.invalid",
		"GIT_COMMITTER_NAME=snello-test", "GIT_COMMITTER_EMAIL=test@snello.invalid", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// fileURL returns the file URL of the directory dir, with the slash
// before a Windows volume that RFC 8089 writes.
func fileURL(dir string) string {
	p := filepath.ToSlash(dir)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}

	return "file://" + p
}

// TestRunCommand runs a program through the command line: the words
// after its path are its own, flags of run's included.
func TestRunCommand(t *testing.T) {
	root := writePrograms(t, map[string]string{"args/main.snello": "package main\n\nimport (\"fmt\"; \"os\")\n\nfunc main() { fmt.Println(os.Args[1:]) }\n"})
	var out, errs strings.Builder
	if code := runWith([]string{runCommand, "-backend", "c", filepath.Join(root, "args"), "-debug", "-backend", "x"}, &out, &errs, testproc.Command); code != exitSuccess || out.String() != "[-debug -backend x]\n" {
		t.Errorf("run exited %d, printed %q and said %q", code, out.String(), errs.String())
	}
}

// TestExitCode holds the exit code of a program's run: its own code,
// signalExitBase and the signal's number for one a signal ended, and a
// failure, said, for one that could not run.
func TestExitCode(t *testing.T) {
	if got := exitCode(nil, io.Discard); got != exitSuccess {
		t.Errorf("no error gave %d", got)
	}

	if got := exitCode(testproc.Command("sh", "-c", "exit 7").Run(), io.Discard); got != 7 {
		t.Errorf("exit 7 gave %d", got)
	}

	var said strings.Builder
	if got := exitCode(testproc.Command(filepath.Join(t.TempDir(), "none")).Run(), &said); got != exitFailure || said.Len() == 0 {
		t.Errorf("a program that does not exist gave %d and said %q", got, said.String())
	}

	needSignals(t)
	err := testproc.Command("sh", "-c", "kill -TERM $$").Run()
	if got := exitCode(err, io.Discard); got != signalExitBase+int(syscall.SIGTERM) {
		t.Errorf("a termination gave %d", got)
	}

	if sig, ok := signalOf(err); !ok || sig != syscall.SIGTERM {
		t.Errorf("signalOf gave %v, %v, want SIGTERM", sig, ok)
	}
}

// writePrograms writes files, by their paths under a new directory,
// and returns the directory.
func writePrograms(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, src := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return root
}
