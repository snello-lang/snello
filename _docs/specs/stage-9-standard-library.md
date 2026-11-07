# Stage 9: the standard library's gaps

Goal: close the gaps of `standard-library-gaps.md` that the owner decided in (2026-11-07), before stage 8 builds the first library outside the compiler on the core. The library stays under decision 7 of `../design/05-standard-library.md`: every addition is a name a Go programmer reaches for in the first hour of a port, written on its reference algorithm and measured where it matters, and nothing that encodes a format, speaks a protocol or wraps a system. Two things come first because everything after them is tested through them: a `testing` package with `snello test`, so that the library's own tests are written in Snello, and the error trait's capabilities, since every new function reports failures through it. The solidity work, fuzzing and Go twins, comes before the widening, since it finds bugs in what exists. Each task's design is recorded in the design note before its code, reviewed by the owner, as every library decision has been.

The order ahead, decided 2026-11-07: this stage, then 8, then 7, then 6.

## Task 9.1: `testing` and `snello test`

**Description:** A program's or a library's own tests have no way to be written in Snello today; the compiler's suites are Go. The design decides, in a note of its own, `../design/09-testing.md`: a `_test.snello` file beside a package's files is part of the package when tested and not otherwise, as Go's `_test.go` is; a test is `func TestName(t mut testing.T)` in such a file, found by name; `snello test DIR` builds the package with its tests and a generated `main` that runs them, reports each as Go's `go test -v` does, and exits with a failure when one fails; `t.Errorf`, `t.Fatalf`, `t.Log`, `t.Run` for subtests, `t.Skip`; a test program that panics is a failure naming the test. Benchmarks and examples as tests are out of this task. The `testing` package is ordinary Snello over `fmt` and `os`; what the compiler writes is the `main` that calls the tests found.

**Acceptance criteria:**
- [ ] `../design/09-testing.md` written and reviewed by the owner before the code
- [ ] `snello test DIR` runs the tests of a package of the program's own and of a `main` package, through both backends, reporting each test and exiting as Go's `go test` does
- [ ] `_test.snello` files are left out of `snello build` and `snello check` and refused in the standard library
- [ ] The library's own tests begin: one package of the standard library has a `_test.snello` run by `go test ./std` through `snello test`
- [ ] Chapter 2 of the book says how a test is written and run; Appendix B maps `go test`

**Verification:**
- [ ] `go test ./cmd/snello ./compiler/importer ./std`
- [ ] Tests pass: `go test ./...`

**Dependencies:** Stage 5
**Files:** `std/testing/`, `cmd/snello/`, `compiler/importer/`, `../design/09-testing.md`, `../book/02-getting-started.md`, `../book/B-snello-for-go-programmers.md`
**Scope:** L

## Task 9.2: The `Error` trait, whole

**Description:** There is no `errors` package, at the owner's word: the one trait does the work, with what Rust's `Error` and Go's `error` make a program write by hand done by the language. Three things, designed first in `../design/05-standard-library.md` and in `../design/04-borrows-ref-and-mut.md` where `?` is: `Source() (trait Error)?` on the trait with a default of none, so a cause is one method and a chain is walked by asking, and `fmt` prints the chain after the message with `%+v`; `?` converting the callee's error into the caller's when the caller's error enum has exactly one variant whose single field is the callee's error type, so `Io(e io.Error)` takes an `io.Error` with nothing written, two such variants refused as ambiguous and a conversion written then; and a derived `Error()` for an enum that lists `Error` and writes none, printing the variant and its fields. The library's own `io.Error` and `strconv.Error` gain `Source` where they wrap.

**Acceptance criteria:**
- [ ] The design recorded and reviewed by the owner before the code: the trait's new method, the `?` rule and its refusal, the derived message
- [ ] A program whose error enum wraps `io.Error` and `strconv.Error` uses `?` on both with no conversion written, through both backends; the ambiguous case is refused with the message designed
- [ ] `Source` walked and printed; a derived `Error()` on an enum
- [ ] Chapter 8 of the book rewritten for it; example 09 uses it

**Verification:**
- [ ] `go test ./compiler/types ./compiler/emit/... ./examples`
- [ ] Tests pass: `go test ./...`

**Dependencies:** 9.1
**Files:** `compiler/types/`, `compiler/emit/`, `std/builtin/`, `std/io/`, `std/strconv/`, `examples/09-errors.snello`, `../book/08-option-result.md`
**Scope:** M

## Task 9.3: Solidity: fuzzing and Go twins

**Description:** `strconv`, the quote reader, `fmt`'s format grammar, `ParseFloat` and `bufio`'s line reading take input the program does not control and have never run under a fuzzer. A Go fuzz target per parser, under `go test -fuzz`, with a corpus checked in and the targets run for a bounded time in CI. And a Go twin per package whose behaviour is defined as Go's, `strconv`, `strings`, `sort`, `time`'s `String`, `bufio`, run by the suite as `formats` is, so that every package's output is pinned to Go's line for line.

**Acceptance criteria:**
- [ ] Fuzz targets for the five parsers, with corpora, finding nothing after a run of ten minutes each; any finding fixed with its input added to the corpus
- [ ] A Go twin for each package named, in `examples/run/`, matched line for line in the run suite of both backends
- [ ] The CI workflow runs each fuzz target for one minute on one runner

**Verification:**
- [ ] `go test -fuzz=. -fuzztime=10m` per target, once; `go test ./...`

**Dependencies:** 9.1
**Files:** `runtime/`, `compiler/types/format_test.go`, `examples/run/`, `.github/workflows/test.yml`
**Scope:** M

## Task 9.4: Unicode and runes

**Description:** A string is bytes, and the case, white-space and printable tables are Unicode 17 already; what is missing is the rune. `for _, r := range s` yields runes with their byte offsets, as Go's does, a language rule recorded in `../design/00-syntax-conventions.md`; `s.Runes()` makes an owned `[]rune`; `strings.Builder.WriteRune`, `bufio.Reader.ReadRune`; a `unicode` package with `IsLetter`, `IsDigit`, `IsSpace`, `IsUpper`, `IsLower`, `IsPrint`, `ToUpper`, `ToLower` on runes over the runtime's tables; `utf8.RuneLen`, `utf8.EncodeRune`, `utf8.DecodeRune` as functions of `unicode/utf8`.

**Acceptance criteria:**
- [ ] A range over a string yields runes and offsets through both backends; an invalid sequence yields the replacement rune and advances one byte, as Go's does
- [ ] The `unicode` and `unicode/utf8` packages, with a Go twin matching rune by rune over the tables' ranges
- [ ] Chapters 3 and 14 cover runes

**Verification:**
- [ ] `go test ./compiler/... ./std ./examples`

**Dependencies:** 9.3
**Files:** `compiler/types/`, `compiler/emit/`, `std/unicode/`, `std/builtin/`, `std/strings/`, `std/bufio/`, `runtime/c/unicode.c`
**Scope:** L

## Task 9.5: Strings, slices, maps, options and results

**Description:** The methods every program reaches for. On `string`: `Index`, `LastIndex`, `Replace`, `ReplaceAll`, `TrimPrefix`, `TrimSuffix`, `TrimSpace`, `Count`, `SplitN`; `strings.NewReader` as an `io.Reader`; `Builder.Grow`. On `[]T`: `Extend(ref []T)`, `Index(ref T) int?`, `First() ref T?`, `Last() ref T?`, `Truncate(n)`. On `map[K]V`: `GetOrInsert(k, v) mut V`, `Len` as `len` stays. On `T?`: `AndThen`, `OrElse`. On `Result<T, E>`: `Ok() T?`, `Err() E?`, `AndThen`, `UnwrapOrElse`. On every integer type: `Abs`, `Clamp`, `CheckedAdd`, `CheckedSub`, `CheckedMul` returning `T?`, which chapter 3 promises; `[]byte` gains `Equal` and `Index`. Each is written on its reference algorithm; `Index` and `Replace` on the search `Contains` has.

**Acceptance criteria:**
- [ ] Every name above in `std/builtin` or its package, documented there, in the library table and in chapter 9 or 14
- [ ] A Go twin in `examples/run/` for the string and slice methods, matched line for line
- [ ] `Index` on a 20 MB text with a 50,001-byte needle takes under two seconds, as `Contains` does

**Verification:**
- [ ] `go test ./compiler/types ./compiler/emit/... ./examples`

**Dependencies:** 9.3
**Files:** `std/builtin/`, `std/strings/`, `runtime/c/string.c`, `runtime/c/slice.c`, `compiler/emit/lower/`
**Scope:** M

## Task 9.6: `math`, `math/bits` and `rand`

**Description:** `math` fills in: `Floor`, `Ceil`, `Round`, `Trunc`, `Pow`, `Exp`, `Log`, `Log2`, `Log10`, `Sin`, `Cos`, `Tan`, `Asin`, `Acos`, `Atan`, `Atan2`, `Sinh`, `Cosh`, `Tanh`, `Hypot`, `Mod`, `Cbrt`, `Inf`, `NaN`, `IsNaN`, `IsInf`, `Signbit`, `Copysign`, `E`, `Ln2`, `MaxInt`, `MinInt`, `MaxFloat64`, `SmallestNonzeroFloat64` and the sized limits; each a bound function to libm or a constant. `math/bits`: `LeadingZeros`, `TrailingZeros`, `OnesCount`, `RotateLeft`, `Reverse`, `Len`, which the backends write inline. `math/rand`: a seedable generator, Go 1.22's PCG, with `Int`, `IntN`, `Int64`, `Float64`, `Shuffle`, `Perm`, and the package functions over a generator seeded from the runtime's random source.

**Acceptance criteria:**
- [ ] Every name above, with a Go twin matched line for line on fixed inputs and, for `rand`, on a fixed seed against Go's PCG
- [ ] `bits` written inline by both backends: the C backend to the compiler's builtins, the LLVM backend to the intrinsics

**Verification:**
- [ ] `go test ./std ./compiler/emit/... ./examples`

**Dependencies:** 9.3
**Files:** `std/math/`, `std/math/bits/`, `std/math/rand/`, `runtime/c/math.c`, `compiler/emit/`
**Scope:** M

## Task 9.7: `fmt` and `strconv`

**Description:** `fmt` gains `Fprint`, the verbs `%x`, `%X`, `%o`, `%b`, `%c`, `%e`, `%g`, a width and the `-` flag, and `Errorf` is not added, task 9.2's `?` and `Source` taking its place. `strconv` gains `ParseInt(s, base)` and `ParseUint`, `FormatInt(n, base)` and `FormatUint`, `ParseBool` and `FormatBool`, `Quote` and `Unquote` over the runtime's quoting, `FormatFloat(f, prec)`, and `f32` through `ParseFloat32` and `FormatFloat32`. The format grammar in `compiler/types/format.go` checks the new verbs as it checks the old.

**Acceptance criteria:**
- [ ] Every verb and function above, matched line for line against a Go twin, `examples/run/formats.snello` extended
- [ ] A verb against the wrong argument refused when the program is compiled, as today

**Verification:**
- [ ] `go test ./compiler/types ./compiler/emit/... ./examples`

**Dependencies:** 9.3
**Files:** `std/fmt/`, `std/strconv/`, `compiler/types/format.go`, `compiler/emit/`, `runtime/c/format.c`, `runtime/c/parse.c`
**Scope:** M

## Task 9.8: `io`, `bufio` and `bytes`

**Description:** `io` gains `ReadAll`, `ReadFull`, `Discard`, a `Seeker` trait with `Seek` on `os.File`; `bufio.Reader` gains `Peek`, `ReadBytes`, `UnreadByte`; `bufio.Scanner` gains `Words()` as a second constructor; a `bytes` package with `Buffer` as a growable `io.Writer` and `io.Reader`, the structure every encoder writes into, over the slice the runtime has.

**Acceptance criteria:**
- [ ] Every name above, with `bytes.Buffer` and `Seek` matched against a Go twin; `io.ReadAll` on a 100 MB file within 1.25 times Go's time
- [ ] Chapter 14 and the library table

**Verification:**
- [ ] `go test ./std ./compiler/emit/... ./examples`

**Dependencies:** 9.3
**Files:** `std/io/`, `std/bufio/`, `std/bytes/`, `std/os/`, `runtime/c/os.c`, `runtime/c/bufio.c`
**Scope:** M

## Task 9.9: `os` and `path`

**Description:** `os` gains the environment, `Getenv`, `LookupEnv`, `Setenv`, `Environ`; directories, `ReadDir` returning entries with name and kind, `Mkdir`, `MkdirAll`, `Stat` and `Lstat` returning a `FileInfo` with size, mode, modification time and whether a directory, `Rename`, `Getwd`, `Chdir`; `ReadFile` as bytes, `WriteFile`, `OpenFile(path, flags, mode)` with append, create, truncate and exclusive; `TempDir`, `Getpid`, `Hostname`. A `path` package of pure string functions, `Join`, `Base`, `Dir`, `Ext`, `Clean`, `Split`, `IsAbs`, with `filepath` as its name for the system's separator as Go has both. `os/exec` and signals stay out, each a design note of its own after this stage.

**Acceptance criteria:**
- [ ] Every name above through both backends, `examples/run/files.snello` extended and a Go twin for `path`
- [ ] Every descriptor opened closed on exec, as the existing ones are; every error an `io.Error` worded as Go's

**Verification:**
- [ ] `go test ./std ./compiler/emit/... ./examples`

**Dependencies:** 9.3
**Files:** `std/os/`, `std/path/`, `std/path/filepath/`, `runtime/c/os.c`
**Scope:** M

## Task 9.10: `time`

**Description:** A calendar in UTC: `Date(year, month, day, hour, min, sec, nsec)`, `Year`, `Month`, `Day`, `Hour`, `Minute`, `Second`, `Nanosecond`, `Weekday`, `YearDay`, `AddDate`; `Format` and `Parse` with Go's reference layout, `2006-01-02 15:04:05`, and the layouts `RFC3339`, `DateTime`, `DateOnly`, `TimeOnly`, `Kitchen`; `ParseDuration`; `Timer` and `Ticker` as values with `Stop` and `Reset`, their `Drop` stopping them, over the runtime's timer heap; `Truncate` and `Round` on `Time`. Zones stay out: UTC only, as decided, with `Local` a later decision.

**Acceptance criteria:**
- [ ] Every name above, `Format` and `Parse` matched against a Go twin over a table of times and layouts, the calendar against Go's over a range of dates including leap years
- [ ] A `Timer` stopped before it fires sends nothing; a `Ticker` dropped ticks no more; `examples/run/clocks.snello` extended

**Verification:**
- [ ] `go test ./std ./compiler/emit/... ./examples`

**Dependencies:** 9.3
**Files:** `std/time/`, `runtime/c/time.c`, `runtime/c/sched.c`
**Scope:** L

## Task 9.11: `sync` and the collections

**Description:** Atomics: `sync.AtomicInt` and `sync.AtomicBool` with `Load`, `Store`, `Add`, `Swap`, `CompareAndSwap`, over the runtime's atomics, `Send` by structure; `WaitGroup.Go(f)` as Go 1.25's. `Deque<T>` with `PushFront`, `PopFront`, `PushBack`, `PopBack`, `Front`, `Back`, `Len`, a ring over a slice; `Set<T>` over the map with `Insert`, `Contains`, `Remove`, `Len`, `range`; `heap.Reverse<T>` as Rust's `cmp::Reverse`; `range` over an `arena.Arena<T>` yielding `(Index<T>, ref T)`; `rc.TryUnwrap`.

**Acceptance criteria:**
- [ ] Every name above, `Deque` and `Set` measured against Go's `container/list` replacement idioms and Rust's `VecDeque` and `HashSet` on a million operations, with the numbers in the library table
- [ ] Example 04's BFS uses `Deque`; chapter 10 covers atomics with the ownership note's sentence updated

**Verification:**
- [ ] `go test ./std ./compiler/emit/... ./examples ./bench`

**Dependencies:** 9.3
**Files:** `std/sync/`, `std/deque/`, `std/set/`, `std/heap/`, `std/arena/`, `std/rc/`, `runtime/c/sync.c`
**Scope:** M

## Task 9.12: `net`: names and IPv6

**Description:** `Dial("example.com", 443)` is refused today. Names resolve through the system's resolver, `getaddrinfo`, on the poller's thread so that a task does not block a worker, with a `LookupHost` returning the addresses; IPv6 addresses listen, dial and print as Go's do; `Conn.LocalAddr`, `Conn.RemoteAddr`, `Listener.Addr` as strings; `DialTimeout` covers the lookup. UDP, Unix sockets and TLS stay out: UDP until a program needs it, TLS as a library of stage 8 over a system library through the binding of chapter 15.

**Acceptance criteria:**
- [ ] A dial by name and by IPv6 address through both backends, with the lookup's failure an `io.Error` worded as Go's, `dial tcp: lookup nosuch.invalid: no such host`
- [ ] A lookup that hangs does not hold a worker: a program of two tasks, one in a lookup against an unreachable resolver and one computing, finishes the computation
- [ ] `examples/run/net.snello` extended; chapter 14

**Verification:**
- [ ] `go test ./std ./compiler/emit/... ./examples`

**Dependencies:** 9.3
**Files:** `std/net/`, `runtime/c/net.c`
**Scope:** L

## Checkpoint: stage 9

- [ ] `snello test` runs the standard library's own tests, written in Snello, and the suite runs them in CI
- [ ] Every parser fuzzed with a corpus checked in, and every package with a Go twin matched line for line
- [ ] Every name the gaps document decided `std` exists, documented in the library table and the book, with the measurements the tasks name
- [ ] The library still holds the note's packages and the stage's, and nothing more; the `later` rows of the gaps document each have a design note named, written or not
- [ ] Reviewed by the owner
