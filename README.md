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

Snello is a compiled language with Go's syntax and memory safety without a garbage collector. Every value has one owner and is freed where its owner dies; the compiler checks every use at compile time, and you never write a lifetime annotation. A program builds to one native executable. *Snello* is the Italian word for lean and nimble, and that is the whole claim: a program carries nothing it does not need, and the language carries nothing a Go developer has to learn twice.

## Why Snello

- **Go's syntax.** `func`, `:=`, slices, maps, `defer`, `range`. A Go developer reads Snello on day one.
- **No garbage collector, no leaks.** Values are freed at a point the compiler knows, on every path out of a function, including a panic; a program's peak memory is what it keeps alive, and it never pauses to collect.
- **No lifetime annotations.** Borrows are written `ref` and `mut` and live inside one function. The compiler infers every lifetime or refuses the program.
- **No `nil`, no exceptions.** An absent value is `T?`, a failure is a `Result`, and `?` passes it up. `match` is exhaustive.

[Chapter 1](_docs/book/01-introduction.md) of the book shows a program and what the compiler refuses in it.

## Quick start

Linux and macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/snello-lang/snello/main/release/install.sh | sh
```

Windows, in PowerShell:

```powershell
irm https://raw.githubusercontent.com/snello-lang/snello/main/release/install.ps1 | iex
```

Then:

```sh
snello new hello
cd hello
snello run .
```

The script installs the latest release, checks what it downloaded, and offers to install clang when a build still needs it. [Chapter 38](_docs/book/38-installing.md) has every way to install, by hand from a release, from source, and what a build needs on each system; [chapter 2](_docs/book/02-getting-started.md) takes it from there, and [chapter 32](_docs/book/32-the-snello-command.md) has every command. Snello runs on Linux, macOS and Windows, on x86-64 and arm64 ([chapter 34](_docs/book/34-platforms.md)).

In an editor, `snello lsp` is Snello's language server, errors as you type, hover, go to definition, references, rename and completion, from the compiler's own checker: the [VS Code extension](https://github.com/snello-lang/vscode-extension) starts it, and [chapter 40](_docs/book/40-the-language-server.md) shows Neovim and Helix.

## Documentation

- [The book](_docs/book/): the language, one feature per chapter, from [the introduction](_docs/book/01-introduction.md); readers coming from Go have [Appendix B](_docs/book/B-snello-for-go-programmers.md).
- [The design](_docs/design/): every decision behind the language and the reasoning for it.
- `snello doc -http`: the standard library's documentation, and your module's, in a browser.
- [Examples](examples/): programs that build and run, and programs that break each rule with the message the compiler gives.

## Contributing

Contributions are welcome. The compiler is written in Go, with nothing but Go's standard library, and working on it takes Go, `clang` and a C compiler. The pinned [Roadmap](https://github.com/snello-lang/snello/issues/122) lists the work in order; [CONTRIBUTING.md](CONTRIBUTING.md) says how it is done, with [the coding standards](CODING_STANDARDS.md) and [the code of conduct](CODE_OF_CONDUCT.md).
