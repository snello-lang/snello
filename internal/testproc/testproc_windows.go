// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testproc

import (
	"os/exec"
	"syscall"
	"unsafe"
)

const (
	// jobObjectExtendedLimitInformation is the class of information
	// SetInformationJobObject sets, JOBOBJECT_EXTENDED_LIMIT_INFORMATION.
	jobObjectExtendedLimitInformation = 9

	// jobObjectLimitKillOnJobClose kills every process in the job when
	// its last handle closes.
	jobObjectLimitKillOnJobClose = 0x2000
)

// A basicLimit is JOBOBJECT_BASIC_LIMIT_INFORMATION.
type basicLimit struct {
	perProcessUserTimeLimit int64
	perJobUserTimeLimit     int64
	limitFlags              uint32
	minimumWorkingSetSize   uintptr
	maximumWorkingSetSize   uintptr
	activeProcessLimit      uint32
	affinity                uintptr
	priorityClass           uint32
	schedulingClass         uint32
}

// An ioCounters is IO_COUNTERS.
type ioCounters struct {
	readOperationCount, writeOperationCount, otherOperationCount uint64
	readTransferCount, writeTransferCount, otherTransferCount    uint64
}

// An extendedLimit is JOBOBJECT_EXTENDED_LIMIT_INFORMATION.
type extendedLimit struct {
	basic                                    basicLimit
	io                                       ioCounters
	processMemoryLimit, jobMemoryLimit       uintptr
	peakProcessMemoryUsed, peakJobMemoryUsed uintptr
}

func init() {
	joinJob()
}

// prepare leaves cmd as it is: the test binary is in its job from the
// start, and a process it starts is in the job with it.
func prepare(*exec.Cmd) {}

// joinJob puts the test binary in a job that kills its processes when
// its handle, which the binary holds to its end, closes. A system that
// refuses leaves the binary as it was.
func joinJob() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	job, _, _ := kernel32.NewProc("CreateJobObjectW").Call(0, 0)
	if job == 0 {
		return
	}

	var info extendedLimit
	info.basic.limitFlags = jobObjectLimitKillOnJobClose
	if ok, _, _ := kernel32.NewProc("SetInformationJobObject").Call(job, jobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info)); ok == 0 {
		syscall.CloseHandle(syscall.Handle(job))
		return
	}

	self, err := syscall.GetCurrentProcess()
	if err != nil {
		syscall.CloseHandle(syscall.Handle(job))
		return
	}

	kernel32.NewProc("AssignProcessToJobObject").Call(job, uintptr(self)) // the handle stays open to the binary's end
}
