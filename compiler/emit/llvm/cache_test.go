// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/snello-lang/snello/compiler/emit/c"
	"github.com/snello-lang/snello/compiler/emit/internal/backendtest"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/types"
)

// TestRuntimeCache checks the cache through this backend: two release
// builds compile the runtime once, with clang, and a debug build after
// the C backend's debug build compiles it no further time, since the
// two compile it the same way and share the entry.
func TestRuntimeCache(t *testing.T) {
	clang, clangLog := backendtest.CountingCompiler(t, "clang")
	cc, ccLog := backendtest.CountingCompiler(t, "cc")
	t.Setenv("CLANG", clang)
	t.Setenv("CC", cc)
	cache := t.TempDir()
	t.Setenv("SNELLO_CACHE", cache)
	const src = "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"cached\") }\n"
	module, err := emit(t, "cached", src, Config{})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		bin, out, err := Build(t.TempDir(), "cached", module, false)
		if err != nil {
			t.Fatalf("release build %d: %v\n%s", i, err, out)
		}

		if printed, err := exec.Command(bin).Output(); err != nil || string(printed) != "cached\n" {
			t.Fatalf("release build %d printed %q, %v", i, printed, err)
		}
	}

	if got := backendtest.RuntimeCompiles(t, clangLog); got != 1 {
		t.Errorf("two release builds compiled the runtime %d times with clang, want once", got)
	}

	if got := backendtest.RuntimeCompiles(t, ccLog); got != 0 {
		t.Errorf("a release build compiled the runtime %d times with cc, want never", got)
	}

	f, file, err := parser.ParseFile("cached.snello", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{}
	imp := importer.NewImporter(info)
	if _, err := (&types.Config{Importer: imp}).Check("", file, f, info); err != nil {
		t.Fatal(err)
	}

	csrc, err := c.Emit(file, f, info, libraries(imp)...)
	if err != nil {
		t.Fatal(err)
	}

	if _, out, err := c.Build(t.TempDir(), "cached", csrc, true); err != nil {
		t.Fatalf("the C backend's debug build: %v\n%s", err, out)
	}

	if _, out, err := Build(t.TempDir(), "cached", module, true); err != nil {
		t.Fatalf("debug build: %v\n%s", err, out)
	}

	if got := backendtest.RuntimeCompiles(t, ccLog); got != 1 {
		t.Errorf("the two backends' debug builds compiled the runtime %d times with cc, want once, shared", got)
	}

	entries, _ := os.ReadDir(filepath.Join(cache, "runtime"))
	if len(entries) != 2 {
		t.Errorf("the cache holds %d entries, want 2: one release, one debug shared by both backends", len(entries))
	}
}
