// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"os/exec"
	"testing"

	"github.com/teak-lang/teak/compiler/emit/internal/backendtest"
	"github.com/teak-lang/teak/compiler/importer"
	"github.com/teak-lang/teak/compiler/parser"
	"github.com/teak-lang/teak/compiler/types"
)

// TestChannelsFlat runs backendtest.Churn and checks that its memory
// stays flat and that a debug build of it reports no block left.
func TestChannelsFlat(t *testing.T) {
	if testing.Short() {
		t.Skip("a million channels")
	}

	f, file, err := parser.ParseFile("churn.teak", []byte(backendtest.Churn))
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{}
	imp := importer.NewImporter(info)
	if _, err := (&types.Config{Importer: imp}).Check("", file, f, info); err != nil {
		t.Fatal(err)
	}

	csrc, err := Emit(file, f, info, libraries(imp)...)
	if err != nil {
		t.Fatal(err)
	}

	bin, out, err := Build(t.TempDir(), "churn", csrc, false)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	cmd := exec.Command(bin)
	got, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, got)
	}

	if string(got) != backendtest.ChurnOut {
		t.Fatalf("printed %q, want %q", got, backendtest.ChurnOut)
	}

	if peak := backendtest.Peak(cmd.ProcessState); peak > backendtest.ChurnLimit {
		t.Errorf("peak memory %d MB, want under %d MB: channels or timers are kept past their last handle", peak>>20, backendtest.ChurnLimit>>20)
	}

	if stdout, stderr, code := run(t, "churn", backendtest.Churn, true); code != 0 || stderr != "" {
		t.Errorf("debug build: exit %d\n%s%s", code, stdout, stderr)
	}
}
