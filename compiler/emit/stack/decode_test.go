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
