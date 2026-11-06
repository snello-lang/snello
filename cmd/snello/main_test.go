// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProgramName checks the name a build gives its binary: the last
// element of the path, read from the absolute path for . and .., and
// without the .snello extension of a file or of a directory so named.
func TestProgramName(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		path string
		dir  bool
		want string
	}{
		{"hello.snello", false, "hello"},
		{"examples/run/fib.snello", false, "fib"},
		{"examples/run/packaged", true, "packaged"},
		{"examples/run/packaged/", true, "packaged"},
		{"x.snello", true, "x"},
		{".", true, filepath.Base(cwd)},
		{"..", true, filepath.Base(filepath.Dir(cwd))},
		{"../snello/..", true, filepath.Base(filepath.Dir(cwd))},
	} {
		if got := programName(c.path, c.dir); got != c.want {
			t.Errorf("programName(%q, %v) = %q, want %q", c.path, c.dir, got, c.want)
		}
	}
}

// TestProgramFiles checks what a build reads: the one file named,
// which must end in .snello, or the .snello files of a directory, which
// must hold one.
func TestProgramFiles(t *testing.T) {
	root := t.TempDir()
	for name, src := range map[string]string{"p/b.snello": "package main\n", "p/a.snello": "package main\n", "p/notes.txt": "", "one.snello": "package main\n", "readme.md": ""} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.Mkdir(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		path  string
		names []string
		dir   bool
		ok    bool
	}{
		{"p", []string{"p/a.snello", "p/b.snello"}, true, true},
		{"one.snello", []string{"one.snello"}, false, true},
		{"readme.md", nil, false, false},
		{"empty", nil, true, false},
		{"nosuch", nil, false, false},
	} {
		names, dir, ok := programFiles(filepath.Join(root, c.path))
		if dir != c.dir || ok != c.ok || len(names) != len(c.names) {
			t.Errorf("programFiles(%q) = %v, %v, %v, want %v, %v, %v", c.path, names, dir, ok, c.names, c.dir, c.ok)
			continue
		}

		for i, name := range c.names {
			if names[i] != filepath.Join(root, name) {
				t.Errorf("programFiles(%q)[%d] = %q, want %q", c.path, i, names[i], filepath.Join(root, name))
			}
		}
	}
}

// TestEmitExamples emits every example with an expected output through
// the driver, which must take a program under any package clause: the
// examples name theirs after what they show, and a slice once refused
// them all for not being package main.
func TestEmitExamples(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "examples", "*.snello"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no examples: %v", err)
	}

	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".snello")
		if _, err := os.Stat(filepath.Join("..", "..", "examples", "expected", name+".out")); err != nil {
			continue // pending: the examples suite names it
		}

		if _, ok := emitSource(path, "llvm", false); !ok {
			t.Errorf("%s: the driver refused it", path)
		}
	}
}
