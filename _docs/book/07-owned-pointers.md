# 7. Owned pointers and recursive data

`compiler/emit/internal/backendtest/testdata/boxes.snello`, `examples/linked-list/linked-list.snello`, `bst.snello` and `parser.snello` exercise this chapter.

A value of type `*T` is an owned pointer: it holds a `T` on the heap and is the one owner of it. `&expr` makes one, moving the value out of the expression into a fresh heap cell:

```go
s.head = Some(&Node{value: v, next: old})       // &x allocates x on the heap
```

That is the only way a pointer is made, and `&` is the only expression in Snello that allocates by itself. There is no address-of: a pointer never points into a variable, a field or an element, so it can never dangle. To refer to a place without owning it, use a borrow (chapter 6).

## When to use a pointer

A struct holds its fields inline, wherever it lives: in a variable, a field, an element of a slice. That is the default, and most structs never need a pointer. In Go a pointer does several jobs: it lets a method change a struct, passes a large struct cheaply, shares one value between several parts of a program, stands for a missing value with `nil`, and links a recursive type. Go can use one tool for all of them because its garbage collector frees the value once nothing points to it. Snello has no collector: every value has an owner that frees it, so each job has a tool that says who owns what, and `*T` is the tool for one of them.

| To | Use | Not `*T`, because |
|---|---|---|
| change a struct in a method | a `mut` receiver: `func (c mut Counter) Add(by int)` | the method borrows the caller's struct for the call, and nothing is allocated |
| pass a struct to a function without copying it | `ref T` to read it, `mut T` to change it | a borrow is one word and ends with the call |
| let a value be missing | `T?` | the option holds the struct inline, with a flag; nothing is allocated |
| give back a value a function made | the value itself | the caller becomes its owner; a move copies the struct's bytes once |
| link a recursive type: a list, a tree, an expression | `*T`, or `*T?` where the chain ends | **this is what `*T` is for**: a type cannot hold itself by value |
| share one value between several owners in a task | `rc.Rc<T>`, with `rc.Weak<T>` for a link back | a pointer has one owner and is never copied |
| share one value between tasks | `sync.Arc<T>`, holding a `sync.Mutex<T>` to change it | the same, and the value crosses tasks, so it must be `Send` |
| build a graph: cycles, parent links, nodes that name each other | an arena, `arena.Arena<T>`, and an `arena.Index<T>` for each link (chapter 13) | owned pointers cannot form a cycle |

Each row in code, the struct passed and changed with no pointer, and the pointer only where the type holds itself:

```go
func (c mut Counter) Add(by int) { c.n += by }      // changes the caller's Counter
func describe(c ref Config) string { ... }           // reads it, copies nothing
func rename(c mut Config, to string) { c.name = to } // changes it, copies nothing
func load() Config { return Config{name: "edge".Clone(), ports: []int{80, 443}} } // the caller owns it

struct Server {
    name  string
    proxy Config?               // may be missing, held inline
}

struct Node {
    value string
    next  *Node?                // the one place a pointer is needed
}

shared := rc.New(load())        // two owners in one task
other := shared.Clone()
hits := sync.NewArc(sync.NewMutex(0)) // one counter for every task

people := arena.New<Person>()   // Ann and Bob name each other
ann := people.Insert(Person{name: "Ann".Clone(), friends: []arena.Index<Person>{}})
bob := people.Insert(Person{name: "Bob".Clone(), friends: []arena.Index<Person>{ann}})
```

A Go program also returns `&local` and keeps using it, which works there because the collector keeps the variable alive. Snello has no such pattern: `&` takes no address, it moves a value to the heap, and a borrow of a local cannot leave its function, `cannot return a borrow of local s`. Return the value, and the caller owns it.

One more reason to use `*T` is a cost, measured rather than assumed: a large struct that moves often, kept in a slice that is sorted or handed from owner to owner, moves as one word through a pointer instead of all its bytes, for one allocation each and a load to reach it.

Every row keeps memory safe without a collector in the same way. A pointer has one owner and its value is freed when that owner is dropped (chapter 5). A borrow is checked never to outlive what it borrows (chapter 6). `Rc` and `Arc` free their value with the last handle, and a `Weak` link does not keep it alive. An arena frees its values with the arena, and an `Index` to a value it removed finds nothing.

## Reading through a pointer

A pointer reaches what it holds with a dot, like a borrow does: `n.value`, `n.left`, `p.Describe()`. Passing a pointer where a `ref T` is wanted lends what it holds: `total(head)` with `head` a `*Node` fits `total(n ref Node)`. The pointer itself is written `*p` only to read or replace the whole value: `*boxed` is the `Point` inside `boxed`, and `*cur = Some(&Node{...})` writes through a `mut` borrow of a pointer slot.

A pointer is an owned value like any other. Assigning or passing it by value moves it; a pointer is never copied, since that would make two owners. `==` on two pointers compares what they hold, and printing one prints what it holds, so a pointer is invisible in output: `{1 Some({2 None})}` is a list of two nodes.

## Optional pointers and recursive types

A type cannot hold itself by value, since it would have no finite size; it holds itself through a pointer, a slice or a map. `struct R { r R? }` is refused with `invalid recursive type R: it contains itself; hold it through a pointer, *R`. The link is usually optional, because a chain ends:

```go
struct Node {
    value string
    next  *Node?                 // owned pointer, or None
}
```

`*Node?` reads as "an optional owned pointer to a node"; `?` applies to the pointer, as chapter 8 explains. An expression enum holds its operands the same way, and a `match` reaches them through the pointer with no extra syntax:

```go
enum Expr {
    Lit(f64)
    Neg(*Expr)
    Bin(op Token, lhs *Expr, rhs *Expr)
}

func Eval(e ref Expr) f64 {
    return match e {
        Lit(v)           => v,
        Neg(inner)       => -Eval(inner),
        Bin(Plus, l, r)  => Eval(l) + Eval(r),
        ...
    }
}
```

## Relinking

Taking a node out of a chain and putting it elsewhere is a move of the pointer, with `take` leaving `None` behind so that no slot ever holds a pointer twice:

```go
func (s mut Stack) Pop() string? {
    n := take(mut s.head) ?? return None
    s.head = n.next                   // moving a field out of the owned node n
    s.len -= 1
    return Some(n.value)              // n is dropped at the end of the function
}
```

Walking down a chain of optional pointers to change one slot is a borrow that moves from slot to slot. `cur` is a `mut *Node?`, a mutable borrow of one slot, and each step rebinds it to a slot one level down:

```go
cur := mut t.root
for {
    n := cur ?? { *cur = Some(&Node{key: k, val: v, left: None, right: None}); return }
    if k < n.key {
        cur = mut n.left
    } else if k > n.key {
        cur = mut n.right
    } else {
        n.val = v
        return
    }
}
```

The binary search tree in `examples/bst/bst.snello` inserts, finds and deletes this way, with no node copied and no slot ever shared.

## What a value costs

A pointer is one machine word, and `*Node?` is a word and a flag. `&expr` is one allocation of the value's size, and nothing else in this chapter allocates: moving a pointer copies the word, and reaching through it is one load. The cell is freed when its owner is dropped, which is chapter 5's rule.
