# 5. Ownership: moves, `Copy`, `take` and `Drop`

`examples/run/drops.snello` prints the order in which values are dropped, `snello build -debug` checks the drops as the program runs, and `bench/memory.snello` measures what freeing without a collector buys, over the runtime's own allocator. The programs under `../../examples/core/errors/` show each rule being broken, and every message below is the one the compiler prints, followed by where the value moved: `use of moved value s; moved at 10:9`.

Snello has no garbage collector. Memory is freed at a point the compiler knows, and the rule that makes that possible is ownership: **every value has exactly one owner, and the value is freed when its owner goes out of scope.** The owner is a variable, a field, an element, or a temporary; the compiler knows which, so every free is decided at compile time and costs nothing at run time. This chapter is what that rule asks of you.

## The model in three rules

Think of every value as a physical object, a notebook. The ownership model is the set of rules about who holds the notebook and who may read or write in it, and there are three.

1. **One owner at a time.** Exactly one variable holds the value. Passing it to a function or storing it in a struct hands the notebook over; the old variable is empty afterwards, and using it is a compile error. To keep a copy you say so, with `Clone`. Small values such as numbers copy silently; they are the `Copy` types.
2. **Lending instead of handing over.** Most functions only need to look at a value, or change it in place and give it back, so instead of handing the notebook over you lend it: `ref` lends it for reading, `mut` for writing, and it comes back when the function returns. The one rule about lending is any number of readers or exactly one writer, never both, and while a writer holds the notebook even the owner keeps their hands off. Chapter 6 is about lending.
3. **The owner cleans up.** When the owning variable goes out of scope the value is freed, and everything inside it with it. A value that must do something on the way out, close a file, remove a directory, has a `Drop` method, and the compiler calls it on that line. There is no garbage collector because the compiler already knows the line where every value dies.

That is the whole model; the rest of this chapter and the next are its consequences. What it buys is the speed of a program that frees its own memory with the three classic mistakes ruled out before the program runs: using a value after it was freed, freeing it twice, and forgetting to free it. Each has one answer the compiler can compute, who owns this, so each is a compile error or impossible.

## Moving

Assigning a value gives it a new owner. The old name no longer owns anything and cannot be used:

```go
s := "hello"
t := s
fmt.Println(s)                        // ERROR "use of moved value s"
```

The same happens whenever a value goes somewhere that keeps it: passed to a parameter declared by value, returned, stored in a field or a slice, sent on a channel, or captured by a task. Each of these is a **move**. Nothing is copied; the value now lives under its new owner and the old name is dead. The compiler reports the use and the move together, and the message is the same whatever moved it, including a closure that consumes the variable and a method that consumes its receiver, such as `f.Close()`. Such a method cannot be called on a borrow of a value that moves, since the borrower has no value to give: `cannot move f out of a borrow`.

Most code never notices. A value usually flows forward: built here, handed to the function that needs it, kept there. The state machine is the clearest case. `step` takes the old state and the event by value and builds the new state from their parts. The session inside moves from `Open` to `Draining` with nothing copied:

```go
func step(s State, ev Event) State {
    return match (s, ev) {
        (Open(sess), Shutdown)  => Draining(sess, 5),
        (Draining(_, _), Timeout) => Closed,       // session dropped here
        (s, _)                  => s,              // ignored event: state moves straight back out
    }
}
```

When you do want both names, say so: `t := s.Clone()` copies the string and both are owned. There is no hidden copy anywhere; `Clone` is the only way to get one.

## `Copy` types

Numbers and `bool` are copied, not moved. `y := x` on an `int` leaves `x` usable. A struct or enum whose fields are all of such types is a `Copy` type too; the compiler works this out from the fields, and nothing is declared:

```go
enum Token {                                    // Copy: every payload is Copy
    Num(f64)
    Plus
    Minus
}
```

```go
op := p.cur                                 // Token is Copy: a copy, not a move
```

A struct with a string or a slice in it is not `Copy`, because those own memory. The rule to keep in mind: **if copying it would duplicate something that must be freed, it moves.**

A type may say it anyway, `struct Edge impl Copy { from, to int }`, and then the compiler holds it to its word: a field that moves, or a `Drop` method, is reported at the declaration, `Edge does not implement Copy: field name has type string, which is not Copy`. Chapter 11 has the other traits a type can list this way.

## Moves in branches and loops

A move on one path counts on every path after they join, and a move inside a loop happens on every iteration:

```go
s := "hello"
if len(s) > 3 {
    keep(s)
}
fmt.Println(s)                        // ERROR "use of moved value s"

for i := 0; i < 3; i += 1 {
    keep(s)                           // ERROR "s moved in a previous iteration of the loop"
}
```

Assigning to a moved variable gives it a value again, and it is usable from there on. After paths join it is usable only if every path gave it one: `if c { s = "b" } else { s = "c" }` revives `s`, `if c { s = "b" }` alone does not, and neither does a `match` with an arm that assigns nothing. Giving back a field clears a move of that field, and giving back the whole clears every part. Inside a loop body an assignment counts for the rest of that iteration only, since a loop with a condition may run its body no times; a `for` with no condition runs it, so what every `break` path gave back is usable after the loop. The check at the end of a loop body is simpler than this: a value declared before the loop and moved at the end of the body is reported, even where the next iteration would give it back before using it. An assignment inside a closure gives nothing back to the function around it, since the closure may never run.

A move on a path that leaves the function does not count for the paths that stay. This is what makes `??` with a `return` work: the key moves into the error only when the else runs, and is still owned on the line after:

```go
text := kv.Get(ref key) ?? return Err(Missing(key))   // key moves only if the else runs
n := atoi(text) ?? return Err(BadValue(key, text.Clone()))
```

## Moving out of a container

Indexing an owned slice or map does not move the element out; it lends it (chapter 6). To take an element, use a method that gives it up, `xs.Remove(i)`, `xs.Pop()`, `m.Remove(k)`, or `take`:

```go
first := xs[0]                        // ERROR "cannot move xs[0] out of the slice"
v := take(mut q.buf[q.head])          // moves the element out and leaves None behind
```

A part of a value no name holds stays where it is too: the field of a value a call lent, `pick(mut xs).name` where `pick` returns `mut Node`, is the borrowed value's, and the field of a value a call returned, `row().name`, or of the cell a returned pointer points at, `(*mk()).name`, belongs to a temporary that is dropped when the statement ends, which would drop the part twice. Every operation that takes a value is refused on such a part: an assignment, an argument passed by value, `if v := row().note`, a `match` arm binding by value, `row().note ?? ""`, `row().note?`, and `range` over `row().tags`. Bind the temporary to a name first, `r := row(); s := r.name`, and the move is from `r`. Reading the part where it is, or lending it, is fine, and the whole temporary is dropped once when the statement ends: `len(row().tags)`, `match row().name { ... }`, `if v := ref row().note`, `for _, t := range ref row().tags`, `grow(mut row().tags)`.

```go
s := pick(mut xs).name                // ERROR "cannot move pick(...).name out of a borrow"
n := (*mk()).name                     // ERROR "cannot move (*mk()).name out of a temporary: bind the temporary to a name first"
t := row().name                       // ERROR "cannot move row().name out of a temporary: bind the temporary to a name first"
fmt.Println(row().name, len(row().tags))   // fine: both are read where they are
```

`take(mut place)` moves the value out of a field or an element and leaves the type's zero value in its place: `None` for an optional, `""` for a string, `0` for a number. A channel handle, an owned pointer, a function value and a type with a `Drop` have no zero value, chapter 18, so `take` refuses them and any struct, tuple, array or enum holding one: `cannot take h.ch: chan int has no zero value to leave behind; replace(mut h.ch, v) leaves v`. `replace(mut place, v)` moves the value out and leaves `v` in its place, for every type; or make the field optional, `(chan int)?`, and `take` leaves `None`. It is how a linked structure is rebuilt without a temporary placeholder:

```go
func (s mut Stack) Push(v string) {
    old := take(mut s.head)
    s.head = Some(&Node{value: v, next: old})
    s.len += 1
}
```

Ranging over an owned collection by value consumes it, moving each element out in turn. Ranging over a borrow lends each element: `for k, v := range counts` moves the keys and values out of the map, and `for _, c := range ref list` reads the list and keeps it.

## Partial moves

A field may be moved out of an owned value, leaving the rest in place, as long as the whole value is never used again. The usual case is a heap node that is about to be dropped:

```go
func (s mut Stack) Pop() string? {
    n := take(mut s.head) ?? return None
    s.head = n.next                   // moving a field out of the owned node n
    s.len -= 1                        // is allowed because n is dropped at the
    return Some(n.value)              // end of this function
}
```

Using `n` as a whole after `n.next` moved out would be an error: `use of partially moved value n`.

## Dropping

When an owner goes out of scope, its value is freed, and so is everything the value owns, recursively. A tree of `*Node?` links is freed by dropping its root. To drop a value before its scope ends, discard it: `_ = conn` moves `conn` into the blank and drops it there, so a later use of `conn` is a use of a moved value and the end of the scope drops nothing. A struct that must do something when it is freed, close a file, remove a directory, implements `Drop`:

```go
struct TempDir impl Drop { path string }

func (t mut TempDir) Drop() { os.RemoveAll(ref t.path) }

func main() {
    tmp := NewTempDir().Expect("tempdir")
    defer fmt.Println("done")
    ...
    // the deferred print runs, then tmp is dropped here: directory removed

}
```

`Drop` runs at the end of the scope, on an early `return`, and when a panic ends the program: the message is printed, then every value still owned is dropped, innermost first, so the temporary directory above is removed even when something between its creation and the end of `main` panics. `defer` is still there for cleanup that belongs to no value, and the two compose: the deferred calls run first, then the scope's values are dropped, since a deferred call may still be looking at one of them. A deferred call does not run on a panic today; put cleanup that must survive a panic in a `Drop`.

A value moved out of a variable is not dropped twice: the move leaves the variable empty, and dropping an empty variable does nothing. That is what makes a move inside an `if` safe without any bookkeeping on your part, and it is why `take` leaves the zero value behind. A value that no variable holds, such as a string built for a `Println`, is dropped when its statement ends.

A method with a plain value receiver takes its receiver and may drop it. That is how `Close` works on a file. A file closes itself when dropped; `f.Close()` is for when the error matters, since it returns the error that `Drop` cannot. Afterwards `f` is gone, so a closed file cannot be written to and cannot be closed twice:

```go
f.Close()?
f.WriteString("late")?                // ERROR "use of moved value f"
```

## Checking the drops: `snello build -debug`

The compiler decides where every value is freed, so a mistake in the compiler would be a leak or a value used after its release, and neither makes a program fail on its own. A debug build makes both fail:

```
snello build -debug prog.snello
./prog
```

The binary runs the same program with a checking runtime. Every allocation is counted, and a program that returns from `main` with values still unreleased reports them and exits with code 3:

```
snello: 3 blocks (9 bytes) not released
  3 bytes: t h e
  3 bytes: a n d
  3 bytes: t h e
```

Released memory is filled with a poison byte and kept from reuse for a while, and a released place is poisoned too. A string, slice, map, pointer or file read after its release is seen by the runtime operation that reads it, which panics with `a released string was used`, naming the kind of value; an owned pointer read after its value moved away panics with `a moved pointer was used`. A release build does none of this and runs at full speed.

No program the checker accepts should ever fail this way, which is what makes the debug build useful to the compiler's own tests: every example program runs under it, so a drop the compiler forgets, or places wrongly, is a failing test rather than a slow leak. Its first find was a rule the checker lacked: a borrow of a temporary, `t := s.Clone().Trim()`, printed poison once the clone was dropped at the end of its statement, and is now refused (chapter 6). It also catches the one escape the checker does not see yet, a closure capturing a local that a function stores through a parameter of function type, when the closure is called after the local is gone.

## What no collector buys

Freeing every value on the line its owner dies is not only about leaks. It changes two numbers a program with a collector cannot control: how much memory it holds, and how long it can stall. `bench/memory.snello` shows both. It keeps a tree of a million nodes alive the whole run and, beside it, runs two thousand rounds that each build a small tree, walk it and drop it:

```go
// round builds a small tree, counts it, and frees it on the way out.
func round() int {
	t := build(12)
	return count(t)
}
```

`t` dies at the closing brace, so the eight thousand blocks of one round are the next round's. Nothing piles up and nothing has to walk the live tree to find out what is dead. The same program in Go and in Rust, measured on an eight-core arm64 laptop:

| | Snello | Go | Rust |
|---|---|---|---|
| Peak memory | 33 MB | 78 MB | 34 MB |
| Mean round | 103 µs | 96 µs | 177 µs |
| Rounds over twice the mean | none | 178 of 2,000, up to 0.7 ms | none |

- **Memory is what is live.** Snello's peak is the live tree. Go's is 2.4 times it: its collector lets garbage reach about the size of the live heap before a cycle.
- **No pauses.** Every Snello round costs the same, since each pays for its own frees as it goes. About one Go round in eleven takes over twice the mean, the rounds a collection runs in.
- **The cost is per object.** Go's mean round is faster: allocating from a collector's heap is a pointer bump, and freeing is batched. Snello pays a call to the allocator for each node, as Rust does, but the runtime's allocator keeps a list of free blocks of each size on every thread, so the call is a few instructions and the mean round is within a tenth of Go's.

Two tests hold this in place. `go test ./bench -run TestNoCollector -v` builds the program with one round, with two thousand and with ten thousand. It fails if the peak with eighty million blocks freed is a tenth over the peak with eight thousand. `SNELLO_BENCH=1 go test ./bench -v` prints the whole table above, for every benchmark and all three languages. The order in which values die is shown by `examples/run/drops.snello`, which prints it, and that nothing is left behind by the debug build of every example, which fails on any block not released.

## What the compiler reports

| Message | Program |
|---|---|
| `use of moved value s` | `use-after-move`, `move-in-branch`, `closure-moves`, `use-after-close` |
| `s moved in a previous iteration of the loop` | `move-in-loop` |
| `cannot move xs[0] out of the slice` | `move-out-of-slice` |
| `cannot move n.value out of a borrow` | `move-out-of-borrow` |
| `cannot move pick(...).name out of a borrow`, `cannot move (*mk()).name out of a temporary: bind the temporary to a name first`, `cannot move row().name out of a temporary: ...` | `TestMoves` in `compiler/types/move_test.go` |
| `use of partially moved value n` | `partial-move` |
