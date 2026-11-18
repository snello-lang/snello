// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package trace

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/runtime"
)

// program is C as the C backend writes it, each line of a Snello
// function's body under a line directive naming the program's file and
// the generated lines under the generated name: at is a function of its
// own that render calls, and main is generated.
const program = `#include <stdlib.h>
#line 10 "grid.snello"
__attribute__((noinline)) static int at(const int *xs, int n, int i) { if (i >= n) abort(); return xs[i]; }
#line 1 "<generated>"
#line 20 "grid.snello"
int render(const int *xs, int n) { return at(xs, n, 5); }
#line 1 "<generated>"
int main(void) { int xs[3] = { 0 }; return render(xs, 3); }
`

// between is C with a function of no debugging information, as the
// runtime's are in a release build, between two functions of the
// program in one section, whose line table runs on across it.
const between = `#line 10 "grid.snello"
__attribute__((noinline)) int first(int x) { return x + 1; }
#line 1 "<generated>"
__attribute__((nodebug, noinline)) int helper(int x) { return x * 3; }
#line 30 "grid.snello"
__attribute__((noinline)) int last(int x) { return helper(x) + first(x); }
`

// TestTableAndPatch reads the table of a compiled object and writes its
// anchors into the linked binary: the table has an anchor for each
// function, a frame of at at its line and of render at its own, and
// none of the generated main; once patched, the anchors hold the
// functions' addresses in the binary, in order of address, and the
// reference word holds its own address.
func TestTableAndPatch(t *testing.T) {
	cc, dir := compilerAndHeaders(t)
	compile := func(name, src string, flags ...string) string {
		return compileC(t, cc, dir, name, src, flags...)
	}

	// A debug build's object: unoptimized, with every variable, type and
	// block described, and its file named by its path, which the table
	// holds by its base name.
	debugObj := compile("debug", strings.ReplaceAll(program, `"grid.snello"`, `"src/grid.snello"`), "-O0", "-g")
	debugTable, debugLayout, err := Table(debugObj)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"\x00grid.snello\x00", "\x00at\x00", "\x00render\x00"} {
		if !strings.Contains(string(debugTable[headerBytes:]), want) {
			t.Errorf("the debug object's table lacks %q", want)
		}
	}

	if !slices.Contains(debugLayout.Anchors, "at") || !slices.Contains(debugLayout.Anchors, "render") {
		t.Errorf("the debug object's anchors %v lack at or render", debugLayout.Anchors)
	}

	obj := compile("program", program)
	table, layout, err := Table(obj)
	if err != nil {
		t.Fatal(err)
	}

	text := string(table[headerBytes:]) // the parts: the names are among them

	for _, name := range []string{"at", "render", "main"} {
		found := false
		for _, a := range layout.Anchors {
			found = found || a == name
		}

		if !found {
			t.Errorf("no anchor of %s among %v", name, layout.Anchors)
		}
	}

	for _, want := range []string{"grid.snello\x00", "\x00at\x00", "\x00render\x00"} {
		if !strings.Contains(text, want) {
			t.Errorf("the table lacks %q:\n%q", want, text)
		}
	}

	var h header
	if err := binary.Read(bytes.NewReader(table), binary.LittleEndian, &h); err != nil {
		t.Fatal(err)
	}

	lines := map[uint32]bool{}
	for i := int64(0); i < h.NFrames; i++ {
		lines[binary.LittleEndian.Uint32(table[headerBytes+uint64(h.Frames)+uint64(i)*frameBytes+2*offsetBytes:])] = true
	}

	for _, line := range []uint32{10, 20} {
		if !lines[line] {
			t.Errorf("no frame at line %d among %v", line, lines)
		}
	}

	if strings.Contains(text, "\x00main\x00") {
		t.Errorf("the generated main has a frame:\n%q", text)
	}

	object, err := Object(table, platform.Host())
	if err != nil {
		t.Fatal(err)
	}

	tableObject := filepath.Join(dir, "table.o")
	if err := os.WriteFile(tableObject, object, 0o644); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(dir, "program")
	if out, err := exec.Command(cc, "-o", bin, obj, tableObject).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	data, err := Patch(bin, layout)
	if err != nil {
		t.Fatal(err)
	}

	addresses, place, err := symbols(bin)
	if err != nil {
		t.Fatal(err)
	}

	header, ok := place(tableSymbol)
	parts, found := place(partsSymbol)
	if !ok || !found {
		t.Fatal("no table in the binary")
	}

	at := parts + binary.LittleEndian.Uint64(data[header+anchorsField:])

	held := map[uint64]bool{}
	last := uint64(0)
	for i := range layout.Anchors {
		got := binary.LittleEndian.Uint64(data[at+uint64(i)*anchorBytes:])
		if got < last {
			t.Errorf("anchor %d holds %#x, below the one before it, %#x", i, got, last)
		}

		held[got], last = true, got
	}

	for _, name := range layout.Anchors {
		if !held[addresses[name]] {
			t.Errorf("no anchor holds the address of %s, %#x", name, addresses[name])
		}
	}

	if got := binary.LittleEndian.Uint64(data[header:]); got != addresses[tableSymbol] {
		t.Errorf("the reference holds %#x, the table's address is %#x", got, addresses[tableSymbol])
	}
}

// TestFunctionWithoutLines holds a function the debugging information
// says nothing of to no frame, beside two functions of the program, in
// one text section, where the line rows of the function before it reach
// over its code, and in a section of each function's own, every one at
// address zero in the object, as LLVM's linker leaves the link-time
// optimizer's object: its anchor has no rows, so that a walk passing
// through a frame of it shows nothing for it, while the functions on
// either side keep theirs.
func TestFunctionWithoutLines(t *testing.T) {
	cc, dir := compilerAndHeaders(t)
	var elf []string // an ELF object, which a Mac's clang writes for Linux
	if platform.Host().OS == platform.Darwin {
		elf = []string{"--target=" + platform.Target{OS: platform.Linux, Arch: platform.Host().Arch}.Triple()}
	}

	for _, sections := range []string{"-fno-function-sections", "-ffunction-sections"} {
		obj := compileC(t, cc, dir, "between", between, append([]string{"-O0", "-g", sections}, elf...)...)
		checkWithoutLines(t, sections, obj)
	}
}

// checkWithoutLines checks the table of obj, the between program
// compiled with the flag named: helper has no rows, first and last some.
func checkWithoutLines(t *testing.T, flag, obj string) {
	t.Helper()
	table, layout, err := Table(obj)
	if err != nil {
		t.Fatal(err)
	}

	var h header
	if err := binary.Read(bytes.NewReader(table), binary.LittleEndian, &h); err != nil {
		t.Fatal(err)
	}

	rows := map[string]uint32{}
	for i, name := range layout.Anchors {
		record := table[headerBytes+uint64(h.Anchors)+uint64(i)*anchorBytes:]
		rows[name] = binary.LittleEndian.Uint32(record[anchorsField+2*offsetBytes:])
	}

	if rows["helper"] != 0 {
		t.Errorf("%s: helper, with no debugging information, has %d rows: another function's reached over it", flag, rows["helper"])
	}

	if rows["first"] == 0 || rows["last"] == 0 {
		t.Errorf("%s: first and last have %d and %d rows, want some each", flag, rows["first"], rows["last"])
	}
}

// compilerAndHeaders returns the C compiler, CC or cc, and a directory
// holding the runtime's headers, or skips the test without a compiler.
func compilerAndHeaders(t *testing.T) (string, string) {
	t.Helper()
	cc := os.Getenv("CC")
	if cc == "" {
		cc = "cc"
	}

	if _, err := exec.LookPath(cc); err != nil {
		t.Skip("no C compiler")
	}

	dir := t.TempDir()
	for name, data := range runtime.Files() {
		if strings.HasSuffix(name, ".h") {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	return cc, dir
}

// compileC compiles the C src, as name.c in dir, to an object with cc,
// optimized a little and with line tables unless flags say otherwise,
// and returns the object's path.
func compileC(t *testing.T, cc, dir, name, src string, flags ...string) string {
	t.Helper()
	file := filepath.Join(dir, name+".c")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	obj := filepath.Join(dir, name+".o")
	args := append(append([]string{"-std=c11", "-O1", "-g1", "-fno-omit-frame-pointer", "-I", dir}, flags...), "-c", "-o", obj, file)
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	return obj
}
