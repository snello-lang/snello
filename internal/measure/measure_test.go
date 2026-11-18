// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package measure

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
)

const (
	// helperVar names, in the environment of the test binary run again
	// as a program to measure, what it does: one of the helper modes.
	helperVar = "SNELLO_MEASURE_HELPER"

	// The helper's modes: write to both of its outputs, exit with
	// failCode, end itself by force, or touch touchBytes of memory.
	modePrint = "print"
	modeFail  = "fail"
	modeKill  = "kill"
	modeTouch = "touch"

	failCode   = 3
	touchBytes = 16 << 20
	pageBytes  = 4096

	// parentBytes is what the test holds while it measures a program
	// that holds touchBytes, so that a reading of the test's own peak
	// shows.
	parentBytes = 96 << 20
)

// TestHelperProcess is the program the tests measure, the test binary
// run again with helperVar set; run as a test, it does nothing.
func TestHelperProcess(t *testing.T) {
	switch os.Getenv(helperVar) {
	case "":
		return
	case modePrint:
		fmt.Fprint(os.Stdout, "out")
		fmt.Fprint(os.Stderr, "err")
	case modeFail:
		os.Exit(failCode)
	case modeKill:
		killSelf()
	case modeTouch:
		touch(touchBytes)
	}

	os.Exit(0)
}

// TestRun runs a program with arguments, giving its two outputs apart,
// how long it ran, and its peak.
func TestRun(t *testing.T) {
	stdout, stderr, res, err := helper(t, New(t), modePrint)
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}

	if stdout != "out" || stderr != "err" {
		t.Errorf("printed %q and %q, want out and err", stdout, stderr)
	}

	if res.Took <= 0 || res.Peak <= 0 {
		t.Errorf("%+v, want a time and a peak", res)
	}
}

// TestRunFails gives an error for a program that exits with a code
// other than zero, naming the code.
func TestRunFails(t *testing.T) {
	_, _, _, err := helper(t, New(t), modeFail)
	if want := fmt.Sprintf("exit status %d", failCode); err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Errorf("err %v, want one ending %q", err, want)
	}
}

// TestPeakIsTheProgramsOwn reads the peak of the program alone: the test
// holds six times what the program does, and the reading is the
// program's, at least what it touched and well under what the test
// holds. On Linux a child the test started itself would read the
// test's.
func TestPeakIsTheProgramsOwn(t *testing.T) {
	m := New(t)
	held := touch(parentBytes)
	_, stderr, res, err := helper(t, m, modeTouch)
	runtime.KeepAlive(held)
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}

	if res.Peak < touchBytes || res.Peak >= parentBytes/2 {
		t.Errorf("peak %d MB, want at least %d MB and under %d MB", res.Peak>>20, touchBytes>>20, parentBytes>>21)
	}
}

// helper runs the test binary as a measured program in the given mode.
func helper(t *testing.T, m *Measurer, mode string) (string, string, Result, error) {
	t.Helper()
	t.Setenv(helperVar, mode)
	var stdout, stderr bytes.Buffer
	res, err := m.Run(&stdout, &stderr, os.Args[0], "-test.run=^TestHelperProcess$")
	return stdout.String(), stderr.String(), res, err
}

// touch makes n bytes and writes a byte of each page, so that the
// system counts them resident, and keeps them until it returns.
func touch(n int) []byte {
	b := make([]byte, n)
	for i := 0; i < len(b); i += pageBytes {
		b[i] = 1
	}

	return b
}
