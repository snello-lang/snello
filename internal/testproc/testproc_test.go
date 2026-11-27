// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testproc

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	// roleEnv tells the test binary run again by TestOutlives what to
	// be: the helper, which starts a sleeper and ends at once, or the
	// sleeper, which sleeps.
	roleEnv = "SNELLO_TESTPROC_ROLE"

	// sleepFor is how long the sleeper sleeps, far longer than the test
	// waits for it to be gone.
	sleepFor = time.Minute

	// goneWithin is how long the sleeper is given to be killed once the
	// helper ends.
	goneWithin = 10 * time.Second
)

// TestHelperProcess is the helper and the sleeper TestOutlives runs,
// and nothing when run as a test.
func TestHelperProcess(t *testing.T) {
	switch os.Getenv(roleEnv) {
	case "helper":
		cmd := Command(os.Args[0], "-test.run=^TestHelperProcess$")
		cmd.Env = append(os.Environ(), roleEnv+"=sleeper")
		if err := cmd.Start(); err != nil {
			os.Exit(2)
		}

		fmt.Println(cmd.Process.Pid)
		os.Exit(0) // ends without waiting, as a binary a timeout or a kill ends: nothing deferred runs
	case "sleeper":
		time.Sleep(sleepFor)
		os.Exit(0)
	}
}

// TestOutlives runs a helper that starts a program through Command and
// ends at once, and wants the program gone soon after: no program a
// test runs outlives the binary that ran it.
func TestOutlives(t *testing.T) {
	wantGone(t, helperPid(t, "^TestHelperProcess$", "helper"))
}

// helperPid runs the test binary again as the helper run names, in the
// role role, and returns the pid it prints once it has ended.
func helperPid(t *testing.T, run, role string) int {
	t.Helper()
	helper := exec.Command(os.Args[0], "-test.run="+run)
	helper.Env = append(os.Environ(), roleEnv+"="+role)
	out, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}

	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}

	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("the helper printed no pid: %v", err)
	}

	helper.Wait()
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("the helper printed %q, not a pid", line)
	}

	return pid
}

// wantGone waits for the program pid to be gone, killing it and failing
// when it outlives goneWithin.
func wantGone(t *testing.T, pid int) {
	t.Helper()
	for deadline := time.Now().Add(goneWithin); alive(pid); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			kill(pid)
			t.Fatalf("the program %d outlived the binary that started it by %v", pid, goneWithin)
		}
	}
}
