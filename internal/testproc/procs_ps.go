// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !windows && !linux

package testproc

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// processes lists the processes the system runs, from ps, which every
// such system has at /bin/ps: one line each, its id, its parent's and
// its group's, and its state, a Z for a zombie. ps runs in a group of
// its own, so that the guard's listing does not find its own ps among
// what its group runs.
func processes() []process {
	ps := exec.Command("/bin/ps", "-A", "-o", "pid=,ppid=,pgid=,stat=")
	ps.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := ps.Output()
	if err != nil {
		return nil
	}

	var list []process
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 4 {
			continue
		}

		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		pgid, err3 := strconv.Atoi(f[2])
		if err1 == nil && err2 == nil && err3 == nil {
			list = append(list, process{pid, ppid, pgid, strings.HasPrefix(f[3], "Z")})
		}
	}

	return list
}
