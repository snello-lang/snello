# Instructions for AI agents

This file is read by coding agents working in this repository. It says what the project is, how work is done here, and what must not change without a human decision.

## What this is

Teak is a programming language in design: Go's syntax and concurrency, memory safety without a garbage collector, no lifetime annotations, and Scala-style traits. There is no compiler yet beyond what is in this repository. The owner is a Go developer building the language to learn compiler construction; the project's purpose is as much their learning as the language itself.

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
- **Explain by analogy to Go.** The owner knows Go well and does not know Rust, OCaml, or type theory. Point at the Go toolchain counterpart (`go/scanner`, `go/parser`, `go/types`) for every design choice. Never explain a Teak feature with Rust vocabulary.
- **Design before code.** A new construct goes into the syntax conventions and an example before it goes into the parser.
- **Tests with every slice.** Table tests, error tests, and the examples sweep, using only the standard `testing` package.
- **The book grows with the code.** `_docs/book/` is the language book for users. A slice that implements or decides a language feature writes or updates the chapter that owns it in the same change, and keeps the chapter's status line true. The book's `README.md` lists the chapters and which stage writes each.
- **Do not commit or push.** The owner commits. Leave the working tree clean of temporary files.

## Constraints that need a human to change

- **Zero dependencies.** The compiler imports only the standard library.
- **No garbage collector**, anywhere in the language's semantics. The Go-emitting backend is scaffolding; nothing may rely on Go's collector.
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
go run ./cmd/teak lex FILE   # print the token stream of a Teak file
```

## Layout

| Path | Contents |
|---|---|
| `_docs/design/` | Design documents, numbered |
| `_docs/specs/` | Implementation plan and per-stage task lists |
| `_docs/book/` | The language book, written chapter by chapter as features land |
| `examples/` | The twenty Teak programs, plus Go versions of the first two |
| `token/` | Token kinds and positions, modeled on `go/token` |
| `scanner/` | The lexer, modeled on `go/scanner` |
| `cmd/teak/` | The command-line driver |
| `_docs/brand/` | Logo and palette |

Packages to come, in order: `ast`, `parser`, then the checker.

## When the design and the code disagree

The design document wins until the owner says otherwise. Report the disagreement; do not silently change either side.
