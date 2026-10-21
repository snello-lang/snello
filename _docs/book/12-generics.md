# 12. Generics

Status: generic types, functions and methods are checked and compiled (stage 2, task 2.1), with their type arguments inferred at calls, variants and struct literals; `examples/run/generics.teak` runs them. Bounds are traits, checked at every use (stage 2, task 2.2; chapter 11). The declarations of `List` and `Map` are cut from `examples/08-generics.teak`; the other samples are the checker's own cases in `compiler/types/generic_test.go`, until the programs that use them run.

A generic type is written once for any element type. Its type parameters follow its name in angle brackets, and its fields use them as types:

```teak
struct List<T> {
    items []T
}
```

`List` on its own is not a type; `List<int>` and `List<string>` are. Each use names the type arguments, one per parameter, and a value is built as for any struct:

```teak
l := List<int>{}
```

Two uses with the same arguments are the same type, and uses with different arguments are different types: a `List<string>` cannot be passed where a `List<int>` is wanted, and the compiler's message names both.

## Methods

A method of a generic type names the type's parameters in its receiver, and uses them in its signature and body:

```teak
func (l mut List<T>) Push(v T)         { l.items.Push(v) }
func (l ref List<T>) Len() int         { return len(l.items) }
func (l ref List<T>) Get(i int) ref T? {
    if i < len(l.items) { return Some(ref l.items[i]) }
    return None
}
```

The receiver's arguments must be names: they declare the parameters for the method, which may name them differently from the declaration. A type in their place, `func (l ref List<int>) Sum() int`, is refused; a method belongs to every `List`, not to one of them.

Called on a `List<int>`, `Push` takes an `int` and `Get` returns a `ref int?`: the method's types are the declaration's with the arguments in place of the parameters.

## What a type parameter can do

Inside the declaration, a value of type `T` is an owned value the compiler knows nothing else about. It moves when it is passed or assigned, as a `string` does, so a method cannot use it twice:

```teak
func (l mut List<T>) Twice(v T) { l.Push(v); l.Push(v) }   // ERROR "use of moved value v"
```

A bound after the parameter's name says more. `T: Copy` promises that every argument is a `Copy` type, so a value of `T` copies instead of moving:

```teak
struct Copied<T: Copy> { v T }
```

Any trait is a bound, `T: Shape`, and gives a value of `T` the trait's methods; several are joined with `+`. Chapter 11 describes bounds and what a call must satisfy.

An instance is `Copy` when its fields are, as any struct is: a `Pair<int, bool>` copies, a `List<string>` moves. A type argument cannot be a borrow, `List<ref string>`, nor a struct that holds one, since the list would then store it (chapter 6). A generic function that only borrows values of a type parameter, `Fprintln<W: io.Writer>(w mut W, ...)`, is the exception: its instance keeps nothing of `W`, so a `bufio.Writer`, which borrows the file under it, may be passed to it. A `mut` parameter of the type must be the only parameter of that type, since through it a value of another could be kept: `Into<T>(dst mut T, v ref T)` does not qualify.

## Generic enums

An enum may be generic too, and may refer to itself through an owned pointer:

```teak
enum Tree<T> { Leaf(v T); Branch(left *Tree<T>, right *Tree<T>) }
```

A `match` on a `Tree<string>` binds `v` as a `string`, and must cover every variant as for any enum. A variant finds its type arguments the way a generic function does, below: `Leaf(1)` is a `Tree<int>`, and `var t Tree<f64> = Leaf(1)` makes the `1` an `f64`. A variant without fields has only its context to go by, so `o := Nothing` is refused, while `var o Opt<int> = Nothing` is fine.

A generic type may refer to itself only with arguments that do not grow. `Tree<T>` inside `Tree` is fine; `struct S<T> { s *S<S<T>>? }` is refused, since an `S<int>` would need an `S<S<int>>`, which would need an `S<S<S<int>>>`, and each is a type of its own when the program is compiled. The same holds for two types that refer to each other, and for a function that calls itself, or another that calls it back, with a growing argument: `Deep(W{v: x}, n - 1)` inside `Deep<T>` is refused the same way.

## How generics compile

Each use of a generic with its own type arguments becomes a type or function of its own in the compiled program: `List<int>` and `List<string>` are two structs, and `Map` called with `int` and `string` is one function. A generic body is compiled once per such instance, with the arguments in place, so a call of a generic function costs what a call of a plain one does, and a `List<int>` is laid out as if it had been written by hand. The price is paid at compile time, once per instance, which is why an instance chain without end is refused.

## Generic functions

A function takes type parameters the way a type does, after its name:

```teak
func Map<T, U>(l List<T>, f func(T) U) List<U> {
    out := List<U>{}
    for _, v := range l.items { out.Push(f(v)) }    // ranging over an owned slice moves
    return out
}
```

A call rarely names the type arguments. They are found from the arguments: in `Map(l, func(x int) int { return x * 2 })`, `l` is a `List<int>`, so `T` is `int`, and the function literal returns an `int`, so `U` is too. When the arguments do not say, the type the result is wanted as does, so `var e List<int> = Empty()` works. Otherwise they are written after the name, all of them or the first few, `Empty<int>()`, and the compiler names the one it could not find:

```teak
e := Empty()                          // ERROR "cannot infer the type parameter T of Empty: give it, Empty<...>"
```

A literal waits for the other arguments before it takes a type, so with `xs` a `[]f64`, the `1` in `Put(1, mut xs)` is an `f64`.

A generic call moves and lends as any call does. `Map` takes its list by value, so the list is gone after the call; `Filter(l, keep)` with `keep func(ref T) bool` lends each element to `keep`. A function returning a borrow follows the same rule as any other: `First<T>(xs ref []T) ref T?` returns a borrow of `xs`, and `xs` stays lent while the result is used.

A struct literal finds its type arguments the same way, from its context and then its fields: `Pair{a: 1, b: "x"}` is a `Pair<int, string>`.

A method may have type parameters of its own, after its receiver's, whether its type is generic or not:

```teak
func (l ref List<T>) Conv<U>(f func(ref T) U) List<U>
```

`l.Conv(func(x ref int) string { ... })` finds `U` from the function literal, as a call of `Map` does. A generic function or method cannot be used as a value without being called, since it is not one function until its type arguments are known.
