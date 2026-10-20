# Stage 3: ownership across tasks

Goal: the rule in `../design/03-ownership-across-tasks.md` is enforced, and programs built on it run. Data races are compile errors. Tasks run on the system's threads and channels are locked queues in the C runtime, behind the interface the stage 4 scheduler implements; what this stage settles is the model, the checks and the library, and the runtime is the reference implementation underneath them.

Reviewed 2026-10-20 before the first task, against the code as stage 2 left it: the runtime and the emitter, which refuse every concurrent construct today, were missing from the plan, and example 03 cannot be an acceptance test while `net/http` does not exist. The tasks below are the reviewed shape; the shared-state library moved after the runtime it needs.

## Task 3.1: `Send` inference

**Description:** A value may cross from one task to another only when its type is `Send`: no other task can reach the memory it owns, so the receiving task owns it alone. The compiler computes `Send` from the structure of the type; a program never writes it on a declaration, and listing it after `impl` is an error. The rules:

| Type | `Send` |
|---|---|
| numbers, `bool`, `string`, a channel handle | yes |
| a struct, enum, tuple, option or result | when every field is |
| a slice, array, map or owned pointer `*T` | when `T` is |
| `ref T`, `mut T` | never: a borrow points into a frame of the task that made it |
| a trait used as a type | only if the trait refines `Send`, `trait Job: Send` |
| a type parameter `T` | only if bounded by it, `T: Send` |

Two things to know beyond the table:

- *How `rc.Rc` is kept out.* By structure an `Rc<T>` is an owned pointer to a cell of two counts and a value, which the table would call `Send`; what makes it unsafe, counts changed without atomics, is not visible in the type. The library says it with a field: `local` is a predeclared type that holds nothing and is not `Send`, so a struct with a `local` field is not `Send` either. Only the standard library may write `local`, as only it may write `any`. `rc`'s cell has one, which covers `Rc` and `Weak` at once, and the compiler keeps no list of library type names.
- *There is no second property.* Rust needs `Sync` beside `Send` for types that change a value through a shared reference. Teak has no such type: a `ref` never changes what it borrows, and the only way to a `mut` from a shared value is a lock, which synchronizes. So "safe to share" and "safe to send" are the same question, and `Arc<T>` is `Send` when `T` is.

**Acceptance criteria:**
- [x] A table test over the type kinds in the design note (2026-10-20: `TestIsSend`, thirty-five kinds, each with its reason when it is not `Send`)
- [x] Recursive types terminate (2026-10-20: `Expr` through a pointer, and `Ring` that is not `Send` for another reason; the second looped a first version that walked the reason apart from the answer, so the reason is cached with the answer)
- [x] `rc.Rc<T>` and `rc.Weak<T>` are not `Send` for any `T`, and the error names the package (2026-10-20: `rc.Rc<int> stays in its task`, from `TestSendInPrograms` through the library importer)

**Verification:**
- [x] Tests pass: `go test ./types/` (2026-10-20; `go test ./...` too, the examples suite compiling `rc` with its new field)

**Done:** 2026-10-20, one slice. `Send` is a predeclared trait beside `Copy`, decided by structure in `standardImplements` and never listed: `impl Send` is refused with "Send is not listed: a type is Send when every field is", while `trait Job: Send` and `T: Send` resolve as any trait does. `NotSend` in `types/send.go` is the walk of `IsCopy`, returning the reason rather than a boolean, and `IsSend` is its answer being empty: the reason names the part that stops the crossing, the borrow, the trait, the type parameter or the declared type that stays in its task, so that a struct holding an `rc.Rc<Node>` is reported by the `Rc`. A declared type caches the answer and the reason together, provisionally `Send` while its own fields are walked, so a type reaching itself through a pointer ends the recursion. The predeclared type `local` is a basic kind after `never`, a name only the library may write (the check beside `any`'s in `typexpr.go`), a byte in C since C has no empty type; `rc.cell` holds one as its first field, which covers `Rc` and `Weak` at once and leaves the compiler without a table of library names. The universe grows by two names, to 51. `std/builtin/builtin.teak` documents both; the design note records why a field and not a directive, and that there is no `Sync`; chapter 10 of the book opens with the rule and the table.

**Dependencies:** Stage 2
**Files:** `types/send.go`, `types/send_test.go`, `types/universe.go`, `std/rc/rc.teak`, `std/builtin/builtin.teak`
**Scope:** S

## Task 3.2: Task boundary checks

**Description:** `spawn` moves its captures and arguments and requires them `Send`; a channel's element type must be `Send` where the channel type is formed; sending moves; receiving yields `T?`; a borrow cannot cross any of these. `close(ch)` joins the builtins; `for range ch` receives until the channel is closed and drained. A `break` in a `select` case leaves the enclosing loop, as example 12 requires, and the rule goes into the syntax conventions.

**Acceptance criteria:**
- [x] The rejected programs at the bottom of example 02 are rejected with messages naming the rule (2026-10-20: `use-after-spawn`, `spawn-twice` and the existing `borrow-across-spawn` under `examples/core/errors/`)
- [x] Examples 02, 13 and 18 check; 12 checks but for `sync` and `time.After` (2026-10-20)

**Verification:**
- [x] Tests pass: `go test ./types/` (2026-10-20; `go test ./...` too)
- [x] Negative programs under `examples/core/errors/` for races fail as planned (2026-10-20: `TestNegativePrograms` over the four new programs)

**Done:** 2026-10-20, three slices. The boundary checks (first slice), the removal of the anonymous struct with `()` as the signal (second), and the `break` rule (third): `break` and `continue` mean the innermost loop, a `switch`, `match` or `select` is not a loop, and outside any loop both are errors; `branch` in `types/move.go` skips the non-loop regions for both, as it already did for `continue`, and `hasBreak` looks through a `switch` or `select` so that a loop with such a break is known to end. The backend needs nothing for it today: `match` is written as a chain of `if`s, and `switch` and `select` are not written yet; 3.3 must write `select` without a C `switch` around the cases, or a C `break` would stop at it.

**Progress:** first slice 2026-10-20, `types/task.go`. A closure written in a `spawn` moves every variable it mentions, at the `spawn` (`spawnCaptures`: a capture the closure reads or changes becomes a move, a `Copy` value is copied in as for any closure, a value the closure already moved stays moved), so the function cannot use them afterwards; each capture, each argument (`spawnArg`) and a receiver taken by value must be `Send`, and the message carries the reason: `cannot spawn with r.Clone(): rc.Rc<int> stays in its task`. The reason names the exported type the program sees, never `rc`'s cell inside it; a first version named the cell, and the test that let it through was tightened to the exact text. A channel's element type must be `Send` where the type is written, so `make(chan rc.Rc<int>)` is refused once rather than at each send: `storable` asks `sendable` after its borrow check, deferred the same way while the declarations resolve. `close` is a builtin, refused on a receive-only channel; `for range ch` yields the value owned, and a send-only channel cannot be ranged. Found on the way: a program could not declare a type named `Result`, which example 02 does, since the built-in shadowed it; a declaration now shadows the built-in as any declaration shadows a predeclared name. Examples 02 (brought to the design: `Push` for `append`), 13 and 18 check; 12 checks but for `sync` and `time.After`, after a rename in its `select`, where `case t := <-tasks:` followed by `t := t ?? return` redeclared `t` in one block. Four negative programs under `examples/core/errors/`, and `TestTaskBoundaries` over the cases around them: a spawn in a loop, in a closure (which then owns what the spawn moved, as `move` tells the closures around it), of a closure variable, of a receiver, of a trait value and of a type parameter, a channel of each, `close` and `range` by direction. Second slice 2026-10-20, a decision the owner made on reading example 12: a struct exists only by declaration. `chan struct{}` and `struct{}{}` were an idiom of another language carried in by the examples, and the compiler had half-followed them (a struct type in type position parsed and checked, but its fields could not be read and the backend refused it); the parser now refuses `struct` in a type with a message naming the rule, and a value that carries nothing is `()`, the unit the language already had: `chan ()`, `done <- ()`. Examples 02 and 12 say so; `01-language-design.md` lists anonymous structs among what Teak leaves out, and the conventions record the decision. Left for the third slice: the `break`-in-`select` rule, in the checker and the syntax conventions.

**Dependencies:** 3.1
**Files:** `types/task.go`, `types/universe.go`, tests
**Scope:** M

## Task 3.3: Tasks and channels in the runtime

**Description:** The design is `../design/06-tasks-and-channels.md`: the interface the compiler emits, the thread-per-task runtime of this stage, the scheduler of stage 4 that replaces it behind the same interface, and the decisions that hold for both. The C runtime gains tasks and channels: `tk_spawn` starts a task on a thread of its own, a channel is a queue under a lock with a condition variable, unbuffered or buffered, with `close`; `select` locks the channels it names in address order, the discipline that stays when the scheduler changes, and a `default` case does not wait; its cases are not written as a C `switch`, so that a `break` in one reaches the loop around it, as the language says. The emitter writes `spawn`, send, receive, `for range ch` and `select`; a spawned closure's environment leaves the frame that wrote it, so its moved captures are copied into a task-owned environment freed when the task ends. The debug allocator is locked. When `main` returns the program ends, as the design decides, and the leak report counts only what the main task owns. A value received from a channel is owned and dropped by the receiver; a value left in a channel when the program ends is not dropped, as the channel itself lives as long as the program.

**Acceptance criteria:**
- [x] Examples 02, 13 and 18 run with their expected output (2026-10-20: `examples/expected/pipeline.out`, `13-actor.out`, `18-iterators.out`)
- [x] A run program under `examples/run/` exercises unbuffered and buffered channels, `close`, `select` with and without `default`, and a task that outlives its spawner's frame (2026-10-20: `tasks.teak` and `select.teak`; example 02's `produce` returns while its task runs)
- [x] `teak build -debug` over those programs reports no leak (2026-10-20: the suite runs every one as a debug build; 13's actor is still ending when `main` returns, so its report is skipped with the note, as decided)

**Verification:**
- [x] Tests pass: `go test ./emit/c/ ./examples/` (2026-10-20; `go test ./...` too)

**Done:** 2026-10-20, three slices: channels and the spawn of a plain call, the spawned closure, and `select`. The third: `tk_select` in the runtime takes an array of cases, locks their channels in address order, chooses at random among the cases that can proceed, performs the chosen one with the send and receive bodies the plain operations share, and otherwise registers the task on every channel named, one link per channel in the frame of the wait, and waits on its own signal; a send on an unbuffered channel counts as ready only while a receiver waits on it, which each channel counts, so a select never commits to a send no one will take. The emitter (`selectStmt`) evaluates every channel and sent value on entry, dispatches on the chosen index as a chain of ifs so that a `break` reaches the loop, drops the value of a send case not chosen, and gives a receive case its `Option` by pre-evaluating the receive for the case's own statement, as `defer` pre-evaluates its call, so a binding, an assignment or a bare receive are written by the ordinary paths. `examples/run/select.teak` covers the cases; example 12 now waits only on `sync` and `time.After`.

**Progress:** first slice 2026-10-20, channels and the spawn of a plain call. The runtime (`teak.c`) has `tk_spawn`, a detached thread with a 256 KB stack and a count of running tasks, and `tk_chan`, a locked ring buffer or hand-off slot with a list of waiting tasks; a waiting task has a wake signal of its own, thread-local, and every send, receive and close wakes the list, so a task can later wait on several channels at once. The guard stack is per thread; the debug allocator is under one lock; `tk_finish` in a debug build gives tasks up to 200 ms to end, then skips the block report when some still run, with a note on standard error. The emitter writes `make`, send, receive as an `Option` built from `tk_chan_recv`, `close`, `len` and `cap`, `for range ch` (`rangeChan`, the value owned by the loop variable or dropped at once), and `spawn` of a function, a library function, a generic instance or a method with its receiver by value (`emit/c/task.go`: a struct of the moved arguments and a trampoline per statement). A channel handle is `tk_chan *` in C. `examples/run/tasks.teak` covers the operations, with a `Drop` type crossing a channel and every task ending before `main`; example 13 runs and has its expected output. `TestTaskPanics` pins the misuses: a second close, a send on a closed channel, a channel closed while a sender waits on it, which panics in the sender rather than leaving it waiting forever, and a panic in a spawned task, which ends the program with its message and status 2. Second slice 2026-10-20, a closure as the spawned callee: it is lifted to a function whose first parameters are the variables it captures (`spawnClosure`), each a value the task owns since the checker made every capture a move or a copy; a moved capture leaves its variable empty at the spawn, the body is written with the captures as plain locals, and the lifted function drops them when it ends as it drops any parameter; a result the closure returns is dropped by the trampoline. Examples 02 and 18 run and have their expected outputs; 18 was brought to the exit policy, since its last line was printed by a task `main` did not wait for, and now waits on a channel, as the book's sample does. Left: `select`, and 12, which also waits for `sync` and `time.After`.

**Dependencies:** 3.2
**Files:** `emit/c/runtime/teak.h`, `emit/c/runtime/teak.c`, `emit/c/task.go`, `emit/c/closure.go`, tests
**Scope:** L

## Task 3.4: Shared-state library

**Description:** `sync` in Teak under `std/sync/`: `Arc<T>` over an atomic count, `Mutex<T>` and `RwLock<T>` whose guards hold a `mut T` or `ref T` and release the lock on drop, `Once`, and `WaitGroup` as a `Copy` handle; over bodiless runtime functions for the atomic count, the lock and the condition variable, and one compiler-lowered unexported function that yields a `mut T` from a `ref`, the one place in the language that happens and the reason a lock exists, as `rc` has `alias` and `leak`. `time.After` and `time.Tick` come with it.

**Acceptance criteria:**
- [x] Touching lock-protected data without the guard is a compile error (2026-10-20: the value is reached only through a guard, and a guard is a borrow; `examples/core/errors/guard-across-spawn.teak` and `guard-of-temporary.teak`)
- [x] A run program shares a counter and a map across tasks through `Arc<Mutex<T>>` and `Arc<RwLock<T>>` (2026-10-20: `examples/run/sync.teak`, release and debug builds, no leak)
- [x] Example 12 runs with its expected output (2026-10-20: `examples/expected/12-worker-pool.out`)

**Verification:**
- [x] Tests pass: `go test ./std/ ./examples/` (2026-10-20; `go test ./...` too)

**Done:** 2026-10-20, two slices. The second: the runtime (`teak.c`) gains one lock object for `Mutex`, `RwLock` and `WaitGroup`, numbered in a table under a lock of its own, with number 0 never handed out so that a zero `WaitGroup` panics as `sync: use of an empty WaitGroup`; a writer waits for every holder, a reader for a writer, the wait group's count wakes the waiters at zero and panics below it, and the atomic add is one builtin. The three wait helpers the channels had now take the list and its mutex, so a lock waits exactly as a channel does and the channels are unchanged. The emitter lowers `sync.alias`, `sync.leak` and `sync.inside` beside `rc`'s two, the last as the same word in C. `examples/run/sync.teak` runs the counter, the map, a `Once` asked by three tasks, a `Drop` value dropped once by the last handle, and a `time.After` deadline, leak-free in a debug build; example 12 runs with four workers, a `WaitGroup` that waits for them to stop and a `break` leaving the loop from inside the `select`, deterministic by waiting for the workers rather than printing from them. Its one-second `time.After` is still sleeping when `main` returns, so its debug build skips the leak report with the note, as the design decides; the suite compares the output and the exit status, so the example passes, and the leak check of a program with a pending timer is a stage 4 matter, when a timer is an entry in a wheel and not a task. The review of the slice found an emitter gap older than the stage: a generic struct reached only through a pointer in a method body, as the box of an empty `Mutex<int>` is in `Lock`, was declared in C but never defined, since nothing in the program built one; a field access now defines the struct it reaches (`expr.go`), and `TestTaskPanics` pins the empty `Mutex`, `WaitGroup` and `rc.Rc` handles with their messages, `TestTimers` three ticks of `time.Tick`, and `examples/run/sync.teak` a hundred locks made and dropped in turn and a hundred held at once.

**Progress:** first slice 2026-10-20, the library and its checking. `std/sync/sync.teak` is written as `../design/05-standard-library.md` decides it (decision 5): `Arc<T>` over a cell with an atomic count, `Mutex<T>` and `RwLock<T>` over a heap box holding the value and the number of a runtime lock, since a `ref` is a copy of the value in C and a lock reached through one must still change the one value; `Guard<T>` and `ReadGuard<T>` as borrow-holding structs whose `Drop` releases the lock and whose `Get` lends the value; `Once` as a `Mutex<bool>`; `WaitGroup` as a `Copy` handle. The step from a `ref` of the box's pointer to a `mut` of the box is `inside`, the lowered function the task describes, beside `alias` and `leak` as `rc` has them. The checker accepts the package and a program using every type (`go test ./std/`, `teak check`), and refuses the three misuses: a guard captured by a spawn (`mut int is a borrow`), an `Arc<rc.Rc<int>>` spawned (`stays in its task`), and `n := m.Lock().Get()` (`cannot keep a borrow of a temporary`). Checking it found a gap in 3.2: a spawned closure that held a borrow of a capture across two statements was refused, since the body is checked before the spawn moves the capture and its borrows looked live at the move; the move now takes effect after the closure, where the body's borrows are over and the function's own still count (`spawnCaptures`, two cases in `task_test.go`). `time.After` and `time.Tick` are written in Teak as a task that sleeps and sends; `../design/06-tasks-and-channels.md` gains the lock section the runtime will follow. Left: the nine runtime functions over the channel's waiter lists, the three lowered calls in the emitter, the run program, example 12, and the book.

**Dependencies:** 3.3
**Files:** `std/sync/sync.teak`, `std/time/time.teak`, runtime functions, tests
**Scope:** M

## Task 3.5: Concurrency suites and the book

**Description:** The positive programs (02, 12, 13, 18 and the run programs) and the negative race programs are in the test run; chapter 10 of the book is written with its Appendix B row; the design note records what the stage decided: no `Sync`, `break` in `select`, the exit policy, the marker type. Example 03 stays pending until a `net/http` exists.

**Acceptance criteria:**
- [x] Positive and negative concurrency suites in the test run (2026-10-20: examples 02, 12, 13 and 18 in `examples/suite_test.go`, the run programs `tasks`, `select` and `sync` in `emit/c`'s suite, and eleven race programs under `examples/core/errors/` in `TestNegativePrograms`)

**Verification:**
- [x] Tests pass: `go test ./...` (2026-10-20)

**Done:** 2026-10-20. The positive suites were in the run since their tasks; what the task added is the three race programs the checkpoint names and the stage had not written, a value used after it was sent (`use-after-send`), an `Arc`'s value changed (`mutate-through-arc`, refused by the borrow rules as a `mut` of a `ref`), and a closure bound to a name and spawned (`borrowing-closure-spawned`), beside the eight the earlier tasks wrote. `03-ownership-across-tasks.md` gains the record of what the stage decided, `break` in `select`, the exit policy, the signal channel, beside the two it already carried, and the list of the race programs; chapter 10 of the book is marked implemented with its sample from the `sync` run program, Appendix B gains the rows for the locks' constructors, the guard and the `WaitGroup` handle, and its chapter 10 row no longer names a task. Example 03 stays pending until a `net/http` exists.

**Dependencies:** 3.4
**Files:** tests, `../book/10-concurrency.md`, `../design/03-ownership-across-tasks.md`
**Scope:** S

## Checkpoint: stage 3

- [x] Race programs rejected; concurrency examples run (2026-10-20: eleven negative programs, examples 02, 12, 13 and 18, three run programs)
- [x] The book chapters this stage owns (see `../book/README.md`) are written and their status lines updated (2026-10-20: chapter 10 implemented, Appendix B's chapter 10 rows)
- [ ] Reviewed by the owner
