# Appendix B. Snello for Go programmers

Status: grows with the chapters; covers chapters 2 to 9, 11, 12 and 14.

The chapters describe Snello on its own terms and name no other language. This appendix is for readers who come from Go: for each chapter, what carries over unchanged and what to unlearn. Snello is not a Go dialect, and Go programs are not Snello programs; the syntax is familiar so that the ideas get your full attention.

## Chapter 2: Packages and modules

| Go | Snello |
|---|---|
| A directory is a package; its files share one package clause | The same |
| `go.mod` with `module`, `go`, `require path version`, `replace path => dir` | `snello.mod` with `require name dir`, and nothing else: no module name, and a requirement names a directory, as Go's `replace` does |
| An import path is found in the standard library, then in the module, then through `replace` and the module cache | The standard library, then the module, then the `require` lines; there is no cache and nothing is fetched |
| `import "example.com/app/shapes"`, the module path in every import | `import "shapes"`: a directory under the root, or a required name; a module has no path |
| `go build` in a directory, or `go run file.go` for one file | `snello build DIR`, or `snello build FILE` for one file |
| A method is declared in the package of its receiver's type | The same |
| Functions implemented in assembly are declared without a body; cgo binds C through `import "C"` and `#cgo LDFLAGS: -lm` comments | A package declares a function without a body and defines it in C beside the package, under the symbol the compiler derives and the header it writes (chapter 15); the main package may not; a system library is `link m` in `snello.mod` |

## Chapter 3: Basics

Unchanged: `package`, `import`, `:=`, `var`, zero values, exported names by capitalization, `func` with named parameters and grouped types, multiple results, closures, `if`, `for` and its range forms, `switch`, `defer`, `len`, `cap`, `make`, `panic`, semicolon insertion.

| In Go | In Snello |
|---|---|
| `int32`, `float64`, `uint8` | `i32`, `f64`, `u8`; `int`, `uint`, `byte`, `rune` as before |
| Untyped constants: `x := 10 * 2.5` | A literal takes its type from context; mix types with a conversion |
| `s[a:b]` copies a string header; the bytes are shared by the GC | `s[a:b]` lends a `ref string`; the original must outlive it |
| `strings.ToLower(s)`, `strings.Fields(s)` | `s.ToLower()`, `s.Fields()`; the package has the functions too (chapter 14) |
| `go f(x)` | `spawn f(x)` |
| `func TestX(t *testing.T)`, `t.Errorf`, `t.Fatalf`, `t.Run`, `t.Log`, `t.Skip`, `t.TempDir`, `t.Cleanup` | `func TestX()` with no handle; `expect.Equal(ref got, ref want)` and the other assertions fail by a panic the runner catches; `testing.Run`, `testing.Log`, `testing.Skip`, `testing.TempDir()` whose `Drop` is the cleanup; record-and-continue is not kept, the first failed expectation ends the test |
| `go test ./...`, `go test -v -run Name` | `snello test DIR`, `snello test -v -run Name DIR`; one package at a time |
| `testify`'s `assert.Equal`, `go-cmp`'s diff | `expect.Equal`, printing got and want and the first difference in words; no library needed |
| `go test -race` | Nothing: a data race is a compile error in Snello, so there is no detector (chapters 10 and 16) |
| `func BenchmarkX(b *testing.B)`, `for b.Loop()`, `b.ReportAllocs()`, `go test -bench .` | `func BenchmarkX()`, `for testing.Loop()`, `testing.ReportAllocs()`, `snello test -bench . DIR`; the line printed is Go's |
| `break` inside `switch` or `select` ends that statement; a labeled `break outer` for the loop | `break` always ends the innermost loop; there are no labels |
| `if x := f(); x > 0 {` | Same. `if x := opt {` with no `;` is a binding on an optional |
| `append`, `delete`, `copy`, `new` | `xs.Push(v)`, `m.Remove(k)`, `xs.Clone()`, `&expr` |
| `nil` | None. An absent value is `T?` |
| `a, b := f()` for two results | Same; several results are a tuple, `return (n, right)` |
| An `if` is a statement | An `if` with an `else` is also an expression |
| A newline after `>` continues the line | A newline after `>` ends the line |

## Chapter 4: Structs, enums and match

Unchanged: struct fields, keyed literals, dot access, methods with receivers, `type Name T`.

| In Go | In Snello |
|---|---|
| `type Node struct { ... }` | `struct Node { ... }` |
| Value and pointer receivers, `(t Tree)` and `(t *Tree)` | Three receivers: `(t ref Tree)` reads, `(t mut Tree)` changes, `(t Tree)` takes |
| A `const` block with `iota`, or an interface with one struct per case | `enum`, with fields per variant |
| A type switch | `match`, exhaustive, usable as an expression |
| `switch` on an enum constant | `match`; a missing case is a compile error |
| `v, ok := m[k]` | `if v := m.Get(ref k) {` |
| `struct{}` and anonymous structs | None: a struct is declared; a value with nothing in it is `()` |

## Chapter 5: Ownership

Nothing here has a Go counterpart; the garbage collector made the questions disappear. What to unlearn:

| In Go | In Snello |
|---|---|
| Assignment copies the value or the pointer; both names stay valid | Assignment moves; the old name is dead unless the type is `Copy` |
| Passing a slice or a struct copies its header or its bytes | Passing by value moves; pass `ref` or `mut` to keep it |
| `defer f.Close()` | A file closes itself when dropped; `f.Close()` consumes it and returns the error |
| Finalizers run eventually, or never | `Drop` runs at scope exit, early return and panic, in order; a deferred call does not run on a panic |
| `x := xs[0]` copies the element | `xs[0]` lends the element; `xs.Remove(0)`, `xs.Pop()` or `take` moves it |
| `tmp := head; head = head.next` | `old := take(mut s.head)` |
| Copy a struct and hope it had no shared state | `s.Clone()` is the only copy, and it is explicit |

### Where the model comes from

The ownership model is not Snello's invention. One owner per value, borrows that are either many readers or one writer, and a `Drop` that runs when the owner is done are the model Rust made mainstream, and before Rust they were the linear types of academic languages. A Rust programmer recognizes Snello's chapters 5 and 6 at once, and that is deliberate: it is the one model with a decade of proof on real systems, and it is what lets Snello drop the garbage collector that Go's simplicity leans on.

Where Snello differs is in what it leaves out, and this is the bet stage 1 of the compiler was built to test:

| In Rust | In Snello |
|---|---|
| Lifetime annotations, `fn f<'a>(x: &'a T) -> &'a U`, say which input a returned borrow comes from; the hardest part of the language to learn | None. A returned borrow comes from the receiver or from the only borrowed parameter; a function where that is ambiguous is refused, and you return a clone or an index instead |
| A borrow is a first-class value: it can live in a struct, a `Vec`, a channel, and the type system tracks its lifetime through all of them | A borrow lives inside one function and cannot be stored, sent or captured by a task; a long-lived structure that points into another uses an arena with indices (chapter 13) |
| `&x`, `&mut x` at every call site; `x.method()` borrows implicitly | `f(x)` lends for reading with no keyword, `f(mut x)` for writing; `ref` and `mut` on the parameter say the rest |
| Programs that need a lifetime are written with one | Those programs are refused; the eight core programs, written to exercise the hard cases, never needed one |

The restriction on borrows is real, and it is what makes the absence of annotations possible: when a borrow cannot leave its function, every lifetime can be inferred or the program rejected, with nothing to write either way.

## Chapter 6: Borrows

| In Go | In Snello |
|---|---|
| `func f(r *Rect)`: no copy, and maybe mutated, maybe kept, maybe nil | `func f(r ref Rect)` reads, `func f(r mut Rect)` changes; neither can keep it or be nil |
| `f(&x)` | `f(mut x)` to change `x`; `f(x)` or `f(ref x)` to read it |
| `p.field` through a pointer; `*p = v` to replace | The same: `n.value` through a borrow; `*cur = v` to replace |
| Append under a live pointer reallocates silently | `xs.Push(v)` while an element borrow is live is a compile error |
| Modify a map during `range`: a runtime surprise | A compile error: a write during a read |
| `if v, ok := m[k]; ok { v.n++ }` changes a copy, silently | A binding through a place copies what `:=` copies; to change it where it lies, write `match mut xs[0] { Full(n) => *n += 1 }` |
| `v, ok := o.Get()` leaves `o` usable | `o.Expect(msg)` takes `o`: a value handed out is not kept |
| Return a pointer to a local; the GC keeps it alive | `cannot return a borrow of local s`; return the value |
| A closure capturing a local by reference, returned from `counter()` | Rejected; put the state in a struct with a method |
| A struct holding a `*string` into a buffer | A struct holding `ref string` is itself a borrow: returned only like a borrow, never stored |
| Pass the same slice to five goroutines | `a spawned task cannot hold a borrow`; move it, clone it, or share it through `sync.Arc` |

## Chapter 9: Collections

| In Go | In Snello |
|---|---|
| `xs = append(xs, v)`, and remember to assign the result | `xs.Push(v)`; the slice grows in place, since it owns its elements |
| A slice is a view; two slices may share an array and see each other's writes | A slice owns its elements; `xs[lo:hi]` is a borrow, and the borrow rules keep the two apart |
| `var xs []int` is `nil` and `len(xs) == 0` | A slice declared without a value is empty; there is no `nil` |
| `xs[i]` on a bad index panics | The same, with the same message |
| `copy(dst, src)`, `sort.Ints(xs)`, `slices.Contains(xs, v)` | `xs.Clone()`, `xs.Sort()`, `xs.Contains(v)`: methods on the slice |
| `strings.Fields(s)` returns new strings | `s.Fields()` yields borrows into `s`, and a loop over them lends `s` |
| `strings.TrimSpace(s)` | `s.Trim()`, a borrow of the middle of `s`; Go's `strings.Trim`, with the bytes to cut, has no counterpart |
| `strings.Lines(s)` keeps each newline | `s.Lines()` drops it, and a carriage return before it |
| `for i, r := range s` yields runes | The same, and an invalid byte reads as U+FFFD and moves on one byte; the string is lent for the loop (chapter 19) |
| `utf8.EncodeRune(p, r)` writes into a buffer of fixed size | `utf8.AppendRune(mut p, r)` appends, growing the slice (chapter 19) |
| `strings.ToUpper` maps each rune by the simple mapping: `"ß"` stays `"ß"` | Full case mapping: `"ß"` upper-cases to `"SS"`, and a final capital sigma lowers to `ς` (chapter 19) |
| `strings.EqualFold` compares by simple folding: `"ß"` and `"ss"` differ | `strings.EqualIgnoreCase` compares by full folding: they are equal (chapter 19) |
| `unicode.SimpleFold(r)` walks a rune's case orbit | `unicode.CaseFold(r)` gives the one rune its case set folds to |
| `strings.ToTitle` maps every letter to title case, `"ǆemal"` to `"ǅEMAL"` | `strings.ToTitle` title-cases by words, Unicode's way: `"ǆemal"` to `"ǅemal"`, `"HELLO wORLD"` to `"Hello World"` (chapter 19) |
| `unicode.IsUpper` is category Lu | `unicode.IsUpper` is the derived `Uppercase` property, `Ⓐ` included; `unicode.Category(r) == "Lu"` is Go's |
| `string(65)` is `"A"`, and `go vet` warns | `string(r)` takes a `rune` only; `string(65)` is refused (chapter 19) |
| `string(rs)` and `[]rune(s)` | `string(rs)` the same; `s.Runes()` for the runes |
| `unicode.Is(unicode.Latin, r)` over a table | `unicode.InScript(r, "Latin")`, and `unicode.Script(r)` names it |
| `golang.org/x/text/unicode/norm`, outside the standard library | `unicode/norm`, in it (chapter 19) |
| Grapheme clusters, words and sentences need a module | `unicode/segment`, in the standard library (chapter 19) |
| `[]byte(s)` copies | `[]byte(s)` converts the owned string, and back |
| `v, ok := m[k]` | `m.Get(k)` is a `ref V?`; `m[k]` alone reads the zero value for a missing key, as in Go |
| `delete(m, k)` | `m.Remove(k)` returns the value as `V?` |
| Map iteration order is random | Insertion order |

## Chapter 7: Owned pointers

| In Go | In Snello |
|---|---|
| `&x` takes the address of a variable; the GC keeps it alive | `&expr` moves the value to the heap; the pointer owns it and nothing else may point there |
| `p := &Node{}`; copying `p` makes a second pointer to the same node | A pointer moves; there is one owner, so no two names reach one node |
| `*p` to read through, `p.field` as a shortcut | `p.field` the same; `*p` only to read or replace the whole value |
| `next *Node` is `nil` at the end of a list | `next *Node?` is `None` at the end; there is no `nil` |
| `p == q` compares addresses | `p == q` compares what they hold |
| A cycle of pointers is collected eventually | A cycle cannot be built from owned pointers; use an arena (chapter 13) |

## Chapter 10: Concurrency

Unchanged: `make(chan T)`, buffered channels, `ch <- v`, `<-ch`, `close`, `for range ch`, `select`, a task is a function started and forgotten. Changed: a channel is not copied freely; a goroutine that shares one gets its own handle, `go produce(jobs)` is `spawn produce(jobs.Clone())`, since the last handle frees the channel and there is no collector to find it.

| In Go | In Snello |
|---|---|
| `go f(x)` | `spawn f(x)` |
| `done := make(chan struct{})`; `done <- struct{}{}` | `done := make(chan ())`; `done <- ()` |
| `for { select { case <-timer: break } }` loops forever: the `break` ends the `select` | The `break` ends the `for` |
| A goroutine may reach any value the spawning function can; sharing a map between two goroutines compiles and races | `spawn` moves what the task takes in; a second task cannot take the same value, and a borrow cannot cross at all |
| Sending copies the value; the sender keeps using it | Sending moves the value; the sender has given it up |
| `v, ok := <-ch` | `<-ch` is a `T?`, `None` once the channel is closed and drained |
| Whether a value may safely cross goroutines is the programmer's problem; `go vet` and the race detector find some cases at run time | `Send` is inferred from the type and checked at compile time at every crossing |
| `sync.Mutex` beside the data; forgetting to lock compiles | `sync.Mutex<T>` owns the data; the only way to it is through the guard `Lock` hands out, `*g.Get() += 1`, and the lock is released when the guard goes out of scope; `sync.RwLock<T>` the same with `Read` and `Write` |
| `var mu sync.Mutex`, `var once sync.Once`: the zero value is ready | `sync.NewMutex(v)`, `sync.NewRwLock(v)`, `sync.NewOnce()`, `sync.NewWaitGroup()`: a lock owns a value and is made with it; the zero value is empty and panics |
| `var wg sync.WaitGroup` passed by pointer, `&wg` | `sync.WaitGroup` is an owned handle on a counter, cloned for each task, `w := wg.Clone()`, like a channel, and freed by the last handle |
| `*rc`-style shared ownership does not exist; the GC shares everything | `rc.Rc` shares within one task and is refused at a task boundary; `sync.Arc` shares across tasks |
| `sync/atomic`: `atomic.Int64`, `atomic.Value`, `atomic.Bool` | `sync.Counter` for an `int` added to as one step; `sync.Atomic<T>` for any `Send`, `Clone` and `Eq` value read and replaced as one step, `Atomic<bool>` for a flag |
| A package-level `var` any goroutine may write; `func init()` runs before `main` | A package variable is read by every task and never assigned, lent as `mut` or moved out of; it changes through the `Counter`, `Atomic` or lock it holds; an initializer that is a call runs once, on first use, and a variable never used is never made (chapter 10) |
| `os.Stdout`, `os.Stderr`, `os.Stdin` are variables | `os.Stdout()`, `os.Stderr()`, `os.Stdin()` are functions giving a handle on the descriptor, since a package variable is never lent `mut` |

## Chapter 11: Traits

| In Go | In Snello |
|---|---|
| `type Shape interface { Area() float64 }` | `trait Shape { func (s ref Self) Area() f64 }`: the receiver says how the method borrows the value |
| A type satisfies an interface by having the methods | A type implements a trait by saying so, `struct Circle impl Shape { ... }`; the compiler then checks the methods |
| A struct with the right methods is a `Shape` wherever one is wanted | A struct that does not list `impl Shape` is not a `Shape`, whatever its methods |
| `type Solid interface { Shape; Volume(h float64) float64 }`: embedding | `trait Solid: Shape { func (s ref Self) Volume(h f64) f64 }`: refinement |
| `func TotalArea[T Shape](xs []T) float64`: a constraint | `func TotalArea<T: Shape>(xs ref []T) f64`: a bound; several are joined with `+` |
| `comparable`, `cmp.Ordered`, `any` | `Eq`, `Ord`, and no bound at all; `Copy` is a bound too |
| `var s Shape = Circle{}`, `[]Shape`: an interface value | The same, with the same cost: one allocation, one indirect call; the value is owned and moves, and a borrow cannot become one |
| No default methods; share code with embedding or a helper function | Default methods in the trait, `super`, and trait fields, all decided and to come |
| Type assertions and type switches on an interface value | Not in Snello: a trait value is used through its methods, and a `match` is for enums |

## Chapter 12: Generics

| In Go | In Snello |
|---|---|
| `type List[T any] struct { items []T }` | `struct List<T> { items []T }`: angle brackets, and no bound means no constraint |
| `List[int]{}` | `List<int>{}` |
| `func (l *List[T]) Push(v T)` | `func (l mut List<T>) Push(v T)` |
| `func (l List[int]) Sum()` declares a type parameter named `int` | Refused: a receiver argument must be a fresh name |
| A `T` value copies, whatever `T` is | A `T` value moves, unless the parameter is bounded `T: Copy` |
| `[T comparable]`, `[T cmp.Ordered]` | `<T: Eq>`, `<T: Ord>`: traits as bounds (chapter 11) |
| Operators only on the built-in types | `struct Vec2 impl Add, Mul<f64>` with `Add` and `Mul` methods: `a + b` is `a.Add(b)`, a direct call (chapter 11) |
| `func Map[T, U any](s []T, f func(T) U) []U` | `func Map<T, U>(l List<T>, f func(T) U) List<U>` |
| `Map[int, string](xs, f)`, or inferred | `Map<int, string>(l, f)`, or inferred, from the arguments and then from the type the result is assigned to |
| No methods with their own type parameters, because interface values call through a method table | `func (l ref List<T>) Conv<U>(...)` is fine; the one place it cannot be called is through a trait used as a type (chapter 11) |
| `Pair[int, string]{a: 1, b: "x"}`: a composite literal names its arguments | `Pair{a: 1, b: "x"}`: inferred from the fields |

## Chapter 14: The standard library

Unchanged: import paths and package names, `import m "math"` to rename, exported names by capitalization, `math.Pi`, `math.Sqrt`, `math.Abs`, `math.Min`, `math.Max`, how a float prints, and the verbs `fmt` has, `%v`, `%d`, `%s`, `%q`, `%f` and `%t`, with a width and a precision.

| In Go | In Snello |
|---|---|
| `float64` throughout `math` | `f64` throughout `math` |
| Each package is compiled once and linked | The library is compiled with the program, into one binary, so the C compiler inlines across the boundary |
| `GOROOT` holds the library's source | The library's source is inside the compiler; a build needs no path and no environment |
| A function with no body is implemented in assembly | A function with no body is the runtime's, in C; only the library may declare one |
| `os.ReadFile` returns bytes and an error | `os.ReadToString` returns `Result<string, io.Error>` (chapter 8) |
| `runtime.NumCPU()` | `os.Cores()`; there is no `runtime` package, and no `GOMAXPROCS`: the runtime runs one worker per processor |
| `os.Open` returns `(*File, error)`; `defer f.Close()` | `os.Open` returns `Result<File, io.Error>`; the file closes itself when it goes out of scope, and `Close()` consumes it to report the error |
| `*fs.PathError` with `Op`, `Path`, `Err`; `errors.Is(err, fs.ErrNotExist)` | `io.Error` with `Op`, `Path`, `Msg`; `e.Kind() == NotExist` |
| `io.Reader` and `io.Writer` are interfaces; `io.Copy(dst, src)` dispatches dynamically | `io.Reader` and `io.Writer` are traits; `io.Copy` is generic over them, one copy per pair of types |
| Printing an `error` prints `err.Error()` | The same, for any type implementing the `Error` trait, and for a value of type `Error`, which holds any error as `error` does |
| `func f() (T, error)`, `if err != nil { return nil, err }` | `func f() Result<T, Error>` and `x?`: an error of any type passes into the `Error` value (chapter 17) |
| `fmt.Errorf("load %s: %w", path, err)`, `errors.Unwrap`, `errors.Is` | A type with `Error()` for the message and `Source()` for the cause; `%+v` prints the chain; `Is` is a `match` on the enum |
| `errors.New("message")` for a one-off error | An enum variant, or a struct listing `Error`: the message is derived from the value |
| A `From` conversion, or wrapping by hand, at every `return err` | `?` wraps the error in the one variant of the function's error enum that holds its type |
| A type with a `String() string` method prints as it, a `fmt.Stringer` | A type prints as its `String` when it lists `impl fmt.Stringer`; traits are named, not matched by method |
| `go vet` warns of a verb that does not fit its argument, `%!d(string=x)` at run time | The format is checked when the program is compiled, and a misfit is an error; `fmt` takes no `%x`, `%e`, `%T` or flags but the minus and the plus on `%v` of an error yet |
| `err.(*strconv.NumError).Err == strconv.ErrRange` | `e.Kind() == strconv.Range` |
| `strconv.Atoi(s)` returns `(int, error)`; `*strconv.NumError` | `strconv.Atoi(s)` returns `Result<int, strconv.Error>`, with `Func`, `Text` and `Msg` |
| `strconv.FormatFloat(f, 'g', -1, 64)` | `strconv.FormatFloat(f)`: the shortest text that reads back, as `fmt` prints it |
| `strings.Builder` | The same, with `WriteString`, `WriteByte`, `Len`, `String` and `Reset` |
| `[]byte(s)` and `string(b)` copy | The same |
| `fmt.Fprintln(os.Stderr, ...)`, `fmt.Fprintln(w, ...)` with `w io.Writer` | `fmt.Fprintln(mut os.Stderr(), ...)`: the writer is lent `mut`, and must implement the `io.Writer` trait; the streams are functions, since a package variable is never lent `mut` |
| `fmt.Fprintln` returns `(n, err)` | `fmt.Fprintln` returns nothing |
