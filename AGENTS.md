# Instructions for AI agents

This file is read by coding agents working in this repository. It says what the project is, how work is done here, and what must not change without a human decision.

## What this is

Snello is a programming language: Go's syntax and concurrency, memory safety without a garbage collector, no lifetime annotations, and Scala-style traits. The compiler in this repository builds the whole language to native binaries through two backends, LLVM and C, with tasks and channels on Snello's own scheduler and a standard library written in Snello. The compiler is written in Go, after the Go toolchain's own packages, and a Snello program runs on Snello's own runtime with nothing of Go inside it.

## Read first, in this order

1. `_docs/design/01-language-design.md`: every decision and why.
2. `_docs/design/00-syntax-conventions.md`: the syntax the examples use.
3. `_docs/design/02-` to `04-`: the memory model notes.
4. `_docs/design/05-standard-library.md`: what the language predeclares, where every other name comes from, and the signature rules.
5. `_docs/design/06-tasks-and-channels.md`: the runtime interface the compiler emits for tasks and channels, and the scheduler behind it.
6. `_docs/design/07-lowering.md`: the shape of every value and the point of every release, as both backends must agree on them; what the LLVM backend changes and why.
7. `_docs/design/08-packages.md`: a directory as a package, the module file, and how an import path is found.
8. `_docs/book/README.md`: the user-facing book and which chapters exist.
9. `examples/README.md`: what each example tests and the decisions they forced.
10. `CODING_STANDARDS.md`: how code is written here.
11. The [milestones](https://github.com/snello-lang/snello/milestones) and the issue of the current task: what is done, what is next, and the acceptance criteria for each task. See "Planning" below.

Do not propose anything these documents already decided without saying which decision you are reopening and why.

## How work is done

- **One slice at a time.** Land one small, runnable, tested piece per turn, explain what it does and why it is shaped that way, and stop so the owner can read it. Do not deliver several packages at once.
- **Explain by analogy to Go.** The compiler follows the Go toolchain's design, so point at the counterpart (`go/scanner`, `go/parser`, `go/types`) for every design choice, and explain in Go's terms, not Rust's, OCaml's or type theory's. Never explain a Snello feature with Rust vocabulary.
- **Design before code.** A new construct goes into the syntax conventions and an example before it goes into the parser.
- **Tests with every slice.** Table tests, error tests, and the examples sweep, using only the standard `testing` package.
- **The book grows with the code.** `_docs/book/` is the language book for users. A slice that implements or decides a language feature writes or updates the chapter that owns it in the same change, and keeps the chapter true to the code. The book's `README.md` lists the chapters.
- **Do not commit or push.** The owner commits. Leave the working tree clean of temporary files.
- **Conventional Commits.** A commit message and a pull request's title are `type(scope)!: subject`, the type one of `feat`, `fix`, `docs`, `test`, `ci`, `chore`, `refactor`, `perf` or `revert`, the scope optional, the subject in lower case with no period, the body after a blank line saying what changed and why. `.github/workflows/commitlint.yml` checks every commit and `pr-title-checker.yml` every title, as GoAkt's do, since a squash merge makes the title the commit on main.
- **No AI authorship.** Do not credit an AI tool, model, or bot as an author anywhere in a contribution. This includes `Co-Authored-By` commit trailers (e.g. `Co-Authored-By: Claude ...`), "Generated with ..." footers in PR descriptions, and any other attribution to an AI tool in commits, PR descriptions, issues, code, or comments. If your tooling adds such lines by default, remove them before committing and before opening the PR. The human who submits the PR is its sole author and must be able to explain and defend every line of it in review. PRs that credit an AI tool as an author will not be reviewed until the attribution is removed.

## Planning

The work is planned on GitHub, not in the repository. A [milestone](https://github.com/snello-lang/snello/milestones) is a stage of the roadmap. The pinned [Roadmap](https://github.com/snello-lang/snello/issues/122) issue lists every open issue in the order it is to be done: the next task is its first unchecked one, and a new issue is added to it in its place. An issue is one task, the size of one pull request, written in one shape: a **Description**, the **Acceptance criteria** and the **Verification** as checklists, then the **Dependencies**, the **Files** and the **Scope** (S, M or L). Each issue carries one or more area labels: `compiler/runtime` for the compiler, its backends, the C runtime and the toolchain, `corelibs` for the standard library under `std/`, and `lib` for the libraries that ship outside the compiler under `lib/`. Check a box only after running its verification step; a pull request closes its issue. A new task is a new issue in the same shape, in its milestone.

- **Vertical slices.** Every task ends with a command that does something visible on a real file, not with a layer that cannot be run.
- **Negative tests are first-class.** A checker change comes with the programs it must refuse, each with its expected message, in the style of the Go compiler's errorcheck tests.

### Definition of done, for every task

- `gofmt -l .` prints nothing; `go vet ./...` and `go test ./...` are clean.
- Every new `.go` and `.snello` file carries the license header.
- A package that processes source has a benchmark over the examples, and the task's commit records the allocations-per-operation figure if it changed.
- The design documents and the book are updated in the same change when the task changes what they describe.
- The issue's own verification steps have been run.

## Constraints that need a human to change

- **Zero dependencies.** The compiler imports only the standard library.
- **No garbage collector**, anywhere. Snello programs are native binaries: the C backend and its runtime own every allocation, and nothing may rely on a collector. Go is the language the compiler is written in, nothing more.
- **Simplicity.** If Go has a way to say it, Snello says it that way. A new construct must be readable by a Go developer without a glossary. The list of things deliberately left out is in the design document under "What Snello leaves out"; do not add any of them back.
- **The examples are the acceptance suite.** Every change keeps all twenty processing, and each one with an expected output running. Changing an example to make a change pass requires changing the design document in the same change.
- **License header** on every `.go` and `.snello` file, in the form shown in `CODING_STANDARDS.md`.
- **Markdown is not hard-wrapped.** One line per paragraph; never reflow prose to a column width.
- **Snello code in Markdown is fenced as `go`.** GitHub does not know Snello, and Go's highlighting fits its syntax.

## Commands

```
go build ./...        # must be clean
go vet ./...          # must be clean
gofmt -l .            # must print nothing
go test ./...         # must pass
go run ./cmd/snello lex FILE     # print the token stream of a Snello file
go run ./cmd/snello parse FILE   # print the syntax tree
go run ./cmd/snello check FILE   # report what the checker finds; silent when the file is correct
go run ./cmd/snello build FILE   # build a native binary in the current directory through the LLVM backend; needs clang on the PATH, or CLANG set
go run ./cmd/snello build DIR    # the same for a program of every .snello file in a directory, named after it
go run ./cmd/snello build -backend c FILE   # the same through the C backend, the reference; needs cc on the PATH, or CC set
go run ./cmd/snello emit FILE    # print the IR module that build compiles; -backend c prints the C source
```

## Layout

| Path | Contents |
|---|---|
| `_docs/design/` | Design documents, numbered |
| `_docs/book/` | The language book, written chapter by chapter as features land |
| `examples/` | The twenty Snello programs, plus Go versions of the first two; `core/` the ownership suite in core Snello, without traits or generics, `run/` the programs that build and run today with their expected output |
| `compiler/` | The compiler, written in Go, one package per pass; nothing under it ships inside a Snello program, which carries only `runtime/` and `std/` |
| `compiler/token/` | Token kinds and positions, modeled on `go/token`; a `FileSet` gives the files of a package distinct positions |
| `compiler/scanner/` | The lexer, modeled on `go/scanner` |
| `compiler/ast/` | The syntax tree, modeled on `go/ast` |
| `compiler/parser/` | The parser, modeled on `go/parser` |
| `compiler/types/` | Types, objects, scopes and the checker, modeled on `go/types`; `format.go` is the grammar of `fmt`'s formats, which the checker checks and both backends lower |
| `compiler/importer/` | Loads the packages a program imports, modeled on `go/importer`: a standard package from the embedded library, a package of the program's own from the directory its path names under the root `snello.mod` marks, or under a `require` line (`mod.go`), and the main package of a file or a directory for the driver and the tests (`program.go`); its tests check every standard package compiles and keep `std/builtin/` in step with the checker |
| `compiler/emit/lower/` | The rules both backends share: what a backend takes in, the names of the symbols, the library functions the compiler writes |
| `compiler/emit/c/` | The C emitter, the reference backend |
| `compiler/emit/llvm/` | The LLVM IR emitter, over the same runtime, built by clang |
| `compiler/emit/internal/backendtest/` | The programs both backends' tests build and run, with what each must give, so the backends are held to one list |
| `runtime/` | The C runtime every Snello program links against, under `c/`, one file a part: `snello.c` the core, a program's start and end, panics and what more than one file uses, which `internal.h` declares for the runtime alone; `slice.c`, `sort.c` (Rust's driftsort), `string.c`, `map.c`, `chan.c` and `sync.c` the values, channels and locks; `format.c`, `quote.c` and `parse.c` numbers and quotes as text; `bufio.c`, `os.c`, `print.c`, `math.c` and `time.c` the runtime's half of those packages; `alloc.c` the allocator, `unicode.c` the case, white-space and printable-rune tables `mkunicode.go` writes from Unicode's data files, `pow10.c` the powers of ten `mkpow10.go` writes, `sched.c` the scheduler, `net.c` the poller and sockets, `debug.c` the checking build; embedded by the package for both backends, which compile the files in parallel |
| `bench/` | The benchmark programs in Snello and Go behind the design document's performance table, timed by `SNELLO_BENCH=1 go test ./bench -run TestBenchmarks -v` |
| `.github/workflows/` | `commitlint.yml` and `pr-title-checker.yml` hold commit messages and pull request titles to Conventional Commits; `test.yml` checks gofmt and vet, runs `go test ./...` natively on x86-64 Linux, arm64 Linux and arm64 macOS, and times the benchmarks on the two Linux runners into the job's summary, on every push and pull request; the README's badge is its status |
| `examples/expected/` | The expected output of each example program that runs, `<name>.out`, with the files it reads in `<name>.files/`; `examples/suite_test.go` builds and runs every example that has one and names the rest as pending |
| `std/` | The standard library, written in Snello, one directory per package, embedded in the compiler by `std.go`, the only Go in it; `builtin/` documents the predeclared names and is never imported |
| `cmd/snello/` | The command-line driver |
| `_docs/brand/` | Logo and palette |

## When the design and the code disagree

The design document wins until the owner says otherwise. Report the disagreement; do not silently change either side.
