# 16. Testing

Status: implemented (stage 9, task 9.1, 2026-11-07): test files, the `testing` and `expect` packages, `snello test` with its flags, subtests, parallel tests, benchmarks and golden files run through both backends, with the failure forms pinned by `compiler/testrun`'s tests; the place of a call kept per task (task 9.1, the same day). Every package of the standard library gets its test file in task 9.3. The design is `../design/09-testing.md`; Appendix B maps the testing tools of other languages onto this chapter.

This chapter is for two readers: a developer writing the tests of a program or a package, and a maintainer of the compiler, the runtime or the library, who needs to know how the pieces fit and where each lives. The first fourteen sections are for the developer; the last two are for the maintainer.

## Test files

A package's tests live beside it in files named `NAME_test.snello`. A test file is part of its package only when the package is tested: `snello build`, `snello check` and `snello emit` never read one, and an import of the package does not either, so a test can be as large and as slow as it likes without costing the program.

A test file carries the package's own clause, `package shapes`, and sees everything in it; or the clause `package shapes_test`, which makes the directory's test files a package of their own that imports `shapes` as its users do and sees its exported names alone, the form the standard library's own tests take, since `expect` and `testing` import `sync`, `fmt`, `os` and `time` and a test inside one of those would import itself. A directory's test files are all of one form, and a `main` package's tests are inside it, since `main` cannot be imported. The suffix `_test` on an import path names such a package and nothing else, and only a test file may import one, as only a test file may import `expect` and `testing`.

Two packages exist for tests: `expect` asserts and `testing` runs. Both may be imported from a test file alone, and an import from any other file is refused, `package testing is imported by test files alone`. The two rules together are the guarantee of this chapter: nothing of a test reaches a program's binary, neither the test functions, nor the two packages, nor the runner the compiler generates. The compiler's suite checks it by building a program beside its tests and searching the binary for a symbol of either package.

## A test is a plain function

A test is a function in a test file whose name begins with `Test` followed by a capital letter, taking nothing and returning nothing. It passes when it returns. It fails by a failed expectation, which is a panic the runner catches at the test's boundary; the values with `Drop` methods the test made since are dropped on the way, a file closed, a directory removed, a lock given back. There is no handle to pass around and no value to return. From `examples/run/packaged/shapes/shapes_test.snello`:

```snello
package shapes

import ("expect"; "testing")

struct Case { name string; kind Kind; want string }

func TestDescribe() {
    cases := []Case{
        Case{name: "round", kind: Round, want: "round"},
        Case{name: "square", kind: Square, want: "square"},
    }
    for _, c := range cases {
        testing.Run(ref c.name, func() { expect.Equal(ref Describe(c.kind), ref c.want) })
    }
}

func TestArea() {
    expect.Equal(ref Area(Rect{W: 2, H: 3}), ref 6.0)
    expect.True(Area(Circle{Radius: 1}) > 3.14)
}
```

A function named `Test` and a capital with any other signature is refused when the package is built for testing, `TestArea has the wrong signature: want func()`, so that a test is never silently not run. A benchmark is the same shape under the name `Benchmark`, in the benchmarks section below.

## Running the tests

`snello test DIR` builds the package in `DIR` with its test files and a generated `main`, runs the result and prints one line when every test passed:

```
ok  	shapes	0.001s
```

When a test fails, its name and message are printed as they happen, then `FAIL` and the package's line:

```
--- FAIL: TestDescribe/square (0.00s)
    shapes_test.snello:14: expect.Equal(ref Describe(c.kind), ref c.want)
        got:  square
        want: squared   (string)
FAIL
FAIL	shapes	0.001s
```

The exit code is 0 when every test passed and 1 otherwise, which is what a CI step checks; a run that outlives `-timeout` exits with 2. `snello test -v DIR` prints every test as it starts and ends, `=== RUN   TestArea` and `--- PASS: TestArea (0.00s)`, with the lines the test logged, and `PASS` before the package's line. `-backend c` or `-backend llvm` chooses the backend, as for `snello build`; the test build is a release build, in a temporary directory, never beside the package. One directory is tested per command; a script loops over the packages of a program.

## The assertions

Every function of `expect` fails the test when its condition does not hold and does nothing otherwise. Each takes its operands by `ref`, so a test asserts on a value and keeps it.

| Assertion | Fails unless |
|---|---|
| `expect.Equal(ref got, ref want)` | the two are equal, for any type with `Eq`; prints got, want and the type |
| `expect.NotEqual(ref got, ref want)` | they differ; prints the value both are |
| `expect.EqualSlice(ref got, ref want)` | the slices are equal element by element; prints both with their lengths and the first element that differs |
| `expect.EqualText(ref got, ref want)` | the two strings are equal; prints the first line that differs, with the line counts when they differ |
| `expect.True(cond)`, `expect.False(cond)` | the condition is as said; prints the expression as written and, for a comparison, the values of its operands |
| `expect.Fail(msg)`, `expect.Failf(format, args...)` | never: fails with the message, `Failf` formatted as `fmt.Sprintf` formats |
| `expect.IsSome(o)`, `expect.IsNone(o)` | the option is as said; `IsSome` returns the value it held, `IsNone` prints the value it found |
| `expect.IsOk(r)`, `expect.IsErr(r)` | the result is as said; `IsOk` returns the value and `IsErr` the error, and each prints the other side when it is wrong |
| `expect.Contains(s, sub)`, `expect.HasPrefix(s, p)`, `expect.HasSuffix(s, s2)` | the string holds, starts or ends with the other |
| `expect.ContainsElem(ref xs, ref v)` | the slice holds the element |
| `expect.Len(ref xs, n)`, `expect.Empty(ref xs)` | the slice has `n` elements, or none; prints the count found |
| `expect.Panics(f)`, `expect.PanicsWith(f, msg)` | the function panics, with exactly that message for the second; the panic is caught and its values dropped, and the test goes on |
| `expect.Golden(name, ref got)` | the text equals `testdata/NAME.golden`; see the golden files section |

The names `IsSome`, `IsOk` and `IsErr` carry `Is` because `Some`, `Ok` and `Err` are the variants' own names, which a function of the package would hide from its own `match` patterns. `IsSome` and `IsOk` return what they found, so the style of a test that needs the value is one assertion per line:

```snello
v := expect.IsOk(Parse("1"))
expect.Equal(ref v, ref 1)
```

## The failure message

The message is built to be read once. Five rules give it its form, and the compiler is what makes the first three possible: before every call of an `expect` function it hands the runtime the file and line, the call's text as the test wrote it, and the parts of the call, so that no assertion needs a message written by hand.

1. **The expression as the test wrote it, then got, then want**, in that order and with those two words.
2. **Where they differ, in words**, before any dump: the element or the line that differs and by how much the lengths differ.
3. **The parts of a condition.** For `expect.True(n > limit)` the message is `expect.True(n > limit) is false` and under it `n is 2, limit is 3`. The parts are shown when the condition is a comparison whose operands are names, fields, elements, literals or `len` of those, which can be read twice without anyone noticing; a call in a condition is evaluated once, so its result is not shown.
4. **The row of a table-driven test** is the subtest's name, `TestDescribe/square`, which is why a table's rows are run through `testing.Run` with the row's name.
5. **One failure, whole.** The first failed assertion ends the test, so a report is one message and not a list; a panic the test did not intend is reported with the runtime's message, `index out of range [5] with length 3`, at the test's name.

The forms, as the compiler's suite pins them:

```
--- FAIL: TestDouble/wrong (0.00s)
    shapes_test.snello:19: expect.EqualSlice(ref Double(ref c.in), ref c.want)
        got:  [2]   ([]int, 1 element)
        want: [3]   (1 element)
        element 0: got 2 where 3 was wanted
```

```
--- FAIL: TestOk (0.00s)
    more_test.snello:12: expect.IsOk(Parse("x")): is Err, Ok was wanted
        error: parse x: not a number
```

```
--- FAIL: TestTextDiff (0.00s)
    more_test.snello:30: expect.EqualText(ref Report(), "line one\nline 2\nline three\n")
        line 2: got "line two" where "line 2" was wanted
```

Two limits follow from how the place is handed over. An assertion written inside another assertion's arguments, `expect.Equal(ref expect.IsOk(r), ref 1)`, reports the outer call's place, since the inner call's place is written over the outer's before the outer reads it; the style the package is written for is one assertion per line, which the value-returning assertions exist for. And an `expect` function, or `testing.Log`, `Logf` and `Skip`, is called and never taken as a value, `f := expect.True`, which the compiler refuses, `expect.True as a value: it is called, so that a failure names its line`, since a call through a value has no place to hand over.

## Subtests and tables

`testing.Run(name, f)` runs `f` as a subtest named `TestParent/name`, reported on its own: a failure in it ends the subtest and the parent goes on with its next case, so one run reports every failing row of a table rather than the first. A subtest's closure captures the row it was made in, as any closure does, and the row's name is what the report shows; the `TestDescribe` sample above is the shape.

## Logging and skipping

`testing.Log(line)` and `testing.Logf(format, args...)` record a line with the file and line it was logged from, printed under the test's result when the test fails or when `-v` is given, and otherwise not at all. `testing.Skip(reason)` ends the test as skipped, reported as `--- SKIP: TestName` with the reason under it when `-v` is given; `testing.Short()` reports whether `-short` was given, for a test to skip its long part:

```snello
func TestLarge() {
    if testing.Short() {
        testing.Skip("large input")
    }
    ...
}
```

## Temporary directories

`testing.TempDir()` makes a directory the test owns and returns it as a `testing.Dir`, with `Path()` for its path and `Join(name)` for a path inside it. The directory and everything in it are removed when the value goes out of scope, at the end of the test or when the test fails, through the value's `Drop`; nothing is registered and nothing is cleaned up by name.

## Parallel tests

A test that calls `testing.Parallel()` as its first line runs beside the other parallel tests of the package, after every sequential test has ended: the call waits until the sequential tests are done, and the runner goes on to the next test meanwhile. Each parallel test runs in a task of its own; its `=== RUN` line is printed when it starts, as every test's is, and its logs and its result are held until it ends and then printed together, so the output of two parallel tests never interleaves. A second call of `Parallel` in one test, or one in its subtest, fails the test with `testing.Parallel called twice`. The place of an assertion, the file, line and text the failure message prints, is the running task's own, so two parallel tests failing at once each report their own.

## Benchmarks

A benchmark is a function named `Benchmark` and a capital looping over `testing.Loop()`:

```snello
func BenchmarkRepeat() {
    testing.ReportAllocs()
    for testing.Loop() {
        _ = Repeat("ab", 16)
    }
}
```

Benchmarks run under `-bench NAME`, after the tests, for every benchmark whose name holds NAME; `-bench .` runs them all, and `-run Nothing -bench .` runs the benchmarks alone. The runner raises the iteration count until one run of the loop takes a second and prints the count and the time per operation:

```
BenchmarkRepeat             3214567          341 ns/op       32 B/op        1 allocs/op
```

The time per operation has decimals under a hundred nanoseconds. A benchmark that fails an expectation is reported as a test is, `--- FAIL: BenchmarkRepeat` with the message under it, and the run ends with `FAIL`. `testing.ReportAllocs()` adds bytes and allocations per operation, counted by the runtime's allocator for the loop alone; `testing.ResetTimer()` restarts the clock and the counts after a setup the loop should not measure.

## Golden files

`expect.Golden(name, ref got)` compares a text with the file `testdata/NAME.golden` beside the package and fails with the first differing line. `snello test -update DIR` makes every `Golden` write its file instead of comparing, which is how the file is made the first time and how it is refreshed when the output changes on purpose; the diff of the file in version control is then the review. The `testdata` directory is made by hand until the library makes directories (task 9.11), and a missing file says so: `open testdata/report.golden: no such file or directory; snello test -update writes the file`.

## The flags

| Flag | What it does |
|---|---|
| `-v` | print every test as it starts and ends, with its logged lines, and `PASS` at the end |
| `-run NAME` | run the tests whose name holds NAME, or every test for `.`; a subtest is not matched on its own, its test is |
| `-bench NAME` | run the benchmarks whose name holds NAME, after the tests; `.` for all |
| `-count N` | run the tests N times; a value that is not a number ends the run with exit code 2 before any test |
| `-failfast` | stop at the first failing test |
| `-timeout DURATION` | end the run with `panic: tests timed out` on the standard error and exit code 2 when it outlives the limit, `30s` or `2m`, handed to the program in whole seconds |
| `-shuffle` | run the tests in a random order, printing the seed so the order can be repeated |
| `-short` | what `testing.Short()` reports, for tests to skip their long part |
| `-update` | make every `expect.Golden` write its file |
| `-backend c` or `-backend llvm` | the backend the test build goes through |

NAME is held as a substring, not matched as a pattern, since the language has no regular expressions yet. The flags are read by `testing.Main` from the program's arguments; `snello test` passes them through unchanged.

## Testing a main package

A program tests its `main` package the same way: `main_test.snello` beside `main.snello`, and `snello test` on the directory. The generated runner joins the package with its `main` left out, since a program's `main` is not what its tests call, and every other function of the package is tested as a library's would be.

## Catching a panic

`testing.Catch(f) string?` runs `f` and returns `None` when it returns, or `Some(message)` when it panics, with the values `f` made since dropped on the way; what the caller holds is untouched. It is what the runner catches a failing test with and what `expect.Panics` is built on. A program outside a test file has no use for it and cannot import it: a panic is a bug and not an error, as chapter 8 says, and a program does not catch its bugs.

## What a test cannot do, and why

- **Memory of a failed test.** The values with `Drop` methods are dropped when a test fails; the plain memory of the frames the panic abandoned, a slice of numbers or a message a `Sprintf` built, is not freed until the process ends, since a guard per owned value would cost every program for the sake of a failed test. A test build is therefore a release build, and the debug build's report of leaks is for programs.
- **A panic inside C** a package binds is not caught; C does not unwind.
- **An assertion on a task the test spawned** is a plain panic, since the mark that turns a failure into a report is the test's own task's: it prints `panic:` with the message and ends the process. Send what a spawned task finds back over a channel and assert on the test's task.
- **No caching of results**, no coverage, no examples run as tests: each is a task when a program asks for it (`../design/09-testing.md`, section 6).
- **An assertion takes an option or a result by value**: `expect.IsSome(h.Peek())` on a `ref T?` is refused, since the value cannot move out of the borrow; bind it instead, `if v := h.Peek() { expect.Equal(v, ref 2) }`.
- **No race detector**, because a data race is a compile error in Snello: a value reaches another task by a move, inside an `Arc`, or through a channel, and a `mut` of a shared value comes only through a lock that owns it (chapter 10). A package variable is under the same rule, read by every task and changed only through what it holds.

## For maintainers: how it is built

The pieces, and where each lives:

- **The runner**, `compiler/testrun`: `PlanDir` lists a package's sources and test files and finds the tests by name on the parsed test files, refusing a wrong signature there; `Source` writes the generated `main`, which lists the tests and calls `testing.Main`; `Build` compiles it through the chosen backend into a temporary directory and `Run` runs the binary with the flags, passing its output through and returning its exit code. `cmd/snello`'s `test` subcommand is a thin wrapper over the three. The generated file is named `snello_test.snello` in the package's directory so that its errors name a file and its imports are answered from the package's module, and it is never written there: the loader takes it as a file in memory. A library package is imported by the runner under its import path, with the importer told which package is under test so that it loads the test files of that package alone; a main package cannot be imported, so the runner joins it, through `importer.Load` with the package's own `main` dropped and the runner's kept.
- **The file rules**, `compiler/importer`: `SourceFiles` leaves test files out, `TestFiles` lists them, and `checkTestImports` refuses `testing` and `expect` outside a test file, the standard library exempt since `expect` imports `testing`.
- **The mark**, `runtime/c/sched.c`: `sn_catch` runs a function under a mark on the task, which holds the guard count and a jump point; a panic on a task with a mark drops the guards down to the mark and jumps to it with the message, instead of printing and exiting. `testing.Catch` is lowered to it by both backends, through an adapter that calls a function value as C calls one.
- **The place of a call**, `compiler/emit/lower.Located` and each backend's `locate`: before every call of an exported function of `expect`, and of `testing.Log`, `Logf` and `Skip`, the backend hands the runtime the file and line, the call's text from `ast.Source`, and for `expect` the parts: the type compared for `Equal`, or the operands of a comparison with their values when `lower.Simple` says they can be read twice. The runtime keeps them on the running task, `sn_testing_line` and `sn_testing_call` in `runtime/c/testing.c`, so that parallel tests never share them, and the packages read them back through the bodiless `where()` and `parts()`. `Logf` and `Failf` are lowered as `fmt.Sprintf` is, into a call of `Log` or `Fail` with the formatted line.
- **The runtime's half of `testing`**, `runtime/c/testing.c`: the running task's identity, which the runner keys its state per test by, in a `sync.Mutex` the package's registry variable owns; and the benchmark's state, one benchmark at a time, with the allocator's two counts, `sn_alloc_count` and `sn_alloc_total`, read before and after the loop, so that `testing.Loop` costs one call and the package has no variable a task writes.
- **The packages**, `std/testing/testing.snello` and `std/expect/expect.snello`: ordinary Snello over `fmt`, `os` and `time`, with nothing privileged but the bodiless functions above. The printing of a failure, the type names, the sizes, the first difference and the dump, is `expect`'s own.

The forms of the output are pinned by `compiler/testrun`'s tests, `TestRun` over the shapes fixture and `TestMore` over the assertions, the flags, parallel tests and benchmarks, through both backends; a change to a message changes a pinned string there. The runtime's mark has its own programs in `compiler/emit/internal/backendtest`, through both backends and both builds.

## For maintainers: the library's own tests

The standard library tests itself in Snello: a `_test.snello` beside every package's file, `std/strings/strings_test.snello` beside `strings.snello`, each an external test package, `package strings_test`, with every exported name called and at least one benchmark. Two commands run them:

- `snello test std/NAME`, in a checkout of the compiler, runs one package's tests from the directory, through either backend, with the library's privileges; the whole library is read from the checkout then, so the sources as edited are what run, not the copy embedded in the compiler running them.
- `make test` runs every package that way through both backends and the example packages with tests, `make test-std` and `make test-examples` each half, inside the Linux image `build/Dockerfile` describes, so that a green run on any machine means the same as CI's; `NATIVE=1 make test` runs them on the host. `make build`, `make check` and `make shell` are the other targets: the Makefile runs Snello, and the Go suite stays `go test ./...`, which CI runs inside the same image through `build/run.sh`.

`go test ./std` is CI's entry to the same tests, a Go file that finds every test file in the embedded library and runs it through the runner above. To add a package's tests, add the file. The `testing` and `expect` packages themselves are tested by the runner's Go tests, since they cannot assert on their own assertions; `os.Exit` is the one exported name no test calls, since it ends the process.
