# Stage 1: core checker and C backend

Goal: `teak build FILE` type-checks a program written in the core of Teak, enforces ownership and borrowing, emits C, and produces a native binary. No traits, no user generics. This stage decides whether the memory model holds.

The predeclared names, number types and the library packages are defined in `../design/05-standard-library.md`; the checker's universe scope is built from its section 1. Core Teak for this stage: structs, enums and `match`, functions and methods with `ref`/`mut`/value receivers, `*T` and `&expr`, `T?` and `Result` with `?` and `??` as built-ins, slices, maps, strings, `Copy` for numbers and plain structs, `Drop`, `defer`.

## Task 1.0: Standard library design

**Description:** A design document, `../design/05-standard-library.md`, that says what the library is, before any checker code depends on the answer. It settles four questions, each with its precedent: which names are predeclared by the language and which come from packages; that there is no prelude, so `Arena`, `Arc` and the rest are imported; that package names and layout follow Go while signatures follow Teak's ownership rules, with a new name wherever ownership changes a function's meaning; and the three signature rules (read-only parameters are `ref`, changed ones `mut`, kept ones by value). It also decides the one language rule the library forces: whether an owned value passed to a `ref` parameter is borrowed implicitly, as a receiver already is. It lists the stage 1 mapping table. It does not list every function; the examples do that, and task 2.6 keeps its criterion.

**Acceptance criteria:**
- [x] The document exists with the four decisions and the call-site rule, each marked decided (2026-10-18; the three items without precedent were decided on the recommendation and await the owner's review)
- [x] The list of predeclared names is in one place and the stage 1 spec refers to it
- [x] Every name the twenty examples use without an import is either predeclared or the example gains the import (examples 01, 03, 12, 15)

**Verification:**
- [x] `teak parse` still accepts all twenty examples after the import sweep
- [ ] Design review by the human

**Dependencies:** Stage 0 checkpoint
**Files:** `_docs/design/05-standard-library.md`, `examples/`

**Done:** 2026-10-18, pending the owner's design review
**Scope:** M

## Task 1.1: Core example set and negative tests

**Description:** Write the stage's acceptance suite before the checker: versions of the examples that use only core Teak, and a set of programs the checker must reject, each with the expected message on the offending line in a `// ERROR "..."` comment, as the Go compiler's errorcheck tests do.

**Acceptance criteria:**
- [x] At least eight positive programs under `examples/core/`, covering the tree, the linked list, the state machine, file copying, the parser and the binary search tree without generics (2026-10-18: tree, list, state, files, calc, bst, wordfreq, config)
- [x] At least twenty negative programs under `examples/core/errors/`: use after move, move in a loop, two `mut` borrows, borrow stored in a struct, borrow returned from two inputs, borrow across `spawn`, `nil` used, missing match arm, and so on (2026-10-18: 27 programs)
- [x] Each negative program has exactly one `// ERROR` line

**Verification:**
- [x] All positive programs parse: `go run ./cmd/teak parse` (2026-10-18; the parser's example test now covers them)
- [x] Reviewed against `../design/04-borrows-ref-and-mut.md`: every rule has at least one negative program (the table in `examples/core/errors/README.md`)

**Done:** 2026-10-18; book chapters 5 and 6 written with it

**Dependencies:** 1.0
**Files:** `examples/core/*.teak`, `examples/core/errors/*.teak`
**Scope:** L, split across two sessions if needed

## Task 1.2: Type representation

**Description:** The `types` package: the representation of every core type, identity and assignability, the universe scope with the basic types, `Option` and `Result` as built-in enums.

**Acceptance criteria:**
- [x] Types for basic, struct, enum, pointer, option, slice, array, map, channel, function, tuple, `ref` and `mut` (2026-10-18; plus `Result` and named types)
- [x] `Identical` and `AssignableTo` with tests
- [x] `Copy` is a property computed from structure, not declared (`IsCopy`)

**Verification:**
- [x] Tests pass: `go test ./types/`

**Dependencies:** 1.1
**Files:** `types/types.go`, `types/object.go`, `types/scope.go`, `types/universe.go`, `types/predicates.go`, `types/types_test.go`

**Done:** 2026-10-18; objects and scopes started early because the universe needs them. Appendix A of the book written with it.
**Scope:** M

## Task 1.3: Scopes and name resolution

**Description:** Package, file, function and block scopes; declaration before use rules as in Go; resolution of every identifier in the AST to its object; errors for undefined and redeclared names.

**Acceptance criteria:**
- [x] Every identifier in the core examples resolves (2026-10-18; `TestCoreExamples`)
- [x] Undefined and redeclared names report the position of both uses (`x redeclared in this block; first declared at 2:8`)

**Verification:**
- [x] Tests pass: `go test ./types/`
- [x] Manual check: `teak check examples/core/tree.teak` reports nothing

**Dependencies:** 1.2
**Files:** `types/check.go`, `types/resolve.go`, `types/typexpr.go`, `types/check_test.go`, `cmd/teak/main.go`

**Done:** 2026-10-18. Type expressions are resolved here as well, so every declared object has its type before task 1.4; `nil-used.teak` is the first negative program the checker passes, and the test harness for the rest (`TestNegativePrograms`) is in place, insisting that unhandled programs produce no error.
**Scope:** M

## Task 1.4: Expression and statement checking

**Description:** Types for every expression; checking of assignments, calls, returns, `?` and `??`, `match` exhaustiveness over enums and options, `if` option binding, `for range` forms. No ownership yet.

**Acceptance criteria:**
- [x] All positive core programs check with no errors (2026-10-18)
- [x] A missing match arm names the missing variant (`match is not exhaustive: missing Failed(_)`, also for tuples: `missing (Some(_), None)`)
- [x] `?` in a function returning the wrong kind reports what kind was expected

**Verification:**
- [x] Tests pass: `go test ./types/`

**Dependencies:** 1.3
**Files:** `types/expr.go`, `types/stmt.go`, `types/match.go`, `types/library.go`, `types/expr_test.go`
**Scope:** L, split into expressions then statements

**Done:** 2026-10-18, in one slice. Expressions carry a mode (value, owned place, borrowed place) beside their type for the ownership passes; literals and None take their type from context; operators read through borrows; the stage 1 library (`fmt`, `os`, `io`, and the predeclared methods) lives in `library.go`; exhaustiveness is the usefulness check from Maranget's "Warnings for pattern matching". Negative programs handled: nil-used, missing-arm, wrong-question-kind, ref-as-mut. Book chapter 8 written with it.

## Task 1.5: C emitter and `teak build`

**Description:** Emit C source for a checked core program and build it with the system C compiler into a native binary. Enums become tagged unions, `match` an if chain, `T?` a struct with a flag, closures a function with an environment, and the runtime under `emit/c/runtime/` provides strings, slices, maps, printing and panics. (Changed 2026-10-18 from a Go emitter; see the design document.) The work goes in slices, each adding programs to `examples/run/` and giving a core program its `.out` file when it starts to build.

**Slices:**
- [x] Functions, numbers, strings and control flow; `teak build FILE` (2026-10-18)
- [x] Structs, enums, methods, `match`, options, results, tuples, owned pointers, `?`, `??`, if expressions and if bindings; `teak emit FILE` (2026-10-18; `calc.teak` builds. Two checker clarifications came with it: `*v += 1` for a bare `mut` borrow of a number, and an owned pointer lent as a `ref` of what it holds)
- [x] Slices, string methods, option and result methods, `take`, equality of composite values (2026-10-18; `list.teak` and `bst.teak` build. Printing quotes a string inside `Some`, `Ok`, `Err` and a variant and prints an empty struct as its name, until the library in Teak decides in task 2.6)
- [x] Maps (2026-10-18; an insertion-ordered hash map in the runtime with a generated hash and equality per key type; `config.teak` still waits for `os.ReadToString` in the last slice)
- [x] Closures, `defer`, and the `os` and `io` functions (2026-10-18; `config.teak`, `tree.teak` and `wordfreq.teak` build. A closure is lifted to a function with an environment of pointers to what it captures; `defer` saves its arguments at the statement and runs before every return; `error` is its message; a program's input files sit beside it in `name.files/`)

**Done:** 2026-10-18, in five slices. All of core Teak but `Drop` compiles; `examples/run/` holds twelve programs that build and run, and five of the eight core programs have their `.out` file. `state.teak` and `files.teak` print or clean up from `Drop` and wait for task 1.9, which also decides what a clone of a slice of strings owns and frees what `&` allocated.

**Acceptance criteria:**
- [x] `teak build hello.teak` produces a native binary that prints hello (2026-10-18)
- [ ] Every positive core program builds and runs with its expected output (calc, list, bst, config, tree and wordfreq 2026-10-18; state and files need the drops of task 1.9)
- [x] The emitted C compiles with `cc -std=c11 -Wall -Wextra -Werror` (2026-10-18; every build and every test uses these flags, plus `-fwrapv` and `-fno-strict-aliasing`)

**Verification:**
- [x] Tests pass: `go test ./emit/c/` (2026-10-18)
- [x] Manual check: build and run three core programs (2026-10-18: calc, list, bst)
- [x] Every program under `examples/run/` prints its `.out` file; each slice adds the programs it makes buildable (2026-10-18)

**Dependencies:** 1.4
**Files:** `emit/c/`, `emit/c/runtime/`, `cmd/teak/main.go`
**Scope:** L

## Task 1.6: Moves

**Description:** Ownership tracking as a dataflow pass over each function: a value is moved by assignment, by passing by value, by returning, by sending, and by capture in `spawn`; using a moved value is an error; `Copy` types are exempt; partial moves out of a value about to be dropped are allowed; moves inside loops and conditionals are tracked by merging states at joins.

**Acceptance criteria:**
- [ ] Use after move reports both the use and the move
- [ ] A move inside a loop body of a value declared outside is an error on the second iteration
- [ ] A move in one branch and a use after the `if` is an error

**Verification:**
- [ ] Tests pass: `go test ./types/`
- [ ] The move-related negative programs fail with their `// ERROR` messages

**Dependencies:** 1.5
**Files:** `types/own.go`, `types/own_test.go`
**Scope:** L

## Task 1.7: Borrows

**Description:** `ref` and `mut` borrows as part of the same pass: creation at call sites and local bindings, liveness to last use within the function, exclusivity (any number of `ref` or one `mut`), the owner frozen while borrowed, reborrowing a chain (`cur = mut n.left`), and the two-mutable-paths rule for slices. One case for this pass to decide: a `match` with `mut` bindings on `m[k]` for a key the map does not hold writes a temporary, since reading a missing key yields the zero value and not an entry; the checker should reject a `mut` binding through a map read, or require `GetMut`.

**Acceptance criteria:**
- [ ] A `mut` borrow while a `ref` is live is an error naming both
- [ ] `v.Push(v.Len())` is accepted: arguments are evaluated before the receiver is borrowed
- [ ] The binary search tree's insert and get check without errors
- [ ] The LRU's rejected form is rejected; the accepted form is accepted

**Verification:**
- [ ] Tests pass: `go test ./types/`
- [ ] The borrow-related negative programs fail with their messages

**Dependencies:** 1.6
**Files:** `types/borrow.go`, `types/borrow_test.go`
**Scope:** L

## Task 1.8: Escaping borrows

**Description:** The rules that keep borrows from escaping: no borrow in an owned struct field, channel, or `spawn` capture; a returned borrow tied to the receiver or the sole borrowed parameter and nothing else; borrow-holding structs and closures treated as borrows.

**Acceptance criteria:**
- [ ] `Peek() ref T?` on a `ref` receiver is accepted; the same on a function with two borrowed parameters is rejected with the message from the design note
- [ ] A struct with a `ref` field can be created and used locally and returned under the one-source rule, but not stored or sent
- [ ] A closure that assigns to a captured local cannot be returned

**Verification:**
- [ ] Tests pass: `go test ./types/`
- [ ] Core parser example (lexer holding `src ref string`) checks

**Dependencies:** 1.7
**Files:** `types/escape.go`, `types/escape_test.go`
**Scope:** M

## Task 1.9: Drops and release checking

**Description:** Scope-exit drops in order, on every exit path; `Close`-style consuming methods; the emitted C frees at every computed drop; a debug build poisons freed memory and asserts that nothing released is reachable afterwards, and a leak checker runs over the core suite, so that a checker mistake is a failing test.

**Acceptance criteria:**
- [ ] A `Drop` type's method runs at scope exit, on early return, and on panic, in the emitted C
- [ ] `teak build -debug` panics with a message if a released value is touched
- [ ] The file-copy core example closes both files on every path

**Verification:**
- [ ] Tests pass: `go test ./emit/c/`
- [ ] Manual check: a program that releases then uses a value panics under `-debug`

**Dependencies:** 1.8
**Files:** `emit/c/drop.go`, `emit/c/runtime/debug.c`
**Scope:** M

## Task 1.10: Core suite green

**Description:** The whole stage's acceptance: every positive core program builds and runs, every negative program is rejected with its message, and the design notes are updated with anything the checker forced.

**Acceptance criteria:**
- [ ] `go test ./...` runs the core suite and passes
- [ ] Any rule that changed is updated in `../design/04-borrows-ref-and-mut.md` and the examples

**Verification:**
- [ ] Tests pass: `go test ./...`
- [ ] Manual check: build and run all positive core programs in one script

**Dependencies:** 1.9
**Files:** `types/suite_test.go`, design notes as needed
**Scope:** S

## Checkpoint: stage 1 (decision point)

- [ ] All tasks above checked
- [ ] Every negative program fails for the planned reason and no other
- [ ] **Decision:** the ownership model holds as designed, or the design document is revised before stage 2. Record the decision in `../design/01-language-design.md`
- [ ] The book chapters this stage owns (see `../book/README.md`) are written and their status lines updated
- [ ] Reviewed by the owner
