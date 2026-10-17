# Example 02: a concurrent pipeline, Go and Teak

A three-stage pipeline, produce, count, merge, with the middle stage fanned
out to four workers. It is the shape from the Go blog's "Pipelines and
cancellation" post, and the point of the example is that Teak keeps that
shape exactly.

| File | Language | Lines that differ from the other |
|---|---|---|
| `pipeline.go` | Go | 0 in the pipeline itself |
| `pipeline.teak` | Teak | 1: `ref job.text` instead of `job.text` |

## Why it is the same

A pipeline already follows ownership discipline. Every value is created in
one stage, sent to the next, and never touched again by the sender. Teak's
rules for tasks are a description of that discipline, so code that already
follows it needs nothing added.

The four rules, each marked in `pipeline.teak` where it applies:

1. Sending on a channel moves the value. The sender no longer owns it.
2. Receiving gives the receiver ownership. When the value goes out of
   scope it is freed, in that task, at that moment.
3. A channel handle is a `Copy` value. Any number of tasks may hold one.
4. `go f()` moves everything it captures into the new task.

## Why it is different underneath

| Property | Go | Teak |
|---|---|---|
| `job` after `out <- job` | Still usable; struct copied, string shared | Compile error: moved |
| Memory of a consumed job | Freed by the GC, eventually | Freed when `job` leaves scope in the worker |
| Two goroutines writing one map | Compiles; data race at runtime | Compile error: value already moved |
| A borrow captured by `go` | No such concept; the pointer is shared | Compile error: borrows cannot cross `go` |
| Sharing when you need it | Pass a pointer and remember the mutex | `Arc<Mutex<T>>`, and the compiler checks it |

The last row is in `../03-http-cache.teak`, where shared state is actually
needed. It is kept out of this example on purpose.

## The one line that differs

Counting words borrows the text instead of consuming it:

```teak
out <- Result{id: job.id, words: len(strings.Fields(ref job.text))}
```

`strings.Fields` only needs to read the text, so it takes a `ref`. Without
the `ref`, `job.text` would be moved into the call, which would also be
legal here since `job` is dropped on the next line. The borrow is the
cheaper and clearer choice. This is the only Teak-specific token in the
whole pipeline.

See `../../design/03-ownership-across-tasks.md` for the rules and the
reasoning behind them.
