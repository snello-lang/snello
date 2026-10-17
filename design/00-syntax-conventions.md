# Syntax conventions

Status: draft, 2026-10-17. These are the rules the programs in `../examples/` assume. Each was tested by writing those programs; when an example reads badly, the rule changes here first. The guiding principle: **if Go already has a way to say it, Teak says it the same way.** New syntax exists only where Go has no answer.

## Carried over from Go unchanged

`package`, `import`, `func`, `type ... struct`, `:=`, `var`, `for`, `if`, `switch`, `defer`, `go`, channels and `select`, slices, maps, methods with named receivers, multiple return values, exported names by capitalization, zero values, mutable parameters and locals.

`for` ranges exactly as in Go: `for i := range xs` yields the index, `for i, x := range xs` the index and element, `for k, v := range m` key and value, `for x := range ch` the received value. An iterator ranges like a channel: `for x := range it` yields each item.

## Ownership and borrowing

Full reasoning in `04-borrows-ref-and-mut.md`. The rules:

- Every value has one owner. Passing or assigning by value **moves** it, unless its type is `Copy`.
- `ref T` is a shared read-only borrow; `mut T` is an exclusive mutable borrow. Both are written at the declaration and at the use site: `func Len(xs ref []T)`, called as `Len(ref xs)`.
- Receivers: `func (t ref Tree) Len()`, `func (t mut Tree) Push()`, `func (f File) Close()` (consumes).
- A borrow lives from its creation to its last use within one function. It cannot be stored in an owned struct, captured by `go`, or sent on a channel.
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

## Heap allocation

`*T` is an owned pointer to a heap value, and `&expr` allocates: the Go spellings, with one owner. Used for recursive types: `next *Node?`, built with `&Node{...}`. It dereferences automatically for field access and method calls. There is no `nil`; an absent pointer is `*T?`.

Go's other uses of `*T`, sharing a value or mutating it through a pointer, are `ref` and `mut` in Teak. A `*T` cannot alias: it is moved like any owned value.

`Rc<T>` and `Weak<T>` are shared single-task ownership; `Arc<T>` is shared across tasks. `Arena<T>` with `Index<T>` is the idiom for linked structures (`02-arenas-and-indices.md`). These are standard library types.

## Types

- Generics with angle brackets: `List<T>`, `func Map<T, U>(...)`. Bounds with a colon: `T: Ord + Clone`.
- Sum types: `type Shape enum { Circle(r float64); Rect(w, h float64) }`.
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
    func (s ref Self) Area() float64                  // required
    func (s ref Self) Describe() string {             // default
        return fmt.Sprintf("area %.2f", s.Area())
    }
}

trait Named {
    var name string                                   // a field every implementor gets
    func (n ref Self) Name() string { return n.name }
}

type Circle struct impl Shape, Named {
    r float64
}

func (c ref Circle) Area() float64 { return math.Pi * c.r * c.r }
```

- A type declares what it implements with `impl` on its declaration: `type Circle struct impl Shape, Named { ... }`. The same for enums. The compiler checks every required method exists. There are no impl blocks; methods are ordinary Go methods.
- Trait fields are stored inline in the implementing struct and set at construction: `Circle{r: 1, name: "circle"}`.
- Overriding a default is defining the method. `super.Method()` inside an override calls the next definition in the impl order.
- Resolution order: the type's own methods, then its impl list from right to left, each trait followed by the traits it refines. Resolved at compile time; every call is direct.
- A trait refining another: `trait Logged: Shape { ... }`.
- Marker and operator traits use the same mechanism: `type Vec3 struct impl Copy, Eq, Add { ... }` with a method `func (a Vec3) Add(b Vec3) Vec3`.
- Bounds: `func Total<T: Shape>(xs ref []T)`. Monomorphized per `T`.
- **A trait used as a type is a Go interface value.** `[]Shape` holds any mix of implementing types; each element owns its value. One allocation and one indirect call per value, the same cost as Go. Such a value is `Send` only if the trait requires it: `trait Shape: Send { ... }`.
- Not in version one: self-types, implicits.

## Concurrency

Full reasoning in `03-ownership-across-tasks.md`.

- `go f(x)` moves `x` into the new task. A closure passed to `go` moves everything it captures.
- Sending moves the value; receiving owns it. Channel handles are `Copy`.
- `Send` is inferred from structure and never written.
- `Mutex<T>` and `RwLock<T>` own their data; `Lock()` returns a guard that is a `mut T` and unlocks on drop.
- `sync` types synchronize internally and are used through `ref`.

## Closures

A closure captures each variable in the weakest mode its body needs: `ref` if it only reads it, `mut` if it assigns to it, by move if it consumes it or the closure is passed to `go`. A closure that captures by borrow is a borrow (see above). A closure that captures only owned or `Copy` values may be stored and returned.

## Destructors

A type that implements `Drop` has `func (t mut T) Drop()`, run when the owner goes out of scope, on every exit path including panic unwinding. `defer` remains for cleanup not tied to a value.
