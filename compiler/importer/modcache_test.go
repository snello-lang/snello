// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package importer

import (
	"archive/tar"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	// referenceHash is the h1 hash of a directory holding a.txt with
	// "hello\n" and sub/b.txt with "world\n", computed apart from the
	// code it checks: shasum -a 256 of each file, the listing's lines
	// hashed by shasum and written by base64.
	referenceHash = "h1:cEiP8rChaw7Gg4phJDRj9Ep9kNhpIidz/E6YKYKlbIc="

	// The git a test runs: its identity for the commits it makes, and
	// the branch a repository starts on.
	gitUser   = "snello-test"
	gitEmail  = "test@snello.invalid"
	gitBranch = "main"

	// gitCheckoutDir is the directory of its own a checkout holds,
	// which no cached copy has.
	gitCheckoutDir = ".git"
)

// gitTestEnv is the environment of the git a test runs: an identity
// for its commits, and no configuration of the machine's or the user's,
// which could sign, hook or redirect them.
var gitTestEnv = []string{
	"GIT_AUTHOR_NAME=" + gitUser, "GIT_AUTHOR_EMAIL=" + gitEmail,
	"GIT_COMMITTER_NAME=" + gitUser, "GIT_COMMITTER_EMAIL=" + gitEmail,
	"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull,
}

// TestHashDir hashes a directory's regular files in the h1 form, which
// a computation apart from the code gives too, whatever order the
// files were written in.
func TestHashDir(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"sub/b.txt": "world\n", "a.txt": "hello\n"})
	if got, err := HashDir(dir); err != nil || got != referenceHash {
		t.Errorf("HashDir = %s, %v, want %s", got, err, referenceHash)
	}
}

// TestModCacheDir puts the module cache where SNELLO_MODCACHE says,
// else in snello-modules in the user's cache directory, apart from the
// build cache.
func TestModCacheDir(t *testing.T) {
	own := t.TempDir()
	user, err := os.UserCacheDir()
	if err != nil {
		t.Skip("no user cache directory here")
	}

	for modcache, want := range map[string]string{own: own, "": filepath.Join(user, modulesDir)} {
		t.Setenv(ModCacheEnv, modcache)
		if got, err := ModCacheDir(); err != nil || got != want {
			t.Errorf("%s=%q: %s, %v, want %s", ModCacheEnv, modcache, got, err, want)
		}
	}
}

// TestModuleDir writes a repository's place in the module cache as its
// host and path and the version, an upper-case letter escaped so that
// two repositories differing by case are two directories, a character
// a file system may refuse escaped by its byte, and a repository on
// the machine's disk under file; a URL whose place would leave the
// cache is refused.
func TestModuleDir(t *testing.T) {
	root := filepath.FromSlash("/cache")
	for url, want := range map[string]string{
		"https://github.com/Snello-Lang/RegExp": "github.com/!snello-!lang/!reg!exp@0.2.0",
		"ssh://git@host:22/team/lib":            "host!3a22/team/lib@0.2.0",
		"file:///srv/repos/lib":                 "file/srv/repos/lib@0.2.0",
		"file:///C:/repos/lib":                  "file/!c!3a/repos/lib@0.2.0",
	} {
		if got, err := moduleDir(root, url, "0.2.0"); err != nil || got != filepath.Join(root, filepath.FromSlash(want)) {
			t.Errorf("%s: %s, %v, want %s", url, got, err, want)
		}
	}

	if got, err := moduleDir(root, "https://../../escaped/x", "0.2.0"); err == nil {
		t.Errorf("a URL leaving the cache gave %s", got)
	}
}

// TestFetchModule fetches a repository's tag into the cache once: its
// files without a clone's .git and without a symbolic link, with the
// commit the tag names and the hash of its files recorded beside it; a
// second fetch finds it there and runs no git; a tag the repository
// does not have fails with git's words; and a copy whose directory was
// removed, its record left, is fetched again.
func TestFetchModule(t *testing.T) {
	root := t.TempDir()
	repo, url := gitRepo(t, map[string]string{"lib.snello": "package lib\n", "doc/README": "the library\n"}, "0.2.0")
	dir, rec, fresh, err := fetchModule(root, url, "0.2.0")
	if err != nil || !fresh {
		t.Fatalf("fetch: %v, fresh %v", err, fresh)
	}

	if _, err := os.Stat(filepath.Join(dir, gitCheckoutDir)); err == nil {
		t.Error("the cached copy holds a .git")
	}

	if _, err := os.Lstat(filepath.Join(dir, "link")); err == nil {
		t.Error("the cached copy keeps a symbolic link")
	}

	if want, err := HashDir(dir); err != nil || rec.hash != want {
		t.Errorf("hash %s, the files' %s, %v", rec.hash, want, err)
	}

	if want := headCommit(t, repo); rec.commit != want {
		t.Errorf("commit %s, the tag's %s", rec.commit, want)
	}

	path := os.Getenv("PATH")
	t.Setenv("PATH", t.TempDir()) // no git: the second fetch must not need one
	if again, againRec, fresh, err := fetchModule(root, url, "0.2.0"); err != nil || fresh || again != dir || againRec != rec {
		t.Errorf("a second fetch: %s %+v %v %v", again, againRec, fresh, err)
	}

	t.Setenv("PATH", path)
	if _, _, _, err := fetchModule(root, url, "0.3.0"); err == nil || !strings.Contains(err.Error(), "fetching "+url+" 0.3.0") {
		t.Errorf("a version with no tag: %v", err)
	}

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	if again, againRec, fresh, err := fetchModule(root, url, "0.2.0"); err != nil || !fresh || again != dir || againRec != rec {
		t.Errorf("a fetch after the directory was removed: %s %+v %v %v", again, againRec, fresh, err)
	}
}

// TestReleaseVersions lists a repository's release tags, highest first
// by SemVer's order, and leaves out a pre-release and a tag that is no
// version.
func TestReleaseVersions(t *testing.T) {
	repo, url := gitRepo(t, map[string]string{"lib.snello": "package lib\n"}, "0.1.0")
	for _, tag := range []string{"v0.2.0", "v0.10.0", "v0.3.0-rc.1", "latest"} {
		git(t, repo, "tag", tag)
	}

	if got, err := ReleaseVersions(url); err != nil || strings.Join(got, " ") != "0.10.0 0.2.0 0.1.0" {
		t.Errorf("ReleaseVersions = %v, %v, want 0.10.0 0.2.0 0.1.0", got, err)
	}
}

// TestFetchTagNotBranch takes the tag of a version, never a branch of
// the tag's name, which a clone by name would take.
func TestFetchTagNotBranch(t *testing.T) {
	repo, url := gitRepo(t, map[string]string{"lib.snello": "package lib // the tag\n"}, "0.1.0")
	git(t, repo, "checkout", "--quiet", "-b", tagPrefix+"0.1.0")
	writeFiles(t, repo, map[string]string{"lib.snello": "package lib // the branch\n"})
	git(t, repo, "commit", "--quiet", "--all", "--message", "a branch named as the tag")
	dir, _, _, err := fetchModule(t.TempDir(), url, "0.1.0")
	if err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, filepath.Join(dir, "lib.snello")); got != "package lib // the tag\n" {
		t.Errorf("fetched %q, want the tag's file", got)
	}
}

// TestFetchUnconverted gives a commit's files as the repository holds
// them, whatever the repository's attributes or the user's git ask: no
// $Format$ expanded, which writes the date and the refs, no filter of
// the user's run, no line ending converted; so two fetches of a commit
// hash alike for every user and at every moment.
func TestFetchUnconverted(t *testing.T) {
	_, url := gitRepo(t, map[string]string{
		".gitattributes": "stamp.txt export-subst\nfiltered.txt filter=upper\ncrlf.txt text eol=crlf\n",
		"stamp.txt":      "$Format:%ar %D$\n",
		"filtered.txt":   "abc\n",
		"crlf.txt":       "one\ntwo\n",
	}, "0.1.0")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "filter.upper.smudge")
	t.Setenv("GIT_CONFIG_VALUE_0", "tr a-z A-Z")
	dir, _, _, err := fetchModule(t.TempDir(), url, "0.1.0")
	if err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{"stamp.txt": "$Format:%ar %D$\n", "filtered.txt": "abc\n", "crlf.txt": "one\ntwo\n"} {
		if got := readFile(t, filepath.Join(dir, name)); got != want {
			t.Errorf("%s: %q, want %q, the commit's own bytes", name, got, want)
		}
	}
}

// TestFetchOutsideHook fetches the repository named, not the one a git
// hook's environment points git at, whose tag of the same name a fetch
// that kept GIT_DIR would take.
func TestFetchOutsideHook(t *testing.T) {
	_, url := gitRepo(t, map[string]string{"lib.snello": "package lib // the dependency\n"}, "0.1.0")
	own, _ := gitRepo(t, map[string]string{"lib.snello": "package lib // the program's own\n"}, "0.1.0")
	t.Setenv("GIT_DIR", filepath.Join(own, gitCheckoutDir))
	t.Setenv("GIT_WORK_TREE", own)
	dir, _, _, err := fetchModule(t.TempDir(), url, "0.1.0")
	if err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, filepath.Join(dir, "lib.snello")); got != "package lib // the dependency\n" {
		t.Errorf("fetched %q under a hook's environment, want the dependency's file", got)
	}
}

// TestFetchAtOnce has eight builds fetch one module at the same moment:
// each gets the one copy, whole, and none fails.
func TestFetchAtOnce(t *testing.T) {
	_, url := gitRepo(t, map[string]string{"lib.snello": greetLib}, "0.1.0")
	root := t.TempDir()
	var wg sync.WaitGroup
	recs := make([]record, 8)
	errs := make([]error, 8)
	for i := range recs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, recs[i], _, errs[i] = fetchModule(root, url, "0.1.0")
		}()
	}

	wg.Wait()
	for i := range recs {
		if errs[i] != nil || recs[i] != recs[0] {
			t.Errorf("fetch %d: %+v, %v, want %+v", i, recs[i], errs[i], recs[0])
		}
	}
}

// TestUntar writes an archive's regular files and directories, passes
// over a symbolic link, and refuses a name that leaves the directory
// and a second entry of one name.
func TestUntar(t *testing.T) {
	archive := func(entries ...*tar.Header) []byte {
		var b bytes.Buffer
		w := tar.NewWriter(&b)
		for _, h := range entries {
			if err := w.WriteHeader(h); err != nil {
				t.Fatal(err)
			}

			if h.Typeflag == tar.TypeReg {
				w.Write(make([]byte, h.Size))
			}
		}

		w.Close()
		return b.Bytes()
	}

	dir := filepath.Join(t.TempDir(), "tree")
	if err := untar(bytes.NewReader(archive(
		&tar.Header{Name: "sub/", Typeflag: tar.TypeDir, Mode: 0o755},
		&tar.Header{Name: "sub/f", Typeflag: tar.TypeReg, Size: 3, Mode: 0o644},
		&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
	)), dir); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, "sub", "f")); err != nil {
		t.Error("a regular file was not written")
	}

	if _, err := os.Lstat(filepath.Join(dir, "link")); err == nil {
		t.Error("a symbolic link was written")
	}

	for name, entries := range map[string][]*tar.Header{
		"a name with ..":   {{Name: "../x", Typeflag: tar.TypeReg}},
		"an absolute name": {{Name: "/x", Typeflag: tar.TypeReg}},
		"a name twice":     {{Name: "x", Typeflag: tar.TypeReg}, {Name: "x", Typeflag: tar.TypeReg}},
	} {
		if err := untar(bytes.NewReader(archive(entries...)), filepath.Join(t.TempDir(), "tree")); err == nil {
			t.Errorf("%s was written", name)
		}
	}
}

// TestRemoveLeftovers removes what a stopped fetch left once an hour has
// passed, a work directory at the root and a record beside the module,
// and leaves what a fetch running now holds.
func TestRemoveLeftovers(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "example.com", "lib@0.1.0")
	old, recent, record := filepath.Join(root, ".fetching-1"), filepath.Join(root, ".fetching-2"), dir+hashSuffix+".fetching-3"
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{old, recent} {
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.WriteFile(record, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	stale := time.Now().Add(-2 * leftoverAge)
	for _, p := range []string{old, record} {
		if err := os.Chtimes(p, stale, stale); err != nil {
			t.Fatal(err)
		}
	}

	removeLeftovers(root, dir)
	for _, p := range []string{old, record} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("a leftover older than an hour was kept: %s", p)
		}
	}

	if _, err := os.Stat(recent); err != nil {
		t.Error("a fetch running now lost its directory")
	}
}

// TestGitEnvironment takes the variables that point git at a repository
// out of a fetch's environment, keeps an ssh of the user's own, and
// names one with no shared connection and no prompt otherwise.
func TestGitEnvironment(t *testing.T) {
	env := gitEnvironment([]string{"GIT_DIR=/r/.git", "GIT_INDEX_FILE=/r/i", "HOME=/h"})
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "GIT_DIR=") || strings.Contains(joined, "GIT_INDEX_FILE=") || !strings.Contains(joined, "HOME=/h") || !strings.Contains(joined, sshCommand) {
		t.Errorf("environment %v", env)
	}

	if env := gitEnvironment([]string{"GIT_SSH_COMMAND=my-ssh"}); strings.Contains(strings.Join(env, "\n"), sshCommand) {
		t.Errorf("the user's ssh replaced: %v", env)
	}
}

// moduleDirOf returns the module cache's directory of url at version
// under root, failing the test when it has none.
func moduleDirOf(t *testing.T, root, url, v string) string {
	t.Helper()
	dir, err := moduleDir(root, url, v)
	if err != nil {
		t.Fatal(err)
	}

	return dir
}

// gitRepo makes a git repository of files in a directory of the test's
// and tags its one commit with the version, a symbolic link among the
// files where the system makes one; it returns the directory and the
// repository's file URL. A machine without git skips the test.
func gitRepo(t *testing.T, files map[string]string, v string) (dir, url string) {
	t.Helper()
	if _, err := exec.LookPath(gitName); err != nil {
		t.Skip("no git here")
	}

	dir = t.TempDir()
	git(t, dir, "init", "--quiet", "--initial-branch", gitBranch)
	writeFiles(t, dir, files)
	os.Symlink(filepath.Join(dir, "lib.snello"), filepath.Join(dir, "link")) // a system that makes none tests the rest
	commit(t, dir, v)
	return dir, fileURL(dir)
}

// fileURL returns the file URL of the directory dir, with the slash
// before a Windows volume that RFC 8089 writes.
func fileURL(dir string) string {
	p := filepath.ToSlash(dir)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}

	return fileScheme + "://" + p
}

// commit commits every file of the repository in dir and tags the
// commit with the version, moving the tag when it is on another.
func commit(t *testing.T, dir, v string) {
	t.Helper()
	git(t, dir, "add", "--all")
	git(t, dir, "commit", "--quiet", "--allow-empty", "--message", "v"+v)
	git(t, dir, "tag", "--force", tagPrefix+v)
}

// headCommit returns the commit the repository in dir has checked out.
func headCommit(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command(gitName, "rev-parse", "HEAD")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), gitTestEnv...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}

	return strings.TrimSpace(string(out))
}

// writeFiles writes files into dir, each name a path under it.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// git runs git in dir with the test's environment, failing the test
// with git's words when it fails.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(gitName, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), gitTestEnv...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
