# Ownership across tasks

Status: decided, 2026-10-17; `Send` inference implemented 2026-10-20 (stage 3, task 3.1, `types/send.go`). This note explains what may cross a task boundary in Teak and why. It is the concurrency half of the memory model in `01-language-design.md`. The worked example is `../../examples/02-pipeline/`.

## The problem

Go lets any goroutine reach any value that any other goroutine can reach. That is what makes Go concurrency easy to start with, and it is also why Go programs have data races: two goroutines writing one map compiles, runs, and corrupts memory. The race detector finds some of them at runtime. The compiler finds none.

Without a garbage collector the problem is sharper. If two tasks can both reach a value, neither can know when to free it. Go's model depends on the GC in a way that is easy to miss until the GC is gone.

## The rule

**A value crossing a task boundary must be moved, immutable, or atomically shared.** Nothing else crosses.

A task boundary is any of:

- a `spawn` statement, for the values it captures,
- a channel send, for the value sent,
- a channel receive, for the value received.

Concretely:

1. **Sending moves.** `ch <- v` transfers ownership of `v` to the channel. Using `v` afterwards is a compile error.
2. **Receiving owns.** `v := <-ch` makes this task the owner of `v`. When `v` goes out of scope it is freed here, deterministically.
3. **Channel handles are `Copy`.** The channel itself is a shared, internally synchronized object; a handle to it is a small value any number of tasks may hold. This is what makes fan-out and fan-in one-liners.
4. **`spawn` moves its captures.** A closure passed to `spawn` takes ownership of every variable it mentions. There is no capture by reference across a task boundary.
5. **Borrows never cross.** A `ref T` or `mut T` cannot be captured by `spawn` or sent on a channel. A borrow is tied to a call frame, and the new task has its own frames.

## `Send`, inferred

A type may cross a task boundary if it is `Send`. The compiler infers this from structure; no annotation is written:

| Type | `Send`? | Why |
|---|---|---|
| Numbers, bools, strings, `Copy` structs | Yes | No shared interior |
| A struct or enum | If every field is | Structural |
| Slices, maps, `*T` | If `T` is | Owned containers and pointers |
| Channel handles | Yes | The channel synchronizes itself |
| `Rc<T>` | No | Non-atomic reference count |
| `Arc<T>` | If `T` is | Atomic reference count |
| `Mutex<T>` | If `T` is | The lock serializes access |
| A trait used as a type (`Shape`) | Only if the trait refines `Send` | The concrete type is unknown |
| `ref T`, `mut T` | Never | Tied to a call frame |

A user never writes `Send` on a type, and listing it after `impl` is refused. It appears in error messages, as in "cannot send `rc.Rc<Node>` across a task: `rc.Rc<Node>` stays in its task", and in two places a program may name it: a trait may refine it, `trait Job: Send`, so that a value of the trait type may cross, and a type parameter may be bounded by it, `T: Send`, so that a generic body may spawn with a `T`.

Two decisions made with the implementation (2026-10-20):

- **`Rc` is not `Send` by a field, not by a rule of the compiler.** Structurally an `Rc<T>` is an owned pointer to a cell of two counts and a value, the same shape `Arc<T>` will have; what differs is that the counts are not atomic, which no type can show. The cell therefore holds a field of the predeclared type `local`, a type that holds nothing and is not `Send`, so every type reaching it is not either. Only the standard library may write `local`, as only it may write `any`; the compiler keeps no table of library names, and a new unsendable library type needs no compiler change. A comment directive was considered and rejected: a comment the parser must read is a second syntax, and a misspelled one silently does nothing.
- **There is no `Sync`.** Rust's second property covers types that change a value through a shared reference, `Cell` and `RefCell`. Teak has none: a `ref` never changes what it borrows, and the only way to a `mut` from a shared value is a lock, which synchronizes. Safe to share and safe to send are therefore one property, and `Arc<T>` is `Send` when `T` is.

## Sharing when you need it

Some state genuinely must be reached from several tasks: a cache, a connection pool, a counter. The tools are library types, and the compiler checks their use:

- `Arc<T>` for shared ownership of an immutable value. Cloning the Arc gives another task its own handle; the value is freed when the last handle drops.
- `Arc<Mutex<T>>` or `Arc<RwLock<T>>` for shared mutable state. The lock owns the data. `Lock()` returns a guard that is a `mut T` borrow and releases on drop, so forgetting to unlock is impossible and so is touching the data without the lock.
- Atomics for counters and flags. (Not in the library yet; `Arc` counts with one atomic add the runtime provides, and a shared counter is an `Arc<Mutex<int>>`.)

The library was written on 2026-10-20 as `05-standard-library.md` decides it: `sync.NewArc(v)`, with `Get` lending the value; `sync.NewMutex(v)` and `sync.NewRwLock(v)`, with `Lock`, `Read` and `Write` returning a guard whose `Get` lends the value, `*g.Get() += 1`, and whose `Drop` gives the lock back; `sync.NewOnce()` with `Do`; `sync.NewWaitGroup()` as a `Copy` handle with `Add`, `Done` and `Wait`. A guard is a borrow-holding struct, so storing it, sending it, or keeping it past the statement that made the lock temporary is refused by the rules of `04-borrows-ref-and-mut.md`, with no rule added for it. The one step from a `ref` of the lock to a `mut` of its value is a function the compiler lowers for the library alone, as `rc` has its two.

`../../examples/03-http-cache.teak` shows `Arc<RwLock<T>>` where a cache is shared by every request task.

## What it costs

- **Fan-out of one value to many tasks needs an explicit choice.** In Go, passing the same slice to five goroutines is one line and a latent race. In Teak it is a compile error, and the fix is to say what you meant: clone it, wrap it in an Arc, or send a piece to each task.
- **No borrowed captures in `spawn`.** Go code that spawns a goroutine to work on a local variable and then waits for it must instead move the variable in and get it back through a channel or a join handle. Structured concurrency, where a scope joins all tasks it spawned, is the planned way to let borrows into tasks safely. It is not in version one.
- **`sync.WaitGroup` as Go has it cannot exist**, since it relies on a shared pointer. Teak's version is a `Copy` handle to an internally synchronized counter, like a channel.

## What it gains

- **Data-race freedom at compile time.** The class of bug that Go's race detector hunts at runtime cannot be written.
- **Deterministic frees across tasks.** A value received from a channel is freed by the receiver when it is done, not by a collector later.
- **Nothing to annotate.** The pipeline example has one Teak-specific token, a `ref`, and it is there for efficiency rather than safety.
- **Actors for free.** A task that owns its state and receives messages is the natural shape under these rules and needs no locks at all. `../../examples/13-actor.teak` shows it.

## What stage 3 decided

The stage implemented this note as written and settled four things it had left open, each dated in the place it was decided:

- **No `Sync`, and a marker type instead of a rule** (above, 2026-10-20): safe to share and safe to send are one property, and `rc` keeps its handles in their task with a `local` field, not a compiler table.
- **`break` means the innermost loop, everywhere** (2026-10-20, `../specs/stage-3-send.md`, task 3.2): `switch`, `match` and `select` are not loops, so a `break` inside a `select` case leaves the `for` around it, which is what a worker's exit on cancellation means to say; a `break` outside any loop is an error. There are no labels.
- **The program ends when `main` returns** (2026-10-20, `06-tasks-and-channels.md`): the other tasks stop with it, and a program that must wait for them says so with a channel or a `sync.WaitGroup`. A panic in any task ends the program with its message and status 2.
- **The signal channel is `chan ()`** (2026-10-20, `../specs/stage-3-send.md`, task 3.2): `()` is the value that carries nothing, so `done <- ()` says what it does, and anonymous struct types left the grammar with the idiom that needed them.

The checkpoint's race programs live under `../../examples/core/errors/`: a value used after it was sent, an `Arc`'s value changed, a borrowing closure spawned, a borrow across a spawn or on a channel, a value spawned twice, a non-`Send` value spawned or carried, and a lock's guard captured by a task or kept past its statement. Each is rejected with the message its header names, and the concurrency examples 02, 12, 13 and 18 run.

## How it runs

`06-tasks-and-channels.md` says how a task runs and how a channel hands a value over: the interface the compiler emits, the runtime of stage 3 and the scheduler of stage 4.

## Precedent

Rust's `Send` and `Sync` are the direct ancestor, with the difference that Teak infers them and has no lifetimes to thread through closures. Pony's reference capabilities and Inko's process model show that move-on-send with inferred sendability is enough for real programs. Swift 6's strict concurrency checking arrived at the same rule from a reference-counted starting point.
