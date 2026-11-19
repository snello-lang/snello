// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stack

import "fmt"

// A decoder reads one function's instructions into its frame and calls,
// with the rules of the image's architecture.
type decoder struct {
	img    *Image
	f      *function
	insts  []inst
	starts map[uint64]string // the first address of every function
	tlv    [2]uint64         // macOS's thread-local descriptors

	decrements []uint64            // the addresses of the instructions that move the stack pointer down
	probed     []uint64            // the addresses of each stack probe's call and of the decrement after it
	tracking   bool                // arm64: the registers are followed, for a call through one that names what it calls
	backward   bool                // a local branch goes back, so the function may have a cycle
	targets    map[uint64]bool     // the addresses local branches go to
	edges      map[uint64][]uint64 // each local branch's targets, by its address
	stops      map[uint64]bool     // the instructions that never go on to the next: a jump, a return
}

func (d *decoder) decode() {
	if len(d.insts) == 0 {
		return
	}

	d.targets = map[uint64]bool{}
	d.edges = map[uint64][]uint64{}
	d.stops = map[uint64]bool{}
	switch d.img.arch {
	case arm64:
		d.arm64()
	case amd64:
		d.amd64()
	}

	for _, a := range d.probed {
		if d.targets[a] {
			d.unknown(fmt.Sprintf("a branch to %#x, inside the frame a stack probe checks in %s", a, d.img.plain(d.f.name)))
		}
	}

	if d.backward && len(d.decrements) > 0 {
		if why := d.looped(); why != "" {
			d.unknown(why)
		}
	}
}

// looped returns why the function's frame is not a sum of its
// decrements: a decrement on a cycle of its flow, which may move the
// stack pointer down any number of times, or a branch to an address no
// instruction starts at, whose flow the analysis cannot follow; or ""
// when neither. The flow is the local branches and every instruction's
// way on to the next, but a jump's and a return's, so a call goes on,
// one that never returns too, which only adds a way: a decrement found
// on no cycle is on none. A decrement a backward branch merely spans
// is no loop: gcc puts the panics of a function after its end, each
// making room for its call's arguments, and branches back from below
// them to where two paths of its body meet.
func (d *decoder) looped() string {
	index := make(map[uint64]int, len(d.insts))
	for i, in := range d.insts {
		index[in.addr] = i
	}

	next := make([][]int, len(d.insts))
	for i, in := range d.insts {
		if !d.stops[in.addr] && i+1 < len(d.insts) {
			next[i] = append(next[i], i+1)
		}

		for _, to := range d.edges[in.addr] {
			j, ok := index[to]
			if !ok {
				return fmt.Sprintf("a branch to %#x, inside an instruction of %s", to, d.img.plain(d.f.name))
			}

			next[i] = append(next[i], j)
		}
	}

	for _, a := range d.decrements {
		start := index[a]
		seen := make([]bool, len(d.insts))
		work := append([]int(nil), next[start]...)
		for len(work) > 0 {
			i := work[len(work)-1]
			work = work[:len(work)-1]
			if i == start {
				return fmt.Sprintf("a stack decrement in a loop in %s", d.img.plain(d.f.name))
			}

			if !seen[i] {
				seen[i] = true
				work = append(work, next[i]...)
			}
		}
	}

	return ""
}

// stop records an instruction that never goes on to the next one.
func (d *decoder) stop(in inst) {
	d.stops[in.addr] = true
}

// within reports whether addr is in the function being decoded.
func (d *decoder) within(addr uint64) bool {
	return addr >= d.insts[0].addr && addr <= d.insts[len(d.insts)-1].addr
}

// branch records a branch at in to t, the instruction's target: a
// local branch, a tail call to another function or to a library's, or
// a branch to a place the analysis cannot name. A branch to the
// function's own entry is local too: a loop that is the whole function,
// which a compiler writes for a task that computes without end, or a
// tail call to itself; one that runs the prologue again is a decrement
// on a cycle, which looped reports.
func (d *decoder) branch(in inst, t target) {
	t = d.img.resolveTarget(t)
	switch {
	case !t.known:
		d.unknown("a branch to a place not named in " + d.img.plain(d.f.name))
	case t.library:
		d.f.calls = append(d.f.calls, call{kind: library, target: t.name})
	case t.name != "":
		if t.name == d.f.name {
			d.local(in, d.insts[0].addr+t.offset)
			return
		}

		d.f.calls = append(d.f.calls, call{kind: direct, target: t.name}) // into its middle too: see call
	default:
		if d.within(t.addr) {
			d.local(in, t.addr)
			return
		}

		if name, ok := d.starts[t.addr]; ok {
			d.f.calls = append(d.f.calls, call{kind: direct, target: name})
			return
		}

		if name, ok := d.pieceInto(t.addr); ok {
			d.f.calls = append(d.f.calls, call{kind: direct, target: name}) // into the middle of a piece: see call
			return
		}

		d.unknown(fmt.Sprintf("a branch to %#x, the start of no function", t.addr))
	}
}

// local records a branch inside the function, and whether it goes back.
func (d *decoder) local(in inst, to uint64) {
	d.targets[to] = true
	d.edges[in.addr] = append(d.edges[in.addr], to)
	d.backward = d.backward || to <= in.addr
}

// call records a call at in to t, which is never local.
func (d *decoder) call(t target) {
	t = d.img.resolveTarget(t)
	switch {
	case !t.known:
		d.unknown("a call of a function not named in " + d.img.plain(d.f.name))
	case t.library:
		d.f.calls = append(d.f.calls, call{kind: library, target: t.name})
	case t.name != "":
		// A call or a branch into the middle of another function, as gcc
		// shares the tail two functions end in alike, a message and an
		// exit, runs code of that function on the caller's frame: the
		// stack pointer moves down there only by that function's own
		// decrements, its frame, each once, since a decrement in a loop
		// is no frame, and up by its restores, and the calls it makes
		// are its calls. It is counted as a call of the whole function,
		// which is never less.
		d.f.calls = append(d.f.calls, call{kind: direct, target: t.name})
	default:
		if name, ok := d.starts[t.addr]; ok {
			d.f.calls = append(d.f.calls, call{kind: direct, target: name})
			return
		}

		if name, ok := d.pieceInto(t.addr); ok {
			d.f.calls = append(d.f.calls, call{kind: direct, target: name}) // into the middle of a piece, counted as a call of the whole piece
			return
		}

		d.unknown(fmt.Sprintf("a call of %#x, the start of no function", t.addr))
	}
}

// probe records a frame a stack probe checked: the probe's call at call
// and the decrement after it at in, which takes size bytes. The size is
// what the instruction before the call put in its register, so a branch
// to the call or to the decrement, from a path that put another size
// there, makes the function unknown once its branches are all read.
func (d *decoder) probe(call uint64, in inst, size int64) {
	d.grow(in, size)
	d.probed = append(d.probed, call, in.addr)
}

// pieceInto returns the piece of a PE binary's code that holds addr, a
// call or a branch into the middle of it, and false for a binary not
// split into pieces.
func (d *decoder) pieceInto(addr uint64) (string, bool) {
	if d.img.pieces == nil {
		return "", false
	}

	return d.img.pieceAt(addr)
}

// grow records an instruction that moves the stack pointer down by n.
func (d *decoder) grow(in inst, n int64) {
	d.f.frame += n
	d.decrements = append(d.decrements, in.addr)
}

// unknown records the first thing about the function the analysis does
// not understand.
func (d *decoder) unknown(why string) {
	if d.f.unknown == "" {
		d.f.unknown = why
	}
}
