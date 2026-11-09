<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="_docs/brand/wordmark-dark.svg">
    <img alt="Snello" src="_docs/brand/wordmark-light.svg" width="220">
  </picture>
</p>

<p align="center">
  <a href="https://github.com/snello-lang/snello/actions/workflows/test.yml"><img alt="Tests" src="https://img.shields.io/github/actions/workflow/status/snello-lang/snello/test.yml?branch=main&label=tests"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/snello-lang/snello?label=license"></a>
</p>

---

Snello is a compiled language with Go's syntax and memory safety without a garbage collector. Every value has one owner and is freed where its owner dies; the compiler checks every use at compile time, and you never write a lifetime annotation. A Snello program builds to a native executable with a small runtime inside and nothing else.

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
    fmt.Println(len(canvas.shapes))      // ERROR "use of moved value canvas.shapes"
}
```

An enum holds one of several variants, each with its own data, and `match` takes it apart and must cover every variant. A struct holds fields, and `Add` changes its canvas in place, which its `mut` receiver says. The loop takes the shapes by value, so they move out of the canvas: its name is still there, but the last line, which reads the shapes afterwards, is refused:

```
shapes.snello:35:21: use of moved value canvas.shapes; moved at 30:23
```

## Why Snello

- **Go's syntax.** `func`, `:=`, slices, maps, `defer`, `range`. A Go developer reads Snello on day one.
- **No garbage collector, no leaks.** Values are freed at a point the compiler knows, and a type with a `Drop` method cleans up there, on every path out of a function, including a panic. A program's peak memory is what it keeps alive, and it never pauses to collect: `bench/memory.snello` peaks at 33 MB with every round even, where the same program in Go peaks at 78 MB with one round in twenty stalled (chapter 5 of the book has the table).
- **No lifetime annotations.** Borrows are written `ref` and `mut` and live inside one function; a borrow is returned only from its one obvious source. The compiler infers every lifetime or refuses the program.
- **No `nil`, no exceptions.** An absent value is `T?`, a failure is a `Result`, and `?` passes it up. `match` is exhaustive.
- **Mistakes that stop the program.** `snello build -debug` builds a binary that panics on any value used after its release and reports anything never released.

Snello is young. The compiler builds the whole language today: functions, methods and closures, structs, enums and `match`, slices, maps and strings, options and results, owned pointers, `defer`, traits and generics, tasks and channels on a scheduler of the runtime's own, files and sockets, and a standard library written in Snello, through an LLVM backend and a C backend held to the same output. What comes next, in order, is in the pinned [Roadmap](https://github.com/snello-lang/snello/issues/122).

## How Snello is built

The compiler is written in Go, with nothing but Go's standard library, after the Go toolchain's own packages: a scanner, a parser, a type and ownership checker, and two backends. Go is only what the compiler is written in. A Snello program contains no Go: the compiler turns it into LLVM IR, or into C, and the system's `clang` or C compiler builds a native executable from it with Snello's runtime, a small library written in C. The standard library is written in Snello. A program you build needs neither Go nor a Go runtime to run.

## Try it

With Go 1.27.2 or later, and `clang` on the path, install the compiler:

```
go install github.com/snello-lang/snello/cmd/snello@latest
```

The compiler carries the standard library and the runtime inside it, so it works from any directory. Write a program:

```go
package main

import "fmt"

func main() {
    fmt.Println("hello, Snello")
}
```

Save it as `hello.snello`, then build it and run it:

```
snello build hello.snello
./hello
```

To work on the compiler, build it from a clone instead:

```
git clone https://github.com/snello-lang/snello
cd snello
go build -o bin/snello ./cmd/snello
bin/snello build examples/hello/hello.snello
```

`make build` does the same inside the Linux image CI uses, for a build that matches CI's; it needs Docker, and `NATIVE=1 make build` uses the Go on your machine instead.

| Command | What it does |
|---|---|
| `snello build FILE` | Compiles a program to a native executable in the current directory, through the LLVM backend and clang; `DIR` builds the program of every `.snello` file in a directory |
| `snello build -debug FILE` | The same, with the checking runtime, which panics on a value used after its release and reports anything never released |
| `snello build -backend c FILE` | The same through the C backend, the reference implementation, and the system's C compiler, `cc` or the one `CC` names |
| `snello check FILE` | Checks a program and prints its errors |
| `snello test DIR` | Runs the tests in a directory's `_test.snello` files |
| `snello emit FILE` | Prints the IR the compiler generates; `-backend c` prints the C |

`clang` is found on the path, or `CLANG` names it.

## Documentation

- [The book](_docs/book/): the language, one feature per chapter, starting from [the introduction](_docs/book/01-introduction.md). Readers coming from Go have [Appendix B](_docs/book/B-snello-for-go-programmers.md).
- [The design](_docs/design/): every decision behind the language and the reasoning for it.
- [Examples](examples/): programs that build and run with the output each prints, and programs that break each rule with the message the compiler gives.

## Contributing

Contributions are welcome. Working on the compiler takes Go, `clang` and a C compiler, nothing more.

1. Pick an issue from the pinned [Roadmap](https://github.com/snello-lang/snello/issues/122), which lists the work in the order it is to be done. Each issue says what to build, how it is accepted and how to verify it, and its label says where it lives: `compiler/runtime` for the compiler, its backends, the runtime and the tools, `corelibs` for the standard library, `lib` for the libraries that ship outside the compiler.
2. Read [CONTRIBUTING.md](CONTRIBUTING.md), [the coding standards](CODING_STANDARDS.md) and [the code of conduct](CODE_OF_CONDUCT.md).
3. Run `go test ./...` before opening a pull request.
