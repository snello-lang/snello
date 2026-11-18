// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package trace

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"testing"

	"github.com/snello-lang/snello/compiler/platform"
)

// TestObjects writes a table into the format of every target, Mach-O
// or ELF for its processor, and reads each back with the standard
// reader: one data section holding the table's bytes
// at its alignment, and the two symbols, the table's at the start and
// the parts' after the header, with the parts' size.
func TestObjects(t *testing.T) {
	table := make([]byte, headerBytes+37) // parts of a length no alignment rounds
	for i := range table {
		table[i] = byte(i)
	}

	for _, target := range platform.Targets() {
		object, err := Object(table, target)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}

		if target.OS == platform.Darwin {
			checkMacho(t, target, object, table)
		} else {
			checkELF(t, target, object, table)
		}
	}

	if _, err := Object(table, platform.Target{OS: "windows", Arch: platform.AMD64}); err == nil {
		t.Error("an object written for windows")
	}
}

// checkMacho checks the Mach-O object written for target: its
// processor, one data section holding the table, and the two symbols.
func checkMacho(t *testing.T, target platform.Target, object, table []byte) {
	t.Helper()
	m, err := macho.NewFile(bytes.NewReader(object))
	if err != nil {
		t.Fatalf("%s: the Mach-O object does not read: %v", target, err)
	}

	if want := map[string]macho.Cpu{platform.AMD64: macho.CpuAmd64, platform.ARM64: macho.CpuArm64}[target.Arch]; m.Cpu != want {
		t.Errorf("%s: the Mach-O object is for %v, want %v", target, m.Cpu, want)
	}

	if len(m.Sections) != 1 || m.Sections[0].Name != machoDataSection || m.Sections[0].Seg != machoData || m.Sections[0].Align != machoAlign {
		t.Errorf("%s: Mach-O sections %+v, want one data section", target, m.Sections)
	}

	if data, err := m.Sections[0].Data(); err != nil || !bytes.Equal(data, table) {
		t.Errorf("%s: the Mach-O section holds %v, %v, want the table", target, data, err)
	}

	names := map[string]uint64{}
	for _, s := range m.Symtab.Syms {
		if s.Type != machoExternal || s.Sect != 1 {
			t.Errorf("%s: Mach-O symbol %s has type %#x in section %d, want a defined external symbol of section 1", target, s.Name, s.Type, s.Sect)
		}

		names[s.Name] = s.Value
	}

	if len(names) != 2 || names[machoPrefix+tableSymbol] != 0 || names[machoPrefix+partsSymbol] != headerBytes {
		t.Errorf("%s: Mach-O symbols %v, want the table at 0 and the parts at %d", target, names, headerBytes)
	}
}

// checkELF checks the ELF object written for target: its processor,
// the data section holding the table, and the two symbols with their
// sizes.
func checkELF(t *testing.T, target platform.Target, object, table []byte) {
	t.Helper()
	e, err := elf.NewFile(bytes.NewReader(object))
	if err != nil {
		t.Fatalf("%s: the ELF object does not read: %v", target, err)
	}

	if want := map[string]elf.Machine{platform.AMD64: elf.EM_X86_64, platform.ARM64: elf.EM_AARCH64}[target.Arch]; e.Machine != want {
		t.Errorf("%s: the ELF object is for %v, want %v", target, e.Machine, want)
	}

	data := e.Section(elfDataName)
	if data == nil || data.Type != elf.SHT_PROGBITS || data.Flags != elf.SHF_ALLOC|elf.SHF_WRITE || data.Addralign != elfAlign {
		t.Fatalf("%s: no ELF data section as wanted: %+v", target, data)
	}

	if held, err := data.Data(); err != nil || !bytes.Equal(held, table) {
		t.Errorf("%s: the ELF section holds %v, %v, want the table", target, held, err)
	}

	syms, err := e.Symbols()
	if err != nil {
		t.Fatal(err)
	}

	sizes := map[string][2]uint64{}
	for _, s := range syms {
		if elf.ST_BIND(s.Info) != elf.STB_GLOBAL || elf.ST_TYPE(s.Info) != elf.STT_OBJECT || s.Section != elfDataIndex {
			t.Errorf("%s: ELF symbol %s has info %#x in section %d, want a global object of the data section", target, s.Name, s.Info, s.Section)
		}

		sizes[s.Name] = [2]uint64{s.Value, s.Size}
	}

	if len(sizes) != 2 || sizes[tableSymbol] != [2]uint64{0, headerBytes} || sizes[partsSymbol] != [2]uint64{headerBytes, uint64(len(table)) - headerBytes} {
		t.Errorf("%s: ELF symbols %v, want the table at 0 and the parts at %d with the rest", target, sizes, headerBytes)
	}
}
