# 14. The standard library

The samples are cut from `compiler/emit/internal/backendtest/testdata/library.snello`, `files.snello`, `text.snello`, `order.snello`, `arena.snello`, `shared.snello`, `buffered.snello`, `clocks.snello` and `streams.snello`, and from `examples/traits/traits.snello` and `14-files.snello`.

Snello predeclares little: the basic types, `Option` and `Result`, the methods of slices, maps and strings, the built-in functions and the standard traits. Everything else lives in a package, and a program names what it uses through an import, as the earlier chapters did with `fmt`:

```go
import ("fmt"; "math")
```

## Importing a package

An import names a package by its path, and the program refers to the package's members through the last element of the path and a dot: `math.Pi`, `math.Sqrt(x)`. A different name may be given, `import m "math"`, and the members are then `m.Pi` and `m.Sqrt`. Only the exported names of a package are visible, the ones starting with a capital letter; the rest are the package's own. Importing a path the library does not have is reported at the import, `package nosuch is not in the standard library`, and nothing further is said about the names the program takes from it.

What needs no import is documented too: `std/builtin/builtin.snello` declares the basic types, `Option` and `Result` with their methods, the methods of slices, maps and strings, the built-in functions and the standard traits, each with its comment, as Go's package `builtin` does. Nothing in it is compiled, and `import "builtin"` is refused, `package builtin is not imported: its names are predeclared`; a test keeps every signature in it in step with the compiler's own.

A library package is ordinary Snello, compiled with the program into the same binary, so a library function costs what a function of the program costs, and the compiler sees through the call as it sees through any other. A library function is a value like any function:

```go
func apply(f func(f64) f64, x f64) f64 { return f(x) }

fmt.Println(apply(math.Sqrt, 2) > 1.41)   // true
```

A few library functions are not written in Snello: the runtime provides them in C, and the package declares them without a body, `func Sqrt(x f64) f64`; the compiler provides `fmt`'s and `os.Exit`, declared the same way. A program cannot do the same; a function without a body is `missing function body`. A library's types are named through the package too, `io.Error`, `os.File`, in a signature or an `impl` list alike, and only their exported fields and methods are reachable from outside the package.

## The packages

| Package | What it has |
|---|---|
| `fmt` | `Println`, `Printf`, `Sprintf`, `Sprint`, and `Fprint`, `Fprintln` and `Fprintf` writing to a `mut io.Writer`, and the `Stringer` trait. A format knows `%v`, `%d`, `%x`, `%X`, `%o`, `%b`, `%c`, `%s`, `%q`, `%f`, `%e`, `%g`, `%t` and `%%`, a precision, `%.2f`, `%.3d`, `%.2s`, a width before the verb, `%6d`, or `%-6s` to pad on the right, and a zero before the width to pad a number with zeros, `%05d` and `%02x`; it is checked against its arguments when the program is compiled |
| `io` | `Error` with `Error()`, `Kind()` and `Written()`, the bytes a write took before it failed, `NewError` for the errors a program's own reader or writer reports and `WithWritten` for one that fails partway, the `Kind` enum, the `Reader`, `Writer` and `Seeker` traits and `SeekFrom`, `Copy`, `ReadAll`, `ReadFull`, `Discard` |
| `os` | `Args`, `Stdin()`, `Stdout()`, `Stderr()`, `File` with `Name`, `Read`, `Write`, `WriteString`, `Seek`, `Sync` and `Close`, `Open`, `Create`, `ReadToString`, `WriteString`, `MkdirTemp`, `Remove`, `RemoveAll`, `Exit`, `Cores`, the processors the runtime runs tasks on, for sizing a pool of workers |
| `testing`, `expect` | the test runner and the assertions of chapter 16, imported by test files alone: `testing.Main`, `Run`, `Log`, `Logf`, `Skip`, `TempDir`, `Parallel`, `Loop`, `ResetTimer`, `ReportAllocs`, `Short`; `expect.Equal`, `NotEqual`, `EqualSlice`, `EqualText`, `True`, `False`, `Fail`, `Failf`, `IsSome`, `IsNone`, `IsOk`, `IsErr`, `Contains`, `ContainsElem`, `HasPrefix`, `HasSuffix`, `Len`, `Empty`, `Golden`, `Panics`, `PanicsWith`, each a panic the runner catches through `testing.Catch`, with the file, line, expression and operands the compiler hands them |
| `math` | `Pi`, `E`, `Ln2`; the functions over `f64`: `Sqrt`, `Cbrt`, `Floor`, `Ceil`, `Trunc`, `Round`, `Mod`, `Pow`, `Exp`, `Log`, `Log2`, `Log10`, `Hypot`, the trigonometric and hyperbolic ones, `Abs`, `Min`, `Max`, `Inf`, `NaN`, `IsInf`, `IsNaN`, `Signbit`, `Copysign`; the limits `MaxInt`, `MinInt`, `MaxFloat64`, `SmallestNonzeroFloat64` and the sized ones, each of its own type (chapter 20) |
| `math/bits` | `LeadingZeros`, `TrailingZeros`, `OnesCount`, `Len`, `RotateLeft` and `Reverse`, each for `uint` and, as `LeadingZeros8` to `LeadingZeros64`, for `u8` to `u64`; the compiler writes each as the processor's instruction (chapter 20) |
| `math/rand` | `Rand`, from `New(seed1, seed2)`, a generator that gives the same numbers for the same seed, with `Int`, `Int64`, `IntN` returning `int?`, `Float64`, `Shuffle` and `Perm`; and the same six as package functions over a source keyed from the system (chapter 20) |
| `strings` | `Builder` with `WriteString`, `WriteByte`, `WriteRune`, `Grow`, `Len`, `String` and `Reset`; `Reader`, from `NewReader`, an `io.Reader` over a string it owns; `Repeat`, a `string?` that is `None` for a negative count, `Join`, `ToLower`, `ToUpper` and `ToTitle` by Unicode's full case mapping, `EqualIgnoreCase`, `ToValidUTF8`, `IndexRune`, `ContainsRune`, `IndexFunc`, `LastIndexFunc`, `TrimFunc`, `TrimLeftFunc`, `TrimRightFunc`, `FieldsFunc`, `Map`, `Fields` |
| `strconv` | `Atoi`, `Itoa`, `ParseInt`, `ParseUint`, `FormatInt` and `FormatUint` in a base from 2 to 36, `ParseFloat`, `ParseFloat32`, `FormatFloat` and `FormatFloat32` with a precision, `ParseBool`, `FormatBool`, `Quote`, `Unquote`, and `Error` with `Func`, `Text`, `Msg` and `Kind()`, `Syntax`, `Range` or `Base`, which is `Clone` so that a program's error may hold one and give it through `Source` (chapter 17) |
| `sort` | `Sort` and `Search` over any `Ord` element, `SortBy` with an order of the program's own, `IsSorted` |
| `heap` | `Heap<T: Ord>`, a priority queue: `New`, `From` to build one from a slice in linear time, `Push`, `Pop` returning the least element first or `None`, `Peek`, `Len`, `IsEmpty`, `Clear`. It is a binary heap over a slice, the layout and algorithm of Go's `container/heap` and Rust's `BinaryHeap`, with Rust's bottom-up `Pop`, so `Push` and `Pop` take O(log n) comparisons; a type that wants the greatest first gives itself a `Cmp` that says so. Elements that compare `Equal` come out in no set order (`examples/graph/graph.snello`) |
| `time` | `Duration` with the constants `Nanosecond` to `Hour`, its readings, `String`, `Round`, `Truncate` and `Abs`, `Time` with `Add`, `Sub`, `Before`, `After`, `IsZero`, `Unix`, `UnixNano` and `String`, `Now`, `Unix`, `Since`, `Sleep`, and `After` and `Tick` as entries in the runtime's timer heap: a `Tick` keeps to its period and drops the ticks a slow receiver misses, and a `Tick` of zero or less never ticks. A `Time` reaches far past any date a program meets, and one from `Now` carries the monotonic clock's reading, so `Since` and `Sub` between two `Now`s are not moved by the system setting its clock; `Sub` gives the longest `Duration` when the span is longer, and `Unix` rounds down, all as in Go (`compiler/emit/internal/backendtest/testdata/clocks.snello`) |
| `arena` | `Arena<T>` with `Insert`, `Get`, `GetMut`, `Remove` and `Len`, `Index<T>`, `New`; chapter 13 |
| `rc` | `Rc<T>` with `Get`, `GetMut`, `Clone`, `Downgrade`, `StrongCount` and `WeakCount`, `Weak<T>` with `Upgrade` and `Clone`, `New` |
| `net` | `Listen` on an IPv4 address and a port, `Listener` with `Port`, `Accept` and `SetDeadline`, `Dial` and `DialTimeout`, `Conn` with `Read`, `Write`, `WriteString`, `Close` and Go's deadlines, `SetDeadline`, `SetReadDeadline` and `SetWriteDeadline`, an `io.Reader` and `io.Writer`; an operation that would block parks the task, so a task per connection costs no thread. A failure reads as Go's, `dial tcp 127.0.0.1:1: connect: connection refused`, and its `Kind` is `Refused`, `Reset` or `TimedOut` where it is one of those; an empty host is every address to `Listen` and this machine to `Dial`; a port out of range, a host name and an IPv6 address are refused with an Error saying which, since names are not looked up yet |
| `bufio` | `Writer` with `Write`, `WriteString`, `WriteByte`, `Flush`, `Buffered`, `Available` and `Get`, `Reader` with `Read`, `ReadByte`, `ReadRune`, `UnreadRune`, `UnreadByte`, `Peek`, `ReadString`, `ReadBytes` and `Get`, `Scanner` with `Scan`, `Bytes`, `Text`, `Err` and `Buffer`, and `MaxScanTokenSize`; `NewWriter`, `NewWriterSize`, `NewReader`, `NewScanner` for lines and `Words` for words |
| `bytes` | `Buffer`, a growable `io.Writer` and `io.Reader` and a `fmt.Stringer`, with `Write`, `WriteString`, `WriteByte`, `WriteRune`, `Grow`, `Read`, `ReadByte`, `ReadRune`, `UnreadByte`, `UnreadRune`, `ReadBytes`, `ReadString`, `Next`, `Bytes`, `String`, `Len`, `Truncate` and `Reset`; `NewBuffer`, `NewBufferString` |
| `unicode` | Every class by general category, `IsLetter`, `IsTitle`, `IsMark`, `IsNumber`, `IsDigit`, `IsPunct`, `IsSymbol`, `IsControl`, `IsGraphic`, `IsPrint`, and `IsSpace`; the derived properties `IsUpper`, `IsLower`, `IsAlphabetic`, `IsIDStart`, `IsIDContinue` and `IsEmoji`; `Category`, `Script`, `ScriptCode` and `InScript`; `DigitValue` and `NumericValue`; `ToUpper`, `ToLower`, `ToTitle` and `CaseFold`; `MaxRune` and `Version`; over Unicode 18's own tables (chapter 19) |
| `unicode/utf8` | `RuneLen`, `ValidRune`, `AppendRune`, `DecodeRune` and `DecodeRuneInString`, `DecodeLastRune` and `DecodeLastRuneInString`, `Valid` and `ValidString`, `RuneCount` and `RuneCountInString`, `FullRune` and `FullRuneInString`, `RuneStart`, `RuneError`, `RuneSelf`, `UTFMax` and `MaxRune` (chapter 19) |
| `unicode/utf16` | `Encode`, `Decode`, `AppendRune`, `EncodeRune`, `DecodeRune`, `RuneLen` and `IsSurrogate`; `EncodeBytes` and `DecodeBytes` with `Order`, `BigEndian` or `LittleEndian`, and the byte-order mark (chapter 19) |
| `unicode/norm` | `Form`, `NFC`, `NFD`, `NFKC` and `NFKD`, `String` and `IsNormalString` (chapter 19) |
| `unicode/segment` | `Graphemes`, `Words` and `Sentences`, by annex 29's rules; `Kind`, `Count` and `Next`, for counting and walking without a slice; `IsWord` (chapter 19) |

Every package the library's design names is written now but `regexp`; `sync` is chapter 10's, since it is about tasks. A range over an arena or a scanner's lines waits for the iterator trait.

## io and os

Every `os` function that can fail returns a `Result` whose error is an `io.Error`: the operation that failed, the path or stream it was on, and the system's reason. It prints as one line, `open /nonexistent: no such file or directory`, because a type implementing the `Error` trait prints as its `Error()` message wherever a value is printed, inside an `Err(...)` as well. A program that must tell one failure from another asks `e.Kind()`, which is `NotExist`, `Permission`, `Exists`, `Other`, or for a connection `Refused`, `Reset` or `TimedOut`. A program's own reader, writer or parser reports its failures the same way, with `io.NewError("parse", "config.toml", "unexpected end", io.Other)`; an error on no path prints without one, `decode: bad header`, and one with no operation is its message alone.

```go
func copy(src ref string, dst ref string) Result<i64, io.Error> {
	in := os.Open(src)?
	out := os.Create(dst)?
	n := io.Copy(mut out, mut in)?
	out.Close()?
	in.Close()?
	return Ok(n)
}
```

An `os.File` implements `Drop`: a file that goes out of scope is closed, on every path out of the function. `Close` consumes the file and reports the failure, which `Drop` cannot, so a program that cares about the error calls `Close` and one that does not lets the file go. The three standard streams are files too, open when the program starts, and stay open for its life: `os.Stdout().WriteString("direct\n")`. Nothing is held back on them: what `fmt` prints and what is written to `os.Stdout` and `os.Stderr` go out at once and in order, a prompt printed without a newline shows before the program reads its answer, and a write that fails says so at the write. `fmt.Println` builds its whole line and writes it once, so two tasks' lines never interleave. A program that writes much in small pieces wraps the stream in a `bufio.Writer`. `os.WriteString(path, s)` writes a whole file as `ReadToString` reads one; `os.Remove` removes one file or empty directory, `os.RemoveAll` a whole tree, naming the entry it failed on when it fails; `os.MkdirTemp(dir, "build-*")` puts a random number where the pattern's last `*` is, and when `dir` itself is missing reports its stat, `stat /tmp/gone: no such file or directory`, as Go does. `compiler/emit/internal/backendtest/testdata/streams.snello` and `files.snello` show these.

`io.Reader` and `io.Writer` are traits with one method each, `Read(buf mut []byte)` and `Write(buf ref []byte)`, and `os.File` implements both, and `io.Seeker` too; a program's own types may too, and then `io.Copy` and `fmt.Fprintln` work on them. `io.Copy` is a generic function over the two traits, `Copy<W: Writer, R: Reader>(dst mut W, src mut R)`, so a copy between two files is one copy of the function, with the reads and writes called directly. A writer that takes fewer bytes than it was given is asked again for the rest.

`io.ReadAll(mut r)` reads a reader to its end and returns every byte, and `io.ReadFull(mut r, mut buf)` reads exactly `len(buf)` bytes, asking `Read` as often as it takes, which a decoder needs for a field of a fixed size. `ReadFull` returns `0` when the input ends before any byte, as `Read` does at the end, and an error of kind `io.UnexpectedEOF` when it ends partway, since the field is not all there: a clean end and a cut record are told apart, as Go's `ReadFull` tells `io.EOF` from `io.ErrUnexpectedEOF`.

```go
field := make([]byte, 3)
for io.ReadFull(mut r, mut field)? == 3 {      // 0 at a clean end; a cut record is an error
	name := make([]byte, int(field[2]))
	io.ReadFull(mut r, mut name)?
}
```

`io.Discard` is a writer that keeps nothing, for output a program must write and does not want. It is a type with nothing in it, so its zero value is ready, `var sink io.Discard`, and a struct field or a function's result can be one. It is not a package variable, as Go's is, since a package variable is never lent `mut`.

`f.Seek(pos)` moves a file's position and returns the new one, counted from the start. Where to is an `io.SeekFrom`: `io.Start(n)` from the start, `io.Current(n)` from where the file is and `io.End(n)` from its end, the last two signed. This is Rust's shape rather than Go's `Seek(offset, whence)`, whose `whence` is an `int` that can hold a value meaning nothing; here a position before the start cannot be written, and there is no fourth way to count. Moving past the end is allowed, as the system allows it; a pipe or a terminal has no position, and seeking one fails.

```go
at := f.Seek(io.Start(33))?           // the third record
rest := io.ReadAll(mut f)?
size := f.Seek(io.End(0))?
```

## bytes

`bytes.Buffer` is a run of bytes written at its end and read from its front: what an encoder writes into and a decoder reads from. It is an `io.Writer`, an `io.Reader` and a `fmt.Stringer`, so `fmt.Fprintf` writes into it, `io.Copy` and `io.ReadAll` read from it, and `fmt.Println` prints what it holds. Its zero value is ready, `var b bytes.Buffer`, and `NewBuffer(b)` takes a slice to read, `NewBufferString(s)` a copy of a string.

The writing half is `strings.Builder`'s: `WriteString`, `WriteByte`, `WriteRune` and `Grow`, which cannot fail and return nothing, and `Write`, which is `io.Writer`'s method and so returns a `Result`. The reading half is `bufio.Reader`'s, without the errors a Buffer cannot have: `ReadByte` gives a `byte?`, `ReadRune` a `(rune, int)?`, `ReadBytes` and `ReadString` the bytes up to a delimiter, `None` once every byte was read, and `UnreadByte` and `UnreadRune` put the last back, when the last call was the read: a write or a `Grow` between them ends that, and they return `false`. `Next(n)` reads `n` bytes and lends them, and `Bytes()` lends what is left, each until the Buffer is next asked anything, which the compiler holds a program to: Go's documentation says those slices stop being valid at the next change, and here using one after it is a compile error. `Truncate(n)` keeps the first `n` bytes not read yet, as a slice's `Truncate` does, where Go's panics for an `n` out of range.

```go
var b bytes.Buffer
b.WriteString("REC1")
b.WriteByte(1)
fmt.Fprintf(mut b, "%d", 42)
fmt.Println(b.Len(), b.ReadByte())         // 7 Some(82)
```

The Buffer is Go's: one slice, and an index into it where reading has got to. All of it is dropped once everything is read. When the slice is full, the bytes not read yet move to its start only if they fill at most half of it, and otherwise the slice grows, which is Go's rule: a move of some bytes always makes room for as many more, so a queue with a long backlog does not move the backlog on every write. Ten million writes of eight bytes, each followed by a read of seven, take 0.02 s and peak at 58 MB, against 0.06 s and 84 MB for Go's.

Go's `bytes` package also has searches and comparisons on `[]byte` as functions; in Snello they are methods of the slice, `b.Equal(o)` and `b.Find(sub)` (chapter 9), so the package has only the Buffer. Go's `ReadFrom` and `WriteTo` are left out: they exist so that Go's `io.Copy` can find a faster path by asking an interface at run time, which Snello's `io.Copy`, compiled for the two types it is given, has no counterpart for.

## math

`math` works over `f64`, as `Pi` is one and every function takes and returns one; a program with another float type converts at the call. Chapter 20 has the whole package, with `math/bits` and `math/rand`; this section is the start of it.

```go
func (c ref Circle) Area() f64      { return math.Pi * c.r * c.r }
func (c ref Circle) Perimeter() f64 { return 2 * math.Pi * c.r }
```

```go
fmt.Println(math.Sqrt(16), math.Abs(-3.5), math.Min(2, 7), math.Max(2, 7))   // 4 3.5 2 7
fmt.Printf("%.2f %.0f %f\n", math.Pi, math.Pi, 1.5)                        // 3.14 3 1.500000
```

The literals `2` and `7` take the type the parameters ask for, as chapter 3 says, so `math.Min(2, 7)` needs no `2.0`. The edges are Go's: `Abs(-0)` is `+0`; `Min` answers `-Inf` when either value is, then `NaN` when either is, and takes `-0` as the smaller zero, `Max` the same with `+Inf` and `+0`. The predeclared `min` and `max` follow Go's specification instead, where `NaN` wins over everything:

```go
fmt.Println(math.Min(nan, 1), math.Min(1, nan), math.Max(nan, 1), math.Min(-inf, nan), math.Max(inf, nan))
// NaN NaN NaN -Inf +Inf
fmt.Println(min(nan, 1.0), min(1.0, nan), max(nan, 1.0), max(1.0, nan), min(-inf, nan))
// NaN NaN NaN NaN NaN
```

## strings and strconv

What a string can do on its own is a method of the string, chapter 9: `s.Len()`, `s.Fields()`, `s.ToLower()`. The `strings` package has what works on more than one string or builds a new one piece by piece, and the same transformations as functions, for a call that reads better that way or a function value to pass along. A `Builder` copies each string it is given in one piece, and `Repeat` and `Join` work out the length of their result and allocate it once. `Repeat` gives a `string?`: `None` for a negative count, or one whose result an `int` could not count, so the caller decides, `strings.Repeat(s, n) ?? ""`, where Go stops the program. A search for a string, `Contains`, `Index`, `LastIndex`, `Count`, `Replace`, `Split`, `SplitN` or `SplitOnce`, takes time in proportion to the text and the string sought, however close that string comes to matching everywhere: a fast filter on its first two bytes finds the candidates, and the Two-Way search glibc and Rust use takes over when they keep failing late, where Go's `Index` can take a second on 20 MB.

```go
var b strings.Builder
b.WriteString("tea")
b.WriteByte('k')
fmt.Println(b.String(), b.Len())                          // snello 4
fmt.Println(strings.Repeat("ab", 3) ?? "", strings.Join(ref parts, ", "))   // ababab x, y, z
```

A `Builder` accumulates bytes; its zero value is ready to use, `String()` gives the text so far as a new string, `Reset()` empties it, and `Grow(n)` makes room for `n` more bytes in at most one allocation, for a builder whose final length is known; a count of zero or less, or one too large to add, does nothing. `strings.NewReader(s)` gives a string to whatever reads bytes, `bufio` or `io.Copy`, as an `io.Reader`; it owns the string, so that a `bufio.Scanner` can hold it, which a borrow could not, and a literal moves in with nothing copied. Each `Read` copies what fits, and a read at the end gives zero. `Join` takes a borrowed slice of strings and a separator.

`strconv` reads numbers, bools and quoted strings from text and writes them back. Reading can fail, so every function that reads returns a `Result` whose error is a `strconv.Error`, with the function, the text and the reason, printing as `strconv.Atoi: parsing "4x": invalid syntax`. Writing cannot fail, and no function of the package panics on what it is given.

```go
fmt.Println(strconv.Atoi("42"), strconv.Atoi("4x"))
// Ok(42) Err("strconv.Atoi: parsing \"4x\": invalid syntax")
match strconv.Atoi("x") {
	Ok(n) => fmt.Println(n),
	Err(e) => fmt.Println(e.Func, e.Text, e.Msg),            // Atoi x invalid syntax
}
```

An error's `Kind()` says which failure it is, as Go's `ErrSyntax` and `ErrRange` do, so a program tells them apart without reading the message: `strconv.Syntax` for text that is not of the kind asked for, `strconv.Range` for a number the type does not hold, and `strconv.Base` for a base that is none. A range is a number's: text that is no number is a syntax error however large the digits it starts with, so `Atoi("99999999999999999999x")` is `Syntax`, where Go and Rust report the range as soon as the digits pass it. A program that clamps on `Range` therefore never clamps on text that was no number.

**Integers.** `Atoi` and `Itoa` are base ten. `ParseInt(s, base)` and `ParseUint(s, base)` read an `int` and a `uint` in a base from 2 to 36, the letters `a` to `z` in either case standing for ten to thirty-five. A base of 0 reads the base from the text, as an integer is written in a program: `0x` for sixteen, `0o` or a leading `0` for eight, `0b` for two, ten without one; and only then may an underscore stand between two digits or after the prefix. `FormatInt(n, base)` and `FormatUint(n, base)` write in a base, and return a `string?`: `None` for a base below 2 or above 36, where Go's `FormatInt` stops the program, so the caller decides, `strconv.FormatInt(n, base) ?? ""`. The base read from the text is for reading only, so `FormatInt(n, 0)` is `None`.

```go
fmt.Println(strconv.ParseInt("ff", 16), strconv.ParseInt("-0x_1F", 0), strconv.ParseInt("0755", 0))
// Ok(255) Ok(-31) Ok(493)
fmt.Println(strconv.ParseInt("12", 37), strconv.ParseUint("-1", 10))
// Err("strconv.ParseInt: parsing \"12\": invalid base 37") Err("strconv.ParseUint: parsing \"-1\": invalid syntax")
fmt.Println(strconv.FormatInt(-255, 16), strconv.FormatUint(255, 2), strconv.FormatInt(255, 1))
// Some("-ff") Some("11111111") None
```

A number in base sixteen, eight or two is more often printed with a verb, `%x`, `%o` and `%b` below, which take no base that could be wrong; `FormatInt` is for a base known only when the program runs.

**Floats.** `ParseFloat` reads what Go's `ParseFloat` reads, underscores between digits and hexadecimal floats with their `p` exponent included, `1_000.5` and `0x1p-2`, and refuses what Go refuses, `-nan` and `0x10`. The result is the `f64` nearest the number written, however many digits it has, a tie going to the even one; a number past the largest `f64` is a `Range` error, and one too small to hold rounds to zero, as a literal does. `ParseFloat32` reads an `f32` the same way, and is not `f32(ParseFloat(s))`: that rounds twice, to the nearest `f64` and then to the nearest `f32`, and the two roundings can land one `f32` away from the nearest.

```go
fmt.Println(strconv.ParseFloat32("1.00000017881393432617187499"))   // Ok(1.0000001), the nearest f32
fmt.Println(f32(strconv.ParseFloat("1.00000017881393432617187499").UnwrapOr(0)))   // 1.0000002, rounded twice
```

`FormatFloat(f, prec)` writes `f` with `prec` digits after the point, rounded to them, as `%.2f` does, for a precision known only when the program runs. A negative `prec` asks for the shortest text that reads back as the same number, as `fmt.Println` prints a float. Every digit written is exact, however many are asked for. An `f64` is a whole number times a power of two no smaller than 2⁻¹⁰⁷⁴, and 2⁻ᵏ written in decimal has exactly k digits after its point, so no `f64` has a digit that is not zero past the 1074th: `FormatFloat(5e-324, 1074)` is the smallest `f64` written out in full, 751 digits after 323 zeros, and a larger `prec` adds zeros that cost no arithmetic. `FormatFloat32` does the same for an `f32`, whose shortest text is the shortest that reads back as the same `f32`.

```go
fmt.Println(strconv.FormatFloat(2.5, 2), strconv.FormatFloat(1e21, 0), strconv.FormatFloat(1e21, -1))
// 2.50 1000000000000000000000 1e+21
fmt.Println(strconv.FormatFloat32(f32(0.1), -1), strconv.FormatFloat(f64(f32(0.1)), -1))
// 0.1 0.10000000149011612
```

**Bools and quoted strings.** `ParseBool` reads `1`, `t`, `T`, `true`, `TRUE` and `True` as true and `0`, `f`, `F`, `false`, `FALSE` and `False` as false, as Go's does, and `FormatBool` writes `true` or `false`. `Quote` writes a string as a literal in double quotes, as `%q` does, and `Unquote` reads a literal back: a string in double quotes with its escapes, a string in back quotes taken as it is, or one character in single quotes, given as a string. The whole text must be the literal.

```go
fmt.Println(strconv.Quote("a\tb"), strconv.Unquote("\"a\\tb\""), strconv.Unquote("'x'"), strconv.Unquote("\"open"))
// "a\tb" Ok("a\tb") Ok("x") Err("strconv.Unquote: parsing \"\\\"open\": invalid syntax")
```

*For maintainers.* The integer and bool functions are written in Snello, `ParseUint` after Go's. The float reader and `Unquote` are the runtime's, `runtime/c/parse.c` and `runtime/c/quote.c`. A float is read as Go 1.27 reads one: the digits into an integer of at most nineteen and a power of ten; a number both of whose parts a float holds exactly by one multiplication or division; any other by unrounded scaling, one 128-bit multiplication by the power of ten from the table floats are printed with; and a number of more digits than nineteen, when the digits left out could change the rounding, by exact decimal arithmetic. Nothing is allocated and nothing is asked of the C library, whose `strtod` reads by the program's locale: a C library a program binds may set the locale to one that writes a comma for the point, and `ParseFloat("1.5")` then failed. Writing is the same: a float asked for with more digits than seventeen is written from every digit it has, `runtime/c/decimal.c`, Go's exact decimal arithmetic, which the reader shares, and never by `printf`. Sixteen million calls on eight texts take 0.36 s on an eight-core arm64 laptop, against 0.52 s for Go's and 0.83 s for the `strtod` it replaced. `runtime/runtime_test.go` holds the readers and the writers to Go's `strconv` and `fmt` on generated texts, the ties between two floats written out in full among them, and `std/std_test.go` has a fuzz test for each of `ParseFloat`, `Atoi` and `Unquote`, which asks a Snello program for the library's answer and holds it to Go's: `go test ./std -run '^$' -fuzz FuzzParseFloat -fuzztime 10m`. An input the fuzzer finds is fixed and kept as a seed of the fuzz test and as a case of the package's own tests.

## fmt

`fmt` is the one package the compiler writes itself: `Println` and its kin take arguments of any type, `func Println(args ...ref any)` as `std/fmt/fmt.snello` declares it, and the compiler chooses how to print each one from its type, so no formatting happens at run time and the format of `Printf` is read when the program is compiled. `...ref any` is the library's spelling for that; a program's own function takes arguments of the types it names. `Fprint`, `Fprintln` and `Fprintf` write where they are told, to any value implementing `io.Writer`, lent `mut`:

```go
fmt.Fprintln(mut os.Stdout(), "to", "stdout", 1)                       // to stdout 1
fmt.Fprintf(mut os.Stdout(), "%5d|%-4s|%3s|%2.1f|\n", 42, "ab", "abcdef", 2.25)   // "   42|ab  |abcdef|2.2|"
fmt.Fprint(mut os.Stdout(), "n=", 1, 2, "\n")                          // n=1 2
```

A width pads with spaces to the left of the value, or to the right after a minus, counting characters rather than bytes, so `é` is one; a value already wider is printed whole. Each of the three writes its text with one call of the writer's `Write`, and says nothing about a failure, as Go programs rarely look at one. `Fprint` joins its arguments as `Sprint` does, with a space between two only when neither is a string, and adds no newline.

The verbs are Go's, and print what Go's print: `%v` any value, `%d` an integer, `%s` a string, an `error`, or a value that prints as its `Error` or `String` method, which includes a type parameter bounded by `Error` or `fmt.Stringer` and a trait value whose trait refines one, `%q` a string quoted and escaped as Go's `strconv.Quote` escapes it, or an integer as its rune in single quotes, `%f` a float, `%t` a bool. `%x`, `%X`, `%o` and `%b` print an integer in base sixteen, with small letters or capitals, eight and two, a negative one as a minus sign and its digits, `-ff`; `%x` and `%X` also print the bytes of a string or a `[]u8`, two digits each, which is how a digest is printed. `%c` prints an integer as the character it is, and U+FFFD for one that is no character. `%e` prints a float with an exponent, `1.234568e+03`, and `%g` in the shortest form, as `%v` prints one. A precision is a float's digits after the point with `%f` and `%e` and in all with `%g` and `%v`, a string's characters with `%s`, `%q` and `%v`, and an integer's least digits with `%d`, `%x`, `%X`, `%o` and `%b`. A zero before the width pads a number with zeros after its sign instead of spaces before it, `%05d`, `%02x` and `%08.3f`; the minus flag overrides it, and so does a precision on an integer, as in Go, and an infinity or `NaN`, which has no digits to put zeros before, is padded with spaces. The zero flag goes with a number printed as one and nothing else: not with a string, and not with `%v`, `%x` or `%X` of a number whose type prints as its `String`. A width and a precision are a million at most, the bound Go's `fmt` holds one to when it is passed as an argument, where a larger one prints `%!(BADWIDTH)`; here it is written in the format, so a larger one is refused when the program is compiled. The verbs a string takes, `%v`, `%s`, `%q`, `%x` and `%X`, print a value through its `Error` or `String` method, whatever its type is declared over, as Go's do; every other verb prints the value itself, so `%g` of a temperature that prints as `21.5°C` is `21.5`, and `%d` of a level that prints as `low` is its number. `%+v` prints an error's chain, its message and then each source's after a colon and a space, and the `+` flag goes nowhere else (chapter 17). Since the format is read when the program is compiled, it is also checked then, as `go vet` checks Go's, but as an error: a verb that does not fit its argument, `fmt.Printf format %d takes an integer, not string`, an argument the format does not read, a verb or flag `fmt` does not have, and a format that is neither a string literal nor a constant declared as one are refused, since its text must be known when the program is built. A float prints in the shortest form that reads back as it, with Go's rule for the exponent, and an `f32` with the digits an `f32` needs:

```go
fmt.Println(0.1, 1.0/3, 5e-324, 1e21, 1e20, 123456789.0, 1e6, 123456.0)
// 0.1 0.3333333333333333 5e-324 1e+21 1e+20 1.23456789e+08 1e+06 123456
fmt.Println(inf, -inf, nan, 9007199254740993.0, Some(inf), []f64{nan, 1})
// +Inf -Inf NaN 9.007199254740992e+15 Some(+Inf) [NaN 1]
fmt.Printf("[%.3d] [%8.3d] [%.3d] [%.0d] [%-5d|] [%t]\n", 7, 7, -7, 0, 42, false)
// [007] [     007] [-007] [] [42   |] [false]
fmt.Printf("[%x] [%X] [%o] [%b] [%c] [%x] [%x]\n", 255, 255, 8, 5, 'é', -255, "hi")
// [ff] [FF] [10] [101] [é] [-ff] [6869]
fmt.Printf("[%e] [%.2e] [%g] [%.3g]\n", 1234.5678, 1234.5678, 1e21, 1234.5678)
// [1.234568e+03] [1.23e+03] [1e+21] [1.23e+03]
fmt.Printf("[%05d] [%05d] [%02x] [%08b] [%08.3f] [%-05d] [%06.3d]\n", 42, -42, 7, 5, 3.14159, 42, 42)
// [00042] [-0042] [07] [00000101] [0003.142] [42   ] [   042]
```

A type that implements `fmt.Stringer`, a `String` method returning a string listed after `impl`, prints as what that method returns, by `Println`, `%v`, `%s` and `%q`, and inside a struct or a slice too, as a type implementing `Error` prints as its message:

```go
struct Celsius impl fmt.Stringer {
	deg f64
}

func (c ref Celsius) String() string { return fmt.Sprintf("%.1f°C", c.deg) }
```

`fmt.Println(c, Reading{place: "attic", temp: Celsius{deg: -3}})` then prints `21.5°C {attic -3.0°C}`. The trait is named, as every Snello trait is: a type with a `String` method that does not list `fmt.Stringer` prints as its fields, or, declared over a number, as its number; `time.Duration` lists it, `type Duration i64 impl fmt.Stringer`, and prints as `1h30m0s`. The samples are from `compiler/emit/internal/backendtest/testdata/formats.snello`.

## sort and time

`sort.Sort` orders a slice in place, least first, by the `Cmp` of its element type, so it takes any slice whose elements are `Ord`: numbers, strings, and a struct or enum that lists `Ord` and gets `Cmp` derived from its fields, chapter 11. `SortBy` takes the order from a function instead. Both lend the slice `mut` and move its elements within it; nothing is copied. The sort is stable, elements that compare equal keeping their order, and is Rust's: driftsort, a quicksort where the input has no order and merges where it has, with a buffer of half the slice. A comparison that is not an order, a NaN among floats or a function that contradicts itself, leaves the order unspecified, but every element is still in the slice once. `Search` finds a value in a sorted slice, or the index where it would go.

```go
sort.Sort(mut nums)
fmt.Println(nums, sort.IsSorted(ref nums), sort.Search(ref nums, 4))   // [1 3 4 5 8] true 2
sort.SortBy(mut scores, func(a ref Score, b ref Score) bool { return a.name < b.name })
```

`time.Duration` is a span of time in nanoseconds, a number type declared over `i64`, so it adds, compares and multiplies by an integer as a number does, and the constants let one be written as a product: `10 * time.Millisecond` is a `Duration` because the literal takes its type from the constant beside it. `time.Time` is an instant, a `Copy` struct that compares with `==` and `<` and has `Add`, `Sub`, `Before` and `After`. `Now` reads the clock, `Since` measures from a `Time`, and `Sleep` waits.

```go
start := time.Now()
time.Sleep(2 * time.Millisecond)
fmt.Println(time.Since(start) >= 2*time.Millisecond)   // true
```

A `Duration`'s `String` gives it as Go prints one: hours, minutes and seconds with the zero leading units left out and the fraction without its trailing zeros, `72h3m0.5s`, `1h30m0s`, `-10s`; under a second the unit that makes the first digit nonzero, `1.5ms`, `2µs`, `999ns`; and `0s` for zero. `Duration` lists `fmt.Stringer`, `type Duration i64 impl fmt.Stringer`, so `fmt.Println(d)` prints that form, as Go's does. `Round(m)` rounds to the nearest multiple of `m`, a half away from zero, `Truncate(m)` toward zero, both leaving `d` alone for an `m` of zero or less, and `Abs` drops the sign; the longest and the most negative `Duration` behave as Go's do. A `Time` lists `fmt.Stringer` and prints in UTC as Go's does, the nanoseconds without their trailing zeros and only when there are any: `time.Unix(1700000000, 500)` prints `2023-11-14 22:13:20.0000005 +0000 UTC`. Go prints the local zone and, for a `Time` from `Now`, the monotonic reading after `m=`; Snello prints UTC and no reading. `compiler/emit/internal/backendtest/testdata/clocks.snello` prints a table of each.

```go
fmt.Println((90 * time.Second).String(), (1500 * time.Nanosecond).String())   // 1m30s 1.5µs
fmt.Println((time.Minute + 31*time.Second).Round(time.Minute).String())       // 2m0s
fmt.Println(time.Unix(951782400, 123456789))   // 2000-02-29 00:00:00.123456789 +0000 UTC
```

## arena

`arena.Arena<T>` owns values in slots and hands out an `arena.Index<T>` for each, a `Copy` value of two numbers that a struct, a slice or a map holds where a pointer would go. `Get`, `GetMut` and `Remove` answer `None` for an index whose value was removed, however many values its slot has held since, because every slot carries a generation that a removal raises and an index remembers the one it was issued with. The package is generic Snello and nothing else, a slice of slots, a free list and a count. Chapter 13 is about it.

```go
a := arena.New<string>()
x := a.Insert("x")
fmt.Println(a.Get(x), a.Remove(x), a.Get(x))   // Some("x") Some("x") None
```

## rc

`rc.Rc<T>` gives one value several owners within a task. `rc.New(v)` takes the value and returns the first handle; `Clone` makes another handle on the same value, not a copy of it; `Get` lends the value to any handle for reading. A handle prints as the value it holds, so does a `Weak` while the value is there, and a struct holding either prints its value among its fields; a `Weak` whose value is gone prints `rc.Weak()`. Neither handle has a zero value; a link that may point at nothing is a `rc.Weak<T>?`. The value lives until its last handle goes, and a type with a `Drop` method shows when that is:

```go
x := rc.New(Loud{name: "x"})
{
	y := x.Clone()
	fmt.Println(y.Get().name, y.StrongCount())   // x 2
}
fmt.Println(x.StrongCount())                     // 1
```

A handle is an owned value like any other: it is moved, not copied, and a struct or a slice holds one as it holds a string. `GetMut` lends the value for changing only while the handle is the one handle, strong or weak, and answers `None` otherwise, because a change through one handle while another reads is what chapter 6 forbids; a value that must change while shared is kept in an arena instead. `Downgrade` gives a `Weak<T>`, a handle that does not keep the value alive: `Upgrade` returns an `Rc` while the value is there and `None` once its last `Rc` is gone, which is how a link back to a value another handle owns is written. That rule also means a cycle of handles cannot be built, since the link that would close it is set through `GetMut` on a handle that is no longer alone, and a lock, chapter 10's other way to change a shared value, holds only a value that may be sent, which a handle is not; so a value in an `Rc` is always released. A handle has no zero value, chapter 18: `var r rc.Rc<T>` is refused, a handle comes from `New`, `Clone` or `Downgrade`, and one that may point at nothing is a `rc.Weak<T>?`. An `Rc` is for one task; its count is a plain number, and chapter 10 will have `sync.Arc` for the other case.

The package is written in Snello over two functions the compiler provides, which copy a pointer and forget one. They are the package's own, not a program's; the count is what makes them safe, and the package is the one place the count is kept.

## bufio

`bufio` puts a buffer between the program and a file. Each of its types borrows the reader or writer under it, `mut`, for as long as it lives, so a `bufio.Writer` is a borrow-holding struct in the sense of chapter 6: lent to calls and dropped at the end of its scope, but not stored or sent. It implements `io.Writer`, so `fmt.Fprintln` and `io.Copy` write through it, and nothing reaches the file until the buffer fills or `Flush` is called, which a program defers right after making the writer.

Since a `Reader` or `Writer` holds what it wraps `mut`, the program cannot reach that value directly while the buffer lives, where Go's program keeps using it beside the buffer. `Get` lends it back through the buffer: a server reading a connection line by line through a `Reader` answers on the same connection with `r.Get().WriteString(...)`. Once the buffer's last use is past, the value is the program's again.

```go
stdout := os.Stdout()
out := bufio.NewWriter(mut stdout)
defer out.Flush()
fmt.Fprintln(mut out, "two", 2)
out.WriteByte('\n')
```

A `Scanner` reads lines: `Scan` reads the next and says whether there was one, `Bytes` lends it without its ending, a newline or a carriage return and a newline, and `Text` copies it. A last line without a newline is a line; an empty file has none; a failed read ends the scan, after what was read before it is given as the last line, and `Err` says why. A line is at most `bufio.MaxScanTokenSize`, 64 KiB, unless `sc.Buffer(n)` sets another limit; a longer one ends the scan with `bufio.Scanner: token too long`, so input a program does not control cannot make it hold a line without bound. The limit holds whatever the reader under it gives at a call, a file's whole block included, since the scanner reads no more than the limit leaves room for, as Go's does. `Bytes` borrows the scanner, so a line is used, or copied with `Text`, before the next `Scan`. A `Reader` hands out bytes, text up to a delimiter, or what an `io.Reader` is asked, from blocks it reads ahead; at the end of the input `ReadString` gives what was left and then `None`, and a later call asks the file again, so a log another program appends to is read on as it grows. A failure costs no data. A `Writer` keeps the first error it meets and returns it from every later `Write` and `Flush`, an empty write included, and a byte the file took is never written again; `NewWriterSize(w, n)` sets the buffer's size, and a size of zero or less is `NewWriter`'s 4096; a `Reader` whose file fails in the middle of `ReadString` keeps what it had read, and the next call returns it with what follows. `compiler/emit/internal/backendtest/testdata/failures.snello` shows all three.

```go
sc := bufio.NewScanner(mut f)
for sc.Scan() {
	fmt.Println(sc.Text(), len(sc.Bytes()))
}
```

`bufio.Words(mut r)` makes a `Scanner` that reads words instead: the runs between white space, as Go's `ScanWords` splits them, white space being what `unicode.IsSpace` says it is. It is a second constructor where Go has a `Split` method, so a `Scanner` reads one kind of piece from its first `Scan` to its last, where Go's `Split` panics when called after scanning starts. The white space before a word never fills the buffer, and a word past the limit ends the scan as a long line does. A word read in many small pieces is decoded once: the search goes on from where the last stopped, where Go's `ScanWords` decodes the word again from its start after every read, which made a word of 64 KiB read a byte at a time cost Go's about two billion rune decodes.

A `Reader` also has `Peek(n)`, which lends the next `n` bytes without reading them, fewer at the end of the input, and refuses a count below zero or past the buffer's 4096 bytes with Go's errors, `bufio: negative count` and `bufio: buffer full`. The bytes are the buffer's, lent until the `Reader` is next asked anything, which the compiler holds a program to, where Go's documentation says they stop being valid at the next read. `ReadBytes(delim)` is `ReadString` giving bytes. `UnreadByte` puts back the last byte read, by `Read`, `ReadByte`, `ReadRune`, `ReadString` or `ReadBytes`, and returns `false`, changing nothing, before any byte was read and after a `Peek`, an `UnreadRune` or another `UnreadByte`. It is Go's rule to the letter: a read that gives nothing at the end of the input leaves the last byte read where it was, so `UnreadByte` still puts it back, and so does a `Peek` refused for its count. `UnreadRune` is stricter: it puts the rune back only when the call before it was the `ReadRune` that read it, and returns `false` after any other call. That is what Go's documentation says of its own; Go's implementation lets a few other calls through, a `Peek` refused for its count and some reads that give nothing, and Snello's none.

```go
r := bufio.NewReader(mut in)
match r.Peek(4) {
	Ok(head) => fmt.Println(string(head)),   // REC1, still to be read
	Err(e) => return Err(e),
}
```

Passing a buffered writer to `fmt.Fprintln`, which is generic over its writer, is what made the one exception to chapter 12's rule on type arguments: a function that only borrows a type parameter's values may take a type argument that holds a borrow.

## net

A server is a `Listener` and a task per connection. Each connection is written as blocking code, a read that waits for the client's bytes, and runs as event-driven code: a read with nothing to read parks the task, and the runtime's poller wakes it when the bytes come, so ten thousand idle connections are ten thousand parked tasks and no threads. A connection is an `io.Reader` and an `io.Writer`, so `bufio` and `io.Copy` work over it as over a file.

A server must not let a client that stops sending hold its task for ever. A deadline, as in Go, is a point in time after which a read or a write that has not finished fails instead of waiting, with an `io.Error` of Kind `io.TimedOut` that reads as Go's, `read tcp 127.0.0.1:50338->127.0.0.1:8080: i/o timeout`. It holds for every read and write from when it is set until it is moved, so one deadline bounds a whole request however many reads it takes, and a server sets one per request:

```go
func serve(c net.Conn) {
	buf := make([]byte, 4096)
	for {
		c.SetReadDeadline(time.Now().Add(30 * time.Second)).Expect("deadline")
		n := match c.Read(mut buf) {
			Ok(n) => n,
			Err(e) => {
				if e.Kind() == io.TimedOut {
					fmt.Println("idle client dropped")
				}
				return
			},
		}
		if n == 0 {
			return // the client closed its side
		}
		c.Write(buf[0:n]).Expect("echo")
	}
}
```

`SetDeadline` sets the reads' and the writes' together, `SetReadDeadline` and `SetWriteDeadline` one of them, and a `Listener`'s `SetDeadline` bounds `Accept`. The zero `Time`, `var none time.Time`, clears a deadline; one that has passed fails every operation at once until it is set again. `DialTimeout(host, port, timeout)` gives up on a connection not made in time. A write whose deadline passes partway, or that a broken connection or a full disk cuts short, has written the first `e.Written()` bytes, the count Go's `Write` returns beside its error.

## Binding C from a package

A package of your own may reach C as the standard library reaches its runtime, a function declared without a body and defined in C beside the package, with a system library named by one line in the module file. Chapter 15 is the whole of it.
