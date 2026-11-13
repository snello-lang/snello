// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stack

import (
	"strings"
	"testing"
)

func TestDecodeTargets(t *testing.T) {
	img := &Image{arch: arm64, os: linux, functions: map[string]*function{}, cLibrary: map[string]bool{}}
	img.read([]byte(`0000000000400600 <intomiddle>:
  400600:	bl	400704 <other+0x4>

0000000000400610 <branchmiddle>:
  400610:	b	400704 <other+0x4>

0000000000400620 <byaddress>:
  400620:	b	400700 <other>
  400624:	bl	400700 <other>

0000000000400700 <other>:
  400700:	nop
  400704:	ret
`), [2]uint64{})
	for _, name := range []string{"intomiddle", "branchmiddle"} { // counted as a call of the whole of other
		if f := img.functions[name]; f.unknown != "" || kinds(f) != "other" {
			t.Errorf("%s: got %+v, %q", name, f, kinds(f))
		}
	}

	if f := img.functions["byaddress"]; f.unknown != "" || kinds(f) != "other other" {
		t.Errorf("byaddress: got %+v, %q", f, kinds(f))
	}
}

func TestDecodeAddresses(t *testing.T) {
	img := decodeArm64(t, `_run:
0000000100000010	bl	0x100000040
0000000100000014	bl	0x100000099
_other:
0000000100000040	ret
`)
	f := img.functions["_run"]
	if kinds(f) != "_other" || !strings.Contains(f.unknown, "the start of no function") {
		t.Fatalf("got %+v, %q", f, kinds(f))
	}
}

func TestMerge(t *testing.T) {
	a := &function{name: "f", frame: 16, calls: []call{{kind: direct, target: "x"}}}
	merge(a, &function{name: "f", frame: 48, calls: []call{{kind: pointer}}, unknown: "u", setsSP: true})
	if a.frame != 48 || len(a.calls) != 2 || a.unknown != "u" || !a.setsSP {
		t.Fatalf("got %+v", a)
	}
}

// TestDecodeSharedTail bounds a function that branches into the tail of
// another, as gcc shares the tail two functions end in alike: at least
// the other's frame and the deepest of its calls, on the caller's own.
func TestDecodeSharedTail(t *testing.T) {
	img := &Image{arch: arm64, os: linux, functions: map[string]*function{}, cLibrary: map[string]bool{"exit": true}}
	img.read([]byte(`0000000000400500 <base>:
  400500:	stp	x29, x30, [sp, #-16]!
  400504:	blr	x8
  400508:	ldp	x29, x30, [sp], #16
  40050c:	ret

0000000000400600 <jumper>:
  400600:	stp	x29, x30, [sp, #-32]!
  400604:	b	400714 <owner+0x14>

0000000000400700 <owner>:
  400700:	stp	x29, x30, [sp, #-64]!
  400704:	mov	x29, sp
  400708:	ldp	x29, x30, [sp], #64
  40070c:	ret
  400710:	nop
  400714:	mov	w0, #0x2
  400718:	bl	400800 <exit@plt>
`), [2]uint64{})
	b := img.Bound("jumper", "base", 0, Options{})
	exit, _ := cLibraryStack("exit")
	if !b.Known() || b.Bytes < 16+32+64+exit+redZone {
		t.Fatalf("got %+v", b)
	}
}

// TestDecodeLoops holds a decrement of the stack pointer to the cycles
// of the function's flow: one a backward branch spans but no path from
// its target reaches, as gcc lays out the panics after a function's end,
// counts in the frame; one inside a loop, and one a backward branch
// reaches through a fall through, make the function unknown, as does a
// branch into the middle of an instruction.
func TestDecodeLoops(t *testing.T) {
	cases := []struct {
		name, listing string
		frame         int64
		unknown       string
	}{
		{"panics after the end", `0000000000401000 <f>:
  401000:	push   %rbx
  401001:	je     401010 <f+0x10>
  401003:	nop
  401004:	pop    %rbx
  401005:	ret
  401010:	sub    $0x20,%rsp
  401014:	call   402000 <g>
  401019:	jmp    401003 <f+0x3>
`, 8 + 0x20, ""},
		{"push in a loop", `0000000000401000 <f>:
  401000:	nop
  401001:	push   %rbx
  401002:	jne    401001 <f+0x1>
  401004:	ret
`, 0, "a stack decrement in a loop in f"},
		{"falls into the loop", `0000000000401000 <f>:
  401000:	nop
  401001:	sub    $0x10,%rsp
  401005:	nop
  401006:	jmp    401001 <f+0x1>
`, 0, "a stack decrement in a loop in f"},
		{"into an instruction", `0000000000401000 <f>:
  401000:	push   %rbx
  401001:	nop
  401002:	jne    401003 <f+0x3>
  401004:	ret
  401005:	jmp    401001 <f+0x1>
`, 0, "inside an instruction of f"},
	}

	for _, c := range cases {
		img := &Image{arch: amd64, os: linux, functions: map[string]*function{}, cLibrary: map[string]bool{}}
		img.read([]byte(c.listing), [2]uint64{})
		f := img.functions["f"]
		switch {
		case c.unknown == "" && (f.unknown != "" || f.frame != c.frame):
			t.Errorf("%s: got frame %d, %q; want %d", c.name, f.frame, f.unknown, c.frame)
		case c.unknown != "" && !strings.Contains(f.unknown, c.unknown):
			t.Errorf("%s: got %q, want %q", c.name, f.unknown, c.unknown)
		}
	}
}
