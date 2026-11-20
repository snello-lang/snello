// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// What the console test types and reads: the text the probe writes to
// the console; the lines typed at it, each ended by the Return key, the
// first other than the probe's text, which the console echoes, the last
// one Ctrl-Z, which ends the input; Ctrl-C, typed as a terminal
// sends it; and what the probe reports as it goes, to a file, since the
// console is its standard streams.
const (
	consoleShown     = "héllo €"
	consoleLine      = "typed ü ∑"
	consoleTyped     = consoleLine + "\r" + "second\r" + "\x1a\r"
	consoleCtrlC     = "\x03"
	consoleWritten   = "written\n"
	consoleEnd       = "end\n"
	consoleCancelled = "cancelled\n"
	consoleRead      = consoleWritten + consoleLine + "\r\n" + "second\r\n" + consoleEnd

	// consoleSettle is how long the probe is given to be in its read
	// once it said it would be, and pollEvery how often its report is
	// read.
	consoleSettle = 500 * time.Millisecond
	pollEvery     = 10 * time.Millisecond
)

// What Windows names: the size of the pseudo console, 80 columns by 25
// rows as a COORD packs them; the attribute that puts a process on one,
// the flag that says a STARTUPINFOEX carries attributes, and the one that
// says its standard handles are given, none here, so that the process
// is given no value of its parent's; a wait
// that ended with its object signalled; and the exit code of a process
// the console's default handler ends on Ctrl-C.
const (
	consoleSize                      = 80 | 25<<16
	procThreadAttributePseudoConsole = 0x00020016
	extendedStartupInfoPresent       = 0x00080000
	startfUseStdHandles              = 0x00000100
	waitObject0                      = 0
	statusControlCExit               = 0xC000013A
)

// consoleProbe puts its standard input and output on the console it
// runs on, whatever handles it was given, takes Ctrl-C as a program
// does by default, writes text to the console through the runtime,
// reads the console's lines through it to the end of the input,
// reporting each, and reads once more, which Ctrl-C interrupts: the
// console's default handler ends the probe, and the read must not
// return first, as the end of the input, for the probe to run on and
// say so.
const consoleProbe = `#include "internal.h"
#include <errno.h>
#include <fcntl.h>
#include <io.h>
#include <stdio.h>
#include <windows.h>
static FILE *report;
static void say(const char *s) {
    fputs(s, report);
    fflush(report);
}
static int console(const wchar_t *name, int fd) {
    HANDLE h = CreateFileW(name, GENERIC_READ | GENERIC_WRITE, FILE_SHARE_READ | FILE_SHARE_WRITE, NULL, OPEN_EXISTING, 0, NULL);
    if (h == INVALID_HANDLE_VALUE) {
        return -1;
    }

    int f = _open_osfhandle((intptr_t)h, _O_BINARY);
    return f < 0 ? -1 : _dup2(f, fd);
}
int main(int argc, char **argv) {
    report = fopen(argv[1], "wb");
    if (report == NULL || !SetConsoleCtrlHandler(NULL, FALSE) || console(L"CONIN$", 0) != 0 || console(L"CONOUT$", 1) != 0) {
        return 1;
    }

    sn_init(argc, argv);
    static const char shown[] = "h\xc3\xa9llo \xe2\x82\xac\n";
    if (sn_write_fd(1, (const uint8_t *)shown, sizeof shown - 1) != (int64_t)(sizeof shown - 1)) {
        return 1;
    }

    say("written\n");
    uint8_t buf[64];
    for (;;) {
        sn_slice_u8 s = { buf, sizeof buf, sizeof buf };
        int64_t n = 0;
        if (sn_os_read(0, &s, &n) != 0) {
            say("failed\n");
            return 1;
        }

        if (n == 0) {
            break;
        }

        fwrite(buf, 1, (size_t)n, report);
        fflush(report);
    }

    say("end\n");
    sn_slice_u8 s = { buf, sizeof buf, sizeof buf };
    int64_t n = 0;
    int64_t code = sn_os_read(0, &s, &n);
    say(code == ECANCELED ? "cancelled\n" : "ended\n");
    return sn_finish(); // ends the program by the interrupt that cancelled it, as a program's end does
}
`

var (
	kernel32                              = syscall.NewLazyDLL("kernel32.dll")
	procCreatePseudoConsole               = kernel32.NewProc("CreatePseudoConsole")
	procClosePseudoConsole                = kernel32.NewProc("ClosePseudoConsole")
	procInitializeProcThreadAttributeList = kernel32.NewProc("InitializeProcThreadAttributeList")
	procUpdateProcThreadAttribute         = kernel32.NewProc("UpdateProcThreadAttribute")
	procDeleteProcThreadAttributeList     = kernel32.NewProc("DeleteProcThreadAttributeList")
)

// startupInfoEx is Windows' STARTUPINFOEXW: the start's information and
// the attributes of the process it starts.
type startupInfoEx struct {
	syscall.StartupInfo
	attributes *byte
}

// A pseudoConsole is a console with no window whose input is what a
// pipe is written and whose output a pipe reads, as a terminal hosts a
// console program.
type pseudoConsole struct {
	handle uintptr
	in     *os.File // what is typed at the console
	out    *os.File // what the console shows, as a terminal's text
}

// TestConsole runs a probe on a pseudo console, as a terminal runs a
// program: what the probe writes shows on the console as its text,
// accents and all; the lines typed reach it as UTF-8, to the line that
// starts with Ctrl-Z; and Ctrl-C, typed as a terminal sends it, cancels
// it: the read it was in fails as a cancelled call, never taken for the
// end of the input, and the program ends by the interrupt with the code
// Windows gives a program Ctrl-C ended.
func TestConsole(t *testing.T) {
	bin := buildProbe(t, filepath.Join(t.TempDir(), "console"), consoleProbe)
	report := filepath.Join(filepath.Dir(bin), "report")
	deadline := time.Now().Add(probeTimeout)
	pc, err := newPseudoConsole()
	if err != nil {
		t.Fatal(err)
	}

	var shown bytes.Buffer
	var drained sync.WaitGroup
	drained.Go(func() { shown.ReadFrom(pc.out) }) // until the console closes; a console no one reads stops
	defer func() {
		pc.close()
		drained.Wait()
		pc.out.Close()
	}()

	probe, err := pc.start(bin, report)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(probe)
	defer syscall.TerminateProcess(probe, 1) // a probe a failure left reading; nothing once it has ended

	if err := awaitReport(report, consoleWritten, probe, deadline); err != nil {
		t.Fatal(err)
	}

	if _, err := pc.in.WriteString(consoleTyped); err != nil {
		t.Fatal(err)
	}

	if err := awaitReport(report, consoleEnd, probe, deadline); err != nil {
		t.Fatal(err)
	}

	if got, _ := os.ReadFile(report); string(got) != consoleRead {
		t.Fatalf("the probe read %q, wanted %q", got, consoleRead)
	}

	time.Sleep(consoleSettle)
	if _, err := pc.in.WriteString(consoleCtrlC); err != nil {
		t.Fatal(err)
	}

	if ev, err := syscall.WaitForSingleObject(probe, uint32(max(time.Until(deadline).Milliseconds(), 0))); ev != waitObject0 {
		t.Fatalf("Ctrl-C did not end the probe: %v", err)
	}

	if got, _ := os.ReadFile(report); !strings.HasSuffix(string(got), consoleCancelled) {
		t.Errorf("the read Ctrl-C interrupted ended the report with %q, wanted the cancelled call", got)
	}

	var code uint32
	if err := syscall.GetExitCodeProcess(probe, &code); err != nil || code != statusControlCExit {
		t.Errorf("Ctrl-C ended the probe with %#x, %v, wanted %#x", code, err, statusControlCExit)
	}

	pc.close()
	drained.Wait()
	if !strings.Contains(shown.String(), consoleShown) {
		t.Errorf("the console showed %q, without the probe's %q", shown.String(), consoleShown)
	}
}

// newPseudoConsole makes a pseudo console and the two pipes it is read
// and written through.
func newPseudoConsole() (*pseudoConsole, error) {
	var inRead, inWrite, outRead, outWrite syscall.Handle
	if err := syscall.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		return nil, err
	}

	if err := syscall.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		syscall.CloseHandle(inRead)
		syscall.CloseHandle(inWrite)
		return nil, err
	}

	var handle uintptr
	hr, _, _ := procCreatePseudoConsole.Call(consoleSize, uintptr(inRead), uintptr(outWrite), 0, uintptr(unsafe.Pointer(&handle)))
	syscall.CloseHandle(inRead) // the console holds its own
	syscall.CloseHandle(outWrite)
	if hr != 0 {
		syscall.CloseHandle(inWrite)
		syscall.CloseHandle(outRead)
		return nil, fmt.Errorf("CreatePseudoConsole: %#x", hr)
	}

	return &pseudoConsole{handle: handle, in: os.NewFile(uintptr(inWrite), "console input"), out: os.NewFile(uintptr(outRead), "console output")}, nil
}

// start runs bin with args on the console and returns its process.
func (pc *pseudoConsole) start(bin string, args ...string) (syscall.Handle, error) {
	var size uintptr
	procInitializeProcThreadAttributeList.Call(0, 1, 0, uintptr(unsafe.Pointer(&size))) // fails, saying the size
	list := make([]byte, size)
	if ok, _, err := procInitializeProcThreadAttributeList.Call(uintptr(unsafe.Pointer(&list[0])), 1, 0, uintptr(unsafe.Pointer(&size))); ok == 0 {
		return 0, fmt.Errorf("InitializeProcThreadAttributeList: %v", err)
	}
	defer procDeleteProcThreadAttributeList.Call(uintptr(unsafe.Pointer(&list[0])))

	if ok, _, err := procUpdateProcThreadAttribute.Call(uintptr(unsafe.Pointer(&list[0])), 0, procThreadAttributePseudoConsole, pc.handle, unsafe.Sizeof(pc.handle), 0, 0); ok == 0 {
		return 0, fmt.Errorf("UpdateProcThreadAttribute: %v", err)
	}

	line := syscall.EscapeArg(bin)
	for _, a := range args {
		line += " " + syscall.EscapeArg(a)
	}

	cmd, err := syscall.UTF16PtrFromString(line)
	if err != nil {
		return 0, err
	}

	si := startupInfoEx{attributes: &list[0]}
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = startfUseStdHandles
	var pi syscall.ProcessInformation
	if err := syscall.CreateProcess(nil, cmd, nil, nil, false, extendedStartupInfoPresent, nil, nil, &si.StartupInfo, &pi); err != nil {
		return 0, err
	}

	syscall.CloseHandle(pi.Thread)
	return pi.Process, nil
}

// close closes the console, which ends what still runs on it and the
// output pipe's writing, and the input pipe; it may be called more than
// once.
func (pc *pseudoConsole) close() {
	if pc.handle != 0 {
		procClosePseudoConsole.Call(pc.handle)
		pc.handle = 0
		pc.in.Close()
	}
}

// awaitReport waits for the probe's report to end in line, and fails
// when the probe exits first or the deadline passes.
func awaitReport(report, line string, probe syscall.Handle, deadline time.Time) error {
	for time.Now().Before(deadline) {
		if got, _ := os.ReadFile(report); strings.HasSuffix(string(got), line) {
			return nil
		}

		if ev, _ := syscall.WaitForSingleObject(probe, uint32(pollEvery.Milliseconds())); ev == waitObject0 {
			var code uint32
			syscall.GetExitCodeProcess(probe, &code)
			got, _ := os.ReadFile(report)
			return fmt.Errorf("the probe exited with %#x before saying %q: %q", code, line, got)
		}
	}

	return errors.New("the probe never said " + strings.TrimSpace(line))
}
