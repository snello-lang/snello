# 18. No empty values

Status: implemented (stage 11, task 11.2, 2026-11-08): no value is ever null or empty. A type has a zero value only when its zero is a real value of it; the checker refuses every way of making the zero of a type that has none; the builtin `replace` moves a value out of a place and leaves another; and the library panics on no mistake a caller can make.

This chapter is for two readers: a developer, who needs to know which declarations the compiler refuses and what to write instead, and a maintainer of the compiler or the library, who needs to know where the rule lives. The last section is the maintainer's.

## Why

Go gives every type a zero value, so that `var x T` is always legal and always means something. For a number, a string or a slice that is a real value: `0`, `""`, an empty slice. For a pointer, a map, a channel, a function or an interface it is `nil`, a value that holds nothing and looks like one that does:

```go
var p *Node
fmt.Println(p.value)    // panic: runtime error: invalid memory address or nil pointer dereference
```

Nothing stops the program from building it, passing it on and storing it; it fails where it is used, which may be far from where it was made, in production, on a path no test took. The nil dereference is among the panics Go programs hit most often, and Tony Hoare, who put the null reference into ALGOL W in 1965, called it his billion-dollar mistake. The same idea comes back in other forms. In C# a `struct` can always be zeroed with `default(T)`, whatever its constructor promises, which is how a `default` `ImmutableArray` throws on its first use. A Go `sync.Mutex` declared with `var` works, but only because Go designed its zero to be an unlocked lock; any type that holds a resource behind a handle has a zero that holds none.

Snello began with Go's rule and its holes: `var p *int` compiled to a null that `*p` followed, and `var m sync.Mutex<int>` to a lock with nothing behind it, whose `Lock` panicked. The borrow rules make every borrow point at a live value, but they had nothing to say about a value that was empty from the start.

Rust and Swift close the hole the same way: there are no implicit zeros. A variable is given a value before it is read, and a value that may be missing has a type that says so, `Option<T>` or `T?`, which the compiler makes every use handle. Snello does the same, and keeps Go's zeros where they are real values, so the habit of `var n int` and `var b strings.Builder` still works.

What it buys:

- **A whole class of crash cannot be written.** A `*Node` always points at a node, a `sync.Mutex<int>` always has its lock, an `rc.Rc<T>` always has its value. There is no check to forget, because there is nothing to check.
- **The type says when a value may be missing.** "Maybe a node" is `*Node?`, and the compiler makes the code that reads it handle `None`.
- **The mistake is found by the compiler**, at `snello check`, with the line and what to write, and not by a user at run time.
- **It costs nothing.** The checker only refuses programs; nothing runs at run time, and a `*T` is still one machine word, now never null.

## Which types have a zero value

A variable declared without a value gets its type's zero value when it has one:

```snello
var n int
var s string
var p Point
var names []string
var next *Node?
fmt.Println(n, len(s), p, len(names), next.IsNone())
// 0 0 {0 0} 0 true
```

| Type | Zero value |
|---|---|
| a number | `0` |
| `bool` | `false` |
| `string` | `""` |
| `T?` | `None`, whatever `T` is |
| `[]T`, `map[K]V` | empty, whatever the element |
| a struct, tuple or array | each part's zero value |
| an enum | its first variant, with each field's zero value |
| `Result<T, E>` | `Err` of `E`'s zero value |

These have none:

| Type | Why |
|---|---|
| `*T`, `chan T`, a function, a trait value | the zero would be a null that every use follows |
| a type that implements `Drop` | it owns something, a cell, a lock, a file; its zero would own nothing |
| a type parameter | the type argument may be any of these |
| a struct, tuple, array, enum or `Result` holding one of these | that part has no zero value |

An option has a zero value whatever it holds, `None`, and so does a slice or a map, empty: `var next *Node?` and `var accounts map[string]Account` are fine.

## What the compiler refuses, and what to write

Every way of making the zero of a type that has none is refused, each with what to write instead:

| Refused | Message | Write instead |
|---|---|---|
| `var m sync.Mutex<int>` | `cannot declare m without a value: sync.Mutex<int> has no zero value; give it one, or declare it sync.Mutex<int>?` | `m := sync.NewMutex(0)` |
| `Box{n: 1}`, where `Box` has a field `p *int` | `missing field p in a Box literal: *int has no zero value; name it` | `Box{p: &x, n: 1}` |
| `sync.Mutex<int>{}` | `cannot build a sync.Mutex<int> with a literal: it has no zero value, and its field p is not exported; use its package's functions` | `sync.NewMutex(0)` |
| `[2]*int{&a}` | `too few elements for [2]*int: 1 of 2, and *int has no zero value for the rest` | `[2]*int{&a, &b}` |
| `make([]*Node, n)` | `make cannot fill a []*Node: *Node has no zero value; use make([]*Node, 0, n) and Push` | `make([]*Node, 0, n)`, then `Push` |
| `print(m[k])`, the map's values `*Node` | `cannot read m[k]: *Node has no zero value for a key the map does not hold; use m.Get(k)` | `m.Get(k)`, a `ref *Node?` |
| `m[k].n = 5`, the map's values `Box` | `cannot write through m[k]: Box has no zero value for a key the map does not hold; use m.GetMut(k)` | `if b := m.GetMut(k) { b.n = 5 }`, or `m[k] = Box{...}` whole |
| `sync.Counter(c)`, `c` of a type declared over it | `cannot convert MyC to sync.Counter: sync.Counter has no zero value, and a value of it is built and taken apart only by its own package` | `sync.NewCounter(n)` |
| `take(mut b.p)`, `b.p` a `*int` | `cannot take b.p: *int has no zero value to leave behind; replace(mut b.p, v) leaves v` | `replace(mut b.p, &x)` |

A value that depends on a condition is built where it is declared, with an `if` or a `match` expression, rather than declared first and assigned on every path:

```snello
lock := if limit > 2 { sync.NewMutex(limit) } else { sync.NewMutex(0) }
```

A link that may point at nothing is an optional, given a value later:

```snello
var parent rc.Weak<Node>?
fmt.Println(parent.IsNone())
parent = Some(root.Downgrade())
if w := ref parent {
    fmt.Println(w.Upgrade().IsSome())
}
```

A map of values without a zero value is written as any map is, `m[k] = v`, and read with `Get`, which says whether the key is there:

```snello
accounts["ada"] = Account{owner: "ada".Clone(), lock: sync.NewMutex(5)}
if acct := accounts.Get("ada") {
    fmt.Println(acct.owner, *acct.lock.Lock().Get())
}
```

Generic code is checked once for every type argument, so a type parameter has no zero value: inside `func f<T>()`, `var x T` and `make([]T, n)` are refused, and `var x T?` is the way to hold a `T` that may not be there yet.

## `replace`

`take(mut place)` moves a value out of a field or an element and leaves the type's zero value behind, so it works only for a type that has one. `replace(mut place, v)` moves the value out and leaves `v`, so the place is never empty, and it works for every type. It is Rust's `mem::replace`:

```snello
a := Account{owner: "ada".Clone(), lock: sync.NewMutex(10)}
old := replace(mut a.lock, sync.NewMutex(20))
fmt.Println(*old.Lock().Get(), *a.lock.Lock().Get())
// 10 20
head := Node{name: "head".Clone(), next: Some(&Node{name: "tail".Clone(), next: None})}
rest := replace(mut head.next, None)
if r := rest {
    fmt.Println(head.name, "then", r.name)
}
// head then tail
```

The place is a variable, or a field, element or `*p` of one, written `mut`; a call's result, a constant or a byte of a string is not a place, since what is moved out of it, or into it, would be lost with it, and `replace` and `take` refuse it: `replace needs a place, a variable or a field, element or *p of one: f() is not one`. The value is moved in as an assignment moves it. The old value is the caller's, dropped where its new owner drops it.

## The library

With the rule, the library's handles exist only from their constructors, and the library panics on no mistake a caller can make:

- `sync`'s `Arc`, `Mutex`, `RwLock`, `Once`, `Counter` and `WaitGroup`, and `rc`'s `Rc` and `Weak`, implement `Drop` or hold a type that does, so a program gets one only from its `New` function, or from `Clone` and `Downgrade`. The panics an empty handle used to give are gone.
- `strings.Repeat(s, n)` returns `string?`: `None` for a negative `n`, or for a string too long for an `int` to count.
- `WaitGroup.Add(n)` and `Done()` return `bool`: `false`, with the count left as it was, when the count would go below zero, a `Done` with no `Add` before it.

```snello
fmt.Println(strings.Repeat("ab", 3) ?? "", strings.Repeat("ab", -1).IsNone())
// ababab true
wg := sync.NewWaitGroup()
fmt.Println(wg.Done(), wg.Add(1), wg.Done())
// false true true
```

What still panics is what the language itself traps, a program's bug rather than a caller's choice: an index out of range, a division by zero, `Unwrap` on `None`, a send on a closed channel, and running out of memory. Inside `rc` and `sync`, a branch that only a broken invariant of the package could reach, a handle whose cell is gone before its `Drop`, keeps a panic that says so.

## How this stays memory safe without a collector

The rule only refuses. A program the checker accepted before and still accepts means what it meant; the programs it now refuses are those that made an empty value, which nothing could use safely. No check runs when the program does, and no value grows a flag. Every `Drop` runs on a value that was built, by its type's constructor or by a literal, never on a zero a declaration made; outside its package, a literal of such a type cannot leave a field out. `replace`, the one addition, takes its place as any `mut` borrow does and leaves a valid value in it in the same step. Every value still has one owner and is freed by its `Drop`; no collector is involved.

## For maintainers

- **The predicate.** `hasZero` in `compiler/types/predicates.go` decides which types have a zero value: a pointer, channel, function, trait value, borrow and type parameter have none, a declared type that implements `Drop` has none, and a composite has one when its parts do. Its table is tested type by type in `predicates_test.go`.
- **The refusals.** `compiler/types/zero.go` holds them all: `zeroDecl`, called for a local declaration from `declStmt` in `stmt.go` and for a package-level one from `initializersOf` in `body.go`; `zeroFields`, called at the end of `structLit` in `literal.go`, for a field left out and for a literal of another package's type whose fields the program cannot name; `zeroArray` for a short array literal; `zeroMake` from `makeCall` in `call.go`, which lets the constant `0` through; and `zeroRead` from `index` in `expr.go`, for a map read that is not an assignment's target, which `noUse` tells apart, and `zeroWrite` for a target that writes part of an entry, `m[k].f = v`, which the flag `inner` marks; and `zeroConversion` from `conversion` in `call.go`, between two types when either has no zero value. A field records the package that declared it, and `visible` in `expr.go` and `zeroFields` judge it by that package, so a type declared over another package's struct opens none of its fields. `hasZero` walks a type with the declared types it is inside, so a type that contains itself, reported elsewhere, ends the walk. `take`'s own refusal is in `takeCall`. They are tested in `zero_test.go`, a package's types across a package boundary included.
- **`replace`.** `BuiltinReplace` in `compiler/types/object.go` and `universe.go`; `replaceCall` in `call.go` checks the place written `mut` and moves the value in, and `lentPlace` refuses a borrow that lends no place, from the mode `unary` records in `lentMode`; `takeCall` uses it too. Both are tested in `call_test.go`. The C backend's `replaceCall` in `compiler/emit/c/methods.go` and the LLVM backend's case in `builtinCall`, `compiler/emit/llvm/builtins.go`, take the place's address first, so a call in the value runs before the place is read, then load the old value and store the new one; each has a test, `TestReplace` in `methods_test.go` and in `builtins_test.go`, and `replace` in `compiler/emit/internal/backendtest` runs a type with `Drop` through both backends in release and debug builds, each value dropped once.
- **The library.** `std/sync/sync.snello` and `std/rc/rc.snello` keep their handles' cells as options, since a `Drop` takes the cell out with `take`; the branch where it is missing panics with the message `broken` names in `sync`, and its own in `rc`. `sn_sync_count` in `runtime/c/sync.c` returns whether it counted. `strings.Repeat` and `arena.Remove` are in their packages, tested in their `_test.snello` files.
- **The example.** `examples/run/zero.snello` is the program this chapter is cut from; `shared.snello` and `formats.snello` show an optional `rc.Weak` and a handle dropped early with `_ = r`.
