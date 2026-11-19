// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package importer

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/snello-lang/snello/compiler/toolchain"
	"github.com/snello-lang/snello/compiler/version"
)

// The module cache: where a module required by version is kept, one
// directory for each repository at each version, with a record beside
// it of the commit its tag named and the hash of its files.
const (
	// ModCacheEnv names the module cache's directory; without it the
	// cache is modulesDir in the user's cache directory, apart from
	// Snello's build cache, so that clearing the one leaves the other.
	ModCacheEnv = "SNELLO_MODCACHE"
	modulesDir  = "snello-modules"

	// versionMark parts a module's directory from its version,
	// tagPrefix begins the tag a version names, tagRefs the reference
	// of a tag, which a fetch names whole so that a branch of the same
	// name is never taken, and hashSuffix ends the record beside a
	// module's directory, which is written last and marks it whole.
	versionMark = "@"
	tagPrefix   = "v"
	tagRefs     = "refs/tags/"
	hashSuffix  = ".h1"

	// refSeparator parts the commit from the reference on a line git
	// ls-remote prints.
	refSeparator = "\t"

	// fileDir is where a repository on the machine's own disk is kept,
	// and escapeMark begins a character a module's directory writes in
	// another form: an upper-case letter as the mark and the letter in
	// lower case, so that two repositories whose names differ by case
	// are two directories on a file system that folds case, and any
	// other character outside safeChars as the mark and its byte in
	// hexadecimal, which begins with a digit where the letter's form
	// begins with a letter.
	fileDir    = "file"
	escapeMark = "!"
	safeChars  = "abcdefghijklmnopqrstuvwxyz0123456789._-/"

	// HashPrefix begins a module's hash, the h1 form: a SHA-256 over a
	// listing of each file's SHA-256 and its path, in base64; shortCommit
	// is how many hexadecimal digits of a commit a message shows.
	HashPrefix  = "h1:"
	shortCommit = 12

	// gitName is the command that fetches a module. A fetch makes a bare
	// repository in cloneDir, writes attributes into its info
	// directory, fetches the one tag into it and unpacks the tag's tree
	// into treeDir, both in a work directory named by fetchingPattern at
	// the cache's root, whose path stays short whatever the module's
	// is, since git on Windows writes no path past 260 characters and a
	// module's escaped path may run to 200 on its own; the tree is
	// renamed from there into place, and the record beside the module
	// is written through a file of the same pattern beside it. A
	// directory or a record a fetch left, stopped, is removed after
	// leftoverAge.
	gitName         = "git"
	cloneDir        = "clone.git"
	treeDir         = "tree"
	infoDir         = "info"
	attributesFile  = "attributes"
	fetchingPattern = ".fetching-*"
	leftoverAge     = time.Hour

	// unconverted is what the fetch's repository says of every file, and
	// overrides what the module's own .gitattributes says: no line
	// ending converted, no $Format$ expanded, which would write the
	// date, no filter run, which the user's own configuration defines,
	// no $Id$ expanded, no encoding changed. A module's files are then
	// its commit's blobs, byte for byte, on every system and for every
	// user, and so is its hash.
	unconverted = "* -text -eol -export-subst -filter -ident -working-tree-encoding\n"

	// sshCommand is the ssh a fetch runs where the user names none: no
	// connection shared with a master that would hold git's output
	// open, and no prompt, which a build has no terminal to answer.
	sshCommand = "GIT_SSH_COMMAND=ssh -o ControlMaster=no -o BatchMode=yes"

	// The permissions of the cache's directories and files.
	cacheDirMode = 0o755
	cacheMode    = 0o644
)

var (
	// gitEnv is what a fetch adds to git's environment: no prompt for
	// a password, so that a repository the build cannot reach fails in
	// git's words. archiveConfig is what git archive runs under: line
	// endings as the repository holds them.
	gitEnv        = []string{"GIT_TERMINAL_PROMPT=0"}
	archiveConfig = []string{"-c", "core.autocrlf=false", "-c", "core.eol=lf"}

	// gitLocalVars are the variables that point git at a repository of
	// the environment's, as a hook runs git with them set, and are
	// taken out of every fetch's environment, so that it reads only the
	// repository it fetches into: git rev-parse --local-env-vars lists
	// them. sshVars name an ssh of the user's own, which a fetch keeps.
	gitLocalVars = []string{
		"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
		"GIT_OBJECT_DIRECTORY", "GIT_DIR", "GIT_WORK_TREE", "GIT_IMPLICIT_WORK_TREE", "GIT_GRAFT_FILE",
		"GIT_INDEX_FILE", "GIT_NO_REPLACE_OBJECTS", "GIT_REPLACE_REF_BASE", "GIT_PREFIX", "GIT_SHALLOW_FILE",
		"GIT_COMMON_DIR",
	}
	sshVars = []string{"GIT_SSH", "GIT_SSH_COMMAND"}
)

// A record is what is known of a module at a version: the commit its
// tag named when it was fetched, and the hash of its files.
type record struct {
	commit, hash string
}

// ModCacheDir returns the module cache's directory, made absolute: the
// one ModCacheEnv names, else snello-modules in the user's cache
// directory, which is apart from Snello's build cache, so that a build
// offline finds its modules whatever became of the build cache.
func ModCacheDir() (string, error) {
	dir := os.Getenv(ModCacheEnv)
	if dir == "" {
		user, err := os.UserCacheDir()
		if err != nil {
			return "", errors.New("no directory for the module cache: set " + ModCacheEnv)
		}

		dir = filepath.Join(user, modulesDir)
	}

	return filepath.Abs(dir)
}

// HashDir returns the hash of the files under dir, the h1 form: a
// SHA-256 over one line for each regular file, its SHA-256 in
// hexadecimal, two spaces and its path under dir with / between its
// elements, in order of path, written in base64 after HashPrefix.
func HashDir(dir string) (string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.Type().IsRegular() {
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}

			files = append(files, filepath.ToSlash(rel))
		}

		return nil
	})
	if err != nil {
		return "", err
	}

	sort.Strings(files)
	sum := sha256.New()
	for _, name := range files {
		if strings.Contains(name, "\n") {
			return "", errors.New(filepath.Join(dir, filepath.FromSlash(name)) + ": a file's name holds a new line, which no module's hash can list")
		}

		f, err := os.Open(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return "", err
		}

		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return "", err
		}

		fmt.Fprintf(sum, "%x  %s\n", h.Sum(nil), name)
	}

	return HashPrefix + base64.StdEncoding.EncodeToString(sum.Sum(nil)), nil
}

// ReleaseVersions returns the versions the repository at rawURL has
// released, those of its tags vMAJOR.MINOR.PATCH with no pre-release,
// as git ls-remote lists them, highest first.
func ReleaseVersions(rawURL string) ([]string, error) {
	if _, err := exec.LookPath(gitName); err != nil {
		return nil, errors.New("listing the releases of " + rawURL + " needs " + gitName + ", which is not found: install it with " + toolchain.GitInstall())
	}

	out, err := runGit(nil, "ls-remote", "--tags", "--refs", "--", rawURL)
	if err != nil {
		return nil, errors.New("listing the releases of " + rawURL + ": " + err.Error())
	}

	type release struct {
		v    version.Version
		name string
	}

	var releases []release
	for _, line := range strings.Split(string(out), lineEnd) {
		_, ref, ok := strings.Cut(strings.TrimSpace(line), refSeparator)
		name, isTag := strings.CutPrefix(ref, tagRefs+tagPrefix)
		v, parsed := version.Parse(name)
		if ok && isTag && parsed && v.Release() && !strings.Contains(name, buildMetadataStart) {
			releases = append(releases, release{v, name})
		}
	}

	slices.SortFunc(releases, func(a, b release) int { return b.v.Compare(a.v) })
	names := make([]string, len(releases))
	for i, r := range releases {
		names[i] = r.name
	}

	return names, nil
}

// moduleDir returns the directory of the repository at rawURL at
// version under the module cache root: its host and path, escaped, and
// the version after versionMark; a repository on the machine's disk
// under fileDir and its path. A URL whose directory would not lie under
// the root is refused, whatever its form, though the module file's
// reading refuses every such URL first.
func moduleDir(root, rawURL, version string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	rel := u.Host + u.Path
	if u.Scheme == fileScheme {
		rel = fileDir + "/" + u.Path
	}

	rel = path.Clean(strings.Trim(escapePath(rel), "/"))
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", errors.New(rawURL + " names no place in the module cache")
	}

	return filepath.Join(root, filepath.FromSlash(rel)+versionMark+version), nil
}

// escapePath writes a repository's host and path, ASCII as the module
// file's reading holds it, as a directory's path: an upper-case letter
// as escapeMark and the letter in lower case, and a character outside
// safeChars as escapeMark and its byte in hexadecimal.
func escapePath(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c >= 'A' && c <= 'Z':
			b.WriteString(escapeMark)
			b.WriteByte(c - 'A' + 'a')
		case strings.IndexByte(safeChars, c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%s%02x", escapeMark, c)
		}
	}

	return b.String()
}

// fetchModule returns the module cache's directory of the repository
// at rawURL at version, and its record from when it entered the cache,
// the commit its tag named and the hash of its files, fetching it the
// first time, which fresh reports. A fetch makes a bare repository of
// its own beside the module's directory, fetches into it the tag of
// version alone, by its full reference, unpacks the tag's tree from git
// archive with nothing converted, its regular files and directories
// and no symbolic link, hashes the files, renames the tree into place
// and records the commit and the hash beside it, which is what marks
// the module whole, so that a build sees a module whole or not at all.
// Of two builds that fetch it at once, the second to rename keeps the
// first's, and refuses when the two found other files, a tag moved
// between the two fetches.
func fetchModule(root, rawURL, version string) (dir string, rec record, fresh bool, err error) {
	if dir, err = moduleDir(root, rawURL, version); err != nil {
		return "", record{}, false, err
	}

	if rec, err := readHashFile(dir); err == nil {
		return dir, rec, false, nil
	}

	what := "fetching " + rawURL + " " + version
	if _, err := exec.LookPath(gitName); err != nil {
		return "", record{}, false, errors.New(what + " needs " + gitName + ", which is not found: install it with " + toolchain.GitInstall())
	}

	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, cacheDirMode); err != nil {
		return "", record{}, false, err
	}

	removeLeftovers(root, dir)
	os.Remove(dir + hashSuffix) // a record whose module was removed: fetched again below
	work, err := os.MkdirTemp(root, fetchingPattern)
	if err != nil {
		return "", record{}, false, err
	}
	defer os.RemoveAll(work) // the clone, and the tree when it was not renamed into place

	clone, tree := filepath.Join(work, cloneDir), filepath.Join(work, treeDir)
	ref := tagRefs + tagPrefix + version
	if rec.commit, err = fetchTag(clone, rawURL, ref); err != nil {
		return "", record{}, false, errors.New(what + ": " + err.Error())
	}

	archive, err := runGit(append(append([]string{}, archiveConfig...), "-C", clone), "archive", "--format=tar", ref)
	if err != nil {
		return "", record{}, false, errors.New(what + ": " + err.Error())
	}

	if err := untar(bytes.NewReader(archive), tree); err != nil {
		return "", record{}, false, errors.New(what + ": " + err.Error())
	}

	if rec.hash, err = HashDir(tree); err != nil {
		return "", record{}, false, err
	}

	if err := os.Rename(tree, dir); err != nil {
		if _, statErr := os.Stat(dir); statErr != nil {
			return "", record{}, false, err
		}

		theirs, err := HashDir(dir) // another build renamed its copy first
		if err != nil {
			return "", record{}, false, err
		}

		if theirs != rec.hash {
			return "", record{}, false, errors.New(what + ": another build fetched it at the same moment and found other files, " + theirs + " where this one found " + rec.hash + ": the tag moved between the two; build again")
		}
	}

	if err := writeHashFile(dir, rec); err != nil {
		return "", record{}, false, err
	}

	return dir, rec, true, nil
}

// fetchTag makes a bare repository at clone that reads its files as
// unconverted says, fetches the tag ref of the repository at rawURL
// into it, that tag alone and no branch of its name, and returns the
// commit the tag names.
func fetchTag(clone, rawURL, ref string) (string, error) {
	if _, err := runGit(nil, "init", "--quiet", "--bare", "--template=", clone); err != nil {
		return "", err
	}

	info := filepath.Join(clone, infoDir)
	if err := os.MkdirAll(info, cacheDirMode); err != nil {
		return "", err
	}

	if err := os.WriteFile(filepath.Join(info, attributesFile), []byte(unconverted), cacheMode); err != nil {
		return "", err
	}

	if _, err := runGit([]string{"-C", clone}, "fetch", "--quiet", "--depth", "1", "--no-tags", "--", rawURL, "+"+ref+":"+ref); err != nil {
		return "", err
	}

	commit, err := runGit([]string{"-C", clone}, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(commit)), nil
}

// runGit runs git with its options before the command, then the
// command and its arguments, in an environment of no repository of its
// own and with no prompt, and returns what it wrote to its output, or
// an error in git's own words.
func runGit(options []string, args ...string) ([]byte, error) {
	cmd := exec.Command(gitName, append(append([]string{}, options...), args...)...)
	cmd.Env = gitEnvironment(os.Environ())
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		said := strings.TrimSpace(stderr.String())
		if said == "" {
			said = err.Error()
		}

		return nil, errors.New(said)
	}

	return stdout.Bytes(), nil
}

// gitEnvironment returns env with the variables that point git at a
// repository taken out, gitEnv added, and the ssh of sshCommand where
// env names no ssh of the user's own.
func gitEnvironment(env []string) []string {
	out := make([]string, 0, len(env)+len(gitEnv)+1)
	ownSSH := false
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if hasName(gitLocalVars, name) {
			continue
		}

		if hasName(sshVars, name) {
			ownSSH = true
		}

		out = append(out, kv)
	}

	out = append(out, gitEnv...)
	if !ownSSH {
		out = append(out, sshCommand)
	}

	return out
}

// hasName reports whether names holds name.
func hasName(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}

	return false
}

// untar writes the regular files and directories of the tar archive r
// under dir, which it makes, and passes over every other entry, a
// symbolic link among them, whose target may lie outside the module
// and outside its hash; an entry whose name leaves dir is refused, and
// so is a second entry of one name.
func untar(r io.Reader, dir string) error {
	if err := os.MkdirAll(dir, cacheDirMode); err != nil {
		return err
	}

	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}

		if err != nil {
			return err
		}

		name := filepath.FromSlash(h.Name)
		if !filepath.IsLocal(name) {
			return errors.New("the archive names " + strconv.Quote(h.Name) + ", outside the module")
		}

		p := filepath.Join(dir, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, cacheDirMode); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeEntry(p, tr); err != nil {
				return err
			}
		}
	}
}

// writeEntry writes a file of an archive, from r, at p, which no file
// holds yet, with the directories above it.
func writeEntry(p string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(p), cacheDirMode); err != nil {
		return err
	}

	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, cacheMode)
	if err != nil {
		return err
	}

	_, err = io.Copy(f, r)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}

	return err
}

// readHashFile returns the record kept beside a module's directory
// since it entered the cache, its commit and its hash; an error when
// there is none, or when the directory itself is gone, a module not
// fetched yet, not whole, or removed.
func readHashFile(dir string) (record, error) {
	data, err := os.ReadFile(dir + hashSuffix)
	if err != nil {
		return record{}, err
	}

	if _, err := os.Stat(dir); err != nil {
		return record{}, err
	}

	fields := strings.Fields(string(data))
	if len(fields) != 2 || !strings.HasPrefix(fields[1], HashPrefix) {
		return record{}, errors.New(dir + hashSuffix + ": no commit and hash")
	}

	return record{fields[0], fields[1]}, nil
}

// writeHashFile records rec beside a module's directory, through a
// file renamed into place, so that a reader finds the record whole or
// not at all.
func writeHashFile(dir string, rec record) error {
	tmp, err := os.CreateTemp(filepath.Dir(dir), filepath.Base(dir)+hashSuffix+fetchingPattern)
	if err != nil {
		return err
	}

	_, err = tmp.WriteString(rec.commit + " " + rec.hash + "\n")
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Chmod(tmp.Name(), cacheMode)
	}

	if err == nil {
		err = os.Rename(tmp.Name(), dir+hashSuffix)
	}

	if err != nil {
		os.Remove(tmp.Name())
	}

	return err
}

// discardModule removes a module's cached copy, its record first, so
// that no build takes a copy half removed for a whole one, then its
// directory.
func discardModule(dir string) error {
	if err := os.Remove(dir + hashSuffix); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return os.RemoveAll(dir)
}

// removeLeftovers removes what a fetch left when it stopped, a work
// directory at the cache's root or a record of the module at dir not
// renamed into place, once leftoverAge has passed, which no fetch
// running takes.
func removeLeftovers(root, dir string) {
	for _, pattern := range []string{filepath.Join(root, fetchingPattern), dir + hashSuffix + fetchingPattern} {
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			if info, err := os.Lstat(m); err == nil && time.Since(info.ModTime()) > leftoverAge {
				os.RemoveAll(m)
			}
		}
	}
}
