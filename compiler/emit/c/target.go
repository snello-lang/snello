// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/snello-lang/snello/compiler/platform"
)

const (
	// targetFlag names clang's target, the triple it compiles and links
	// for; sysrootFlag the directory it reads the target's headers and
	// libraries from; useLLD links with LLVM's linker, which writes a
	// binary of any processor, where a Linux system's own ld writes its
	// own processor's alone; dumpMachine asks gcc which target it is
	// built for, and progName a compiler where it finds a program it
	// runs, beside itself or on the path.
	targetFlag  = "--target="
	sysrootFlag = "--sysroot="
	useLLD      = "-fuse-ld=lld"
	dumpMachine = "-dumpmachine"
	progName    = "-print-prog-name="

	// lldName is LLVM's linker for ELF as clang runs it, gccLinux the
	// system part of the triple a gcc built for Linux names, and
	// tripleSeparator what parts a triple's processor from the rest,
	// which a gcc's -dumpmachine begins with as the target's triple does.
	lldName         = "ld.lld"
	gccLinux        = "-linux-"
	tripleSeparator = "-"

	// coffLinkerName is LLVM's linker for Windows' images as clang runs
	// it; noSecureWarnings keeps the Universal CRT's headers from
	// warning of the C library's calls they would have a program spell
	// the Microsoft way, which every build of the runtime's makes an
	// error; pthreadFlag is the POSIX threads' flag, which a Windows
	// build has no use for and clang warns of; includeFlag names a
	// directory of system headers and libraryFlag one of libraries.
	coffLinkerName   = "lld-link"
	noSecureWarnings = "-D_CRT_SECURE_NO_WARNINGS"
	pthreadFlag      = "-pthread"
	includeFlag      = "-isystem"
	libraryFlag      = "-L"
)

// The directories of Windows' SDK and Microsoft's C runtime under a
// sysroot in the layout the xwin tool writes, the headers' and each
// processor's libraries', named as xwin names the processors.
var (
	msvcIncludes = []string{"crt/include", "sdk/include/ucrt", "sdk/include/um", "sdk/include/shared"}
	msvcLibs     = []string{"crt/lib", "sdk/lib/um", "sdk/lib/ucrt"}
	msvcArch     = map[string]string{platform.AMD64: "x86_64", platform.ARM64: "aarch64"}
)

// CheckTarget returns why the compiler cc cannot build for t, or nil:
// the host needs nothing more, but on Windows, where checkWindows says
// what a build takes; another platform needs a compiler that builds for
// it, clang for any or a gcc built for it alone, and its system files
// and a linker for it. macOS is built for on macOS alone,
// with Apple's SDK and linker; another Linux takes ld.lld through
// clang, or the linker of the gcc built for it, and, on a host that is
// not Linux, the sysroot SNELLO_SYSROOT_<target> names, since nothing
// of a Linux system is there to link against.
func CheckTarget(cc string, t platform.Target) error {
	host := platform.Host()
	if t == host && t.OS != platform.Windows {
		return nil
	}

	where := "building for " + t.String() + " on " + host.String()
	if t.OS == platform.Darwin && host.OS != platform.Darwin {
		return errors.New(where + " takes Apple's SDK and linker, which run on macOS alone")
	}

	if t.OS == platform.Windows {
		return checkWindows(cc, t, where)
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

	if !findsLinker(cc, lldName) {
		return errors.New(where + " takes LLVM's linker, " + lldName + ", which is neither beside " + cc + " nor on the PATH: install lld")
	}

	return nil
}

// checkWindows returns why cc cannot build for Windows on t, or nil:
// clang builds for it, which a gcc built for another system cannot,
// against Microsoft's SDK and C runtime, which a Windows machine with
// Visual Studio's build tools has and any other reads from the sysroot
// SNELLO_SYSROOT_windows_<processor> names, and links through LLVM's
// COFF linker, whose link-time optimization reads clang's objects.
func checkWindows(cc string, t platform.Target, where string) error {
	if IsGCC(cc) {
		return errors.New(where + ": " + cc + " is gcc, which builds for Windows' MinGW alone; set CC to clang")
	}

	if dir := t.Sysroot(); dir != "" {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return errors.New(where + ": " + t.SysrootVariable() + " names " + dir + ", which is not a directory")
		}

		libs := filepath.Join(dir, filepath.FromSlash(msvcLibs[0]), msvcArch[t.Arch])
		if info, err := os.Stat(libs); err != nil || !info.IsDir() {
			return errors.New(where + ": " + t.SysrootVariable() + " names " + dir + ", which holds no libraries for " +
				msvcArch[t.Arch] + ": xwin --arch " + msvcArch[t.Arch] + " splat --output " + dir + " writes them")
		}
	} else if platform.Host().OS != platform.Windows {
		return errors.New(where + " takes Microsoft's SDK and C runtime: set " + t.SysrootVariable() +
			" to a directory the xwin tool wrote them into, xwin splat --output DIR")
	}

	if !findsLinker(cc, coffLinkerName) {
		return errors.New(where + " takes LLVM's linker, " + coffLinkerName + ", which is neither beside " + cc + " nor on the PATH: install lld")
	}

	return nil
}

// findsLinker reports whether the compiler cc finds the linker name, as
// its link would: clang beside itself, where LLVM's installer puts its
// linkers whether or not their directory is on the path, or on the
// path, which is where any other compiler looks.
func findsLinker(cc, name string) bool {
	if out, err := exec.Command(cc, progName+name).Output(); err == nil {
		if found := strings.TrimSpace(string(out)); filepath.IsAbs(found) {
			if info, err := os.Stat(found); err == nil && !info.IsDir() {
				return true
			}
		}
	}

	_, err := exec.LookPath(name)
	return err == nil
}

// TargetFlags are the options that make the compiler cc compile for t:
// none for a Linux host, which every compiler builds for already; for
// another platform, and for macOS, whose triple names the oldest
// version a program runs on, clang's target triple, which a gcc built
// for t needs not and refuses, and the sysroot t names, when it names
// one.
func TargetFlags(cc string, t platform.Target) []string {
	if t == platform.Host() && t.OS != platform.Darwin && t.OS != platform.Windows {
		return nil
	}

	if t.OS == platform.Windows {
		return []string{targetFlag + t.Triple()} // the SDK's headers are compileFlags', since a link takes none
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

// compileFlags are the options a compile for t adds to the target's, a
// link taking none of them: on Windows, the define that quiets the
// Universal CRT's headers, and the SDK's headers under the sysroot t
// names, when it names one.
func compileFlags(t platform.Target) []string {
	if t.OS != platform.Windows {
		return nil
	}

	flags := []string{noSecureWarnings}
	if dir := t.Sysroot(); dir != "" {
		for _, sub := range msvcIncludes {
			flags = append(flags, includeFlag, filepath.Join(dir, filepath.FromSlash(sub)))
		}
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
	if t.OS == platform.Windows { // LLVM's COFF linker, here as on Windows itself, with the SDK's libraries
		flags := []string{useLLD}
		if dir := t.Sysroot(); dir != "" {
			for _, sub := range msvcLibs {
				flags = append(flags, libraryFlag+filepath.Join(dir, filepath.FromSlash(sub), msvcArch[t.Arch]))
			}
		}

		return flags
	}

	if t == platform.Host() || t.OS != platform.Linux || IsGCC(cc) {
		return nil
	}

	return []string{useLLD}
}
