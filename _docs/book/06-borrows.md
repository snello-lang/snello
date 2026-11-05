# 6. Borrows: `ref` and `mut`

Status: decided (`../design/04-borrows-ref-and-mut.md`, `05-standard-library.md` section 5); borrows and closures compile (stage 1, task 1.5); all six rules are checked (stage 1, tasks 1.7 and 1.8, `snello check`). Until the closure environment copies moved values, a closure that captures a local, even by moving it, counts as a borrow of it. The programs under `../../examples/core/errors/` show each rule being broken, every message below is the one the compiler prints, and `examples/run/closures.snello` shows closures that keep the rules.

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

```snello
strings.Fields(ref job.text)          // lend the text for the call; ref is optional here
io.Copy(mut out, mut in)              // both lent mutably for the duration of the copy
x.Describe()                          // a receiver borrows itself: no keyword
```

Passing an owned value to a `ref` parameter borrows it with no keyword, as a receiver already does; writing `ref` makes the borrow visible and means the same thing. An owned pointer is lent as what it holds: `total(head)` with `head` a `*Node` fits a `ref Node` parameter. `mut` is always written when the argument is an owned value, because a visible mutation is worth a word. A borrow variable is passed on as itself, with no keyword, since its mode is already part of its type: `visit(n)` where `n` is a `ref Node`, `bump(a, b)` where both are `mut int`. Receivers follow the parameter they are: `func (t ref Tree) Path()` reads the tree, `func (t mut Tree) AddChild()` changes it, and `func (f File) Close()` takes it.

A borrow can also be bound to a local name, and then it lives from that line to the last line that uses the name:

```snello
n := ref t.nodes[id]                  // n is a borrow of one node, no copy
if !n.live { return }
visit(n)
```

Through a borrow, fields and methods are reached with a dot; no `*` is written. Replacing the whole value behind a `mut` is the one place `*` appears: `*cur = Some(&Node{...})`, and for a `mut int` named `n`, `*n += 1`. Writing `n = 5`, `n += 1` or `n++` instead is an error, because assigning to a borrow variable rebinds it to another place, as in `cur = mut n.left`.

## The rules

The compiler enforces six rules. They are what make it possible to never write down how long a borrow lasts: every borrow has an obvious owner, and the compiler finds it.

1. **A borrow lives inside one function, from its creation to its last use.** A borrow passed to a callee ends when the callee returns. The compiler computes this; nothing is written.
2. **A borrow cannot be stored or sent.** Not in a field of an owned struct, not in a slice, not on a channel, not captured by `spawn`. A struct or closure that holds a borrow is itself a borrow and obeys the same rules.
3. **A borrow can be returned only from one obvious source**: the receiver, for a `ref` or `mut` method, or the only borrowed parameter. A function with two borrowed inputs and a borrowed result is rejected.
4. **Any number of `ref`, or exactly one `mut`.** While a value is borrowed `mut`, nothing else may look at it, including its owner. While it is borrowed `ref`, any number of other `ref` borrows may exist, but nothing may change it.
5. **A borrow passes on narrower or equal.** A `mut T` can be handed to a function wanting `ref T` or `mut T`; a `ref T` only as `ref T`. A borrow of a field or element can be taken from a borrow of the whole, and a borrow variable may be reassigned to a borrow derived from itself. Writing `mut` does not widen a `ref`: `mut r` where `r` is a `ref`, and `mut xs[0:2]` where `xs` is a `ref []int`, are refused, while `mut xs[0:2]` of a slice the function owns, or holds `mut`, lends that piece for changing.
6. **The owner outlives every borrow.** This follows from the others: a borrow never leaves the function that can see its owner, except by rule 3, riding on an input borrow the caller already holds.

Rule 6 reaches one value the others do not name: a temporary, a value no variable holds, such as the result of `s.Clone()` in `s.Clone().Trim()`. A temporary is dropped when its statement ends, so a borrow of it can be used inside the statement, `fmt.Println(s.Clone().Trim())`, ranged over, matched on, but not kept in a variable or returned:

```snello
t := s.Clone().Trim()                 // ERROR "cannot keep a borrow of a temporary: s.Clone() is dropped when the statement ends"
```

Bind the clone first, `c := s.Clone(); t := c.Trim()`, and `t` borrows `c`, which lives to the end of the scope. A literal is not a temporary: `t := "  lit  ".Trim()` is fine, since the literal lives for the whole program. A temporary's fields and elements are places in it, read and lent where they are, `len(row().tags)`, `grow(mut row().tags)`, and dropped with it once; none is moved out of it (chapter 5).

Rule 4 is the one you will meet first. A method with a `mut` receiver creates a `mut` borrow, so a `ref` held across the call is a conflict:

```snello
xs := []int{1, 2}
r := ref xs
xs.Push(3)                            // ERROR "cannot borrow xs as mut: xs is borrowed as ref"
fmt.Println(r)
```

Move the `Println` before the `Push`, or the `Push` before the `ref`, and it compiles: the borrow `r` ends at its last use. The message names both ends, where `r` was created and the use that keeps it alive: `xs is borrowed as ref by r at 16:5, used at 18:17`. Arguments are evaluated before the receiver is borrowed, so `xs.Push(len(xs))` and `xs.Insert(xs.Pop() ?? 0, 2)` are fine: the borrow each argument takes is over before `Push` takes its `mut`. The same rule is why a slice cannot be grown while a borrow of an element is live, why a map cannot be changed while it is being ranged, and why an iterator cannot be invalidated: each of those is a write during a read, and the compiler rejects it.

A value chosen at run time borrows from every place it may come from: `x := if c { ref s } else { ref z }` holds both `s` and `z`, as `match` does over its arms and `o ?? ref z` over both sides, so neither may change while `x` is used, and `return if c { s } else { ref z }` is refused when `z` is a local.

A borrow a call returns stays lent while it is passed along. In `fmt.Println(m.Get("a"), m.Remove("b"))` the first argument still points into `m` when `Remove` would change it, which could move the very entry being printed, so the call is refused: `cannot borrow m as mut: m is borrowed as ref at 4:14`. Print the borrow in a statement of its own, or turn it into text first, `fmt.Println(fmt.Sprint(m.Get("a")), m.Remove("b"))`. A borrow read into a value before the call takes its `mut` is over, so `m.Insert("b", *(m.Get("a") ?? ref 0) + 1)` is fine.

While a value is lent `mut`, the owner may not even read it: `a := mut n; fmt.Println(n); *a += 1` is `cannot use n: n is borrowed as mut by a`. A loop counts a use on its next pass, so a borrow created before a loop and used anywhere inside it is live for the whole loop: `used in the next pass of the loop at 12:9`.

## Walking a structure

Rule 5 is what makes pointer-chasing code possible. A borrow variable can be re-pointed at a borrow reached through itself, so a `mut` walk down a chain of `*Node?` slots is one loop:

```snello
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

Each `cur = mut n.left` ends the old borrow and starts one derived from it, with the same owner behind it. The type of `cur` is `mut (*Node?)`, a mutable borrow of an optional pointer. The same shape with `ref` is `Get`.

Two `mut` borrows into one slice at the same time are rejected, because the compiler cannot prove `i` and `j` differ:

```snello
a := mut xs[i]
b := mut xs[j]                        // ERROR "cannot borrow xs[j] as mut: xs is already borrowed as mut"
```

The idiom is to copy the needed values out first, then write. `Remove` in the core tree clones a child list before recursing for exactly this reason. Two `mut` borrows of different fields of one struct are fine; the fields are known to be distinct.

## Matching through a borrow

A `match`, an `if` binding and `??` bind what is inside the value they look at, and the binding keeps the borrow of the value. On a borrow variable the bindings are borrows of the same kind: through `s mut Slot`, `Full(p)` makes `p` a `mut Point`, and `p.Shift(1, 1)` changes the point in the slot. On a place reached through a borrow, `t.nodes[id].parent` inside a method on `t mut Tree`, a binding copies what `:=` would copy and borrows the rest: `if parent := t.nodes[id].parent` makes `parent` a plain `NodeID`, free to index `t.nodes` again, while `if n := t.nodes[id].next` makes `n` a `mut *Node` that keeps `t.nodes` lent. To change a `Copy` payload in place, write the borrow out, as a call does: `match mut xs[0] { Full(n) => *n += 1, Vacant => {} }`. An option a call returns lends what the call lent: `if v := a.Get(i)` keeps `a` lent for as long as `v` is used, exactly as `v := a.Get(i).Expect("there")` does, so removing from `a` inside the body is refused while `v` is still to be read. A function's result lends the argument passed to its one borrowed parameter, wherever that parameter stands: `if v := pick(0, ref m)` keeps `m` lent, as a method's result keeps its receiver. The `else` branch and the `None` arm hold nothing, since the binding is not in scope there.

A `mut` binding through a map read is refused, because `m[k]` for a key the map does not hold reads the zero value and a write to it would go nowhere: `cannot bind mut through m[k]: a key the map does not hold reads the zero value; use m.GetMut(k)`.

`Expect`, `Unwrap` and `UnwrapOr` take the optional or `Result` they open, as `Close` takes a file, so `o.Expect("present")` moves `o` unless its type is `Copy`.

## Returning a borrow

A method may return a borrow into its receiver, and the result lives as long as the receiver borrow does:

```snello
func (s ref Stack) Peek() ref string? {
    n := s.head ?? return None
    return Some(ref n.value)
}
```

A function may do the same with its only borrowed parameter: `func Trim(s ref string) ref string`. With two borrowed parameters the source is ambiguous and the function cannot be written; return a clone, or an index that says which one:

```snello
func Longest(a ref string, b ref string) ref string {    // ERROR "Longest returns a borrow but has two borrowed parameters"
```

This is the price of never writing down how long a borrow lasts: the few functions whose answer is ambiguous are refused, and each has an idiom.

## Structs and closures that hold a borrow

A struct with a `ref` or `mut` field is itself a borrow. It can be built and used inside a function and dropped before the function returns, which is exactly what a lexer needs:

```snello
struct Lexer {
    src ref string          // the Lexer is therefore a borrow
    pos int
}

func Parse(src ref string) Result<Expr, ParseError> {
    p := Parser{lex: Lexer{src: src, pos: 0}, cur: EOF}
    ...
}
```

It is returned only as a borrow is, tied to the receiver or to the only borrowed parameter, so `NewLexer(src ref string) Lexer` is fine and `text.Fields()` can return an iterator that borrows `text`. It cannot be stored in owned data or sent: `a slice cannot hold Lexer: it holds a borrow`, reported where the slice type is written, as `a channel cannot carry a borrow` is; nor be the type argument of a generic type, or of a generic function that takes or returns a value of that type, `a type argument cannot hold Lexer: it holds a borrow`. A generic function that only borrows it, `fmt.Fprintln(mut w, ...)` with a `bufio.Writer`, is fine (chapter 12). A closure that reads a local that is not `Copy`, or assigns a local, captures it by borrow and is a borrow in the same way: it may be passed to a call, bound to a variable and called, but not stored in a field, slice or map, sent, spawned or returned. A `Copy` value the closure only reads is copied in when the closure is made, so the closure holds no borrow and may go anywhere; a function value is never copied in, since what it borrows is known by the variable holding it, not by its type, so a closure calling another closure borrows it: `adder(10)` returns `func(x int) int { return x + offset }`, and `snap := func() int { return n }` keeps the `n` of that moment, whatever is assigned to `n` after. One that captures nothing may go anywhere too; one that moves what it captures owns it, and will be allowed everywhere once the compiler copies moved values into the closure. A `counter()` that returns a closure incrementing a local is rejected, because the closure assigns the local, which dies when `counter` returns; the state goes into a struct with a method instead. A variable bound to a closure holds its borrows, so `g := func() { xs.Push(1) }` keeps `xs` lent `mut` until the last use of `g`.

## What cannot be written

| Not expressible | Idiom instead |
|---|---|
| A function returning one of two borrowed inputs | Return a clone, or an index |
| An owned value holding a borrow: `[]ref T`, a stored callback that captures a local | An iterator (a borrow-holding struct), indexes, or own the data |
| A self-referential struct | An arena with indexes (chapter 13) |
| Lending a local to a spawned task | Move it in, or share it through `sync.Arc` (chapter 10) |

## What the compiler reports

Every message about a live borrow ends with where it began and the use that keeps it alive: `by r at 16:5, used at 18:17`, or `by the range loop at 20:5` for a collection a loop is ranging over.

| Message | Program |
|---|---|
| `cannot borrow xs as mut: xs is borrowed as ref` | `mut-while-ref` |
| `cannot borrow n as mut: n is already borrowed as mut` | `two-mut`, `two-slots` |
| `cannot borrow n as ref: n is already borrowed as mut`, `cannot use n: n is borrowed as mut` | `TestBorrows` in `compiler/types/borrow_test.go` |
| `cannot assign to s: s is borrowed`, `cannot move s: s is borrowed` | `assign-while-borrowed`, `move-while-borrowed` |
| `cannot use xs (ref []int) as mut []int` | `ref-as-mut` |
| `cannot assign to n.value through a ref borrow` | `assign-through-ref` |
| `cannot borrow t.nodes as mut through a ref receiver` | `mut-through-ref-receiver` |
| `cannot borrow r as mut: r is a ref borrow`, `cannot borrow xs[0:1] as mut: it is a slice of xs, reached through a ref borrow`, `cannot borrow m as mut: m is borrowed as ref at ...` for a borrow passed beside it | `TestLending` in `compiler/types/borrow_test.go` |
| `cannot bind mut through m[k]: ... use m.GetMut(k)` | `TestBorrows` |
| `a slice cannot hold a borrow`, `a channel cannot carry a borrow`, `a spawned task cannot hold a borrow` | `borrow-in-slice`, `borrow-on-channel`, `borrow-across-spawn` |
| `Longest returns a borrow but has two borrowed parameters` | `return-two-borrows` |
| `cannot return a borrow of local s`, `cannot return a borrow of m: a method's returned borrow is tied to its receiver` | `return-local-borrow`, `TestEscapes` in `compiler/types/escape_test.go` |
| `cannot keep a borrow of a temporary: s.Clone() is dropped when the statement ends`, `cannot return a borrow of a temporary: ...` | `TestBorrows`, `TestEscapes` |
| `a slice cannot hold Lexer: it holds a borrow` | `borrow-struct-in-slice` |
| `closure captures n by borrow and cannot be returned`, `... stored`, `... sent`, `... spawned` | `closure-returned`, `closure-stored`, `TestEscapes` |
