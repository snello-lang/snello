<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="_docs/brand/logo-dark.svg">
    <img alt="Teak" src="_docs/brand/logo-light.svg" width="320">
  </picture>
</p>

<p align="center">
  <strong>The Teak Programming Language</strong>
</p>

---

Teak is a compiled language with Go's syntax and memory safety without a garbage collector. Every value has one owner and is freed where its owner dies; the compiler checks every use at compile time, and you never write a lifetime annotation. A Teak program builds to a native executable with a small runtime inside and nothing else.

## A taste

```teak
package main

import "fmt"

struct Rect {
    w f64
    h f64
}

func (r ref Rect) Area() f64 {
    return r.w * r.h
}

enum Shape {
    Circle(radius f64)
    Box(rect Rect)
}

func area(s Shape) f64 {
    return match s {
        Circle(radius) => 3.14 * radius * radius,
        Box(rect)      => rect.Area(),
    }
}

func grow(shapes mut []Shape) {
    shapes.Push(Box(Rect{w: 2.0, h: 3.0}))
}

func main() {
    shapes := []Shape{Circle(1.0)}
    grow(mut shapes)
    total := 0.0
    for _, s := range shapes {    // the loop takes the shapes: shapes moves here
        total += area(s)
    }

    fmt.Println(total)
    fmt.Println(len(shapes))      // ERROR "use of moved value shapes"
}
```

A struct holds fields and an enum holds one of several variants, each with its own data; `match` takes the enum apart and must cover every variant. `Area` reads its receiver, which `ref` says. `grow` borrows the slice to change it, and the call says so with `mut`. The loop takes the shapes by value, so it uses the slice up, and the last line, which reads it afterwards, is refused:

```
shapes.teak:39:21: use of moved value shapes; moved at 34:23
```

## Why Teak

- **Go's syntax.** `func`, `:=`, slices, maps, `defer`, `range`. A Go developer reads Teak on day one.
- **No garbage collector, no leaks.** Values are freed at a point the compiler knows, and a type with a `Drop` method cleans up there, on every path out of a function, including a panic.
- **No lifetime annotations.** Borrows are written `ref` and `mut` and live inside one function; a borrow is returned only from its one obvious source. The compiler infers every lifetime or refuses the program.
- **No `nil`, no exceptions.** An absent value is `T?`, a failure is a `Result`, and `?` passes it up. `match` is exhaustive.
- **Mistakes that stop the program.** `teak build -debug` builds a binary that panics on any value used after its release and reports anything never released.

Teak is young. The compiler handles core Teak today: functions, methods and closures, structs, enums and `match`, slices, maps and strings, options and results, owned pointers, `defer`, files and printing. Traits, generics, lightweight tasks with channels, a standard library written in Teak, and an LLVM backend are being built, in that order.

## Getting started

Build the compiler from source with Go; a Teak program needs a C compiler on the path, `cc`.

```
git clone https://github.com/teak-lang/teak
cd teak
go build -o teak ./cmd/teak
./teak build examples/run/hello.teak
./hello
```

| Command | What it does |
|---|---|
| `teak build FILE` | Compiles a program to a native executable in the current directory |
| `teak build -debug FILE` | The same, with the checking runtime |
| `teak check FILE` | Checks a program and prints its errors |
| `teak emit FILE` | Prints the C the compiler generates |

## Documentation

- [The book](_docs/book/): the language, one feature per chapter, starting from [the introduction](_docs/book/01-introduction.md). Readers coming from Go have [Appendix B](_docs/book/B-teak-for-go-programmers.md).
- [The design](_docs/design/): every decision behind the language and the reasoning for it.
- [Examples](examples/): programs that build and run with the output each prints, and programs that break each rule with the message the compiler gives.

## Contributing

Read [the coding standards](CODING_STANDARDS.md) and [the code of conduct](CODE_OF_CONDUCT.md). Run `go test ./...` before opening a pull request; the tests build and run every example, so they need `cc` too. Contributors working with AI tools also follow [AGENTS.md](AGENTS.md).

## License

[BSD 3-Clause](LICENSE)
