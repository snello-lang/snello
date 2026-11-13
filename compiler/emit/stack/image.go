// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stack

import (
	"bufio"
	"bytes"
	"debug/elf"
	"debug/macho"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

const (
	// otool is macOS's disassembler, and otoolText the option that has
	// it print the code with symbols for the calls' targets.
	otool     = "otool"
	otoolText = "-tV"

	// objdump is the disassembler on Linux, and objdumpText and
	// objdumpNoBytes the options that have it print the code without
	// each instruction's bytes.
	objdump        = "objdump"
	objdumpText    = "-d"
	objdumpNoBytes = "--no-show-raw-insn"

	// threadVars is the section of a Mach-O binary that holds the
	// descriptors of its thread-local variables.
	threadVars = "__thread_vars"

	// lazyPointers is the section of a Mach-O binary that holds the
	// pointers of the imports bound at their first calls, which one
	// built with chained fixups, binding every import as it starts,
	// does not have.
	lazyPointers = "__la_symbol_ptr"

	// machoTypeMask is the bits of a Mach-O symbol's type that say what
	// kind it is; an imported function's are zero, N_UNDF. The library
	// ordinal of such a symbol is the high byte of its description,
	// machoOrdinalShift bits up.
	machoTypeMask     = 0x0e
	machoOrdinalShift = 8

	// stubNote is how otool names the imported function a call goes to
	// through its stub, "symbol stub for: _malloc".
	stubNote = "symbol stub for: "

	// pltSuffix ends objdump's name of a call into a shared library,
	// "malloc@plt", through its entry in the procedure linkage table.
	pltSuffix = "@plt"

	// offsetMark comes between a function's name and the offset into
	// it in objdump's target "run+0x14".
	offsetMark = "+0x"

	// hexPrefix starts an address otool prints alone.
	hexPrefix = "0x"

	// symbolOpen and symbolClose bracket objdump's symbol after an
	// address, "400480 <malloc@plt>".
	symbolOpen  = "<"
	symbolClose = ">"
)

// cLibraries are the C library's shared objects, by the start of their
// names: macOS's libSystem and the GNU C library's parts on Linux. A
// call into any other shared library makes a task unbounded.
var cLibraries = []string{"/usr/lib/libSystem", "libc.so", "libm.so", "libpthread.so", "libdl.so", "librt.so", "ld-linux"}

// noteSeparators are what the disassemblers put between an
// instruction's operands and their note: otool's ";", objdump's "//"
// on arm64 and "#" on x86-64, after a space or a tab.
var noteSeparators = []string{" ; ", "\t; ", " // ", "\t// ", " # ", "\t# "}

// headings are the first words of the lines of a listing that are not
// instructions: otool's section line, objdump's file and section lines.
var headings = []string{"(", "file", "Disassembly"}

var (
	otoolLabel   = regexp.MustCompile(`^(_\S+):$`)
	objdumpLabel = regexp.MustCompile(`^([0-9a-f]+) <(\S+)>:$`)
	instLine     = regexp.MustCompile(`^\s*([0-9a-f]+):?\s+(\S+)\s*(.*)$`)
)

// An inst is one instruction as the disassembler printed it: its
// address, its operation and its operands, and the disassembler's note
// after them, a stub's symbol or a page's address.
type inst struct {
	addr uint64
	op   string
	args string
	note string
}

// A target is where a branch or a call goes, as the disassembler named
// it: a function of the binary by name, with the offset into it, a
// library's function through its stub, or an address alone.
type target struct {
	name    string
	offset  uint64
	library bool
	addr    uint64
	known   bool // a name or an address was read
}

// Load reads the linked binary at path: its format, its architecture
// and the libraries its imported functions come from, then every
// function's frame and calls from the system's disassembler, otool on
// macOS and objdump on Linux.
func Load(path string) (*Image, error) {
	img := &Image{functions: map[string]*function{}, cLibrary: map[string]bool{}}
	var tlv [2]uint64 // macOS's section of thread-local descriptors
	if m, err := macho.Open(path); err == nil {
		defer m.Close()
		img.os = darwin
		switch m.Cpu {
		case macho.CpuArm64:
			img.arch = arm64
		case macho.CpuAmd64:
			img.arch = amd64
		default:
			return nil, fmt.Errorf("%s: unsupported processor %v", path, m.Cpu)
		}

		if err := machoLibraries(m, img.cLibrary); err != nil {
			return nil, err
		}

		if s := m.Section(threadVars); s != nil {
			tlv = [2]uint64{s.Addr, s.Addr + s.Size}
		}

		if s := m.Section(lazyPointers); s != nil && s.Size > 0 {
			img.lazy = true
		}
	} else if e, err := elf.Open(path); err == nil {
		defer e.Close()
		img.os = linux
		switch e.Machine {
		case elf.EM_AARCH64:
			img.arch = arm64
		case elf.EM_X86_64:
			img.arch = amd64
		default:
			return nil, fmt.Errorf("%s: unsupported processor %v", path, e.Machine)
		}

		syms, err := e.ImportedSymbols()
		if err != nil {
			return nil, fmt.Errorf("%s: %v", path, err)
		}

		for _, s := range syms {
			if isCLibrary(s.Library) {
				img.cLibrary[s.Name] = true
			}
		}

		img.lazy = len(syms) > 0 && !elfBindsNow(e)
	} else {
		return nil, fmt.Errorf("%s: neither Mach-O nor ELF", path)
	}

	out, err := disassemble(img.os, path)
	if err != nil {
		return nil, err
	}

	img.read(out, tlv)
	return img, nil
}

// elfBindsNow reports whether the binary has the dynamic linker bind
// every import as it starts, by any of the three marks a linker writes
// for it.
func elfBindsNow(e *elf.File) bool {
	if v, err := e.DynValue(elf.DT_BIND_NOW); err == nil && len(v) > 0 {
		return true
	}

	if v, err := e.DynValue(elf.DT_FLAGS); err == nil && len(v) > 0 && v[0]&uint64(elf.DF_BIND_NOW) != 0 {
		return true
	}

	v, err := e.DynValue(elf.DT_FLAGS_1)
	return err == nil && len(v) > 0 && v[0]&uint64(elf.DF_1_NOW) != 0
}

// machoLibraries marks the imported functions that macOS binds from
// libSystem: an undefined symbol's library is the ordinal in its
// description, an index into the libraries the binary loads, in their
// order.
func machoLibraries(m *macho.File, into map[string]bool) error {
	libs, err := m.ImportedLibraries()
	if err != nil {
		return err
	}

	if m.Symtab == nil {
		return nil
	}

	for _, s := range m.Symtab.Syms {
		if s.Type&machoTypeMask != 0 {
			continue
		}

		n := int(s.Desc >> machoOrdinalShift)
		if n >= 1 && n <= len(libs) && isCLibrary(libs[n-1]) {
			into[s.Name] = true
		}
	}

	return nil
}

func isCLibrary(lib string) bool {
	for _, p := range cLibraries {
		if strings.HasPrefix(lib, p) {
			return true
		}
	}

	return false
}

// disassemble runs the system's disassembler over the binary's code.
func disassemble(os system, path string) ([]byte, error) {
	var cmd *exec.Cmd
	if os == darwin {
		cmd = exec.Command(otool, otoolText, path)
	} else {
		cmd = exec.Command(objdump, objdumpText, objdumpNoBytes, path)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %v: %s", cmd.Path, err, strings.TrimSpace(stderr.String()))
	}

	return out, nil
}

// read splits the disassembler's text into functions and decodes each
// one's instructions with the architecture's rules.
func (img *Image) read(text []byte, tlv [2]uint64) {
	type body struct {
		name  string
		insts []inst
	}

	var bodies []*body
	var cur *body
	sc := bufio.NewScanner(bytes.NewReader(text))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if m := otoolLabel.FindStringSubmatch(line); m != nil {
			cur = &body{name: m[1]}
			bodies = append(bodies, cur)
			continue
		}

		if m := objdumpLabel.FindStringSubmatch(line); m != nil {
			cur = &body{name: m[2]}
			bodies = append(bodies, cur)
			continue
		}

		if cur == nil {
			continue
		}

		if in, ok := parseInst(line); ok {
			cur.insts = append(cur.insts, in)
		}
	}

	starts := map[uint64]string{} // a function's first address, for a branch the disassembler printed as an address alone
	for _, b := range bodies {
		if len(b.insts) > 0 {
			starts[b.insts[0].addr] = b.name
		}
	}

	for _, b := range bodies {
		f := &function{name: b.name}
		d := decoder{img: img, f: f, insts: b.insts, starts: starts, tlv: tlv}
		d.decode()
		if old := img.functions[f.name]; old != nil {
			merge(old, f) // two local functions of one name: the worst of both
			continue
		}

		img.functions[f.name] = f
	}
}

// merge keeps in a the worst of a and b: the larger frame, both calls,
// and either's unknown.
func merge(a, b *function) {
	a.frame = max(a.frame, b.frame)
	a.calls = append(a.calls, b.calls...)
	if a.unknown == "" {
		a.unknown = b.unknown
	}

	a.setsSP = a.setsSP || b.setsSP
}

// parseInst reads one instruction line of otool's or objdump's: the
// address, with a colon after it in objdump's, the operation, the
// operands, and the note after one of noteSeparators.
func parseInst(line string) (inst, bool) {
	m := instLine.FindStringSubmatch(line)
	if m == nil {
		return inst{}, false
	}

	for _, h := range headings {
		if strings.HasPrefix(m[2], h) {
			return inst{}, false
		}
	}

	addr, err := strconv.ParseUint(m[1], 16, 64)
	if err != nil {
		return inst{}, false
	}

	in := inst{addr: addr, op: m[2], args: strings.TrimSpace(m[3])}
	for _, sep := range noteSeparators {
		if i := strings.Index(in.args, sep); i >= 0 {
			in.note = strings.TrimSpace(in.args[i+len(sep):])
			in.args = strings.TrimSpace(in.args[:i])
			break
		}
	}

	return in, true
}

// parseTarget reads the target of a branch or a call: otool's "_name",
// or an address with a stub's note, objdump's "400480 <malloc@plt>" or
// "400600 <foo+0x1c>", or an address alone.
func parseTarget(args, note string) target {
	if i := strings.Index(note, stubNote); i >= 0 {
		return target{name: strings.TrimSpace(note[i+len(stubNote):]), library: true, known: true}
	}

	if i := strings.Index(args, symbolOpen); i >= 0 && strings.HasSuffix(args, symbolClose) {
		sym := args[i+len(symbolOpen) : len(args)-len(symbolClose)]
		if name, ok := strings.CutSuffix(sym, pltSuffix); ok {
			return target{name: name, library: true, known: true}
		}

		t := target{name: sym, known: true}
		if j := strings.LastIndex(sym, offsetMark); j > 0 {
			off, err := strconv.ParseUint(sym[j+len(offsetMark):], 16, 64)
			if err == nil {
				t.name, t.offset = sym[:j], off
			}
		}

		return t
	}

	if strings.HasPrefix(args, symbolPrefix) {
		return target{name: args, known: true}
	}

	if a, err := strconv.ParseUint(strings.TrimPrefix(args, hexPrefix), 16, 64); err == nil {
		return target{addr: a, known: true}
	}

	return target{}
}
