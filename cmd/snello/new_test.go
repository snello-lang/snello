// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/version"
)

// TestNewModule starts a module as snello new does: the module file
// with the compiler's version, the program and its test, and the
// commands that enter, build, run and test it; the module's tests pass
// and its program runs, through both backends; a directory that exists
// is refused and left as it was.
func TestNewModule(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hello")
	var said strings.Builder
	if code := run([]string{newCommand, dir}, io.Discard, &said); code != exitSuccess {
		t.Fatalf("new exited %d and said %q", code, said.String())
	}

	want := "snello new: wrote " + dir + ", a module with a program and its test:\n\tcd " + dir + "\n\tsnello build .\n\tsnello run .\n\tsnello test .\n"
	if said.String() != want {
		t.Errorf("new said %q, want %q", said.String(), want)
	}

	for name, text := range map[string]string{"snello.mod": "snello " + version.Current + "\n", "main.snello": mainSource, "main_test.snello": mainTestSource} {
		if src, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(src) != text {
			t.Errorf("%s holds %q, %v", name, src, err)
		}
	}

	for _, backend := range []string{"llvm", "c"} {
		var out, errs strings.Builder
		if code := run([]string{testCommand, "-backend", backend, dir}, &out, &errs); code != exitSuccess || !strings.HasPrefix(out.String(), "ok  \thello\t") {
			t.Errorf("%s: the module's tests exited %d, printed %q and said %q", backend, code, out.String(), errs.String())
		}

		out.Reset()
		if code := run([]string{runCommand, "-backend", backend, dir}, &out, &errs); code != exitSuccess || out.String() != "Hello, Snello!\n" {
			t.Errorf("%s: the module's program exited %d and printed %q", backend, code, out.String())
		}
	}

	said.Reset()
	if code := run([]string{newCommand, dir}, io.Discard, &said); code != exitFailure || said.String() != "snello new: "+dir+" already exists\n" {
		t.Errorf("a second new exited %d and said %q", code, said.String())
	}

	if src, _ := os.ReadFile(filepath.Join(dir, "main.snello")); string(src) != mainSource {
		t.Error("a refused new changed the module")
	}

	parent := t.TempDir()
	said.Reset()
	slash := filepath.Join(parent, "slash")
	if code := run([]string{newCommand, slash + "/"}, io.Discard, &said); code != exitSuccess || !strings.HasPrefix(said.String(), "snello new: wrote "+slash+",") {
		t.Errorf("new with a trailing slash exited %d and said %q", code, said.String())
	}

	if code := run([]string{newCommand, filepath.Join(parent, "none", "deep")}, io.Discard, io.Discard); code != exitFailure {
		t.Errorf("new under a directory that does not exist exited %d", code)
	}

	if _, err := os.Stat(filepath.Join(parent, "none")); !os.IsNotExist(err) {
		t.Errorf("new under a directory that does not exist made it: %v", err)
	}
}

// TestShellPath holds the cd line's path to what a shell reads as that
// path: plain when it is plain, quoted when it holds a space or a quote,
// and after ./ when it begins with -.
func TestShellPath(t *testing.T) {
	for path, want := range map[string]string{
		"hello":      "hello",
		"apps/hello": "apps/hello",
		"/abs/x-1.0": "/abs/x-1.0",
		"my app":     "'my app'",
		"it's":       `'it'\''s'`,
		"-dash":      "./-dash",
		"-my app":    "'./-my app'",
		"$HOME":      "'$HOME'",
	} {
		if got := shellPath(path); got != want {
			t.Errorf("shellPath(%q) = %q, want %q", path, got, want)
		}
	}
}
