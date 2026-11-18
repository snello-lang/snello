// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stack

import (
	"strings"
	"testing"
)

// decodeWindows reads a listing of LLVM's objdump of a Windows binary
// whose slots hold WakeByAddressSingle and GetAddrInfoW.
func decodeWindows(t *testing.T, a arch, listing string) *Image {
	t.Helper()
	img := &Image{arch: a, os: windows, functions: map[string]*function{}, cLibrary: map[string]bool{},
		slots: map[uint64]string{0x140066560: "WakeByAddressSingle", 0x140066570: "GetAddrInfoW", 0x140002060: "WakeByAddressSingle", 0x140002068: "GetAddrInfoW"}}
	img.read([]byte(listing), [2]uint64{})
	return img
}

// TestPiecesAmd64 splits a PE binary's x86-64 code where no branch
// reaches it: the thunk of an import by its ordinal, which has no
// symbol, after the padding that follows the thunk before it, is a
// function of its own, which a call of its address reaches, and the
// thunk before it calls only its own import; code after a return that
// a branch reaches stays in its function, as does code after a jump a
// branch reaches through the nop that aligns it; and a call into the
// middle of a piece is a call of the piece.
func TestPiecesAmd64(t *testing.T) {
	img := decodeWindows(t, amd64, `0000000140058c20 <WakeByAddressSingle>:
140058c20:     	jmpq	*0xd93a(%rip)           # 0x140066560 <__imp_WakeByAddressSingle>
140058c26:     	int3
140058c27:     	int3
140058c28:     	int3
140058c29:     	int3
140058c2a:     	int3
140058c2b:     	int3
140058c2c:     	int3
140058c2d:     	int3
140058c2e:     	int3
140058c2f:     	int3
140058c30:     	jmpq	*0xd93a(%rip)           # 0x140066570 <__imp_GetAddrInfoW>

0000000140001000 <resolver>:
140001000:     	subq	$0x28, %rsp
140001004:     	callq	0x140058c30 <WakeByAddressSingle+0x10>
140001009:     	addq	$0x28, %rsp
14000100d:     	retq

0000000140001010 <cold>:
140001010:     	testl	%ecx, %ecx
140001012:     	jne	0x140001015 <cold+0x5>
140001014:     	retq
140001015:     	callq	0x140001000 <resolver>
14000101a:     	retq

0000000140001030 <aligned>:
140001030:     	cmpl	$0x2, %eax
140001033:     	jne	0x14000103a <aligned+0xa>
140001035:     	jmp	0x140001000 <resolver>
14000103a:     	nop
14000103b:     	callq	0x140001000 <resolver>
140001040:     	retq

0000000140001020 <middle>:
140001020:     	callq	0x140001004 <resolver+0x4>
140001025:     	retq
`)
	for name, want := range map[string]string{
		"WakeByAddressSingle":      "lib:WakeByAddressSingle",
		"WakeByAddressSingle+0x10": "lib:GetAddrInfoW",
		"resolver":                 "WakeByAddressSingle+0x10",
		"cold":                     "resolver",
		"middle":                   "resolver",
		"aligned":                  "resolver resolver",
	} {
		f := img.functions[name]
		if f == nil {
			t.Errorf("no function %s in %v", name, img.Functions())
			continue
		}

		if got := kinds(f); got != want || f.unknown != "" {
			t.Errorf("%s: calls %q (%s), want %q", name, got, f.unknown, want)
		}
	}

	if img.functions["cold+0x5"] != nil || img.functions["aligned+0xb"] != nil {
		t.Errorf("code a branch reaches was split from its function: %v", img.Functions())
	}
}

// TestPiecesArm64 splits the thunks of two imports, the second with no
// symbol, as a PE binary's arm64 code lays them out one after another,
// each its own function.
func TestPiecesArm64(t *testing.T) {
	img := decodeWindows(t, arm64, `00000001400010c4 <WakeByAddressSingle>:
1400010c4:     	adrp	x16, 0x140002000
1400010c8:     	ldr	x16, [x16, #0x60]
1400010cc:     	br	x16
1400010d0:     	adrp	x16, 0x140002000
1400010d4:     	ldr	x16, [x16, #0x68]
1400010d8:     	br	x16
`)
	for name, want := range map[string]string{"WakeByAddressSingle": "lib:WakeByAddressSingle", "WakeByAddressSingle+0xc": "lib:GetAddrInfoW"} {
		if f := img.functions[name]; f == nil || kinds(f) != want {
			t.Errorf("%s: %+v, want calls %q among %v", name, f, want, img.Functions())
		}
	}
}

// TestPiecesSharedName reads a target named by a symbol two functions
// share, two static functions of one name, by its name, as every system
// does, since its address is not known: the call is of the two read as
// one.
func TestPiecesSharedName(t *testing.T) {
	img := decodeWindows(t, amd64, `0000000140001000 <helper>:
140001000:     	retq

0000000140001010 <helper>:
140001010:     	subq	$0x40, %rsp
140001014:     	addq	$0x40, %rsp
140001018:     	retq

0000000140001020 <caller>:
140001020:     	callq	0x140001014 <helper+0x4>
140001025:     	retq
`)
	if got := kinds(img.functions["caller"]); got != "helper" {
		t.Errorf("caller: calls %q, want helper, by its name", got)
	}

	if f := img.functions["helper"]; f == nil || f.frame != 0x40 {
		t.Errorf("helper: %+v, want the worse of the two", f)
	}
}

// TestPiecesSound holds the cuts to what keeps every bound whole: a body
// whose name two symbols share is not cut, so a call into its code by
// that name counts the frame its second part makes; an address past
// every piece is the start of no function; a body a branch crosses a
// would-be cut of is not cut there, so that what the branch carries,
// a register that held no import, reaches the code after it; and a
// body that jumps through a register, as a table of jumps does, is not
// cut at all.
func TestPiecesSound(t *testing.T) {
	img := decodeWindows(t, amd64, `0000000140001000 <helper>:
140001000:     	retq
140001001:     	subq	$0x1000, %rsp
140001008:     	addq	$0x1000, %rsp
14000100f:     	retq

0000000140001010 <helper>:
140001010:     	retq

0000000140001020 <caller>:
140001020:     	callq	0x140001001 <helper+0x1>
140001025:     	retq

0000000140001030 <stray>:
140001030:     	callq	0x140009000
140001035:     	retq

0000000140001040 <crossing>:
140001040:     	movq	%rcx, %r15
140001043:     	testl	%edx, %edx
140001045:     	jne	0x140001057 <crossing+0x17>
140001047:     	retq
140001048:     	int3
140001050:     	movq	0x65581(%rip), %r15     # 0x140066570 <__imp_GetAddrInfoW>
140001057:     	callq	*%r15
14000105a:     	retq

0000000140001060 <table>:
140001060:     	jmpq	*%rax
140001062:     	int3
140001063:     	callq	0x140001020 <caller>
140001068:     	retq
`)
	if f := img.functions["helper"]; f == nil || f.frame < 0x1000 {
		t.Errorf("helper, whose name two symbols share: %+v, want its whole code read, the 4096 bytes its second part takes", f)
	}

	if f := img.functions["stray"]; !strings.Contains(f.unknown, "the start of no function") {
		t.Errorf("stray: %+v, want a call of no function", f)
	}

	if f := img.functions["crossing"]; f == nil || kinds(f) != "*" || img.functions["crossing+0x10"] != nil {
		t.Errorf("crossing: %+v, pieces %v, want one piece calling through a pointer", f, img.Functions())
	}

	if f := img.functions["table"]; f == nil || kinds(f) != "* caller" || img.functions["table+0x3"] != nil {
		t.Errorf("table: %+v, pieces %v, want one piece", f, img.Functions())
	}
}

// TestPiecesStops ends a piece at every form of a jump or a return that
// does not go on, and not at a breakpoint, after which code may run.
func TestPiecesStops(t *testing.T) {
	amd := decodeWindows(t, amd64, `0000000140001000 <ret8>:
140001000:     	retq	$0x8
140001003:     	int3
140001004:     	callq	0x140001000 <ret8>

0000000140001010 <rex>:
140001010:     	rex64 jmpq *%rax
140001013:     	int3
140001014:     	callq	0x140001010 <rex>

0000000140001020 <notracked>:
140001020:     	notrack jmpq *%rax
140001023:     	int3
140001024:     	callq	0x140001020 <notracked>
`)
	for _, name := range []string{"ret8+0x4"} {
		if amd.functions[name] == nil {
			t.Errorf("no piece %s in %v", name, amd.Functions())
		}
	}

	arm := decodeWindows(t, arm64, `0000000140001000 <authenticated>:
140001000:     	retaa
140001004:     	bl	0x140001000 <authenticated>

0000000140001010 <trapped>:
140001010:     	brk	#0xf000
140001014:     	bl	0x140001010 <trapped>
140001018:     	ret
`)
	if arm.functions["authenticated+0x4"] == nil || arm.functions["trapped+0x4"] != nil {
		t.Errorf("arm64 pieces %v, want one after retaa and none after brk", arm.Functions())
	}
}
