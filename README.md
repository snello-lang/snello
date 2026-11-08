<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="_docs/brand/wordmark-dark.svg">
    <img alt="Snello" src="_docs/brand/wordmark-light.svg" width="220">
  </picture>
</p>

<p align="center">
  <a href="https://github.com/snello-lang/snello/actions/workflows/test.yml"><img alt="Tests" src="https://img.shields.io/github/actions/workflow/status/snello-lang/snello/test.yml?branch=main&label=tests"></a>
  <a href="go.mod"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/snello-lang/snello?label=go"></a>
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

Snello is young. The compiler builds the whole language today: functions, methods and closures, structs, enums and `match`, slices, maps and strings, options and results, owned pointers, `defer`, traits and generics, tasks and channels on a scheduler of the runtime's own, files and sockets, and a standard library written in Snello, through an LLVM backend and a C backend held to the same output. Packages of a program's own, outside the compiler, are being built now; see [the milestones](https://github.com/snello-lang/snello/milestones) for what is done and what is next.

## Getting started

Build the compiler from source with Go. Building a Snello program needs `clang` on the path, or the one `CLANG` names; the C backend needs `cc`, or the one `CC` names.

```
git clone https://github.com/snello-lang/snello
cd snello
make build          # the compiler into bin/snello, built inside the Linux image; NATIVE=1 make build uses the Go on your machine
bin/snello build examples/run/hello.snello
./hello
```

| Command | What it does |
|---|---|
| `snello build FILE` | Compiles a program to a native executable in the current directory, through the LLVM backend and clang |
| `snello build -debug FILE` | The same, with the checking runtime |
| `snello build -backend c FILE` | The same through the C backend, the reference implementation, and the system's C compiler |
| `snello check FILE` | Checks a program and prints its errors |
| `snello emit FILE` | Prints the IR the compiler generates; `-backend c` prints the C |

## Documentation

- [The book](_docs/book/): the language, one feature per chapter, starting from [the introduction](_docs/book/01-introduction.md). Readers coming from Go have [Appendix B](_docs/book/B-snello-for-go-programmers.md).
- [The design](_docs/design/): every decision behind the language and the reasoning for it.
- [Examples](examples/): programs that build and run with the output each prints, and programs that break each rule with the message the compiler gives.

## Contributing

Read [the coding standards](CODING_STANDARDS.md) and [the code of conduct](CODE_OF_CONDUCT.md). Run `go test ./...` before opening a pull request, or `build/run.sh go test ./...` to run it inside the Linux image CI uses; the tests build and run every example through both backends, so they need `clang` and `cc` too, and take a few minutes. The Makefile runs Snello: `make test` runs the library's and the examples' own tests through `snello test`, `make test-std` and `make test-examples` each half, and `make check` runs `snello check` over the examples, each inside the image unless `NATIVE=1` is given. Contributors working with AI tools also follow [AGENTS.md](AGENTS.md).

## License

[BSD 3-Clause](LICENSE)
