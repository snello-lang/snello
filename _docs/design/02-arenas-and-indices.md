# Arenas and indices

Status: decided, 2026-10-17; `std/arena/arena.snello` implements the library section (2026-10-19), with `New<T>()` taking its type argument where nothing infers it and the range over an arena waiting for the iterator trait. This note explains how Snello handles data structures whose nodes refer to each other, such as trees with parent links, graphs, and caches. It is the direct consequence of the memory model in `01-language-design.md`.

## The problem

A Go developer writes a tree like this:

```go
type Node struct {
    value    string
    parent   *Node
    children []*Node
}
```

Every node holds pointers to other nodes. It works because the garbage collector keeps a node alive for as long as anything points at it. Nobody has to decide who owns a node.

Snello has no garbage collector. Every value has exactly one owner, and the owner frees the value when it goes out of scope. That rule gives deterministic memory use and no pauses, but it requires ownership to form a tree: one owner per value, no cycles.

Children fit that rule. The parent owns its children. The parent link does not. If a child held a pointer back to its parent, the parent would have two owners, its own parent and its child, and the compiler could not say who frees it.

Rust solves this with reference counting plus weak pointers, or with lifetime annotations that let a node hold a borrow of another. Snello has no lifetime annotations by design, and its borrows cannot be stored in structs at all. So a stored pointer from one node to another is simply not available in the core language.

## The solution

Move ownership up one level. One container owns every node, and nodes refer to each other by position in that container instead of by pointer.

```snello
type NodeID int

struct Node {
    value    string
    parent   NodeID?       // None for the root
    children []NodeID
}

struct Tree {
    nodes []Node           // the arena: owns every node
}
```

The tree owns a single slice. Every node lives in it. A "pointer" to a node is its index. Following the parent link is `t.nodes[n.parent?]`.

Because an index is a plain integer:

- it can be stored in any struct,
- it can be copied freely,
- it can be sent across tasks,
- and the ownership story stays trivial: the slice owns everything, and when the tree is dropped, everything goes with it.

Every operation on a node needs the arena as well as the index, which is why the methods in `../../examples/01-tree.snello` all take the tree as their receiver. You never hand someone "a node" on its own.

## The standard library `Arena<T>`

Writing the slice by hand, as above, explains the idea but leaves stale indices unchecked. The standard library provides `Arena<T>` and `Index<T>` so that everyday code gets the checks for free.

```snello
import "arena"

type Index<T>                                   // Copy; a slot number plus a generation

func arena.New<T>() Arena<T>
func (a mut Arena<T>) Insert(v T) Index<T>      // takes ownership of v
func (a ref Arena<T>) Get(i Index<T>) ref T?    // None if i is stale
func (a mut Arena<T>) GetMut(i Index<T>) mut T? // None if i is stale
func (a mut Arena<T>) Remove(i Index<T>) T?     // moves the value out; slot reused later
func (a ref Arena<T>) Len() int
for i, v := range a { ... }                     // i: Index<T>, v: ref T, live slots only
```

- `Index<T>` is typed, so an index into an arena of `Node` cannot be used on an arena of `Edge`. It is `Copy` and `Send`.
- Each slot carries a generation counter that increments on `Remove`. An `Index<T>` carries the generation it was issued with, so a lookup with a stale index returns `None` rather than the value now living in that slot. This turns the use-after-free of a raw pointer into a checked, recoverable condition.
- Removed slots go on a free list and are reused by later inserts, so a long-lived arena with churn does not grow without bound.
- `Get` and `GetMut` return borrows tied to the arena borrow, under the one built-in rule in `01-language-design.md`. They cannot be stored.

The tree in `../../examples/01-tree.snello` is written against this API.

## What it costs

- **Stale indices are a runtime condition, not a compile-time one.** The generation check makes them safe and detectable, but the compiler cannot prove an index is live the way it proves an owner is. Code that holds indices across removals must handle `None`.
- **Aliasing inside one slice.** Mutating `nodes[a]` and `nodes[b]` in the same expression is two mutable paths into one slice, and the compiler cannot prove `a != b`. The idiom is to copy the needed fields out as plain values first, then write back. `../../examples/15-lru.snello` shows both the rejected form and the accepted one.
- **No node without its arena.** A function that wants to work on a node must receive the arena too. In practice this is one extra parameter.
- **A small per-slot overhead.** The generation counter is one integer per slot, and each lookup compares it. This is far cheaper than a reference count or a runtime borrow flag.

## What it gains

- No ownership puzzle. The hardest case for an ownership system becomes the easiest.
- One allocation for the whole structure instead of one per node.
- Nodes packed together in memory, so walks are cache-friendly. Arenas are usually faster than pointer-linked structures even in languages that have a GC.
- Trivial serialization: write out the slice.
- Trivial `Send`: a struct of integers and a slice is sendable, so a whole graph can be moved to another task with no further checks.

## When to use something else

The arena is the recommended idiom, not the only one. The standard library provides `Rc<T>` and `Weak<T>` for code that genuinely needs shared, pointer-shaped ownership, for example when nodes must outlive any single container or be shared by several. `../../examples/01-tree/` shows the tree written with a slice by hand and with the library arena; a version with `Rc` children and `Weak` parent links is not among them, since a parent link is set after its node is shared and Snello changes a shared value only through a handle that is alone or through a lock, which holds only what may be sent and so never an `Rc`. `Rc` costs a reference count per node and is not `Send`; `Arc` is the atomic version for cross-task sharing.

## Precedent

This is not an idea forced by Snello's constraints. The Go compiler, most game engines, and the fastest graph and LRU libraries in both Go and Rust are written with arenas and indices, because it is faster than pointer chasing. Snello's bet is that making it the recommended idiom costs Go developers little, since many of them already write it, and in exchange removes the single hardest topic from the language.
