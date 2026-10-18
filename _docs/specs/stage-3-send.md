# Stage 3: ownership across tasks

Goal: the rule in `../design/03-ownership-across-tasks.md` is enforced. Data races are compile errors.

## Task 3.1: `Send` inference

**Description:** Compute `Send` for every type from its structure: fields, `Rc` not, `Arc` conditional, `Mutex<T>` conditional, borrows never, trait-as-type only if the trait refines `Send`.

**Acceptance criteria:**
- [ ] A table test over the type kinds in the design note
- [ ] Recursive types terminate

**Verification:**
- [ ] Tests pass: `go test ./types/`

**Dependencies:** Stage 2
**Files:** `types/send.go`, `types/send_test.go`
**Scope:** S

## Task 3.2: Task boundary checks

**Description:** `spawn` moves its captures and requires them `Send`; channel element types must be `Send`; sending moves; receiving yields `T?`; a borrow cannot cross any of these.

**Acceptance criteria:**
- [ ] The rejected programs at the bottom of example 02 are rejected with messages naming the rule
- [ ] Examples 02, 12 and 13 check

**Verification:**
- [ ] Tests pass: `go test ./types/`
- [ ] Negative programs under `examples/core/errors/` for races fail as planned

**Dependencies:** 3.1
**Files:** `types/task.go`, tests
**Scope:** M

## Task 3.3: Shared-state library

**Description:** `Arc`, `Mutex<T>`, `RwLock<T>` with guards that are `mut`/`ref` borrows released on drop; `sync.Once`; `WaitGroup` as a `Copy` handle; mapped onto Go's `sync`.

**Acceptance criteria:**
- [ ] Example 03 checks and runs against a stub database
- [ ] Touching lock-protected data without the guard is a compile error

**Verification:**
- [ ] Tests pass: `go test ./std/sync/`

**Dependencies:** 3.2
**Files:** `std/sync/`
**Scope:** M

## Task 3.4: Concurrency examples green

**Description:** Examples 02, 03, 12, 13 run with expected output; a race test suite is rejected.

**Acceptance criteria:**
- [ ] Positive and negative concurrency suites in the test run

**Verification:**
- [ ] Tests pass: `go test ./...`

**Dependencies:** 3.3
**Files:** tests
**Scope:** S

## Checkpoint: stage 3

- [ ] Race programs rejected; concurrency examples run
- [ ] Reviewed by the owner
