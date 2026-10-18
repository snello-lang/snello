# Syntax conventions

Status: draft, 2026-10-17. These are the rules the programs in `../../examples/` assume. Each was tested by writing those programs; when an example reads badly, the rule changes here first. The guiding principle: **if Go already has a way to say it, Teak says it the same way.** The principle covers syntax and keywords, which are what make Teak readable at first sight. The spelling of the primitive number types is vocabulary, not grammar, and there Teak follows the convention of the newer systems languages (`i32`, `f64`); see the number types below and `05-standard-library.md`. That is the one deliberate departure, stated once so it does not become a slope. New syntax exists only where Go has no answer.

## Carried over from Go unchanged

`package`, `import`, `func`, `type ... struct`, `:=`, `var`, `for`, `if`, `switch`, `defer`, channels and `select`, slices, maps, methods with named receivers, multiple return values, exported names by capitalization, zero values, mutable parameters and locals.

`for` ranges exactly as in Go: `for i := range xs` yields the index, `for i, x := range xs` the index and element, `for k, v := range m` key and value, `for x := range ch` the received value. An iterator ranges like a channel: `for x := range it` yields each item.

## Ownership and borrowing

Full reasoning in `04-borrows-ref-and-mut.md`. The rules:

- Every value has one owner. Passing or assigning by value **moves** it, unless its type is `Copy`.
- `ref T` is a shared read-only borrow; `mut T` is an exclusive mutable borrow. Both are written at the declaration and at the use site: `func Len(xs ref []T)`, called as `Len(ref xs)`.
- Receivers: `func (t ref Tree) Len()`, `func (t mut Tree) Push()`, `func (f File) Close()` (consumes).
- A borrow lives from its creation to its last use within one function. It cannot be stored in an owned struct, captured by `spawn`, or sent on a channel.
- A borrow may be returned only when tied to one input: the receiver (`func (s ref Stack<T>) Peek() ref T?`) or the sole borrowed parameter (`func Trim(s ref string) ref string`). Two borrowed inputs and a borrowed output is a compile error; clone or return an index.
- Any number of `ref` or exactly one `mut` at a time.
- A borrow variable may be reassigned to a borrow derived from itself: `cur = mut n.left` where `n` was reached through `cur`. This is what makes walking a chain of boxes possible (example 20).
- **Borrow-holding structs and closures.** A struct with a `ref` or `mut` field, and a closure that captures by borrow, are themselves borrows and obey every borrow rule. A lexer holding `src ref string` is legal; storing that lexer in an owned struct is not (examples 05, 11, 18).
- Field access through a borrow needs no `*`: `n.value`. Assigning a whole new value through a `mut` borrow does: `*v = *v * k`.
- `take(mut place)` moves the value out of a field or element and leaves the type's zero value (`None` for an option) behind.
- Indexing an owned slice yields a borrow. To move an element out use `xs.Remove(i)`, `xs.Pop()`, or `take` on an optional element.
- Ranging: `range xs` on an owned collection consumes it, moving each element; `range ref xs` yields `ref` elements; `range mut xs` yields `mut` elements. A collection that is already a borrow ranges as that borrow.
- Slices have methods `Push`, `Pop`, `Remove`, `Reverse`; `append` remains as in Go.
- `+` on strings borrows both operands and returns a new string.

## Grammar rules decided by the parser

These came out of writing the parser and are binding on every example.

- **Where `?` binds.** `?` applies to the whole pointer or borrow before it, and to the element inside a container. So `*Node?` is an optional pointer, `ref T?` is an optional borrow, `[]int?` is a slice of optional ints, and `map[K]V?` is a map whose values are optional. To make a whole container optional, parenthesize: `([]int)?`.
- **`<` in a type** always opens a type argument list: `List<T>`, `arena.Index<Node>`. There is no less-than in a type, so there is nothing to confuse it with. The `>>` that closes `Arc<RwLock<T>>` is two closing brackets; the parser splits it.
- **`<` in an expression** is less-than unless the tokens after it form a type list closed by `>` that is directly followed by `(` or `{`, in which case it is a type argument list on a call or a composite literal: `arena.New<Node>()`, `Stack<string>{}`. The parser tries the type-argument reading first and backs up if it fails. In practice `a < b` is never followed by `>` and `(`, so ordinary comparisons are unaffected; the one thing to avoid is `a < b > (c)`, which reads as a generic call.
- **Parameter names versus types.** In `func(from, to NodeID, w int)` the identifiers before a type are names sharing it; in `func(T, U) V` they are types. An identifier followed by a comma is decided by what ends its group, as in Go.
- **Array lengths** are integer literals.
- **A newline after `>` ends the line**, as it does after an identifier, a `)` or a `?`. Go inserts no semicolon after `>`, but in Teak a line ends in `>` or `>>` whenever a generic type closes it: `cache Arc<RwLock<Entry>>`. The cost is that a comparison or a shift cannot break its line after the operator; keep the right operand on the same line.
- **`var` and `const`** declare one name list per keyword: `var x, y int = 1, 2`. Grouping with parentheses is not decided yet; nothing in the examples needs it.
- **`??` sits below every other operator and groups to the right.** `a ?? b == c` is `a ?? (b == c)`, and `a ?? b ?? c` is `a ?? (b ?? c)`. Its right side may be an expression, a block, or a `return`, `break` or `continue`.
- **Postfix `?` binds tighter than any prefix operator.** `-x?` is `-(x?)` and `ref x?` is `ref (x?)`.
- **A `{` after a name opens a composite literal, except in a control clause.** In `if x == y {` the brace opens the body, as in Go. Write `if x == (T{}) {` to compare against a literal there. A type written out is never ambiguous, so `for _, k := range []int{5, 3} {` is fine.
- **A type may stand as an expression** where Go allows it: as the argument of `make`, as the function of a conversion such as `[]byte(s)`, and as the type of a composite literal.
- **An `if` binding is a short declaration followed directly by `{`.** `if x := opt {` binds; `if x := f(); x > 0 {` is Go's init-and-condition, told apart by the `;`. The left side of a binding is a name, `_`, a tuple of them, or a variant such as `Some(n)`.
- **`spawn` starts a task**, where Go writes `go`. It is the one keyword Teak renames: `go` is the name of another language, and `spawn` is the verb Rust, Erlang and Elixir use for the same thing. Not `async`, which in every language that has it marks a function for a later `await`; Teak has no `await` and no two kinds of function. `spawn` and `defer` take a call, as Go's `go` and `defer` do; any other expression is an error. A `for` may range with no variable: `for range ch {`.
- **A match arm is `pattern [if guard] => body`, ended by a comma or a newline.** The body is an expression, a block, or one statement: `Stop => return,`, `Balance(reply) => reply <- n,`, `"-i" => o.ignoreCase = true,`. Because a comma ends the arm, a `return` in an arm carries one expression, and several results are written as a tuple: `return (n, right)`. A bare name in a pattern is a binding or a constant variant such as `None`; the checker tells them apart. A parenthesized single pattern is that pattern.
- **One name on the left takes one expression on the right.** `x := f()`; only a list takes a list, `a, b := b, a`, as in Go.
- **An `if` expression has an `else`.** Every path yields a value, so `x := if c { a }` without an `else` is an error.
- **`impl` goes after `struct` or `enum`**: `type Circle struct impl Shape { ... }`. Written before them it is reported and still recorded, so the rest of the declaration is checked.

## Heap allocation

`*T` is an owned pointer to a heap value, and `&expr` allocates: the Go spellings, with one owner. Used for recursive types: `next *Node?`, built with `&Node{...}`. It dereferences automatically for field access and method calls. There is no `nil`; an absent pointer is `*T?`.

Go's other uses of `*T`, sharing a value or mutating it through a pointer, are `ref` and `mut` in Teak. A `*T` cannot alias: it is moved like any owned value.

`Rc<T>` and `Weak<T>` are shared single-task ownership; `Arc<T>` is shared across tasks. `Arena<T>` with `Index<T>` is the idiom for linked structures (`02-arenas-and-indices.md`). These are standard library types.

## Types

Number types, with `int` and `uint` the platform word size and `int` the index type as in Go; `byte` is `u8` and `rune` is `i32`:

| Kind | Types |
|---|---|
| Signed integers | `i8`, `i16`, `i32`, `i64`, `int` |
| Unsigned integers | `u8`, `u16`, `u32`, `u64`, `uint` |
| Floating point | `f32`, `f64` |

No `i128`, no complex numbers, no `uintptr`. Integer arithmetic wraps as in Go; checked arithmetic is a library function returning `T?`. A literal takes the type its context asks for.

- Generics with angle brackets: `List<T>`, `func Map<T, U>(...)`. Bounds with a colon: `T: Ord + Clone`.
- Sum types: `type Shape enum { Circle(r f64); Rect(w, h f64) }`.
- `match` with exhaustiveness checking. Patterns: literals, variants with bindings, tuples `(a, b)`, ranges `'0'..='9'`, alternatives `|`, guards `if cond`, wildcard `_`.
- `Option<T>` written `T?`, with `Some(x)` and `None`. `Result<T, E>` with `Ok(x)` and `Err(e)`. No `nil`.
- `x?` propagates `None` or `Err` from a function returning the same kind.
- `x ?? expr` unwraps or evaluates `expr`, which may `return`, `break`, or `continue`: `n := take(mut s.head) ?? return None`.
- `if x := opt { ... }` binds `x` when `opt` is `Some`; tuples destructure: `if (k, v) := line.SplitOnce("=") { ... }`.
- `.Expect("why")` unwraps or panics with the message.
- `if` and `match` are expressions when every branch yields a value: `pat := if ci { "(?i)" + p } else { p.Clone() }`, `return match e { ... }`.
- Receiving from a channel yields `T?`: `None` once the channel is closed and drained. `for x := range ch` yields `T` and ends on close.

## Traits

Full reasoning in `01-language-design.md`. Traits are Go interfaces that may also carry default methods and fields.

```teak
trait Shape {
    func (s ref Self) Area() f64                  // required
    func (s ref Self) Describe() string {             // default
        return fmt.Sprintf("area %.2f", s.Area())
    }
}

trait Named {
    var name string                                   // a field every implementor gets
    func (n ref Self) Name() string { return n.name }
}

type Circle struct impl Shape, Named {
    r f64
}

func (c ref Circle) Area() f64 { return math.Pi * c.r * c.r }
```

- A type declares what it implements with `impl` on its declaration: `type Circle struct impl Shape, Named { ... }`. The same for enums. The compiler checks every required method exists. There are no impl blocks; methods are ordinary Go methods.
- Trait fields are stored inline in the implementing struct and set at construction: `Circle{r: 1, name: "circle"}`. A trait field has no default value; `var name string = "x"` inside a trait is an error.
- Overriding a default is defining the method. `super.Method()` inside an override calls the next definition in the impl order.
- Resolution order: the type's own methods, then its impl list from right to left, each trait followed by the traits it refines. Resolved at compile time; every call is direct.
- A trait refining another: `trait Logged: Shape { ... }`.
- Marker and operator traits use the same mechanism: `type Vec3 struct impl Copy, Eq, Add { ... }` with a method `func (a Vec3) Add(b Vec3) Vec3`.
- Bounds: `func Total<T: Shape>(xs ref []T)`. Monomorphized per `T`.
- **A trait used as a type is a Go interface value.** `[]Shape` holds any mix of implementing types; each element owns its value. One allocation and one indirect call per value, the same cost as Go. Such a value is `Send` only if the trait requires it: `trait Shape: Send { ... }`.
- Not in version one: self-types, implicits.

## Concurrency

Full reasoning in `03-ownership-across-tasks.md`.

- `spawn f(x)` moves `x` into the new task. A closure passed to `spawn` moves everything it captures.
- Sending moves the value; receiving owns it. Channel handles are `Copy`.
- `Send` is inferred from structure and never written.
- `Mutex<T>` and `RwLock<T>` own their data; `Lock()` returns a guard that is a `mut T` and unlocks on drop.
- `sync` types synchronize internally and are used through `ref`.

## Closures

A closure captures each variable in the weakest mode its body needs: `ref` if it only reads it, `mut` if it assigns to it, by move if it consumes it or the closure is passed to `spawn`. A closure that captures by borrow is a borrow (see above). A closure that captures only owned or `Copy` values may be stored and returned.

## Destructors

A type that implements `Drop` has `func (t mut T) Drop()`, run when the owner goes out of scope, on every exit path including panic unwinding. `defer` remains for cleanup not tied to a value.
