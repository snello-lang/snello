# Teak language design

Status: draft, 2026-10-17; the memory model confirmed at the stage 1 checkpoint, 2026-10-18 (see the decision at the end of the memory model section); the task model confirmed at the stage 3 checkpoint, 2026-10-20 (see the decision at the end of the concurrency section). This document records the decisions behind Teak and the reasoning for each. Syntax lives in `00-syntax-conventions.md`; the programs that test these decisions live in `../../examples/`.

## Goals

1. Go's syntax and simplicity. A Go developer reads Teak on day one.
2. Memory safety at compile time, without a garbage collector.
3. No lifetime annotations.
4. Go's concurrency model: lightweight tasks, channels, `select`.
5. Data-race freedom, checked by the compiler.
6. Traits in the Scala sense: default methods, state, mixins.
7. Compile times in Go's range, not C++'s.

Goal 2 is the one that shapes everything. Go's simplicity is largely bought by its garbage collector, and Go's concurrency leans on it too, since any pointer can be shared with any goroutine. Removing the collector means a memory model that decides when values die, and that model has to stay simple enough that goals 1 and 3 survive.

## Non-goals

**Teak is not a drop-in replacement for Go.** The syntax is Go's so that a Go developer reads Teak immediately, but ordinary Go programs do not compile as Teak and are not meant to. Go code relies on `nil`, first-class pointers, implicitly shared mutable state, structural interfaces, and a garbage collector keeping values alive; Teak has none of these. Code is ported to Teak, not copied. The relationship is Zig to C or Kotlin to Java, not TypeScript to JavaScript. Code that already follows ownership discipline, such as a channel pipeline, looks nearly identical in both; code that does not will change shape.

**Importing Go packages is not a language feature.** The compiler is written in Go, which makes a bridge to Go packages tempting. It is deliberately not offered: every Go value crossing that boundary brings `nil`, aliasing, and GC dependence with it, and a Teak program runs on its own runtime, which could never honor such imports. Interop with Go or C will be a bounded foreign-function boundary, designed separately once the native backend exists.

## What Teak leaves out

Simplicity is a goal with the same weight as the others, and it is kept by subtraction. Each of these was considered and rejected, and each rejection is a decision that needs an argument to reverse:

| Left out | Why | What Teak has instead |
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
| Anonymous structs | A struct has a name; `struct{}` as "nothing" is a workaround for a language without a unit | `()`, the unit Teak already has: a signal channel is `chan ()` and the signal is `()` (decided 2026-10-20) |

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
- Teak therefore has lifetimes but never writes them: every borrow's lifetime is inferred from the function it lives in or from the one input it was derived from, and programs where that is ambiguous are rejected rather than annotated. `04-borrows-ref-and-mut.md` has the rules and the list of what cannot be written.
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

```teak
struct Circle impl Shape, Named { r f64 }
func (c ref Circle) Area() f64 { return math.Pi * c.r * c.r }
```

- **Nominal, declared with `impl`.** Go's implicit satisfaction does not combine with default methods and fields, because it becomes ambiguous which default applies and where a field lives. Declaring it costs one word on the type and gives coherence for generics.
- **No impl blocks.** Rust groups methods by trait in separate blocks. Go's methods-with-receivers already say everything needed, so Teak keeps them. The compiler checks the declared traits are satisfied.
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
2. Self-hosting later becomes a translation job, since Teak has Go syntax.
3. `go build` builds the compiler, and the compiler needs nothing but the system C compiler to build a Teak program.
4. `go/types` is the best reference implementation of a production type checker in a language the team reads natively, and the Go runtime is the reference for the stage 4 scheduler.

Go's lack of sum types is mitigated with sealed interfaces (an unexported marker method per node family) and the `go-check-sumtype` linter, which restores exhaustiveness checking on type switches.

### Backend: C first, LLVM later

Teak produces a native executable from the first program. Stages 1 through 3 emit C source and build it with the system C compiler, `cc`, which every machine with a developer toolchain has. The runtime, strings, slices, maps, options, printing and panics, and from stage 3 tasks and channels, is C under `compiler/emit/c/runtime/`, compiled into every program. Nothing of Go is involved in a Teak program: no collector, no scheduler, no `go build`. The emitted C is the portable assembler that Nim, Haxe and Chicken Scheme started from, and the same optimizing backends that LLVM offers build it.

Why not LLVM IR from the start: C is a fraction of the code per construct, readable when something goes wrong, and needs no toolchain beyond `cc`. Stage 4 replaces the C emitter with LLVM IR when control over optimization, debug information, and the GPU targets of stage 6 demand it. The runtime carries over unchanged, and the C emitter is shaped so that the replacement is one package under `compiler/emit/`.

**No collector, ever.** The checker decides where every value is freed, and the emitted C frees it there (task 1.9). A debug build poisons freed memory and asserts that nothing is reachable after its release, and a leak checker runs over the suite, so a missing or misplaced free is a failing test, not a slow leak.

Decided 2026-10-18, replacing an earlier plan to emit Go source for the first three stages and keep Go's runtime as scaffolding. The owner's reason: Teak is its own language, and a Teak program must not run on another language's runtime, even temporarily.

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

Where Teak should land once the native backend exists. Claims here are expectations from the design, not measurements; they become the benchmark suite for stage 4.

| | Against Go | Against Rust |
|---|---|---|
| CPU-bound code | Faster: no collector work, no write barriers, monomorphized generics | Equal: same ownership model, same optimizer, same lack of hidden costs |
| Allocation-heavy code | Mixed: a bump allocator plus batched collection can beat per-object free | Equal, with the same mitigation: arenas |
| Memory footprint | Lower: values die at scope end, no collector headroom | Equal |
| Latency | No pauses; tail latency is flat | Equal |
| Many-core scaling | Better: no stop-the-world, and data races are impossible | Equal, with lighter concurrency than async |
| Dynamic dispatch | Same: a trait as a type is an interface value | Same |
| Compile time | Much slower: monomorphization plus LLVM | Equal |

Measured 2026-10-20, on an eight-core arm64 laptop, by `bench/`, and again 2026-10-21 with the runtime's own allocator (stage 5, task 5.1), the numbers below (the programs in Teak, Go and Rust, best of three runs; `TEAK_BENCH=1 go test ./bench -v` reprints the table):

| Program | Teak (LLVM) | Teak (C) | Go | Rust |
|---|---|---|---|---|
| `fib`, CPU-bound | 31 ms | 28 ms | 34 ms | 31 ms |
| `tree`, allocation-heavy | 38 ms | 41 ms | 35 ms | 61 ms |
| `words`, strings and a map | 194 ms | 197 ms | 150 ms | 148 ms |
| `tasks`, a spawn | 997 ns | 1010 ns | 176 ns | not measured |
| `tasks`, a context switch | 991 ns | 988 ns | 125 ns | not measured |
| `memory`, peak memory | 33 MB | 65 MB | 78 MB | 34 MB |
| `memory`, the mean round; rounds over twice it | 103 µs; none | 119 µs; none | 96 µs; 178 of 2,000, up to 0.7 ms | 177 µs; none |

What the numbers say against the expectations: CPU-bound code is where the table expected it, level with Rust and ahead of Go. Allocation-heavy code was level with Rust and behind Go by the margin the table foresaw, 63 ms against 38, the collector's batching against a free per node; the runtime's own allocator, written for that measurement, brings it within a tenth of Go and ahead of Rust, whose boxes go to the system's allocator: a small block comes from a list of free blocks of its size kept by each thread, so an allocation and a free are a few instructions and no lock, as in Go's runtime. Strings and a map are behind both, by less since the allocator, 256 ms before it: `fmt.Sprint` allocates every string and the map hashes every key with a general routine, where Go's and Rust's are specialized; the library, not the language. A spawn and a switch cost seven to eleven times Go's: a task is a reserved stack and a locked queue today, where Go's is a two-kilobyte stack and a lock-free one, and a channel operation takes the channel's mutex where Go's fast path does not; the shape is right and the constants are the next work. Rust has no tasks of this kind in its standard library, so that row is not measured against it. The task numbers move by about a fifth from one run to the next, since they time the operating system's threads waking each other; the other rows repeat within a few milliseconds.

The `memory` rows measure the two expectations no other row does, footprint and latency, with a tree of a million nodes kept alive while two thousand small ones are built and dropped beside it. Both hold. Teak's peak is the live tree, however many small trees die: `TestNoCollector` in `bench/` builds the program with one, two thousand and ten thousand rounds and fails if the peak grows by a tenth. Go's peak is 2.4 times the live tree, the headroom its collector leaves garbage before a cycle. Teak's rounds are even, and Go's are faster on average but about one in eleven takes over twice its mean, up to 0.7 ms in this run and 7 ms in the first, when a cycle walks the live tree; Rust's match Teak's, as the model predicts. The mean is the allocator row again: Go's bump allocation against a free per node, a gap the runtime's allocator closed from 184 µs to 103. The C backend's peak is twice the LLVM backend's because its `*Node?` is a pointer and a flag, where the LLVM backend's is a bare pointer, the zero-cost option the first of the details below promises.

The one place Teak can lose to Go is code that allocates and discards many small objects, because a collector makes allocation nearly free and pays later in batches, while ownership pays per object. The mitigations are already in the design: arenas, `Copy` structs, and a fast allocator in the runtime, written in stage 5.

Three implementation details the table depends on:

- `T?` on a pointer or an `Index<T>` costs zero extra bytes; `None` is a reserved bit pattern.
- A generation check on an arena index is one compare.
- `Drop` calls inline, so a scope exit is a few stores, not a function call per value.

Compile time is the real trade. Rust-class speed and Go-class compile time have never coexisted, and monomorphization is the reason. The budget is decided early (see Goals); the usual relief is dictionary passing for generics in debug builds, which the trait design permits.

## Accelerators

Not in version one. Designed for from day one, under one rule: **nothing in version one may preclude it.** That rule forbids, for example, hidden allocation in `?` or in string concatenation.

Why it fits Teak better than a collected language:

- The stage 4 backend is LLVM, which already targets NVIDIA and AMD GPUs. That is how Mojo and Julia got there.
- Ownership is the right model for device memory. Moving a buffer to a device is a move; a device buffer is freed by `Drop`; the `Send` inference extends naturally to "may this type live on a device". A collector cannot see device memory, which is why collected languages struggle here.
- `Copy` structs with operator traits are what auto-vectorizers want, and SIMD vector types fit the standard library without new syntax.

What it will need, as a later stage:

- **A kernel-safe subset** the compiler identifies: no allocation, no tasks, no trait-as-type dispatch, no unbounded recursion. This is the style example 16 already uses; it needs naming and checking, not new syntax.
- **A `Device<T>` buffer** in the standard library whose moves are the host-to-device copies.
- **A kernel launch** as a function call with a grid argument.

## Prior art

| Language | What Teak takes from it |
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
5. Weiss et al., *Oxide: The Essence of Rust*. A readable formal model of what Teak simplifies.
6. The Inko compiler and the Hylo language specification.
7. The Go runtime scheduler, `runtime/proc.go`. Stage 4.

*Types and Programming Languages* can wait until stage 1 is working.

## Name

Teak: a hardwood known for durability and resistance to rot, which maps onto memory safety without a collector. Four letters, one syllable, no collision with an existing language. GitHub organization `teak-lang`, following the golang and rust-lang convention. Brand assets are in `../brand/`.
