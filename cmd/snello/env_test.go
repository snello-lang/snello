// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/emit/c"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/compiler/toolchain"
	"github.com/snello-lang/snello/compiler/version"
)

// columns parts a line of env at its runs of two or more spaces, which
// tabwriter pads the columns with; a value holding two spaces in a row
// would part wrongly, and none of the tests' does.
var columns = regexp.MustCompile(`  +`)

// sysroot is the sysroot variable the tests set and clear, a target's
// that takes one on every system.
var sysroot = platform.Target{OS: platform.Linux, Arch: platform.ARM64}.SysrootVariable()

// rows runs env and returns each setting's value and source by name.
func rows(t *testing.T) map[string][2]string {
	t.Helper()
	var out strings.Builder
	if code := run([]string{envCommand}, &out, io.Discard); code != exitSuccess {
		t.Fatalf("env exited %d: %s", code, out.String())
	}

	rows := map[string][2]string{}
	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		fields := columns.Split(strings.TrimRight(line, " "), 3)
		if len(fields) != 3 {
			t.Fatalf("a line of env is not three columns: %q", line)
		}

		rows[fields[0]] = [2]string{fields[1], fields[2]}
	}

	return rows
}

// TestEnv prints every setting with its value and where it came from:
// the compilers as the path finds them or as their variables name
// them, the cache's directory or off, the sysroots set and not, the
// temporary directory, and the module of the directory or none.
func TestEnv(t *testing.T) {
	t.Setenv(toolchain.ClangEnv, "")
	t.Setenv(toolchain.CCEnv, "")
	t.Setenv(c.CacheEnv, "")
	t.Setenv(importer.ModCacheEnv, "")
	t.Setenv(sysroot, "")
	t.Chdir(t.TempDir())
	got := rows(t)
	for name, want := range map[string][2]string{
		versionSetting: {version.Describe(), thisBinary},
		sysroot:        {notSet, "a build for linux/arm64 reads it when set"},
		moduleSetting:  {noModule, noModuleNote},
	} {
		if got[name] != want {
			t.Errorf("%s is %q, want %q", name, got[name], want)
		}
	}

	for _, name := range []string{targetSetting, toolchain.ClangEnv, toolchain.CCEnv, c.CacheEnv, importer.ModCacheEnv, tempSetting().Name} {
		if _, ok := got[name]; !ok {
			t.Errorf("env lacks %s", name)
		}
	}

	if got[c.CacheEnv][1] != systemCache || got[toolchain.CCEnv][1] != onPath {
		t.Errorf("with nothing set, the cache is %q and cc %q", got[c.CacheEnv], got[toolchain.CCEnv])
	}

	for name := range got {
		if strings.HasPrefix(name, "SNELLO_SYSROOT_darwin") {
			t.Errorf("env lists %s, and macOS takes no sysroot", name)
		}
	}

	// The variables, set; the compiler named as the system names a
	// program, so that the path finds it on Windows too.
	clang := filepath.Join(t.TempDir(), platform.Host().Executable("my-clang"))
	if err := os.WriteFile(clang, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv(toolchain.ClangEnv, clang)
	t.Setenv(c.CacheEnv, c.CacheOff)
	t.Setenv(sysroot, "/opt/sysroot")
	if err := os.WriteFile(importer.ModFile, []byte("snello 0.1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cwd, _ := os.Getwd()
	got = rows(t)
	for name, want := range map[string][2]string{
		toolchain.ClangEnv: {clang, toolchain.ClangEnv + namesIt},
		c.CacheEnv:         {c.CacheOff, c.CacheEnv},
		sysroot:            {"/opt/sysroot", sysroot},
		moduleSetting:      {filepath.Join(cwd, importer.ModFile), "snello 0.1.0"},
	} {
		if got[name] != want {
			t.Errorf("with the variables set, %s is %q, want %q", name, got[name], want)
		}
	}

	// A module file that does not read is named, with why.
	if err := os.WriteFile(importer.ModFile, []byte("bogus\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got = rows(t); got[moduleSetting][0] != filepath.Join(cwd, importer.ModFile) || !strings.Contains(got[moduleSetting][1], "bogus") {
		t.Errorf("a module file that does not read is %q", got[moduleSetting])
	}
}

// TestEnvOne prints one setting's value alone, empty for one that is
// not set, as JSON when asked, and refuses a name that is none with
// the names listed.
func TestEnvOne(t *testing.T) {
	t.Setenv(toolchain.CCEnv, "/opt/cc")
	t.Setenv(sysroot, "")
	var out, errs strings.Builder
	if code := run([]string{envCommand, toolchain.CCEnv}, &out, &errs); code != exitSuccess || out.String() != "/opt/cc\n" {
		t.Errorf("env CC exited %d and printed %q", code, out.String())
	}

	out.Reset()
	if code := run([]string{envCommand, sysroot}, &out, &errs); code != exitSuccess || out.String() != "\n" {
		t.Errorf("env of a sysroot not set exited %d and printed %q", code, out.String())
	}

	out.Reset()
	if code := run([]string{envCommand, "-" + jsonFlag, toolchain.CCEnv}, &out, &errs); code != exitSuccess || strings.TrimSpace(out.String()) != "{\n  \"CC\": \"/opt/cc\"\n}" {
		t.Errorf("env -json CC exited %d and printed %q", code, out.String())
	}

	out.Reset()
	if code := run([]string{envCommand, "NOPE"}, &out, &errs); code != exitUsage || out.String() != "" || !strings.Contains(errs.String(), "no setting NOPE; the settings are VERSION, TARGET, CLANG, CC, ") {
		t.Errorf("env NOPE exited %d, printed %q and said %q", code, out.String(), errs.String())
	}
}

// TestEnvJSON prints the settings as JSON, name to value.
func TestEnvJSON(t *testing.T) {
	var out strings.Builder
	if code := run([]string{envCommand, "-" + jsonFlag}, &out, io.Discard); code != exitSuccess {
		t.Fatalf("env -json exited %d: %s", code, out.String())
	}

	var values map[string]string
	if err := json.Unmarshal([]byte(out.String()), &values); err != nil {
		t.Fatalf("env -json printed no JSON: %v\n%s", err, out.String())
	}

	if values[versionSetting] != version.Describe() || values[targetSetting] == "" || values[moduleSetting] == "" {
		t.Errorf("env -json printed %v", values)
	}
}
