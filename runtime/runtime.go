// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package runtime holds, under c/, the C runtime every Snello program links
// against, embedded in the compiler so that a build needs nothing but
// the compiler and a C compiler. snello.c is its core, the start and the
// end of a program, panics, and what more than one file uses, which
// internal.h declares; each part of the language and the library has a
// file of its own: slice.c and sort.c, Rust's driftsort, for slices,
// string.c for strings, format.c, quote.c and parse.c for numbers and
// quotes as text, map.c for maps, bufio.c, os.c, print.c, math.c and
// time.c for their packages, and chan.c and sync.c for channels and
// locks. alloc.c is the allocator, unicode.c the tables of case, white
// space and printable runes that mkunicode.go writes, pow10.c the
// powers of ten floats are formatted with, which mkpow10.go writes,
// sched.c the scheduler that runs the tasks and net.c the sockets over
// its poller. Both backends write these files next to the program they
// emit and compile them with it; debug.c is the checking build's
// tracker of every block.
package runtime

//go:generate go run mkunicode.go
//go:generate go run mkpow10.go

import (
	"embed"
	"path/filepath"
)

//go:embed c/snello.h c/internal.h c/snello.c c/slice.c c/sort.c c/string.c c/format.c c/quote.c c/parse.c c/map.c c/bufio.c c/os.c c/print.c c/math.c c/rand.c c/time.c c/chan.c c/sync.c c/alloc.c c/unicode.c c/rune.c c/norm.c c/segment.c c/pow10.c c/sched.c c/net.c c/debug.c c/testing.c
var files embed.FS

// sources are the runtime's files a build compiles.
var sources = []string{
	"snello.c", "slice.c", "sort.c", "string.c", "format.c", "quote.c", "parse.c", "map.c", "bufio.c",
	"os.c", "print.c", "math.c", "rand.c", "time.c", "chan.c", "sync.c", "alloc.c", "unicode.c", "rune.c", "norm.c", "segment.c", "pow10.c",
	"sched.c", "net.c", "debug.c", "testing.c",
}

// headers are the runtime's headers: snello.h, which the program's C
// includes too, and internal.h, which only the runtime's files do.
var headers = []string{"snello.h", "internal.h"}

// Files returns the runtime's files by name.
func Files() map[string][]byte {
	out := make(map[string][]byte)
	for _, name := range append(append([]string{}, headers...), sources...) {
		out[name], _ = files.ReadFile("c/" + name)
	}

	return out
}

// Paths returns the paths of the runtime's files a build compiles, once
// Files has been written into dir.
func Paths(dir string) []string {
	out := make([]string, len(sources))
	for i, name := range sources {
		out[i] = filepath.Join(dir, name)
	}

	return out
}
