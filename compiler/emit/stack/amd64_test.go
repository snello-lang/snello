// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stack

import (
	"strings"
	"testing"
)

// decodeAmd64 reads one listing of objdump's on x86-64.
func decodeAmd64(t *testing.T, listing string) *Image {
	t.Helper()
	img := &Image{arch: amd64, os: linux, functions: map[string]*function{}, cLibrary: map[string]bool{"malloc": true, "free": true}}
	img.read([]byte(listing), [2]uint64{})
	return img
}

func TestAmd64Frame(t *testing.T) {
	img := decodeAmd64(t, `0000000000401126 <run>:
  401126:	push   %rbp
  401127:	mov    %rsp,%rbp
  40112a:	push   %rbx
  40112b:	sub    $0x28,%rsp
  40112f:	and    $0xfffffffffffffff0,%rsp
  401133:	lea    -0x18(%rsp),%rsp
  401138:	add    $0x28,%rsp
  40113c:	lea    -0x8(%rbp),%rsp
  401140:	pop    %rbx
  401141:	mov    %rbp,%rsp
  401144:	leave
  401145:	ret
`)
	if f := img.functions["run"]; f.frame != 8+8+0x28+15+0x18 || f.unknown != "" || f.setsSP {
		t.Fatalf("got %+v", f)
	}
}

func TestAmd64Calls(t *testing.T) {
	img := decodeAmd64(t, `0000000000401126 <run>:
  401126:	callq  401030 <malloc@plt>
  40112b:	call   401200 <leaf>
  401130:	call   *%rax
  401132:	call   *0x2eb0(%rip)        # 403ff0 <free@GLIBC_2.2.5>
  401138:	je     401140 <run+0x1a>
  40113a:	jmp    401200 <leaf>
  401140:	jmpq   *0x2eb2(%rip)        # 403ff8 <malloc@GLIBC_2.2.5>

0000000000401200 <leaf>:
  401200:	ret
`)
	if got := kinds(img.functions["run"]); got != "lib:malloc leaf * lib:free leaf lib:malloc" {
		t.Fatalf("got %q", got)
	}
}

func TestAmd64Unknown(t *testing.T) {
	for _, c := range []struct {
		name, body, want string
		setsSP           bool
	}{
		{"dynamic frame", "  401126:\tsub    %rax,%rsp\n", "depends on a value", false},
		{"jump table", "  401126:\tnotrack jmp *%rax\n", "jump through a register", false},
		{"sets sp", "  401126:\tmov    %rdi,%rsp\n", "", true},
		{"probe loop", "  401120:\tnop\n  401126:\tsub    $0x1000,%rsp\n  40112d:\torq    $0x0,(%rsp)\n  401132:\tcmp    %r11,%rsp\n  401135:\tjne    401126 <run+0x6>\n", "in a loop", false},
	} {
		img := decodeAmd64(t, "0000000000401120 <run>:\n"+c.body)
		f := img.functions["run"]
		if c.setsSP != f.setsSP || (c.want != "" && !strings.Contains(f.unknown, c.want)) {
			t.Errorf("%s: got %+v", c.name, f)
		}
	}
}

func TestAmd64Plain(t *testing.T) {
	for _, c := range []struct{ in, op, args string }{
		{"notrack jmp *%rax", "jmp", "*%rax"},
		{"bnd jmp 401000 <x>", "jmp", "401000 <x>"},
		{"callq 401000 <x>", "call", "401000 <x>"},
		{"pushq %rbp", "push", "%rbp"},
	} {
		parts := strings.SplitN(c.in, " ", 2)
		got := amd64Plain(inst{op: parts[0], args: parts[1]})
		if got.op != c.op || got.args != c.args {
			t.Errorf("%q: got %q %q", c.in, got.op, got.args)
		}
	}
}
