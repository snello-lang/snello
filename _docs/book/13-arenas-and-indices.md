# 13. Arenas and indices

The tree is cut from `examples/tree/tree-arena.snello`, the rest from `compiler/emit/internal/backendtest/testdata/arena.snello`, which runs stale indices too.

Chapter 7 built a list and a tree from owned pointers, and chapter 6 said what a borrow cannot do: live in a struct. Between them they leave one shape of data out. A node that points back at its parent, a graph whose nodes point at each other, a cache that is both a map and a list: each needs a link that does not own what it names, and Snello has no such pointer. This chapter is the answer, and it is the answer Go programmers already know from any code that cares about speed: put every node in one container and link them by position.

## An index instead of a pointer

The container owns every node. A link is a number, the node's place in the container, and a number has nothing for the ownership rules to say about it: it is `Copy`, it goes in a struct, a slice or a map, it crosses a task. Following a link is indexing the container. Dropping the container drops every node at once, in one allocation's worth of work rather than one per node.

Written by hand, that is a slice and an index type:

```go
type NodeID int

struct Node {
	value    string
	parent   NodeID?       // None for the root
	children []NodeID
}

struct Tree {
	nodes []Node           // owns every node
}
```

It works, and it has one hole. Remove a node, reuse its slot for another, and every `NodeID` still pointing at the slot now names the wrong node without a word from anyone. That is the use-after-free of a pointer, back as a logic error. The standard library's arena closes it.

## `arena.Arena<T>` and `arena.Index<T>`

```go
import "arena"

a := arena.New<string>()
x := a.Insert("x")         // Index<string>
y := a.Insert("y")
fmt.Println(a.Len(), a.Get(x), a.Get(y))   // 2 Some("x") Some("y")
```

`Insert` takes the value, which the arena owns from then on, and returns an `Index<T>`. `Get` lends the value the index names, `GetMut` lends it for changing, and `Remove` moves it back out; each returns an `Option`, because the index may be stale. `Len` counts the values held. An `Index<T>` is a struct of two numbers, so it is `Copy`, `Eq` and `Hash`: it is compared with `==`, kept in a struct or a slice, and used as a map key. It carries its element type, so an `Index<Node>` cannot be handed to an `Arena<Edge>`.

The type is given to `New` when nothing else fixes it, `arena.New<Node>()`; a field of a declared type does not infer it.

## Stale indices

Every slot in the arena has a generation, and an `Index` remembers the generation its slot had when the value went in. `Remove` empties the slot and raises its generation. From then on every copy of the old index fails the comparison, however many values the slot has held since:

```go
stale := y
fmt.Println(a.Remove(y), a.Len(), a.Get(stale), a.Remove(stale))   // Some("y") 1 None None

w := a.Insert("w")                           // reuses y's slot
fmt.Println(a.Get(w), a.Get(stale), w == stale)                      // Some("w") None false
```

The slot is reused, so an arena that inserts and removes for a long time does not grow; the stale index still answers `None`, so the reuse is safe. A zero-valued `Index`, `var i arena.Index<string>`, names nothing in any arena. What a pointer makes a crash or a silent corruption, an arena makes an `Option` the program handles: a `match`, an `if` binding, or `Expect` where a stale index would be a bug worth a panic with a message.

An index belongs to the arena that issued it, and the type says only the element type: an `Index<string>` from one `Arena<string>` used on another `Arena<string>` is not refused, and names whatever that arena holds in the same slot on the same generation, a value it has nothing to do with, which `Remove` would take out. Keep each index with its arena, as a field beside it or in a structure that owns both; the arena libraries of other languages leave the same rule to the program. An arena prints the values it holds, in the order of their slots: `fmt.Println(a)` prints `arena[y z]`.

The check is one comparison per lookup, and it is a runtime check, not a compile-time one. The compiler proves who owns a value; it cannot prove an index is live. Code that keeps indices across removals handles `None`.

## Walking an arena

`a.Iter()` walks the values in the order of their slots, each with the index that names it, and a `range` over it lends them for the loop:

```go
for i, v := range a.Iter() {
	fmt.Println(i, v)      // an Index<T> and a ref T
}
```

The arena is lent for as long as the loop runs, so the body cannot insert or remove meanwhile; collect the indices to change in a slice, and change after the loop. A walk is a value with a `Next` method, as every walk over a collection of the standard library's packages is, chapter 3.

## A tree

A tree keeps its nodes in one arena and its links as indices, in both directions:

```go
struct Node {
	value    string
	parent   arena.Index<Node>?         // None for the root
	children []arena.Index<Node>
}

struct Tree {
	nodes arena.Arena<Node>             // owns every node
	root  arena.Index<Node>
}

func (t mut Tree) AddChild(parent arena.Index<Node>, value string) arena.Index<Node> {
	id := t.nodes.Insert(Node{value: value, parent: Some(parent), children: []arena.Index<Node>{}})
	t.nodes.GetMut(parent).Expect("parent exists").children.Push(id)
	return id
}

func (t mut Tree) Remove(id arena.Index<Node>) {
	node := t.nodes.Remove(id) ?? return         // already gone: nothing to do
	for _, child := range node.children { t.Remove(child) }
	if parent := node.parent {
		if p := t.nodes.GetMut(parent) { p.children.RemoveValue(id) }
	}
}
```

The parent link is the thing an owned pointer could not express, and here it is a field like any other. `Remove` moves the node out, walks its children by their indices and takes the node out of its parent's list; the indices the caller still holds for the removed subtree answer `None` from then on. A node is never handed around on its own: a function that works on one takes the arena, or the tree, and the index, which is the one cost in writing.

## Two borrows of one arena

`GetMut` lends the arena `mut` for as long as the borrow it returns lives, so two values of one arena cannot be changed through two borrows in one expression; the compiler cannot know the two indices differ. The idiom is to read what is needed out as values first, then write:

```go
e := c.slots.Get(s).Expect("live")
prev, next := e.prev, e.next                // Index is Copy; the borrow of e ends here
if p := prev { c.slots.GetMut(p).Expect("live").next = next }
```

The borrow `Get` returns is tied to the arena's borrow, under the rule of chapter 6, so it cannot be stored or returned past the arena; the index can, and is what a structure stores.

## When to reach for something else

An arena is the recommended shape for linked data, not the only one. A value that must outlive any one container, or be shared by several owners, is a job for `rc.Rc<T>` and `rc.Weak<T>`, chapter 14; they cost a count per value, lend the value for changing only to a handle that is alone, and are not sent across tasks. `examples/tree/` holds the same tree written three ways: Go's pointers, a slice by hand, and the library arena.
