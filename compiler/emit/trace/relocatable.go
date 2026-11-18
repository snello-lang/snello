// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package trace

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
)

// The places in a 64-bit ELF file that laying out an object reads and
// writes: in the file's header, where the section headers are, how
// large each is and how many; in a section header, its address; in a
// symbol, its section and its value; and the size of a symbol.
const (
	elfShoffAt     = 0x28
	elfShentsizeAt = 0x3a
	elfShnumAt     = 0x3c
	elfShAddrAt    = 0x10
	elfSymShndxAt  = 6
	elfSymValueAt  = 8
	elfSymBytes    = 24

	// layoutBase is the address the first section of code is laid out
	// at: far above any address of the object as it is, so that a line
	// row relocation did not move, one against a section a symbol names
	// through the extended table of an object of more than 65,280
	// sections, which Go's reader does not relocate, falls inside no
	// section, and the function shows no line rather than another's.
	layoutBase = 1 << 32
)

// layOut returns the 64-bit little-endian ELF relocatable object data
// with each section of code at an address of its own, one after
// another at its alignment from layoutBase, and each symbol of such a
// section moved with it, as a link places them. Every section of an
// object is at address zero, and its debugging information is
// relocated against the section's symbol, so in an object of a section
// per function, which LLVM's linker makes of the link-time optimizer's
// output whatever it is asked, every function's line rows and scopes
// would begin at zero, and each would be read as every other's. An
// object of another form, or of one section of code, comes back as it
// is.
func layOut(data []byte) ([]byte, error) {
	f, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	if f.Type != elf.ET_REL || f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB || len(data) < elfShnumAt+2 {
		return data, nil
	}

	bases := make([]uint64, len(f.Sections))
	code, next := 0, uint64(layoutBase)
	for i, s := range f.Sections {
		if s.Flags&elf.SHF_EXECINSTR == 0 || s.Size == 0 {
			continue
		}

		align := max(s.Addralign, 1)
		bases[i] = (next + align - 1) / align * align
		next = bases[i] + s.Size
		code++
	}

	if code < 2 {
		return data, nil
	}

	out := bytes.Clone(data)
	shoff := binary.LittleEndian.Uint64(out[elfShoffAt:])
	shentsize := uint64(binary.LittleEndian.Uint16(out[elfShentsizeAt:]))
	for i, base := range bases {
		at := shoff + uint64(i)*shentsize + elfShAddrAt
		if base == 0 { // no section of code
			continue
		}

		if at+8 > uint64(len(out)) {
			return nil, errors.New("a section header past the end of the object")
		}

		binary.LittleEndian.PutUint64(out[at:], base)
	}

	for _, s := range f.Sections {
		if s.Type != elf.SHT_SYMTAB {
			continue
		}

		if s.Offset+s.Size > uint64(len(out)) {
			return nil, errors.New("a symbol table past the end of the object")
		}

		for at := s.Offset; at+elfSymBytes <= s.Offset+s.Size; at += elfSymBytes {
			shndx := int(binary.LittleEndian.Uint16(out[at+elfSymShndxAt:]))
			if shndx < len(bases) && bases[shndx] != 0 {
				value := binary.LittleEndian.Uint64(out[at+elfSymValueAt:])
				binary.LittleEndian.PutUint64(out[at+elfSymValueAt:], value+bases[shndx])
			}
		}
	}

	return out, nil
}
