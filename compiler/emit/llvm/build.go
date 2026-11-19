// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/snello-lang/snello/compiler/emit/c"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/compiler/toolchain"
)

// Flags are the options clang is given for the module: the standard
// optimization pipeline, which is what the backend is for. A release
// build adds link-time optimization: the module and the runtime are
// compiled by clang to IR and optimized together when linked, so the
// runtime's hot functions are inlined into the program. A debug build
// links the module with the runtime compiled by the C compiler under
// the C backend's flags; the program's own functions in it are marked
// to be left unoptimized (optnone).
//
// DebugFlags keep a debug build's trace of a panic exact through the
// standard library's code, which is optimized around the program's:
// the merging of the tails and of the common code of two paths, which
// would fold two panics into one call of no line, is off; so is the
// outlining of code shared by several places into a function of its
// own, which a trace could show at one of them only, and the analysis
// that finds a function's cold code, a panic's path first of all, to
// split it off into a function of its own, which a trace would show as
// the function twice; and a call a function ends in stays a call and
// not a jump, so that the function keeps its frame in the trace.
var (
	Flags      = []string{"-O2"}
	DebugFlags = []string{"-mllvm", "-enable-tail-merge=false", "-mllvm", "-simplifycfg-sink-common=false", "-mllvm", "-simplifycfg-hoist-common=false", "-mno-outline", "-mllvm", "-hot-cold-static-analysis=false", "-fno-optimize-sibling-calls"}
)

// Compiler returns the compiler that turns the module into an object:
// clang, the one compiler that reads IR, named through CLANG when it
// is set.
func Compiler() string {
	return toolchain.Clang()
}

// Build writes the module and the runtime into dir, with the C side of
// every package that binds C, compiles the module to an object with
// clang, links it with the runtime and that C compiled by the C
// compiler, and returns the path of the binary named name, name.exe
// for Windows, with the bound of each task's stack the analysis of the linked binary proves
// written into it. The compilers' output comes back with any error. A
// debug build compiles the runtime's checks in, as the C backend's
// does. The module is emitted for the target t, which the runtime and
// the link are then built for.
func Build(dir, name string, module []byte, t platform.Target, debug bool, bindings ...lower.Binding) (bin string, output []byte, err error) {
	if err := requireCompilers(debug); err != nil {
		return "", nil, err
	}

	if triple := moduleTriple(module); triple != "" && triple != t.Triple() {
		return "", nil, errors.New("the module is emitted for " + triple + ", and the build is for " + t.String() + ", " + t.Triple())
	}

	module, err = Targeted(module) // every function compiled for the runtime's processor, so that the two are optimized as one
	if err != nil {
		return "", nil, err
	}

	if err := os.WriteFile(filepath.Join(dir, name+".ll"), module, 0o644); err != nil {
		return "", nil, err
	}

	if err := c.WriteHeaders(dir); err != nil { // snello.h, for a package's C; the runtime's sources are compiled once and kept
		return "", nil, err
	}

	bound, err := c.BindingSources(dir, bindings)
	if err != nil {
		return "", nil, err
	}

	obj := filepath.Join(dir, name+".ll.o") // never the object of a runtime file, whatever the program is called
	args := append([]string{}, Flags...)

	if triple := moduleTriple(module); triple != "" {
		args = append(args, "-target", triple) // the module's, so that clang does not override it with the host's
	}

	if debug {
		args = append(args, DebugFlags...)
	} else {
		args = append(args, "-flto")
	}

	args = append(args, "-c", "-o", obj, filepath.Join(dir, name+".ll"))
	cc := c.Compiler()
	if !debug {
		cc = Compiler() // the module is clang's IR: clang compiles the runtime to IR beside it and links the two
	}

	if err := c.CheckTarget(cc, t); err != nil {
		return "", nil, err
	}

	rtargs := c.BuildFlags(cc, t, debug)

	var compiled []byte
	var moduleErr error
	done := make(chan struct{})
	go func() { // the module beside the runtime's files
		compiled, moduleErr = exec.Command(Compiler(), args...).CombinedOutput()
		close(done)
	}()

	rt, output, err := c.RuntimeObjects(dir, cc, rtargs)
	var objs []string
	if err == nil {
		var boundOutput []byte
		pkgArgs := rtargs // a release build's package C as the runtime is compiled, with no line tables
		if debug {
			pkgArgs = c.ProgramFlags(cc, t, debug) // a debug build's as the program's, for the debugger
		}

		objs, boundOutput, err = c.Objects(cc, append(append([]string{}, pkgArgs...), "-I", dir), bound) // with snello.h
		output = append(output, boundOutput...)
	}

	<-done
	output = append(compiled, output...)
	if moduleErr != nil {
		return "", output, moduleErr
	}

	if err != nil {
		return "", output, err
	}

	bin = filepath.Join(dir, t.Executable(name))
	linked, err := c.Link(cc, t, debug, dir, bin, append(append([]string{obj}, rt...), objs...), bindings)
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

// requireCompilers returns the error a build reports for a compiler it
// runs that cannot be found or does not run, which says how to install
// one: clang, and for a debug build the C compiler, which compiles the
// runtime. Each is asked its version once, which the build asks anyway.
func requireCompilers(debug bool) error {
	if _, err := c.CompilerID(Compiler()); err != nil {
		return err
	}

	if debug {
		_, err := c.CompilerID(c.Compiler())
		return err
	}

	return nil
}
