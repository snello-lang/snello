// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package measure

import (
	"fmt"
	"io"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/snello-lang/snello/internal/testproc"
)

// readCounters is the access GetProcessMemoryInfo asks of a handle,
// PROCESS_QUERY_LIMITED_INFORMATION and PROCESS_VM_READ.
const readCounters = 0x1000 | 0x0010

// processMemoryInfo is GetProcessMemoryInfo, which kernel32 exports as
// K32GetProcessMemoryInfo.
var processMemoryInfo = syscall.NewLazyDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")

// memoryCounters is PROCESS_MEMORY_COUNTERS, the counters
// GetProcessMemoryInfo fills.
type memoryCounters struct {
	size                       uint32
	pageFaults                 uint32
	peakWorkingSet, workingSet uintptr
	quotaPeakPaged, quotaPaged uintptr
	quotaPeakNonPaged          uintptr
	quotaNonPaged              uintptr
	pagefile, peakPagefile     uintptr
}

// A Measurer runs programs and measures them.
type Measurer struct{}

// New returns a Measurer, which on Windows needs nothing made.
func New(testing.TB) *Measurer {
	return &Measurer{}
}

// Run runs the program at path with args, writing its standard output
// to stdout and its standard error to stderr, and measures it. A
// program that fails gives an error.
func (m *Measurer) Run(stdout, stderr io.Writer, path string, args ...string) (Result, error) {
	cmd := testproc.Command(path, args...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return Result{}, err
	}

	// The handle os/exec holds keeps the program's id from being reused
	// until Wait, so this opens the program, and keeps its counters past
	// Wait's close of that handle.
	h, openErr := syscall.OpenProcess(readCounters, false, uint32(cmd.Process.Pid))
	if openErr == nil {
		defer syscall.CloseHandle(h)
	}

	err := cmd.Wait()
	res := Result{Took: time.Since(start)}
	if err != nil {
		return res, fmt.Errorf("%s: %v", path, err)
	}

	if openErr != nil {
		return res, fmt.Errorf("opening %s to read its peak: %v", path, openErr)
	}

	counters := memoryCounters{size: uint32(unsafe.Sizeof(memoryCounters{}))}
	if ok, _, callErr := processMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&counters)), uintptr(counters.size)); ok == 0 {
		return res, fmt.Errorf("reading the peak of %s: %v", path, callErr)
	}

	res.Peak = int64(counters.peakWorkingSet)
	return res, nil
}
