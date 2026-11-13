// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lower

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/types"
)

func TestSpawnEntry(t *testing.T) {
	for _, k := range []int{0, 1, 9, 10, 4096} {
		name := SpawnEntry(k)
		if got, ok := SpawnIndex(name); !ok || got != k {
			t.Errorf("SpawnIndex(%q) = %d, %v, want %d", name, got, ok, k)
		}
	}

	if SpawnTask(3) != "sn_task_3" || SpawnEntry(3) != "sn_task_3_run" {
		t.Errorf("SpawnTask(3) = %q, SpawnEntry(3) = %q", SpawnTask(3), SpawnEntry(3))
	}

	for _, name := range []string{"sn_task__run", "sn_task_01_run", "sn_task_-1_run", "sn_task_+1_run", "sn_task_1", "task_1_run", "sn_task_1_run.cold", "sn_task_x_run"} {
		if k, ok := SpawnIndex(name); ok {
			t.Errorf("SpawnIndex(%q) = %d, want no spawn", name, k)
		}
	}
}

// TestCallbackNames checks each name of a function the runtime calls
// through a pointer carries its prefix and tells the types apart.
func TestCallbackNames(t *testing.T) {
	str, num := types.Typ[types.String], types.Typ[types.Int]
	for _, c := range []struct {
		prefix string
		name   func(types.Type) string
	}{{ElemDropPrefix, ElemDrop}, {KeyHashPrefix, KeyHash}, {KeyEqPrefix, KeyEq}} {
		if a, b := c.name(str), c.name(num); !strings.HasPrefix(a, c.prefix) || !strings.HasPrefix(b, c.prefix) || a == b {
			t.Errorf("%s: %q and %q", c.prefix, a, b)
		}
	}

	if LazyInit("sn__x") != LazyInitPrefix+"sn__x" {
		t.Errorf("LazyInit(sn__x) = %q", LazyInit("sn__x"))
	}
}
