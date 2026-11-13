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

	decrements []uint64        // the addresses of the instructions that move the stack pointer down
	backward   [][2]uint64     // the local branches that go back, as target and branch
	targets    map[uint64]bool // the addresses local branches go to
}

func (d *decoder) decode() {
	if len(d.insts) == 0 {
		return
	}

	d.targets = map[uint64]bool{}
	switch d.img.arch {
	case arm64:
		d.arm64()
	case amd64:
		d.amd64()
	}

	for _, b := range d.backward {
		for _, a := range d.decrements {
			if a >= b[0] && a <= b[1] && d.f.unknown == "" {
				d.f.unknown = fmt.Sprintf("a stack decrement in a loop in %s", d.img.plain(d.f.name))
			}
		}
	}
}

// within reports whether addr is in the function being decoded.
func (d *decoder) within(addr uint64) bool {
	return addr >= d.insts[0].addr && addr <= d.insts[len(d.insts)-1].addr
}

// branch records a branch at in to t, the instruction's target: a
// local branch, a tail call to another function or to a library's, or
// a branch to a place the analysis cannot name. It reports whether the
// branch stays in the function.
func (d *decoder) branch(in inst, t target) bool {
	switch {
	case !t.known:
		d.unknown("a branch to a place not named in " + d.img.plain(d.f.name))
		return false
	case t.library:
		d.f.calls = append(d.f.calls, call{kind: library, target: t.name})
		return false
	case t.name != "":
		if t.name == d.f.name && t.offset > 0 {
			d.local(in, d.insts[0].addr+t.offset)
			return true
		}

		d.f.calls = append(d.f.calls, call{kind: direct, target: t.name}) // into its middle too: see call
		return false
	default:
		if d.within(t.addr) && t.addr != d.insts[0].addr {
			d.local(in, t.addr)
			return true
		}

		if name, ok := d.starts[t.addr]; ok {
			d.f.calls = append(d.f.calls, call{kind: direct, target: name})
			return false
		}

		d.unknown(fmt.Sprintf("a branch to %#x, the start of no function", t.addr))
		return false
	}
}

// local records a branch inside the function, and whether it goes back.
func (d *decoder) local(in inst, to uint64) {
	d.targets[to] = true
	if to <= in.addr {
		d.backward = append(d.backward, [2]uint64{to, in.addr})
	}
}

// call records a call at in to t, which is never local.
func (d *decoder) call(t target) {
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

		d.unknown(fmt.Sprintf("a call of %#x, the start of no function", t.addr))
	}
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
