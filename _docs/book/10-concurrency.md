# 10. Concurrency: `spawn`, channels, `select`

Tasks run on one worker thread per core, and a task that waits on a channel, a lock or a socket parks instead of holding a thread. `compiler/emit/internal/backendtest/testdata/tasks.snello`, `select.snello` and `sync.snello` and the pipeline, worker-pool, actor and iterators examples run what this chapter describes, and the race programs under `compiler/types/testdata/errors/` are rejected. The samples are cut from `examples/pipeline/pipeline.snello`, `examples/actor/actor.snello`, `examples/iterators/iterators.snello` and `compiler/emit/internal/backendtest/testdata/sync.snello`.

A task is a function running on its own, started with `spawn`. Tasks talk through channels. That much is familiar. What Snello adds is one rule about the values that pass between tasks, and the compiler enforces it, so that two tasks can never reach one value at the same time: the bug that a race detector hunts at run time cannot be written.

## The rule

A value crossing from one task to another is **moved, immutable, or atomically shared**. Nothing else crosses. There are three crossings:

- `spawn f(x)`, or `spawn func() { ... }()`, for the arguments and the variables the closure mentions;
- `ch <- v`, for the value sent;
- `<-ch`, for the value received.

Each is a move, as chapter 5 describes a move. After `ch <- job`, `job` is gone from the sender; the receiver that does `j := <-ch` owns `j` and drops it when it goes out of scope, there and then. A closure given to `spawn` owns every variable it mentions, so the function that spawned it cannot use them afterwards. And a borrow, `ref T` or `mut T`, never crosses: it points into a frame of the task that made it, and the new task has frames of its own.

A channel handle is owned too. The channel is one shared object that synchronizes itself; a handle on it is a small value, moved like any other, and `ch.Clone()` gives another handle on the same channel. Each task that sends or receives takes a handle of its own, which is what makes a pipeline a matter of handing a clone of `out` to each worker.

```go
func produce(n int) <-chan Job {
    out := make(chan Job)
    w := out.Clone()                            // rule 3: a handle for the task
    spawn func() {                              // rule 4: `w` is moved into the task
        defer close(w)
        for i := 0; i < n; i++ {
            job := Job{id: i, text: "..."}
            w <- job                         // rule 1: job is moved into the channel
        }
    }()
    return out
}
```

## `Send`: what may cross

A value may cross a task boundary when its type is `Send`. Nobody writes `Send` on a type; the compiler reads it off the type's structure, the way it reads `Copy`:

| Type | `Send` | Because |
|---|---|---|
| Numbers, `bool`, `string`, a channel handle | yes | nothing inside is shared |
| A struct, enum, tuple, option or result | when every field is | what it holds is what crosses |
| A slice, array, map or owned pointer `*T` | when `T` is | an owned container moves with its contents |
| `ref T`, `mut T` | never | tied to a frame of the task that made it |
| A trait used as a type, `Shape` | only if the trait refines `Send`: `trait Job: Send` | the type behind it is not known at the crossing |
| A type parameter `T` | only when bounded by it: `T: Send` | the body is checked once for every `T` |
| `rc.Rc<T>`, `rc.Weak<T>` | never | their count of handles is not atomic |

The last row is the one place structure is not the whole story, and it is settled inside the library rather than by a rule of the compiler: the cell that `rc`'s handles share holds a field of the predeclared type `local`, which holds nothing and is not `Send`, so any type holding it stays in its task. A program cannot write `local`, and cannot list `Send` on a type either; both are read from the fields.

```go
spawn run(Account{balance: initial}, ch.Clone())      // Account is Send: an int and a slice of ints
```

When a value cannot cross, the message names the part that stops it, not the type around it: an `rc.Rc<Node>` inside a struct is reported as `rc.Rc<Node> stays in its task`, a borrow as `ref Node is a borrow`, a trait value as `Shape does not refine Send`. The fix is to say what was meant: clone the value, send a piece of it to each task, or share it through `sync.Arc`, which chapter 10 covers once the library is written.

There is no second property beside `Send`. Nothing in Snello changes a value through a shared `ref` except under a lock, so a value that is safe to send is safe to share, and one word covers both.

## What the compiler checks

A closure written in a `spawn` statement takes every variable it mentions with it. The move happens at the `spawn`, whatever the closure does with the variable inside, so the function cannot use the variable afterwards; a `Copy` value is copied in, as it is for any closure. A channel the closure sends on is a handle like any other, so the task is given a clone of its own, `d` above, and `main` keeps `done`.

```go
names := []string{"a", "b"}
done := make(chan ())
d := done.Clone()
spawn func() { fmt.Println(names); d <- () }()
// fmt.Println(names)             // error: names was moved into the task
<-done
```

Each variable the closure takes, each argument of a spawned call, and a receiver taken by value must be `Send`. The message names the part that is not, as the table above reads it: `cannot spawn with r.Clone(): rc.Rc<int> stays in its task`. A borrow cannot be passed to a spawned function at all: `a spawned task cannot hold a borrow`.

A channel carries only `Send` values, and the compiler says so where the channel type is written rather than at each send: `make(chan rc.Rc<int>)` is refused with `a channel cannot carry rc.Rc<int>: rc.Rc<int> stays in its task`, and `make(chan ref string)` with `a channel cannot carry a borrow`. A send then needs no check beyond the move it is, and a receive yields a `T?` the task owns.

`close(ch)` closes a channel: a receive yields `None` once the values already sent are taken, and a range over the channel ends. A receive-only channel cannot be closed. `for v := range ch` receives until then, and each `v` is owned by the loop body; `for range ch` with no variable drops each value as it arrives.

```go
for r := range merge(workers) {          // `workers` is moved into merge; the range borrows the channel merge returns
    total += r.words
}
```

## `select`: whichever is ready

`select` waits for the first of several channel operations that can proceed and runs that case; with a `default` it does not wait. A receive case binds the `T?` the receive yields, a send case sends, and a bare `<-ch` discards what it receives. When several cases are ready at once, one is chosen at random, so a busy channel cannot starve the others.

```go
select {
case got := <-tasks:                        // got is Task?; the task is moved out of the channel
    t := got ?? return                      // None: channel closed, no more work
    out <- Done{id: t.id, size: len(t.payload)}
case <-cancel.Done():
    return
}
```

A send case goes only when the send would not wait: when the channel has room, or, for an unbuffered channel, when a receiver is already waiting on it. The values of send cases are evaluated on entry, and the value of a case that was not chosen is dropped.

## How it runs

A task is not a thread. The runtime keeps one worker thread per core and runs the tasks on them: `spawn` gives the task a small stack of its own and puts it on a worker's queue, and a task runs until it waits, on a channel, a lock, a timer or a socket, when the worker switches to the next task and whoever the first was waiting for puts it back. Starting a task costs about a microsecond and switching between two about the same, so a program may have a hundred thousand of them waiting at once. A task that computes without ever waiting keeps its worker; the others run on the rest. A value sent on a channel is copied into the channel and the sender's variable is empty from then on; a value received is copied out into memory the receiving task owns, so each value is owned by exactly one task at any moment, which is what the rules above promise. An unbuffered channel hands a value over directly, and the sender continues only once a receiver has taken it; a buffered one holds up to its capacity. Three things to know:

- **The program ends when `main` returns**, whatever the other tasks are doing. A program that needs its tasks to finish waits for them, on a channel as `compiler/emit/internal/backendtest/testdata/tasks.snello` does.
- **A channel is freed by its last handle.** A handle is an owned value: assigning it, passing it or capturing it in a `spawn` moves it, and `ch.Clone()` makes another handle on the same channel, so each task that sends or receives gets its own, `spawn produce(jobs.Clone())`. Sending, receiving, `close`, `len`, `for range` and `select` only borrow the handle, so a loop sends on one handle as often as it likes, and a function in the same task that only uses a channel takes it by `ref`. When the last handle is dropped the channel is freed, with any values still in it. Closing stays explicit: the last handle going does not close a channel, since no one is left to see it. Two handles are equal, `==`, when they are on one channel, as in Go, so a channel can be a map key.
- **A panic in any task ends the program**, with the message, the drops of that task's own values, and exit status 2, as a panic in `main` does.
- **A program that can never go on stops.** When every task waits, the main task among them, and nothing is left that could wake one, no timer and no socket, the program ends with `fatal error: all tasks are asleep - deadlock!` and status 2, as Go's does: a receive no task will ever send to, a `WaitGroup` that never reaches zero, two tasks each waiting for the other. A task asleep in `time.Sleep`, or waiting on a socket, or a pending `time.After`, is something that can still wake one, so such a program waits.

`snello build -debug` checks a concurrent program as it checks any other, and reports the memory still held at the end; when a task is still running at that point the report is skipped, with a note, since what a running task owns is not a leak.

## Sharing state: `sync`

Some state genuinely must be reached from several tasks: a counter, a cache, a cancellation flag. The `sync` package has the types for it, and the compiler checks their use with the rules of chapter 6 and nothing added. The sample is cut from `compiler/emit/internal/backendtest/testdata/sync.snello`.

- **`sync.Arc<T>`** is a handle on a value shared between tasks, as `rc.Rc` is within one: `sync.NewArc(v)` makes the first handle, `Clone` another, `Get` lends the value for reading, and the value is dropped with the last handle. An `Arc<T>` is `Send` when `T` is, so each task gets a clone of its own. An `Arc` never lends its value for changing.
- **`sync.Mutex<T>`** owns a value and lends it for changing to one task at a time. `Lock` waits for the lock and returns a guard; `Get` on the guard lends the `mut T`, and the guard gives the lock back when it is dropped, at the end of its scope.
- **`sync.RwLock<T>`** lends either for reading to any number of tasks, with `Read`, or for changing to one, with `Write`; each returns a guard of its kind. A task waiting to write holds back the readers that come after it, as Go's `RWMutex` does, so readers cannot keep a writer out; a task therefore does not `Read` again a lock it is reading.
- **`sync.Counter`** is an `int` several tasks add to as one step each, `hits.Add(1)`, with `Load`, `Store` and `CompareAndSwap`: one instruction, no lock, nothing parks. **`sync.Atomic<T>`** holds a value of any `Send`, `Clone` and `Eq` type that tasks read and replace as one step each, a flag, a name, a configuration: `Load` gives a copy, `Store` and `Swap` replace it, and `CompareAndSwap` replaces it when it equals the value expected. Both take `ref self`, as a lock does, since what they hold is what makes a change through a `ref` safe.
- **`sync.Once`** runs a function the first time `Do` is called and never again; **`sync.WaitGroup`** counts tasks with `Add` and `Done` and waits for zero with `Wait`. A `WaitGroup` is an owned handle like a channel: a task is given its own with `wg.Clone()` before the `spawn`, and the counter is freed with the last handle.

```go
counter := sync.NewArc(sync.NewMutex(0))
wg := sync.NewWaitGroup()
for i := 0; i < 4; i++ {
	c := counter.Clone()
	w := wg.Clone() // the task's own handle on the group
	wg.Add(1)
	spawn func() {
		g := c.Get().Lock()
		*g.Get() += 1
		w.Done()
	}()
}

wg.Wait()
fmt.Println(*counter.Get().Lock().Get()) // 4
```

The guard is what makes the lock safe to use: it holds a borrow of the value, so it is tied to the lock and the scope that took it. Keeping it in a struct, sending it, capturing it in a `spawn`, or binding the value of a lock the statement drops, `n := m.Lock().Get()`, are each refused by the borrow rules, with the message those rules give; and since the value is reached only through a guard, touching it without the lock cannot be written at all. The lock is released where the guard's scope ends, so a block, or a function of its own, bounds the critical section.

Each of these types is made with its `New` function, and none has a zero value: `var m sync.Mutex<int>` is refused, since a lock with nothing behind it could only fail when used (chapter 18). `WaitGroup.Add` and `Done` return `false`, leaving the count as it was, for a count that would go below zero. Each lock is reached through its own handle, so tasks taking locks of their own never wait on one another.

## Package variables

A `var` at the top level of a file is a package variable, and it is the one value no move ever touches: a `spawn` moves what its closure mentions, a channel moves what it carries, but a package variable is mentioned by no one and reached by every task. So it is under one rule, in three sentences. It may be read from any task and lent as `ref`, and it is never assigned, lent as `mut` or moved out of. Its type is one every task may share, `Send`, so an `rc` handle cannot be one. A change goes through what it holds: a `Counter`, an `Atomic` or a lock.

```go
var names = []string{"round", "square"}       // read anywhere, by any task
var hits = sync.NewCounter(0)                  // hits.Add(1) from any task, exact
var state = sync.NewAtomic("idle")             // state.Swap("busy".Clone()), state.Load()
var cache = sync.NewMutex(map[string]int{})    // g := cache.Lock(); g.Get().Insert(k, v)
var config = load("snello.toml")               // a call: made once, on first use
```

What the compiler says to the three ways of changing one, an assignment, a `mut` lent to a function or a method, and a move out:

```
main.snello:5:9: counter is a package variable, shared by every task; change it through a sync.Mutex, a sync.Atomic or a sync.Counter it holds
```

A package variable whose initializer is a call, the last line above, is made once, on first use, by whichever task reaches it first; a task that reaches it while another is making it waits, and a variable never used is never made, so it costs nothing. An initializer that reads its own variable through another function is a panic naming it, `package variable a is read by its own initializer`, and an initializer is plain code: one that waits for another task would wait forever. A variable with a literal initializer is plain memory, as before. The standard streams are functions for the same rule, `os.Stdout()`, since a `Write` needs a `mut` and a package variable is never lent one: each call gives a fresh handle on the descriptor, and dropping it never closes the stream.

## How tasks run

Tasks are coroutines on a few threads, a task that waits parks instead of holding a thread, and a timer is an entry in a heap one thread serves. Sockets, in the `net` package of chapter 14, park the same way. A `time.After` or `time.Tick` is an entry in that heap, not a task, holding a handle on its channel: an `After` drops it once it has fired, and either is taken out of the heap the moment the program drops its own last handle, since no one could receive from it. So a loop that waits in a `select` with a `time.After` beside a case that wins leaves nothing behind, as in Go since 1.23, and a `Tick` stops when its channel is dropped. Structured concurrency, where a scope joins the tasks it spawned and so could lend them a borrow, is a later design question; version one moves.
