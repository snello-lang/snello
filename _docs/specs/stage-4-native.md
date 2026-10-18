# Stage 4: native backend and runtime

Goal: all twenty examples run as native binaries with no Go toolchain involved, and the performance table in the design document is measured.

## Task 4.1: Lowering design

**Description:** A design note for the intermediate form between the checked AST and LLVM IR: how enums, options, borrows, drops, closures and interface values are laid out. Written before code.

**Acceptance criteria:**
- [ ] `../design/05-lowering.md` covers every type kind and every drop point
- [ ] `T?` on a pointer is documented as zero-cost

**Verification:**
- [ ] Reviewed by the owner

**Dependencies:** Stage 3
**Files:** `../design/05-lowering.md`
**Scope:** M

## Task 4.2: LLVM IR for core, hello world native

**Description:** Emit LLVM IR as text for the core subset and shell out to `clang`. No tasks, no channels yet.

**Acceptance criteria:**
- [ ] `teak build -target native hello.teak` produces a binary with no Go runtime
- [ ] The core positive programs that use no concurrency run natively

**Verification:**
- [ ] Tests pass: `go test ./emit/llvm/`

**Dependencies:** 4.1
**Files:** `emit/llvm/`
**Scope:** L

## Task 4.3: Runtime: allocation, panics, unwinding

**Description:** The C runtime's first pieces: allocator, panic with message and exit, unwinding that runs drops.

**Acceptance criteria:**
- [ ] A panic in a nested call runs every pending `Drop` on the way out
- [ ] No leaks reported by a leak checker over the core suite

**Verification:**
- [ ] Tests pass: `go test ./emit/llvm/` with the runtime linked

**Dependencies:** 4.2
**Files:** `runtime/`
**Scope:** L

## Task 4.4: Tasks: scheduler and stacks

**Description:** M:N work-stealing scheduler, context switch in assembly per architecture, fixed virtual stack reservations with lazy commit and a stack pool.

**Acceptance criteria:**
- [ ] One hundred thousand sleeping tasks fit in memory
- [ ] `spawn f()` runs on another core when one is free

**Verification:**
- [ ] A benchmark records task spawn cost and context switch cost

**Dependencies:** 4.3
**Files:** `runtime/sched/`
**Scope:** L

## Task 4.5: Channels, select, netpoller

**Description:** Channels with the Go semantics, `select`, and blocking-looking I/O over kqueue, epoll or io_uring.

**Acceptance criteria:**
- [ ] Examples 02, 12, 13 run natively
- [ ] Example 03 serves requests with a real socket

**Verification:**
- [ ] Tests pass: `go test ./runtime/...`

**Dependencies:** 4.4
**Files:** `runtime/chan/`, `runtime/net/`
**Scope:** L

## Task 4.6: All twenty native, benchmarks

**Description:** Every example runs natively with its expected output; the benchmark suite implied by the performance table in the design document is written and run against Go and Rust equivalents.

**Acceptance criteria:**
- [ ] Native suite green
- [ ] Numbers recorded in `../design/01-language-design.md` next to the expectations

**Verification:**
- [ ] Tests pass: `go test ./...`

**Dependencies:** 4.5
**Files:** `bench/`, design document
**Scope:** M

## Task 4.7: Go backend demoted

**Description:** The Go backend becomes the reference implementation used for differential testing; the native backend is the default.

**Acceptance criteria:**
- [ ] A test runs every example through both backends and compares output

**Verification:**
- [ ] Tests pass: `go test ./...`

**Dependencies:** 4.6
**Files:** `cmd/teak/`, tests
**Scope:** S

## Checkpoint: stage 4

- [ ] Native suite green; benchmark table filled in
- [ ] The book chapters this stage owns (see `../book/README.md`) are written and their status lines updated
- [ ] Reviewed by the owner
