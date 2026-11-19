// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package trace

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/snello-lang/snello/compiler/platform"
)

// The two C files of the image the tests link for Windows: the
// program's own, with line tables, and another, which stands for the C
// library's code the link adds; each with a static function of one
// name, which the image then defines twice.
const (
	ownSource = `int other(int x);
int data_word = 0x5e110;
__attribute__((noinline)) static int helper(int x) { return x + 3; }
__attribute__((noinline)) int own(int x) { return helper(x) + 1; }
int start(void) { return own(other(data_word)); }
`
	otherSource = `__attribute__((noinline)) static int helper(int x) { return x * 5; }
__attribute__((noinline)) int other(int x) { return helper(x) * 2; }
`
	dataWord = 0x5e110
)

// linkImage links a Windows image for amd64 from ownSource, compiled
// with line tables, and otherSource, with no C library, its DWARF and
// its symbol table kept as a build's first link keeps them, and returns
// its path and the object of ownSource's. It skips the test without
// clang and LLVM's COFF linker.
func linkImage(t *testing.T) (image, own string) {
	t.Helper()
	for _, tool := range []string{"clang", "lld-link"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("no " + tool)
		}
	}

	dir := t.TempDir()
	target := "--target=" + platform.Target{OS: platform.Windows, Arch: platform.AMD64}.Triple()
	own, other := filepath.Join(dir, "own.obj"), filepath.Join(dir, "other.obj")
	image = filepath.Join(dir, "image.exe")
	for _, c := range []struct {
		src, obj string
		flags    []string
	}{{ownSource, own, []string{"-gdwarf", "-g1"}}, {otherSource, other, nil}} {
		src := c.obj + ".c"
		if err := os.WriteFile(src, []byte(c.src), 0o644); err != nil {
			t.Fatal(err)
		}

		args := append([]string{target, "-O1", "-c", "-o", c.obj, src}, c.flags...)
		if out, err := exec.Command("clang", args...).CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, out)
		}
	}

	link := []string{target, "-fuse-ld=lld", "-nostdlib", "-Wl,/entry:start", "-Wl,/subsystem:console", "-Wl,/debug:dwarf", "-o", image, own, other}
	if out, err := exec.Command("clang", link...).CombinedOutput(); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}

	return image, own
}

// TestImageTable reads a table from a linked image with the program's
// object: the functions the object defines have anchors, the other
// object's have none, and a file that is no image is refused.
func TestImageTable(t *testing.T) {
	image, own := linkImage(t)
	table, layout, err := ImageTable(image, own)
	if err != nil {
		t.Fatal(err)
	}

	if uint64(len(table)) <= headerBytes {
		t.Errorf("a table of %d bytes, want anchors after the header", len(table))
	}

	for _, name := range []string{"own", "start"} {
		if !slices.Contains(layout.Anchors, name) {
			t.Errorf("anchors %v, want %s, the program's", layout.Anchors, name)
		}
	}

	if slices.Contains(layout.Anchors, "other") {
		t.Errorf("anchors %v hold other, which the program's object does not define", layout.Anchors)
	}

	if _, _, err := ImageTable(own); err == nil {
		t.Error("ImageTable read an object as a linked image")
	}
}

// TestReadPE reads the image's code: its functions at their offsets,
// those the program does not define marked foreign, and its DWARF.
func TestReadPE(t *testing.T) {
	image, _ := linkImage(t)
	f, err := pe.Open(image)
	if err != nil {
		t.Fatal(err)
	}

	defer f.Close()
	obj := readPE(f, map[string]bool{"own": true, "start": true})
	if obj.dwarf == nil {
		t.Error("the image's DWARF was not read")
	}

	foreign := map[string]bool{}
	for _, s := range obj.sections {
		for _, sym := range s.symbols {
			foreign[sym.name] = sym.foreign
		}
	}

	for name, want := range map[string]bool{"own": false, "start": false, "other": true} {
		if got, ok := foreign[name]; !ok || got != want {
			t.Errorf("%s: read %v, foreign %v, want foreign %v", name, ok, got, want)
		}
	}
}

// TestPESymbols reads the image's symbols for the patch: a data word
// found where the file holds it, and a name defined twice, the static
// helper of each object, left out, as the patch cannot tell which is
// meant.
func TestPESymbols(t *testing.T) {
	image, _ := linkImage(t)
	addresses, place, err := symbols(image)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := addresses["helper"]; ok {
		t.Error("helper, defined twice, has an address")
	}

	at, ok := place("data_word")
	if !ok {
		t.Fatalf("data_word has no place in the file; symbols %v", addresses)
	}

	data, err := os.ReadFile(image)
	if err != nil {
		t.Fatal(err)
	}

	if got := binary.LittleEndian.Uint32(data[at:]); got != dataWord {
		t.Errorf("the file holds %#x at data_word's place, want %#x", got, dataWord)
	}

	if _, ok := place("missing"); ok {
		t.Error("a name the image does not define has a place")
	}
}

// checkCOFF checks the COFF object written for target: its processor,
// one data section holding the table, and the two external symbols, the
// table's at the start and the parts' after the header, the longer name
// through the string table.
func checkCOFF(t *testing.T, target platform.Target, object, table []byte) {
	t.Helper()
	f, err := pe.NewFile(bytes.NewReader(object))
	if err != nil {
		t.Fatalf("%s: the COFF object does not read: %v", target, err)
	}

	if want := map[string]uint16{platform.AMD64: coffAmd64, platform.ARM64: coffArm64}[target.Arch]; f.Machine != want {
		t.Errorf("%s: the COFF object is for %#x, want %#x", target, f.Machine, want)
	}

	if len(f.Sections) != 1 || f.Sections[0].Name != coffDataName {
		t.Fatalf("%s: COFF sections %v, want one data section", target, f.Sections)
	}

	if data, err := f.Sections[0].Data(); err != nil || !bytes.Equal(data, table) {
		t.Errorf("%s: the COFF section holds %v, %v, want the table", target, data, err)
	}

	values := map[string]uint32{}
	for _, s := range f.Symbols {
		if s.SectionNumber != 1 || s.StorageClass != coffExternal {
			t.Errorf("%s: COFF symbol %s in section %d of class %d, want an external symbol of section 1", target, s.Name, s.SectionNumber, s.StorageClass)
		}

		values[s.Name] = s.Value
	}

	if len(values) != 2 || values[tableSymbol] != 0 || values[partsSymbol] != uint32(headerBytes) {
		t.Errorf("%s: COFF symbols %v, want the table at 0 and the parts at %d", target, values, headerBytes)
	}
}
