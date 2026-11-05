# 3. Basics

Status: implemented. The syntax is parsed (stage 0), the types and names are checked (stage 1, task 1.4, `snello check`), and everything here except `switch` and `spawn` compiles to native code (stage 1, task 1.5).

This chapter is the shape of a Snello program: files, names, values, functions and control flow. Everything later in the book is built from these parts.

## A program

```snello
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

A file starts with `package`, then its imports, then declarations in any order. Imports are grouped in parentheses, one per line or separated by `;`, and every imported package is referred to by its name: `fmt.Println`, `os.Exit`. Chapter 14 lists the packages. A program starts at `func main()` in package `main`.

Names that start with a capital letter are exported from their package; the others are private to it. Comments run from `//` to the end of the line. A statement ends at the end of its line; a line ends after a name, a literal, a closing bracket, `?` or `>`, so a comparison or a shift keeps its right operand on the same line as the operator. Braces open on the line of the `func`, `if` or `for` they belong to.

## Variables

```snello
count := 0
var x, y int = 1, 2
path := []string{}
s := Stack{}            // zero value: every field at its zero value
```

`:=` declares a variable and infers its type from the value. `var` declares with a type written out, and a variable declared without a value starts at its type's zero value: `0`, `false`, `""`, an empty slice or map, a struct with every field at its zero value. Assignment is `=`, and compound assignment is `+=`, `-=` and the rest. `const` declares a name for a literal, `const Scale f64 = 2.5`, inside a function or at the top level of a file, where `var` is not compiled yet.

Assigning a value to a new name hands the value over. For a number or a `bool` nothing is lost: `y := x` leaves both names usable. For a string, a slice, a struct or anything else that owns memory, `t := s` moves the value to `t`, and `s` cannot be used afterwards. Chapter 5 explains the rule; most code never meets it, because a value usually flows forward from where it is made to where it is needed.

## Numbers

| Kind | Types |
|---|---|
| Signed integers | `i8`, `i16`, `i32`, `i64`, `int` |
| Unsigned integers | `u8`, `u16`, `u32`, `u64`, `uint` |
| Floating point | `f32`, `f64` |
| Aliases | `byte` is `u8`, `rune` is `i32` |

`int` and `uint` are the word size of the machine, and `int` is the type of an index and a length. There is no `uintptr`, no `i128` and no complex type.

A literal takes the type its context asks for, and a conversion is a call of the type name:

```snello
v := f64(first - '0')               // first is a byte; v is f64
v = v*10 + f64(l.src[l.pos] - '0')  // 10 is f64 because v is
n = n*10 + int(c - '0')
```

A literal never carries a type of its own into an expression: `10` is an `f64` in the second line because `v` is, and an `int` in the third because `n` is. Mixing two number types in one expression needs a conversion. Integer arithmetic wraps on overflow; checked arithmetic is a library function that returns `T?` (chapter 8).

## Booleans and strings

`bool` has `true` and `false`, with `&&`, `||` and `!`.

A `string` is a sequence of UTF-8 bytes that owns its memory and never changes. `len(s)` is its length in bytes and `s[i]` is a byte. Character literals such as `'+'` and `'0'` take the type the context asks for, usually `byte`.

```snello
for l.pos < len(l.src) && l.src[l.pos] == ' ' { l.pos += 1 }
c := l.src[l.pos]
text := l.src[start:l.pos]
target := tmp.path + "/copy.txt"
```

`s[a:b]` lends a piece of the string, as a `ref string`, for as long as the original is around; it copies nothing (chapter 6). `+` reads both sides and builds a new string. The operations on strings are methods on the value: `s.Fields()`, `s.Split("\n")`, `s.SplitOnce("=")`, `s.Trim()`, `s.HasPrefix(p)`, `s.Contains(t)`, `s.ToLower()`, and `s.Clone()` makes an owned copy. Chapter 9 lists them.

## Functions

```snello
func Copy(src ref string, dst ref string) Result<i64, error> { ... }

func popMin(n *Node) (*Node, *Node?) {
    match take(mut n.left) {
        None    => { right := take(mut n.right); return (n, right) },
        Some(l) => { min, rest := popMin(l); n.left = rest; return (min, Some(n)) },
    }
}
```

A function is declared with `func`, its parameters are named before their types, and names that share a type are grouped: `func(from, to NodeID, w int)`. Several results are written as a tuple type, `(*Node, *Node?)`, returned as a tuple, `return (n, right)`, and taken apart at the call, `min, rest := popMin(l)`.

A parameter says how the function takes its argument. `src ref string` reads it; `out mut File` changes it in place; a plain `n *Node` takes ownership of it. Chapter 6 covers the three modes. Until then, read `ref` as "borrowed to read" and `mut` as "borrowed to change".

Function literals are closures. A closure that only reads a variable borrows it; one that assigns to it borrows it mutably:

```snello
count := 0
t.Walk(0, func(n ref Node) { count += 1 })
fmt.Println(count)                 // 3
```

## Control flow

`if`, `for`, `switch`, `break`, `continue`, `return` and `defer` take braces and no parentheses around their conditions. `break` ends the innermost loop and `continue` goes to its next iteration, wherever they stand inside it: a `switch`, a `match` or a `select` is not a loop and does not catch them, and outside a loop both are errors. An `if` may start with a statement: `if x := f(); x > 0 {`. `for` has three forms, an endless `for {`, a condition `for cond {`, and a counter `for i := 0; i < n; i += 1 {`, and a range form: `for i := range xs` yields indexes, `for i, x := range xs` indexes and elements, `for k, v := range m` keys and values, `for x := range ch` the values received from a channel, and `for range ch` receives and discards them.

```snello
for i := 0; i < len(s); i += 1 {
    c := s[i]
    if c < '0' || c > '9' { return None }
    n = n*10 + int(c - '0')
}

for _, k := range []int{5, 3, 8, 1, 4} { t.Insert(k, fmt.Sprint(k)) }
```

`if` is also an expression when every branch yields a value, and then the `else` is required:

```snello
pat := if ci { "(?i)" + p } else { p.Clone() }
```

`if x := opt {`, a short declaration followed directly by `{`, is a binding, not an init statement. It binds `x` when the optional `opt` holds a value and skips the block when it does not; the `;` tells the two forms apart. Chapter 8 covers optionals, and chapter 4 covers `match`, which handles enums and replaces most uses of `switch`.

`defer` runs a call when the enclosing function returns, after the return value is computed. The arguments are evaluated where the `defer` stands, and several deferred calls run last first. `spawn f(x)` starts a task running the call; chapter 10 covers it. Both take a call and nothing else.

## Built-in functions

`len` gives the length of a slice, map or string and `cap` the capacity of a slice; `make` builds a slice, map or channel of a given size; `panic` stops the program with a message; `min` and `max` give the least and the greatest of two or more numbers of one type; `print` is a debugging aid, and `fmt.Println` is the one to use for output. `take(mut place)` moves a value out of a field or element and leaves the zero value behind; it belongs to chapter 5.

Slices and maps have methods rather than functions: `xs.Push(v)`, `m.Remove(k)` (chapter 9). `&expr` allocates a value on the heap (chapter 7). There is no `nil`: a value that may be absent has type `T?` (chapter 8).
