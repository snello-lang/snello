# 5. Ownership: moves, `Copy`, `take` and `Drop`

Status: moves are checked (stage 1, task 1.6, `teak check`) and `take` compiles (stage 1, task 1.5); drops come with task 1.9. The programs under `../../examples/core/errors/` show each rule being broken, and every message below is the one the compiler prints, followed by where the value moved: `use of moved value s; moved at 10:9`.

Teak has no garbage collector. Memory is freed at a point the compiler knows, and the rule that makes that possible is ownership: **every value has exactly one owner, and the value is freed when its owner goes out of scope.** The owner is a variable, a field, an element, or a temporary; the compiler knows which, so every free is decided at compile time and costs nothing at run time. This chapter is what that rule asks of you.

## Moving

Assigning a value gives it a new owner. The old name no longer owns anything and cannot be used:

```teak
s := "hello"
t := s
fmt.Println(s)                        // ERROR "use of moved value s"
```

The same happens whenever a value goes somewhere that keeps it: passed to a parameter declared by value, returned, stored in a field or a slice, sent on a channel, or captured by a task. Each of these is a **move**. Nothing is copied; the value now lives under its new owner and the old name is dead. The compiler reports the use and the move together, and the message is the same whatever moved it, including a closure that consumes the variable and a method that consumes its receiver, such as `f.Close()`.

Most code never notices. A value usually flows forward: built here, handed to the function that needs it, kept there. The state machine is the clearest case. `step` takes the old state and the event by value and builds the new state from their parts. The session inside moves from `Open` to `Draining` with nothing copied:

```teak
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

Numbers, `bool`, and channel handles are copied, not moved. `y := x` on an `int` leaves `x` usable. A struct or enum whose fields are all of such types is a `Copy` type too; the compiler works this out from the fields, and nothing is declared:

```teak
enum Token {                                    // Copy: every payload is Copy
    Num(f64)
    Plus
    Minus
}
```

```teak
op := p.cur                                 // Token is Copy: a copy, not a move
```

A struct with a string or a slice in it is not `Copy`, because those own memory. The rule to keep in mind: **if copying it would duplicate something that must be freed, it moves.**

## Moves in branches and loops

A move on one path counts on every path after they join, and a move inside a loop happens on every iteration:

```teak
s := "hello"
if len(s) > 3 {
    keep(s)
}
fmt.Println(s)                        // ERROR "use of moved value s"

for i := 0; i < 3; i += 1 {
    keep(s)                           // ERROR "s moved in a previous iteration of the loop"
}
```

A move on a path that leaves the function does not count for the paths that stay. This is what makes `??` with a `return` work: the key moves into the error only when the else runs, and is still owned on the line after:

```teak
text := kv.Get(ref key) ?? return Err(Missing(key))   // key moves only if the else runs
n := atoi(text) ?? return Err(BadValue(key, text.Clone()))
```

## Moving out of a container

Indexing an owned slice or map does not move the element out; it lends it (chapter 6). To take an element, use a method that gives it up, `xs.Remove(i)`, `xs.Pop()`, `m.Remove(k)`, or `take`:

```teak
first := xs[0]                        // ERROR "cannot move xs[0] out of the slice"
v := take(mut q.buf[q.head])          // moves the element out and leaves None behind
```

`take(mut place)` moves the value out of a field or an element and leaves the type's zero value in its place: `None` for an optional, `""` for a string, `0` for a number. It is how a linked structure is rebuilt without a temporary placeholder:

```teak
func (s mut Stack) Push(v string) {
    old := take(mut s.head)
    s.head = Some(&Node{value: v, next: old})
    s.len += 1
}
```

Ranging over an owned collection by value consumes it, moving each element out in turn. Ranging over a borrow lends each element: `for k, v := range counts` moves the keys and values out of the map, and `for _, c := range ref list` reads the list and keeps it.

## Partial moves

A field may be moved out of an owned value, leaving the rest in place, as long as the whole value is never used again. The usual case is a heap node that is about to be dropped:

```teak
func (s mut Stack) Pop() string? {
    n := take(mut s.head) ?? return None
    s.head = n.next                   // moving a field out of the owned node n
    s.len -= 1                        // is allowed because n is dropped at the
    return Some(n.value)              // end of this function
}
```

Using `n` as a whole after `n.next` moved out would be an error: `use of partially moved value n`.

## Dropping

When an owner goes out of scope, its value is freed, and so is everything the value owns, recursively. A tree of `*Node?` links is freed by dropping its root. A struct that must do something when it is freed, close a file, remove a directory, implements `Drop`:

```teak
struct TempDir impl Drop { path string }

func (t mut TempDir) Drop() { os.RemoveAll(ref t.path) }

func main() {
    tmp := NewTempDir().Expect("tempdir")
    defer fmt.Println("done")
    ...
    // tmp dropped here: directory removed. Then the deferred print runs.
}
```

`Drop` runs at the end of the scope, on an early `return`, and while a panic unwinds. `defer` is still there for cleanup that belongs to no value, and the two compose: deferred calls run after the scope's drops.

A method with a plain value receiver takes its receiver and may drop it. That is how `Close` works on a file. A file closes itself when dropped; `f.Close()` is for when the error matters, since it returns the error that `Drop` cannot. Afterwards `f` is gone, so a closed file cannot be written to and cannot be closed twice:

```teak
f.Close()?
f.WriteString("late")?                // ERROR "use of moved value f"
```

## What the compiler reports

| Message | Program |
|---|---|
| `use of moved value s` | `use-after-move`, `move-in-branch`, `closure-moves`, `use-after-close` |
| `s moved in a previous iteration of the loop` | `move-in-loop` |
| `cannot move xs[0] out of the slice` | `move-out-of-slice` |
| `cannot move n.value out of a borrow` | `move-out-of-borrow` |
| `use of partially moved value n` | `partial-move` |
