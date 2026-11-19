// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package importer

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/compiler/types"
)

const (
	// greetLib is a library a test's repository holds, at the version
	// its tag names, and greetBye the same with one more function;
	// greetUser a program that calls it, under the name its require
	// line gives.
	greetLib  = "package greet\n\nfunc Hello() int { return 1 }\n"
	greetBye  = greetLib + "\nfunc Bye() int { return 2 }\n"
	greetUser = "package main\n\nimport \"greet\"\n\nfunc main() { greet.Hello() }\n"
)

// TestResolveLocks builds a program against a repository's tag: the
// module is fetched, the program loads against it, and snello.lock
// records its URL, version, commit and hash; a second build changes
// nothing and needs no git; a cached copy whose files changed, or whose
// directory was removed, is fetched again; a tag moved to other code is
// refused naming both commits, and accepted once its line is removed;
// a commit whose files hash otherwise than the lock file says is
// refused; and a program that drops the requirement drops the file.
func TestResolveLocks(t *testing.T) {
	cache := modCache(t)
	repo, url := gitRepo(t, map[string]string{"lib.snello": greetLib}, "0.1.0")
	prog := writeTree(t, map[string]string{
		ModFile:       "snello 0.1.0\n\nrequire greet " + url + " 0.1.0\n",
		"main.snello": greetUser,
	})
	if err := loadDir(prog); err != nil {
		t.Fatalf("a program requiring a tag: %v", err)
	}

	lockPath := filepath.Join(prog, LockFile)
	lock := readFile(t, lockPath)
	first := headCommit(t, repo)
	cached := moduleDirOf(t, cache, url, "0.1.0")
	hash, err := HashDir(cached)
	if err != nil || lock != url+" 0.1.0 "+first+" "+hash+"\n" {
		t.Fatalf("snello.lock holds %q, want the module's line with %s and %s (%v)", lock, first, hash, err)
	}

	path := os.Getenv("PATH")
	t.Setenv("PATH", t.TempDir()) // no git: a build from the cache runs none
	if err := loadDir(prog); err != nil {
		t.Errorf("a second build, offline from the cache: %v", err)
	}

	if again := readFile(t, lockPath); again != lock {
		t.Errorf("a build that changed nothing rewrote snello.lock: %q", again)
	}

	t.Setenv("PATH", path)
	writeFiles(t, cached, map[string]string{"lib.snello": greetLib + "// changed in the cache\n"})
	if err := loadDir(prog); err != nil {
		t.Errorf("a cached copy changed: %v, want it fetched again", err)
	}

	if got := readFile(t, filepath.Join(cached, "lib.snello")); got != greetLib {
		t.Errorf("the cached copy holds %q after the build, want the tag's file", got)
	}

	if err := os.RemoveAll(cached); err != nil {
		t.Fatal(err)
	}

	if err := loadDir(prog); err != nil {
		t.Errorf("a cached copy removed: %v, want it fetched again", err)
	}

	writeFiles(t, repo, map[string]string{"lib.snello": greetBye})
	commit(t, repo, "0.1.0")
	moved := "was locked at commit " + first[:shortCommit] + ", " + hash + ", and the tag v0.1.0 now names commit " + headCommit(t, repo)[:shortCommit]
	if err := os.RemoveAll(cached); err != nil { // fetched anew, as on a teammate's machine
		t.Fatal(err)
	}

	if err := loadDir(prog); err == nil || !strings.Contains(err.Error(), moved) || !strings.Contains(err.Error(), "the tag was moved to other code") {
		t.Errorf("a tag moved to other code: %v, want %q", err, moved)
	}

	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}

	if err := loadDir(prog); err != nil {
		t.Fatalf("the moved tag accepted once its line is removed: %v", err)
	}

	lock = readFile(t, lockPath)
	fields := strings.Fields(lock)
	writeFiles(t, prog, map[string]string{LockFile: strings.Replace(lock, fields[3], HashPrefix+"AAAA", 1)})
	if err := loadDir(prog); err == nil || !strings.Contains(err.Error(), "only two commits sharing one id make that") {
		t.Errorf("a commit whose files hash otherwise: %v", err)
	}

	writeFiles(t, prog, map[string]string{ModFile: "snello 0.1.0\n", "main.snello": "package main\n\nfunc main() {}\n"})
	if err := loadDir(prog); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(lockPath); err == nil {
		t.Error("a program that requires nothing by version kept snello.lock")
	}
}

// TestResolveStaleCache fetches again a cached copy that the lock file
// records at another commit, as a teammate's lock records a tag its
// author moved after this machine fetched it: the build holds to the
// lock file and builds what it records.
func TestResolveStaleCache(t *testing.T) {
	mine := modCache(t)
	repo, url := gitRepo(t, map[string]string{"lib.snello": greetLib}, "0.1.0")
	prog := writeTree(t, map[string]string{
		ModFile:       "snello 0.1.0\n\nrequire greet " + url + " 0.1.0\n",
		"main.snello": greetUser,
	})
	if err := loadDir(prog); err != nil {
		t.Fatal(err)
	}

	writeFiles(t, repo, map[string]string{"lib.snello": greetBye})
	commit(t, repo, "0.1.0")
	teammate := writeTree(t, map[string]string{
		ModFile:       "snello 0.1.0\n\nrequire greet " + url + " 0.1.0\n",
		"main.snello": "package main\n\nimport \"greet\"\n\nfunc main() { greet.Bye() }\n",
	})
	t.Setenv(ModCacheEnv, t.TempDir()) // the teammate's machine, which fetches the tag as it is now
	if err := loadDir(teammate); err != nil {
		t.Fatal(err)
	}

	t.Setenv(ModCacheEnv, mine) // back on this machine, whose cache holds the tag as it was
	writeFiles(t, prog, map[string]string{LockFile: readFile(t, filepath.Join(teammate, LockFile)), "main.snello": "package main\n\nimport \"greet\"\n\nfunc main() { greet.Bye() }\n"})
	if err := loadDir(prog); err != nil {
		t.Errorf("a stale cached copy under a teammate's lock: %v, want it fetched again", err)
	}
}

// TestResolveLayout leaves a lock file whose records are the build's as
// it is, its line endings a Windows checkout's included.
func TestResolveLayout(t *testing.T) {
	modCache(t)
	_, url := gitRepo(t, map[string]string{"lib.snello": greetLib}, "0.1.0")
	prog := writeTree(t, map[string]string{
		ModFile:       "snello 0.1.0\n\nrequire greet " + url + " 0.1.0\n",
		"main.snello": greetUser,
	})
	if err := loadDir(prog); err != nil {
		t.Fatal(err)
	}

	crlf := strings.ReplaceAll(readFile(t, filepath.Join(prog, LockFile)), "\n", "\r\n")
	writeFiles(t, prog, map[string]string{LockFile: crlf})
	if err := loadDir(prog); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, filepath.Join(prog, LockFile)); got != crlf {
		t.Errorf("a lock file of the same records rewritten: %q", got)
	}
}

// TestResolveSelects takes the highest of two versions of one
// repository two modules require, within one minor before 1.0.0, and
// locks both, the one the program builds against and the one read for
// its requirements; two minor versions before 1.0.0, or two majors, are
// refused naming both and who requires each.
func TestResolveSelects(t *testing.T) {
	modCache(t)
	repo, url := gitRepo(t, map[string]string{"lib.snello": greetLib}, "0.1.0")
	writeFiles(t, repo, map[string]string{"lib.snello": greetBye})
	commit(t, repo, "0.1.1")
	writeFiles(t, repo, map[string]string{"lib.snello": greetLib})
	commit(t, repo, "0.2.0")
	commit(t, repo, "1.0.0")
	commit(t, repo, "2.0.0")
	_, midURL := gitRepo(t, map[string]string{
		ModFile:      "snello 0.1.0\n\nrequire greet " + url + " 0.1.1\n",
		"mid.snello": "package mid\n\nimport \"greet\"\n\nfunc Twice() int { return greet.Bye() * 2 }\n",
	}, "1.0.0")
	prog := writeTree(t, map[string]string{
		ModFile:       "snello 0.1.0\n\nrequire greet " + url + " 0.1.0\nrequire mid " + midURL + " 1.0.0\n",
		"main.snello": "package main\n\nimport (\"greet\"; \"mid\")\n\nfunc main() { greet.Bye(); mid.Twice() }\n",
	})
	if err := loadDir(prog); err != nil {
		t.Fatalf("0.1.0 and 0.1.1 required: %v", err)
	}

	lock := readFile(t, filepath.Join(prog, LockFile))
	for _, want := range []string{url + " 0.1.0 ", url + " 0.1.1 ", midURL + " 1.0.0 "} {
		if !strings.Contains(lock, want) {
			t.Errorf("snello.lock has no line for %q:\n%s", want, lock)
		}
	}

	writeFiles(t, prog, map[string]string{ModFile: "snello 0.1.0\n\nrequire greet " + url + " 0.2.0\nrequire mid " + midURL + " 1.0.0\n"})
	want := url + " is required at 0.1.1 by " + midURL + " 1.0.0 and at 0.2.0 by the program; a program builds against one"
	if err := loadDir(prog); err == nil || err.Error() != want {
		t.Errorf("0.1.1 and 0.2.0 required: %v, want %q", err, want)
	}

	_, majorURL := gitRepo(t, map[string]string{ModFile: "snello 0.1.0\n\nrequire greet " + url + " 1.0.0\n", "m.snello": "package major\n"}, "1.0.0")
	writeFiles(t, prog, map[string]string{
		ModFile:       "snello 0.1.0\n\nrequire greet " + url + " 2.0.0\nrequire major " + majorURL + " 1.0.0\n",
		"main.snello": greetUser,
	})
	want = url + " is required at 1.0.0 by " + majorURL + " 1.0.0 and at 2.0.0 by the program; a program builds against one"
	if err := loadDir(prog); err == nil || err.Error() != want {
		t.Errorf("1.0.0 and 2.0.0 required: %v, want %q", err, want)
	}
}

// TestResolveLoser builds a program whose selected modules agree, while
// a version that lost the selection required another minor of a third
// repository: that version is never built, so what it requires is
// nothing to the program.
func TestResolveLoser(t *testing.T) {
	modCache(t)
	cRepo, cURL := gitRepo(t, map[string]string{"c.snello": "package c\n\nfunc C() int { return 2 }\n"}, "0.2.0")
	commit(t, cRepo, "0.3.0")
	xRepo, xURL := gitRepo(t, map[string]string{ModFile: "snello 0.1.0\n\nrequire c " + cURL + " 0.2.0\n", "x.snello": "package x\n"}, "1.0.0")
	writeFiles(t, xRepo, map[string]string{ModFile: "snello 0.1.0\n\nrequire c " + cURL + " 0.3.0\n"})
	commit(t, xRepo, "1.1.0")
	_, zURL := gitRepo(t, map[string]string{ModFile: "snello 0.1.0\n\nrequire x " + xURL + " 1.0.0\n", "z.snello": "package z\n"}, "1.0.0")
	prog := writeTree(t, map[string]string{
		ModFile:       "snello 0.1.0\n\nrequire z " + zURL + " 1.0.0\nrequire x " + xURL + " 1.1.0\n",
		"main.snello": "package main\n\nfunc main() {}\n",
	})
	if err := loadDir(prog); err != nil {
		t.Errorf("a version that lost the selection decided the build: %v", err)
	}
}

// TestResolveLocalRequirer names a module of the program's own reached
// by a directory, by its module file, as the one that requires what it
// requires.
func TestResolveLocalRequirer(t *testing.T) {
	modCache(t)
	repo, url := gitRepo(t, map[string]string{"lib.snello": greetLib}, "0.2.0")
	commit(t, repo, "0.3.0")
	prog := writeTree(t, map[string]string{
		ModFile:              "snello 0.1.0\n\nrequire util ../util\nrequire greet " + url + " 0.3.0\n",
		"main.snello":        greetUser,
		"../util/" + ModFile: "snello 0.1.0\n\nrequire greet " + url + " 0.2.0\n",
	})
	want := "by " + filepath.Join(prog, "..", "util", ModFile)
	if err := loadDir(prog); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("a local module's requirement: %v, want it named %q", err, want)
	}
}

// TestResolveRequiresRoot resolves the module as it is held, not as
// its file on disk reads, when a module it requires by a directory
// requires the root back: the root's requirement is the program's
// alone, and a requirement the file on disk still holds, of another
// major, counts for nothing.
func TestResolveRequiresRoot(t *testing.T) {
	modCache(t)
	repo, url := gitRepo(t, map[string]string{"lib.snello": greetLib}, "1.0.0")
	commit(t, repo, "2.0.0")
	prog := writeTree(t, map[string]string{
		ModFile:          "snello 0.1.0\n\nrequire lib ./lib\nrequire greet " + url + " 1.0.0\n",
		"lib/" + ModFile: "snello 0.1.0\n\nrequire app ..\n",
		"lib/lib.snello": "package lib\n",
	})
	m, err := ParseModFile(filepath.Join(prog, ModFile), []byte("snello 0.1.0\n\nrequire lib ./lib\nrequire greet "+url+" 2.0.0\n"))
	if err != nil {
		t.Fatal(err)
	}

	if err := m.Resolve(); err != nil {
		t.Fatalf("resolving the module held: %v", err)
	}

	want := []Dependency{{URL: url, Version: "2.0.0", Required: []Requirement{{Version: "2.0.0", By: programRequirer}}}}
	if got := m.Dependencies(); !reflect.DeepEqual(got, want) {
		t.Errorf("Dependencies = %+v, want %+v", got, want)
	}
}

// TestResolveFetchedReach refuses a module fetched by version that
// requires a module by a directory, which is on its author's disk
// alone.
func TestResolveFetchedReach(t *testing.T) {
	modCache(t)
	_, url := gitRepo(t, map[string]string{ModFile: "snello 0.1.0\n\nrequire lexer ../lexer\n", "lib.snello": greetLib}, "0.1.0")
	prog := writeTree(t, map[string]string{
		ModFile:       "snello 0.1.0\n\nrequire greet " + url + " 0.1.0\n",
		"main.snello": greetUser,
	})
	if err := loadDir(prog); err == nil || !strings.Contains(err.Error(), url+" 0.1.0 requires lexer by a directory") {
		t.Errorf("a fetched module requiring a directory: %v", err)
	}
}

// TestCheckFetchedRequire refuses a require line of a fetched module
// that names what its author's disk alone holds: a directory, or a
// repository on a disk where the module came from a host; a module from
// this machine's disk may require another repository on it.
func TestCheckFetchedRequire(t *testing.T) {
	const key, host, disk = "the module", "https://example.com/m", "file:///srv/m"
	byDir := Require{Name: "lexer", Dir: "/home/author/lexer"}
	onDisk := Require{Name: "lexer", URL: "file:///home/author/lexer", Version: "0.1.0"}
	byHost := Require{Name: "lexer", URL: "https://example.com/lexer", Version: "0.1.0"}
	for _, c := range []struct {
		from string
		req  Require
		want string
	}{
		{host, byDir, "by a directory"}, {disk, byDir, "by a directory"},
		{host, onDisk, "a repository on its author's disk"}, {disk, onDisk, ""}, {host, byHost, ""},
	} {
		err := checkFetchedRequire(key, c.from, c.req)
		if (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("from %s, %+v: %v, want %q", c.from, c.req, err, c.want)
		}
	}
}

// TestResolveNoCache builds a program that requires nothing by version
// where no module cache can be named: the cache is asked for only when
// a module is required by version.
func TestResolveNoCache(t *testing.T) {
	t.Setenv(ModCacheEnv, "")
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("LocalAppData", "")
	prog := writeTree(t, map[string]string{
		ModFile:               "snello 0.1.0\n\nrequire util ../util\n",
		"main.snello":         "package main\n\nimport \"util\"\n\nfunc main() { util.F() }\n",
		"../util/util.snello": "package util\n\nfunc F() {}\n",
	})
	if err := loadDir(prog); err != nil {
		t.Errorf("a program with no require by version, and no cache: %v", err)
	}
}

// TestDependencies lists the modules a program builds against by
// version with who requires which version of each, and the shortest
// chain of requirements from the program to each.
func TestDependencies(t *testing.T) {
	modCache(t)
	repo, url := gitRepo(t, map[string]string{"lib.snello": greetLib}, "0.1.0")
	writeFiles(t, repo, map[string]string{"lib.snello": greetBye})
	commit(t, repo, "0.1.1")
	_, midURL := gitRepo(t, map[string]string{
		ModFile:      "snello 0.1.0\n\nrequire greet " + url + " 0.1.1\n",
		"mid.snello": "package mid\n",
	}, "1.0.0")
	dir := writeTree(t, map[string]string{ModFile: "snello 0.1.0\n\nrequire mid " + midURL + " 1.0.0\nrequire greet " + url + " 0.1.0\n"})
	m, err := FindModule(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := m.Resolve(); err != nil {
		t.Fatal(err)
	}

	deps := m.Dependencies()
	if len(deps) != 2 {
		t.Fatalf("dependencies %+v, want two", deps)
	}

	byURL := map[string]Dependency{}
	for _, d := range deps {
		byURL[d.URL] = d
	}

	greet := byURL[url]
	if greet.Version != "0.1.1" || !slices.Contains(greet.Required, Requirement{"0.1.0", programRequirer}) || !slices.Contains(greet.Required, Requirement{"0.1.1", midURL + " 1.0.0"}) {
		t.Errorf("greet: %+v, want 0.1.1, required at 0.1.0 by the program and at 0.1.1 by mid", greet)
	}

	if got := strings.Join(m.Why(url), " -> "); got != programRequirer+" -> "+url+" 0.1.1" {
		t.Errorf("why greet: %q, want the program's own requirement", got)
	}

	if got := strings.Join(m.Why(midURL), " -> "); got != programRequirer+" -> "+midURL+" 1.0.0" {
		t.Errorf("why mid: %q", got)
	}

	if m.Why("https://example.com/none") != nil {
		t.Error("a repository the program does not reach has a chain")
	}
}

// modCache gives the test a module cache of its own, through
// SNELLO_MODCACHE.
func modCache(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv(ModCacheEnv, root)
	return root
}

// loadDir loads the program in dir as snello build DIR does.
func loadDir(dir string) error {
	names, err := SourceFiles(dir, platform.Host())
	if err != nil {
		return err
	}

	_, _, err = LoadProgram(names, platform.Host(), &types.Info{})
	return err
}

// readFile returns the text of the file name, failing the test when it
// does not read.
func readFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}
