// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"

	"github.com/snello-lang/snello/runtime"
)

// Flags are the options the C compiler is given. Every warning is an
// error: the emitted C must be as clean as the Go of the compiler.
// -fwrapv makes signed overflow wrap, as Snello's integers do; without
// it C leaves overflow undefined and the optimizer may assume it away.
// -fno-strict-aliasing lets the runtime work on every slice type
// through sn_slice, which has the same layout. A variable the program
// assigns and never reads is the checker's to judge, not the C
// compiler's, so that warning is off. The optimization level is the
// build's: Release or Debug.
var Flags = []string{"-std=c11", "-Wall", "-Wextra", "-Werror", "-Wno-tautological-compare", "-Wno-unused-but-set-variable", "-fwrapv", "-fno-strict-aliasing", "-pthread"}

// Release are the options of a release build: the standard
// optimizations, and link-time optimization over the program and the
// runtime together, so that a runtime function a program calls in a
// loop, a push or a hash, is inlined into it as a function of the
// program would be. Debug are a debug build's, which compiles the
// runtime's checks in and is built for speed of building.
var (
	Release = []string{"-O2", "-flto"}
	Debug   = []string{"-O1", "-DSN_DEBUG"}
)

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
// compiles the runtime's checks in: see runtime/c/debug.c.
func Build(dir, name string, src []byte, debug bool) (bin string, output []byte, err error) {
	files := runtime.Files()
	files[name+".snello.c"] = src // never the name of a runtime file, whatever the program is called
	for file, data := range files {
		if err := os.WriteFile(filepath.Join(dir, file), data, 0o644); err != nil {
			return "", nil, err
		}
	}

	args := append([]string{}, Flags...)
	if debug {
		args = append(args, Debug...)
	} else {
		args = append(args, Release...)
	}

	objs, output, err := Objects(Compiler(), args, append([]string{filepath.Join(dir, name+".snello.c")}, runtime.Paths(dir)...))
	if err != nil {
		return "", output, err
	}

	bin = filepath.Join(dir, name)
	linked, err := exec.Command(Compiler(), append(append(append(args, "-o", bin), objs...), "-lm")...).CombinedOutput()
	return bin, append(output, linked...), err
}

// Objects compiles each of the C files srcs to an object beside it with
// cc and args, as many at once as there are processors, and returns
// the objects in the order of srcs, with the compilers' output in that
// order too. A build's time is then its longest file's, not the sum of
// the runtime's files; under -flto an object is the file's IR, and the
// link optimizes the program and the runtime together as one.
func Objects(cc string, args, srcs []string) (objs []string, output []byte, err error) {
	objs = make([]string, len(srcs))
	outs := make([][]byte, len(srcs))
	errs := make([]error, len(srcs))
	slots := make(chan struct{}, goruntime.NumCPU())
	var wg sync.WaitGroup
	for i, src := range srcs {
		objs[i] = strings.TrimSuffix(src, filepath.Ext(src)) + ".o"
		wg.Add(1)
		go func(i int, src string) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			outs[i], errs[i] = exec.Command(cc, append(append([]string{}, args...), "-c", "-o", objs[i], src)...).CombinedOutput()
		}(i, src)
	}

	wg.Wait()
	for i := range srcs {
		output = append(output, outs[i]...)
		if err == nil {
			err = errs[i]
		}
	}

	return objs, output, err
}
