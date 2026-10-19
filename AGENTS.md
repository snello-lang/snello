# Instructions for AI agents

This file is read by coding agents working in this repository. It says what the project is, how work is done here, and what must not change without a human decision.

## What this is

Teak is a programming language: Go's syntax and concurrency, memory safety without a garbage collector, no lifetime annotations, and Scala-style traits. The compiler in this repository builds core Teak to native binaries today (stages 0 and 1 of `_docs/specs/plan.md`); traits, generics, tasks and the standard library in Teak are the stages ahead. The compiler is written in Go, after the Go toolchain's own packages, and a Teak program runs on Teak's own runtime with nothing of Go inside it.

## Read first, in this order

1. `_docs/design/01-language-design.md`: every decision and why.
2. `_docs/design/00-syntax-conventions.md`: the syntax the examples use.
3. `_docs/design/02-` to `04-`: the memory model notes.
4. `_docs/design/05-standard-library.md`: what the language predeclares, where every other name comes from, and the signature rules.
5. `_docs/book/README.md`: the user-facing book and which chapters exist.
6. `examples/README.md`: what each example tests and the decisions they forced.
7. `CODING_STANDARDS.md`: how code is written here.
8. `_docs/specs/plan.md` and the stage file for the current stage: what is done, what is next, and the acceptance criteria for each task. Check a task off, with the date, only after running its verification steps.

Do not propose anything these documents already decided without saying which decision you are reopening and why.

## How work is done

- **One slice at a time.** Land one small, runnable, tested piece per turn, explain what it does and why it is shaped that way, and stop so the owner can read it. Do not deliver several packages at once.
- **Explain by analogy to Go.** The compiler follows the Go toolchain's design, so point at the counterpart (`go/scanner`, `go/parser`, `go/types`) for every design choice, and explain in Go's terms, not Rust's, OCaml's or type theory's. Never explain a Teak feature with Rust vocabulary.
- **Design before code.** A new construct goes into the syntax conventions and an example before it goes into the parser.
- **Tests with every slice.** Table tests, error tests, and the examples sweep, using only the standard `testing` package.
- **The book grows with the code.** `_docs/book/` is the language book for users. A slice that implements or decides a language feature writes or updates the chapter that owns it in the same change, and keeps the chapter's status line true. The book's `README.md` lists the chapters and which stage writes each.
- **Do not commit or push.** The owner commits. Leave the working tree clean of temporary files.
- **No AI authorship.** Do not credit an AI tool, model, or bot as an author anywhere in a contribution. This includes `Co-Authored-By` commit trailers (e.g. `Co-Authored-By: Claude ...`), "Generated with ..." footers in PR descriptions, and any other attribution to an AI tool in commits, PR descriptions, issues, code, or comments. If your tooling adds such lines by default, remove them before committing and before opening the PR. The human who submits the PR is its sole author and must be able to explain and defend every line of it in review. PRs that credit an AI tool as an author will not be reviewed until the attribution is removed.

## Constraints that need a human to change

- **Zero dependencies.** The compiler imports only the standard library.
- **No garbage collector**, anywhere. Teak programs are native binaries: the C backend and its runtime own every allocation, and nothing may rely on a collector. Go is the language the compiler is written in, nothing more.
- **Simplicity.** If Go has a way to say it, Teak says it that way. A new construct must be readable by a Go developer without a glossary. The list of things deliberately left out is in the design document under "What Teak leaves out"; do not add any of them back.
- **The examples are the acceptance suite.** Every stage must process all twenty. Changing an example to make a stage pass requires changing the design document in the same change.
- **License header** on every `.go` and `.teak` file, in the form shown in `CODING_STANDARDS.md`.
- **Markdown is not hard-wrapped.** One line per paragraph; never reflow prose to a column width.

## Commands

```
go build ./...        # must be clean
go vet ./...          # must be clean
gofmt -l .            # must print nothing
go test ./...         # must pass
go run ./cmd/teak lex FILE     # print the token stream of a Teak file
go run ./cmd/teak parse FILE   # print the syntax tree
go run ./cmd/teak check FILE   # report what the checker finds; silent when the file is correct
go run ./cmd/teak build FILE   # build a native binary in the current directory; needs cc on the PATH, or CC set
go run ./cmd/teak emit FILE    # print the C source that build compiles
```

## Layout

| Path | Contents |
|---|---|
| `_docs/design/` | Design documents, numbered |
| `_docs/specs/` | Implementation plan and per-stage task lists |
| `_docs/book/` | The language book, written chapter by chapter as features land |
| `examples/` | The twenty Teak programs, plus Go versions of the first two; `core/` is the stage 1 suite, `run/` the programs that build and run today with their expected output |
| `token/` | Token kinds and positions, modeled on `go/token` |
| `scanner/` | The lexer, modeled on `go/scanner` |
| `ast/` | The syntax tree, modeled on `go/ast` |
| `parser/` | The parser, modeled on `go/parser` |
| `types/` | Types, objects, scopes and the checker, modeled on `go/types` |
| `emit/c/` | The C emitter and, under `runtime/`, the C runtime every Teak program links against |
| `.github/workflows/` | `test.yml` runs `go test ./...` on Linux and macOS on every push and pull request; the README's badge is its status |
| `examples/expected/` | The expected output of each example program that runs, `<name>.out`, with the files it reads in `<name>.files/`; `examples/suite_test.go` builds and runs every example that has one and names the rest as pending |
| `std/` | The standard library, written in Teak, one directory per package, embedded in the compiler; `std.go` is the importer that loads a package for a program importing it; `builtin/` documents the predeclared names and is never imported, with `builtin_test.go` keeping it in step with the checker |
| `cmd/teak/` | The command-line driver |
| `_docs/brand/` | Logo and palette |

Packages to come: the LLVM backend under `emit/`.

## When the design and the code disagree

The design document wins until the owner says otherwise. Report the disagreement; do not silently change either side.
