// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package stack bounds the stack a task can use, from the linked
// program itself: every function's frame and every call it makes, as
// the system's disassembler reads them out of the binary after the C
// compiler and the linker have optimized it, so that no inlining or
// link-time change escapes the count. A task whose deepest path through
// the calls is known gets a stack of that size from the runtime, packed
// with others and touched page by page as they are used, where every
// other task keeps a reservation of its own behind a guard page.
//
// The bound must never be below what the task uses, since a small stack
// has no guard: whatever the analysis does not understand makes the
// task unbounded rather than smaller. A recursion, a call through a
// pointer whose targets are not known, a jump through a register, a
// frame whose size depends on a value, a write to the stack pointer of
// a kind not recognized, a call into a shared library other than the
// system's C library or to one of its functions whose stack was not
// measured: each is a reason, and the task keeps a large stack. The C
// library's functions are counted at what each was measured to use on
// every system the runtime supports, and a margin more.
package stack

import (
	"fmt"
	"sort"
	"strings"
)

const (
	// cLibraryMargin is how much more than its measured figure a call
	// into the C library is counted at, in percent: room for an update
	// of a system's C library that deepens a function, which the test
	// that measures every function again on each system catches.
	cLibraryMargin = 50

	// stackAlign is what every frame's size is a multiple of, on both
	// processors, and what a C library function's allowance is rounded
	// up to.
	stackAlign = 16

	// threadLocalKey is the entry of cLibraryStacks for the system's
	// accessor of thread-local variables, which macOS calls through a
	// pointer rather than by a name.
	threadLocalKey = "thread_local"

	// redZone is the space below the stack pointer a function may use
	// without moving it, which the ABI of macOS on arm64 and of every
	// system on x86-64 allows a leaf: counted once, at the deepest
	// point of every path.
	redZone = 128

	// returnAddress is what a call pushes on x86-64; on arm64 the
	// return address is in a register, saved in the callee's frame.
	returnAddress = 8

	// symbolPrefix is what macOS puts before the name of every C
	// symbol: the function sn_spawn is _sn_spawn in a Mach-O binary.
	symbolPrefix = "_"

	// cloneMark starts what a C compiler adds to the name of a copy it
	// makes of a function, run.isra.0 or run.constprop.0, or the LTO of
	// LLVM to one it promotes, run.llvm.1234: a copy does what the
	// function it was made of does.
	cloneMark = "."
)

// The processors and the systems the analysis reads binaries of.
const (
	arm64 arch = "arm64"
	amd64 arch = "amd64"

	darwin system = "darwin"
	linux  system = "linux"
)

// The kinds of call.
const (
	direct      callKind = iota // to a function of the binary, named
	library                     // to a function of a shared library, through its stub
	pointer                     // through a register or memory
	threadLocal                 // the system's accessor of thread-local variables, which macOS calls through a pointer
)

// cLibraryStacks is the stack each function of the system's C library
// that the runtime calls was measured to use, in bytes below its
// caller's stack pointer: the most of macOS on arm64 and of Linux on
// arm64 and x86-64, built with gcc and with clang, as testdata/libc.c
// measures each on a stack filled with a pattern, down its heavier
// paths. getaddrinfo is not here, since it runs on threads of its own
// and loads other libraries, nor pthread_once, which calls a function
// of its caller's.
var cLibraryStacks = map[string]int64{
	"__errno_location":            24,
	"__error":                     16,
	"__memcpy_chk":                24,
	"__sched_cpucount":            152,
	"__stack_chk_fail":            2760,
	"abort":                       312,
	"accept":                      120,
	"acos":                        56,
	"aligned_alloc":               784,
	"asin":                        56,
	"atan":                        48,
	"atan2":                       80,
	"bind":                        24,
	"bzero":                       0,
	"calloc":                      656,
	"cbrt":                        96,
	"chdir":                       24,
	"chmod":                       64,
	"clock_gettime":               384,
	"clock_gettime_nsec_np":       240,
	"close":                       120,
	"closedir":                    344,
	"connect":                     120,
	"cos":                         56,
	"cosh":                        80,
	"dirfd":                       32,
	"epoll_create1":               24,
	"epoll_ctl":                   40,
	"epoll_wait":                  136,
	"exit":                        624,
	"exp":                         40,
	"fclose":                      472,
	"fcntl":                       136,
	"fdopendir":                   2576,
	"fflush":                      312,
	"fmod":                        40,
	"fopen":                       536,
	"fputc":                       112,
	"fputs":                       928,
	"free":                        400,
	"freeaddrinfo":                336,
	"fscanf":                      2752,
	"fstat":                       168,
	"fstatat":                     168,
	"fsync":                       120,
	"fwrite":                      224,
	"gai_strerror":                560,
	"getcwd":                      1720,
	"getentropy":                  24,
	"gethostname":                 472,
	"getpeername":                 168,
	"getpid":                      24,
	"getrlimit":                   40,
	"getsockname":                 40,
	"getsockopt":                  40,
	"hypot":                       24,
	"if_indextoname":              544,
	"if_nametoindex":              912,
	"inet_ntop":                   2128,
	"inet_pton":                   200,
	"kevent":                      96,
	"kqueue":                      16,
	"listen":                      24,
	"log":                         32,
	"log10":                       56,
	"log2":                        32,
	"lseek":                       24,
	"lstat":                       168,
	"malloc":                      584,
	"memchr":                      32,
	"memcmp":                      32,
	"memcpy":                      24,
	"memmove":                     24,
	"memset":                      24,
	"mkdir":                       24,
	"mmap":                        64,
	"mprotect":                    24,
	"munmap":                      32,
	"nanosleep":                   176,
	"open":                        200,
	"openat":                      200,
	"os_unfair_lock_lock":         20,
	"os_unfair_lock_unlock":       20,
	"posix_memalign":              832,
	"pow":                         56,
	"prctl":                       56,
	"pthread_attr_destroy":        48,
	"pthread_attr_init":           48,
	"pthread_attr_setdetachstate": 24,
	"pthread_cond_broadcast":      128,
	"pthread_cond_init":           72,
	"pthread_cond_signal":         160,
	"pthread_cond_timedwait":      336,
	"pthread_cond_wait":           288,
	"pthread_condattr_destroy":    48,
	"pthread_condattr_init":       40,
	"pthread_condattr_setclock":   48,
	"pthread_create":              656,
	"pthread_mutex_destroy":       72,
	"pthread_mutex_init":          120,
	"pthread_mutex_lock":          112,
	"pthread_mutex_unlock":        80,
	"read":                        120,
	"readdir":                     144,
	"realloc":                     976,
	"rename":                      24,
	"rewinddir":                   64,
	"rmdir":                       24,
	"sched_getaffinity":           176,
	"sched_yield":                 24,
	"send":                        120,
	"setrlimit":                   48,
	"setsockopt":                  40,
	"siglongjmp":                  112,
	"sigsetjmp":                   88,
	"sin":                         56,
	"sinh":                        80,
	"snprintf":                    3312,
	"socket":                      24,
	"sqrt":                        24,
	"stat":                        168,
	"strchr":                      24,
	"strerror":                    656,
	"strlen":                      24,
	"strtoul":                     112,
	"sysconf":                     1392,
	"sysctlbyname":                176,
	"tan":                         72,
	"tanh":                        48,
	"thread_local":                768,
	"unlink":                      32,
	"unlinkat":                    24,
	"write":                       120,
}

// cLibraryAliases are the names a system's headers give a function of
// cLibraryStacks, which a binary imports it by.
var cLibraryAliases = map[string]string{
	"__isoc23_fscanf":  "fscanf",
	"__isoc23_strtoul": "strtoul",
	"__isoc99_fscanf":  "fscanf",
	"__isoc99_strtoul": "strtoul",
	"__sigsetjmp":      "sigsetjmp",
	"bcmp":             "memcmp",
}

// A callKind says how a function calls another.
type callKind int

// An arch is a processor's instruction set.
type arch string

// A system is an operating system, which decides the binary's format
// and the disassembler that reads it.
type system string

// A Bound is the result for one entry function: the bytes of stack its
// deepest path can use, or the reason it has none.
type Bound struct {
	Bytes  int64
	Reason string   // empty when the bound is known
	Path   []string // the deepest path, from the base, when the bound is known
}

// Known reports whether the entry has a bound.
func (b Bound) Known() bool {
	return b.Reason == ""
}

// Options say what the analysis cannot read from the code.
type Options struct {
	// Targets names, for a function that calls through a pointer, the
	// functions the pointer can hold: the drops the compiler wrote for
	// the elements of channels, which the runtime's channel calls.
	Targets map[string][]string

	// Switches are functions that move to another stack, counted at
	// the frame they push on this one: the switch to another task, and
	// the call of a function on a stack of its own, as a panic runs.
	// What they run after they set the stack pointer is not counted.
	Switches []string
}

// A call is one call a function makes.
type call struct {
	kind   callKind
	target string // the function called, for a direct call or a library's function
}

// A function is what the analysis reads of one function of the binary.
type function struct {
	name    string
	frame   int64  // the bytes its instructions move the stack pointer down by, summed
	calls   []call // the calls it makes, a tail call among them
	unknown string // what it does to the stack that the analysis does not understand, empty for nothing
	setsSP  bool   // it sets the stack pointer to a value of a register, as a switch to another stack does
}

// An Image is the functions of one linked binary, by name, and the
// shared library each imported function comes from.
type Image struct {
	arch      arch
	os        system
	functions map[string]*function
	cLibrary  map[string]bool // imported functions that are the system's C library's
	lazy      bool            // imports are bound at their first calls, by the dynamic linker's resolver on the caller's stack
}

// Bound returns the bound of the stack of a task that runs entry, the
// function the compiler wrote for a spawn: the deepest path from base,
// the runtime's function a task's stack begins in, whose call through
// a pointer is the call of entry, below top, the bytes between the top
// of the stack and the stack pointer base begins with, which the
// runtime's first context holds.
func (img *Image) Bound(entry, base string, top int64, opts Options) Bound {
	b := img.function(base)
	if b == nil {
		return Bound{Reason: "no function " + base}
	}

	targets := make(map[string][]string, len(opts.Targets)+1)
	for f, ts := range opts.Targets {
		targets[f] = ts
	}

	targets[img.plain(b.name)] = []string{entry}
	opts.Targets = targets
	w := walker{img: img, opts: opts, memo: map[string]result{}, active: map[string]bool{}}
	r := w.depth(base)
	if r.reason != "" {
		return Bound{Reason: r.reason}
	}

	return Bound{Bytes: top + r.bytes + redZone, Path: r.path}
}

// function returns the named function, by its name in the binary or,
// in one of macOS, by its name in C, without the leading underscore.
func (img *Image) function(name string) *function {
	if f := img.functions[name]; f != nil || img.os != darwin {
		return f
	}

	return img.functions[symbolPrefix+name]
}

type result struct {
	bytes  int64
	reason string
	path   []string // the functions of the deepest path, the first the one walked
}

// walker finds the deepest path from a function, each function's result
// kept once found, and the functions on the path being walked kept to
// tell a recursion.
type walker struct {
	img    *Image
	opts   Options
	memo   map[string]result
	active map[string]bool
}

func (w *walker) depth(name string) result {
	f := w.img.function(name)
	if f == nil {
		return result{reason: "a call of " + name + ", which is not in the binary"}
	}

	if r, ok := w.memo[f.name]; ok {
		return r
	}

	if w.active[f.name] {
		return result{reason: "a recursion through " + w.img.plain(f.name)}
	}

	if w.isSwitch(f.name) {
		return result{bytes: f.frame, path: []string{w.img.plain(f.name)}} // what it runs, it runs on another stack
	}

	if f.unknown != "" {
		return result{reason: f.unknown}
	}

	if f.setsSP {
		return result{reason: w.img.plain(f.name) + " sets the stack pointer"}
	}

	w.active[f.name] = true
	r := w.calls(f)
	delete(w.active, f.name)
	if r.reason == "" {
		r.bytes += f.frame
		r.path = append([]string{w.img.plain(f.name)}, r.path...)
	}

	w.memo[f.name] = r
	return r
}

// calls returns the deepest of f's calls, without f's own frame.
func (w *walker) calls(f *function) result {
	var deepest result
	for _, c := range f.calls {
		var r result
		switch c.kind {
		case direct:
			r = w.depth(c.target)
		case library:
			if !w.img.cLibrary[c.target] {
				return result{reason: "a call of " + w.img.plain(c.target) + ", from a shared library other than the C library"}
			}

			if w.img.lazy {
				return result{reason: "a call of " + w.img.plain(c.target) + ", which the binary binds at its first call, on the task's stack"}
			}

			n, ok := cLibraryStack(w.img.plain(c.target))
			if !ok {
				return result{reason: "a call of " + w.img.plain(c.target) + ", a function of the C library whose stack is not measured"}
			}

			r = result{bytes: n, path: []string{w.img.plain(c.target)}}
		case threadLocal:
			n, _ := cLibraryStack(threadLocalKey)
			r = result{bytes: n, path: []string{threadLocalKey}}
		case pointer:
			targets, ok := w.opts.Targets[w.img.origin(f.name)]
			if !ok {
				return result{reason: "a call through a pointer in " + w.img.plain(f.name)}
			}

			for _, t := range targets {
				tr := w.depth(t)
				if tr.reason != "" {
					return tr
				}

				if tr.bytes >= r.bytes {
					r = tr
				}
			}
		}

		if r.reason != "" {
			return r
		}

		if w.img.arch == amd64 {
			r.bytes += returnAddress
		}

		if r.bytes > deepest.bytes || deepest.path == nil {
			deepest = r
		}
	}

	return deepest
}

// cLibraryStack returns what a call of the C library's function name,
// its name in C, is counted at: its measured figure and the margin,
// aligned.
func cLibraryStack(name string) (int64, bool) {
	if a, ok := cLibraryAliases[name]; ok {
		name = a
	}

	n, ok := cLibraryStacks[name]
	if !ok {
		return 0, false
	}

	n += n * cLibraryMargin / 100
	return (n + stackAlign - 1) &^ (stackAlign - 1), true
}

func (w *walker) isSwitch(name string) bool {
	for _, s := range w.opts.Switches {
		if w.img.origin(name) == s {
			return true
		}
	}

	return false
}

// plain is a symbol's name in C: without the underscore macOS adds to
// every name, and as it is on any other system, whose names may start
// with one of their own.
func (img *Image) plain(name string) string {
	if img.os != darwin {
		return name
	}

	return strings.TrimPrefix(name, symbolPrefix)
}

// origin is the name in C of the function name is, or of the function
// a compiler's copy named name was made of: run for run.isra.0.
func (img *Image) origin(name string) string {
	n, _, _ := strings.Cut(img.plain(name), cloneMark)
	return n
}

// Functions lists the names of the image's functions, sorted, for a
// report or a test.
func (img *Image) Functions() []string {
	var out []string
	for n := range img.functions {
		out = append(out, img.plain(n))
	}

	sort.Strings(out)
	return out
}

// String reports the image as a table of every function, its frame and
// its calls, for a reader of a bound that came out unknown.
func (img *Image) String() string {
	var b strings.Builder
	for _, n := range img.Functions() {
		f := img.function(n)
		fmt.Fprintf(&b, "%s %d", n, f.frame)
		if f.unknown != "" {
			fmt.Fprintf(&b, " (%s)", f.unknown)
		}

		if f.setsSP {
			b.WriteString(" (sets sp)")
		}

		for _, c := range f.calls {
			switch c.kind {
			case direct:
				fmt.Fprintf(&b, " %s", img.plain(c.target))
			case library:
				fmt.Fprintf(&b, " lib:%s", img.plain(c.target))
			case pointer:
				b.WriteString(" *")
			case threadLocal:
				b.WriteString(" tlv")
			}
		}

		b.WriteString("\n")
	}

	return b.String()
}
