# 8. Option, Result, `?` and `??`

Status: implemented and checked (stage 1, task 1.4, `teak check`). The programs under `../../examples/core/errors/` named at the end show the errors.

Teak has no `nil`. A value that may be absent has an optional type, and an operation that may fail returns a result. Both are enums the language knows, so `match` takes them apart like any enum, and two operators, `?` and `??`, handle the common cases in one character or two.

## `T?`

`T?` is a value that is either `Some(x)` with an `x` of type `T`, or `None`:

```teak
struct Node {
    value    string
    parent   NodeID?               // None for the root
    children []NodeID
}

```

```teak
n := Node{value: value, parent: Some(parent), children: []NodeID{}}
root := Node{value: "root", parent: None, children: []NodeID{}}
```

`?` applies to the whole pointer or borrow before it and to the element inside a container: `*Node?` is an optional pointer, `ref string?` an optional borrow, `[]int?` a slice of optional ints. A borrow of an optional is written with parentheses, `mut (*Node?)`. Appendix A has the full rule.

`None` has no type of its own; it takes the type the context asks for, a field, a parameter, a result, or the other side of a comparison. Written where nothing asks for a type, it is an error: `cannot tell the type of None here`. `Some(x)` takes the type of `x`, or the context's when it has one, so `Some(ref n.value)` is a `ref string?` where that is wanted.

The methods on an optional are `Expect(msg)`, which yields the value or stops the program with the message, `Unwrap()`, `UnwrapOr(v)`, `IsSome()` and `IsNone()`:

```teak
text := os.ReadToString("input.txt").Expect("read input")
```

## `Result<T, E>`

A `Result<T, E>` is either `Ok(x)` with an `x` of type `T` or `Err(e)` with an `e` of type `E`. The error type is any type the program chooses, usually an enum with one variant per thing that can go wrong, or the predeclared `error`:

```teak
enum ConfigError {
    Unreadable(path string, cause error)
    BadValue(key string, text string)
    Missing(key string)
}

func Load(path string) Result<Config, ConfigError> { ... }
func Copy(src ref string, dst ref string) Result<i64, error> { ... }
```

`Ok(x)` and `Err(e)` take their full type from the context, since the argument says nothing about the other half; writing `x := Ok(1)` with nothing asking for a type is an error. A function that returns a result and has nothing to return on success returns `Result<(), E>` and writes `return Ok(())`. The methods are the option's, under the names `Expect`, `Unwrap`, `UnwrapOr`, `IsOk` and `IsErr`.

## Taking them apart

`match` handles both as the enums they are, exhaustively:

```teak
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

```teak
if parent := t.nodes[id].parent {
    t.nodes[parent].children.RemoveValue(id)
}

if (k, v) := line.SplitOnce("=") {
    kv.Insert(k.Trim().Clone(), v.Trim().Clone())
}
```

The left side may be a name, a tuple of names, or a variant pattern. `if x := opt {` binds; `if x := f(); cond {` with a `;` is an initializer and a condition.

## `??`: a value or an exit

`x ?? else` yields the value inside `x` when it is `Some` or `Ok`, and otherwise evaluates `else`. The else is an expression of the inside's type, or a `return`, `break` or `continue`, or a block ending in one:

```teak
n := take(mut s.head) ?? return None
text := kv.Get(ref key) ?? return Err(Missing(key))   // key moves only if the else runs
at := cur ?? break
n := cur ?? { *cur = Some(&Node{key: k, val: v, left: None, right: None}); return }
```

`??` sits below every other operator and groups to the right: `a ?? b == c` is `a ?? (b == c)`. Through a borrow it yields the same borrow of the inside, so `cur ?? ...` on a `mut (*Node?)`, a mutable borrow of an optional pointer, is a `mut *Node`, which is what walking a tree needs (chapter 6).

## `?`: pass the failure up

`x?` inside a function yields the value inside `x` and returns early with the absent or failed case when there is none. The function's result type says which kind `?` handles: in a function returning `T?`, `x?` takes an optional and returns `None`; in one returning `Result<T, E>`, it takes a `Result<U, E>` with the same error type and returns the `Err`:

```teak
func Copy(src ref string, dst ref string) Result<i64, error> {
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

The two kinds do not mix. `?` on a result inside a function returning an optional has nowhere to put the error, and the compiler says so: `cannot use ? on a Result in a function returning an option`. To turn one into the other, use `??` and choose a value for the failing case, or `match` and build the other kind. The same applies to a result whose error type differs from the function's: `? cannot pass on an error of type E1 from a function returning Result<T, E2>`. Converting between error types is a `match` or a method today; an automatic conversion through a trait is a question for stage 2.

## What the compiler reports

| Message | Program |
|---|---|
| `nil is not defined; an absent value is None` | `nil-used` |
| `match is not exhaustive: missing Failed(_)` | `missing-arm` |
| `cannot use ? on a Result in a function returning an option` | `wrong-question-kind` |
