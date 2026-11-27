# 22. Variadic functions

A function's last parameter may take any number of arguments. It comes in two forms, and the form says what happens to the arguments, as every parameter says it: `...T` takes them, `...ref T` borrows them for the call.

```go
func Sum(xs ...int) int {
    total := 0
    for _, x := range xs {
        total += x
    }

    return total
}

func Join(sep string, parts ...ref string) string { ... }

Sum()                     // 0
Sum(1, 2, 3)              // 6
Sum(nums...)              // the slice itself
Join(", ", ref a, b)      // each lent for the call
Join(", ", words...)      // the slice lent
```

`compiler/emit/internal/backendtest/testdata/variadic.snello` is the program this chapter is cut from, run through both backends in release and debug builds.

## Why it is there

A function such as a sum, a join or a logger takes as many values as its caller has. Without a variadic parameter, every call builds a slice by hand, `Sum([]int{1, 2, 3})`, which costs an allocation and says nothing about who owns the values. The two forms make the call short and say what happens to the arguments, and the checker holds both sides to it:

- **Every argument is checked against its type.** C's variadic functions check none: `printf("%s\n", 42)` compiles with a warning and crashes when it runs, and a function that reads one argument more than it was given reads whatever lies on the stack. In Snello, an argument of the wrong type is a compile error, and the function sees exactly the arguments it was passed.
- **No hidden sharing.** In Go, `scale(s...)` hands the function the caller's own array, so a function that writes `xs[0] *= 10` changes the caller's `s` behind its back. With `...T` the slice moves into the function, so the caller cannot use it after the call and no one sees a change made to it; with `...ref T` the function borrows it and cannot change it at all.
- **No borrow outlives its owner.** A `...ref T` argument is lent for the call alone: the function cannot return it or store it, so no pointer into the caller's frame is left dangling after the call.
- **No allocation.** The arguments are laid out on the caller's stack, so a call costs what a call with fixed parameters costs, and a variadic function is no reason to avoid one on a hot path.

## `...T`: the function takes the arguments

Inside the function, `xs ...T` is an owned `[]T`: a slice made for the call, the arguments moved into it in order. The function ranges over it, indexes it, keeps it or passes it on, and drops it at its end like any slice it owns, dropping the elements with it.

An argument that is not `Copy` moves, as one passed to a parameter by value does, so the caller cannot use it after the call:

```go
func keep(parts ...string) {}

s := "a".Clone()
keep(s, "b".Clone())
print(s)                  // use of moved value s
```

Pass a clone, `keep(s.Clone())`, or take the arguments as `...ref T`.

The slice costs nothing when the function only reads it: the caller lays the arguments out on its own stack, and a function that ranges over its slice, indexes it, changes its elements or lends it uses them there, so `Sum(1, 2, 3)` allocates nothing. A function that keeps the slice, by returning it, storing it or passing it on with `...`, gives it a buffer of its own when it starts, and one that pushes onto it moves it to one then; either costs the allocation a slice literal would.

Each argument is an element of the function's slice, so a closure that borrows a local is none, as it is no element of a slice literal, whether or not the function keeps the slice:

```go
func run(fs ...func()) {}

w := "w".Clone()
run(func() { print(w) })  // closure captures w by borrow and cannot be stored
```

Give the closure what it needs by value, or take it as a `func()` parameter, which the compiler follows.

## `...ref T`: the function borrows them

Inside the function, `parts ...ref string` is a `ref []string` of the arguments, each lent for the call alone. A caller lends an owned value without writing `ref`, as to any `ref` parameter, and writes `ref` for a value it builds there, `ref made()`, which is dropped when the call's statement ends. While the call runs, the arguments' owners are frozen, as for any borrow.

The caller lays the arguments' values side by side in an array of its own stack frame and passes a view of it: no allocation, and nothing is dropped twice, since the values belong to their owners. That array lives for the call alone, so the function returns no borrow of the arguments:

```go
func first(xs ...ref string) ref string {
    return xs[0]   // cannot return a borrow of xs: the arguments of a variadic ...ref parameter are lent for the call alone
}
```

The same holds inside a closure in the function, which cannot return a borrow of `xs` either. Return a copy, `xs[0].Clone()`, or take the value returned as a `ref` parameter of its own: since `xs` is never a returned borrow's source, a function returning one borrow may take it beside a `...ref` parameter, `func pick(d ref string, xs ...ref string) ref string`.

There is no `...mut T`: a slice cannot hold several `mut` borrows apart. A function that changes several values takes a `mut []T`.

## Passing a slice on

`...` after a call's last argument passes a slice as the variadic parameter, as the only argument after the fixed ones:

- for `...T` the slice is a `[]T`, and it moves in: the function owns the caller's slice, and nothing is copied;
- for `...ref T` the slice is lent, or is a `ref []T` already, so one variadic function passes its arguments on to another for nothing:

```go
func Bracket(parts ...ref string) string { return "[" + Join("|", parts...) + "]" }
```

`one(xs...)` on a function with no variadic parameter is refused, and so are more arguments before `...` than the fixed ones, `sum(1, xs...)` for `sum(xs ...int)`. A built-in, `print(xs...)`, a variant, `Some(xs...)`, a conversion and the library's printing functions take no slice either.

## Types, generics, methods and values

A function's type writes the form, `func(...int) int`, `func(string, ...ref string)`, and is a type of its own: `Sum` is not a `func([]int) int`. A variadic function is a value like any other, `add := Sum; add(7, 8)`, and a closure may be variadic, `func(xs ...int) int { ... }`.

A generic element is inferred from every argument: `Count(1, 2)` makes `T` an `int`, and `Count(1, "a")` is refused. A call with no argument gives nothing to infer from and writes the type, `Count<int>()`.

A method may be variadic, and so may a trait's: an implementing type's method takes the same form, and a call through a trait value or a bound passes the arguments as a direct call does, none at all included.

A spawned call builds the slice at the spawn, where it evaluates every argument; a `...ref T` argument cannot cross to another task, as no borrow can. A deferred call evaluates its arguments when the `defer` runs and builds the slice when the call does, so it holds what a `...ref T` argument lends until the function returns, as it holds any borrowed argument: the borrowed variable must live that long and is not moved before, and a temporary is refused, `defer show(ref made())`, since it is gone by then.

An enum variant's field is not variadic: it holds one value, a slice for many.

## The library's `...ref any`

`fmt.Println` and its kin are declared `func Println(args ...ref any)`. That form is the library's alone: the compiler writes those functions itself and prints each argument by its own type, so they take no slice. A program's variadic parameter names its type, `...T` or `...ref T`.

## Memory safe, no collector

`...T` moves each argument into a slice the function owns and drops, as passing a slice literal by value would. `...ref T` lends each argument as a `ref` parameter does, its owner frozen for the call, and nothing of it outlives the call, since no borrow of it is returned and none can be stored. Nothing is collected.

## For maintainers

- **The design.** `_docs/design/00-syntax-conventions.md`, section "Variadic functions".
- **The parser.** `parseParams` in `compiler/parser/types.go` reads `...` before the last parameter's type into `Param.Dots`; `parseCall` in `expr.go` reads `...` after the last argument into `CallExpr.Ellipsis`, and refuses it before another argument.
- **The parameter.** `params` and `variadicType` in `compiler/types/typexpr.go` give `...T` the type `[]T` and `...ref T` the type `ref []T`, mark the signature variadic, keep `...ref any` for the library, and refuse `...mut T` and a variadic variant field. `VariadicParam` in `types.go` tells the forms apart for the checker and the backends, and `signatureString` writes them. Signature identity, `Identical` in `predicates.go`, compares the variadic mark, and a trait's required method keeps it, `trait.go`.
- **The call.** `args` and `genericCall` in `call.go` check each argument past the fixed ones against the element, `T` or `ref T`, made once per call by `variadicArg`; `argSlot` says what each argument is checked against, and whether it is packed into the function's own slice, which `argEffects` treats as stored, as a slice literal's element. After `...` the one argument goes against the parameter's slice. `argCount` checks the number of arguments and `...`, and `noSpread` refuses `...` in a call of a built-in, a variant or a conversion. `deferStmt` in `stmt.go` holds a deferred call's variadic arguments as their elements, or the slice after `...`.
- **The `...ref` parameter.** `params` marks its variable `lent` in `object.go`; `sourcePlace`, `followReturn` and `sourceParam` in `escape.go` refuse a returned borrow of it, from the function or a closure in it, and `checkSources` and `borrowedParam` leave it out of the returned borrow's source.
- **The backends.** `callOperands` in `compiler/emit/c/expr.go` and in `compiler/emit/llvm/call.go` evaluates every argument in order through `operands`, each packed one to its element's type, a lent one as a `ref`, and then `variadicValue` makes the parameter's value from an array in the caller's frame, in C an array declared where the call is, in the IR a slot of the function's frame: a slice of capacity `SN_CALLER` for `...T`, a slice over the values for `...ref T`. `SN_CALLER` is in `runtime/c/snello.h`: `sn_slice_free` frees no such buffer, `sn_slice_grow` moves it to a buffer of its own, and `sn_slice_unframe` does that alone; `SN_SLICE_FREE`, and the same test in the IR's slice drop, call the runtime only for a slice with a buffer of its own. `FramedVariadic` in `compiler/emit/lower/lower.go` decides whether a function's `...T` slice may stay in the caller's frame, from the checker's moves and captures, and the backends' `function` gives it a buffer when it starts otherwise; `unframeLast` does it for a spawn. Every call path passes whether the call spreads a slice: direct calls, methods, trait values and bounds, function values, `defer` and `spawn`.
- **The tests.** `TestVariadicCalls` in `compiler/types/call_test.go` and `TestVariadicParams` and `TestTypeExprErrors` in `typexpr_test.go`; `compiler/types/testdata/errors/variadic-moved.snello`, `variadic-lent.snello`, `variadic-spread.snello`, `variadic-deferred.snello` and `variadic-kept.snello`, each refusal with its message; the parser's `TestExprs` and `TestExprErrors`; and `variadic.snello` through both backends.
