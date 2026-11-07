# Stage 8: built-in libraries

Goal: the libraries that ship with Snello outside the compiler, under `lib/` at the repository's root, each a package of the program's own with nothing the standard library is privileged to, built on the core stage 5 finished: packages, the module file and C bound from a package. They are what Rust's crates are to its `std`: the standard library stays the small, rock-solid core of `05-standard-library.md`, decision 7, and everything that encodes a format, speaks a protocol or talks to a service is a library here. Each is held to the industry's reference implementation and measured against Go's and Rust's, as the standard packages are. Planned 2026-11-07, when the owner moved `regexp` out of stage 5 so that the core stays lean; the stage is taken after the core is in use, and its number is an index.

## Task 8.1: How a shipped library is found

**Description:** A library under `lib/` must reach a program without the program naming the compiler's own directory. The design decides how, in `../design/08-packages.md`: the lookup of section 4 gains a place after the standard library and before the module's directories, where a library the compiler ships is found by its name, `import "regexp"`, with its source embedded in the compiler as `std/` is, or a `require` line that names it with no directory, `require regexp`, which says the dependency is one the compiler ships. The first reads as Go's standard library does and the second as Cargo's dependency on a published crate; the note weighs them against decision 7, which keeps the standard library small on purpose, and the owner decides. Either way a shipped library is compiled with the program from source, as the standard library is, and is a package of the program's own in every rule: no function without a body but through its own C side, none of the standard library's names.

**Acceptance criteria:**
- [ ] The decision recorded in `../design/08-packages.md` with the reason, reviewed by the owner before the code
- [ ] A program imports a library under `lib/` by the form decided, through both backends, with the program's own packages and the standard library beside it
- [ ] A library under `lib/` that uses a standard privilege is refused with the program's message

**Verification:**
- [ ] `go test ./compiler/importer ./compiler/emit/...`
- [ ] Tests pass: `go test ./...`

**Dependencies:** Stage 5, tasks 5.3 and 5.5
**Files:** `compiler/importer/`, `lib/`, `../design/08-packages.md`, `../book/02-getting-started.md`
**Scope:** S


## Task 8.2: `regexp` as a package outside the compiler

**Description:** The proof that the core is enough: `regexp` written under `lib/regexp/` at the repository's root, outside `std/`, with nothing a package of the program's own cannot write, so that it compiles under the rules of task 5.3 and binds no C. Go's syntax, the RE2 subset, compiled to a program and run by a Pike VM, which is what Go's `regexp` and the `PikeVM` of Rust's `regex` crate run, so a match takes time linear in the input and no pattern backtracks; the subset is what example 19 uses and the common forms, literals, `.`, classes with ranges and `\d`, `\w`, `\s`, the anchors, `*`, `+`, `?`, `{n,m}`, alternation, groups and the `(?i)` flag, written in the package's doc comment. `Compile` returns a `Result` whose error carries Go's message for the pattern, `missing closing )`; `Match` says whether a line matches, `Find` returns the span of the first match or none. Every gap the package meets in the language is a finding of this task, closed in the core or recorded as a decision in the design note it belongs to; that is what the proof is for. The package is measured against Go's `regexp` and Rust's `regex` crate on a grep of a 100 MB file, as every standard package is measured, with the numbers in the package's README; Rust's crate runs a lazy DFA and is expected to be well ahead, Go's is the target.

**Acceptance criteria:**
- [ ] `lib/regexp/` compiles as a package of the program's own under task 5.3's rules, with no function without a body and none of the standard library's names
- [ ] Table tests for the matcher over every form of the subset, and a malformed pattern refused by `Compile` with Go's message
- [ ] `(a*)*b` against thirty `a`s and no `b`, which a backtracking matcher takes exponential time on, matches in under a millisecond
- [ ] Not slower than Go's `regexp` on the grep of a 100 MB file, measured against Go's and Rust's with the method of stage 5's task 5.8 and recorded in `lib/regexp/README.md`
- [ ] Every language gap the package met is closed or recorded, listed in this task's progress

**Verification:**
- [ ] `go test ./lib/...`, which builds the package's tests through both backends
- [ ] Tests pass: `go test ./...`

**Dependencies:** Stage 5, tasks 5.3 and 5.5; 8.1
**Files:** `lib/regexp/`, `compiler/types/`, `compiler/emit/`
**Scope:** L

## Task 8.3: Example 19 on `regexp`

**Description:** Example 19, the grep, imports `regexp` from `lib/` by the form task 8.1 decided, since the examples show how Snello programs are written and a program with a dependency is one of them; this is the owner's decision, the alternative being a plain-substring grep over the standard library alone. The example also fails the checker for three reasons outside the library: it names `bufio.Writer` without its type argument, it ranges over `bufio.Scanner.Lines`, which does not exist, and `fmt.Fprintf` cannot infer its writer from the argument; each is settled as a library change or an example change, recorded in the design note it belongs to.

**Acceptance criteria:**
- [ ] Example 19 runs with an expected output and the files it reads, through `lib/regexp`, and leaves the pending list; the decision recorded in `examples/README.md`
- [ ] The pending list in `examples/suite_test.go` is empty

**Verification:**
- [ ] Tests pass: `go test ./examples`

**Dependencies:** 8.2
**Files:** `examples/19-cli-grep.snello`, `examples/expected/19-cli-grep.out`, `examples/suite_test.go`, `examples/README.md`
**Scope:** S

## Checkpoint

The owner reviews the first library written outside the compiler: `regexp` under `lib/`, found by a program the way task 8.1 decided, matching in linear time, measured against Go's and Rust's with the numbers in its README, and every gap it met in the core closed or recorded as a decision. All twenty examples run natively on the three runners, with an empty pending list, and the book's chapter 14 names the libraries that ship beside the standard library and how a program reaches them.
