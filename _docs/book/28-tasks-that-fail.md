# 28. Tasks that fail

The design is the section "A handle on a task" of `../design/00-syntax-conventions.md`; Appendix B maps Go's `go` and `recover` onto this chapter. The samples are cut from `examples/handles/handles.snello` and `compiler/emit/internal/backendtest/testdata/handles.snello`, and the library is `std/task/task.snello`.

A panic is a bug: an index past the end of a slice, an `Expect` on a `None`, a `panic` call the program wrote for a state it thought impossible. Chapter 5 says what a panic does in one task, and chapter 10 says what it does to the program: it ends it, with the message and exit status 2, whichever task it happened in. That is the right default, and a program that has not thought about failure gets it. A server has thought about it. A bug in the task serving one request should not end the tasks serving the others, and the program wants to know that the task failed and why. This chapter is the one construct for that.

## A handle on a task

`spawn f(x)` as a statement starts a task and gives nothing back. The same `spawn` written where a value is wanted starts the task under a mark and gives a handle on it:

```go
fine := spawn check([3]int{3, 4, 5}, 3)
match fine.Join() {
	Ok(total) => fmt.Println("checked", total),
	Err(p) => fmt.Println("check failed:", p.Message()),
}
```

The handle is a `task.Handle<T>`, `T` the type the call returns, `int` here; a call that returns nothing gives a `task.Handle<()>`. `Join` waits until the task has ended and returns a `Result<T, task.Panic>`: `Ok` with what the call returned, or `Err` with the panic that ended it, whose `Message()` lends the message the panic was raised with, `index out of range [3] with length 3` for a bug the runtime found, or the text a `panic` call was given. Nothing about `check` says how it was started: the same function runs under a statement, where its panic ends the program, and under a handle, where its panic is the `Err`. The choice is made at the spawn, once.

```go
broken := spawn check([3]int{1, 2, 3}, 4) // asks for a fourth reading of three
match broken.Join() {
	Ok(total) => fmt.Println("checked", total),
	Err(p) => fmt.Println("check failed:", p.Message()),
}
```

A panic is still a bug and `Result` is still how a failure is handled, as chapter 8 and chapter 17 say: a function that can fail returns an error, and a program does not write `panic` where it means `Err`. The handle is containment. It limits what a bug takes down to the task it is in, so that the program can report it, count it, and go on.

## What a failed task leaves

A contained panic ends its task as an uncontained one ends the program, and in the same order:

- **The message is printed** on the standard error stream when the panic happens, `panic: index out of range [3] with length 3`, with the task's trace under it as chapter 17 shows, so that a failure the program only counts still shows. A program that wants silence redirects its standard error.
- **The values with a `Drop` method** the task made since it started are dropped, innermost first, **and so are the channel handles it held**, since a handle lost would keep its channel open for every other handle; **its deferred calls run**, once each. The audit below prints how far the task got, whether the task returned or failed:

```go
struct Audit impl Drop {
	step int
}

func (a mut Audit) Drop() { fmt.Println("audit: reached step", a.step) }

func check(readings [3]int, want int) int {
	audit := Audit{step: 0}
	total := 0
	for i := 0; i < want; i++ {
		total += readings[i]
		audit.step = i + 1
	}

	return total
}
```

- **Then the task hands its `Err` to the handle** and ends. `Join` returns after the drops, so what a `Drop` prints comes before what the joiner prints, as the example's output shows.

Two things a failed task leaves as they are. **A lock the task held is released by its guard's drop and nothing more**: the value under it may be half changed, since the task stopped in the middle of changing it, and a later `Lock` finds it so. A task whose panic is contained should own its state, or share only what one step changes whole. **The plain memory of the frames the panic abandoned**, a slice of numbers or a message a `Sprintf` built, stays allocated until the process ends, as it does for a failed test (chapter 16): a guard per owned value, which would free it, was measured at twice the time of the JSON benchmarks and fifteen times the cost of a variadic call, for every program, and a panic is a bug the program fixes. A debug build counts those blocks among what was not released and says why: `2 panics were contained by a task's handle; the memory of the frames a panic abandons is not released`. The example passes its readings as an array, a value of the frame, so that its debug build reports nothing; a server that contains a panic now and then loses the frames of that one task, and one that contains thousands has a bug to fix.

## A handle is a value

A handle is an owned value, and `Join` takes it: a task is joined once, by whoever holds the handle. It is `Send`, so it may be moved into another task and joined there, which is how a task of its own gathers a batch:

```go
func collect(batches []task.Handle<Report>) int {
	failed := 0
	for _, h := range batches {
		match h.Join() {
			Ok(r) => fmt.Println("batch", r.batch, "total", r.total),
			Err(p) => {
				failed += 1
				fmt.Println("a batch failed:", p.Message())
			},
		}
	}

	return failed
}

batches := []task.Handle<Report>{
	spawn summarize(1, [3]int{3, 4, 5}, 3),
	spawn summarize(2, [3]int{1, 2, 3}, 4),
	spawn summarize(3, [3]int{7, 0, 0}, 1),
}
collector := spawn collect(batches)
```

A handle has no zero value, as a channel handle has none: one comes only from a spawn, and `var h task.Handle<int>` is refused. A handle dropped without a `Join` leaves its task running; the task's result is dropped when the task ends, by the task if the handle went first, with the handle otherwise. `_ = spawn work()` therefore starts a task and forgets it, as the statement does, with one difference: its panic is contained and printed, and ends nothing. A `Join` on a task that never ends waits forever, as a receive on a channel no one sends on does, and the deadlock report says so when nothing else can run.

A handle is a value, not a name for the task. Nothing is sent to a task through it and no task finds another by it: there is no address, no mailbox, no link and no kill. A task that should stop is told so the way chapter 10 tells one, over a channel it selects on. A group of tasks, waited for together and told of the first that failed, is the library type of the next section, written over handles.

A method call gives a handle as a function call does, `spawn c.Twice()` with the receiver moved in, and so does a spawned closure, `spawn func() int { return k + 1 }()`, which takes what it mentions with it as chapter 10 says. A handle may be passed to a generic function, `func join<T: Send>(h task.Handle<T>) Result<T, task.Panic>`, and a handle may be joined by a task that was itself started for a handle, `spawn join(spawn fails(2))`.

## A group of tasks

A function that starts several tasks and needs all of them has `task.Group`: it waits for them together and hears of the first that failed. `g.Add(h)` puts a task started for a handle in the group, `g.Wait()` returns once every member has ended, `Ok` when each returned and `Err` with the first panic among them, and `g.Done()` gives a channel that ends when a member fails, so that the others can stop. The samples are cut from `examples/group/group.snello`.

```go
totals := make(chan (int, int), 3)
batches := task.NewGroup()
batches.Add(spawn summarize(1, [3]int{3, 4, 5}, 3, totals.Clone()))
batches.Add(spawn summarize(2, [3]int{1, 2, 3}, 2, totals.Clone()))
batches.Add(spawn summarize(3, [3]int{7, 0, 0}, 1, totals.Clone()))
match batches.Wait() {
	Ok(_) => fmt.Println("every batch reported,", len(totals), "totals"),
	Err(p) => fmt.Println("a batch failed:", p.Message()),
}
```

A member is a task of no result, a `task.Handle<()>`: the group is for the tasks a function runs for their effect, as a server's are, and a member with a value to give back sends it on a channel, as `summarize` sends its total. A `Result` a member computes is a value it already holds and reports as it likes; what a member cannot report itself is a panic, and that is what the group hears of.

`Done` is for the members that would otherwise run forever. A server that answers requests until told to stop selects on it beside its requests, as the worker pool of chapter 10 selects on its cancel; when the audit beside it fails, the group ends `Done`, the server leaves its loop, and `Wait` returns with the audit's panic once both have ended:

```go
func serve(requests <-chan string, stop <-chan (), served chan<- int) {
	n := 0
	for {
		select {
		case got := <-requests:
			r := got ?? break
			fmt.Println("served", r)
			n += 1
			served <- n
		case <-stop:
			fmt.Println("server stopping: another task failed")
			return
		}
	}
}

g := task.NewGroup()
g.Add(spawn serve(requests.Clone(), g.Done(), served.Clone()))
g.Add(spawn audit([3]int{1, 2, 3}, 4, served.Clone(), 2)) // asks for a fourth reading of three
match g.Wait() {
	Ok(_) => fmt.Println("every task returned"),
	Err(p) => fmt.Println("a task failed:", p.Message()),
}
```

The stop is asked for and never forced: `Done` ends when the first member fails, by the drop of the one sending handle the group keeps, as chapter 10 says a channel ends, and otherwise with the group itself once `Wait` has taken it; a member that does not select on it is waited for. There is no kill, as a handle has none, so a member that ignores `Done` and never ends is waited for forever, and the deadlock report says so when nothing else can run. `Wait` takes the group, so a group is waited for once, by the function that owns it; a group dropped without a `Wait` leaves its members running, as a handle dropped leaves its task. A group is that function's value and its members are that function's tasks: there is no tree of groups, and no group outlives the function that made it.

## Starting a task again

The library restarts nothing. A program that wants a task run again when it failed writes the loop, with its limit in plain sight:

```go
for attempt := 1; attempt <= 3; attempt++ {
	match (spawn flaky(attempt)).Join() {
		Ok(n) => {
			fmt.Println("succeeded on attempt", n)
			break
		}
		Err(p) => fmt.Println("retrying:", p.Message()),
	}
}
```

What is left out, and why: there is no supervisor, no restart strategy and no kind of child. Each of those is a policy, how many times, how long between, which failures count, and the policy belongs to the program that has the facts; a loop over `Join` states it in three lines that a reader sees, where a supervisor would hide it behind a type's defaults. A library that restarts tasks also needs a task to be startable again from a value, which a spawned call is not: the call's arguments moved into the task when it started, and a second start needs their clones, which only the program knows how to make.

## What the compiler checks

The call is spawned as a statement's is: every argument, every variable a closure captures and a receiver taken by value is `Send` and moves into the task, with the messages of chapter 10, and a borrow cannot be spawned. The result is `Send` too, since the handle carries it to the task that joins. A call returning an `rc.Rc<int>` is refused with `cannot spawn for a handle: rc.Rc<int> stays in its task`; a call lending a borrow in, which is how a function comes to return one, is refused as the statement refuses it, `a spawned task cannot hold a borrow`. The handle's type is written `task.Handle<T>` by a program that names it, in a field or a parameter, which imports `task`; a program that only writes `h := spawn f(x)` and `h.Join()` names nothing and imports nothing.

## For maintainers

- **The parser** reads `spawn` where an expression is as an `ast.SpawnExpr`, holding the call as an `ast.SpawnStmt` does; `spawn` at the start of a statement is still the statement. **The checker** spawns the call under the statement's rules, refuses a result that is not `Send`, loads the `task` package through the importer whether or not the program imports it, and gives the expression the type `task.Handle<T>`.
- **The library** is `std/task`: `Handle<T: Send>` holds a channel of one slot carrying `Result<T, Panic>`, `Join` takes the handle and receives from it, and `Panic` holds the message. The task holds a sending handle until it has sent, so a receive waits for the task's one value. `Group` holds a `sync.Arc<sync.Mutex<failure>>`, the first panic and `Done`'s one sending handle, the receiving end of `Done` and a `sync.WaitGroup` counting its watchers: `Add` counts one in and spawns a watcher, a task that joins the member and, if it failed, keeps the panic when none is kept yet and takes the sending handle out of the cell, which drops it and ends `Done`, under the cell's lock, then counts itself out; `Wait` is the `WaitGroup`'s wait and then takes the panic out of the cell. Nothing in the runtime observes a handle but a receive on it, which is why a member has a watcher, a small bounded task.
- **Both backends** make the channel at the spawn, put a clone of its handle in the task's block beside the arguments, and write the call in a function of its own, named with the prefix `lower.CaughtPrefix`, which the task's entry runs under the runtime's mark with `sn_catch`. The entry then offers the handle `Ok` with the result or `Err` with the panic, drops the value when the offer finds the handle gone, drops the task's channel handle and frees the block. The value of the expression is the handle struct holding the channel.
- **The runtime's** `sn_catch` takes whether to print the panic: a task's entry asks for the print, `testing.Catch` does not. The mark otherwise is the one a test's failure unwinds to, and the drops and deferred calls are its.
- **The stack analysis** knows `sn_catch` calls through a pointer to one of the functions named with `lower.CaughtPrefix`, as it knows the channel's drops, so a task with a handle keeps a small stack when its call is bounded: about 6 KB for the example's tasks.
- **Both backends register a channel handle** on the runtime's guard stack as they register a value with a `Drop` method, so that a contained panic gives it up; the shared program `handle-releases-channel` holds a debug build to it.
- **Tests**: `compiler/emit/internal/backendtest/testdata/handles.snello` and `group.snello` on both backends, in release and debug; `std/task/task_test.snello`; the refused program `compiler/types/testdata/errors/handle-not-send.snello`; the parser's tables; `TestRuntimeOptions` for the analysis.
