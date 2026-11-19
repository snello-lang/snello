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

	"github.com/snello-lang/snello/compiler/importer"
)

// TestDependencyCommands drives the commands that change and show a
// module's dependencies over a repository with three releases, 0.1.0,
// 0.1.1 with one more function, and 1.0.0: add without a version
// requires the latest release, with one changes the line, and again
// keeps it; update raises to the latest compatible release, and with
// -major past the major; deps prints who requires what, and -why the
// chain; add refuses a name a directory of the module holds and a URL
// with no scheme before asking git anything, and a version with no tag,
// the module file left as it was; remove refuses a name not required
// and one a file imports, and removes the line and the lock file once
// nothing does, a name given twice once; deps then lists nothing; and
// fetch fills the module cache.
func TestDependencyCommands(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git here")
	}

	cache := t.TempDir()
	t.Setenv(importer.ModCacheEnv, cache)
	repo := t.TempDir()
	gitIn(t, repo, "init", "--quiet", "--initial-branch", "main")
	release := func(v, src string) {
		if err := os.WriteFile(filepath.Join(repo, "greet.snello"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}

		gitIn(t, repo, "add", "--all")
		gitIn(t, repo, "commit", "--quiet", "--allow-empty", "--message", v)
		gitIn(t, repo, "tag", "v"+v)
	}

	hello := "package greet\n\nfunc Hello() string { return \"hello\" }\n"
	release("0.1.0", hello)
	release("0.1.1", hello+"\nfunc Bye() string { return \"bye\" }\n")
	release("1.0.0", hello)
	url := fileURL(repo)
	mod := t.TempDir()
	modFile := filepath.Join(mod, importer.ModFile)
	writeFile(t, modFile, "snello 0.1.0\n")
	writeFile(t, filepath.Join(mod, "main.snello"), "package main\n\nfunc main() {}\n")
	t.Chdir(mod)
	snello := func(want int, args ...string) string {
		t.Helper()
		var out, errs strings.Builder
		if code := run(args, &out, &errs); code != want {
			t.Fatalf("snello %s exited %d, want %d: %s%s", strings.Join(args, " "), code, want, out.String(), errs.String())
		}

		return out.String() + errs.String()
	}

	if got := snello(exitSuccess, addCommand, "greet", url); got != "required greet "+url+" 1.0.0\n" {
		t.Errorf("add without a version: %q", got)
	}

	if got := snello(exitSuccess, addCommand, "greet", url, "0.1.0"); got != "greet "+url+" 1.0.0 -> 0.1.0\n" {
		t.Errorf("add with a version: %q", got)
	}

	if got := snello(exitSuccess, updateCommand); got != "greet "+url+" 0.1.0 -> 0.1.1\n" {
		t.Errorf("update: %q", got)
	}

	if got := snello(exitSuccess, updateCommand); got != "every dependency is at its latest compatible release\n" {
		t.Errorf("update with nothing to raise: %q", got)
	}

	if got := snello(exitSuccess, updateCommand, "-"+majorFlag, "greet"); got != "greet "+url+" 0.1.1 -> 1.0.0\n" {
		t.Errorf("update -major: %q", got)
	}

	if got := snello(exitSuccess, addCommand, "greet", url); got != "greet "+url+" 1.0.0 is required already\n" {
		t.Errorf("add of the latest release again: %q", got)
	}

	if got := snello(exitSuccess, depsCommand); got != url+" 1.0.0\n"+requiredIndent+"1.0.0 by the program\n" {
		t.Errorf("deps: %q", got)
	}

	if got := snello(exitSuccess, depsCommand, "-"+whyFlag, "greet"); got != "the program -> "+url+" 1.0.0\n" {
		t.Errorf("deps -why: %q", got)
	}

	if got := snello(exitFailure, depsCommand, "-"+whyFlag, "nosuch"); !strings.Contains(got, "nosuch is not a name snello.mod requires by version: give the module's URL") {
		t.Errorf("deps -why of a name not required: %q", got)
	}

	writeFile(t, filepath.Join(mod, "main.snello"), "package main\n\nimport \"greet\"\n\nfunc main() { greet.Hello() }\n")
	before := readText(t, modFile)
	if got := snello(exitFailure, removeCommand, "nosuch"); !strings.Contains(got, "nosuch is not required by snello.mod") {
		t.Errorf("remove of a name not required: %q", got)
	}

	if got := snello(exitFailure, removeCommand, "greet"); !strings.Contains(got, "greet is still imported, at "+filepath.Join(mod, "main.snello")+":3: remove the imports first") {
		t.Errorf("remove while imported: %q", got)
	}

	if readText(t, modFile) != before {
		t.Error("a refused remove changed snello.mod")
	}

	if err := os.Mkdir(filepath.Join(mod, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(mod, "lib", "own.snello"), "package lib\n")
	if got := snello(exitFailure, addCommand, "lib", url); !strings.Contains(got, "lib is a directory of the module") || readText(t, modFile) != before {
		t.Errorf("add of a name a directory holds: %q", got)
	}

	if got := snello(exitFailure, addCommand, "other", "github.com/x/other"); !strings.Contains(got, "has no scheme git fetches from") {
		t.Errorf("add of a URL with no scheme: %q", got)
	}

	if got := snello(exitFailure, addCommand, "other", url, "9.9.9"); !strings.Contains(got, "fetching "+url+" 9.9.9") || readText(t, modFile) != before {
		t.Errorf("add of a version with no tag: %q, snello.mod %q", got, readText(t, modFile))
	}

	if err := os.RemoveAll(cache); err != nil {
		t.Fatal(err)
	}

	snello(exitSuccess, fetchCommand)
	if entries, _ := os.ReadDir(cache); len(entries) == 0 {
		t.Error("fetch filled no module cache")
	}

	writeFile(t, filepath.Join(mod, "main.snello"), "package main\n\nfunc main() {}\n")
	if got := snello(exitSuccess, removeCommand, "greet", "greet"); got != "removed greet\n" {
		t.Errorf("remove: %q", got)
	}

	if got := readText(t, modFile); got != "snello 0.1.0\n" {
		t.Errorf("snello.mod after remove: %q", got)
	}

	if _, err := os.Stat(filepath.Join(mod, importer.LockFile)); err == nil {
		t.Error("snello.lock kept with nothing required by version")
	}

	if got := snello(exitSuccess, depsCommand); got != "no dependencies by version\n" {
		t.Errorf("deps with nothing required: %q", got)
	}
}

// TestCommandsCheckFirst refuses what add and update are given before
// git is asked anything: a name required by a directory, which a
// repository no one can reach would otherwise answer with git's words,
// and a name not required by version after one that is.
func TestCommandsCheckFirst(t *testing.T) {
	const unreachable = "file:///nonexistent/repo"
	mod := t.TempDir()
	writeFile(t, filepath.Join(mod, importer.ModFile), "snello 0.1.0\n\nrequire ext ./ext\nrequire far "+unreachable+" 1.0.0\n")
	if err := os.Mkdir(filepath.Join(mod, "ext"), 0o755); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(mod, "ext", importer.ModFile), "snello 0.1.0\n")
	t.Chdir(mod)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{addCommand, "ext", unreachable}, "ext is required already, from " + filepath.Join(mod, "ext") + ": remove it first"},
		{[]string{updateCommand, "far", "nosuch"}, "nosuch is not required by version in snello.mod"},
	} {
		var out, errs strings.Builder
		if code := run(c.args, &out, &errs); code != exitFailure || !strings.Contains(errs.String(), c.want) {
			t.Errorf("snello %s: exit %d, %q, want %q", strings.Join(c.args, " "), code, errs.String(), c.want)
		}
	}
}

// TestLatestFor raises a requirement to the highest release above it
// that is compatible, a 0.0.x release to none, or the highest above it
// with major, and to none when nothing is above it.
func TestLatestFor(t *testing.T) {
	releases := []string{"2.0.0", "1.4.0", "1.3.2", "0.3.0", "0.2.5", "0.2.0", "0.0.4", "0.0.3"}
	for _, c := range []struct {
		current string
		major   bool
		want    string
	}{
		{"1.3.2", false, "1.4.0"}, {"1.3.2", true, "2.0.0"}, {"0.2.0", false, "0.2.5"},
		{"0.2.0", true, "2.0.0"}, {"2.0.0", false, ""}, {"2.0.0", true, ""}, {"0.3.0", false, ""},
		{"0.0.3", false, ""}, {"0.0.3", true, "2.0.0"},
	} {
		if got := latestFor(c.current, releases, c.major); got != c.want {
			t.Errorf("latestFor(%s, major %v) = %q, want %q", c.current, c.major, got, c.want)
		}
	}
}

// writeFile writes text to the file name.
func writeFile(t *testing.T, name, text string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readText returns the text of the file name.
func readText(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}
