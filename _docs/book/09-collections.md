# 9. Collections: slices, maps and strings

`compiler/emit/internal/backendtest/testdata/slices.snello`, `maps.snello` and `closures.snello` exercise this chapter, and the programs `string-methods`, `slice-methods`, `map-option-result-methods` and `search-long-needle` in `backendtest.go` its methods, through both backends; `TestStringSearch` and `TestSearchLinear` in `runtime/runtime_test.go` hold the string searches to a plain reference and to linear time. The methods are declared in `compiler/types/library.go`, with the bound each asks of the element, lowered in each backend's `methods.go`, and the string searches are the runtime's, in `runtime/c/string.c`.

A slice is an owned, growable list. A map is an owned table from keys to values. A string is an owned sequence of bytes holding UTF-8 text. All three own their memory, so the rules of chapter 5 apply to them: they move when assigned or passed by value, they are lent with `ref` and `mut`, and they are freed when their owner is dropped. None of them is ever `nil`; a slice or map declared without a value is empty and ready to use.

## Slices

A slice literal lists its elements, and `make` builds one of a given length, filled with the element's zero value; for an element with no zero value, a pointer or a type with `Drop` (chapter 18), `make([]T, 0, n)` reserves the room and `Push` fills it:

```go
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
| `xs.Index(v)` | The position of the first element equal to `v`, as `int?`: `None` when there is none |
| `xs.First()`, `xs.Last()` | The first or last element as a borrow, `ref T?`: `None` when the slice is empty |
| `xs.Extend(ys)` | Appends a clone of every element of `ys`, growing the slice once; a `Clone` the element's type writes runs for each |
| `xs.Truncate(n)` | Drops the elements from `n` on and keeps the room; an `n` past the end changes nothing, and zero or less empties the slice |
| `xs.Clear()` | Removes every element and keeps the room |
| `xs.Swap(i, j)` | Exchanges the elements at `i` and `j`, moving each into the other's place |
| `xs.Reverse()` | Reverses the elements in place |
| `xs.Sort()`, `xs.SortBy(less)` | Sort in place, keeping equal elements in their order; `Sort` orders numbers and strings, `SortBy` takes the ordering as a function |
| `xs.Clone()` | A new slice holding a copy of every element |

`Push`, `Pop`, `Insert`, `Remove`, `RemoveValue`, `Clear`, `Swap`, `Reverse`, `Sort`, `Extend` and `Truncate` change the slice, so they need it owned or lent with `mut`; through a `ref` they are an error. `Swap` is how an algorithm moves elements it does not own a copy of: Go's `xs[i], xs[j] = xs[j], xs[i]` would move each element out of the slice, which is refused for a type that is not `Copy`, `cannot move xs[j] out of the slice`. The core tree uses most of them:

```go
t.nodes.Push(Node{value: value, parent: Some(parent), children: []NodeID{}, live: true})
t.nodes[parent].children.Push(id)
children := t.nodes[id].children.Clone()
t.nodes[parent].children.RemoveValue(id)
path.Reverse()
```

The range form of `for` walks a slice. `for i, x := range xs` gives the index and the element; `for _, x := range xs` only the element; `for i := range xs` only the index. The element is lent, not copied, when the slice is reached through a borrow, so through a `mut` the loop can change it in place:

```go
func (b mut Bag) bump() {
    for _, v := range b.items {
        *v += 1
    }
}
```

Ranging over an owned slice by value moves each element out, as chapter 5 describes, which is how `wordfreq` turns a map into a list.

Two slices do not compare with `==`: equality of a list is a question with more than one answer. Elements compare, which is what `Contains`, `RemoveValue` and `Index` use, so their element must be `Eq`, or a slice of such elements, which they compare element by element, or a borrow of one, as the pieces `Split` gives are; `Sort` needs `Ord`, and `Extend` and `Clone` need `Clone`, and the compiler says so at the call: `Contains needs the elements to be Eq: Point is not`. A `[]byte` has two methods of its own: `b.Equal(o)` compares the bytes of two, and `b.Find(sub)` gives the offset of a run of bytes, `int?`, by the search `s.Index` makes; `b.Index(c)` finds one byte, as `Index` finds one element of any slice.

## Strings

A string is immutable bytes. `len(s)` is its length in bytes and `s[i]` the byte at `i`, of type `byte`; `s[lo:hi]` lends the bytes between, as a `ref string`. `+` builds a new string from two, and `==` and `<` compare bytes. `for i, r := range s` yields each rune and the byte offset it starts at, and the string is lent for the loop; chapter 19 has runes, the `unicode` packages and what a string that is not valid UTF-8 reads as. `[]byte(s)` copies the bytes into a slice that can be changed, and `string(b)` copies a byte slice into a string; both leave their operand as it was.

Operations on text are methods. Most return views into the string, which cost nothing and live as borrows do:

| Method | Returns |
|---|---|
| `s.Len()` | The length in bytes, like `len(s)` |
| `s.Trim()` | `s` without white space at either end, as a borrow: Go's `strings.TrimSpace` |
| `s.Fields()` | The pieces between white space, each a borrow; use it in a `for` |
| `s.Lines()` | The lines, without their newline or a carriage return before it, each a borrow; a last line without a newline counts, a trailing newline makes no empty one. Go's `strings.Lines` keeps each newline |
| `s.Split(sep)` | The pieces between each `sep`, likewise; an empty `sep` gives each UTF-8 sequence, and none of an empty string |
| `s.SplitOnce(sep)` | The parts before and after the first `sep`, as `(ref string, ref string)?` |
| `s.SplitN(sep, n)` | At most `n` pieces, the last holding the rest; zero or less gives none, since `Split` is the name for all |
| `s.TrimSpace()` | The same as `Trim`, under Go's name |
| `s.TrimPrefix(p)`, `s.TrimSuffix(p)` | `s` without `p` at its start or end, as a borrow; `s` itself when `p` is not there |
| `s.Index(t)`, `s.LastIndex(t)` | The offset of the first or last `t`, as `int?`: `None` when it is not there |
| `s.Count(t)` | How many times `t` occurs without overlapping; an empty `t` counts the runes plus one |
| `s.Replace(old, new, n)`, `s.ReplaceAll(old, new)` | A new, owned string with the first `n`, or every, `old` replaced; `n` of zero or less replaces none |
| `s.HasPrefix(t)`, `s.HasSuffix(t)`, `s.Contains(t)` | Whether `t` begins, ends or appears in `s` |
| `s.ToUpper()`, `s.ToLower()` | A new, owned string, every letter changed by Unicode's simple case mapping |
| `s.Clone()` | A new, owned copy of `s` |

Text is UTF-8 and the methods read it as Go's `strings` package does: `ToUpper` and `ToLower` change every letter, `"àéî".ToUpper()` is `ÀÉÎ`, white space for `Trim` and `Fields` is what Unicode calls white space, a no-break space or an ideographic space among it, and a byte that is not valid UTF-8 reads as the replacement character U+FFFD, which `ToUpper` writes in its place. `compiler/emit/internal/backendtest/testdata/unicode.snello` checks each against Go's output. A slice expression out of range stops the program in Go's words, `slice bounds out of range [3:1]` or `[:10] with length 3`, and so does `make` with a negative length or a capacity below it, `makeslice: len out of range`.

The pieces `Fields`, `Lines`, `Split` and `SplitN` yield borrow the string they came from, so `for w := range text.Fields()` lends `text` for the loop. To keep a piece, clone it or build an owned string from it, which is what `wordfreq` does with `w.ToLower()` before using the word as a map key. `SplitOnce` is made for `if` with a binding: A variable holding the pieces holds the string as long as it is used, as a variable holding a `ref string` does: `parts := s.Split(",")` then `s = t` while `parts` is still read is refused, `cannot assign to s: s is borrowed by parts`, and pieces of another string pushed or extended into `parts` make it hold that string too.

```go
if (k, v) := line.SplitOnce("=") {
    settings.Insert(k.Trim().Clone(), v.Trim().Clone())
}
```

## Maps

A map holds one value per key. A map literal lists its entries, or none, and `make(map[K]V)` is the same as the empty literal:

```go
kv := map[string]string{}
counts := map[string]int{}
```

`m[k] = v` stores a value, making the entry when the key is new, and `m[k]` reads one. Reading a key that is not there gives the value type's zero value and leaves the map unchanged, so `counts[w] += 1` counts a word whether or not it was seen before. For values with no zero value, chapter 18, reading `m[k]` and writing part of it, `m[k].f = v`, are refused: `m.Get(k)` and `m.GetMut(k)` say whether the key is there, and `m[k] = v` stores a whole value. `len(m)` is the number of entries.

The methods say what happens to the entry:

| Method | What it does |
|---|---|
| `m.Get(k)` | The value as a borrow, `ref V?`: `None` when the key is absent |
| `m.GetMut(k)` | The same as a `mut V?`, to change the value in place |
| `m.Insert(k, v)` | Stores `v` under `k`, replacing what was there |
| `m.GetOrInsert(k, v)` | The value under `k` as a `mut V`, storing `v` first when the key is absent; one lookup either way, and `k` and `v` are dropped when the key is there |
| `m.Remove(k)` | Takes the entry out and returns its value as `V?` |
| `m.Contains(k)` | Whether the key is there |
| `m.Clone()` | A new map with a clone of every entry; its keys and values must be `Clone` |

`GetOrInsert` is the counter's method when `m[k] += 1` will not do, a value with no zero value or one to build only once: `*groups.GetOrInsert(key, 0) += 1`.

`Get` and `GetMut` answer with an optional because the entry may be absent, which is what `config` relies on:

```go
text := kv.Get(ref key) ?? return Err(Missing(key))
```

A key is any type that is `Eq` and `Hash` (chapter 11): numbers, strings, booleans, enums and tuples of those by their structure, and a struct that lists `Hash`. A slice, a map or a trait value cannot be a key, and the compiler says so at the map type. A map owns its keys and values, so inserting a borrowed string clones it first: `kv.Insert(k.Trim().Clone(), v.Trim().Clone())`.

`for k, v := range m` walks the entries in the order they were inserted, which is also the order a map prints in: `map[one:1 two:2]`. Through a `mut` borrow the values are lent for writing, as slice elements are; ranging over an owned map by value moves each key and value out, which is how `wordfreq` turns its counts into a list to sort.

Two maps do not compare with `==`, for the same reason two slices do not.

## Deques and sets

Two collections of the standard library stand beside the slice and the map. A `deque.Deque<T>` is a queue with two ends: `PushBack` and `PopFront` make a queue, `PushBack` and `PopBack` a stack, and `Front`, `Back` and `Get` read without removing. Each takes constant time, a push amortized over the doubling of the buffer, since the elements lie in a ring over one buffer, where a slice used as a queue moves every remaining element down on each `Remove(0)`. A deque has no zero value, since it frees its buffer when it goes: one comes from `deque.New<T>()` or `deque.WithCapacity<T>(n)`. A deque prints as `deque[a b c]` and a set as `set[a b c]`.

```go
queue := deque.New<NodeID>()
queue.PushBack(start)
for {
	n := queue.PopFront() ?? break
	visit(n)
}
```

A `set.Set<T>` holds each value once, for a `T` that is `Eq` and `Hash` as a map's key is. `Insert` adds a value and reports whether it was new, `Contains` asks, `Remove` takes one out and reports whether it was there, and `set.From(xs)` makes one from a slice. It is a map of the values to nothing, as Go writes `map[T]struct{}`, so it costs what the map costs.

```go
seen := set.New<string>()
for _, w := range ref words {
	if !seen.Insert(w.Clone()) {
		fmt.Println("again:", w)
	}
}
```

Both are walked with `range` over their `Iter`, which lends each element for the loop: `for v := range d.Iter()` from front to back, and `for v := range s.Iter()` in the order the values were inserted. A walk over a collection is a value with a `Next` method, chapter 3, and the loop holds the collection lent until it ends, so the body reads it and cannot change it.

## Arrays

An array, `[4]int`, is a value of a fixed length: zero-valued by `var`, written as a literal with its elements in order, `[3]int{1, 2}` leaving the rest zero, indexed to read and write with the same bounds check as a slice, and copied whole on assignment, so a struct of arrays is `Copy` when its elements are. `len` and `cap` are its length. An array prints as a slice does, `[0 5 10 0]`, is cloned element by element inside a type that lists `Clone`, and drops each element that needs it. It is not a slice: it is not sliced, pushed to or ranged over yet, and an array is not `Eq` or `Hash` by structure, so a struct holding one writes its own `Eq`. `compiler/emit/internal/backendtest/testdata/arrays.snello` and `examples/matrix/matrix.snello`, whose `Mat4` holds a `[16]f64`, run them.

## What the compiler reports

| Message | Meaning |
|---|---|
| `cannot index a value of type int` | Only slices, arrays, maps and strings index |
| `index must be an integer, not string` | A slice index is an integer of any integer type |
| `values of type []int cannot be compared with ==` | Compare elements, or use `Contains`; the same for maps |
| `index out of range [5] with length 3` | At run time: the program stops instead of reading past the end |

## What a value costs

A slice is three words, a pointer to its elements, a length and a capacity, and its elements lie one after another in one allocation. A map is eight words: its keys and values lie in insertion order in one block, with each key's hash, and an index finds them, a Swiss table as Go's and Rust's maps are, which compares keys only where seven bits of the hash already match; a removed entry leaves its place until the next growth compacts the block. Keys are hashed with a seed drawn when the program starts, as in Go and Rust, so keys chosen to collide cannot be prepared ahead; the seed changes nothing a program sees, since a range follows insertion order. `make(map[K]V, n)` makes room for `n` entries at once. `Push` doubles the room when it runs out, so appending `n` elements allocates about `log n` times. A string is two words, a pointer and a length, and a literal points at the program's own bytes; only `+`, `Clone`, `ToUpper` and `ToLower` allocate. A borrow of part of either is the same two or three words pointing into the middle, and allocates nothing.
