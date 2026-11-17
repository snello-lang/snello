# 31. Iterators

The design is the section "A type argument that holds a borrow" of `../design/00-syntax-conventions.md`, and decision 16 of `../design/05-standard-library.md`. The library is `std/iter/iter.snello`, and its tests are `std/iter/iter_test.snello`.

An iterator is a value a loop pulls items from, one at a time. A collection makes one over its items, an adapter wraps one in another that changes what it gives, and a consumer pulls every item and gives one answer:

```go
d := deque.New<int>()
for i := 0; i < 10; i++ { d.PushBack(i) }

squares := d.Iter().Filter(isEven).Map(square).Collect()   // [0 4 16 36 64]
for i, x := range d.Iter().Enumerate() { ... }
total := d.Iter().Fold(0, add)
```

The iterator over the deque lends its elements and holds the deque's borrow, and so does every adapter over it: the deque cannot change while the chain is alive, and is free again after its last use. Nothing is written for this, and nothing is checked when the program runs.

## The trait

`iter.Iterator<T>` asks for one method:

```go
trait Iterator<T> {
    func (it mut Self) Next() T?
    // the adapters and consumers below, each a default method
}
```

`Next` gives the next item, or `None` once there are no more. A type that lists the trait writes `Next` and gets everything else:

```go
struct Count impl iter.Iterator<int> { next, end int }

func (c mut Count) Next() int? {
    if c.next >= c.end { return None }
    c.next++
    return Some(c.next - 1)
}

evens := Count{0, 10}.Filter(isEven).Collect()   // [0 2 4 6 8]
```

`range` takes any value with `Next`, by its shape, so a type need not list the trait to be ranged over. Listing it gives the type the adapters, and lets a generic function take it by a bound (below). Every iterator of the library lists it: `d.Iter()` of a deque and `s.Iter()` of a set are `iter.Iterator<ref T>`, `a.Iter()` of an arena is an `iter.Iterator<(arena.Index<T>, ref T)>`, and a JSON document's `doc.Elements(n)` and `doc.Members(n)` are iterators of nodes and of members.

## Adapters and consumers

An adapter takes the iterator it wraps by value and gives nothing until it is pulled from, so a chain does no work until a loop or a consumer runs it.

| Adapter | Gives |
|---|---|
| `Map(f func(T) U)` | `f` of each item |
| `Filter(keep func(ref T) bool)` | the items `keep` is true of, each lent to `keep` |
| `Take(n int)` | the first `n` items, or all when there are fewer; none for `n` of 0 or less |
| `Skip(n int)` | the items after the first `n` |
| `Enumerate()` | each item with its index from 0, `(int, T)` |
| `Zip(other)` | the items of both in pairs, `(T, V)`, ending when either ends |
| `Chain(other)` | the items of one, then those of the other, which gives the same type of item |

A consumer lends the iterator `mut` and pulls from it, so `it.Count()` leaves `it` to its owner, and a consumer called on a chain's temporary is fine too.

| Consumer | Returns |
|---|---|
| `Count()` | how many items there were |
| `Fold(init, f func(A, T) A)` | `init` combined with each item in turn, `init` for none |
| `Any(test func(ref T) bool)` | whether `test` is true of an item, stopping at the first; false for none |
| `All(test func(ref T) bool)` | whether `test` is true of every item, stopping at the first it is not; true for none |
| `Find(test func(ref T) bool)` | the first item `test` is true of, or `None`, leaving the iterator after it |
| `Collect()` | every item, in a slice, in order |

The list is short on purpose: each adapter is one more name to learn, and each here earns its place. Turning borrowed items into owned ones is a `Map`, `d.Iter().Map(func(s ref string) string { return s.Clone() })`.

## Over borrowed items

An iterator that lends its items, an `iter.Iterator<ref T>`, holds the borrow of what it walks, and the checker follows that borrow through every adapter and every consumer:

```go
evens := d.Iter().Filter(isEven)
d.PushBack(2)              // ERROR: cannot borrow d as mut: d is borrowed as ref by evens at 3:5, used at 5:17
fmt.Println(evens.Count())
```

An item outlives nothing it borrows. `Collect` over borrowed items gives a slice of the borrows, which holds the deque as the iterator did, and lets it go after its own last use:

```go
xs := d.Iter().Skip(7).Collect()   // the borrows of the last three elements
fmt.Println(xs)
d.PushBack(10)                     // fine: xs is not used after this
```

The rules that keep this safe are chapter 6's, with nothing new to write:

- **An iterator over a borrow is a borrow.** It cannot go into owned data, be sent, be spawned or become a trait value, and is returned only as a borrow is, tied to the receiver or the one borrowed parameter. `[]iter.Take<deque.Iter<int>, ref int>` is refused: `a slice cannot hold iter.Take<deque.Iter<int>, ref int>: it holds a borrow`. A struct with an adapter in a field holds the borrow too, as a struct with a `ref` field does.
- **A function returns a chain only over what it was given.** `func evens(d ref deque.Deque<int>) iter.Filter<deque.Iter<int>, ref int>` returns `d.Iter().Filter(isEven)`, tied to `d`; the same chain over a deque the function made is refused, `cannot return a borrow of local d`.
- **A closure given to an adapter borrows nothing.** An adapter keeps its function, and a closure that borrows cannot be passed to a function that keeps it (chapter 6), so a test that reads a local string, or a closure that adds into a local, is refused: `closure captures n by borrow and cannot be passed to Map, which keeps f`. A closure that captures copies, a number it reads, goes anywhere; a test that needs a string it would borrow is written in the loop's body instead, `for x := range d.Iter() { if *x == target { ... } }`.

`Filter`, `Any`, `All` and `Find` lend each item to their test, so over borrowed items the test takes a borrow of a borrow, `func(x ref ref int) bool { return **x % 2 == 0 }`.

## Writing an iterator

An iterator over a collection holds a `ref` borrow of it and gives what it borrows:

```go
struct Iter<T> impl iter.Iterator<ref T> {
    d ref Deque<T>
    i int
}

func (it mut Iter<T>) Next() ref T? {
    if it.i >= it.d.len { return None }
    it.i++
    return it.d.Get(it.i - 1)
}
```

An item borrows what the iterator borrows, never the iterator itself: a loop holds an item while it asks for the next one, and a consumer holds them all. So an iterator that gives a borrow of a field it owns is refused, and so is one that gives what it reaches through a `mut` borrow, which the next call could change or free:

```go
struct Own impl iter.Iterator<ref int> { n int }

func (o mut Own) Next() ref int? {
    return Some(ref o.n)   // ERROR: cannot return a borrow of o.n: o owns it, and Iterator<ref int>.Next returns only what o borrows
}
```

The same holds of any method a trait declares with a result that borrows nothing of its own, `Next() T?` or a function of the type `func Of(s ref string) Self`: an implementation returns only what its receiver and the parameters the trait declares with `Self` or the trait's type parameters hold behind `ref` borrows.

## Generic functions over iterators

A bound may name an iterator of borrowed items:

```go
func sum<I: iter.Iterator<ref int>>(it mut I) int {
    total := 0
    for {
        x := it.Next() ?? break
        total += *x
    }
    return total
}

it := d.Iter()
fmt.Println(sum(mut it))
```

A type argument may hold a borrow, `ref int` here and `deque.Iter<int>` for `I`, and a generic function or type is checked once whatever its arguments are, so the checker follows the borrow from outside:

- **What a generic call returns holds what its arguments hold.** `id(View{s: ref t})` holds `t`, and is refused where `t` goes first. A generic function cannot make a value of its type parameter out of nothing, so every such value it returns came from its arguments.
- **A `mut` argument takes what the others hold.** With `func (b mut Bag<T>) Add(x T)`, `b.Add(ref t)` makes `b` hold `t`, and `b` used after `t` is gone is refused: `cannot keep a borrow of t in b: t is dropped at the end of its block`.
- **A generic body hides no borrow.** A function that puts a value of its type parameter in a trait value or a closure takes no type argument that holds a borrow, `hide's T cannot hold View: it holds a borrow, which hide hides in a trait value at 30:50`, nor does one that passes its parameter on to such a function; nor a function that matches on its type parameter (chapter 24).
- **Owned data the checker cannot see into holds no borrow.** An instance that would keep its argument in a map, a channel or a pointer is refused, `a map cannot hold View: it holds a borrow`, so `deque.Deque<ref T>`, `set.Set<ref T>` and `rc.Rc<ref T>` are refused. A slice of borrows cannot be written as a type, `[]ref int{}`, since nothing would say what it borrows; a collection of borrows is the slice `Collect` gives, which holds what the iterator held.

## What it costs

An adapter is a struct of the iterator it wraps and its function, and each instance is compiled for its own types, so a chain is a loop with the calls written into it. On an arm64 Mac, the sum of the squares of the even numbers among a million, twenty times over (`bench/iterators.snello`), costs per item, in nanoseconds, the median of ten runs:

| | LLVM backend | C backend |
|---|---|---|
| `d.Iter().Filter(isEven).Map(square).Fold(0, add)` over a deque | 1.3 | 0.6 |
| the loop written by hand over the same deque | 1.4 | 0.4 |
| a chain over an iterator that counts | 0.9 | 0.9 |

Through the LLVM backend the chain costs what the loop does. Through the C backend the loop by hand is vectorized and the chain, a loop that leaves from inside, is not, so the chain costs half again as much; `../design/01-language-design.md` has the numbers beside other programs'.

## For maintainers

What a generic call passes along is in `compiler/types/passing.go`, an implementation's word in `word.go`, and what a generic body hides in `hide.go`. `passBorrows` runs at each call whose function was made by substitution and whose type arguments hold a borrow: it binds each type parameter of the declaration to what it stands for in the call (`bindings`), refuses an instance that keeps a borrow in a map, channel or pointer (`ownedParts` in `escape.go`), records the arguments whose borrows the result holds, which `sources` and `source` read beside `callSource`, and gives each argument lent `mut` what the others of its type parameter hold, through the same `holdValue` a `Push` on a slice of borrows uses. A result whose declared type holds no borrow holds only what was passed, so no temporary an argument lends is kept by it.

An implementation that keeps its trait's word, one whose trait declares a result with a type parameter's value in it outside any declared borrow (`paramByValue`) while its own result holds a borrow, is recorded by `noteWord` when the impl list is checked, and its body checks every borrow it returns (`wordSource`, `wordLoan`): a borrow of a place of a parameter is decided by its path, which must cross a `ref` borrow stored in it, carried from the return through the locals that borrow the parameter (`returnedPart`), and a loan records whether it read a borrow stored in a place or lent the place (`borrow.read`). A lend of a place behind a parameter also copies what the parameter holds; those copies go wherever the loan of the place goes, so the loan decides and the copies are passed over; what a parameter taken by value holds is its own to give (`flowHeld`).

`hide` marks a type parameter a body puts in a trait value or a closure, `hideEdge` records each generic call a generic body makes, its receiver included, `instanceHides` asks of every instance whose arguments hold a borrow what its declaration's methods hide, since a method reached through a bound is chosen by the instance, and `resolveHides` passes the marks along the calls once every body is checked, then refuses the type arguments that hold a borrow for a marked parameter. A match on a type refuses one in `typeArm` of `reach.go`.

Inference binds a bare type parameter to what a borrow passed to it borrows, but binds a borrow itself when it meets one inside a type, `Iterator<V>` against `Iterator<ref int>`; and a method's own type parameter bounded by its trait's parameters, `Zip<J: Iterator<V>, V>` or `Chain<J: Iterator<T>>`, is checked with the trait's arguments in place (`satisfiesIn`).

The C backend calls a function value through a copy of it, as the LLVM backend does, so that an adapter's field does not escape into the call and the C compiler makes the call direct. The tests are `TestPassing` and `TestIterRefusals` in `compiler/types/passing_test.go`, `TestWord` in `word_test.go` and `TestHide` in `hide_test.go`, the programs `iter-*` and `generic-*` under `compiler/types/testdata/errors/`, and `std/iter/iter_test.snello` through both backends.
