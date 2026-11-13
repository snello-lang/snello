// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package backendtest

import (
	"strconv"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/emit/stack"
)

// functionValue begins the analysis's reason for a task that calls a
// function value, through a pointer whose targets it does not know.
const functionValue = "a call through a pointer"

// FunctionValues are the programs a spawn of which calls a function
// value, by name, with the call: such a task keeps a guarded stack,
// unless a release build's optimizer turned the call into a direct one.
var FunctionValues = map[string]string{
	"sync":                 "Once.Do calls the function it is given",
	"catch-across-threads": "testing.Catch calls the function it is given",
}

// Bounded fails t for each spawn of the program name built at bin whose
// task the analysis of the linked program left without a bound of its
// stack, naming the analysis's reason: such a task runs on a guarded
// stack, which costs a spawn two system calls and nothing a program
// prints shows, so a change that loses a bound fails here instead. A
// spawn the linker removed has no task to bound, and one of the
// FunctionValues programs may call a function value.
func Bounded(t *testing.T, name, bin string) {
	t.Helper()
	table, err := stack.Table(bin)
	if err != nil {
		t.Errorf("the bounds of %s: %v", bin, err)
		return
	}

	var lost []int
	for k, b := range table {
		if b == lower.Unbounded {
			lost = append(lost, k)
		}
	}

	if len(lost) == 0 {
		return
	}

	bounds, err := stack.Patch(bin) // the analysis again, for its reasons
	if err != nil || len(bounds) != len(table) {
		t.Errorf("spawns %v of %s have no bound: %v", lost, bin, err)
		return
	}

	var reasons []string
	for _, k := range lost {
		reasons = append(reasons, bounds[k].Reason)
	}

	if why := unexpected(name, lost, reasons); len(why) > 0 {
		t.Errorf("%s has spawns with no bound of their stacks:\n%s", bin, strings.Join(why, "\n"))
	}
}

// unexpected returns, of the spawns lost of program name with the
// analysis's reasons, those a change lost: not removed by the linker,
// nor a call of a function value in one of the FunctionValues.
func unexpected(name string, lost []int, reasons []string) []string {
	_, calls := FunctionValues[name]
	var why []string
	for i, k := range lost {
		if reasons[i] != stack.Removed && !(calls && strings.HasPrefix(reasons[i], functionValue)) {
			why = append(why, "spawn "+strconv.Itoa(k)+": "+reasons[i])
		}
	}

	return why
}
