// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"errors"
	"runtime"

	"github.com/snello-lang/snello/compiler/platform"
)

// The narrow scalars each convention extends: every one, a bool alone,
// or none.
var (
	extendsNarrow = map[string]bool{"i1": true, "i8": true, "i16": true}
	extendsBool   = map[string]bool{"i1": true}
)

// A call into the C runtime passes a value of three words, a string or
// a slice, as the platform's C calling convention says, which the IR
// does not abstract away: clang lowers such a struct parameter to a
// pointer to a copy the caller makes, marked byval on x86-64 under
// System V, Linux's and macOS's, where the callee finds the copy in the
// argument area, and plain on arm64 and under Windows' x64 convention,
// which passes any struct of more than eight bytes so, where the callee
// finds the pointer in a register or the argument area; a struct result
// comes back through a pointer the caller passes first, sret, on all.
// The emitter declares every runtime function it calls in that form,
// and the calls agree with the declarations. The program's own
// functions pass their values as IR aggregates, which both sides of
// such a call read the same way since both are IR. The runtime has no
// other aggregate at its boundary: snello.h passes a map, a channel and
// a lock by pointer or by number.
//
// A scalar narrower than a register is extended to it with zeros by
// the caller under the conventions that require it, Apple's and System
// V's x86-64, and Windows' x64 for a bool alone; AAPCS64 on Linux and
// Windows leaves the register's upper bits unspecified, and a value
// declared extended there would be read as clean when the C side left
// it not.
type abi struct {
	byval    bool
	extended map[string]bool // the narrow IR types the convention extends: i1, i8, i16
}

// targetABI returns the convention of the processor named as Go names
// it, amd64 or arm64, under the system named, either the host's for an
// empty name.
func targetABI(arch, system string) (abi, error) {
	if system == "" {
		system = runtime.GOOS
	}

	switch hostArch(arch) {
	case platform.AMD64:
		if system == platform.Windows {
			return abi{byval: false, extended: extendsBool}, nil
		}

		return abi{byval: true, extended: extendsNarrow}, nil
	case platform.ARM64:
		if system == platform.Darwin {
			return abi{byval: false, extended: extendsNarrow}, nil
		}

		return abi{byval: false}, nil
	}

	return abi{}, errors.New("the LLVM backend does not know the calling convention of " + arch)
}

// hostArch returns arch, or the host's processor for an empty name.
func hostArch(arch string) string {
	if arch == "" {
		return runtime.GOARCH
	}

	return arch
}

// triple writes the target triple of the module, which names the
// processor and the system the module is compiled for, so that clang
// lays the module out for them whatever machine it runs on: the host's
// for an empty name. The data layout is left to clang, which supplies
// the target's own.
func triple(arch, os string) string {
	if os == "" {
		os = runtime.GOOS
	}

	return platform.Target{OS: os, Arch: hostArch(arch)}.Triple()
}

// result writes the parameter through which a three-word struct of the
// IR type t comes back from the runtime: a pointer to the caller's
// slot, passed first.
func (a abi) result(t string) string {
	return "ptr sret(" + t + ") align 8"
}

// small writes the attribute a scalar narrower than a register
// carries into or out of the runtime where the convention extends it to
// the register with zeros, as abi says; the runtime's narrow scalars are
// its bool and uint8, both unsigned. A wider scalar needs nothing.
func (a abi) small(t string) string {
	if a.extended[t] {
		return " zeroext"
	}

	return ""
}

// words writes the parameter through which a three-word struct of the
// IR type t is passed to the runtime, as a declaration and a call both
// write it.
func (a abi) words(t string) string {
	if a.byval {
		return "ptr byval(" + t + ") align 8"
	}

	return "ptr"
}
