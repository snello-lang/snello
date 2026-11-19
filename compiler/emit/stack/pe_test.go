// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stack

import (
	"debug/pe"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/platform"
)

// peSource is a program of no C library that calls the system's
// libraries as a Windows program does: through the thunk the linker
// writes for an import declared plainly, and through the slot itself
// for one declared imported, as the SDK's headers declare every
// function; a tail call through a thunk; a frame larger than a page,
// checked by its own stack probe; and a call into a library that is
// not the system's.
const peSource = `unsigned long __stdcall GetCurrentProcessId(void);
__declspec(dllimport) unsigned long __stdcall GetCurrentThreadId(void);
void __stdcall Other(void);
static volatile char sink;
__attribute__((naked)) void __chkstk(void) { __asm__("ret"); }
__attribute__((noinline)) int big(int n) {
    volatile char b[20000];
    for (int i = 0; i < n; i++) {
        b[i] = (char)i;
    }

    return b[n / 2];
}
__attribute__((noinline)) unsigned long thunked(void) { return GetCurrentProcessId() + 1; }
__attribute__((noinline)) unsigned long slot(void) { return GetCurrentThreadId() + 1; }
__attribute__((noinline)) unsigned long tail(void) { return GetCurrentProcessId(); }
__attribute__((noinline)) void other(void) {
    Other();
    sink = 1;
}
int start(void) {
    other();
    return big(3) + (int)thunked() + (int)slot() + (int)tail();
}
`

// pePatchSource is patchSource as a Windows program of no C library
// lays it out: the table of bounds, task_entry, and the functions of two
// spawns' tasks, one with a bound and one recursive.
const pePatchSource = `#include <stdint.h>
volatile uint32_t sn_stack_bounds[2] = {4294967295u, 4294967295u};
volatile int ended;
__attribute__((noinline)) void sn_task_0_run(void *p) { volatile char buf[200]; for (int i = 0; i < 200; i++) buf[i] = (char)(intptr_t)p; ended = buf[199]; }
__attribute__((noinline)) void sn_task_1_run(void *p) { volatile char b[16]; b[0] = 1; if (p != 0) sn_task_1_run((char *)p - 1); ended = b[0]; }
__attribute__((noinline)) void task_entry(void (*run)(void *), void *p) { run(p); ended = 1; }
int start(void) {
    task_entry(ended > 5 ? sn_task_1_run : sn_task_0_run, 0);
    return 0;
}
`

// peLibraries are the libraries peSource imports from, by the text of
// the definition an import library is made from: other.dll's function
// by its ordinal alone, as a library may export one.
var peLibraries = map[string]string{
	"kernel32": "LIBRARY kernel32.dll\nEXPORTS\n  GetCurrentProcessId\n  GetCurrentThreadId\n",
	"other":    "LIBRARY other.dll\nEXPORTS\n  Other @5 NONAME\n",
}

// peMachines are the linker's names of the processors.
var peMachines = map[string]string{platform.AMD64: "x64", platform.ARM64: "arm64"}

// linkPE links src for Windows on the processor arch, against import
// libraries the linker makes from peLibraries, and returns the binary's
// path; it skips the test without clang, lld-link or a disassembler
// that reads PE.
func linkPE(t *testing.T, arch, src string) string {
	t.Helper()
	for _, tool := range []string{"clang", "lld-link"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("no " + tool)
		}
	}

	if _, err := exec.LookPath(llvmObjdump); err != nil && runtime.GOOS != string(darwin) {
		t.Skip("no " + llvmObjdump + ", which reads PE")
	}

	dir := t.TempDir()
	machine := "/machine:" + peMachines[arch]
	link := []string{"--target=" + platform.Target{OS: platform.Windows, Arch: arch}.Triple(), "-O2", "-fuse-ld=lld", "-nostdlib", "-Wl,/entry:start", "-Wl,/subsystem:console", "-Wl,/debug:symtab"}
	for name, def := range peLibraries {
		defPath, lib := filepath.Join(dir, name+".def"), filepath.Join(dir, name+".lib")
		if err := os.WriteFile(defPath, []byte(def), 0o644); err != nil {
			t.Fatal(err)
		}

		if out, err := exec.Command("lld-link", "/lib", "/def:"+defPath, machine, "/out:"+lib).CombinedOutput(); err != nil {
			t.Fatalf("import library: %v\n%s", err, out)
		}

		link = append(link, lib)
	}

	c, bin := filepath.Join(dir, "p.c"), filepath.Join(dir, "p.exe")
	if err := os.WriteFile(c, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := exec.Command("clang", append(link, "-o", bin, c)...).CombinedOutput(); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}

	return bin
}

// TestLoadPE reads a PE binary of each processor: the system's imports
// marked as the C library's and another library's not, a call through
// an import's thunk and through its slot each a call of the import, a
// tail call through a thunk too, and a frame the stack probe checked
// counted whole.
func TestLoadPE(t *testing.T) {
	for _, arch := range []string{platform.AMD64, platform.ARM64} {
		t.Run(arch, func(t *testing.T) {
			img, err := Load(linkPE(t, arch, peSource))
			if err != nil {
				t.Fatal(err)
			}

			if img.os != windows || string(img.arch) != arch {
				t.Errorf("read as %s/%s", img.os, img.arch)
			}

			if !img.cLibrary["GetCurrentProcessId"] || !img.cLibrary["GetCurrentThreadId"] || img.cLibrary["Other"] {
				t.Errorf("the system's imports: %v", img.cLibrary)
			}

			frame := int64(20000) // the array, rounded up below by each processor's alignment and saved registers
			if f := img.function("big"); f == nil || f.unknown != "" || f.setsSP || f.frame < frame || f.frame > frame+64 {
				t.Errorf("big: %+v, want a frame of at least %d", f, frame)
			}

			for name, want := range map[string]string{
				"thunked":             "GetCurrentProcessId",
				"slot":                "lib:GetCurrentThreadId",
				"tail":                "GetCurrentProcessId",
				"GetCurrentProcessId": "lib:GetCurrentProcessId",
				"other":               "Other",
				"Other":               "lib:Other",
			} {
				f := img.function(name)
				if f == nil {
					t.Errorf("no function %s in %v", name, img.Functions())
					continue
				}

				if got := kinds(f); !slices.Contains(strings.Fields(got), want) || f.unknown != "" {
					t.Errorf("%s: calls %q (%s), want %s among them", name, got, f.unknown, want)
				}
			}
		})
	}
}

// TestPatchPE patches the bounds of a PE binary of each processor: the
// table found by its symbol, the bounded task's bound written into it
// and the recursive task's left unbounded, and the table read back as
// written.
func TestPatchPE(t *testing.T) {
	for _, arch := range []string{platform.AMD64, platform.ARM64} {
		t.Run(arch, func(t *testing.T) {
			bin := linkPE(t, arch, pePatchSource)
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
		})
	}
}

// TestPESlotLibraries reads the library of each slot of a binary's
// table of imports, by the slot's address, in lower case.
func TestPESlotLibraries(t *testing.T) {
	f, err := pe.Open(linkPE(t, platform.AMD64, peSource))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	h, ok := f.OptionalHeader.(*pe.OptionalHeader64)
	if !ok {
		t.Fatal("not a 64-bit PE binary")
	}

	libraries, err := peSlotLibraries(f, h)
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, l := range libraries {
		got = append(got, l)
	}

	slices.Sort(got)
	if want := []string{"kernel32.dll", "kernel32.dll", "other.dll"}; !slices.Equal(got, want) {
		t.Errorf("the slots' libraries: %v, want %v", got, want)
	}
}

// TestIsWindowsLibrary holds the system's libraries apart from others,
// an API set among them.
func TestIsWindowsLibrary(t *testing.T) {
	for lib, want := range map[string]bool{
		"kernel32.dll":                     true,
		"ntdll.dll":                        true,
		"iphlpapi.dll":                     true,
		"api-ms-win-core-synch-l1-2-0.dll": true,
		"shell32.dll":                      false,
		"other.dll":                        false,
	} {
		if got := isWindowsLibrary(lib); got != want {
			t.Errorf("%s: %v, want %v", lib, got, want)
		}
	}
}

// TestLinkMap reads which functions came from Microsoft's C runtime from
// a linker's map, and counts a call of one at its measured stack, and a
// call of the program's own function named as one of the runtime's,
// which the link took over the runtime's, at its own code's; a name
// both the runtime and the program define, a static function of the
// program's beside the runtime's, is not the runtime's; and a function
// of the runtime the table does not hold leaves the task unbounded,
// never walked.
func TestLinkMap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "link.map")
	text := ` m

 Preferred load address is 0000000140000000

 Start         Length     Name                   Class
 0001:00000000 0000003eH .text                   CODE
 0001:00000000       round                      0000000140001000     m-d730b5.o
 0001:00000020       floor                      0000000140001020     m-d730b5.o
 0001:00002700       floor                      0000000140003700     libucrt:d:\th.obj.amd64fre\minkernel\crts\ucrt\src\appcrt\tran\amd64\floor.obj
 0001:00000010       run                        0000000140001010 f   C:\Users\A B\AppData\Local\Temp\snello-build\main.o
 0001:00002730       free                       0000000140003730     libucrt:d:\th.obj.amd64fre\minkernel\crts\ucrt\src\appcrt\heap\mt\objfre\amd64\free.obj
 0001:0000b500       memset                     000000014000c500     libvcruntime:D:\a\_work\1\s\objr\amd64\memset.obj
 0001:0004913c       MovUpTo128WithXMM          000000014004a13c     libvcruntime:D:\a\_work\1\s\objr\amd64\memcpy.obj
 0002:000079d8       __imp_HeapAlloc            00000001400159d8     kernel32:KERNEL32.dll
`
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	img := image(amd64, base(0), fn("run", 16, to("free"), to("round")), fn("free", 8), fn("round", 4000))
	img.os = windows
	if err := img.readLinkMap(path); err != nil {
		t.Fatal(err)
	}

	if !img.cRuntime["free"] || !img.cRuntime["memset"] || img.cRuntime["round"] || img.cRuntime["floor"] || img.cRuntime["run"] || img.cRuntime["__imp_HeapAlloc"] {
		t.Fatalf("the C runtime's functions: %v", img.cRuntime)
	}

	free, _ := libraryStack(windows, "free")
	b := img.Bound("run", "base", 0, Options{})
	if want := 16 + max(free, 4000) + 2*returnAddress + redZone; !b.Known() || b.Bytes != want || !slices.Contains(b.Path, "round") {
		t.Fatalf("got %+v, want %d bytes through round's own code", b, want)
	}

	img.cRuntime["round"] = true // as if the runtime's: counted at its measured figure, under the program's 4000
	if b := img.Bound("run", "base", 0, Options{}); !b.Known() || b.Bytes >= 16+4000 {
		t.Fatalf("round as the runtime's: got %+v", b)
	}

	img.functions["run"] = fn("run", 16, to("MovUpTo128WithXMM")) // the C runtime's, and in no table: never walked
	img.functions["MovUpTo128WithXMM"] = fn("MovUpTo128WithXMM", 8)
	if b := img.Bound("run", "base", 0, Options{}); b.Known() || !strings.Contains(b.Reason, "MovUpTo128WithXMM, a function of the C runtime whose stack is not measured") {
		t.Fatalf("a function of the C runtime with no figure: got %+v", b)
	}
}
