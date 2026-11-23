// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package importer

import (
	"path/filepath"
	"slices"
	"testing"
)

// TestModulePackages lists the packages of a module, the root's and those
// under it, one of tests alone among them, and none of a hidden
// directory, one under _, a testdata one or a module nested in it; and
// every platform's files of a package, but an ignored one.
func TestModulePackages(t *testing.T) {
	root := writeTree(t, map[string]string{
		ModFile:                     "snello 0.1.0\n",
		"main.snello":               "package main\n",
		"lib/lib.snello":            "package lib\n",
		"lib/poll_linux.snello":     "package lib\n",
		"lib/poll_windows.snello":   "package lib\n",
		"lib/_draft.snello":         "package lib\n",
		"lib/inner/inner.snello":    "package inner\n",
		".hidden/h.snello":          "package h\n",
		"_old/o.snello":             "package o\n",
		"lib/testdata/t.snello":     "package t\n",
		"nested/" + ModFile:         "snello 0.1.0\n",
		"nested/n.snello":           "package n\n",
		"docs/README.md":            "no package\n",
		"tested/tested_test.snello": "package tested\n",
	})

	want := []string{root, filepath.Join(root, "lib"), filepath.Join(root, "lib", "inner"), filepath.Join(root, "tested")}
	if got := Packages(root); !slices.Equal(got, want) {
		t.Errorf("Packages:\n got %q\nwant %q", got, want)
	}

	lib := filepath.Join(root, "lib")
	want = []string{filepath.Join(lib, "lib.snello"), filepath.Join(lib, "poll_linux.snello"), filepath.Join(lib, "poll_windows.snello")}
	if got := PackageFiles(lib); !slices.Equal(got, want) {
		t.Errorf("PackageFiles:\n got %q\nwant %q", got, want)
	}
}

// TestModules finds the module a folder is in and every module under
// it, nested ones among them, but none hidden, under _ or in testdata.
func TestModules(t *testing.T) {
	root := writeTree(t, map[string]string{
		ModFile:                     "snello 0.1.0\n",
		"tools/" + ModFile:          "snello 0.1.0\n",
		"tools/deep/" + ModFile:     "snello 0.1.0\n",
		".hidden/" + ModFile:        "snello 0.1.0\n",
		"_old/" + ModFile:           "snello 0.1.0\n",
		"lib/testdata/m/" + ModFile: "snello 0.1.0\n",
	})

	want := []string{root, filepath.Join(root, "tools"), filepath.Join(root, "tools", "deep")}
	if got := Modules(root); !slices.Equal(got, want) {
		t.Errorf("Modules(root):\n got %q\nwant %q", got, want)
	}

	if got := Modules(filepath.Join(root, "lib")); !slices.Equal(got, []string{root}) {
		t.Errorf("Modules of a folder in a module: %q, want the module", got)
	}
}
