# 33. Cancellation and deadlines

The design is "Cancellation and deadlines" in `../design/06-tasks-and-channels.md`, and the library's side decided 17 of `../design/05-standard-library.md`. The code is `std/task/task.snello`, `runtime/c/scope.c` and the waits of `chan.c`, `timer.c`, `net.c` and `lookup.c`.

A program that talks to the world waits: for a connection, for bytes, for a value on a channel, for a timer. Sooner or later it wants a wait to end for a reason of its own, the request has taken too long, the server is shutting down, another task has failed, and the question is how that reason reaches the wait. Go answers it with `context.Context`, a value handed by hand to every function that may block, and the chapter's last section maps it. Snello attaches the answer to the task: a deadline or a cancel reaches every call the task makes that waits, and every task it starts, with nothing passed and no signature changed.

## A deadline for a function's work

`task.Within(d, body)` runs `body` with a deadline `d` from now. Every call `body` makes that waits, and every task it starts, is under that deadline, whoever wrote the function being called; when the deadline passes, each wait that is open ends, and each call that would wait after it stops at once. `Within` returns `Some` with what `body` returned, or `None` when the body was cut short:

```go
page := task.Within(2 * time.Second, func() Result<Page, io.Error> {
	c := net.Dial(host, 443)?       // the dial, the write and the read share the two seconds
	c.WriteString(request)?
	return readPage(mut c)          // readPage was written with no word of deadlines
}) ?? Err(io.NewError("get", host, "timed out", io.TimedOut))
```

A value the body returns after it was cut short is dropped, and `None` comes back instead: a total summed from a channel the deadline ended early is never taken for the whole one. A body that wants the partial value anyway keeps it where it can reach it, in a variable outside the closure.

A deadline only narrows. `task.Within(10 * time.Second, ...)` inside a body with one second left still has one second, and no call widens a deadline it inherited. A `d` of zero or less has passed already: the body runs, every wait in it stops at once, and its value is dropped.

## A group cancelled

`task.Group`, chapter 28's, is a scope: the tasks added to it are cancelled together, by `g.Cancel()`, or by the first member to panic, so that the others stop. A member added to a cancelled group starts cancelled, and a task a member starts is in the group too, whenever it was started: a cancel reaches the whole tree of work under the group.

```go
g := task.NewGroup()
g.Add(spawn serve(listener))
g.Add(spawn flush(queue.Clone()))
_ = <-shutdown
g.Cancel()                              // every wait in serve and flush ends
match g.Wait() {
	Ok(_)  => {},
	Err(p) => fmt.Println("a task failed:", p.Message()),
}
```

A function's work is under the scope it runs in: a group made inside a `Within` is under its deadline, and a `Within` inside a member of a group is cancelled with the group. The main task is in no scope, and nothing cancels it.

## What a cancelled call does

A cancel lands where a task waits, and each call says it in the form it already has for "nothing more will come", so that no call gains a parameter or a second result type:

| The call | Cancelled while it waits, or called once cancelled |
|---|---|
| A library call returning `Result`: `Read`, `Write`, `Accept`, `Dial`, `LookupHost`, and what is built on them, `io.Copy`, `bufio` | `Err` of an `io.Error` of Kind `io.Cancelled`, or of Kind `io.TimedOut` when a deadline passed, the Kind a socket's own deadline gives; a `Write` reports the bytes it had written |
| A receive, `<-ch`, `for range ch` | `None`, as from a channel that has ended: the loop ends |
| A send, `ch <- v` | drops the value and goes on, as a send with no receiver left does |
| `select` | one case is taken as if its channel had ended: a receive case with `None`, a send case dropping its value; a `select` with `default` does not wait and is not affected |
| `time.Sleep` | returns at once |
| `h.Join()`, `g.Wait()`, `wg.Wait()`, a lock's `Lock` | not affected, below |

A call that need not wait does not look: a send to a channel with room, a receive of a value already there, a `Read` of bytes the system has buffered. A loop that drains a full channel drains it.

**`task.Cancelled()`** reports whether the running task's work is cancelled, by a `Cancel`, a panic in its group, or a deadline passed. A task whose receive gave `None` asks it to tell a cancel from the end of its channel, and a task that computes without waiting asks it between rounds, since a cancel lands only where a task waits:

```go
for r := range requests {           // None once cancelled: the loop ends
	serve(r)
}

if task.Cancelled() {
	fmt.Println("server stopping")
}
```

There is no channel to select on for the cancel: every wait already wakes, so a `select` needs no case for it.

## What is waited for, and what is never forced

`Join`, a group's `Wait` and a `WaitGroup`'s `Wait` are not cancelled. They wait for tasks that are cancelled with the waiter, since a task inherits its scope, and return once those tasks have stopped, with their results: returning sooner would leave tasks running with no one to wait for them. A lock is held for a few instructions and is not a place to stop.

A cancel is asked for and never forced. There is no kill, as a handle has none: a task stopped at an arbitrary instruction would leave a lock's value half changed and its owned values undropped. A task that never waits and never asks `task.Cancelled()` runs to its end, and what waits for it waits that long; `Within` returns `None` when its body returns, and the deadlock report still names a task that waits on nothing that can come.

A task that must outlive its scope is not started in it: the work is sent on a channel to a task started outside, as a server sends a log line to its writer.

## Go's `context`, mapped

| In Go | In Snello |
|---|---|
| `ctx context.Context` as the first parameter | nothing: the task carries it |
| `context.WithTimeout(ctx, d)`, `defer cancel()` | `task.Within(d, func() T { ... })` |
| `context.WithDeadline(ctx, t)` | `task.Within(t.Sub(time.Now()), ...)` |
| `context.WithCancel(ctx)` and `cancel()` | a `task.Group` and `g.Cancel()` |
| `errgroup.WithContext` | a `task.Group`: the first panic cancels the others |
| `select { case v := <-ch: ...; case <-ctx.Done(): ... }` | `<-ch`, which gives `None` once cancelled |
| `ctx.Err()`, `context.Canceled`, `context.DeadlineExceeded` | `task.Cancelled()`, `io.Cancelled`, `io.TimedOut` |
| `DialContext`, `QueryContext`, `CommandContext` | `Dial`, and every call as it is |
| `context.AfterFunc(ctx, func() { conn.Close() })` | nothing: the `Read` wakes |
| `ctx.Value(key)` | a parameter |
| `context.WithoutCancel(ctx)` | a task started outside the scope, given the work on a channel |

What a port loses is the one thing the token does well: a signature that says a function can be cancelled. In Snello the rule says it instead, every call that waits, and the port gains what the token loses silently, the helper that was never handed the context and sleeps its full second under a 200 ms deadline.

## For maintainers

- **A scope** is a record of the runtime's, `sn_scope` in `runtime/c/internal.h`, made by `scope.c`: the scope it was made under, which it holds, and for a task's own scope the group that took it in; its state, live, cancelled or timed out; the tasks in it and the scopes under it, in lists under its lock; and its deadline, a timer of the wheel in `timer.c`. A task's record holds its scope and its place in the scope's list, `sn_scope_link`; the main task's scope is NULL, and a task in no scope reads that one word at each spawn and each wait and calls nothing more.
- **A spawn** puts the task in its spawner's scope, `sn_scope_inherit`: one atomic add on the scope's count and no lock. The task goes in the scope's list of tasks, which a cancel wakes, only at its first wait a cancel may end, `sn_cancel_wait_state`, under the scope's lock and before it reads the scope's state; a task that never waits needs no wake. A spawn for a handle, `sn_spawn_handle`, makes no scope until one is needed: the handle's channel, made by `sn_chan_handle` with both handles counted so that the spawn clones nothing, has a word that holds the task tagged `running` until the task first starts a task, enters a scope or makes a group, each of which makes the task's own scope with one compare-and-swap on the word, or until a group takes the handle; a task that ends with none leaves `ended` there, which a late `Add` reads. A task a group takes before it has started anything goes into the group's scope itself, put in its list by the `Add`, which wakes it when the group is cancelled already.
- **A cancel**, `scope_cancel`, marks the scope with one compare-and-swap, so that one cancel walks into it, and under its lock wakes each task in it with `sn_wake` and marks and holds each scope under it, then walks into those once it has let the lock go: a loop through `pending`, never a recursion, since a recursion on a task's path makes the stack analysis of chapter 30 leave every task unbounded, and so on a large guarded stack. The first version was recursive, and every task of every program paid for it, a tenth of a wave of sleeps on Linux; `backendtest.Bounded` holds the test programs' spawns to a bound, which is how such a change fails. A release of a scope's last hold, `sn_scope_release`, is a loop for the same reason, over the scopes it held through the freed record's own links. A walk that reaches a scope whose count is already zero marks it and does not hold it (`retain_live`): nothing is in it, and a hold taken then would free it twice. A deadline's timer holds its scope and fires with the wheel's lock let go, `sn_deadline_arm_counted`, since the walk takes locks of its own; the `Within` that leaves takes the timer out, `sn_deadline_disarm` saying whether it did, and lets the timer's hold go only then.
- **A wait takes itself back.** A cancel's wake is the scheduler's plain wake, kept by a task that is running or about to park for its next park. A wait that a cancel may end, woken before its waker came, looks at its task's scope, `sn_cancel_wait_state`, which first puts the task in the scope's list when it is not yet, and finding it cancelled takes the wait back with the very claim its waker would make, so the two cannot both win: a channel's wait, plain or a `select`'s, with the compare-and-swap of the wait state an operation claims it with, `sn_await_cancellable`, then takes its waiters off their queues; a sleep by taking its timer out of its wheel under the wheel's lock, `sleep_cut`, unless the firing took it first; a socket's wait by the compare-and-swap that takes the task out of the socket's word, as after any park, with `sn_net_cancelled` saying which code; a name lookup by taking itself out of the lookup's word, leaving the answer to the resolver's thread. The loads are relaxed: the cancel's wake and the park's compare-and-swap order the mark before the look. Every cancelled path is a `noinline, cold` function, so that the waits compile as they did; inlined by the link-time optimizer into a channel's receive, they made every receive save more registers, and a pipeline of buffered channels ran a quarter slower.
- **A handle's receive is never cancelled**, by the channel's `handle` flag, which `wait_on` in `chan.c` reads: `Join` and `Wait` keep their results.
- **A member's panic cancels its group** from the task's entry, which both backends write: as `sn_catch` returns, the entry calls `sn_handle_failed`, which marks the handle's channel failed and cancels the group the channel's word names; a group that takes the handle after reads the mark and does the same, each writing before it reads. The group's scope records the first handle to fail, which `Wait` asks for through `firstFailed`.
- **The library** is `std/task`: `Within` enters and leaves a scope around its body through `enter` and `leave`, and asks `Cancelled` before it keeps the value; `Group` holds its scope's handle and its members' handles, `Add` is `adopt`, `Cancel` is `cancelScope`, and `Wait` joins the members in turn. `net` maps the runtime's `cancelled` code to `io.Cancelled` and a deadline's to `io.TimedOut` in `stopError`; `io.Kind` gained `Cancelled`.
- **What it costs**, measured against main on the 8-core arm64 Mac and in the arm64 Linux image, no row behind: a spawn 160 ns against 157, a spawn for a handle 164 against 188, the switch 79 against 78, a pipeline of buffered channels and a wave of 512 sleeps level, a group of ten thousand members 342 ns a member against 391, and a socket round trip level. The group is behind Go's 159 for a goroutine counted by a `WaitGroup`, a lighter construct with no result channel, as it was before. The design note has the rows for both systems.
- **Tests**: `std/task/task_test.snello` for every wait a cancel ends, inheritance, narrowing and `Within`, a body returning as its deadline fires, a panic inside `Within`, and a cancel while a member enters and leaves scopes; `std/net/net_test.snello` for a `Read` under `Within` and cancelled, and an `Accept` cancelled; `compiler/emit/internal/backendtest/testdata/cancel.snello` and `group.snello` on both backends, in release and debug, each spawn held to a small stack; `TestCancelRaces` in `runtime/runtime_test.go`, twenty thousand rounds of a cancel against a sender for one wait and of an `Add` against a member ending, and `TestScopeDeadlineRaces`, thousands of rounds of a deadline firing as its `Within` leaves, an inner scope left as the outer's deadline cancels both, and a group cancelled while its member enters and leaves scopes.
