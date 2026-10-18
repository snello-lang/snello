# The standard library

Status: draft for decision, 2026-10-18. This note says what the Teak standard library is: which names the language itself knows, where every other name comes from, how the library relates to Go's, and the rules every library signature follows. It exists because the twenty example programs use library names that no other document defines, and the checker in stage 1 is the first code that needs the answers. Three decisions are marked **open** for the human; everything else follows a precedent named in place.

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

**Option and result.** `T?` with `Some` and `None`; `Result<T, E>` with `Ok` and `Err`. Both are enums the compiler knows, because `?` and `??` and `match` exhaustiveness are language features over them. Their methods: `Expect`, `Unwrap`, `UnwrapOr`, `IsSome`, `IsNone`, `IsOk`, `IsErr`, `Map`, `MapErr`.

**Functions.** `len`, `cap`, `make`, `take`, `panic`, `print`.

**Methods on the built-in collection types.** Go cannot declare a method on a built-in type, so it hides these behind `append`, `delete` and the `strings` package. Teak puts them on the value, which is what the examples already write.

| On | Methods |
|---|---|
| `[]T` | `Push`, `Pop`, `Insert`, `Remove`, `RemoveValue`, `Clear`, `Reverse`, `Sort`, `SortBy`, `Contains`, `Clone` |
| `map[K]V` | `Get`, `GetMut`, `Insert`, `Remove`, `Contains`, `Clone` |
| `string` | `Len`, `Clone`, `Fields`, `Split`, `SplitOnce`, `Trim`, `HasPrefix`, `HasSuffix`, `Contains`, `ToUpper`, `ToLower` |

**The standard traits.** `Copy`, `Clone`, `Eq`, `Ord`, `Hash`, `Drop`, `Error`, and the operator traits `Add`, `Sub`, `Mul`, `Div`, `Rem`, `Neg`. Every number type is `Copy`, `Eq`, `Ord` and `Hash`, and implements the operator traits.

**Open 1.** Whether the collection methods are predeclared, as above, or whether only the types are predeclared and the methods come from a package the program imports. The recommendation is predeclared, because Go's `append` and `len` are, and because `xs.Push(v)` with an import line above it would surprise a Go developer.

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
| `strings` | `Builder`, `Repeat`, `Join` |
| `strconv` | `Atoi`, `Itoa`, `ParseFloat`, `FormatFloat`, `Error` |
| `sort` | `Sort`, `SortBy`, `Search` |
| `math` | `Pi`, `Sqrt`, `Abs`, `Min`, `Max` |
| `time` | `Time`, `Duration`, `Now`, `Since`, `Sleep`, `After`, `Tick`, `Nanosecond` to `Hour` |
| `regexp` | `Regexp`, `Compile` |

The examples that use `Arc`, `Index` or `Arena` without a qualifier gain an import line and the qualifier. `net/http`, `json` and `db` in the HTTP example are placeholders for a later library and are not part of this list.

**Open 2.** The package name for `Rc` and `Weak`. `rc` follows Rust's `std::rc`; `shared` would say what the package is for. Either is one word in a handful of import lines.

## 3. Go's package map, Teak's signatures

Package names and layout are Go's, so a Go developer knows where to look, and the first backend emits Go. Inside a package the rule is: **the same name when the meaning is the same, a new name when ownership changes the meaning.** `strings.Repeat` is the same function in both languages. `os.ReadFile` is not: Go's returns a byte slice and a nullable error, Teak's returns `Result<string, io.Error>`, so it is `os.ReadToString`. Kotlin treats Java's library the same way: the same names where nothing changed, new ones where null safety did.

## 4. Three signature rules

Every library function follows these, so a reader can predict a signature from a function's meaning. They are Rust's conventions in Teak's words, and they follow from `04-borrows-ref-and-mut.md`.

1. A parameter that is only read is `ref T`: `strings.Repeat(s ref string, n int) string`, `fmt.Println(args ...ref any)`.
2. A parameter that is changed is `mut T`: `sort.Sort(xs mut []T)`, `fmt.Fprintln(w mut io.Writer, args ...ref any)`.
3. A parameter that is kept, stored or sent is taken by value, which moves it: `Push(v T)`, `arena.New(v T) Index<T>`, `sync.Arc.New(v T) Arc<T>`.

Results are owned. `Copy` types are by value everywhere, and for them rule 1 reads as plain pass-by-value, as in Go.

## 5. The call-site rule

Rule 1 forces one language decision. The borrows note today says a borrow at a call site is written with the keyword, `strings.Fields(ref job.text)`, and every print call in the examples passes owned values without it: `fmt.Println("error:", e)`. Under the written rule that call moves `e` into `Println`, which is not what anyone means.

**Open 3, recommended yes.** An owned value passed where a `ref T` parameter is expected is borrowed for the call, exactly as a receiver already is: `x.Describe()` borrows `x` with no keyword. `mut` stays explicit at every call site, because a visible mutation is worth a word, as Go's `&x` and Swift's `&` are. Writing `ref` at the call site remains legal and means the same thing. With this rule the print calls are correct as written, and the borrows note changes one example line.

## The stage 1 mapping table

Stage 1 has no library code. It has a table in the Go emitter that maps each of these names onto the Go function of the same meaning, and nothing more: `fmt.Println`, `fmt.Printf`, `fmt.Sprintf`, `fmt.Sprint`, `os.Exit`, `os.Stderr`, `os.ReadToString`, `strings.Fields`, `strings.Repeat`, `time.Now`, `math.Pi`, `math.Sqrt`, and the methods of section 1 on slices, maps, strings, options and results. Everything in the table is debt that the stage 4 runtime repays. Library code written in Teak, which begins at task 2.6, carries over to stage 4 unchanged.

## What this does not decide

Which functions exist beyond the ones above. The twenty examples are the specification for that, and task 2.6 keeps its criterion: every function the examples call exists with the signature the examples use.
