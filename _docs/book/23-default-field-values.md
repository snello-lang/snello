# 23. Default field values

A struct field may declare a default after its type. A literal that leaves the field out takes it:

```go
struct Server {
    host    string = "localhost"
    port    int = 8080
    backlog int
    verbose bool = true
}

s := Server{backlog: 16}     // localhost, 8080, 16, true
t := Server{port: 9000}      // localhost, 9000, 0, true
```

A setting left out is then a value the type chose, not the zero Go would give: `verbose` above is `true` unless a literal says otherwise. This is how a Snello function takes settings. It takes them as such a struct, every setting named, in any order, any of them skipped. There are no default parameters, no overloads and no builder:

```go
f := os.OpenOptions{Write: true, Append: true, Create: true}.Open(ref path)?
```

`compiler/emit/internal/backendtest/testdata/defaults.snello` is the program this chapter is cut from, run through both backends in release and debug builds.

## Why it is there

A struct of settings is how a function takes many options, and every caller sets only the few it cares about. In Go the fields left out are zero, and zero has to mean "not set", which breaks down as soon as zero is also a setting someone wants. Default field values let the type say what a field left out holds, and remove the bugs that come with the zero:

- **Zero stays a value of its own.** In Go, does `Timeout: 0` mean no timeout, or the default? The package has to pick one and every caller has to know which. With `timeout int = 30`, a literal that leaves it out gets 30, and one that writes `timeout: 0` gets 0.
- **No fallbacks scattered through the code.** The Go pattern `if o.Port == 0 { port = 8080 }` is written wherever the setting is read, is easily forgotten in one of those places, and can disagree with itself. The default is written once, on the field.
- **A new field reaches every caller safely.** A field added to a settings struct gets its default at every existing call site, not a silent zero that may switch something off.
- **Settings are named.** `Open(path, true, false, true)` compiles just as well with two booleans swapped, and opens the file wrong. `os.OpenOptions{Write: true, Create: true}` names each setting, and any can be left out, in any order, with no overloads, default parameters or builder types to maintain.
- **No value skips the defaults.** A struct with a default has no zero value, so `var o Options`, `make([]Options, n)` and a map read of a missing key, each of which would make a value with zeros where the type declared defaults, are refused at compile time.
- **Settings that cannot go together are refused.** `os.OpenOptions` checks its fields when it opens the file: truncating a file opened for appending, or making one opened for reading alone, is an invalid argument, not a surprise on disk.

## A constant each

A default is a constant expression of the field's type, as a constant's value is (chapter 21): literals, other constants, operators and conversions between number types, and a struct literal of constant fields. A struct constant is no part of another constant's expression (chapter 21), so a default writes the literal, `at Point = Point{x: 5}`, not `at Point = Origin` for `const Origin = Point{x: 5}`.

```go
const Base = 8000

struct Options {
    port  int = Base + 80
    mode  Mode = 0o644          // a type declared over a number type
    ratio f64 = 1.0 / 4.0
    level int = -1
    at    Point = Point{x: 5}   // a struct constant; y takes Point's own default
}
```

The compiler computes each default once, when it checks the struct, and refuses one that is no constant or not of the field's type:

| Refused | The message |
|---|---|
| `n int = size()` | `size() is not constant` |
| `n int = count`, `count` a `var` | `count is not constant` |
| `n int = "four"` | `cannot use a value of type string as int in field default` |
| `w, h int = 1` | `a default for each field: 2 names but 1 default` |
| `item T = 0` in `struct Box<T>` | `a field of type T takes no default: no constant has every type a type parameter stands for` |
| `Point = Point{}`, an embedded field | `an embedded field takes no default` |
| `var name string = "x"` in a trait | `a trait field takes no default: the type that implements the trait gives the field its value` |

Fields declared together take one default each, `w, h f64 = 1.0, 2.0`, or none. A generic struct's other fields keep theirs: `struct Box<T> { item T; count int = 3 }` gives every `Box<string>` and `Box<f64>` a count of 3.

## Left out, the default

A keyed literal may leave out any field that declares a default. Each field it leaves out takes, in order of preference:

1. its default;
2. its zero value, when the field declares no default;
3. nothing: the literal is refused when the field's type has no zero value (chapter 18).

A positional literal may stop before the last fields, when every field it leaves out declares a default:

```go
struct Size {
	w int
	h int = 1
}

Size{3}         // w 3, h 1
Size{3, 4}      // w 3, h 4
Size{}          // w 0, h 1
```

A positional literal that leaves out a field without a default is refused, `a positional struct literal gives every field but the last ones that declare a default: Mid has 3 fields, not 1`. A keyed literal names each field once, `duplicate field w in a Size literal`.

A literal of another package's struct leaves out a field that is not exported when that field declares a default. It also leaves out one with no default when the struct would have a zero value but for its defaults: the package chose those values, as it chose the defaults. So `os.OpenOptions{Write: true}` builds anywhere.

## No zero value

A struct with a field that declares a default has no zero value. A zero would fill the fields with zeros, past the defaults the type declared: `var o os.OpenOptions` would open nothing for reading, while every literal reads. The places that would make such a zero are refused, as for any type without a zero value:

| Refused | The message | Write instead |
|---|---|---|
| `var o os.OpenOptions` | `cannot declare o without a value: os.OpenOptions declares defaults, which a zero value would skip; write o := os.OpenOptions{}` | `o := os.OpenOptions{}` |
| `make([]Server, n)` | `make cannot fill a []Server: Server has no zero value; use make([]Server, 0, n) and Push` | `make([]Server, 0, n)`, then `Push` |
| `print(m[k])`, the map's values `Server` | `cannot read m[k]: Server has no zero value for a key the map does not hold; use m.Get(k)` | `m.Get(k)` |
| `take(mut c.server)` | `cannot take c.server: Server has no zero value to leave behind; replace(mut c.server, value) leaves value in its place` | `replace(mut c.server, Server{})` |

`T{}` is the value with every default, and the message for a declaration offers it when it builds the value. A type that holds such a struct has no zero value either, as a type that holds a pointer has none. A literal of it names that field, since a field is never filled with another type's defaults: `Holds{d: Def{}}`, not `Holds{}`. `Server?`, `[]Server` and `map[string]Server` still have one, `None` and empty. A type declared over such a struct, `type Local Server`, shares its defaults, so the two convert to each other as any declared type and its underlying type do.

The rule only refuses. Every program the compiler accepted before defaults existed still means what it meant.

## What it costs

Nothing at run time. A literal is written with every field it leaves out set to the default's value, computed by the compiler, as a constant is written. In a package variable, `var global = Options{port: 9}`, that value is part of the program's data, as any constant literal is. Nothing runs to make a default, and nothing is allocated for one.

## Memory safe, no collector

A default is a constant. A string default is a literal that owns nothing, so a value that holds one drops it as it drops any string. Every field is set when the value is made, so no field is ever read before it holds a value of its type, and nothing is collected.

## For maintainers

- **The design.** `_docs/design/00-syntax-conventions.md`, section "Default field values".
- **The parser.** `parseField` in `compiler/parser/types.go` reads `= values` after a field's type into `Field.Defaults` in `compiler/ast/ast.go`, a `FieldDefaults` with one expression per name. It is a pointer, so a field without defaults is no larger than before.
- **The field.** `fields` in `compiler/types/typexpr.go` refuses, through `defaultsFit`, a default of a trait's field, of an embedded field, of a field whose type holds a type parameter, and a count that is not one per name. It puts each default in the package's table, `Package.defaults` in `check.go`, which the field's `Var` names by an index, `def` in `object.go`: an `int32` that fits in the bytes the variable's flags leave free, so no variable, of the many the checker makes, grows, and `TestDefaultSizes` in `object_test.go` holds it there. `Var.Default` and `Struct.Default(i)` in `types.go` read it; `substVar` in `generic.go` keeps the index in an instance of a generic struct, whose field reads the generic struct's default. It notes each in `Checker.fieldDefaults` for the check.
- **The value.** `checkDefaults` in `constant.go` checks each noted default after the package's constants, in the file that declares it, through `constValue`, the check a constant's value has, as a `field default`, so `Info.Values` holds its folded value. A default that is refused is dropped from its struct, so the field is checked as one that declares none and the one mistake is the one error.
- **The literal.** `keyed` in `literal.go` lets a positional literal stop before the last fields when `Struct.defaultsFrom` says every field left out declares a default, and rewrites it to the keyed form; `structLit` refuses a field named twice, `keyedBefore`; `zeroFields` in `zero.go` passes over a field left out that declares a default.
- **No zero value.** `zeroIn` in `predicates.go` takes whether defaults count, `Struct.hasDefaults`: `hasZero` counts them, so a struct with a default, or a type that holds one, has no zero value, and every refusal of a zero value follows; `hasOwnZero` does not, and is what the rule for another package's hidden fields asks. `filledByDefaults` in `zero.go` says when a declaration's message offers `T{}`, and `zeroConversion` asks `hasOwnZero` too, since a type declared over such a struct shares its defaults.
- **The backends.** `LeftOut` in `compiler/emit/lower/lower.go` gives the indices of the fields a literal leaves out that declare a default, in the struct's order. `compositeLit` in `compiler/emit/c/expr.go` and the struct case in `compiler/emit/llvm/expr.go` write each with `lower.Folded` of its default, and `staticValue` in `c/c.go` and `llvm/llvm.go` does the same for a package variable's initial value.
- **The library.** `OpenOptions` in `std/os/os.snello`, with `Open` mapping the fields to `OpenFile`'s flags and refusing the combinations that make no sense as an invalid argument; `TestOpenOptions` in `os_test.snello`.
- **The tests.** The parser's `TestDecls` and `TestDeclErrors`; `TestDefaultLiterals` in `compiler/types/literal_test.go`, `TestStructDefault` in `types_test.go`, `TestVarDefault` and `TestDefaultSizes` in `object_test.go`, `TestFoldDefaults` and `TestRefusedDefault` in `constant_test.go`, `TestHasZero` and `TestHasOwnZero` in `predicates_test.go`, `TestZeroRefusals`, `TestZeroAcrossPackages` and `TestDefaultsAcrossPackages` in `zero_test.go`; `compiler/types/testdata/errors/default-not-constant.snello` and `default-no-zero.snello`, each refusal with its message; `TestLeftOut` in `compiler/emit/lower/lower_test.go`; and `defaults.snello` through both backends.
