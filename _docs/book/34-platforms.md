# 34. Platforms

The design is `../design/12-platforms.md`, which says why each platform is supported as it is; this chapter says what that means for a program. Building for another platform is chapter 2's, and the variables a build reads are chapter 32's.

A Snello program is built for a platform, a system and a processor, and runs there with nothing installed beside it. The language is the same on every platform, and so is the checker: a program that compiles for one compiles for every other, but for the files a package writes for one system alone. What differs is what the system underneath does, and this chapter says where a program sees it.

## The platforms

Snello builds for three systems, Linux, macOS and Windows, each on two processors, amd64 and arm64, named as Go names them. A platform's tier says what is promised of it:

- **Tier 1**: built and tested on every change, and a failure blocks the change.
- **Tier 2**: built on every change, its tests run where a machine for it exists, and a failure is fixed before the next release.

| Platform | Tier | Tested on |
|---|---|---|
| `linux/amd64` | 1 | Debian 13, on GitHub's x86-64 Linux machine |
| `linux/arm64` | 1 | Debian 13, on GitHub's arm64 Linux machine |
| `darwin/arm64` | 1 | GitHub's macOS arm64 machine |
| `darwin/amd64` | 1 once its tests pass | GitHub's Intel macOS machine, `macos-15-intel`, its job not blocking until they do |
| `windows/amd64` | 1 | GitHub's Windows x86-64 machine |
| `windows/arm64` | 2 | GitHub's Windows arm64 machine, its tests run and not blocking |

A platform holds its tier once its tests run and pass there. Both Windows jobs run the suite natively, `go test ./...`, `make test` and `make check`, and pass, the tests of what the last section lists as not built on Windows skipped there.

## The oldest version of each system

A program is built to run on these versions and every later one:

| System | Oldest version | Why |
|---|---|---|
| Linux | kernel 5.4, with glibc 2.29 | a child process's pidfd and the wait on it, and a child started in a directory of its own, `posix_spawn_file_actions_addchdir_np` |
| macOS | 14.4 | the waits on an address the scheduler and the locks are built on |
| Windows | Windows 10, Windows Server 2016 | the system's random source, `ProcessPrng`; a deadline is kept by the high-resolution timer from Windows 10 1803, and on an earlier version comes on the system's tick of about 16 ms |

Every macOS build names 14.4 as the oldest version it runs on, whichever macOS built it. A Linux program built against a sysroot runs on the Linux the sysroot came from and on any with that C library or a later one.

## Building for another platform

`snello build -target windows/amd64 hello.snello` builds the program for another platform from the one you are on, and a program built for Windows from any system is named with `.exe`, `hello.exe`. What each pair of systems needs, the linker and the target's system files, is in chapter 2's "Building for another platform", and the variables that name them in chapter 32's "Environment". A program built for Windows carries Microsoft's C runtime inside it, so it needs nothing installed beside it.

## File names for a platform

A package that does something differently on each system writes a file for each and names it for its platform, `poll_linux.snello`, `poll_darwin.snello`, `poll_windows.snello`, with `poll.snello` beside them for every platform. `poll_unix.snello` is built for every Unix, Linux and macOS, and not for Windows, so code the two share is one file, beside the one for Windows:

```
poll/
    poll.snello            every platform
    poll_unix.snello       Linux and macOS
    poll_windows.snello    Windows
```

A file named for a system Snello does not build for, `poll_freebsd.snello`, is left out. The same names choose a package's C files, chapter 15. The full rule, a processor in the name and the words a name may end in, is chapter 2's section on packages.

## What differs on Windows

A program does not ask which system it runs on to do the ordinary things: the standard library does them the way the system expects. These are the places where a program sees Windows.

- **The binary** is `NAME.exe`. `snello run` and `snello test` build and run it under that name.
- **Paths.** Windows separates a path's elements with `\` and takes `/` as well, and begins a path with its volume, a drive, `C:`, a share, `\\server\share`, or a device's path, `\\?\C:`. `path/filepath` follows the system: `filepath.Separator` is `\`, `Clean` and `Join` write `\`, both separators split, and `filepath.VolumeName` gives the volume, which `Dir` and `Clean` keep in front. The `os` package reads a path the same way, and what it gives back, `os.Getwd()` and `os.TempDir()`, is written with `\`. `path`, for the paths of URLs, keeps `/` on every system. Chapter 14 has both packages.
- **A path's length.** A path of 248 UTF-16 units or more, about that many characters, is passed to the system in its long form, so that a path of up to 32,767 is taken whether or not the machine enables long paths.
- **Permissions.** Windows keeps no permission bits and has no umask. A file made without the owner's write bit, `0o444`, is read-only, and the rest of the bits given are not kept; a file's mode read back is `0444` for a read-only file, `0666` for any other, and `0777` for a directory, `0555` for a read-only one, so `fi.Mode()` prints `-rw-rw-rw-` for an ordinary file.
- **The console.** The standard streams on a console are read and written as the console's own text, so what the program prints shows as it is written, accents and all, whatever the console's code page, which the program leaves as it found it. A line typed at the console that starts with Ctrl-Z ends the input, as Ctrl-D does on a terminal elsewhere. Redirected to a file or a pipe, the streams carry the program's bytes as they are.
- **The interrupt.** Windows has no signals: Ctrl-C and Ctrl-Break are `signal.Interrupt`, and the console's close, the logoff and the shutdown `signal.Terminate`, delivered as on Linux and macOS, chapter 36; a program that asks for none is cancelled by them as a deadline cancels it, and ends with the exit code `STATUS_CONTROL_C_EXIT`, `0xC000013A`, as Windows ends a program Ctrl-C ended. A read of the console that Ctrl-C interrupts fails as a cancelled read does when the program was cancelled, and is made again otherwise, never taken for the end of the input. A child's `Terminate` is its `Kill`, since there is no signal to send, and its `Status` never has a signal. `snello run` exits with the program's code; chapter 32 says what it does with an interrupt on Linux and macOS.
- **The environment.** A variable's name is matched with no regard to its case, `Path` and `PATH` being one variable, as Windows matches it.
- **Temporary files.** `os.TempDir()` is the directory Windows names for them, from `TMP`, `TEMP` or the user's profile, where elsewhere it is `TMPDIR` or `/tmp`.
- **Processors.** `os.Cores()` counts the processors in the process's affinity mask, which a job object or `start /affinity` may narrow, within the processor group the process runs in, a group being at most 64 processors.
- **Binding C.** `link m` and `link c` in `snello.mod` link nothing more on Windows, where the C runtime holds the C library and its mathematics in one, so a module that names them builds on every system. A package's C for Windows is compiled by clang against Microsoft's headers, `shim_windows.c`.
- **A task's stack** is bounded as it is elsewhere, from the program's binary, a call into Windows' system libraries or its C runtime counted at the stack it was measured to use on Windows; on arm64, a tier-2 platform whose figures no failing test blocks a change on, a task that calls into them runs on the large guarded stack. A task the compiler cannot bound runs on the large guarded stack, 256 KB, which Windows sets aside in full against its commit limit, where Linux and macOS set aside only what is touched: a program of a hundred thousand such tasks at once asks for some 25 GB, and a spawn past the limit panics, `cannot reserve a task's stack: the system's commit limit is reached`.
- **The debugger** is `cdb`, Windows' own, which reads a debug build's PDB, `prog.pdb`, with the visualizers that show the program's values as it writes them inside it, and `snello debug` loads Snello's script into it, `!tasks` listing the tasks; WinDbg and Visual Studio read the same PDB (chapter 30).
- **A failure's message** reads as it does elsewhere: a Windows failure is reported as the error the same failure is on Linux and macOS, so `io.NotExist` and `open C:\x\notes.txt: no such file or directory` read alike everywhere.

## What is not built on Windows yet

- **A profiler** is not described for Windows yet; chapter 30 covers `perf` and Instruments.
- **`!tasks`** in `cdb` lists each task's state and not yet a waiting task's frames; on arm64 the SDK's `cdb` has no provider of scripts, so `!tasks` is not available there (chapter 30).

## For maintainers

- **The system layer** (`runtime/c/system.h`): the threads, locks and conditions the runtime calls, each inlined to the POSIX call on Linux and macOS, so that their code is what it was, and to Windows' own on Windows, `CreateThread`, the slim reader-writer lock and the condition variable; a wait on an address is `WaitOnAddress`. A one-time setup is the runtime's own on every system, `sn_once` in `internal.h`, on that wait, so that the stack analysis follows the call of the setup, which a system's once makes through a pointer. The rule for code that differs by system is in `CODING_STANDARDS.md`: under `#if defined(_WIN32)`, or whole in `os_windows.c`, since one list of files compiles for every target.
- **Files and the console** (`runtime/c/os_windows.c`): every path converted from UTF-8 to UTF-16 at the call and given the long form past 248 UTF-16 units, a file opened by `CreateFileW` with full sharing and kept as a C library descriptor, at most 8,192 at once, read and written straight through its handle; a failure translated to the error number the other systems give it, `sn_errno_of`; the console read and written through `ReadConsoleW` and `WriteConsoleW`, a read that Ctrl-C interrupts, which succeeds with nothing read, made again rather than taken for the end; the environment from `GetEnvironmentStringsW` and the arguments from `GetCommandLineW`.
- **The switch** (`sn_switch` in `runtime/c/sched.c`): Windows x64's callee-saved registers, `xmm6` to `xmm15` among them, and arm64's without `x18`; on both it saves and restores the stack's bounds in the thread's information block, `StackBase`, `StackLimit` and `DeallocationStack`, which Windows reads when `__chkstk` probes a large frame and when it unwinds. A task's stack is a `VirtualAlloc` reservation with a guard page of no access.
- **The poller and the keeper** (`runtime/c/net.c`, `sched.c`): a completion port, each socket polled through AFD for the direction a task is about to wait on, each poll a block from the C library's allocator that the port gives back when it completes, so that the readiness model of the other systems holds; the keeper waits on the same port for its wake and for a high-resolution waitable timer the system posts to it, and gives back a packet that is not its own. A name is looked up by `GetAddrInfoW` on the lookup threads, `lookup.c`, its failure's text formatted for each call.
- **The trace** (`compiler/emit/trace/trace.go`, `coff.go`): the table is read from a first link's image with its DWARF, `ImageTable`, anchors for the functions the program's own objects define, and its object written as COFF; `Link` in `compiler/emit/c/build.go` has the Windows link, `lld-link` with `/debug:dwarf`, `/lldsavetemps:prelink` and `/debug:symtab`.
- **The calling convention** (`compiler/emit/llvm/abi.go`): `targetABI` gives each platform's way of passing the runtime's three-word struct and its narrow scalars, `TestABIMatchesClang` holding each to clang.
- **The toolchain** (`compiler/emit/c/target.go`, `compiler/toolchain`): `CheckTarget` and `checkWindows` say what a Windows build lacks, the sysroot's headers and libraries are `compileFlags`' and `linkerFlags`', and `CC` is clang on Windows; `compiler/platform` reads a file's name for its platform, `_unix` included.
- **The tests**: `TestCrossBuild` in `cmd/snello/main_test.go` builds `examples/bound` and `examples/pipeline` for Windows where a Windows sysroot is named and checks each is a PE binary for its processor; `os_windows_test.snello` and `filepath_windows_test.snello` hold the library's Windows behaviour and run on Windows alone, and `TestConsole`, `runtime/os_windows_test.go`, runs a program on a pseudo console, as a terminal runs one, types at it and presses Ctrl-C. The runtime's probes, C programs `runtime/runtime_test.go` builds against the runtime, are one source for every system: what they need of the system beyond the runtime, a thread to wait for, a pipe, the processor time used, an alarm and a client's socket, is `probe.h`'s, written beside each, POSIX's calls or Windows' under it. A probe still running at its deadline, a lost wake, is reported with each of its threads' stacks before it is ended, as the system's debugger reads them: `cdb` from Windows' SDK, which reads the CodeView a probe is built with there, `lldb` on macOS and `gdb` on Linux. CI's `windows-latest` and `windows-11-arm` jobs in `.github/workflows/test.yml` run `go test ./...`, `make test` and `make check` natively, `make` with `NATIVE=1` under Git's Bash.
