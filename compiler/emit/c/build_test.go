// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"bytes"
	"debug/pe"
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

	windows := platform.Target{OS: platform.Windows, Arch: platform.AMD64}
	for _, debugging := range []bool{false, true} {
		flags := ProgramFlags("clang", windows, debugging)
		if !slices.Contains(flags, dwarfFormat) {
			t.Errorf("a Windows program is compiled with %v, want DWARF, %s", flags, dwarfFormat)
		}

		if debugging && (!slices.Contains(flags, codeView) || !slices.Equal(flags[len(flags)-len(debugInfo):], debugInfo)) {
			t.Errorf("a Windows debug build's program is compiled with %v, want %s, and %v last", flags, codeView, debugInfo)
		}

		if rt := BuildFlags("clang", windows, debugging); slices.Contains(rt, pthreadFlag) || !slices.Contains(rt, noSecureWarnings) {
			t.Errorf("the Windows runtime is compiled with %v, want no %s and %s", rt, pthreadFlag, noSecureWarnings)
		}
	}

	if got := linkLineTables(windows); got != nil {
		t.Errorf("a Windows link writes line tables, %v, which has LLVM's COFF linker write a PDB", got)
	}

	if got := linkLineTables(platform.Host()); platform.Host().OS != platform.Windows && !slices.Equal(got, []string{lineTables}) {
		t.Errorf("the host's link writes %v, want %s", got, lineTables)
	}
}

// TestBuildWindows builds a program for each Windows processor where a
// build for it can be made, with Microsoft's SDK from the sysroot its
// variable names, or on Windows: a PE image for the processor, named
// with .exe, its table of a panic's trace read from the first link.
func TestBuildWindows(t *testing.T) {
	toolchainClang(t)
	t.Setenv("CC", "clang")
	src := emitSource(t, "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hello\") }\n")
	machine := map[string]uint16{platform.AMD64: pe.IMAGE_FILE_MACHINE_AMD64, platform.ARM64: pe.IMAGE_FILE_MACHINE_ARM64}
	built := 0
	for _, arch := range []string{platform.AMD64, platform.ARM64} {
		target := platform.Target{OS: platform.Windows, Arch: arch}
		if CheckTarget(Compiler(), target) != nil {
			continue
		}

		built++
		for _, debug := range []bool{false, true} {
			bin, out, err := Build(t.TempDir(), "hello", []byte(src), target, debug)
			if err != nil {
				t.Fatalf("%s: %v\n%s", target, err, out)
			}

			if filepath.Base(bin) != "hello.exe" {
				t.Errorf("%s: the binary is %s, want hello.exe", target, bin)
			}

			f, err := pe.Open(bin)
			if err != nil {
				t.Fatalf("%s: %v", target, err)
			}

			if f.Machine != machine[arch] {
				t.Errorf("%s: an image for %#x, want %#x", target, f.Machine, machine[arch])
			}

			if debug && (f.Section(".debug_info") != nil || len(f.Symbols) == 0) { // the PDB holds what a debugger reads, the symbols what the stacks' analysis does
				t.Errorf("%s: a debug build's image keeps its DWARF, or lost its symbols (%d)", target, len(f.Symbols))
			}

			f.Close()
			pdb, err := os.ReadFile(strings.TrimSuffix(bin, filepath.Ext(bin)) + pdbSuffix)
			switch {
			case debug && (err != nil || !bytes.Contains(pdb, []byte(`<Type Name="sn_string">`))):
				t.Errorf("%s: a debug build's PDB, with the visualizers in it: %v", target, err)
			case !debug && err == nil:
				t.Errorf("%s: a release build wrote a PDB", target)
			}
		}
	}

	if built == 0 {
		t.Skip("no Windows SDK here: set SNELLO_SYSROOT_windows_amd64 to a directory xwin wrote")
	}
}

// TestBundleWindows holds Bundle to copying a Windows debug build's PDB
// from the build's directory beside the binary it was copied to, under
// the name the binary gives it, the build's binary's, and to an error
// where the build wrote none.
func TestBundleWindows(t *testing.T) {
	build, dir := t.TempDir(), t.TempDir()
	bin, out := filepath.Join(build, "prog.exe"), filepath.Join(dir, "renamed.exe")
	windowsTarget := platform.Target{OS: platform.Windows, Arch: platform.AMD64}
	if _, err := Bundle(bin, out, windowsTarget); err == nil {
		t.Error("Bundle found a PDB the build did not write")
	}

	if err := os.WriteFile(filepath.Join(build, "prog"+pdbSuffix), []byte("pdb"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Bundle(bin, out, windowsTarget); err != nil {
		t.Fatal(err)
	}

	if got, err := os.ReadFile(filepath.Join(dir, "prog"+pdbSuffix)); err != nil || string(got) != "pdb" {
		t.Errorf("the PDB beside the binary: %q, %v", got, err)
	}
}

// TestBundle holds Bundle to the platform's place for the debugging
// information: on macOS a bundle beside the binary, gathered from the
// objects it was linked from and made anew over one an earlier build
// left; for Linux nothing, since the information is in the binary.
func TestBundle(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "prog")
	linux := platform.Target{OS: platform.Linux, Arch: platform.AMD64}
	if output, err := Bundle(out, out, linux); err != nil || output != nil {
		t.Errorf("Bundle for Linux: %v, %s", err, output)
	}

	if _, err := os.Stat(out + bundleSuffix); !os.IsNotExist(err) {
		t.Error("Bundle made a bundle for Linux")
	}

	if goruntime.GOOS != platform.Darwin {
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
		for _, written := range []string{keptObject, firstLink, firstLink + ltoObject, tableObject} {
			if object == written {
				t.Errorf("the link writes %s, the object of the runtime's %s", written, name)
			}
		}
	}
}
