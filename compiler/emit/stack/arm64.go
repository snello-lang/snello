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
// the page otool's note gives an adrp, "0x10000c000".
var (
	arm64PreIndex = regexp.MustCompile(`\[sp, #-(0x[0-9a-f]+|\d+)\]!`)
	arm64Imm      = regexp.MustCompile(`^#(0x[0-9a-f]+|\d+)(?:, lsl #(\d+))?$`)
	arm64LoadBare = regexp.MustCompile(`^([xw]\d+), \[([xw]\d+)\]$`)
	arm64Reg      = regexp.MustCompile(`^[xw](\d+)$`)
	arm64Page     = regexp.MustCompile(`^(0x[0-9a-f]+)`)
)

// arm64 reads the function: its frame from every instruction that moves
// the stack pointer down, its calls and tail calls, and what it does to
// the stack pointer that is not a frame.
func (d *decoder) arm64() {
	for _, in := range d.insts { // the local branches first, which end and begin the blocks the thread-local reading needs
		if arm64Branch(in.op) {
			if t := parseTarget(arm64Last(in.args), in.note); t.known && !t.library {
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

	starts, succ := d.arm64Blocks()
	held := d.arm64Held(starts, succ)
	state := map[int]uint64{} // registers known to hold an address
	thunk := map[int]bool{}   // registers loaded from a thread-local descriptor
	block := 0
	for i, in := range d.insts {
		if block < len(starts) && starts[block] == i {
			clear(state) // a block begins: only what every path into it holds is known
			clear(thunk)
			for r, a := range held[block] {
				state[r] = a
			}

			block++
		}

		d.arm64Stack(in)
		switch {
		case in.op == arm64OpCall:
			d.call(parseTarget(in.args, in.note))
			arm64Clobber(state, thunk)
		case strings.HasPrefix(in.op, arm64OpCallReg):
			if r, ok := arm64RegNum(in.args); ok && thunk[r] {
				d.f.calls = append(d.f.calls, call{kind: threadLocal})
			} else {
				d.f.calls = append(d.f.calls, call{kind: pointer})
			}

			arm64Clobber(state, thunk)
		case arm64JumpReg(in.op):
			d.stop(in)
			d.unknown("a jump through a register in " + d.img.plain(d.f.name))
		case arm64Branch(in.op):
			if in.op == arm64OpBranch {
				d.stop(in)
			}

			d.branch(in, parseTarget(arm64Last(in.args), in.note))
		case strings.HasPrefix(in.op, arm64OpReturn):
			d.stop(in)
		default:
			d.arm64Track(in, state, thunk)
		}
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
	t := parseTarget(arm64Last(in.args), in.note)
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
	if d.tlv[1] == 0 {
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
			thunk := map[int]bool{}
			last := len(d.insts)
			if b+1 < n {
				last = starts[b+1]
			}

			for _, x := range d.insts[starts[b]:last] {
				if x.op == arm64OpCall || strings.HasPrefix(x.op, arm64OpCallReg) {
					arm64Clobber(state, thunk)
					continue
				}

				d.arm64Track(x, state, thunk)
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

// arm64Track follows the registers that hold the address of a
// thread-local descriptor, or the function loaded from one.
func (d *decoder) arm64Track(in inst, state map[int]uint64, thunk map[int]bool) {
	if d.tlv[1] == 0 {
		return
	}

	dst, ok := arm64RegNum(arm64First(in.args))
	if !ok {
		return
	}

	switch {
	case in.op == arm64OpPage:
		if m := arm64Page.FindStringSubmatch(in.note); m != nil {
			page, _ := strconv.ParseUint(m[1], 0, 64)
			state[dst], thunk[dst] = page, false
			return
		}
	case in.op == arm64OpAdd:
		parts := strings.SplitN(in.args, arm64Operands, 3)
		if len(parts) == 3 {
			if src, ok := arm64RegNum(parts[1]); ok {
				if base, ok := state[src]; ok {
					if n, ok := arm64Immediate(parts[2]); ok {
						state[dst], thunk[dst] = base+uint64(n), false
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
					state[dst], thunk[dst] = a, false
					return
				}

				if thunk[src] {
					delete(state, dst)
					thunk[dst] = true
					return
				}
			}
		}
	case in.op == arm64OpLoad:
		if m := arm64LoadBare.FindStringSubmatch(in.args); m != nil {
			src, _ := arm64RegNum(m[2])
			if a, ok := state[src]; ok && a >= d.tlv[0] && a < d.tlv[1] {
				delete(state, dst)
				thunk[dst] = true
				return
			}
		}
	}

	for _, r := range arm64Written(in) {
		delete(state, r)
		delete(thunk, r)
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
func arm64Clobber(state map[int]uint64, thunk map[int]bool) {
	for _, r := range arm64CallerSaved() {
		delete(state, r)
		delete(thunk, r)
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

// arm64Last is a branch's target among its operands: what follows the
// register and the bit a compare and branch names first, and the whole
// of a plain branch's, objdump's "400600 <foo+0x8>" among them.
func arm64Last(args string) string {
	if i := strings.LastIndex(args, arm64Operands); i >= 0 && !strings.Contains(args[:i], symbolOpen) {
		return strings.TrimSpace(args[i+len(arm64Operands):])
	}

	return args
}
