# 32. The snello command

The design of the module file and its `snello` line is decision 6 of `../design/08-packages.md`. The command is `cmd/snello/`, its checks of the system `compiler/toolchain/`, and its version `compiler/version/`.

`snello` is one program that does everything: it starts a module, builds, runs and tests a program, checks your system, and says what it is. `snello help` lists the commands, each with a line, and `snello help COMMAND`, or `-h` after a command, prints one command's usage and flags:

```
$ snello help
snello builds, checks and tests Snello programs.

Usage:

	snello COMMAND [ARGUMENTS]

The commands are:

    build      compile a program, a file or a directory, to a binary
    check      check a program, or a package, and print its errors
    debug      build a program for a debugger and run it under one
    doctor     check what a build needs, and say how to fix what is missing
    emit       print the IR, or the C, that build compiles
    help       print the commands, or a command's help
    init       start a module, writing its snello.mod
    lex        print the tokens of a source file
    new        start a module in a new directory, with a program and its test
    parse      print the syntax tree of a source file
    run        build a program, run it with ARGS, and leave no file
    test       build and run the tests of a package
    version    print the version, commit, channel and platform

Run snello help COMMAND for a command's flags and arguments.
```

A command line given wrongly says how, and what to run for help, and exits 2: `snello: unknown command "x"; run snello help`, or a flag the command does not take followed by its usage line.

## Starting a module

`snello new DIR` makes the directory `DIR`, in one that exists, and starts a module in it: a `snello.mod` holding the version line, a `main.snello` that prints a greeting, and a `main_test.snello` with its test. It prints the commands that enter the directory and build, run and test the module, the directory quoted as a shell reads it. A directory that exists is refused, and one that `new` fails to fill is removed, so it leaves a module whole or nothing.

`snello init` makes the current directory a module, and `snello init DIR` another directory that exists: it writes `snello.mod` with the version line alone, and refuses a directory that has one, with the file's path.

Both write the line `snello 0.1.0`, the version of the compiler that ran them: the oldest release that compiles the module. A compiler older than the line refuses the module and says to upgrade it, and a module file with no line is read as written for 0.1.0, the first release, with a warning on every command that builds, checks or tests it. Chapter 2 and the design say why.

## Building and running

`snello build FILE` compiles a program of one file into a binary named after it, in the current directory; `snello build DIR` compiles the program of every `.snello` file in a directory, named after the directory. `-backend c` builds through the C backend, `-debug` builds for a debugger, unoptimized and with the runtime's checks, which chapter 30 covers, and `-target linux/arm64` builds for another platform, which chapter 2 covers.

`snello run FILE|DIR [ARGS]` builds the program into a directory of its own, runs it, and removes the directory, leaving no file behind. It takes build's `-backend` and `-debug` before the program's path, and builds for the machine it runs on; every word after the path is the program's, flags included, `snello run . -n 3`. The program reads the command's input and writes to its output, and its exit code is the command's: 3 for a program that exits with 3, 2 for one that panics, after the panic's trace. A program that does not build exits 1.

While the program runs, an interrupt or a quit from the terminal, `Ctrl-C` or `Ctrl-\`, reaches the program, which answers it; `snello run` waits for it to end. A program that an interrupt ends ends `snello run` by the same interrupt, so that a shell running a script of `snello run` lines stops at it rather than go on to the next. A termination or a hangup sent to `snello run` alone is passed on to the program, which is never left running without it.

`snello test DIR` builds the tests of the package in `DIR` and runs them; chapter 16 teaches it. `snello check FILE|DIR` prints what the checker finds, or nothing for a program that is correct, and `snello emit` the IR, or the C, a build compiles; both take `-target`, for the files and the module a build for that platform compiles.

## Checking your system

A build needs clang, version 15 or later, which compiles the IR Snello writes; a link with link-time optimization, which a release build links with; and a C compiler, which the C backend and a debug build compile with. `snello doctor` checks each, by trying it as a build uses it, and prints a line for each, with the fix under one that failed; it exits 1 when a build would fail:

```
$ snello doctor
fail  clang: clang not found
      install it with sudo apt install clang, or set CLANG
fail  link-time optimization: not tried without clang
      install clang first
fail  C compiler: cc not found
      install it with sudo apt install clang, or set CC
ok    snello on the path: /usr/local/bin
```

Installing clang is all a build needs; it brings a C compiler and a linker that takes link-time optimization with it, on each system the fix names:

| System | The command |
|---|---|
| macOS | `xcode-select --install` |
| Debian, Ubuntu | `sudo apt install clang` |
| Fedora | `sudo dnf install clang` |
| Arch | `sudo pacman -S clang` |

A build, a run or a test that cannot use a compiler says the same line in place of the system's error, `clang not found: install it with xcode-select --install, or set CLANG`. A compiler that is found and does not run is said in its own words: on a Mac with no developer tools, `/usr/bin/clang` is there and only asks for them, `clang cannot run (xcode-select: note: No developer tools were found …)`. The last check, whether running `snello` by name runs this one, fails no build and only notes it.

## Environment

`CLANG` names the clang to use, and `CC` the C compiler; a message about either says which variable named it. `SNELLO_CACHE` names the directory the compiled runtime is kept in, `off` for none, and `TMPDIR` the directory builds work in. `SNELLO_SYSROOT_linux_amd64` and `SNELLO_SYSROOT_linux_arm64` name the system files of each Linux target, a sysroot, which a build for it on another system reads (chapter 2).

## Version

`snello version` prints the version, the commit it was built from, the channel and the platform:

```
$ snello version
snello 0.1.0 (8477985) stable darwin/arm64
```

A build from source says `devel` in the channel's place, and `-dirty` after a commit whose tree had changes; a binary built with no record of its commit leaves the commit out.

## For maintainers

The commands are one table, `commands()` in `cmd/snello/commands.go`: each entry has its name, what it takes after its flags and how many arguments, its line, and `declare`, which declares its flags on the `flag.FlagSet` its command line is parsed with and returns the work. The help reads the same set, so the two cannot drift; `run` dispatches, and `TestHelpCommand` holds every declared flag to the help. A new command is a row and a `declare` function, and needs nothing else.

`compile` in `main.go` is the one build path: it loads and checks the program, makes the build's work directory, and has the backend build into it. The work directory is a `workDir`, `cmd/snello/signals.go`, guarded by one goroutine for the command's life: while the build runs, a signal that would end the command removes the directory and ends the command by that signal, through `raise`; once `run` hands it the program, it passes a termination or a hangup on and leaves an interrupt and a quit to the program, which the terminal sends them to as well. `run` removes the binary's directory when the program ends, not as it starts, since macOS kills a program whose file goes while it is still loading; and it raises a program's ending signal on itself, the rule M. Cracauer's "Proper handling of SIGINT/SIGQUIT" sets for a program that runs another. Their tests run the test binary as a helper process and signal it.

`compiler/toolchain` is what a build needs from the system: `Clang()` and `CC()` name the compilers through `CLANG` and `CC`, `Checks` runs the doctor's checks, each a probe, a module of opaque-pointer IR compiled with `-x ir`, a program linked with `-flto`, a program compiled with the C compiler, and `Install` names the fix from `runtime.GOOS` and, on Linux, the `ID` and `ID_LIKE` of os-release. A compiler that cannot be found or does not run is a `*MissingError`: `c.CompilerID`, which every build calls first and which caches by the compiler's file, returns one for both, so the gate costs a build nothing, and `compile` and `testrun` print it with no backend's name before it. The probes replace a version number because Apple numbers its clang its own way: Apple clang 14.0.3 is LLVM 15.

`build/distros/` holds an image of each distribution the fixes name, bare and with clang, and `check.sh`, which runs in each: with clang, doctor, then new, run, run for a debugger and test through both backends; bare, doctor failing with the distribution's command and build, run and test saying that line exactly. `make distros` builds and runs them on the host's Docker; Arch runs as amd64, emulated on an arm64 machine. Run it after changing a check, a fix or the commands it drives.

The version is `compiler/version`: `Current`, the release being built, which a module's line is checked against; `Describe`, the line `snello version` prints; and `channel`, a variable a release build stamps with the linker, `-ldflags "-X github.com/snello-lang/snello/compiler/version.channel=stable"`, which `TestVersionStamp` builds and reads back.
