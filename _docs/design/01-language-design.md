# Snello language design

Status: decided; the memory model and the task model each confirmed by an implementation that holds them as designed (see the decision at the end of the memory model section and of the concurrency section). This document records the decisions behind Snello and the reasoning for each. Syntax lives in `00-syntax-conventions.md`; the programs that test these decisions live in `../../examples/`.

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
| `nil`, and a zero value for a type that has none | The billion-dollar mistake: a null pointer, an empty lock or file handle that fails where it is used, far from where it was made | `T?`; a zero value only where it is a real value of the type, a number, a string, a slice, a map, an option; `replace(mut place, v)` to move a value out of a place (`00-syntax-conventions.md`, zero values) |
| Exceptions | Hidden control flow, costly without a GC | `Result` and `?` |
| `switch` | Two ways to branch on a value, one of which checks nothing | `match`, which checks that every case is covered; with no value, `match { cond => ..., _ => ... }` runs the first arm whose condition holds |
| Labels on `break` and `continue` | Leaving several loops at once almost always means found or give up | `break` and `continue` mean the innermost loop; a nested exit is a small function that `return`s, with a `T?` result and `??` |
| Parameter defaults and overloading | Defaults are trailing only, cannot skip one and are lost through a function value; overloading makes `Max(1, 2)` and `f := Max` ambiguous once literals and type arguments are inferred | Defaults on struct fields, a constant each, and the settings of a function taken as such a struct, every setting named, in any order, any of them skipped (planned) |
| Inheritance | Fragile base classes | Traits with defaults, and Go embedding |
| Impl blocks, `dyn`, `Box` | Rust vocabulary with Go equivalents | `impl` on the type line, a trait as a type, `*T` |
| async/await | Function coloring | Tasks that block, as goroutines do |
| Structural interfaces | Ambiguous once traits have defaults and fields | Nominal traits declared with `impl` |
| Self-types, implicits, macros | Where Scala's and Rust's complexity lives | Nothing |
| Runtime reflection | It reads and writes a value behind the checker's back, and keeps type metadata alive at run time | A standard trait the compiler derives at compile time, that hands each field to a visitor by name as a `ref` and builds a value field by field, returning a `Result` (planned) |
| `unsafe` and raw pointers | They step outside the memory safety the checker proves | C reached through a package that binds it (`08-packages.md`), behind a handle with `Drop` |
| A garbage collector | The premise | Ownership, `Drop`, arenas, `Rc`/`Arc` where sharing is real |
| Anonymous structs | A struct has a name; `struct{}` as "nothing" is a workaround for a language without a unit | `()`, the unit Snello already has: a signal channel is `chan ()` and the signal is `()` |

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

**Decision: the ownership model holds as designed.** The checker implements the model as written above and in the borrows note: one owner per value, `ref` and `mut` borrows, the six rules, and no lifetime annotation anywhere. The eight core programs compile, run and free everything under a debug build that counts every allocation, and the 27 negative programs are rejected with the planned message and no other. Four refinements were needed, each a precise answer to a case the design had not spelled out and none a new concept for the programmer, each recorded in `04-borrows-ref-and-mut.md`: the arguments of a call are evaluated, and their borrows end, before the receiver is borrowed; a pattern through a borrow copies `Copy` parts and borrows the rest; a `mut` binding through a map read is refused; and a borrow of a temporary dies with its statement. Traits and generics are built on the model unchanged.

## Concurrency

Keep Go's surface: `spawn f()` where Go writes `go f()`, channels, `select`, `sync.WaitGroup`.

Add one rule: **a value crossing a task boundary must be moved, immutable, or atomically shared.** The compiler infers a `Send` property from type structure. A type is `Send` if all its fields are; `Rc` is not, `Arc` is, `Mutex<T>` is when `T` is. Users write no annotations.

A mutex owns its data, as in Rust: `Mutex<T>` hands out a guard that is a `mut T` borrow and unlocks on drop. This makes lock-protected sharing type-checked and buys data-race freedom, which Go does not have.

**Decision: the task model holds as designed.** The compiler implements the rule above and `03-ownership-across-tasks.md` as written: `Send` inferred from the structure of every type with nothing to annotate, a `spawn` that moves what its closure mentions, channels that move, and a lock whose guard is the only way to its value. Examples 02, 12, 13 and 18 run, and eleven race programs are rejected with the planned message and no other. Four refinements were needed, each recorded where it was decided and none a new concept for the programmer: there is no `Sync`, since a `ref` never changes what it borrows and the only way to a `mut` from a shared value is a lock; `rc` is kept in its task by a `local` field the library writes, not by a table in the compiler; `break` means the innermost loop everywhere, so a `select` case can leave the loop around it; and the signal channel is `chan ()`, with anonymous struct types gone from the grammar. One fact of the compiled program shaped the library: a `ref T` is a copy of the value in C, so a lock boxes its value behind a pointer and one lowered function steps from a `ref` of that pointer to a `mut` of the box, the single place the language does so. The runtime sits behind the interface `06-tasks-and-channels.md` fixes, first a thread per task and locked queues, now the scheduler.

Actors fall out naturally: a task that owns its state and receives messages needs no locks at all.

The runtime: an M:N work-stealing scheduler, blocking-looking I/O over kqueue, epoll, or io_uring, and no async/await coloring. Stacks start as fixed virtual reservations with lazy commit and a stack pool; copying stacks come later if task counts demand it, and are possible without a GC since the compiler knows every frame layout. `06-tasks-and-channels.md` has the design, and the thread-per-task runtime that came before it behind the same interface.

## Traits

A trait is a Go interface that may also carry default methods and fields. A type says what it implements on its declaration, and its methods are ordinary Go methods:

```snello
struct Circle impl Shape, Named { r f64 }
func (c ref Circle) Area() f64 { return math.Pi * c.r * c.r }
```

- **Nominal, declared with `impl`.** Go's implicit satisfaction does not combine with default methods and fields, because it becomes ambiguous which default applies and where a field lives. Declaring it costs one word on the type and gives coherence for generics.
- **No impl blocks.** Rust groups methods by trait in separate blocks. Go's methods-with-receivers already say everything needed, so Snello keeps them. The compiler checks the declared traits are satisfied.
- **Fields in traits** are stored inline in the implementing struct and set at construction. A trait field obeys the ownership rules of a struct field, with no exception: a default method on a borrowed `Self` returns a borrow of the field or a clone of it, never the field itself (when example 07's `return n.name` was found to move a string out of a borrow).
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
4. `go/types` is the best reference implementation of a production type checker in a language the team reads natively, and the Go runtime is the reference for the scheduler.

Go's lack of sum types is mitigated with sealed interfaces (an unexported marker method per node family) and the `go-check-sumtype` linter, which restores exhaustiveness checking on type switches.

### Backend: C first, LLVM later

Snello produces a native executable from the first program. The first backend emits C source and build it with the system C compiler, `cc`, which every machine with a developer toolchain has. The runtime, strings, slices, maps, options, printing and panics, tasks and channels, is C under `compiler/emit/c/runtime/`, compiled into every program. Nothing of Go is involved in a Snello program: no collector, no scheduler, no `go build`. The emitted C is the portable assembler that Nim, Haxe and Chicken Scheme started from, and the same optimizing backends that LLVM offers build it.

Why not LLVM IR from the start: C is a fraction of the code per construct, readable when something goes wrong, and needs no toolchain beyond `cc`. The LLVM backend, now the default, came when control over optimization, debug information and the GPU targets demanded it; the runtime carried over unchanged, the backend is one package under `compiler/emit/`, and the C backend stays as the reference the LLVM backend is tested against.

**No collector, ever.** The checker decides where every value is freed, and the emitted code frees it there. A debug build poisons freed memory and asserts that nothing is reachable after its release, and a leak checker runs over the suite, so a missing or misplaced free is a failing test, not a slow leak.

This replaced an earlier plan to emit Go source and keep Go's runtime as scaffolding. The reason: Snello is its own language, and a Snello program must not run on another language's runtime, even temporarily.

### Order of construction

The compiler is built in order: the lexer and parser; the core checker, with moves, borrows and destructors and no traits or generics, emitting C; traits, generics, sum types and `?`; `Send` and tasks; the LLVM backend and the scheduler; then packages, the library and the tools. The work ahead is tracked in the project's milestones, https://github.com/snello-lang/snello/milestones.

Ownership comes before traits because ownership is the bet. The core checker was run against the twenty programs with traits and generics stripped out, so that if it rejected one, the design would change before anything was built on top of it. Borrow checking inside a function, with reborrows, conditional moves, moves in loops, and calls like `v.Push(v.Len())`, is the part of Rust's compiler that took longest, and dropping annotations removes none of it.

The twenty programs in `../../examples/` are the acceptance test for every change.

## Performance

Where Snello should land with the native backend. The table states expectations from the design; the measurements below test them.

| | Against Go | Against Rust |
|---|---|---|
| CPU-bound code | Faster: no collector work, no write barriers, monomorphized generics | Equal: same ownership model, same optimizer, same lack of hidden costs |
| Allocation-heavy code | Mixed: a bump allocator plus batched collection can beat per-object free | Equal, with the same mitigation: arenas |
| Memory footprint | Lower: values die at scope end, no collector headroom | Equal |
| Latency | No pauses; tail latency is flat | Equal |
| Many-core scaling | Better: no stop-the-world, and data races are impossible | Equal, with lighter concurrency than async |
| Dynamic dispatch | Same: a trait as a type is an interface value | Same |
| Compile time | Much slower: monomorphization plus LLVM | Equal |

Measured on an eight-core arm64 laptop by `bench/`: each binary run once for nothing and then ten times, the median reported, with the least and the most in the table `SNELLO_BENCH=1 go test ./bench -run TestBenchmarks -v` prints; the CI workflow measures the same on the x86-64 and arm64 Linux runners and writes each table to its job's summary. The comparison is with Go, the language Snello is written against; there are no Rust versions, so that the table has one comparison and no dependency. Earlier measurements, before and after the runtime's allocator, are in this file's history; what moved since is said below.

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

The same programs on the two Linux runners of the CI workflow, four processors each, from the `bench` job's summaries of run 42365775764, Snello through the LLVM backend against Go, the C backend's numbers in the summaries:

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

What the numbers say against the expectations: CPU-bound code is where the table expected it, a tenth ahead of Go. Allocation-heavy code is level with Go, where the first measurement had it behind by the margin the table foresaw, 63 ms against 38, the collector's batching against a free per node: the runtime's own allocator closed that, a small block coming from a list of free blocks of its size kept by each thread, so an allocation and a free are a few instructions and no lock, as in Go's runtime. `churn` is that allocator under contention, every core allocating at once and blocks freed on a thread other than the one that made them, and it is five to six times ahead of Go, whose collector must walk what eight goroutines make. Strings and a map were behind Go, 194 ms against 150, with the cause put in the library; the map's Swiss table and keyed hash and the strings' single allocation per concatenation, written for the library review, put them at 0.6 of Go's time without a change aimed at this row, and `fmt.Sprint` still allocating every string is no longer what the row measures. A context switch is under Go's since the spinning worker and the inject queue took the thread wake out of a handoff and the task's state became one atomic word. A spawn is twice Go's through the LLVM backend on the Mac, 2.6 times on the x86-64 runner and 1.6 on the arm64 one, from six times before the scheduler's rework: a push wakes a worker only when none spins and hands the spin to the one it wakes, each worker's queue is a lock-free ring as Go's `runq` is, and each keeps a cache of stacks; what is left between it and Go's is the record's `calloc`, where Go draws from a per-P free list, and the stack itself, 256 KB reserved against Go's two, whose first page a burst of spawns touches per task before any runs: the peak memory of the row is that backlog, each task not yet run holding a page, and it is larger through the C backend, whose spawning loop is slower and whose backlog is longer, which is also why its spawn reads higher, the loop's own cost being in the number. The task row's numbers fall in two clusters a run apart, about 365 ns and about 450, which the least and the most in the printed table show. The task numbers move by a few percent from one run to the next and the other rows by less, which the least and the most in the printed table show.

The `memory` rows measure the two expectations no other row does, footprint and latency, with a tree of a million nodes kept alive while two thousand small ones are built and dropped beside it. Both hold. Snello's peak is the live tree, however many small trees die: `TestNoCollector` in `bench/` builds the program with one, two thousand and ten thousand rounds and fails if the peak grows by a tenth. Go's peak is 2.4 times the live tree, the headroom its collector leaves garbage before a cycle. Snello's rounds are even, and Go's are faster on average through the LLVM backend's margin, 91 µs against 98, but about one in seventeen takes over twice its mean, up to 0.9 ms in this run and 14 ms in the slowest of the ten, when a cycle walks the live tree. The mean was the allocator row again, Go's bump allocation against a free per node, a gap the runtime's allocator closed from 184 µs to 103 and the measurement by median puts at 91. The C backend's peak is twice the LLVM backend's because its `*Node?` is a pointer and a flag, where the LLVM backend's is a bare pointer, the zero-cost option the first of the details below promises.

The one place Snello can lose to Go is code that allocates and discards many small objects, because a collector makes allocation nearly free and pays later in batches, while ownership pays per object. The mitigations are already in the design: arenas, `Copy` structs, and the runtime's own fast allocator.

Three implementation details the table depends on:

- `T?` on a pointer or an `Index<T>` costs zero extra bytes; `None` is a reserved bit pattern.
- A generation check on an arena index is one compare.
- `Drop` calls inline, so a scope exit is a few stores, not a function call per value.

Compile time is the real trade. Rust-class speed and Go-class compile time have never coexisted, and monomorphization is the reason. The budget is decided early (see Goals); the usual relief is dictionary passing for generics in debug builds, which the trait design permits.

## Accelerators

Not in version one. Designed for from day one, under one rule: **nothing in version one may preclude it.** That rule forbids, for example, hidden allocation in `?` or in string concatenation.

Why it fits Snello better than a collected language:

- The native backend is LLVM, which already targets NVIDIA and AMD GPUs. That is how Mojo and Julia got there.
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

1. Thorsten Ball, *Writing an Interpreter in Go* and *Writing a Compiler in Go*. The lexer and parser.
2. The `go/types` package source. The checker.
3. Robert Nystrom, *Crafting Interpreters*, second half. Closures, resolution, bytecode.
4. Osvald et al., *Gentrification Gone Too Far? Affordable 2nd-Class Values*. The paper behind second-class borrows.
5. Weiss et al., *Oxide: The Essence of Rust*. A readable formal model of what Snello simplifies.
6. The Inko compiler and the Hylo language specification.
7. The Go runtime scheduler, `runtime/proc.go`. The scheduler.

*Types and Programming Languages* can wait until the checker is working.

## Name

Snello: a hardwood known for durability and resistance to rot, which maps onto memory safety without a collector. Four letters, one syllable, no collision with an existing language. GitHub organization `snello-lang`, following the golang and rust-lang convention. Brand assets are in `../brand/`.
