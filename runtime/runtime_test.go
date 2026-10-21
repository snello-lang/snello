// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"unicode"
)

// TestUnicodeTables checks that c/unicode.c is what mkunicode.go writes
// from the Unicode Character Database in the directory TEAK_UCD names,
// so the tables cannot be edited by hand. It is skipped without one,
// since the database is not kept in the repository.
func TestUnicodeTables(t *testing.T) {
	ucd := os.Getenv("TEAK_UCD")
	if ucd == "" {
		t.Skip("TEAK_UCD names no copy of the Unicode Character Database")
	}

	out := filepath.Join(t.TempDir(), "unicode.c")
	if b, err := exec.Command("go", "run", "mkunicode.go", "-ucd", ucd, "-o", out).CombinedOutput(); err != nil {
		t.Fatalf("mkunicode: %v\n%s", err, b)
	}

	want, _ := os.ReadFile(out)
	got, _ := os.ReadFile(filepath.Join("c", "unicode.c"))
	if !bytes.Equal(got, want) {
		t.Fatal("c/unicode.c is stale: run go run mkunicode.go in runtime/")
	}
}

// TestUnicodeLookup compiles the tables with a program that asks them
// about every rune and compares each answer with Go's unicode package,
// an implementation of the same standard written apart: the case of
// every rune either way, whether it is white space, and whether it is
// printable. It runs when Go's Unicode is the version the tables were
// made from, since a rune added in another version is answered
// differently by right tables.
func TestUnicodeLookup(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("c", "unicode.c"))
	if err != nil {
		t.Fatal(err)
	}

	m := regexp.MustCompile(`Unicode Character Database\n// ([0-9.]+);`).FindSubmatch(src)
	if m == nil {
		t.Fatal("c/unicode.c names no Unicode version")
	}

	if string(m[1]) != unicode.Version {
		t.Skipf("the tables are Unicode %s, Go's unicode package %s", m[1], unicode.Version)
	}

	cc := os.Getenv("CC")
	if cc == "" {
		cc = "cc"
	}

	if _, err := exec.LookPath(cc); err != nil {
		t.Skip("no C compiler")
	}

	dir := t.TempDir()
	for name, data := range Files() {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	probe := `#include "teak.h"
#include <stdio.h>
int main(void) {
    for (int32_t r = 0; r <= 0x10FFFF; r++) {
        int32_t u = tk_rune_upper(r), l = tk_rune_lower(r);
        bool s = tk_rune_space(r), p = tk_isprint(r);
        if (u != r || l != r || s || !p) {
            printf("%X %X %X %d %d\n", r, u, l, s, p);
        }
    }
    return 0;
}
`
	if err := os.WriteFile(filepath.Join(dir, "probe.c"), []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(dir, "probe")
	if b, err := exec.Command(cc, "-std=c11", "-o", bin, filepath.Join(dir, "probe.c"), filepath.Join(dir, "unicode.c")).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}

	got, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatal(err)
	}

	var want bytes.Buffer
	for r := rune(0); r <= unicode.MaxRune; r++ {
		u, l, s, p := unicode.ToUpper(r), unicode.ToLower(r), unicode.IsSpace(r), unicode.IsPrint(r)
		if u != r || l != r || s || !p {
			fmt.Fprintf(&want, "%X %X %X %d %d\n", r, u, l, bit(s), bit(p))
		}
	}

	if !bytes.Equal(got, want.Bytes()) {
		g, w := bytes.Split(got, []byte("\n")), bytes.Split(want.Bytes(), []byte("\n"))
		for i := 0; i < len(g) && i < len(w); i++ {
			if !bytes.Equal(g[i], w[i]) {
				t.Fatalf("line %d: the tables say %q, Go says %q", i+1, g[i], w[i])
			}
		}

		t.Fatalf("the tables give %d lines, Go %d", len(g), len(w))
	}
}

// TestRemoveAllDeep removes a tree four hundred directories deep
// through tk_os_removeAll, called from a spawned task, whose stack is
// 256 KB: the walk must keep no frame per level, since a tree can be
// deeper than any stack, and must work relative to a directory already
// open, since a path can be longer than the system opens in one call.
// The depth is what a kernel makes and removes in a second or two,
// Darwin's doing work that grows with the depth on every call under
// the tree: a walk with a stat buffer and a few words per level, some
// 220 bytes, would need about twelve hundred levels to run past the
// stack, and Darwin's path limit of 1024 bytes is passed at about five
// hundred, so this depth guards the shape of the walk, not its limits.
// The tree is made a level at a time through os.Root, which works
// relative to an open directory the same way.
func TestRemoveAllDeep(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "tree")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}

	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 400; i++ {
		if err := r.Mkdir("a", 0o755); err != nil {
			t.Fatal(err)
		}

		sub, err := r.OpenRoot("a")
		if err != nil {
			t.Fatal(err)
		}

		r.Close()
		r = sub
	}

	r.Close()
	probe := `#include "teak.h"
#include <stdio.h>
#include <string.h>
static tk_chan *done;
static tk_string path;
static void run(void *arg) {
    (void)arg;
    tk_string failed = TK_STR("", 0), op = TK_STR("", 0);
    int64_t code = tk_os_removeAll(path, &failed, &op);
    printf("%lld", (long long)code);
    if (code != 0) {
        printf(" %.*s %.*s", (int)op.len, (const char *)op.ptr, (int)failed.len, (const char *)failed.ptr);
    }

    printf("\n");
    tk_string_drop(&failed);
    tk_string_drop(&op);
    tk_chan_send(done, &code);
}
int main(int argc, char **argv) {
    tk_init(argc, argv);
    path = TK_STR(argv[1], (int64_t)strlen(argv[1]));
    done = tk_chan_make(sizeof(int64_t), 1, NULL); // buffered: the sender is done with it once its value is in
    tk_spawn(run, NULL);
    int64_t code;
    tk_chan_recv(done, &code);
    tk_chan_drop(&done); // the task's work is over: the last handle frees the channel
    return tk_finish();
}
`
	bin := buildProbe(t, filepath.Join(dir, "build"), probe)
	out, err := exec.Command(bin, root).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if string(out) != "0\n" {
		t.Fatalf("RemoveAll gave %q, want 0", out)
	}

	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("the tree is still there: %v", err)
	}
}

func bit(b bool) int {
	if b {
		return 1
	}

	return 0
}

// buildProbe compiles a C program against the whole runtime in dir, as
// the C backend compiles a program and with its flags, as a debug build
// so that the runtime's checks and its leak report run; it skips the
// test without a C compiler.
func buildProbe(t *testing.T, dir, src string) string {
	t.Helper()
	cc := os.Getenv("CC")
	if cc == "" {
		cc = "cc"
	}

	if _, err := exec.LookPath(cc); err != nil {
		t.Skip("no C compiler")
	}

	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	for name, data := range Files() {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "probe.c"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(dir, "probe")
	args := []string{"-std=c11", "-Wall", "-Wextra", "-Werror", "-Wno-tautological-compare", "-fwrapv", "-fno-strict-aliasing", "-pthread", "-O1", "-DTK_DEBUG", "-o", bin, filepath.Join(dir, "probe.c")}
	args = append(args, Paths(dir)...)
	args = append(args, "-lm")
	if b, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}

	return bin
}
