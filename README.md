<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="_docs/brand/wordmark-dark.svg">
    <img alt="Snello" src="_docs/brand/wordmark-light.svg" width="220">
  </picture>
</p>

<p align="center">
  <a href="https://github.com/snello-lang/snello/releases"><img alt="Release" src="https://img.shields.io/github/v/release/snello-lang/snello?include_prereleases&label=release"></a>
  <a href="https://github.com/snello-lang/snello/actions/workflows/test.yml"><img alt="Tests" src="https://img.shields.io/github/actions/workflow/status/snello-lang/snello/test.yml?branch=main&label=tests"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/snello-lang/snello?label=license"></a>
</p>

---

Snello is a compiled language with Go's syntax and memory safety without a garbage collector. Every value has one owner and is freed where its owner dies; the compiler checks every use at compile time, and you never write a lifetime annotation. A program builds to one native executable with a small runtime inside and nothing else.

*Snello* is the Italian word for lean and nimble, and it is the whole claim: a program carries nothing it does not need, no garbage collector and no pause it did not ask for; and the language carries nothing a Go developer has to learn twice.

## A taste

```go
package main

import "fmt"

enum Shape {
    Circle(radius f64)
    Rect(w f64, h f64)
}

struct Canvas {
    name   string
    shapes []Shape
}

func (c mut Canvas) Add(s Shape) {
    c.shapes.Push(s)
}

func area(s Shape) f64 {
    return match s {
        Circle(radius) => 3.14 * radius * radius,
        Rect(w, h)     => w * h,
    }
}

func main() {
    canvas := Canvas{name: "sketch", shapes: []Shape{Circle(1.0)}}
    canvas.Add(Rect(2.0, 3.0))
    total := 0.0
    for _, s := range canvas.shapes {    // the loop takes the shapes: they move out of the canvas
        total += area(s)
    }

    fmt.Println(canvas.name, total)
    fmt.Println(len(canvas.shapes))      // COMPILE ERROR "use of moved value canvas.shapes"
}
```

An enum holds one of several variants, each with its own data, and `match` takes it apart and must cover every one. A struct holds fields, and `Add` changes its canvas in place, which its `mut` receiver says. The loop takes the shapes by value, so they move out of the canvas: its name is still there, but the last line, which reads the shapes afterwards, is refused at compile time, before anything runs:

```
shapes.snello:35:21: use of moved value canvas.shapes; moved at 30:23
```

## Install

Every release on the [Releases page](https://github.com/snello-lang/snello/releases) holds one archive per platform, Linux, macOS and Windows on x86-64 and arm64, each with the `snello` binary, which carries the compiler, the runtime and the standard library. Download yours, unpack it, and put `snello` on your path:

```
tar -xzf snello-0.1.0-linux-amd64.tar.gz
mv snello-0.1.0-linux-amd64/snello ~/.local/bin/
```

On Windows, unzip `snello-0.1.0-windows-amd64.zip` and add its folder to your `Path`. Every archive is listed in the release's `SHA256SUMS`, signed, and the release's notes say how to verify what you downloaded.

A build needs `clang`, version 15 or later, which compiles the code Snello writes, and a C compiler, which the C backend and a debug build compile with; on Windows, LLVM and Visual Studio's Build Tools. `snello doctor` checks each one and prints the line that installs what is missing:

```
$ snello doctor
ok    clang: Apple clang version 21.0.0 (clang-2100.4.1.1)
ok    link-time optimization: clang links with -flto
ok    C compiler: Apple clang version 21.0.0 (clang-2100.4.1.1)
ok    git: git version 2.56.1
ok    snello on the path: /Users/ana/.local/bin
```

With Go 1.27.2 or later on your machine, `go install github.com/snello-lang/snello/cmd/snello@latest` builds the same compiler from source.

## Hello, Snello

```
snello new hello
cd hello
snello run .
```

`new` writes a module with a program and its test, `run` builds the program and runs it, `snello test .` runs the test, and `snello build .` writes the executable into the current directory. A program is a `.snello` file:

```go
package main

import "fmt"

func main() {
    fmt.Println("hello, Snello")
}
```

`snello build hello.snello` builds it to `./hello`, and `snello build -target linux/arm64 hello.snello` builds it for another platform: for the other Linux with the distribution's cross packages, for Windows on Windows with the Build Tools, and from Linux or macOS with LLVM's linker and Microsoft's SDK in a sysroot, which [chapter 2](_docs/book/02-getting-started.md) sets up.

## Why Snello

- **Go's syntax.** `func`, `:=`, slices, maps, `defer`, `range`. A Go developer reads Snello on day one.
- **No garbage collector, no leaks.** Values are freed at a point the compiler knows, and a type with a `Drop` method cleans up there, on every path out of a function, including a panic. A program's peak memory is what it keeps alive, and it never pauses to collect: `bench/memory.snello` peaks at 33 MB with every round even, where the same program in Go peaks at 78 MB with one round in twenty stalled (chapter 5 of the book has the table).
- **No lifetime annotations.** Borrows are written `ref` and `mut` and live inside one function; a borrow is returned only from its one obvious source. The compiler infers every lifetime or refuses the program.
- **No `nil`, no exceptions.** An absent value is `T?`, a failure is a `Result`, and `?` passes it up. `match` is exhaustive.
- **Mistakes that stop the program.** `snello build -debug` builds a binary that panics on any value used after its release and reports anything never released.

Snello is young. The compiler builds the whole language today: functions, methods and closures, structs, enums and `match`, slices, maps and strings, options and results, owned pointers, `defer`, traits and generics, tasks and channels on a scheduler of the runtime's own, files and sockets, and a standard library written in Snello, through an LLVM backend and a C backend held to the same output. What comes next, in order, is in the pinned [Roadmap](https://github.com/snello-lang/snello/issues/122).

## Platforms

| Platform | Runs on |
|---|---|
| `linux/amd64`, `linux/arm64` | kernel 5.4 and glibc 2.29 or later |
| `darwin/amd64`, `darwin/arm64` | macOS 14.4 or later |
| `windows/amd64`, `windows/arm64` | Windows 10, Windows Server 2016 or later |

Every platform is built and tested on every change; [chapter 34](_docs/book/34-platforms.md) of the book says what each one is promised, its tier, and what is not yet built on Windows.

## The snello command

| Command | What it does |
|---|---|
| <code>snello&nbsp;new&nbsp;DIR</code> | Starts a module in a new directory, with a program and its test |
| <code>snello&nbsp;run&nbsp;FILE</code> | Builds a program and runs it with the arguments after its path, leaving no file; `DIR` runs a directory's program |
| <code>snello&nbsp;build&nbsp;FILE</code> | Compiles a program to a native executable; `-debug` builds with the checking runtime, `-backend c` through the C backend, `-target OS/ARCH` for another platform |
| <code>snello&nbsp;test&nbsp;DIR</code> | Runs the tests and the examples in a directory's `_test.snello` files |
| <code>snello&nbsp;check&nbsp;FILE</code> | Checks a program and prints its errors |
| <code>snello&nbsp;doc&nbsp;PACKAGE</code> | Prints a package's documentation; `-http` serves the standard library's and your module's in a browser |
| <code>snello&nbsp;add&nbsp;NAME&nbsp;URL</code> | Requires a dependency from a git repository by version; `update`, `remove`, `deps` and `fetch` manage them |
| <code>snello&nbsp;env</code> | Prints what a build reads here, each setting with its value and where it came from |
| <code>snello&nbsp;doctor</code> | Checks what a build needs and prints the line that installs what is missing |
| <code>snello&nbsp;version</code> | Prints the version, the commit, the channel and the platform |

`snello help COMMAND` prints a command's flags, and [chapter 32](_docs/book/32-the-snello-command.md) of the book has every command and every variable `snello` reads.

## How Snello is built

The compiler is written in Go, with nothing but Go's standard library, after the Go toolchain's own packages: a scanner, a parser, a type and ownership checker, and two backends. Go is only what the compiler is written in. A Snello program contains no Go: the compiler turns it into LLVM IR, or into C, and the system's `clang` or C compiler builds a native executable from it with Snello's runtime, a small library written in C. The standard library is written in Snello, and a program you build needs neither Go nor a Go runtime to run.

## Documentation

- [The book](_docs/book/): the language, one feature per chapter, starting from [the introduction](_docs/book/01-introduction.md). Readers coming from Go have [Appendix B](_docs/book/B-snello-for-go-programmers.md).
- [The design](_docs/design/): every decision behind the language and the reasoning for it.
- `snello doc -http`: the standard library's documentation, and your module's, in a browser; [chapter 37](_docs/book/37-documenting-a-package.md) says how to write it.
- [Examples](examples/): programs that build and run with the output each prints, and programs that break each rule with the message the compiler gives.

## Contributing

Contributions are welcome. Working on the compiler takes Go, `clang` and a C compiler, nothing more:

```
git clone https://github.com/snello-lang/snello
cd snello
go build -o bin/snello ./cmd/snello
bin/snello build examples/hello/hello.snello
```

1. Pick an issue from the pinned [Roadmap](https://github.com/snello-lang/snello/issues/122), which lists the work in the order it is to be done. Each issue says what to build, how it is accepted and how to verify it, and its label says where it lives: `compiler/runtime` for the compiler, its backends, the runtime and the tools, `corelibs` for the standard library, `lib` for the libraries that ship outside the compiler.
2. Read [CONTRIBUTING.md](CONTRIBUTING.md), [the coding standards](CODING_STANDARDS.md) and [the code of conduct](CODE_OF_CONDUCT.md).
3. Run `go test ./...` before opening a pull request.
