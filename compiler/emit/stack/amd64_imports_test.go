// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stack

import "testing"

// decodeWindowsAmd64 reads one listing of LLVM's objdump of a Windows
// x86-64 binary whose slot at 0x140066840 holds QueryPerformanceCounter.
func decodeWindowsAmd64(t *testing.T, listing string) *Image {
	t.Helper()
	img := &Image{arch: amd64, os: windows, functions: map[string]*function{}, cLibrary: map[string]bool{}, slots: map[uint64]string{0x140066840: "QueryPerformanceCounter"}}
	img.read([]byte(listing), [2]uint64{})
	return img
}

// TestAmd64Imports follows a function loaded from its slot into a
// register: a load hoisted out of a loop into a register a call keeps,
// called through on every turn, is a call of the import; and a register
// one path into the call writes over, one loaded into a register a call
// may change and called after another call, one an instruction writes
// without naming it, are calls through a pointer, as is a call through
// a register no load reached. A comparison of the register reads it
// and keeps it. A register a write the analysis does not know of, mulx,
// a loop, a system call, a string compare of SSE, may have changed is
// forgotten, as one written on a loop's way back, swapped, popped,
// copied to another, or reached by no path from the function's entry.
// A prefix LLVM's objdump prints on a line of its own, lock, writes
// none.
func TestAmd64Imports(t *testing.T) {
	img := decodeWindowsAmd64(t, `0000000140003c40 <hoisted>:
140003c40:     	pushq	%r15
140003c42:     	movq	0x62bf7(%rip), %r15     # 0x140066840 <__imp_QueryPerformanceCounter>
140003c49:     	cmpq	$0x0, %r15
140003c4d:     	leaq	0x8(%rsp), %rcx
140003c52:     	callq	*%r15
140003c55:     	testl	%eax, %eax
140003c57:     	jne	0x140003c4d <hoisted+0xd>
140003c59:     	popq	%r15
140003c5b:     	retq

0000000140003c60 <overwritten>:
140003c60:     	movq	0x62bd9(%rip), %r15     # 0x140066840 <__imp_QueryPerformanceCounter>
140003c67:     	testl	%ecx, %ecx
140003c69:     	je	0x140003c6e <overwritten+0xe>
140003c6b:     	movq	%rdx, %r15
140003c6e:     	callq	*%r15
140003c71:     	retq

0000000140003c80 <clobbered>:
140003c80:     	movq	0x62bb9(%rip), %rax     # 0x140066840 <__imp_QueryPerformanceCounter>
140003c87:     	callq	0x140003cc0 <leaf>
140003c8c:     	callq	*%rax
140003c8e:     	retq

0000000140003ca0 <implicit>:
140003ca0:     	movq	0x62b99(%rip), %rdx     # 0x140066840 <__imp_QueryPerformanceCounter>
140003ca7:     	mulq	%rcx
140003caa:     	callq	*%rdx
140003cac:     	retq

0000000140003cb0 <unloaded>:
140003cb0:     	callq	*%rbx
140003cb2:     	retq

0000000140003d00 <mulx>:
140003d00:     	movq	0x62b39(%rip), %r15     # 0x140066840 <__imp_QueryPerformanceCounter>
140003d07:     	mulxq	%rax, %r15, %r14
140003d0c:     	callq	*%r15
140003d0f:     	retq

0000000140003d10 <looped>:
140003d10:     	movq	0x62b29(%rip), %rcx     # 0x140066840 <__imp_QueryPerformanceCounter>
140003d17:     	loop	0x140003d19 <looped+0x9>
140003d19:     	callq	*%rcx
140003d1b:     	retq

0000000140003d20 <system>:
140003d20:     	movq	0x62b19(%rip), %rax     # 0x140066840 <__imp_QueryPerformanceCounter>
140003d27:     	syscall
140003d29:     	jmpq	*%rax

0000000140003d30 <stringed>:
140003d30:     	movq	0x62b09(%rip), %rcx     # 0x140066840 <__imp_QueryPerformanceCounter>
140003d37:     	pcmpistri	$0x0, %xmm1, %xmm0
140003d3d:     	callq	*%rcx
140003d3f:     	retq

0000000140003d40 <backedge>:
140003d40:     	movq	0x62af9(%rip), %r15     # 0x140066840 <__imp_QueryPerformanceCounter>
140003d47:     	callq	*%r15
140003d4a:     	movq	%rax, %r15
140003d4d:     	testl	%eax, %eax
140003d4f:     	jne	0x140003d47 <backedge+0x7>
140003d51:     	retq

0000000140003d60 <swapped>:
140003d60:     	movq	0x62ad9(%rip), %r15     # 0x140066840 <__imp_QueryPerformanceCounter>
140003d67:     	xchgq	%r15, %rcx
140003d6a:     	callq	*%r15
140003d6d:     	retq

0000000140003d70 <popped>:
140003d70:     	movq	0x62ac9(%rip), %r15     # 0x140066840 <__imp_QueryPerformanceCounter>
140003d77:     	popq	%r15
140003d79:     	callq	*%r15
140003d7c:     	retq

0000000140003e00 <shuffled>:
140003e00:     	movq	0x62a39(%rip), %r15     # 0x140066840 <__imp_QueryPerformanceCounter>
140003e07:     	pshufd	$0x44, %xmm0, %xmm0     # xmm0 = xmm0[0,1,0,1]
140003e0c:     	callq	*%r15
140003e0f:     	testl	%eax, %eax
140003e11:     	jne	0x140003e07 <shuffled+0x7>
140003e13:     	retq

0000000140003e20 <masked>:
140003e20:     	movq	0x62a19(%rip), %r15     # 0x140066840 <__imp_QueryPerformanceCounter>
140003e27:     	pmovmskb	%xmm0, %r15d
140003e2c:     	callq	*%r15
140003e2f:     	retq

0000000140003d80 <tail>:
140003d80:     	pushq	%rsi
140003d81:     	movq	0x62ab9(%rip), %rsi     # 0x140066840 <__imp_QueryPerformanceCounter>
140003d88:     	movq	%rsi, %rax
140003d8b:     	popq	%rsi
140003d8c:     	jmpq	*%rax

0000000140003d90 <unreached>:
140003d90:     	movq	0x62aa9(%rip), %r15     # 0x140066840 <__imp_QueryPerformanceCounter>
140003d97:     	retq
140003d98:     	callq	*%r15
140003d9b:     	retq

0000000140003da0 <locked>:
140003da0:     	movq	0x62a99(%rip), %r15     # 0x140066840 <__imp_QueryPerformanceCounter>
140003da7:     	lock
140003da8:     	cmpxchgl	%r13d, (%rsi)
140003dac:     	callq	*%r15
140003daf:     	retq

0000000140003cc0 <leaf>:
140003cc0:     	retq
`)
	for name, want := range map[string]string{
		"hoisted":       "lib:QueryPerformanceCounter",
		"overwritten":   "*",
		"clobbered":     "leaf *",
		"implicit":      "*",
		"unloaded":      "*",
		"mulx":          "*",
		"looped":        "*",
		"system":        "*",
		"stringed":      "*",
		"backedge":      "*",
		"swapped":       "*",
		"popped":        "*",
		"shuffled":      "lib:QueryPerformanceCounter", // a vector shuffle in the loop writes no general register
		"masked":        "*",                           // a vector instruction whose last operand is a general register writes it
		"tail":          "*",
		"unreached":     "",
		"unreached+0x8": "*", // the code no branch reaches is a piece of its own, which loaded nothing
		"locked":        "lib:QueryPerformanceCounter",
	} {
		if got := kinds(img.functions[name]); got != want {
			t.Errorf("%s: calls %q, want %q", name, got, want)
		}
	}
}

// TestAmd64EffectOf knows what the instructions a compiler writes do to
// the registers, a size suffix or none, a compare-and-exchange's rax
// among them, and knows none that writes a register it does not name
// otherwise: mulx, a loop, a string compare of SSE, a system call.
func TestAmd64EffectOf(t *testing.T) {
	for _, c := range []struct {
		op, args string
		want     amd64Effect
	}{
		{"cmp", "$0x0,%r15", amd64WritesNone}, {"cmpq", "$0x0,%r15", amd64WritesNone}, {"testl", "%eax,%eax", amd64WritesNone},
		{"xorl", "%r15d,%r15d", amd64WritesLast}, {"movslq", "%ecx,%r15", amd64WritesLast}, {"cmovne", "%rdx,%r15", amd64WritesLast},
		{"setb", "%al", amd64WritesLast}, {"jne", "0x1400 <f+0x8>", amd64WritesNone}, {"xchg", "%rax,%r15", amd64WritesBoth},
		{"imulq", "%rcx", amd64WritesRdxRax}, {"imulq", "%rcx,%r15", amd64WritesLast}, {"divq", "%rcx", amd64WritesRdxRax},
		{"cmpxchgl", "%ecx,(%rdx)", amd64WritesRdxRax}, {"mulxq", "%rax,%r15,%r14", 0}, {"loop", "0x1400 <f+0x8>", 0},
		{"pcmpistri", "$0x0,%xmm1,%xmm0", 0}, {"syscall", "", 0}, {"stosq", "%rax,%es:(%rdi)", 0},
		{"pshufd", "$0x44,%xmm0,%xmm0", amd64WritesLast}, {"pmovmskb", "%xmm0,%eax", amd64WritesLast}, {"punpcklqdq", "%xmm1,%xmm0", amd64WritesLast},
	} {
		if got := amd64EffectOf(inst{op: c.op, args: c.args}); got != c.want {
			t.Errorf("%s %s: %v, want %v", c.op, c.args, got, c.want)
		}
	}
}

// TestAmd64RegisterOf names the register an operand names, whole or a
// part, and none for memory.
func TestAmd64RegisterOf(t *testing.T) {
	for operand, want := range map[string]string{"%r15": "r15", "%r15d": "r15", "%eax": "rax", "%dil": "rdi", "(%rax)": "", "$0x1": ""} {
		if got, _ := amd64RegisterOf(operand); got != want {
			t.Errorf("%s: %q, want %q", operand, got, want)
		}
	}
}
