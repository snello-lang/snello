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

`len(xs)` is the number of elements and `cap(xs)` the room before the next growth. `xs[i]` reads or writes an element, and an index outside the slice stops the program with `index out of range [5] with length 3`. `xs[lo:hi]` lends the elements from `lo` up to `hi`: the result is a borrow of the slice, with the type `ref []T`, or `mut []T` when `xs` was reached through a `mut`. A sub-slice is a window on its slice's elements: a function given `mut xs[1:4]` may change, sort and reverse those elements, but their number is the slice's to change, so `Push`, `Pop`, `Insert`, `Remove`, `RemoveValue` and `Clear` on a sub-slice, or replacing it whole, stop the program with `Push on a sub-slice: it lends the elements of its slice and cannot change its length`, before anything is changed. `cap` of a sub-slice is its length.

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

A string is immutable bytes. `len(s)` is its length in bytes and `s[i]` the byte at `i`, of type `byte`; `s[lo:hi]` lends the bytes between, as a `ref string`. `+` builds a new string from two, and `==` and `<` compare bytes. `for i, c := range s` yields each byte and its index. `[]byte(s)` copies the bytes into a slice that can be changed, and `string(b)` copies a byte slice into a string; both leave their operand as it was.

Operations on text are methods. Most return views into the string, which cost nothing and live as borrows do:

| Method | Returns |
|---|---|
| `s.Len()` | The length in bytes, like `len(s)` |
| `s.Trim()` | `s` without white space at either end, as a borrow: Go's `strings.TrimSpace` |
| `s.Fields()` | The pieces between white space, each a borrow; use it in a `for` |
| `s.Lines()` | The lines, without their newline or a carriage return before it, each a borrow; a last line without a newline counts, a trailing newline makes no empty one. Go's `strings.Lines` keeps each newline |
| `s.Split(sep)` | The pieces between each `sep`, likewise; an empty `sep` gives each UTF-8 sequence, and none of an empty string |
| `s.SplitOnce(sep)` | The parts before and after the first `sep`, as `(ref string, ref string)?` |
| `s.HasPrefix(t)`, `s.HasSuffix(t)`, `s.Contains(t)` | Whether `t` begins, ends or appears in `s` |
| `s.ToUpper()`, `s.ToLower()` | A new, owned string, every letter changed by Unicode's simple case mapping |
| `s.Clone()` | A new, owned copy of `s` |

Text is UTF-8 and the methods read it as Go's `strings` package does: `ToUpper` and `ToLower` change every letter, `"àéî".ToUpper()` is `ÀÉÎ`, white space for `Trim` and `Fields` is what Unicode calls white space, a no-break space or an ideographic space among it, and a byte that is not valid UTF-8 reads as the replacement character U+FFFD, which `ToUpper` writes in its place. `examples/run/unicode.teak` checks each against Go's output. A slice expression out of range stops the program in Go's words, `slice bounds out of range [3:1]` or `[:10] with length 3`, and so does `make` with a negative length or a capacity below it, `makeslice: len out of range`.

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

A key is any type that is `Eq` and `Hash` (chapter 11): numbers, strings, booleans, enums and tuples of those by their structure, and a struct that lists `Hash`. A slice, a map or a trait value cannot be a key, and the compiler says so at the map type. A map owns its keys and values, so inserting a borrowed string clones it first: `kv.Insert(k.Trim().Clone(), v.Trim().Clone())`.

`for k, v := range m` walks the entries in the order they were inserted, which is also the order a map prints in: `map[one:1 two:2]`. Through a `mut` borrow the values are lent for writing, as slice elements are; ranging over an owned map by value moves each key and value out, which is how `wordfreq` turns its counts into a list to sort.

Two maps do not compare with `==`, for the same reason two slices do not.

## Arrays

An array, `[4]int`, is a value of a fixed length: zero-valued by `var`, written as a literal with its elements in order, `[3]int{1, 2}` leaving the rest zero, indexed to read and write with the same bounds check as a slice, and copied whole on assignment, so a struct of arrays is `Copy` when its elements are. `len` and `cap` are its length. An array prints as a slice does, `[0 5 10 0]`, is cloned element by element inside a type that lists `Clone`, and drops each element that needs it. It is not a slice: it is not sliced, pushed to or ranged over yet, and an array is not `Eq` or `Hash` by structure, so a struct holding one writes its own `Eq`. `examples/run/arrays.teak` and `examples/16-matrix.teak`, whose `Mat4` holds a `[16]f64`, run them.

## What the compiler reports

| Message | Meaning |
|---|---|
| `cannot index a value of type int` | Only slices, arrays, maps and strings index |
| `index must be an integer, not string` | A slice index is an integer of any integer type |
| `values of type []int cannot be compared with ==` | Compare elements, or use `Contains`; the same for maps |
| `index out of range [5] with length 3` | At run time: the program stops instead of reading past the end |

## What a value costs

A slice is three words, a pointer to its elements, a length and a capacity, and its elements lie one after another in one allocation. A map is eight words: its keys and values lie in two arrays in insertion order, found through a hash index that doubles as the map grows; a removed entry leaves its slot until the next growth compacts the arrays. `Push` doubles the room when it runs out, so appending `n` elements allocates about `log n` times. A string is two words, a pointer and a length, and a literal points at the program's own bytes; only `+`, `Clone`, `ToUpper` and `ToLower` allocate. A borrow of part of either is the same two or three words pointing into the middle, and allocates nothing.
