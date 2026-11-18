// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/snello-lang/snello/compiler/platform"
)

const (
	// targetFlag names clang's target, the triple it compiles and links
	// for; sysrootFlag the directory it reads the target's headers and
	// libraries from; useLLD links with LLVM's linker, which writes a
	// binary of any processor, where a Linux system's own ld writes its
	// own processor's alone; dumpMachine asks gcc which target it is
	// built for.
	targetFlag  = "--target="
	sysrootFlag = "--sysroot="
	useLLD      = "-fuse-ld=lld"
	dumpMachine = "-dumpmachine"

	// lldName is LLVM's linker for ELF as clang runs it, gccLinux the
	// system part of the triple a gcc built for Linux names, and
	// tripleSeparator what parts a triple's processor from the rest,
	// which a gcc's -dumpmachine begins with as the target's triple does.
	lldName         = "ld.lld"
	gccLinux        = "-linux-"
	tripleSeparator = "-"
)

// CheckTarget returns why the compiler cc cannot build for t, or nil:
// the host needs nothing more; another platform needs a compiler that
// builds for it, clang for any or a gcc built for it alone, and its
// system files and a linker for it. macOS is built for on macOS alone,
// with Apple's SDK and linker; another Linux takes ld.lld through
// clang, or the linker of the gcc built for it, and, on a host that is
// not Linux, the sysroot SNELLO_SYSROOT_<target> names, since nothing
// of a Linux system is there to link against.
func CheckTarget(cc string, t platform.Target) error {
	host := platform.Host()
	if t == host {
		return nil
	}

	where := "building for " + t.String() + " on " + host.String()
	if t.OS == platform.Darwin && host.OS != platform.Darwin {
		return errors.New(where + " takes Apple's SDK and linker, which run on macOS alone")
	}

	gcc, sysroot := IsGCC(cc), t.Sysroot()
	if gcc {
		cpu, _, _ := strings.Cut(t.Triple(), tripleSeparator)
		out, err := exec.Command(cc, dumpMachine).Output()
		machine := strings.TrimSpace(string(out))
		if err != nil || !strings.HasPrefix(machine, cpu+tripleSeparator) || !strings.Contains(machine, gccLinux) {
			return errors.New(where + ": " + cc + " is gcc for " + machine + ", which builds for its own target alone; set CC to clang, or to a gcc built for " + t.String())
		}
	}

	if sysroot != "" {
		if info, err := os.Stat(sysroot); err != nil || !info.IsDir() {
			return errors.New(where + ": " + t.SysrootVariable() + " names " + sysroot + ", which is not a directory")
		}
	}

	if t.OS != platform.Linux {
		return nil // macOS on the other processor, with the SDK the host's compiler has
	}

	if sysroot == "" && host.OS != platform.Linux {
		return errors.New(where + " takes a Linux system's headers and libraries: set " + t.SysrootVariable() + " to a directory holding them, a sysroot")
	}

	if gcc {
		return nil // a gcc built for the target links with its own linker
	}

	if _, err := exec.LookPath(lldName); err != nil {
		return errors.New(where + " takes LLVM's linker, " + lldName + ", which is not on the PATH: install lld")
	}

	return nil
}

// TargetFlags are the options that make the compiler cc compile for t:
// none for a Linux host, which every compiler builds for already; for
// another platform, and for macOS, whose triple names the oldest
// version a program runs on, clang's target triple, which a gcc built
// for t needs not and refuses, and the sysroot t names, when it names
// one.
func TargetFlags(cc string, t platform.Target) []string {
	if t == platform.Host() && t.OS != platform.Darwin {
		return nil
	}

	var flags []string
	if !IsGCC(cc) {
		flags = append(flags, targetFlag+t.Triple())
	}

	if dir := t.Sysroot(); dir != "" {
		flags = append(flags, sysrootFlag+dir)
	}

	return flags
}

// linkerFlags are the options a link for t by the compiler cc adds to
// its own: for a Linux other than the host through clang, LLVM's
// linker, which reads every processor's objects and the link-time
// optimizer's options as the system's linker does; a gcc built for the
// target links with the linker built with it, which reads its own
// link-time optimizer's objects and LLVM's linker does not.
func linkerFlags(cc string, t platform.Target) []string {
	if t == platform.Host() || t.OS != platform.Linux || IsGCC(cc) {
		return nil
	}

	return []string{useLLD}
}
