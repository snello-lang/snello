// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lower

import (
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/types"
)

// The names below are those the analysis of a linked program reads to
// bound the stack of each task: the table it writes the bounds into,
// the function each spawn's task runs, and the functions the runtime
// calls through the pointers the program gives it, which the analysis
// takes, by their names, for the targets of those calls.

const (
	// StackBounds is the program's table of the stack each of its
	// spawns can use, in bytes, one entry a spawn in the order the
	// backend writes them, which the spawn passes to the runtime. The
	// backend writes every entry Unbounded, and the analysis of the
	// linked binary writes the bound of each task it can prove into the
	// binary itself, since only the linked code says what a task calls.
	StackBounds = "sn_stack_bounds"

	// Unbounded is a spawn's entry in StackBounds when its task has no
	// bound: the runtime gives it a large stack behind a guard page.
	// It is not zero, so that the table is data the binary's file holds
	// for the analysis to write over, not zeros the loader makes.
	Unbounded = 0xFFFFFFFF

	// ElemDropPrefix starts the name of the function a backend gives
	// the runtime to drop an element of a channel, or a key or a value
	// of a map, with, and no other function's.
	ElemDropPrefix = "sn_elemdrop_"

	// KeyHashPrefix and KeyEqPrefix start the names of the hash and the
	// equality of a map's key type a backend gives the runtime, in the
	// map's type.
	KeyHashPrefix = "sn_khash_"
	KeyEqPrefix   = "sn_keq_"

	// LazyInitPrefix starts the name of the initializer of a package
	// variable made on its first use, which the runtime's maker of such
	// variables calls.
	LazyInitPrefix = "sn_lazyinit_"

	// spawnPrefix and spawnSuffix bracket the index of a spawn in the
	// name of the function its task runs, sn_task_0_run.
	spawnPrefix = "sn_task_"
	spawnSuffix = "_run"
)

// ElemDrop returns the name of the function that drops an element of
// type elem of a channel or a map, which the runtime is given.
func ElemDrop(elem types.Type) string {
	return ElemDropPrefix + Mangle(elem)
}

// KeyHash and KeyEq return the names of the hash and the equality of a
// map's key type.
func KeyHash(key types.Type) string {
	return KeyHashPrefix + Mangle(key)
}

func KeyEq(key types.Type) string {
	return KeyEqPrefix + Mangle(key)
}

// LazyInit returns the name of the initializer of the package variable
// whose symbol is name.
func LazyInit(name string) string {
	return LazyInitPrefix + name
}

// SpawnTask returns the stem of the names a backend gives what it
// writes for a program's spawn k, counted from zero in the order it
// writes the spawns: the block of the task's values, a spawned closure
// lifted to a function, and the entry.
func SpawnTask(k int) string {
	return spawnPrefix + strconv.Itoa(k)
}

// SpawnEntry returns the name of the function the task of a program's
// spawn k runs: the analysis finds the function of each entry of
// StackBounds in the binary by it.
func SpawnEntry(k int) string {
	return SpawnTask(k) + spawnSuffix
}

// SpawnIndex reads a spawn's index back from the name of its task's
// function, and reports whether name is one.
func SpawnIndex(name string) (int, bool) {
	digits, ok := strings.CutPrefix(name, spawnPrefix)
	if !ok {
		return 0, false
	}

	digits, ok = strings.CutSuffix(digits, spawnSuffix)
	if !ok {
		return 0, false
	}

	k, err := strconv.Atoi(digits)
	if err != nil || k < 0 || strconv.Itoa(k) != digits { // the digits as SpawnEntry writes them, with no sign or leading zero
		return 0, false
	}

	return k, true
}
