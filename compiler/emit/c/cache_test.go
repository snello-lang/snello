// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/snello-lang/snello/compiler/emit/internal/backendtest"
)

// TestCacheReuse builds a program twice and checks that the second
// build compiles no file of the runtime, and that a debug build, whose
// flags differ, compiles it anew once.
func TestCacheReuse(t *testing.T) {
	cc, log := backendtest.CountingCompiler(t, "cc")
	t.Setenv("CC", cc)
	t.Setenv("SNELLO_CACHE", t.TempDir())
	src := []byte(emitSource(t, "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"cached\") }\n"))
	for i, want := range []struct {
		debug    bool
		compiles int
	}{{false, 1}, {false, 1}, {true, 2}, {true, 2}} {
		dir := t.TempDir()
		bin, out, err := Build(dir, "cached", src, want.debug)
		if err != nil {
			t.Fatalf("build %d: %v\n%s", i, err, out)
		}

		printed, err := exec.Command(bin).Output()
		if err != nil || string(printed) != "cached\n" {
			t.Fatalf("build %d printed %q, %v", i, printed, err)
		}

		if got := backendtest.RuntimeCompiles(t, log); got != want.compiles {
			t.Errorf("after build %d (debug %v), the runtime was compiled %d times, want %d", i, want.debug, got, want.compiles)
		}

		if _, err := os.Stat(filepath.Join(dir, "sched.c")); err == nil {
			t.Errorf("build %d wrote the runtime's sources beside the program", i)
		}
	}
}

// TestCacheOff checks that SNELLO_CACHE=off compiles the runtime where
// the program is built, every time.
func TestCacheOff(t *testing.T) {
	cc, log := backendtest.CountingCompiler(t, "cc")
	t.Setenv("SNELLO_CACHE", "off")
	if _, ok := cacheRoot(); ok {
		t.Fatal("SNELLO_CACHE=off left the cache on")
	}

	args := append(append([]string{}, Flags...), Debug...)
	for i := 1; i <= 2; i++ {
		dir := t.TempDir()
		objs, out, err := RuntimeObjects(dir, cc, args)
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}

		if filepath.Dir(objs[0]) != dir {
			t.Errorf("with the cache off, the objects are in %s, want %s", filepath.Dir(objs[0]), dir)
		}

		if got := backendtest.RuntimeCompiles(t, log); got != i {
			t.Errorf("with the cache off, after %d builds the runtime was compiled %d times", i, got)
		}
	}
}

// TestCacheRoot checks where the cache lives: under SNELLO_CACHE when it
// names a directory, else under the user's cache directory.
func TestCacheRoot(t *testing.T) {
	t.Setenv("SNELLO_CACHE", "/some/where")
	if root, ok := cacheRoot(); !ok || root != filepath.Join("/some/where", "runtime") {
		t.Errorf("SNELLO_CACHE=/some/where gave %q, %v", root, ok)
	}

	t.Setenv("SNELLO_CACHE", "")
	user, err := os.UserCacheDir()
	if err != nil {
		t.Skip("no user cache directory")
	}

	if root, ok := cacheRoot(); !ok || root != filepath.Join(user, "snello", "runtime") {
		t.Errorf("no SNELLO_CACHE gave %q, %v", root, ok)
	}
}

// TestKey checks that the key changes with each thing the objects
// depend on, and with nothing else.
func TestKey(t *testing.T) {
	env := []string{"SDKROOT="}
	base := keyOf([]byte("runtime"), "cc 1", []string{"-O2", "-flto"}, env)
	if keyOf([]byte("runtime"), "cc 1", []string{"-O2", "-flto"}, env) != base {
		t.Error("the same inputs gave two keys")
	}

	for name, key := range map[string]string{
		"a runtime source":    keyOf([]byte("runtime changed"), "cc 1", []string{"-O2", "-flto"}, env),
		"the compiler":        keyOf([]byte("runtime"), "cc 2", []string{"-O2", "-flto"}, env),
		"a flag":              keyOf([]byte("runtime"), "cc 1", []string{"-O1", "-DSN_DEBUG"}, env),
		"the flags' split":    keyOf([]byte("runtime"), "cc 1", []string{"-O2 -flto"}, env),
		"the compiler's SDK":  keyOf([]byte("runtime"), "cc 1", []string{"-O2", "-flto"}, []string{"SDKROOT=/sdk"}),
		"a flag moved to env": keyOf([]byte("runtime"), "cc 1", []string{"-O2"}, []string{"-flto"}),
	} {
		if key == base {
			t.Errorf("a change to %s kept the key", name)
		}
	}
}

// TestCompilerID checks that a compiler is told apart by its path, by a
// rebuild in place, which changes its size or time, and by its version.
func TestCompilerID(t *testing.T) {
	dir := t.TempDir()
	write := func(name, version string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\necho '"+version+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}

		return path
	}

	id := func(cc string) string {
		s, err := compilerID(cc)
		if err != nil {
			t.Fatal(err)
		}

		return s
	}

	a := write("a", "fake 1.0")
	first := id(a)
	if !strings.Contains(first, "fake 1.0") || !strings.HasPrefix(first, a+" ") {
		t.Errorf("the identity %q names neither the path nor the version", first)
	}

	if id(write("b", "fake 1.0")) == first {
		t.Error("two compilers at two paths have one identity")
	}

	write("a", "fake 1.0 rebuilt") // the same path, another size
	if id(a) == first {
		t.Error("a compiler rebuilt in place kept its identity")
	}

	if _, err := compilerID(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing compiler has an identity")
	}
}

// TestHeadersUnchanged checks that an entry is used only while every
// system header it lists has the size and time it had when made.
func TestHeadersUnchanged(t *testing.T) {
	dir := t.TempDir()
	header := filepath.Join(dir, "stdio.h")
	if err := os.WriteFile(header, []byte("int printf();\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest, err := manifestOf(header)
	if err != nil {
		t.Fatal(err)
	}

	if !headersUnchanged(manifest) {
		t.Fatal("an unchanged header reads as changed")
	}

	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(header, later, later); err != nil {
		t.Fatal(err)
	}

	if headersUnchanged(manifest) {
		t.Error("a header given a new time reads as unchanged")
	}

	manifest, _ = manifestOf(header)
	if err := os.WriteFile(header, []byte("int printf(const char *, ...);\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	os.Chtimes(header, later, later)
	if headersUnchanged(manifest) {
		t.Error("a header of another size reads as unchanged")
	}

	os.Remove(header)
	if headersUnchanged(manifest) {
		t.Error("a removed header reads as unchanged")
	}
}

// manifestOf writes the manifest line of one header, as a made entry's
// holds it.
func manifestOf(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	return []byte(cacheVersion + "\n" + strconv.FormatInt(fi.Size(), 10) + " " + strconv.FormatInt(fi.ModTime().UnixNano(), 10) + " " + path + "\n"), nil
}

// TestDepfilePaths checks the reading of a compiler's dependency file:
// the target left out, lines continued by a backslash, a space escaped
// in a path.
func TestDepfilePaths(t *testing.T) {
	for _, tt := range []struct {
		name, data string
		want       []string
	}{
		{"continued lines and an escaped space", "/tmp/w/sched.o: /tmp/w/sched.c /tmp/w/snello.h \\\n  /usr/include/stdio.h /Library/My\\ SDK/sys/types.h\n",
			[]string{"/tmp/w/sched.c", "/tmp/w/snello.h", "/usr/include/stdio.h", "/Library/My SDK/sys/types.h"}},
		{"carriage returns and tabs", "sched.o:\tsched.c \\\r\n\tstdio.h\r\n", []string{"sched.c", "stdio.h"}},
		{"a target with an escaped space", "/tmp/my\\ dir/sched.o: /tmp/my\\ dir/sched.c\n", []string{"/tmp/my dir/sched.c"}},
		{"a dollar and a hash", "sched.o: /tmp/a$$b/sched.c /tmp/c\\#d/x.h\n", []string{"/tmp/a$b/sched.c", "/tmp/c#d/x.h"}},
		{"no trailing newline", "sched.o: sched.c stdio.h", []string{"sched.c", "stdio.h"}},
		{"the target line ends in a backslash", "sched.o: \\\n sched.c\n", []string{"sched.c"}},
		{"a drive-like path keeps its colon", "sched.o: /tmp/a:b/sched.c\n", []string{"/tmp/a:b/sched.c"}},
	} {
		if got := depfilePaths([]byte(tt.data)); !slices.Equal(got, tt.want) {
			t.Errorf("%s: depfilePaths = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestFillOnce checks that builds of one process that miss the same
// entry at once compile the runtime once, and leave the entry whole
// with nothing being made beside it.
func TestFillOnce(t *testing.T) {
	cc, log := backendtest.CountingCompiler(t, "cc")
	cache := t.TempDir()
	t.Setenv("SNELLO_CACHE", cache)
	args := append(append([]string{}, Flags...), Debug...)
	var wg sync.WaitGroup
	results := make([][]string, 4)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			objs, out, err := RuntimeObjects(t.TempDir(), cc, args)
			if err != nil {
				t.Errorf("%v\n%s", err, out)
			}

			results[i] = objs
		}(i)
	}

	wg.Wait()
	if got := backendtest.RuntimeCompiles(t, log); got != 1 {
		t.Errorf("four builds at once compiled the runtime %d times, want once", got)
	}

	for _, objs := range results[1:] {
		if !slices.Equal(objs, results[0]) {
			t.Errorf("the builds linked different objects: %v and %v", objs, results[0])
		}
	}

	entries, _ := os.ReadDir(filepath.Join(cache, "runtime"))
	if len(entries) != 1 || strings.HasPrefix(entries[0].Name(), "tmp-") {
		t.Errorf("the cache holds %v, want one entry", entries)
	}

	if _, err := os.Stat(filepath.Join(filepath.Dir(results[0][0]), manifestName)); err != nil {
		t.Errorf("the entry has no manifest: %v", err)
	}
}

// TestFillRace checks a build that finds, when it renames its entry
// into place, that another process made it first: it keeps the other's
// and removes its own.
func TestFillRace(t *testing.T) {
	cc, _ := backendtest.CountingCompiler(t, "cc")
	cache := t.TempDir()
	t.Setenv("SNELLO_CACHE", cache)
	t.Cleanup(func() { testHookBeforeRename = nil })
	var made string
	testHookBeforeRename = func(work, entry string) { // the other build: a whole entry, put in place first
		if err := os.CopyFS(entry, os.DirFS(work)); err != nil {
			t.Fatal(err)
		}

		made = work
	}

	objs, out, err := RuntimeObjects(t.TempDir(), cc, append(append([]string{}, Flags...), Debug...))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if _, err := os.Stat(made); err == nil {
		t.Errorf("the losing build left its own entry at %s", made)
	}

	if strings.Contains(objs[0], "tmp-") {
		t.Errorf("the build links its own objects, %s, not the entry's", objs[0])
	}
}

// TestStaleEntry checks that an entry whose objects are gone is set
// aside and made anew.
func TestStaleEntry(t *testing.T) {
	cc, log := backendtest.CountingCompiler(t, "cc")
	t.Setenv("SNELLO_CACHE", t.TempDir())
	args := append(append([]string{}, Flags...), Debug...)
	objs, out, err := RuntimeObjects(t.TempDir(), cc, args)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	os.Remove(objs[3])
	if _, out, err := RuntimeObjects(t.TempDir(), cc, args); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if got := backendtest.RuntimeCompiles(t, log); got != 2 {
		t.Errorf("an entry missing an object was used: the runtime was compiled %d times, want 2", got)
	}

	if _, err := os.Stat(objs[3]); err != nil {
		t.Errorf("the entry was not made anew: %v", err)
	}
}

// TestTrim checks that an entry unused for five days is removed, and a
// directory left being made or set aside for an hour, and nothing else.
func TestTrim(t *testing.T) {
	root := t.TempDir()
	ages := map[string]time.Duration{
		"fresh":             time.Minute,
		"old":               6 * 24 * time.Hour,
		"tmp-recent":        time.Minute,
		"tmp-abandoned":     2 * time.Hour,
		"used.stale-1":      2 * time.Hour,
		"used.stale-recent": time.Minute,
	}

	for name, age := range ages {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}

		then := time.Now().Add(-age)
		os.Chtimes(dir, then, then)
	}

	trim(root)
	var left []string
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		left = append(left, e.Name())
	}

	if want := []string{"fresh", "tmp-recent", "used.stale-recent"}; !slices.Equal(left, want) {
		t.Errorf("trim left %v, want %v", left, want)
	}
}

// TestCompilerInKey checks, end to end, that two compilers get two
// entries: the runtime compiled by each, with nothing shared.
func TestCompilerInKey(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("SNELLO_CACHE", cache)
	args := append(append([]string{}, Flags...), Debug...)
	ccA, log := backendtest.CountingCompiler(t, "cc")
	ccB, _ := backendtest.CountingCompiler(t, "cc") // the same compiler at another path
	for _, cc := range []string{ccA, ccB} {
		if _, out, err := RuntimeObjects(t.TempDir(), cc, args); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}

	entries, _ := os.ReadDir(filepath.Join(cache, "runtime"))
	if got := backendtest.RuntimeCompiles(t, log); got != 1 || len(entries) != 2 {
		t.Errorf("two compilers: the first compiled the runtime %d times (want 1) and the cache holds %d entries (want 2)", got, len(entries))
	}
}

// TestHeaderChangeRebuilds checks that an entry made from headers that
// have since changed is set aside and made anew: the manifest is given
// another time for one header, as an update of the system gives it.
func TestHeaderChangeRebuilds(t *testing.T) {
	cc, log := backendtest.CountingCompiler(t, "cc")
	cache := t.TempDir()
	t.Setenv("SNELLO_CACHE", cache)
	args := append(append([]string{}, Flags...), Debug...)
	objs, out, err := RuntimeObjects(t.TempDir(), cc, args)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	manifest := filepath.Join(filepath.Dir(objs[0]), manifestName)
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) < 3 {
		t.Fatalf("the manifest lists no system header:\n%s", data)
	}

	size, rest, _ := strings.Cut(lines[1], " ")
	mtime, path, _ := strings.Cut(rest, " ")
	lines[1] = size + " " + strconv.FormatInt(mustInt(t, mtime)+1, 10) + " " + path
	if err := os.WriteFile(manifest, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, out, err := RuntimeObjects(t.TempDir(), cc, args); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if got := backendtest.RuntimeCompiles(t, log); got != 2 {
		t.Errorf("a changed header left the entry in use: the runtime was compiled %d times, want 2", got)
	}

	entries, _ := os.ReadDir(filepath.Join(cache, "runtime"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}

	if len(names) != 2 || !strings.Contains(strings.Join(names, " "), ".stale-") {
		t.Errorf("the cache holds %v, want the new entry and the old set aside", names)
	}
}

func mustInt(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}

	return n
}

// TestDeadEntry checks that an entry without a manifest is made anew,
// once, and leaves nothing behind.
func TestDeadEntry(t *testing.T) {
	cc, log := backendtest.CountingCompiler(t, "cc")
	cache := t.TempDir()
	t.Setenv("SNELLO_CACHE", cache)
	args := append(append([]string{}, Flags...), Debug...)
	objs, out, err := RuntimeObjects(t.TempDir(), cc, args)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	entry := filepath.Dir(objs[0])
	os.Remove(filepath.Join(entry, manifestName))
	for i := 0; i < 2; i++ {
		if _, out, err := RuntimeObjects(t.TempDir(), cc, args); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}

	if got := backendtest.RuntimeCompiles(t, log); got != 2 {
		t.Errorf("after its manifest was removed, the runtime was compiled %d times, want 2", got)
	}

	if _, err := os.Stat(filepath.Join(entry, manifestName)); err != nil {
		t.Errorf("the entry was not made anew: %v", err)
	}

	entries, _ := os.ReadDir(filepath.Join(cache, "runtime"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "tmp-") {
			t.Errorf("a build left %s behind", e.Name())
		}
	}
}

// TestCacheFallback checks that a cache that cannot be used does not
// fail the build: the runtime is compiled beside the program, as with
// the cache off. A cache directory that cannot be made, and one whose
// path the compiler's dependency files escape.
func TestCacheFallback(t *testing.T) {
	cc, _ := backendtest.CountingCompiler(t, "cc")
	args := append(append([]string{}, Flags...), Debug...)
	for name, cache := range map[string]string{
		"an unwritable directory": filepath.Join(t.TempDir(), "file", "below"),
		"a dollar in the path":    filepath.Join(t.TempDir(), "a$b"),
	} {
		if name == "an unwritable directory" {
			os.WriteFile(filepath.Dir(cache), nil, 0o644) // a file where the directory would be
		}

		t.Setenv("SNELLO_CACHE", cache)
		dir := t.TempDir()
		objs, out, err := RuntimeObjects(dir, cc, args)
		if err != nil {
			t.Errorf("%s: the build failed: %v\n%s", name, err, out)
			continue
		}

		if name == "an unwritable directory" && filepath.Dir(objs[0]) != dir {
			t.Errorf("%s: the objects are in %s, want beside the program in %s", name, filepath.Dir(objs[0]), dir)
		}
	}
}
