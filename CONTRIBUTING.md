# Contributing to Snello

Thank you for helping build Snello. This page says how to find work, how to set up, how to test a change, and what a pull request needs.

## Finding work

The work is planned on GitHub. The pinned [Roadmap](https://github.com/snello-lang/snello/issues/122) lists every open issue in the order it is to be done, and each [milestone](https://github.com/snello-lang/snello/milestones) is a stage of it. An issue is one task, the size of one pull request: a description, the acceptance criteria and the verification steps as checklists, then the issues it depends on, the files it touches and its scope (S, M or L). Its label says where it lives:

| Label | Where |
|---|---|
| `compiler/runtime` | The compiler under `compiler/`, its backends, the C runtime under `runtime/`, and the tools under `cmd/` |
| `corelibs` | The standard library under `std/`, written in Snello |
| `lib` | The libraries that ship outside the compiler under `lib/` |

Comment on an issue before starting it, so two people do not build the same thing. A bug, or a feature that is not planned, starts as a new issue in the same shape.

## Setting up

You need Go 1.27.2 or later, `clang` for the LLVM backend, and a C compiler, `cc`, for the C backend. The compiler has no dependencies beyond Go's standard library.

```
git clone https://github.com/snello-lang/snello
cd snello
go build -o bin/snello ./cmd/snello
bin/snello build examples/hello/hello.snello
./hello
```

`clang` and `cc` are found on the path, or `CLANG` and `CC` name them.

Before changing anything, read the design that governs it: [the language design](_docs/design/01-language-design.md) for every decision and why, [the syntax conventions](_docs/design/00-syntax-conventions.md), and the note on the part you are changing in [`_docs/design/`](_docs/design/). The design wins over the code: when they disagree, say so in the issue rather than change either side.

## Testing a change

```
go build ./...
go vet ./...
gofmt -l .
go test ./...
```

`gofmt -l .` must print nothing. `go test ./...` builds and runs every example through both backends, as a release build and as a debug build, so it needs `clang` and `cc` and takes a few minutes; `go test ./compiler/...` is the quick loop while you work.

The Makefile runs Snello's own tests through `snello test`: `make test` runs the library's and the examples' tests, `make test-std` and `make test-examples` each half, and `make check` runs `snello check` over the examples. Each runs inside the Linux image CI uses, built from `build/Dockerfile`, so it needs Docker; `NATIVE=1` runs it on your machine instead. `build/run.sh go test ./...` runs the Go tests inside the same image. CI runs the tests on x86-64 Linux, arm64 Linux and arm64 macOS.

## A pull request

- **One issue, one pull request.** Keep it to the issue's scope, and check off its acceptance criteria as you meet them.
- **Tests with the change.** Every Go file has its `_test.go`. A checker change comes with the programs it must refuse, each with its expected message. Use only Go's `testing` package.
- **Docs with the change.** A change to the language updates its design note and its chapter of [the book](_docs/book/) in the same pull request.
- **The style.** [The coding standards](CODING_STANDARDS.md) say how code is written here; every `.go`, `.snello` and `.c` file starts with the license header they show. Markdown is not hard-wrapped, and Snello code in it is fenced as `go`, since GitHub does not highlight Snello.
- **Conventional Commits.** Commit messages and the pull request's title are `type(scope)!: subject`: the type one of `feat`, `fix`, `docs`, `test`, `ci`, `chore`, `refactor`, `perf` or `revert`, the subject in lower case with no period, body lines at most 100 characters. CI checks both, and a squash merge makes the title the commit on `main`.
- **You are the author.** You must be able to explain and defend every line in review. Contributors working with AI tools also follow [AGENTS.md](AGENTS.md), which forbids crediting a tool as an author.

## Conduct

Everyone taking part follows [the code of conduct](CODE_OF_CONDUCT.md).
