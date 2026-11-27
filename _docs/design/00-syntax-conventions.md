# Syntax conventions

Status: decided. These are the rules the programs in `../../examples/` assume. Each was tested by writing those programs; when an example reads badly, the rule changes here first. The guiding principle: **if Go already has a way to say it, Snello says it the same way.** The principle covers syntax and keywords, which are what make Snello readable at first sight. The spelling of the primitive number types is vocabulary, not grammar, and there Snello follows the convention of the newer systems languages (`i32`, `f64`); see the number types below and `05-standard-library.md`. That is the one deliberate departure, stated once so it does not become a slope. New syntax exists only where Go has no answer.

## Carried over from Go unchanged

`package`, `import`, `func`, `type Name T` for a new type over another, `:=`, `var`, `for`, `if`, `defer`, channels and `select`, slices, maps, methods with named receivers, several results, as one tuple (below), exported names by capitalization, zero values for the types that have one (below), mutable parameters and locals.

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
- **A match arm is `pattern [if guard] => body`, ended by a comma or a newline.** The body is an expression, a block, or one statement: `Stop => return,`, `Balance(reply) => reply <- n,`, `"-i" => o.ignoreCase = true,`. A `return` gives one value everywhere, and several results are one tuple, `return (n, right)`; in an arm a comma after the return's value ends the arm. A bare name in a pattern is a binding or a constant variant such as `None`; the checker tells them apart. A parenthesized single pattern is that pattern.
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
- A type argument may hold a borrow, `ref T` or a type that holds one, under the four rules of the section below, so an iterator that lends a deque's elements is an `iter.Iterator<ref T>` and an adapter over it is an instance like any other.
- Type arguments are inferred, after `go/types`: first those written at the use, `Map<int>(...)`, which may be a prefix; then from the arguments, matching each parameter's type against its argument's, a literal or `None` going last so that it takes its type from the others; then from the type the context expects of the result. A borrow passed to a bare `T` binds what it borrows, never the borrow; inside a type a parameter binds what stands in its place, a borrow too, so `[]T` against a `[]ref string` binds `T` to `ref string`, and `Iterator<V>` read off an `Iterator<ref int>` binds `V` to `ref int`. A method's own type parameter may be bounded by its trait's or its type's parameters, `Chain<J: Iterator<T>>`, checked with their arguments in place. A variant of a generic enum and a literal of a generic struct written without arguments, `Leaf(1)`, `Pair{a: 1, b: 2.0}`, are inferred the same way, the context first.
- A method may have type parameters of its own, `func (l ref List<T>) Conv<U>(f func(ref T) U) List<U>`, on a generic type or a plain one; they come after the receiver's. Go forbids this because an interface value calls through a method table, which cannot hold a method that needs a copy per type argument; Snello compiles a copy per instance, so the only place a generic method cannot be called is through a trait used as a type, as in Rust. A generic function or method is not a value until its type arguments are known, so it must be called. Snello's generics follow the mature model, not Go's restrictions.
- A generic type refers to itself, directly or through other generic types, only with arguments that do not grow: `struct S<T> { s *S<S<T>>? }` is refused, because each instance is a separate type once generics are compiled and the instances would never end. This is Go's instantiation-cycle check for generic functions, applied to types since Snello monomorphizes them, and to calls: a generic function that calls itself, or another that calls it back, with an argument that wraps its own parameter is refused for the same reason.
- A type cannot contain itself by value, generic or not: only a pointer, slice or map breaks the chain, as in Go.
- **`type` declares a new type over another, and nothing else.** `type Celsius f64` is a type of its own, as Go's `type Celsius float64` is: converted with its name both ways, keeping its values' operators, a struct's fields and the methods the language gives the type under it, a string's `ToUpper` or an integer's `Abs`, but none of the methods written for that type, with its own methods and `impl` list. A struct, an enum and a trait are declared by their own keyword, `struct Node { ... }`, and Go's `type Node struct { ... }` is refused with that spelling. There is no alias, `type A = B`: Go's two forms differ by one character and mean the opposite, a type kept apart and the same type renamed, and here a name always means one type, which a reader sees from the declaration's first word. The cost is the alias's use in Go, moving a type between packages while code names its old place; here a moved type is renamed at every use, which the compiler finds. `byte` and `rune` are the predeclared aliases of `u8` and `i32`.
- Sum types: `enum Shape { Circle(r f64); Rect(w, h f64) }`.
- `match` with exhaustiveness checking. Patterns: literals including negative numbers `-1`, variants with bindings, tuples `(a, b)`, ranges `'0'..='9'`, alternatives `|`, guards `if cond`, wildcard `_`.
- `Option<T>` written `T?`, with `Some(x)` and `None`. `Result<T, E>` with `Ok(x)` and `Err(e)`. No `nil`.
- `x?` propagates `None` or `Err` from a function returning the same kind.
- `x ?? expr` unwraps or evaluates `expr`, which may `return`, `break`, or `continue`: `n := take(mut s.head) ?? return None`.
- `if x := opt { ... }` binds `x` when `opt` is `Some`; tuples destructure: `if (k, v) := line.SplitOnce("=") { ... }`.
- `.Expect("why")` unwraps or panics with the message.
- `if` and `match` are expressions when every branch yields a value: `pat := if ci { "(?i)" + p } else { p.Clone() }`, `return match e { ... }`.
- Receiving from a channel yields `T?`: `None` once the channel has ended, its last sending handle dropped and its buffer drained ("A channel ends by ownership" below). `for x := range ch` yields `T` and ends then.

### A type argument that holds a borrow

An iterator over a deque lends the deque's elements, so it is an `iter.Iterator<ref T>`, and `d.Iter().Filter(p).Map(f)` is an instance of `iter.Map` over an instance of `iter.Filter` over `deque.Iter<T>`, each holding the deque's borrow. Rust writes the lifetime, `Iterator<Item = &'a T>`, and every adapter carries it; Snello writes none, so four rules let the checker follow the borrow through generic code, which it checks once and does not look inside for each type argument. Nothing is checked when the program runs.

1. **An instance holds what its type arguments hold.** `iter.Map<deque.Iter<int>, ref int, int>` borrows the deque, so it is a struct that holds a borrow (chapter 6 of the book): it cannot go into owned data, be sent or spawned, or become a trait value, and is returned only as a borrow is. Three things would hide the borrow from the checker, and a type argument that holds one is refused where each would happen:
   - **Owned data the checker does not follow.** An instance that keeps the argument in a map, a channel or a pointer, in its fields or in the signature of the function called, is refused at the type written or the call, `a pointer cannot hold View: it holds a borrow`. A slice of one is followed, as the slice `Split` gives is. So `deque.Deque<ref T>`, `rc.Rc<ref T>` and `set.Set<ref T>`, whose storage is a pointer or a map, are refused, and a collection of borrows is the slice `Collect` gives.
   - **A trait value or a closure made in a generic body.** A body that makes a trait value of a value whose type mentions a type parameter, `return Wrap<T>{x: x}` as a `Show`, or captures one in a closure, `func() T { return x }`, hides the borrow in a value whose type holds none, so that parameter takes no type argument that holds a borrow, `Hide's T cannot hold View: it holds a borrow, which Hide hides in a trait value at 4:12`.
   - **A match on the type parameter** (`11-reflection-at-compile-time.md`), whose arm reads the parameter as the type it matched and can make a borrow of it.
   The last two are followed through the generic calls a body makes, as a match on a type is: a function that passes its `T` to one that hides it hides it too, in an argument or as the receiver, `h.Hide()` with `h` of a type parameter passing it on as `Self`. A method reached through a bound is the instance's, which no call in a generic body names, so an instance of a generic type whose methods hide its parameter, `Box<View>` with `Box`'s `Hide` making a trait value of its `T`, is refused where it is made.
2. **A generic call passes borrows along.** At a call of a generic function or method with a type argument that holds a borrow, the result holds what every argument of a type holding a borrow holds, the receiver's included, read out of it as `q := r` reads what `r` holds, so `d.Iter().Filter(p).Map(f)` borrows `d` until the chain's last use. Each argument lent `mut` whose parameter mentions such a type parameter, directly or through a bound, takes what the other arguments of that type parameter hold, as an assignment's target takes what its value holds, since the body may store one in it: with `func Put<T>(xs mut []T, x T)` and `r := ref t`, `Put(mut xs, r)` makes `xs` borrow `t`, and is refused when `t` goes first. An argument that holds a `mut` borrow inside it, a struct with a `mut` field passed by value, is refused beside another argument that brings a borrow of the same type parameter, since the store would go through a borrow the call cannot name. It is sound because a function generic in `T` makes no value of `T` (rule 3 sees to the bounds' functions, and rule 1 to the bodies that would hide one): each value it returns or stores came from its arguments, which is Reynolds' parametricity (*Types, Abstraction and Parametric Polymorphism*, 1983), as Wadler's *Theorems for Free!* (1989) states it for a function's result. It is the rule a slice of borrows follows for `Push` already, and Rust gets the same by giving the arguments and the result one lifetime. A declared borrow is still tied by the rule for returned borrows, to the receiver or the one borrowed parameter, so a function that is not generic still cannot return a `Zip` or `Chain` of two borrowed sources, as it cannot return either of two borrowed strings.
3. **An implementation keeps its trait's word.** Where a trait declares a result with a value of a type parameter in it that no borrow it writes leads to, `Next() T?`, `Get() (T, ref int)` or `func Of(s ref string) Self`, an implementation's result holds only borrows stored inside its receiver and the parameters whose declared type mentions `Self` or the trait's parameters, reached through `ref` borrows; a value behind a declared borrow, `Peek() ref T?`, is reached through that borrow, which the rule for returned borrows ties to the receiver: never the borrow a parameter is, never a borrow of what the receiver owns, never a reborrow through a `mut`, which would give two live `mut` items or one freed by the next call. `deque.Iter.Next` returns `ref it.d.buf[i]`, through the `ref` borrow `it.d` holds; `Some(mut o.xs)` and `func View.Of(s ref string) View { return View{s: s} }` are refused, `Next returns a borrow of o, which it owns: Iterator<mut []int>.Next returns only what o borrows`. Rule 2 rests on it, since a generic body that calls `Next` or `T.Of` treats each result as built from the arguments it gave. Rust's `Iterator` makes the same promise: its `Item` cannot borrow from `&mut self`, and an iterator that lends from itself is not one.
4. **A bound may take a borrow.** `func Sum<I: iter.Iterator<ref int>>(it mut I) int` is a function generic in an iterator of borrowed items, and `ref T` and `mut T` are types like any other inside `<...>`. A trait value of such an instance, `iter.Iterator<ref int>` as a type, stays refused: it would hide the borrow.

A closure given to an adapter borrows nothing, by the rule for a closure passed to a function that keeps it: what a closure borrows is known by the variable holding it, not by its type, so an adapter's type cannot carry it. It captures copies, and a test that needs a string the closure would borrow is written in the loop's body instead. `Filter`'s test takes `ref T`, so over borrowed items it takes a `ref ref int`, as Rust's `filter` gives `&&i32`.

**Memory safe, no collector.** Every value the rules admit holds its borrows where the flow graph of `10-borrow-checking.md` already follows them: one owner, borrows that do not outlive their source, nothing in owned data the graph does not see, nothing across tasks, and a value that holds a borrow and runs a `Drop` reads it when dropped, whether alone or as the element of a slice. Rules 2 and 3 only tell the graph where a borrow went through generic code it does not look inside. Nothing is counted or checked when the program runs.

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
- **A conversion between a declared type and its underlying type moves.** `string(n)` for `type Name string`, `P2(p)` for `type P2 P`, gives the value itself under the other name, so a value that is not `Copy` moves, as it does to a parameter by value. A borrow converts to a borrow of the same kind, a view: `string(n[1:])` for a `ref Name` is a `ref string`, the same words, lending what the borrow lends and owning nothing, so no second owner frees it. Reading a declared type as its underlying type is anyone's, since a read cannot break what the type keeps: `string(ref n)`, `[]int(ref ids)`, and a `ref` of a declared type passes as one of its underlying type with nothing written when that type has no name, `sum(ref ids)` for `sum(xs ref []int)`. Seeing a borrowed value as a declared type, `Sorted(ref xs)`, and a `mut` view of a declared type as another, `[]int(mut ids)`, are the declaring package's alone: `cannot convert ref []int to lib.Sorted: only package lib, which declares Sorted, may see a value as one`. The rule is about borrows, what one owner lends and another sees: code outside the package neither hands the package's functions a view of someone else's value under the package's type, nor changes, through another type, a value of the package's type that it was only lent. A value a program owns it converts as before, `lib.Sorted(xs.Clone())`, as Go converts one, so a type that must keep a promise over its values keeps its fields its own (a struct with unexported fields), not its underlying type. Rust has no safe view of a borrow as another type, which needs `unsafe` or a crate wrapping it; the package line and the `ref`/`mut` split give one with nothing new to learn. Both backends take the operand as an argument by value is taken: a variable is moved, a temporary is not dropped after it. A struct converts to a type declared over it in both backends, the same fields under two names, and one without a zero value only for its defaults converts as any other (Zero values).
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
- **A `return` gives one value**, `return (n, right)` for several, a tuple; a statement `return n, right` is refused with that form, `return gives one value, and several are one tuple: write return (a, b)`. After `??`, `x ?? return Err(e)`, and in a match arm, a comma after the return's value ends the arm or the element it stands in.
- **Reflection is at compile time.** A generic function asks which type its type parameter is with `match T { int => ..., []E => ..., struct => ..., _ => ... }`, goes through a struct's fields with `for f := range fields(T)`, each with the constants `f.name` and `f.default` and the type `f.type`, reaches one with `v.[f]`, lent, and builds a value from its fields, `T{ for f := range fields(T) { [f]: ... } }`; an enum's variants are an arm each, `match v { for c := range variants(T) => ... }`. An arm naming a type takes that type alone, so `type Celsius f64` takes its own arm, its kind's or `_`, never `f64`'s; the arm written once per variant holds `v`, which is not assigned, moved or lent `mut` in it. The compiler checks the function once and, compiling it for a type, keeps the arm the type matches and writes each loop out once per part. It reaches the fields the function's package can name, or every field of a type that lists the marker trait `Reflect`; a type no arm matches, a field not reached, types that grow without end, a chain of generic calls deeper than 256 and a `refuse(msg)` an instance reaches, through the arms of the matches on a type, on `attr` and on a part's name the compiler decides, are refused where the function is used, by the checker. Nothing describes a type when the program runs (`11-reflection-at-compile-time.md`).
- **Attributes are typed constants read at compile time.** `@` and a constant of a type that lists the predeclared marker trait `Attribute` is written after a field's type and default, `Host string @json.Name("host")`, after a variant, `Dot @json.Name("dot")`, or on a type before its body, `struct Options @Usage("the server's options") { ... }`, with `type Usage string impl Attribute` the program's own; a struct with no fields is written by its name, `@json.OmitEmpty`, and one type is given once per declaration. A type listing `FieldAttribute`, `VariantAttribute` or `TypeAttribute`, which refine `Attribute`, is written on those places alone, `struct Skip impl FieldAttribute {}`, and refused elsewhere. A reflecting function reads one with `attr(f, json.Name)`, `attr(c, ...)` or `attr(T, ...)`, an option that is a constant in each copy written out; nothing of it is kept when the program runs. Go's struct tags are strings the compiler does not check; these are values it does (`11-reflection-at-compile-time.md`, section 8).
- **A trait used as a type is a Go interface value.** `[]Shape` holds any mix of implementing types; each element owns its value. One allocation and one indirect call per value, the same cost as Go. Such a value is `Send` only if the trait requires it: `trait Shape: Send { ... }`. A trait value satisfies its own trait as a bound only when every method and field of the bound's traits can be reached through a table: none written with `Self`, none with type parameters of its own, and no function of the type, since the body may reach any of them through the parameter and a trait value cannot.
- Not in version one: self-types, implicits.

### An instance of a generic trait as a type

A generic trait takes type parameters, and an instance of it gives each one an argument. **An instance with every argument given is a type, as a trait's name is**, and a value of it is a trait value, with all a trait value does and nothing more. The example is a trait a program declares for the events it raises, with a default method:

```go
trait Listener<E> {
	func (l mut Self) On(e ref E)
	func (l mut Self) OnAll(es ref []E) { ... }   // a default
}

struct Audit impl Listener<Order> { seen int }
struct Revenue impl Listener<Order> { sum int }
struct Logged<E> impl Listener<E> { prefix string }

listeners := []Listener<Order>{Audit{seen: 0}, Revenue{sum: 0}, Logged<Order>{prefix: "order seen"}}

func notify<E>(ls mut []Listener<E>, e ref E) {   // any listeners of any event
	for i := 0; i < len(ls); i++ {
		ls[i].On(e)
	}
}
```

`[]Listener<Order>` holds listeners of different types, each element owning its value, which a bound cannot do: `L: Listener<Order>` stands for one type in each instance of the code. An iterator needs no such trait: `range` takes any type with `Next` by its shape (above).

- **What it holds.** A value of a type that implements that instance: `Audit`, which lists `Listener<Order>`, or `Logged<Order>`, whose generic type implements the trait over its own parameter. Each instance is its own type: a `Listener<Order>` is no `Listener<Refund>`, as a `[]int` is no `[]i64`. A type argument cannot hold a borrow, as one of an instance turned into a trait value cannot: `Listener<ref string>` is refused, since the value would hide the borrow.
- **In generic code** an argument may name a type parameter, `notify<E>(ls mut []Listener<E>, e ref E)`, and a generic type's field may hold one, `struct Bus<E> { listeners []Listener<E> }`.
- **What it does is what a trait value does.** Its methods are the instance's, `On(e ref Order)` and the default `OnAll`, called through its table. It has no zero value, is not `Eq`, prints only when its trait is `Error` or `fmt.Stringer`, and is `Send` only when the trait requires it. A value of one trait is not one of another, as for any trait value.
- **What it cannot call.** A method a table cannot hold: one whose parameters or results are written with `Self`, and one with type parameters of its own, which would need a function for every type argument. The compiler names the method and the reason. For the same reason such a value meets its own instance as a bound only when its trait has no such method.
- **One instance per type.** A type implements a generic trait at one argument, counting the traits it implements through refinement: with `trait Refunds: Listener<Refund>`, `struct Audit impl Listener<Order>, Refunds` is refused where `Audit` is declared, `Audit implements Listener<Order> and through Refunds, Listener<Refund>: a type implements a generic trait at one argument`. So which `Listener` a type implements always has one answer.
- **Inference reads that answer.** `send<E, L: Listener<E>>(l mut L, e E)` called `send(mut counter, 5)`, with `struct Counter impl Listener<i64>`, reads `E` as `i64` off `Counter`'s instance before the literal takes a type, so the 5 is an `i64`; `notify(mut listeners, ref order)` reads `E` off `[]Listener<Order>`. A type that implements no instance is named: `cannot infer the type parameter E of count: Blob implements no Listener`.

**Considered and not taken: the trait value implementing its own trait.** Rust's `Box<dyn Trait>` may implement `Trait`, so a method a table cannot hold is compiled once more for the boxed value. It would let such a method be called through the value, at the price of a second rule for which body runs, since a default a refining trait replaces would run the replaced body through the box. One rule, a trait value calls what its table holds, is kept.

**The precedents.** Go's instantiated generic interface, `Listener[Order]`, is a type; Rust's `dyn Listener<Order>` is a trait object, which leaves out a generic method as Snello does; Swift's `any Listener<Order>` is an existential. Rust reads an argument off a trait's one associated type, as Snello reads it off a type's one instance; Go infers no type argument from a method set.

**What it costs** is what a trait value costs: one allocation when the value is made and one indirect call per method call. Each pair of a type and an instance has its own table, built once when the program is compiled and named for the instance's arguments and the trait's package, as a generic type's instance is named.

**Memory safe, no collector.** The value is a trait value: one owner, a box holding the value and a pointer to a table, freed by its `Drop`, which drops the value it holds. Nothing is added when the program runs.

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

### A handle on a task

A panic in any task ends the program, with its message and status 2, as a panic in `main` does, and `spawn f(x)` written as a statement keeps that meaning. **`spawn` written as an expression starts the task under a mark and gives a handle on it**: `h := spawn crunch(data)` is a `task.Handle<int>` when `crunch` returns an `int`, and `h.Join()` waits until the task has ended and returns `Result<int, task.Panic>`, `Ok` with what the call returned or `Err` with the panic that ended it, whose `Message()` lends the message. A panic is still a bug and `Result` is still how a failure is handled; the handle is containment, as a thread's join handle is in Rust, Java and Swift: it limits what a bug takes down to the task it is in.

```go
h := spawn crunch(data)                             // task.Handle<int>
match h.Join() {
	Ok(n)  => fmt.Println("result", n),
	Err(p) => fmt.Println("the task failed:", p.Message()),
}
```

- **The call is spawned as a statement's is.** Every argument, every variable a closure captures and a receiver taken by value is `Send` and moves into the task, and a borrow cannot be spawned, with the statement's messages. The call's result is `Send` too, since the handle carries it to the task that joins: `cannot spawn for a handle: rc.Rc<int> stays in its task`. A call that returns nothing gives a `task.Handle<()>`.
- **A handle is an owned value that `Join` takes.** It is moved, never cloned, so a task is joined once, by whoever holds the handle, in any task, since a handle is `Send`; it has no zero value, as a channel handle has none. A handle dropped without a `Join` leaves its task running, and the task's result is dropped when the task ends. A `Join` on a task that never ends waits forever, as a receive on a channel no one sends on does, and the deadlock report says so when nothing else can run.
- **A handle is a value, not a name for the task.** Nothing is sent to a task through it and no task finds another by it: there is no address, no mailbox, no link and no kill. A group of tasks is a library type written over handles.
- **A contained panic ends its task as an uncontained one ends the program.** The message is printed on the standard error stream when the panic happens, `panic: index out of range [7] with length 3`, so that a failure the program only counts still shows; the values with a `Drop` method the task made since it started are dropped, innermost first, and so are the channel handles it held, since a handle lost would keep its channel open for every other handle; its deferred calls run, once each; then the task hands `Err` to its handle and ends. A lock the task held is released by its guard's drop and nothing more: the value under it may be half changed, so a task whose panic is contained should own its state. The plain memory of the frames the panic abandoned, a slice of numbers or a message a `Sprintf` built, stays allocated until the process ends, as it does for a failed test ("Catching a panic" in the book's testing chapter): a guard per owned value would cost every function of every program a registration per value, and a panic is a bug the program fixes.
- **`spawn` as a statement keeps its meaning.** Its panic ends the program. A program that wants a task's failure contained writes the handle, where a Go program recovers in every goroutine it starts; the choice is at the spawn, once, and nothing in the called function says which way it was started.

**Considered and not taken.** A library function taking a closure, `task.Spawn(func() { ... })`, as Rust's `thread::spawn`: a closure handed to a function is a borrow of the frame that wrote it and cannot outlive the call, so a spawned closure needs the keyword, which moves its captures. A `Detach` method: dropping the handle is the detach. A handle in a `select`, a `TryJoin` or an `IsDone`: a group written over `Join` covers the programs that were asked for, and the handle stays one method until a program shows the need. A panic that is not printed when a handle catches it, as Go's `recover` prints nothing: a server that joins a thousand tasks and counts the failures would then hide the bug's message, and the program that wants silence redirects its standard error.

**The precedents.** Rust's `thread::spawn` returns a `JoinHandle<T>` whose `join` gives `Result<T, Box<dyn Any>>`, Java's `Thread.start` and Swift's `Task` return the handle from the call that starts the task, and Kotlin's `async` returns a `Deferred<T>`; none gives a goroutine's silence to a bug. Go has `go` alone, and a panic in any goroutine ends the process unless that goroutine recovers.

**What it costs.** A `spawn` with a handle makes a channel of one slot, one allocation, clones its handle into the task, and runs the call under a mark, one `sigsetjmp`; the result crosses as a channel value does. A `spawn` as a statement costs what it did.

**Memory safe, no collector.** A handle is a channel handle, and the task holds the other: the result or the panic goes through the channel by the rules every value crossing a task obeys, the last handle frees the channel, and a result no one joined is dropped by the task. The unwinding is the one a failed test uses.

### A group of tasks

A function that starts several tasks and needs all of them waits for them together and hears of the first that failed. **`task.Group` is written in the library over handles, and is a scope**: `g.Add(h)` puts a task started for a handle in the group, with every task it starts, `g.Cancel()` cancels them, as the first member to panic does, so that the others stop, and `g.Wait()` returns once every task has ended, `Ok` when each returned and `Err` with the first panic among them. It is Go's `errgroup.Group` and the task group of Swift, Java and Kotlin, with a panic where those have an error, since a panic is the one failure a task has that its caller does not already hold as a value.

```go
g := task.NewGroup()
g.Add(spawn serve(listener))
g.Add(spawn flush(queue.Clone()))
match g.Wait() {
	Ok(_)  => {},
	Err(p) => fmt.Println("a task failed:", p.Message()),
}
```

- **A member is a task started for a handle of no result**, `task.Handle<()>`. A task with a value to give back sends it on a channel or is joined by its own handle; the group is for the tasks a function runs for their effect, as a server's are. A `Result` a member computes is a value it already holds and reports as it likes: the group hears of panics, which a member cannot report itself.
- **A cancel is a stop asked for, never forced.** The group is a scope ("Cancellation and deadlines" in `06-tasks-and-channels.md`): `Cancel`, and the first member's panic, cancel it, and every call a member makes that waits stops waiting, a receive with `None`, a send dropping its value, a `select` with a case as if its channel had ended, `time.Sleep` returning, a `Read` failing with `io.Cancelled`; `task.Cancelled()` says so to a member that asks. A member that never waits and never asks is waited for. There is no kill, as the handle has none, so a member that ignores its cancellation and never ends is waited for forever, and the deadlock report says so when nothing else can run.
- **`Wait` takes the group**, so a group is waited for once, by the function that owns it; a group dropped without a `Wait` leaves its members running, as a handle dropped leaves its task. A group is a value the function owns, and its members are that function's: there is no tree of groups and no group that outlives its function.
- **The library has no type that restarts tasks.** A program that wants a task run again writes the loop over `Join`, with its limit in plain sight, and the book shows it. There is no supervisor, no restart strategy and no kind of child: each of those is a policy, and a policy belongs to the program that has the facts.

**Considered and not taken.** A `Go(f, a)` that spawns for the group, as `errgroup` does: the group would then be the one place a task is started without the keyword, and the keyword is what moves the task's values in; `Add(spawn f(a))` says the same with the spawn in sight. A member with a result, `Group<T>` with `Wait` returning `[]T`: the programs asked for run their tasks for their effect, and a group over handles of a result is one line more when one shows the need. A `Done` channel for a member to select on, which the group first had: every wait a member makes is woken by the cancel, so the channel was a second way to ask one question, and it went when the group became a scope.

**What it costs.** A group is its scope, one record of the runtime's, and its members' handles in a slice. `Add` puts the member in the scope with one compare-and-swap on the handle's channel, and `Wait` joins the members in turn: no task stands by a member, since the runtime that catches a member's panic cancels the group as it does and records the first to fail, so a group of ten thousand members costs 342 ns a member against 391 with a watcher each. What its programs found, a channel handle lost to a contained panic, in a frame and as an argument evaluated before one that panicked, is fixed beside it, in the backends' guards.

**Memory safe, no collector.** The members' handles are moved into the group and joined by `Wait`, which takes it; the scope is freed by its last holder, the group or the last member to leave it; a panic's message crosses from the member to `Wait` through the handle's channel, as any result does.

### A channel ends by ownership

Every channel panic Snello had was about closing, and every channel deadlock but one was a task waiting on a channel whose other side was gone. Both came from one gap Go has and Snello need not: nothing owned the end of a channel, so closing was a call anyone could make at any time, and the runtime could not tell a channel no one would ever send on from one whose sender was busy. Snello's handles are owned, cloned per task and dropped by the last holder, and the checker tells a sending handle, `chan<- T`, from a receiving one, `<-chan T`. **A channel ends when its last sending handle is dropped.** A receive then yields `None` once the buffer is empty, `for range ch` ends, and a `select` receive case on it is ready with `None`. A `chan T` handle sends and receives, so it counts on both sides: a producer that keeps a `chan T` keeps its channel open, and hands the receiving end away as a `<-chan T`, as `produce` does, or keeps a `chan<- T` of its own.

```go
func produce(n int) <-chan Job {
	out := make(chan Job)
	w := out.Clone()
	spawn func() {
		for i := 0; i < n; i++ {
			w <- Job{id: i}
		}
	}()                              // w goes with the task: the channel ends when the task does
	return out                       // moved where a <-chan Job is wanted: a receiving handle from here on
}

for j := range produce(3) {          // ends when the sender is gone and the buffer is empty
	fmt.Println(j.id)
}
```

- **`close` leaves the language.** Nothing is left for it to do: the end is the drop of the last sender, which a program writes with a scope, a `take` or `_ = w`. `close(ch)` is refused with `close is gone: a channel ends when its last sending handle is dropped`. The three panics, `send on a closed channel`, `close of a closed channel` and `channel closed while a send waited on it`, have no way to happen and leave the runtime.
- **A send on a channel whose last receiving handle is gone drops the value and goes on**, where a Go goroutine blocks forever: no one can ever take the value, so dropping it, in the sending task, is the one thing to do. A producer that wants to stop asks `ch.Open()`, which reports whether a receiving handle remains; a stop is asked for and never forced, as a task group's `Done` is. A send that was waiting when the last receiver went drops its value the same way.
- **A handle's direction is its type's.** `ch.Clone()` clones in the handle's direction; a `chan T` moved where a `<-chan T` or a `chan<- T` is wanted, into a parameter, a field, a result or a send, gives the other half up there, as a drop of that half would, which may end the channel or free its senders. A borrow gives nothing up: a `ref chan T` reads as a `ref <-chan T` or a `ref chan<- T` as before.
- **No new kind of channel, no priority in `select`, no unbounded buffer, no broadcast.** Each is a new concept; a library type may bring one later when a program shows the need.
- **The deadlock report names the channel.** What remains of deadlock is a genuine cycle, tasks each waiting for another, and a receive from a channel no task will send on while a sender's handle sits unused. A channel knows where it was made, and the report says what the main task waits for and how many tasks wait on that channel: `fatal error: all tasks are asleep - deadlock!` followed by `the main task waits to receive on the channel made at pipeline.snello:39, which 2 sending and 1 receiving handles remain on, with 0 tasks waiting to send and 1 to receive`.

**Considered and not taken.** A channel that ends when its last handle of either kind goes: a task waiting to receive holds a handle, so the channel it waits on would never end, and a receiver could never learn that the senders are gone. A `v, ok := <-ch` or a `Close` method: the `T?` says it, and a method is the call anyone could make. A `nil` channel to park a `select` case on: a case a program wants out of is written out of the `select`. Dropping the value of a refused send silently where a program would rather know: `Open` asks, and a value a program cannot afford to lose is not sent to a channel whose receiver may be gone.

**The precedents.** Rust's `mpsc`: a `recv` returns an error once every `Sender` is dropped, and a `send` returns the value once the `Receiver` is gone, which is the rule here with the ends tied to Snello's typed handles. Go closes by a call, with its five ways to go wrong; Kotlin's channels close by a call too, and Swift's `AsyncStream` finishes when its continuation is dropped, the ownership half of this rule.

**What it costs.** The count of a channel's handles becomes two, senders and receivers, in one word, on the line where the one count was: a clone or a drop of a `chan T` is one atomic add of both halves, as it was one add, and of a one-direction handle one add of its half. A send reads a flag beside the lock, set when the last receiver went, where it read the closed flag, and a receive the flag the last sender set: neither reads the counts, whose line every clone and drop writes, and which cost a spawn a tenth more when a send read it. A conversion of a `chan T` to one direction is one atomic subtract, written by the compiler only where a `chan T` value changes type, which a program does once per handle it hands away. The channel's position is one pointer. The `tasks` row of `bench/`, the pipeline example and a program of sends and receives pay nothing new.

**Memory safe, no collector.** A channel is freed when both counts are zero, by the last handle of either kind, with the values still in its buffer dropped as today; the value a sender drops because no receiver remains is dropped by the sender, in its task, once, through the channel's element drop; a handle given up at a conversion is given up once, since the value's type changes once.

## Closures

A closure captures each variable in the weakest mode its body needs: by copy if the variable is `Copy`, holds no borrow and is only read, in which case the closure holds the value as it was when the closure was made (the iterators example's `adder(10)` returns `func(x int) int { return x + offset }`); `ref` if it only reads a value that is not `Copy`; `mut` if it assigns to it; by move if it consumes it or the closure is passed to `spawn`. A closure that captures by borrow is a borrow (see above). A closure that captures only copies may be stored and returned, and is a value with nothing to drop: the compiler keeps the copies in the function value itself, a few words beside the function pointer.

## Destructors

A type that implements `Drop` has `func (t mut T) Drop()`, run when the owner goes out of scope, on every exit path including panic unwinding. The language runs it, once: a program does not call `Drop` by hand, take it as a method value, defer it or reach it through a `T: Drop` bound, since the language would then run it again on a value whose resources are gone, as Rust refuses an explicit call of its destructor; `_ = v` drops a value early. `defer` remains for cleanup not tied to a value.

- **A type with a `Drop` is a struct or an enum**, or a type declared over one, whether it lists `Drop` or a trait that requires it. Whether its value is live, built and not moved away, is a flag kept beside its fields, so that its `Drop` runs once; a type declared over a number, a string or a slice has no fields to keep it beside, `Handle cannot implement Drop: it is declared over int, and only a struct or an enum has a Drop; hold the int in a field, struct Handle { v int }`. Rust implements `Drop` only on a type declared with fields, a struct, an enum or a union, and a newtype there is a struct.
- **No part leaves a value with a `Drop`.** Its `Drop` reads its fields when it goes, so a move of a field out of it, through any path of fields, and a pattern binding a variant's part by value out of such an enum are refused, as Rust refuses them; `replace` and `take` leave a value in the place, a clone copies, and a match on a borrow lends.
