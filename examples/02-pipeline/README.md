# Example 02: a concurrent pipeline, Go and Snello

A three-stage pipeline, produce, count, merge, with the middle stage fanned out to four workers. It is the shape from the Go blog's "Pipelines and cancellation" post, and the point of the example is that Snello keeps that shape.

| File | Language | Lines that differ from the other |
|---|---|---|
| `pipeline.go` | Go | 0 in the pipeline itself |
| `pipeline.snello` | Snello | `ref job.text` instead of `job.text`, and a `.Clone()` of a channel handle for each task that uses it |

## Why it is the same

A pipeline already follows ownership discipline. Every value is created in one stage, sent to the next, and never touched again by the sender. Snello's rules for tasks are a description of that discipline, so code that already follows it needs little added: each task that sends or receives on a channel says so by taking a handle of its own.

The four rules, each marked in `pipeline.snello` where it applies:

1. Sending on a channel moves the value. The sender no longer owns it.
2. Receiving gives the receiver ownership. When the value goes out of scope it is freed, in that task, at that moment.
3. A channel handle is owned. `ch.Clone()` gives another handle on the same channel for another task, and the last handle to go frees the channel, with no collector to find it.
4. `spawn f()` moves everything it captures into the new task.

## Why it is different underneath

| Property | Go | Snello |
|---|---|---|
| `job` after `out <- job` | Still usable; struct copied, string shared | Compile error: moved |
| Memory of a consumed job | Freed by the GC, eventually | Freed when `job` leaves scope in the worker |
| Two goroutines writing one map | Compiles; data race at runtime | Compile error: value already moved |
| A borrow captured by `spawn` | No such concept; the pointer is shared | Compile error: borrows cannot cross `spawn` |
| Sharing when you need it | Pass a pointer and remember the mutex | `Arc<Mutex<T>>`, and the compiler checks it |

The last row is in `../03-cache-server.snello`, where shared state is actually needed. It is kept out of this example on purpose.

## The lines that differ

A task that uses a channel gets a handle of its own, cloned before the `spawn`:

```go
w := out.Clone()
spawn func() {
    defer close(w)
    ...
}()
return out
```

In Go the goroutine and the caller share one pointer and the collector frees the channel once both are gone. Snello has no collector, so the handles are counted, and the clone is the one place the count goes up: an atomic add where a channel is handed to another task, nothing where a function in the same task only borrows it.

Counting words borrows the text instead of consuming it:

```go
out <- Result{id: job.id, words: len(strings.Fields(ref job.text))}
```

`strings.Fields` only needs to read the text, so it takes a `ref`. Without the `ref`, `job.text` would be moved into the call, which would also be legal here since `job` is dropped on the next line. The borrow is the cheaper and clearer choice.

See `../../_docs/design/03-ownership-across-tasks.md` for the rules and the reasoning behind them.
