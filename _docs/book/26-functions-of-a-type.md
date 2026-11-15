# 26. Functions of a type

Some functions belong to a type but have no value of it to work on: a constructor that makes one, `Point.Of(1, 2)`, a parser that turns text into one, `Duration.Parse("5s")`, a decoder that turns JSON into one. A method cannot be one of them, since a method is called on a value that already exists, and making that value is the whole job. Snello lets a type have such functions, written with **the type's name, a dot and the function's name**, as they are called:

```go
struct Point {
	x int
	y int
}

func Point.Of(x int, y int) Point { return Point{x: x, y: y} }

func Point.Diag(n int) Point { return Point.Of(n, n) }

p := Point.Diag(5)                        // {5 5}
```

A function of a type is an ordinary function, checked and compiled as any is and called directly. It is not a constructor in the sense of other languages: Snello still makes a value with a literal, `Point{x: 1, y: 2}`, and nothing runs on its own when a value is made. The function only lives under the type's name, where a reader finds it, rather than in the package beside every other type's, `NewPoint`, `ParsePoint`. `compiler/emit/internal/backendtest/testdata/type-funcs.snello` is the program this chapter is cut from, built through both backends in release and debug builds.

## Why it is there

Go writes each of these functions as a package function named after the type: `time.ParseDuration`, `strconv.ParseInt`, `NewPoint`. That serves code that knows the type, but not generic code. A JSON decoder written once for every type has to ask "make me a `T` from this text", and Go has no way to say "the parser of `T`". Go's `encoding/json` works around it by making a zero value first and calling a method on it, `var t T; t.UnmarshalJSON(b)`, which Snello cannot do: many types have no zero value, a file, a lock, a type with a `Drop`, a struct whose fields have no defaults (chapter 18).

A function of a type is what fills that gap. Because it belongs to the type, a trait can require one, and a generic function then calls the one of the type it was given:

```go
trait Parse { func Parse(s ref string) Self? }

struct Q impl Parse { n int }

func Q.Parse(s ref string) Q? { return Some(Q{n: len(s)}) }

func parsed<T: Parse>(s ref string) T? { return T.Parse(s) }   // the parser of T, whatever T is

parsed<Q>("abc")                          // Some(Q{n: 3})
```

This is how `encoding/json` lets a type read its own JSON: a type that lists `json.Unmarshaler` writes `func Celsius.UnmarshalJSON(text ref string) Result<Celsius, json.Error>`, and json, compiled for `Celsius`, calls `T.UnmarshalJSON(raw)`. Rust has the same, associated functions, `T::parse(s)`, which a trait may require; C# 11 added static abstract interface members for the same reason, and Swift's protocols require static functions.

## How it differs from a plain function

Where the type is known, it does not: `Point.Of(1, 2)` and a package function `NewPoint(1, 2)` compile to the same direct call, at the same cost. The only difference there is where the name lives, under the type or beside every other function of the package.

The difference is in generic code. A trait can require a function of the type, `func Parse(s ref string) Self?`, and a generic function then asks for the one of the type it was given, `T.Parse(s)`. A plain function cannot be required by a trait, and there is no way to name "the `NewX` of whatever `T` is". This is the one thing a function of a type does that a plain function cannot, and the reason it is in the language: `encoding/json`, written once for every type, makes a `Celsius` from text by calling `T.UnmarshalJSON(raw)`, where no value exists yet to call a method on.

So write a plain function when nothing generic needs it, and a function of the type when a trait requires it, or when it belongs to the type and you want it found there.

## When to use which

| Write | When | Example |
|---|---|---|
| a method, `func (p ref Point) Dist(q ref Point) f64` | it works on a value that exists | `p.Dist(q)` |
| a function of the type, `func Point.Of(...) Point` | it makes a value of the type, or belongs to the type and needs no value of it; or a trait requires it | `Point.Of(1, 2)`, `Duration.Parse("5s")` |
| a package function, `func Join(parts ref []string) string` | it belongs to no one type | `strings.Join(parts)` |

A struct literal stays the way to make a value from its fields; a function of the type is for a value made with work: checked input, a parse, a default worked out, a value from another package whose fields it keeps to itself, as `json.Error.At(0, "bad date")` makes an error whose fields `encoding/json` does not export.

## Writing one

- **On a declared type of the package, of any kind.** `func Point.Of(...)` belongs to the struct `Point`, and is declared in `Point`'s package as a method is; an enum has them, `func Shape.Unit() Shape`, and so does a type declared over another, `func Celsius.Parse(text ref string) Celsius?` for `type Celsius f64`. Another package's type, and a predeclared type, `func int.Of()`, take none, as they take no methods. From another package one is called through the type, `geo.Point.Of(1, 2)`, when it is exported.
- **On a generic type.** The type's parameters are written after its name, `func Pair<A, B>.Of(a A, b B) Pair<A, B>`, and the call names an instance, `Pair<int, string>.Of(1, "a")`.
- **With type parameters of its own.** After the function's name, `func Point.Twice<T: Clone>(v T) (T, T)`, called `Point.Twice<string>(s)`.
- **As a value.** `f := Point.Of` is a function value, as a package function's name is.
- **One name, one member.** A type's functions and methods share names: `Of` is a method or a function of `Point`, not both.

## In a trait

A trait declares a function without a receiver, `func Parse(s ref string) Self?`, with `Self` the type that implements it, and that type writes it as its own, `func Q.Parse(...)`. It is called on a type parameter bounded by the trait, `T.Parse(s)`, or matched by an arm naming the trait (chapter 24). Inside the trait, a default method calls it on `Self`, the type that inherits the default:

```go
trait Named {
	func Count() int
	func (s ref Self) Twice() int { return Self.Count() * 2 }
}

struct A impl Named { n int }

func A.Count() int { return 3 }

// A{n: 1}.Twice() is 6, and so it is through a value of Named, var x Named = A{n: 1}
```

A trait's function has no default body in this version, and a trait value has no type to call one on.

## What is refused

| Written | Refused with |
|---|---|
| `p.Of(1)`, a function of `P` called on a value | `Of is a function of P, which takes no value of it: call P.Of(...)` |
| `P.X()`, a method called on the type | `X is a method of P: call it on a value of it` |
| `P.Nope()` | `P has no function Nope` |
| `func (p ref P) Of()` beside `func P.Of()` | `P.Of redeclared` |
| `func int.Of()` | `a function of a type is declared on a declared type` |
| `func (q ref Q) Parse(...)` for a trait requiring a function | `Q.Parse is a method, and Parse wants a function of the type` |
| `trait T { func F() int { return 1 } }` | `a trait's function has no default body` |
| `x.Count()`, `x` a value of a trait that requires `Count` | `cannot call Count through Named: it is a function of the type, which takes no value` |
| `v.Make()`, `v` a value of a parameter `T: Mk` | `Make is a function of T, which takes no value of it: call T.Make(...)` |
| `via<K>()`, the trait `K` given for a bound of itself | `the function K of K takes no value, and a trait value has no type to call it on` |
| `G.Count()`, `G` generic | `G is generic: name an instance, G<...>.Count` |
| `g := P.Twice`, a generic function of `P` not called | `cannot use the generic function Twice without calling it` |
| `func G<A: Clone>.Dup(...)` | `a function of a type names its type's parameters, whose bounds the type declares` |
| `E.A()`, `A` a variant of `E` | `E has no function A: A is a variant of it, written by its name, A` |
| `func Show.Default()`, `Show` a trait, generic or of the language, `Clone` | `Show is a trait: it lists the functions it requires, func Default(...), and each type that implements it declares its own` |
| `func Result<T, E>.Of()`, a type of the language or of another package | `cannot define new functions on non-local type Result` |
| `func G<int>.Of()`, a type argument where the type's parameter is named | `a function of a type names the type's parameters: G<A>` |
| `func G<A>.Of<A>(...)`, a function's own parameter named as its type's | `A is already a type parameter of G: name Of's own another way` |
| `Self.Count()` outside a trait | `Self is only defined inside a trait` |

## Memory safe, no collector

A function of a type takes and returns values by the rules every function does, and a call of one is a direct call to the type's own function, chosen when the program is compiled; nothing is looked up or kept when it runs.

## For maintainers

- **The design.** "Functions of a type" in `_docs/design/00-syntax-conventions.md`.
- **The syntax.** `parseFuncDecl` in `compiler/parser/decl.go` reads a name, its type parameters and a `.` as the type, `ast.FuncDecl.Of`, then the function's name and its own parameters. In an expression, `parseTypeArgsIfAny` in `expr.go` reads `Pair<int, string>` as an instance when its `>` is followed by `.`, `ast.TypeExpr`.
- **The check.** `resolveFuncDecl` in `compiler/types/resolve.go` opens the scope of the type's parameters as it does a receiver's, reads the type in that scope before the function's own parameters are opened, refuses an own parameter that would hide one of the type's, marks the function `Func.static`, and `refuseOwner` refuses a trait or a type of the language before the type is read, and `attachFunc` puts the function among its type's methods, recording the type's name. `typeFunc` in `expr.go` resolves `Point.Of` and `T.Parse`, the selector reading its left side as a type through `Checker.wantType`; a method call on a value refuses a function of the type. `traitFunc` in `trait.go` checks a trait's function; `checkSignature` refuses a method for a function and the reverse; `substFunc` keeps the flag through substitution. `Self.Count()` parses as a type, `ast.TypeExpr` of `ast.SelfType`, and in a default body `Self` is the trait's type parameter bounded by the trait, so it takes the path `T.Parse(s)` does. The reach walk in `reach.go` binds a generic type's parameters from the instance before the dot, `G<bool>.Kind`, as it binds a method's from its receiver, so what the function's body reaches is found for that instance.
- **The backends.** `lower.TypeFunc` in `compiler/emit/lower/typefunc.go` tells both backends that a selector names a function of a type, `Point.Of` or `T.Parse`, which takes no receiver, and `lower.TypeOwner` gives the type a declaration is a function of, whose parameters an instance of the function takes. The C backend's `staticCall` in `compiler/emit/c/expr.go` and the LLVM backend's in `compiler/emit/llvm/call.go` call that function, named as a method, `lower.MethodName`; `ownerOf` in each `mono.go` finds the type of a declaration, and `generic`, `request` and `writeInstance` treat a generic type's function as a generic type's method. `funcValue` takes the function's symbol, so a function of a type is a value too.
- **The tests.** The parser's `TestFuncOfType`; `TestTypeFuncs` in `compiler/types/resolve_test.go` and the trait cases in `trait_test.go`; `type-funcs.snello` and `trait-arms.snello` through both backends.
