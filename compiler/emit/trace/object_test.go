// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package trace

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"testing"
)

// TestObjects writes a table into both formats and reads each back
// with the standard reader: one data section holding the table's bytes
// at its alignment, and the two symbols, the table's at the start and
// the parts' after the header, with the parts' size.
func TestObjects(t *testing.T) {
	table := make([]byte, headerBytes+37) // parts of a length no alignment rounds
	for i := range table {
		table[i] = byte(i)
	}

	object, err := machoFile(table)
	if err != nil {
		t.Fatal(err)
	}

	m, err := macho.NewFile(bytes.NewReader(object))
	if err != nil {
		t.Fatalf("the Mach-O object does not read: %v", err)
	}

	if len(m.Sections) != 1 || m.Sections[0].Name != machoDataSection || m.Sections[0].Seg != machoData || m.Sections[0].Align != machoAlign {
		t.Errorf("Mach-O sections %+v, want one data section", m.Sections)
	}

	if data, err := m.Sections[0].Data(); err != nil || !bytes.Equal(data, table) {
		t.Errorf("the Mach-O section holds %v, %v, want the table", data, err)
	}

	names := map[string]uint64{}
	for _, s := range m.Symtab.Syms {
		if s.Type != machoExternal || s.Sect != 1 {
			t.Errorf("Mach-O symbol %s has type %#x in section %d, want a defined external symbol of section 1", s.Name, s.Type, s.Sect)
		}

		names[s.Name] = s.Value
	}

	if len(names) != 2 || names[machoPrefix+tableSymbol] != 0 || names[machoPrefix+partsSymbol] != headerBytes {
		t.Errorf("Mach-O symbols %v, want the table at 0 and the parts at %d", names, headerBytes)
	}

	object, err = elfFile(table)
	if err != nil {
		t.Fatal(err)
	}

	e, err := elf.NewFile(bytes.NewReader(object))
	if err != nil {
		t.Fatalf("the ELF object does not read: %v", err)
	}

	data := e.Section(elfDataName)
	if data == nil || data.Type != elf.SHT_PROGBITS || data.Flags != elf.SHF_ALLOC|elf.SHF_WRITE || data.Addralign != elfAlign {
		t.Fatalf("no ELF data section as wanted: %+v", data)
	}

	if held, err := data.Data(); err != nil || !bytes.Equal(held, table) {
		t.Errorf("the ELF section holds %v, %v, want the table", held, err)
	}

	syms, err := e.Symbols()
	if err != nil {
		t.Fatal(err)
	}

	sizes := map[string][2]uint64{}
	for _, s := range syms {
		if elf.ST_BIND(s.Info) != elf.STB_GLOBAL || elf.ST_TYPE(s.Info) != elf.STT_OBJECT || s.Section != elfDataIndex {
			t.Errorf("ELF symbol %s has info %#x in section %d, want a global object of the data section", s.Name, s.Info, s.Section)
		}

		sizes[s.Name] = [2]uint64{s.Value, s.Size}
	}

	if len(sizes) != 2 || sizes[tableSymbol] != [2]uint64{0, headerBytes} || sizes[partsSymbol] != [2]uint64{headerBytes, uint64(len(table)) - headerBytes} {
		t.Errorf("ELF symbols %v, want the table at 0 and the parts at %d with the rest", sizes, headerBytes)
	}
}
