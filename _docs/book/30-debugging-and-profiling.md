# 30. Debugging and profiling

The design is the section "Debugger information" of `../design/07-lowering.md`; chapter 17 shows what a panic prints, which is often all a bug needs. The sessions below are cut from `TestDebugger` in `cmd/snello/debug_test.go`, which runs them through both backends with lldb on macOS and with gdb on Linux.

A Snello program is debugged with the debuggers C and Rust programmers use, lldb and gdb, and profiled with the profilers they use, `perf` and Instruments. The compiler writes the standard debugging information those tools read, the same that clang and gcc write, so nothing about them changes but the names they show.

## Which debuggers

Snello supports two debuggers, **lldb** and **gdb**, the debuggers of the C toolchains Snello builds with:

| System | Processor | Debugger `snello debug` starts | Tested |
|---|---|---|---|
| macOS | arm64 (Apple silicon) | lldb, the one Apple's developer tools ship, Xcode's or its command-line tools' | every session in this chapter, through both backends |
| macOS | amd64 (Intel) | lldb, as on arm64 | not tested: the CI's macOS machines are arm64 |
| Linux | arm64 | gdb, with its Python support: Debian's and Ubuntu's `gdb` package, not `gdb-minimal` | every session in this chapter, through both backends, and with the C backend compiled by gcc as well as clang |
| Linux | amd64 | gdb, as on arm64 | as on arm64 |
| Windows | amd64, arm64 | cdb, which Windows' SDK installs with the Build Tools; WinDbg, its window, reads the same | every session in this chapter, through both backends, on CI's Windows machines |

Each debugger reads the debugging information on its own, so breakpoints, stepping, the backtrace and printing variables work in any version that reads DWARF. Snello's support, the formatted values and `tasks`, is a Python script that both debuggers load, and it needs a debugger built with Python, as Apple's lldb and Debian's gdb are. lldb on Linux reads the same information and loads the same script, but `snello debug` starts gdb there and the tests do not run lldb on Linux. Delve is not supported, since it reads only Go programs. Windows' own debuggers, `cdb`, WinDbg and Visual Studio's, read CodeView from a PDB instead of DWARF, and show Snello's values by visualizers, a natvis file, which a Windows debug build writes from the program's own types and the linker puts in the PDB, so that every one of them shows the values with nothing loaded; the `tasks` command is a script of theirs, `snello.js`, which `snello debug` loads into `cdb` as `!tasks`; on Windows it lists each task's state and not yet a waiting task's frames, and the SDK's `cdb` for arm64 ships without the provider of scripts, so `!tasks` is not available there. An editor's debugger that drives lldb or gdb, such as VS Code's CodeLLDB or its C/C++ extension, shows what the debugger shows, but loads Snello's script only when told to, as below.

## A build for the debugger

`snello build -debug` builds a program for a debugger. Your own code, your packages' with your program's, is compiled with no optimization, so that every line has its instructions and every variable is kept for its whole block; the standard library is optimized, as Rust's is in a debug build, and the runtime's checks are compiled in. Through the C backend, the reference implementation, the standard library is compiled unoptimized too (the maintainers' section says why). A release build is no use to a debugger: the optimizer writes small functions into their callers, folds what it can work out, and keeps a variable only while it needs it, so that a breakpoint at a line may find no code there at all.

```
$ snello build -debug prog
```

On Linux the debugging information is in the binary. On Windows it is in `prog.pdb` beside `prog.exe`, the visualizers of the program's values in it. On macOS the linker leaves it in the objects it links, which the build removes, so the build gathers it first into `prog.dSYM` beside the binary, as `clang -g` does; lldb finds the bundle there and takes it when it matches the binary.

## One command: `snello debug`

`snello debug` builds the program the same way and starts the debugger on it: lldb on macOS, gdb on Linux, `cdb` on Windows, from the path or from where Windows' SDK installs it. Anything after the program's path is passed to the program:

```
$ snello debug prog --verbose input.txt
```

It also loads Snello's support into the debugger, which shows values as the program writes them and adds the command `tasks`. A debugger loads no such script from a binary on its own, which is why the command does it, as Rust's `rust-lldb` and Go's Delve do. With `snello build -debug` and a debugger started by hand, everything below works but the formatted values and `tasks`, which the script adds once it is loaded: `command script import PATH/snello.py` in lldb, `source PATH/snello.py` in gdb. `snello debug` writes the script, the first time it runs, into a directory named by its version under `debug/` in Snello's cache directory, which chapter 32 names for each system, or the directory `SNELLO_CACHE` names; it is also `cmd/snello/snello.py` in a checkout of the repository.

## Stopping, stepping and printing

A breakpoint is set at a line of a file, and the debugger stops there with the source around it. In lldb:

```
(lldb) breakpoint set -f main.snello -l 19
(lldb) run
* thread #1, stop reason = breakpoint 1.1
    frame #0: prog`sum(n=7) at main.snello:19
   18  	    for i := 0; i < n; i++ {
-> 19  	        total += i
   20  	    }
(lldb) frame variable
(sn_int) n = 7
(sn_int) total = 0
(sn_int) i = 0
(lldb) next
    frame #0: prog`sum(n=7) at main.snello:18
(lldb) bt
  * frame #0: prog`sum(n=7) at main.snello:18
    frame #1: prog`main at main.snello:51
```

In gdb the same session is `break main.snello:19`, `run`, `info args`, `info locals`, `next` and `bt`.

Every local and parameter is shown under its own name, in the block that declares it, so the `j` of a second loop is `j` and not the first loop's. A closure's captured variables are shown under their own names inside the closure through the LLVM backend; through the C backend they are reached through the closure's record, `sn_env->count`.

A function of your own is shown as your program names it, as a panic's trace names it: `sum`, a method `Grid.At`, a closure `main.func1`, a function of your own package `geo.Area`. A breakpoint on a function is set by that name, `breakpoint set -n sum` in lldb and `break sum` in gdb, and a breakpoint at a line needs no name. Two kinds of function are shown by their symbol, the name each has in the binary: a function of the standard library, `sn_fmt_Println`, which the optimizer may have written into its caller, and every function of a program built with the C backend, since C gives a function one name: `sn__sum`, `sn_main` for `main`, a method its type's name and its own, a function of a package the package's path and its name. A profiler shows symbols too.

## Values as the program writes them

Snello's support shows a value as the program writes it, in either debugger and through either backend:

```
(lldb) frame variable
(Point) p = {
  x = 3
  y = 4
}
(sn_string) name = "snello"
(sn_opt_int) o = Some(5)
(sn_slice_int) xs = len=3
(Shape) sh = Rect(2, 3)
(sn_res_int_string) r = Ok(4)
```

A string is its text; a slice its length, and its elements when it is opened, `frame variable xs` in lldb, which gdb prints at once, `xs = len=3 = {1, 2, 3}`; an option `Some(v)` or `None`; a result `Ok(v)` or `Err(e)`; an enum its case and fields; a map its count. A value of a struct is its fields. The types are named as the C backend names them, `sn_int`, `sn_opt_int`, `sn_slice_int`, and both backends describe their members alike. Without the support a debugger still names an enum's case, `tag = sn_case_5_Shape_Rect`, the length of the enum's name before it, and shows the rest as their parts: a string as its `ptr`, `len` and `cap`.

A string's text is read up to the debugger's own limit for a summary, and a slice's elements up to its limit for children, so that a value not yet set, whose length is any number, never reads more than that.

## Tasks

`tasks` lists every task of the program with its state, and the stack of each task that waits, in the form a panic's trace has:

```
(lldb) tasks
task 1: running
task 2: parked
	wait at main.snello:26
```

A running task's stack is its thread's, which the debugger's own `bt` shows, `thread backtrace all` in lldb and `thread apply all bt` in gdb. A task that has not started yet is listed with the function it will run. `tasks` reads a list a debug build keeps of its tasks, and a release build does not, so it lists nothing in a release build.

## The standard library's source

The standard library is part of the compiler, with no copy on disk, and the debugging information names its files under `/snello/std/`, a place no file of yours is. `snello debug` writes the library the program was built with into the cache directory and points the debugger there, so that a step into `fmt.Println` shows its source. A debugger started by hand can be pointed at a checkout of the repository: `settings set target.source-map /snello/std CHECKOUT/std` in lldb, `set substitute-path /snello/std CHECKOUT/std` in gdb. A step into the standard library shows optimized code, as a step into Rust's does: a line may be passed over, and a variable may be gone where its value is in no register.

## Profiling

A profiler samples where a program spends its time, and is best run on a release build, which is the code that ships. `perf` on Linux walks the stacks by their frame pointers, which every Snello function that calls another keeps:

```
$ snello build prog
$ perf record --call-graph fp ./prog
$ perf report
```

On macOS, Instruments' Time Profiler does the same, from Xcode or from the command line with `xcrun xctrace record --template 'Time Profiler' --launch -- ./prog`, which needs Xcode itself and not only its command-line tools. No profiler is described for Windows yet.

A release build's profile names functions by their symbols, as the debugger does, and shows no lines: a release build keeps no line information for a profiler, as Rust's release builds keep none unless asked. Its optimizer writes small functions into their callers, so their time is their caller's. A profile of a debug build shows every function and line, but of unoptimized code, whose time is not the release build's.

## For maintainers

- **The build** (`compiler/emit/c/build.go`, `compiler/emit/llvm/build.go`): a debug build compiles the C backend's program and a package's C with `ProgramFlags`, which end in `-O0 -g`, and the LLVM backend's module optimized, without link-time optimization, with the program's own functions, of its main package and its own packages, marked `optnone noinline`, and `DebugFlags`, which keep a panic's trace exact through the standard library's optimized code; the runtime keeps `BuildFlags`, optimized and with no debugging information, and its entry in the cache. The C backend leaves the whole program unoptimized because clang, told to leave one function of an optimized file alone, joins a multiply and an add despite `-ffp-contract=off`, which the shared program `multiply-then-add` catches. `Bundle` runs `dsymutil` on macOS; `snello build -debug` calls it before the build's directory is removed. `__memset_chk`, which unoptimized C calls where optimized C writes stores, is among the C library's functions the stacks' analysis measures (`compiler/emit/stack/testdata/libc.c`), and a channel's element drop is named apart from a map's (`lower.ChanDrop`), so that the analysis takes the channel's drop for a call of the channels' element types' drops alone.
- **The LLVM backend** (`compiler/emit/llvm/debug.go`): a debug build's unit is `FullDebug`; `local` and `parameter` declare each slot with `llvm.dbg.declare`; a closure's captures are declared on a slot holding its box's address with `DW_OP_deref` and `DW_OP_plus_uconst`; each frame past a function's first has a lexical block, which `scopeNow` gives the locations; a function of the program's own is named as the program names it (`subprogramNode`), whose symbol the trace's table reads from the object's symbol table at the function's address (`rows` in `compiler/emit/trace/trace.go`); a parameter's store into its slot is at no line, so that a breakpoint on the function stops after it; each function has its type; the helpers are artificial, at line zero; and `debugType` describes each type under the C backend's names, from the sizes and alignments the backend lays values out by, the C names in `cBases` and `cNames`.
- **The C backend** (`compiler/emit/c`): the directives name a file by `lower.SourceName`, the standard library's under `lower.StdSources`; a local is renamed only while a local of its name is in scope (`locals.go`), every frame being a C block; an enum's tag is an enumeration of `lower.CaseName`s, switched on and subtracted as an `int`; the runtime's header marks its inline functions `SN_NODEBUG`.
- **The runtime** (`runtime/c/sched.c`): a debug build links each task onto `debug_tasks` at its spawn and unlinks it as its record is freed, so that the list holds only records still held, and publishes `sn_debugger`, a `sn_debugger_table` of the list's address, the offsets in a task's record, the saved frame's offsets `SN_SAVED_FP` and `SN_SAVED_PC` beside the switch's assembly, and the states' values.
- **The script** (`cmd/snello/snello.py`): embedded in the compiler and written with the standard library's sources to `debug/` in the cache directory under a hash of both (`debugSupport` in `cmd/snello/debug.go`); its lldb half registers summaries, a slice's children and the `tasks` command; its gdb half registers printers, `tasks`, and a skip of the runtime's header.
- **Tests**: `TestDebugger` drives lldb or gdb over a program built through both backends, with gcc too on Linux; `TestDebugInformation`, `TestReleaseNotDescribed` and `TestDebugTypesMatchC` in the LLVM backend, the last reading both backends' objects with Go's reader of the format; `TestLocalNames` in the C backend; `TestDebuggerTable` in the runtime; `TestTableAndPatch` reads a table from an unoptimized object; `TestBundle`, `TestProgramFlags`, `TestDebugSupport` and `TestDebuggerCommand`.
