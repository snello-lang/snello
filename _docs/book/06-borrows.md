# 6. Borrows: `ref` and `mut`

Status: decided (`../design/04-borrows-ref-and-mut.md`, `05-standard-library.md` section 5); checked from stage 1, tasks 1.7 and 1.8. The programs under `../../examples/core/errors/` show each rule being broken.

Moving is the right thing when the callee keeps the value. Most functions do not keep it; they look at it, or change it, and give it back. For those, a value is **borrowed**: lent to the callee for the length of the call, with the owner unchanged. A borrow is written `ref T` to read or `mut T` to change.

## The three modes

| Parameter | Meaning | After the call, the caller… |
|---|---|---|
| `func f(v T)` | **Take.** Ownership moves into `f`. | cannot use `v` (moved), unless `T` is `Copy` |
| `func f(v ref T)` | **Look.** A shared, read-only borrow. | still owns `v`, unchanged |
| `func f(v mut T)` | **Change.** An exclusive, mutable borrow. | still owns `v`, possibly changed |

A signature says exactly one of these three things, and the compiler holds the callee to it. A function declared `ref` cannot change its argument; one declared by value cannot be called twice with the same argument.

The choice, in one line: **reading it, `ref`; changing it, `mut`; keeping it, sending it or consuming it, by value.** `Copy` types are passed by value everywhere, and for them the first mode is a plain copy.

## At the call site

```teak
strings.Fields(ref job.text)          // lend the text for the call; ref is optional here
io.Copy(mut out, mut in)              // both lent mutably for the duration of the copy
x.Describe()                          // a receiver borrows itself: no keyword
```

Passing an owned value to a `ref` parameter borrows it with no keyword, as a receiver already does; writing `ref` makes the borrow visible and means the same thing. `mut` is always written when the argument is an owned value, because a visible mutation is worth a word. A borrow variable is passed on as itself, with no keyword, since its mode is already part of its type: `visit(n)` where `n` is a `ref Node`, `bump(a, b)` where both are `mut int`. Receivers follow the parameter they are: `func (t ref Tree) Path()` reads the tree, `func (t mut Tree) AddChild()` changes it, and `func (f File) Close()` takes it.

A borrow can also be bound to a local name, and then it lives from that line to the last line that uses the name:

```teak
n := ref t.nodes[id]                  // n is a borrow of one node, no copy
if !n.live { return }
visit(n)
```

Through a borrow, fields and methods are reached with a dot; no `*` is written. Replacing the whole value behind a `mut` is the one place `*` appears: `*cur = Some(&Node{...})`.

## The rules

The compiler enforces six rules. They are what make it possible to never write down how long a borrow lasts: every borrow has an obvious owner, and the compiler finds it.

1. **A borrow lives inside one function, from its creation to its last use.** A borrow passed to a callee ends when the callee returns. The compiler computes this; nothing is written.
2. **A borrow cannot be stored or sent.** Not in a field of an owned struct, not in a slice, not on a channel, not captured by `spawn`. A struct or closure that holds a borrow is itself a borrow and obeys the same rules.
3. **A borrow can be returned only from one obvious source**: the receiver, for a `ref` or `mut` method, or the only borrowed parameter. A function with two borrowed inputs and a borrowed result is rejected.
4. **Any number of `ref`, or exactly one `mut`.** While a value is borrowed `mut`, nothing else may look at it, including its owner. While it is borrowed `ref`, any number of other `ref` borrows may exist, but nothing may change it.
5. **A borrow passes on narrower or equal.** A `mut T` can be handed to a function wanting `ref T` or `mut T`; a `ref T` only as `ref T`. A borrow of a field or element can be taken from a borrow of the whole, and a borrow variable may be reassigned to a borrow derived from itself.
6. **The owner outlives every borrow.** This follows from the others: a borrow never leaves the function that can see its owner, except by rule 3, riding on an input borrow the caller already holds.

Rule 4 is the one you will meet first. A method with a `mut` receiver creates a `mut` borrow, so a `ref` held across the call is a conflict:

```teak
xs := []int{1, 2}
r := ref xs
xs.Push(3)                            // ERROR "cannot borrow xs as mut: xs is borrowed as ref"
fmt.Println(r)
```

Move the `Println` before the `Push`, or the `Push` before the `ref`, and it compiles: the borrow `r` ends at its last use. Arguments are evaluated before the receiver is borrowed, so `xs.Push(xs.Len())` is fine: the `ref` taken by `Len` is over before `Push` takes its `mut`. The same rule is why a slice cannot be grown while a borrow of an element is live, why a map cannot be changed while it is being ranged, and why an iterator cannot be invalidated: each of those is a write during a read, and the compiler rejects it.

## Walking a structure

Rule 5 is what makes pointer-chasing code possible. A borrow variable can be re-pointed at a borrow reached through itself, so a `mut` walk down a chain of `*Node?` slots is one loop:

```teak
func (t mut Tree) Insert(k int, v string) {
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
}
```

Each `cur = mut n.left` ends the old borrow and starts one derived from it, with the same owner behind it. The same shape with `ref` is `Get`.

Two `mut` borrows into one slice at the same time are rejected, because the compiler cannot prove `i` and `j` differ:

```teak
a := mut xs[i]
b := mut xs[j]                        // ERROR "cannot borrow xs[j] as mut: xs is already borrowed as mut"
```

The idiom is to copy the needed values out first, then write. `Remove` in the core tree clones a child list before recursing for exactly this reason. Two `mut` borrows of different fields of one struct are fine; the fields are known to be distinct.

## Returning a borrow

A method may return a borrow into its receiver, and the result lives as long as the receiver borrow does:

```teak
func (s ref Stack) Peek() ref string? {
    n := s.head ?? return None
    return Some(ref n.value)
}
```

A function may do the same with its only borrowed parameter: `func Trim(s ref string) ref string`. With two borrowed parameters the source is ambiguous and the function cannot be written; return a clone, or an index that says which one:

```teak
func Longest(a ref string, b ref string) ref string {    // ERROR "Longest returns a borrow but has two borrowed parameters"
```

This is the price of never writing down how long a borrow lasts: the few functions whose answer is ambiguous are refused, and each has an idiom.

## Structs and closures that hold a borrow

A struct with a `ref` or `mut` field is itself a borrow. It can be built and used inside a function and dropped before the function returns, which is exactly what a lexer needs:

```teak
struct Lexer {
    src ref string          // the Lexer is therefore a borrow
    pos int
}

func Parse(src ref string) Result<Expr, ParseError> {
    p := Parser{lex: Lexer{src: src, pos: 0}, cur: EOF}
    ...
}
```

It is returned only as a borrow is, tied to the receiver or to the only borrowed parameter, so `NewLexer(src ref string) Lexer` is fine and `text.Fields()` can return an iterator that borrows `text`. It cannot be stored in owned data or sent: `a slice cannot hold Lexer: it holds a borrow`. A closure that reads or assigns a local captures it by borrow and is a borrow in the same way; one that captures only owned or `Copy` values, or moves what it captures, may be stored and returned. A `counter()` that returns a closure incrementing a local is rejected, because the local dies when `counter` returns; the state goes into a struct with a method instead.

## What cannot be written

| Not expressible | Idiom instead |
|---|---|
| A function returning one of two borrowed inputs | Return a clone, or an index |
| An owned value holding a borrow: `[]ref T`, a stored callback that captures a local | An iterator (a borrow-holding struct), indexes, or own the data |
| A self-referential struct | An arena with indexes (chapter 13) |
| Lending a local to a spawned task | Move it in, or share it through `sync.Arc` (chapter 10) |

## What the compiler reports

| Message | Program |
|---|---|
| `cannot borrow xs as mut: xs is borrowed as ref` | `mut-while-ref` |
| `cannot borrow n as mut: n is already borrowed as mut` | `two-mut`, `two-slots` |
| `cannot assign to s: s is borrowed`, `cannot move s: s is borrowed` | `assign-while-borrowed`, `move-while-borrowed` |
| `cannot borrow xs as mut: xs is a ref borrow` | `ref-as-mut` |
| `cannot assign to n.value through a ref borrow` | `assign-through-ref`, `mut-through-ref-receiver` |
| `a slice cannot hold a borrow`, `a channel cannot carry a borrow`, `a spawned task cannot hold a borrow` | `borrow-in-slice`, `borrow-on-channel`, `borrow-across-spawn` |
| `Longest returns a borrow but has two borrowed parameters` | `return-two-borrows` |
| `cannot return a borrow of local s` | `return-local-borrow` |
| `a slice cannot hold Lexer: it holds a borrow` | `borrow-struct-in-slice` |
| `closure captures n by borrow and cannot be returned` | `closure-returned`, `closure-stored` |
