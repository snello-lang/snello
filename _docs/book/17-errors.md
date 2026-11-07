# 17. Errors

Status: the `Error` trait with `Error()` and `Source()`, the derived message, `?` passing an error into the one variant that holds its type or into a trait value, `%+v` printing the chain, and a trait value of `Error` or `fmt.Stringer` printed as its message, checked and compiled through both backends (stage 9, task 9.4). `examples/run/errors.snello` runs everything in this chapter, `examples/09-errors.snello` is a program built on it, and the programs under `../../examples/core/errors/` named at the end show the refusals.

An error in Snello is a value. A function that can fail returns a `Result<T, E>`, chapter 8, and `E` is a type the program declares, an enum with one variant per thing that can go wrong, or the trait `Error` itself when the caller only needs a message. This chapter is about `E`: what makes a type an error, what the language writes for it, how an error passes up through `?` when the types differ, and how a chain of causes is kept and printed.

## The `Error` trait

A type is an error when it lists the standard trait `Error`:

```snello
trait Error {
    func (s ref Self) Error() string
    func (s ref Self) Source() Error?
}
```

`Error()` is the message. `Source()` is the error this one wraps, or `None`. Both are written for you when you write nothing, so the shortest error type is one line:

```snello
enum AppError impl Error, Clone {
    Read(cause io.Error)
    Parse(cause strconv.Error)
    Missing(key string)
    Empty
}
```

The trait's name is also a type, chapter 11: a value of type `Error` holds any error, and `Result<T, Error>` is a function's way of saying it fails with something that has a message, without naming which.

## The message

A type that lists `Error` and writes no `Error()` has it derived: the message is the value as `%v` prints it, chapter 4's form for an enum, a struct or a type declared over a predeclared one. Nothing else is written for the message, so there is no second grammar to learn, and the same rule serves an enum, a struct and `type Code int impl Error`:

```snello
struct Pair impl Error { key string; n int }
type Code int impl Error

fmt.Println(Missing("port"), Empty, Pair{key: "k", n: 1}, Code(42))
// Missing("port") Empty {k 1} 42
```

A string field prints quoted inside a variant, so `Parse(cause strconv.Error)` prints as `Parse("strconv.Atoi: parsing \"q\": invalid syntax")`, with the cause's message inside. That line is right for a log, a test and a quick program, and it is not a line for an end user. A program that wants its own wording writes `Error()`, as it always could, and `?` and the rest of this chapter apply to it the same:

```snello
struct LoadError impl Error, Clone {
    path  string
    cause AppError
}

func (e ref LoadError) Error() string { return fmt.Sprintf("load %s", e.path) }
```

The derivation needs every field to be a value `fmt` prints. A function value or a trait value of a trait other than `Error` and `fmt.Stringer` is not one, and the compiler says so at the declaration, naming the field: `Job does not implement Error: field run has type func(), which cannot be printed`. A type that writes `Error()` itself may hold anything.

An error prints as its message wherever a value is printed: `Println`, `%v`, `%s`, `%q`, inside an `Err(...)`, a struct or a slice. A trait value of `Error` prints the same way, through the method of the value it holds, and so does a trait value of `fmt.Stringer`; a trait value of any other trait does not print yet.

## The cause

`Source()` gives the error under this one. A type that writes none gives `None`. A type that wraps another error writes it, returning an owned trait value, which is why the field is cloned: a trait value owns what it holds, chapter 11, and the method lends the error it is called on:

```snello
func (e ref LoadError) Source() Error? { return Some(e.cause.Clone()) }
```

So a type that is a cause lists `Clone`, as `AppError` above does and as the library's `io.Error` and `strconv.Error` do. The cost is one clone and one allocation each time a source is asked for, which is on the error path and nowhere else.

A chain is walked by asking. `Source()` on a trait value of `Error` goes to the method of the value it holds, so one loop serves every error:

```snello
if s := boxed.Source() {
    fmt.Println("source:", s)              // source: load /nonexistent/x
}
```

`%+v` prints the chain for you: the message, then each source's message after a colon and a space, down to the error with no source. On an error with no source it prints the same as `%v`:

```snello
fmt.Printf("%v\n%+v\n", e, e)
// load /nonexistent/x
// load /nonexistent/x: Read("open /nonexistent/x: no such file or directory")

var boxed Error = Outer{inner: e}
fmt.Printf("%+v\n", boxed)
// start: load /nonexistent/x: Read("open /nonexistent/x: no such file or directory")
```

The `+` flag goes with `v` on an error and nowhere else, with a width but no precision, since a chain is not a text to cut. Since the compiler checks every format, chapter 14, `%+v` on an `int` is refused: `fmt.Printf format %+v takes an error, not int`, and `%+d` is refused as the format is read. There is one meaning for the flag, and a message that includes its cause's text by hand, as `Parse("...")` does, prints that text twice under `%+v` only if the type also writes a `Source` for it: a type writes the cause into its message or gives it through `Source`, not both.

## `?` passes an error on

`x?` in a function returning `Result<T, E>` returns `Err` with the error when `x` is `Err`, chapter 8. When the error's type is `E`, it is passed as it is. When it is not, `?` passes it on wherever an assignment would take it, and into the one variant that holds it:

- **Into the variant that holds its type.** When `E` is an enum with exactly one variant whose only field has the error's type, `?` wraps the error in that variant. `Read(cause io.Error)` takes an `io.Error`, and `Parse(cause strconv.Error)` a `strconv.Error`, with nothing written:

```snello
func load(path string) Result<int, AppError> {
    text := os.ReadToString(ref path)?         // an io.Error becomes Read(e)
    n := strconv.Atoi(ref text)?               // a strconv.Error becomes Parse(e)
    return Ok(n)
}
```

- **Into a trait value.** When `E` is a trait the error's type implements, `?` makes the trait value. With `Error` itself, a function fails with any error, which is the shape of a function returning `(T, error)` elsewhere:

```snello
func loadAny(path string) Result<int, Error> {
    text := os.ReadToString(ref path)?         // an io.Error becomes an Error value
    n := strconv.Atoi(ref text)?               // and so does a strconv.Error
    return Ok(n)
}

match loadAny("/nonexistent/x") {
    Ok(n)  => fmt.Println(n),
    Err(e) => fmt.Println("error:", e),        // error: open /nonexistent/x: no such file or directory
}
```

- **As it is**, when the types match, as `parse(text)?` inside a function returning the same `Result<int, AppError>`.

Two variants holding the same type leave the choice to the program, and `?` refuses: `? cannot pass on an error of type io.Error from a function returning Result<string, SyncError>: SyncError holds an io.Error in Read and in Write; convert it`. An enum with no such variant is refused with the reason: `no variant of ConfigError holds an io.Error`. A variant with two fields, `Unreadable(path string, cause io.Error)`, is not a target, since `?` has nothing to put in the other field. In each case the conversion is written as it always was, with `MapErr` or a `match`:

```snello
func wrap(path string) Result<int, LoadError> {
    return load(path.Clone()).MapErr(func(e AppError) LoadError {
        return LoadError{path: path, cause: e}
    })
}
```

The conversion is one step: `?` never wraps an error in a variant whose field is itself an enum holding the error. And it belongs to `?` alone: `return Err(e)`, an assignment and `MapErr` take exactly the type written.

## The library's errors

Every `os` and `net` function that can fail returns an `io.Error`: the operation, the path or address, the system's reason, and a `Kind` to match on, `NotExist`, `Permission`, `Exists`, `Refused`, `Reset`, `TimedOut` or `Other`. `strconv.Atoi` and `ParseFloat` return a `strconv.Error` with the function, the text and the reason, and a `Kind` of `Syntax` or `Range`. Chapter 14 has both. Neither wraps another error, so their `Source` is `None`; both are `Clone`, so a program's error may hold one and give it through its own `Source`.

A program that must tell failures apart matches on its own enum, and reaches the kind through the field:

```snello
match load("app.conf") {
    Ok(n)                                   => fmt.Println(n),
    Err(Read(e)) if e.Kind() == io.NotExist => fmt.Println("no config, using defaults"),
    Err(e)                                  => { fmt.Println(e); os.Exit(1) },
}
```

A test asserts on an error with `expect.IsErr`, which gives the error back for a closer look, chapter 16.

## For maintainers

- **The trait.** `Error` is predeclared in `compiler/types/universe.go`, its two methods given by `requires` and `also`; `std/builtin/builtin.snello` documents it beside the other standard traits. The trait's table, for a value of type `Error`, has the drop, then `Error`, then `Source`, in both backends.
- **The derivations.** `deriveMethod` in `compiler/types/stdtraits.go` adds each of the trait's methods a type did not write, marked derived; `unprintable` and `printable` decide whether the derived message can print the fields. The backends write a derived `Error()` as the stringer of the type's structure and a derived `Source()` as a function returning `None`: `messenger` and `sourcer` in `compiler/emit/c/error.go` and `compiler/emit/llvm/error.go`, reached through each backend's `methodNameOf` as the other derived methods are. The stringer of a declared type prints its structure rather than its message, so that a derived message over a basic type prints the number and not itself.
- **`?`.** `try` in `compiler/types/expr.go` calls `passError` when the error types differ: it accepts a trait the type implements, finds the one variant holding the type, and records it in `Info.Wraps` for the backends; two variants and none are refused there. Each backend's `try` hands the error to `passed`, which wraps it with `variantValue`, boxes it with `box`, or leaves it.
- **The chain.** `ParseFormat` in `compiler/types/format.go` reads the `+` flag into `FormatPiece.Plus` and refuses it on any verb but `v`; `verbFits` in `printf.go` refuses it on a type that is not an error. Each backend's `verb` writes `%+v` as a call of `chainer`, one function per type, which asks `Error` and then `Source` of the value and of each source through its table, dropping each source once asked.
- **Printing a trait value.** `toText` in the C backend and `textAt` in the LLVM backend print a trait value whose trait refines `Error` or `fmt.Stringer` through its table, `traitText` and `traitMessage`; the stringer refuses any other trait value as before.
- **The tests.** Each file above has its test beside it: `universe_test.go` for the trait's two methods, `stdtraits_test.go` for what a derived message can print, `expr_test.go` for the `?` rule and what `Info.Wraps` records, `format_test.go` and `printf_test.go` for the flag; in each backend `error_test.go` reads the C or the IR written for a derived `Source`, the chain and a trait value's message, and `trait_test.go` or `call_test.go`, `hoist_test.go` or `expr_test.go`, `library_test.go` and `helpers_test.go` read what the derived methods, `?`, `%+v` and a trait value's printing compile to. `error-derived`, `error-chain` and `error-pass` in `compiler/emit/internal/backendtest` run through both backends in release and debug builds; the refusals are the programs below; `examples/run/errors.snello` and `examples/09-errors.snello` run in the suite.

## What the compiler reports

| Message | Program |
|---|---|
| `? cannot pass on an error of type io.Error from a function returning Result<string, SyncError>: SyncError holds an io.Error in Read and in Write; convert it` | `try-ambiguous` |
| `? cannot pass on an error of type io.Error from a function returning Result<string, ConfigError>: no variant of ConfigError holds an io.Error` | `try-no-variant` |
| `Job does not implement Error: field run has type func(), which cannot be printed` | `error-unprintable` |
| `fmt.Printf format %+v takes an error, not int` | `format-plus` |
