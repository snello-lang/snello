# 3. Basics

Status: the syntax in this chapter is implemented (stage 0, `teak parse`). Types and names are checked from stage 1.

Teak programs look like Go programs. This chapter walks through the parts that a Go developer already knows and marks the places where Teak differs. If you know Go, read only the paragraphs that begin with **Different from Go**.

## A program

```teak
package wordfreq

import ("fmt"; "os")

struct Count { word string; n int }

func main() {
    text := os.ReadToString("input.txt").Expect("read input")
    counts := map[string]int{}
    for w := range text.Fields() {
        counts[w.ToLower()] += 1
    }
    fmt.Println(len(counts))
}
```

A file starts with `package`, then its imports, then declarations in any order. Imports are grouped in parentheses, one per line or separated by `;`, and every package is referred to by its name: `fmt.Println`, `os.Exit`. Names that start with a capital letter are exported from the package; the others are private to it. Comments are `//` to the end of the line. A statement ends at the end of its line, so there are no semicolons to write, and the braces go where Go puts them: `{` on the same line as `func`, `if` or `for`.

**Different from Go.** A line may end in `>` as well, because a generic type can close a line: `cache sync.Arc<sync.RwLock<Entry>>`. The cost is that a comparison cannot break its line after the operator. Keep the right operand on the same line.

## Variables

```teak
count := 0
var x, y int = 1, 2
path := []string{}
s := Stack{}            // zero value: every field at its zero value
```

`:=` declares and infers, `var` declares with a type, and every type has a zero value, as in Go. Assignment is `=`, and compound assignment is `+=`, `-=` and the rest. A name declared and never used is an error.

**Different from Go.** Assigning a value to a new name may move it. For a number or a `bool` nothing changes: `y := x` copies, and both names are usable. For a string, a slice, a struct or anything that owns memory, `t := s` moves the value to `t`, and `s` cannot be used afterwards. Chapter 5 explains why; for this chapter, know that the compiler tracks who owns each value and that most code never notices.

## Numbers

| Kind | Types |
|---|---|
| Signed integers | `i8`, `i16`, `i32`, `i64`, `int` |
| Unsigned integers | `u8`, `u16`, `u32`, `u64`, `uint` |
| Floating point | `f32`, `f64` |
| Aliases | `byte` is `u8`, `rune` is `i32` |

`int` and `uint` are the word size of the machine, and `int` is the index type, as in Go. There is no `uintptr`, no `i128` and no complex type.

**Different from Go.** The sized types are spelled the way most newer compiled languages spell them: `i32` where Go writes `int32`, `f64` where Go writes `float64`. This is the one place Teak's vocabulary leaves Go's on purpose.

A literal takes the type its context asks for, and a conversion is a call of the type name, as in Go:

```teak
v := f64(first - '0')               // first is a byte; v is f64
v = v*10 + f64(l.src[l.pos] - '0')  // 10 is f64 because v is
n = n*10 + int(c - '0')
```

**Different from Go.** There is no untyped constant arithmetic. `10` is an `f64` in the first line because `v` is, and an `int` in the last because `n` is; a literal never carries a type of its own into an expression. Integer arithmetic wraps on overflow as in Go, and checked arithmetic is a library function that returns `T?`.

## Booleans and strings

`bool` has `true` and `false`, `&&`, `||` and `!`, exactly as in Go.

A `string` is UTF-8 bytes, immutable, with `len(s)` the length in bytes and `s[i]` a byte. Character literals such as `'+'` and `'0'` are written as in Go and take the type the context asks for, usually `byte`.

```teak
for l.pos < len(l.src) && l.src[l.pos] == ' ' { l.pos += 1 }
c := l.src[l.pos]
text := l.src[start:l.pos]
target := tmp.path + "/copy.txt"
```

**Different from Go.** A string owns its bytes, so `s[a:b]` does not copy and does not produce a second owner: it lends the piece, as a `ref string`, for as long as the original is around. `+` reads both sides and builds a new string. The functions Go keeps in the `strings` package are methods on the value: `s.Fields()`, `s.Split("\n")`, `s.SplitOnce("=")`, `s.Trim()`, `s.HasPrefix(p)`, `s.Contains(t)`, `s.ToLower()`, and `s.Clone()` makes an owned copy. Chapter 9 lists them.

## Functions

```teak
func Copy(src ref string, dst ref string) Result<i64, error> { ... }

func popMin(n *Node) (*Node, *Node?) {
    match take(mut n.left) {
        None    => { right := take(mut n.right); return (n, right) },
        Some(l) => { min, rest := popMin(l); n.left = rest; return (min, Some(n)) },
    }
}
```

A function is declared with `func`, its parameters are named before their types, and names that share a type are grouped: `func(from, to NodeID, w int)`. Several results are written as a tuple type, `(*Node, *Node?)`, returned as a tuple, `return (n, right)`, and taken apart at the call, `min, rest := popMin(l)`.

**Different from Go.** A parameter says how the function takes its argument. `src ref string` reads it; `out mut File` changes it in place; a plain `n *Node` takes ownership of it. Go has `T` and `*T` for this and leaves the intent to a comment; Teak writes the intent and checks it. Chapter 6 covers the three modes. Until then, read `ref` as "borrowed to read" and `mut` as "borrowed to change".

Function literals are closures and are written as in Go. A closure that only reads a variable borrows it; one that assigns to it borrows it mutably:

```teak
count := 0
t.Walk(0, func(n ref Node) { count += 1 })
fmt.Println(count)                 // 3
```

## Control flow

`if`, `for`, `switch`, `break`, `continue`, `return` and `defer` are Go's, braces required and no parentheses around conditions. The init form `if x := f(); x > 0 {` works. `for` has Go's three forms and its range forms: `for i := range xs` yields indexes, `for i, x := range xs` indexes and elements, `for k, v := range m` keys and values, `for x := range ch` received values, and `for range ch` receives and discards.

```teak
for i := 0; i < len(s); i += 1 {
    c := s[i]
    if c < '0' || c > '9' { return None }
    n = n*10 + int(c - '0')
}

for _, k := range []int{5, 3, 8, 1, 4} { t.Insert(k, fmt.Sprint(k)) }
```

**Different from Go.** `if` is also an expression when every branch yields a value, and then the `else` is required:

```teak
pat := if ci { "(?i)" + p } else { p.Clone() }
```

**Different from Go.** `if x := opt {`, a short declaration followed directly by `{`, is not Go's init form. It binds `x` when the optional `opt` holds a value and skips the block when it does not. The `;` tells the two apart. Chapter 8 covers optionals; chapter 4 covers `match`, which replaces Go's type switch and most uses of `switch` on an enum.

`defer` and `spawn` take a call, as Go's `defer` and `go` do. `spawn f(x)` starts a task where Go writes `go f(x)`; chapter 10 covers it.

## Built-in functions

`len`, `cap`, `make` and `panic` are Go's. `print` writes to standard error for debugging; `fmt.Println` is the one to use. `take(mut place)` moves a value out of a field or element and leaves the zero value behind; it belongs to chapter 5.

**Different from Go.** There is no `append`, `delete`, `copy` or `new`. Slices and maps have methods instead, `xs.Push(v)`, `m.Remove(k)`, and `&expr` allocates (chapter 7). There is no `nil`: a value that may be absent has type `T?` (chapter 8).
