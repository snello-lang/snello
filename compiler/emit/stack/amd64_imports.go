// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stack

import (
	"maps"
	"regexp"
	"strings"
)

// amd64SizeSuffixes are the letters objdump ends a mnemonic with for
// its operands' size.
const amd64SizeSuffixes = "bwlq"

// What an instruction does to the registers, by its mnemonic: writes
// its last operand alone, writes none of the registers it names, writes
// both its operands, or writes rdx and rax and its last operand, as a
// division, a one-operand multiplication and a compare-and-exchange,
// which writes rax alone, do.
const (
	amd64WritesLast amd64Effect = iota + 1
	amd64WritesNone
	amd64WritesBoth
	amd64WritesRdxRax
)

// An amd64Effect is what an instruction does to the registers.
type amd64Effect int

// amd64Parts are the registers of x86-64 by the 64-bit name the
// analysis keeps them under, and the names of their narrower parts, as
// objdump prints them.
var amd64Parts = map[string][]string{
	"rax": {"%rax", "%eax", "%ax", "%al", "%ah"},
	"rbx": {"%rbx", "%ebx", "%bx", "%bl", "%bh"},
	"rcx": {"%rcx", "%ecx", "%cx", "%cl", "%ch"},
	"rdx": {"%rdx", "%edx", "%dx", "%dl", "%dh"},
	"rsi": {"%rsi", "%esi", "%si", "%sil"},
	"rdi": {"%rdi", "%edi", "%di", "%dil"},
	"rbp": {"%rbp", "%ebp", "%bp", "%bpl"},
	"r8":  {"%r8", "%r8d", "%r8w", "%r8b"},
	"r9":  {"%r9", "%r9d", "%r9w", "%r9b"},
	"r10": {"%r10", "%r10d", "%r10w", "%r10b"},
	"r11": {"%r11", "%r11d", "%r11w", "%r11b"},
	"r12": {"%r12", "%r12d", "%r12w", "%r12b"},
	"r13": {"%r13", "%r13d", "%r13w", "%r13b"},
	"r14": {"%r14", "%r14d", "%r14w", "%r14b"},
	"r15": {"%r15", "%r15d", "%r15w", "%r15b"},
}

// amd64PartOf is amd64Parts turned about: the register each name is
// of.
var amd64PartOf = func() map[string]string {
	out := map[string]string{}
	for r, parts := range amd64Parts {
		for _, p := range parts {
			out[p] = r
		}
	}

	return out
}()

// amd64KeptByCalls are the registers a call leaves as they were, as
// Windows' convention keeps them: every other one a call may change.
var amd64KeptByCalls = map[string]bool{"rbx": true, "rbp": true, "rdi": true, "rsi": true, "r12": true, "r13": true, "r14": true, "r15": true}

// amd64Effects are the mnemonics whose effect on the registers the
// analysis knows, without their size suffix, and amd64EffectPrefixes
// the families it knows by their start: a conditional move or set
// writes its last operand, a jump writes none. Any other instruction,
// one that writes a register it does not name, as a string instruction,
// a loop, cpuid or a system call does, or one the analysis never met,
// makes it forget every register, so that no write it does not know of
// leaves a register taken for an import's.
var (
	amd64Effects = map[string]amd64Effect{
		"mov": amd64WritesLast, "movabs": amd64WritesLast, "lea": amd64WritesLast, "pop": amd64WritesLast,
		"add": amd64WritesLast, "sub": amd64WritesLast, "and": amd64WritesLast, "or": amd64WritesLast, "xor": amd64WritesLast,
		"adc": amd64WritesLast, "sbb": amd64WritesLast, "neg": amd64WritesLast, "not": amd64WritesLast,
		"inc": amd64WritesLast, "dec": amd64WritesLast, "shl": amd64WritesLast, "shr": amd64WritesLast,
		"sar": amd64WritesLast, "sal": amd64WritesLast, "rol": amd64WritesLast, "ror": amd64WritesLast,
		"shld": amd64WritesLast, "shrd": amd64WritesLast, "bsf": amd64WritesLast, "bsr": amd64WritesLast,
		"tzcnt": amd64WritesLast, "lzcnt": amd64WritesLast, "popcnt": amd64WritesLast, "bswap": amd64WritesLast,
		"movzbw": amd64WritesLast, "movzbl": amd64WritesLast, "movzbq": amd64WritesLast, "movzwl": amd64WritesLast, "movzwq": amd64WritesLast,
		"movsbw": amd64WritesLast, "movsbl": amd64WritesLast, "movsbq": amd64WritesLast, "movswl": amd64WritesLast, "movswq": amd64WritesLast, "movslq": amd64WritesLast,
		"movd": amd64WritesLast, "movss": amd64WritesLast, "movsd": amd64WritesLast, "movaps": amd64WritesLast, "movups": amd64WritesLast,
		"movapd": amd64WritesLast, "movupd": amd64WritesLast, "movdqa": amd64WritesLast, "movdqu": amd64WritesLast,
		"pxor": amd64WritesLast, "xorps": amd64WritesLast, "xorpd": amd64WritesLast,
		"addsd": amd64WritesLast, "subsd": amd64WritesLast, "mulsd": amd64WritesLast, "divsd": amd64WritesLast,
		"cvtsi2sd": amd64WritesLast, "cvtsi2ss": amd64WritesLast, "cvttsd2si": amd64WritesLast, "cvttss2si": amd64WritesLast,
		// The SSE and SSE2 instructions a C compiler writes a copy, a
		// clear or a sum of vectors with, each of which writes its last
		// operand alone: a shuffle, an unpack, a logic, an integer
		// arithmetic, a compare of lanes, an insert, an extract, a
		// scalar arithmetic or a conversion.
		"pshufd": amd64WritesLast, "pshuflw": amd64WritesLast, "pshufhw": amd64WritesLast, "shufps": amd64WritesLast, "shufpd": amd64WritesLast,
		"punpcklqdq": amd64WritesLast, "punpckhqdq": amd64WritesLast, "punpckldq": amd64WritesLast, "punpckhdq": amd64WritesLast,
		"punpcklbw": amd64WritesLast, "punpcklwd": amd64WritesLast, "unpcklps": amd64WritesLast, "unpcklpd": amd64WritesLast, "unpckhpd": amd64WritesLast,
		"movlhps": amd64WritesLast, "movhlps": amd64WritesLast, "movlps": amd64WritesLast, "movhps": amd64WritesLast, "movlpd": amd64WritesLast, "movhpd": amd64WritesLast,
		"pand": amd64WritesLast, "pandn": amd64WritesLast, "por": amd64WritesLast, "andps": amd64WritesLast, "andnps": amd64WritesLast, "orps": amd64WritesLast,
		"andpd": amd64WritesLast, "andnpd": amd64WritesLast, "orpd": amd64WritesLast,
		"paddb": amd64WritesLast, "paddw": amd64WritesLast, "paddd": amd64WritesLast, "paddq": amd64WritesLast,
		"psubb": amd64WritesLast, "psubw": amd64WritesLast, "psubd": amd64WritesLast, "psubq": amd64WritesLast,
		"psllq": amd64WritesLast, "psrlq": amd64WritesLast, "pslld": amd64WritesLast, "psrld": amd64WritesLast, "pslldq": amd64WritesLast, "psrldq": amd64WritesLast,
		"pcmpeqb": amd64WritesLast, "pcmpeqw": amd64WritesLast, "pcmpeqd": amd64WritesLast, "pcmpgtb": amd64WritesLast, "pcmpgtd": amd64WritesLast,
		"pmovmskb": amd64WritesLast, "movmskps": amd64WritesLast, "movmskpd": amd64WritesLast,
		"pinsrw": amd64WritesLast, "pextrw": amd64WritesLast,
		"addss": amd64WritesLast, "subss": amd64WritesLast, "mulss": amd64WritesLast, "divss": amd64WritesLast,
		"sqrtsd": amd64WritesLast, "sqrtss": amd64WritesLast, "minsd": amd64WritesLast, "maxsd": amd64WritesLast, "minss": amd64WritesLast, "maxss": amd64WritesLast,
		"cvtss2sd": amd64WritesLast, "cvtsd2ss": amd64WritesLast, "cvtsd2si": amd64WritesLast, "cvtss2si": amd64WritesLast,
		"cmp": amd64WritesNone, "test": amd64WritesNone, "bt": amd64WritesNone, "push": amd64WritesNone,
		"ucomisd": amd64WritesNone, "comisd": amd64WritesNone, "ucomiss": amd64WritesNone, "comiss": amd64WritesNone,
		"nop": amd64WritesNone, "endbr64": amd64WritesNone, "int3": amd64WritesNone, "pause": amd64WritesNone,
		"lfence": amd64WritesNone, "mfence": amd64WritesNone, "sfence": amd64WritesNone, "ret": amd64WritesNone,
		"xchg": amd64WritesBoth, "xadd": amd64WritesBoth,
		"mul": amd64WritesRdxRax, "div": amd64WritesRdxRax, "idiv": amd64WritesRdxRax, "cqto": amd64WritesRdxRax,
		"cltd": amd64WritesRdxRax, "cwtd": amd64WritesRdxRax, "cltq": amd64WritesRdxRax, "cwtl": amd64WritesRdxRax, "cbtw": amd64WritesRdxRax,
		"cmpxchg": amd64WritesRdxRax,
	}
	amd64EffectPrefixes = map[string]amd64Effect{"cmov": amd64WritesLast, "set": amd64WritesLast, "j": amd64WritesNone}
)

// amd64Register is a register named as an operand, with nothing around
// it.
var amd64Register = regexp.MustCompile(`^%[a-z0-9]+$`)

// amd64Imports returns, for the function's x86-64 code in a PE binary,
// the registers that hold an imported function when each instruction
// runs, by the instruction's index, for the calls and jumps through a
// register that a compiler writes once it has loaded a function from
// its slot of the table of imports into a register kept across calls:
// a forward analysis over the function's blocks that keeps, at each
// block's start, what every path into it leaves, the slot's function
// or nothing. It returns nil for a function that makes no such call,
// or of a binary with no slots.
func (d *decoder) amd64Imports(insts []inst) []map[string]string {
	if len(d.img.slots) == 0 || !amd64CallsThroughRegister(insts) {
		return nil
	}

	starts, succ := d.amd64Blocks(insts)
	in := make([]map[string]string, len(starts))
	out := make([]map[string]string, len(starts)) // nil: no path reached the block yet
	in[0] = map[string]string{}
	preds := make([][]int, len(starts))
	for b, ss := range succ {
		for _, s := range ss {
			preds[s] = append(preds[s], b)
		}
	}

	for changed := true; changed; {
		changed = false
		for b := range starts {
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

					for r, name := range entry {
						if out[p][r] != name {
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
			for _, x := range insts[starts[b]:amd64BlockEnd(starts, b, len(insts))] {
				amd64Track(x, state)
			}

			if out[b] == nil || !maps.Equal(out[b], state) {
				out[b] = state
				changed = true
			}
		}
	}

	held := make([]map[string]string, len(insts)) // kept for the calls and jumps through a register alone
	for b := range starts {
		state := maps.Clone(in[b])
		if state == nil {
			state = map[string]string{} // no path reaches it: it knows nothing
		}

		for i := starts[b]; i < amd64BlockEnd(starts, b, len(insts)); i++ {
			if amd64ThroughRegister(insts[i]) {
				held[i] = maps.Clone(state)
			}

			amd64Track(insts[i], state)
		}
	}

	return held
}

// amd64HeldImport returns the imported function the call or jump at
// index i goes to through a register that held, the analysis knows, a
// function loaded from its slot.
func amd64HeldImport(held []map[string]string, i int, in inst) (string, bool) {
	if held == nil || held[i] == nil {
		return "", false
	}

	operand := strings.TrimPrefix(in.args, amd64Indirect)
	r, ok := amd64PartOf[operand]
	if !ok || operand != amd64Parts[r][0] { // a call goes through a whole register
		return "", false
	}

	name := held[i][r]
	return name, name != ""
}

// amd64ThroughRegister reports whether an instruction calls or jumps
// through a register.
func amd64ThroughRegister(in inst) bool {
	return (in.op == amd64OpCall || in.op == amd64OpJump) && strings.HasPrefix(in.args, amd64Indirect) &&
		amd64Register.MatchString(strings.TrimPrefix(in.args, amd64Indirect))
}

// amd64CallsThroughRegister reports whether the function calls or jumps
// through a register.
func amd64CallsThroughRegister(insts []inst) bool {
	for _, in := range insts {
		if amd64ThroughRegister(in) {
			return true
		}
	}

	return false
}

// amd64Blocks splits the function's instructions into its basic
// blocks: the index of each block's first instruction, and each
// block's successors by their place in that list. A block ends at a
// jump, a return or a conditional jump, or before an instruction a
// local branch goes to.
func (d *decoder) amd64Blocks(insts []inst) (starts []int, succ [][]int) {
	targets := map[uint64]bool{}
	for _, in := range insts {
		if to, ok := d.amd64Local(in); ok {
			targets[to] = true
		}
	}

	byAddr := map[uint64]int{}
	for i, in := range insts {
		if i == 0 || targets[in.addr] || amd64Ends(insts[i-1].op) {
			byAddr[in.addr] = len(starts)
			starts = append(starts, i)
		}
	}

	succ = make([][]int, len(starts))
	for b := range starts {
		last := insts[amd64BlockEnd(starts, b, len(insts))-1]
		if to, ok := d.amd64Local(last); ok {
			if n, ok := byAddr[to]; ok {
				succ[b] = append(succ[b], n)
			}
		}

		if last.op != amd64OpJump && last.op != amd64OpReturn && b+1 < len(starts) {
			succ[b] = append(succ[b], b+1)
		}
	}

	return starts, succ
}

// amd64Local returns the address in the function a jump or a
// conditional jump goes to, and false for one that leaves it.
func (d *decoder) amd64Local(in inst) (uint64, bool) {
	if !amd64Ends(in.op) || in.op == amd64OpReturn || strings.HasPrefix(in.args, amd64Indirect) {
		return 0, false
	}

	t := d.img.resolveTarget(parseTarget(in.args, in.note))
	switch {
	case !t.known || t.library:
		return 0, false
	case t.name == d.f.name:
		return d.insts[0].addr + t.offset, true
	case t.name == "" && d.within(t.addr):
		return t.addr, true
	}

	return 0, false
}

// amd64Ends reports whether an instruction ends a block.
func amd64Ends(op string) bool {
	return op == amd64OpJump || op == amd64OpReturn || strings.HasPrefix(op, amd64OpCond) || strings.HasPrefix(op, amd64OpLoop)
}

// amd64BlockEnd is the index past the last instruction of block b.
func amd64BlockEnd(starts []int, b, n int) int {
	if b+1 < len(starts) {
		return starts[b+1]
	}

	return n
}

// amd64Track follows what the instruction does to the registers that
// hold an imported function: a load of one from its slot, relative to
// the instruction, into a whole register; a call, which may change
// every register the convention does not keep; and every other
// instruction's writes, those it names and those it does not, as
// amd64EffectOf says them, every register forgotten for an instruction
// it does not know.
func amd64Track(in inst, state map[string]string) {
	last := in.args[strings.LastIndex(in.args, amd64Operands)+1:]
	if in.op == amd64OpMove && strings.Contains(in.args, amd64RIP) {
		if r, ok := amd64PartOf[last]; ok && last == amd64Parts[r][0] {
			if name, ok := amd64Import(in); ok {
				state[r] = name
				return
			}
		}
	}

	if in.op == amd64OpCall {
		for r := range state {
			if !amd64KeptByCalls[r] {
				delete(state, r)
			}
		}

		return
	}

	switch amd64EffectOf(in) {
	case amd64WritesLast:
		delete(state, amd64PartOf[last])
	case amd64WritesNone:
	case amd64WritesBoth:
		for _, o := range strings.Split(in.args, amd64Operands) {
			delete(state, amd64PartOf[o])
		}
	case amd64WritesRdxRax:
		delete(state, "rax")
		delete(state, "rdx")
		delete(state, amd64PartOf[last]) // a compare-and-exchange's destination
	default:
		clear(state)
	}
}

// amd64EffectOf returns what the instruction does to the registers, by
// its mnemonic with or without its size suffix, or by its family; 0 for
// one the analysis does not know. A multiplication of one operand
// writes rdx and rax, one of more its last operand alone.
func amd64EffectOf(in inst) amd64Effect {
	op := in.op
	if op == "imul" || (len(op) == len("imulq") && strings.HasPrefix(op, "imul")) {
		if strings.Contains(in.args, amd64Operands) {
			return amd64WritesLast
		}

		return amd64WritesRdxRax
	}

	if e, ok := amd64Effects[op]; ok {
		return e
	}

	if amd64Prefix(op) { // alone on its line: the instruction it prefixes is read on the next
		return amd64WritesNone
	}

	if n := len(op); n > 1 && strings.ContainsRune(amd64SizeSuffixes, rune(op[n-1])) {
		if e, ok := amd64Effects[op[:n-1]]; ok {
			return e
		}
	}

	for p, e := range amd64EffectPrefixes {
		if strings.HasPrefix(op, p) {
			return e
		}
	}

	return 0
}

// amd64RegisterOf returns the 64-bit register an operand names, whole
// or a part of it, and false for an operand that is no register.
func amd64RegisterOf(operand string) (string, bool) {
	r, ok := amd64PartOf[operand]
	return r, ok
}
