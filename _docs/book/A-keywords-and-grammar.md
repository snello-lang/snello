# Appendix A. Keywords and grammar notes

Status: implemented (stage 0, `teak parse`). Grows when a stage adds syntax.

## Keywords

Thirty-one words are reserved and cannot be used as names.

| | | | | |
|---|---|---|---|---|
| `break` | `case` | `chan` | `const` | `continue` |
| `default` | `defer` | `else` | `enum` | `for` |
| `func` | `if` | `impl` | `import` | `map` |
| `match` | `mut` | `package` | `range` | `ref` |
| `return` | `select` | `Self` | `spawn` | `struct` |
| `super` | `switch` | `trait` | `type` | `var` |

`Self` and `super` have meaning only inside a trait (chapter 11). `None`, `Some`, `Ok`, `Err`, `true`, `false`, the type names and the built-in functions are predeclared names, not keywords: they are visible in every file and can be shadowed, though nothing good comes of it.

## Lines

A statement ends at the end of its line when the line ends in a name, a literal, `)`, `]`, `}`, `?`, `>` or `>>`, or one of `return`, `break` and `continue`. Otherwise the statement continues on the next line, so a long call breaks after a comma or an opening bracket, and a method chain breaks after the dot:

```teak
return strconv.ParseFloat(text).
    Map(func(v f64) Token { return Num(v) })
```

Because a line may end in `>`, a comparison or a shift keeps its right operand on the same line as the operator.

## Where things bind

- `?` applies to the whole pointer or borrow before it and to the element inside a container: `*Node?` is an optional pointer, `ref T?` an optional borrow, `[]int?` a slice of optional ints, `map[K]V?` a map whose values are optional. An optional container is written with parentheses: `([]int)?`.
- Postfix `?` on an expression binds tighter than any prefix operator: `-x?` is `-(x?)`.
- `??` sits below every other operator and groups to the right: `a ?? b == c` is `a ?? (b == c)`, and `a ?? b ?? c` is `a ?? (b ?? c)`. Its right side is an expression, a block, or a `return`, `break` or `continue`.
- In a type, `<` always opens a type argument list: `arena.Index<Node>`. In an expression, `<` is less-than unless the tokens after it form a type list closed by `>` directly followed by `(` or `{`: `arena.New<Node>()`, `Stack<string>{}`. Avoid `a < b > (c)`, which reads as a call with type arguments.
- A `{` directly after a name opens a composite literal, except in the condition of an `if`, `for`, `switch` or `match`, where it opens the body. To compare against a literal there, parenthesize it: `if x == (T{}) {`. A written-out type is never ambiguous: `for _, k := range []int{5, 3} {`.
- `if x := opt {` is a binding on an optional; `if x := f(); x > 0 {` is an initializer and a condition. The `;` tells them apart.

## Declarations

- `struct Name impl A, B { fields }`, `enum Name { variants }`, `trait Name { ... }`, `type Name T`, `func`, `var`, `const`. The `impl` list comes after the name.
- `var` and `const` declare one name list per keyword: `var x, y int = 1, 2`.
- Parameter names and types: in `func(from, to NodeID, w int)` the names before a type share it; in `func(T, U) V` the identifiers are types. An identifier followed by a comma is decided by what ends its group.
- Array lengths are integer literals.
- A match arm is `pattern [if guard] => body`, ended by a comma or a newline. A `return` in an arm takes one expression; several values are returned as a tuple.
- `spawn` and `defer` take a call. `for range ch {` ranges with no variable.
- One name on the left of `:=` or `=` takes one expression on the right; a list takes a list, `a, b := b, a`.
- An `if` used as an expression has an `else`.
