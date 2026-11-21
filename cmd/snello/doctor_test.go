// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/toolchain"
)

// TestDoctor prints one line a check and exits as a build would: with
// nothing to build with, a failure for each compiler with its fix under
// it; with clang there, success.
func TestDoctor(t *testing.T) {
	t.Setenv(toolchain.ClangEnv, "")
	t.Setenv(toolchain.CCEnv, "")
	path := os.Getenv("PATH")
	t.Setenv("PATH", t.TempDir())
	var out strings.Builder
	if code := doctor(filepath.Join(t.TempDir(), "snello"), &out); code != exitFailure {
		t.Errorf("doctor with no compiler exited %d", code)
	}

	want := "fail  clang: clang not found\n      " + toolchain.InstallFix(toolchain.ClangEnv) + "\n"
	if !strings.HasPrefix(out.String(), want) || !strings.Contains(out.String(), "note  snello on the path:") {
		t.Errorf("doctor with no compiler printed\n%s\nwant it to begin\n%s", out.String(), want)
	}

	t.Setenv("PATH", path)
	if _, err := exec.LookPath(toolchain.Clang()); err != nil {
		t.Skip("no clang here")
	}

	out.Reset()
	if code := doctor(self(), &out); code != exitSuccess || !strings.HasPrefix(out.String(), "ok    clang: ") {
		t.Errorf("doctor with clang exited %d and printed\n%s", code, out.String())
	}
}
