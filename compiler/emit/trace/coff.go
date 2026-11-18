// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package trace

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"fmt"

	"github.com/snello-lang/snello/compiler/platform"
)

// COFF, Windows' object format: the processors, the sizes of the file
// header, a section header and a symbol, the data section's name and
// flags (initialized data, read and written, aligned to eight bytes),
// a symbol's class as an external one and its type as a function's, and
// the flag of a section of code.
const (
	coffAmd64         = 0x8664
	coffArm64         = 0xaa64
	coffHeaderBytes   = 20
	coffSectionBytes  = 40
	coffSymbolBytes   = 18
	coffShortName     = 8
	coffStringSizeLen = 4
	coffDataName      = ".data"
	coffDataFlags     = 0x40 | 0x40000000 | 0x80000000 | 0x00400000
	coffExternal      = 2
	coffFunctionType  = 0x20
	coffCodeFlag      = 0x20
)

// coffFile is the COFF object holding table for the processor arch: the
// file header, one data section's header and its bytes, the two
// symbols, and the string table of the name too long for a symbol's own
// eight bytes.
func coffFile(table []byte, arch string) ([]byte, error) {
	machine := uint16(coffAmd64)
	switch arch {
	case platform.AMD64:
	case platform.ARM64:
		machine = coffArm64
	default:
		return nil, fmt.Errorf("the table of a panic's trace is not written for %s", arch)
	}

	data := coffHeaderBytes + sections*coffSectionBytes
	symtab := data + len(table)
	var out bytes.Buffer
	w := func(v ...any) {
		for _, x := range v {
			binary.Write(&out, binary.LittleEndian, x)
		}
	}

	w(machine, uint16(sections), uint32(0), uint32(symtab), uint32(symbolCount), uint16(0), uint16(0))
	var name [coffShortName]byte
	copy(name[:], coffDataName)
	w(name, uint32(0), uint32(0), uint32(len(table)), uint32(data), uint32(0), uint32(0), uint16(0), uint16(0),
		uint32(coffDataFlags))
	out.Write(table)
	var strings bytes.Buffer
	symbol := func(n string, value uint32) {
		var field [coffShortName]byte
		if len(n) <= coffShortName {
			copy(field[:], n)
		} else { // four zero bytes, then the name's offset in the string table, which counts its own size
			binary.LittleEndian.PutUint32(field[4:], uint32(coffStringSizeLen+strings.Len()))
			strings.WriteString(n + nameEnd)
		}

		w(field, value, uint16(sectionIndex), uint16(0), uint8(coffExternal), uint8(0))
	}

	symbol(tableSymbol, 0)
	symbol(partsSymbol, uint32(headerBytes))
	w(uint32(coffStringSizeLen + strings.Len()))
	out.Write(strings.Bytes())
	return out.Bytes(), nil
}

// readPE reads what the table needs from a linked Windows image: its
// sections of code, the functions of each from its symbol table, at
// their offsets in the section, those own does not name marked foreign,
// and its DWARF, whose addresses are the image's, its base and the
// section's address and the offset added.
func readPE(f *pe.File, own map[string]bool) *object {
	base := imageBase(f)
	obj := &object{}
	for i, s := range f.Sections {
		if s.Characteristics&coffCodeFlag == 0 {
			continue
		}

		sec := section{name: s.Name, address: base + uint64(s.VirtualAddress), size: uint64(s.VirtualSize)}
		for _, sym := range f.Symbols {
			if int(sym.SectionNumber) == i+1 && sym.Type == coffFunctionType && sym.Name != "" {
				sec.symbols = append(sec.symbols, symbol{name: sym.Name, offset: uint64(sym.Value), foreign: !own[sym.Name]})
			}
		}

		obj.sections = append(obj.sections, sec)
	}

	if d, err := f.DWARF(); err == nil {
		obj.dwarf = d
	}

	return obj
}

// peSymbols gives add each symbol a linked Windows image defines, at
// its address: the image's base, its section's address and its offset.
func peSymbols(f *pe.File, add func(name string, value uint64, weak bool)) {
	base := imageBase(f)
	for _, sym := range f.Symbols {
		if sym.SectionNumber > 0 && int(sym.SectionNumber) <= len(f.Sections) && sym.Name != "" {
			s := f.Sections[sym.SectionNumber-1]
			add(sym.Name, base+uint64(s.VirtualAddress)+uint64(sym.Value), false)
		}
	}
}

// pePlace returns the function giving where in a linked Windows image's
// file the symbol at its address in addresses is.
func pePlace(f *pe.File, addresses map[string]uint64) func(string) (uint64, bool) {
	base := imageBase(f)
	return func(name string) (uint64, bool) {
		addr, ok := addresses[name]
		if !ok {
			return 0, false
		}

		for _, s := range f.Sections {
			start := base + uint64(s.VirtualAddress)
			if addr >= start && addr < start+uint64(s.Size) { // within the bytes the file holds of the section
				return uint64(s.Offset) + addr - start, true
			}
		}

		return 0, false
	}
}

// imageBase is the address the image is linked to load at.
func imageBase(f *pe.File) uint64 {
	switch h := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		return h.ImageBase
	case *pe.OptionalHeader32:
		return uint64(h.ImageBase)
	}

	return 0
}
