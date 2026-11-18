// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stack

import (
	"bufio"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/snello-lang/snello/compiler/emit/lower"
)

const (
	// importSlotPrefix starts the name of the slot of the table of
	// imports a PE binary calls an imported function through, the
	// linker's __imp_HeapAlloc for HeapAlloc.
	importSlotPrefix = "__imp_"

	// peImportDirectory is the index of the table of imports among the
	// data directories of a PE binary's optional header.
	peImportDirectory = 1

	// The import table's layout: a descriptor of 20 bytes for each
	// library, the offsets of its list of imports, OriginalFirstThunk,
	// of its name and of its slots, FirstThunk, among them, and a list
	// and slots of 8 bytes an import, both ended by a zero entry.
	peDescriptorBytes   = 20
	peLookupOffset      = 0
	peNameOffset        = 12
	peSlotsOffset       = 16
	peSlotBytes         = 8
	peLibraryNameLength = 256 // the longest name of a library read
)

// cRuntimeLibraries are the libraries of Microsoft's C runtime, as the
// linker's map names the library a symbol came from: the Universal CRT,
// the static C runtime's start and vcruntime, which every program links.
var cRuntimeLibraries = []string{"libucrt", "libcmt", "libvcruntime"}

// mapSymbol is a symbol's line of the linker's map: its section and
// offset, its name, its address, the map's flags for a function or an
// inline one, and where it came from, a library and its object,
// "libucrt:...\\free.obj", or an object of the program's own.
var mapSymbol = regexp.MustCompile(`^\s*[0-9a-fA-F]{4}:[0-9a-fA-F]{8}\s+(\S+)\s+[0-9a-fA-F]{16}\s+(?:[fi]\s+)*(.+)$`)

// windowsLibraries are the system's libraries a Windows program calls
// for what a C library does elsewhere, by the start of their names in
// lower case, the API sets' api-ms-win- among them: a call into any
// other library makes a task unbounded. Microsoft's C library itself
// is linked into the program, its functions the program's own.
var windowsLibraries = []string{"kernel32.dll", "kernelbase.dll", "ntdll.dll", "ws2_32.dll", "iphlpapi.dll", "api-ms-win-"}

// loadPE reads what Load needs of a PE binary: its processor, and the
// slots of its table of imports, by their addresses, with the functions
// of the system's libraries among them marked as the C library's.
func loadPE(path string, f *pe.File, img *Image) error {
	img.os = windows
	switch f.Machine {
	case pe.IMAGE_FILE_MACHINE_AMD64:
		img.arch = amd64
	case pe.IMAGE_FILE_MACHINE_ARM64:
		img.arch = arm64
	default:
		return fmt.Errorf("%s: unsupported processor %#x", path, f.Machine)
	}

	h, ok := f.OptionalHeader.(*pe.OptionalHeader64)
	if !ok {
		return fmt.Errorf("%s: not a 64-bit PE binary", path)
	}

	libraries, err := peSlotLibraries(f, h)
	if err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}

	img.slots = map[uint64]string{}
	for _, s := range f.Symbols {
		name, ok := strings.CutPrefix(s.Name, importSlotPrefix)
		if !ok || s.SectionNumber <= 0 || int(s.SectionNumber) > len(f.Sections) {
			continue
		}

		addr := h.ImageBase + uint64(f.Sections[s.SectionNumber-1].VirtualAddress) + uint64(s.Value)
		img.slots[addr] = name
		if isWindowsLibrary(libraries[addr]) {
			img.cLibrary[name] = true
		}
	}

	return nil
}

// readLinkMap reads the linker's map of the binary at path, which names
// the library and the object each symbol came from, and marks the
// functions that came from Microsoft's C runtime and from nothing else:
// those a call of is counted at its measured stack, and no other, so
// that a function of the program's own named as one of the runtime's,
// one the link takes over the runtime's or a static one beside it,
// which the map lists too, is walked as the program's code.
func (img *Image) readLinkMap(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	ofRuntime, ofOther := map[string]bool{}, map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	for sc.Scan() {
		m := mapSymbol.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}

		if lib, _, ok := strings.Cut(m[2], ":"); ok && slices.Contains(cRuntimeLibraries, strings.ToLower(lib)) {
			ofRuntime[m[1]] = true
		} else {
			ofOther[m[1]] = true
		}
	}

	img.cRuntime = map[string]bool{}
	for name := range ofRuntime {
		if !ofOther[name] {
			img.cRuntime[name] = true
		}
	}

	return sc.Err()
}

// peSlotLibraries reads the table of imports of f, whose optional
// header is h, and returns the library each slot's function comes from,
// in lower case, by the slot's address: the table names an import by
// its name or by its ordinal, and the slot is what a call goes through
// either way.
func peSlotLibraries(f *pe.File, h *pe.OptionalHeader64) (map[uint64]string, error) {
	out := map[uint64]string{}
	if len(h.DataDirectory) <= peImportDirectory || h.DataDirectory[peImportDirectory].Size == 0 {
		return out, nil
	}

	for at := h.DataDirectory[peImportDirectory].VirtualAddress; ; at += peDescriptorBytes {
		d, err := peRead(f, at, peDescriptorBytes)
		if err != nil {
			return nil, err
		}

		if len(d) < peDescriptorBytes {
			return nil, errors.New("the table of imports runs past its section")
		}

		lookup := binary.LittleEndian.Uint32(d[peLookupOffset:])
		name := binary.LittleEndian.Uint32(d[peNameOffset:])
		slots := binary.LittleEndian.Uint32(d[peSlotsOffset:])
		if lookup == 0 && name == 0 && slots == 0 {
			return out, nil // the zero descriptor that ends the table
		}

		library, err := peString(f, name)
		if err != nil {
			return nil, err
		}

		if lookup == 0 {
			lookup = slots // a table with no list of its own: the slots hold it before the loader binds them
		}

		for i := uint32(0); ; i++ {
			e, err := peRead(f, lookup+i*peSlotBytes, peSlotBytes)
			if err != nil {
				return nil, err
			}

			if len(e) < peSlotBytes {
				return nil, errors.New("a list of imports runs past its section")
			}

			if binary.LittleEndian.Uint64(e) == 0 {
				break
			}

			out[h.ImageBase+uint64(slots+i*peSlotBytes)] = strings.ToLower(library)
		}
	}
}

// peRead returns at most n bytes of f at the relative virtual address
// rva, as the loader lays them out: from the section that holds rva,
// to its end at most, and zeros for what lies past the bytes the file
// holds of it, which the loader fills with zeros.
func peRead(f *pe.File, rva, n uint32) ([]byte, error) {
	for _, s := range f.Sections {
		end := s.VirtualAddress + max(s.VirtualSize, s.Size)
		if rva < s.VirtualAddress || rva >= end {
			continue
		}

		b := make([]byte, min(n, end-rva))
		if at := rva - s.VirtualAddress; at < s.Size {
			if _, err := s.ReadAt(b[:min(uint32(len(b)), s.Size-at)], int64(at)); err != nil {
				return nil, err
			}
		}

		return b, nil
	}

	return nil, fmt.Errorf("the address %#x is in no section", rva)
}

// peString returns the string of f that ends in a zero byte at the
// relative virtual address rva.
func peString(f *pe.File, rva uint32) (string, error) {
	b, err := peRead(f, rva, peLibraryNameLength)
	if err != nil {
		return "", err
	}

	name, _, ok := strings.Cut(string(b), "\x00")
	if !ok {
		return "", fmt.Errorf("the name at %#x has no end", rva)
	}

	return name, nil
}

// peTable finds the table of bounds in a PE binary, whose symbols, like
// a Mach-O binary's, carry no size: the table runs to the next
// symbol of its section, or to the end of the section's bytes in the
// file, past which the loader fills it with zeros.
func peTable(f *pe.File) (table, error) {
	var tab *pe.Symbol
	spawns := 0
	for i, s := range f.Symbols {
		if k, ok := lower.SpawnIndex(s.Name); ok && s.SectionNumber > 0 {
			spawns = max(spawns, k+1)
		}

		if s.Name == lower.StackBounds {
			tab = f.Symbols[i]
		}
	}

	if spawns == 0 {
		return table{}, nil // the linker removes a table nothing reads
	}

	if tab == nil || tab.SectionNumber <= 0 || int(tab.SectionNumber) > len(f.Sections) {
		return table{}, errors.New("no table of bounds")
	}

	sec := f.Sections[tab.SectionNumber-1]
	end := sec.Size // the bytes the file holds
	for _, s := range f.Symbols {
		if s.SectionNumber == tab.SectionNumber && s.Value > tab.Value {
			end = min(end, s.Value)
		}
	}

	if tab.Value >= end {
		return table{}, errors.New("the table of bounds has no bytes in the file")
	}

	return table{offset: uint64(sec.Offset) + uint64(tab.Value), entries: int((end - tab.Value) / boundBytes), spawns: spawns}, nil
}

// isWindowsLibrary reports whether a library, its name in lower case,
// is one of windowsLibraries.
func isWindowsLibrary(library string) bool {
	for _, p := range windowsLibraries {
		if strings.HasPrefix(library, p) {
			return true
		}
	}

	return false
}
