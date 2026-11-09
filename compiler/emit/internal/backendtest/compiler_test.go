// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package backendtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCountingCompiler checks that the counting compiler logs each run
// and compiles as the real one does, and that the count is of the
// runtime's compiles alone.
func TestCountingCompiler(t *testing.T) {
	cc, log := CountingCompiler(t, "cc")
	dir := t.TempDir()
	src := filepath.Join(dir, "sched.c")
	if err := os.WriteFile(src, []byte("int sn_dummy(void) { return 1; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		if out, err := exec.Command(cc, "-c", "-o", filepath.Join(dir, "sched.o"), src).CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}

	if out, err := exec.Command(cc, "--version").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if got := RuntimeCompiles(t, log); got != 2 {
		t.Errorf("RuntimeCompiles = %d, want 2", got)
	}

	if _, err := os.Stat(filepath.Join(dir, "sched.o")); err != nil {
		t.Errorf("the counting compiler compiled nothing: %v", err)
	}
}
