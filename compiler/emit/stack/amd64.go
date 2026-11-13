// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stack

import (
	"regexp"
	"strconv"
	"strings"
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
	amd64PushSize = 8           // the bytes a push or the frame pointer enter saves take

	amd64RegSP = "rsp" // the stack pointer and the frame pointer as lea's base register names them
	amd64RegFP = "rbp"
	amd64Minus = "-" // the sign of an offset below its base

	amd64ImmSign  = "$" // the start of an immediate operand
	amd64Operands = "," // what comes between two operands
	amd64Prefixed = " " // what comes between a prefix and the operation it prefixes
)

// amd64Prefixes are the prefixes objdump prints before an operation.
var amd64Prefixes = map[string]bool{"notrack": true, "bnd": true, "rep": true, "repz": true, "repe": true, "repnz": true, "repne": true, "lock": true, "data16": true, "cs": true, "ds": true, "es": true, "fs": true, "gs": true, "ss": true}

// amd64Sized are the mnemonics the analysis reads that objdump may
// print with the size suffix q, callq for call.
var amd64Sized = map[string]bool{"callq": true, "jmpq": true, "pushq": true, "popq": true, "subq": true, "addq": true, "andq": true, "leaq": true, "movq": true, "retq": true, "leaveq": true, "enterq": true}

// The x86-64 operand forms: an immediate subtracted from or masked into
// the stack pointer, "$0x28,%rsp", an address relative to a register
// loaded into it, "-0x18(%rsp),%rsp", and the symbol a call through
// the table of imported functions names in its note,
// "<malloc@GLIBC_2.2.5>".
var (
	amd64ImmToSP  = regexp.MustCompile(`^\$(0x[0-9a-f]+|\d+),%rsp$`)
	amd64LeaToSP  = regexp.MustCompile(`^(-?)(0x[0-9a-f]+|\d+)?\(%(\w+)\),%rsp$`)
	amd64Imported = regexp.MustCompile(`<([^@>+]+)@[^>]*>`)
)

// amd64 reads the function: its frame, its calls and tail calls, and
// what it does to the stack pointer that is not a frame.
func (d *decoder) amd64() {
	for _, in := range d.insts {
		in = amd64Plain(in)
		switch {
		case in.op == amd64OpPush:
			d.grow(in, amd64PushSize)
		case in.op == amd64OpEnter:
			n, _ := strconv.ParseInt(strings.TrimPrefix(strings.SplitN(in.args, amd64Operands, 2)[0], amd64ImmSign), 0, 64)
			d.grow(in, n+amd64PushSize)
		case in.op == amd64OpCall:
			if strings.HasPrefix(in.args, amd64Indirect) {
				if m := amd64Imported.FindStringSubmatch(in.note); m != nil && strings.Contains(in.args, amd64RIP) {
					d.f.calls = append(d.f.calls, call{kind: library, target: m[1]}) // through the table of imports, as -fno-plt calls
				} else {
					d.f.calls = append(d.f.calls, call{kind: pointer})
				}
			} else {
				d.call(parseTarget(in.args, in.note))
			}
		case in.op == amd64OpReturn:
			d.stop(in)
		case in.op == amd64OpJump:
			d.stop(in)
			if strings.HasPrefix(in.args, amd64Indirect) {
				if m := amd64Imported.FindStringSubmatch(in.note); m != nil && strings.Contains(in.args, amd64RIP) {
					d.f.calls = append(d.f.calls, call{kind: library, target: m[1]})
				} else {
					d.unknown("a jump through a register in " + d.img.plain(d.f.name))
				}
			} else {
				d.branch(in, parseTarget(in.args, in.note))
			}
		case strings.HasPrefix(in.op, amd64OpCond) || strings.HasPrefix(in.op, amd64OpLoop):
			d.branch(in, parseTarget(in.args, in.note))
		default:
			d.amd64Stack(in)
		}
	}
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
		m := amd64ImmToSP.FindStringSubmatch(in.args)
		if m == nil {
			d.f.setsSP = true
			return
		}

		mask, _ := strconv.ParseUint(m[1], 0, 64)
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

// amd64Plain drops the prefixes objdump prints before an operation and
// the size suffix of the operations the analysis reads, so that
// "notrack jmp *%rax" is jmp and "callq" is call.
func amd64Plain(in inst) inst {
	for amd64Prefixes[in.op] {
		parts := strings.SplitN(in.args, amd64Prefixed, 2)
		in.op = parts[0]
		in.args = ""
		if len(parts) == 2 {
			in.args = strings.TrimSpace(parts[1])
		}
	}

	if amd64Sized[in.op] {
		in.op = in.op[:len(in.op)-1]
	}

	return in
}
