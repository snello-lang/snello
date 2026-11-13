// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stack

import (
	"strings"
	"testing"
)

// tlvRange is the descriptor section of the arm64 fixtures.
var tlvRange = [2]uint64{0x10000c1b8, 0x10000c200}

// decodeArm64 reads one macOS listing of functions.
func decodeArm64(t *testing.T, listing string) *Image {
	t.Helper()
	img := &Image{arch: arm64, os: darwin, functions: map[string]*function{}, cLibrary: map[string]bool{"_malloc": true}}
	img.read([]byte(listing), tlvRange)
	return img
}

func kinds(f *function) string {
	var out []string
	for _, c := range f.calls {
		switch c.kind {
		case direct:
			out = append(out, c.target)
		case library:
			out = append(out, "lib:"+c.target)
		case pointer:
			out = append(out, "*")
		case threadLocal:
			out = append(out, "tlv")
		}
	}

	return strings.Join(out, " ")
}

func TestArm64Frame(t *testing.T) {
	img := decodeArm64(t, `_run:
0000000100000010	stp	x29, x30, [sp, #-0x10]!
0000000100000014	sub	sp, sp, #0x1, lsl #12
0000000100000018	sub	sp, sp, #0x30
000000010000001c	str	x19, [sp, #-16]!
0000000100000020	stp	x20, x21, [sp, #0x20]
0000000100000024	add	sp, sp, #0x30
0000000100000028	mov	sp, x29
000000010000002c	ldp	x29, x30, [sp], #0x10
0000000100000030	ret
`)
	if f := img.functions["_run"]; f.frame != 0x10+0x1000+0x30+16 || f.unknown != "" || f.setsSP {
		t.Fatalf("got %+v", f)
	}
}

func TestArm64Calls(t *testing.T) {
	img := decodeArm64(t, `_run:
0000000100000010	bl	_leaf
0000000100000014	bl	0x100000100 ; symbol stub for: _malloc
0000000100000018	cbz	x0, 0x100000024
000000010000001c	blr	x8
0000000100000020	b	_tail
0000000100000024	b	0x100000100 ; symbol stub for: _free
_leaf:
0000000100000030	ret
_tail:
0000000100000040	b	0x100000030
`)
	if got := kinds(img.functions["_run"]); got != "_leaf lib:_malloc * _tail lib:_free" {
		t.Fatalf("_run calls %q", got)
	}

	if got := kinds(img.functions["_tail"]); got != "_leaf" {
		t.Fatalf("a branch to another function's first address is a tail call: %q", got)
	}
}

func TestArm64Linux(t *testing.T) {
	img := &Image{arch: arm64, os: linux, functions: map[string]*function{}, cLibrary: map[string]bool{}}
	img.read([]byte(`0000000000400600 <run>:
  400600:	stp	x29, x30, [sp, #-48]!
  400604:	bl	400480 <malloc@plt>
  400608:	cbz	x0, 400614 <run+0x14>
  40060c:	bl	400700 <leaf>
  400610:	b	400700 <leaf>
  400614:	tbnz	w0, #3, 400600 <run>
  400618:	ret

0000000000400700 <leaf>:
  400700:	ret
`), [2]uint64{})
	f := img.functions["run"]
	if f.frame != 48 || kinds(f) != "lib:malloc leaf leaf run" {
		t.Fatalf("got frame %d, calls %q", f.frame, kinds(f))
	}
}

func TestArm64Unknown(t *testing.T) {
	for _, c := range []struct {
		name, body, want string
		setsSP           bool
	}{
		{"dynamic frame", "0000000100000010\tsub\tsp, sp, x8\n", "depends on a value", false},
		{"jump table", "0000000100000010\tbr\tx8\n", "jump through a register", false},
		{"sets sp", "0000000100000010\tmov\tsp, x1\n", "", true},
		{"probe loop", "000000010000000c\tnop\n0000000100000010\tsub\tsp, sp, #0x1, lsl #12\n0000000100000014\tstr\txzr, [sp]\n0000000100000018\tcmp\tsp, x9\n000000010000001c\tb.ne\t0x100000010\n", "in a loop", false},
		{"nowhere", "0000000100000010\tb\t0x100009000\n", "start of no function", false},
	} {
		f := decodeArm64(t, "_run:\n"+c.body).functions["_run"]
		if c.setsSP != f.setsSP || (c.want != "" && !strings.Contains(f.unknown, c.want)) {
			t.Errorf("%s: got %+v", c.name, f)
		}
	}
}

// TestArm64ThreadLocal tells macOS's accesses of thread-local
// variables, a call through the descriptor's first word, from other
// calls through a register: a descriptor's address known in the block,
// one held on every path into a later block, and one held on a path
// but not another, which is a call through a pointer.
func TestArm64ThreadLocal(t *testing.T) {
	img := decodeArm64(t, `_inblock:
0000000100000010	adrp	x0, 11 ; 0x10000c000
0000000100000014	add	x0, x0, #0x1d0
0000000100000018	ldr	x8, [x0]
000000010000001c	blr	x8
0000000100000020	ret
_held:
0000000100000030	adrp	x21, 11 ; 0x10000c000
0000000100000034	add	x21, x21, #0x1b8
0000000100000038	cbz	x1, 0x100000048
000000010000003c	ldr	x8, [x21]
0000000100000040	mov	x0, x21
0000000100000044	blr	x8
0000000100000048	ldr	x8, [x21]
000000010000004c	blr	x8
0000000100000050	ret
_onepath:
0000000100000060	cbz	x1, 0x10000006c
0000000100000064	adrp	x21, 11 ; 0x10000c000
0000000100000068	add	x21, x21, #0x1b8
000000010000006c	ldr	x8, [x21]
0000000100000070	blr	x8
0000000100000074	ret
_reused:
0000000100000080	adrp	x21, 11 ; 0x10000c000
0000000100000084	add	x21, x21, #0x1b8
0000000100000088	cbz	x1, 0x100000094
000000010000008c	mov	w21, #-0x1
0000000100000090	b	0x100000098
0000000100000094	nop
0000000100000098	ldr	x8, [x21]
000000010000009c	blr	x8
00000001000000a0	ret
_outside:
00000001000000b0	adrp	x0, 3 ; 0x100004000
00000001000000b4	add	x0, x0, #0x9c0
00000001000000b8	ldr	x8, [x0]
00000001000000bc	blr	x8
00000001000000c0	ret
`)
	for name, want := range map[string]string{
		"_inblock": "tlv",
		"_held":    "tlv tlv",
		"_onepath": "*",
		"_reused":  "*",
		"_outside": "*",
	} {
		if got := kinds(img.functions[name]); got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}

func TestArm64Written(t *testing.T) {
	for _, c := range []struct {
		in   inst
		want []int
	}{
		{inst{op: "mov", args: "x21, x0"}, []int{21}},
		{inst{op: "str", args: "x21, [sp, #8]"}, nil},
		{inst{op: "ldp", args: "x22, x21, [sp, #0x40]"}, []int{22, 21}},
		{inst{op: "ldr", args: "x0, [x1], #8"}, []int{0, 1}},
		{inst{op: "stxr", args: "w9, x8, [x0]"}, []int{9}},
		{inst{op: "cmp", args: "x8, x21"}, nil},
	} {
		got := arm64Written(c.in)
		if len(got) != len(c.want) {
			t.Errorf("%v: got %v, want %v", c.in, got, c.want)
			continue
		}

		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%v: got %v, want %v", c.in, got, c.want)
			}
		}
	}
}

func TestArm64Immediate(t *testing.T) {
	for s, want := range map[string]int64{"#0x40": 0x40, "#64": 64, "#0x1, lsl #12": 0x1000} {
		if got, ok := arm64Immediate(s); !ok || got != want {
			t.Errorf("%s: got %d, %v", s, got, ok)
		}
	}

	if _, ok := arm64Immediate("x8"); ok {
		t.Error("a register read as an immediate")
	}
}
