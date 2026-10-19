# The standard library

Status: decided 2026-10-18. This note says what the Teak standard library is: which names the language itself knows, where every other name comes from, how the library relates to Go's, and the rules every library signature follows. It exists because the twenty example programs use library names that no other document defines, and the checker in stage 1 is the first code that needs the answers. Three decisions had no precedent to follow; they are marked **decided** below with the reason, and the owner accepted all three with the stage 1 review (2026-10-18). Everything else follows a precedent named in place.

## The precedents

Three languages define the space, and Teak follows Go wherever Go has an answer, as the syntax conventions require.

- **Go** keeps the language's own vocabulary to a few dozen predeclared identifiers, `len`, `make`, `append`, `error` and the basic types, and puts everything else in packages that must be imported. There is no prelude.
- **Rust** makes `Option` and `Result` library types but imports them through a prelude, and the `?` operator is language syntax that knows them.
- **Swift** does the same with less ceremony: `T?` is sugar for a library enum, and the standard library is imported implicitly.

Teak takes Go's shape, a short predeclared list and no prelude, with Swift's sugar for the option type, which the syntax conventions already fixed as `T?`.

## 1. What the language knows

These names are predeclared: visible in every file without an import, and known to the compiler. The list is deliberately short and this section is its only home; the stage 1 checker's universe scope is built from it.

**Number types.** The short names used by Rust, Zig, Odin, Hare, Carbon and by LLVM itself, plus Go's everyday words. This is the one place Teak's vocabulary departs from Go's on purpose: Go's syntax and keywords are what make Teak readable at first sight, while the spelling of a primitive type is vocabulary, and vocabulary is where a language shows it is not a dialect.

| Kind | Types |
|---|---|
| Signed integers | `i8`, `i16`, `i32`, `i64`, `int` |
| Unsigned integers | `u8`, `u16`, `u32`, `u64`, `uint` |
| Floating point | `f32`, `f64` |
| Aliases | `byte` for `u8`, `rune` for `i32` |

`int` and `uint` are the word size of the platform and `int` is the index type, as in Go, so there is no `usize`. `byte` and `rune` name a meaning rather than a width and stay because strings are UTF-8 as in Go. Integer arithmetic wraps on overflow as in Go; checked arithmetic is a library function returning `T?`, not a language mode. There is no `i128`, no complex type and no `uintptr`: the first two are library types if ever needed, the third waits for the foreign-function boundary. Literals take the type their context asks for; there is no untyped constant arithmetic in the first version.

**Other basic types.** `bool`, `string`, `error`.

**Option and result.** `T?` with `Some` and `None`; `Result<T, E>` with `Ok` and `Err`. Both are enums the compiler knows, because `?` and `??` and `match` exhaustiveness are language features over them. Their methods: `Expect`, `Unwrap`, `UnwrapOr`, `IsSome`, `IsNone`, `IsOk`, `IsErr`, `Map`, `MapErr`. The ones that give the inside away, `Expect`, `Unwrap`, `UnwrapOr`, `Map` and `MapErr`, take their receiver; the tests lend it (decided with the borrow checker, 2026-10-18).

**Functions.** `len`, `cap`, `make`, `take`, `panic`, `print`, `min`, `max` (the last two added 2026-10-19 with the examples: Go has them since 1.21, and example 10 uses `min`).

**Methods on the built-in collection types.** Go cannot declare a method on a built-in type, so it hides these behind `append`, `delete` and the `strings` package. Teak puts them on the value, which is what the examples already write.

| On | Methods |
|---|---|
| `[]T` | `Push`, `Pop`, `Insert`, `Remove`, `RemoveValue`, `Clear`, `Reverse`, `Sort`, `SortBy`, `Contains`, `Clone` |
| `map[K]V` | `Get`, `GetMut`, `Insert`, `Remove`, `Contains`, `Clone`. `m[k]` on a missing key reads the zero value and changes nothing; `m[k] = v` and `m[k] += v` make the entry. A range visits entries in insertion order (decided 2026-10-18 with the C backend) |
| `string` | `Len`, `Clone`, `Fields`, `Lines`, `Split`, `SplitOnce`, `Trim`, `HasPrefix`, `HasSuffix`, `Contains`, `ToUpper`, `ToLower` |
| `byte` | `IsDigit`, `IsSpace`, `IsLetter`, `IsUpper`, `IsLower`, ASCII |

**The standard traits.** `Copy`, `Clone`, `Eq`, `Ord`, `Hash`, `Drop`, `Error`, and the operator traits `Add`, `Sub`, `Mul`, `Div`, `Rem`, `Neg`. Every number type is `Copy`, `Eq`, `Ord` and `Hash`, and implements the operator traits.

**Decided 1 (2026-10-18): predeclared.** The collection methods are predeclared with their types, as above, because Go's `append` and `len` are, and because `xs.Push(v)` with an import line above it would surprise a Go developer. The alternative was a package the program imports for them.

## 2. No prelude: everything else is imported

Go's rule, and the rule the design's "what Teak leaves out" table implies, since a prelude is an implicit. Every type and function not in section 1 lives in a package and is named through it: `arena.Index<Node>`, `sync.Arc<T>`.

| Package | Contents |
|---|---|
| `arena` | `Arena<T>`, `Index<T>`, `New` |
| `rc` | `Rc<T>`, `Weak<T>` |
| `sync` | `Arc<T>`, `Mutex<T>`, `RwLock<T>`, `Once`, `WaitGroup` |
| `fmt` | `Println`, `Printf`, `Sprintf`, `Sprint`, `Fprintln`, `Fprintf` |
| `os` | `Args`, `Exit`, `Stdin`, `Stdout`, `Stderr`, `File`, `ReadToString`, `WriteString`, `Open`, `Create`, `Remove`, `RemoveAll`, `MkdirTemp` |
| `io` | `Reader`, `Writer`, `Error`, `Copy` |
| `bufio` | `Reader`, `Writer`, `Scanner`, `NewReader`, `NewWriter`, `NewScanner` |
| `strings` | `Builder`, `Repeat`, `Join`, `ToLower`, `ToUpper`, `Fields` |
| `strconv` | `Atoi`, `Itoa`, `ParseFloat`, `FormatFloat`, `Error` |
| `sort` | `Sort`, `SortBy`, `Search`, `IsSorted` |
| `math` | `Pi`, `Sqrt`, `Abs`, `Min`, `Max`, over `f64` |
| `time` | `Time`, `Duration`, `Now`, `Since`, `Sleep`, `After`, `Tick`, `Nanosecond` to `Hour` |
| `regexp` | `Regexp`, `Compile` |

Examples 01, 03, 12 and 15 name `arena.Arena`, `arena.Index` and `sync.Arc` through their packages (sweep done 2026-10-18); no example uses a library name without an import. `net/http`, `json` and `db` in the HTTP example, `heap` in the graph example and `net` in the state machine are placeholders for a later library and are not part of this list.

**Decided 2 (2026-10-18): `rc`.** The package for `Rc` and `Weak` is `rc`, after Rust's `std::rc`, so the type reads `rc.Rc<T>`. The alternative, `shared`, says what the package is for but makes the type read `shared.Rc<T>`, a name that explains itself twice.

## 3. Go's package map, Teak's signatures

Package names and layout are Go's, so a Go developer knows where to look. Inside a package the rule is: **the same name when the meaning is the same, a new name when ownership changes the meaning.** `strings.Repeat` is the same function in both languages. `os.ReadFile` is not: Go's returns a byte slice and a nullable error, Teak's returns `Result<string, io.Error>`, so it is `os.ReadToString`. Kotlin treats Java's library the same way: the same names where nothing changed, new ones where null safety did.

## 4. Three signature rules

Every library function follows these, so a reader can predict a signature from a function's meaning. They are Rust's conventions in Teak's words, and they follow from `04-borrows-ref-and-mut.md`.

1. A parameter that is only read is `ref T`: `strings.Repeat(s ref string, n int) string`, `fmt.Println(args ...ref any)`.
2. A parameter that is changed is `mut T`: `sort.Sort(xs mut []T)`, `fmt.Fprintln(w mut io.Writer, args ...ref any)`.
3. A parameter that is kept, stored or sent is taken by value, which moves it: `Push(v T)`, `arena.New(v T) Index<T>`, `sync.Arc.New(v T) Arc<T>`.

Results are owned. `Copy` types are by value everywhere, and for them rule 1 reads as plain pass-by-value, as in Go.

## 5. The call-site rule

Rule 1 forces one language decision. The borrows note today says a borrow at a call site is written with the keyword, `strings.Fields(ref job.text)`, and every print call in the examples passes owned values without it: `fmt.Println("error:", e)`. Under the written rule that call moves `e` into `Println`, which is not what anyone means.

**Decided 3 (2026-10-18): yes.** An owned value passed where a `ref T` parameter is expected is borrowed for the call, exactly as a receiver already is: `x.Describe()` borrows `x` with no keyword. `mut` stays explicit at every call site, because a visible mutation is worth a word, as Go's `&x` and Swift's `&` are. Writing `ref` at the call site remains legal and means the same thing. With this rule the print calls are correct as written; the borrows note states the rule beside its call-site example.

## How the library is built

Decided 2026-10-19, with the first package. The library is Teak source under `std/`, one directory per import path holding one file named after the package, `std/math/math.teak`, embedded in the compiler binary so that a build needs no library path and no environment, as Go's toolchain carries its own standard library. When a program imports a path the compiler's table does not answer, the importer (`std.Importer`, the counterpart of `go/importer`) parses and checks that package once, into the same `types.Info` as the program, and the C backend compiles the package with the program into the one C file, each of its top-level names qualified by the import path in the runtime's reserved space: `math.Sqrt` is `tk_math_Sqrt`. There is no separate compilation and no library binary; a Teak program is one translation unit, which is what lets the C compiler inline across the boundary.

A library function the runtime must provide is declared without a body, as a Go function implemented in assembly is, and the runtime defines it in C under that same qualified name: `func Sqrt(x f64) f64` in package `math` is `double tk_math_Sqrt(double)` in `teak.h`. A library variable declared without a value is the runtime's the same way: `var Args []string` in `os` is filled by the runtime at start-up. The runtime's side of `os` is small and primitive on purpose: each function takes a descriptor and returns the system's error number, zero on success, storing its result through its last parameter, and the Teak code builds the `Result` and the `io.Error`. A program may not declare a function without a body; the checker reports `missing function body`. Only the exported names of a package are visible to the program, by Go's capitalization rule, and so are only the exported fields and methods of its types; an import of a path the library does not have is reported at the import.

`io.Error` is a struct: the operation, the path and the system's reason, with a `Kind` the program can match on, `NotExist`, `Permission`, `Exists`, `Other`, and `Error()` giving Go's wording. It implements the standard trait `Error`, which its own name shadows inside package `io`; the syntax conventions resolve a trait's name as a trait for that reason. `io.Errno(op, path, code)` builds one from an error number, which is how `os` reports. `io.Reader` and `io.Writer` are traits with one method each, and `io.Copy` is generic over them, since a borrowed trait value is not a type yet.

Every package is a `.teak` file under `std/`, the ones the compiler writes included (decided 2026-10-19, at the owner's word: a reader expects `std/` to list the whole library, with the documentation where Go puts it). `std/fmt/fmt.teak` declares `Println`, `Printf`, `Sprint`, `Sprintf`, `Fprintln` and `Fprintf` without bodies, with `...ref any` for the arguments the compiler takes by their types, and the backend writes each call from a table of those six names and `os.Exit`; the checker holds no table of library names at all. `fmt` is the compiler's by design: its functions take arguments of any type and the compiler lowers each by its type, so there is no formatter in the runtime. `Fprintln` and `Fprintf` are generic over `W: io.Writer`, since a borrowed trait value is not a type yet, and the backend writes the formatted text with one call of the type's `Write`. `os.Exit` is declared `func Exit(code int)` and the checker knows the call never returns, as it knows of `panic`, until a signature can say so.

`arena` is written in Teak with no runtime at all (2026-10-19): `Arena<T>` is a slice of slots, each an `Option` of the value and a generation, a free list of slot numbers and a count; `Index<T>` is the slot number and the generation, `Copy`, `Eq` and `Hash`, with `T` as a phantom parameter so an index carries its element type. Generations start at 1 so that the zero `Index` names nothing. `Get` and `GetMut` match on the slot's `Option` through a borrow of the arena, and `Remove` takes the value out with `take` and raises the generation. `New<T>()` takes its type argument explicitly where nothing infers it, as example 15's `slots: arena.New()` would need; the iteration `for i, v := range a` of `02-arenas-and-indices.md` waits for the iterator trait.

`rc` is written in Teak over two functions the compiler provides (2026-10-19): `Rc<T>` and `Weak<T>` each hold an `Option` of an owned pointer to a shared cell, the value as an `Option` beside a strong and a weak count; `alias(p ref *cell<T>) *cell<T>` gives a second owned copy of the pointer a borrow lends, and `leak(p *cell<T>)` consumes a pointer and frees nothing. They are the one unsafe step reference counting needs, declared without bodies and unexported, so a program cannot reach them, and lowered by the backend to the pointer itself, as `fmt`'s functions are lowered to the printers. `Clone` and `Downgrade` alias the cell and raise a count; `Drop` takes the pointer out of the handle with `take`, lowers a count, drops the value with the last `Rc`, and either leaks the pointer, so the cell outlives this handle, or lets it go, so the last handle of either kind frees the cell. `Get` lends the value through the pointer; `GetMut` lends it only when the strong count is 1 and the weak count 0, since a `Weak` elsewhere could otherwise be upgraded and read while the `mut` borrow lives. There is no interior mutability: a tree whose nodes hold `Weak` links back to their parents, as `examples/01-tree/README.md` describes, cannot be built with `Rc` alone, since a node is shared by the time its link is set; that wants a `Cell`-like type or the arena, and is noted for the checkpoint.

`bufio` is written in Teak over `io`'s traits (2026-10-19): `Writer<W: io.Writer>`, `Reader<R: io.Reader>` and `Scanner<R: io.Reader>` each hold a `mut` borrow of the value under them, so they are borrow-holding structs under the rules of `00-syntax-conventions.md`, lent and dropped but never stored, and `NewWriter(w mut W)` returns one tied to its one borrowed parameter. `Writer` implements `io.Writer` and `Reader` implements `io.Reader`, so `fmt.Fprintln` and `io.Copy` take them; for that the rule on type arguments gained its one exception, a generic function that only borrows values of a type parameter takes an argument holding a borrow (`lentOnly`). The buffer is a byte slice written out in blocks of 4096 bytes; the scanner keeps the unconsumed tail and reads a block when it has no newline, strips `\n` and `\r\n`, and counts a last line without one. A range over a scanner's lines, as example 19 writes it, waits for the iterator trait; the Go idiom `for sc.Scan()` stands until then.

**Decided 4 (2026-10-19): `std/builtin` documents the predeclared names.** `Option`, `Result` and the methods of section 1 stay the compiler's, since `?`, `??`, the `if` binding and `T?` are language features over them and a method of their own cannot be declared in Teak; but a reader of `std/` expects the whole library there, and `Push`, `Expect` and `Map` were documented nowhere a reader of the library would look. So `std/builtin/builtin.teak` declares them all with doc comments and no bodies, as Go's package `builtin` does, with the receivers written as a call would use them, `func (xs mut []T) Push(v T)`, `func (o T?) Map<U>(f func(v T) U) U?`. It is never compiled and its import is refused. The objection to a second statement of the signatures, that it drifts, is answered by a test: `TestBuiltin` under `std/` checks the file's methods as methods of holder structs and compares each with the checker's table, receiver mode, parameters and result, and reads the table's `case` names back to find a method the file omits. The file is thus the written specification of the predeclared surface, which the checker had only as code. Writing it found a parser gap: an optional tuple, `(ref string, ref string)?`, parsed as a parameter type but not as a result, so `SplitOnce`'s own signature could not be written; `parseResults` takes the `?` now.

Written in Teak: `math`, `io`, `os`, `strconv`, `strings`, `sort`, `time` but for `After` and `Tick`, which hand a `Time` over a channel and come with stage 3, `arena`, `rc` over its two compiler-provided primitives, and `bufio`; `fmt` is declared there and written by the compiler, and `builtin` documents the predeclared names. `sort.Sort` is the slice's `SortBy` with the element type's `Cmp`, and `time.Duration` is a number type over `i64`, so a `Duration` is written as a product with a constant, `10 * time.Millisecond`. Example 10 calls `sort.SliceBy`, which this table names `SortBy` as the slice method is; the example was changed to `SortBy` at task 2.7 (2026-10-19). `strings.Fields` returns the slice of borrows the string's method yields, a type only the library may write until the iterator trait exists. `strings.Builder` is a `[]byte` written to and copied out by `String()`; `strconv.Atoi` is written in Teak and `ParseFloat` reads through the runtime. The table below is now the predeclared methods alone.

## The core library table

Stage 1 had no library code written in Teak: a table in the C emitter mapped each library name onto a function of the C runtime with the same meaning. What remains of it is the methods of section 1 on slices, maps, strings, options and results, each a runtime function, and the lowering of the seven functions the compiler provides. Library code written in Teak carries over to stage 4 unchanged.

## What this does not decide

Which functions exist beyond the ones above. The twenty examples are the specification for that, and task 2.6 keeps its criterion: every function the examples call exists with the signature the examples use.
