// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/teak-lang/teak/compiler/emit/c"
	"github.com/teak-lang/teak/runtime"
)

// Flags are the options clang is given for the module: the standard
// optimization pipeline, which is what the backend is for. The runtime
// beside it is compiled by the C compiler under the C backend's flags.
var Flags = []string{"-O2"}

// Compiler returns the compiler that turns the module into an object:
// clang, the one compiler that reads IR, named through CLANG when it
// is set.
func Compiler() string {
	if clang := os.Getenv("CLANG"); clang != "" {
		return clang
	}

	return "clang"
}

// Build writes the module and the runtime into dir, compiles the
// module to an object with clang, links it with the runtime compiled
// by the C compiler, and returns the path of the binary named name.
// The compilers' output comes back with any error. A debug build
// compiles the runtime's checks in, as the C backend's does.
func Build(dir, name string, module []byte, debug bool) (bin string, output []byte, err error) {
	files := runtime.Files()
	files[name+".ll"] = module
	for file, data := range files {
		if err := os.WriteFile(filepath.Join(dir, file), data, 0o644); err != nil {
			return "", nil, err
		}
	}

	obj := filepath.Join(dir, name+".o")
	args := append([]string{}, Flags...)
	if t := moduleTriple(module); t != "" {
		args = append(args, "-target", t) // the module's, so that clang does not override it with the host's
	}

	args = append(args, "-c", "-o", obj, filepath.Join(dir, name+".ll"))
	output, err = exec.Command(Compiler(), args...).CombinedOutput()
	if err != nil {
		return "", output, err
	}

	bin = filepath.Join(dir, name)
	args = append([]string{}, c.Flags...)
	if debug {
		args = append(args, "-DTK_DEBUG")
	}

	args = append(args, "-o", bin, obj)
	args = append(args, runtime.Paths(dir)...)
	args = append(args, "-lm")
	linked, err := exec.Command(c.Compiler(), args...).CombinedOutput()
	return bin, append(output, linked...), err
}

// moduleTriple reads the target triple the module names, or nothing.
func moduleTriple(module []byte) string {
	const key = "target triple = \""
	i := bytes.Index(module, []byte(key))
	if i < 0 {
		return ""
	}

	rest := module[i+len(key):]
	if j := bytes.IndexByte(rest, '"'); j >= 0 {
		return string(rest[:j])
	}

	return ""
}
