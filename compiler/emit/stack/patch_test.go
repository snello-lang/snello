// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stack

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/compiler/types"
	"github.com/snello-lang/snello/internal/testproc"
)

// patchSource is a program laid out as a Snello program is for Patch:
// the table of bounds, the runtime's task_entry, which calls a task's
// function through a pointer, and the functions of two spawns' tasks,
// one with a bound and one recursive. It prints its table as it runs.
// windowsLineEnd and unixLineEnd are a line's end as Windows' C library
// writes one to a stream in text mode and as every other system does.
const (
	windowsLineEnd = "\r\n"
	unixLineEnd    = "\n"
	patchSource    = `#include <stdint.h>
#include <stdio.h>
volatile uint32_t sn_stack_bounds[2] = {4294967295u, 4294967295u};
volatile int ended;
__attribute__((noinline)) void sn_task_0_run(void *p) { volatile char buf[200]; for (int i = 0; i < 200; i++) buf[i] = (char)(intptr_t)p; ended = buf[199]; }
__attribute__((noinline)) void sn_task_1_run(void *p) { volatile char b[16]; b[0] = 1; if (p != NULL) sn_task_1_run((char *)p - 1); ended = b[0]; }
__attribute__((noinline)) void task_entry(void (*run)(void *), void *p) { run(p); ended = 1; }
int main(int argc, char **argv) {
	(void)argv;
	task_entry(argc > 5 ? sn_task_1_run : sn_task_0_run, NULL);
	printf("%u %u\n", (unsigned)sn_stack_bounds[0], (unsigned)sn_stack_bounds[1]);
	return 0;
}
`
)

// patchWindowsFlags link a program on Windows as a Snello program is:
// by LLVM's linker, keeping the symbol table.
var patchWindowsFlags = []string{"-D_CRT_SECURE_NO_WARNINGS", "-fuse-ld=lld", "-Wl,/debug:symtab"}

// buildPatch builds src with the system's compiler as a Snello program
// is linked, and returns the binary: on Windows by clang through LLVM's
// linker, keeping the symbols the analysis finds the functions by.
func buildPatch(t *testing.T, src string) string {
	t.Helper()
	cc := os.Getenv("CC")
	if cc == "" {
		cc = libcCC
		if runtime.GOOS == platform.Windows {
			cc = libcWindowsCC
		}
	}

	tool := objdump
	switch system(runtime.GOOS) {
	case darwin:
		tool = otool
	case windows:
		tool = llvmObjdump
	}

	for _, need := range []string{cc, tool} {
		if _, err := exec.LookPath(need); err != nil {
			t.Skipf("no %s", need)
		}
	}

	dir := t.TempDir()
	c := filepath.Join(dir, "p.c")
	if err := os.WriteFile(c, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(dir, "p")
	if system(runtime.GOOS) == windows {
		bin += libcWindowsSuffix
	}

	args := []string{"-O1", "-fno-stack-protector", "-o", bin, c}
	switch system(runtime.GOOS) {
	case linux:
		args = append(args, libcLinuxFlags...)
	case windows:
		args = append(args, patchWindowsFlags...)
	}

	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	return bin
}

func TestPatch(t *testing.T) {
	bin := buildPatch(t, patchSource)
	bounds, err := Patch(bin, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	if len(bounds) != 2 || !bounds[0].Known() || bounds[0].Bytes < 200 || bounds[1].Known() || !strings.Contains(bounds[1].Reason, "recursion") {
		t.Fatalf("bounds %+v, want the first known and the second a recursion", bounds)
	}

	table, err := Table(bin)
	if err != nil || len(table) != 2 || int64(table[0]) != bounds[0].Bytes || table[1] != lower.Unbounded {
		t.Fatalf("table %v, %v, want %d and unbounded", table, err, bounds[0].Bytes)
	}

	out, err := testproc.Command(bin).CombinedOutput()                  // on macOS, a binary whose signature no longer matched would be killed
	got := strings.ReplaceAll(string(out), windowsLineEnd, unixLineEnd) // Windows' C library writes a line's end as two bytes to a stream in text mode
	if want := fmt.Sprintf("%d %d\n", table[0], table[1]); err != nil || got != want {
		t.Fatalf("the patched program: %v, %q, want %q", err, got, want)
	}

	if _, err := os.Stat(bin + patched); !os.IsNotExist(err) {
		t.Errorf("the copy written beside the binary is left: %v", err)
	}
}

// TestPatchNoSpawn checks a program with no spawn is left as linked.
func TestPatchNoSpawn(t *testing.T) {
	bin := buildPatch(t, strings.NewReplacer("sn_task_0_run", "task_zero", "sn_task_1_run", "task_one").Replace(patchSource))
	before, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}

	if bounds, err := Patch(bin, nil, ""); err != nil || bounds != nil {
		t.Fatalf("got %v, %v", bounds, err)
	}

	if after, err := os.ReadFile(bin); err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the binary changed: %v", err)
	}
}

func TestPatchErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x")
	if err := os.WriteFile(path, []byte("not a binary"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Patch(path, nil, ""); err == nil {
		t.Error("Patch of no binary: no error")
	}

	if _, err := Table(path); err == nil {
		t.Error("Table of no binary: no error")
	}

	bin := buildPatch(t, strings.ReplaceAll(patchSource, "sn_stack_bounds", "other_table"))
	if _, err := Table(bin); err == nil || !strings.Contains(err.Error(), "no table") {
		t.Errorf("Table of a program with spawns and no table: %v", err)
	}

	bin = buildPatch(t, strings.NewReplacer("sn_stack_bounds", "other_table", "sn_task_0_run", "task_zero", "sn_task_1_run", "task_one").Replace(patchSource))
	if table, err := Table(bin); err != nil || len(table) != 0 {
		t.Errorf("Table of a program with no spawn and no table: %v, %v", table, err)
	}
}

func TestRuntimeOptions(t *testing.T) {
	drop, chanDrop, hash, init := lower.ElemDropPrefix+"a", lower.ChanDropPrefix+"c", lower.KeyHashPrefix+"k", lower.LazyInitPrefix+"v"
	caught := lower.SpawnBody(0) + " " + lower.CatchAdapter(types.Typ[types.Int])
	img := image(arm64, fn(drop, 8), fn(chanDrop, 8), fn(lower.ChanDropPrefix+"b.isra.0", 8), fn(hash, 8), fn(init, 8), fn(mapPrefix+"find", 8), fn("other", 8), fn(lower.SpawnBody(0), 8), fn(lower.CatchAdapter(types.Typ[types.Int]), 8))
	opts := img.runtimeOptions()
	if got := strings.Join(opts.Targets[chanDropCaller], " "); got != chanDrop {
		t.Errorf("the channel's drops %q, want the channels' one not a copy, and no map's", got)
	}

	if got := strings.Join(opts.Targets[lazyMaker], " "); got != init {
		t.Errorf("the initializers %q", got)
	}

	if got := strings.Join(opts.Targets[catcher], " "); got != caught {
		t.Errorf("the functions run under a mark %q, want %q", got, caught)
	}

	for _, f := range []string{mapPrefix + "find", "find_slot"} {
		if got := strings.Join(opts.Targets[f], " "); got != drop+" "+hash {
			t.Errorf("%s's targets %q, want the map's drop and the key hash, and no channel's", f, got)
		}
	}

	if _, ok := opts.Targets["other"]; ok {
		t.Error("a function of no map has targets")
	}

	if strings.Join(opts.Switches, " ") != switchTask+" "+onStack+" "+onBoundedStack+" "+longJump {
		t.Errorf("switches %v", opts.Switches)
	}
}
