// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stack

import (
	"maps"
	"regexp"
	"strconv"
	"strings"
)

// The arm64 mnemonics and registers the analysis reads.
const (
	arm64OpCall      = "bl"   // a call
	arm64OpCallReg   = "blr"  // a call through a register; with pointer authentication, blraa and the rest
	arm64OpJumpReg   = "br"   // a jump through a register; braa and the rest
	arm64OpBreak     = "brk"  // a breakpoint, which is no jump though it starts as one
	arm64OpBranch    = "b"    // a branch
	arm64OpCondition = "b."   // the start of a conditional branch, b.eq
	arm64OpCBZ       = "cbz"  // compare with zero and branch
	arm64OpCBNZ      = "cbnz" // and its negation
	arm64OpTBZ       = "tbz"  // test a bit and branch
	arm64OpTBNZ      = "tbnz" // and its negation
	arm64OpReturn    = "ret"  // a return; with pointer authentication, retaa and the rest
	arm64OpPage      = "adrp" // a page's address into a register
	arm64OpAdd       = "add"
	arm64OpSub       = "sub"
	arm64OpMove      = "mov"
	arm64OpLoad      = "ldr"
	arm64OpStore     = "st" // the start of every store's mnemonic, which writes no register of its first operand
	arm64OpCompare   = "cmp"
	arm64OpCompareN  = "cmn"

	arm64ProbeReg  = "x15" // the register a frame's size is put in for the stack probe, in units of arm64ProbeUnit
	arm64ProbeUnit = 16
	arm64Probed    = "sp, sp, x15, lsl #4" // the frame the stack probe checked, taken from the stack pointer

	arm64SP          = "sp"  // the stack pointer
	arm64FP          = "x29" // the frame pointer
	arm64LinkReg     = 30    // x30, the link register a call writes
	arm64LastScratch = 18    // x0 to x18 are the registers a call may change
	arm64LastReg     = 30    // x30, the highest numbered general register

	arm64Operands = ", " // what comes between two operands
)

// arm64Writeless are the mnemonics whose first operand is no register
// they write: stores, comparisons, hints and barriers.
var arm64Writeless = map[string]bool{
	"str": true, "strb": true, "strh": true, "stur": true, "sturb": true, "sturh": true, "stp": true, "stnp": true, "stlr": true, "stlrb": true, "stlrh": true,
	"cmp": true, "cmn": true, "tst": true, "ccmp": true, "ccmn": true, "fcmp": true, "fccmp": true,
	"prfm": true, "nop": true, "dmb": true, "dsb": true, "isb": true, "hint": true, "yield": true,
}

// arm64PairLoads are the starts of the mnemonics that load a pair of
// registers, both of which they write.
var arm64PairLoads = []string{"ldp", "ldnp", "ldxp", "ldaxp"}

// The arm64 forms the analysis reads: a store that moves the stack
// pointer down before it writes, "[sp, #-16]!", an immediate operand
// with its optional shift, "#0x1, lsl #12", a load of a register from
// the address another holds, with no offset, "[x0]", a register, and
// a load of one with an offset, "[x8, #0x220]", and the page otool's
// note gives an adrp, "0x10000c000".
var (
	arm64PreIndex = regexp.MustCompile(`\[sp, #-(0x[0-9a-f]+|\d+)\]!`)
	arm64Imm      = regexp.MustCompile(`^#(0x[0-9a-f]+|\d+)(?:, lsl #(\d+))?$`)
	arm64LoadBare = regexp.MustCompile(`^([xw]\d+), \[([xw]\d+)\]$`)
	arm64LoadAt   = regexp.MustCompile(`^(x\d+), \[(x\d+), #(0x[0-9a-f]+|\d+)\]$`)
	arm64Reg      = regexp.MustCompile(`^[xw](\d+)$`)
	arm64Page     = regexp.MustCompile(`^(0x[0-9a-f]+)`)
)

// arm64 reads the function: its frame from every instruction that moves
// the stack pointer down, its calls and tail calls, and what it does to
// the stack pointer that is not a frame. A frame larger than a page is
// made by three instructions in a row, as Windows has a compiler make
// one: its size in sixteens put in x15, the call of the stack probe,
// which touches each page of the frame in order so that the system's
// guard page grows the stack, and the frame taken from the stack
// pointer by x15 shifted back to bytes, which no branch may reach but at
// the first, decode checks.
func (d *decoder) arm64() {
	for _, in := range d.insts { // the local branches first, which end and begin the blocks the thread-local reading needs
		if arm64Branch(in.op) {
			if t := d.img.resolveTarget(parseTarget(arm64Last(in.args), in.note)); t.known && !t.library {
				if (t.name == d.f.name && t.offset > 0) || (t.name == "" && d.within(t.addr) && t.addr != d.insts[0].addr) {
					to := t.addr
					if t.name != "" {
						to = d.insts[0].addr + t.offset
					}

					d.targets[to] = true
				}
			}
		}
	}

	d.tracking = d.arm64Tracks()
	starts, succ := d.arm64Blocks()
	held := d.arm64Held(starts, succ)
	state := map[int]uint64{}  // registers known to hold an address
	loaded := map[int]string{} // registers holding a function to call: the thread-local accessor, threadLocalKey, or an imported one
	toProbe := int64(-1)       // the size the instruction before put in x15, or -1
	probed := int64(-1)        // the size the instruction before, the stack probe's call, checked, or -1
	var probeCall uint64       // the address of that call
	block := 0
	for i, in := range d.insts {
		if block < len(starts) && starts[block] == i {
			clear(state) // a block begins: only what every path into it holds is known
			clear(loaded)
			for r, a := range held[block] {
				state[r] = a
			}

			block++
		}

		size, checked, checkedAt := toProbe, probed, probeCall
		toProbe, probed = -1, -1
		if in.op == arm64OpSub && in.args == arm64Probed && checked >= 0 {
			d.probe(checkedAt, in, checked*arm64ProbeUnit)
			continue
		}

		if in.op == arm64OpMove && arm64First(in.args) == arm64ProbeReg {
			if n, ok := arm64Immediate(strings.TrimPrefix(in.args, arm64ProbeReg+arm64Operands)); ok {
				toProbe = n
			}
		}

		d.arm64Stack(in)
		switch {
		case in.op == arm64OpCall:
			t := parseTarget(in.args, in.note)
			if stackProbes[t.name] && size >= 0 {
				probed, probeCall = size, in.addr
			}

			d.call(t)
			arm64Clobber(state, loaded)
		case strings.HasPrefix(in.op, arm64OpCallReg):
			d.f.calls = append(d.f.calls, arm64CallThrough(in, loaded))
			arm64Clobber(state, loaded)
		case arm64JumpReg(in.op): // a tail call through a pointer, which the compiler writes for a call that ends a function, since both backends build with jump tables off, so the switches are tables of comparisons, or a PE binary's thunk of an import: a call, counted over this frame, then the end
			d.f.calls = append(d.f.calls, arm64CallThrough(in, loaded))
			d.stop(in)
		case arm64Branch(in.op):
			if in.op == arm64OpBranch {
				d.stop(in)
			}

			d.branch(in, parseTarget(arm64Last(in.args), in.note))
		case strings.HasPrefix(in.op, arm64OpReturn):
			d.stop(in)
		default:
			d.arm64Track(in, state, loaded)
		}
	}
}

// arm64CallThrough is the call a call or a jump through a register
// makes: of the function the register was loaded with, the thread-local
// accessor or an imported function, or through a pointer the analysis
// does not know.
func arm64CallThrough(in inst, loaded map[int]string) call {
	r, ok := arm64RegNum(in.args)
	switch name := loaded[r]; {
	case !ok || name == "":
		return call{kind: pointer}
	case name == threadLocalKey:
		return call{kind: threadLocal}
	default:
		return call{kind: library, target: name}
	}
}

// arm64Blocks splits the function into its basic blocks: the index of
// each block's first instruction, and each block's successors, by
// their place in that list. A block ends at a branch, a return or a
// jump through a register, or before an instruction a branch goes to;
// a call returns, and ends none.
func (d *decoder) arm64Blocks() (starts []int, succ [][]int) {
	byAddr := map[uint64]int{}
	for i, in := range d.insts {
		if i == 0 || d.targets[in.addr] || arm64Ends(d.insts[i-1].op) {
			byAddr[in.addr] = len(starts)
			starts = append(starts, i)
		}
	}

	succ = make([][]int, len(starts))
	for b := range starts {
		last := len(d.insts) - 1
		if b+1 < len(starts) {
			last = starts[b+1] - 1
		}

		in := d.insts[last]
		next := b + 1
		switch {
		case strings.HasPrefix(in.op, arm64OpReturn), arm64JumpReg(in.op):
		case arm64Branch(in.op):
			if t := d.localTarget(in); t >= 0 {
				if n, ok := byAddr[uint64(t)]; ok {
					succ[b] = append(succ[b], n)
				}
			}

			if in.op != arm64OpBranch && next < len(starts) {
				succ[b] = append(succ[b], next)
			}
		default:
			if next < len(starts) {
				succ[b] = append(succ[b], next)
			}
		}
	}

	return starts, succ
}

// localTarget returns the address a branch goes to in the function, or
// -1 for one that leaves it.
func (d *decoder) localTarget(in inst) int64 {
	t := d.img.resolveTarget(parseTarget(arm64Last(in.args), in.note))
	switch {
	case !t.known || t.library:
		return -1
	case t.name == d.f.name && t.offset > 0:
		return int64(d.insts[0].addr + t.offset)
	case t.name == "" && d.within(t.addr) && t.addr != d.insts[0].addr:
		return int64(t.addr)
	}

	return -1
}

// arm64Held returns, for each block, the registers that hold the same
// known address on every path into it: a forward analysis that keeps,
// at each block's entry, what all of its predecessors leave, starting
// from nothing at the function's entry and from everything at a block
// no path has reached yet, until nothing changes.
func (d *decoder) arm64Held(starts []int, succ [][]int) []map[int]uint64 {
	n := len(starts)
	in := make([]map[int]uint64, n)
	out := make([]map[int]uint64, n) // nil: no path reached yet
	in[0] = map[int]uint64{}
	if !d.tracking {
		for b := range in {
			in[b] = map[int]uint64{}
		}

		return in
	}

	preds := make([][]int, n)
	for b, ss := range succ {
		for _, s := range ss {
			preds[s] = append(preds[s], b)
		}
	}

	for changed := true; changed; {
		changed = false
		for b := 0; b < n; b++ {
			entry := in[b]
			if b > 0 {
				entry = nil
				for _, p := range preds[b] {
					if out[p] == nil {
						continue
					}

					if entry == nil {
						entry = maps.Clone(out[p])
						continue
					}

					for r, a := range entry {
						if pa, ok := out[p][r]; !ok || pa != a {
							delete(entry, r)
						}
					}
				}

				if entry == nil {
					continue // no path has reached it yet
				}
			}

			in[b] = entry
			state := maps.Clone(entry)
			loaded := map[int]string{}
			last := len(d.insts)
			if b+1 < n {
				last = starts[b+1]
			}

			for _, x := range d.insts[starts[b]:last] {
				if x.op == arm64OpCall || strings.HasPrefix(x.op, arm64OpCallReg) {
					arm64Clobber(state, loaded)
					continue
				}

				d.arm64Track(x, state, loaded)
			}

			if out[b] == nil || !maps.Equal(out[b], state) {
				out[b] = state
				changed = true
			}
		}
	}

	for b := range in {
		if in[b] == nil {
			in[b] = map[int]uint64{} // no path reaches it: assume nothing
		}
	}

	return in
}

// arm64Ends reports whether an instruction ends a block.
func arm64Ends(op string) bool {
	return arm64Branch(op) || strings.HasPrefix(op, arm64OpReturn) || arm64JumpReg(op)
}

// arm64JumpReg reports whether op is a jump through a register, br or a
// form of it with pointer authentication.
func arm64JumpReg(op string) bool {
	return strings.HasPrefix(op, arm64OpJumpReg) && op != arm64OpBreak
}

// arm64Stack reads what the instruction does to the stack pointer.
func (d *decoder) arm64Stack(in inst) {
	if m := arm64PreIndex.FindStringSubmatch(in.args); m != nil {
		n, _ := strconv.ParseInt(m[1], 0, 64)
		d.grow(in, n)
		return
	}

	rest, ok := strings.CutPrefix(in.args, arm64SP+arm64Operands)
	if !ok || strings.HasPrefix(in.op, arm64OpStore) || in.op == arm64OpCompare || in.op == arm64OpCompareN {
		return // the stack pointer is not what the instruction writes
	}

	switch {
	case in.op == arm64OpSub && strings.HasPrefix(rest, arm64SP+arm64Operands):
		if n, ok := arm64Immediate(strings.TrimPrefix(rest, arm64SP+arm64Operands)); ok {
			d.grow(in, n)
		} else {
			d.unknown("a frame whose size depends on a value in " + d.img.plain(d.f.name))
		}
	case in.op == arm64OpAdd && strings.HasPrefix(rest, arm64SP+arm64Operands):
		// moves it up, giving back what a frame took
	case (in.op == arm64OpAdd || in.op == arm64OpSub) && strings.HasPrefix(rest, arm64FP+arm64Operands), in.op == arm64OpMove && rest == arm64FP:
		// restores it from the frame pointer, within the frame already counted
	default:
		d.f.setsSP = true
	}
}

// arm64Tracks reports whether the function's registers are to be
// followed: the binary has what arm64Track follows, macOS's thread-local
// descriptors or a PE binary's slots of imports, and the function calls
// or jumps through a register, which is what the following names.
func (d *decoder) arm64Tracks() bool {
	if d.tlv[1] == 0 && len(d.img.slots) == 0 {
		return false
	}

	for _, in := range d.insts {
		if strings.HasPrefix(in.op, arm64OpCallReg) || arm64JumpReg(in.op) {
			return true
		}
	}

	return false
}

// arm64Track follows the registers that hold an address the analysis
// knows, and those that hold a function loaded from one: the accessor
// a thread-local descriptor holds, or the function a slot of a PE
// binary's table of imports holds.
func (d *decoder) arm64Track(in inst, state map[int]uint64, loaded map[int]string) {
	if !d.tracking {
		return
	}

	dst, ok := arm64RegNum(arm64First(in.args))
	if !ok {
		return
	}

	switch {
	case in.op == arm64OpPage: // the page in otool's note, or after the register in objdump's operands
		m := arm64Page.FindStringSubmatch(in.note)
		if m == nil {
			m = arm64Page.FindStringSubmatch(arm64Second(in.args))
		}

		if m != nil {
			page, _ := strconv.ParseUint(m[1], 0, 64)
			state[dst] = page
			delete(loaded, dst)
			return
		}
	case in.op == arm64OpAdd:
		parts := strings.SplitN(in.args, arm64Operands, 3)
		if len(parts) == 3 {
			if src, ok := arm64RegNum(parts[1]); ok {
				if base, ok := state[src]; ok {
					if n, ok := arm64Immediate(parts[2]); ok {
						state[dst] = base + uint64(n)
						delete(loaded, dst)
						return
					}
				}
			}
		}
	case in.op == arm64OpMove:
		parts := strings.SplitN(in.args, arm64Operands, 2)
		if len(parts) == 2 {
			if src, ok := arm64RegNum(parts[1]); ok {
				if a, ok := state[src]; ok {
					state[dst] = a
					delete(loaded, dst)
					return
				}

				if name, ok := loaded[src]; ok {
					delete(state, dst)
					loaded[dst] = name
					return
				}
			}
		}
	case in.op == arm64OpLoad:
		var from uint64
		known := false
		if m := arm64LoadBare.FindStringSubmatch(in.args); m != nil {
			src, _ := arm64RegNum(m[2])
			from, known = state[src]
		} else if m := arm64LoadAt.FindStringSubmatch(in.args); m != nil {
			src, _ := arm64RegNum(m[2])
			off, _ := strconv.ParseUint(m[3], 0, 64)
			if base, ok := state[src]; ok {
				from, known = base+off, true
			}
		}

		if known {
			if name, ok := d.img.slots[from]; ok {
				delete(state, dst)
				loaded[dst] = name
				return
			}

			if from >= d.tlv[0] && from < d.tlv[1] {
				delete(state, dst)
				loaded[dst] = threadLocalKey
				return
			}
		}
	}

	for _, r := range arm64Written(in) {
		delete(state, r)
		delete(loaded, r)
	}
}

// arm64Written returns the registers an instruction writes: its first
// operand, but for a store, a comparison or a branch, which write none;
// both first operands of a load of a pair; and the base of an address
// written back.
func arm64Written(in inst) []int {
	var out []int
	if !arm64Writeless[in.op] {
		if r, ok := arm64RegNum(arm64First(in.args)); ok {
			out = append(out, r)
		}

		for _, p := range arm64PairLoads {
			if !strings.HasPrefix(in.op, p) {
				continue
			}

			parts := strings.SplitN(in.args, arm64Operands, 3)
			if len(parts) >= 2 {
				if r, ok := arm64RegNum(parts[1]); ok {
					out = append(out, r)
				}
			}

			break
		}
	}

	if i := strings.Index(in.args, "["); i >= 0 && (strings.Contains(in.args, "]!") || strings.Contains(in.args, "], #")) {
		base := strings.SplitN(in.args[i+1:], ",", 2)[0]
		if r, ok := arm64RegNum(strings.TrimSuffix(base, "]")); ok {
			out = append(out, r)
		}
	}

	return out
}

// arm64Clobber forgets the registers a call may change: x0 to x18 and
// the link register, x30.
func arm64Clobber(state map[int]uint64, loaded map[int]string) {
	for _, r := range arm64CallerSaved() {
		delete(state, r)
		delete(loaded, r)
	}
}

func arm64CallerSaved() []int {
	out := make([]int, 0, arm64LastScratch+2)
	for r := 0; r <= arm64LastScratch; r++ {
		out = append(out, r)
	}

	return append(out, arm64LinkReg)
}

// arm64Branch reports whether op is a branch to a target named in its
// operands: b, a conditional b.cond, or a compare and branch.
func arm64Branch(op string) bool {
	return op == arm64OpBranch || strings.HasPrefix(op, arm64OpCondition) || op == arm64OpCBZ || op == arm64OpCBNZ || op == arm64OpTBZ || op == arm64OpTBNZ
}

// arm64Immediate reads "#0x40", "#64" or "#0x1, lsl #12".
func arm64Immediate(s string) (int64, bool) {
	m := arm64Imm.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}

	n, err := strconv.ParseInt(m[1], 0, 64)
	if err != nil {
		return 0, false
	}

	if m[2] != "" {
		shift, _ := strconv.Atoi(m[2])
		n <<= shift
	}

	return n, true
}

// arm64RegNum reads x0 to x30, or w0 to w30, as the register's number.
func arm64RegNum(s string) (int, bool) {
	m := arm64Reg.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}

	n, _ := strconv.Atoi(m[1])
	return n, n <= arm64LastReg
}

func arm64First(args string) string {
	return strings.SplitN(args, ",", 2)[0]
}

// arm64Second is the second of an instruction's operands, or "".
func arm64Second(args string) string {
	parts := strings.SplitN(args, arm64Operands, 3)
	if len(parts) < 2 {
		return ""
	}

	return parts[1]
}

// arm64Last is a branch's target among its operands: what follows the
// register and the bit a compare and branch names first, and the whole
// of a plain branch's, objdump's "400600 <foo+0x8>" among them.
func arm64Last(args string) string {
	if i := strings.LastIndex(args, arm64Operands); i >= 0 && !strings.Contains(args[:i], symbolOpen) {
		return strings.TrimSpace(args[i+len(arm64Operands):])
	}

	return args
}
