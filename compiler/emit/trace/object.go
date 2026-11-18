// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package trace

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/snello-lang/snello/compiler/platform"
)

// The object the table is linked from holds one section of data, the
// table and its parts, and two symbols, the table's and the parts',
// and no relocation, since the parts hold offsets and no pointer: the
// compiler writes it itself, in the native format of the target's
// system, since a C compiler's run costs a build a fair part of its
// time for a file of data alone. The constants are the formats' own.
const (
	// A name in a string table ends in a zero byte, and the table begins
	// with one, the empty name.
	nameEnd = "\x00"

	// The object's sections and symbols: one section, the first, and two
	// symbols.
	sections     = 1
	sectionIndex = 1
	symbolCount  = 2

	// Mach-O: the 64-bit magic, the processors, the object file type,
	// the load commands and their sizes, the section's names, the
	// alignment as a power of two, the symbol type of a defined external
	// symbol in a section, and the platform of macOS with the earliest
	// version the runtime runs on.
	machoMagic        = 0xfeedfacf
	machoArm64        = 0x0100000c
	machoX86_64       = 0x01000007
	machoX86_64All    = 0x3
	machoObject       = 1
	machoHeaderBytes  = 32
	machoSegment64    = 0x19
	machoSegmentBytes = 72
	machoSectionBytes = 80
	machoSymtab       = 0x2
	machoSymtabBytes  = 24
	machoBuildVersion = 0x32
	machoBuildBytes   = 24
	machoPlatformMac  = 1
	machoMinOS        = platform.MacOSMajor<<16 | platform.MacOSMinor<<8 // the oldest macOS a program runs on, as the build names it
	machoProtAll      = 7
	machoAlign        = 3 // as a power of two
	machoAlignBytes   = 8
	machoNlistBytes   = 16
	machoExternal     = 0x0f // N_EXT | N_SECT
	machoCommands     = 3
	machoNoSubtype    = 0
	machoData         = "__DATA"
	machoNoSegment    = "" // an object's one segment has no name
	machoDataSection  = "__data"
	machoPrefix       = "_"

	// ELF: the class, the byte order, the relocatable type, the
	// processors, the section types and flags, the symbol binding and
	// type of a global object, and the sizes of the headers.
	elfClass64       = 2
	elfLittle        = 1
	elfVersion       = 1
	elfRelocatable   = 1
	elfX86_64        = 62
	elfAarch64       = 183
	elfHeaderBytes   = 64
	elfSectionBytes  = 64
	elfSymbolBytes   = 24
	elfProgbits      = 1
	elfSymtabType    = 2
	elfStrtabType    = 3
	elfWriteAlloc    = 3 // SHF_WRITE | SHF_ALLOC
	elfGlobalObject  = 0x11
	elfDataIndex     = sectionIndex
	elfStrtabIndex   = 3
	elfShstrtabIndex = 4
	elfSections      = 5
	elfFirstGlobal   = 1
	elfSymbols       = symbolCount + 1 // the null symbol first
	elfAlign         = 8
	elfByteAlign     = 1
	elfDataName      = ".data"
	elfSymtabName    = ".symtab"
	elfStrtabName    = ".strtab"
	elfShstrtabName  = ".shstrtab"
)

// elfIdent begins an ELF file: the magic, the class, the byte order,
// the version, the system's interface and the padding.
var elfIdent = []byte{0x7f, 'E', 'L', 'F', elfClass64, elfLittle, elfVersion, 0, 0, 0, 0, 0, 0, 0, 0, 0}

// Object writes the native object holding table for the target t, the
// program's: the Mach-O of macOS, the ELF of Linux or the COFF of
// Windows, for its processor.
func Object(table []byte, t platform.Target) ([]byte, error) {
	switch t.OS {
	case platform.Darwin:
		return machoFile(table, t.Arch)
	case platform.Linux:
		return elfFile(table, t.Arch)
	case platform.Windows:
		return coffFile(table, t.Arch)
	}

	return nil, fmt.Errorf("the table of a panic's trace is not written for %s", t.OS)
}

// machoFile is the Mach-O object: the header, a segment of one data
// section, the build version, the symbol table, then the section's
// bytes, the two symbols and their names, for the processor arch.
func machoFile(table []byte, arch string) ([]byte, error) {
	cpu, sub := uint32(machoArm64), uint32(machoNoSubtype)
	switch arch {
	case platform.ARM64:
	case platform.AMD64:
		cpu, sub = machoX86_64, machoX86_64All
	default:
		return nil, fmt.Errorf("the table of a panic's trace is not written for %s", arch)
	}

	commands := machoSegmentBytes + machoSectionBytes + machoBuildBytes + machoSymtabBytes
	data := machoHeaderBytes + commands
	symtab := align(data+len(table), machoAlignBytes)
	strings := symtab + symbolCount*machoNlistBytes
	names, tableName, partsName := names(machoPrefix+tableSymbol, machoPrefix+partsSymbol)
	var out bytes.Buffer
	w := func(v ...any) {
		for _, x := range v {
			binary.Write(&out, binary.LittleEndian, x)
		}
	}

	w(uint32(machoMagic), cpu, sub, uint32(machoObject), uint32(machoCommands), uint32(commands), uint32(0), uint32(0))
	w(uint32(machoSegment64), uint32(machoSegmentBytes+machoSectionBytes), name16(machoNoSegment), uint64(0), uint64(len(table)), uint64(data), uint64(len(table)), uint32(machoProtAll), uint32(machoProtAll), uint32(sections), uint32(0))
	w(name16(machoDataSection), name16(machoData), uint64(0), uint64(len(table)), uint32(data), uint32(machoAlign), uint32(0), uint32(0), uint32(0), uint32(0), uint32(0), uint32(0))
	w(uint32(machoBuildVersion), uint32(machoBuildBytes), uint32(machoPlatformMac), uint32(machoMinOS), uint32(0), uint32(0))
	w(uint32(machoSymtab), uint32(machoSymtabBytes), uint32(symtab), uint32(symbolCount), uint32(strings), uint32(len(names)))
	out.Write(table)
	out.Write(make([]byte, symtab-data-len(table)))
	w(uint32(tableName), uint8(machoExternal), uint8(sectionIndex), uint16(0), uint64(0))
	w(uint32(partsName), uint8(machoExternal), uint8(sectionIndex), uint16(0), headerBytes)
	out.WriteString(names)
	return out.Bytes(), nil
}

// elfFile is the ELF object: the header, the data, the symbol table,
// the two string tables, then the section headers, for the processor
// arch.
func elfFile(table []byte, arch string) ([]byte, error) {
	machine := uint16(elfX86_64)
	switch arch {
	case platform.AMD64:
	case platform.ARM64:
		machine = elfAarch64
	default:
		return nil, fmt.Errorf("the table of a panic's trace is not written for %s", arch)
	}

	names, tableName, partsName := names(tableSymbol, partsSymbol)
	sectionNames, dataName, symtabName, strtabName, shstrtabName := sectionNames(elfDataName, elfSymtabName, elfStrtabName, elfShstrtabName)
	data := elfHeaderBytes
	symtab := align(data+len(table), elfAlign)
	strings := symtab + elfSymbols*elfSymbolBytes
	shstrings := strings + len(names)
	headers := align(shstrings+len(sectionNames), elfAlign)
	var out bytes.Buffer
	w := func(v ...any) {
		for _, x := range v {
			binary.Write(&out, binary.LittleEndian, x)
		}
	}

	out.Write(elfIdent)
	w(uint16(elfRelocatable), machine, uint32(elfVersion), uint64(0), uint64(0), uint64(headers), uint32(0), uint16(elfHeaderBytes), uint16(0), uint16(0), uint16(elfSectionBytes), uint16(elfSections), uint16(elfShstrtabIndex))
	out.Write(table)
	out.Write(make([]byte, symtab-data-len(table)))
	w(uint32(0), uint8(0), uint8(0), uint16(0), uint64(0), uint64(0)) // the null symbol
	w(uint32(tableName), uint8(elfGlobalObject), uint8(0), uint16(elfDataIndex), uint64(0), headerBytes)
	w(uint32(partsName), uint8(elfGlobalObject), uint8(0), uint16(elfDataIndex), headerBytes, uint64(len(table))-headerBytes)
	out.WriteString(names)
	out.WriteString(sectionNames)
	out.Write(make([]byte, headers-shstrings-len(sectionNames)))
	section := func(name, kind, flags uint32, offset, size int, link, info uint32, alignment, entry uint64) {
		w(name, kind, uint64(flags), uint64(0), uint64(offset), uint64(size), link, info, alignment, entry)
	}

	section(0, 0, 0, 0, 0, 0, 0, 0, 0) // the null section
	section(uint32(dataName), elfProgbits, elfWriteAlloc, data, len(table), 0, 0, elfAlign, 0)
	section(uint32(symtabName), elfSymtabType, 0, symtab, elfSymbols*elfSymbolBytes, elfStrtabIndex, elfFirstGlobal, elfAlign, elfSymbolBytes)
	section(uint32(strtabName), elfStrtabType, 0, strings, len(names), 0, 0, elfByteAlign, 0)
	section(uint32(shstrtabName), elfStrtabType, 0, shstrings, len(sectionNames), 0, 0, elfByteAlign, 0)
	return out.Bytes(), nil
}

// names is a string table of the two symbols' names, with the offset
// of each in it.
func names(table, parts string) (string, int, int) {
	s := nameEnd + table + nameEnd
	return s + parts + nameEnd, len(nameEnd), len(s)
}

// sectionNames is a string table of the four sections' names, with the
// offset of each in it.
func sectionNames(data, symtab, strtab, shstrtab string) (string, int, int, int, int) {
	s := nameEnd
	at := func(name string) int {
		offset := len(s)
		s += name + nameEnd
		return offset
	}

	d, sy, st, sh := at(data), at(symtab), at(strtab), at(shstrtab)
	return s, d, sy, st, sh
}

// name16 is a Mach-O name: the bytes in a field of sixteen.
func name16(s string) [16]byte {
	var out [16]byte
	copy(out[:], s)
	return out
}

// align rounds n up to a multiple of to.
func align(n, to int) int { return (n + to - 1) / to * to }
