# Appendix B. Snello for Go programmers

The chapters describe Snello on its own terms. This appendix is for readers who come from Go: for each chapter, what carries over unchanged and what to unlearn. Snello is not a Go dialect, and Go programs are not Snello programs; the syntax is familiar so that the ideas get your full attention.

## Chapter 2: Getting started

| Go | Snello |
|---|---|
| A directory is a package; its files share one package clause | The same |
| `go.mod` with `module`, `go`, `require path version`, `replace path => dir` | `snello.mod` with `snello 0.1.0`, `require name dir`, `require name url version` and `link name`, and nothing else: no module name; a requirement names a directory, as Go's `replace` does, or a git repository and a version, which names the tag `vX.Y.Z` as Go's does |
| `go run file.go` builds into a temporary directory and runs it; a program's failure is reported as `exit status 3` and go's own status 1 | `snello run FILE` or `snello run DIR`, the arguments after the path the program's; the program's exit code is the command's, 3 for 3, and nothing is left behind |
| No command starts a project; `go mod init` writes `go.mod` alone | `snello new DIR` writes a module in a new directory: `snello.mod`, a `main.snello` and a `main_test.snello` that pass `snello test` |
| `go mod init example.com/app` writes `go.mod` in the current directory | `snello init` writes `snello.mod` in the current directory, or `snello init DIR` in another, holding the compiler's own version, `snello 0.1.0` from a 0.1.0 compiler, and no module name, since a module has none; a directory with a module file already is refused, as Go refuses one |
| `go 1.27.4` names the oldest Go that builds the module; a `go.mod` without the line is read as `go 1.16`, silently | `snello 0.1.0` names the oldest Snello that builds the module, a Semantic Versioning version, `0.2.0-rc.1` included; a file without the line is read as `0.1.0`, the first release, with a warning on every command that builds, checks or tests the module; a later version than the compiler's is refused, as Go refuses one with `GOTOOLCHAIN=local`, since nothing is fetched |
| An import path is found in the standard library, then in the module, then through `replace` and the module cache | The standard library, then the module, then the `require` lines: a directory, or a version fetched by `git` into the module cache, `SNELLO_MODCACHE`; no proxy and no checksum database |
| `go.sum` holds the `h1:` hash of the `go.mod` of every module version in the build, and of its files when a package of it is built, written by `go mod tidy` and `go get`, and a build missing one stops; a mismatch is a `SECURITY ERROR` naming two hashes | `snello.lock` holds one line a module reached by version, its commit and the `h1:` hash of its files, written by the build itself and checked on every build; a moved tag stops the build naming both commits; minimal version selection picks the highest version required, two versions that are not compatible, `1.x` and `2.x`, `0.2.x` and `0.3.x`, or `0.0.3` and `0.0.4`, are refused, and an import path never carries `/v2` |
| `go get example.com/re@latest` adds or changes a requirement, and `go get example.com/re@none` drops it | `snello add re URL [VERSION]` requires the latest release or the version named, and `snello remove re` drops it, refused while a file of the module still imports `re`; both leave `snello.mod` as it was when the fetch or the check fails (chapter 35) |
| `go get -u ./...` raises every dependency, the indirect ones too, within its major version | `snello update [NAME...]` raises what `snello.mod` requires to its latest compatible release, the one sharing its first number other than zero, as Cargo reads SemVer, where Go reads every `v0` as one; `-major` crosses majors, with no new import path to write |
| `go mod tidy` adds and drops requirements and `go.sum` lines from the imports | No such step: the build writes `snello.lock` itself, and `snello remove` drops a requirement once nothing imports it |
| `go mod graph` and `go mod why -m` | `snello deps`, each module with its selected version and who requires which, and `snello deps -why NAME` or `-why URL`, the chain from the program |
| `go mod download` fills the module cache; `go mod vendor` copies dependencies into the repository | `snello fetch` fills the module cache; there is no vendoring, a dependency is fetched and checked against the lock file |
| `import "example.com/app/shapes"`, the module path in every import | `import "shapes"`: a directory under the root, or a required name; a module has no path |
| `go build` in a directory, or `go build file.go` for one file | `snello build DIR`, or `snello build FILE` for one file |
| `GOOS=linux GOARCH=arm64 go build`, with cgo a `CC` for the target and its sysroot | `snello build -target linux/arm64`, a flag; the runtime is C, so another Linux takes lld and the target's C library, from a distribution's cross packages or a sysroot the target's variable names, `SNELLO_SYSROOT_linux_arm64`; Windows takes clang, `lld-link` and Microsoft's SDK and C runtime, from Visual Studio's Build Tools on Windows or a sysroot the target's variable names elsewhere, and writes `NAME.exe`, as Go does; macOS is built for on macOS alone |
| A method is declared in the package of its receiver's type | The same |
| `poll_linux.go`, `poll_arm64.go`, `poll_darwin_arm64.go` are built for their platform alone; `//go:build linux` chooses inside a file; a file named for a system Go knows and the build is not for is left out | The same file names, for `.snello`, `_test.snello`, `.c` and `.h` files, and the same list of words, with `unix` among them: `poll_unix.snello` is built for Linux and macOS and not Windows, where Go builds a `poll_unix.go` everywhere and takes `unix` only in a build line; no build line; a file for a platform Snello does not build for is left out, as Go leaves one out, and a name beginning with `.` or `_` is no file of the package (chapter 2) |
| Functions implemented in assembly are declared without a body; cgo binds C through `import "C"` and `#cgo LDFLAGS: -lm` comments | A package declares a function without a body and defines it in C beside the package, under the symbol the compiler derives and the header it writes (chapter 15); the main package may not; a system library is `link m` in `snello.mod` |

## Chapter 3: Basics

Unchanged: `package`, `import`, `:=`, `var` inside a function, zero values for the types whose zero is a real value (chapter 18), exported names by capitalization, `func` with named parameters and grouped types, `if`, `for` over an array, a slice, a map, a string or a channel, `defer`, `len`, `cap`, `make`. Changed, in the rows below: package variables, ranges, several results, closures (chapter 6), semicolon insertion, and `panic`, which has no `recover`.

| In Go | In Snello |
|---|---|
| `int32`, `float64`, `uint8` | `i32`, `f64`, `u8`; `int`, `uint`, `byte`, `rune` as before |
| A package `var` is assigned from anywhere | A package `var` is read by every task and assigned by none: `counter is a package variable, shared by every task; change it through a sync.Mutex, a sync.Atomic or a sync.Counter it holds` |
| `for i := range 10`, and `range` over a function | Refused, `cannot range over a value of type int`; a range runs over an array, a slice, a map, a string, a channel or an iterator, a value with a `Next` method (chapter 31) |
| Untyped constants: `x := 10 * 2.5` | Refused, `mismatched types int and f64 in *`: a literal takes its type from its context and a constant from its declaration (chapter 21); mix types with a conversion |
| `uint8(f)` for a float past the type's range is implementation-dependent: `uint8(300.7)` is 44 on arm64 | `u8(f)` is the nearest end of the range, and 0 for a NaN, on every processor |
| `x << n` with any integer count | the count has the type of the number shifted; a count of the width or more gives 0 or -1, as in Go |
| `s[a:b]` copies a string header; the bytes are shared by the GC | `s[a:b]` lends a `ref string`; the original must outlive it |
| `strings.ToLower(s)`, `strings.Fields(s)` | `s.ToLower()`, `s.Fields()`; the package has the functions too (chapter 14) |
| `go f(x)` | `spawn f(x)` |
| `func TestX(t *testing.T)`, `t.Errorf`, `t.Fatalf`, `t.Run`, `t.Log`, `t.Skip`, `t.TempDir`, `t.Cleanup` | `func TestX()` with no handle; `expect.Equal(ref got, ref want)` and the other assertions fail by a panic the runner catches; `testing.Run`, `testing.Log`, `testing.Skip`, `testing.TempDir()` whose `Drop` is the cleanup; record-and-continue is not kept, the first failed expectation ends the test |
| `go test ./...`, `go test -v -run Name` | `snello test DIR`, `snello test -v -run Name DIR`; one package at a time |
| `testify`'s `assert.Equal`, `go-cmp`'s diff | `expect.Equal`, printing got and want and the first difference in words; no library needed |
| `go test -race` | Nothing: a data race is a compile error in Snello, so there is no detector (chapters 10 and 16) |
| `func BenchmarkX(b *testing.B)`, `for b.Loop()`, `b.ReportAllocs()`, `go test -bench .` | `func BenchmarkX()`, `for testing.Loop()`, `testing.ReportAllocs()`, `snello test -bench . DIR`; the line has Go's columns, the count, `ns/op`, `B/op` and `allocs/op`, without Go's `-N` processor suffix and the `goos`, `goarch`, `pkg` and `cpu` lines before it |
| `break` inside `switch` or `select` ends that statement; a labeled `break outer` for the loop | `break` always ends the innermost loop; there are no labels |
| `if x := f(); x > 0 {` | Same. `if x := opt {` with no `;` is a binding on an optional |
| `append`, `delete`, `slices.Clone`, `new` | `xs.Push(v)`, `m.Remove(k)`, `xs.Clone()`, `&expr`; there is no `copy` into an existing slice, `undefined: copy` |
| `nil` | None. An absent value is `T?` |
| `panic(v)` with a value of any type, and `recover` | `panic(msg)` with one message, a string owned or lent, the text the program wrote; there is no `recover`: a task's panic reaches whoever joins its handle (chapters 17 and 28) |
| `a, b := f()` for two results | Same; several results are one tuple, returned as one, `return (n, right)`; the statement `return n, right` is refused with that form |
| An `if` is a statement | An `if` with an `else` is also an expression |
| A newline after `>` continues the line | A newline after `>` ends the line |
| The order of a variable's read against a call is left open: `ys[i] = f()` may read `i` after `f` | Everything is left to right: the indexes and keys of an assignment's places, then its values, then the stores |

## Chapter 4: Structs, enums and match

Unchanged: struct fields, keyed literals, dot access, methods with receivers.

| In Go | In Snello |
|---|---|
| `type Node struct { ... }` | `struct Node { ... }`; Go's spelling is refused with Snello's |
| `type Shape interface { ... }` | `trait Shape { ... }` (chapter 11) |
| `type Celsius float64`, a new type over another | the same, `type Celsius f64`, and the only thing `type` declares: no assignment from `f64` without `Celsius(x)`, none of the methods written for the type under it, its own and the traits it lists, `type Celsius f64 impl fmt.Stringer` |
| `type Name = Other`, an alias | None: `expected type, found '='`; a name is one type. `byte` and `rune` are the predeclared aliases of `u8` and `i32` |
| Value and pointer receivers, `(t Tree)` and `(t *Tree)` | Three receivers: `(t ref Tree)` reads, `(t mut Tree)` changes, `(t Tree)` takes |
| A `const` block with `iota`, or an interface with one struct per case | `enum`, with fields per variant |
| A type switch | `match`, exhaustive, usable as an expression |
| `switch` on an enum constant | `match`; a missing case is a compile error |
| `switch x { case 1, 2: ... default: ... }` | `match x { 1 \| 2 => ..., _ => ... }`: Snello has no `switch`, and `match` takes literals, ranges and guards |
| A `switch` with no value, `switch { case x > 0: ... default: ... }` | `match { x > 0 => ..., _ => ... }`: the conditions in turn, the arm `_` required and last |
| `v, ok := m[k]` | `if v := m.Get(ref k) {` |
| `struct{}` and anonymous structs | None: a struct is declared; a value with nothing in it is `()` |

## Chapter 5: Ownership

Nothing here has a Go counterpart; the garbage collector made the questions disappear. What to unlearn:

| In Go | In Snello |
|---|---|
| Assignment copies the value or the pointer; both names stay valid | Assignment moves; the old name is dead unless the type is `Copy` |
| Passing a slice or a struct copies its header or its bytes | Passing by value moves; pass `ref` or `mut` to keep it |
| `defer f.Close()` | A file closes itself when dropped; `f.Close()` consumes it and returns the error |
| Finalizers run eventually, or never | `Drop` runs at scope exit, early return and panic, in order; deferred calls run on a return and on a panic, as in Go, in one order with the drops |
| `x := xs[0]` copies the element | `x := xs[0]` copies a `Copy` element and is refused for any other, `cannot move xs[0] out of the slice`, since indexing lends; `xs.Remove(0)` or `take(mut xs[0])` moves it out, and `xs.Pop()` the last |
| `tmp := head; head = head.next` | `old := take(mut s.head)` |
| Copy a struct and hope it had no shared state | A struct of `Copy` fields alone copies on assignment, as an `int` does; any other is copied only by `s.Clone()`, for a type that lists `impl Clone`, and it is explicit |

### Where the model comes from

The ownership model is not Snello's invention. One owner per value, borrows that are either many readers or one writer, and a `Drop` that runs when the owner is done are the model Rust made mainstream, and before Rust they were the affine types and the region-checked pointers of research languages such as Cyclone. A Rust programmer recognizes Snello's chapters 5 and 6 at once, and that is deliberate: it is a model with a decade of use on real systems, and it is what lets Snello drop the garbage collector that Go's simplicity leans on.

Where Snello differs is in what it leaves out, and this is the bet the compiler's first checker was built to test:

| In Rust | In Snello |
|---|---|
| Lifetime annotations, `fn f<'a>(x: &'a T, y: &T) -> &'a U`, say which input a returned borrow comes from where the elision rules, the receiver or the only borrowed input, do not decide | None. The elision rules are the only rule: a returned borrow comes from the receiver or from the only borrowed parameter; a function where that is ambiguous is refused, and you return a clone or an index instead |
| A borrow is a first-class value: it can live in a struct, a `Vec`, a channel, and the type system tracks its lifetime through all of them | A borrow lives inside one function and cannot be stored, sent or captured by a task; a long-lived structure that points into another uses an arena with indices (chapter 13) |
| `&x`, `&mut x` at every call site; `x.method()` borrows implicitly | `f(x)` lends for reading with no keyword, `f(mut x)` for writing; `ref` and `mut` on the parameter say the rest |
| Programs that need a lifetime are written with one | Those programs are refused; the eight core programs of the design compile, run and free everything without one |

The restriction on borrows is real, and it is what makes the absence of annotations possible: when a borrow cannot leave its function, every lifetime can be inferred or the program rejected, with nothing to write either way.

## Chapter 6: Borrows

| In Go | In Snello |
|---|---|
| `func f(r *Rect)`: no copy, and maybe mutated, maybe kept, maybe nil | `func f(r ref Rect)` reads, `func f(r mut Rect)` changes; neither can keep it or be nil |
| `f(&x)` | `f(mut x)` to change `x`; `f(x)` or `f(ref x)` to read it |
| `p.field` through a pointer; `*p = v` to replace | The same: `n.value` through a borrow; `*cur = v` to replace |
| Append under a live pointer may reallocate, and the pointer silently keeps the old array | `xs.Push(v)` while an element borrow is live is a compile error |
| Modify a map during `range`: allowed, and an entry added may or may not be produced | A compile error: a write during a read |
| `if v, ok := m[k]; ok { v.n++ }` changes a copy, silently | `if v := m.Get(ref k) { v.n += 1 }` is refused, `cannot assign to v.n through a ref borrow`; `if v := m.GetMut(ref k) { v.n += 1 }` changes the entry where it lies. A `Copy` payload bound through a place is copied, as `:=` copies it: write `match mut xs[0] { Full(n) => *n += 1 }` to change it in place |
| A `(v, ok)` result leaves the value it came from usable | `o.Expect(msg)` takes `o` unless its type is `Copy`: a value handed out is not kept |
| Return a pointer to a local; the GC keeps it alive | `cannot return a borrow of local s`; return the value |
| A closure capturing a local by reference, returned from `counter()` | Rejected; put the state in a struct with a method |
| A struct holding a `*string` into a buffer | A struct holding `ref string` is itself a borrow: returned only like a borrow, never stored |
| Pass the same slice to five goroutines | `a spawned task cannot hold a borrow`; move it, clone it, or share it through `sync.Arc` |

## Chapter 7: Owned pointers

| In Go | In Snello |
|---|---|
| `&x` takes the address of a variable; the GC keeps it alive | `&expr` moves the value to the heap; the pointer is its one owner, and a borrow of what it holds lasts no longer than a borrow of anything else |
| `p := &Node{}`; copying `p` makes a second pointer to the same node | A pointer moves; there is one owner, so no two names reach one node |
| `*p` to read through, `p.field` as a shortcut | `p.field` the same; `*p` only to read or replace the whole value |
| `next *Node` is `nil` at the end of a list | `next *Node?` is `None` at the end; there is no `nil` |
| `p == q` compares addresses | `p == q` compares what they hold |
| A cycle of pointers is collected eventually | A cycle cannot be built from owned pointers; use an arena (chapter 13) |
| A pointer for every job: a method that changes a struct, a large argument, a shared value, `nil`, a recursive type | A tool for each: a `mut` receiver, a `ref` or `mut` parameter, `rc.Rc` or `sync.Arc`, `T?`, and `*T` for a recursive type, or for a large struct that moves often; chapter 7, When to use a pointer |
| `return &local`, which the GC keeps alive | Return the value; `&` moves a value to the heap and takes no address, and a borrow of a local cannot leave its function |

## Chapter 9: Collections

| In Go | In Snello |
|---|---|
| `xs = append(xs, v)`, and remember to assign the result | `xs.Push(v)` changes `xs` itself, with no result to assign, since the slice owns its elements; a full slice doubles its room |
| A slice is a view; two slices may share an array and see each other's writes | A slice owns its elements; `xs[lo:hi]` is a borrow, and the borrow rules keep the two apart |
| `var xs []int` is `nil` and `len(xs) == 0` | A slice declared without a value is empty; there is no `nil` |
| `xs[i]` on a bad index panics | The same, in Go's words without `runtime error:`: `panic: index out of range [5] with length 3` |
| `slices.Clone(xs)`, `sort.Ints(xs)`, `slices.Contains(xs, v)` | `xs.Clone()`, `xs.Sort()`, `xs.Contains(v)`: methods on the slice |
| `slices.Index(xs, v)` returns -1 when absent | `xs.Index(v)` returns `int?`: `None` when absent |
| `xs = append(xs, ys...)`, `xs = xs[:n]` | `xs.Extend(ys)`, `xs.Truncate(n)`, which drops the elements it cuts off |
| `xs[0]`, `xs[len(xs)-1]` on a slice that may be empty | `xs.First()`, `xs.Last()`, `ref T?` |
| `bytes.Equal(a, b)`, `bytes.Index(a, sep)` | `a.Equal(b)`, `a.Find(sep)`, `int?`; `a.Index(c)` finds one byte, as on every slice |
| `strings.Fields(s)` returns substrings sharing the bytes of `s`, which the GC keeps alive | `s.Fields()` yields borrows into `s`, and a loop over them lends `s` |
| `strings.TrimSpace(s)` | `s.TrimSpace()` or `s.Trim()`, a borrow of the middle of `s`; Go's `strings.Trim`, with a set of runes to cut, is `strings.TrimFunc(s, f)`, cutting the runes `f` accepts |
| `strings.TrimPrefix(s, p)`, `strings.TrimSuffix(s, p)` | `s.TrimPrefix(p)`, `s.TrimSuffix(p)`, a borrow of `s` |
| `strings.Index(s, t)`, `strings.LastIndex(s, t)` return -1 when absent | `s.Index(t)`, `s.LastIndex(t)` return `int?` |
| `strings.Replace(s, old, new, -1)` replaces every one | `s.ReplaceAll(old, new)`; `s.Replace(old, new, n)` with `n` of zero or less replaces none |
| `strings.Count(s, t)`, `strings.SplitN(s, sep, n)` | `s.Count(t)`, `s.SplitN(sep, n)`, its pieces borrows of `s`; `n` of zero or less gives none |
| `if _, ok := m[k]; !ok { m[k] = v }` then `m[k]` | `m.GetOrInsert(k, v)`: one lookup, a `mut V` |
| Integer overflow wraps; `math/bits` gives the carry of unsigned arithmetic, and nothing checks signed | It wraps; `a.CheckedAdd(b)`, `CheckedSub` and `CheckedMul` give `None` instead, and `Abs` and `Clamp` are methods |
| `strings.Lines(s)` keeps each newline | `s.Lines()` drops it, and a carriage return before it |
| `for i, r := range s` yields runes | The same, an invalid byte reading as U+FFFD and moving on one byte, as in Go; the string is lent for the loop (chapter 19) |
| `p = utf8.AppendRune(p, r)`, assigning the result | `utf8.AppendRune(mut p, r)` appends, growing the slice, with nothing to assign; there is no `EncodeRune` (chapter 19) |
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
| `[]byte(s)` copies | The same: `[]byte(s)` and `string(b)` copy, and leave their operand as it was |
| `v, ok := m[k]` | `m.Get(k)` is a `ref V?`; `m[k]` alone reads the zero value for a missing key, as in Go, and is refused when the values have none |
| `delete(m, k)` | `m.Remove(k)` returns the value as `V?` |
| Map iteration order is random | Insertion order |

## Chapter 10: Concurrency

Unchanged: `make(chan T)`, buffered channels, `ch <- v`, `for range ch`, `select` and its cases, a task is a function started and forgotten; `<-ch` gives a `T?`, below. Changed: a channel is not copied freely; a goroutine that shares one gets its own handle, `go produce(jobs)` is `spawn produce(jobs.Clone())`, since the last handle frees the channel and there is no collector to find it. There is no `close`: a channel ends when its last sending handle is dropped, a `chan T` handle counting as one, so a producer hands the receiving end away as a `<-chan T` and lets its sender go; a send with no receiver left drops its value, and `ch.Open()` says whether one remains.

| In Go | In Snello |
|---|---|
| `go f(x)` | `spawn f(x)` |
| `done := make(chan struct{})`; `done <- struct{}{}` | `done := make(chan ())`; `done <- ()` |
| A panic in any goroutine ends the process, unless that goroutine `recover`s; `go` returns nothing | `spawn f(x)` as a statement ends the program on a panic, and there is no `recover`; `h := spawn f(x)` gives a `task.Handle<T>`, and `h.Join()` returns the result or the panic that ended the task (chapter 28) |
| `errgroup.WithContext`, from `golang.org/x/sync`: `g.Go(f)`, `g.Wait()` returning the first error, the context cancelled when one goroutine fails | `task.Group`: `g.Add(spawn f(x))`, `g.Wait()` returning the first panic, the members cancelled by `g.Cancel()` or by the first panic (chapters 28 and 33) |
| `ctx context.Context` as the first parameter of every function that may block; `context.WithTimeout`, `WithCancel`; `select` with a `case <-ctx.Done()` beside every receive; `ctx.Err()` | Nothing is passed: `task.Within(d, func() T { ... })` gives the body's calls and the tasks they start a deadline, a `task.Group` is cancelled by `Cancel` or a member's panic, and every call that waits stops: a receive with `None`, a send dropping its value, `Sleep` returning, `Read` and `Dial` failing with `io.Cancelled` or `io.TimedOut`; `task.Cancelled()` is `ctx.Err() != nil` (chapter 33) |
| `DialContext`, `QueryContext`, `CommandContext`: a second form of each call that blocks; `conn.Read` takes no context, and is reached only through the connection, by a deadline set on it or by closing it | `Dial`, `Read`, `Accept`, `Sleep` as they are: the cancel reaches every wait the runtime owns |
| `exec.Command(name, args...)`, `cmd.Stdout = w` with a goroutine copying, `cmd.Run()` returning an `*ExitError` to type-assert for the code, `cmd.Args[0]` the program's own name | `exec.Command(name, args...)`, each stream `Inherit`, `Piped` or `Null`, `Run` giving a `Status` whose `Code()` and `Signal()` are options, `Args` without the name (chapter 36) |
| `exec.CommandContext`, which kills the child when the context ends, with `Cancel` and `WaitDelay` to say how; a `Process` with no drop, left a zombie unless waited | A `Child` owns the child: dropping it before `Wait` kills and collects it, `Detach` lets it run on, and `task.Within` around `Wait` ends the wait with the child still running, so the drop that follows kills it, as `CommandContext` does |
| `signal.Notify(c, os.Interrupt)` with a buffered channel the program sizes; `signal.Stop`, `Ignore`, `Reset`; a `SIGINT` with no `Notify` exits at once, deferred functions unrun | `signal.Notify(signal.Interrupt, signal.Terminate)` returning a channel one slot deep, dropped to stop; an interrupt no channel took cancels the program as a deadline does, so every wait returns and `main`'s drops run, and the program then ends by the signal |
| `ctx.Value(key)` | A parameter |
| `for { select { case <-timer: break } }` loops forever: the `break` ends the `select` | The `break` ends the `for` |
| A goroutine may reach any value the spawning function can; sharing a map between two goroutines compiles and races | `spawn` moves what the task takes in; a second task cannot take the same value, and a borrow cannot cross at all |
| Sending copies the value; the sender keeps using it | Sending moves the value; the sender has given it up |
| `v, ok := <-ch` | `<-ch` is a `T?`, `None` once the channel has ended, its last sender gone, and is drained |
| `close(ch)`; a send on a closed channel and a second close panic at run time, and so does a send after a receiver closed the channel; a goroutine may block forever on a channel no one will send to or receive from | The end of a channel is the drop of its last sending handle, so a send after the end, a second end and an end by a receiver cannot be written, and a send with no receiver left drops its value (chapter 29); a receive on a channel whose sending handles are kept and never used still waits, `ch := make(chan int); <-ch`, and so does a send on one whose receiving handle is kept and never used, `ch <- 1`, and when every task waits the program ends with `fatal error: all tasks are asleep - deadlock!` |
| Whether a value may safely cross goroutines is the programmer's problem; the race detector finds some cases at run time | `Send` is inferred from the type and checked at compile time at every crossing |
| `sync.Mutex` beside the data; forgetting to lock compiles | `sync.Mutex<T>` owns the data; the only way to it is through the guard `Lock` hands out, `*g.Get() += 1`, and the lock is released when the guard goes out of scope; `sync.RwLock<T>` the same with `Read` and `Write` |
| `var mu sync.Mutex`, `var once sync.Once`: the zero value is ready | `sync.NewMutex(v)`, `sync.NewRwLock(v)`, `sync.NewOnce()`, `sync.NewWaitGroup()`: a lock owns a value and is made with it; it has no zero value, and `var mu sync.Mutex<int>` is refused |
| `var p *T`, `var ch chan T`, `var f func()`: `nil`; the pointer and the function panic when used, and the channel blocks forever | Refused: a pointer, a channel, a function and a type with `Drop` have no zero value; `*T?` for one that may be missing, chapter 18 |
| `make([]*T, n)`: `n` nil pointers | `make([]*T, 0, n)` and `Push` |
| `var wg sync.WaitGroup` passed by pointer, `&wg` | `sync.WaitGroup` is an owned handle on a counter, cloned for each task, `w := wg.Clone()`, like a channel, and freed by the last handle |
| Go has no counted shared ownership; the GC shares everything | `rc.Rc` shares within one task and is refused at a task boundary; `sync.Arc` shares across tasks |
| `sync/atomic`: `atomic.Int64`, `atomic.Value`, `atomic.Bool` | `sync.Counter` for an `int` added to as one step; `sync.Atomic<T>` for any `Send`, `Clone` and `Eq` value read and replaced as one step, `Atomic<bool>` for a flag |
| A package-level `var` any goroutine may write; `func init()` runs before `main` | A package variable is read by every task and never assigned, lent as `mut` or moved out of; it changes through the `Counter`, `Atomic` or lock it holds; an initializer that is a call runs once, on first use, and a variable never used is never made; nothing runs before `main`, so no package function may be named `init` (chapter 10) |
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
| `var s Shape = Circle{}`, `[]Shape`: an interface value | The same, at one allocation and one indirect call each time, where Go's compiler leaves out the allocation for a value that does not escape; the value is owned and moves, and a borrow cannot become one |
| No default methods; share code with embedding or a helper function | Default methods in the trait, `super` to reach the one a method overrides, and trait fields (chapter 11) |
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
| `func Map[T, U any](s []T, f func(T) U) []U` | `func Map<T, U>(s []T, f func(T) U) []U` |
| `Map[int, string](xs, f)`, or inferred | `Map<int, string>(xs, f)`, or inferred, from the arguments and then from the type the result is assigned to |
| `func (l List[E]) Apply[F any](f func(E) F) List[F]`, a generic method, which satisfies no interface method, since an interface method declares no type parameters | `func (l ref List<T>) Conv<U>(...)` the same; a trait's required method declares none either, and a default with its own cannot be called through a trait used as a type (chapter 11) |
| `Pair[int, string]{a: 1, b: "x"}`: a composite literal names its arguments | `Pair{a: 1, b: "x"}`: inferred from the fields |

## Chapter 14: The standard library

Unchanged: import paths and package names, `import m "math"` to rename, exported names by capitalization, `math.Pi`, `math.Sqrt`, `math.Abs`, `math.Min`, `math.Max`, how a float prints, and the verbs `fmt` has, `%v`, `%d`, `%s`, `%q`, `%f` and `%t`, with a width and a precision.

| In Go | In Snello |
|---|---|
| `float64` throughout `math` | `f64` throughout `math` |
| Each package is compiled once and linked; a call into another package is inlined when the callee is small | The library is compiled with the program, as one module, so the optimizer sees every library function's body and decides inlining over the whole program |
| `GOROOT` holds the library's source | The library's source is embedded in the compiler: no directory and no variable say where it is |
| A function with no body is implemented outside Go, in assembly or through `go:linkname` | A function with no body is defined in C: the runtime's or the compiler's in the library, and a package's own `.c` files in a package that binds C; the main package may declare none (chapter 15) |
| `os.ReadFile` returns bytes and an error | `os.ReadFile` returns `Result<[]byte, io.Error>`, and `os.ReadToString` a string (chapter 8) |
| `os.LookupEnv` returns `(string, bool)` | `os.LookupEnv` returns a `string?`: `os.LookupEnv("HOME") ?? "/"` |
| `os.Setenv` also sets the C library's environment under cgo | `os.Setenv` changes the program's copy alone: a bound C library reads the environment the program started with |
| `os.O_RDONLY` and the other flags are the system's numbers | The same names, with numbers of Snello's own, the same on every system |
| `os.FileInfo` and `os.DirEntry` are interfaces | Structs with Go's methods but `Sys` and `Info`: `Name`, `Size`, `Mode`, `ModTime`, `IsDir`; `Name`, `IsDir`, `Type` |
| `path.Join(a, b, c)` takes any number | `path.Join(a, b)` takes two: `path.Join(ref path.Join(a, b), c)` |
| `filepath.Separator` is `\` on Windows, which takes `/` too, and `filepath.VolumeName` gives `C:` or `\\server\share` | The same: `filepath` follows the system, with `VolumeName`, and `IsSeparator` where Go has `os.IsPathSeparator`; `path` separates with `/` everywhere |
| `path.Base`, `path.Ext` and `path.Split` return strings sharing the argument's memory | They lend parts of the argument: a `ref string`, nothing copied |
| `runtime.NumCPU()` | `os.Cores()`; there is no `runtime` package, and no `GOMAXPROCS`: the runtime starts one worker per processor, and adds one only when every worker is held by a task that does not let go |
| `os.Open` returns `(*File, error)`; `defer f.Close()` | `os.Open` returns `Result<File, io.Error>`; the file closes itself when it goes out of scope, and `Close()` consumes it to report the error |
| `*fs.PathError` with `Op`, `Path`, `Err`; `errors.Is(err, fs.ErrNotExist)` | `io.Error` with `Op`, `Path`, `Msg`; `e.Kind() == io.NotExist` |
| `io.Reader` and `io.Writer` are interfaces; `io.Copy(dst, src)` dispatches dynamically | `io.Reader` and `io.Writer` are traits; `io.Copy` is generic over them, one copy per pair of types |
| Printing an `error` prints `err.Error()` | The same, for any type implementing the `Error` trait, and for a value of type `Error`, which holds any error as `error` does |
| `func f() (T, error)`, `if err != nil { return nil, err }` | `func f() Result<T, Error>` and `x?`: an error of any type passes into the `Error` value (chapter 17) |
| `fmt.Errorf("load %s: %w", path, err)`, `errors.Unwrap`, `errors.Is` | A type with `Error()` for the message and `Source()` for the cause; `%+v` prints the chain; `Is` is a `match` on the enum |
| `errors.New("message")` for a one-off error | An enum variant, or a struct listing `Error`: the message is derived from the value |
| Wrapping by hand at every `return err`, `return fmt.Errorf("read: %w", err)` or an error type of the program's | `?` wraps the error in the one variant of the function's error enum that holds its type |
| A type with a `String() string` method prints as it, a `fmt.Stringer` | A type prints as its `String` when it lists `impl fmt.Stringer`; traits are named, not matched by method |
| `go vet` warns of a verb that does not fit its argument, `%!d(string=x)` at run time | The format is checked when the program is compiled, and a misfit is an error; `fmt` has `%v`, `%d`, `%x`, `%X`, `%o`, `%b`, `%c`, `%s`, `%q`, `%f`, `%e`, `%g` and `%t`, and no `%T`, `%p`, `%U`, `%E` or `%G`; of the flags it takes the minus, the zero on a number, and the plus on `%v` of an error |
| `%x` on a float, a hexadecimal float; `%b` on a float | Refused: `%x`, `%X`, `%o` and `%b` take an integer, and `%x` and `%X` the bytes of a string or a `[]u8` |
| `%05s` pads a string with zeros | Refused: the `0` flag goes with a number |
| `%*d` with a width above a million prints `%!(BADWIDTH)` when the program runs | No `*`: a width or precision is written in the format, a million at most, and a larger one is refused when the program is compiled |
| `err.(*strconv.NumError).Err == strconv.ErrRange` | `e.Kind() == strconv.Range` |
| `strconv.Atoi(s)` returns `(int, error)`; `*strconv.NumError` | `strconv.Atoi(s)` returns `Result<int, strconv.Error>`, with `Func`, `Text` and `Msg` |
| `strconv.FormatFloat(f, 'g', -1, 64)` | `strconv.FormatFloat(f, -1)`: the shortest text that reads back, as `fmt` prints it |
| `strconv.FormatFloat(f, 'f', 2, 64)`, and `'e'` and `'g'` with a precision | `strconv.FormatFloat(f, 2)`: digits after the point; an exponent is `fmt.Sprintf("%.2e", f)` |
| `strconv.ParseFloat(s, 32)` returns a `float64` to convert | `strconv.ParseFloat32(s)` returns an `f32`; `FormatFloat32` writes one |
| `strconv.ParseInt(s, 16, 64)`, with a `bitSize` | `strconv.ParseInt(s, 16)` reads an `int` and `ParseUint` a `uint`; a narrower type is a comparison with its limit converted, `n <= int(math.MaxInt8)` |
| `strconv.FormatInt(n, 37)` panics | `strconv.FormatInt(n, base)` returns a `string?`, `None` for a base that is none |
| `strconv.Atoi("99999999999999999999x")` is `ErrRange` | `Syntax`: a range is a number's, and this text is none |
| `strconv.Unquote("''")` is the empty string | An error: an empty character literal is no literal, as in a program |
| `strconv.AppendInt`, `QuoteToASCII`, `QuoteRune`, `ParseComplex` | Not in the library: `%q` quotes a rune, and a `strings.Builder` takes what `Sprintf` writes |
| `strings.Builder` | The same, with `WriteString`, `WriteByte`, `WriteRune`, `Grow`, `Len`, `String` and `Reset` |
| `strings.NewReader(s)` | The same; the `Reader` owns the string, so `bufio` can hold it |
| `[]byte(s)` and `string(b)` copy | The same |
| `fmt.Fprintln(os.Stderr, ...)`, `fmt.Fprintln(w, ...)` with `w io.Writer` | `fmt.Fprintln(mut os.Stderr(), ...)`: the writer is lent `mut`, and must implement the `io.Writer` trait; the streams are functions, since a package variable is never lent `mut` |
| `f.Seek(offset, io.SeekStart)`, a `whence` of 0, 1 or 2 | `f.Seek(io.Start(n))`, `io.Current(n)` or `io.End(n)`: a `SeekFrom`, and the position a `u64` |
| `io.ReadFull` returns `io.EOF` with nothing read, `io.ErrUnexpectedEOF` partway | `Ok(0)` with nothing read, an error of `Kind` `io.UnexpectedEOF` partway |
| `io.Discard`, a variable | `io.Discard`, a type: `var sink io.Discard` |
| `sc.Split(bufio.ScanWords)`, which panics after `Scan` | `bufio.Words(mut r)`, a second constructor |
| `r.Peek(n)` returns bytes valid until the next read | The same bytes lent: using them after the next read is a compile error |
| `r.UnreadByte()` returns an `error` | `r.UnreadByte()` returns a `bool`, as `UnreadRune` does |
| `bytes.Buffer`'s `WriteString` and `WriteByte` return an error that is always `nil`; `ReadByte` returns `io.EOF` at the end | They return nothing; `ReadByte` returns a `byte?` |
| `b.Truncate(n)` panics for an `n` out of range | Keeps all for an `n` past the length and none for one below zero, as a slice's `Truncate` does |
| `bytes.Equal(a, b)`, `bytes.Index(a, b)` | `a.Equal(b)`, `a.Find(b)`: methods of `[]byte` |
| `fmt.Fprintln` returns `(n, err)` | `fmt.Fprintln` returns nothing, and so do `Fprint` and `Fprintf` |
| `fmt.Print`, `fmt.Errorf`, `fmt.Sscanf` | Not in the library: `Printf` or `Fprint` to `os.Stdout()`; an error is a type with `Error()` and `Source()`; text is read with `bufio.Scanner` and `strconv` |

## Chapter 20: Numbers

Unchanged: the names and the arguments of `math`'s functions and their special cases, `math/bits`' names with the width at the end, and the numbers a seeded generator gives, which are those of Go's `rand.New(rand.NewPCG(seed1, seed2))`.

| In Go | In Snello |
|---|---|
| `math.MaxInt8` is an untyped constant | `math.MaxInt8` is an `i8`; each limit has the type it is the limit of |
| `math` is written in Go, with assembly on some processors | the powers, logarithms and trigonometry are the system's math library's, within a few units in the last place |
| `bits.Mul64`, `Add64`, `Div64`, `ReverseBytes` | not in the package; the six counting and moving operations are |
| `rand.New(rand.NewPCG(1, 2))`, a `*Rand` over a `Source` | `rand.New(1, 2)`, a `Rand`; one generator, no `Source` to choose |
| `r.IntN(0)` panics | `r.IntN(0)` is `None`; `IntN` returns `int?` |
| `r.Perm(-1)` panics | `r.Perm(-1)` is an empty slice |
| `r.Shuffle(len(xs), func(i, j int) { xs[i], xs[j] = xs[j], xs[i] })` | `r.Shuffle(mut xs)` |
| A `*Rand` is shared by copying the pointer | A `Rand` is a value: lend it by `mut` to share one sequence; a copy replays from the same point, as a copied `PCG` does |
| `rand.Uint64`, `Uint32`, `N`, `NormFloat64`, `ExpFloat64` | not in the package |

## Chapter 21: Constants

| In Go | In Snello |
|---|---|
| `const KB = 1 << 10`, untyped, exact to 512 bits in Go's compiler, folded wherever a constant expression is written | Typed by its context, folded in its own type with an overflow refused, in a `const` and an array length; elsewhere `x := KB * KB` is run-time arithmetic and wraps on overflow |
| `iota` in a `const` block | an `enum`, whose cases are named |
| `-0.0` is a zero | `-0.0` is a negative zero, as at run time |
| `len("abc")` is a constant | `len` is not constant; write the number, or make it a `var` |
| a constant is a basic type | a struct of constant fields may be a constant too, read by its fields |

## Chapter 22: Variadic functions

| In Go | In Snello |
|---|---|
| `func Sum(xs ...int)` receives a slice, `Sum(s...)` shares the caller's array | `...T` receives an owned slice the arguments are moved into; `Sum(s...)` moves `s` in, so the function owns it |
| one form | `...ref T` lends each argument for the call, laid out on the caller's stack, with no allocation; the function returns no borrow of them |
| `f(xs...)` with `xs []T` | the same; for `...ref T`, `xs` is lent, and a variadic function passes its own `xs...` on for free |
| `func(...int)` is a type of its own | the same, written `func(...int)` or `func(...ref string)` |

## Chapter 23: Default field values

| In Go | In Snello |
|---|---|
| A settings struct whose zero fields mean "the default", read with `if o.Port == 0 { port = 8080 }` | `port int = 8080` in the struct; a literal that leaves the field out takes it, and `0` stays a value of its own |
| Functional options, `NewServer(WithPort(9000), WithVerbose())` | a struct literal, `Server{port: 9000}`, every setting named, any of them skipped |
| `os.OpenFile(path, os.O_WRONLY\|os.O_APPEND\|os.O_CREATE, 0o644)` | the same, or `os.OpenOptions{Read: false, Append: true, Create: true, Perm: 0o644}.Open(path)` |
| Every struct has a zero value, `var o Options` | a struct with a default has none: `var o Options` is refused, `o := Options{}` takes every default |

## Chapter 24: Reflection at compile time

| In Go | In Snello |
|---|---|
| `reflect.TypeOf(v).Kind()` when the program runs | `match T { int => ..., struct => ... }`, decided when the program is compiled, one arm kept per type |
| `for i := 0; i < t.NumField(); i++ { f := t.Field(i) }` | `for f := range fields(T) { ... v.[f] ... }`, written out once per field, `f.name` a constant |
| `encoding/json` skips unexported fields in silence | a field the function cannot reach is a compile error, unless the type lists `Reflect` |
| `func ParsePoint(s string) Point`, a package function per type | `func Point.Parse(s ref string) Point?`, a function of the type, which a trait may require and a generic function call, `T.Parse(s)` (chapter 26) |
| `json.Marshaler` and `json.Unmarshaler`, the latter a pointer method that fills a value already made | the same traits, `UnmarshalJSON` a function of the type that returns the value: `func Celsius.UnmarshalJSON(text ref string) Result<Celsius, json.Error>` |
| `json:"name,omitempty"` tags rename a field and leave it out | typed attributes the compiler checks: `Host string @json.Name("host") @json.OmitEmpty`, and `@json.Skip` for `json:"-"` (chapter 25) |
| `json.Marshal` of a channel field fails when it runs | refused when the program is compiled, naming the type no arm takes |
| A `MarshalJSON` and an `UnmarshalJSON` written by hand to give a choice of shapes a `"type"` field | `@json.Tag("type")` on the enum, or `@json.Tag("t") @json.Content("c")`, or `@json.Untagged`; a layout json cannot write is refused when the program is compiled |
| A generic function finds a type it cannot handle when it runs, in a type switch on `any(v)`, and panics or returns an error | `refuse("...")` in the arm that type takes refuses the program when it is compiled, with the chain of calls and the message |
| a `map[int]V` written with its keys sorted by their text, `"10"` before `"2"`, and read from a key `"01"` or `"+1"` | sorted by their values, `"2"` before `"10"`; a key is read only as JSON writes an integer, so `"01"` and `"+1"` are errors with their offset |
| `json.MarshalIndent(v, "", "  ")` and `json.Indent(&buf, src, "", "  ")` | the same layout: `json.MarshalIndent(ref v, "", "  ")` and `json.Indent(ref text, "", "  ")`, each returning the text; space after the value is dropped, where Go's `Indent` copies it, and `Indent` refuses text that is not UTF-8, which Go's passes through |
| `json.NewDecoder(r)`, `dec.Decode(&v)` until `io.EOF`, and `dec.Token()` and `dec.More()` for an array read an element at a time | `json.NewDecoder(mut r)`, `dec.Decode<T>()` until `None`; for an array or object `dec.BeginArray()`, `dec.BeginObject()`, `dec.Key()`, `dec.More()`, `dec.EndArray()`, `dec.EndObject()` and `dec.Peek()`, each saying what it reads, in place of one `Token` of any kind; a reader's failure is `dec.IoError()` |
| A Decoder buffers a value of any size; a bound on the input is `io.LimitReader` or `http.MaxBytesReader` around the reader | A value's text is bounded, 64 MiB by default, `dec.SetLimit(n)` another; past it the value is refused with its offset and every later call returns that error |
| `json.NewEncoder(w)`, `enc.Encode(v)` | `json.NewEncoder(mut w)`, `enc.Encode(ref v)`, a line break after each value as Go writes it; after a write fails, every later `Encode` returns the failure, as Go's does |

## Chapter 27: Writing Snello

| In Go | In Snello |
|---|---|
| gofmt's layout and Effective Go's names | the same layout and the same naming, plus the file order of chapter 27: constants, types, exported API, then helpers |
| `switch c { case beginObject: }`, a constant in a case | `match { c == beginObject => ... }`: a constant is no pattern |
| `panic` in a library on a bad argument | an option or a `Result` with an error that says where; the standard library does not panic on its caller's input, and `expect` and `testing` fail a test by panicking |
| a pointer parameter says nothing of who keeps the value | `ref T` read, `mut T` changed, `T` kept: the signature says it |

## Chapter 30: Debugging and profiling

| In Go | In Snello |
|---|---|
| `dlv debug` | `snello debug`, which starts lldb on macOS or gdb on Linux with Snello's formatters and `tasks` loaded, and cdb on Windows with the values shown by the PDB's visualizers and `!tasks` loaded, which cdb for arm64 cannot run |
| `go version` prints `go version go1.27.4 darwin/arm64`, and `go1.28-devel_…` with the commit and its date for a build from a Git checkout of the development tree; `go help` and `go help build` | `snello version` prints `snello 0.1.0 (8477985) stable darwin/arm64`, the commit when the build recorded one, and `devel` in the channel's place for a build from source; `snello help` and `snello help build`, read from the table the command line is parsed with |
| `go build -gcflags=all="-N -l"` for a build a debugger can follow | `snello build -debug`: your code unoptimized, and the standard library optimized through the LLVM backend and unoptimized through the C backend |
| `goroutines` and `goroutine N bt` in Delve, `info goroutines` in gdb | `tasks`, every task with its state and a waiting task's stack |
| `b main.sum` | `breakpoint set -n sum` in lldb, `break sum` in gdb, or a breakpoint at the line, `main.snello:19`; with the C backend, the function's symbol, `sn__sum` |
| the DWARF in every binary, which `-ldflags=-w` strips | the debugging information in a debug build alone, in the binary on Linux, in `prog.dSYM` on macOS and in `prog.pdb` on Windows; a release build keeps symbols for a profiler and no lines |
| `pprof` | the system's profilers, `perf` and Instruments, on a release build, which keeps its frame pointers |

## Chapter 31: Iterators

| In Go | In Snello |
|---|---|
| `iter.Seq[V]`, a function that calls the loop's body back with each item | `iter.Iterator<T>`, a value the loop pulls items from with `Next`; `range` takes any value whose type has `Next`, whether or not it lists the trait |
| No adapters in the standard library: a filter or a map is a loop, or a helper of your own | `Map`, `Filter`, `Take`, `Skip`, `Enumerate`, `Zip` and `Chain`, and the consumers `Count`, `Fold`, `Any`, `All`, `Find` and `Collect`, as default methods of the trait |
| `maps.All(m)`, `slices.Values(s)` | `range` over the map or the slice itself, with no adapter to chain; `d.Iter()`, `s.Iter()` and `a.Iter()` are the iterators of a deque, a set and an arena |
| An iterator over a collection that changes while it runs sees whatever the change leaves | The collection cannot change while an iterator over it, or an adapter over that, is alive: the checker refuses it |
| A generic type argument may be anything, a pointer into a collection included, which the collector keeps alive | A type argument may hold a borrow, and the instance holds it: the checker keeps the collection alive and unchanged for as long as the instance lives |

## Chapter 37: Documenting a package

| In Go | In Snello |
|---|---|
| A doc comment above a declaration, with headings, lists, code and `[Name]` links (go.dev/doc/comment) | The same comments, by the same rules, but for link definitions, `[Text]: URL`: a web address is written in the text, and a link to a name that names nothing is warned of, where text in brackets that is no name, `[RFC 7159]`, stays text |
| A `const ( ... )` block shares the comment above it | Consecutive `const` lines, with no blank line between, share the comment above the first, as a block does |
| A link that names nothing is left as text, silently | A link that names nothing is warned of, with its file and line |
| `go doc strings.Builder` | `snello doc strings.Builder`, whose signatures show how each argument is passed, `ref`, `mut` or by value |
| `go doc Reader` looks in the current directory's package alone, and `go doc rand.Int` takes the first package named `rand` it finds, `crypto/rand` | `snello doc Reader` names every package that has it, `bufio, io and strings`, and a package settles it |
| A type's page cannot say which interfaces it satisfies | A type's declaration names the traits it implements, and a trait's page lists the types that implement it |

## Chapter 38: Installing Snello

| In Go | In Snello |
|---|---|
| An archive or an installer per platform from go.dev/dl, its SHA256 on the page | The one-line install script, which checks the archive against the release's `SHA256SUMS`, or an archive per platform from the Releases page, with the list signed through Sigstore and each archive's provenance attested |
| `go install golang.org/dl/go1.x.y@latest` for another version | `SNELLO_VERSION=0.1.0` given to the script, `curl -fsSL … \| SNELLO_VERSION=0.1.0 sh`, or `$env:SNELLO_VERSION = '0.1.0'` before the line on Windows |
| A `go` line newer than the toolchain downloads that toolchain, under `GOTOOLCHAIN=auto` | A `snello` line newer than the compiler is refused, `this module needs snello 0.9.0; this is snello 0.1.0: upgrade the compiler` |
| `go version` | `snello version`, which adds the commit and the channel, `stable` for a release and `devel` for a build from source |
| Each major release is supported until two newer ones are out | The latest two minor releases get fixes, as Go's two do, and a `0.x` minor may break programs, saying how under **Breaking changes** in its notes |
