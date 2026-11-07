# Snello language design

Status: draft, 2026-10-17; the memory model confirmed at the stage 1 checkpoint, 2026-10-18 (see the decision at the end of the memory model section); the task model confirmed at the stage 3 checkpoint, 2026-10-20 (see the decision at the end of the concurrency section). This document records the decisions behind Snello and the reasoning for each. Syntax lives in `00-syntax-conventions.md`; the programs that test these decisions live in `../../examples/`.

## Goals

1. Go's syntax and simplicity. A Go developer reads Snello on day one.
2. Memory safety at compile time, without a garbage collector.
3. No lifetime annotations.
4. Go's concurrency model: lightweight tasks, channels, `select`.
5. Data-race freedom, checked by the compiler.
6. Traits in the Scala sense: default methods, state, mixins.
7. Compile times in Go's range, not C++'s.

Goal 2 is the one that shapes everything. Go's simplicity is largely bought by its garbage collector, and Go's concurrency leans on it too, since any pointer can be shared with any goroutine. Removing the collector means a memory model that decides when values die, and that model has to stay simple enough that goals 1 and 3 survive.

## Non-goals

**Snello is not a drop-in replacement for Go.** The syntax is Go's so that a Go developer reads Snello immediately, but ordinary Go programs do not compile as Snello and are not meant to. Go code relies on `nil`, first-class pointers, implicitly shared mutable state, structural interfaces, and a garbage collector keeping values alive; Snello has none of these. Code is ported to Snello, not copied. The relationship is Zig to C or Kotlin to Java, not TypeScript to JavaScript. Code that already follows ownership discipline, such as a channel pipeline, looks nearly identical in both; code that does not will change shape.

**Importing Go packages is not a language feature.** The compiler is written in Go, which makes a bridge to Go packages tempting. It is deliberately not offered: every Go value crossing that boundary brings `nil`, aliasing, and GC dependence with it, and a Snello program runs on its own runtime, which could never honor such imports. Interop with Go or C will be a bounded foreign-function boundary, designed separately once the native backend exists.

## What Snello leaves out

Simplicity is a goal with the same weight as the others, and it is kept by subtraction. Each of these was considered and rejected, and each rejection is a decision that needs an argument to reverse:

| Left out | Why | What Snello has instead |
|---|---|---|
| Lifetime annotations | The hardest part of Rust to learn | Borrows inferred from one source, or rejected |
| Complex numbers and untyped constant arithmetic | Rare, and a subtle corner of Go's specification | A library `Complex<T>` with the operator traits; literals typed by context |
| `nil` | The billion-dollar mistake | `T?` |
| Exceptions | Hidden control flow, costly without a GC | `Result` and `?` |
| Inheritance | Fragile base classes | Traits with defaults, and Go embedding |
| Impl blocks, `dyn`, `Box` | Rust vocabulary with Go equivalents | `impl` on the type line, a trait as a type, `*T` |
| async/await | Function coloring | Tasks that block, as goroutines do |
| Structural interfaces | Ambiguous once traits have defaults and fields | Nominal traits declared with `impl` |
| Self-types, implicits, macros | Where Scala's and Rust's complexity lives | Nothing |
| A garbage collector | The premise | Ownership, `Drop`, arenas, `Rc`/`Arc` where sharing is real |
| Anonymous structs | A struct has a name; `struct{}` as "nothing" is a workaround for a language without a unit | `()`, the unit Snello already has: a signal channel is `chan ()` and the signal is `()` (decided 2026-10-20) |

The test for any future addition: a Go developer must read it on day one without a glossary. If it needs a Rust or Scala word to explain, it is the wrong shape.

## Memory model

### Options considered

| Model | Example | What it costs |
|---|---|---|
| Mutable value semantics, no first-class references | Hylo | Graphs and linked structures need indices or arenas |
| Ownership with runtime-checked borrows | Inko | Small runtime cost; a drop with live borrows panics |
| Generational references | Vale | Per-dereference check, mostly elided |
| Ownership with elided reference counting | Swift | Atomic increments unless thread confinement is proven |
| Ownership with lifetime-annotated borrows | Rust | The annotations, and most of Rust's learning curve |

### Decision

Ownership with moves by default and **second-class borrows**.

- Every value has exactly one owner. Assignment and passing by value move the value. Types marked `Copy` (numbers, small plain structs) are copied.
- A borrow (`ref T` shared, `mut T` exclusive) may be passed further down the call stack but may not be stored in a struct, captured by a task, put in a channel, or returned, with one built-in exception: a method with a borrowed receiver may return a borrow into that receiver, tied to the receiver's borrow.
- Snello therefore has lifetimes but never writes them: every borrow's lifetime is inferred from the function it lives in or from the one input it was derived from, and programs where that is ambiguous are rejected rather than annotated. `04-borrows-ref-and-mut.md` has the rules and the list of what cannot be written.
- Destructors run deterministically when the owner goes out of scope. `defer` remains for cleanup not tied to a value.
- Structures that need non-owning links (parent pointers, graphs, LRU caches) use arenas with indices, which is already common Go idiom, or the standard library's `Rc`/`Weak`. Both are library types. See `02-arenas-and-indices.md` for the full reasoning.

The programs in `../../examples/` confirm the model handles trees, graphs, parsers, linked lists, an LRU cache, and a binary search tree with in-place mutation. Open questions they surfaced are listed in `../../examples/README.md`.

### Why not the alternatives

Inko's runtime-checked borrows are the fallback if second-class borrows prove too restrictive in practice; they allow stored references at a small cost. Hylo's value semantics go further than needed and make callbacks awkward. Vale's generational references are elegant but young and carry a per-access cost in the worst case. Swift's reference counting conflicts with the no-hidden-cost goal.

**Decision at the stage 1 checkpoint (2026-10-18): the ownership model holds as designed.** Stage 1 implemented the model as written above and in the borrows note: one owner per value, `ref` and `mut` borrows, the six rules, and no lifetime annotation anywhere. The eight core programs compile, run and free everything under a debug build that counts every allocation, and the 27 negative programs are rejected with the planned message and no other. Four refinements were needed, each a precise answer to a case the design had not spelled out and none a new concept for the programmer, each dated in `04-borrows-ref-and-mut.md`: the arguments of a call are evaluated, and their borrows end, before the receiver is borrowed; a pattern through a borrow copies `Copy` parts and borrows the rest; a `mut` binding through a map read is refused; and a borrow of a temporary dies with its statement. Two gaps carry into stage 2 as checker work, not model changes: a function-typed parameter is trusted to be owned, so a callee that stores its callback can keep a borrowing closure, which the debug build catches at run time; and a deferred call does not run on a panic, while `Drop` does. Stage 2 builds traits and generics on the model unchanged.

## Concurrency

Keep Go's surface: `spawn f()` where Go writes `go f()`, channels, `select`, `sync.WaitGroup`.

Add one rule: **a value crossing a task boundary must be moved, immutable, or atomically shared.** The compiler infers a `Send` property from type structure. A type is `Send` if all its fields are; `Rc` is not, `Arc` is, `Mutex<T>` is when `T` is. Users write no annotations.

A mutex owns its data, as in Rust: `Mutex<T>` hands out a guard that is a `mut T` borrow and unlocks on drop. This makes lock-protected sharing type-checked and buys data-race freedom, which Go does not have.

**Decision at the stage 3 checkpoint (2026-10-20): the task model holds as designed.** Stage 3 implemented the rule above and `03-ownership-across-tasks.md` as written: `Send` inferred from the structure of every type with nothing to annotate, a `spawn` that moves what its closure mentions, channels that move, and a lock whose guard is the only way to its value. Examples 02, 12, 13 and 18 run, and eleven race programs are rejected with the planned message and no other. Four refinements were needed, each dated where it was decided and none a new concept for the programmer: there is no `Sync`, since a `ref` never changes what it borrows and the only way to a `mut` from a shared value is a lock; `rc` is kept in its task by a `local` field the library writes, not by a table in the compiler; `break` means the innermost loop everywhere, so a `select` case can leave the loop around it; and the signal channel is `chan ()`, with anonymous struct types gone from the grammar. One fact of the compiled program shaped the library: a `ref T` is a copy of the value in C, so a lock boxes its value behind a pointer and one lowered function steps from a `ref` of that pointer to a `mut` of the box, the single place the language does so. The runtime of this stage is a thread per task and locked queues, behind the interface `06-tasks-and-channels.md` fixes; stage 4 replaces it with the scheduler behind the same interface. Two gaps carry forward as later work, not model changes: a struct holding a plain `*T` accepts `var` and has no value to hold, which the checker should refuse; and a timer is a task that sleeps, so a program ending with a timer pending skips its debug leak report, until the scheduler's timer wheel.

Actors fall out naturally: a task that owns its state and receives messages needs no locks at all.

Runtime, when it arrives in stage 4: an M:N work-stealing scheduler, blocking-looking I/O over kqueue, epoll, or io_uring, and no async/await coloring. Stacks start as fixed virtual reservations with lazy commit and a stack pool; copying stacks come later if task counts demand it, and are possible without a GC since the compiler knows every frame layout. Stage 3 runs tasks on the system's threads behind the same interface; `06-tasks-and-channels.md` has both.

## Traits

A trait is a Go interface that may also carry default methods and fields. A type says what it implements on its declaration, and its methods are ordinary Go methods:

```snello
struct Circle impl Shape, Named { r f64 }
func (c ref Circle) Area() f64 { return math.Pi * c.r * c.r }
```

- **Nominal, declared with `impl`.** Go's implicit satisfaction does not combine with default methods and fields, because it becomes ambiguous which default applies and where a field lives. Declaring it costs one word on the type and gives coherence for generics.
- **No impl blocks.** Rust groups methods by trait in separate blocks. Go's methods-with-receivers already say everything needed, so Snello keeps them. The compiler checks the declared traits are satisfied.
- **Fields in traits** are stored inline in the implementing struct and set at construction. A trait field obeys the ownership rules of a struct field, with no exception: a default method on a borrowed `Self` returns a borrow of the field or a clone of it, never the field itself (decided 2026-10-19, when example 07's `return n.name` was found to move a string out of a borrow).
- **Resolution is at compile time per concrete type.** Own methods first, then the impl list from right to left. `super` calls the next definition in that order. Every call is direct.
- **Traits are the generic constraint.** Generics are monomorphized by default, with dictionary passing available later to control compile times.
- **A trait used as a type is a Go interface value.** `[]Shape` holds any mix of implementing types. One allocation and one indirect call per value, the same as Go. There is no separate syntax for this.
- **Marker and operator traits use the same mechanism:** `impl Copy, Eq, Add`.
- **Not in version one:** self-types and implicits. They carry most of Scala's complexity for little gain here.

## Features the goals force

These are not on the original list but follow from the ones that are.

- **Sum types and pattern matching** with exhaustiveness checking.
- **`Option` and `Result` with a `?` operator.** There is no `nil`, and a language without a GC has no cheap exceptions, so errors must be values with ergonomic propagation.
- **Deterministic destructors** (`Drop`), which pair with `defer`.
- **Owned heap pointers**, spelled `*T` and created with `&expr` as in Go, but with one owner and no `nil`.
- **A compile-time budget**, decided early, because monomorphization is where fast compilers go to die.

## Implementation strategy

### Host language: Go

The compiler is written in Go. Reasons, in order of weight:

1. The project is a collaboration where every line must be readable and debuggable by a Go developer.
2. Self-hosting later becomes a translation job, since Snello has Go syntax.
3. `go build` builds the compiler, and the compiler needs nothing but the system C compiler to build a Snello program.
4. `go/types` is the best reference implementation of a production type checker in a language the team reads natively, and the Go runtime is the reference for the stage 4 scheduler.

Go's lack of sum types is mitigated with sealed interfaces (an unexported marker method per node family) and the `go-check-sumtype` linter, which restores exhaustiveness checking on type switches.

### Backend: C first, LLVM later

Snello produces a native executable from the first program. Stages 1 through 3 emit C source and build it with the system C compiler, `cc`, which every machine with a developer toolchain has. The runtime, strings, slices, maps, options, printing and panics, and from stage 3 tasks and channels, is C under `compiler/emit/c/runtime/`, compiled into every program. Nothing of Go is involved in a Snello program: no collector, no scheduler, no `go build`. The emitted C is the portable assembler that Nim, Haxe and Chicken Scheme started from, and the same optimizing backends that LLVM offers build it.

Why not LLVM IR from the start: C is a fraction of the code per construct, readable when something goes wrong, and needs no toolchain beyond `cc`. Stage 4 replaces the C emitter with LLVM IR when control over optimization, debug information, and the GPU targets of stage 6 demand it. The runtime carries over unchanged, and the C emitter is shaped so that the replacement is one package under `compiler/emit/`.

**No collector, ever.** The checker decides where every value is freed, and the emitted C frees it there (task 1.9). A debug build poisons freed memory and asserts that nothing is reachable after its release, and a leak checker runs over the suite, so a missing or misplaced free is a failing test, not a slow leak.

Decided 2026-10-18, replacing an earlier plan to emit Go source for the first three stages and keep Go's runtime as scaffolding. The owner's reason: Snello is its own language, and a Snello program must not run on another language's runtime, even temporarily.

### Stages

| Stage | Deliverable | Rough effort |
|---|---|---|
| 0 | Lexer and parser for the Go-like syntax, modeled on `go/scanner` and `go/parser` | weeks |
| 1 | Core checker: structs, functions, `*T`, moves, `ref`/`mut` borrows, destructors. No traits, no generics. Emit C; the first native binaries. | 4 to 8 months |
| 2 | Traits, generics, sum types, `Option`/`Result`, `?`. | 2 to 3 months |
| 3 | `Send` inference on `spawn` and channel sends; tasks on runtime threads, channels in the C runtime. | 1 to 2 months |
| 4 | LLVM IR backend replacing the C emitter; M:N scheduler, netpoller and stack management in the runtime. | year two |
| 5 | Kernel-safe subset, `Device<T>`, kernel launch through the LLVM GPU targets. | after 4 |

Ownership comes before traits because ownership is the bet. Stage 1 is run against the twenty programs with traits and generics stripped out; if the checker rejects one, the design changes before anything is built on top of it. Borrow checking inside a function, with reborrows, conditional moves, moves in loops, and calls like `v.Push(v.Len())`, is the part of Rust's compiler that took longest, and dropping annotations removes none of it. The estimate reflects that.

The twenty programs in `../../examples/` are the acceptance test for every stage. Each stage must compile and run all of them before the next begins.

## Performance

Where Snello should land once the native backend exists. Claims here are expectations from the design, not measurements; they become the benchmark suite for stage 4.

| | Against Go | Against Rust |
|---|---|---|
| CPU-bound code | Faster: no collector work, no write barriers, monomorphized generics | Equal: same ownership model, same optimizer, same lack of hidden costs |
| Allocation-heavy code | Mixed: a bump allocator plus batched collection can beat per-object free | Equal, with the same mitigation: arenas |
| Memory footprint | Lower: values die at scope end, no collector headroom | Equal |
| Latency | No pauses; tail latency is flat | Equal |
| Many-core scaling | Better: no stop-the-world, and data races are impossible | Equal, with lighter concurrency than async |
| Dynamic dispatch | Same: a trait as a type is an interface value | Same |
| Compile time | Much slower: monomorphization plus LLVM | Equal |

Measured 2026-11-07 under the method of stage 5's task 5.8, on an eight-core arm64 laptop, by `bench/`: each binary run once for nothing and then ten times, the median reported, with the least and the most in the table `SNELLO_BENCH=1 go test ./bench -run TestBenchmarks -v` prints; the CI workflow measures the same on the x86-64 and arm64 Linux runners and writes each table to its job's summary. The comparison is with Go, the language Snello is written against; the Rust versions kept until this date were removed, so that the table has one comparison and no dependency. Earlier measurements, 2026-10-20 before the runtime's allocator and 2026-10-21 after it, best of three runs, are in this file's history; what moved since is said below.

| Program | Snello (LLVM) | Snello (C) | Go |
|---|---|---|---|
| `fib`, CPU-bound | 31 ms | 31 ms | 34 ms |
| `tree`, allocation-heavy | 35 ms | 37 ms | 36 ms |
| `words`, strings and a map | 86 ms | 62 ms | 146 ms |
| `churn`, the allocator under contention | 32 ms | 28 ms | 174 ms |
| `tasks`, a spawn | 375 ns | 529 ns | 176 ns |
| `tasks`, a context switch | 121 ns | 121 ns | 125 ns |
| `memory`, peak memory | 33 MB | 66 MB | 78 MB |
| `memory`, the mean round; rounds over twice it | 91 µs; none | 109 µs; none | 98 µs; 115 of 2,000, up to 0.9 ms |

The same programs on the two Linux runners of the CI workflow, four processors each, from the `bench` job's summaries of the run of 2026-11-07 (run 42365775764), Snello through the LLVM backend against Go, the C backend's numbers in the summaries:

| Program | x86-64 Linux, Snello | x86-64 Linux, Go | arm64 Linux, Snello | arm64 Linux, Go |
|---|---|---|---|---|
| `fib` | 22 ms | 47 ms | 22 ms | 29 ms |
| `tree` | 34 ms | 49 ms | 38 ms | 66 ms |
| `words` | 104 ms | 170 ms | 101 ms | 224 ms |
| `churn` | 27 ms | 134 ms | 31 ms | 188 ms |
| `tasks`, a spawn | 481 ns | 186 ns | 530 ns | 338 ns |
| `tasks`, a context switch | 83 ns | 115 ns | 180 ns | 175 ns |
| `memory`, peak memory | 34 MB | 73 MB | 34 MB | 80 MB |
| `memory`, the mean round; rounds over twice it | 90 µs; none | 142 µs; 263 of 2,000 | 108 µs; none | 171 µs; 32 of 2,000 |

What the numbers say against the expectations: CPU-bound code is where the table expected it, a tenth ahead of Go. Allocation-heavy code is level with Go, where the first measurement had it behind by the margin the table foresaw, 63 ms against 38, the collector's batching against a free per node: the runtime's own allocator closed that, a small block coming from a list of free blocks of its size kept by each thread, so an allocation and a free are a few instructions and no lock, as in Go's runtime. `churn` is that allocator under contention, every core allocating at once and blocks freed on a thread other than the one that made them, and it is five to six times ahead of Go, whose collector must walk what eight goroutines make. Strings and a map were behind Go, 194 ms against 150 on 2026-10-21, with the cause put in the library; the map's Swiss table and keyed hash and the strings' single allocation per concatenation, written for the library review on 2026-10-22, put them at 0.6 of Go's time without a change aimed at this row, and `fmt.Sprint` still allocating every string is no longer what the row measures. A context switch is under Go's since the spinning worker and the inject queue took the thread wake out of a handoff and the task's state became one atomic word (task 5.10 of stage 5). A spawn is twice Go's through the LLVM backend on the Mac, 2.6 times on the x86-64 runner and 1.6 on the arm64 one, from six times at the start of that task: a push wakes a worker only when none spins and hands the spin to the one it wakes, each worker's queue is a lock-free ring as Go's `runq` is, and each keeps a cache of stacks; what is left between it and Go's is the record's `calloc`, where Go draws from a per-P free list, and the stack itself, 256 KB reserved against Go's two, whose first page a burst of spawns touches per task before any runs: the peak memory of the row is that backlog, each task not yet run holding a page, and it is larger through the C backend, whose spawning loop is slower and whose backlog is longer, which is also why its spawn reads higher, the loop's own cost being in the number. The task row's numbers fall in two clusters a run apart, about 365 ns and about 450, which the least and the most in the printed table show. The task numbers move by a few percent from one run to the next and the other rows by less, which the least and the most in the printed table show.

The `memory` rows measure the two expectations no other row does, footprint and latency, with a tree of a million nodes kept alive while two thousand small ones are built and dropped beside it. Both hold. Snello's peak is the live tree, however many small trees die: `TestNoCollector` in `bench/` builds the program with one, two thousand and ten thousand rounds and fails if the peak grows by a tenth. Go's peak is 2.4 times the live tree, the headroom its collector leaves garbage before a cycle. Snello's rounds are even, and Go's are faster on average through the LLVM backend's margin, 91 µs against 98, but about one in seventeen takes over twice its mean, up to 0.9 ms in this run and 14 ms in the slowest of the ten, when a cycle walks the live tree. The mean was the allocator row again, Go's bump allocation against a free per node, a gap the runtime's allocator closed from 184 µs to 103 and the measurement by median puts at 91. The C backend's peak is twice the LLVM backend's because its `*Node?` is a pointer and a flag, where the LLVM backend's is a bare pointer, the zero-cost option the first of the details below promises.

The one place Snello can lose to Go is code that allocates and discards many small objects, because a collector makes allocation nearly free and pays later in batches, while ownership pays per object. The mitigations are already in the design: arenas, `Copy` structs, and a fast allocator in the runtime, written in stage 5.

Three implementation details the table depends on:

- `T?` on a pointer or an `Index<T>` costs zero extra bytes; `None` is a reserved bit pattern.
- A generation check on an arena index is one compare.
- `Drop` calls inline, so a scope exit is a few stores, not a function call per value.

Compile time is the real trade. Rust-class speed and Go-class compile time have never coexisted, and monomorphization is the reason. The budget is decided early (see Goals); the usual relief is dictionary passing for generics in debug builds, which the trait design permits.

## Accelerators

Not in version one. Designed for from day one, under one rule: **nothing in version one may preclude it.** That rule forbids, for example, hidden allocation in `?` or in string concatenation.

Why it fits Snello better than a collected language:

- The stage 4 backend is LLVM, which already targets NVIDIA and AMD GPUs. That is how Mojo and Julia got there.
- Ownership is the right model for device memory. Moving a buffer to a device is a move; a device buffer is freed by `Drop`; the `Send` inference extends naturally to "may this type live on a device". A collector cannot see device memory, which is why collected languages struggle here.
- `Copy` structs with operator traits are what auto-vectorizers want, and SIMD vector types fit the standard library without new syntax.

What it will need, as a later stage:

- **A kernel-safe subset** the compiler identifies: no allocation, no tasks, no trait-as-type dispatch, no unbounded recursion. This is the style example 16 already uses; it needs naming and checking, not new syntax.
- **A `Device<T>` buffer** in the standard library whose moves are the host-to-device copies.
- **A kernel launch** as a function call with a grid argument.

## Prior art

| Language | What Snello takes from it |
|---|---|
| Inko | The closest existing match; single ownership plus lightweight processes without a GC |
| Hylo | Value semantics without lifetimes |
| Vale | Generational references and region-based elision |
| Mojo | Ownership with lighter origins than Rust |
| Swift 6 | Ownership modifiers and strict concurrency checking |
| Pony | Reference capabilities for data-race-free actors |
| Austral | Linear types kept deliberately small |
| Koka | Perceus reference counting with reuse |
| Nim | Compiling a safer language to C source as its native backend |

## Reading list

In order of when each becomes useful.

1. Thorsten Ball, *Writing an Interpreter in Go* and *Writing a Compiler in Go*. Stage 0.
2. The `go/types` package source. Stage 1.
3. Robert Nystrom, *Crafting Interpreters*, second half. Closures, resolution, bytecode.
4. Osvald et al., *Gentrification Gone Too Far? Affordable 2nd-Class Values*. The paper behind second-class borrows.
5. Weiss et al., *Oxide: The Essence of Rust*. A readable formal model of what Snello simplifies.
6. The Inko compiler and the Hylo language specification.
7. The Go runtime scheduler, `runtime/proc.go`. Stage 4.

*Types and Programming Languages* can wait until stage 1 is working.

## Name

Snello: a hardwood known for durability and resistance to rot, which maps onto memory safety without a collector. Four letters, one syllable, no collision with an existing language. GitHub organization `snello-lang`, following the golang and rust-lang convention. Brand assets are in `../brand/`.
