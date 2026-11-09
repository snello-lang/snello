// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/snello-lang/snello/runtime"
)

// The runtime is compiled once for each compiler and set of flags and
// kept in a cache, as Go keeps its build cache and Zig its compiler_rt:
// a build then compiles only the program and links the runtime's
// objects. An entry is a directory named by its key, a hash of all
// that the objects depend on: the runtime's sources and headers, the
// compiler, identified by its path, size, time and version, and the
// flags. The system headers the runtime includes are recorded in the
// entry's manifest with their size and time when it is made, as the
// compiler's dependency files list them, and a build that finds one
// changed makes the entry anew, as ccache's direct mode and Make do.
//
// An entry is compiled in a directory of its own and renamed into
// place, so that a build sees an entry whole or not at all; of two
// builds that make one at once, the second to rename keeps the first's
// and drops its own. Builds of one process that miss the same key wait
// for the first. An entry unused for five days is removed when another
// is made, and its time is renewed at most once an hour when used, as
// Go trims its cache.

const (
	cacheVersion = "snello runtime cache 1" // a change to what an entry holds or how its key is made
	trimAfter    = 5 * 24 * time.Hour       // an entry unused this long is removed
	renewAfter   = time.Hour                // how stale an entry's time may be before a use renews it
	leftoverAge  = time.Hour                // a directory being made, or set aside as stale, this old was left by a build that stopped
	manifestName = "manifest"
)

// cacheRoot returns the directory the runtime's entries are kept in:
// runtime/ under SNELLO_CACHE when it names one, else under snello/ in
// the user's cache directory. SNELLO_CACHE=off, or no cache directory
// at all, turns the cache off, and every build compiles the runtime.
func cacheRoot() (string, bool) {
	dir := os.Getenv("SNELLO_CACHE")
	switch dir {
	case "off":
		return "", false
	case "":
		user, err := os.UserCacheDir()
		if err != nil {
			return "", false
		}

		dir = filepath.Join(user, "snello")
	}

	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs // a relative SNELLO_CACHE would move with the working directory
	}

	return filepath.Join(dir, "runtime"), true
}

// A runtimeFill is one making of an entry, which the builds of this
// process that miss the same key wait for.
type runtimeFill struct {
	done   chan struct{}
	objs   []string
	output []byte
	err    error
}

// A cacheError is a failure of the cache itself, a directory that
// cannot be made or a dependency file that cannot be read, as against
// the compiler failing to compile the runtime: a build meeting one
// compiles the runtime beside the program, as before the cache.
type cacheError struct{ err error }

func (e cacheError) Error() string { return "runtime cache: " + e.err.Error() }
func (e cacheError) Unwrap() error { return e.err }

var (
	fillsMu sync.Mutex
	fills   = make(map[string]*runtimeFill)
)

// testHookBeforeRename, when set, runs before a made entry is renamed
// into place, for a test to make the entry first, as another build
// would.
var testHookBeforeRename func(work, entry string)

// RuntimeObjects returns the objects of the runtime's files compiled by
// cc with args, and the compiler's output when it ran: from the cache,
// or made into it; with the cache off, the files are written into dir
// and compiled there.
func RuntimeObjects(dir, cc string, args []string) (objs []string, output []byte, err error) {
	root, ok := cacheRoot()
	if !ok {
		return compileRuntime(dir, cc, args)
	}

	key, err := runtimeKey(cc, args)
	if err != nil {
		return compileRuntime(dir, cc, args) // no such compiler: the build reports it as it always has
	}

	entry := filepath.Join(root, key)
	if objs, ok := lookup(entry); ok {
		return objs, nil, nil
	}

	fillsMu.Lock()
	f := fills[key]
	if f == nil {
		f = &runtimeFill{done: make(chan struct{})}
		fills[key] = f
		fillsMu.Unlock()
		f.objs, f.output, f.err = fill(root, entry, cc, args)
		close(f.done)
		fillsMu.Lock()
		delete(fills, key)
		fillsMu.Unlock()
	} else {
		fillsMu.Unlock()
		<-f.done
	}

	var ce cacheError
	if errors.As(f.err, &ce) {
		return compileRuntime(dir, cc, args) // the cache cannot be used: the build goes on without it
	}

	return f.objs, f.output, f.err
}

// compileRuntime writes the runtime's files into dir and compiles them
// there, as a build did before the cache.
func compileRuntime(dir, cc string, args []string) ([]string, []byte, error) {
	if err := writeRuntime(dir); err != nil {
		return nil, nil, err
	}

	return Objects(cc, args, runtime.Paths(dir))
}

// writeRuntime writes the runtime's sources and headers into dir.
func writeRuntime(dir string) error {
	for name, data := range runtime.Files() {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return err
		}
	}

	return nil
}

// WriteHeaders writes the runtime's headers into dir, which the
// program's C and a package's C include.
func WriteHeaders(dir string) error {
	for name, data := range runtime.Files() {
		if !strings.HasSuffix(name, ".h") {
			continue
		}

		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return err
		}
	}

	return nil
}

// runtimeObjectPaths returns the paths of the runtime's objects in the
// entry or directory dir, in the order the runtime lists its files.
func runtimeObjectPaths(dir string) []string {
	paths := runtime.Paths(dir)
	for i, p := range paths {
		paths[i] = strings.TrimSuffix(p, ".c") + ".o"
	}

	return paths
}

// runtimeKey returns the key of the entry for the runtime compiled by
// cc with args.
func runtimeKey(cc string, args []string) (string, error) {
	id, err := compilerID(cc)
	if err != nil {
		return "", err
	}

	return keyOf(runtimeSum(), id, args, compilerEnv()), nil
}

// compilerEnvVars are the environment variables a C compiler reads that
// change what it compiles: where it looks for headers, and on Apple
// systems which SDK it compiles against and for which release.
var compilerEnvVars = []string{"CPATH", "C_INCLUDE_PATH", "SDKROOT", "MACOSX_DEPLOYMENT_TARGET"}

// compilerEnv returns the values of the variables the compiler reads,
// each named, for the key.
func compilerEnv() []string {
	env := make([]string, len(compilerEnvVars))
	for i, name := range compilerEnvVars {
		env[i] = name + "=" + os.Getenv(name)
	}

	return env
}

// keyOf makes a key of the hash of the runtime's files, the compiler's
// identity, the flags and the compiler's environment.
func keyOf(sum []byte, compiler string, args, env []string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\n%x\n%s\n", cacheVersion, sum, compiler)
	for _, a := range args {
		fmt.Fprintf(h, "%s\x00", a)
	}

	h.Write([]byte{'\n'})
	for _, e := range env {
		fmt.Fprintf(h, "%s\x00", e)
	}

	return hex.EncodeToString(h.Sum(nil))[:32]
}

var (
	runtimeSumOnce sync.Once
	runtimeSumVal  []byte
)

// runtimeSum returns the hash of the runtime's sources and headers, by
// name, made once.
func runtimeSum() []byte {
	runtimeSumOnce.Do(func() {
		files := runtime.Files()
		names := make([]string, 0, len(files))
		for name := range files {
			names = append(names, name)
		}

		sort.Strings(names)
		h := sha256.New()
		for _, name := range names {
			fmt.Fprintf(h, "%s %d\n", name, len(files[name]))
			h.Write(files[name])
		}

		runtimeSumVal = h.Sum(nil)
	})

	return runtimeSumVal
}

var compilerIDs sync.Map // the version of each compiler, by its path, size and time

// compilerID identifies the compiler cc: the path it resolves to, the
// file's size and time, which a compiler rebuilt in place changes, and
// what it says its version is, asked once per binary.
func compilerID(cc string) (string, error) {
	path, err := exec.LookPath(cc)
	if err != nil {
		return "", err
	}

	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}

	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}

	stamp := fmt.Sprintf("%s %d %d", path, fi.Size(), fi.ModTime().UnixNano())
	if v, ok := compilerIDs.Load(stamp); ok {
		return v.(string), nil
	}

	version, err := exec.Command(path, "--version").Output()
	if err != nil {
		return "", err
	}

	id := stamp + "\n" + string(version)
	compilerIDs.Store(stamp, id)
	return id, nil
}

// lookup returns the objects of the entry, when it is whole and the
// headers it was made from are as they were. An entry whose headers
// changed is set aside, to be made anew.
func lookup(entry string) ([]string, bool) {
	data, err := os.ReadFile(filepath.Join(entry, manifestName))
	if err != nil {
		if _, statErr := os.Stat(entry); statErr == nil {
			setAside(entry) // a directory with no manifest: left by a removal that stopped, or damaged
		}

		return nil, false
	}

	if !headersUnchanged(data) {
		setAside(entry)
		return nil, false
	}

	objs := runtimeObjectPaths(entry)
	for _, obj := range objs {
		if _, err := os.Stat(obj); err != nil {
			setAside(entry)
			return nil, false
		}
	}

	if fi, err := os.Stat(entry); err == nil && time.Since(fi.ModTime()) > renewAfter {
		now := time.Now()
		os.Chtimes(entry, now, now) // a failure only lets the entry be trimmed sooner
	}

	return objs, true
}

// headersUnchanged reports whether every header a manifest lists has
// the size and time it had when the entry was made.
func headersUnchanged(manifest []byte) bool {
	for _, line := range strings.Split(string(manifest), "\n") {
		if line == "" || line == cacheVersion {
			continue
		}

		size, rest, ok1 := strings.Cut(line, " ")
		mtime, path, ok2 := strings.Cut(rest, " ")
		if !ok1 || !ok2 {
			return false
		}

		fi, err := os.Stat(path)
		if err != nil || strconv.FormatInt(fi.Size(), 10) != size || strconv.FormatInt(fi.ModTime().UnixNano(), 10) != mtime {
			return false
		}
	}

	return true
}

// setAside renames a stale entry out of the way, so that it can be made
// anew under its name; the trim removes it later. A build that looked
// the entry up just before, and has not yet read its objects, fails
// its link once, and succeeds when run again: this happens only when a
// system header changes while builds run.
func setAside(entry string) {
	stale := entry + ".stale-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if os.Rename(entry, stale) == nil { // another build may have done it first
		now := time.Now()
		os.Chtimes(stale, now, now) // the trim removes it an hour from now
	}
}

// fill makes the entry: compiles the runtime in a directory of its own
// under root, with the compiler writing the headers each file read,
// records those in the manifest, and renames the directory into
// place.
func fill(root, entry, cc string, args []string) ([]string, []byte, error) {
	if objs, ok := lookup(entry); ok {
		return objs, nil, nil // made by another build of this process since the first look
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, nil, cacheError{err}
	}

	work, err := os.MkdirTemp(root, "tmp-")
	if err != nil {
		return nil, nil, cacheError{err}
	}

	if err := writeRuntime(work); err != nil {
		os.RemoveAll(work)
		return nil, nil, cacheError{err}
	}

	_, output, err := Objects(cc, append(append([]string{}, args...), "-MD"), runtime.Paths(work))
	if err != nil {
		os.RemoveAll(work)
		return nil, output, err // the compiler's: the build would fail without the cache too
	}

	manifest, err := headerManifest(work)
	if err != nil {
		os.RemoveAll(work)
		return nil, output, cacheError{err}
	}

	if err := os.WriteFile(filepath.Join(work, manifestName), manifest, 0o644); err != nil {
		os.RemoveAll(work)
		return nil, output, cacheError{err}
	}

	tidy(work)
	if testHookBeforeRename != nil {
		testHookBeforeRename(work, entry)
	}

	if err := os.Rename(work, entry); err != nil {
		if objs, ok := lookup(entry); ok {
			os.RemoveAll(work) // another build made it first
			return objs, output, nil
		}

		if err := os.Rename(work, entry); err != nil { // lookup set a damaged entry aside; a second failure is the cache's
			os.RemoveAll(work)
			return nil, output, cacheError{err}
		}
	}

	trim(root)
	return runtimeObjectPaths(entry), output, nil
}

// headerManifest reads the dependency files the compiler wrote beside
// each object in dir, and returns the manifest of the headers they name
// outside dir, each with its size and time: the system's headers, which
// the key does not hold. The runtime's own are in the key.
func headerManifest(dir string) ([]byte, error) {
	seen := make(map[string]bool)
	var paths []string
	for _, src := range runtime.Paths(dir) {
		data, err := os.ReadFile(strings.TrimSuffix(src, ".c") + ".d")
		if err != nil {
			return nil, err
		}

		for _, dep := range depfilePaths(data) {
			if !filepath.IsAbs(dep) {
				continue // the runtime's own, beside the file
			}

			if rel, err := filepath.Rel(dir, dep); err == nil && !strings.HasPrefix(rel, "..") {
				continue
			}

			if !seen[dep] {
				seen[dep] = true
				paths = append(paths, dep)
			}
		}
	}

	sort.Strings(paths)
	var b bytes.Buffer
	b.WriteString(cacheVersion + "\n")
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			return nil, err
		}

		fmt.Fprintf(&b, "%d %d %s\n", fi.Size(), fi.ModTime().UnixNano(), p)
	}

	return b.Bytes(), nil
}

// depfilePaths returns the prerequisites a dependency file in Make's
// form lists, the target before the first colon left out: one path
// after another, separated by white space, a line continued by a
// backslash, and Make's escapes undone, a backslash before a space or
// a #, and a $ doubled.
func depfilePaths(data []byte) []string {
	s := string(data)
	var paths []string
	var cur strings.Builder
	target := true // up to the first unescaped colon
	flush := func() {
		if cur.Len() > 0 && !target {
			paths = append(paths, cur.String())
		}

		cur.Reset()
	}

	for i := 0; i < len(s); i++ {
		switch ch := s[i]; {
		case ch == '\\' && i+1 < len(s) && (s[i+1] == ' ' || s[i+1] == '#'):
			cur.WriteByte(s[i+1])
			i++
		case ch == '$' && i+1 < len(s) && s[i+1] == '$':
			cur.WriteByte('$')
			i++
		case ch == '\\' && i+1 < len(s) && (s[i+1] == '\n' || s[i+1] == '\r'):
			flush()
			i++
		case ch == ':' && target && (i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\n' || s[i+1] == '\r' || s[i+1] == '\t'):
			cur.Reset()
			target = false
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
			flush()
		default:
			cur.WriteByte(ch)
		}
	}

	flush()
	return paths
}

// tidy removes from a made entry all but the objects and the manifest.
func tidy(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, e := range entries {
		if name := e.Name(); name != manifestName && !strings.HasSuffix(name, ".o") {
			os.Remove(filepath.Join(dir, name))
		}
	}
}

// trim removes the entries under root unused for five days, and the
// directories left being made or set aside by builds that stopped.
func trim(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}

	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			continue
		}

		age := time.Since(fi.ModTime())
		name := e.Name()
		leftover := strings.HasPrefix(name, "tmp-") || strings.Contains(name, ".stale-")
		if leftover && age > leftoverAge || age > trimAfter {
			os.RemoveAll(filepath.Join(root, name)) // another build may be removing it too
		}
	}
}
