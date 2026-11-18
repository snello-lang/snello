// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"debug/elf"
	"debug/macho"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/emit/c"
	"github.com/snello-lang/snello/compiler/emit/llvm"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/emit/stack"
	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/compiler/toolchain"
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
		names, dir, ok := programFiles(filepath.Join(root, c.path), platform.Host(), io.Discard)
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

		if _, _, ok := emitSource(path, "llvm", platform.Host(), false, os.Stderr); !ok {
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
		if got := stderrOf(t, func() { code = check(path, platform.Host()) }); !strings.HasPrefix(got, want) || (code == 0) != compiles {
			t.Errorf("check %s exited %d and said %q, want it to begin %q", program, code, got, want)
		}

		ok := !compiles
		if got := stderrOf(t, func() { _, _, ok = emitSource(path, "llvm", platform.Host(), false, os.Stderr) }); !strings.HasPrefix(got, want) || ok != compiles {
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

// TestBuildWithoutCompiler builds, runs and tests with no compiler on
// the path, through both backends, and builds for a debugger with clang
// there and the C compiler a debug build needs missing: each says what
// to install, as doctor does, in place of the system's error, with no
// backend's name before it.
func TestBuildWithoutCompiler(t *testing.T) {
	root := writePrograms(t, map[string]string{
		"a/main.snello":      "package main\n\nfunc main() {}\n",
		"a/main_test.snello": "package main\n\nfunc TestA() {}\n",
	})
	t.Setenv(toolchain.ClangEnv, "")
	t.Setenv(toolchain.CCEnv, "")
	path := os.Getenv("PATH")
	t.Setenv("PATH", "")
	t.Chdir(t.TempDir())
	for backend, want := range map[string]string{"llvm": "clang not found", "c": "cc not found"} {
		line := want + ": install it with " + toolchain.Install() + ", or set "
		for _, command := range []string{buildCommand, runCommand, testCommand} {
			code := -1
			said := stderrOf(t, func() {
				code = run([]string{command, "-backend", backend, filepath.Join(root, "a")}, os.Stdout, os.Stderr)
			})
			if code != exitFailure || !strings.HasPrefix(said, line) {
				t.Errorf("%s -backend %s exited %d and said %q, want %q", command, backend, code, said, line)
			}
		}
	}

	t.Setenv("PATH", path)
	if _, err := exec.LookPath(toolchain.Clang()); err != nil {
		t.Skip("no clang here for a debug build without cc")
	}

	missing := filepath.Join(t.TempDir(), "cc")
	t.Setenv(toolchain.CCEnv, missing)
	code := -1
	said := stderrOf(t, func() { code = run([]string{buildCommand, "-debug", filepath.Join(root, "a")}, os.Stdout, os.Stderr) })
	if want := "CC names " + missing + ", not found: "; code != exitFailure || !strings.HasPrefix(said, want) {
		t.Errorf("a debug build without cc exited %d and said %q, want %q", code, said, want)
	}
}

// TestCheckTarget checks a directory for the platform -target names:
// the files of that platform are checked and no other's, so a fault in
// the file of one system is found by a check for it, from any machine.
func TestCheckTarget(t *testing.T) {
	dir := t.TempDir()
	for name, src := range map[string]string{
		"main.snello":        "package main\n\nfunc main() { _ = name() }\n",
		"name_linux.snello":  "package main\n\nfunc name() string { return \"linux\" }\n",
		"name_darwin.snello": "package main\n\nfunc name() string { return 1 }\n",
		"width_arm64.snello": "package main\n\nconst width = 64\n",
		"width_amd64.snello": "package main\n\nconst width = 64\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	for _, c := range []struct {
		target string
		code   int
	}{
		{"linux/amd64", exitSuccess},
		{"linux/arm64", exitSuccess},
		{"darwin/arm64", exitFailure}, // name_darwin.snello returns an int as a string
	} {
		var out, errs strings.Builder
		var code int
		stderrOf(t, func() { code = run([]string{checkCommand, "-target", c.target, dir}, &out, &errs) })
		if code != c.code {
			t.Errorf("check -target %s exited %d, want %d: %s", c.target, code, c.code, errs.String())
		}
	}
}

// TestCrossBuild builds two examples for every target but the host
// that this machine has the tools for, through both backends: bound,
// whose packages bind C and choose files by platform, and pipeline,
// whose tasks the stacks' analysis bounds, which it must for every
// target whose C library's stacks are measured, read by the objdump
// for the target's processor. It runs each where the machine can run
// the target's programs: under Rosetta for macOS on the other
// processor, under qemu for the other Linux. A target this machine
// cannot build for is named in the log and passed over; one it builds
// for but cannot run is checked for its format.
func TestCrossBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a program for each target")
	}

	examples, err := filepath.Abs(filepath.Join("..", "..", "examples"))
	if err != nil {
		t.Fatal(err)
	}

	t.Chdir(t.TempDir())
	for _, target := range platform.Targets() {
		if target == platform.Host() {
			continue
		}

		for _, backend := range []string{lower.LLVMBackend, lower.CBackend} {
			cc := c.Compiler()
			if backend == lower.LLVMBackend {
				cc = llvm.Compiler()
			}

			if err := c.CheckTarget(cc, target); err != nil {
				t.Logf("%s through %s: not built here: %v", target, backend, err)
				continue
			}

			for _, name := range []string{"bound", "pipeline"} {
				crossBuild(t, filepath.Join(examples, name), name, target, backend)
			}
		}
	}
}

// crossBuild builds the example in dir, named name, for target through
// backend, checks its format and, for one that spawns, its tasks'
// bounds, and runs it where this machine can, against its output.
func crossBuild(t *testing.T, dir, name string, target platform.Target, backend string) {
	t.Helper()
	what := name + " for " + target.String() + " through " + backend
	var code int
	msgs := stderrOf(t, func() { code = build(dir, backend, target, false) })
	if code != exitSuccess {
		t.Errorf("%s: the build failed:\n%s", what, msgs)
		return
	}

	if err := checkFormat(name, target); err != nil {
		t.Errorf("%s: %v", what, err)
		return
	}

	if name == "pipeline" && target != (platform.Target{OS: platform.Darwin, Arch: platform.AMD64}) { // macOS on Intel: its C library's stacks are not measured
		bounds, err := stack.Patch(name, nil)
		if err != nil || len(bounds) == 0 {
			t.Errorf("%s: the stacks' analysis read %d tasks, %v", what, len(bounds), err)
		}

		for i, b := range bounds {
			if !b.Known() {
				t.Errorf("%s: task %d unbounded: %s", what, i, b.Reason)
			}
		}
	}

	runner := runnerFor(target)
	if runner == nil {
		t.Logf("%s: built; this machine cannot run it", what)
		return
	}

	want, err := os.ReadFile(filepath.Join(dir, name+".out"))
	if err != nil {
		t.Fatal(err)
	}

	got, err := exec.Command(runner[0], append(runner[1:], "./"+name)...).Output()
	if err != nil || string(got) != string(want) {
		t.Errorf("%s: ran %v and printed\n%s\nwant\n%s", what, err, got, want)
		return
	}

	t.Logf("%s: built and ran under %s", what, filepath.Base(runner[0]))
}

// checkFormat says whether the binary at path is an executable for the
// target: Mach-O for macOS, ELF for Linux, of its processor.
func checkFormat(path string, target platform.Target) error {
	if target.OS == platform.Darwin {
		f, err := macho.Open(path)
		if err != nil {
			return err
		}

		defer f.Close()
		if want := map[string]macho.Cpu{platform.AMD64: macho.CpuAmd64, platform.ARM64: macho.CpuArm64}[target.Arch]; f.Cpu != want {
			return fmt.Errorf("a Mach-O binary for %v, want %v", f.Cpu, want)
		}

		return nil
	}

	f, err := elf.Open(path)
	if err != nil {
		return err
	}

	defer f.Close()
	if want := map[string]elf.Machine{platform.AMD64: elf.EM_X86_64, platform.ARM64: elf.EM_AARCH64}[target.Arch]; f.Machine != want {
		return fmt.Errorf("an ELF binary for %v, want %v", f.Machine, want)
	}

	return nil
}

// runnerFor returns the command that runs a program built for target
// on this machine, before the program's path, or nil when there is
// none: Rosetta's arch for macOS on Intel from an Apple processor, and
// qemu's user mode for the other Linux processor, given the target's
// system files, from its sysroot or the cross packages' directory.
func runnerFor(target platform.Target) []string {
	host := platform.Host()
	switch {
	case host.OS == platform.Darwin && target.OS == platform.Darwin && target.Arch == platform.AMD64:
		if exec.Command("arch", "-x86_64", "/usr/bin/true").Run() == nil {
			return []string{"arch", "-x86_64"}
		}
	case host.OS == platform.Linux && target.OS == platform.Linux:
		cpu := map[string]string{platform.AMD64: "x86_64", platform.ARM64: "aarch64"}[target.Arch]
		qemu, err := exec.LookPath("qemu-" + cpu)
		if err != nil {
			return nil
		}

		root := target.Sysroot()
		if root == "" {
			root = "/usr/" + cpu + "-linux-gnu"
		}

		return []string{qemu, "-L", root}
	}

	return nil
}
