// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stack

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// pieceMark comes between a symbol's name and the offset of a piece of
// its code that is a function of its own, "WakeByAddressSingle+0x10",
// a name no symbol has.
const pieceMark = "+0x"

// The mnemonics that end a function's way on, the instruction after
// them reached by a branch or by nothing, and those a linker pads the
// space between two functions with, as the disassemblers print them.
var (
	amd64Stops   = map[string]bool{amd64OpJump: true, amd64OpReturn: true, "ud2": true}
	amd64Padding = map[string]bool{"int3": true, "nop": true, "nopw": true, "nopl": true}
	arm64Padding = map[string]bool{"udf": true, "nop": true}
)

// A body is the instructions the disassembler printed under one label,
// at the label's address where the disassembler prints one.
type body struct {
	name  string
	addr  uint64
	insts []inst
}

// A piece is a function of a PE binary's code: a symbol's code, or a
// part of it no branch of the symbol's reaches, from its first
// instruction's address to its last's.
type piece struct {
	start, end uint64
	name       string
}

// A localEdge is a branch of a body to an address in the body.
type localEdge struct{ from, to uint64 }

// splitPieces splits the bodies of a PE binary's listing into pieces,
// so that a function with no symbol of its own, as the thunk of an
// import by its ordinal is, is not read as the end of the function
// before it. A body is cut after an end of its way on, a jump or a
// return, and the padding after it, where no branch of the body crosses
// the cut either way, so that each piece holds every way into and out
// of its code but its calls. A body is left whole when its name is
// shared, since a target named by a shared name is read by the name,
// as on every system, or when it jumps through a register or memory
// other than a slot of the table of imports, whose targets a cut could
// part from it. A piece is named for its symbol and its offset from it.
// It records each piece's range, and each symbol's address where its
// name is its own, for resolveTarget.
func (img *Image) splitPieces(bodies []*body) []*body {
	img.symbolStart = map[string]uint64{}
	named := map[string]int{}
	for _, b := range bodies {
		named[b.name]++
	}

	var out []*body
	for _, b := range bodies {
		if len(b.insts) == 0 {
			continue
		}

		parts := []*body{b}
		if named[b.name] == 1 {
			img.symbolStart[b.name] = b.addr
			if !img.jumpsThroughPointer(b) {
				parts = img.split(b)
			}
		}

		for _, p := range parts {
			img.pieces = append(img.pieces, piece{start: p.insts[0].addr, end: p.insts[len(p.insts)-1].addr, name: p.name})
			out = append(out, p)
		}
	}

	sort.Slice(img.pieces, func(i, j int) bool { return img.pieces[i].start < img.pieces[j].start })
	return out
}

// split returns the pieces of one body.
func (img *Image) split(b *body) []*body {
	edges := img.localEdges(b)
	cur := &body{name: b.name, addr: b.addr}
	var out []*body
	for i := 0; i < len(b.insts); i++ {
		cur.insts = append(cur.insts, b.insts[i])
		if !img.stops(b.insts[i]) {
			continue
		}

		j := i + 1
		for j < len(b.insts) && img.padding(b.insts[j]) {
			j++
		}

		if j == len(b.insts) || crossed(edges, b.insts[i+1].addr) {
			continue
		}

		out = append(out, cur)
		at := b.insts[j].addr
		cur = &body{name: fmt.Sprintf("%s%s%x", b.name, pieceMark, at-b.addr), addr: at}
		i = j - 1 // the padding belongs to neither
	}

	return append(out, cur)
}

// crossed reports whether a branch goes across the cut at the address
// cut, from before it to after or the other way.
func crossed(edges []localEdge, cut uint64) bool {
	for _, e := range edges {
		if (e.from < cut) != (e.to < cut) {
			return true
		}
	}

	return false
}

// localEdges returns the body's branches to addresses in the body.
func (img *Image) localEdges(b *body) []localEdge {
	var out []localEdge
	first, last := b.insts[0].addr, b.insts[len(b.insts)-1].addr
	for _, in := range b.insts {
		var t target
		switch img.arch {
		case amd64:
			in = amd64Plain(in)
			if !amd64Ends(in.op) || in.op == amd64OpReturn || strings.HasPrefix(in.args, amd64Indirect) {
				continue
			}

			t = parseTarget(in.args, in.note)
		case arm64:
			if !arm64Branch(in.op) {
				continue
			}

			t = parseTarget(arm64Last(in.args), in.note)
		}

		switch {
		case t.name == b.name:
			out = append(out, localEdge{from: in.addr, to: b.addr + t.offset})
		case t.name == "" && t.known && t.addr >= first && t.addr <= last:
			out = append(out, localEdge{from: in.addr, to: t.addr})
		}
	}

	return out
}

// jumpsThroughPointer reports whether the body jumps through a register
// or memory that holds no import, as a table of jumps does, whose
// targets the analysis does not know.
func (img *Image) jumpsThroughPointer(b *body) bool {
	for i, in := range b.insts {
		switch img.arch {
		case amd64:
			in = amd64Plain(in)
			if in.op == amd64OpJump && strings.HasPrefix(in.args, amd64Indirect) {
				if _, ok := amd64Import(in); !ok {
					return true
				}
			}
		case arm64:
			if arm64JumpReg(in.op) && !img.arm64ThunkJump(b.insts, i) {
				return true
			}
		}
	}

	return false
}

// arm64ThunkJump reports whether the jump at index i ends the thunk of
// an import: an adrp of the slot's page, a load of the slot into the
// register, and the jump through it.
func (img *Image) arm64ThunkJump(insts []inst, i int) bool {
	if i < 2 || insts[i-2].op != arm64OpPage || insts[i-1].op != arm64OpLoad {
		return false
	}

	m := arm64LoadAt.FindStringSubmatch(insts[i-1].args)
	page := arm64Page.FindStringSubmatch(arm64Second(insts[i-2].args))
	if m == nil || page == nil || arm64First(insts[i-2].args) != m[2] || m[1] != strings.TrimSpace(insts[i].args) {
		return false
	}

	base, err1 := strconv.ParseUint(page[1], 0, 64)
	off, err2 := strconv.ParseUint(m[3], 0, 64)
	_, slot := img.slots[base+off]
	return err1 == nil && err2 == nil && slot
}

// stops reports whether an instruction never goes on to the next.
func (img *Image) stops(in inst) bool {
	if img.arch == amd64 {
		return amd64Stops[amd64Plain(in).op]
	}

	return in.op == arm64OpBranch || arm64JumpReg(in.op) || strings.HasPrefix(in.op, arm64OpReturn)
}

// padding reports whether an instruction is what a linker pads between
// two functions with.
func (img *Image) padding(in inst) bool {
	if img.arch == amd64 {
		return amd64Padding[amd64Plain(in).op]
	}

	return arm64Padding[in.op]
}

// resolveTarget turns a target named by a symbol and an offset into the
// address it names, where the binary was split into pieces and the
// symbol is named once, so that a branch or a call into a symbol's code
// reaches the piece that holds it; any other target is as it was.
func (img *Image) resolveTarget(t target) target {
	if img.pieces == nil || t.name == "" || t.library {
		return t
	}

	start, ok := img.symbolStart[t.name]
	if !ok {
		return t
	}

	return target{addr: start + t.offset, known: true}
}

// pieceAt returns the name of the piece whose instructions span the
// address, and false for an address in none.
func (img *Image) pieceAt(addr uint64) (string, bool) {
	i := sort.Search(len(img.pieces), func(i int) bool { return img.pieces[i].start > addr })
	if i == 0 || addr > img.pieces[i-1].end {
		return "", false
	}

	return img.pieces[i-1].name, true
}

// baseSymbol returns the symbol a piece's name is of: the name before
// pieceMark, or the name itself.
func baseSymbol(name string) string {
	if i := strings.Index(name, pieceMark); i > 0 {
		return name[:i]
	}

	return name
}
