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
var Flags = []string{"-std=c11", "-Wall", "-Wextra", "-Werror", "-fwrapv", "-O1"}

// Build writes the emitted program and the runtime into dir, compiles
// them with the system C compiler, and returns the path of the binary
// named name. The compiler's output comes back with any error.
func Build(dir, name string, src []byte) (bin string, output []byte, err error) {
	header, source := Runtime()
	files := map[string][]byte{"teak.h": header, "teak.c": source, name + ".c": src}
	for file, data := range files {
		if err := os.WriteFile(filepath.Join(dir, file), data, 0o644); err != nil {
			return "", nil, err
		}
	}

	bin = filepath.Join(dir, name)
	args := append(append([]string{}, Flags...), "-o", bin, filepath.Join(dir, name+".c"), filepath.Join(dir, "teak.c"), "-lm")
	cmd := exec.Command("cc", args...)
	output, err = cmd.CombinedOutput()
	return bin, output, err
}
