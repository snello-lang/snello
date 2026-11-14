# Syntax conventions

Status: decided. These are the rules the programs in `../../examples/` assume. Each was tested by writing those programs; when an example reads badly, the rule changes here first. The guiding principle: **if Go already has a way to say it, Snello says it the same way.** The principle covers syntax and keywords, which are what make Snello readable at first sight. The spelling of the primitive number types is vocabulary, not grammar, and there Snello follows the convention of the newer systems languages (`i32`, `f64`); see the number types below and `05-standard-library.md`. That is the one deliberate departure, stated once so it does not become a slope. New syntax exists only where Go has no answer.

## Carried over from Go unchanged

`package`, `import`, `func`, `type ... struct`, `:=`, `var`, `for`, `if`, `defer`, channels and `select`, slices, maps, methods with named receivers, multiple return values, exported names by capitalization, zero values for the types that have one (below), mutable parameters and locals.

`for` ranges exactly as in Go: `for i := range xs` yields the index, `for i, x := range xs` the index and element, `for k, v := range m` key and value, `for x := range ch` the received value. An iterator ranges like a channel: `for x := range it` yields each item, and `for i, v := range it` the two parts of an item that is a pair.

## Ownership and borrowing

Full reasoning in `04-borrows-ref-and-mut.md`. The rules:

- Every value has one owner. Passing or assigning by value **moves** it, unless its type is `Copy`.
- `ref T` is a shared read-only borrow; `mut T` is an exclusive mutable borrow. Both are written at the declaration and at the use site: `func Len(xs ref []T)`, called as `Len(ref xs)`.
- Receivers: `func (t ref Tree) Len()`, `func (t mut Tree) Push()`, `func (f File) Close()` (consumes).
- A borrow lives from its creation to its last use within one function. It cannot be stored in an owned struct, captured by `spawn`, or sent on a channel.
- A borrow may be returned only when tied to one input: the receiver (`func (s ref Stack<T>) Peek() ref T?`) or the sole borrowed parameter (`func Trim(s ref string) ref string`). Two borrowed inputs and a borrowed output is a compile error; clone or return an index.
- Any number of `ref` or exactly one `mut` at a time.
- A borrow variable may be reassigned to a borrow derived from itself: `cur = mut n.left` where `n` was reached through `cur`. This is what makes walking a chain of boxes possible (the bst example).
- **Borrow-holding structs and closures.** A struct with a `ref` or `mut` field, and a closure that captures by borrow, are themselves borrows and obey every borrow rule. A lexer holding `src ref string` is legal; storing that lexer in owned data is not, and returning it follows the rule for returned borrows (examples 05, 11, 18).
- Field access through a borrow needs no `*`: `n.value`. Assigning a whole new value through a `mut` borrow does: `*v = *v * k`.
- `take(mut place)` moves the value out of a field or element and leaves the type's zero value (`None` for an option) behind, for a type that has one; `replace(mut place, v)` moves the value out and leaves `v`, for every type (zero values, below).
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
- **An array length is a constant expression of an integer type**, at least zero: `[4]int`, `[N]byte`, `[2 * N + 1]int`, as a constant's value is one below.
- **A newline after `>` ends the line**, as it does after an identifier, a `)` or a `?`. Go inserts no semicolon after `>`, but in Snello a line ends in `>` or `>>` whenever a generic type closes it: `cache Arc<RwLock<Entry>>`. The cost is that a comparison or a shift cannot break its line after the operator; keep the right operand on the same line.
- **`var` and `const`** declare one name list per keyword: `var x, y int = 1, 2`. Grouping with parentheses is not decided yet; nothing in the examples needs it.
- **`??` sits below every other operator and groups to the right.** `a ?? b == c` is `a ?? (b == c)`, and `a ?? b ?? c` is `a ?? (b ?? c)`. Its right side may be an expression, a block, or a `return`, `break` or `continue`.
- **An iterator is any value whose type has `Next` on a `mut` receiver, taking nothing and returning `T?`.** A range loop over one calls `Next` until it gives `None`, as Rust's `for` calls `next` and as Go 1.23 ranges over a function by its shape: no trait names an iterator, so the `Iterator` trait a program declares for its adapters is its own, and a standard collection gives its iterator from a method, `for i, v := range a.Iter()`, `for v := range s.Iter()`, as Go's `maps.All` and Rust's `iter` do. One variable takes the item; two take the parts of an item that is a pair, `(Index<T>, ref T)`. A `ref` of an iterator cannot be ranged over, since `Next` changes it; a variable is lent `mut` for the loop, as `range mut it` lends it, or consumed when it owns its items and holds no borrow, as a slice is; what the iterator borrows is lent for the loop; an iterator made for the loop, a call's result, is dropped after it, and an item the loop binds nothing to is dropped at once.
- **A range over a string yields runes** (`05-standard-library.md`, decided 12). `for i, r := range s` gives each rune and the byte offset it starts at, and `for i := range s` the offsets alone; a byte that does not start a valid UTF-8 sequence yields U+FFFD and the loop moves on one byte. `len(s)` and `s[i]` stay in bytes.
- **Postfix `?` binds tighter than any prefix operator.** `-x?` is `-(x?)` and `ref x?` is `ref (x?)`.
- **`?` passes an error on where an assignment would take it, and into the one variant that holds its type** (`05-standard-library.md`, decided 11). In a function returning `Result<T, E>`, `x?` on a `Result<U, F>` returns the error as it is when `F` is `E`, as a trait value when `E` is a trait `F` implements, and wrapped in the variant when `E` is an enum with exactly one variant whose only field has type `F`; two such variants are refused as ambiguous, and none as today. The conversion is one step and belongs to `?` alone.
- **A `{` after a name opens a composite literal, except in a control clause.** In `if x == y {` the brace opens the body, as in Go. Write `if x == (T{}) {` to compare against a literal there. A type written out is never ambiguous, so `for _, k := range []int{5, 3} {` is fine.
- **There is no `switch`; `match` does its work.** On a value, `match x { ... }` takes literals, ranges, alternatives and guards, `1 | 2 =>`, `'0'..='9' =>`, `n if n > 0 =>`, and checks that every case is covered, which a `switch` does not. With no value, `match { cond => body, ..., _ => body }` tries each arm's condition in turn and runs the first that holds, as Go's `switch` with no value does, Kotlin's `when` with no subject and Lisp's `cond`: each condition a `bool`, the arm `_` required and last, since the compiler cannot tell whether conditions cover every case, an arm its condition alone, two joined with `&&`. It is parsed as a match on the unit, each condition the guard of a wildcard, so the checker and the backends see one shape. The word `switch` stays reserved, and the parser reports one with a message naming both forms, skipping it whole, wherever it stands, so the statements after it are parsed and report their own syntax errors. No program used one, and neither backend had ever compiled one.
- **A guard moves nothing.** A guard, and so a condition of a match with no value, runs whether or not its arm is taken, the next arm tried when it fails; a value it moved would be gone for the arms after it, which the paths of the match did not see, so `match () { _ if eat(s) => {}, _ => keep(s) }` was accepted and dropped `s` twice. A move in a guard of a value declared before it is refused, as Rust refuses it; a guard reads, and lends with `ref`, and a name it declares itself, in a closure, a block or a match inside it, is its own to move.
- **`break` and `continue` mean the innermost loop.** `match` and `select` are not loops and never catch either, so a `break` in a `select` case inside a `for` ends the `for`, which is what the worker pool's deadline needs; there are no labels. Outside any loop both are errors. (The checker had let a `switch` or `select` swallow a `break`, as another language does, and the worker pool's loop could never have ended.)
- **A struct exists only by declaration.** `struct` never appears in a type; `chan struct{}` and `struct{}{}` are refused. A value that carries nothing is the unit `()`, which the language already has as the result of a function returning nothing and the value in `Ok(())`: a channel that only signals is `chan ()`, and the signal is `done <- ()`. (The examples had carried the `struct{}` idiom over from another language, and the compiler had half-followed them; see `01-language-design.md`, what Snello leaves out.)
- **A struct literal names its fields, `Vec3{x: 1, y: 2, z: 3}`, or gives every field in order, `Vec3{1, 2, 3}`, as Go allows**; the checker rewrites the positional form to the named one. Mixing the two in one literal, or leaving a field out of the positional form, is refused.
- **A type may stand as an expression** where Go allows it: as the argument of `make`, as the function of a conversion such as `[]byte(s)`, and as the type of a composite literal.
- **An `if` binding is a short declaration followed directly by `{`.** `if x := opt {` binds; `if x := f(); x > 0 {` is Go's init-and-condition, told apart by the `;`. The left side of a binding is a name, `_`, a tuple of them, or a variant such as `Some(n)`.
- **`spawn` starts a task**, where Go writes `go`. It is the one keyword Snello renames: `go` is the name of another language, and `spawn` is the verb Rust, Erlang and Elixir use for the same thing. Not `async`, which in every language that has it marks a function for a later `await`; Snello has no `await` and no two kinds of function. `spawn` and `defer` take a call, as Go's `go` and `defer` do; any other expression is an error. A `for` may range with no variable: `for range ch {`.
- **A match arm is `pattern [if guard] => body`, ended by a comma or a newline.** The body is an expression, a block, or one statement: `Stop => return,`, `Balance(reply) => reply <- n,`, `"-i" => o.ignoreCase = true,`. Because a comma ends the arm, a `return` in an arm carries one expression, and several results are written as a tuple: `return (n, right)`. A bare name in a pattern is a binding or a constant variant such as `None`; the checker tells them apart. A parenthesized single pattern is that pattern.
- **One name on the left takes one expression on the right.** `x := f()`; only a list takes a list, `a, b := b, a`, as in Go.
- **An `if` expression has an `else`.** Every path yields a value, so `x := if c { a }` without an `else` is an error.
- **`impl` goes after `struct` or `enum`**: `struct Circle impl Shape { ... }`. Written before them it is reported and still recorded, so the rest of the declaration is checked.

## Constants

A `const` names a value the compiler computes, at the top level of a file or inside a function: `const KB = 1 << 10`, `const Limit u8 = 3 * 80`, `const Comma byte = ','`, `const Greeting = "hello, " + Name`. It gives a fixed value one name and one place, checks it when the program is compiled, so an overflow, a division by zero or a lost fraction is an error there and not a wrong number at run time, as Go checks it and C's `#define` does not (a `uint8_t` given 300 holds 44 with a warning), and costs nothing when the program runs.

- **A constant expression** is made of literals, the names of constants, of this package or another's, `math.MaxInt8`, the operators over numbers, booleans and strings, `+ - * / %`, `& | ^ &^ << >>`, the comparisons, `&& || !` and the unary `-` and `^`, the conversions between number types, `f64(KB)`, `u8(Limit)`, and those that give a string or a bool another name of its kind, `Name("host")` for `type Name string`, as Go's constant conversions do, though a rune does not become a string, `string('A')` is refused, and parentheses. Anything else is not constant, and a `const` that takes it is refused naming the part: `f() is not constant`, `n is not constant`. A call runs at run time and a variable can change, and neither is computed by the compiler. There is no `iota`, since an `enum` names its cases, and no function runs at compile time, which Rust's `const fn` and C++'s `constexpr` do and which a compiler written in Go would have to interpret.
- **Typed as every expression is.** A literal takes its type from its context, as everywhere in Snello: the constant's declared type, or `int`, `f64`, `rune`, `string` or `bool` by itself. There is no untyped constant arithmetic (01-language-design.md, what Snello leaves out), so `const MB i64 = KB * KB` is refused when `KB` is an `int`, as the same assignment is anywhere, and is written `i64(KB) * i64(KB)`.
- **Folded exactly, in the type's arithmetic.** The checker computes the value as `go/types` does, an integer exactly with `math/big` and held to the type at each operation, a float with the machine's own arithmetic. An integer the type cannot hold is refused, `constant 1099511627776 overflows i32`, where the same arithmetic at run time wraps: a constant is a number written down, as a literal is, and a literal past its type is refused already. A division or a remainder by zero is refused, and so is a negative shift count. A float operation is the processor's, rounded to the type, `f64` or `f32`, as at run time, so that a constant and the same expression on variables agree to the bit, a negative zero included, which Go's exact constants would make a zero; a result too large for the type is refused as an overflow, so no constant is infinite or not a number. A conversion keeps the value: an integer that does not fit the target is refused, a float converted to an integer type must have no fraction, `constant 2.5 truncated to int`, and an integer converted to a float rounds to the nearest. A comparison gives a `bool`.
- **Outside a `const`, arithmetic is run-time arithmetic.** An expression written elsewhere, `x := KB * KB`, is computed when it runs and wraps at its type's width (07-lowering.md, the integers), even when every operand is a constant; only a constant's value and an array length are folded. Go folds every constant expression wherever it is written, through its untyped constants, which Snello leaves out.
- **A number, a boolean, a string, or a struct of them.** A constant's type is a number type, `bool` or `string`, or a type declared over one, `type Celsius f64`; a struct literal whose fields are constant expressions stays a constant, as it was before expressions, and each field is folded. A struct constant is a value only as a whole: it is no part of an expression, `const Again = Origin` is refused, and its fields are read where it is used, `Origin.x`.
- **In any order.** The top-level constants of a package may name each other in any order and from any file of it, as in Go: the checker computes a constant the first time another, or an array length, needs it, and resolves a type declared over another, `type Count int`, the first time a constant's type or conversion needs it, so `const N Count = 4` may come before `type Count int`. A blank constant, `const _ = KB * 4`, is checked as any other and written nowhere. A constant that needs itself, through others or not, is refused, `initialization cycle: A refers to itself`, with the chain of names. A local constant is computed where it is declared and sees only the constants before it, as a local variable does.
- **An array length is a constant** of any integer type, at least zero: `[N]byte`, `[2 * N]int`.
- **What the backends see.** Each constant is written from its folded value, as a constant global in the IR and a `static const` of the file in C, one a function declares included, so the two backends agree on it by construction and neither computes anything. A top-level `var` is not a constant and keeps its rule: a literal is written as the initial value, and anything else is made the first time the variable is used. A top-level `var` whose type is inferred from its value is checked in the order of the files, so one that names another declared after it, whose type is not known yet, is refused, `y is used before its type is known: declare y before this, or with its type`, where before it passed the checker with no type and stopped the compiler.
- **Memory safe, no collector.** A constant is computed by the compiler and written into the program; nothing is allocated, owned or freed for it.

## Variadic functions

The last parameter of a function, a method or a closure may take any number of arguments, as Go's does, in one of two forms that say what happens to the arguments, as every parameter says it. Each argument is checked against its type, where C's variadic functions check none (`printf("%s", 42)` compiles and crashes); a slice passed on with `...` is moved or lent, never shared as Go shares the caller's array, which a function writing to it changes behind the caller's back; and a lent argument cannot outlive the call.

```go
func Sum(xs ...int) int
func Join(sep string, parts ...ref string) string

Sum(1, 2, 3)
Sum(nums...)
Join(", ", ref first, last)
Join(", ", words...)
```

- **`...T` owns its arguments.** The function receives an owned `[]T`, a slice made for the call with the arguments moved into it, which it drops, or keeps, as any owned parameter. An argument that is not `Copy` is moved, as one passed by value is, and a use of it after the call is `use of moved value`. Each argument is an element of the function's slice, so a closure that borrows is none, as it is no element of a slice literal: `closure captures w by borrow and cannot be stored`, whether or not the function keeps the slice, since the checker does not follow closures inside a slice; such a closure goes to a `func()` parameter. It is Go's `...T` with ownership: Go's slice shares the caller's array, and here the slice is the function's own.
- **`...ref T` borrows them.** The function receives a `ref []T` of the arguments, each lent for the call alone, as a `ref` parameter is: an owned argument is lent without `ref`, as to any `ref` parameter, and a temporary, `ref made()`, is dropped when the call's statement ends. The caller lays the arguments' values side by side in an array of its own frame, which nothing drops, since each value is the argument's own, and passes a view of it: no allocation, and each value is read through the view while its owner is frozen for the call. Since the array lives for the call alone, the function returns no borrow of the arguments, from its body or from a closure inside it, `cannot return a borrow of xs: the arguments of a variadic ...ref parameter are lent for the call alone`; the parameter is never a returned borrow's source, so a function returning one borrow may take a `...ref` parameter beside it, `func pick(d ref string, xs ...ref string) ref string`.
- **No `...mut T`.** A slice cannot hold several `mut` borrows apart, and the values a view holds are copies of the arguments' bits; a function that changes several values takes a `mut []T`.
- **`f(xs...)` passes a slice.** After the last argument, `...` passes the slice itself as the variadic parameter, the only argument after the fixed ones, as in Go: a `[]T`, moved, for `...T`, so the function owns the caller's slice and nothing is copied, and a `[]T` lent, or a `ref []T`, for `...ref T`, so one variadic function passes its arguments on to another, `Join(sep, parts...)`, for free.
- **Typed as the declaration says.** A function's type writes the form, `func(...int) int`, `func(string, ...ref string)`, and is a type of its own: `Sum` is not a `func([]int) int`. A generic element is inferred from every argument, `Count(1, 2)` makes `T` an `int`, and a call with no argument to infer it from writes it, `Count<int>()`. A trait's method is variadic as the trait declares it. An enum variant's field is not variadic: it holds one value.
- **What the backends see.** Both write a call of a variadic function of the program's with the arguments evaluated left to right with the others, each to its element's type, then made the one value the parameter takes, from an array in the caller's frame, with no heap allocation: for `...T` a slice of capacity `SN_CALLER`, whose elements the function owns and drops and whose buffer nothing frees, for `...ref T` a slice over the arguments' values. A `...T` function that only reads its slice, ranges over it, changes its elements or lends it uses the caller's array as it is, so `Sum(1, 2, 3)` allocates nothing, as Go's escape analysis keeps a slice that does not escape on the stack; the first change of its length, `Push`, moves it to a buffer of its own, and a function whose body moves the slice whole, other than consuming it in a range loop, or captures it in a closure, gives it one when it starts (`sn_slice_unframe`), since it may keep it past the call. A spawned call gives it one too, since the task outlives the frame. A spread call passes its slice as an argument to a parameter of the slice's type. The function itself is an ordinary function whose last parameter is a slice or a `ref` of one, so a function value, a method and a trait's method need nothing more. A spawned call builds the slice at the spawn, as it evaluates every argument there; a deferred call saves each argument when the `defer` runs and builds the slice when the call does, holding what a `...ref` argument lends, or a slice passed on to one, until the function returns, as it holds any borrowed argument, and a call of the library's functions or a built-in, a variant or a conversion takes no `...`.
- **Memory safe, no collector.** `...T` moves each argument into a slice the function owns and drops, as a slice literal passed by value would; `...ref T` lends each argument for the call as a `ref` parameter does, freezing its owner, and nothing of it outlives the call, since no borrow of it is returned and none can be stored. Nothing is collected.

## Default field values

A struct field may declare a default, a constant expression, after its type: `struct OpenOptions { Read bool = true; Write bool; Perm FileMode = 0o666 }`. A literal that leaves the field out takes its default, so a setting left out is a value the type chose, which Go's zero cannot say: `OpenOptions{Write: true}` reads and writes, with the permissions `0o666`. It is what the language has in place of parameter defaults and overloading (01-language-design.md, what Snello leaves out): a function takes its settings as such a struct, `os.OpenOptions{Append: true, Create: true}.Open("log.txt")`, every setting named, in any order, any of them skipped, with no new call syntax and nothing lost when the function is a value. Swift and Zig have default field values, and Rust accepted them for the same reason, builder types weighing too much.

- **A constant each.** The default is a constant expression of the field's type, as a constant's value is (Constants above): `port int = 8080`, `name string = "anon"`, `limit u8 = 3 * 80`, `origin Point = Point{x: 0, y: 0}`. A call, a variable or a value that is not constant is refused, `f() is not constant`, and so is a default of another type, `cannot use a value of type string as int in field default`. Fields declared together take one default each, `w, h f64 = 1.0, 2.0`. A field whose type is a type parameter, or holds one, takes none, since no constant has every type; an embedded field takes none either, and neither does a trait's field, `a trait field takes no default`, since the type that implements the trait gives it its value.
- **Left out, the default.** A keyed literal that leaves a field out gives it its default, then its zero value, then is refused when its type has none, the rule of the zero values below. A positional literal may stop before the last fields when every field it leaves out declares a default, `Size{3}` for `struct Size { w int; h int = 1 }`; any other positional literal still gives every field. A literal of another package's struct leaves out a field that is not exported when that field declares a default, or when the struct would have a zero value but for its defaults, since the package chose those values. A keyed literal names each field once, `duplicate field w in a Size literal`.
- **No zero value.** A struct with a field that declares a default has no zero value, nor has any type that holds one: `var o OpenOptions` would fill the fields with zeros, past the defaults the type declared, and so would `make([]OpenOptions, n)` and a map read of a missing key. Each is refused as for any type without a zero value (Zero values below), and `OpenOptions{}` is the value with every default; the declaration says so when that literal builds the value, `cannot declare o without a value: OpenOptions declares defaults, which a zero value would skip; write o := OpenOptions{}`. The rule only refuses; nothing the checker accepted before changes.
- **What the backends see.** A literal is written with every field it leaves out that declares a default set to the default's folded value, as a constant is written (Constants above), in a function and in a package variable's initial value alike. Nothing runs to make a default.
- **Memory safe, no collector.** A default is a constant, a string one a literal that owns nothing; every field is set when the value is made, and nothing is allocated for it.

## Zero values

No value in Snello is ever null or empty. A variable declared without a value gets its type's zero value, as in Go, but only when that zero is a real value of the type.

**Types with a zero value**

| Type | Zero value |
|---|---|
| a number | `0` |
| `bool` | `false` |
| `string` | `""` |
| `T?` | `None`, whatever `T` is |
| `[]T`, `map[K]V` | empty, whatever the element |
| a struct, tuple or array | each part's zero value |
| an enum | its first variant, with each field's zero value |
| `Result<T, E>` | `Err` of `E`'s zero value |

**Types without one**

| Type | Why |
|---|---|
| `*T`, `chan T`, a function, a trait value | the zero would be a null that every use follows |
| a type that implements `Drop` | it owns something, a cell, a lock, a file; its zero would own nothing |
| a type parameter | the type argument may be one of the above |
| a struct, tuple, array, enum or `Result` holding one of these | that part has no zero value |

A value that may be missing is written `T?`: `var next *Node?` is `None`.

**What the checker refuses**

For a type without a zero value, every way of making one is an error:

| Refused | Write instead |
|---|---|
| `var m sync.Mutex<int>` | `m := sync.NewMutex(0)`; `m := if c { ... } else { ... }` when it depends on a condition; or `var m sync.Mutex<int>?` |
| `Box{}`, where `Box` has a field `p *int` | `Box{p: &n}`: name the field |
| `[3]*Node{a}`, too short | `[3]*Node{a, b, c}` |
| `make([]*Node, n)` | `make([]*Node, 0, n)`, then `Push`; `make([]*Node, 0)` is allowed |
| `x := m[k]`, where the map's values are `*Node` | `m.Get(k)`, which gives a `ref *Node?`; writing `m[k] = v` is allowed |
| `m[k].n = 5`, where the map's values are a struct with a `*int` field | `m.GetMut(k)`; a write of part of an entry would make the rest from the zero value |
| `sync.Counter(c)`, where `c`'s type is declared over `sync.Counter` | `sync.NewCounter(n)`: a type without a zero value converts to and from no other type, and a type declared over another package's struct opens none of its fields |
| `take(mut b.p)`, where `b.p` is a `*int` | `replace(mut b.p, &n)` |

A declaration is not given a value later: `var m T` followed by an assignment on every path is refused too, and an `if` or `match` expression says the same thing in one statement.

**`replace`**

`replace(place mut T, v T) T` moves the value out of a place, a variable or a field, element or `*p` of one, written `mut`, and leaves `v` there; a call's result, a constant or a byte of a string is no place, and `replace` and `take` refuse it. It works for every type, where `take` works only for a type with a zero value, and the place is never empty. It is Rust's `mem::replace`.

**In the library**

- `sync`'s `Arc`, `Mutex`, `RwLock`, `Once`, `Counter` and `WaitGroup`, and `rc`'s `Rc` and `Weak`, implement `Drop`, so a program gets one only from its `New` function. Their "empty handle" panics are gone, and a `Weak` that may point at nothing is a `Weak<T>?`.
- `strings.Repeat` returns `string?`: `None` for a negative count, or a string too long for an `int`.
- `WaitGroup.Add` and `Done` return `bool`: `false`, with the count unchanged, when the count would go below zero.
- `arena.Remove` takes its value without `Expect`.
- `testing.Parallel` returns `bool`: `false`, changing nothing, when the test runs in parallel already or the call is on a task that is no test's own.

With these, the library panics on no mistake a caller can make, and has no branch that only a broken invariant could reach: `rc`'s and `sync`'s handles hold their cells by address, with no empty state, and a cell's value is where its count says it is, so `Get`, `Lock`, `Read` and `Write` have no case for a handle without its value. What still panics in the library is how a test fails or skips, which the test runner catches: `expect`, `testing.Skip`, and `testing.TempDir` when the system makes no directory.

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

No `i128`, no complex numbers, no `uintptr`. Integer arithmetic wraps as in Go; checked arithmetic is a method returning `T?`: `CheckedAdd`, `CheckedSub` and `CheckedMul` on every integer type. A literal takes the type its context asks for.

- Generics with angle brackets: `List<T>`, `func Map<T, U>(...)`. Bounds with a colon: `T: Ord + Clone`.
- A method of a generic type names the type's parameters in its receiver, `func (l mut List<T>) Push(v T)`, and may rename them. Each receiver argument must be a name that is not already a type: `func (l ref List<int>) Sum()` is refused, where Go would silently declare a parameter called `int`. A method belongs to every instance.
- A type argument cannot hold a borrow: `List<ref string>` is refused, since the instance would store one, and an inferred one obeys the same rule. One exception: a generic function that only borrows values of a type parameter, every parameter mentioning it a `ref` or `mut` and the result not mentioning it, takes a type argument that holds a borrow, since its instance keeps nothing of that type in its own frame; `fmt.Fprintln(mut w, ...)` and `io.Copy(mut w, mut r)` take a `bufio.Writer`, which borrows the file under it. A `mut` parameter mentioning the type parameter must be the only parameter that does, since through it the body could store a value taken from another parameter into the caller's data, `Into<T>(dst mut T, v ref T)`; the rule applies to a written type argument as to an inferred one.
- Type arguments are inferred, after `go/types`: first those written at the use, `Map<int>(...)`, which may be a prefix; then from the arguments, matching each parameter's type against its argument's, a literal or `None` going last so that it takes its type from the others; then from the type the context expects of the result. A borrow passed to a bare `T` binds what it borrows, never the borrow. A variant of a generic enum and a literal of a generic struct written without arguments, `Leaf(1)`, `Pair{a: 1, b: 2.0}`, are inferred the same way, the context first.
- A method may have type parameters of its own, `func (l ref List<T>) Conv<U>(f func(ref T) U) List<U>`, on a generic type or a plain one; they come after the receiver's. Go forbids this because an interface value calls through a method table, which cannot hold a method that needs a copy per type argument; Snello compiles a copy per instance, so the only place a generic method cannot be called is through a trait used as a type, as in Rust. A generic function or method is not a value until its type arguments are known, so it must be called. Snello's generics follow the mature model, not Go's restrictions.
- A generic type refers to itself, directly or through other generic types, only with arguments that do not grow: `struct S<T> { s *S<S<T>>? }` is refused, because each instance is a separate type once generics are compiled and the instances would never end. This is Go's instantiation-cycle check for generic functions, applied to types since Snello monomorphizes them, and to calls: a generic function that calls itself, or another that calls it back, with an argument that wraps its own parameter is refused for the same reason.
- A type cannot contain itself by value, generic or not: only a pointer, slice or map breaks the chain, as in Go.
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

```go
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

- A type declares what it implements with `impl` on its declaration: `struct Circle impl Shape, Named { ... }`. The same for enums, and for a type declared over another, after the type: `type Duration i64 impl fmt.Stringer` (Go's `Duration` satisfies `fmt.Stringer` by its method alone, and Snello lists what a type implements wherever it is declared). The compiler checks every required method exists. There are no impl blocks; methods are ordinary Go methods.
- **A trait's name is looked up as a trait.** In an `impl` list or a bound, a name resolves to the nearest trait of that name, past a type that shadows it, so that a package may declare a type named `Error` and list the standard trait `Error` on it, as `io` does. Elsewhere the name is the type. A value whose type implements `Error` prints as its `Error()` message, as the predeclared `error` prints as its text.
- **A variant of another package's enum is named through the package, or bare in a pattern.** `io.NotExist` is a value and a constructor as `NotExist` is inside `io`; in a pattern, a bare name that is no name in scope but a variant of the scrutinee's enum is that variant, so a `match` on an `io.Kind` reads as it would in `io`, and a name that is neither binds. A type's name is not a value: `x := T` is `T (type) is not an expression`, as in Go.
- **A variadic parameter is `...T` or `...ref T`**, as "Variadic functions" below says. `...ref any` is the library's printing functions' alone: `func Println(args ...ref any)` is how `fmt` declares what the compiler writes, each argument taken by its own type, and `any` is a name only there, since a value of any type is otherwise a trait value. A library function declared without a body is provided by the runtime or by the compiler; `os.Exit` is one the compiler provides and knows never to return, as it knows of `panic`, since a signature cannot say so yet. Every package of the library is a `.snello` file under `std/`, these declarations included, so the checker holds no table of library names.
- **`[]byte(s)` and `string(b)` copy.** A conversion between a string and its bytes reads its operand and builds a new owned value, as in Go; the operand stays with its owner.
- **A conversion between a declared type and its underlying type moves.** `string(n)` for `type Name string`, `P2(p)` for `type P2 P`, gives the value itself under the other name, so a value that is not `Copy` moves, as it does to a parameter by value, and a borrow of one is refused, `cannot convert ref Name to string: the conversion takes the value, and a borrow lends it`, since the result and the borrowed value's owner would both free it. Both backends take the operand as an argument by value is taken: a variable is moved, a temporary is not dropped after it. A struct converts to a type declared over it in both backends, the same fields under two names, and one without a zero value only for its defaults converts as any other (Zero values).
- **Across packages, exported names only.** A program reaches the exported names of a package, `math.Pi`, `os.File`, and the exported fields and methods of its types; `io.Error{kind: ...}` is refused as `kind of io.Error is not exported`. A library package may declare a function without a body and a variable without a value: both are the runtime's, under the C name the compiler gives them. A top-level `const` takes a constant, as "Constants" below says; a top-level `var` takes any value, made the first time the variable is used.
- Trait fields are stored inline in the implementing struct and set at construction: `Circle{r: 1, name: "circle"}`. A trait field has no default value; `var name string = "x"` inside a trait is an error. A trait field follows the ownership rules of a struct field: `func (n ref Self) Name() ref string { return ref n.name }` borrows it, and `return n.name` is refused as a move out of a borrow.
- Overriding a default is defining the method. `super.Method()` inside an override calls the next definition in the impl order.
- Resolution order: the type's own methods, then its impl list from right to left, each trait followed by the traits it refines; a trait reached more than once counts where it was reached last, so a trait always precedes the traits it refines. Two traits providing a default for the same method, neither refining the other, are an ambiguity the type settles by declaring the method. Resolved at compile time; every call is direct.
- A trait refining another: `trait Logged: Shape { ... }`.
- Marker and operator traits use the same mechanism: `struct Vec3 impl Copy, Eq, Add { ... }` with a method `func (a Vec3) Add(b Vec3) Vec3`.
- The standard traits' methods, all on `ref Self` but `Drop`'s: `Clone() Self`, `Eq(o ref Self) bool`, `Cmp(o ref Self) Ordering`, `Hash() u64`, `Error() string`, `Drop()` on `mut Self`; `Ord` and `Hash` refine `Eq`. `Ordering` is a predeclared enum, `Less`, `Equal`, `Greater`, and every number and string has `Cmp`. A declared type listing `Eq`, `Ord`, `Hash` or `Clone` without writing the method has it derived from its fields, each of which must implement the trait, and the first that does not is reported with its type; `Copy` is the same check with no method, and a `Drop` type is never `Copy`. The predeclared types and their composites implement the markers by structure, and a type declared over one, `type ID int`, is what it is, and what it lists after `impl`; an enum of comparable payloads is `Eq` and `Hash` by structure, a struct only by listing them, since `==` on a struct has always gone through a method. `==` and a map's hashing go through the `Eq` and `Hash` a type wrote. A map key must be `Eq` and `Hash`, and a trait value is never one.
- The operator traits: `Add`, `Sub`, `Mul`, `Div`, `Rem` each require `func (a Self) Op(o Self) Self` and give `+ - * / %`; `Neg` requires `func (a Self) Neg() Self` and gives the unary `-`. A binary operator trait takes one type argument, the type of the right operand, `Mul<f64>`, and `Mul` alone is `Mul<Self>`: `Mul` listed on `V` satisfies a bound `Mul<V>` and `Mul<V>` listed satisfies `Mul`, while `Mul<f64>` listed does not satisfy `Mul`. The argument may be a type parameter of the declaration or of the bound, which are in scope for the impl list and the bounds. A type lists one `Mul`. `a op= b` is `a = a op b` for every type `op` works on. `a + b` is `a.Add(b)`: both operands by value, so a non-Copy operand moves, and a borrow of a non-Copy value is refused as it is for any value receiver. Numbers implement every operator trait with their own type on the right, strings implement `Add`. `==` is `Eq`, not an operator trait; `<` has no trait, orderings go through `Cmp`. The result type is `Self`; an operator producing another type waits for a use.
- A method taking its receiver by value cannot be called on a borrow of a value that is not `Copy`: `cannot move a out of a borrow`. A borrow of a `Copy` value is read.
- Assigning to a moved variable gives it back. After a branch set or select it is owned only when every path assigned it, the whole counting for a part; after a loop with a condition nothing is given back, since the body may not have run, while a loop without one gives back what every break path did; a closure gives nothing back to the function around it.
- A type that writes one of `Eq` and `Hash` must write the other: equal values must hash alike, and the compiler can promise that only for the pair it derives.
- Numbers, strings and `bool` have the methods of their standard traits by name, `Eq`, `Cmp`, `Hash` and `Clone`, with the receiver's own type for `Self`, so a `type ID int` compares with an `ID`.
- Bounds: `func Total<T: Shape>(xs ref []T)`. Monomorphized per `T`.
- **A `return` after `??` takes one result**, `x ?? return Err(e)`, as a `return` in a match arm does: a comma after it ends the arm or the element it stands in, and several results are written as a tuple.
- **Reflection is at compile time.** A generic function asks which type its type parameter is with `match T { int => ..., []E => ..., struct => ..., _ => ... }`, goes through a struct's fields with `for f := range fields(T)`, each with the constants `f.name` and `f.default` and the type `f.type`, reaches one with `v.[f]`, lent, and builds a value from its fields, `T{ for f := range fields(T) { [f]: ... } }`; an enum's variants are an arm each, `match v { for c := range variants(T) => ... }`. An arm naming a type takes that type alone, so `type Celsius f64` takes its own arm, its kind's or `_`, never `f64`'s; the arm written once per variant holds `v`, which is not assigned, moved or lent `mut` in it. The compiler checks the function once and, compiling it for a type, keeps the arm the type matches and writes each loop out once per part. It reaches the fields the function's package can name, or every field of a type that lists the marker trait `Reflect`; a type no arm matches, a field not reached, types that grow without end and a chain of generic calls deeper than 256 are refused where the function is used, by the checker. Nothing describes a type when the program runs (`11-reflection-at-compile-time.md`).
- **Attributes are typed constants read at compile time.** `@` and a constant of a type that lists the predeclared marker trait `Attribute` is written after a field's type and default, `Host string @json.Name("host")`, after a variant, `Dot @json.Name("dot")`, or on a type before its body, `struct Options @Usage("the server's options") { ... }`, with `type Usage string impl Attribute` the program's own; a struct with no fields is written by its name, `@json.OmitEmpty`, and one type is given once per declaration. A type listing `FieldAttribute`, `VariantAttribute` or `TypeAttribute`, which refine `Attribute`, is written on those places alone, `struct Skip impl FieldAttribute {}`, and refused elsewhere. A reflecting function reads one with `attr(f, json.Name)`, `attr(c, ...)` or `attr(T, ...)`, an option that is a constant in each copy written out; nothing of it is kept when the program runs. Go's struct tags are strings the compiler does not check; these are values it does (`11-reflection-at-compile-time.md`, section 8).
- **A trait used as a type is a Go interface value.** `[]Shape` holds any mix of implementing types; each element owns its value. One allocation and one indirect call per value, the same cost as Go. Such a value is `Send` only if the trait requires it: `trait Shape: Send { ... }`. A trait value satisfies its own trait as a bound only when no method or field of the bound's traits is written with `Self`, since the body may reach them through the parameter and a trait value cannot.
- Not in version one: self-types, implicits.

### Functions of a type

A type may declare a function that takes no value of it, written with **the type's name, a dot and the function's name**, as it is called: `func Point.Parse(text ref string) Result<Point, ParseError>`, called `Point.Parse("1,2")`. A generic type writes its parameters, `func Pair<A, B>.Of(a A, b B) Pair<A, B>`. It is a function, compiled as any is, that lives in the type's name. Where the type is known it is no different from a package function, `NewPoint`: the same direct call, a name in another place. What a plain function cannot do is be required by a trait and called on a type parameter, `T.Parse(s)`, which generic code needs to make a value of a type it does not know, as `encoding/json` does with `T.UnmarshalJSON(text)`; that is why it is in the language.

- **A trait may require one.** A trait declares a function without a receiver, `func Parse(text ref string) Result<Self, ParseError>`, which `Self` is the implementing type in, and a type that lists the trait writes it as its own, `func Point.Parse(...)`; a default method of the trait calls it on `Self`, the type that inherits the default, `Self.Parse(text)`; a trait's function has no default body in this version. A trait value has no type to call it on, so a trait with one is used through a bound or a match on a type, never through a value: `cannot call Parse through Named: it is a function of the type, which takes no value`, and a trait with one is no type argument for a bound of itself.
- **Called on a type parameter.** `T.Parse(text)` calls the function of the type `T` stands for, where `T` is bounded by the trait, `func read<T: Parse>(...)`, or matched by an arm naming it (`11-reflection-at-compile-time.md`, section 1). Each instance calls the type's own function directly; nothing is looked up when the program runs.
- **The precedents.** Rust calls these associated functions, `T::parse(s)`, and a trait may require one; C# 11 added static abstract members to interfaces for the same reason; Swift's protocols require static functions. Go has none: a package function per type, `ParseDuration`, or a method on a zero value the caller makes first, `var t Time; t.UnmarshalJSON(b)`, which needs a zero value Snello does not give every type (Zero values above).
- **Memory safe, no collector.** A function of a type is a function: it takes and returns values by the rules every function does, and a call of one is a direct call.

## Concurrency

Full reasoning in `03-ownership-across-tasks.md`.

- `spawn f(x)` moves `x` into the new task. A closure passed to `spawn` moves everything it captures.
- Sending moves the value; receiving owns it. A channel handle is owned: `ch.Clone()` gives another for another task, and the channel operations borrow it.
- `Send` is inferred from structure and never written.
- `Mutex<T>` and `RwLock<T>` own their data; `Lock()` returns a guard that is a `mut T` and unlocks on drop.
- `sync` types synchronize internally and are used through `ref`.

## Closures

A closure captures each variable in the weakest mode its body needs: by copy if the variable is `Copy`, holds no borrow and is only read, in which case the closure holds the value as it was when the closure was made (the iterators example's `adder(10)` returns `func(x int) int { return x + offset }`); `ref` if it only reads a value that is not `Copy`; `mut` if it assigns to it; by move if it consumes it or the closure is passed to `spawn`. A closure that captures by borrow is a borrow (see above). A closure that captures only copies may be stored and returned, and is a value with nothing to drop: the compiler keeps the copies in the function value itself, a few words beside the function pointer.

## Destructors

A type that implements `Drop` has `func (t mut T) Drop()`, run when the owner goes out of scope, on every exit path including panic unwinding. The language runs it, once: a program does not call `Drop` by hand, take it as a method value, defer it or reach it through a `T: Drop` bound, since the language would then run it again on a value whose resources are gone, as Rust refuses an explicit call of its destructor; `_ = v` drops a value early. `defer` remains for cleanup not tied to a value.

- **A type with a `Drop` is a struct or an enum**, or a type declared over one, whether it lists `Drop` or a trait that requires it. Whether its value is live, built and not moved away, is a flag kept beside its fields, so that its `Drop` runs once; a type declared over a number, a string or a slice has no fields to keep it beside, `Handle cannot implement Drop: it is declared over int, and only a struct or an enum has a Drop; hold the int in a field, struct Handle { v int }`. Rust implements `Drop` only on a type declared with fields, a struct, an enum or a union, and a newtype there is a struct.
- **No part leaves a value with a `Drop`.** Its `Drop` reads its fields when it goes, so a move of a field out of it, through any path of fields, and a pattern binding a variant's part by value out of such an enum are refused, as Rust refuses them; `replace` and `take` leave a value in the place, a clone copies, and a match on a borrow lends.
