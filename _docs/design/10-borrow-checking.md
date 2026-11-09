# Borrow checking along paths

Status: implemented, in `compiler/types/flow.go`. This note says how the checker decides where a borrow is live: along the paths a function can take, not by the order of its lines. The rules it checks are those of `04-borrows-ref-and-mut.md`; this note is how their liveness is computed, and the rules that computation adds.

## Why paths

A borrow lives from its creation to its last use (rule 1), and while it lives its owner may not be changed, moved or dropped (rules 4 and 6). Deciding "while it lives" from source positions, a borrow held by `r` live at an access when `r` is used at a later position in the file, is simple, and wrong both ways, since positions are not paths.

It accepts programs that read freed memory, which only a `-debug` build would catch at run time:

```go
r := ref s
{
    t := "b".Clone()
    r = ref t
}
fmt.Println(*r)          // t was dropped at the brace
```

```go
for i := 0; i < 2; i++ {
    fmt.Println(*r)      // the second pass reads the t of the first
    t := "loop".Clone()
    r = ref t
}
```

And it refuses a correct program, since the use is at a later position though no path reaches it after the write:

```go
r := ref s
if cond {
    s = "b".Clone()      // fine: no path reaches the read after the write
} else {
    fmt.Println(*r)
}
```

## The decision

The checker records, as it walks a function, a small graph of what happens in it, and decides every borrow's liveness on that graph at the function's end. A borrow is live at a point when some path from that point reaches a use of it. This is how Rust's borrow checker has decided liveness since its non-lexical lifetimes, on its control-flow graph; and it is the computation the Go compiler's `cmd/compile/internal/liveness` makes over its SSA blocks to find the variables live at each call. Nothing is written in the program: the graph is the checker's.

## The graph

A function's body is cut into blocks, straight runs of events, joined by the edges control can take:

| Construct | Edges |
|---|---|
| `if`, `match`, `??`, `select`, `&&`, `\|\|` | from the point before to each arm or case, and from each arm that does not leave to the point after; an `if` with no `else`, and the left side of `&&` and `\|\|`, also go straight past |
| a match guard | from the guard's end into its arm, and on to the next arm, which runs when the guard is false, dropping what the pattern bound on the way |
| a loop | into its head, where each pass begins, then its test, then its body, then back to the head; out from the test, before any of the body; a `for` loop's post statement between the body's end and the head |
| `break`, `continue` | to the point after the loop, or to its post statement or head |
| `return`, a `?` that returns, a call that never returns, `panic` | nowhere, after dropping what every open block declares: nothing after them is used |

A match that binds a part of its value by value moves the value before any arm runs, so the move is recorded where the arms begin. A closure's body is a function of its own, with a graph of its own, which points to the graph of the body it is written in.

The events, each in its block, in the order the program does them:

| Event | Recorded when |
|---|---|
| use of `v` | `v` or any part of it is read |
| loan: `v` holds a borrow of a place, in the part `path` of `v` | `ref s`, `mut s`, a call whose result borrows, a closure that captures, a pattern bound through a borrow, `h.src = ref t` |
| copy: `v` takes the loans another variable holds | a `ref` borrow read out of a variable or a field, `q := r` or `r = h.src`; a moved `mut` borrow, `q := p`; a closure copied, `h := g` |
| assignment to `v`, or to a part of it | `v = e`, `v.f = e`, and a declaration, which gives the variable its first value; recorded after the value is made, since the target is evaluated first and given its value last, as in Go |
| access to a place: read, write, move, `ref` or `mut` | the accesses rule 4 checks |
| drop of `v` | where the block that declares `v` ends, on every path out of it, last declared first, as the backends drop |

A borrow lent for one call or statement, and a borrow of a temporary, end with their call or statement and need no graph: they are checked where they are made.

## Three passes over the graph

Each runs once per function, after its walk, to a fixed point, as Go's liveness pass does.

- **Liveness**, backwards: `v` is live at a point when some path from it reaches a use of `v` before an assignment to the whole of `v` or its drop. The drop of a variable whose type runs a `Drop` that may read what it borrows is a use of it there.
- **Loans in effect**, forwards: a loan is in effect at a point when some path from where it was made reaches the point with no assignment to its holder, or to the part of it the loan is in, on the way. A drop ends a variable's life, not what it holds: a value carried out of its block still holds what the block's names held.
- **Variables dropped**, forwards: the variables some path to a point has dropped and not declared again since.

A loan is **live** at a point when it is in effect there and its holder is live there. An access collides with every live loan of an overlapping place, by the kinds rule 4 gives; a drop of `v` collides with every live loan of `v`, which is rule 6; and a loan made of a variable already dropped, a block's value borrowing the block's own local, is refused. Each collision is reported with where the loan began and the use that keeps it live, the first one a path from the access reaches.

## Reading through a borrow

What a variable can reach through another is fixed when it does so. A borrow made of a place that holds borrows itself, a borrow variable or a struct with a borrow field, copies the loans that place holds at that moment, each in the part of the new holder that reaches it, so the new holder keeps them alive whatever the place is given afterwards, and lets go of each as that part is given another value. Two kinds of borrow are kept apart:

- **A borrow of what a borrow variable points to**, `x := r.Get(k)` or `m := cur ?? return`, keeps the pointee lent; `r` itself may be pointed elsewhere, and the old pointee stays lent through `x`. A `mut` one keeps `r` from being used while it lives: `cannot use cur: cur is borrowed as mut by m`.
- **A borrow of the variable itself**, made by a closure that captures it, reads the variable when the closure is called, so the variable may not be pointed elsewhere while the closure lives: `cannot assign to r: r is borrowed by g`.

A store through a `mut` borrow is a store into what it points to: with `m := mut h`, `m.src = ref t` makes `h` hold `t`, and lets go of what `h.src` held before, when `h` is the one place `m` may point to there; a `mut` borrow that may point to one of two places on some path leaves each as it was.

## What it refuses

The rules the computation adds, each a refusal of a program that would read freed memory, each as Rust refuses it when no lifetime is written:

| Rule | Example | Message |
|---|---|---|
| A local dies at the end of its block | `{ t := ...; r = ref t }; print(*r)` | `cannot keep a borrow of t in r: t is dropped at the end of its block at 21:5, used at 22:18` |
| A loop's body ends on every pass | a borrow of the body's `t` read by the next pass | `... used in the next pass of the loop at 19:22` |
| A block's value may not borrow the block's local | `x := if c { t := ...; ref t } else { ... }` | `cannot keep a borrow of t: t is dropped at the end of its block` |
| A `mut` parameter is the caller's | `func rename(l mut Label, name ref string) { l.text = name }` | `cannot store a borrow of name in l: l is the caller's, and a signature cannot say that it borrows name` |
| A closure may not store a borrow outside itself | `func() { r = ref z }` | `cannot store a borrow in r from inside a closure: r is declared outside it, and what the closure borrows ends with each call` |
| A deferred call borrows only a whole variable, one that is a borrow or holds none | `defer show(ref xs[0])` | `a deferred call borrows only a whole variable, one that is a borrow or holds none: ref xs[0] is not one` |
| A deferred call keeps those until the function returns | `{ t := ...; defer show(ref t) }` | `cannot keep a borrow of t in the deferred call: ..., and the deferred call runs when the function returns` |
| A closure that borrows is used where it is made | `g := if c { func() { use(s) } } else { ... }` | `a closure that borrows cannot be chosen by if or match, or come out of a block: it is used where it is made, bound with :=, called, or passed to a call` |
| A `Drop` uses what its value borrows | `p := Peek{src: ref s}; { t := ...; p.src = ref t }` | `... used when p is dropped at 5:1, by its Drop` |

A store into the caller's places is followed however it is written, through a copy of the parameter, a literal or a field holding a `mut` borrow: each parameter that carries a borrow holds, in the graph, a borrow of a variable standing for what the caller owns behind it, and a borrow stored into that variable, of anything but itself, is refused. A `mut` parameter may still be changed, and may be given what it held already, `l.text = l.title`. A closure that borrows is used where it is made, bound with `:=`, called or passed to a call, so what it borrows is known where it is used; one that borrows nothing goes anywhere. Rust accepts a store like `rename`'s only when a lifetime ties the two parameters, which Snello does not write. A deferred call runs when the function returns, after every other use and before the function's own variables are dropped, so it may borrow them; it borrows only whole variables: one that holds no borrow may be read and changed before then, and only moving or dropping it collides; a borrow variable, the `mut` receiver of `defer s.cleanup()`, is read by the call when it runs, so it may not be pointed elsewhere before, and what it points to is held as the variable is. A type with no `Drop` anywhere in it borrows only until its last use.

## How it stays memory safe without a collector

The rules are those of the borrows note; the checker applies them on every path, not on the order of the lines. It refuses a program whenever some path makes an access that collides with a loan live at that point, drops a value a live loan borrows, borrows a value already dropped, or stores a borrow where the function cannot follow it, which is what a use of freed memory or a write behind a reader is; and where following a borrow would need more than the graph, a closure as a value or a deferred call holding a borrow, the language refuses the program outright. Two reviews of the design probed it with programs that read freed memory under the debug build; each is a case of `TestFlowModel`. It accepts some programs a position check would refuse, each one where no path reaches the conflict. Nothing runs when the program does: no check, no count, no flag on a value. Every value still has one owner and is freed by its `Drop` at a point the compiler knows.

## What stays out

- **Across functions.** A borrow lives inside one function, and a call is checked by its signature alone (rules 2 and 3). A function that stores a callback it was given is a separate question, with its own issue.
- **Annotations.** Nothing is written in the source: where the computation cannot follow a borrow, a store into a `mut` parameter or out of a closure, the program is refused with a message that says what to write instead.

## What it costs

A function's graph has a block per branch and loop, and the passes go over its blocks once, in the order the blocks were made, which is the order control reaches them when there is no loop, and to a fixed point when there is one, with a bit set over the function's variables and another over its loans; a body that holds no borrow is not solved at all, and the graphs are kept for reuse. On the checker's benchmark over the examples, `BenchmarkCheck` in `compiler/types/check_test.go`, a check takes about 330 µs against 200 µs before, with 2,142 allocations against 2,145 and 135 KB against 139 KB: the time is the passes over each body that holds a borrow, and an access recorded for each read once a body holds one; a use is recorded only of a variable whose type can hold a borrow, and no access before the body's first borrow outside a loop, where no later borrow can reach it.

## In the code

`compiler/types/flow.go` builds the graph during the walk, from the hooks the move pass calls, and solves it at the body's end. `borrow.go` keeps the collisions, the access kinds and the messages, and tells a borrow read out of a place from one lent by it (`sources`); `stmt.go` records stores (`stored`, with `refPrefix` for a `mut` borrow held in a field), refuses a closure's store outside itself (`storeRefused`) and a closure that borrows used anywhere but where it is made (`usedWhereMade`), places a `for` loop's post statement (`flowPost`) and holds, or refuses, what a deferred call borrows (`deferStmt`, `deferHold`); `flowParams` and `checkEscape` in `flow.go` follow a store into the caller's places, `copyLoans` gives a copy the loans of its source in the parts that reach them, and `throughEnds` lets a store through a borrow end what the place held; `match.go` chains a guard to the next arm, dropping what the arm bound on the way (`flowGuardFails`), and moves a consumed value before the arms; `pin` in `borrow.go` resolves the names a block's or an arm's value carries before the block ends; `escape.go` follows a returned borrow through the loans in effect at the return. `dropReads` in `predicates.go` says which types' drops read what they borrow. `TestFlowPaths` and `TestFlowModel` in `flow_test.go` test the graph edge by edge and case by case, and the programs `borrow-outlives-block`, `borrow-field-outlives-block`, `borrow-next-pass`, `borrow-out-of-block`, `borrow-into-caller`, `closure-stores-borrow`, `closure-used-where-made`, `defer-outlives-block`, `defer-borrows-whole` and `drop-reads-borrow` under `examples/core/errors/` show the messages.
