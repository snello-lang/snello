# Stage 9: the standard library's gaps

Goal: close the gaps of `standard-library-gaps.md` that the owner decided in (2026-11-07), before stage 8 builds the first library outside the compiler on the core. The library stays under decision 7 of `../design/05-standard-library.md`: every addition is a name a Go programmer reaches for in the first hour of a port, written on its reference algorithm and measured where it matters, and nothing that encodes a format, speaks a protocol or wraps a system. Two things come first because everything after them is tested through them: a `testing` package with `snello test`, so that the library's own tests are written in Snello, and the error trait's capabilities, since every new function reports failures through it. Fuzzing goes with the task that touches each parser, 9.9 and 9.10, and no package pins its output to Go's (task 9.5, dropped 2026-11-07). Each task's design is recorded in the design note before its code, reviewed by the owner, as every library decision has been.

The order ahead, decided 2026-11-07: this stage, then 8, then 7, then 6. Within the stage, task 9.2 was added on 2026-11-07 ahead of everything else when a race through a package-level `var` was found, and 9.3 behind it, decided: every package of the library has its tests.

## Task 9.1: `testing` and `snello test`

**Description:** A program's or a library's own tests have no way to be written in Snello today; the compiler's suites are Go. The design is `../design/09-testing.md`, decided simpler than Go's and not Go copied: a `_test.snello` file beside a package's files is part of the package when tested and not otherwise; a test is `func TestName()`, a plain function with no handle, found by name; a failed assertion is a panic, which the runner catches at the test's boundary through a mark the runtime gains, unwinding the test's values; cleanup is `Drop`; `snello test DIR` builds the package with its tests and a generated `main` and runs it, reporting as `go test -v` does. The `testing` and `expect` packages are rich enough that no library outside them is needed, in three slices: the runtime's mark, `sn_catch`, first, since `expect.Panics` and fuzzing wait on it too; then `expect.Equal`, `True`, `Fail`, `testing.Run` for subtests, `Log`, `Skip`, `TempDir` and the runner; then the diffs `go-cmp` gives, `Some`, `Ok`, `Contains`, `Panics`, golden files with `-update`, `testing.Parallel`, benchmarks with `testing.Loop` reporting ns/op and allocations per op from the runtime's allocator, and the flags `-run`, `-bench`, `-v`, `-count`, `-failfast`, `-timeout`, `-shuffle`, `-short`. What the compiler writes is the `main` that calls the tests found, and the file, line and expression text the two packages' functions report.

**Acceptance criteria:**
- [x] `../design/09-testing.md` reviewed by the owner before the code (2026-11-07: three rounds, the handle dropped, the failure message's rules, no `-race`)
- [x] The runtime's mark: a panic on a task with a mark unwinds to it, dropping the values with `Drop` made since, and is returned as a message; one without ends the process as today; programs in `backendtest` through both backends, release and debug, the debug build reporting nothing left, but for the one program that pins what the design leaves unfreed, the plain values of the abandoned frames, which runs as a release build (2026-11-07: `sn_catch` and the mark in `sched.c`, `testing.Catch` lowered through an adapter in each backend, the programs `catch`, `catch-abandoned`, `catch-drop-panics` and `catch-across-threads`; the review found the LLVM backend cloning a panic's message where the C backend did not, a leak under a mark, fixed)
- [x] `snello test DIR` runs the tests of a package of the program's own and of a `main` package, through both backends, reporting each test and exiting as Go's `go test` does; a failed assertion prints the expression as written, got and want in that order with their types and sizes, the first difference in words, the values of a condition's operands, and the subtest's case, in the form the design note shows, each form pinned by a test (2026-11-07: `compiler/testrun` plans, generates, builds and runs; `snello test` with every flag; the compiler writes the file, line, call text and parts before every call of an exported `expect` function and of `testing.Log`, `Logf` and `Skip`, `ast.Source` printing the call as written, the operands' values for a comparison of simple operands; `TestRun` and `TestMore` pin the forms through both backends. The subtest's case is its name, `TestDouble/wrong`, since a closure's captured row is the test's to name; an assertion nested in another's arguments reports the outer's place, documented, the style being one assertion per line)
- [x] `_test.snello` files are left out of `snello build`, `snello check`, `snello emit` and of every import, `testing` and `expect` are refused from any file but a test file, and a test function with the wrong signature is refused with the message designed; a program's binary built beside its tests holds no symbol of either package, checked by the suite (2026-11-07: `importer.SourceFiles` and `TestFiles`, `checkTestImports`, `TestTestFiles`; the signature refused at discovery; the symbols checked in `TestRun` through both backends)
- [x] The library's own tests begin: one package of the standard library has a `_test.snello` run by `go test ./std` through the runner, under the library's privileges (2026-11-07: `std/strings/strings_test.snello`, run by `std/std_test.go` through `testrun.PlanStd` in both backends)
- [x] The place of a call is the running task's: two parallel tests asserting at once never share it (2026-11-07: found while checking the book's claim of race freedom, `where` and `parts` were package variables of `testing` and `expect`; now `sn_testing_line` and `sn_testing_call` in `runtime/c/testing.c` keep them on the task and the packages' bodiless `where()` and `parts()` read them back; `TestMore`'s parallel pair asserts twenty thousand times each while one fails)
- [x] The review before the pull request (2026-11-07, five axes, by a reviewer reading the design note first): a second `testing.Parallel` call stood in for the test's verdict, now refused with `testing.Parallel called twice`; `-count` and `-timeout` took garbage as zero, now typed flags in `snello test` and checked in `Main` with exit code 2; `-run .` runs every test; an `expect` function taken as a value lost its place, now refused by both backends; a benchmark failing an expectation is reported with `--- FAIL`; the timeout's line goes to the standard error, and a test binary ended by a signal exits 2 with a line; `sn_panic` takes the mark path on a task alone, never from the timers' or the poller's thread; the parts decision shared in `lower.Parts`, with `len` recognized as the predeclared function; the design note's rules 3 to 5, samples and table brought to what was built; a checker crash on a package whose check failed, met on the way, made a plain error
- [x] The third slice: the diffs, the option and result assertions, `Panics`, golden files with `-update`, `Parallel`, benchmarks reporting ns/op and allocations per op, and the flags, each with a test of the packages themselves in Snello (2026-11-07: `EqualSlice` and `EqualText` say the first difference; `IsSome`, `IsNone`, `IsOk`, `IsErr`, named with `Is` since the variants own the bare names; `Golden` with `-update` into a `testdata` the author makes until task 9.11; `Parallel` behind a gate with each test in a task and its lines held; `Loop`, `ResetTimer`, `ReportAllocs` with the allocator's counts; `-count`, `-failfast`, `-timeout`, `-shuffle`, `-short`; `TestMore` drives each through the runner, and `std/strings/strings_test.snello` has a benchmark)
- [x] Chapter 2 of the book says how a test is written and run, with a table-driven test, an assertion and a benchmark; Appendix B maps `go test`, `testing.T`, `testify`, `go-cmp` and `goldie` (2026-11-07: the tests section of chapter 2, cut from `examples/run/packaged/shapes/shapes_test.snello`; six rows of Appendix B)

**Verification:**
- [x] `go test ./cmd/snello ./compiler/importer ./compiler/emit/... ./std` (2026-11-07, and `./compiler/testrun`)
- [x] Tests pass: `go test ./...` (2026-11-07, after each slice)

**Dependencies:** Stage 5
**Files:** `runtime/c/sched.c`, `std/testing/`, `std/expect/`, `cmd/snello/`, `compiler/importer/`, `compiler/emit/lower/`, `../design/09-testing.md`, `../book/02-getting-started.md`, `../book/B-snello-for-go-programmers.md`
**Scope:** XL, three slices

## Task 9.2: Package variables and data-race freedom

**Description:** A package-level `var` is a plain shared global today: the checker moves what a `spawn` mentions, but a global is not captured, so any task may assign one, lend it as `mut` or move out of it, and two tasks writing one lose updates or free a string twice. Found on 2026-11-07 while checking the book's claim that a data race is a compile error, and decided the same day: there will never be a race in Snello. The design is the section "Package variables" of `../design/03-ownership-across-tasks.md`, three sentences: a package variable may be read from any task and is never assigned, lent as `mut` or moved out of; its type must be `Send`; a change goes through an atomic or a lock it holds. With it come `sync.Counter` (`Add`, `Load`, `Store`, `CompareAndSwap`, one instruction each) and `sync.Atomic<T>` of any `Send`, `Clone` and `Eq` type (`Load`, `Store`, `Swap`, `CompareAndSwap`, under a lock it owns), pulled forward from task 9.13 since a counter is the first global anyone writes; initializers that call, made once on first use under the runtime's once-guard, as Rust's `LazyLock` and not Go's `init` order; and the standard streams as functions, `os.Stdout()`, so that nothing lends a `mut` of a global. Locals stay mutable, recorded with the owner's `val`/`var` question answered in the note. The gap sentences written on 2026-11-07 into the design notes, chapter 10 and Appendix B come out.

**Acceptance criteria:**
- [x] The design reviewed by the owner before the code: the rule and its message, what a package variable may hold, lazy initialization, the streams as functions, atomics in `sync`, locals unchanged (2026-11-07: the note's section "Package variables", reviewed as uncommitted text; the atomics decided twice the same day, a hardware `Atomic<T: Integer>` first and then `Counter` with `Atomic<T>` of anything, decided)
- [x] The checker refuses an assignment, a `mut` lent or taken by a method, a move out, and a `match` or `if v :=` binding by value, each with the message designed, and a non-`Send` type in a package variable; the programs under `examples/core/errors/`: the counter, the slice pushed to, the string assigned, each from two tasks, the map moved out, the `rc` handle held (2026-11-07: one check in the borrow checker's `access`, a package variable of another package made a place; `global-assigned`, `global-mut`, `global-moved`, `global-not-send`; `globals` in the backends' programs runs what is allowed from two tasks)
- [x] `sync.Counter` over `int` on C11 atomics, and `sync.Atomic<T>` of any `Send`, `Clone` and `Eq` type over the runtime lock, through both backends; a counter from two tasks exact, measured against `Mutex<int>` (2026-11-07: `atomics` program; 7 ns and 17 ns contended through the word against 10 ns and 10 to 13 ns through the lock, in `05-standard-library.md`, decided 10)
- [x] A package variable initialized by a call is made once on first use, from two tasks at once, through both backends; never used, never made; the debug build reports nothing left (2026-11-07: `sn_lazy_make` in `sched.c`, `lazyVar` in both backends; `lazy-globals`, `lazy-cycle`, `lazy-panic`)
- [x] `os.Stdin()`, `os.Stdout()` and `os.Stderr()` as functions returning a handle whose drop never closes the descriptor; the eighteen uses in the library, the examples and the book changed; `testing`'s variables one `Mutex<Config>` and the benchmark's state in the runtime; `report.snello` and `text.snello` rewritten as the first programs under the rule (2026-11-07: the registry in a `Mutex<Registry>` as well, with the round's channels cloned out of it, since nothing waits under the lock)
- [x] Chapters 3, 10 and 14 and Appendix B state the rule and show the atomic, the lock and the lazy variable; the gap sentences removed (2026-11-07: chapter 10's section "Package variables", chapter 16's bullet, `07-lowering.md`'s section on the lazy form)

**Verification:**
- [x] `go test ./compiler/types ./compiler/emit/... ./std ./examples` (2026-11-07)
- [x] Tests pass: `go test ./...` (2026-11-07)

**Dependencies:** 9.1
**Files:** `compiler/types/`, `compiler/emit/c/`, `compiler/emit/llvm/`, `compiler/emit/lower/`, `runtime/c/`, `std/sync/`, `std/os/`, `std/testing/`, `examples/core/errors/`, `examples/run/`, `../design/03-ownership-across-tasks.md`, `../design/05-standard-library.md`, `../book/03-basics.md`, `../book/10-concurrency.md`, `../book/14-standard-library.md`
**Scope:** L, four slices: the checker; atomics and lazy initializers; the library and the examples; the book

## Task 9.3: The library's tests, every package, and the way they run

**Description:** Task 9.1 gave the standard library its first test file, `std/strings/strings_test.snello`, run by `go test ./std` through the runner. Decided 2026-11-07, three things that belong together: every package has its tests; the tests run by Snello's own command, not only through a Go wrapper; and the command runs the same way on every machine. The library's tests are Snello tests, in `_test.snello` files beside each package, found and run by the same runner `snello test` uses, under the library's privileges and through both backends: the proof that the library works is the library's own runner on the library's own code, which `go test ./std` only wraps for CI.

*The tests.* A `_test.snello` in every package but `expect` and `testing`, which `compiler/testrun`'s Go tests drive through the runner and which cannot assert on themselves: `arena`, `bufio`, `builtin`, `fmt`, `heap`, `io`, `math`, `net`, `os`, `rc`, `sort`, `strconv`, `strings` (extended), `sync` and `time`, by the size of their exported surface `builtin` 54 names, `sync` 34, `time` 26, `bufio` 20, `os` 19, `net` 15, `rc` 13, `strings` 10, `io` 8, `heap` 8, `arena` 7, `strconv` 6, `fmt` 6, `sort` 4, `math` 4. Every exported function and method is called at least once, with a table where the name has cases, and every package has at least one benchmark; `net` tests on a loopback listener, `os` and `bufio` on `testing.TempDir`, `sync` from two tasks. What a test finds wrong in a package is fixed in the same change and listed under the criteria with its fix.

*The command.* `snello test std/NAME`, in a checkout of the compiler, runs the standard package's tests as `snello test DIR` runs a program's: the runner plans through the library's path (`testrun.PlanStd`) when the directory is a standard package's own, named by its last two components under the repository, reading the files from the directory so that the tests run against the sources as edited and not the copy embedded in the binary that runs them; `-backend` chooses the backend as for any package. `std/std_test.go` stays as CI's entry, a call of the same plan for every package with a test file.

*The Makefile and the image.* A `Makefile` at the root and a `Dockerfile` under `build/`: one image on `debian:trixie-slim` with Go pinned to the version of `go.mod` and `clang` and `lld` as Debian's packages, nothing else, since the runtime uses glibc's thread and scheduler calls and the tests measure speed, which musl would change. The Makefile runs Snello, decided 2026-11-07: `make build`, the compiler into `bin/snello`, which the ignore file hides, the one Go command since the compiler is Go; `make test`, `snello test` over every library package with a test file and every example package with tests, through both backends, failing on the first package that fails, with `make test-std` and `make test-examples` as its halves; `make check`, `snello check` over the examples; `make shell`, the container with the repository mounted. Every target runs inside the image by default through `build/run.sh`, `docker run` with the repository mounted and the Go build cache in a named volume so a second run is fast, and `NATIVE=1` runs the same target on the host for a machine without Docker. The Go suite stays `go test ./...` and the benchmarks `go test ./bench`, which CI's Linux jobs run inside the same image through `build/run.sh`, so a green run on a laptop and a green job mean the same thing; the macOS job stays native, since it covers kqueue. On a Mac the container is an arm64 Linux, so the Linux arm64 job reproduces locally and the x86-64 job stays CI's.

**Acceptance criteria:**
- [x] A `_test.snello` in each of the fifteen packages, every exported name called at least once, at least one benchmark each; `go test ./std` green through both backends; any package bug found listed here with its fix (2026-11-07: every file an external test package, `package NAME_test`, which task 9.1's design had left out and the first package after `strings` needed, since `testing` imports `sync`, `fmt`, `os` and `time` and a test inside one of those is an import cycle; two backend bugs found and fixed, a generic function returning its `T` given `()` emitted as a void function that still returned a value, `generic-unit-return`, and `Eq` on `time.Duration` through a bound reported unsupported instead of going to the predeclared type's own, `boundCall` in both backends; `os.Exit` is the one name no test calls, since it ends the process; a test build is a release build, as task 9.1 decided)
- [x] `snello test std/NAME` runs a standard package's tests from its sources in the checkout, through either backend, with the package's privileges; refused with a clear message outside a checkout of the compiler; `TestRun` covers it (2026-11-07: `Importer.StdDir` reads the whole library from the checkout, `testrun.PlanStdDir`, `TestStdDir`; a scratch copy with an edited assertion failed on the edited line; the review found a project's own `std/strings` taken for the library's and the checkout now has to hold every package, and found a program able to import `NAME_test` and carry the harness, closed in `checkTestImports`)
- [x] `Makefile` and `build/Dockerfile`: `make build`, `make test`, `make test-std`, `make test-examples`, `make check` and `make shell`, each in the image by default and native with `NATIVE=1`; the image builds from the pinned versions alone (2026-11-07: Debian trixie slim, Go from `go.mod`, Debian's clang and lld; `check`, `test-std` and `test-examples` green inside the image on an arm64 Mac and natively; `go test` kept out of the Makefile, which runs Snello, and run by CI through `build/run.sh` with the bench table under `bin/` so that no artifact lands in the tree)
- [x] The CI workflow's Linux jobs run in the image, cached by the `Dockerfile`'s hash, through the same `build/run.sh`; the macOS job native as today; the five jobs green (2026-11-07: the workflow builds it with GitHub's layer cache keyed on the processor and the Dockerfile's hash, the same four steps in the test job and the benchmark job; `go test ./...`, `make test` and the benchmarks in it)
- [x] Chapter 16's section for maintainers says `make test-std` and `snello test std/NAME`, and the README's build and test lines say `make build` and `make test`; `bench/README.md` says how the Mac's numbers are taken natively (2026-11-07; the external form in chapter 16's first section and in `09-testing.md`, decided 5, with the two `expect` limits the writing showed, for task 9.7)

**Verification:**
- [x] `make test`, `make check` in the image and native (2026-11-07; `build/run.sh go test ./...` in the image)
- [x] Tests pass: `go test ./...` (2026-11-07)

**Dependencies:** 9.2
**Files:** `std/*/*_test.snello`, `std/std_test.go`, `compiler/testrun/`, `cmd/snello/`, `Makefile`, `build/Dockerfile`, `.github/workflows/test.yml`, `.gitignore`, `README.md`, `bench/README.md`, `../book/16-testing.md`
**Scope:** L, three slices: the command and the Makefile with the image, so that the tests that follow run through them; the fifteen test files, in the order of the surface above, each a commit; CI and the book

## Task 9.4: The `Error` trait, whole

**Description:** There is no `errors` package: the one trait does the work, with what Rust's `Error` and Go's `error` make a program write by hand done by the language. Three things, designed first in `../design/05-standard-library.md` and in `../design/00-syntax-conventions.md` where the rules of `?` are: `Source() (trait Error)?` on the trait with a default of none, so a cause is one method and a chain is walked by asking, and `fmt` prints the chain after the message with `%+v`; `?` converting the callee's error into the caller's when the caller's error enum has exactly one variant whose single field is the callee's error type, so `Io(e io.Error)` takes an `io.Error` with nothing written, two such variants refused as ambiguous and a conversion written then; and a derived `Error()` for an enum that lists `Error` and writes none, printing the variant and its fields. The library's own `io.Error` and `strconv.Error` gain `Source` where they wrap.

**Acceptance criteria:**
- [x] The design recorded and reviewed before the code: the trait's new method, the `?` rule and its refusal, the derived message (2026-11-07: decided 11 of the library note and the `?` bullet of the syntax conventions; `?` also passes an error into a trait value, so `Result<T, Error>` is Go's `error`; the derived message is the value as `%v` prints it, for a struct as for an enum; `%+v` is refused on anything but an error; a trait value of `Error` or `fmt.Stringer` prints)
- [x] A program whose error enum wraps `io.Error` and `strconv.Error` uses `?` on both with no conversion written, through both backends; the ambiguous case is refused with the message designed (2026-11-07: `error-pass` in the backend tests and `examples/run/errors.snello`; `try-ambiguous` and `try-no-variant` under `examples/core/errors`)
- [x] `Source` walked and printed; a derived `Error()` on an enum (2026-11-07: `error-chain` and `error-derived`, a derived message on a struct and a type over `int` as well; `strconv.Error` lists `Clone` so that a `Source` can give it; the checker refuses a derived message over a field that cannot print, `error-unprintable`, and `%+v` on a non-error, `format-plus`)
- [x] Chapter 8 of the book rewritten for it; example 09 uses it (2026-11-07: errors got a chapter of their own, 17, cut from `examples/run/errors.snello`, with chapter 8's `?` section pointing at it; example 09 drops its two `MapErr` closures and its hand-written `Error()`, and prints a chain through a `LoadError` with `Source`)

**Verification:**
- [x] `go test ./compiler/types ./compiler/emit/... ./examples` (2026-11-07)
- [x] Tests pass: `go test ./...` (2026-11-07, natively, with `make test` and `make check`; every Go file the task touched has its test file beside it)

**Dependencies:** 9.1
**Files:** `compiler/types/`, `compiler/emit/`, `std/builtin/`, `std/io/`, `std/strconv/`, `examples/09-errors.snello`, `../book/08-option-result.md`
**Scope:** M

## Task 9.5: Dropped: no Go twins, and the fuzzing folded into 9.9 and 9.10

**Description:** This task was "Solidity: fuzzing and Go twins": a fuzz target per parser run in CI, and a Go program beside each package whose output would pin Snello's line for line. Dropped, decided 2026-11-07: Snello is a language setting its own course, and Go is a map for package names, never a specification, so a package's expected output is the language's own, written and reviewed as the book is, and conformance with Go holds only where a design note decided "as Go's" (floats in `fmt`, the Unicode tables, `ParseFloat`'s grammar), which `examples/run/formats.snello` pins already. The fuzzing keeps its point, that the four parsers reading a program's input are C or lower to C and a bug there is memory unsafety, and loses its ceremony: one bounded campaign each, as a criterion of the task that touches the parser, 9.9 for `ParseFloat`, `Atoi` and the quote reader and 9.10 for `bufio`, with every finding fixed and its input kept as an ordinary test, and no CI job. The format grammar is Go code in the checker that only ever reads source text and is not fuzzed. A fuzzing mode of `snello test` of its own, with the corpus beside the test file, is a language question for later, outside this stage. The number stays so that the tasks after it keep theirs.

## Task 9.6: Unicode and runes

**Description:** A string is bytes, and the case, white-space and printable tables are Unicode's already; what is missing is the rune. `for _, r := range s` yields runes with their byte offsets, as Go's does, a language rule recorded in `../design/00-syntax-conventions.md`; `s.Runes()` makes an owned `[]rune`; `strings.Builder.WriteRune`, `bufio.Reader.ReadRune`; a `unicode` package with `IsLetter`, `IsDigit`, `IsSpace`, `IsUpper`, `IsLower`, `IsPrint`, `ToUpper`, `ToLower` on runes over the runtime's tables; `utf8.RuneLen`, `utf8.AppendRune`, `utf8.DecodeRune` as functions of `unicode/utf8`.

**Acceptance criteria:**
- [x] The design recorded and reviewed before the code (2026-11-08: decided 12 of the library note and the range bullet of the syntax conventions)
- [x] A range over a string yields runes and offsets through both backends; an invalid sequence yields the replacement rune and advances one byte (2026-11-08: `range-runes` in the backend tests; an owned string is lent for the loop, not consumed; no program ranged over a string before)
- [x] The `unicode` and `unicode/utf8` packages, their tables checked rune by rune against the Unicode Character Database files they are written from (2026-11-08: `UnicodeData.txt` and `PropList.txt` 18.0.0 checked in under `runtime/testdata/ucd/`, the tables regenerated byte for byte from them; `TestUnicodeLookup` compares all 1,114,112 code points with the files instead of with Go's `unicode` package; `unicode/utf8` is the first package two levels down, so the embed, the importer's test packages and `snello test std/...` take a nested path; two gaps found and recorded below)
- [x] Chapter 19, Text, written for developers and maintainers, and chapters 3, 9 and 14 pointing at it (2026-11-08, cut from `examples/run/runes.snello`; Appendix B and the index too)
- [x] Unicode whole, decided 13 of the library note: every class, `Category`, `Script` and `InScript`, `ToTitle` and `CaseFold`; the rest of `unicode/utf8`; `unicode/utf16`; full case mapping on strings with the final sigma, and `EqualIgnoreCase` by full folding; `IndexRune`, `ContainsRune` and `Map`; `unicode/norm` and `unicode/segment`; each table tested on every code point and each algorithm on every line of Unicode's test file for it (2026-11-08: at Unicode 18.0.0, the latest, its files checked in under `runtime/testdata/ucd/18.0.0/` in place of 17.0.0's; the generator writes every table and stops on a property value it does not know; `TestUnicodeLookup`, `TestCaseMapping`, `TestNormalization` and `TestSegmentation` run every code point and every line of Unicode's test files; Unicode 18 restated rule GB9c, a linker joining the consonant after it with no consonant before, which the tests found; `EqualFold` is named `EqualIgnoreCase` and the rune's folding `CaseFold`, decided to say what each answers; `unicode/utf16`, `unicode/norm` and `unicode/segment` are new packages two levels down; the run example and chapter 19 cover every name); after the review: sentence boundaries and canonical ordering made linear, measured quadratic before, with `TestLinearTime`; `strings.ToTitle` by words; `string(r)` and `string(rs)`; the derived properties, digit and numeric values, script codes, `Version`, `RuneSelf`, UTF-16 bytes, `ToValidUTF8`, the `Func` searches, `segment`'s `Count`, `Next` and `IsWord`, `UnreadRune`, and the quick check with `TestQuickCheck`, which found a wrong shortcut in it; the encoder writes U+FFFD for a value that is not a code point; `make test` finds the nested packages)
- Gaps found, not fixed here: neither backend compiles a `switch` statement (dropped from the language, decided 2026-11-08 in `language-gaps.md`, task 11.1), so `RuneLen` and `ReadRune` are written with `if` chains; and `expect.Equal` on a tuple does not compile, the backends having no `Eq` for a tuple through a bound, so the tests compare the parts, for task 9.7

**Verification:**
- [x] `go test ./compiler/... ./std ./examples` (2026-11-08)
- [x] Tests pass: `go test ./...` (2026-11-08, natively, with `make test` and `make check`, after the review's fixes; CI green on every job of pull request 15)

**Dependencies:** 9.1
**Files:** `compiler/types/`, `compiler/emit/`, `std/unicode/`, `std/builtin/`, `std/strings/`, `std/bufio/`, `runtime/c/unicode.c`
**Scope:** L

## Task 9.7: Strings, slices, maps, options and results

**Description:** The methods every program reaches for. On `string`: `Index`, `LastIndex`, `Replace`, `ReplaceAll`, `TrimPrefix`, `TrimSuffix`, `TrimSpace`, `Count`, `SplitN`; `strings.NewReader` as an `io.Reader`; `Builder.Grow`. On `[]T`: `Extend(ref []T)`, `Index(ref T) int?`, `First() ref T?`, `Last() ref T?`, `Truncate(n)`. On `map[K]V`: `GetOrInsert(k, v) mut V`, `Len` as `len` stays. On `T?`: `AndThen`, `OrElse`. On `Result<T, E>`: `Ok() T?`, `Err() E?`, `AndThen`, `UnwrapOrElse`. On every integer type: `Abs`, `Clamp`, `CheckedAdd`, `CheckedSub`, `CheckedMul` returning `T?`, which chapter 3 promises; `[]byte` gains `Equal` and `Index`. Each is written on its reference algorithm; `Index` and `Replace` on the search `Contains` has.

**Acceptance criteria:**
- [ ] Every name above in `std/builtin` or its package, documented there, in the library table and in chapter 9 or 14
- [ ] An example under `examples/run/` for the string and slice methods, its expected output the language's own, reviewed
- [ ] `Index` on a 20 MB text with a 50,001-byte needle takes under two seconds, as `Contains` does

**Verification:**
- [ ] `go test ./compiler/types ./compiler/emit/... ./examples`

**Dependencies:** 9.1
**Files:** `std/builtin/`, `std/strings/`, `runtime/c/string.c`, `runtime/c/slice.c`, `compiler/emit/lower/`
**Scope:** M

## Task 9.8: `math`, `math/bits` and `rand`

**Description:** `math` fills in: `Floor`, `Ceil`, `Round`, `Trunc`, `Pow`, `Exp`, `Log`, `Log2`, `Log10`, `Sin`, `Cos`, `Tan`, `Asin`, `Acos`, `Atan`, `Atan2`, `Sinh`, `Cosh`, `Tanh`, `Hypot`, `Mod`, `Cbrt`, `Inf`, `NaN`, `IsNaN`, `IsInf`, `Signbit`, `Copysign`, `E`, `Ln2`, `MaxInt`, `MinInt`, `MaxFloat64`, `SmallestNonzeroFloat64` and the sized limits; each a bound function to libm or a constant. `math/bits`: `LeadingZeros`, `TrailingZeros`, `OnesCount`, `RotateLeft`, `Reverse`, `Len`, which the backends write inline. `math/rand`: a seedable generator, Go 1.22's PCG, with `Int`, `IntN`, `Int64`, `Float64`, `Shuffle`, `Perm`, and the package functions over a generator seeded from the runtime's random source.

**Acceptance criteria:**
- [ ] Every name above in an example under `examples/run/` on fixed inputs, `rand` on a fixed seed, with the generator and the sequence it must give decided in the task's design note
- [ ] `bits` written inline by both backends: the C backend to the compiler's builtins, the LLVM backend to the intrinsics

**Verification:**
- [ ] `go test ./std ./compiler/emit/... ./examples`

**Dependencies:** 9.1
**Files:** `std/math/`, `std/math/bits/`, `std/math/rand/`, `runtime/c/math.c`, `compiler/emit/`
**Scope:** M

## Task 9.9: `fmt` and `strconv`

**Description:** `fmt` gains `Fprint`, the verbs `%x`, `%X`, `%o`, `%b`, `%c`, `%e`, `%g`, a width and the `-` flag, and `Errorf` is not added, task 9.4's `?` and `Source` taking its place. `strconv` gains `ParseInt(s, base)` and `ParseUint`, `FormatInt(n, base)` and `FormatUint`, `ParseBool` and `FormatBool`, `Quote` and `Unquote` over the runtime's quoting, `FormatFloat(f, prec)`, and `f32` through `ParseFloat32` and `FormatFloat32`. The format grammar in `compiler/types/format.go` checks the new verbs as it checks the old.

**Acceptance criteria:**
- [ ] Every verb and function above in `examples/run/formats.snello`, extended; floats print as decided 6 of the library note has them
- [ ] `ParseFloat`, `Atoi` and the quote reader fuzzed for ten minutes each by a Go test driving the runtime, written once here and used again by 9.10; every finding fixed and its input kept as an ordinary test; no CI job (2026-11-07, from the dropped task 9.5)
- [ ] A verb against the wrong argument refused when the program is compiled, as today

**Verification:**
- [ ] `go test ./compiler/types ./compiler/emit/... ./examples`

**Dependencies:** 9.1
**Files:** `std/fmt/`, `std/strconv/`, `compiler/types/format.go`, `compiler/emit/`, `runtime/c/format.c`, `runtime/c/parse.c`
**Scope:** M

## Task 9.10: `io`, `bufio` and `bytes`

**Description:** `io` gains `ReadAll`, `ReadFull`, `Discard`, a `Seeker` trait with `Seek` on `os.File`; `bufio.Reader` gains `Peek`, `ReadBytes`, `UnreadByte`; `bufio.Scanner` gains `Words()` as a second constructor; a `bytes` package with `Buffer` as a growable `io.Writer` and `io.Reader`, the structure every encoder writes into, over the slice the runtime has.

**Acceptance criteria:**
- [ ] Every name above, with `bytes.Buffer` and `Seek`, in an example under `examples/run/`; `io.ReadAll` on a 100 MB file within 1.25 times Go's time
- [ ] `bufio`'s line reader fuzzed for ten minutes with the harness of 9.9: it never hands out a line past its limit and never reads a byte twice; every finding fixed and its input kept as a test (2026-11-07, from the dropped task 9.5)
- [ ] Chapter 14 and the library table

**Verification:**
- [ ] `go test ./std ./compiler/emit/... ./examples`

**Dependencies:** 9.9, for the fuzzing harness
**Files:** `std/io/`, `std/bufio/`, `std/bytes/`, `std/os/`, `runtime/c/os.c`, `runtime/c/bufio.c`
**Scope:** M

## Task 9.11: `os` and `path`

**Description:** `os` gains the environment, `Getenv`, `LookupEnv`, `Setenv`, `Environ`; directories, `ReadDir` returning entries with name and kind, `Mkdir`, `MkdirAll`, `Stat` and `Lstat` returning a `FileInfo` with size, mode, modification time and whether a directory, `Rename`, `Getwd`, `Chdir`; `ReadFile` as bytes, `WriteFile`, `OpenFile(path, flags, mode)` with append, create, truncate and exclusive; `TempDir`, `Getpid`, `Hostname`. A `path` package of pure string functions, `Join`, `Base`, `Dir`, `Ext`, `Clean`, `Split`, `IsAbs`, with `filepath` as its name for the system's separator as Go has both. `os/exec` and signals stay out, each a design note of its own after this stage.

**Acceptance criteria:**
- [ ] Every name above through both backends, `examples/run/files.snello` extended and `path` in an example of its own
- [ ] Every descriptor opened closed on exec, as the existing ones are; every error an `io.Error` worded as Go's

**Verification:**
- [ ] `go test ./std ./compiler/emit/... ./examples`

**Dependencies:** 9.1
**Files:** `std/os/`, `std/path/`, `std/path/filepath/`, `runtime/c/os.c`
**Scope:** M

## Task 9.12: `time`

**Description:** A calendar in UTC: `Date(year, month, day, hour, min, sec, nsec)`, `Year`, `Month`, `Day`, `Hour`, `Minute`, `Second`, `Nanosecond`, `Weekday`, `YearDay`, `AddDate`; `Format` and `Parse` with Go's reference layout, `2006-01-02 15:04:05`, and the layouts `RFC3339`, `DateTime`, `DateOnly`, `TimeOnly`, `Kitchen`; `ParseDuration`; `Timer` and `Ticker` as values with `Stop` and `Reset`, their `Drop` stopping them, over the runtime's timer heap; `Truncate` and `Round` on `Time`. Zones stay out: UTC only, as decided, with `Local` a later decision.

**Acceptance criteria:**
- [ ] Every name above, `Format` and `Parse` over a table of times and layouts decided in the task's design note, the calendar checked over a range of dates including leap years against the civil calendar's own rules
- [ ] A `Timer` stopped before it fires sends nothing; a `Ticker` dropped ticks no more; `examples/run/clocks.snello` extended

**Verification:**
- [ ] `go test ./std ./compiler/emit/... ./examples`

**Dependencies:** 9.1
**Files:** `std/time/`, `runtime/c/time.c`, `runtime/c/sched.c`
**Scope:** L

## Task 9.13: `sync` and the collections

**Description:** Atomics: `sync.AtomicInt` and `sync.AtomicBool` with `Load`, `Store`, `Add`, `Swap`, `CompareAndSwap`, over the runtime's atomics, `Send` by structure; `WaitGroup.Go(f)` as Go 1.25's. `Deque<T>` with `PushFront`, `PopFront`, `PushBack`, `PopBack`, `Front`, `Back`, `Len`, a ring over a slice; `Set<T>` over the map with `Insert`, `Contains`, `Remove`, `Len`, `range`; `heap.Reverse<T>` as Rust's `cmp::Reverse`; `range` over an `arena.Arena<T>` yielding `(Index<T>, ref T)`; `rc.TryUnwrap`.

**Acceptance criteria:**
- [ ] Every name above, `Deque` and `Set` measured against Go's `container/list` replacement idioms and Rust's `VecDeque` and `HashSet` on a million operations, with the numbers in the library table
- [ ] Example 04's BFS uses `Deque`; chapter 10 covers atomics with the ownership note's sentence updated

**Verification:**
- [ ] `go test ./std ./compiler/emit/... ./examples ./bench`

**Dependencies:** 9.1
**Files:** `std/sync/`, `std/deque/`, `std/set/`, `std/heap/`, `std/arena/`, `std/rc/`, `runtime/c/sync.c`
**Scope:** M

## Task 9.14: `net`: names and IPv6

**Description:** `Dial("example.com", 443)` is refused today. Names resolve through the system's resolver, `getaddrinfo`, on the poller's thread so that a task does not block a worker, with a `LookupHost` returning the addresses; IPv6 addresses listen, dial and print as Go's do; `Conn.LocalAddr`, `Conn.RemoteAddr`, `Listener.Addr` as strings; `DialTimeout` covers the lookup. UDP, Unix sockets and TLS stay out: UDP until a program needs it, TLS as a library of stage 8 over a system library through the binding of chapter 15.

**Acceptance criteria:**
- [ ] A dial by name and by IPv6 address through both backends, with the lookup's failure an `io.Error` worded as Go's, `dial tcp: lookup nosuch.invalid: no such host`
- [ ] A lookup that hangs does not hold a worker: a program of two tasks, one in a lookup against an unreachable resolver and one computing, finishes the computation
- [ ] `examples/run/net.snello` extended; chapter 14

**Verification:**
- [ ] `go test ./std ./compiler/emit/... ./examples`

**Dependencies:** 9.1
**Files:** `std/net/`, `runtime/c/net.c`
**Scope:** L

## Checkpoint: stage 9

- [ ] `snello test` runs the standard library's own tests, written in Snello, and the suite runs them in CI
- [ ] The four parsers that read a program's input fuzzed once, their findings kept as tests; every package's expected output the language's own, reviewed
- [ ] Every name the gaps document decided `std` exists, documented in the library table and the book, with the measurements the tasks name
- [ ] The library still holds the note's packages and the stage's, and nothing more; the `later` rows of the gaps document each have a design note named, written or not
- [ ] Reviewed by the owner
