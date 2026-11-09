# 6. Borrows: `ref` and `mut`

The design is in `../design/04-borrows-ref-and-mut.md` and section 5 of `../design/05-standard-library.md`. Until the closure environment copies moved values, a closure that captures a local, even by moving it, counts as a borrow of it. The programs under `../../compiler/types/testdata/errors/` show each rule being broken, every message below is the one the compiler prints, and `compiler/emit/internal/backendtest/testdata/closures.snello` shows closures that keep the rules.

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

```go
strings.Fields(ref job.text)          // lend the text for the call; ref is optional here
io.Copy(mut out, mut in)              // both lent mutably for the duration of the copy
x.Describe()                          // a receiver borrows itself: no keyword
```

Passing an owned value to a `ref` parameter borrows it with no keyword, as a receiver already does; writing `ref` makes the borrow visible and means the same thing. An owned pointer is lent as what it holds: `total(head)` with `head` a `*Node` fits a `ref Node` parameter. `mut` is always written when the argument is an owned value, because a visible mutation is worth a word. A borrow variable is passed on as itself, with no keyword, since its mode is already part of its type: `visit(n)` where `n` is a `ref Node`, `bump(a, b)` where both are `mut int`. Receivers follow the parameter they are: `func (t ref Tree) Path()` reads the tree, `func (t mut Tree) AddChild()` changes it, and `func (f File) Close()` takes it.

A borrow can also be bound to a local name, and then it lives from that line to its last use, on any path the function can take:

```go
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

```go
t := s.Clone().Trim()                 // ERROR "cannot keep a borrow of a temporary: s.Clone() is dropped when the statement ends"
```

Bind the clone first, `c := s.Clone(); t := c.Trim()`, and `t` borrows `c`, which lives to the end of the scope. A literal is not a temporary: `t := "  lit  ".Trim()` is fine, since the literal lives for the whole program. A temporary's fields and elements are places in it, read and lent where they are, `len(row().tags)`, `grow(mut row().tags)`, and dropped with it once; none is moved out of it (chapter 5).

Rule 4 is the one you will meet first. A method with a `mut` receiver creates a `mut` borrow, so a `ref` held across the call is a conflict:

```go
xs := []int{1, 2}
r := ref xs
xs.Push(3)                            // ERROR "cannot borrow xs as mut: xs is borrowed as ref"
fmt.Println(r)
```

Move the `Println` before the `Push`, or the `Push` before the `ref`, and it compiles: the borrow `r` ends at its last use. The message names both ends, where `r` was created and the use that keeps it alive: `xs is borrowed as ref by r at 16:5, used at 18:17`. Arguments are evaluated before the receiver is borrowed, so `xs.Push(len(xs))` and `xs.Insert(xs.Pop() ?? 0, 2)` are fine: the borrow each argument takes is over before `Push` takes its `mut`. The same rule is why a slice cannot be grown while a borrow of an element is live, why a map cannot be changed while it is being ranged, and why an iterator cannot be invalidated: each of those is a write during a read, and the compiler rejects it.

A value chosen at run time borrows from every place it may come from: `x := if c { ref s } else { ref z }` holds both `s` and `z`, as `match` does over its arms and `o ?? ref z` over both sides, so neither may change while `x` is used, and `return if c { s } else { ref z }` is refused when `z` is a local.

A borrow a call returns stays lent while it is passed along. In `fmt.Println(m.Get("a"), m.Remove("b"))` the first argument still points into `m` when `Remove` would change it, which could move the very entry being printed, so the call is refused: `cannot borrow m as mut: m is borrowed as ref at 4:14`. Print the borrow in a statement of its own, or turn it into text first, `fmt.Println(fmt.Sprint(m.Get("a")), m.Remove("b"))`. A borrow read into a value before the call takes its `mut` is over, so `m.Insert("b", *(m.Get("a") ?? ref 0) + 1)` is fine.

While a value is lent `mut`, the owner may not even read it: `a := mut n; fmt.Println(n); *a += 1` is `cannot use n: n is borrowed as mut by a`.

## How long a borrow lives

A borrow lives while some path the function can take still reaches a use of it. The compiler follows the paths, not the order of the lines: the arms of an `if` and a `match`, the right side of `&&` and `||`, a loop going round, a `break`, a `continue`, a `return` and a `?` that returns, each of which drops what its open blocks declare. Nothing about it is written. This section is what that means in practice; every rule here refuses a program that could read freed or changed memory.

### Branches

An access on one arm of an `if` collides only with the borrows live on that arm, so a write in one arm and a read in the other are fine:

```go
r := ref s
if full {
    s = "b".Clone()                   // fine: no path reaches the read after the write
} else {
    fmt.Println(*r)
}
```

### Loops

A loop's next pass reaches what its body read before, so a borrow created before a loop and used anywhere inside it is live for the whole loop: `used in the next pass of the loop at 12:9`. A `break` leaves the loop for good: a loop that moves the collection it ranges over and then breaks is fine, since no pass comes round to read it. A range loop's variables are each pass's own, and one that is a borrow of an element keeps the collection lent while it is used.

### Blocks

A local dies at the end of the block that declares it: the closing brace of a block, a loop's body on every pass, an `if` or a `match` arm, or where a `break` or `continue` leaves it. A borrow of it may be kept in a variable declared outside the block, but not used after the block ends:

```go
s := "outer".Clone()
r := ref s
{
    t := "inner".Clone()
    r = ref t                         // ERROR "cannot keep a borrow of t in r: t is dropped at the end of its block at 6:1, used at 7:14"
}
fmt.Println(*r)
```

The message names where `t` is dropped and the use that would read it. Give `r` another borrow before that use, `r = ref s`, and the program is fine. In a loop the same holds pass by pass: a borrow of the body's variable may be used later in the same pass, but not read by the next pass before it is given another borrow, `t is dropped at the end of its block at 22:5, used in the next pass of the loop at 19:22`.

A block's value may not borrow what the block declares, since the block's locals are gone by the time the value is taken: `x := if full { t := "inner".Clone(); ref t } else { ref s }` is `cannot keep a borrow of t: t is dropped at the end of its block`, and so is a literal or a variant that holds such a borrow, `Holder{src: ref t}`. A value that borrows from outside the block, or an arm's binding that borrows the matched value, is carried out whole: `x := match m.Get(k) { Some(v) => v, None => return }` keeps `m` lent while `x` is used.

### Fields and borrows of borrows

A struct holds what is stored in its fields: `l.text = ref t` makes `l` hold `t`, and `l` cannot be used after `t`'s block, whichever field is read, until `l.text` is given another borrow. A store through a `mut` borrow is a store into what it points to: with `m := mut l`, `m.text = ref t` makes `l` hold `t`.

A borrow read out of another, `q := r`, or out of a field, `q := l.text`, holds what that one held when it was read, and `r` may then point elsewhere without freeing it. A `mut` borrow read through another, `m := cur ?? return`, keeps that one lent while it lives: `cannot use cur: cur is borrowed as mut by m`.

### Closures

A closure borrows what it captures where it is made, and calling it is a use of what it borrows; a closure that moves what it captures moves it there. A closure that borrows is used where it is made: bound with `:=`, called, or passed to a call. It cannot be chosen by `if` or `match`, come out of a block, be assigned with `=` or be copied to another variable, since what it borrows would have to be followed from value to value: `a closure that borrows cannot be chosen by if or match, or come out of a block: it is used where it is made, bound with :=, called, or passed to a call`. Choose inside the closure instead, or give each closure its own name. A closure that borrows nothing may go anywhere.

A closure that captures a borrow variable reads the variable when it is called, so the variable may not point elsewhere meanwhile: `cannot assign to r: r is borrowed by g`. A closure may not store a borrow in a variable declared outside it, since what the closure borrows lives only while it is called: `cannot store a borrow in r from inside a closure: r is declared outside it, and what the closure borrows ends with each call`. Return the value from the closure, and assign it where the variable is declared.

### A `mut` parameter is the caller's

A function may change what a `mut` parameter holds, but not store in it a borrow of anything else: the caller cannot see from the signature that the parameter now borrows the argument, and would free the argument while the parameter still points at it.

```go
func rename(l mut Label, name ref string) {
    l.text = name                     // ERROR "cannot store a borrow of name in l: l is the caller's, and a signature cannot say that it borrows name"
}
```

The compiler follows the store however it is written: through a copy of the parameter, `m := l; m.text = name`, inside a literal, `l.inner = Inner{src: name}`, or through a `mut` borrow held in a field of a parameter taken by value. Storing what the parameter held already, `l.text = l.title`, is fine. Take the value by value and own it, or return the new value. Rust refuses the same function unless a lifetime ties the two parameters together; Snello writes no lifetimes, so the function is refused.

### Deferred calls

A deferred call runs when the function returns or panics, after every other use, and before the variables it borrows are dropped, which are declared before it. It borrows only whole variables: its receiver, an argument, or what a deferred closure captures. One that holds no borrow is reached only when the call runs, so it may be read and changed before then, but not moved away or dropped:

```go
t := Tally{n: 1, name: "t".Clone()}
defer t.Add(5)                        // fine: t is reached when the function returns
t.Add(1)
keep(t)                               // ERROR "cannot move t: t is borrowed by the deferred call"
```

A borrow variable, the receiver of `defer s.cleanup()` in a method on `s mut Server`, is read by the call when it runs, so it may not be pointed elsewhere before then, and what it points to is held as it would be by the variable. Anything else may move or die before the call runs, and is refused where the call is deferred: an element, `defer show(ref xs[0])`; what a borrow points to; a value that holds a borrow; a closure that borrows, called or passed: `a deferred call borrows only a whole variable, one that is a borrow or holds none: ref xs[0] is not one`. A local of an inner block is dropped when the block ends, before the call runs: `{ t := "inner".Clone(); defer show(ref t) }` is `cannot keep a borrow of t in the deferred call: t is dropped at the end of its block, and the deferred call runs when the function returns`. Take the value whole, or make the call before the function returns.

### `Drop`

A type with a `Drop` may read what it borrows when it is dropped, so what it borrows must outlive it. Variables are dropped last declared first, so a value declared after the one that borrows it is dropped first:

```go
p := Peek{src: ref s}
{
    t := "inner".Clone()
    p.src = ref t                     // ERROR "cannot keep a borrow of t in p: t is dropped at the end of its block at 5:1, used when p is dropped at 6:1, by its Drop"
}
```

Replacing such a value, `p = Peek{...}`, runs the old one's `Drop` there, which is a use too. A type without a `Drop` anywhere in it borrows only until its last use, like any other.

## Walking a structure

Rule 5 is what makes pointer-chasing code possible. A borrow variable can be re-pointed at a borrow reached through itself, so a `mut` walk down a chain of `*Node?` slots is one loop:

```go
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

```go
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

```go
func (s ref Stack) Peek() ref string? {
    n := s.head ?? return None
    return Some(ref n.value)
}
```

A function may do the same with its only borrowed parameter: `func Trim(s ref string) ref string`. With two borrowed parameters the source is ambiguous and the function cannot be written; return a clone, or an index that says which one:

```go
func Longest(a ref string, b ref string) ref string {    // ERROR "Longest returns a borrow but has two borrowed parameters"
```

This is the price of never writing down how long a borrow lasts: the few functions whose answer is ambiguous are refused, and each has an idiom.

## Structs and closures that hold a borrow

A struct with a `ref` or `mut` field is itself a borrow. It can be built and used inside a function and dropped before the function returns, which is exactly what a lexer needs:

```go
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

### Passing a closure to a function

A closure that borrows may be passed to a function that calls it while it runs, as `SortBy` does:

```go
bias := "ab".Clone()
xs.SortBy(func(a ref int, b ref int) bool { return *a + bias.Len() < *b }) // fine: SortBy only calls it
```

It may not be passed to a function that keeps it, one whose body stores it somewhere that outlives the call:

```go
struct Keeper { f func() string }

func store(k mut Keeper, f func() string) { k.f = f }

func main() {
    k := Keeper{f: func() string { return "".Clone() }}
    {
        s := "hello".Clone()
        r := ref s
        store(mut k, func() string { return (*r).Clone() }) // ERROR "closure captures r by borrow and cannot be passed to store, which keeps f: assigned to k.f at 3:45; pass a closure that borrows nothing, or give it what it needs by value"
    }
    fmt.Println(k.f())                                       // would read s, dropped at the brace
}
```

Nothing in `store`'s signature says it keeps `f`: the compiler reads it from the body. A function keeps a parameter of a function type, or of a type parameter's type, when it stores it in a field, an element, a map or a variant, returns it, sends it, spawns it, gives it to another variable, or passes it to a function that keeps it, in its own body or in a closure written inside it; calling it keeps nothing. Giving the parameter to another variable counts as keeping it, however the variable is then used, so a wrapper that aliases its callback, `g := f; g()`, keeps it; call the parameter directly instead. The message names the line that keeps it. A closure that borrows nothing, or captures only by copy, may be kept. Since a call through a function value cannot see which body it reaches, a function that keeps a parameter can only be called by name, not used as a value, and a closure cannot keep its own parameter; a type's method may keep no more than its trait's declaration of it.

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
| `cannot keep a borrow of t in r: t is dropped at the end of its block at 21:5, used at 22:18`; `... at the end of the loop's pass ...`, `... where its block is left ...`, `... used in the next pass of the loop ...` | `borrow-outlives-block`, `borrow-field-outlives-block`, `borrow-next-pass`, `TestFlowPaths` in `compiler/types/flow_test.go` |
| `cannot keep a borrow of t: t is dropped at the end of its block` | `borrow-out-of-block` |
| `cannot store a borrow of name in l: l is the caller's, and a signature cannot say that it borrows name` | `borrow-into-caller` |
| `cannot store a borrow in r from inside a closure: r is declared outside it, ...`; `... and r outlives the closure` | `closure-stores-borrow` |
| `cannot keep a borrow of t in the deferred call: ..., and the deferred call runs when the function returns` | `defer-outlives-block` |
| `a deferred call borrows only a whole variable, one that is a borrow or holds none: ref xs[0] is not one`, `a deferred call cannot call g: it is a closure that borrows` | `defer-borrows-whole`, `TestFlowModel` |
| `a closure that borrows cannot be chosen by if or match, or come out of a block`, `... assigned with =`, `... copied to another variable` | `closure-used-where-made`, `TestFlowModel` |
| `cannot keep a borrow of t in p: ..., used when p is dropped at 5:1, by its Drop` | `drop-reads-borrow` |
| `a slice cannot hold Lexer: it holds a borrow` | `borrow-struct-in-slice` |
| `closure captures n by borrow and cannot be returned`, `... stored`, `... sent`, `... spawned` | `closure-returned`, `closure-stored`, `TestEscapes` |
| `closure captures r by borrow and cannot be passed to store, which keeps f: assigned to k.f at ...`; `cannot use store as a value: it keeps its parameter f, ...`; `a closure cannot keep its parameter f: ...`; `method On keeps its parameter f, ..., which the trait's On does not` | `callback-kept`, `TestKeeps` |

## For maintainers

- **The rules.** `compiler/types/borrow.go` decides what collides with what: the access kinds, the borrows a value carries (`hold`, `derive`, `sources`, which tells a borrow read out of a place from one lent by it, and walks literals and variants) and the messages. A borrow lent for one call or statement is a temporary there, checked at once. `stored` in `stmt.go` records a borrow put into a field or through a borrow, a `mut` borrow held in a field among them (`refPrefix`); `storeRefused` refuses a closure's body storing one in a variable outside it; `usedWhereMade` refuses a closure that borrows anywhere but where it is made; `deferStmt` and `deferHold` hold the whole variables a deferred call borrows (`atExit`: only a move or a drop collides; a borrow variable may not be pointed elsewhere) and refuse the rest.
- **What a function keeps.** `compiler/types/keep.go` records, while each body is checked, the parameters it keeps (`keepParam`, from the places that store, return, send or spawn a value) and the ones it passes on (`keepArg`), and at the package's end follows them to a fixed point and checks each call that passed a closure that borrows (`resolveKeeps`).
- **The paths.** `compiler/types/flow.go` records each body as a graph while the checker walks it, from the hooks the move pass already calls at every branch and loop (`forkAt`, `branchEnd`, `joinAt`, `enterRegion`, `leaveRegion`, `branch`), with a match guard's way on to the next arm, which drops what the arm's pattern bound, a loop's test apart from its body, a `for` loop's post statement after it, the right side of `&&` and `||` and the error side of `?` as branches, and the drops of a `return`, a `panic` or a call that never returns. At the body's end it solves the graph: liveness backwards, a `Drop` counting as a use; the borrows in effect and the variables that may be dropped forwards; then every access, drop, new borrow and returned borrow against what is live where it is. A declaration is an assignment in the graph; a drop ends a variable's life but not what it holds, so a value carried out of a block, which `pin` resolves before the block ends, holds it still. A borrow made through a place that holds borrows copies what the place holds at that moment, each in the part of the new holder that reaches it, and a store through a `mut` borrow lets go of what the place held when the borrow has one place it may point to. Each parameter that carries a borrow holds a borrow of a variable standing for what the caller owns behind it (`flowParams`), so that `checkEscape` sees a store reaching the caller's places however it is made. A closure's body has a graph of its own, with a pointer to the one around it. The design is `_docs/design/10-borrow-checking.md`.
- **The tests.** `TestFlowPaths` in `flow_test.go` covers each kind of edge, `TestFlowModel` how the graph follows evaluation and each rule, `TestBorrows` and `TestLending` in `borrow_test.go` the rules of chapter 6's first half, `TestDropReads` in `predicates_test.go` which types' drops read their borrows, and the programs under `compiler/types/testdata/errors/` the messages.
