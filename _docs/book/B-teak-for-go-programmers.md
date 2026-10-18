# Appendix B. Teak for Go programmers

Status: grows with the chapters; covers chapters 3 to 6.

The chapters describe Teak on its own terms and name no other language. This appendix is for readers who come from Go: for each chapter, what carries over unchanged and what to unlearn. Teak is not a Go dialect, and Go programs are not Teak programs; the syntax is familiar so that the ideas get your full attention.

## Chapter 3: Basics

Unchanged: `package`, `import`, `:=`, `var`, zero values, exported names by capitalization, `func` with named parameters and grouped types, multiple results, closures, `if`, `for` and its range forms, `switch`, `defer`, `len`, `cap`, `make`, `panic`, semicolon insertion.

| In Go | In Teak |
|---|---|
| `int32`, `float64`, `uint8` | `i32`, `f64`, `u8`; `int`, `uint`, `byte`, `rune` as before |
| Untyped constants: `x := 10 * 2.5` | A literal takes its type from context; mix types with a conversion |
| `s[a:b]` copies a string header; the bytes are shared by the GC | `s[a:b]` lends a `ref string`; the original must outlive it |
| `strings.ToLower(s)`, `strings.Fields(s)` | `s.ToLower()`, `s.Fields()` |
| `go f(x)` | `spawn f(x)` |
| `if x := f(); x > 0 {` | Same. `if x := opt {` with no `;` is a binding on an optional |
| `append`, `delete`, `copy`, `new` | `xs.Push(v)`, `m.Remove(k)`, `xs.Clone()`, `&expr` |
| `nil` | None. An absent value is `T?` |
| `a, b := f()` for two results | Same; several results are a tuple, `return (n, right)` |
| An `if` is a statement | An `if` with an `else` is also an expression |
| A newline after `>` continues the line | A newline after `>` ends the line |

## Chapter 4: Structs, enums and match

Unchanged: struct fields, keyed literals, dot access, methods with receivers, `type Name T`.

| In Go | In Teak |
|---|---|
| `type Node struct { ... }` | `struct Node { ... }` |
| Value and pointer receivers, `(t Tree)` and `(t *Tree)` | Three receivers: `(t ref Tree)` reads, `(t mut Tree)` changes, `(t Tree)` takes |
| A `const` block with `iota`, or an interface with one struct per case | `enum`, with fields per variant |
| A type switch | `match`, exhaustive, usable as an expression |
| `switch` on an enum constant | `match`; a missing case is a compile error |
| `v, ok := m[k]` | `if v := m.Get(ref k) {` |

## Chapter 5: Ownership

Nothing here has a Go counterpart; the garbage collector made the questions disappear. What to unlearn:

| In Go | In Teak |
|---|---|
| Assignment copies the value or the pointer; both names stay valid | Assignment moves; the old name is dead unless the type is `Copy` |
| Passing a slice or a struct copies its header or its bytes | Passing by value moves; pass `ref` or `mut` to keep it |
| `defer f.Close()` | A file closes itself when dropped; `f.Close()` consumes it and returns the error |
| Finalizers run eventually, or never | `Drop` runs at scope exit, early return and panic, in order |
| `x := xs[0]` copies the element | `xs[0]` lends the element; `xs.Remove(0)`, `xs.Pop()` or `take` moves it |
| `tmp := head; head = head.next` | `old := take(mut s.head)` |
| Copy a struct and hope it had no shared state | `s.Clone()` is the only copy, and it is explicit |

## Chapter 6: Borrows

| In Go | In Teak |
|---|---|
| `func f(r *Rect)`: no copy, and maybe mutated, maybe kept, maybe nil | `func f(r ref Rect)` reads, `func f(r mut Rect)` changes; neither can keep it or be nil |
| `f(&x)` | `f(mut x)` to change `x`; `f(x)` or `f(ref x)` to read it |
| `p.field` through a pointer; `*p = v` to replace | The same: `n.value` through a borrow; `*cur = v` to replace |
| Append under a live pointer reallocates silently | `xs.Push(v)` while an element borrow is live is a compile error |
| Modify a map during `range`: a runtime surprise | A compile error: a write during a read |
| Return a pointer to a local; the GC keeps it alive | `cannot return a borrow of local s`; return the value |
| A closure capturing a local by reference, returned from `counter()` | Rejected; put the state in a struct with a method |
| A struct holding a `*string` into a buffer | A struct holding `ref string` is itself a borrow: local use only |
| Pass the same slice to five goroutines | `a spawned task cannot hold a borrow`; move it, clone it, or share it through `sync.Arc` |
