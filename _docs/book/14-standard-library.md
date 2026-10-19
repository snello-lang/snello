# 14. The standard library

Status: the library is written in Teak, one package per directory under `std/`, built into the compiler and compiled with the program that imports it (stage 2, task 2.6): `math` first, then `io` and `os`, then `strings` and `strconv`, then `sort` and `time`, then `arena` and `rc`, then `bufio`. Every package is declared under `std/`, `fmt` included: its functions are declared without bodies, since the compiler writes the printing by the type of each argument, and `os.Exit` is declared the same way. The samples are cut from `examples/run/library.teak`, `files.teak`, `text.teak`, `order.teak`, `arena.teak`, `shared.teak` and `buffered.teak`, and from `examples/07-traits.teak` and `14-files.teak`.

Teak predeclares little: the basic types, `Option` and `Result`, the methods of slices, maps and strings, the built-in functions and the standard traits. Everything else lives in a package, and a program names what it uses through an import, as the earlier chapters did with `fmt`:

```teak
import ("fmt"; "math")
```

## Importing a package

An import names a package by its path, and the program refers to the package's members through the last element of the path and a dot: `math.Pi`, `math.Sqrt(x)`. A different name may be given, `import m "math"`, and the members are then `m.Pi` and `m.Sqrt`. Only the exported names of a package are visible, the ones starting with a capital letter; the rest are the package's own. Importing a path the library does not have is reported at the import, `package heap is not in the standard library`, and nothing further is said about the names the program takes from it.

What needs no import is documented too: `std/builtin/builtin.teak` declares the basic types, `Option` and `Result` with their methods, the methods of slices, maps and strings, the built-in functions and the standard traits, each with its comment, as Go's package `builtin` does. Nothing in it is compiled, and `import "builtin"` is refused, `package builtin is not imported: its names are predeclared`; a test keeps every signature in it in step with the compiler's own.

A library package is ordinary Teak, compiled with the program into the same binary, so a library function costs what a function of the program costs, and the compiler sees through the call as it sees through any other. A library function is a value like any function:

```teak
func apply(f func(f64) f64, x f64) f64 { return f(x) }

fmt.Println(apply(math.Sqrt, 2) > 1.41)   // true
```

A few library functions are not written in Teak: the runtime provides them in C, and the package declares them without a body, `func Sqrt(x f64) f64`; the compiler provides `fmt`'s and `os.Exit`, declared the same way. A program cannot do the same; a function without a body is `missing function body`. A library's types are named through the package too, `io.Error`, `os.File`, in a signature or an `impl` list alike, and only their exported fields and methods are reachable from outside the package.

## The packages

| Package | What it has |
|---|---|
| `fmt` | `Println`, `Printf`, `Sprintf`, `Sprint`, and `Fprintln` and `Fprintf` writing to a `mut io.Writer`. A format knows `%d`, `%s`, `%v`, `%q`, `%%` and `%f` with a precision, `%.2f`, and a width before the verb, `%6d`, or `%-6s` to pad on the right |
| `io` | `Error` with `Error()` and `Kind()`, the `Kind` enum, the `Reader` and `Writer` traits, `Copy` |
| `os` | `Args`, `Stdin`, `Stdout`, `Stderr`, `File` with `Name`, `Read`, `Write`, `WriteString`, `Sync` and `Close`, `Open`, `Create`, `ReadToString`, `MkdirTemp`, `RemoveAll`, `Exit` |
| `math` | `Pi`; `Sqrt`, `Abs`, `Min`, `Max`, over `f64` |
| `strings` | `Builder` with `WriteString`, `WriteByte`, `Len`, `String` and `Reset`; `Repeat`, `Join`, `ToLower`, `ToUpper`, `Fields` |
| `strconv` | `Atoi`, `Itoa`, `ParseFloat`, `FormatFloat`, and `Error` with `Func`, `Text` and `Msg` |
| `sort` | `Sort` and `Search` over any `Ord` element, `SortBy` with an order of the program's own, `IsSorted` |
| `time` | `Duration` with the constants `Nanosecond` to `Hour` and its readings, `Time` with `Add`, `Sub`, `Before`, `After`, `Unix` and `UnixNano`, `Now`, `Unix`, `Since`, `Sleep` |
| `arena` | `Arena<T>` with `Insert`, `Get`, `GetMut`, `Remove` and `Len`, `Index<T>`, `New`; chapter 13 |
| `rc` | `Rc<T>` with `Get`, `GetMut`, `Clone`, `Downgrade`, `StrongCount` and `WeakCount`, `Weak<T>` with `Upgrade` and `Clone`, `New` |
| `bufio` | `Writer` with `Write`, `WriteString`, `WriteByte`, `Flush` and `Buffered`, `Reader` with `Read`, `ReadByte` and `ReadString`, `Scanner` with `Scan`, `Bytes`, `Text` and `Err`; `NewWriter`, `NewWriterSize`, `NewReader`, `NewScanner` |

Every package the examples import is written now, but for `regexp` and `sync`, which wait for later stages; `time.After` and `Tick` come with channels, and a range over an arena or a scanner's lines with the iterator trait.

## io and os

Every `os` function that can fail returns a `Result` whose error is an `io.Error`: the operation that failed, the path or stream it was on, and the system's reason. It prints as one line, `open /nonexistent: no such file or directory`, because a type implementing the `Error` trait prints as its `Error()` message wherever a value is printed, inside an `Err(...)` as well. A program that must tell one failure from another asks `e.Kind()`, which is `NotExist`, `Permission`, `Exists` or `Other`.

```teak
func copy(src ref string, dst ref string) Result<i64, io.Error> {
	in := os.Open(src)?
	out := os.Create(dst)?
	n := io.Copy(mut out, mut in)?
	out.Close()?
	in.Close()?
	return Ok(n)
}
```

An `os.File` implements `Drop`: a file that goes out of scope is closed, on every path out of the function. `Close` consumes the file and reports the failure, which `Drop` cannot, so a program that cares about the error calls `Close` and one that does not lets the file go. The three standard streams are files too, open when the program starts: `os.Stdout.WriteString("direct\n")`.

`io.Reader` and `io.Writer` are traits with one method each, `Read(buf mut []byte)` and `Write(buf ref []byte)`, and `os.File` implements both; a program's own types may too, and then `io.Copy` and `fmt.Fprintln` work on them. `io.Copy` is a generic function over the two traits, `Copy<W: Writer, R: Reader>(dst mut W, src mut R)`, so a copy between two files is one copy of the function, with the reads and writes called directly. A writer that takes fewer bytes than it was given is asked again for the rest.

## math

`math` works over `f64`, as `Pi` is one and every function takes and returns one; a program with another float type converts at the call.

```teak
func (c ref Circle) Area() f64      { return math.Pi * c.r * c.r }
func (c ref Circle) Perimeter() f64 { return 2 * math.Pi * c.r }
```

```teak
fmt.Println(math.Sqrt(16), math.Abs(-3.5), math.Min(2, 7), math.Max(2, 7))   // 4 3.5 2 7
fmt.Printf("%.2f %.0f %f\n", math.Pi, math.Pi, 1.5)                        // 3.14 3 1.500000
```

The literals `2` and `7` take the type the parameters ask for, as chapter 3 says, so `math.Min(2, 7)` needs no `2.0`.

## strings and strconv

What a string can do on its own is a method of the string, chapter 9: `s.Len()`, `s.Fields()`, `s.ToLower()`. The `strings` package has what works on more than one string or builds a new one piece by piece, and the same transformations as functions, for a call that reads better that way or a function value to pass along.

```teak
var b strings.Builder
b.WriteString("tea")
b.WriteByte('k')
fmt.Println(b.String(), b.Len())                          // teak 4
fmt.Println(strings.Repeat("ab", 3), strings.Join(ref parts, ", "))   // ababab x, y, z
```

A `Builder` accumulates bytes; its zero value is ready to use, `String()` gives the text so far as a new string, and `Reset()` empties it. `Join` takes a borrowed slice of strings and a separator.

`strconv` reads numbers from text and writes them back. Reading can fail, so `Atoi` and `ParseFloat` return a `Result` whose error is a `strconv.Error`, with the function, the text and the reason, printing as `strconv.Atoi: parsing "4x": invalid syntax`. `Itoa` and `FormatFloat` take one argument and write the number as `fmt.Println` would.

```teak
fmt.Println(strconv.Atoi("42"), strconv.Atoi("4x"))
// Ok(42) Err("strconv.Atoi: parsing \"4x\": invalid syntax")
match strconv.Atoi("x") {
	Ok(n) => fmt.Println(n),
	Err(e) => fmt.Println(e.Func, e.Text, e.Msg),            // Atoi x invalid syntax
}
```

`Atoi` is written in Teak, digit by digit with the overflow check; `ParseFloat` asks the runtime to read the number, as a literal is read.

## fmt

`fmt` is the one package the compiler writes itself: `Println` and its kin take arguments of any type, `func Println(args ...ref any)` as `std/fmt/fmt.teak` declares it, and the compiler chooses how to print each one from its type, so no formatting happens at run time and the format of `Printf` is read when the program is compiled. `...ref any` is the library's spelling for that; a program's own function takes arguments of the types it names. `Fprintln` and `Fprintf` write where they are told, to any value implementing `io.Writer`, lent `mut`:

```teak
fmt.Fprintln(mut os.Stdout, "to", "stdout", 1)                       // to stdout 1
fmt.Fprintf(mut os.Stdout, "%5d|%-4s|%3s|%2.1f|\n", 42, "ab", "abcdef", 2.25)   // "   42|ab  |abcdef|2.2|"
```

A width pads with spaces to the left of the value, or to the right after a minus, counting characters rather than bytes, so `é` is one; a value already wider is printed whole. `Fprintln` writes the text with one call of the writer's `Write`, and says nothing about a failure, as Go programs rarely look at one.

## sort and time

`sort.Sort` orders a slice in place, least first, by the `Cmp` of its element type, so it takes any slice whose elements are `Ord`: numbers, strings, and a struct or enum that lists `Ord` and gets `Cmp` derived from its fields, chapter 11. `SortBy` takes the order from a function instead. Both lend the slice `mut` and move its elements within it; nothing is copied. `Search` finds a value in a sorted slice, or the index where it would go.

```teak
sort.Sort(mut nums)
fmt.Println(nums, sort.IsSorted(ref nums), sort.Search(ref nums, 4))   // [1 3 4 5 8] true 2
sort.SortBy(mut scores, func(a ref Score, b ref Score) bool { return a.name < b.name })
```

`time.Duration` is a span of time in nanoseconds, a number type declared over `i64`, so it adds, compares and multiplies by an integer as a number does, and the constants let one be written as a product: `10 * time.Millisecond` is a `Duration` because the literal takes its type from the constant beside it. `time.Time` is an instant, a `Copy` struct that compares with `==` and `<` and has `Add`, `Sub`, `Before` and `After`. `Now` reads the clock, `Since` measures from a `Time`, and `Sleep` waits.

```teak
start := time.Now()
time.Sleep(2 * time.Millisecond)
fmt.Println(time.Since(start) >= 2*time.Millisecond)   // true
```

A `Duration` prints as its count of nanoseconds and a `Time` as its fields, for now.

## arena

`arena.Arena<T>` owns values in slots and hands out an `arena.Index<T>` for each, a `Copy` value of two numbers that a struct, a slice or a map holds where a pointer would go. `Get`, `GetMut` and `Remove` answer `None` for an index whose value was removed, however many values its slot has held since, because every slot carries a generation that a removal raises and an index remembers the one it was issued with. The package is generic Teak and nothing else, a slice of slots, a free list and a count. Chapter 13 is about it.

```teak
a := arena.New<string>()
x := a.Insert("x")
fmt.Println(a.Get(x), a.Remove(x), a.Get(x))   // Some("x") Some("x") None
```

## rc

`rc.Rc<T>` gives one value several owners within a task. `rc.New(v)` takes the value and returns the first handle; `Clone` makes another handle on the same value, not a copy of it; `Get` lends the value to any handle for reading. The value lives until its last handle goes, and a type with a `Drop` method shows when that is:

```teak
x := rc.New(Loud{name: "x"})
{
	y := x.Clone()
	fmt.Println(y.Get().name, y.StrongCount())   // x 2
}
fmt.Println(x.StrongCount())                     // 1
```

A handle is an owned value like any other: it is moved, not copied, and a struct or a slice holds one as it holds a string. `GetMut` lends the value for changing only while the handle is the one handle, strong or weak, and answers `None` otherwise, because a change through one handle while another reads is what chapter 6 forbids; a value that must change while shared is kept in an arena instead. `Downgrade` gives a `Weak<T>`, a handle that does not keep the value alive: `Upgrade` returns an `Rc` while the value is there and `None` once its last `Rc` is gone, which is how a link back to a value another handle owns is written. That rule also means a cycle of handles cannot be built, since the link that would close it is set through `GetMut` on a handle that is no longer alone; so a value in an `Rc` is always released. The zero value of a handle is empty: it counts nothing, clones to another empty handle, and `Get` on it panics. An `Rc` is for one task; its count is a plain number, and chapter 10 will have `sync.Arc` for the other case.

The package is written in Teak over two functions the compiler provides, which copy a pointer and forget one. They are the package's own, not a program's; the count is what makes them safe, and the package is the one place the count is kept.

## bufio

`bufio` puts a buffer between the program and a file. Each of its types borrows the reader or writer under it, `mut`, for as long as it lives, so a `bufio.Writer` is a borrow-holding struct in the sense of chapter 6: lent to calls and dropped at the end of its scope, but not stored or sent. It implements `io.Writer`, so `fmt.Fprintln` and `io.Copy` write through it, and nothing reaches the file until the buffer fills or `Flush` is called, which a program defers right after making the writer.

```teak
out := bufio.NewWriter(mut os.Stdout)
defer out.Flush()
fmt.Fprintln(mut out, "two", 2)
out.WriteByte('\n')
```

A `Scanner` reads lines: `Scan` reads the next and says whether there was one, `Bytes` lends it without its ending, a newline or a carriage return and a newline, and `Text` copies it. A last line without a newline is a line; an empty file has none; a failed read ends the scan and `Err` says why. `Bytes` borrows the scanner, so a line is used, or copied with `Text`, before the next `Scan`. A `Reader` hands out bytes, text up to a delimiter, or what an `io.Reader` is asked, from blocks it reads ahead.

```teak
sc := bufio.NewScanner(mut f)
for sc.Scan() {
	fmt.Println(sc.Text(), len(sc.Bytes()))
}
```

Passing a buffered writer to `fmt.Fprintln`, which is generic over its writer, is what made the one exception to chapter 12's rule on type arguments: a function that only borrows a type parameter's values may take a type argument that holds a borrow.
