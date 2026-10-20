// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"os"
	"os/exec"
	"path/filepath"
)

// Flags are the options the C compiler is given. Every warning is an
// error: the emitted C must be as clean as the Go of the compiler.
// -fwrapv makes signed overflow wrap, as Teak's integers do; without
// it C leaves overflow undefined and the optimizer may assume it away.
// -fno-strict-aliasing lets the runtime work on every slice type
// through tk_slice, which has the same layout.
var Flags = []string{"-std=c11", "-Wall", "-Wextra", "-Werror", "-Wno-tautological-compare", "-fwrapv", "-fno-strict-aliasing", "-pthread", "-O1"}

// Compiler returns the C compiler to run: the CC environment variable
// when it is set, as every build tool reads it, else cc.
func Compiler() string {
	if cc := os.Getenv("CC"); cc != "" {
		return cc
	}

	return "cc"
}

// Build writes the emitted program and the runtime into dir, compiles
// them with the C compiler, and returns the path of the binary named
// name. The compiler's output comes back with any error. A debug build
// compiles the runtime's checks in: see runtime/debug.c.
func Build(dir, name string, src []byte, debug bool) (bin string, output []byte, err error) {
	files := Runtime()
	files[name+".c"] = src
	for file, data := range files {
		if err := os.WriteFile(filepath.Join(dir, file), data, 0o644); err != nil {
			return "", nil, err
		}
	}

	bin = filepath.Join(dir, name)
	args := append([]string{}, Flags...)
	if debug {
		args = append(args, "-DTK_DEBUG")
	}

	args = append(args, "-o", bin, filepath.Join(dir, name+".c"), filepath.Join(dir, "teak.c"), filepath.Join(dir, "debug.c"), "-lm")
	cmd := exec.Command(Compiler(), args...)
	output, err = cmd.CombinedOutput()
	return bin, output, err
}
