// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stack

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseInst(t *testing.T) {
	for _, c := range []struct {
		line string
		want inst
	}{
		{"0000000100001c70\tstp\tx29, x30, [sp, #-0x10]!", inst{addr: 0x100001c70, op: "stp", args: "x29, x30, [sp, #-0x10]!"}},
		{"0000000100001954\tbl\t0x100004870 ; symbol stub for: _malloc", inst{addr: 0x100001954, op: "bl", args: "0x100004870", note: "symbol stub for: _malloc"}},
		{"0000000100001944\tadrp\tx21, 11 ; 0x10000c000", inst{addr: 0x100001944, op: "adrp", args: "x21, 11", note: "0x10000c000"}},
		{"  4005d4:\tbl\t400480 <malloc@plt>", inst{addr: 0x4005d4, op: "bl", args: "400480 <malloc@plt>"}},
		{"  400600:\tstp\tx29, x30, [sp, #-48]!", inst{addr: 0x400600, op: "stp", args: "x29, x30, [sp, #-48]!"}},
		{"  40113a:\tcall   *0x2eb0(%rip)        # 403ff0 <free@GLIBC_2.2.5>", inst{addr: 0x40113a, op: "call", args: "*0x2eb0(%rip)", note: "403ff0 <free@GLIBC_2.2.5>"}},
		{"  401126:\tpush   %rbp", inst{addr: 0x401126, op: "push", args: "%rbp"}},
	} {
		got, ok := parseInst(c.line)
		if !ok || got != c.want {
			t.Errorf("%q: got %+v, %v; want %+v", c.line, got, ok, c.want)
		}
	}

	for _, line := range []string{"", "(__TEXT,__text) section", "Disassembly of section .text:", "/tmp/a.out:     file format elf64-littleaarch64"} {
		if got, ok := parseInst(line); ok {
			t.Errorf("%q read as an instruction: %+v", line, got)
		}
	}
}

func TestParseTarget(t *testing.T) {
	for _, c := range []struct {
		args, note string
		want       target
	}{
		{"_sn_free", "", target{name: "_sn_free", known: true}},
		{"0x100004870", "symbol stub for: _malloc", target{name: "_malloc", library: true, known: true}},
		{"0x100001978", "", target{addr: 0x100001978, known: true}},
		{"400480 <malloc@plt>", "", target{name: "malloc", library: true, known: true}},
		{"400700 <tail>", "", target{name: "tail", known: true}},
		{"400614 <run+0x14>", "", target{name: "run", offset: 0x14, known: true}},
		{"x8", "", target{}},
	} {
		if got := parseTarget(c.args, c.note); got != c.want {
			t.Errorf("%q %q: got %+v, want %+v", c.args, c.note, got, c.want)
		}
	}
}

func TestIsCLibrary(t *testing.T) {
	for lib, want := range map[string]bool{
		"/usr/lib/libSystem.B.dylib": true,
		"libc.so.6":                  true,
		"libpthread.so.0":            true,
		"ld-linux-aarch64.so.1":      true,
		"libsqlite3.so.0":            false,
		"/usr/lib/libz.1.dylib":      false,
	} {
		if got := isCLibrary(lib); got != want {
			t.Errorf("%s: got %v", lib, got)
		}
	}
}

// TestRead splits a listing into functions, macOS's and Linux's, and
// keeps the worse of two local functions of one name.
func TestRead(t *testing.T) {
	img := &Image{arch: arm64, functions: map[string]*function{}, cLibrary: map[string]bool{}}
	img.read([]byte(`/tmp/a:
(__TEXT,__text) section
_run:
0000000100000010	sub	sp, sp, #0x20
0000000100000014	bl	_leaf
0000000100000018	ret
_leaf:
0000000100000020	sub	sp, sp, #0x10
0000000100000024	ret
_leaf:
0000000100000030	sub	sp, sp, #0x40
0000000100000034	ret
`), [2]uint64{})
	if f := img.functions["_run"]; f == nil || f.frame != 0x20 || len(f.calls) != 1 || f.calls[0].target != "_leaf" {
		t.Fatalf("_run: %+v", f)
	}

	if f := img.functions["_leaf"]; f == nil || f.frame != 0x40 {
		t.Fatalf("two _leaf, the larger kept: %+v", f)
	}
}

// loadSource is the program TestLoad and TestLoadLazy build: a frame of
// a known size, a call into the C library, write, which no compiler may
// drop, and a chain of calls.
const loadSource = `#include <string.h>
#include <unistd.h>
__attribute__((noinline)) int leaf(int n) { volatile char buf[512]; memset((char *)buf, n, sizeof buf); return buf[n & 511]; }
__attribute__((noinline)) int middle(int n) { write(1, "middle\n", 7); return leaf(n); }
__attribute__((noinline)) int base(int n) { return middle(n) + 1; }
int main(int argc, char **argv) { (void)argv; return base(argc); }
`

// The link options that have the system bind every import at its
// first call, for TestLoadLazy: Linux's, and macOS's that turn off the
// chained fixups that bind them all as the program starts.
var lazyFlags = map[system][]string{linux: {"-Wl,-z,lazy"}, darwin: {"-Wl,-no_fixup_chains"}}

// loadC builds loadSource with the system's compiler, optimized, with
// the link options flags, and loads the binary.
func loadC(t *testing.T, flags ...string) *Image {
	t.Helper()
	cc := os.Getenv("CC")
	if cc == "" {
		cc = "cc"
	}

	tool := objdump
	if system(runtime.GOOS) == darwin {
		tool = otool
	}

	for _, need := range []string{cc, tool} {
		if _, err := exec.LookPath(need); err != nil {
			t.Skipf("no %s", need)
		}
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "p.c")
	if err := os.WriteFile(src, []byte(loadSource), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(dir, "p")
	if out, err := exec.Command(cc, append([]string{"-O2", "-o", bin, src}, flags...)...).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	img, err := Load(bin)
	if err != nil {
		t.Fatal(err)
	}

	return img
}

// TestLoad bounds loadSource's functions, linked as a Snello program is.
func TestLoad(t *testing.T) {
	var flags []string
	if system(runtime.GOOS) == linux {
		flags = []string{"-Wl,-z,now"}
	}

	img := loadC(t, flags...)
	if img.lazy {
		t.Fatalf("a binary that binds every import as it starts is read as lazy")
	}

	leaf := img.function("leaf")
	if leaf == nil || leaf.frame < 512 {
		t.Fatalf("leaf's frame holds its 512 bytes: %+v\n%s", leaf, img)
	}

	b := img.Bound("middle", "base", 0, Options{})
	write, _ := cLibraryStack("write")
	if deepest := img.function("middle").frame + max(leaf.frame, write); !b.Known() || b.Bytes < deepest { // the worse of leaf and write, after middle's own frame
		t.Fatalf("middle calls leaf and puts: got %+v\n%s", b, img)
	}
}

// TestLoadLazy checks a binary that binds an import at its first call,
// on the caller's stack, gives a call of it no bound.
func TestLoadLazy(t *testing.T) {
	img := loadC(t, lazyFlags[system(runtime.GOOS)]...)
	if b := img.Bound("middle", "base", 0, Options{}); !img.lazy || b.Known() || !strings.Contains(b.Reason, "first call") {
		t.Fatalf("got %+v, lazy %v", b, img.lazy)
	}
}

func TestLoadNotABinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x")
	if err := os.WriteFile(path, []byte("not a binary"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "neither Mach-O nor ELF") {
		t.Fatalf("got %v", err)
	}
}
