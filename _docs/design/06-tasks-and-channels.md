# Tasks and channels: the runtime

Status: decided, 2026-10-20, before the first line of task 3.3. This note says how a Teak task runs and how a channel works underneath the rules of `03-ownership-across-tasks.md`, in two steps: the runtime stage 3 builds, and the scheduler stage 4 replaces it with. The compiler is written against an interface that both implement, so the step from one to the other changes nothing the compiler emits.

## The problem

C has no model of a task. It has the system's threads, locks and condition variables, and nothing that lets many tasks share one thread. A language whose concurrency is "start a task, talk through channels" needs more than that: tasks must be cheap enough to start by the thousand, a task that waits must not cost a thread, and a channel must hand a value from one task to another without either one reaching the other's memory.

None of that is a property of C. It is a property of a runtime written in C, as it is a property of a runtime written in any language: a scheduler is a program that keeps a list of tasks and decides which one the thread runs next. Teak's runtime gets that program in two steps, and the first is deliberately the simplest one that is correct.

## The interface

Every concurrent construct the compiler emits lowers to one of these calls and nothing else. The emitted code never names a thread, a lock or a condition variable.

| Teak | Runtime call | Meaning |
|---|---|---|
| `spawn f(x, y)` | `tk_spawn(run, args)` | starts a task; `run` is a trampoline the compiler writes for the call site, `args` the arguments moved into a block the task owns and frees |
| `make(chan T)`, `make(chan T, n)` | `tk_chan_make(sizeof T, n)` | a new channel; `n` is 0 for an unbuffered one |
| `ch <- v` | `tk_chan_send(ch, &v)` | moves `v` into the channel; blocks until a receiver takes it or there is room; panics on a closed channel |
| `<-ch` | `tk_chan_recv(ch, &out)` | moves the next value out, into memory the receiving task owns; false once the channel is closed and empty, which the compiler turns into `None` |
| `close(ch)` | `tk_chan_close(ch)` | no more sends; panics when closed twice |
| `len(ch)`, `cap(ch)` | `tk_chan_len`, `tk_chan_cap` | values waiting, room for values |
| `select { ... }` | `tk_select(cases, n, has_default)` | waits for the first case that can proceed and returns its index; with `default`, returns at once when none can |
| `for v := range ch` | `tk_chan_recv` in a loop | until the channel is closed and empty |

A channel handle is a pointer to the channel, which is why it is `Copy` and why any number of tasks may hold one. A value sent is copied bit for bit into the channel and the sender's place is emptied, as any move empties its source; a value received is copied out the same way. The copy is of the value's bytes, not of what it owns: a slice sent on a channel moves its pointer, length and capacity, and the receiver owns the elements from then on. This is what makes "sending moves, receiving owns" true at run time with no bookkeeping at all.

## Step one: a thread per task (stage 3)

`tk_spawn` starts a system thread, detached, with a stack of 256 KB rather than the system's default of several megabytes, so that thousands of tasks fit in memory. The task runs the trampoline, which calls the function with the moved arguments, frees the argument block, and ends.

A channel is a struct holding a mutex, a ring buffer of `cap` elements of `elem` bytes, a `closed` flag, for an unbuffered channel a one-element hand-off slot, and a list of the tasks waiting on it. A task that must wait, because the buffer is full or empty or the hand-off slot is taken, has one wake signal of its own, a condition variable in this step, puts itself on the channel's list, releases the channel and waits on its signal; every send, receive and close on the channel wakes the tasks on the list, and each one wakes, takes the channel's lock again and rechecks. A send on an unbuffered channel puts the value in the slot and waits until a receiver has taken it, so the sender does not continue before the hand-off, which is what makes an unbuffered channel a synchronization point and not a buffer of one; a channel closed while a sender waits on it panics in that sender, since no receiver need ever come and the alternative is a task that waits forever. A send on a closed channel, and a second close, panic too. A thread can wait on one condition at a time, which is why the signal belongs to the task and not to the channel: it is the one shape that lets a task wait on several channels at once.

`select` is that same wait over several channels. It locks the channels it names in increasing address order, so that two selects over the same channels cannot deadlock, and looks for a case that can proceed: a receive when a value waits or the channel is closed, a send when there is room, which on an unbuffered channel means a receiver is waiting on it, since a select must not commit to a send that no one will take; every channel counts the tasks waiting to receive from it for that. The count is a promise only of a receiver waiting at that moment: when that receiver is itself a select that wakes and finds another of its cases ready too, it may take that one, and the sender then waits for the next receiver. Step two's wait lists hand the value to the receiver directly and close that gap. If none can, it puts the task on the list of every channel named, releases them all, waits on the task's signal, and tries again from the start. A plain send or receive is a select of one case. When several cases can proceed it picks one at random, since always taking the first in source order would let a busy channel starve the others; a `select` is the one place in the language where the program does not say which of two ready things happens first, and the runtime must not make that choice systematically. With a `default` it returns without waiting.

What this step costs: a task is a thread, so starting one is a system call and switching between two is a kernel context switch, microseconds rather than nanoseconds; the number of tasks is bounded by the system's thread limit, tens of thousands, not millions; and a task that blocks in a system call, reading a file or a socket, blocks a thread. For the twenty example programs and for the race tests none of this matters, and the implementation is short enough to be obviously right, which is what the stage 4 scheduler is then checked against.

## Step two: many tasks on few threads (stage 4)

The design document decided this in its runtime section: an M:N work-stealing scheduler, blocking-looking I/O over kqueue, epoll or io_uring, and no async/await. In the terms of this note:

- **A task is a coroutine, not a thread.** It owns a stack, reserved with `mmap` and committed by the system lazily as it is touched, so a reservation of a megabyte costs nothing until used, and a saved set of callee-saved registers. Switching from one task to another saves those registers and the stack pointer and restores the next task's: some twenty instructions of assembly per architecture, with no system call. Stacks come from a pool and go back to it when a task ends.
- **N worker threads run M tasks.** One worker per core, each with a queue of runnable tasks; a worker whose queue is empty steals from another's. This is the shape every runtime of this kind has converged on.
- **A channel parks a task instead of blocking a thread.** The channel keeps its lock and its list of waiting tasks from step one; what changes is the task's wake signal. A receive on an empty channel puts the current task on the list and switches to the next runnable task instead of waiting on a condition; the send that later arrives takes the waiter off the list and puts it back on a run queue instead of signalling. `select` registers the task on every channel it names, as it did, and is woken by the first.
- **I/O parks the same way.** A read that would block registers the descriptor with the poller and parks the task; the worker runs others; the poller's event puts the task back. The program is written as blocking code and runs as event-driven code, which is the promise behind leaving async/await out.
- **Timers** are a wheel the poller consults, which is what `time.After` and `time.Tick` sit on; in step one they are a task that sleeps and sends, so a timer not yet due when `main` returns is a task still running, and a debug build skips its leak report for the program, with the note, as it does for any running task.

Two properties of the language make this scheduler simpler than its counterparts elsewhere. The compiler knows the layout of every frame and the extent of every borrow, so a task's stack holds nothing the runtime has to find or scan, as a collector would have to. And the `Send` rule means that whatever a task owns is reachable from no other task, so the scheduler may run a task on any thread and move it between threads freely, without a memory model question at every step.

## Locks and counts

The `sync` package (`05-standard-library.md`, decision 5) sits on one more runtime object, which the same two steps implement: a lock, with a count and a list of waiting tasks. The library reaches it through nine functions it declares without a body, and nothing else in the program does.

| `sync` | Runtime call | Meaning |
|---|---|---|
| `NewMutex`, `NewRwLock`, `NewWaitGroup` | `tk_sync_newLock() int64` | a new lock, numbered; the number is what the library holds |
| `Mutex.Drop`, `RwLock.Drop` | `tk_sync_freeLock(h)` | the lock's number is free again; a `WaitGroup` never frees its lock, since its handle is `Copy` |
| `Lock`, `Write` | `tk_sync_lock(h)`, `tk_sync_unlock(h)` | held by one task, which waits for every holder before it |
| `Read` | `tk_sync_lockShared(h)`, `tk_sync_unlockShared(h)` | held by any number of readers at once, who wait for a writer; a writer waits for them |
| `WaitGroup.Add`, `Done` | `tk_sync_count(h, n)` | the count changes under the lock; reaching zero wakes the waiters, going below it panics |
| `WaitGroup.Wait` | `tk_sync_wait(h)` | until the count is zero |
| `Arc.Clone`, `Arc.Drop` | `tk_sync_atomicAdd(n, d)` | one atomic add on the cell's count, no lock |

The runtime keeps the locks in a table it grows, under a lock of its own, and hands out indices, so that the Teak side holds an `int` as it holds a file descriptor and the structure of a lock stays the runtime's; index zero is never handed out, so an empty handle is caught. Each lock is a mutex of the system's, a count of readers, a flag for the writer, the wait group's count, and the list of waiting tasks of the channels' kind: a task that must wait, because the lock is held or the count is not yet zero, links itself on the list, releases the lock and waits on its own signal, and every release, and every change of the count, wakes the list. That is the channel's wait exactly, which is the point: step two parks a task on a lock as it parks one on a channel, by changing what the signal is and nothing else. A writer takes the lock when no reader and no writer holds it, a reader when no writer does; readers are not counted ahead of a waiting writer, so a steady stream of readers can keep a writer waiting, which is the simplest rule and the one the examples are indifferent to. A lock is not recursive: a task that locks what it holds waits forever, as in Go.

## Decisions that hold for both steps

- **A channel lives as long as the program.** Its handle is `Copy`, copied wherever a value is copied, so no count of handles exists and no last handle can free the channel. A program that makes a channel per request keeps each until it exits. This is the cost of `Copy` handles, and the design chose them for the fan-out and fan-in they make trivial; a bounded form, a channel pool or a channel that frees itself once closed and empty with no task waiting, is a stage 4 question, recorded here so it is not forgotten. Channels are allocated outside the debug build's block count, so a program is not reported for the channels it made.
- **The program ends when `main` returns**, whatever other tasks are doing; they stop with it. A program that must wait for its tasks says so, with a channel or a `sync.WaitGroup`. In a debug build the leak report counts blocks, and a task still running at the end may own some; the report is therefore skipped, with a note on standard error naming the number of tasks still running, since a block owned by a running task is not a leak. A task that has sent its last word is often still ending when `main` returns, so the debug build waits up to 200 ms for the count to reach zero before deciding.
- **A panic in any task ends the program**, as it does in the main task: the message is printed, the guarded values of the panicking task are dropped innermost first, and the program exits with status 2. The other tasks' values are not dropped; they are on other threads, in the middle of whatever they were doing, and cannot be unwound from here. The guard stack is therefore per thread.
- **A closure given to `spawn` owns its environment.** For every other closure the environment lives in the frame of the function that wrote it, since such a closure is a borrow; a spawned closure outlives that frame, so the compiler copies the moved captures into a block the task owns and frees when it ends, beside the arguments.
- **The debug allocator is locked.** Its block list, counts and quarantine are shared by every task; one mutex covers them. The release allocator is the system's, which is already safe.
- **The stack of a task in step one is 256 KB.** A recursion deeper than that ends the program with the system's stack fault rather than a Teak message; the lazily committed reservations of step two remove the limit for practical purposes.

## What it costs

- Step one makes a task expensive, as above; the examples do not notice, a server would.
- `select` locking every channel it names serializes selects over shared channels; with few channels per select, which is every real use, this is a handful of uncontended locks.
- A value is copied twice on its way through a channel, in and out of the buffer; a large value is sent behind a pointer, `*T`, which is `Send` whenever `T` is and costs one word.

## What it gains

- The compiler is done with concurrency once it emits the interface; the scheduler is runtime work alone, checked against the same example programs.
- The two steps share the channel structure, its list of waiting tasks, the lock discipline of `select`, and every decision above, so step two is a change of how a task waits, not of what a channel is.
- Programs are written once, as blocking code, and get the scheduler's speed when it arrives without a change.

## Precedent

Go's runtime is the closest ancestor: goroutines on an M:N scheduler with work stealing, channels with wait queues, a netpoller for I/O, all written in Go and assembly over the system's threads. Step two is that design without the garbage collector's stack scanning, which `Send` and the compiler's knowledge of frames make unnecessary. Erlang's BEAM and Pony run one scheduler per core over mailboxes, the actor shape of example 13 made the whole model; Java's virtual threads and Tokio's executor are the same scheduler shape under different surfaces. Step one is how every one of these began: Go 1.0 ran on a scheduler with a single global lock, and shipped.
