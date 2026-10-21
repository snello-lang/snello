// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package runtime holds, under c/, the C runtime every Teak program links
// against, embedded in the compiler so that a build needs nothing but
// the compiler and a C compiler: strings, slices and maps, printing,
// panics and the guard stack, tasks, channels and locks, and the system
// calls the library's bodiless functions stand for; alloc.c is the
// allocator, unicode.c the tables of case, white space and printable
// runes that mkunicode.go writes, sched.c the scheduler that runs the
// tasks and net.c the sockets over its poller. Both backends write these
// files next to the program they emit and compile them with it; debug.c
// is the checking build's tracker of every block.
package runtime

//go:generate go run mkunicode.go

import (
	"embed"
	"path/filepath"
)

//go:embed c/teak.h c/teak.c c/alloc.c c/unicode.c c/sched.c c/net.c c/debug.c
var files embed.FS

// sources are the runtime's files a build compiles.
var sources = []string{"teak.c", "alloc.c", "unicode.c", "sched.c", "net.c", "debug.c"}

// Files returns the runtime's files by name.
func Files() map[string][]byte {
	out := make(map[string][]byte)
	for _, name := range append([]string{"teak.h"}, sources...) {
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
