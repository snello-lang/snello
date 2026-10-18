# Example 01: a tree with parent pointers, three ways

A tree whose nodes link back to their parent is the structure that breaks single ownership: a child pointing at its parent gives the parent two owners. This folder shows the same program three times so the gain of each step is visible.

| File | Language | How nodes link | Who frees a node |
|---|---|---|---|
| `tree.go` | Go | Pointers | The garbage collector, eventually |
| `tree-slice.teak` | Teak | Indices into a slice you manage | The slice, when the tree is dropped |
| `tree-arena.teak` | Teak | `Index<Node>` into the standard library `Arena<Node>` | `Remove` immediately, or the arena when dropped |

Read them in that order.

## What each version gives up and gains

| Property | Go pointers | Hand-rolled slice | `Arena<T>` |
|---|---|---|---|
| Memory freed when | GC decides | Tree dropped | `Remove` or tree dropped |
| Allocations for N nodes | N | 1 (amortized) | 1 (amortized) |
| GC pauses | Yes | No | No |
| Use-after-remove | Silent: stale pointer keeps node alive | Silent: stale index reads another node once slots are reused | Detected: `Get` returns `None` |
| Slot reuse after remove | n/a | Unsafe, so slots leak | Safe, via generation counter |
| Link crosses tasks | Pointer is shared, races possible | Index is an int, `Send` | Index is an int, `Send` |
| Serializable | Needs a graph walk | Write the slice | Write the arena |
| Lines of Tree code | 60 | 70 | 60 |

The step from Go to the slice buys determinism and one allocation, at the cost of managing liveness by hand. The step from the slice to `Arena<T>` removes that cost: the generation check turns a stale index from a silent bug into a `None` the caller must handle, and lets slots be reused.

## The one line that differs most

Go, after removing subtree `a`:

```go
t.Remove(a)
fmt.Println(t.Path(b))   // [a b]: b is still alive through a stale pointer
```

Teak with `Arena<T>`:

```teak
t.Remove(a)
fmt.Println(t.nodes.Get(b))   // None: b was freed, and the index knows it
```

The Go program keeps a removed subtree alive for as long as any variable points into it, which is a classic source of leaks. The Teak program frees it immediately and makes every old handle say so.

## The escape hatch

Code that needs pointer-shaped shared ownership, for example nodes that must outlive any single container, can use `Rc<Node>` with `Weak<Node>` back-links from the standard library. It costs a reference count and one allocation per node, a runtime borrow check on each access, and the tree is not `Send`. It is deliberately not shown as a full version here; see `../../_docs/design/02-arenas-and-indices.md` for when it is the right choice.
