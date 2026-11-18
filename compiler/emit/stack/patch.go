// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stack

import (
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"

	"github.com/snello-lang/snello/compiler/emit/lower"
)

const (
	// Removed is the reason of the bound of a spawn whose task's
	// function the linker left out of the binary, since no path of the
	// program reaches the spawn: a task that never runs.
	Removed = "the linker removed the spawn's task, which nothing reaches"

	// taskBase is the runtime's function a task's stack begins in: the
	// first switch to a task returns into it, and it calls the task's
	// function through a pointer.
	taskBase = "task_entry"

	// switchTask, onStack, onBoundedStack and longJump are the runtime's
	// functions that move to another stack, or back up this one: the
	// switch to another task; the call of a function on a stack of its
	// own, which a panic runs through, and on Windows its form that sets
	// the stack's bounds in the thread's information block; and the
	// runtime's own long jump of Windows, which a contained panic takes
	// to the mark of the innermost catch still running, an ancestor's
	// frame on the same stack, so no deeper than a path already counted.
	switchTask     = "sn_switch"
	onStack        = "sn_on_stack"
	onBoundedStack = "sn_on_stack_bounded"
	longJump       = "sn_longjmp_regs"

	// chanDropCaller is the runtime's function that calls the drop a
	// channel holds, through a pointer to one of the functions named
	// with lower.ChanDropPrefix; lazyMaker calls a package variable's
	// initializer, one of those named with lower.LazyInitPrefix.
	chanDropCaller = "chan_drop_value"
	lazyMaker      = "sn_lazy_make"

	// catcher is the runtime's function that runs a function under a
	// mark, through a pointer to one of those named with
	// lower.CaughtPrefix: the call of a task started for a handle, or
	// the adapter of a testing.Catch.
	catcher = "sn_catch"

	// mapPrefix starts the name of every function of the runtime's maps:
	// every call through a pointer they make, as mapHelpers' do, is of
	// a map type's key hash, key equality or element drop.
	mapPrefix = "sn_map_"

	// boundBytes is the size of an entry of the table of bounds, an
	// unsigned 32-bit integer in the processor's order, little-endian
	// on both.
	boundBytes = 4

	// machoZerofill is the type of a Mach-O section the loader fills
	// with zeros, which has no bytes in the file to write over, in the
	// low byte of the section's flags.
	machoZerofill = 0x1
	machoTypeBits = 0xff

	// LinkMapFile is the name the build gives the linker's map of a
	// Windows link, beside the binary in the build's directory, which
	// Patch reads the C runtime's functions from.
	LinkMapFile = "link.map"

	// patched ends the name of the copy of the binary written beside it,
	// which replaces it once complete and, on macOS, signed.
	patched = ".bounds"

	// codesign signs the copy on macOS, which runs no binary whose
	// signature does not match it: ad hoc, "-s -", as the linker signed
	// the binary, replacing the linker's, "-f".
	codesign      = "codesign"
	codesignForce = "-f"
	codesignSign  = "-s"
	codesignAdHoc = "-"
)

// contextTop is the bytes between the top of a task's stack and the
// stack pointer taskBase begins with, by processor: none on arm64,
// whose first switch pops the whole context the runtime lays there,
// and on x86-64 the slot of the return address the switch returned
// through.
var contextTop = map[arch]int64{arm64: 0, amd64: returnAddress}

// A table is where the program's table of bounds is in its file, and
// how many spawns the program has, one past the greatest index of the
// function of a spawn's task in its symbols: a Mach-O symbol carries no
// size, and its table may be followed by padding.
type table struct {
	offset  uint64 // of the first entry
	entries int    // that the table's bytes hold
	spawns  int
}

// Patch bounds the stack of each task of the Snello program linked at
// path and writes every bound it proves into the program's table of
// bounds, lower.StackBounds, so that the runtime gives the task a small
// stack. It returns the bound of each spawn, by its index, for a
// report. The binary's bytes are given when the caller patched them
// already, the trace's table, and read otherwise; the binary is
// written anew beside itself, signed again on macOS, and renamed over
// itself, once for both patches; on an error of the analysis it is
// left with every task unbounded, which is always safe. A program with
// no spawn is not read past its symbols. linkMap is the linker's map of
// a Windows link, which names the library each function of the binary
// came from: a function of Microsoft's C runtime, linked into the
// program, is counted at its measured stack, and with no map, "", every
// function's code is walked, as it is on Linux and macOS.
func Patch(path string, data []byte, linkMap string) ([]Bound, error) {
	given := data != nil
	bounds, data, wrote, err := bound(path, data, linkMap)
	if wrote || given {
		if werr := Replace(path, data); werr != nil {
			return bounds, werr
		}
	}

	return bounds, err
}

// bound writes the bounds into data, the binary's bytes, read from
// path when none are given, with Patch's linkMap, and reports the
// bounds, the bytes, and whether any bound was written; on an error the
// bytes are as given.
func bound(path string, data []byte, linkMap string) ([]Bound, []byte, bool, error) {
	tab, read, err := read(path, data)
	if err != nil || tab.spawns == 0 {
		return nil, data, false, err
	}

	data = read
	img, err := Load(path)
	if err != nil {
		return nil, data, false, err
	}

	if linkMap != "" {
		if err := img.readLinkMap(linkMap); err != nil {
			return nil, data, false, err
		}
	}

	bounds := make([]Bound, tab.spawns)
	for k := range bounds {
		bounds[k] = Bound{Reason: Removed}
	}

	opts := img.runtimeOptions()
	for _, name := range img.Functions() {
		if k, ok := lower.SpawnIndex(name); ok && k < len(bounds) {
			bounds[k] = img.Bound(name, taskBase, contextTop[img.arch], opts)
		}
	}

	wrote := false
	for k, b := range bounds {
		if b.Known() && b.Bytes < lower.Unbounded {
			binary.LittleEndian.PutUint32(data[tab.offset+uint64(k*boundBytes):], uint32(b.Bytes))
			wrote = true
		}
	}

	return bounds, data, wrote, nil
}

// Table returns the bound of the stack of each spawn's task of the
// program linked at path, by the spawn's index, from the program's
// table of bounds as Patch left it: lower.Unbounded for a task with no
// bound. It reads no more than the binary's symbols and the table.
func Table(path string) ([]uint32, error) {
	tab, data, err := read(path, nil)
	if err != nil {
		return nil, err
	}

	out := make([]uint32, tab.spawns)
	for k := range out {
		out[k] = binary.LittleEndian.Uint32(data[tab.offset+uint64(k*boundBytes):])
	}

	return out, nil
}

// read locates the program's table of bounds and reads the binary,
// whose bytes hold the table for each spawn the symbols name, unless
// they are given.
func read(path string, data []byte) (table, []byte, error) {
	tab, err := locate(path)
	if err != nil {
		return table{}, nil, err
	}

	if data == nil {
		data, err = os.ReadFile(path)
		if err != nil {
			return table{}, nil, err
		}
	}

	if tab.spawns > tab.entries || tab.offset+uint64(tab.spawns*boundBytes) > uint64(len(data)) {
		return table{}, nil, fmt.Errorf("%s: the table of bounds is shorter than the program's spawns", path)
	}

	return tab, data, nil
}

// mapHelpers are the functions of the runtime's maps not named with
// mapPrefix that make a call through a map type's pointers.
var mapHelpers = []string{"key_hash", "find_slot"}

// runtimeOptions are what the analysis of a Snello program cannot read
// from its code: the runtime's switches of stacks, and the targets of
// the runtime's calls through the pointers the program gives it, every
// function of the binary named for each.
func (img *Image) runtimeOptions() Options {
	named := func(prefixes ...string) []string {
		var out []string
		for _, name := range img.Functions() {
			for _, p := range prefixes {
				if strings.HasPrefix(name, p) && !strings.Contains(name, cloneMark) { // a copy is called directly, never through a pointer
					out = append(out, name)
				}
			}
		}

		return out
	}

	targets := map[string][]string{chanDropCaller: named(lower.ChanDropPrefix), lazyMaker: named(lower.LazyInitPrefix), catcher: named(lower.CaughtPrefix)}
	keys := named(lower.KeyHashPrefix, lower.KeyEqPrefix, lower.ElemDropPrefix)
	for _, name := range append(named(mapPrefix), mapHelpers...) {
		targets[img.origin(name)] = keys
	}

	return Options{Targets: targets, Switches: []string{switchTask, onStack, onBoundedStack, longJump}}
}

// locate finds the program's table of bounds in its file, and counts
// the program's spawns by the functions of their tasks in its symbols.
func locate(path string) (table, error) {
	if m, err := macho.Open(path); err == nil {
		defer m.Close()
		return machoTable(m)
	}

	if p, err := pe.Open(path); err == nil {
		defer p.Close()
		return peTable(p)
	}

	e, err := elf.Open(path)
	if err != nil {
		return table{}, fmt.Errorf("%s: neither Mach-O, ELF nor PE", path)
	}

	defer e.Close()
	return elfTable(e)
}

func elfTable(e *elf.File) (table, error) {
	syms, err := e.Symbols()
	if err != nil {
		return table{}, err
	}

	var tab *elf.Symbol
	spawns := 0
	for i, s := range syms {
		if k, ok := lower.SpawnIndex(s.Name); ok && elf.ST_TYPE(s.Info) == elf.STT_FUNC {
			spawns = max(spawns, k+1)
		}

		if s.Name == lower.StackBounds {
			tab = &syms[i]
		}
	}

	if spawns == 0 {
		return table{}, nil // the linker removes a table nothing reads
	}

	if tab == nil || int(tab.Section) >= len(e.Sections) {
		return table{}, errors.New("no table of bounds")
	}

	sec := e.Sections[tab.Section]
	if sec.Type == elf.SHT_NOBITS || tab.Value < sec.Addr {
		return table{}, errors.New("the table of bounds has no bytes in the file")
	}

	return table{offset: sec.Offset + tab.Value - sec.Addr, entries: int(tab.Size / boundBytes), spawns: spawns}, nil
}

// machoTable finds the table in a Mach-O binary, whose symbols carry no
// size: the table runs to the next symbol of its section, or its end.
func machoTable(m *macho.File) (table, error) {
	if m.Symtab == nil {
		return table{}, errors.New("no symbols")
	}

	var tab *macho.Symbol
	spawns := 0
	for i, s := range m.Symtab.Syms {
		name := strings.TrimPrefix(s.Name, symbolPrefix)
		if k, ok := lower.SpawnIndex(name); ok && s.Sect != 0 {
			spawns = max(spawns, k+1)
		}

		if name == lower.StackBounds {
			tab = &m.Symtab.Syms[i]
		}
	}

	if spawns == 0 {
		return table{}, nil // the linker removes a table nothing reads
	}

	if tab == nil || tab.Sect == 0 || int(tab.Sect) > len(m.Sections) {
		return table{}, errors.New("no table of bounds")
	}

	sec := m.Sections[tab.Sect-1]
	if sec.Flags&machoTypeBits == machoZerofill || tab.Value < sec.Addr {
		return table{}, errors.New("the table of bounds has no bytes in the file")
	}

	end := sec.Addr + sec.Size
	var after []uint64
	for _, s := range m.Symtab.Syms {
		if s.Sect == tab.Sect && s.Value > tab.Value {
			after = append(after, s.Value)
		}
	}

	if len(after) > 0 {
		sort.Slice(after, func(i, j int) bool { return after[i] < after[j] })
		end = after[0]
	}

	return table{offset: uint64(sec.Offset) + tab.Value - sec.Addr, entries: int((end - tab.Value) / boundBytes), spawns: spawns}, nil
}

// Replace writes data over the binary at path as replace does, for the
// trace's table, which the panic's trace package writes before the
// bounds: signed when it is a macOS binary, a Mach-O file, so that the
// program runs, whatever system it was built on.
func Replace(path string, data []byte) error {
	os_ := linux
	if len(data) >= 4 && binary.LittleEndian.Uint32(data) == macho.Magic64 {
		os_ = darwin
	}

	return replace(path, data, os_)
}

// replace writes data beside path, signs it on macOS, and renames it
// over path, so that a program never runs half written, nor, on macOS,
// from a file the system has seen with another signature.
func replace(path string, data []byte, os_ system) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	tmp := path + patched
	syscall.ForkLock.Lock() // no fork while the binary is open for writing: a child would hold it open until it execs, and an exec of the binary meanwhile fails, text file busy
	err = os.WriteFile(tmp, data, info.Mode().Perm())
	syscall.ForkLock.Unlock()
	if err != nil {
		return err
	}

	if os_ == darwin {
		if out, err := exec.Command(codesign, codesignForce, codesignSign, codesignAdHoc, tmp).CombinedOutput(); err != nil {
			os.Remove(tmp)
			return fmt.Errorf("%s: %v\n%s", codesign, err, out)
		}
	}

	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}

	return nil
}
