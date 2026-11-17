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
		{"compiler/emit/internal/backendtest/testdata/fib.snello", false, "fib"},
		{"examples/packaged", true, "packaged"},
		{"examples/packaged/", true, "packaged"},
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
	paths, err := filepath.Glob(filepath.Join("..", "..", "examples", "*", "*.snello"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no examples: %v", err)
	}

	for _, path := range paths {
		if _, err := os.Stat(strings.TrimSuffix(path, ".snello") + ".out"); err != nil {
			continue // pending: the examples suite names it
		}

		if _, _, ok := emitSource(path, "llvm", false); !ok {
			t.Errorf("%s: the driver refused it", path)
		}
	}
}

// TestWarnModule holds the warning a command prints before it loads a
// program: for a module whose file has no snello line, the file and the
// line to add; for one with the line, and a program in no module,
// nothing.
func TestWarnModule(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) string {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}

		return path
	}

	loose := write("loose/main.snello", "package main\n\nfunc main() {}\n")
	bare := write("bare/main.snello", "package main\n\nfunc main() {}\n")
	write("bare/snello.mod", "// no version\n")
	versioned := write("versioned/main.snello", "package main\n\nfunc main() {}\n")
	write("versioned/snello.mod", "snello 0.1.0\n")
	for _, tt := range []struct {
		name, want string
	}{
		{loose, ""},
		{bare, "warning: " + filepath.Join(dir, "bare", "snello.mod") + ` has no snello line: it is read as snello 0.1.0, the first release; add the line "snello 0.1.0"` + "\n"},
		{versioned, ""},
	} {
		var b strings.Builder
		warnModule(&b, []string{tt.name})
		if b.String() != tt.want {
			t.Errorf("%s: printed %q, want %q", tt.name, b.String(), tt.want)
		}
	}
}

// TestInitModule starts a module in a directory, and says which file it
// wrote; a second init there is refused with the command's exit code,
// and two directories are a usage error.
func TestInitModule(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	if code := run([]string{initCommand, dir}, io.Discard, &b); code != exitSuccess || b.String() != "snello init: wrote "+filepath.Join(dir, "snello.mod")+"\n" {
		t.Errorf("init exited %d and said %q", code, b.String())
	}

	src, err := os.ReadFile(filepath.Join(dir, "snello.mod"))
	if err != nil || string(src) != "snello "+version.Current+"\n" {
		t.Errorf("the module file holds %q, %v", src, err)
	}

	b.Reset()
	if code := run([]string{initCommand, dir}, io.Discard, &b); code != exitFailure || !strings.Contains(b.String(), "already exists") {
		t.Errorf("a second init exited %d and said %q", code, b.String())
	}

	here := t.TempDir()
	t.Chdir(here)
	b.Reset()
	if code := run([]string{initCommand}, io.Discard, &b); code != exitSuccess || b.String() != "snello init: wrote "+filepath.Join(here, "snello.mod")+"\n" {
		t.Errorf("init in the current directory exited %d and said %q", code, b.String())
	}

	b.Reset()
	if code := run([]string{initCommand, "a", "b"}, io.Discard, &b); code != exitUsage || !strings.HasPrefix(b.String(), "usage: snello init [DIR]") {
		t.Errorf("init with two directories exited %d and said %q", code, b.String())
	}
}

// TestCommandsWarn runs check and the emit that build and debug share
// on a program whose module file has no snello line: each says so on
// standard error before anything else, a program that does not compile
// included.
func TestCommandsWarn(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"snello.mod":         "// no version\n",
		"good/main.snello":   "package main\n\nfunc main() {}\n",
		"broken/main.snello": "package main\n\nfunc main() { x }\n",
	}
	for name, text := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	want := "warning: " + filepath.Join(dir, "snello.mod") + " has no snello line"
	for program, compiles := range map[string]bool{"good": true, "broken": false} {
		path := filepath.Join(dir, program)
		code := -1
		if got := stderrOf(t, func() { code = check(path) }); !strings.HasPrefix(got, want) || (code == 0) != compiles {
			t.Errorf("check %s exited %d and said %q, want it to begin %q", program, code, got, want)
		}

		ok := !compiles
		if got := stderrOf(t, func() { _, _, ok = emitSource(path, "llvm", false) }); !strings.HasPrefix(got, want) || ok != compiles {
			t.Errorf("emit %s gave %v and said %q, want it to begin %q", program, ok, got, want)
		}
	}
}

// stderrOf returns what run writes to standard error, gathered in a
// file, which takes output of any length, and with standard error put
// back however run ends.
func stderrOf(t *testing.T, run func()) string {
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}

	defer f.Close()
	saved := os.Stderr
	os.Stderr = f
	func() {
		defer func() { os.Stderr = saved }()
		run()
	}()

	out, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}

	return string(out)
}
