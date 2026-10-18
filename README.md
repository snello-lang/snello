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

## Status

Teak compiles. `teak build` turns a program in core Teak into a native executable: functions, methods and closures, structs, enums and `match`, slices, maps and strings, options and results with `?` and `??`, owned pointers, `defer`, files and printing. The compiler checks ownership, moves and borrows, places every drop, and `teak build -debug` builds a binary that stops on a value used after its release and reports anything never released. Stages 0 and 1 of the plan below are done (2026-10-18); stage 2, traits and generics, is next.

What does not exist yet: traits, generics, tasks and channels, and the standard library written in Teak. The programs that need them are in the repository as the acceptance test of the stages that will add them.

## Getting started

The compiler is built from source with the Go toolchain; a Teak program needs a C compiler on the path, `cc`.

```
git clone https://github.com/teak-lang/teak
cd teak
go build -o teak ./cmd/teak
./teak build examples/run/hello.teak
./hello
```

`teak check FILE` runs the checker and prints its errors, `teak emit FILE` prints the C the compiler would build, and `teak build -debug FILE` builds with the checking runtime. The book under [`_docs/book/`](_docs/book/) is the guide to the language, one feature per chapter, with every sample cut from a program under [`examples/`](examples/).

## Features

Implemented today:

- **Go's syntax.** `func`, `:=`, slices, maps, `defer`, `range`, with `struct` and `enum` declarations and `spawn` where Go says `go`. A Go developer reads Teak on day one.
- **Memory safety without a garbage collector.** Every value has one owner and is freed where the owner dies, with a `Drop` method run there, on every path out of a function including a panic. Checked at compile time.
- **No lifetime annotations.** Borrows, `ref` and `mut`, live inside one function and cannot be stored or sent to another task; a borrow is returned only from its one obvious source, the receiver or the only borrowed parameter. That restriction removes the hardest part of Rust, and stage 1 showed the core programs never needed an annotation.
- **Sum types and pattern matching**, exhaustive, with `T?` and `Result` instead of `nil` and exceptions.

Planned, in the stages below:

- **Scala-style traits, not interfaces.** Nominal traits with default methods and fields; a type says what it implements with `impl` on its declaration. Stage 2.
- **Go's concurrency, race-free.** Lightweight tasks, channels and `select`; anything crossing a task boundary must be moved, immutable or atomically shared, and the compiler infers which types qualify. Stage 3.
- **A native backend** through LLVM with an M:N scheduler, and GPU kernels on the same backend. Stages 4 and 5.

## Layout

| Path | Contents |
|---|---|
| [`cmd/teak`](cmd/teak/) | The compiler command: `build`, `check`, `emit`, `parse`, `lex` |
| `scanner/`, `parser/`, `ast/`, `token/` | The front end, after the Go toolchain's packages of the same names |
| [`types/`](types/) | The checker: types, moves, borrows, escapes |
| [`emit/c/`](emit/c/) | The C backend and the runtime under `emit/c/runtime/`, compiled into every program |
| [`examples/core/`](examples/core/) | The stage 1 acceptance suite: eight programs that run, and the negative programs the checker must reject |
| [`examples/run/`](examples/run/) | Smaller programs, one per backend feature, each with the output it prints |
| [`examples/`](examples/) | The twenty programs the language was designed against; all run once stage 2 lands |
| [`_docs/book/`](_docs/book/) | The book: how to write Teak |
| [`_docs/design/`](_docs/design/) | The language design, the decisions and their reasoning |
| [`_docs/specs/`](_docs/specs/) | The implementation plan and per-stage task lists, ticked as tasks complete |
| [`_docs/brand/`](_docs/brand/) | Logo, wordmark and palette |

## Plan

| Stage | What | State |
|---|---|---|
| 0 | Lexer and parser for the Go-like syntax, written in Go | done 2026-10-18 |
| 1 | Core checker: ownership, moves, `ref`/`mut` borrows, drops. C backend, native binaries, debug build | done 2026-10-18 |
| 2 | Traits, generics, and the standard library written in Teak. Still emit C | next |
| 3 | Sendable inference for `spawn` and channels | |
| 4 | LLVM backend and a native runtime with an M:N scheduler | |
| 5 | GPU kernels through the LLVM targets: a kernel-safe subset and device buffers | |

## License

[BSD 3-Clause](LICENSE)
