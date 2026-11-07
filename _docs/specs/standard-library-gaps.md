# What the standard library is missing

Status: written 2026-11-07 for the owner's review and decided the same day. Every `std` row and the solidity rows go into stage 9, `stage-9-standard-library.md`, with `testing` as its first task; the `errors` package is refused in favour of the `Error` trait whole, task 9.2; the `later` rows each get a design note after the stage; the `lib` rows are stage 8's; the `out` rows stay out. The decision column says which.

## How to read it

The library is fifteen packages and about 3,600 lines of Snello over the C runtime, reviewed twice in stage 5, each package on its reference algorithm and measured against Go's and Rust's. It is small by the rule of decision 7 of the library note: a package joins when the language or the runtime needs it, or when every program would otherwise write it; a package that encodes a format, speaks a protocol or wraps a system is built outside the compiler, as Rust's crates are built on `std`. The gaps below are judged against that rule, not against Go's size: Go ships about 150 packages, Rust's `std` is large, and Snello will never be either. A gap is worth closing when a Go programmer writing an ordinary program hits it in the first hour.

The recommendations:

- **std**: add to the standard library, under the rule.
- **lib**: a library outside the compiler, stage 8, on the core as it is.
- **out**: leave out, with the reason.
- **later**: not now; the design it needs is named.

Where a gap is in the language rather than the library, it is marked so.

## Cross-cutting gaps

These are not a package's but the library's, and they bear on whether it can be called solid.

| Gap | What it is | Recommendation | Decision |
|---|---|---|---|
| No fuzzing | `strconv`, the quote reader, `fmt`'s format grammar, `ParseFloat` and `bufio`'s line reading take input the program does not control and have never run under a fuzzer; Go fuzzes all of these | std: a Go fuzz target per parser, under `go test -fuzz`, since the runtime is C and the parsers are small; a corpus checked in | stage 9 |
| One conformance twin | Only `fmt` has a Go twin whose output pins Snello's line for line (`examples/run/formats.snello`); `strconv`, `strings`, `sort`, `time.String` and `bufio` have expected outputs written by hand | std: a Go twin per package whose behaviour is defined as Go's, run by the suite as `formats` is | stage 9 |
| No `testing` package | A program's own tests have no way to be written in Snello; the compiler's suites are Go | later: a `testing` package needs a decision on how a test is found and run, `snello test`, which is a stage of its own; it is the largest single gap for a user | stage 9, task 9.1, first |
| Unicode is partial | Case, white space and printable runes are Unicode 17 tables, and `Fields`, `Trim`, `ToUpper` use them; there is no `rune` iteration over a string, no `utf8` package, no `unicode` package to ask a rune's category | std, in two parts: `for _, r := range s` yielding runes is the language's (chapter 3 says a string is bytes); a `unicode` package with `IsLetter`, `IsDigit`, `IsSpace`, `IsUpper`, `IsLower`, `ToUpper`, `ToLower` on runes, over the tables the runtime already has | stage 9 |
| No `errors` package | `io.Error` and `strconv.Error` each carry a `Kind`; there is no way to wrap one error in another with a message, as Go's `fmt.Errorf("%w")` and `errors.Is` do, and no `errors.New` for a program's one-off error | no package: the `Error` trait gains `Source()`, `?` converts the callee's error into the one variant of the caller's enum that holds its type, and an enum's `Error()` is derived when not written | stage 9, task 9.2 |
| No `context` | A deadline or a cancellation carried through calls; Go's `context.Context` | out for now: example 12 shows the pattern with a channel in an `Arc`, which is what Go's context is underneath; a package may come when the first library needs to take one, stage 8's `net/http` | out |

## Package by package

### `builtin`: strings, slices, maps, options, results

| Gap | Go or Rust | Recommendation | Decision |
|---|---|---|---|
| `string`: no `Index`, `LastIndex`, `Replace`, `ReplaceAll`, `TrimPrefix`, `TrimSuffix`, `TrimLeft`, `TrimRight`, `TrimSpace` by name, `Count`, `EqualFold`, `Title`, `Cut` | Go `strings` | std: `Index`, `Replace`, `TrimPrefix`, `TrimSuffix`, `Count` are in every program; the rest out | stage 9 |
| `string`: no indexing by rune, no `[]rune(s)`, no `[]byte(s)` as an owned copy named as such | Go conversions | language: `[]byte(s)` exists for a view; a `Runes()` method with the `unicode` work above | stage 9, task 9.4 |
| `[]T`: no `Index`/`Find` returning the position, no `Map`, `Filter`, `Any`, `All`, `Sum`, no `Extend`/append of a slice, no `Truncate`, no `Last`/`First` as options | Rust iterators, Go `slices` | std: `Extend(ref []T)` and `Index(ref T) int?` now; `Map` and `Filter` with example 18's iterator trait, which is where they belong | stage 9 |
| `[]T`: no binary search by key, `SortBy` takes `less` not a key | Go `slices.BinarySearchFunc`, Rust `sort_by_key` | out: `sort.Search` and `SortBy` cover it | out |
| `map[K]V`: no `Keys`, `Values`, no `Len` method (`len` is the builtin), no `Entry` API, no `GetOrInsert` | Rust `entry`, Go `maps` | std: `GetOrInsert(k, v) mut V` is the one every counter program wants beyond `m[k] += 1`; `Keys` and `Values` out, a `range` does it | stage 9 |
| `T?`: no `OrElse(func)`, `AndThen`, `Filter`, `Take`, `Replace`, `Zip` | Rust `Option` | std: `AndThen` and `OrElse`; `Take` is `take(mut p)` already | stage 9 |
| `Result<T, E>`: no `Ok()`/`Err()` to the option, no `AndThen`, no `UnwrapOrElse`, no `?` across error types | Rust `Result` | std: `Ok() T?`, `Err() E?`, `AndThen`; `?` converting the error type is the language's and waits on the `errors` decision | stage 9 |
| No `min`/`max` over a slice, no `abs` for integers, no `clamp` | Go `min`, `max` builtins exist here for two values | std: integer `Abs` beside `math.Abs`, `Clamp`; out for the rest | stage 9 |
| Integer overflow: wraps; no checked, saturating or wrapping methods | Rust `checked_add` and friends; Go has none | std: `CheckedAdd`, `CheckedMul`, `CheckedSub` returning `T?` on every integer type, which chapter 3 already promises | stage 9 |

### `fmt`

| Gap | Go or Rust | Recommendation | Decision |
|---|---|---|---|
| No `Errorf` | Go | std, with the `errors` decision: `fmt.Errorf` returning the predeclared error type | stage 9 |
| No `Sscanf`, `Scan`, `Sscan` | Go | out: `bufio.Scanner` with `strconv` is the Snello way, as Rust has no `scanf` | out |
| Verbs: `%x`, `%X`, `%o`, `%b`, `%e`, `%g`, `%c`, `%U`, width and `-` flag, `%+v`, `%#v`, `%T`, `%p` | Go | std: `%x`, `%c`, width and `-`, since tables and hex are everywhere; `%T` and `%p` out, there is no reflection and a pointer prints as its value | stage 9 |
| No `Fprint` (Fprintln and Fprintf exist) | Go | std: trivial | stage 9 |
| `Stringer` is honoured; no `Formatter` for a type to take the verb | Go `fmt.Formatter`, Rust `Display`/`Debug` | out: `Stringer` is enough until a type needs two forms | out |

### `strings`

| Gap | Go or Rust | Recommendation | Decision |
|---|---|---|---|
| Package holds `Builder`, `Repeat`, `Join`, `Fields`, `ToLower`, `ToUpper`; the rest are methods on `string` | | the split between method and package function is a decision to record, not a gap | recorded as is |
| `Builder`: no `WriteRune`, `Grow`, `Cap` | Go | std: `WriteRune` with the rune work, `Grow` now | stage 9 |
| No `Reader` over a string as an `io.Reader` | Go `strings.NewReader` | std: small, and the only way to feed a string to `bufio` or `io.Copy` | stage 9 |
| No `Compare`, `EqualFold`, `Map`, `Title`, `SplitN`, `FieldsFunc`, `TrimFunc`, `IndexFunc` | Go | out, but `SplitN` std | out |

### `strconv`

| Gap | Go or Rust | Recommendation | Decision |
|---|---|---|---|
| `Atoi` and `Itoa` only for `int`; no `ParseInt(s, base, bits)`, `ParseUint`, `FormatInt(n, base)`, `ParseBool`, `FormatBool`, `Quote`, `Unquote`, `AppendInt` | Go | std: `ParseInt` with a base and `FormatInt` with a base, `ParseBool`, `Quote` and `Unquote` (the runtime has the quoting already, for `%q`) | stage 9 |
| `FormatFloat` has one form, Go's shortest `%g`; no precision or format argument | Go `FormatFloat(f, fmt, prec, bits)` | std: `FormatFloat(f, prec)` or the `fmt` verbs carry it; one of the two | stage 9 |
| `f32` parsing and formatting | Go `bitSize` | std: `ParseFloat32`, or a bits argument as Go's | stage 9 |

### `math`

| Gap | Go or Rust | Recommendation | Decision |
|---|---|---|---|
| `Pi`, `Sqrt`, `Abs`, `Min`, `Max` only; no `Floor`, `Ceil`, `Round`, `Trunc`, `Pow`, `Exp`, `Log`, `Log2`, `Log10`, `Sin`, `Cos`, `Tan`, `Atan2`, `Hypot`, `Mod`, `Inf`, `NaN`, `IsNaN`, `IsInf`, `MaxInt` and the other limits, `E` | Go `math` | std: all of these; each is one bound function to libm, which `mathx` in `examples/run/bound/` shows takes five lines, and the limits are constants | stage 9 |
| No `math/rand` | Go, Rust `rand` crate | std: the runtime has a seeded wyhash random source for map hashing already; `rand.Int`, `rand.IntN`, `rand.Float64`, `rand.Shuffle`, with a seedable generator as Go 1.22's `rand.New(rand.NewPCG(...))` | stage 9 |
| No `math/bits` | Go | std: `LeadingZeros`, `TrailingZeros`, `OnesCount`, `RotateLeft`, each one instruction; the backends can write them inline | stage 9 |

### `io` and `bufio`

| Gap | Go or Rust | Recommendation | Decision |
|---|---|---|---|
| No `ReadAll`, `ReadFull`, `WriteString` helper, `MultiWriter`, `TeeReader`, `LimitReader`, `Discard`, `EOF` as a value (end is `0` from `Read` and `None` from `ReadString`) | Go | std: `ReadAll`, `ReadFull`, `Discard`; the rest out | stage 9 |
| No `Seeker`, `Closer`, `ReaderAt`; `File` has no `Seek`, `Stat`, `Truncate` | Go | std: `Seek` on `File` and a `Seeker` trait, since a program reading a binary format needs it | stage 9 |
| `bufio.Reader`: no `ReadLine`, `Peek`, `UnreadByte`, `ReadRune`, `ReadBytes` | Go | std: `Peek` and `ReadBytes`; `ReadRune` with the rune work | stage 9 |
| `bufio.Scanner`: lines only; no `Split(ScanWords)`, `ScanRunes` | Go | std: `Words()` as a second constructor, out for a custom split function | stage 9 |
| No `bytes` package: no `bytes.Buffer`, `bytes.Equal`, `Index`, `Contains` on `[]byte` | Go | std: `Buffer` as a growable `io.Writer` and `io.Reader` is what every encoder writes into; `Equal` and `Index` as methods on `[]byte` | stage 9 |

### `os`

| Gap | Go or Rust | Recommendation | Decision |
|---|---|---|---|
| No environment: `Getenv`, `Setenv`, `Environ`, `LookupEnv` | Go | std: `Getenv` and `LookupEnv`; every tool reads `HOME` or `PATH` | stage 9 |
| No directories: `ReadDir`, `Mkdir`, `MkdirAll`, `Getwd`, `Chdir`, `Stat`, `Lstat`, `Rename`, `Chmod` | Go | std: `ReadDir`, `Mkdir`, `MkdirAll`, `Stat` (returning a `FileInfo` with size, mode, modification time, is-directory), `Rename`, `Getwd` | stage 9 |
| No `ReadFile` as bytes (`ReadToString` only), no `WriteFile` as bytes, no `OpenFile` with flags (append, create-or-truncate, exclusive) | Go | std: `ReadFile` returning `[]byte`, `WriteFile`, `OpenFile(path, flags, mode)` | stage 9 |
| No `Hostname`, `Getpid`, `UserHomeDir`, `TempDir` | Go | std: `TempDir` and `Getpid`; `Hostname` and `UserHomeDir` out until a program needs them | stage 9 |
| No `path` or `path/filepath`: `Join`, `Base`, `Dir`, `Ext`, `Clean`, `Abs`, `Split`, `Match`, `Walk` | Go | std: `path.Join`, `Base`, `Dir`, `Ext`, `Clean` as pure string functions; `filepath.Walk` out, `ReadDir` and recursion do it | stage 9 |
| No `os/exec`, no `os/signal` | Go | later: a child process needs the scheduler's poller to wait on it, and a signal handler needs a decision on which task runs it; both are designs of their own | after stage 9, a design note first |
| No `flag` | Go | lib, stage 8: a flag parser is ordinary Snello over `os.Args`, and example 19 shows the hand-written form | stage 8 |
| No `log` | Go, Rust `log` crate | lib, stage 8: `fmt.Fprintln(mut os.Stderr, ...)` with a time prefix is twenty lines | stage 8 |

### `time`

| Gap | Go or Rust | Recommendation | Decision |
|---|---|---|---|
| `Time` prints in UTC in one form; no `Format`, `Parse`, no layouts, no `Year`, `Month`, `Day`, `Hour`, `Weekday`, `Date` constructor, no `Local`, no zones | Go `time` | std: `Date(y, m, d, h, min, s, ns)` and the field accessors, in UTC; `Format` and `Parse` with Go's reference layout; zones out, UTC only, as the design note already decided | stage 9 |
| No `Timer` and `Ticker` as values with `Stop` and `Reset`; `After` and `Tick` are channels that cannot be stopped from the program | Go | std: `NewTimer` and `NewTicker` returning a struct whose `Drop` stops it, which the runtime's timer heap already supports for a dropped channel | stage 9 |
| No `Duration` parsing, `ParseDuration` | Go | std, with `Parse` | stage 9 |
| No monotonic reading of a `Time` by the program (`Since` uses it) | Go hides it too | out | out |

### `sync`

| Gap | Go or Rust | Recommendation | Decision |
|---|---|---|---|
| No atomics: `atomic.Int64`, `Add`, `Load`, `Store`, `CompareAndSwap`, `Bool` | Go `sync/atomic`, Rust `std::sync::atomic` | std: `sync.AtomicInt` and `AtomicBool` with `Load`, `Store`, `Add`, `CompareAndSwap`; `03-ownership-across-tasks.md` names them as not yet in the library and a shared counter is an `Arc<Mutex<int>>` today | stage 9 |
| No `Cond`, no `Semaphore`, no `Map` | Go | out: a channel is the condition variable, as Go's own advice; a semaphore is a buffered channel | out |
| No `Pool` | Go | out: no collector means no garbage to amortize | out |
| `Once.Do` takes `func()` only; no `OnceValue` | Go 1.21 | out | out |
| `WaitGroup` is `Clone` per task, not a shared pointer | Go | this is a decision, recorded; a `Go` method as Go 1.25's `wg.Go(f)` would spare the `Add`/`Done` pair: std | recorded as is |
| No `errgroup`, no structured concurrency, no scoped tasks that may borrow | Rust `thread::scope`, Go `errgroup` | later: the design note names structured concurrency as the way to let borrows into tasks; a stage of its own | after stage 9, a design note first |

### `net`

| Gap | Go or Rust | Recommendation | Decision |
|---|---|---|---|
| IPv4 TCP only: no IPv6, no DNS (`Dial("example.com")` is refused), no UDP, no Unix sockets, no TLS | Go `net`, `crypto/tls` | std: DNS through the system resolver (`getaddrinfo` on the poller's thread, so a task does not block a worker) and IPv6, since `Dial("host", port)` without them is a toy; UDP std when a program needs it; Unix sockets out; TLS lib, stage 8, over a system library through the binding of chapter 15 | stage 9 |
| No `Conn.LocalAddr`, `RemoteAddr`, no `Listener.Addr` (only `Port`) | Go | std: both, as strings | stage 9 |
| No `KeepAlive`, `SetNoDelay` (always on), `SetReadBuffer` | Go | out | out |
| No `net/http`, `net/url` | Go | lib, stage 8, as decided; `url` parsing with it | stage 8 |

### `sort`, `heap`, `arena`, `rc`

| Gap | Go or Rust | Recommendation | Decision |
|---|---|---|---|
| `sort`: no `SortStable` by name (every sort is stable), no `Slice` by index function, no `Reverse` | Go | out: `SortBy` with the reversed comparison; a note in chapter 14 that the sort is stable | out |
| `heap`: min-heap only; a max-heap needs a `Cmp` that says so | Rust `BinaryHeap` is a max-heap, Go's is what `Less` says | std: a `Reverse<T>` wrapper as Rust's `cmp::Reverse` | stage 9 |
| `arena`: no iteration over the live slots, no `Clear`, no `Len` of live versus capacity | | std: `range` over an arena yielding `(Index<T>, ref T)`, with the iterator trait | stage 9 |
| `rc`: no `MakeMut`/copy-on-write, no `TryUnwrap` | Rust | std: `TryUnwrap() T?` taking the value out of a sole `Rc`; `MakeMut` out | stage 9 |
| No `list`, `ring`, `deque` | Go `container`, Rust `VecDeque` | std: `Deque<T>` with `PushFront`, `PopFront`, `PushBack`, `PopBack`, since a queue on a slice is quadratic and example 04's BFS wants one; `list` out, chapter 13 says why | stage 9 |
| No `set` | Rust `HashSet` | std: `Set<T>` over the map, `Insert`, `Contains`, `Remove`, `Len`, `range`; `map[T]()` is what programs write today | stage 9 |

### Outside the library, by decision 7 and stage 8

Not gaps, but listed so that the review has the whole picture: `regexp` (task 8.2), `json`, `net/http`, `encoding/*` (`base64`, `hex`, `csv`, `binary`), `compress/*`, `crypto/*` and `hash/*` (`sha256`, `md5`, `crc32`, through C bindings), `database/sql`, `html`, `text/template`, `image`, `archive`, `embed`, `plugin`, `reflect`, `unsafe`, `runtime` (its one function is `os.Cores`), `go/*`.

## What the recommendations add up to

If every **std** line above is accepted, the library grows by roughly: `math` and `math/bits` and `rand`, the `unicode` and rune work, `errors`, `bytes`, `path`, about fifteen methods on strings, slices, maps, options and results, ten `os` functions and `File.Seek`, `time`'s calendar and formatting, `net`'s DNS and IPv6, atomics, `Deque` and `Set`, timers as values, the fuzz targets and the Go twins. That is on the order of two to three thousand lines of Snello and a few hundred of C, a stage's worth, and it would make the first hour of a Go programmer's port go without a missing name. The **later** lines, `testing`, `os/exec`, signals and structured concurrency, each need a design note before a line of code.
