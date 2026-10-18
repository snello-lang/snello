# Stage 1: core checker and Go backend

Goal: `teak build FILE` type-checks a program written in the core of Teak, enforces ownership and borrowing, emits Go, and produces a binary. No traits, no user generics. This stage decides whether the memory model holds.

The predeclared names, number types and the library packages are defined in `../design/05-standard-library.md`; the checker's universe scope is built from its section 1. Core Teak for this stage: structs, enums and `match`, functions and methods with `ref`/`mut`/value receivers, `*T` and `&expr`, `T?` and `Result` with `?` and `??` as built-ins, slices, maps, strings, `Copy` for numbers and plain structs, `Drop`, `defer`.

## Task 1.0: Standard library design

**Description:** A design document, `../design/05-standard-library.md`, that says what the library is, before any checker code depends on the answer. It settles four questions, each with its precedent: which names are predeclared by the language and which come from packages; that there is no prelude, so `Arena`, `Arc` and the rest are imported; that package names and layout follow Go while signatures follow Teak's ownership rules, with a new name wherever ownership changes a function's meaning; and the three signature rules (read-only parameters are `ref`, changed ones `mut`, kept ones by value). It also decides the one language rule the library forces: whether an owned value passed to a `ref` parameter is borrowed implicitly, as a receiver already is. It lists the stage 1 mapping table. It does not list every function; the examples do that, and task 2.6 keeps its criterion.

**Acceptance criteria:**
- [ ] The document exists with the four decisions and the call-site rule, each marked decided by the human (draft written 2026-10-18; three items open)
- [ ] The list of predeclared names is in one place and the stage 1 spec refers to it
- [ ] Every name the twenty examples use without an import is either predeclared or the example gains the import

**Verification:**
- [ ] `teak parse` still accepts all twenty examples after the import sweep
- [ ] Design review by the human

**Dependencies:** Stage 0 checkpoint
**Files:** `_docs/design/05-standard-library.md`, `examples/`
**Scope:** M

## Task 1.1: Core example set and negative tests

**Description:** Write the stage's acceptance suite before the checker: versions of the examples that use only core Teak, and a set of programs the checker must reject, each with the expected message on the offending line in a `// ERROR "..."` comment, as the Go compiler's errorcheck tests do.

**Acceptance criteria:**
- [ ] At least eight positive programs under `examples/core/`, covering the tree, the linked list, the state machine, file copying, the parser and the binary search tree without generics
- [ ] At least twenty negative programs under `examples/core/errors/`: use after move, move in a loop, two `mut` borrows, borrow stored in a struct, borrow returned from two inputs, borrow across `spawn`, `nil` used, missing match arm, and so on
- [ ] Each negative program has exactly one `// ERROR` line

**Verification:**
- [ ] All positive programs parse: `go run ./cmd/teak parse`
- [ ] Reviewed against `../design/04-borrows-ref-and-mut.md`: every rule has at least one negative program

**Dependencies:** 1.0
**Files:** `examples/core/*.teak`, `examples/core/errors/*.teak`
**Scope:** L, split across two sessions if needed

## Task 1.2: Type representation

**Description:** The `types` package: the representation of every core type, identity and assignability, the universe scope with the basic types, `Option` and `Result` as built-in enums.

**Acceptance criteria:**
- [ ] Types for basic, struct, enum, pointer, option, slice, array, map, channel, function, tuple, `ref` and `mut`
- [ ] `Identical` and `AssignableTo` with tests
- [ ] `Copy` is a property computed from structure, not declared

**Verification:**
- [ ] Tests pass: `go test ./types/`

**Dependencies:** 1.1
**Files:** `types/type.go`, `types/universe.go`, `types/type_test.go`
**Scope:** M

## Task 1.3: Scopes and name resolution

**Description:** Package, file, function and block scopes; declaration before use rules as in Go; resolution of every identifier in the AST to its object; errors for undefined and redeclared names.

**Acceptance criteria:**
- [ ] Every identifier in the core examples resolves
- [ ] Undefined and redeclared names report the position of both uses

**Verification:**
- [ ] Tests pass: `go test ./types/`
- [ ] Manual check: `teak check examples/core/tree.teak` reports nothing

**Dependencies:** 1.2
**Files:** `types/scope.go`, `types/resolve.go`, `cmd/teak/main.go`
**Scope:** M

## Task 1.4: Expression and statement checking

**Description:** Types for every expression; checking of assignments, calls, returns, `?` and `??`, `match` exhaustiveness over enums and options, `if` option binding, `for range` forms. No ownership yet.

**Acceptance criteria:**
- [ ] All positive core programs check with no errors
- [ ] A missing match arm names the missing variant
- [ ] `?` in a function returning the wrong kind reports what kind was expected

**Verification:**
- [ ] Tests pass: `go test ./types/`

**Dependencies:** 1.3
**Files:** `types/expr.go`, `types/stmt.go`, `types/check_test.go`
**Scope:** L, split into expressions then statements

## Task 1.5: Go emitter and `teak build`

**Description:** Emit Go source for a checked core program and run `go build` on it. Enums become tagged structs, `match` becomes a switch, `T?` becomes a struct with a flag or a pointer, `Drop` becomes a deferred call. The first end-to-end program runs.

**Acceptance criteria:**
- [ ] `teak build hello.teak` produces a binary that prints hello
- [ ] Every positive core program builds and runs with its expected output
- [ ] Emitted Go is `gofmt`-clean

**Verification:**
- [ ] Tests pass: `go test ./emit/golang/`
- [ ] Manual check: run three core programs

**Dependencies:** 1.4
**Files:** `emit/golang/emit.go`, `emit/golang/emit_test.go`, `cmd/teak/main.go`
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

**Description:** `ref` and `mut` borrows as part of the same pass: creation at call sites and local bindings, liveness to last use within the function, exclusivity (any number of `ref` or one `mut`), the owner frozen while borrowed, reborrowing a chain (`cur = mut n.left`), and the two-mutable-paths rule for slices.

**Acceptance criteria:**
- [ ] A `mut` borrow while a `ref` is live is an error naming both
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
- [ ] A struct with a `ref` field can be created and used locally but not stored or returned
- [ ] A closure that assigns to a captured local cannot be returned

**Verification:**
- [ ] Tests pass: `go test ./types/`
- [ ] Core parser example (lexer holding `src ref string`) checks

**Dependencies:** 1.7
**Files:** `types/escape.go`, `types/escape_test.go`
**Scope:** M

## Task 1.9: Drops and release checking

**Description:** Scope-exit drops in order, on every exit path; `Close`-style consuming methods; emitted Go calls an explicit release at every computed drop; a debug build mode asserts that nothing released is reachable afterwards, so that Go's collector can never hide a checker mistake.

**Acceptance criteria:**
- [ ] A `Drop` type's method runs at scope exit, on early return, and on panic, in the emitted Go
- [ ] `teak build -debug` panics with a message if a released value is touched
- [ ] The file-copy core example closes both files on every path

**Verification:**
- [ ] Tests pass: `go test ./emit/golang/`
- [ ] Manual check: a program that releases then uses a value panics under `-debug`

**Dependencies:** 1.8
**Files:** `emit/golang/drop.go`, `emit/golang/runtime_debug.go`
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
- [ ] Reviewed by the owner
