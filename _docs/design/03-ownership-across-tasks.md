# Ownership across tasks

Status: decided, 2026-10-17; `Send` inference implemented 2026-10-20 (stage 3, task 3.1, `compiler/types/send.go`). This note explains what may cross a task boundary in Snello and why. It is the concurrency half of the memory model in `01-language-design.md`. The worked example is `../../examples/02-pipeline/`.

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
3. **Channel handles are owned and cloned.** The channel itself is a shared, internally synchronized object with a count of its handles; a handle is moved as any owned value is, `ch.Clone()` gives another to hand to another task, and the last handle to go frees the channel (decided 2026-10-21, replacing `Copy` handles, which no last handle could free; `06-tasks-and-channels.md` has the reasoning). Fan-out and fan-in clone the handle once per task; a function within a task borrows it.
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
| `Mutex<T>` | If `T` is | The lock serializes access; it holds only a `T` that is `Send` (2026-10-21), since it exists to share between tasks, and around an `rc.Rc` it was the one way to close a cycle of handles that is never released |
| A trait used as a type (`Shape`) | Only if the trait refines `Send` | The concrete type is unknown |
| `ref T`, `mut T` | Never | Tied to a call frame |

A user never writes `Send` on a type, and listing it after `impl` is refused. It appears in error messages, as in "cannot send `rc.Rc<Node>` across a task: `rc.Rc<Node>` stays in its task", and in two places a program may name it: a trait may refine it, `trait Job: Send`, so that a value of the trait type may cross, and a type parameter may be bounded by it, `T: Send`, so that a generic body may spawn with a `T`.

Two decisions made with the implementation (2026-10-20):

- **`Rc` is not `Send` by a field, not by a rule of the compiler.** Structurally an `Rc<T>` is an owned pointer to a cell of two counts and a value, the same shape `Arc<T>` will have; what differs is that the counts are not atomic, which no type can show. The cell therefore holds a field of the predeclared type `local`, a type that holds nothing and is not `Send`, so every type reaching it is not either. Only the standard library may write `local`, as only it may write `any`; the compiler keeps no table of library names, and a new unsendable library type needs no compiler change. A comment directive was considered and rejected: a comment the parser must read is a second syntax, and a misspelled one silently does nothing.
- **There is no `Sync`.** Rust's second property covers types that change a value through a shared reference, `Cell` and `RefCell`. Snello has none: a `ref` never changes what it borrows, and the only way to a `mut` from a shared value is a lock, which synchronizes. Safe to share and safe to send are therefore one property, and `Arc<T>` is `Send` when `T` is.

## Sharing when you need it

Some state genuinely must be reached from several tasks: a cache, a connection pool, a counter. The tools are library types, and the compiler checks their use:

- `Arc<T>` for shared ownership of an immutable value. Cloning the Arc gives another task its own handle; the value is freed when the last handle drops.
- `Arc<Mutex<T>>` or `Arc<RwLock<T>>` for shared mutable state. The lock owns the data. `Lock()` returns a guard that is a `mut T` borrow and releases on drop, so forgetting to unlock is impossible and so is touching the data without the lock.
- Atomics for counters and flags. (Not in the library yet; `Arc` counts with one atomic add the runtime provides, and a shared counter is an `Arc<Mutex<int>>`.)

The library was written on 2026-10-20 as `05-standard-library.md` decides it: `sync.NewArc(v)`, with `Get` lending the value; `sync.NewMutex(v)` and `sync.NewRwLock(v)`, with `Lock`, `Read` and `Write` returning a guard whose `Get` lends the value, `*g.Get() += 1`, and whose `Drop` gives the lock back; `sync.NewOnce()` with `Do`; `sync.NewWaitGroup()` as a `Copy` handle with `Add`, `Done` and `Wait`. A guard is a borrow-holding struct, so storing it, sending it, or keeping it past the statement that made the lock temporary is refused by the rules of `04-borrows-ref-and-mut.md`, with no rule added for it. The one step from a `ref` of the lock to a `mut` of its value is a function the compiler lowers for the library alone, as `rc` has its two.

`../../examples/03-cache-server.snello` shows `Arc<RwLock<T>>` where a cache is shared by every connection task, each holding a clone of the `Arc` and borrowing the map under the lock for one request at a time.

## What it costs

- **Fan-out of one value to many tasks needs an explicit choice.** In Go, passing the same slice to five goroutines is one line and a latent race. In Snello it is a compile error, and the fix is to say what you meant: clone it, wrap it in an Arc, or send a piece to each task.
- **No borrowed captures in `spawn`.** Go code that spawns a goroutine to work on a local variable and then waits for it must instead move the variable in and get it back through a channel or a join handle. Structured concurrency, where a scope joins all tasks it spawned, is the planned way to let borrows into tasks safely. It is not in version one.
- **`sync.WaitGroup` as Go has it cannot exist**, since it relies on a shared pointer. Snello's version is an owned handle on an internally synchronized counter, cloned for each task and freed by the last handle, like a channel.

## What it gains

- **Data-race freedom at compile time.** The class of bug that Go's race detector hunts at runtime cannot be written, for every value a task owns, borrows or shares through `sync`, and for a package variable, which the section below puts under the same rule (a plain shared global until 2026-11-07, found while checking the claim for the book and closed the same day).
- **Deterministic frees across tasks.** A value received from a channel is freed by the receiver when it is done, not by a collector later.
- **Nothing to annotate.** The pipeline example has one Snello-specific token, a `ref`, and it is there for efficiency rather than safety.
- **Actors for free.** A task that owns its state and receives messages is the natural shape under these rules and needs no locks at all. `../../examples/13-actor.snello` shows it.

## What stage 3 decided

The stage implemented this note as written and settled four things it had left open, each dated in the place it was decided:

- **No `Sync`, and a marker type instead of a rule** (above, 2026-10-20): safe to share and safe to send are one property, and `rc` keeps its handles in their task with a `local` field, not a compiler table.
- **`break` means the innermost loop, everywhere** (2026-10-20, `../specs/stage-3-send.md`, task 3.2): `switch`, `match` and `select` are not loops, so a `break` inside a `select` case leaves the `for` around it, which is what a worker's exit on cancellation means to say; a `break` outside any loop is an error. There are no labels.
- **The program ends when `main` returns** (2026-10-20, `06-tasks-and-channels.md`): the other tasks stop with it, and a program that must wait for them says so with a channel or a `sync.WaitGroup`. A panic in any task ends the program with its message and status 2.
- **The signal channel is `chan ()`** (2026-10-20, `../specs/stage-3-send.md`, task 3.2): `()` is the value that carries nothing, so `done <- ()` says what it does, and anonymous struct types left the grammar with the idiom that needed them.

The checkpoint's race programs live under `../../examples/core/errors/`: a value used after it was sent, an `Arc`'s value changed, a borrowing closure spawned, a borrow across a spawn or on a channel, a value spawned twice, a non-`Send` value spawned or carried, and a lock's guard captured by a task or kept past its statement. Each is rejected with the message its header names, and the concurrency examples 02, 12, 13 and 18 run.

## Package variables

Status: implemented 2026-11-07 (stage 9, task 9.2), as this section says; the decisions were the owner's the same day, and what the building changed is marked "as built".

**The problem.** Everything above moves: a `spawn` takes what its closure mentions, a channel takes what it carries, and a borrow stays in its frame. A package variable is mentioned by no one and moved by nothing; it is simply there, and so every task reaches it. Found on 2026-11-07 while checking the book's claim that a data race is a compile error:

```snello
var counter int = 0

func work(done chan<- ()) {
    for i := 0; i < 1000000; i++ { counter += 1 }
    done <- ()
}
```

Two tasks running `work` print a different number each time, `1623118 of 2000000`, and the same program over a `var name string` frees the string twice, which the debug build reports as `a block was released twice`. The checker accepts both. Decided the same day: there will never be a race in Snello, and this is closed by a rule, not a note.

**The rule, in three sentences.** A package variable may be read from any task, and is never assigned, lent as `mut`, or moved out of. Its type must be one every task may share, which is `Send` as inferred above, so an `rc` handle is refused as a `spawn` refuses it. A change goes through an atomic or a lock the variable holds, and the lock's guard is the only way to a `mut`, as it is for every value that crosses a task.

The checker's message, for the three ways to change one:

```
main.snello:5:9: counter is a package variable, shared by every task; change it through a sync.Mutex or an atomic held in it
```

for `counter += 1`, for `bump(mut counter)`, for `names.Push("x")` through a method taking `mut`, for `m := cache` when the type is not `Copy`, and for a `match` arm or an `if v :=` that binds a part of one by value; as built the message names the three holders, `change it through a sync.Mutex, a sync.Atomic or a sync.Counter it holds`, and the one check sits where every access of a place goes through, the borrow checker's `access`, with a package variable of another package, `os.Args`, made a place so that the rule reaches it. Reading stays as it is anywhere: `fmt.Println(counter)`, `names[i]`, `ref names`, and copying a `Copy` value out. A `const` is unchanged. `take(mut cache)` needs no rule of its own, since it lends the place as `mut` first. A package variable whose type is not `Send`, an `rc` handle, is refused where it is declared, `shared cannot be a package variable: rc.Rc<Node> stays in its task`. The programs are `global-assigned`, `global-mut`, `global-moved` and `global-not-send` under `../../examples/core/errors/`, and `globals` in the backends' programs runs what is allowed from two tasks.

The same program, correct, and the first thing anyone writes, so atomics come with this task rather than with the collections (decided 2026-11-07):

```snello
var hits = sync.NewCounter(0)

func work(done chan<- ()) {
    for i := 0; i < 1000000; i++ { hits.Add(1) }
    done <- ()
}

fmt.Println(hits.Load())   // 2000000, every time
```

**As built, two types, decided that an atomic holds anything.** `sync.Counter` is an `int` in one word of the runtime's, named by a handle the value holds as a lock is, with `Add`, `Load`, `Store` and `CompareAndSwap` each one instruction, sequentially consistent as Go's. `sync.Atomic<T>` holds a value of any type that is `Send`, `Clone` and `Eq`, a flag, a name, a configuration, with `Load` giving a copy, `Store`, `Swap` and `CompareAndSwap` replacing it, each one indivisible step under a lock of the runtime's that the `Atomic` owns, since one word of hardware holds only an integer or a pointer and a value of any size needs the lock. The first draft was a hardware `Atomic<T>` over the integer widths alone, bounded by a new standard trait `Integer` with conversions on a bounded type parameter, `T(cell)` and `i64(v)`; the owner asked for an atomic of anything, the lock-based `Atomic<T>` is simpler and needs nothing new in the language, and the `Integer` trait came out again. Measured on an eight-core arm64 laptop through the LLVM backend, ten million increments: the word costs 7 ns each from one task and 17 ns with two tasks contending for the line, and `Mutex<int>` 10 ns from one task and 10 to 13 ns with two, the spinlock letting one task keep the line for a run; so the lock-based `Atomic<T>` gives up nothing a program would notice, and `Counter` is for the counter that must never park. The methods take `ref self` on both, since the word or the lock is what makes a change through a `ref` safe, as a `Mutex` is; both are `Send` and so may be package variables. A value that is not a count and changes in more than one step is a `sync.NewMutex(map[string]int{})` locked where it is used, and a `RwLock` when read often and changed rarely. No `Arc` is needed around any of them: a package variable is already reachable from every task, which is why it needs the lock.

**What a package variable may hold, and from when.** Its type must be `Send`, inferred as above, whether the type is written out or inferred from the value, and a trait used as its type must refine `Send`: `held cannot be a package variable: rc.Rc<Node> stays in its task`, `unit cannot be a package variable: Shape does not refine Send`, checked once every type of the package is complete (`global-declared-not-send`, `global-trait-not-send`; the first build checked the inferred form alone, which the review before the pull request found). Its initializer, until this task, had to be a literal, which both backends refuse otherwise, `a top-level var or const whose value is not a literal`. A `Mutex` or an `Atomic` is made by a call, so the rule needs initializers that call:

```snello
var registry = sync.NewMutex(map[string]int{})
var config = load("snello.toml")
```

Decided, as built: **such a variable is initialized once, on first use, under the runtime's once-guard**, which is Rust's `LazyLock` and not Go's `init` order. The backends write the initializer as a function and every read of the variable as a check of a flag and a call when it is unset; the flag is an atomic the runtime keeps, `sn_lazy` as built, so two tasks reaching an unmade variable at once make it once and the second waits. What this buys over "run before `main` in declaration order, imported packages first": no order to define across packages, no cycle between two initializers to detect, no question of what an initializer may do before `main`, and a variable never used costs nothing, not even its initializer. What it costs: one atomic load and a predicted branch on each use of such a variable, about a nanosecond, which the lock or the atomic it holds pays anyway. A variable with a literal initializer stays plain memory, as today. As built, each backend writes the variable as a zero cell with the runtime's `sn_lazy` beside it, the initializer as a function of its own, an assignment to the cell written as any statement is, and a getter every use goes through, one acquire load and a call of `sn_lazy_make` when the cell is unmade (`runtime/c/sched.c`); `lazyVar` in `compiler/emit/c/c.go` and `compiler/emit/llvm/llvm.go`. An initializer that panics ends the program as any panic does, and under a test's mark leaves the variable unmade, so the next use runs it again (`lazy-panic`); an initializer that reads another lazy variable makes it first; and one that reads its own variable through a call is a panic naming it, `package variable a is read by its own initializer` (`lazy-cycle`), found by the task that holds the guard meeting itself, so the compiler does not look for the cycle. A task that reaches a variable another task is making waits on a condition of the runtime's, holding its worker thread, which is what an initializer that waits for another task would deadlock on: an initializer is plain code.

**A `ref` of a package variable is still not returned** from a function, `cannot return a borrow of names: a returned borrow is tied to the receiver or a borrowed parameter`: with the rule the borrow would be sound for the program's life, since nothing ever changes or moves the variable, so the refusal is now conservative rather than necessary, and is kept until a program needs the relaxation.

**Drop at the end** stays as both backends do it today: a package variable that owns something is dropped when `main` returns, after the program's tasks, so that the debug build's leak report names what the program itself leaked; a lazy variable never made has nothing to drop.

**The standard streams.** `os.Stdin`, `os.Stdout` and `os.Stderr` are package variables of type `File`, and `io.Reader` and `io.Writer` take `mut Self`, so every `fmt.Fprintln(mut os.Stderr, ...)` and `bufio.NewReader(mut os.Stdin)` in the library, the examples and the book, eighteen places, lends a `mut` of a package variable, which the rule refuses. Four ways were weighed:

1. **Functions: `os.Stdin()`, `os.Stdout()`, `os.Stderr()`**, each returning a fresh handle on the descriptor, a `File` with `stream` set so that dropping it never closes the descriptor (recommended). The handle is a local, lent `mut` as any local is, `fmt.Fprintln(mut os.Stderr(), ...)`, or held, `out := os.Stdout(); w := bufio.NewWriter(mut out)`. Two tasks writing to the terminal through two handles interleave lines and share no memory, which is what the kernel already guarantees for a descriptor. Nothing changes in `io`, `fmt` or `bufio`; the eighteen places gain a pair of parentheses.
2. A `Stream` type whose `Read` and `Write` take `ref`, since writing to a descriptor changes nothing in the value. Correct, but `io.Writer` takes `mut Self`, so `fmt.Fprintln` and `bufio` would need a second bound or a second trait, and a `bufio.Writer` keeps `mut` for its buffer: two shapes of writer where there was one.
3. Exempting the three streams in the checker. The least work and exactly the special case the rule exists to avoid.
4. Keeping them as variables of an `Atomic`-like type the checker knows: a fourth kind of thing.

Decided, as built: the functions. `os.Args` is only read, and stays a variable. The two examples that changed a package variable, `examples/run/packaged/report.snello` and `examples/run/text.snello`, were rewritten on a `Counter` and a local, and are the first two programs to show the rule; `testing`'s fourteen variables became one `sync.NewMutex(Config{})` written once by `Main` and one `sync.NewMutex(Registry{})` for the states of the running tests, both made on first use, with the benchmark's state moved into the runtime, `runtime/c/testing.c`, so that `testing.Loop` costs one call and no lock.

**Locals stay as they are.** The owner asked whether Snello should take Scala's `val`/`var` and Rust's `let`/`let mut`, which the two spellings `x := v` and `var x` could carry with no new keyword: `:=` as the `val`, so that `xs := []int{}; xs.Push(1)` reads `xs was declared with :=, which does not change; declare it with var`. Decided (proposed): not now. A local is reachable by one task, so it closes no race; `mut` at every call site already says where a value changes; and the form costs every program that grows a slice declared with `:=`, a new case in the borrow rules, and a second sentence to learn, against the criterion decided for the stage, that the rule a reader can state in one sentence wins. It is recorded here with its example so that it can be reopened with a reason.

**Outside the guarantee.** A bound C function may write anything, and a C library's statics are its own; the rule covers Snello, as `09-testing.md` says of the race detector.

**Why the rest already holds.** A `spawn` moves what it mentions, a channel moves what it carries, an `Arc` lends `ref` alone, a lock's guard cannot cross a spawn or outlive its statement, a closure captures by the same rules, and a `ref` of a package variable taken inside a task is held by that task's frame and cannot cross a spawn either. A package variable was the one value no move ever touched, and the three sentences above put it under the same rule as everything else.

## How it runs

`06-tasks-and-channels.md` says how a task runs and how a channel hands a value over: the interface the compiler emits, the runtime of stage 3 and the scheduler of stage 4.

## Precedent

Rust's `Send` and `Sync` are the direct ancestor, with the difference that Snello infers them and has no lifetimes to thread through closures. Pony's reference capabilities and Inko's process model show that move-on-send with inferred sendability is enough for real programs. Swift 6's strict concurrency checking arrived at the same rule from a reference-counted starting point.
