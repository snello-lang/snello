<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="_docs/brand/wordmark-dark.svg">
    <img alt="Snello" src="_docs/brand/wordmark-light.svg" width="220">
  </picture>
</p>

<p align="center">
  <a href="https://github.com/snello-lang/snello/releases"><img alt="Release" src="https://img.shields.io/github/v/release/snello-lang/snello?include_prereleases&label=release"></a>
  <a href="https://github.com/snello-lang/snello/actions/workflows/test.yml"><img alt="Tests" src="https://img.shields.io/github/actions/workflow/status/snello-lang/snello/test.yml?label=tests"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/snello-lang/snello?label=license"></a>
</p>

---

Snello is a compiled language with Go's syntax and memory safety without a garbage collector. Every value has one owner and is freed where its owner dies; the compiler checks every use, and you never write a lifetime annotation. A program builds to one native executable, with tasks and channels on Snello's own scheduler. *Snello* is the Italian word for lean and nimble: a program carries nothing it does not need, and the language carries nothing a Go developer has to learn twice.

## Install

Linux and macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/snello-lang/snello/main/release/install.sh | sh
```

Windows, in PowerShell 5.1 or later:

```powershell
irm https://raw.githubusercontent.com/snello-lang/snello/main/release/install.ps1 | iex
```

The script installs the latest release, checks what it downloaded against the release's checksums, and its provenance when the GitHub CLI is signed in, and offers to install clang when a build still needs it: Snello builds through the system's **clang, version 15 or later**, which `snello doctor` checks. Every release also has an archive per platform on the [Releases page](https://github.com/snello-lang/snello/releases), and to work on the compiler, `go build -o bin/snello ./cmd/snello` in a clone builds it with Go alone. [Chapter 38](_docs/book/38-installing.md) has every way to install, upgrade and uninstall, how to verify a download, and the platforms and releases that are supported.

Then:

```sh
snello new hello
cd hello
snello run .
```

[Chapter 2](_docs/book/02-getting-started.md) takes it from there, and [chapter 32](_docs/book/32-the-snello-command.md) has every command.

## Features

### The language

- **Go's syntax.** `func`, `:=`, slices, maps, `defer`, `range`, methods with named receivers, multiple results, exported names by capitalization. A Go developer reads Snello on day one; [Appendix B](_docs/book/B-snello-for-go-programmers.md) lists what carries over and what to unlearn.
- **Memory safety.** No use after free, no double free, no borrow that outlives its value, no `nil` to dereference and no data race: the compiler checks every move and every borrow and refuses a program that breaks one. What only a running program can know is checked as it runs: an index outside a slice stops the program instead of reading past its end, and an arena index whose value was removed gives back `None` instead of another value. The one boundary is C a package binds, which the compiler cannot see into.
- **No garbage collector.** Every value has one owner and is freed where the owner dies, at a point the compiler knows, on every path out of a function, a panic's included, with its `Drop` run there. A program's peak memory is what it keeps alive, and it never pauses to collect. Sharing a value between owners is a choice you make, with `rc`, not the default.
- **Borrows without lifetime annotations.** A borrow is written `ref` or `mut` at the declaration and at the call, and lives inside one function: the compiler infers how long, or refuses the program with a message that says why.
- **No `nil`, no exceptions.** An absent value is `T?`, a failure is a `Result`, `?` passes it up and `??` gives a default. A value is never empty: a type without a sensible zero value has none.
- **Enums with data, and exhaustive `match`.** Variants carry fields, patterns destructure them, and a `match` that misses a case does not compile.
- **Traits.** Declared methods with defaults, fields, refinement and `super`, implemented by `impl`; a trait as a type is a value of any type that implements it. The standard traits, `Eq`, `Ord`, `Hash`, `Clone`, `Copy` and `Drop`, and the operator traits come with the language, and the compiler derives the ones it can.
- **Generics.** Generic types, functions and methods with trait bounds, inferred at the call, compiled once per instance with no boxing.
- **Reflection at compile time and attributes.** A function can look at the type it is given, its fields and variants, and typed attributes on them, all resolved by the compiler: `encoding/json` encodes a struct with the code you would write by hand.
- **More that keeps code plain.** Default field values, functions of a type (`Point.Of(...)`), owned pointers for recursive data, arenas with generation-checked indices, variadic functions, and iterators with `Map`, `Filter`, `Zip`, `Fold` and `Collect`.

### Concurrency

- **Tasks and channels.** `spawn` starts a task on Snello's own scheduler, one worker per core with work stealing and preemption, so a task that computes without waiting never holds up the others. Channels and `select` connect them, and a channel ends when its last sender is dropped.
- **No data races.** What a task may take across its boundary, `Send`, is inferred from the type's structure, and the compiler refuses a program that would share a value unsafely.
- **Tasks that fail on their own.** `spawn` gives a handle whose `Join` returns the task's result or the panic that ended it; a group of tasks is cancelled together, and a deadline bounds a function's calls and every task they start.

### The standard library

Written in Snello, under the rule that no mistake a caller can make panics:

| Area | Packages |
|---|---|
| Text and numbers | `fmt` with formats checked at compile time, `strings`, `bytes`, `strconv`, `unicode` with `utf8`, `utf16`, normalization and segmentation, `math`, `math/bits`, `math/rand` |
| Collections | slices and maps built in, `deque`, `heap`, `set`, `sort`, `iter`, `arena`, `rc` |
| Systems | `os`, `os/exec`, `os/signal`, `io`, `bufio`, `path`, `path/filepath`, `time`, `net` |
| Concurrency | `sync`, `task` |
| Data | `encoding/json` |
| Testing | `testing`, `expect` |

### The `snello` command

One command does the whole job, with nothing else to install:

- **`new`, `init`, `build`, `run`:** start a module and build to a native binary, a file or a directory at a time.
- **`test`:** tests as plain functions, subtests, parallel tests, benchmarks, examples and golden files.
- **`check`, `doc`, `doc -http`:** the checker's errors without a build, and every package's documentation in a terminal or a browser.
- **`add`, `remove`, `update`, `deps`, `fetch`:** dependencies by their repository and a release tag, locked in `snello.lock`, fetched once into a module cache.
- **`debug`:** a debug build under lldb, gdb or cdb, with Snello's values and tasks shown.
- **`lsp`:** the language server, below.
- **`doctor`, `env`, `version`:** what a build needs and how to fix what is missing, and what it reads.

### The language server

`snello lsp` is part of the `snello` command, so wherever Snello is installed the server is too, at the same version, with nothing to set. It checks through the compiler's own loader and checker, so the editor and the build never disagree on a program.

- **Errors as you type.** Every syntax error and every error of the checker, the borrow checker's included, on the word it names, word for word what `snello check` prints, from the editor's text, saved or not, test files included.
- **Every platform's files.** A file for another system, `poll_windows.snello` open on a Mac, is checked for its own system.
- **Hover and go to definition.** A name's declaration as the checker types it, with its doc comment, and a jump to where it is declared: in the module, a module it requires or the standard library.
- **Outline and workspace symbols.** A file's declarations as a tree, and every declaration of the module found by a few of its letters.
- **References and rename.** Every use of a name across the module, and a rename of all of them at once, refused when the program would no longer compile or would mean something else.
- **Completion.** After a dot, the members of what is before it; on a name, every name visible there; in an import, the packages.

The [VS Code extension](https://github.com/snello-lang/vscode-extension) starts it for you, and [chapter 40](_docs/book/40-the-language-server.md) shows Neovim and Helix, which need only the command.

### Builds

- **Native binaries through LLVM or C.** Two backends over one runtime, held to the same tests; the LLVM backend is the default.
- **Six platforms.** Linux, macOS and Windows on x86-64 and arm64, each built for from another with `-target` and the target's system files ([chapter 34](_docs/book/34-platforms.md)).
- **Small, safe stacks.** Each task's stack is sized from the linked binary, so a task takes the memory it can use and no more, and running past it ends the program instead of corrupting memory.
- **Panics that say where.** A panic prints its message and the task's stack, each frame's file and line, in every build, a stripped binary's included.
- **C when you need it.** A function without a body in a package is bound to C beside it, and a handle is freed by its `Drop`.

Performance is measured against Go on every pull request: the same programs in both languages, timed on x86-64 and arm64. The latest run's table is in the `bench` job's summary, and `SNELLO_BENCH=1 go test ./bench -run TestBenchmarks -v` prints any machine's own.

## Documentation

- [The book](_docs/book/): the language, one feature per chapter, from [the introduction](_docs/book/01-introduction.md).
- [The design](_docs/design/): every decision behind the language and the reasoning for it.
- [Examples](examples/): programs that build and run, and programs that break each rule with the message the compiler gives.
- `snello doc -http`: the standard library's documentation, and your module's, in a browser.
- [Security](SECURITY.md): how to report a vulnerability privately, and the releases that get fixes.

## Contributing

Contributions are welcome. The compiler is written in Go, with nothing but Go's standard library, and working on it takes Go, `clang` and a C compiler. The pinned [Roadmap](https://github.com/snello-lang/snello/issues/122) lists the work in order; [CONTRIBUTING.md](CONTRIBUTING.md) says how it is done, with [the coding standards](CODING_STANDARDS.md) and [the code of conduct](CODE_OF_CONDUCT.md).
