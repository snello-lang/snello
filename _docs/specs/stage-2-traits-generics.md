# Stage 2: traits, generics, and the full examples

Goal: all twenty examples compile through the Go backend and run with their expected output.

## Task 2.1: Generic functions and types

**Description:** Type parameters on functions and types, inference at call sites, instantiation in the checker, monomorphization in the emitter.

**Acceptance criteria:**
- [ ] `List<T>` and `Map<T, U>` from example 08 check and run
- [ ] A call with an uninferable type parameter reports which one

**Verification:**
- [ ] Tests pass: `go test ./types/ ./emit/golang/`

**Dependencies:** Stage 1
**Files:** `types/generic.go`, `emit/golang/mono.go`, tests
**Scope:** L

## Task 2.2: Trait declarations and `impl`

**Description:** `trait` declarations with required methods; `impl` lists on struct and enum declarations; the check that every required method exists with the right signature; traits as bounds on type parameters.

**Acceptance criteria:**
- [ ] A type missing a required method reports the trait and the method
- [ ] `TotalArea<T: Shape>` monomorphizes for `Circle`

**Verification:**
- [ ] Tests pass: `go test ./types/`

**Dependencies:** 2.1
**Files:** `types/trait.go`, tests
**Scope:** M

## Task 2.3: Defaults, fields, super, resolution order

**Description:** Default methods inherited by implementors; trait fields stored inline and set at construction; `super`; the resolution order (own methods, then the `impl` list right to left); trait refinement.

**Acceptance criteria:**
- [ ] Example 07 prints `[logged] circle with area 3.14` then `a rectangle`
- [ ] Two traits providing the same default with no override on the type reports an ambiguity

**Verification:**
- [ ] Tests pass: `go test ./types/ ./emit/golang/`

**Dependencies:** 2.2
**Files:** `types/trait.go`, `emit/golang/trait.go`, tests
**Scope:** M

## Task 2.4: Trait as a type

**Description:** A trait name used as a type is an owned interface value; emitted as a Go interface; trait fields reachable through synthesized accessors.

**Acceptance criteria:**
- [ ] `[]Shape` in example 07 holds a `Circle` and a `Rect` and dispatches correctly
- [ ] Assigning a non-implementing type reports the missing trait

**Verification:**
- [ ] Tests pass: `go test ./emit/golang/`

**Dependencies:** 2.3
**Files:** `types/trait.go`, `emit/golang/trait.go`
**Scope:** M

## Task 2.5: Standard traits

**Description:** `Copy`, `Clone`, `Eq`, `Ord`, `Hash`, `Drop`, `Error`, and the operator traits `Add`, `Sub`, `Mul`, with `Copy` and `Eq` derivable by declaration for types whose fields qualify.

**Acceptance criteria:**
- [ ] Example 16 runs: operators on `Vec3` and `Mat4`
- [ ] Declaring `impl Copy` on a type with a non-`Copy` field reports the field

**Verification:**
- [ ] Tests pass: `go test ./types/`

**Dependencies:** 2.4
**Files:** `types/universe.go`, `types/stdtraits.go`
**Scope:** M

## Task 2.6: Standard library, first cut

**Description:** The library the examples import, written in Teak where possible and mapped onto Go packages otherwise: `Option`/`Result` methods, slice methods (`Push`, `Pop`, `Remove`, `Reverse`), `take`, `Arena<T>` and `Index<T>`, `Rc`/`Weak`, `strings`, `strconv`, `fmt`, `os`, `io`, `bufio`, `sort`, `time` shims.

**Acceptance criteria:**
- [ ] Each function the examples call exists with the signature the examples use
- [ ] `Arena<T>` has generation-checked indices with a test for stale access

**Verification:**
- [ ] Tests pass: `go test ./std/...`

**Dependencies:** 2.5
**Files:** `std/`
**Scope:** L, split by package

## Task 2.7: All twenty examples green

**Description:** Every example compiles and runs with the output in its comments. Expected outputs are recorded as test fixtures.

**Acceptance criteria:**
- [ ] A test builds and runs all twenty and compares stdout to fixtures
- [ ] Compile time for the whole suite is measured and recorded in `plan.md`

**Verification:**
- [ ] Tests pass: `go test ./...`

**Dependencies:** 2.6
**Files:** `examples/expected/`, `cmd/teak/suite_test.go`
**Scope:** M

## Checkpoint: stage 2

- [ ] All twenty examples run with expected output
- [ ] Compile time recorded; decision on dictionary passing for debug builds
- [ ] The book chapters this stage owns (see `../book/README.md`) are written and their status lines updated
- [ ] Reviewed by the owner
