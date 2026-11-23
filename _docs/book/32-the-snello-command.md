# 32. The snello command

The design of the module file and its `snello` line is decision 6 of `../design/08-packages.md`. The command is `cmd/snello/`, its checks of the system `compiler/toolchain/`, and its version `compiler/version/`.

`snello` is one program that does everything: it starts a module, changes and shows its dependencies, builds, runs and tests a program, checks your system, and says what it is. `snello help` lists the commands, each with a line, and `snello help COMMAND`, or `-h` after a command, prints one command's usage and flags:

```
$ snello help
snello builds, checks and tests Snello programs.

Usage:

	snello COMMAND [ARGUMENTS]

The commands are:

    add        require a dependency by version, its latest release unless one is named
    build      compile a program, a file or a directory, to a binary
    check      check a program, or a package, and print its errors
    debug      build a program for a debugger and run it under one
    deps       print the dependencies by version, and who requires each
    doc        print a package's documentation, or a name's
    doctor     check what a build needs, and say how to fix what is missing
    emit       print the IR, or the C, that build compiles
    fetch      fetch every dependency into the module cache, for a build with no network
    help       print the commands, or a command's help
    init       start a module, writing its snello.mod
    lex        print the tokens of a source file
    lsp        serve an editor the Language Server Protocol on standard input and output
    new        start a module in a new directory, with a program and its test
    parse      print the syntax tree of a source file, with its comments
    remove     take dependencies out of snello.mod, once nothing imports them
    run        build a program, run it with ARGS, and leave no file
    test       build and run the tests of a package
    update     raise dependencies to their latest compatible releases
    version    print the version, commit, channel and platform

Run snello help COMMAND for a command's flags and arguments.
```

A command line given wrongly says how, and what to run for help, and exits 2: `snello: unknown command "x"; run snello help`, or a flag the command does not take followed by its usage line.

## Starting a module

`snello new DIR` makes the directory `DIR`, in one that exists, and starts a module in it: a `snello.mod` holding the version line, a `main.snello` that prints a greeting, and a `main_test.snello` with its test. It prints the commands that enter the directory and build, run and test the module, the directory quoted as a shell reads it. A directory that exists is refused, and one that `new` fails to fill is removed, so it leaves a module whole or nothing.

`snello init` makes the current directory a module, and `snello init DIR` another directory that exists: it writes `snello.mod` with the version line alone, and refuses a directory that has one, with the file's path.

Both write the line `snello 0.1.0`, the version of the compiler that ran them: the oldest release that compiles the module. A compiler older than the line refuses the module and says to upgrade it, and a module file with no line is read as written for 0.1.0, the first release, with a warning on every command that builds, checks or tests it. Chapter 2 and the design say why.

## Dependencies

`snello add NAME URL [VERSION]` requires a module from a git repository, at its latest release or at the version named; `snello remove NAME...` takes requirements out, refusing one a file of the module still imports; `snello update [NAME...]` raises dependencies to their latest compatible releases, and with `-major` past their major versions; `snello deps` prints every dependency with who requires which version, and `snello deps -why NAME` the chain that brings one in; `snello fetch` fills the module cache so that a build needs no network. Each works on the module of the current directory and changes `snello.mod` only when its dependencies still fetch, select and lock after the change, leaving the file as it was otherwise. Chapter 35 is the guide.

## Building and running

`snello build FILE` compiles a program of one file into a binary named after it, in the current directory; `snello build DIR` compiles the program of every `.snello` file in a directory, named after the directory. A binary built for Windows is named with `.exe`, `hello.exe`. `-backend c` builds through the C backend, `-debug` builds for a debugger, unoptimized and with the runtime's checks, which chapter 30 covers, and `-target linux/arm64` builds for another platform, which chapter 2 covers.

`snello run FILE|DIR [ARGS]` builds the program into a directory of its own, runs it, and removes the directory, leaving no file behind. It takes build's `-backend` and `-debug` before the program's path, and builds for the machine it runs on; every word after the path is the program's, flags included, `snello run . -n 3`. The program reads the command's input and writes to its output, and its exit code is the command's: 3 for a program that exits with 3, 2 for one that panics, after the panic's trace. A program that does not build exits 1.

While the program runs, an interrupt or a quit from the terminal, `Ctrl-C` or `Ctrl-\`, reaches the program and ends it; `snello run` waits for it to end. A program that an interrupt ends ends `snello run` by the same interrupt, so that a shell running a script of `snello run` lines stops at it rather than go on to the next. A termination or a hangup sent to `snello run` alone is passed on to the program, which is never left running without it. This is Linux's and macOS's; Windows has no signals, and there `Ctrl-C` and `Ctrl-Break` end the program as Windows ends one, with the exit code `STATUS_CONTROL_C_EXIT`, `0xC000013A`, which `snello run` exits with too (chapter 34).

`snello test DIR` builds the tests of the package in `DIR` and runs them; chapter 16 teaches it. `snello check FILE|DIR` prints what the checker finds, or nothing for a program that is correct, and `snello emit` the IR, or the C, a build compiles; both take `-target`, for the files and the module a build for that platform compiles.

## Reading the documentation

`snello doc` prints the documentation of a package, the comments on its declarations: `snello doc strings` the overview of a standard package, `snello doc strings.Builder` a name with its methods, `snello doc ./geo` or `snello doc` alone a package of the program's own; `-all` prints every name with its comment and `-u` the unexported names too; `snello doc -http` serves the pages to a browser at `http://localhost:6060/`. Chapter 37 has how to write the comments and every form of the command.

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
ok    git: git version 2.47.3
ok    snello on the path: /usr/local/bin
```

Installing clang is all a build needs on Linux and macOS; it brings a C compiler and a linker that takes link-time optimization with it, on each system the fix names. Windows takes Visual Studio's Build Tools beside LLVM, since clang links against the SDK and the C runtime they install:

| System | The command |
|---|---|
| macOS | `xcode-select --install` |
| Debian, Ubuntu | `sudo apt install clang` |
| Fedora | `sudo dnf install clang` |
| Arch | `sudo pacman -S clang` |
| Windows | `winget install LLVM.LLVM`, then `C:\Program Files\LLVM\bin` added to your `Path` and a new window, since the installer adds it to no Path; and Visual Studio's Build Tools with the C++ workload |

The doctor checks `git` too, which a build needs only to fetch a module required by version (chapter 35), so a missing `git` does not fail the doctor; it names the command that installs it: `xcode-select --install` on macOS, which brings it with clang, `sudo apt install git` on Debian and Ubuntu, `sudo dnf install git` on Fedora, `sudo pacman -S git` on Arch, and `winget install Git.Git` on Windows.

On Windows the C compiler is clang as well, and the doctor checks the Build Tools after clang: it builds a program of C that uses the C library, its header and a function of it, as a build compiles against the SDK's headers and links against the C runtime, and a failure prints what clang said, a header or a library not found, with the Build Tools as its fix. A machine whose builds read the SDK and the C runtime from a sysroot, `SNELLO_SYSROOT_windows_amd64` or `_arm64` (Environment, below), needs no Build Tools, and the check says so. The link-time optimization check links with LLVM's `lld-link`, the linker a Windows build uses.

A build, a run or a test that cannot use a compiler says the same line in place of the system's error, `clang not found: install it with xcode-select --install, or set CLANG`. A compiler that is found and does not run is said in its own words: on a Mac with no developer tools, `/usr/bin/clang` is there and only asks for them, `clang cannot run (xcode-select: note: No developer tools were found …)`. The last check, whether running `snello` by name runs this one, fails no build and only notes it.

## Environment

These are the variables `snello` reads. None is needed on a machine with clang installed, and with Visual Studio's Build Tools on Windows.

| Variable | What it names | Without it |
|---|---|---|
| `CLANG` | The clang the LLVM backend compiles with | `clang` on the path |
| `CC` | The C compiler the C backend and a debug build compile with | `cc` on the path; `clang` on Windows, which has no `cc` |
| `SNELLO_CACHE` | The directory the compiled runtime is kept in, or `off` to keep none | the user's cache directory: `~/Library/Caches/snello` on macOS, `$XDG_CACHE_HOME/snello` or `~/.cache/snello` on Linux, `%LocalAppData%\snello` on Windows |
| `SNELLO_MODCACHE` | The directory the modules required by version are fetched into (chapter 35) | `snello-modules` in the user's cache directory, apart from the runtime's cache |
| `SNELLO_SYSROOT_<system>_<processor>` | A target's system files, a sysroot, for a build for it (chapter 2): `SNELLO_SYSROOT_linux_amd64`, `SNELLO_SYSROOT_linux_arm64`, `SNELLO_SYSROOT_windows_amd64` and `SNELLO_SYSROOT_windows_arm64`; read on any system where it is set | the files of the cross packages on Debian and Ubuntu for the other Linux, of Visual Studio's Build Tools on Windows for Windows; anywhere else a build for that target is refused, saying which variable to set |
| `TMPDIR`; on Windows `TMP`, then `TEMP` | The directory builds work in | the system's temporary directory |

`snello env` prints what a build in the current directory reads, one setting a line with its value and where the value came from, so that the question behind most build problems, which clang did it pick, is it using my sysroot, is answered at a glance and can be pasted into a bug report:

```
$ snello env
VERSION                       snello 0.1.0 (8477985) stable darwin/arm64  this binary
TARGET                        darwin/arm64                                this machine; -target names another
CLANG                         /usr/bin/clang                              on the path
CC                            /usr/bin/cc                                 on the path
SNELLO_CACHE                  /Users/ana/Library/Caches/snello            the system's cache directory
SNELLO_MODCACHE               /Users/ana/Library/Caches/snello-modules    beside the runtime's cache
SNELLO_SYSROOT_linux_amd64    not set                                     a build for linux/amd64 reads it when set
SNELLO_SYSROOT_linux_arm64    not set                                     a build for linux/arm64 reads it when set
SNELLO_SYSROOT_windows_amd64  /opt/xwin                                   SNELLO_SYSROOT_windows_amd64
SNELLO_SYSROOT_windows_arm64  not set                                     a build for windows/arm64 reads it when set
TMPDIR                        /var/folders/4m/T                           TMPDIR
MODULE                        /Users/ana/app/snello.mod                   snello 0.1.0
```

A compiler is shown as the file that would run, found on the path or named by its variable, or noted as not found with `snello doctor` to run; a sysroot is listed for each target that takes one, which is why there is none for macOS; the module is the nearest `snello.mod` here or above, with its version line, or none. A module file that does not read is named with why in the source column, so that a build's failure is foreseen here. `snello env NAME` prints one value alone, for a script, `snello env CLANG`, and an empty line for a setting that is not set; `snello env -json` prints every setting as JSON, name to value, for an editor, and `-json NAME` the one named. There is no way to set a value through `snello`: the environment is the one source, so what `env` prints is what a build sees, and nothing is kept in a file to forget. `snello doctor` says whether the toolchain works; `snello env` says what is set.

A message about a compiler says which variable named it, `CC names /opt/cc, not found`. A target's linker, `ld.lld` for Linux and `lld-link` for Windows, is found beside the clang a build runs or on the path, so LLVM's directory need be on the path only for clang itself, unless `CLANG` names it. The target in a sysroot's variable is spelled as `-target` spells it, the slash an underscore.

## Version

`snello version` prints the version, the commit it was built from, the channel and the platform:

```
$ snello version
snello 0.1.0 (8477985) stable darwin/arm64
```

A build from source says `devel` in the channel's place, and `-dirty` after a commit whose tree had changes; a binary built with no record of its commit leaves the commit out.

## For maintainers

The commands are one table, `commands()` in `cmd/snello/commands.go`: each entry has its name, what it takes after its flags and how many arguments, its line, and `declare`, which declares its flags on the `flag.FlagSet` its command line is parsed with and returns the work. The help reads the same set, so the two cannot drift; `run` dispatches, and `TestHelpCommand` holds every declared flag to the help. A new command is a row and a `declare` function, and needs nothing else.

`compile` in `main.go` is the one build path: it loads and checks the program, makes the build's work directory, and has the backend build into it. The work directory is a `workDir`, `cmd/snello/signals.go`, guarded by one goroutine for the command's life: while the build runs, a signal that would end the command removes the directory and ends the command by that signal, through `raise`; once `run` hands it the program, it passes a termination or a hangup on and leaves an interrupt and a quit to the program, which the terminal sends them to as well. `signalSelf` sends the command its ending signal through its process's `Signal`, which Windows refuses, having no signals to send, so there the command ends by the exit code alone. `run` removes the binary's directory when the program ends, not as it starts, since macOS kills a program whose file goes while it is still loading; and it raises a program's ending signal on itself, the rule M. Cracauer's "Proper handling of SIGINT/SIGQUIT" sets for a program that runs another. Their tests run the test binary as a helper process and signal it.

`compiler/toolchain` is what a build needs from the system: `Clang()` and `CC()` name the compilers through `CLANG` and `CC`, `Checks` runs the doctor's checks, each a probe, a module of opaque-pointer IR compiled with `-x ir`, a program linked with `-flto`, a program compiled with the C compiler, and `Install` names the fix from `runtime.GOOS`, `winget install LLVM.LLVM` on Windows, and, on Linux, the `ID` and `ID_LIKE` of os-release. A compiler that cannot be found or does not run is a `*MissingError`: `c.CompilerID`, which every build calls first and which caches by the compiler's file, returns one for both, so the gate costs a build nothing, and `compile` and `testrun` print it with no backend's name before it. The probes replace a version number because Apple numbers its clang its own way: Apple clang 14.0.3 is LLVM 15.

`checkGit` runs `git --version`, and `GitInstall` names its install command the way `Install` names clang's, from `gitInstalls`; its check is not `Needed`, so a missing `git` fails no doctor run. The commands that change and show dependencies, `add`, `remove`, `update`, `deps` and `fetch`, are `cmd/snello/deps.go`; their work is the importer's, `SetRequire`, `DropRequire`, `Rewrite`, `ImportsOf`, `ReleaseVersions`, `Dependencies` and `Why`, which chapter 35 describes for maintainers.

`build/distros/` holds an image of each distribution the fixes name, bare and with clang, and `check.sh`, which runs in each: with clang, doctor, then new, run, run for a debugger and test through both backends; bare, doctor failing with the distribution's command and build, run and test saying that line exactly. `make distros` builds and runs them on the host's Docker; Arch runs as amd64, emulated on an arm64 machine. Run it after changing a check, a fix or the commands it drives; CI runs it on both Linux runners on every change, Arch on the x86-64 one.

The version is `compiler/version`: `Current`, the release being built, which a module's line is checked against; `Describe`, the line `snello version` prints; and `channel`, a variable a release build stamps with the linker, `-ldflags "-X github.com/snello-lang/snello/compiler/version.channel=stable"`, which `TestVersionStamp` builds and reads back.
