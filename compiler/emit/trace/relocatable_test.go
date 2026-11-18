// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package trace

import (
	"bytes"
	"debug/elf"
	"os"
	"testing"

	"github.com/snello-lang/snello/compiler/platform"
)

// TestLayOut lays out an ELF object of a section per function: each
// section of code at an address of its own, past the one before it and
// at its alignment, and each function's symbol at its section's address
// plus its offset in it; an object of one section of code, and a file
// that is not a relocatable object, come back as they are.
func TestLayOut(t *testing.T) {
	cc, dir := compilerAndHeaders(t)
	var elfTarget []string
	if platform.Host().OS == platform.Darwin {
		elfTarget = []string{"--target=" + platform.Target{OS: platform.Linux, Arch: platform.Host().Arch}.Triple()}
	}

	split := compileC(t, cc, dir, "split", between, append([]string{"-O0", "-g", "-ffunction-sections"}, elfTarget...)...)
	data, err := os.ReadFile(split)
	if err != nil {
		t.Fatal(err)
	}

	laid, err := layOut(data)
	if err != nil {
		t.Fatal(err)
	}

	before, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}

	after, err := elf.NewFile(bytes.NewReader(laid))
	if err != nil {
		t.Fatalf("the laid out object does not read: %v", err)
	}

	end := uint64(layoutBase) // above every address of the object as it was
	code := 0
	for i, s := range after.Sections {
		if s.Flags&elf.SHF_EXECINSTR == 0 || s.Size == 0 {
			continue
		}

		code++
		if before.Sections[i].Addr != 0 {
			t.Errorf("section %s was at %#x in the object, want 0", s.Name, before.Sections[i].Addr)
		}

		if s.Addr < end || s.Addr%max(s.Addralign, 1) != 0 {
			t.Errorf("section %s at %#x, want at or past %#x at its alignment %d", s.Name, s.Addr, end, s.Addralign)
		}

		end = s.Addr + s.Size
	}

	if code < 3 {
		t.Fatalf("%d sections of code, want one per function", code)
	}

	was, err := before.Symbols()
	if err != nil {
		t.Fatal(err)
	}

	moved, err := after.Symbols()
	if err != nil {
		t.Fatal(err)
	}

	for i, s := range moved {
		if elf.ST_TYPE(s.Info) != elf.STT_FUNC {
			continue
		}

		if want := after.Sections[s.Section].Addr + was[i].Value; s.Value != want {
			t.Errorf("%s at %#x, want its section's address plus its offset, %#x", s.Name, s.Value, want)
		}
	}

	whole := compileC(t, cc, dir, "whole", between, append([]string{"-O0", "-g", "-fno-function-sections"}, elfTarget...)...)
	data, err = os.ReadFile(whole)
	if err != nil {
		t.Fatal(err)
	}

	if laid, err := layOut(data); err != nil || !bytes.Equal(laid, data) {
		t.Errorf("an object of one section of code was changed: %v", err)
	}

	if _, err := layOut([]byte("not an object")); err == nil {
		t.Error("a file that is no ELF object laid out")
	}
}
