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
	"github.com/snello-lang/snello/compiler/emit/trace"
	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/compiler/toolchain"
	"github.com/snello-lang/snello/runtime"
)

const (
	// lineTables writes line tables and nothing more, which gcc and
	// clang both read, so that the trace of a panic has each frame's file
	// and line from the object the program is linked from: a release
	// build's program is compiled with it, and so links the build, which
	// writes the tables of what it optimizes; the runtime's files are
	// not, since the trace shows no line of the runtime's, and the
	// optimizing link is the shorter for writing none. The tables are
	// data beside the code and shape no instruction.
	lineTables = "-g1"

	// lto asks for link-time optimization; stripDebug asks the final link
	// of a release build on Linux to leave the debugging sections out of
	// the binary, whose trace table has replaced them.
	lto        = "-flto"
	stripDebug = "-Wl,--strip-debug"

	// leafFramePointer frees the frame pointer in a function that calls
	// none, which a debug build's program keeps instead: gdb finds where a
	// function gcc compiled for x86-64 starts after its prologue by the
	// frame pointer's setup, and stops a leaf with none before its
	// parameters are in their slots.
	leafFramePointer = "-momit-leaf-frame-pointer"

	// dsymutil gathers the debugging information a macOS link leaves in
	// its objects into a bundle, named for the binary with bundleSuffix,
	// where the platform's debuggers and profilers look for it.
	dsymutil     = "dsymutil"
	bundleSuffix = ".dSYM"

	// outputFlag names the file a compiler, a linker or dsymutil writes.
	outputFlag = "-o"

	// keptObject, firstLink and tableObject name the files a link writes
	// into the build's directory: the object the link-time optimizer
	// wrote, the first link's binary and the trace's table, each with a
	// dot in its stem, which the name of no runtime file's object has,
	// since the runtime's objects share the directory when the cache is
	// off, and its trace.c is trace.o.
	keptObject  = "linked.lto.o"
	firstLink   = "linked.first"
	tableObject = "trace.table.o"
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
// -fno-omit-frame-pointer with -momit-leaf-frame-pointer keeps the
// frame pointer in every function that calls another, which the trace
// of a panic walks, and frees it in a leaf, which is never below
// another frame.
var Flags = []string{"-std=c11", "-Wall", "-Wextra", "-Werror", "-Wno-tautological-compare", "-Wno-unused-but-set-variable", "-Wno-missing-braces", "-Wno-overflow", "-Wno-integer-overflow", "-fwrapv", "-fno-strict-aliasing", "-ffp-contract=off", "-fno-stack-protector", "-fno-jump-tables", "-pthread", "-fno-omit-frame-pointer", leafFramePointer}

// Release are the options of a release build: the standard
// optimizations, and link-time optimization over the program and the
// runtime together, so that a runtime function a program calls in a
// loop, a push or a hash, is inlined into it as a function of the
// program would be. Debug are a debug build's, which compiles the
// runtime's checks in and the runtime for speed of building; the
// program adds debugInfo.
var (
	Release = []string{"-O2", lto}
	Debug   = []string{"-O1", "-DSN_DEBUG"}
)

// debugInfo are the options a debug build compiles the program's C and
// a package's with: no optimization and the full debugging information,
// so that a debugger stops at every line, steps from one to the next,
// and finds every variable in its slot for its whole scope. They come
// after the build's own, since the last optimization level and the
// last -g given win. The whole of the C is unoptimized, the standard
// library's functions with the program's: clang marks a function it is
// told to leave alone, optnone, and then joins a multiply and an add
// into one instruction whatever -ffp-contract says, which the language
// forbids. The runtime is compiled with the build's alone, optimized
// and with none, so that a step goes over a call of it as over a call
// of the C library.
var debugInfo = []string{"-O0", "-g"}

// GCCFlags are gcc's own, beside Flags: every function in one text
// section, which gcc otherwise splits into one for main and one for
// cold code, so that the trace of a panic reads the program's code as
// one section.
var GCCFlags = []string{"-fno-reorder-functions", "-fno-reorder-blocks-and-partition"}

// Compiler returns the C compiler to run: the CC environment variable
// when it is set, as every build tool reads it, else cc.
func Compiler() string {
	return toolchain.CC()
}

// Build writes the emitted program and the runtime's headers into dir,
// with the C side of every package that binds C, compiles them with
// the C compiler for the target t, links them with the runtime's
// objects, which the cache holds once they are compiled, and returns
// the path of the binary named name, with the bound of each task's
// stack the analysis of the linked binary proves written into it. The
// compiler's output comes back with any error.
// A debug build compiles the runtime's checks in: see
// runtime/c/debug.c.
func Build(dir, name string, src []byte, t platform.Target, debug bool, bindings ...lower.Binding) (bin string, output []byte, err error) {
	if _, err := CompilerID(Compiler()); err != nil { // a compiler that cannot be found or does not run, said as the doctor says it
		return "", nil, err
	}

	if err := CheckTarget(Compiler(), t); err != nil {
		return "", nil, err
	}

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

	rtArgs := BuildFlags(Compiler(), t, debug)
	var rt []string
	var rtOutput []byte
	var rtErr error
	done := make(chan struct{})
	go func() { // the runtime beside the program
		rt, rtOutput, rtErr = RuntimeObjects(dir, Compiler(), rtArgs)
		close(done)
	}()

	args := append(ProgramFlags(Compiler(), t, debug), "-I", dir) // the program's own, and snello.h for its C and a package's
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
	linked, err := Link(Compiler(), t, debug, dir, bin, append(append([]string{objs[0]}, rt...), objs[1:]...), bindings) // the program, then the runtime, then the packages' C
	return bin, append(output, linked...), err
}

// Bundle gathers the debugging information of the debug build bin, for
// the target t, into the bundle beside out, the binary the build was
// copied to, for macOS,
// where the linker leaves it in the objects of the build's directory,
// which the build removes: the bundle is made before, as clang's
// driver makes one when a run compiles and links, and replaces one an
// earlier build left. On Linux the information is in the binary and
// nothing is done. The tool's output comes back with any error.
func Bundle(bin, out string, t platform.Target) ([]byte, error) {
	if t.OS != platform.Darwin {
		return nil, nil
	}

	bundle := out + bundleSuffix
	if err := os.RemoveAll(bundle); err != nil {
		return nil, err
	}

	return exec.Command(dsymutil, bin, outputFlag, bundle).CombinedOutput()
}

// ProgramFlags are the options the compiler cc is given to compile the
// program's own C and a package's for the target t: the build's, with
// the line tables of a release build or the debugging information of a
// debug one.
func ProgramFlags(cc string, t platform.Target, debug bool) []string {
	if debug {
		return append(without(BuildFlags(cc, t, debug), leafFramePointer), debugInfo...)
	}

	return append(BuildFlags(cc, t, debug), lineTables)
}

// BuildFlags are the options the compiler cc is given to compile the
// runtime's files of a build for the target t and to link it: Flags,
// the build's Release or Debug, gcc's own, which link-time
// optimization reads at the link, and the target's, TargetFlags.
func BuildFlags(cc string, t platform.Target, debug bool) []string {
	args := append([]string{}, Flags...)
	if debug {
		args = append(args, Debug...)
	} else {
		args = append(args, Release...)
	}

	if IsGCC(cc) {
		args = append(args, GCCFlags...)
	}

	return append(args, TargetFlags(cc, t)...)
}

// Link links the objects into the binary bin, in dir, with the trace
// of a panic's table written from them: the first link keeps the native
// object the program is linked from, the one the link-time optimizer
// wrote with its internalizing and inlining, through the linker on
// macOS and through the linker's plugin on Linux, which is asked for
// one text section; the runtime's own empty tables, native, answer that
// link, and the program's, linked after, replace them.
// With gcc, which keeps no such object, a partial link joins the
// objects, at the cost of what it cannot internalize; a debug build's
// objects are native already, each with its line tables, which a
// partial link would not keep on macOS. The table is read from the
// native objects, written as an object of its own, and linked with
// them and the runtime's native objects in a plain final link, which
// strips the debugging sections a release build has no more use for;
// then each anchor's address is written into the binary, and the
// bounds of the tasks' stacks with it, in one write. The compilers'
// output comes back with any error. The link is for the target t, with
// LLVM's linker for a Linux other than the host.
func Link(cc string, t platform.Target, debug bool, dir, bin string, objs []string, bindings []lower.Binding) ([]byte, error) {
	kept := filepath.Join(dir, keptObject)
	first := filepath.Join(dir, firstLink)
	args := append(append(BuildFlags(cc, t, debug), lineTables), linkerFlags(cc, t)...) // the link writes the line tables of what it optimizes
	plain := without(args, lto)                                                         // for the final link
	var output []byte
	var cmd []string
	var out []byte
	var err error
	native := objs                // what the final link links, and the table is read from: the objects, or the one the optimizer wrote
	whole := !debug && !IsGCC(cc) // the first link makes the binary, over the runtime's own empty tables
	switch {
	case debug:
	case !whole:
		cmd = append(append(append([]string{}, args...), "-r", outputFlag, kept, "-flinker-output=nolto-rel"), objs...)
		native = []string{kept}
	case t.OS == platform.Darwin:
		cmd = append(append(append([]string{}, args...), outputFlag, first), objs...)
		cmd = append(append(cmd, LinkFlags(bindings)...), SystemLinkFlags(t)...)
		cmd = append(cmd, "-Wl,-object_path_lto,"+kept)
		native = []string{kept}
	default:
		cmd = append(append(append([]string{}, args...), outputFlag, first), objs...)
		cmd = append(append(cmd, LinkFlags(bindings)...), SystemLinkFlags(t)...)
		cmd = append(cmd, "-Wl,-plugin-opt=obj-path="+kept, "-Wl,-plugin-opt=-function-sections=0", "-Wl,-plugin-opt=-data-sections=0")
		native = []string{kept}
	}

	if cmd != nil {
		out, err = exec.Command(cc, cmd...).CombinedOutput()
		output = append(output, out...)
		if err != nil {
			return output, err
		}
	}

	table, layout, err := trace.Table(native...)
	if err != nil {
		return output, err
	}

	object, err := trace.Object(table, t)
	if err != nil {
		return output, err
	}

	tablePath := filepath.Join(dir, tableObject)
	if err := os.WriteFile(tablePath, object, 0o644); err != nil {
		return output, err
	}

	cmd = append(append(append([]string{}, plain...), outputFlag, bin), native...)
	if whole {
		cmd = append(cmd, nativeRuntime(objs)...) // which the optimizer's object does not hold
	}

	cmd = append(append(append(cmd, tablePath), LinkFlags(bindings)...), SystemLinkFlags(t)...)
	if !debug && t.OS == platform.Linux {
		cmd = append(cmd, stripDebug)
	}

	out, err = exec.Command(cc, cmd...).CombinedOutput()
	output = append(output, out...)
	if err != nil {
		return output, err
	}

	patched, err := trace.Patch(bin, layout)
	if err != nil {
		return output, err
	}

	stack.Patch(bin, patched) // a program it cannot bound keeps its tasks on guarded stacks, which is always safe
	return output, nil
}

// nativeRuntime is the runtime's objects among objs compiled to native
// code whatever the build's optimization, which the optimizing link
// leaves out of the object it writes.
func nativeRuntime(objs []string) []string {
	var out []string
	for _, o := range objs {
		if runtime.Native[strings.TrimSuffix(filepath.Base(o), filepath.Ext(o))+".c"] {
			out = append(out, o)
		}
	}

	return out
}

// without is args with flag left out.
func without(args []string, flag string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a != flag {
			out = append(out, a)
		}
	}

	return out
}

// Objects compiles each of the C files srcs to an object beside it with
// cc and args, as many at once as there are processors, and returns
// the objects in the order of srcs, with the compilers' output in that
// order too. A build's time is then its longest file's, not the sum of
// the runtime's files; under -flto an object is the file's IR, and the
// link optimizes the program and the runtime together as one, but for
// a file the runtime names native, compiled to code whatever the
// arguments say.
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
			own := args
			if runtime.Native[filepath.Base(src)] {
				own = without(args, lto)
			}

			outs[i], errs[i] = exec.Command(cc, append(append([]string{}, own...), "-c", outputFlag, objs[i], src)...).CombinedOutput()
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
