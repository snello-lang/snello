# 2. Getting started

## The compiler

The compiler is a single program, `snello`, which carries the runtime and the standard library inside it. One line installs it from the latest release and, when a build still needs clang, offers to install that too:

```sh
curl -fsSL https://raw.githubusercontent.com/snello-lang/snello/main/release/install.sh | sh      # Linux and macOS
irm https://raw.githubusercontent.com/snello-lang/snello/main/release/install.ps1 | iex           # Windows, in PowerShell
```

[Chapter 38](38-installing.md) has every way to install, by hand from a release with its verification, from source with Go, what a build needs on each system, and upgrading and uninstalling by each way.

`snello version` says which compiler it is: the version, the commit it was built from, the channel a release is published on or `devel` for a build from source, and the system it runs on.

```
$ snello version
snello 0.1.0 (8477985) devel darwin/arm64
```

`snello help` lists every command with a line saying what it does, and `snello help build` the flags and arguments of one, as `go help build` does; `-h` after a command prints the same.

`snello doctor` checks what a build needs from your system, by trying each thing as a build uses it: clang, which compiles what Snello writes and must be version 15 or later; a link with link-time optimization, which a release build links with; a C compiler, which the C backend and a debug build compile with; and, apart from the build, whether `snello`'s directory is on your path. A check that fails prints the command that fixes it on your system, and the command exits 1 when a build would fail:

```
$ snello doctor
ok    clang: Debian clang version 19.1.7 (3+b1)
ok    link-time optimization: clang links with -flto
ok    C compiler: Debian clang version 19.1.7 (3+b1)
ok    snello on the path: /usr/local/bin
```

Installing clang is all a build needs on Linux and macOS: `xcode-select --install` on macOS, `sudo apt install clang` on Debian and Ubuntu, `sudo dnf install clang` on Fedora, `sudo pacman -S clang` on Arch; each brings a C compiler and a linker that takes link-time optimization with it. On Windows a build takes LLVM, `winget install LLVM.LLVM`, which brings clang and its linker, and Visual Studio's Build Tools with the C++ workload, which hold Microsoft's SDK and C runtime; clang finds them by itself. A build that cannot start clang says the same, `clang not found: install it with sudo apt install clang, or set CLANG`; `CLANG` and `CC` name the compilers to use instead. Chapter 32 has every command in full.

## A first module

`snello new` starts a program in a directory of its own: the module file, a program that prints a greeting, and its test.

```
$ snello new hello
snello new: wrote hello, a module with a program and its test:
	cd hello
	snello build .
	snello run .
	snello test .
$ cd hello
$ snello run .
Hello, Snello!
$ snello test .
ok  	hello	0.001s
```

The directory holds three files: `snello.mod`, the line `snello 0.1.0`, which the section on packages comes to; `main.snello`, the program; and `main_test.snello`, its test, which chapter 16 teaches.

```go
package main

import "fmt"

// greeting returns the line main prints for name.
func greeting(name ref string) string {
    return "Hello, " + name + "!"
}

func main() {
    fmt.Println(greeting("Snello"))
}
```

`snello run` builds the program into a directory of its own, runs it, and removes it, leaving no file behind: the arguments after the program's path are the program's, `snello run . -n 3`, and its exit code is the command's, 3 for a program that exits with 3. An interrupt from the terminal reaches the program and ends it; a program the interrupt ends ends `snello run` the same way, so a script that runs it stops too. On Windows, which has no signals, `Ctrl-C` ends the program as Windows ends one, and `snello run` exits with the program's code (chapter 34). `snello build` writes the binary instead, named after the directory, `./hello`. `snello new` refuses a directory that exists, and makes the directory alone, in one that must exist; `snello init`, below, makes a directory that exists a module.

## Hello

A program need not be a module: a single file in a package `main` with a `func main()` builds and runs on its own.

```go
package main

import "fmt"

func main() {
    fmt.Println("hello, snello")
}
```

Save it as `hello.snello` and build it:

```
snello build hello.snello
./hello
```

`snello build` writes a native executable named after the file, in the current directory, or after the directory when given one, with `.exe` when it is built for Windows: `snello build DIR` builds every `.snello` file in it as one program, which the section on packages below comes to. The file name must end in `.snello`. More programs are under `examples/` in the repository, each with the output it prints beside it. The binary contains Snello's runtime and nothing else; it does not need the compiler, or anything the compiler was built with, to run.

`snello build` writes LLVM IR and has clang compile it, with the optimizer that implies; `snello build -backend c FILE` builds the same program through the C backend, the reference implementation, which hands C to the system's C compiler. The two produce binaries that print the same thing, and the compiler's own tests hold them to it. `snello build -debug FILE` builds the same program with a checking runtime, which stops it on a value used after its release and reports, at the end, the values never released. Chapter 5 says what it catches and why; a program that passes the checker should never trip it, so it is a tool for finding the compiler's mistakes, not the program's.

The first build compiles Snello's runtime, the C every program links, and keeps it in the cache directory of your system, which chapter 32 names for each system; every later build with the same compiler and mode compiles only the program, which takes a fraction of a second. `SNELLO_CACHE=DIR` keeps it in `DIR` instead, and `SNELLO_CACHE=off` compiles the runtime with every build. Removing the directory clears it.

## Building for another platform

`snello build -target linux/arm64 FILE` builds the program for Linux on an arm64 processor, whatever machine runs the compiler. The targets are `darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64`, `windows/amd64` and `windows/arm64`, a system and a processor as Go names them, and without the flag the target is the machine you are on. The binary is the target's: copy it there and run it. `snello check -target linux/amd64 DIR` checks the files a build for that target would compile, so a mistake in a file for another system, a `poll_linux.snello` of the section on packages below checked on a Mac, is found where you are.

A build for another platform needs three things the compiler does not carry: a C compiler that builds for the target, the target's system files, its C library above all, and a linker that writes the target's binaries.

- **On Linux, for the other processor**, Debian and Ubuntu package all three: clang and lld, and the other processor's C library and the compiler's support library, `sudo apt install clang lld libc6-dev-amd64-cross libgcc-14-dev-amd64-cross` on Debian 13 for amd64 from an arm64 machine (`arm64` in the names for the other way, and the release's gcc version in `libgcc-N-dev`). Clang finds the files where the packages put them, and nothing more is set. `qemu-user` runs the other processor's programs, `qemu-x86_64 -L /usr/x86_64-linux-gnu ./hello`, for a try before the program is copied. The C backend may build with gcc instead, a gcc built for the target named by `CC`, `CC=x86_64-linux-gnu-gcc` from Debian's `gcc-x86-64-linux-gnu`, which links with its own linker.
- **On macOS, for Linux**, LLVM's linker, `brew install lld`, and the Linux system's files in a directory of their own, a sysroot, named by the target's variable, `SNELLO_SYSROOT_linux_amd64` for `-target linux/amd64` (chapter 32 lists them). A sysroot is a Linux system's `/usr/include` and its libraries; `build/sysroot.sh linux/amd64 DIR` in the compiler's repository writes one from Debian's image with Docker, and the program then runs on Debian, or on any Linux with that C library or a later one.
- **On macOS, for the other processor**, nothing more: the SDK the compiler already has builds for both. An Apple Mac runs an Intel program under Rosetta. A program built for macOS, for either processor and on any macOS, runs on macOS 14.4 and every later version.
- **macOS from Linux** is refused: Apple's SDK and linker run on macOS alone.
- **On Windows, for the other processor**, the Build Tools' libraries for that processor, their ARM64 or x64 component, which the installer adds on request. A program built for Windows, from any system, is named with `.exe`, `hello.exe`, and carries Microsoft's C runtime inside it, so it needs nothing installed beside it; Windows 10 is the oldest version it is built for (chapter 34).
- **Windows from macOS or Linux** takes clang and LLVM's linker, `brew install lld` or `sudo apt install lld`, which bring `lld-link`, and Microsoft's SDK and C runtime in the directory the target's variable names, `SNELLO_SYSROOT_windows_amd64` for `-target windows/amd64` and `SNELLO_SYSROOT_windows_arm64` for `-target windows/arm64`, laid out as the `xwin` tool writes them: `xwin --accept-license --arch x86_64,aarch64 splat --output DIR` writes both processors' into one directory, which both variables may name, and a directory without the target's processor's libraries is refused, naming the `xwin` command that writes them. Microsoft's licence lets you download them for your own use and not pass them on, so the compiler cannot carry them. Nothing in CI builds this way.

What is missing is named before anything is compiled, with what to do:

```
$ snello build -target linux/amd64 hello.snello
llvm: building for linux/amd64 on darwin/arm64 takes a Linux system's headers and libraries: set SNELLO_SYSROOT_linux_amd64 to a directory holding them, a sysroot
```

## The other commands

`snello check FILE` runs the parser and the checker and prints every error with its position, or nothing when the program is correct. It is what `snello build` runs first, and it is useful on its own while writing. `snello emit FILE` prints the IR that `snello build` compiles, for reading what the compiler made of a program; `snello emit -backend c FILE` prints the C source.

`snello doc` prints a package's documentation, the comments on its declarations: `snello doc strings` the overview of a package, `snello doc strings.Builder` a name with its methods, and `snello doc` alone the package in the current directory (chapter 37).

`snello parse FILE` prints the syntax tree, each declaration with its doc comment, and `snello lex FILE` the tokens. They exist for people working on the compiler.

## Tests

A package's tests live beside it in files named `NAME_test.snello`, which are part of the package only when it is tested: `snello build` never reads one, and nothing of a test reaches a program's binary. A test is a plain function named `Test` and a capital, taking nothing and returning nothing; it passes when it returns, and it fails by a failed expectation, which is a panic the runner catches at the test's boundary, dropping what the test made. The `expect` package asserts and the `testing` package runs; both may be imported from a test file alone. From `examples/packaged/shapes/shapes_test.snello`:

```go
package shapes

import ("expect"; "testing")

struct Case {
	name string
	kind Kind
	want string
}

func TestDescribe() {
    cases := []Case{
        Case{name: "round", kind: Round, want: "round"},
        Case{name: "square", kind: Square, want: "square"},
    }
    for _, c := range cases {
        testing.Run(ref c.name, func() { expect.Equal(ref Describe(c.kind), ref c.want) })
    }
}

func TestArea() {
    expect.Equal(ref Area(Rect{W: 2, H: 3}), ref 6.0)
    expect.True(Area(Circle{Radius: 1}) > 3.14)
}
```

`snello test examples/packaged/shapes` builds the package with its tests and a generated `main`, runs it, and prints `ok  	shapes	0.001s`, or each failure with the expression as the test wrote it, then `got` and `want`, and the first difference in words:

```
--- FAIL: TestDescribe/square (0.00s)
    shapes_test.snello:14: expect.Equal(ref Describe(c.kind), ref c.want)
        got:  square
        want: squared   (string)
FAIL
FAIL	shapes	0.001s
```

`snello test -v DIR` prints every test's lines, and `-run NAME` runs the tests whose name holds NAME. The compiler hands every assertion its file and line and the call as the test wrote it, and for `expect.True(n > limit)` the values of the operands, so a failure reads `expect.True(n > limit) is false` and then `n is 2, limit is 3`. Chapter 16 has the rest: every assertion and the message it prints, subtests and tables, logging and skipping, temporary directories, parallel tests, benchmarks, golden files, the flags, testing a `main` package, and how it is all built.

## Packages

A program grows past one file the way a Go program does: a directory is a package, and a module file names the tree the packages live in. Each `.snello` file in a directory begins with the same package clause, and the files see each other's declarations. The directory the program is in is the main package, and `snello build DIR` builds every file in it into a binary named after the directory; `snello build FILE` still builds the one file alone, as `go run hello.go` does.

A file `snello.mod` at the root of the tree marks the module, and an import names a directory under the root:

```
app/
    snello.mod          snello 0.1.0: it marks the root and names the version
    main.snello         package main; import "shapes"
    shapes/
        shape.snello    package shapes
        area.snello     package shapes
```

```go
package main

import (
    "fmt"
    "shapes"
)

func main() {
    c := shapes.Circle{Radius: 2}
    fmt.Println(shapes.Area(c))
}
```

`snello init` makes the current directory the root of a module, `snello init DIR` another directory: it writes `snello.mod` with one line, the version of Snello the module is written for, the version of the compiler that ran it, and refuses a directory that has a module file already. The line is a Semantic Versioning version, the oldest release that compiles the module; it may stand anywhere in the file, and `init` writes it first, where a reader looks:

```
$ snello init
snello init: wrote /home/me/app/snello.mod
$ cat snello.mod
snello 0.1.0
```

A compiler older than the version refuses the module and says so, `/home/me/app/snello.mod:1: this module needs snello 0.3.0; this is snello 0.1.0: upgrade the compiler`, rather than failing on what it does not know; a pre-release, `0.1.0-alpha.1`, compiles a module written `snello 0.1.0`, since it is that release in the making; a module the program requires is checked the same way, by its own file. A module file without the line is read as written for the first release, 0.1.0, so a new release never changes what it means, and every command that builds, checks or tests the module says so on standard error, with the line to add:

```
warning: /home/me/app/snello.mod has no snello line: it is read as snello 0.1.0, the first release; add the line "snello 0.1.0"
```

A program with no module file above it, one file built on its own, is read as the version of the compiler building it.

A package that does something differently on each system writes a file for each and names it for its platform, as Go does: `poll_linux.snello` is built only for Linux, `poll_arm64.snello` only for an arm64 processor, and `poll_darwin_arm64.snello` only for macOS on arm64, while `poll.snello` beside them is built everywhere. `poll_unix.snello` is built for every Unix, Linux and macOS, and not for Windows, so code the two Unix systems share is one file beside `poll_windows.snello`; it takes a processor too, `poll_unix_arm64.snello`. The platform is the last part of the name, after an underscore, and before `_test` in a test file's, `poll_linux_test.snello`; nothing inside a file chooses, so a listing of the directory shows which file is for which platform:

```
poll/
    poll.snello              every platform: what the others share
    poll_linux.snello        Linux
    poll_darwin.snello       macOS
    poll_windows.snello      Windows
    poll_linux_test.snello   tests that run on Linux
```

Snello builds for `linux`, `darwin` and `windows`, on `amd64` and `arm64`, and the files chosen are the ones for the platform the program is built for, the machine you are on unless `-target` says otherwise. A file named for another system or processor, `poll_freebsd.snello` or `cpu_riscv64.c`, is left out, so a package can carry the files of every system. The words a name may end in are the systems `aix`, `android`, `darwin`, `dragonfly`, `freebsd`, `hurd`, `illumos`, `ios`, `js`, `linux`, `nacl`, `netbsd`, `openbsd`, `plan9`, `solaris`, `wasip1`, `windows` and `zos`, and the processors `386`, `amd64`, `amd64p32`, `arm`, `armbe`, `arm64`, `arm64be`, `loong64`, `mips`, `mipsle`, `mips64`, `mips64le`, `mips64p32`, `mips64p32le`, `ppc`, `ppc64`, `ppc64le`, `riscv`, `riscv64`, `s390`, `s390x`, `sparc`, `sparc64` and `wasm`, Go's list, and `unix` for every system of that list that is a Unix: a name ending in one of them, `decode_js.snello`, is for that platform and no other. A last word that is not among them is part of the name, `string_builder.snello`, and so is a misspelt one, `poll_linx.snello`, which is built everywhere: check the spelling. A file whose name begins with a dot or an underscore, an editor's lock or a file set aside, is no file of the package. A directory whose every file is for another platform is no package on this one, `every .snello file in poll is for another platform than darwin/arm64`. A file named on the command line, `snello build tool_linux.snello`, is built as named. The same names choose a package's C files, chapter 15.

An import is answered by the standard library first, then by the module's directories, then by a `require` line in `snello.mod`, which names a dependency and where it is. A dependency on your own disk is a directory, `require regexp ../regexp`, after which the program imports `"regexp"` and `"regexp/syntax"`; it is read where it is, as Go's `replace` reads one, and nothing about it is recorded. The module has no name, since nothing needs one: a dependency is the directory it is, so two dependencies that each require a third share one copy of it, and a value of the third's types passes between them. A directory or a dependency named as a standard package is refused, so `strings` is always the standard `strings`.

### Dependencies by version

A library someone publishes lives in a git repository, and a program requires it by the repository and a release:

```
$ snello add regexp https://github.com/snello-lang/regexp
required regexp https://github.com/snello-lang/regexp 0.2.0
```

`snello add` writes `require regexp https://github.com/snello-lang/regexp 0.2.0` into `snello.mod`; the program imports `"regexp"`, and the build fetches the release, tagged `v0.2.0`, and records its commit and a hash of its files in `snello.lock`, which you commit with the module file. `snello update`, `snello remove`, `snello deps` and `snello fetch` raise, remove, show and fetch dependencies. Chapter 35 is the guide: versions, the lock file, updating, offline builds, private repositories, publishing a library, and what each error means.

## A second program

```go
package main

import "fmt"

func fib(n int) int {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}

func main() {
    for i := 0; i < 10; i += 1 {
        fmt.Println(i, fib(i))
    }
}
```

Chapter 3 explains everything this program uses.
