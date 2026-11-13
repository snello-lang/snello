// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package backendtest

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/emit/stack"
)

func TestUnexpected(t *testing.T) {
	pointer := functionValue + " in sn_task_2_run"
	recursion := "a recursion through walk"
	for _, c := range []struct {
		name    string
		reasons []string
		want    string
	}{
		{"any", []string{stack.Removed}, ""},
		{"any", []string{pointer}, "spawn 0: " + pointer},
		{"sync", []string{pointer}, ""},
		{"sync", []string{recursion, pointer}, "spawn 0: " + recursion},
	} {
		lost := make([]int, len(c.reasons))
		for i := range lost {
			lost[i] = i
		}

		if got := strings.Join(unexpected(c.name, lost, c.reasons), "\n"); got != c.want {
			t.Errorf("%s %q: got %q, want %q", c.name, c.reasons, got, c.want)
		}
	}
}

func TestFunctionValues(t *testing.T) {
	for name, why := range FunctionValues {
		if why == "" {
			t.Errorf("%s: no call named", name)
		}
	}
}
