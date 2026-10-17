# Ownership across tasks

Status: decided, 2026-10-17. This note explains what may cross a task
boundary in Teak and why. It is the concurrency half of the memory model
in `01-language-design.md`. The worked example is `../examples/02-pipeline/`.

## The problem

Go lets any goroutine reach any value that any other goroutine can reach.
That is what makes Go concurrency easy to start with, and it is also why
Go programs have data races: two goroutines writing one map compiles,
runs, and corrupts memory. The race detector finds some of them at
runtime. The compiler finds none.

Without a garbage collector the problem is sharper. If two tasks can both
reach a value, neither can know when to free it. Go's model depends on the
GC in a way that is easy to miss until the GC is gone.

## The rule

**A value crossing a task boundary must be moved, immutable, or
atomically shared.** Nothing else crosses.

A task boundary is any of:

- a `go` statement, for the values it captures,
- a channel send, for the value sent,
- a channel receive, for the value received.

Concretely:

1. **Sending moves.** `ch <- v` transfers ownership of `v` to the channel.
   Using `v` afterwards is a compile error.
2. **Receiving owns.** `v := <-ch` makes this task the owner of `v`. When
   `v` goes out of scope it is freed here, deterministically.
3. **Channel handles are `Copy`.** The channel itself is a shared,
   internally synchronized object; a handle to it is a small value any
   number of tasks may hold. This is what makes fan-out and fan-in
   one-liners.
4. **`go` moves its captures.** A closure passed to `go` takes ownership
   of every variable it mentions. There is no capture by reference across
   a task boundary.
5. **Borrows never cross.** A `ref T` or `mut T` cannot be captured by
   `go` or sent on a channel. A borrow is tied to a call frame, and the
   new task has its own frames.

## `Send`, inferred

A type may cross a task boundary if it is `Send`. The compiler infers
this from structure; no annotation is written:

| Type | `Send`? | Why |
|---|---|---|
| Numbers, bools, strings, `Copy` structs | Yes | No shared interior |
| A struct or enum | If every field is | Structural |
| Slices, maps, `*T` | If `T` is | Owned containers and pointers |
| Channel handles | Yes | The channel synchronizes itself |
| `Rc<T>` | No | Non-atomic reference count |
| `Arc<T>` | If `T` is `Send + Sync` | Atomic reference count |
| `Mutex<T>` | If `T` is | The lock serializes access |
| A trait used as a type (`Shape`) | Only if the trait refines `Send` | The concrete type is unknown |
| `ref T`, `mut T` | Never | Tied to a call frame |

A user never writes `Send`. It appears only in error messages, as in
"cannot send `Rc<Node>` across a task: `Rc` is not `Send`".

## Sharing when you need it

Some state genuinely must be reached from several tasks: a cache, a
connection pool, a counter. The tools are library types, and the compiler
checks their use:

- `Arc<T>` for shared ownership of an immutable value. Cloning the Arc
  gives another task its own handle; the value is freed when the last
  handle drops.
- `Arc<Mutex<T>>` or `Arc<RwLock<T>>` for shared mutable state. The lock
  owns the data. `Lock()` returns a guard that is a `mut T` borrow and
  releases on drop, so forgetting to unlock is impossible and so is
  touching the data without the lock.
- Atomics for counters and flags.

`../examples/03-http-cache.teak` shows `Arc<RwLock<T>>` where a cache is
shared by every request task.

## What it costs

- **Fan-out of one value to many tasks needs an explicit choice.** In Go,
  passing the same slice to five goroutines is one line and a latent
  race. In Teak it is a compile error, and the fix is to say what you
  meant: clone it, wrap it in an Arc, or send a piece to each task.
- **No borrowed captures in `go`.** Go code that spawns a goroutine to
  work on a local variable and then waits for it must instead move the
  variable in and get it back through a channel or a join handle.
  Structured concurrency, where a scope joins all tasks it spawned, is
  the planned way to let borrows into tasks safely. It is not in version
  one.
- **`sync.WaitGroup` as Go has it cannot exist**, since it relies on a
  shared pointer. Teak's version is a `Copy` handle to an internally
  synchronized counter, like a channel.

## What it gains

- **Data-race freedom at compile time.** The class of bug that Go's race
  detector hunts at runtime cannot be written.
- **Deterministic frees across tasks.** A value received from a channel is
  freed by the receiver when it is done, not by a collector later.
- **Nothing to annotate.** The pipeline example has one Teak-specific
  token, a `ref`, and it is there for efficiency rather than safety.
- **Actors for free.** A task that owns its state and receives messages is
  the natural shape under these rules and needs no locks at all.
  `../examples/13-actor.teak` shows it.

## Precedent

Rust's `Send` and `Sync` are the direct ancestor, with the difference
that Teak infers them and has no lifetimes to thread through closures.
Pony's reference capabilities and Inko's process model show that
move-on-send with inferred sendability is enough for real programs.
Swift 6's strict concurrency checking arrived at the same rule from a
reference-counted starting point.
