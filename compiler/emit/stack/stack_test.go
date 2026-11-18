// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stack

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/snello-lang/snello/compiler/platform"
)

// libcTimeout bounds the measuring program's run, whose every case ends
// in well under a second. The measuring programs are POSIX's and
// Windows', each built with the system's compiler, clang on Windows,
// which has no cc, and the options of its build, binding every import
// as the program starts on Linux, and on Windows the C runtime's define
// and LLVM's linker.
const (
	libcTimeout       = 2 * time.Minute
	libcSource        = "libc.c"
	libcWindowsSource = "libc_windows.c"
	libcCC            = "cc"
	libcWindowsCC     = "clang"
	libcWindowsSuffix = ".exe"

	// unmeasuredSystem is a system none of the tables was measured on.
	unmeasuredSystem = system("freebsd")
)

var (
	libcFlags        = []string{"-O1", "-fno-builtin", "-pthread"}
	libcLibs         = []string{"-lm"}
	libcLinuxFlags   = []string{"-Wl,-z,now"}
	libcWindowsFlags = []string{"-O1", "-fno-builtin", "-D_CRT_SECURE_NO_WARNINGS", "-fuse-ld=lld"}
)

// image builds an Image from a table of functions, for the bound's own
// rules apart from any disassembler.
func image(a arch, fs ...*function) *Image {
	img := &Image{arch: a, os: linux, functions: map[string]*function{}, cLibrary: map[string]bool{"malloc": true}}
	for _, f := range fs {
		img.functions[f.name] = f
	}

	return img
}

func fn(name string, frame int64, calls ...call) *function {
	return &function{name: name, frame: frame, calls: calls}
}

func to(name string) call { return call{kind: direct, target: name} }

// base is the function a task's stack begins in, as the runtime's is:
// its frame and a call through a pointer, the call of the task's entry.
func base(frame int64, name ...string) *function {
	f := fn("base", frame, call{kind: pointer})
	if len(name) > 0 {
		f.name = name[0]
	}

	return f
}

func TestBoundDeepestPath(t *testing.T) {
	img := image(arm64,
		base(32),
		fn("run", 64, to("a"), to("b")),
		fn("a", 100),
		fn("b", 16, to("c")),
		fn("c", 200),
	)
	b := img.Bound("run", "base", 160, Options{})
	if !b.Known() || b.Bytes != 160+32+64+16+200+redZone { // run, then b, then c: deeper than a
		t.Fatalf("got %+v", b)
	}

	if got := strings.Join(b.Path, " "); got != "base run b c" {
		t.Fatalf("path %q, want base run b c", got)
	}
}

func TestBoundAmd64ReturnAddresses(t *testing.T) {
	img := image(amd64, base(8), fn("run", 16, to("a")), fn("a", 24))
	b := img.Bound("run", "base", 64, Options{})
	if want := int64(64 + 8 + returnAddress + 16 + returnAddress + 24 + redZone); b.Bytes != want {
		t.Fatalf("got %d, want %d", b.Bytes, want)
	}
}

func TestBoundLibrary(t *testing.T) {
	img := image(arm64, base(0), fn("run", 10, call{kind: library, target: "malloc"}))
	malloc, _ := cLibraryStack("malloc")
	if b := img.Bound("run", "base", 0, Options{}); b.Bytes != 10+malloc+redZone {
		t.Fatalf("a call into the C library: got %+v", b)
	}

	img = image(arm64, base(0), fn("run", 10, call{kind: threadLocal}))
	local, _ := cLibraryStack(threadLocalKey)
	if b := img.Bound("run", "base", 0, Options{}); b.Bytes != 10+local+redZone {
		t.Fatalf("a thread-local's accessor: got %+v", b)
	}

	img = image(arm64, base(0), fn("run", 10, call{kind: library, target: "getaddrinfo"}))
	img.cLibrary["getaddrinfo"] = true
	if b := img.Bound("run", "base", 0, Options{}); b.Known() || !strings.Contains(b.Reason, "not measured") {
		t.Fatalf("a C library function not measured: got %+v", b)
	}

	img = image(arm64, base(0), fn("run", 10, call{kind: library, target: "sqlite3_step"}))
	if b := img.Bound("run", "base", 0, Options{}); b.Known() || !strings.Contains(b.Reason, "other than the C library") {
		t.Fatalf("a call into another library: got %+v", b)
	}
}

// TestBoundUnmeasured leaves a task unbounded that calls into the C
// library of a system its stacks were never measured on, FreeBSD, a
// thread-local's accessor among the calls, and bounds the same calls on
// macOS on arm64, where they were.
func TestBoundUnmeasured(t *testing.T) {
	for _, c := range []call{{kind: library, target: "malloc"}, {kind: threadLocal}} {
		img := image(amd64, base(0), fn("run", 10, c))
		img.os = unmeasuredSystem
		if b := img.Bound("run", "base", 0, Options{}); b.Known() || !strings.Contains(b.Reason, "not measured on freebsd/amd64") {
			t.Errorf("%+v on freebsd/amd64: got %+v", c, b)
		}

		img = image(arm64, base(0), fn("run", 10, c))
		img.os = darwin
		if b := img.Bound("run", "base", 0, Options{}); !b.Known() {
			t.Errorf("%+v on darwin/arm64: got %+v", c, b)
		}
	}
}

func TestBoundUnbounded(t *testing.T) {
	for _, c := range []struct {
		name string
		img  *Image
		want string
	}{
		{"recursion", image(arm64, base(0), fn("run", 8, to("a")), fn("a", 8, to("run"))), "recursion"},
		{"pointer", image(arm64, base(0), fn("run", 8, call{kind: pointer})), "through a pointer"},
		{"missing", image(arm64, base(0), fn("run", 8, to("gone"))), "not in the binary"},
		{"unknown", image(arm64, base(0), fn("run", 8, to("a")), &function{name: "a", unknown: "a jump through a register in a"}), "jump through a register"},
		{"sets sp", image(arm64, base(0), fn("run", 8, to("a")), &function{name: "a", setsSP: true}), "sets the stack pointer"},
		{"no base", image(arm64, fn("run", 8)), "no function base"},
	} {
		b := c.img.Bound("run", "base", 0, Options{})
		if b.Known() || !strings.Contains(b.Reason, c.want) {
			t.Errorf("%s: got %+v, want a reason with %q", c.name, b, c.want)
		}
	}
}

func TestBoundOptions(t *testing.T) {
	img := image(arm64,
		base(0),
		fn("run", 8, to("drop"), to("panic"), to("switch")),
		fn("drop", 40, call{kind: pointer}),
		fn("elem", 500),
		fn("panic", 30, to("onstack")),
		&function{name: "onstack", frame: 16, setsSP: true, calls: []call{{kind: pointer}}, unknown: "a frame that depends on a value in onstack"}, // what it runs is on another stack
		&function{name: "switch", frame: 160, setsSP: true},
	)
	opts := Options{Targets: map[string][]string{"drop": {"elem"}}, Switches: []string{"switch", "onstack"}}
	b := img.Bound("run", "base", 0, opts)
	if !b.Known() || b.Bytes != 8+40+500+redZone {
		t.Fatalf("got %+v", b)
	}

	if opts.Targets["base"] != nil {
		t.Fatalf("Bound changed the caller's targets: %v", opts.Targets)
	}

	opts.Targets["drop"] = []string{"gone"}
	if b := img.Bound("run", "base", 0, opts); b.Known() {
		t.Fatalf("a pointer's target not in the binary: got %+v", b)
	}
}

func TestBoundUnderscore(t *testing.T) {
	img := image(arm64, base(16, "_base"), fn("_run", 8, call{kind: library, target: "___error"}))
	img.os = darwin
	img.cLibrary["___error"] = true
	errno, _ := cLibraryStack("__error")
	if b := img.Bound("run", "base", 0, Options{}); !b.Known() || b.Bytes != 16+8+errno+redZone {
		t.Fatalf("macOS's names: got %+v", b)
	}

	img = image(arm64, base(16), fn("run", 8, call{kind: library, target: "__errno_location"}), fn("_only", 8))
	img.cLibrary["__errno_location"] = true
	errno, _ = cLibraryStack("__errno_location")
	if b := img.Bound("run", "base", 0, Options{}); !b.Known() || b.Bytes != 16+8+errno+redZone {
		t.Fatalf("Linux's names, which keep their underscores: got %+v", b)
	}

	if b := img.Bound("only", "base", 0, Options{}); b.Known() {
		t.Fatalf("a name found by an underscore only macOS adds: got %+v", b)
	}
}

func TestImageString(t *testing.T) {
	img := image(arm64, fn("run", 8, to("a"), call{kind: library, target: "malloc"}, call{kind: pointer}, call{kind: threadLocal}), &function{name: "a", setsSP: true, unknown: "x"})
	s := img.String()
	for _, want := range []string{"run 8 a lib:malloc * tlv", "a 0 (x) (sets sp)"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
}

// TestMeasured reports the platforms the C library's stacks are measured
// on, and no other.
func TestMeasured(t *testing.T) {
	for _, c := range []struct {
		goos, goarch string
		want         bool
	}{{platform.Linux, platform.AMD64, true}, {platform.Linux, platform.ARM64, true}, {platform.Darwin, platform.ARM64, true}, {platform.Darwin, platform.AMD64, false}, {platform.Windows, platform.AMD64, true}, {platform.Windows, platform.ARM64, false}, {"plan9", platform.AMD64, false}} {
		if got := Measured(c.goos, c.goarch); got != c.want {
			t.Errorf("%s/%s: %v, want %v", c.goos, c.goarch, got, c.want)
		}
	}
}

// TestCLibraryStackAllowance counts each function of both tables at its
// figure and the margin, aligned, an alias at its function's, a Windows
// C runtime function's own code under the function overriding's name
// at the function's, and getaddrinfo at none.
func TestCLibraryStackAllowance(t *testing.T) {
	for sys, table := range map[system]map[string]int64{linux: cLibraryStacks, windows: windowsLibraryStacks} {
		for name, measured := range table {
			n, ok := libraryStack(sys, name)
			if !ok || n < measured+measured*cLibraryMargin/100 || n%stackAlign != 0 {
				t.Errorf("%s on %s: measured %d, counted at %d", name, sys, measured, n)
			}
		}
	}

	for alias, name := range cLibraryAliases {
		a, aok := cLibraryStack(alias)
		n, ok := cLibraryStack(name)
		if !aok || !ok || a != n {
			t.Errorf("%s is counted at %d, %s at %d", alias, a, name, n)
		}
	}

	if _, ok := cLibraryStack("getaddrinfo"); ok {
		t.Error("getaddrinfo has an allowance")
	}

	n, _ := libraryStack(windows, "memset")
	if o, ok := libraryStack(windows, "memset"+overrideDefault); !ok || o != n {
		t.Errorf("memset's own code under the function overriding's name is counted at %d, %v, memset at %d", o, ok, n)
	}
}

// TestCLibraryStack builds the measuring program of the system the
// tests run on with its compiler, testdata/libc.c on Linux and macOS,
// linked to bind every function as it starts as a Snello program is,
// and testdata/libc_windows.c on Windows, runs it, and holds what it
// measures each function of the system's libraries and of the C
// library to use to the function's allowance: a system whose libraries
// go deeper than the table allows fails here, before any task can be
// given a stack too small for it.
func TestCLibraryStack(t *testing.T) {
	if !Measured(runtime.GOOS, runtime.GOARCH) {
		t.Skipf("no task is bounded by the libraries' stacks on %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	cc := os.Getenv("CC")
	if cc == "" {
		cc = libcCC
		if runtime.GOOS == platform.Windows {
			cc = libcWindowsCC
		}
	}

	if _, err := exec.LookPath(cc); err != nil {
		t.Skipf("no %s", cc)
	}

	bin := filepath.Join(t.TempDir(), "libc")
	var args []string
	switch system(runtime.GOOS) {
	case windows:
		bin += libcWindowsSuffix
		args = slices.Concat(libcWindowsFlags, []string{"-o", bin, filepath.Join("testdata", libcWindowsSource)})
	case linux:
		args = slices.Concat(libcFlags, []string{"-o", bin, filepath.Join("testdata", libcSource)}, libcLibs, libcLinuxFlags)
	default:
		args = slices.Concat(libcFlags, []string{"-o", bin, filepath.Join("testdata", libcSource)}, libcLibs)
	}

	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), libcTimeout)
	defer cancel()
	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, bin)
	cmd.Stdout = &stdout // stderr has what __stack_chk_fail prints
	if err := cmd.Run(); err != nil {
		t.Fatalf("%v\n%s", err, stdout.String())
	}

	table := "cLibraryStacks"
	if system(runtime.GOOS) == windows {
		table = "windowsLibraryStacks"
	}

	lines := 0
	sc := bufio.NewScanner(&stdout)
	for sc.Scan() {
		name, figure, ok := strings.Cut(sc.Text(), " ")
		measured, err := strconv.ParseInt(figure, 10, 64)
		if !ok || err != nil {
			t.Fatalf("a line that is no name and figure: %q", sc.Text())
		}

		lines++
		allowed, known := libraryStack(system(runtime.GOOS), name)
		switch {
		case !known:
			t.Errorf("%s uses %d bytes and has no entry in %s", name, measured, table)
		case measured > allowed:
			t.Errorf("%s uses %d bytes, more than its allowance of %d: raise its entry in %s", name, measured, allowed, table)
		}
	}

	if lines == 0 {
		t.Fatal("the program measured nothing")
	}
}
