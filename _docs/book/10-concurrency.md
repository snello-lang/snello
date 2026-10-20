# 10. Concurrency: `spawn`, channels, `select`

Status: implemented (stage 3). `Send` is inferred by the compiler (task 3.1) and checked at the three crossings, with `close` and `for range ch` (task 3.2); `teak build` runs tasks, channels and `select` (task 3.3; `examples/run/tasks.teak` and `select.teak`, examples 02, 13 and 18); the shared-state library, `sync`, and `time.After` run (task 3.4; `examples/run/sync.teak`, example 12); the race programs under `examples/core/errors/` are rejected (task 3.5). The samples are cut from `examples/02-pipeline/pipeline.teak`, `examples/13-actor.teak`, `examples/18-iterators.teak` and `examples/run/sync.teak`.

A task is a function running on its own, started with `spawn`. Tasks talk through channels. That much is familiar. What Teak adds is one rule about the values that pass between tasks, and the compiler enforces it, so that two tasks can never reach one value at the same time: the bug that a race detector hunts at run time cannot be written.

## The rule

A value crossing from one task to another is **moved, immutable, or atomically shared**. Nothing else crosses. There are three crossings:

- `spawn f(x)`, or `spawn func() { ... }()`, for the arguments and the variables the closure mentions;
- `ch <- v`, for the value sent;
- `<-ch`, for the value received.

Each is a move, as chapter 5 describes a move. After `ch <- job`, `job` is gone from the sender; the receiver that does `j := <-ch` owns `j` and drops it when it goes out of scope, there and then. A closure given to `spawn` owns every variable it mentions, so the function that spawned it cannot use them afterwards. And a borrow, `ref T` or `mut T`, never crosses: it points into a frame of the task that made it, and the new task has frames of its own.

Channel handles are `Copy`. The channel is one shared object that synchronizes itself; a handle to it is a small value that any number of tasks may hold, which is what makes a pipeline a matter of passing the same `out` to several workers.

```teak
func produce(n int) <-chan Job {
    out := make(chan Job)
    spawn func() {                              // `out` is Copy, so it is copied into the task
        defer close(out)
        for i := 0; i < n; i++ {
            job := Job{id: i, text: "..."}
            out <- job                       // rule 1: job is moved into the channel
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

```teak
spawn run(Account{balance: initial}, ch)      // Account is Send: an int and a slice of ints
```

When a value cannot cross, the message names the part that stops it, not the type around it: an `rc.Rc<Node>` inside a struct is reported as `rc.Rc<Node> stays in its task`, a borrow as `ref Node is a borrow`, a trait value as `Shape does not refine Send`. The fix is to say what was meant: clone the value, send a piece of it to each task, or share it through `sync.Arc`, which chapter 10 covers once the library is written.

There is no second property beside `Send`. Nothing in Teak changes a value through a shared `ref` except under a lock, so a value that is safe to send is safe to share, and one word covers both.

## What the compiler checks

A closure written in a `spawn` statement takes every variable it mentions with it. The move happens at the `spawn`, whatever the closure does with the variable inside, so the function cannot use the variable afterwards; a `Copy` value is copied in, as it is for any closure.

```teak
names := []string{"a", "b"}
done := make(chan ())
spawn func() { fmt.Println(names); done <- () }()
// fmt.Println(names)             // error: names was moved into the task
<-done
```

Each variable the closure takes, each argument of a spawned call, and a receiver taken by value must be `Send`. The message names the part that is not, as the table above reads it: `cannot spawn with r.Clone(): rc.Rc<int> stays in its task`. A borrow cannot be passed to a spawned function at all: `a spawned task cannot hold a borrow`.

A channel carries only `Send` values, and the compiler says so where the channel type is written rather than at each send: `make(chan rc.Rc<int>)` is refused with `a channel cannot carry rc.Rc<int>: rc.Rc<int> stays in its task`, and `make(chan ref string)` with `a channel cannot carry a borrow`. A send then needs no check beyond the move it is, and a receive yields a `T?` the task owns.

`close(ch)` closes a channel: a receive yields `None` once the values already sent are taken, and a range over the channel ends. A receive-only channel cannot be closed. `for v := range ch` receives until then, and each `v` is owned by the loop body; `for range ch` with no variable drops each value as it arrives.

```teak
for r := range merge(workers) {          // `workers` is moved into merge
    total += r.words
}
```

## `select`: whichever is ready

`select` waits for the first of several channel operations that can proceed and runs that case; with a `default` it does not wait. A receive case binds the `T?` the receive yields, a send case sends, and a bare `<-ch` discards what it receives. When several cases are ready at once, one is chosen at random, so a busy channel cannot starve the others.

```teak
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

A task runs on its own thread, started when `spawn` runs and gone when its function returns. A value sent on a channel is copied into the channel and the sender's variable is empty from then on; a value received is copied out into memory the receiving task owns, so each value is owned by exactly one task at any moment, which is what the rules above promise. An unbuffered channel hands a value over directly, and the sender continues only once a receiver has taken it; a buffered one holds up to its capacity. Three things to know:

- **The program ends when `main` returns**, whatever the other tasks are doing. A program that needs its tasks to finish waits for them, on a channel as `examples/run/tasks.teak` does.
- **A channel lives as long as the program.** Its handle is `Copy`, so nothing counts the handles and nothing frees the channel; a value still inside one when the program ends is not dropped. A program that makes a channel per request keeps each until it exits.
- **A panic in any task ends the program**, with the message, the drops of that task's own values, and exit status 2, as a panic in `main` does.

`teak build -debug` checks a concurrent program as it checks any other, and reports the memory still held at the end; when a task is still running at that point the report is skipped, with a note, since what a running task owns is not a leak.

## Sharing state: `sync`

Some state genuinely must be reached from several tasks: a counter, a cache, a cancellation flag. The `sync` package has the types for it, and the compiler checks their use with the rules of chapter 6 and nothing added. The sample is cut from `examples/run/sync.teak`.

- **`sync.Arc<T>`** is a handle on a value shared between tasks, as `rc.Rc` is within one: `sync.NewArc(v)` makes the first handle, `Clone` another, `Get` lends the value for reading, and the value is dropped with the last handle. An `Arc<T>` is `Send` when `T` is, so each task gets a clone of its own. An `Arc` never lends its value for changing.
- **`sync.Mutex<T>`** owns a value and lends it for changing to one task at a time. `Lock` waits for the lock and returns a guard; `Get` on the guard lends the `mut T`, and the guard gives the lock back when it is dropped, at the end of its scope.
- **`sync.RwLock<T>`** lends either for reading to any number of tasks, with `Read`, or for changing to one, with `Write`; each returns a guard of its kind.
- **`sync.Once`** runs a function the first time `Do` is called and never again; **`sync.WaitGroup`** counts tasks with `Add` and `Done` and waits for zero with `Wait`. A `WaitGroup` is a `Copy` handle like a channel, so a task is spawned with a copy of it.

```teak
counter := sync.NewArc(sync.NewMutex(0))
wg := sync.NewWaitGroup()
for i := 0; i < 4; i++ {
	c := counter.Clone()
	wg.Add(1)
	spawn func() {
		g := c.Get().Lock()
		*g.Get() += 1
		wg.Done()
	}()
}

wg.Wait()
fmt.Println(*counter.Get().Lock().Get()) // 4
```

The guard is what makes the lock safe to use: it holds a borrow of the value, so it is tied to the lock and the scope that took it. Keeping it in a struct, sending it, capturing it in a `spawn`, or binding the value of a lock the statement drops, `n := m.Lock().Get()`, are each refused by the borrow rules, with the message those rules give; and since the value is reached only through a guard, touching it without the lock cannot be written at all. The lock is released where the guard's scope ends, so a block, or a function of its own, bounds the critical section.

Each of these types is made with its `New` function. The zero value, `var m sync.Mutex<int>`, is empty, and `Lock` on it panics, as `Get` on an empty `rc.Rc` does.

## What comes next

Stage 4 replaces the runtime under this chapter without changing a line of it: tasks become coroutines on a few threads, a task that waits parks instead of holding a thread, and a timer becomes an entry in a wheel. Until then a `time.After` or `time.Tick` is a task that sleeps, so a program that returns from `main` with a timer not yet due leaves a task running, and a debug build skips its leak report with the note. Structured concurrency, where a scope joins the tasks it spawned and so could lend them a borrow, is a later design question; version one moves.
