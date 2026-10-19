# 7. Owned pointers and recursive data

Status: implemented, checked (stage 1, task 1.4) and compiled to native code (stage 1, task 1.5). `examples/run/boxes.teak`, `examples/core/list.teak`, `bst.teak` and `calc.teak` exercise this chapter. What a pointer owns is freed when the pointer is dropped (stage 1, task 1.9).

A value of type `*T` is an owned pointer: it holds a `T` on the heap and is the one owner of it. `&expr` makes one, moving the value out of the expression into a fresh heap cell:

```teak
s.head = Some(&Node{value: v, next: old})       // &x allocates x on the heap
```

That is the only way a pointer is made, and `&` is the only expression in Teak that allocates by itself. There is no address-of: a pointer never points into a variable, a field or an element, so it can never dangle. To refer to a place without owning it, use a borrow (chapter 6).

## Reading through a pointer

A pointer reaches what it holds with a dot, like a borrow does: `n.value`, `n.left`, `p.Describe()`. Passing a pointer where a `ref T` is wanted lends what it holds: `total(head)` with `head` a `*Node` fits `total(n ref Node)`. The pointer itself is written `*p` only to read or replace the whole value: `*boxed` is the `Point` inside `boxed`, and `*cur = Some(&Node{...})` writes through a `mut` borrow of a pointer slot.

A pointer is an owned value like any other. Assigning or passing it by value moves it; a pointer is never copied, since that would make two owners. `==` on two pointers compares what they hold, and printing one prints what it holds, so a pointer is invisible in output: `{1 Some({2 None})}` is a list of two nodes.

## Optional pointers and recursive types

A type cannot hold itself by value, since it would have no finite size; it holds itself through a pointer, a slice or a map. `struct R { r R? }` is refused with `invalid recursive type R: it contains itself; hold it through a pointer, *R`. The link is usually optional, because a chain ends:

```teak
struct Node {
    value string
    next  *Node?                 // owned pointer, or None
}
```

`*Node?` reads as "an optional owned pointer to a node"; `?` applies to the pointer, as chapter 8 explains. An expression enum holds its operands the same way, and a `match` reaches them through the pointer with no extra syntax:

```teak
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

```teak
func (s mut Stack) Pop() string? {
    n := take(mut s.head) ?? return None
    s.head = n.next                   // moving a field out of the owned node n
    s.len -= 1
    return Some(n.value)              // n is dropped at the end of the function
}
```

Walking down a chain of optional pointers to change one slot is a borrow that moves from slot to slot. `cur` is a `mut *Node?`, a mutable borrow of one slot, and each step rebinds it to a slot one level down:

```teak
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

The binary search tree in `examples/core/bst.teak` inserts, finds and deletes this way, with no node copied and no slot ever shared.

## What a value costs

A pointer is one machine word, and `*Node?` is a word and a flag. `&expr` is one allocation of the value's size, and nothing else in this chapter allocates: moving a pointer copies the word, and reaching through it is one load. The cell is freed when its owner is dropped, which is chapter 5's rule.
