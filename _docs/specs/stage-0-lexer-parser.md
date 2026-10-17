# Stage 0: lexer and parser

Goal: `teak parse FILE` reads any of the twenty example programs, builds a syntax tree, and prints it. Modeled on `go/scanner`, `go/ast`, `go/parser`.

Done when every task below is checked and the checkpoint has been reviewed.

## Task 0.1: Token set

**Description:** The `token` package: every token kind Teak has, keyword lookup, operator precedence, source positions.

**Acceptance criteria:**
- [x] All Go tokens Teak keeps, plus `trait`, `impl`, `enum`, `match`, `ref`, `mut`, `Self`, `super`, `?`, `??`, `=>`, `..=`
- [x] `Lookup` distinguishes keywords from identifiers
- [x] `Precedence` places `??` below every other binary operator

**Verification:**
- [x] Build succeeds: `go build ./...`

**Dependencies:** None
**Files:** `token/token.go`
**Scope:** S
**Done:** 2026-10-17

## Task 0.2: Scanner

**Description:** The lexer, with Go's semicolon-insertion rule extended to postfix `?`, comments skipped, and errors for malformed literals.

**Acceptance criteria:**
- [x] Representative snippets from the examples produce exact token sequences
- [x] Each kind of malformed literal reports one error with a specific message
- [x] All twenty examples lex with no illegal tokens

**Verification:**
- [x] Tests pass: `go test ./scanner/`

**Dependencies:** 0.1
**Files:** `scanner/scanner.go`, `scanner/scanner_test.go`
**Scope:** M
**Done:** 2026-10-18

## Task 0.3: `teak lex`

**Description:** The driver's first subcommand: print the token stream of a file with positions.

**Acceptance criteria:**
- [x] `go run ./cmd/teak lex examples/13-actor.teak` prints one token per line
- [x] Exit status 1 when the file has lexical errors

**Verification:**
- [x] Manual check on three example files

**Dependencies:** 0.2
**Files:** `cmd/teak/main.go`
**Scope:** XS
**Done:** 2026-10-17

## Task 0.4: AST node types

**Description:** The `ast` package: sealed interfaces `Node`, `Decl`, `Stmt`, `Expr`, `Type`, `Pattern`, each with an unexported marker method, and the concrete node structs for everything the examples use. Every node carries its position. No parser yet; this task is the data structure and a printer.

**Acceptance criteria:**
- [x] One struct per construct in `../design/00-syntax-conventions.md`, grouped by interface
- [x] An `ast.Print` that writes an indented tree, readable by a person
- [x] A test constructs a small tree by hand and checks the printed form

**Verification:**
- [x] Tests pass: `go test ./ast/`
- [x] `go vet ./...` reports no unused or unreachable type switch arms

**Dependencies:** 0.1
**Files:** `ast/ast.go`, `ast/print.go`, `ast/ast_test.go`
**Scope:** M
**Done:** 2026-10-18

## Task 0.5: Parser skeleton and `teak parse`

**Description:** The `parser` package with its token lookahead, error reporting with positions, synchronization after an error, and parsing of a file's package clause and imports only. The `teak parse` command prints the tree. This is the end-to-end path every later task extends.

**Acceptance criteria:**
- [x] `package x` with imports parses to a `File` node
- [x] An error names the position, what was found, and what was expected
- [x] After an error, parsing resumes at the next top-level declaration

**Verification:**
- [x] Tests pass: `go test ./parser/`
- [x] Manual check: `go run ./cmd/teak parse` on a file containing only a package clause and imports

**Dependencies:** 0.4
**Files:** `parser/parser.go`, `parser/parser_test.go`, `cmd/teak/main.go`
**Scope:** M
**Done:** 2026-10-18

## Task 0.6: Types

**Description:** Parsing of type expressions: named, qualified, generic instantiation `List<T>`, owned pointer `*T`, option `T?`, slice, array, map, channel, function, tuple `(A, B)`, and the parameter modes `ref T` and `mut T`. Decides the rule for `<` as a type-parameter bracket and records it in the syntax conventions.

**Acceptance criteria:**
- [ ] Every type form in the examples parses to the right node
- [ ] `a < b` as an expression is not mistaken for a type instantiation; the rule is written down
- [ ] `*Node<K, V>?` parses as option of pointer of instantiated type

**Verification:**
- [ ] Tests pass: `go test ./parser/`
- [ ] Design updated: `../design/00-syntax-conventions.md` states the `<` rule

**Dependencies:** 0.5
**Files:** `parser/types.go`, `parser/parser_test.go`, `../design/00-syntax-conventions.md`
**Scope:** M

## Task 0.7: Declarations

**Description:** Top-level declarations: `func` with named receivers in all three modes, `type ... struct impl A, B { }`, `type ... enum impl ... { }` with variants, `trait` with required methods, default methods, fields and refinement, generic parameter lists with bounds, `var` and `const`.

**Acceptance criteria:**
- [ ] Every declaration form in examples 07, 16, 17 and 18 parses
- [ ] A missing `{`, a stray `impl`, and a variant without parentheses each give a specific error

**Verification:**
- [ ] Tests pass: `go test ./parser/`
- [ ] Manual check: `teak parse examples/07-traits.teak` prints the trait and struct declarations

**Dependencies:** 0.6
**Files:** `parser/decl.go`, `parser/parser_test.go`
**Scope:** M

## Task 0.8: Statements

**Description:** Go's statement set: assignment and `:=`, `if` including the option-binding form `if x := opt {` and `if (k, v) := opt {`, all `for` forms including `range` over `ref` and `mut`, `switch`, `select`, `go`, `defer`, `return`, `break`, `continue`, send, blocks, and `match` as a statement.

**Acceptance criteria:**
- [ ] Every statement form in examples 02, 12 and 19 parses
- [ ] Go's `if init; cond {` and Teak's `if x := opt {` are both accepted and distinguished

**Verification:**
- [ ] Tests pass: `go test ./parser/`
- [ ] Manual check: `teak parse examples/12-worker-pool.teak`

**Dependencies:** 0.7
**Files:** `parser/stmt.go`, `parser/parser_test.go`
**Scope:** M

## Task 0.9: Expressions

**Description:** Precedence climbing over Go's operators plus `??` at the bottom; unary `ref`, `mut`, `&`, `*`, `-`, `!`, `<-`; postfix `?`, call, selector, index, slice; composite literals for structs, slices and maps; function literals; generic calls `arena.New<Node>()`; `if` as an expression.

**Acceptance criteria:**
- [ ] Every expression form in examples 06, 09, 15 and 20 parses
- [ ] `n := take(mut s.head) ?? return None` parses with `return` inside the `??` else
- [ ] Precedence tests: `a ?? b == c` groups as `a ?? (b == c)`; `-x?` groups as `-(x?)`

**Verification:**
- [ ] Tests pass: `go test ./parser/`
- [ ] Manual check: `teak parse examples/20-bst.teak`

**Dependencies:** 0.8
**Files:** `parser/expr.go`, `parser/parser_test.go`
**Scope:** M

## Task 0.10: Match and patterns

**Description:** `match` in expression and statement position; arms with `=>`; patterns: literal, identifier binding, wildcard `_`, variant with sub-patterns, tuple, range `..=`, alternatives `|`, guards `if`.

**Acceptance criteria:**
- [ ] Every pattern form in examples 05, 09 and 17 parses
- [ ] `(Connecting(n, _), Timeout) if n < 3 => ...` parses as a tuple pattern with a guard
- [ ] An arm without `=>` and a pattern with an unbalanced parenthesis each give a specific error

**Verification:**
- [ ] Tests pass: `go test ./parser/`
- [ ] Manual check: `teak parse examples/17-state-machine.teak`

**Dependencies:** 0.9
**Files:** `parser/pattern.go`, `parser/parser_test.go`
**Scope:** M

## Task 0.11: Examples sweep and error recovery

**Description:** A test that parses all twenty examples with zero errors, a table of malformed inputs with their expected messages, and a check that the parser reports at most one error per malformed line rather than cascading.

**Acceptance criteria:**
- [ ] All twenty `.teak` files parse with no errors
- [ ] Ten malformed inputs each produce the one expected message
- [ ] `teak parse` on every example prints a tree whose first and last declarations match the source

**Verification:**
- [ ] Tests pass: `go test ./...`
- [ ] Manual check: `for f in examples/*.teak examples/*/*.teak; do go run ./cmd/teak parse $f > /dev/null || echo FAIL $f; done` prints nothing

**Dependencies:** 0.10
**Files:** `parser/parser_test.go`, `parser/testdata/`
**Scope:** S

## Checkpoint: stage 0

- [ ] All tasks above checked
- [ ] `go test ./...` passes; `gofmt -l .` and `go vet ./...` are clean
- [ ] The grammar decisions made during parsing (the `<` rule, `if` forms, `??` precedence) are recorded in `../design/00-syntax-conventions.md`
- [x] Open question from `plan.md` decided: stage 1 includes `match` and enums (2026-10-18)
- [ ] Reviewed by the owner before stage 1 begins
