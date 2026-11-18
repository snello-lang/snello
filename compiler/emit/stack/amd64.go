// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stack

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// The x86-64 mnemonics, registers and operand forms the analysis reads,
// in the AT&T syntax objdump prints.
const (
	amd64OpPush    = "push"
	amd64OpPop     = "pop"
	amd64OpEnter   = "enter" // a frame of its first operand's bytes, and the frame pointer pushed
	amd64OpCall    = "call"
	amd64OpJump    = "jmp"
	amd64OpReturn  = "ret"
	amd64OpCond    = "j"    // the start of every conditional jump, je or jne
	amd64OpLoop    = "loop" // the start of loop, loope and loopne
	amd64OpSub     = "sub"
	amd64OpAdd     = "add"
	amd64OpAnd     = "and"
	amd64OpLea     = "lea"
	amd64OpMove    = "mov"
	amd64OpCompare = "cmp"
	amd64OpTest    = "test"

	amd64SP       = "%rsp"      // the stack pointer, the last operand of an instruction that writes it
	amd64OnStack  = "(%rsp)"    // an operand in memory at the stack pointer, which the instruction writes and not the pointer
	amd64Restore  = "%rbp,%rsp" // the stack pointer set from the frame pointer, as an epilogue restores it
	amd64Indirect = "*"         // the start of the operand of a call or a jump through a register or memory
	amd64RIP      = "(%rip)"    // an address relative to the instruction, as the table of imports is reached
	amd64TLVCall  = "*(%rdi)"   // the call through the descriptor whose address lea loaded, as macOS reaches a thread-local variable
	amd64Probed   = "%rax,%rsp" // the frame a stack probe checked, its size in rax, taken from the stack pointer
	amd64PushSize = 8           // the bytes a push or the frame pointer enter saves take

	amd64RegSP = "rsp" // the stack pointer and the frame pointer as lea's base register names them
	amd64RegFP = "rbp"
	amd64Minus = "-" // the sign of an offset below its base

	amd64ImmSign  = "$"   // the start of an immediate operand
	amd64Operands = ","   // what comes between two operands
	amd64Rex      = "rex" // the start of every form GNU objdump prints a REX prefix in alone, rex64 or rex.W
)

// amd64Prefixes are the prefixes objdump prints before an operation.
var amd64Prefixes = map[string]bool{"notrack": true, "bnd": true, "rep": true, "repz": true, "repe": true, "repnz": true, "repne": true, "lock": true, "data16": true, "cs": true, "ds": true, "es": true, "fs": true, "gs": true, "ss": true}

// amd64Sized are the mnemonics the analysis reads that objdump may
// print with a size suffix, callq for call and movl for mov.
var amd64Sized = map[string]bool{"callq": true, "jmpq": true, "pushq": true, "popq": true, "subq": true, "addq": true, "andq": true, "leaq": true, "movq": true, "movl": true, "retq": true, "leaveq": true, "enterq": true}

// The x86-64 operand forms: an immediate subtracted from or masked into
// the stack pointer, "$0x28,%rsp", a mask GNU objdump writes unsigned,
// "$0xfffffffffffffff0,%rsp", and LLVM's signed, "$-0x10,%rsp", an
// address relative to a register
// loaded into it, "-0x18(%rsp),%rsp", and the symbol a call through
// the table of imported functions names in its note,
// "<malloc@GLIBC_2.2.5>", or the slot of a PE binary's table of
// imports, "<__imp_HeapAlloc>"; and the size of the frame a stack probe
// is to check, put in eax before the probe's call, "$0x4e40,%eax".
var (
	amd64ImmToSP  = regexp.MustCompile(`^\$(0x[0-9a-f]+|\d+),%rsp$`)
	amd64MaskToSP = regexp.MustCompile(`^\$(-)?(0x[0-9a-f]+|\d+),%rsp$`)
	amd64LeaTLV   = regexp.MustCompile(`^([\w.$]+)\(%rip\),%rdi$`)
	amd64LeaToSP  = regexp.MustCompile(`^(-?)(0x[0-9a-f]+|\d+)?\(%(\w+)\),%rsp$`)
	amd64Imported = regexp.MustCompile(`<([^@>+]+)@[^>]*>`)
	amd64Slot     = regexp.MustCompile(`<` + importSlotPrefix + `([^@>+]+)>`)
	amd64ToProbe  = regexp.MustCompile(`^\$(0x[0-9a-f]+|\d+),%eax$`)
)

// amd64 reads the function: its frame, its calls and tail calls, and
// what it does to the stack pointer that is not a frame.
// A frame larger than a page is made by three instructions in a row, as
// Windows has a compiler make one: its size put in eax, the call of the
// stack probe, which touches each page of the frame in order so that
// the system's guard page grows the stack, and the frame taken from the
// stack pointer by that size, which no branch may reach but at the
// first, decode checks.
func (d *decoder) amd64() {
	descriptor := false  // the instruction before loaded a thread-local descriptor's address
	toProbe := int64(-1) // the size the instruction before put in eax, or -1
	probed := int64(-1)  // the size of the frame the instruction before, the stack probe's call, checked, or -1
	var probeCall uint64 // the address of that call
	plain := make([]inst, len(d.insts))
	for i, in := range d.insts {
		plain[i] = amd64Plain(in)
	}

	held := d.amd64Imports(plain) // the registers that hold an imported function, at each call through one
	for i, in := range plain {
		loaded := descriptor
		descriptor = in.op == amd64OpLea && d.isDescriptor(in.args)
		size, checked, checkedAt := toProbe, probed, probeCall
		toProbe, probed = -1, -1
		switch {
		case in.op == amd64OpPush:
			d.grow(in, amd64PushSize)
		case in.op == amd64OpEnter:
			n, _ := strconv.ParseInt(strings.TrimPrefix(strings.SplitN(in.args, amd64Operands, 2)[0], amd64ImmSign), 0, 64)
			d.grow(in, n+amd64PushSize)
		case in.op == amd64OpCall:
			if strings.HasPrefix(in.args, amd64Indirect) {
				if loaded && in.args == amd64TLVCall {
					d.f.calls = append(d.f.calls, call{kind: threadLocal}) // the system's accessor, which the descriptor's first word holds
				} else if name, ok := amd64Import(in); ok {
					d.f.calls = append(d.f.calls, call{kind: library, target: name}) // through the table of imports, as -fno-plt and every PE binary call
				} else if name, ok := amd64HeldImport(held, i, in); ok {
					d.f.calls = append(d.f.calls, call{kind: library, target: name}) // through a register loaded from the slot
				} else {
					d.f.calls = append(d.f.calls, call{kind: pointer})
				}
			} else {
				t := parseTarget(in.args, in.note)
				if stackProbes[t.name] && size >= 0 {
					probed, probeCall = size, in.addr
				}

				d.call(t)
			}
		case in.op == amd64OpReturn:
			d.stop(in)
		case in.op == amd64OpJump:
			d.stop(in)
			if strings.HasPrefix(in.args, amd64Indirect) {
				if name, ok := amd64Import(in); ok {
					d.f.calls = append(d.f.calls, call{kind: library, target: name})
				} else if name, ok := amd64HeldImport(held, i, in); ok {
					d.f.calls = append(d.f.calls, call{kind: library, target: name})
				} else {
					d.f.calls = append(d.f.calls, call{kind: pointer}) // a tail call through a pointer, which the compiler writes for a call that ends a function, since both backends build with jump tables off, so the switches are tables of comparisons: a call, counted over this frame, then the end
				}
			} else {
				d.branch(in, parseTarget(in.args, in.note))
			}
		case strings.HasPrefix(in.op, amd64OpCond) || strings.HasPrefix(in.op, amd64OpLoop):
			d.branch(in, parseTarget(in.args, in.note))
		case in.op == amd64OpSub && in.args == amd64Probed && checked >= 0:
			d.probe(checkedAt, in, checked)
		default:
			if m := amd64ToProbe.FindStringSubmatch(in.args); m != nil && in.op == amd64OpMove {
				toProbe, _ = strconv.ParseInt(m[1], 0, 64)
			}

			d.amd64Stack(in)
		}
	}
}

// amd64Import returns the imported function a call or a jump through
// memory relative to the instruction goes to, by the slot of the table
// of imports its note names, and whether it names one.
func amd64Import(in inst) (string, bool) {
	if !strings.Contains(in.args, amd64RIP) {
		return "", false
	}

	for _, re := range []*regexp.Regexp{amd64Imported, amd64Slot} {
		if m := re.FindStringSubmatch(in.note); m != nil {
			return m[1], true
		}
	}

	return "", false
}

// isDescriptor reports whether lea's operands load the address of one
// of macOS's thread-local descriptors into the register a call through
// it takes, "_current(%rip),%rdi".
func (d *decoder) isDescriptor(args string) bool {
	m := amd64LeaTLV.FindStringSubmatch(args)
	return m != nil && d.img.tlvNames[m[1]]
}

// amd64Stack reads what an instruction that is no call nor branch does
// to the stack pointer.
func (d *decoder) amd64Stack(in inst) {
	if !strings.HasSuffix(in.args, amd64SP) || strings.HasSuffix(in.args, amd64OnStack) {
		return // the stack pointer is not what it writes
	}

	switch in.op {
	case amd64OpSub:
		m := amd64ImmToSP.FindStringSubmatch(in.args)
		if m == nil {
			d.unknown("a frame whose size depends on a value in " + d.img.plain(d.f.name))
			return
		}

		n, _ := strconv.ParseInt(m[1], 0, 64)
		d.grow(in, n)
	case amd64OpAnd: // aligns it down, by less than the alignment
		m := amd64MaskToSP.FindStringSubmatch(in.args)
		if m == nil {
			d.f.setsSP = true
			return
		}

		mask, _ := strconv.ParseUint(m[2], 0, 64)
		if m[1] == amd64Minus {
			mask = -mask
		}

		d.grow(in, int64(^mask))
	case amd64OpLea:
		m := amd64LeaToSP.FindStringSubmatch(in.args)
		switch {
		case m == nil:
			d.f.setsSP = true
		case m[3] == amd64RegSP && m[1] == amd64Minus:
			n, _ := strconv.ParseInt(m[2], 0, 64)
			d.grow(in, n)
		case m[3] == amd64RegSP || m[3] == amd64RegFP:
			// moves it up, or restores it from the frame pointer
		default:
			d.f.setsSP = true
		}
	case amd64OpAdd, amd64OpPop, amd64OpCompare, amd64OpTest:
		// moves it up, gives back what a push took, or reads it
	case amd64OpMove:
		if in.args != amd64Restore {
			d.f.setsSP = true
		}
	default:
		d.f.setsSP = true
	}
}

// amd64Prefix reports whether op is a prefix objdump prints before an
// operation, or on a line of its own: a REX prefix among them.
func amd64Prefix(op string) bool {
	return amd64Prefixes[op] || strings.HasPrefix(op, amd64Rex)
}

// amd64Plain drops the prefixes objdump prints before an operation, a
// REX prefix GNU's prints alone among them, and the size suffix of the
// operations the analysis reads, so that "notrack jmp *%rax" and
// "rex.W jmp *%rax" are jmp and "callq" is call, and writes the
// operands with nothing after the comma between two, as GNU objdump
// does and LLVM's, the one a Mac has, does not: "$0x38, %rsp" is
// "$0x38,%rsp".
func amd64Plain(in inst) inst {
	for amd64Prefix(in.op) && strings.TrimSpace(in.args) != "" { // a prefix alone, which LLVM's objdump prints on a line of its own, stays
		rest := strings.TrimSpace(in.args)
		op, args := rest, ""
		if i := strings.IndexFunc(rest, unicode.IsSpace); i >= 0 { // a tab after the prefix, as LLVM's objdump prints one, or a space
			op, args = rest[:i], rest[i+1:]
		}

		in.op, in.args = op, strings.TrimSpace(args)
	}

	if amd64Sized[in.op] {
		in.op = in.op[:len(in.op)-1]
	}

	in.args = strings.ReplaceAll(in.args, amd64Operands+" ", amd64Operands)
	return in
}
