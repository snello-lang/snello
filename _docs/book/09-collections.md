# 9. Collections: slices, maps and strings

Status: implemented, checked (stage 1, task 1.4) and compiled to native code (stage 1, task 1.5, third slice). `examples/run/slices.teak`, `maps.teak` and `closures.teak` exercise this chapter.

A slice is an owned, growable list. A map is an owned table from keys to values. A string is an owned sequence of bytes holding UTF-8 text. All three own their memory, so the rules of chapter 5 apply to them: they move when assigned or passed by value, they are lent with `ref` and `mut`, and they are freed when their owner is dropped. None of them is ever `nil`; a slice or map declared without a value is empty and ready to use.

## Slices

A slice literal lists its elements, and `make` builds one of a given length, filled with the element's zero value:

```teak
xs := []int{3, 1, 2}
path := []string{}
buf := make([]int?, cap)
```

`len(xs)` is the number of elements and `cap(xs)` the room before the next growth. `xs[i]` reads or writes an element, and an index outside the slice stops the program with `index out of range [5] with length 3`. `xs[lo:hi]` lends the elements from `lo` up to `hi`: the result is a borrow of the slice, with the type `ref []T`, or `mut []T` when `xs` was reached through a `mut`.

Growing and shrinking are methods, since a slice owns its elements and the methods say what happens to them:

| Method | What it does |
|---|---|
| `xs.Push(v)` | Appends `v`, growing the slice when it is full |
| `xs.Pop()` | Removes and returns the last element, as `T?`: `None` when the slice is empty |
| `xs.Insert(i, v)` | Puts `v` at index `i`, moving the rest up |
| `xs.Remove(i)` | Removes and returns the element at `i` |
| `xs.RemoveValue(v)` | Removes the first element equal to `v`; reports whether one was found |
| `xs.Contains(v)` | Reports whether an element equals `v` |
| `xs.Clear()` | Removes every element and keeps the room |
| `xs.Reverse()` | Reverses the elements in place |
| `xs.Sort()`, `xs.SortBy(less)` | Sort in place, keeping equal elements in their order; `Sort` orders numbers and strings, `SortBy` takes the ordering as a function |
| `xs.Clone()` | A new slice holding a copy of every element |

`Push`, `Pop`, `Insert`, `Remove`, `RemoveValue`, `Clear`, `Reverse` and `Sort` change the slice, so they need it owned or lent with `mut`; through a `ref` they are an error. The core tree uses most of them:

```teak
t.nodes.Push(Node{value: value, parent: Some(parent), children: []NodeID{}, live: true})
t.nodes[parent].children.Push(id)
children := t.nodes[id].children.Clone()
t.nodes[parent].children.RemoveValue(id)
path.Reverse()
```

The range form of `for` walks a slice. `for i, x := range xs` gives the index and the element; `for _, x := range xs` only the element; `for i := range xs` only the index. The element is lent, not copied, when the slice is reached through a borrow, so through a `mut` the loop can change it in place:

```teak
func (b mut Bag) bump() {
    for _, v := range b.items {
        *v += 1
    }
}
```

Ranging over an owned slice by value moves each element out, as chapter 5 describes, which is how `wordfreq` turns a map into a list.

Two slices do not compare with `==`: equality of a list is a question with more than one answer. Elements compare, which is what `Contains` and `RemoveValue` use.

## Strings

A string is immutable bytes. `len(s)` is its length in bytes and `s[i]` the byte at `i`, of type `byte`; `s[lo:hi]` lends the bytes between, as a `ref string`. `+` builds a new string from two, and `==` and `<` compare bytes. `for i, c := range s` yields each byte and its index.

Operations on text are methods. Most return views into the string, which cost nothing and live as borrows do:

| Method | Returns |
|---|---|
| `s.Len()` | The length in bytes, like `len(s)` |
| `s.Trim()` | `s` without white space at either end, as a borrow |
| `s.Fields()` | The pieces between white space, each a borrow; use it in a `for` |
| `s.Split(sep)` | The pieces between each `sep`, likewise |
| `s.SplitOnce(sep)` | The parts before and after the first `sep`, as `(ref string, ref string)?` |
| `s.HasPrefix(t)`, `s.HasSuffix(t)`, `s.Contains(t)` | Whether `t` begins, ends or appears in `s` |
| `s.ToUpper()`, `s.ToLower()` | A new, owned string |
| `s.Clone()` | A new, owned copy of `s` |

The pieces `Fields` and `Split` yield borrow the string they came from, so `for w := range text.Fields()` lends `text` for the loop. To keep a piece, clone it or build an owned string from it, which is what `wordfreq` does with `w.ToLower()` before using the word as a map key. `SplitOnce` is made for `if` with a binding:

```teak
if (k, v) := line.SplitOnce("=") {
    settings.Insert(k.Trim().Clone(), v.Trim().Clone())
}
```

## Maps

A map holds one value per key. A map literal lists its entries, or none, and `make(map[K]V)` is the same as the empty literal:

```teak
kv := map[string]string{}
counts := map[string]int{}
```

`m[k] = v` stores a value, making the entry when the key is new, and `m[k]` reads one. Reading a key that is not there gives the value type's zero value and leaves the map unchanged, so `counts[w] += 1` counts a word whether or not it was seen before. `len(m)` is the number of entries.

The methods say what happens to the entry:

| Method | What it does |
|---|---|
| `m.Get(k)` | The value as a borrow, `ref V?`: `None` when the key is absent |
| `m.GetMut(k)` | The same as a `mut V?`, to change the value in place |
| `m.Insert(k, v)` | Stores `v` under `k`, replacing what was there |
| `m.Remove(k)` | Takes the entry out and returns its value as `V?` |
| `m.Contains(k)` | Whether the key is there |
| `m.Clone()` | A new map with a copy of every entry |

`Get` and `GetMut` answer with an optional because the entry may be absent, which is what `config` relies on:

```teak
text := kv.Get(ref key) ?? return Err(Missing(key))
```

A key is any type with `==`: numbers, strings, booleans, enums, tuples and structs of those. A map owns its keys and values, so inserting a borrowed string clones it first: `kv.Insert(k.Trim().Clone(), v.Trim().Clone())`.

`for k, v := range m` walks the entries in the order they were inserted, which is also the order a map prints in: `map[one:1 two:2]`. Through a `mut` borrow the values are lent for writing, as slice elements are; ranging over an owned map by value moves each key and value out, which is how `wordfreq` turns its counts into a list to sort.

Two maps do not compare with `==`, for the same reason two slices do not.

## What the compiler reports

| Message | Meaning |
|---|---|
| `cannot index a value of type int` | Only slices, arrays, maps and strings index |
| `index must be an integer, not string` | A slice index is an integer of any integer type |
| `values of type []int cannot be compared with ==` | Compare elements, or use `Contains`; the same for maps |
| `index out of range [5] with length 3` | At run time: the program stops instead of reading past the end |

## What a value costs

A slice is three words, a pointer to its elements, a length and a capacity, and its elements lie one after another in one allocation. A map is eight words: its keys and values lie in two arrays in insertion order, found through a hash index that doubles as the map grows; a removed entry leaves its slot until the next growth compacts the arrays. `Push` doubles the room when it runs out, so appending `n` elements allocates about `log n` times. A string is two words, a pointer and a length, and a literal points at the program's own bytes; only `+`, `Clone`, `ToUpper` and `ToLower` allocate. A borrow of part of either is the same two or three words pointing into the middle, and allocates nothing.
