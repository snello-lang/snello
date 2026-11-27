// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testproc

import (
	"os"
	"strconv"
	"strings"
)

// processes lists the processes the system runs, from /proc: each
// one's stat file gives its parent and group after its name, which is
// in parentheses and may hold any byte, so the fields are read after
// the last parenthesis. A process that ends while it is read is left
// out.
func processes() []process {
	dir, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}

	var out []process
	for _, d := range dir {
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}

		stat, err := os.ReadFile("/proc/" + d.Name() + "/stat")
		if err != nil {
			continue
		}

		i := strings.LastIndexByte(string(stat), ')')
		if i < 0 {
			continue
		}

		fields := strings.Fields(string(stat[i+1:])) // state, parent, group, ...
		if len(fields) < 3 {
			continue
		}

		ppid, err1 := strconv.Atoi(fields[1])
		pgid, err2 := strconv.Atoi(fields[2])
		if err1 == nil && err2 == nil {
			out = append(out, process{pid, ppid, pgid, fields[0] == "Z"})
		}
	}

	return out
}
