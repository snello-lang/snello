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

	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/emit/stack"
)

// Flags are the options the C compiler is given. Every warning is an
// error: the emitted C must be as clean as the Go of the compiler.
// -fwrapv makes signed overflow wrap, as Snello's integers do; without
// it C leaves overflow undefined and the optimizer may assume it away.
// -fno-strict-aliasing lets the runtime work on every slice type
// through sn_slice, which has the same layout. A variable the program
// assigns and never reads is the checker's to judge, not the C
// compiler's, so that warning is off, and so is the one that asks for
// a brace around every level of {0}, which the emitter writes as the
// zero of any type whatever its shape. Arithmetic on constants that
// overflows is no mistake either, since the language has it wrap: the
// two warnings of it, gcc's and clang's, are off, each compiler passing
// over the other's name. -ffp-contract=off keeps a
// multiply and an add two operations, each rounded, as the language has
// them and the LLVM backend writes them: a compiler left to join them
// into one instruction rounds once, and the last digits of a*b + c
// would then differ between the backends and between processors.
// -fno-stack-protector leaves out the check a C compiler may add to a
// function with an array of its own, against a write past it, which
// some systems' compilers add by default: a Snello program checks every
// index, and the check's failure path, deeper than any function the
// runtime calls, would set the bound of nearly every task's stack.
// -fno-jump-tables writes a switch as comparisons, not as a table of
// addresses jumped through by a register, which the analysis of a
// task's stack cannot follow, so that a task reaching a switch keeps
// its bound: x86-64's compilers made tables of the switches over an
// error's number, and left every task that reads a socket unbounded.
// The optimization level is the build's: Release or Debug.
var Flags = []string{"-std=c11", "-Wall", "-Wextra", "-Werror", "-Wno-tautological-compare", "-Wno-unused-but-set-variable", "-Wno-missing-braces", "-Wno-overflow", "-Wno-integer-overflow", "-fwrapv", "-fno-strict-aliasing", "-ffp-contract=off", "-fno-stack-protector", "-fno-jump-tables", "-pthread"}

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

// Build writes the emitted program and the runtime's headers into dir,
// with the C side of every package that binds C, compiles them with
// the C compiler, links them with the runtime's objects, which the
// cache holds once they are compiled, and returns the path of the
// binary named name, with the bound of each task's stack the analysis
// of the linked binary proves written into it. The compiler's output
// comes back with any error.
// A debug build compiles the runtime's checks in: see
// runtime/c/debug.c.
func Build(dir, name string, src []byte, debug bool, bindings ...lower.Binding) (bin string, output []byte, err error) {
	prog := filepath.Join(dir, name+".snello.c") // never the name of a runtime file, whatever the program is called
	if err := os.WriteFile(prog, src, 0o644); err != nil {
		return "", nil, err
	}

	if err := WriteHeaders(dir); err != nil {
		return "", nil, err
	}

	bound, err := BindingSources(dir, bindings)
	if err != nil {
		return "", nil, err
	}

	args := append([]string{}, Flags...)
	if debug {
		args = append(args, Debug...)
	} else {
		args = append(args, Release...)
	}

	var rt []string
	var rtOutput []byte
	var rtErr error
	done := make(chan struct{})
	rtArgs := append([]string{}, args...) // the runtime's own: the program's gain -I below
	go func() {                           // the runtime beside the program
		rt, rtOutput, rtErr = RuntimeObjects(dir, Compiler(), rtArgs)
		close(done)
	}()

	args = append(args, "-I", dir) // snello.h, for the program's C and a package's
	objs, output, err := Objects(Compiler(), args, append([]string{prog}, bound...))
	<-done
	output = append(output, rtOutput...)
	if err != nil {
		return "", output, err
	}

	if rtErr != nil {
		return "", output, rtErr
	}

	bin = filepath.Join(dir, name)
	link := append(append([]string{}, args...), "-o", bin, objs[0]) // the program, then the runtime, then the packages' C
	link = append(append(link, rt...), objs[1:]...)
	link = append(append(link, LinkFlags(bindings)...), SystemLinkFlags()...)
	linked, err := exec.Command(Compiler(), link...).CombinedOutput()
	if err == nil {
		stack.Patch(bin) // a program it cannot bound keeps its tasks on guarded stacks, which is always safe
	}

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
