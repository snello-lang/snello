# 21. Constants

A constant is a value the compiler computes and writes into the program. It is declared with `const`, at the top level of a file or inside a function, and an array's length is one too:

```go
const KB = 1 << 10
const MB = KB * KB
const Limit u8 = 3 * 80
const Greeting = "hello, " + Name
const Name = "snello"

var buffer [4 * KB]byte
```

`compiler/emit/internal/backendtest/testdata/constants.snello` is the program this chapter is cut from: every kind of constant, built through both backends and set beside the same arithmetic done at run time.

## Why it is there

A value that never changes belongs in one place, under a name, worked out once. Without constants it is a number copied by hand wherever it is needed, or a package variable that costs a load at every use and could be any value as far as the compiler knows. A constant gives the value a name, computes it while the program is compiled, and checks it there:

- **A value out of its type's range is a compile error.** `const Limit u8 = 300` is refused, `constant 300 overflows u8`. In C, `#define LIMIT 300` assigned to a `uint8_t` compiles with a warning and holds 44. Go refuses it too; Snello holds every constant to the same rule, a struct's field and an array's length included.
- **Division by zero, a lost fraction and a constant defined through itself are caught before the program runs**, each named in its message, not met as a crash or a wrong number in production.
- **One name, one value.** A limit, a size or a mask is changed in one place, and every use follows, the arrays it sizes among them: `var buffer [4 * KB]byte` grows when `KB` does.
- **No cost when the program runs.** The value is written into the program as a literal would be: nothing to compute at start-up, nothing to load, and the C compiler or LLVM sees a number it can fold further.

## What a constant is made of

A constant's value is a constant expression:

- literals: numbers, characters, `true` and `false`, strings;
- the names of other constants, of this package or another's, `math.MaxInt8`;
- the operators over numbers, `+ - * / %`, `& | ^ &^ << >>`, the unary `-` and `^`;
- the comparisons, `== != < <= > >=`, and `&& || !` over booleans;
- `+` joining strings;
- the conversions between number types, `f64(KB)`, `u8(Limit)`, `Celsius(100)` for a type declared over a number type;
- a string or a bool given another name of its kind, `Name("host")` for `type Name string`, which is how an attribute over a string is written (chapter 25);
- parentheses.

Nothing else is constant, since its value is known only when the program runs. A call, a variable, an element of a slice, `len` and a conversion of a number to a string, `string('A')`, are refused, each with its reason: the part that is not constant, or `constant 65 does not convert to string`:

```go
const n = size()        // size() is not constant
const m = count * 2     // count is not constant: count is a var
```

Such a value is a `var`, which takes any value and is made the first time it is used.

A constant is a number, a boolean or a string, or a type declared over one of them. A struct whose fields are constant expressions is a constant too, `const Origin = Point{x: 0, y: 0}`, and is read by its fields where it is used; it is a value only as a whole, so it is no part of another constant's expression, and an array or a slice is never a constant.

## Its type

A constant's literals take their type from the constant, as every literal takes its type from its context: `Limit` above is a `u8`, and `KB` is an `int`, since nothing says otherwise. A float literal is an `f64`, a character a `rune`, unless the constant gives another type: `const Comma byte = ','` is a `byte`, which `WriteByte` takes and a byte of a string compares with, as `encoding/json` names JSON's punctuation. Arithmetic is between values of one type, as everywhere, so `const MB i64 = KB * KB` is refused while `KB` is an `int`, `cannot use a value of type int as i64 in declaration`, and is written `i64(KB) * i64(KB)`.

## How it is computed

The compiler works the value out in the arithmetic of the constant's type, one operation at a time, and refuses a value the type cannot hold. Arithmetic on variables wraps at run time, but a constant is a number written down, and one written past its type is a mistake:

| Refused | The message |
|---|---|
| `const Narrow i32 = 1 << 40` | `constant 1099511627776 overflows i32` |
| `const Below u8 = 1 - 2` | `constant -1 overflows u8` |
| `const Halved = KB / 0`, `KB % 0` | `division by zero` |
| `const Back = 1 << -1` | `negative shift count -1` |
| `const Cut = int(2.5)` | `constant 2.5 truncated to int`: a conversion keeps the value |
| `const Byte = u8(KB)` | `constant 1024 overflows u8` |
| `const Far = 1e308 * 10.0` | `constant 1e308 * 10.0 overflows f64`: no constant is infinite |

An integer is computed exactly: a division truncates toward zero, a remainder has the sign of the number divided, `>>` keeps the sign, and `^` flips the bits of the type's width, so `^u8(0x0f)` is `240`. A float is computed as the processor computes it, rounded to `f64` or to `f32` after each operation, so `const Third f32 = 1.0 / 3.0` is the same `f32` as `f32(one) / f32(three)` on variables, to the bit, and `-0.0` stays a negative zero. An integer converted to a float is rounded to the nearest float. A comparison gives a `bool`.

Only a constant and an array length are computed by the compiler. Anything else is ordinary arithmetic, done when the program runs: `x := KB * KB` multiplies two `int`s at run time, and wraps if it must.

## Order

The constants at the top level of a package may be declared in any order and in any of its files: `Greeting` above uses `Name`, declared after it, and a constant may have a type declared after it, `const N Count = 4` before `type Count int`. The compiler computes each constant the first time something needs it. A constant that needs its own value, by itself or through others, is refused with the chain:

```go
const A = B + 1  // initialization cycle: A refers to B, B refers to A
const B = A * 2
```

A constant inside a function sees only what is declared before it, as a variable there does. `const _ = KB * 4` is checked like any other and written nowhere.

A package variable declared without a type takes the type of its value, which is known once its declaration has been checked, in the order of the files. `var twice = limit * 2` before `var limit = 3` is refused, `limit is used before its type is known: declare limit before this, or with its type`. Declare `limit` first, or with its type, `var limit int = 3`.

## Array lengths

An array's length is a constant expression of any integer type, at least zero:

```go
const Size = 3
var cells [Size]int
var wide [2 * Size + 1]byte
```

A length that is not constant is refused, `array length must be a non-negative integer constant: n is not constant`, and so is a negative one or a float.

## Memory safe, no collector

A constant is computed by the compiler and written into the program. Nothing is allocated, owned or freed for it, and nothing about it happens when the program runs.

## For maintainers

- **The design.** `_docs/design/00-syntax-conventions.md`, section "Constants", states the rules; the lowering note, `07-lowering.md`, says that the backends write a constant from its value.
- **The parser.** `parseSliceOrArrayType` in `compiler/parser/types.go` reads an array's length as any expression; the checker requires it to be constant.
- **The value.** `Constant` in `compiler/types/constant.go` holds a folded value: an integer as a `big.Int`, a float as the `float64` its type holds, an `f32` rounded to one, a bool or a string. `Const` in `object.go` keeps its constant's value, `Val()`, and `Info.Values` in `check.go` the value of each constant's value expression and of each field of a struct constant.
- **The check.** `constValue` checks a value: `notConstant` refuses what is not a constant expression before the expression is checked, the expression is checked as any is, with its types also written into `folding`, the value is held to the declared type by `assign`, and `fold` computes it, `foldUnary`, `foldBinary`, `foldInt`, `foldFloat` and `foldConversion`, each result held to its type by `represent`, which reports an overflow. Nothing is folded after the check reported an error, so none is reported twice, and `unreported` refuses a value that could not be folded when nothing has been reported, so no constant without a value reaches a backend. `arrayLen` does the same for a length.
- **Order.** `collectConsts` notes every top-level constant, a blank one among them, and `checkConsts` checks them after the types, in the order they are declared. A constant needed before its turn, by an array length while the types are resolved or by another constant, is checked then by `checkConst`, which enters the file that declares it and puts aside what is being checked; `constCycle` reports a constant met again while it is checked. `collectEarly` and `resolveEarly` in `resolve.go` resolve a type declared over another before its turn, for a constant's type, a conversion and a type declared over a later one. A local constant is checked by `localConsts`, from `declStmt` in `stmt.go`.
- **Package variables.** `collectUninferred` notes the variables whose type their value gives, and `ident` in `expr.go` refuses a use of one before its declaration is checked.
- **The backends.** `Folded` in `compiler/emit/lower/constant.go` writes a folded value as the literal a program would have written, with its types, so both backends write the value the checker computed and neither computes one. The LLVM backend writes every constant as a constant global, `varDecl` in `llvm.go` and `declStmt` in `stmt.go`; the C backend writes a top-level one as a `static const` in `varDecl` in `c.go` and one a function declares as a `static const` of the file in `localConsts` in `stmt.go`, which `ident` in `expr.go` names.
- **The tests.** `constant_test.go` holds the folding to run-time arithmetic, the values recorded and the ranges of the integer types; `check_test.go` the order across files and types, blank constants and array lengths; `compiler/types/testdata/errors/const-not-constant.snello`, `const-overflows.snello`, `const-cycle.snello` and `global-before-type.snello` each refusal with its message; `compiler/emit/lower/constant_test.go` the literals `Folded` writes; and `constants.snello` both backends in release and debug builds.
