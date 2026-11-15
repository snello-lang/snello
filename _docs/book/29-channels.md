# 29. Channels

The design is the section "A channel ends by ownership" of `../design/00-syntax-conventions.md`; chapter 10 introduced channels beside `spawn` and `select`, and Appendix B maps Go's `close` onto this chapter. The samples are cut from `examples/channels/channels.snello`, and the test programs are `compiler/emit/internal/backendtest/testdata/tasks.snello` and `select.snello` and the shared programs `channel-ends` and `send-without-receiver`.

A channel is the one shared thing in Snello, and chapter 10 said how a value crosses it: sending moves, receiving owns, and each task holds a handle of its own. This chapter is about the two ends of a channel and how they close. In Go, nothing owns the end of a channel: `close` is a call anyone may make at any time, so a send on a closed channel panics, a second close panics, a receiver that closes surprises the senders, and a goroutine waiting on a channel no one will ever use waits forever, because the runtime cannot tell that channel from one whose sender is busy. Snello's handles are owned, and their types say which end they hold, so the ends can belong to the handles. The rule is one sentence, and the rest of the chapter is what follows from it:

**A channel ends when its last sending handle is dropped.**

## A producer that returns

A function that sends on a channel takes a sending handle, `chan<- T`. When the function returns, the handle is dropped with its other locals; if it was the last sending handle, the channel has ended, and a receiver sees `None` once the values already sent are taken:

```go
func produce(n int, out chan<- int) {
	for i := 1; i <= n; i++ {
		out <- i
	}
}

func numbers(n int) <-chan int {
	ch := make(chan int)
	spawn produce(n, ch.Clone())
	return ch
}

total := 0
for v := range numbers(5) {
	total += v
}
```

Nothing closes the channel, and nothing could close it twice or too early: the end is the producer's return. Two lines in `numbers` carry the rule. `ch.Clone()` is moved into `produce`, where a `chan<- int` is wanted, so that handle keeps its sending end alone. `return ch` moves `ch` where a `<-chan int` is wanted, so the function's own handle keeps its receiving end alone. After that, the producer's handle is the one sender, and the range in `main` ends when `produce` returns.

A `chan T` handle sends and receives, and counts on both sides. A producer that keeps a `chan T` keeps its channel open, however many `chan<- T` clones it hands away, and the receivers wait on it until the `chan T` goes too. That is the one thing to know when a range does not end: find the `chan T` that is still held, and narrow it or let it go. `var in <-chan int = ch` narrows a handle in place, and a parameter, a field or a result typed with one end narrows the handle moved into it.

## A stop that is a drop

A task that should stop when told selects on a channel it never receives a value from, as chapter 10's worker does; what it waits for is the end. Under this chapter's rule the stop is not a call: whoever holds the sending end of that channel stops the tasks by letting it go.

```go
func worker(jobs <-chan int, stop <-chan (), counts chan<- int) {
	count := 0
	for {
		select {
		case got := <-jobs:
			count += got ?? break
		case <-stop:
			break
		}
	}

	counts <- count
}

jobs := make(chan int, 4)
stop := make(chan ())
counts := make(chan int, 2)
for i := 0; i < 2; i++ {
	spawn worker(jobs.Clone(), stop.Clone(), counts.Clone())
}

var jobsIn chan<- int = jobs // main keeps the sending end of jobs alone
jobsIn <- 10
jobsIn <- 20
_ = stop                     // the one sending end: both workers see the end
```

Each worker was given `stop.Clone()` where a `<-chan ()` is wanted, so the workers hold receiving ends only, and `main`'s `stop` is the one sending handle. `_ = stop` drops it, and every worker's `select` wakes with the end. A second stop cannot be written: there is nothing left to drop. A stop from the wrong side cannot be written either: a task holding a `<-chan ()` has no sending end to give up. The worker pool example keeps its one sending handle under a lock, so that the first of several tasks to cancel drops it and the others find it gone; the task group of chapter 28 does the same for its `Done`.

## A sender whose receiver is gone

The rule has a mirror. A send on a channel whose last receiving handle is gone cannot ever be taken, so it drops its value and goes on, where a Go goroutine blocks forever. A sender that wants to stop when no one listens asks `Open`, which says whether a receiving handle remains:

```go
func report(out chan<- int, sent chan<- bool) {
	n := 0
	for out.Open() {
		out <- n
		n += 1
		time.Sleep(time.Millisecond)
	}

	sent <- true
}

readings := make(chan int)
stopped := make(chan bool, 1)
spawn report(readings.Clone(), stopped.Clone())
first := <-readings
_ = readings // the last receiving end goes with it
```

`main` takes one reading and lets its receiving handle go; the reporter's next send finds no receiver, drops the reading, and `Open` answers `false` on the next pass. The reporter ends, where in Go it would have been one more goroutine asleep forever. `Open` is a question asked, never a stop forced: a sender that ignores it goes on sending into nothing, each value dropped as it is sent. `Open` on a `chan T` is always `true`, since that handle receives itself; it is a question for a `chan<- T`.

## What this removes

Each of these is a bug a Go program can write and a Snello program cannot:

- **A send on a closed channel.** There is no close; a send needs a sending handle, and while one exists the channel has not ended.
- **A close of a closed channel.** There is nothing to call twice; the end is a drop, and a handle is dropped once.
- **A close by a receiver, or by one of several senders behind the others' backs.** A receiving handle has no sending end to give up, and the channel ends only when every sending handle is gone.
- **A goroutine blocked forever on a receive no one will send to.** Once the senders are gone the receive yields `None`; a receive that waits is waiting on a sender that exists.
- **A goroutine blocked forever on a send no one will receive.** Once the receivers are gone the send drops its value and returns.

What remains of deadlock is a genuine cycle, tasks each waiting for another, and a task holding a sending handle it never uses while another waits to receive. The report names it: `fatal error: all tasks are asleep - deadlock!` is followed by what the main task waits for, `the main task waits to receive on the channel made at pipeline.snello:39, which 2 sending and 1 receiving handles remain on, with 0 tasks waiting to send and 1 to receive`. The sending handles that remain are the ones to look for.

## Buffered channels

A buffered channel ends like an unbuffered one, and its buffer is drained first: a receive takes the values still in the ring, in order, and yields `None` after the last. The test programs show it with a channel of three tickets filled and then narrowed to its receiving end, `var left <-chan Ticket = tickets`, after which a receive gives each ticket and a range the rest. When the last receiving handle goes with values still in the buffer, they stay there until the last handle of either kind frees the channel, which drops them.

## For maintainers

- **The runtime** counts the handles of each direction in one word, the sending handles in its low half and the receiving ones in its high half, so that a `chan T` handle is still one atomic add to clone and one subtract to drop. `sn_chan_clone`, `sn_chan_drop` and `sn_chan_narrow` take the handle's direction; the last sender to go marks the channel ended and wakes the receivers waiting, the last receiver to go wakes the senders waiting, which drop their values through the channel's element drop, in their own task; a send and a receive read two flags beside the lock, set by the last receiver's and the last sender's drop, and never the counts, whose line every clone and drop writes. `chan_drop_value` is the one function that calls the element drop, which the stack analysis knows by name; a refused send's drop goes through it too, and the analysis counts a jump through a register, a tail call, as a call through a pointer, since that is what the compiler makes of it.
- **The compiler** refuses `close` with `close is gone: a channel ends when its last sending handle is dropped`, keeps the name predeclared so that the message is reached, refuses a send on a receive-only handle and a receive from a send-only one, and gives `Open` to `chan T` and `chan<- T`. Both backends write the direction into every clone and drop, from the handle's static type, and write `sn_chan_narrow` where a `chan T` value meets a one-direction type, in `valueTo`, the one place a value meets the type wanted. A channel type's mangled name carries its direction, so that each direction has a dropper of its own. `make` passes where the channel was made, for the deadlock report.
- **The library**: `time`'s `Timer` and `Ticker` keep a sending end beside `C`, so that `C` ends with them and not before, and the runtime's timer holds a sending handle of its own; `testing`'s gate is a receiving end in every test, and the runner's handle is the one sender; `task.Group` keeps `Done`'s sending handle in its cell. A `time.After` channel ends once its timer has fired and dropped its handle.
- **Tests**: the shared programs `channel-ends` and `send-without-receiver` on both backends, in release and debug; the misuse programs `deadlock-receive` and `deadlock-send` for the report; `close-gone.snello` among the refused programs; `TestTaskBoundaries` in the checker for the directions; `TestMangle` for the names.
