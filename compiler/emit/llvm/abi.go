// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"errors"
	"runtime"
)

// A call into the C runtime passes a value of three words, a string or
// a slice, as the processor's C calling convention says, which the IR
// does not abstract away: clang lowers such a struct parameter to a
// pointer to a copy the caller makes, marked byval on x86-64, where the
// callee finds the copy in the argument area, and plain on arm64,
// where the callee finds the pointer in a register; a struct result
// comes back through a pointer the caller passes first, sret, on both.
// The emitter declares every runtime function it calls in that form,
// and the calls agree with the declarations. The program's own
// functions pass their values as IR aggregates, which both sides of
// such a call read the same way since both are IR. The runtime has no
// other aggregate at its boundary: teak.h passes a map, a channel and
// a lock by pointer or by number.
type abi struct {
	byval bool
}

// targetABI returns the convention of the processor named as Go names
// it, amd64 or arm64, or the host's for an empty name.
func targetABI(arch string) (abi, error) {
	switch hostArch(arch) {
	case "amd64":
		return abi{byval: true}, nil
	case "arm64":
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
// lays the module out for them whatever machine it runs on. The data
// layout is left to clang, which supplies the target's own.
func triple(arch, os string) string {
	if os == "" {
		os = runtime.GOOS
	}

	cpu := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[hostArch(arch)]
	if os == "darwin" {
		if cpu == "aarch64" {
			cpu = "arm64"
		}

		return cpu + "-apple-macosx"
	}

	return cpu + "-unknown-linux-gnu"
}

// result writes the parameter through which a three-word struct of the
// IR type t comes back from the runtime: a pointer to the caller's
// slot, passed first.
func (a abi) result(t string) string {
	return "ptr sret(" + t + ") align 8"
}

// small writes the attribute a scalar narrower than a register
// carries into or out of the runtime: the caller extends it to the
// register with zeros, which the Apple and x86-64 conventions require
// of the caller and the others accept. The runtime's narrow scalars
// are its bool and uint8, both unsigned. A wider scalar needs nothing.
func (a abi) small(t string) string {
	switch t {
	case "i1", "i8", "i16":
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
