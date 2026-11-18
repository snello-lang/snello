// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/runtime"
)

// TestProgramFlags holds the program's C to the line tables in a release
// build and to no optimization and the full debugging information in a
// debug one, after the build's own options, so that they win, with a
// frame pointer in every function; and the runtime's to neither, so
// that a step goes over a call of it.
func TestProgramFlags(t *testing.T) {
	release := ProgramFlags("cc", platform.Host(), false)
	if release[len(release)-1] != lineTables || slices.Contains(release, "-O0") {
		t.Errorf("a release build's program is compiled with %v", release)
	}

	debug := ProgramFlags("cc", platform.Host(), true)
	if !slices.Equal(debug[len(debug)-len(debugInfo):], debugInfo) {
		t.Errorf("a debug build's program is compiled with %v, want %v last", debug, debugInfo)
	}

	if slices.Contains(debug, leafFramePointer) || !slices.Contains(release, leafFramePointer) {
		t.Errorf("a debug build's program frees a leaf's frame pointer, or a release build's keeps it: %v, %v", debug, release)
	}

	for _, debugging := range []bool{false, true} {
		if rt := BuildFlags("cc", platform.Host(), debugging); slices.Contains(rt, lineTables) || slices.ContainsFunc(rt, func(f string) bool { return slices.Contains(debugInfo, f) }) {
			t.Errorf("the runtime is compiled with %v", rt)
		}
	}
}

// TestBundle holds Bundle to the platform's place for the debugging
// information: on macOS a bundle beside the binary, gathered from the
// objects it was linked from and made anew over one an earlier build
// left; elsewhere nothing, since the information is in the binary.
func TestBundle(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "prog")
	if goruntime.GOOS != platform.Darwin {
		if output, err := Bundle(out, out, platform.Host()); err != nil || output != nil {
			t.Errorf("Bundle on %s: %v, %s", goruntime.GOOS, err, output)
		}

		if _, err := os.Stat(out + bundleSuffix); !os.IsNotExist(err) {
			t.Errorf("Bundle made a bundle on %s", goruntime.GOOS)
		}

		return
	}

	if _, err := exec.LookPath(dsymutil); err != nil {
		t.Skip("no dsymutil")
	}

	src := filepath.Join(dir, "prog.c")
	if err := os.WriteFile(src, []byte("int answer(void) { return 42; }\nint main(void) { return answer() - 42; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	obj := filepath.Join(dir, "prog.o")
	for _, args := range [][]string{{"-g", "-c", "-o", obj, src}, {"-o", out, obj}} { // compiled and linked apart, as a build is: the information stays in the object
		if output, err := exec.Command(Compiler(), args...).CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, output)
		}
	}

	stale := filepath.Join(out+bundleSuffix, "stale")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}

	if output, err := Bundle(out, out, platform.Host()); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}

	if _, err := os.Stat(filepath.Join(out+bundleSuffix, "Contents", "Resources", "DWARF", "prog")); err != nil {
		t.Errorf("the bundle holds no debugging information: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("the bundle an earlier build left was not replaced")
	}
}

// TestLinkFileNames holds the files a link writes into the build's
// directory apart from the runtime's objects, which share it when the
// cache is off: a table written over trace.c's object linked the
// program without the runtime's trace.
func TestLinkFileNames(t *testing.T) {
	for name := range runtime.Files() {
		object := strings.TrimSuffix(name, filepath.Ext(name)) + ".o"
		for _, written := range []string{keptObject, firstLink, tableObject} {
			if object == written {
				t.Errorf("the link writes %s, the object of the runtime's %s", written, name)
			}
		}
	}
}
