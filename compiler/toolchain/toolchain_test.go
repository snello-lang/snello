// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package toolchain

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/platform"
)

// TestDistribution names a Linux system from its os-release: by its ID,
// quoted or not, by the first of its ID_LIKE that a fix is known for,
// and none for a system no fix is known for.
func TestDistribution(t *testing.T) {
	for release, want := range map[string]string{
		"NAME=\"Debian GNU/Linux\"\nID=debian\n":             debian,
		"ID=ubuntu\nID_LIKE=debian\n":                        ubuntu,
		"ID=fedora\n":                                        fedora,
		"ID=arch\n":                                          arch,
		"ID=linuxmint\nID_LIKE=\"ubuntu debian\"\n":          ubuntu,
		"ID=\"almalinux\"\nID_LIKE=\"rhel centos fedora\"\n": fedora,
		"ID=endeavouros\nID_LIKE=arch\n":                     arch,
		"ID=alpine\n":                                        "",
		"":                                                   "",
	} {
		if got := distribution(strings.NewReader(release)); got != want {
			t.Errorf("distribution(%q) = %q, want %q", release, got, want)
		}
	}
}

// TestGitInstall names the command that installs git on each system
// the fixes name, and the package manager elsewhere.
func TestGitInstall(t *testing.T) {
	for system, want := range map[string]string{
		darwin:  "xcode-select --install",
		windows: "winget install Git.Git",
		debian:  "sudo apt install git",
		ubuntu:  "sudo apt install git",
		fedora:  "sudo dnf install git",
		arch:    "sudo pacman -S git",
		"":      "your system's package manager",
	} {
		if got := gitInstallFor(system); got != want {
			t.Errorf("gitInstallFor(%q) = %q, want %q", system, got, want)
		}
	}
}

// TestInstallFor names the command that installs clang on each system a
// fix is known for, and says what to do on any other.
func TestInstallFor(t *testing.T) {
	for system, want := range map[string]string{
		darwin:  "xcode-select --install",
		windows: "winget install LLVM.LLVM",
		debian:  "sudo apt install clang",
		ubuntu:  "sudo apt install clang",
		fedora:  "sudo dnf install clang",
		arch:    "sudo pacman -S clang",
		"":      "your system's package manager, clang 15 or later",
	} {
		if got := installFor(system); got != want {
			t.Errorf("installFor(%q) = %q, want %q", system, got, want)
		}
	}
}

// TestRequire refuses a compiler that cannot be started with what to do
// here, and takes one that can.
func TestRequire(t *testing.T) {
	err := Require(filepath.Join(t.TempDir(), "nosuch"), ClangEnv)
	var missing *MissingError
	if !errors.As(err, &missing) || !strings.HasSuffix(err.Error(), " not found: "+InstallFix(ClangEnv)) {
		t.Errorf("a compiler that does not exist: %v", err)
	}

	if err := Require("sh", CCEnv); err != nil {
		t.Errorf("sh: %v", err)
	}
}

// TestCompilers reads the compilers a build runs from CLANG and CC, and
// takes clang and cc when they are not set, clang for both on Windows.
func TestCompilers(t *testing.T) {
	t.Setenv(ClangEnv, "")
	t.Setenv(CCEnv, "")
	cc := ccName
	if runtime.GOOS == windows {
		cc = clangName
	}

	if Clang() != clangName || CC() != cc {
		t.Errorf("unset: %q, %q", Clang(), CC())
	}

	t.Setenv(ClangEnv, "clang-19")
	t.Setenv(CCEnv, "gcc")
	if Clang() != "clang-19" || CC() != "gcc" {
		t.Errorf("set: %q, %q", Clang(), CC())
	}
}

// TestChecksOldClang refuses a clang that cannot read the IR Snello
// writes, naming its version and the minimum, and leaves the link
// untried: a script that answers --version as clang 14 does and fails
// to compile, as clang 14 fails on an opaque pointer.
func TestChecksOldClang(t *testing.T) {
	if runtime.GOOS == windows {
		t.Skip("the fake compilers are shell scripts, which Windows does not run")
	}

	clang := fakeCompiler(t, "clang version 14.0.0", "error: expected type")
	t.Setenv(ClangEnv, clang)
	t.Setenv(CCEnv, "sh")
	checks := Checks(clang)
	if c := checks[0]; c.OK || !c.Needed || !strings.Contains(c.Detail, "clang version 14.0.0 cannot read the IR Snello writes, which needs clang 15 or later") || !strings.Contains(c.Fix, "install a newer clang") {
		t.Errorf("clang 14: %+v", c)
	}

	if c := checks[1]; c.OK || c.Detail != "not tried without clang" {
		t.Errorf("the link with clang 14: %+v", c)
	}
}

// TestChecksMissing fails clang and the C compiler where none can be
// started, with the fix, the Build Tools among them on Windows, and
// notes, without failing, a command whose directory is not on the
// path.
func TestChecksMissing(t *testing.T) {
	t.Setenv(ClangEnv, "")
	t.Setenv(CCEnv, "")
	t.Setenv("PATH", t.TempDir())
	t.Setenv(platform.Host().SysrootVariable(), "")
	needed := []string{"clang", "link-time optimization", "C compiler"}
	if runtime.GOOS == windows {
		needed = []string{"clang", buildToolsCheck, "link-time optimization", "C compiler"}
	}

	checks := Checks(filepath.Join(t.TempDir(), "snello"))
	if len(checks) != len(needed)+2 {
		t.Fatalf("%d checks", len(checks))
	}

	for i, name := range needed {
		if c := checks[i]; c.Name != name || c.OK || !c.Needed || c.Fix == "" {
			t.Errorf("%s: %+v", name, c)
		}
	}

	if c := checks[0]; c.Detail != "clang not found" || c.Fix != InstallFix(ClangEnv) {
		t.Errorf("clang: %+v", c)
	}

	if c := checks[len(needed)]; c.Name != gitName || c.OK || c.Needed || c.Fix != "install it with "+GitInstall()+"; "+gitNeed {
		t.Errorf("git: %+v", c)
	}

	if c := checks[len(needed)+1]; c.OK || c.Needed {
		t.Errorf("the path: %+v", c)
	}
}

// TestChecksPresent passes every check a build needs where clang is
// installed, and the path's when the command's directory is on it.
func TestChecksPresent(t *testing.T) {
	if _, err := exec.LookPath(Clang()); err != nil {
		t.Skip("no clang here")
	}

	dir := t.TempDir()
	self := filepath.Join(dir, platform.Host().Executable(commandName))
	if err := os.WriteFile(self, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, c := range Checks(self) {
		if !c.OK {
			t.Errorf("%s: %s; %s", c.Name, c.Detail, c.Fix)
		}
	}
}

// TestChecksUnusable fails a compiler that is found and does not run,
// as the launcher of a compiler not installed does, in its own words,
// and one that runs and writes nothing, as a compiler that reads no IR
// does when it takes the IR for a file to link; and a clang that
// cannot link with link-time optimization.
func TestChecksUnusable(t *testing.T) {
	if runtime.GOOS == windows {
		t.Skip("the fake compilers are shell scripts, which Windows does not run")
	}

	t.Setenv(CCEnv, "sh")
	broken := script(t, "echo 'xcode-select: note: No developer tools were found' >&2; exit 1")
	t.Setenv(ClangEnv, broken)
	if c := Checks(broken)[0]; c.OK || !strings.Contains(c.Detail, "cannot run (xcode-select: note: No developer tools were found)") || c.Fix != InstallFix(ClangEnv) {
		t.Errorf("a clang that does not run: %+v", c)
	}

	t.Setenv(CCEnv, broken)
	if c := Checks(broken)[2]; c.OK || !strings.Contains(c.Detail, "cannot run") {
		t.Errorf("a cc that does not run: %+v", c)
	}

	t.Setenv(CCEnv, "sh")
	silent := script(t, "if [ \"$1\" = --version ]; then echo 'gcc 16'; fi; exit 0")
	t.Setenv(ClangEnv, silent)
	if c := Checks(silent)[0]; c.OK || !strings.Contains(c.Detail, "gcc 16 cannot read the IR Snello writes") {
		t.Errorf("a compiler that writes no object: %+v", c)
	}

	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("no clang here to fail only with -flto")
	}

	noLTO := script(t, "for a; do [ \"$a\" = -flto ] && { echo 'no plugin for -flto' >&2; exit 1; }; done; exec clang \"$@\"")
	t.Setenv(ClangEnv, noLTO)
	if c := Checks(noLTO)[1]; c.OK || c.Detail != "clang cannot link with -flto: no plugin for -flto" || c.Fix == "" {
		t.Errorf("a clang that cannot link with -flto: %+v", c)
	}
}

// TestCheckBuildTools builds a program of the C library to find the
// SDK and the C runtime: a clang that cannot is reported with what it
// said and the Build Tools to install, one that can is found, the
// check is not tried without clang, and a sysroot the host's variable
// names stands for the Build Tools whatever clang finds.
func TestCheckBuildTools(t *testing.T) {
	sysrootVariable := platform.Host().SysrootVariable()
	t.Setenv(sysrootVariable, "")
	if c := checkBuildTools(t.TempDir(), false); c.OK || !c.Needed || c.Detail != "not tried without clang" {
		t.Errorf("without clang: %+v", c)
	}

	sysroot := t.TempDir()
	t.Setenv(sysrootVariable, sysroot)
	if c := checkBuildTools(t.TempDir(), false); !c.OK || !strings.Contains(c.Detail, sysroot) || !strings.Contains(c.Detail, sysrootVariable) {
		t.Errorf("with a sysroot: %+v", c)
	}

	t.Setenv(sysrootVariable, "")
	if runtime.GOOS == windows {
		t.Skip("the fake compilers are shell scripts, which Windows does not run")
	}

	t.Setenv(ClangEnv, fakeCompiler(t, "clang version 19.1.0", "fatal error: stdio.h file not found"))
	if c := checkBuildTools(t.TempDir(), true); c.OK || !strings.Contains(c.Detail, "stdio.h file not found") || !strings.Contains(c.Fix, buildTools) {
		t.Errorf("a clang that finds no SDK: %+v", c)
	}

	t.Setenv(ClangEnv, script(t, "exit 0"))
	if c := checkBuildTools(t.TempDir(), true); !c.OK || c.Detail != "clang finds the SDK and the C runtime" {
		t.Errorf("a clang that builds: %+v", c)
	}
}

// TestChecksNoTempDir reports, as the one check, a temporary directory
// the probes cannot be written in, named by TMPDIR on Unix and by TMP
// and TEMP on Windows.
func TestChecksNoTempDir(t *testing.T) {
	none := filepath.Join(t.TempDir(), "none")
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, none)
	}

	checks := Checks("snello")
	if len(checks) != 1 || checks[0].OK || !checks[0].Needed || checks[0].Fix == "" {
		t.Errorf("no temporary directory: %+v", checks)
	}
}

// TestMissingError says which compiler a build cannot use and why: by
// its name, or by the variable that names it when one does.
func TestMissingError(t *testing.T) {
	t.Setenv(ClangEnv, "")
	if got := (&MissingError{Name: "clang", Env: ClangEnv}).Error(); got != "clang not found: "+InstallFix(ClangEnv) {
		t.Errorf("unset: %q", got)
	}

	t.Setenv(ClangEnv, "/opt/clang")
	if got := (&MissingError{Name: "/opt/clang", Env: ClangEnv}).Error(); !strings.HasPrefix(got, "CLANG names /opt/clang, not found: ") {
		t.Errorf("set: %q", got)
	}

	t.Setenv(CCEnv, "")
	if got := (&MissingError{Name: "cc", Env: CCEnv, Reason: "cannot run (exit status 1)"}).what(); got != "cc cannot run (exit status 1)" {
		t.Errorf("a reason: %q", got)
	}
}

// TestRelease reads the first os-release file that exists, and none
// after one that exists and does not read.
func TestRelease(t *testing.T) {
	dir := t.TempDir()
	etc, lib := filepath.Join(dir, "etc"), filepath.Join(dir, "lib")
	if err := os.WriteFile(lib, []byte("ID=fedora\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := release([]string{etc, lib}); got != fedora {
		t.Errorf("only the second: %q", got)
	}

	if err := os.WriteFile(etc, []byte("ID=arch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := release([]string{etc, lib}); got != arch {
		t.Errorf("both: %q, want the first's", got)
	}

	if got := distribution(failingReader{}); got != "" {
		t.Errorf("a reader that fails: %q", got)
	}

	if runtime.GOOS == windows {
		t.Skip("Windows does not deny reading a file through its permission bits")
	}

	if err := os.Chmod(etc, 0); err != nil {
		t.Fatal(err)
	}

	if got := release([]string{etc, lib}); got != "" && os.Getuid() != 0 {
		t.Errorf("a first that does not read: %q, want none", got)
	}
}

// TestCheckPath takes a command reached on the path through a link as
// on the path, and notes one whose directory is not.
func TestCheckPath(t *testing.T) {
	dir := t.TempDir()
	name := platform.Host().Executable(commandName)
	target := filepath.Join(dir, "opt", name)
	link := filepath.Join(dir, "bin", name)
	for _, d := range []string{filepath.Dir(target), filepath.Dir(link)} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", filepath.Dir(link))
	if c := checkPath(target); !c.OK {
		t.Errorf("through a link: %+v", c)
	}

	t.Setenv("PATH", "")
	if c := checkPath(target); c.OK || c.Needed || c.Fix == "" {
		t.Errorf("off the path: %+v", c)
	}
}

// script writes a shell script running body, and returns its path.
func script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "compiler")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	return path
}

// fakeCompiler writes a script that prints version for --version and
// fails, printing failure, for anything else, and returns its path.
func fakeCompiler(t *testing.T, version, failure string) string {
	t.Helper()
	return script(t, "if [ \"$1\" = --version ]; then echo '"+version+"'; exit 0; fi\necho '"+failure+"' >&2\nexit 1")
}

// failingReader is a reader that fails.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("unreadable") }
