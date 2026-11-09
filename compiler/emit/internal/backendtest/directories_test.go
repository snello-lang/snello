// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package backendtest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestDirectories checks that the examples' programs of several
// packages are found, and only they: a folder of separate programs of
// one file, as tree and pipeline are, is not one program.
func TestDirectories(t *testing.T) {
	dirs, err := Directories(filepath.Join("..", "..", "..", "..", "examples"))
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, dir := range dirs {
		names = append(names, filepath.Base(dir))
	}

	if want := []string{"bound", "packaged"}; !slices.Equal(names, want) {
		t.Errorf("Directories found %v, want %v", names, want)
	}
}

// TestPrograms checks that every program the backends' tests share has
// its expected output beside it, and that the folder holds programs of
// one file only.
func TestPrograms(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}

	n := 0
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("testdata/%s: the shared programs are of one file; a program of several packages is an example", e.Name())
			continue
		}

		if !strings.HasSuffix(e.Name(), ".snello") {
			continue
		}

		n++
		if _, err := os.Stat(filepath.Join("testdata", strings.TrimSuffix(e.Name(), ".snello")+".out")); err != nil {
			t.Errorf("testdata/%s has no expected output beside it", e.Name())
		}
	}

	if n == 0 {
		t.Error("no programs under testdata")
	}
}
