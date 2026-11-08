# Syntax conventions

Status: draft, 2026-10-17. These are the rules the programs in `../../examples/` assume. Each was tested by writing those programs; when an example reads badly, the rule changes here first. The guiding principle: **if Go already has a way to say it, Snello says it the same way.** The principle covers syntax and keywords, which are what make Snello readable at first sight. The spelling of the primitive number types is vocabulary, not grammar, and there Snello follows the convention of the newer systems languages (`i32`, `f64`); see the number types below and `05-standard-library.md`. That is the one deliberate departure, stated once so it does not become a slope. New syntax exists only where Go has no answer.

## Carried over from Go unchanged

`package`, `import`, `func`, `type ... struct`, `:=`, `var`, `for`, `if`, `defer`, channels and `select`, slices, maps, methods with named receivers, multiple return values, exported names by capitalization, zero values, mutable parameters and locals.

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
- **Borrow-holding structs and closures.** A struct with a `ref` or `mut` field, and a closure that captures by borrow, are themselves borrows and obey every borrow rule. A lexer holding `src ref string` is legal; storing that lexer in owned data is not, and returning it follows the rule for returned borrows (examples 05, 11, 18).
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
- **A newline after `>` ends the line**, as it does after an identifier, a `)` or a `?`. Go inserts no semicolon after `>`, but in Snello a line ends in `>` or `>>` whenever a generic type closes it: `cache Arc<RwLock<Entry>>`. The cost is that a comparison or a shift cannot break its line after the operator; keep the right operand on the same line.
- **`var` and `const`** declare one name list per keyword: `var x, y int = 1, 2`. Grouping with parentheses is not decided yet; nothing in the examples needs it.
- **`??` sits below every other operator and groups to the right.** `a ?? b == c` is `a ?? (b == c)`, and `a ?? b ?? c` is `a ?? (b ?? c)`. Its right side may be an expression, a block, or a `return`, `break` or `continue`.
- **A range over a string yields runes** (decided 2026-11-08, `05-standard-library.md`, decided 12). `for i, r := range s` gives each rune and the byte offset it starts at, and `for i := range s` the offsets alone; a byte that does not start a valid UTF-8 sequence yields U+FFFD and the loop moves on one byte. `len(s)` and `s[i]` stay in bytes.
- **Postfix `?` binds tighter than any prefix operator.** `-x?` is `-(x?)` and `ref x?` is `ref (x?)`.
- **`?` passes an error on where an assignment would take it, and into the one variant that holds its type** (decided 2026-11-07, `05-standard-library.md`, decided 11). In a function returning `Result<T, E>`, `x?` on a `Result<U, F>` returns the error as it is when `F` is `E`, as a trait value when `E` is a trait `F` implements, and wrapped in the variant when `E` is an enum with exactly one variant whose only field has type `F`; two such variants are refused as ambiguous, and none as today. The conversion is one step and belongs to `?` alone.
- **A `{` after a name opens a composite literal, except in a control clause.** In `if x == y {` the brace opens the body, as in Go. Write `if x == (T{}) {` to compare against a literal there. A type written out is never ambiguous, so `for _, k := range []int{5, 3} {` is fine.
- **There is no `switch`; `match` does its work** (decided 2026-11-08, `../specs/language-gaps.md`, task 11.1). On a value, `match x { ... }` takes literals, ranges, alternatives and guards, `1 | 2 =>`, `'0'..='9' =>`, `n if n > 0 =>`, and checks that every case is covered, which a `switch` does not. With no value, `match { cond => body, ..., _ => body }` tries each arm's condition in turn and runs the first that holds, as Go's `switch` with no value does, Kotlin's `when` with no subject and Lisp's `cond`: each condition a `bool`, the arm `_` required and last, since the compiler cannot tell whether conditions cover every case, an arm its condition alone, two joined with `&&`. It is parsed as a match on the unit, each condition the guard of a wildcard, so the checker and the backends see one shape. The word `switch` stays reserved, and the parser reports one with a message naming both forms, skipping it whole, wherever it stands, so the statements after it are parsed and report their own syntax errors. No program used one, and neither backend had ever compiled one.
- **A guard moves nothing** (decided 2026-11-08 with task 11.1, closing a hole). A guard, and so a condition of a match with no value, runs whether or not its arm is taken, the next arm tried when it fails; a value it moved would be gone for the arms after it, which the paths of the match did not see, so `match () { _ if eat(s) => {}, _ => keep(s) }` was accepted and dropped `s` twice. A move in a guard of a value declared before it is refused, as Rust refuses it; a guard reads, and lends with `ref`, and a name it declares itself, in a closure, a block or a match inside it, is its own to move.
- **`break` and `continue` mean the innermost loop.** `match` and `select` are not loops and never catch either, so a `break` in a `select` case inside a `for` ends the `for`, which is what example 12's deadline needs; there are no labels. Outside any loop both are errors. (Decided 2026-10-20: the checker had let a `switch` or `select` swallow a `break`, as another language does, and example 12's loop could never have ended.)
- **A struct exists only by declaration.** `struct` never appears in a type; `chan struct{}` and `struct{}{}` are refused. A value that carries nothing is the unit `()`, which the language already has as the result of a function returning nothing and the value in `Ok(())`: a channel that only signals is `chan ()`, and the signal is `done <- ()`. (Decided 2026-10-20: the examples had carried the `struct{}` idiom over from another language, and the compiler had half-followed them; see `01-language-design.md`, what Snello leaves out.)
- **A struct literal names its fields, `Vec3{x: 1, y: 2, z: 3}`, or gives every field in order, `Vec3{1, 2, 3}`, as Go allows** (decided 2026-10-19 with example 16; the checker rewrites the positional form to the named one). Mixing the two in one literal, or leaving a field out of the positional form, is refused.
- **A type may stand as an expression** where Go allows it: as the argument of `make`, as the function of a conversion such as `[]byte(s)`, and as the type of a composite literal.
- **An `if` binding is a short declaration followed directly by `{`.** `if x := opt {` binds; `if x := f(); x > 0 {` is Go's init-and-condition, told apart by the `;`. The left side of a binding is a name, `_`, a tuple of them, or a variant such as `Some(n)`.
- **`spawn` starts a task**, where Go writes `go`. It is the one keyword Snello renames: `go` is the name of another language, and `spawn` is the verb Rust, Erlang and Elixir use for the same thing. Not `async`, which in every language that has it marks a function for a later `await`; Snello has no `await` and no two kinds of function. `spawn` and `defer` take a call, as Go's `go` and `defer` do; any other expression is an error. A `for` may range with no variable: `for range ch {`.
- **A match arm is `pattern [if guard] => body`, ended by a comma or a newline.** The body is an expression, a block, or one statement: `Stop => return,`, `Balance(reply) => reply <- n,`, `"-i" => o.ignoreCase = true,`. Because a comma ends the arm, a `return` in an arm carries one expression, and several results are written as a tuple: `return (n, right)`. A bare name in a pattern is a binding or a constant variant such as `None`; the checker tells them apart. A parenthesized single pattern is that pattern.
- **One name on the left takes one expression on the right.** `x := f()`; only a list takes a list, `a, b := b, a`, as in Go.
- **An `if` expression has an `else`.** Every path yields a value, so `x := if c { a }` without an `else` is an error.
- **`impl` goes after `struct` or `enum`**: `struct Circle impl Shape { ... }`. Written before them it is reported and still recorded, so the rest of the declaration is checked.

## Heap allocation

`*T` is an owned pointer to a heap value, and `&expr` allocates: the Go spellings, with one owner. Used for recursive types: `next *Node?`, built with `&Node{...}`. It dereferences automatically for field access and method calls. There is no `nil`; an absent pointer is `*T?`.

Go's other uses of `*T`, sharing a value or mutating it through a pointer, are `ref` and `mut` in Snello. A `*T` cannot alias: it is moved like any owned value.

`Rc<T>` and `Weak<T>` are shared single-task ownership; `Arc<T>` is shared across tasks. `Arena<T>` with `Index<T>` is the idiom for linked structures (`02-arenas-and-indices.md`). These are standard library types.

## Types

Number types, with `int` and `uint` the platform word size and `int` the index type as in Go; `byte` is `u8` and `rune` is `i32`:

| Kind              | Types                             |
|-------------------|-----------------------------------|
| Signed integers   | `i8`, `i16`, `i32`, `i64`, `int`  |
| Unsigned integers | `u8`, `u16`, `u32`, `u64`, `uint` |
| Floating point    | `f32`, `f64`                      |

No `i128`, no complex numbers, no `uintptr`. Integer arithmetic wraps as in Go; checked arithmetic is a library function returning `T?`. A literal takes the type its context asks for.

- Generics with angle brackets: `List<T>`, `func Map<T, U>(...)`. Bounds with a colon: `T: Ord + Clone`.
- A method of a generic type names the type's parameters in its receiver, `func (l mut List<T>) Push(v T)`, and may rename them. Each receiver argument must be a name that is not already a type: `func (l ref List<int>) Sum()` is refused, where Go would silently declare a parameter called `int`. A method belongs to every instance. (Decided 2026-10-18.)
- A type argument cannot hold a borrow: `List<ref string>` is refused, since the instance would store one, and an inferred one obeys the same rule. (Decided 2026-10-18.) One exception, decided 2026-10-19 with `bufio`: a generic function that only borrows values of a type parameter, every parameter mentioning it a `ref` or `mut` and the result not mentioning it, takes a type argument that holds a borrow, since its instance keeps nothing of that type in its own frame; `fmt.Fprintln(mut w, ...)` and `io.Copy(mut w, mut r)` take a `bufio.Writer`, which borrows the file under it. A `mut` parameter mentioning the type parameter must be the only parameter that does, since through it the body could store a value taken from another parameter into the caller's data, `Into<T>(dst mut T, v ref T)`; the rule applies to a written type argument as to an inferred one.
- Type arguments are inferred, after `go/types`: first those written at the use, `Map<int>(...)`, which may be a prefix; then from the arguments, matching each parameter's type against its argument's, a literal or `None` going last so that it takes its type from the others; then from the type the context expects of the result. A borrow passed to a bare `T` binds what it borrows, never the borrow. A variant of a generic enum and a literal of a generic struct written without arguments, `Leaf(1)`, `Pair{a: 1, b: 2.0}`, are inferred the same way, the context first. (Decided 2026-10-18.)
- A method may have type parameters of its own, `func (l ref List<T>) Conv<U>(f func(ref T) U) List<U>`, on a generic type or a plain one; they come after the receiver's. Go forbids this because an interface value calls through a method table, which cannot hold a method that needs a copy per type argument; Snello compiles a copy per instance, so the only place a generic method cannot be called is through a trait used as a type (task 2.4), as in Rust. A generic function or method is not a value until its type arguments are known, so it must be called. (Decided 2026-10-18, on the owner's direction that Snello's generics follow the mature model, not Go's restrictions.)
- A generic type refers to itself, directly or through other generic types, only with arguments that do not grow: `struct S<T> { s *S<S<T>>? }` is refused, because each instance is a separate type once generics are compiled and the instances would never end. This is Go's instantiation-cycle check for generic functions, applied to types since Snello monomorphizes them, and to calls: a generic function that calls itself, or another that calls it back, with an argument that wraps its own parameter is refused for the same reason. (Decided 2026-10-18.)
- A type cannot contain itself by value, generic or not: only a pointer, slice or map breaks the chain, as in Go. Stage 1 assumed this and never checked it; a self-containing struct overflowed the compiler's stack. (Decided 2026-10-18.)
- Sum types: `enum Shape { Circle(r f64); Rect(w, h f64) }`.
- `match` with exhaustiveness checking. Patterns: literals including negative numbers `-1`, variants with bindings, tuples `(a, b)`, ranges `'0'..='9'`, alternatives `|`, guards `if cond`, wildcard `_`.
- `Option<T>` written `T?`, with `Some(x)` and `None`. `Result<T, E>` with `Ok(x)` and `Err(e)`. No `nil`.
- `x?` propagates `None` or `Err` from a function returning the same kind.
- `x ?? expr` unwraps or evaluates `expr`, which may `return`, `break`, or `continue`: `n := take(mut s.head) ?? return None`.
- `if x := opt { ... }` binds `x` when `opt` is `Some`; tuples destructure: `if (k, v) := line.SplitOnce("=") { ... }`.
- `.Expect("why")` unwraps or panics with the message.
- `if` and `match` are expressions when every branch yields a value: `pat := if ci { "(?i)" + p } else { p.Clone() }`, `return match e { ... }`.
- Receiving from a channel yields `T?`: `None` once the channel is closed and drained. `for x := range ch` yields `T` and ends on close.

## Traits

Full reasoning in `01-language-design.md`. Traits are Go interfaces that may also carry default methods and fields.

```snello
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

struct Circle impl Shape, Named {
    r f64
}

func (c ref Circle) Area() f64 { return math.Pi * c.r * c.r }
```

- A type declares what it implements with `impl` on its declaration: `struct Circle impl Shape, Named { ... }`. The same for enums, and for a type declared over another, after the type: `type Duration i64 impl fmt.Stringer` (decided 2026-10-21; Go's `Duration` satisfies `fmt.Stringer` by its method alone, and Snello lists what a type implements wherever it is declared). The compiler checks every required method exists. There are no impl blocks; methods are ordinary Go methods.
- **A trait's name is looked up as a trait.** In an `impl` list or a bound, a name resolves to the nearest trait of that name, past a type that shadows it, so that a package may declare a type named `Error` and list the standard trait `Error` on it, as `io` does. Elsewhere the name is the type. A value whose type implements `Error` prints as its `Error()` message, as the predeclared `error` prints as its text. (Decided 2026-10-19, with `io` written in Snello.)
- **A variant of another package's enum is named through the package, or bare in a pattern.** `io.NotExist` is a value and a constructor as `NotExist` is inside `io`; in a pattern, a bare name that is no name in scope but a variant of the scrutinee's enum is that variant, so a `match` on an `io.Kind` reads as it would in `io`, and a name that is neither binds. A type's name is not a value: `x := T` is `T (type) is not an expression`, as in Go. (Decided 2026-10-19, with the review of the library.)
- **A variadic parameter is `...ref any`, in the standard library alone.** `func Println(args ...ref any)` is how `fmt` declares what the compiler writes: `any` is a name only there, since a value of any type is otherwise a trait value, and a program's own variadic parameter is not supported yet. A library function declared without a body is provided by the runtime or by the compiler; `os.Exit` is one the compiler provides and knows never to return, as it knows of `panic`, since a signature cannot say so yet. Every package of the library is a `.snello` file under `std/`, these declarations included, so the checker holds no table of library names. (Decided 2026-10-19.)
- **`[]byte(s)` and `string(b)` copy.** A conversion between a string and its bytes reads its operand and builds a new owned value, as in Go; the operand stays with its owner. (Decided 2026-10-19, with `strings` written in Snello.)
- **Across packages, exported names only.** A program reaches the exported names of a package, `math.Pi`, `os.File`, and the exported fields and methods of its types; `io.Error{kind: ...}` is refused as `kind of io.Error is not exported`. A library package may declare a function without a body and a variable without a value: both are the runtime's, under the C name the compiler gives them. A top-level `var` or `const` of any package takes a literal value, a number, character, boolean or string or a struct of such, since nothing runs before `main`. (Decided 2026-10-19.)
- Trait fields are stored inline in the implementing struct and set at construction: `Circle{r: 1, name: "circle"}`. A trait field has no default value; `var name string = "x"` inside a trait is an error. A trait field follows the ownership rules of a struct field: `func (n ref Self) Name() ref string { return ref n.name }` borrows it, and `return n.name` is refused as a move out of a borrow.
- Overriding a default is defining the method. `super.Method()` inside an override calls the next definition in the impl order.
- Resolution order: the type's own methods, then its impl list from right to left, each trait followed by the traits it refines; a trait reached more than once counts where it was reached last, so a trait always precedes the traits it refines (decided 2026-10-19, with the first slice of defaults). Two traits providing a default for the same method, neither refining the other, are an ambiguity the type settles by declaring the method. Resolved at compile time; every call is direct.
- A trait refining another: `trait Logged: Shape { ... }`.
- Marker and operator traits use the same mechanism: `struct Vec3 impl Copy, Eq, Add { ... }` with a method `func (a Vec3) Add(b Vec3) Vec3`.
- The standard traits' methods, all on `ref Self` but `Drop`'s: `Clone() Self`, `Eq(o ref Self) bool`, `Cmp(o ref Self) Ordering`, `Hash() u64`, `Error() string`, `Drop()` on `mut Self`; `Ord` and `Hash` refine `Eq`. `Ordering` is a predeclared enum, `Less`, `Equal`, `Greater`, and every number and string has `Cmp`. A declared type listing `Eq`, `Ord`, `Hash` or `Clone` without writing the method has it derived from its fields, each of which must implement the trait, and the first that does not is reported with its type; `Copy` is the same check with no method, and a `Drop` type is never `Copy`. The predeclared types and their composites implement the markers by structure, and a type declared over one, `type ID int`, is what it is, and what it lists after `impl`; an enum of comparable payloads is `Eq` and `Hash` by structure, a struct only by listing them, since `==` on a struct has always gone through a method. `==` and a map's hashing go through the `Eq` and `Hash` a type wrote. A map key must be `Eq` and `Hash`, and a trait value is never one. (Decided 2026-10-19, with the first slice of the standard traits.)
- The operator traits: `Add`, `Sub`, `Mul`, `Div`, `Rem` each require `func (a Self) Op(o Self) Self` and give `+ - * / %`; `Neg` requires `func (a Self) Neg() Self` and gives the unary `-`. A binary operator trait takes one type argument, the type of the right operand, `Mul<f64>`, and `Mul` alone is `Mul<Self>`: `Mul` listed on `V` satisfies a bound `Mul<V>` and `Mul<V>` listed satisfies `Mul`, while `Mul<f64>` listed does not satisfy `Mul`. The argument may be a type parameter of the declaration or of the bound, which are in scope for the impl list and the bounds. A type lists one `Mul`. `a op= b` is `a = a op b` for every type `op` works on. `a + b` is `a.Add(b)`: both operands by value, so a non-Copy operand moves, and a borrow of a non-Copy value is refused as it is for any value receiver. Numbers implement every operator trait with their own type on the right, strings implement `Add`. `==` is `Eq`, not an operator trait; `<` has no trait, orderings go through `Cmp`. The result type is `Self`; an operator producing another type waits for a use. (Decided 2026-10-19, with the second slice of the standard traits.)
- A method taking its receiver by value cannot be called on a borrow of a value that is not `Copy`: `cannot move a out of a borrow`. A borrow of a `Copy` value is read. (A stage 1 hole, closed 2026-10-19: the call was accepted and the value dropped twice.)
- Assigning to a moved variable gives it back. After a branch set or select it is owned only when every path assigned it, the whole counting for a part; after a loop with a condition nothing is given back, since the body may not have run, while a loop without one gives back what every break path did; a closure gives nothing back to the function around it. (Decided 2026-10-19, closing a stage 1 crash: the move pass deleted the record of a move made before the branch, under the index the branch had saved.)
- A type that writes one of `Eq` and `Hash` must write the other: equal values must hash alike, and the compiler can promise that only for the pair it derives. (Decided 2026-10-19.)
- Numbers, strings and `bool` have the methods of their standard traits by name, `Eq`, `Cmp`, `Hash` and `Clone`, with the receiver's own type for `Self`, so a `type ID int` compares with an `ID`.
- Bounds: `func Total<T: Shape>(xs ref []T)`. Monomorphized per `T`.
- **A trait used as a type is a Go interface value.** `[]Shape` holds any mix of implementing types; each element owns its value. One allocation and one indirect call per value, the same cost as Go. Such a value is `Send` only if the trait requires it: `trait Shape: Send { ... }`. A trait value satisfies its own trait as a bound only when no method or field of the bound's traits is written with `Self`, since the body may reach them through the parameter and a trait value cannot (decided 2026-10-19).
- Not in version one: self-types, implicits.

## Concurrency

Full reasoning in `03-ownership-across-tasks.md`.

- `spawn f(x)` moves `x` into the new task. A closure passed to `spawn` moves everything it captures.
- Sending moves the value; receiving owns it. A channel handle is owned: `ch.Clone()` gives another for another task, and the channel operations borrow it.
- `Send` is inferred from structure and never written.
- `Mutex<T>` and `RwLock<T>` own their data; `Lock()` returns a guard that is a `mut T` and unlocks on drop.
- `sync` types synchronize internally and are used through `ref`.

## Closures

A closure captures each variable in the weakest mode its body needs: by copy if the variable is `Copy`, holds no borrow and is only read, in which case the closure holds the value as it was when the closure was made (decided 2026-10-19 with example 18: `adder(10)` returns `func(x int) int { return x + offset }`); `ref` if it only reads a value that is not `Copy`; `mut` if it assigns to it; by move if it consumes it or the closure is passed to `spawn`. A closure that captures by borrow is a borrow (see above). A closure that captures only copies may be stored and returned, and is a value with nothing to drop: the compiler keeps the copies in the function value itself, a few words beside the function pointer.

## Destructors

A type that implements `Drop` has `func (t mut T) Drop()`, run when the owner goes out of scope, on every exit path including panic unwinding. `defer` remains for cleanup not tied to a value.
