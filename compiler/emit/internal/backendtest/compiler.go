// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package backendtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// CountingCompiler writes a compiler into a directory of its own that
// logs each run's arguments, one line a run, and hands them on to the
// real compiler named, cc or clang; it returns the compiler and the
// log. The tests of the runtime cache count the compiler's runs
// through it. The test is skipped when the real compiler is missing.
func CountingCompiler(t *testing.T, real string) (cc, log string) {
	t.Helper()
	path, err := exec.LookPath(real)
	if err != nil {
		t.Skipf("no %s on the PATH", real)
	}

	dir := t.TempDir()
	log = filepath.Join(dir, "runs")
	cc = filepath.Join(dir, real)
	script := "#!/bin/sh\necho \"$@\" >> '" + log + "'\nexec '" + path + "' \"$@\"\n"
	if err := os.WriteFile(cc, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	return cc, log
}

// RuntimeCompiles counts the runs in a counting compiler's log that
// compiled a file of the runtime: one file stands for the runtime,
// since its files are compiled together.
func RuntimeCompiles(t *testing.T, log string) int {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		return 0
	}

	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		args := strings.Fields(line)
		if slices.Contains(args, "-c") && slices.ContainsFunc(args, func(a string) bool { return strings.HasSuffix(a, "/sched.c") }) {
			n++
		}
	}

	return n
}
