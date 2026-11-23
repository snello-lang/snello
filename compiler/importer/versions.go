// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package importer

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/snello-lang/snello/compiler/version"
)

// The lock file: beside the program's snello.mod, one line for every
// module the program reaches by version, its repository's URL, the
// version, the commit its tag named and the hash of its files, sorted;
// written by the build when the records it holds changed, through a
// file renamed into place, which replacingPattern names beside it.
const (
	LockFile         = "snello.lock"
	lockFields       = 4
	lockFileMode     = 0o644
	replacingPattern = ".replacing-*"

	// programRequirer names the program's own module among the modules
	// that require a repository, in a message about two versions.
	programRequirer = "the program"
)

// A Dependency is a module a program builds against by version: its
// repository, the version selected, and every requirement of it that
// counts, the program's and its selected modules', with who makes it.
type Dependency struct {
	URL, Version string
	Required     []Requirement
}

// A Requirement is one module's requirement of a repository: the
// version it asks for, and who asks, the program, a module of the
// program's own by its file, or a module by its URL and version.
type Requirement struct {
	Version, By string
}

// A selection is what Resolve chose of the modules a program reaches
// by version: the directory of the one version of each repository the
// program builds against, by URL; the dependencies, sorted by URL; and
// for each repository who first requires it on the way from the
// program, the shortest chain.
type selection struct {
	dirs  map[string]string
	deps  []Dependency
	first map[string]string
}

// A want is a requirement of a repository at a version, and who makes
// it, for a message: the program, a module of the program's own by its
// file, or a module fetched by version by its URL and version.
type want struct{ url, version, by string }

// A reached is one module a program reaches by version: its version,
// read, who requires it, where the module cache keeps it, its record,
// and what it requires in turn.
type reached struct {
	version  string
	parsed   version.Version
	by       string
	dir      string
	rec      record
	requires []want
}

// Resolve fetches the modules m reaches by version and selects one
// version of each repository the program builds against: the highest
// any module requires, which is the oldest every module accepts, the
// minimal version selection of Cox's "Minimal Version Selection". A
// requirement a selected module makes, or the program's, that the
// selected version cannot meet, another major, or another minor before
// 1.0.0, is refused naming both; a version that lost the selection
// requires nothing of the program. The walk follows the require lines
// of m and of every module they lead to: a module of the program's own
// reached by a directory, whose file is passed over when it does not
// read, since only an import that reaches it reads it, and a module
// fetched by version, which may not require by a directory or by a
// repository on its author's disk.
//
// Each module is checked against the record of its entry into the
// module cache and against snello.lock beside m's file. A cached copy
// whose files changed, or that the lock file records otherwise, is
// discarded and fetched again, since the lock file, not the cache, is
// what the program holds to; a tag that, fetched anew, names another
// commit than the lock file's, or a commit whose files hash otherwise,
// fails the build naming both. The lock file is written when its
// records changed, and removed when m reaches nothing by version.
// Every require line by version of m is given the directory of its
// repository's selected version, and the importer gives the lines of
// the modules it reaches the same.
func (m *Module) Resolve() error {
	return m.resolve(false)
}

// ResolveCached is Resolve for a caller that only reads the program, the
// language server as the developer types: every required module is
// taken from the module cache and held to the lock file, and a module
// the cache does not hold is an error that names snello fetch. Nothing
// is fetched, discarded, hashed or written, the lock file included, so
// that an editor's checks never touch the network or the files of the
// program, and cost no more than reading what the cache holds.
func (m *Module) ResolveCached() error {
	return m.resolve(true)
}

// resolve is Resolve, or ResolveCached when cached is set.
func (m *Module) resolve(cached bool) error {
	var program []want
	walked := map[string]bool{canonDir(m.Dir): true}
	if err := walkLocal(m, programRequirer, walked, &program); err != nil {
		return err
	}

	if len(program) == 0 {
		if cached {
			return nil
		}

		return removeLock(m.Dir)
	}

	root, err := ModCacheDir()
	if err != nil {
		return err
	}

	lockName := filepath.Join(m.Dir, LockFile)
	locked, err := readLock(lockName)
	if err != nil {
		return err
	}

	byURL := map[string][]*reached{}
	byKey := map[string]*reached{}
	queue := append([]want(nil), program...)
	for len(queue) > 0 {
		w := queue[0]
		queue = queue[1:]
		key := w.url + fieldSeparator + w.version
		if byKey[key] != nil {
			continue
		}

		r, err := reach(root, w, locked[key], cached)
		if err != nil {
			return err
		}

		byKey[key] = r
		byURL[w.url] = append(byURL[w.url], r)
		queue = append(queue, r.requires...)
	}

	selected := map[string]*reached{}
	for url, rs := range byURL {
		best := rs[0]
		for _, r := range rs[1:] {
			if r.parsed.Compare(best.parsed) > 0 {
				best = r
			}
		}

		selected[url] = best
	}

	required, first, err := checkSelected(program, selected, byKey)
	if err != nil {
		return err
	}

	if !cached {
		if err := writeLockIfChanged(lockName, locked, byKey); err != nil {
			return err
		}
	}

	sel := &selection{dirs: map[string]string{}, first: first}
	for url, r := range selected {
		sel.dirs[url] = r.dir
		if len(required[url]) > 0 {
			sel.deps = append(sel.deps, Dependency{URL: url, Version: r.version, Required: required[url]})
		}
	}

	sort.Slice(sel.deps, func(i, j int) bool { return sel.deps[i].URL < sel.deps[j].URL })

	for i := range m.Requires {
		if m.Requires[i].URL != "" {
			m.Requires[i].Dir = sel.dirs[m.Requires[i].URL]
		}
	}

	m.selection = sel
	return nil
}

// Dependencies returns the modules the program builds against by
// version, as Resolve selected them, sorted by URL, each with every
// requirement of it that counts; nothing before Resolve.
func (m *Module) Dependencies() []Dependency {
	if m.selection == nil {
		return nil
	}

	return m.selection.deps
}

// Why returns the shortest chain of requirements from the program to
// the repository at url, the program or a module of its own first and
// the module the program builds against last, each module by its URL
// and version; nothing for a repository the program does not reach, or
// before Resolve.
func (m *Module) Why(url string) []string {
	if m.selection == nil || m.selection.first[url] == "" {
		return nil
	}

	version := map[string]string{}
	for _, d := range m.selection.deps {
		version[d.URL] = d.Version
	}

	chain := []string{url + fieldSeparator + version[url]}
	for seen := map[string]bool{url: true}; ; {
		by := m.selection.first[url]
		chain = append([]string{by}, chain...)
		next, _, isModule := strings.Cut(by, fieldSeparator)
		if !isModule || seen[next] || m.selection.first[next] == "" {
			return chain
		}

		seen[next], url = true, next
	}
}

// walkLocal adds to wants the require lines by version of mod, which
// by names, and of every module of the program's own its require lines
// by directory lead to, each named by its own module file, once each.
// A module whose file does not read is passed over: the import that
// reaches it, if one does, reports it.
func walkLocal(mod *Module, by string, walked map[string]bool, wants *[]want) error {
	for _, r := range mod.Requires {
		if r.URL != "" {
			*wants = append(*wants, want{r.URL, r.Version, by})
			continue
		}

		dir := canonDir(r.Dir)
		if walked[dir] {
			continue
		}

		walked[dir] = true
		local, err := readModule(r.Dir)
		if err != nil {
			continue
		}

		if err := walkLocal(local, filepath.Join(r.Dir, ModFile), walked, wants); err != nil {
			return err
		}
	}

	return nil
}

// reach fetches the module w names, or finds it in the module cache,
// checks it against its record and the lock file's, was, discarding a
// cached copy either finds changed and fetching it again, and reads
// what it requires; cached takes it from the cache alone, as
// ResolveCached says.
func reach(root string, w want, was record, cached bool) (*reached, error) {
	key := w.url + fieldSeparator + w.version
	var dir string
	var rec record
	var err error
	if cached {
		dir, rec, err = inCache(root, w, key)
	} else {
		dir, rec, err = fetchChecked(root, w, was, key)
	}

	if err != nil {
		return nil, err
	}

	if was != (record{}) && was != rec {
		return nil, errors.New(LockFile + ": " + key + " " + changed(was, rec, w.version))
	}

	parsed, _ := version.Parse(w.version) // read when the line was
	r := &reached{version: w.version, parsed: parsed, by: w.by, dir: dir, rec: rec}
	fetched, err := readModule(dir)
	if err != nil {
		return nil, errors.New(strings.Replace(err.Error(), filepath.Join(dir, ModFile), key+": "+ModFile, 1))
	}

	for _, req := range fetched.Requires {
		if err := checkFetchedRequire(key, w.url, req); err != nil {
			return nil, err
		}

		r.requires = append(r.requires, want{req.URL, req.Version, key})
	}

	return r, nil
}

// fetchChecked fetches the module w names, or finds it in the module
// cache, and returns its directory and record once its files hash to the
// record: a cached copy whose files changed, or whose record the lock
// file's, was, disagrees with, is discarded and fetched again, since the
// lock file, not the cache, is what the program holds to.
func fetchChecked(root string, w want, was record, key string) (string, record, error) {
	dir, rec, fresh, err := fetchModule(root, w.url, w.version)
	if err != nil {
		return "", record{}, err
	}

	now, err := HashDir(dir)
	if err != nil {
		return "", record{}, err
	}

	if !fresh && (now != rec.hash || (was != record{} && was != rec)) {
		if err := discardModule(dir); err != nil {
			return "", record{}, err
		}

		if dir, rec, _, err = fetchModule(root, w.url, w.version); err != nil {
			return "", record{}, err
		}

		if now, err = HashDir(dir); err != nil {
			return "", record{}, err
		}
	}

	if now != rec.hash {
		return "", record{}, errors.New("the module cache's copy of " + key + " changed as it was fetched; build again")
	}

	return dir, rec, nil
}

// inCache returns the directory and the record of the module w names as
// the module cache holds it, or an error naming snello fetch for one it
// does not hold.
func inCache(root string, w want, key string) (string, record, error) {
	module, err := moduleDir(root, w.url, w.version)
	if err != nil {
		return "", record{}, err
	}

	rec, err := readHashFile(module)
	if err != nil {
		return "", record{}, errors.New(key + " is not in the module cache: snello fetch fetches it")
	}

	return filepath.Join(module, treeDir), rec, nil
}

// checkFetchedRequire refuses a require line of the module key, fetched
// from the repository at from, that names what is on its author's disk
// alone: a directory, or a repository on a disk where the module came
// from a host; a module fetched from this machine's own disk may
// require another repository on it, which this machine reaches.
func checkFetchedRequire(key, from string, req Require) error {
	if req.URL == "" {
		return errors.New(key + " requires " + req.Name + " by a directory, " + req.Dir + ", which a module fetched by version cannot reach")
	}

	if onDisk, fromDisk := strings.HasPrefix(req.URL, fileScheme+":"), strings.HasPrefix(from, fileScheme+":"); onDisk && !fromDisk {
		return errors.New(key + " requires " + req.Name + " from " + req.URL + ", a repository on its author's disk, which a module fetched from a host cannot reach")
	}

	return nil
}

// checkSelected walks the requirements of the program and of every
// selected module, breadth first, and refuses one the selected version
// of its repository cannot meet, naming both and who requires each. It
// returns every requirement walked, by repository, and who requires
// each repository first, nearest the program.
func checkSelected(program []want, selected map[string]*reached, byKey map[string]*reached) (map[string][]Requirement, map[string]string, error) {
	required, first := map[string][]Requirement{}, map[string]string{}
	seen := map[string]bool{}
	queue := append([]want(nil), program...)
	for len(queue) > 0 {
		w := queue[0]
		queue = queue[1:]
		s := selected[w.url]
		parsed, _ := version.Parse(w.version)
		if !parsed.Compatible(s.parsed) {
			return nil, nil, errors.New(w.url + " is required at " + w.version + " by " + w.by + " and at " + s.version + " by " + s.by + "; a program builds against one")
		}

		required[w.url] = append(required[w.url], Requirement{w.version, w.by})
		if _, ok := first[w.url]; !ok {
			first[w.url] = w.by
		}

		key := w.url + fieldSeparator + s.version
		if !seen[key] {
			seen[key] = true
			queue = append(queue, byKey[key].requires...)
		}
	}

	return required, first, nil
}

// changed says how the module at version, now rec, differs from the
// record the lock file kept of it, was, both fetched anew: its tag moved
// to another commit, named by both with their hashes, or the same
// commit's files hash otherwise; and what to do.
func changed(was, rec record, v string) string {
	if was.commit != rec.commit {
		return "was locked at commit " + short(was.commit) + ", " + was.hash + ", and the tag " + tagPrefix + v + " now names commit " + short(rec.commit) + ", " + rec.hash + ":\nthe tag was moved to other code, or the repository rewritten; if the change is meant, remove the line and build again"
	}

	return "was locked with commit " + short(was.commit) + "'s files hashed " + was.hash + ", and the commit's files now hash " + rec.hash + ":\nonly two commits sharing one id make that; remove the line only if you trust the repository"
}

// short returns the first shortCommit digits of a commit, as a message
// shows it.
func short(commit string) string {
	if len(commit) > shortCommit {
		return commit[:shortCommit]
	}

	return commit
}

// readModule reads the module file of a required module's directory,
// or gives the directory alone, a module with no file, when it has none.
func readModule(dir string) (*Module, error) {
	name := filepath.Join(dir, ModFile)
	src, err := os.ReadFile(name)
	if err != nil {
		return &Module{Dir: dir}, nil
	}

	return ParseModFile(name, src)
}

// readLock returns the records of the lock file name by URL and
// version; none for a file that does not exist. A line that is not a
// URL, a version, a commit and a hash is refused with its line.
func readLock(name string) (map[string]record, error) {
	data, err := os.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]record{}, nil
	}

	if err != nil {
		return nil, err
	}

	locked := map[string]record{}
	for i, line := range strings.Split(string(data), lineEnd) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		if len(fields) != lockFields || !strings.HasPrefix(fields[3], HashPrefix) {
			return nil, errors.New(lineAt(name, i) + "a line of the lock file is a URL, a version, a commit and a hash, " + HashPrefix + "...; remove the file and build again to write it anew")
		}

		locked[fields[0]+fieldSeparator+fields[1]] = record{fields[2], fields[3]}
	}

	return locked, nil
}

// writeLockIfChanged writes the lock file name with a line for every
// module reached, sorted by URL and version, when its records differ
// from locked, what the file held: a file that differs only in its
// layout, a checkout's line endings, is left as it is.
func writeLockIfChanged(name string, locked map[string]record, byKey map[string]*reached) error {
	now := map[string]record{}
	for key, r := range byKey {
		now[key] = r.rec
	}

	if maps.Equal(now, locked) {
		return nil
	}

	keys := make([]string, 0, len(now))
	for key := range now {
		keys = append(keys, key)
	}

	sort.Slice(keys, func(i, j int) bool {
		a, b := byKey[keys[i]], byKey[keys[j]]
		ua, ub := strings.TrimSuffix(keys[i], fieldSeparator+a.version), strings.TrimSuffix(keys[j], fieldSeparator+b.version)
		if ua != ub {
			return ua < ub
		}

		return a.parsed.Compare(b.parsed) < 0
	})

	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key + fieldSeparator + now[key].commit + fieldSeparator + now[key].hash + lineEnd)
	}

	return replaceFile(name, b.String(), lockFileMode)
}

// replaceFile writes text to the file name through a file renamed into
// place, so that a reader finds the old file or the new one whole, and
// a write stopped part way leaves the old one. A link is followed, the
// file it names replaced and the link kept, and the old file's
// permissions are kept; a new file has mode.
func replaceFile(name, text string, mode os.FileMode) error {
	if target, err := filepath.EvalSymlinks(name); err == nil {
		name = target
	}

	if info, err := os.Stat(name); err == nil {
		mode = info.Mode().Perm()
	}

	tmp, err := os.CreateTemp(filepath.Dir(name), filepath.Base(name)+replacingPattern)
	if err != nil {
		return err
	}

	_, err = tmp.WriteString(text)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Chmod(tmp.Name(), mode)
	}

	if err == nil {
		err = os.Rename(tmp.Name(), name)
	}

	if err != nil {
		os.Remove(tmp.Name())
	}

	return err
}

// removeLock removes the lock file of a module in dir that reaches no
// module by version any more, whose every line would be dropped.
func removeLock(dir string) error {
	err := os.Remove(filepath.Join(dir, LockFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	return err
}
