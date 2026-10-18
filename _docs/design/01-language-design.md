# Teak language design

Status: draft, 2026-10-17. This document records the decisions behind Teak and the reasoning for each. Syntax lives in `00-syntax-conventions.md`; the programs that test these decisions live in `../../examples/`.

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

**Importing Go packages is not a language feature.** The first backend emits Go source, which would make importing Go packages easy. It is deliberately not offered: every Go value crossing that boundary brings `nil`, aliasing, and GC dependence with it, and the native backend in stage 4 could never honor such imports. Interop with Go or C will be a bounded foreign-function boundary, designed separately once the native backend exists.

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

## Concurrency

Keep Go's surface: `spawn f()` where Go writes `go f()`, channels, `select`, `sync.WaitGroup`.

Add one rule: **a value crossing a task boundary must be moved, immutable, or atomically shared.** The compiler infers a `Send` property from type structure. A type is `Send` if all its fields are; `Rc` is not, `Arc` is, `Mutex<T>` is when `T` is. Users write no annotations.

A mutex owns its data, as in Rust: `Mutex<T>` hands out a guard that is a `mut T` borrow and unlocks on drop. This makes lock-protected sharing type-checked and buys data-race freedom, which Go does not have.

Actors fall out naturally: a task that owns its state and receives messages needs no locks at all.

Runtime, when it arrives in stage 4: an M:N work-stealing scheduler, blocking-looking I/O over kqueue, epoll, or io_uring, and no async/await coloring. Stacks start as fixed virtual reservations with lazy commit and a stack pool; copying stacks come later if task counts demand it, and are possible without a GC since the compiler knows every frame layout.

## Traits

A trait is a Go interface that may also carry default methods and fields. A type says what it implements on its declaration, and its methods are ordinary Go methods:

```teak
struct Circle impl Shape, Named { r f64 }
func (c ref Circle) Area() f64 { return math.Pi * c.r * c.r }
```

- **Nominal, declared with `impl`.** Go's implicit satisfaction does not combine with default methods and fields, because it becomes ambiguous which default applies and where a field lives. Declaring it costs one word on the type and gives coherence for generics.
- **No impl blocks.** Rust groups methods by trait in separate blocks. Go's methods-with-receivers already say everything needed, so Teak keeps them. The compiler checks the declared traits are satisfied.
- **Fields in traits** are stored inline in the implementing struct and set at construction.
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
3. One toolchain for the first three stages: the compiler is Go, emits Go, and `go build` produces the binary.
4. `go/types` is the best reference implementation of a production type checker in a language the team reads natively, and the Go runtime is the reference for the stage 4 scheduler.

Go's lack of sum types is mitigated with sealed interfaces (an unexported marker method per node family) and the `go-check-sumtype` linter, which restores exhaustiveness checking on type switches.

### Backend: Go source first, LLVM later

Stages 0 through 3 emit Go source. This gives goroutines, channels, `select`, the scheduler, and a cross-platform optimizing backend for free, so the novel work, the type system, can be validated before any runtime exists.

**This does not make Teak garbage collected.** The language's semantics never depend on a collector: the checker decides where every value is freed, and a Teak program that is only correct because Go's collector caught something is a bug in the checker. To keep that honest, the Go backend emits an explicit release call at every point the checker computed a drop, and a debug mode asserts that nothing is reachable after its release. Go's collector is scaffolding that stage 4 removes; no Teak program will notice it was ever there.

Stage 4 emits LLVM IR as text and shells out to `clang`, with a runtime in C. No cgo, no bindings. Borgo, a Rust-flavored language that compiles to Go, is evidence the first half of this path works.

### Stages

| Stage | Deliverable | Rough effort |
|---|---|---|
| 0 | Lexer and parser for the Go-like syntax, modeled on `go/scanner` and `go/parser` | weeks |
| 1 | Core checker: structs, functions, `*T`, moves, `ref`/`mut` borrows, destructors. No traits, no generics. Emit Go. | 4 to 8 months |
| 2 | Traits, generics, sum types, `Option`/`Result`, `?`. | 2 to 3 months |
| 3 | `Send` inference on `spawn` and channel sends, mapped to Go concurrency. | 1 month |
| 4 | LLVM backend, C runtime with M:N scheduler, netpoller, stack management. | year two |
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

The one place Teak can lose to Go is code that allocates and discards many small objects, because a collector makes allocation nearly free and pays later in batches, while ownership pays per object. The mitigations are already in the design: arenas, `Copy` structs, and a fast allocator in the stage 4 runtime.

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
| Borgo | Compiling a safer language to Go source |

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
