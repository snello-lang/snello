// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package backendtest

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/platform"
)

// What lists a binary's symbols with their sections where the tests
// read them, and the program's entry as each lists it: macOS's nm
// writes the section before the name, which begins with an underscore,
// and Linux's, in its sysv form, the name first and the section last.
const (
	symbolLister  = "nm"
	darwinSymbols = "-m"
	linuxSymbols  = "--format=sysv"
	darwinMain    = " _main"
	linuxMain     = "main "
	sysvSeparator = "|"
)

// Open checks that the program a backend built as bin has its main in
// the code section, the only code the scheduler takes a task off its
// worker in: a backend that left a program's code out of it would
// leave the program's tasks never preempted, and every program would
// still print what it should. A system whose symbols the tests cannot
// list with their sections, or one with no lister, checks nothing.
func Open(t *testing.T, name, bin string) {
	t.Helper()
	option := linuxSymbols
	switch runtime.GOOS {
	case platform.Darwin:
		option = darwinSymbols
	case platform.Linux:
	default:
		return
	}

	listing, err := exec.Command(symbolLister, option, bin).Output()
	if err != nil {
		return
	}

	if !mainOpen(string(listing), runtime.GOOS) {
		t.Errorf("%s: main is not in the code section, %s", name, lower.CodeSection(runtime.GOOS))
	}
}

// mainOpen reports whether a listing of a binary's symbols, as the
// system's lister writes it, has main in the system's code section.
func mainOpen(listing, system string) bool {
	section := lower.CodeSection(system)
	if system == platform.Darwin { // the segment and the section, without the kind
		parts := strings.Split(section, ",")
		section = "(" + parts[0] + "," + parts[1] + ")"
	}

	for _, line := range strings.Split(listing, "\n") {
		if system == platform.Darwin {
			if strings.HasSuffix(line, darwinMain) && strings.Contains(line, section) {
				return true
			}

			continue
		}

		fields := strings.Split(line, sysvSeparator)
		if strings.HasPrefix(line, linuxMain) && strings.TrimSpace(fields[len(fields)-1]) == section {
			return true
		}
	}

	return false
}
