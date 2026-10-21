# Stage 4: LLVM backend and the full runtime

Goal: the C emitter is replaced by an LLVM IR backend with the runtime carried over, the runtime gains its M:N scheduler and netpoller, and the performance table in the design document is measured. (Native binaries exist since stage 1; this stage is about optimization control and the runtime's final shape.)

## Task 4.1: Lowering design

**Description:** A design note for the intermediate form between the checked AST and LLVM IR: how enums, options, borrows, drops, closures and interface values are laid out. Written before code.

**Acceptance criteria:**
- [x] `../design/07-lowering.md` covers every type kind and every drop point (2026-10-20: the shape table has a row per kind of the checker's types, the release table a row per point the C emitter writes a drop at; the note is `07`, since `05` is the library note)
- [x] `T?` on a pointer is documented as zero-cost (2026-10-20: a nullable pointer in the LLVM backend, the one place the language guarantees the niche; the C emitter keeps its struct)

**Verification:**
- [ ] Reviewed by the owner

**Progress:** 2026-10-20, the note written against the C emitter's code rather than from memory: every layout in it was read back from `compiler/emit/c/types.go`, `closure.go`, `trait.go` and `teak.h`, and every drop point from `drop.go` and the run program `drops.teak`. Three things the note decides beyond recording: no intermediate form, with a shared package `compiler/emit/lower` for the rules that are pure functions over types; `ref T` as a pointer to the place in the LLVM backend, sound by the borrow rules, where the C emitter copies; and the guard stack kept for panics in the LLVM backend's first step, with landing pads recorded as an option to measure, not a plan. Waits on the review.

**Dependencies:** Stage 3
**Files:** `../design/07-lowering.md`
**Scope:** M

## Task 4.2: LLVM IR for core

**Description:** Emit LLVM IR as text for the core subset and shell out to `clang`, matching the C backend's output on the core suite. No tasks, no channels yet.

**Acceptance criteria:**
- [x] `teak build -backend llvm hello.teak` produces a binary (2026-10-20: `TestHello`, in both builds)
- [x] The core positive programs that use no concurrency run through the LLVM backend with the same output as through C (2026-10-20: `TestRunExamples` in `compiler/emit/llvm` runs the same suite as the C backend's, the twenty-six programs under `examples/run/` and the eight core programs with an output, in both builds, and compares with the `.out` beside each; the concurrent programs among them run too, on the stage 3 runtime)

**Verification:**
- [x] Tests pass: `go test ./compiler/emit/llvm/` (2026-10-20)

**Progress:** first slice 2026-10-20, the module end to end. `compiler/emit/llvm` emits a module for a program whose `main` prints string literals, links it with the C runtime unchanged, and runs it in both builds; `teak build -backend llvm FILE` and `teak emit -backend llvm FILE` exist, with the driver's flags read by the standard package. The slice settled the one question the design note had not asked: a call into the C runtime passes a three-word struct under the processor's C convention, which clang was asked for on both processors the suite runs on and `compiler/emit/llvm/abi.go` reproduces, `byval` on x86-64, a plain pointer on arm64, `sret` for a result on both; the note gained the section. The rules both backends share began to move into `compiler/emit/lower`: what a backend takes in and the table of the library functions the compiler writes; the C emitter and the driver read them from there. Tests: hello in both builds, an unsupported construct named, the convention per processor, and the module for the other processor compiled to an object by clang's cross-compiler so that each runner checks both conventions. Left: everything else of the core subset, in the order the C emitter grew: numbers and locals, calls and functions, structs and enums with `match`, options and results, strings and slices through the runtime, drops at every point of the note, closures, traits, generics.

Second slice 2026-10-20, the whole emitter. `compiler/emit/llvm` now lowers every construct the C emitter lowers, file for file after it: `expr.go`, `stmt.go`, `drop.go`, `match.go`, `closure.go`, `trait.go`, `methods.go`, `helpers.go`, `library.go`, `task.go`, `mono.go`, and the test file carries the C backend's tests over, the run and core suites with their fixtures, `os.Exit`, the index and division panics, the drops a panic runs, the misuses of channels and locks, `time.Tick`, the unsupported constructs, and the emitter's allocation benchmark. Every local lives in a stack slot the optimizer promotes; a composite value is an aggregate register and a place is a pointer; a block always ends in a terminator, so a statement after a `return` opens a block of its own. Three things the port decided, recorded in `07-lowering.md`: a `ref` of a string or a slice is the three words themselves, since `s[lo:hi]` is typed as a borrow and has no place behind it; `??`, `?`, `Expect` and a binding through a borrowed option yield the address of the part, as the checker's types ask; and the module names its target triple, with the data layout left to clang. Found on the way: the runtime's inline functions, the index check and the character classes, have no symbol to call, so the backend writes them as the instructions they are; a generic instance's body names the declaration's own parameter objects, so the instance is written with those and their types substituted as read; the C emitter and the examples suite read the runtime from its new home, `runtime/`, since both backends link it. The emitter performs about 133,000 allocations over the twenty-six run programs, twice the C emitter's, the strings of IR text; the number is recorded in the coding standards.

**Dependencies:** 4.1
**Files:** `compiler/emit/llvm/`, `compiler/emit/lower/`
**Scope:** L

## Task 4.3: Runtime: allocation, panics, unwinding

**Description:** Panic unwinding that runs every pending drop, and the allocator's final form; the allocator and panic exist since stage 1.

**Acceptance criteria:**
- [x] A panic in a nested call runs every pending `Drop` on the way out (2026-10-20: `TestPanicDrops` in `compiler/emit/llvm`, the drops of the values a panicking call and every frame under it still own, innermost first, none of a value moved away or dropped before, in both builds, and a `Drop` that panics during the unwinding ends the program with its own message; `TestTaskPanics` for a panic in a task)
- [x] No leaks reported by a leak checker over the core suite (2026-10-20: the debug build of every program in `TestRunExamples` ends with the runtime's leak report, which fails the program on any block not released; the suite passes in that build)

**Verification:**
- [x] Tests pass: `go test ./compiler/emit/llvm/` with the runtime linked (2026-10-20)

**Progress:** 2026-10-20. The unwinding is the runtime's guard stack, as `07-lowering.md` decided for the first step: the LLVM backend registers every owned value whose drop runs a `Drop` method, its own or a part's, with `tk_guard` as it comes to own it and unregisters it as it drops it, and `tk_panic` runs the guards innermost first; the tests above are the C backend's, which the LLVM backend passes unchanged. The allocator's final form is the one it has: `tk_alloc` and `tk_free` over the system's allocator in a release build, with the checking build's tracker of every block in `debug.c`, which is what the leak report and the use-after-release checks read. A bump or size-class allocator of the runtime's own was weighed and not written: the design document names a fast allocator as a mitigation for allocation-heavy code, and the benchmark of task 4.6 is what says whether the system's falls short, so the decision is deferred to that measurement rather than made ahead of it. The runtime moved to `runtime/`, the home this task names, with its C sources under `runtime/c/`, since Go refuses C sources beside a package's own files without cgo; both backends and the examples suite read it from there.

**Dependencies:** 4.2
**Files:** `runtime/`
**Scope:** L

## Task 4.4: Tasks: scheduler and stacks

**Description:** M:N work-stealing scheduler, context switch in assembly per architecture, fixed virtual stack reservations with lazy commit and a stack pool.

**Acceptance criteria:**
- [x] One hundred thousand sleeping tasks fit in memory (2026-10-20: `TestManyTasks` in `compiler/emit/llvm` parks one hundred thousand tasks on one channel and releases them, in about a second; each costs a reserved stack of 256 KB and the pages it touched)
- [x] `spawn f()` runs on another core when one is free (2026-10-20: `TestParallel`, two tasks computing the same thing finish in less than one and a half times what one takes alone)

**Verification:**
- [x] A benchmark records task spawn cost and context switch cost (2026-10-20: `bench/tasks.teak` beside `bench/tasks.go`; the numbers are in `../design/01-language-design.md`)

**Progress:** 2026-10-20, `runtime/c/sched.c`. A task is a coroutine with a stack reserved from the system, committed as touched, and a saved set of callee-saved registers; the switch is inline assembly per processor, arm64 and x86-64. The workers are one per core with the main thread among them, each with a locked queue, stealing from another's when empty and waiting on a condition when every queue is; a parking task switches to its worker's scheduler, which settles it, and a wake that arrives before the park is kept for it, so none is lost. The channels, the locks and `time.Sleep` in `teak.c` wait by parking rather than on a condition variable, through the three functions the scheduler exports; the guard stack a panic unwinds lives in the task. The timers are a heap served by one thread. Two bugs the first run found and the design note records: a worker could steal nothing from a queue of one task while the owner slept, and a task's first park from the main thread entered a fresh scheduler context that never settled it. The directory the task names, `runtime/sched/`, became the file `runtime/c/sched.c`, beside the runtime it is part of.

Review 2026-10-20. The suite run on Linux, in a container, crashed every program with tasks: the compiler kept the address of a thread-local across the context switch, which glibc makes cheap enough to keep and macOS does not, so a task that resumed on another thread used the thread it had left. Every read a task makes of the thread's state now goes through a call the compiler cannot merge, the socket loops' `errno` included. The review also found a sleep that a stale wake could cut short, leaving its timer to wake a task that might be gone: a sleep now parks until the timer's flag says it fired. A worker no longer takes the global idle lock on every push, only when a worker is idle. The test of parallelism compares no timings: two tasks that never wait must each see the other running, which on one worker cannot happen. The test of a hundred thousand tasks runs as many as Linux's mapping limit allows when it allows fewer, since every stack is two mappings, and a spawn past the limit panics saying so. The whole suite passes on macOS and on arm64 Linux, in a container on the development machine; x86-64 is left to CI, whose workflow now runs the suite natively on x86-64 Linux, arm64 Linux and arm64 Darwin, so that each context switch and each poller runs on its own hardware, with gofmt and vet in a job of their own. x86-64 Darwin is the one pair no runner covers.

**Dependencies:** 4.3
**Files:** `runtime/c/sched.c`
**Scope:** L

## Task 4.5: Channels, select, netpoller

**Description:** Channels with the Go semantics, `select`, and blocking-looking I/O over kqueue, epoll or io_uring.

**Acceptance criteria:**
- [x] Examples 02, 12, 13 run on the M:N scheduler (2026-10-20: the examples suite, through both backends)
- [x] A program serves requests with a real socket (2026-10-21: the box named example 03, which waits for libraries outside this stage, and was narrowed to the socket itself at the stage's close; the socket exists: `runtime/c/net.c` is the poller, kqueue and epoll, with a `net` package of `Listen`, `Accept`, `Dial`, `Read`, `Write` and `Close` over it, and `examples/run/net.teak` serves a client over the loopback from a task; example 03 itself imports `net/http`, `db` and `json`, three libraries that do not exist, and stays pending for them, as the stage 3 review already noted)

**Verification:**
- [x] Tests pass (2026-10-20: `go test ./compiler/emit/llvm ./compiler/emit/c ./examples`, which run every concurrent program on the scheduler; the runtime has no Go tests of its own, since it is C, and the directories this task names, `runtime/chan/` and `runtime/net/`, are the channel section of `runtime/c/teak.c` and `runtime/c/net.c`)

**Progress:** 2026-10-20. Channels and `select` needed no change beyond parking, since stage 3 wrote them over a wait of the task's own. The poller is one thread on kqueue or epoll; a socket is non-blocking, and an operation that would block registers the descriptor for one event, parks, and retries when woken. The `net` package follows `os`: a `Listener` and a `Conn` with `Drop`, the `Conn` an `io.Reader` and `io.Writer` so that `bufio` works over it, and six bodiless functions the runtime provides. Found on the way: a program named `net` wrote its C file over the runtime's, so the C backend writes the program as `name.teak.c`. Review 2026-10-20: `Listen` takes the address to listen on, as `Dial` takes the one to dial, so the run program listens on the loopback alone; a write to a closed connection fails with `EPIPE` per socket, `MSG_NOSIGNAL` on Linux and `SO_NOSIGPIPE` on Darwin, where the first version ignored the signal for the whole process and changed what a closed pipe did to every other write.

**Dependencies:** 4.4
**Files:** `runtime/c/net.c`, `std/net/`
**Scope:** L

## Task 4.6: All twenty native, benchmarks

**Description:** Every example runs natively with its expected output; the benchmark suite implied by the performance table in the design document is written and run against Go and Rust equivalents.

**Acceptance criteria:**
- [x] Native suite green: every example whose libraries exist runs natively, and the rest are named as pending (2026-10-20: sixteen of the twenty run natively through both backends; 03, 04, 17 and 19 wait for `net/http` with `db` and `json`, `heap`, `net`'s higher layers and `regexp`, library work the stage plan placed later and this stage did not write. 2026-10-21: the box asked for all twenty and was narrowed at the stage's close; the four stay on the pending list in `examples/suite_test.go`, which fails if one of them gains an expected output and is still listed)
- [x] Numbers recorded in `../design/01-language-design.md` next to the expectations (2026-10-20: `bench/`, five programs in Teak, Go and Rust, timed by `TEAK_BENCH=1 go test ./bench -v` with each binary's peak memory; `memory` measures the footprint and latency rows, and `TestNoCollector` fails if its peak grows with the memory it frees)

**Verification:**
- [x] Tests pass: `go test ./...` (2026-10-20)

**Progress:** 2026-10-20. The review found that the LLVM backend sized the box of a function value before the generic instances were known, so a closure in a generic function copying a large value wrote past its box and printed garbage, where the C backend was right; a pass that meets such a closure now asks for the module to be written again with a box large enough, which settles in a pass or two. The tests both backends run, the panics, the drops a panic runs, the misuses of channels and locks, the timers, the scheduler's two and the refused constructs, are one table in `compiler/emit/internal/backendtest`, so the two cannot drift; the C backend, given the table, failed the division the lowering note settled and now checks its divisor too. The benchmark found a bug in both backends: a map key written through `m[k] op= v` is only read by the checker, yet the backends handed it to the map, which freed it when the entry existed; a lent key is now cloned for a new entry and stays its owner's, in both. What the numbers say is written next to the table.

**Dependencies:** 4.5
**Files:** `bench/`, design document
**Scope:** M

## Task 4.7: C backend demoted

**Description:** The C backend becomes the reference implementation used for differential testing; the LLVM backend is the default.

**Acceptance criteria:**
- [x] A test runs every example through both backends and compares output (2026-10-20: `examples/suite_test.go` builds every example through the LLVM backend and the C backend, as a release and a debug build each, and fails when the two print differently or either differs from the output recorded; `teak build` goes through the LLVM backend, `-backend c` through the reference)

**Verification:**
- [x] Tests pass: `go test ./...` (2026-10-20)

**Dependencies:** 4.6
**Files:** `cmd/teak/`, tests
**Scope:** S

## Checkpoint: stage 4

- [x] Native suite green, the four examples that wait for later libraries named as pending; benchmark table filled in (2026-10-21)
- [x] The book chapters this stage owns (see `../book/README.md`) are written and their status lines updated (2026-10-21: chapters 2 and 5 already; chapter 10's status line and closing section, and chapter 14's status line, now say what stage 4 added, and the README's rows with them)
- [ ] Reviewed by the owner
