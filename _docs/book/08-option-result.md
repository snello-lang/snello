# 8. Option, Result, `?` and `??`

`examples/run/options.snello`, `boxes.snello` and `slices.snello` exercise this chapter; the programs under `../../examples/core/errors/` named at the end show the errors.

Snello has no `nil`. A value that may be absent has an optional type, and an operation that may fail returns a result. Both are enums the language knows, so `match` takes them apart like any enum, and two operators, `?` and `??`, handle the common cases in one character or two.

## `T?`

`T?` is a value that is either `Some(x)` with an `x` of type `T`, or `None`:

```go
struct Node {
    value    string
    parent   NodeID?               // None for the root
    children []NodeID
}

```

```go
n := Node{value: value, parent: Some(parent), children: []NodeID{}}
root := Node{value: "root", parent: None, children: []NodeID{}}
```

`?` applies to the whole pointer or borrow before it and to the element inside a container: `*Node?` is an optional pointer, `ref string?` an optional borrow, `[]int?` a slice of optional ints. A borrow of an optional is written with parentheses, `mut (*Node?)`. Appendix A has the full rule.

`None` has no type of its own; it takes the type the context asks for, a field, a parameter, a result, or the other side of a comparison. Written where nothing asks for a type, it is an error: `cannot tell the type of None here`. `Some(x)` takes the type of `x`, or the context's when it has one, so `Some(ref n.value)` is a `ref string?` where that is wanted.

The methods on an optional are `Expect(msg)`, which yields the value or stops the program with the message, `Unwrap()`, `UnwrapOr(v)`, `IsSome()` and `IsNone()`. The first three take the optional, since they give its value away, so after `o.Expect("present")` the name `o` is gone unless its type is `Copy`; the two tests only read it:

```go
text := os.ReadToString("input.txt").Expect("read input")
```

## `Result<T, E>`

A `Result<T, E>` is either `Ok(x)` with an `x` of type `T` or `Err(e)` with an `e` of type `E`. The error type is any type the program chooses, usually an enum with one variant per thing that can go wrong, or the trait `Error` itself, which holds any error; chapter 17 is about `E`:

```go
enum ConfigError impl Error {
    Unreadable(cause io.Error)
    BadValue(cause strconv.Error)
    Missing(key string)
}

func Load(path string) Result<Config, ConfigError> { ... }
func loadAny(path string) Result<int, Error> { ... }
```

`Ok(x)` and `Err(e)` take their full type from the context, since the argument says nothing about the other half; writing `x := Ok(1)` with nothing asking for a type is an error. A function that returns a result and has nothing to return on success returns `Result<(), E>` and writes `return Ok(())`. The methods are the option's, under the names `Expect`, `Unwrap`, `UnwrapOr`, `IsOk` and `IsErr`.

## Taking them apart

`match` handles both as the enums they are, exhaustively:

```go
match Parse(ref src) {
    Ok(e)  => fmt.Println(Eval(ref e)),
    Err(e) => fmt.Println("error:", e),
}

text := match os.ReadToString(ref path) {
    Ok(t)  => t,
    Err(e) => return Err(Unreadable(path, e)),  // path moves only on this path
}
```

When only the present case matters, an `if` binding is shorter. The block runs with the pattern bound when the optional holds a value, and the `else`, if any, when it does not:

```go
if parent := t.nodes[id].parent {
    t.nodes[parent].children.RemoveValue(id)
}

if (k, v) := line.SplitOnce("=") {
    kv.Insert(k.Trim().Clone(), v.Trim().Clone())
}
```

The left side may be a name, a tuple of names, or a variant pattern. `if x := opt {` binds; `if x := f(); cond {` with a `;` is an initializer and a condition.

## The methods

Both types have `Expect(msg)`, which gives the inside or panics with the message, `Unwrap`, which panics with a message of its own, and `UnwrapOr(v)`, which gives the inside or `v`. An optional answers `IsSome` and `IsNone`, a result `IsOk` and `IsErr`. `Map(f)` applies a function to the present or successful case and leaves the other as it is, so `half(8).Map(twiceText)` is `Some("8")` and `half(3).Map(twiceText)` is `None`; a result also has `MapErr(f)`, which maps the error and leaves a success alone. The function may be a closure, capturing what it needs, or a function's name, and the result type of the method is whatever the function gives back:

```go
suffix := "!"
half(8).Map(func(v int) string { return fmt.Sprint(v) + suffix })       // Some("4!")
parse("").MapErr(func(e string) int { return e.Len() })                 // Err(5)
v := parse(s).MapErr(func(e string) int { return e.Len() })?            // the mapped error passes up
```

The methods that give the inside away, `Expect`, `Unwrap`, `UnwrapOr`, `Map` and `MapErr`, take their receiver: the optional is moved into the call and cannot be used after. The tests, `IsSome` and the rest, lend it.

## `??`: a value or an exit

`x ?? else` yields the value inside `x` when it is `Some` or `Ok`, and otherwise evaluates `else`. The else is an expression of the inside's type, or a `return`, `break` or `continue`, or a block ending in one:

```go
n := take(mut s.head) ?? return None
text := kv.Get(ref key) ?? return Err(Missing(key))   // key moves only if the else runs
at := cur ?? break
n := cur ?? { *cur = Some(&Node{key: k, val: v, left: None, right: None}); return }
```

`??` sits below every other operator and groups to the right: `a ?? b == c` is `a ?? (b == c)`. Through a borrow it yields the same borrow of the inside, so `cur ?? ...` on a `mut (*Node?)`, a mutable borrow of an optional pointer, is a `mut *Node`, which is what walking a tree needs (chapter 6).

## `?`: pass the failure up

`x?` inside a function yields the value inside `x` and returns early with the absent or failed case when there is none. The function's result type says which kind `?` handles: in a function returning `T?`, `x?` takes an optional and returns `None`; in one returning `Result<T, E>`, it takes a `Result<U, E>` with the same error type and returns the `Err`:

```go
func Copy(src ref string, dst ref string) Result<i64, io.Error> {
    in := os.Open(src)?                     // owned; closed by Drop on every exit path
    out := os.Create(dst)?
    n := io.Copy(mut out, mut in)?
    out.Close()?
    return Ok(n)
}

func (p mut Parser) advance() Result<(), ParseError> {
    p.cur = p.lex.Next()?
    return Ok(())
}
```

The two kinds do not mix. `?` on a result inside a function returning an optional has nowhere to put the error, and the compiler says so: `cannot use ? on a Result in a function returning an option`. To turn one into the other, use `??` and choose a value for the failing case, or `match` and build the other kind.

A result whose error type differs from the function's passes through `?` when the function's error type has a place for it: the one variant of its enum whose only field has the error's type, so `Unreadable(cause io.Error)` takes an `io.Error` with nothing written, or a trait value when the function returns `Result<T, Error>`. Anything else is refused, `? cannot pass on an error of type E1 from a function returning Result<T, E2>`, with the reason, and the conversion is a `MapErr` or a `match`. Chapter 17 has the rule and its refusals.

## What the compiler reports

| Message | Program |
|---|---|
| `nil is not defined; an absent value is None` | `nil-used` |
| `match is not exhaustive: missing Failed(_)` | `missing-arm` |
| `cannot use ? on a Result in a function returning an option` | `wrong-question-kind` |
