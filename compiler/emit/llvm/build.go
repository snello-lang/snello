// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/snello-lang/snello/compiler/emit/c"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/runtime"
)

// Flags are the options clang is given for the module: the standard
// optimization pipeline, which is what the backend is for. A release
// build adds link-time optimization: the module and the runtime are
// compiled by clang to IR and optimized together when linked, so the
// runtime's hot functions are inlined into the program. A debug build
// links the module with the runtime compiled by the C compiler under
// the C backend's flags.
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

// Build writes the module and the runtime into dir, with the C side of
// every package that binds C, compiles the module to an object with
// clang, links it with the runtime and that C compiled by the C
// compiler, and returns the path of the binary named name.
// The compilers' output comes back with any error. A debug build
// compiles the runtime's checks in, as the C backend's does.
func Build(dir, name string, module []byte, debug bool, bindings ...lower.Binding) (bin string, output []byte, err error) {
	files := runtime.Files()
	files[name+".ll"] = module
	for file, data := range files {
		if err := os.WriteFile(filepath.Join(dir, file), data, 0o644); err != nil {
			return "", nil, err
		}
	}

	bound, err := c.BindingSources(dir, bindings)
	if err != nil {
		return "", nil, err
	}

	obj := filepath.Join(dir, name+".ll.o") // never the object of a runtime file, whatever the program is called
	args := append([]string{}, Flags...)
	if t := moduleTriple(module); t != "" {
		args = append(args, "-target", t) // the module's, so that clang does not override it with the host's
	}

	if !debug {
		args = append(args, "-flto")
	}

	args = append(args, "-c", "-o", obj, filepath.Join(dir, name+".ll"))
	cc := c.Compiler()
	rtargs := append([]string{}, c.Flags...)
	if debug {
		rtargs = append(rtargs, c.Debug...)
	} else {
		cc = Compiler() // the module is clang's IR: clang compiles the runtime to IR beside it and links the two
		rtargs = append(rtargs, c.Release...)
	}

	var compiled []byte
	var moduleErr error
	done := make(chan struct{})
	go func() { // the module beside the runtime's files
		compiled, moduleErr = exec.Command(Compiler(), args...).CombinedOutput()
		close(done)
	}()

	rtargs = append(rtargs, "-I", dir) // snello.h, for a package's C
	objs, output, err := c.Objects(cc, rtargs, append(runtime.Paths(dir), bound...))
	<-done
	output = append(compiled, output...)
	if moduleErr != nil {
		return "", output, moduleErr
	}

	if err != nil {
		return "", output, err
	}

	bin = filepath.Join(dir, name)
	linked, err := exec.Command(cc, append(append(append(rtargs, "-o", bin, obj), objs...), "-lm")...).CombinedOutput()
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
