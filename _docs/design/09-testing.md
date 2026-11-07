# Testing: `_test.snello` files, the `testing` package and `snello test`

Status: proposed 2026-11-07 for task 9.1 of stage 9, for the owner's review before the first line of code. This note says how a program's or a library's own tests are written in Snello, how they are found, built and run, and what the `testing` package is, in Go's terms wherever Go has the term.

## The problem

Every test of Snello so far is Go: the compiler's packages test themselves, the examples suite builds and runs the example programs, and the run suite compares programs with their expected output. A program written in Snello has no way to test itself, and neither has the standard library: a package's tests would be Go programs driving Snello binaries, which is the wrong signal for a library meant to stand on its own, and no signal at all for a user. Go's answer is three things that fit together, and Snello takes the same three: a test file beside the package that is part of it only when tested, a `testing` package with a `T` the test reports through, and a command that builds the package with its tests and a generated `main` and runs the result.

## 1. A test file is part of its package only when tested

A file named `NAME_test.snello` in a package's directory is a test file. It has the package's own package clause and sees everything the package declares, exported or not, as Go's internal tests do; a test file with a different package name is an error, `x_test.snello: package strings_test, want strings`, since Go's external test packages are not in this design. A test file is left out of everything but a test build: `snello build`, `snello check` and `snello emit` do not read it, and an import of the package does not either, so a test can be as large and as slow as it likes without costing the program. The rule is one line in the function that lists a package's sources, `importer.SourceFiles`, and the embedded standard library follows it by construction, since a standard package is loaded by the one file named as its directory.

The standard library's own packages have test files too: `std/strings/strings_test.snello` beside `strings.snello`, checked under the library's privileges as the package is. They are what the stage 9 checkpoint means by the library testing itself.

**Nothing of a test reaches a program's binary** (decided 2026-11-07). The file rule is half of it; the other half is the packages: `testing` and `expect` may be imported from a `_test.snello` file alone, and an import of either from any other file is refused, `package testing is imported by test files alone`, so that no program ships its test harness, where a Go binary can carry `testing` and whatever imports it. The generated `main` of a test build lives in memory and is never written beside the package, and a test build goes to a temporary directory, never beside the program's binary.

## 2. A test is a plain function

A test is a function in a test file whose name begins with `Test` followed by a capital letter, taking nothing and returning nothing:

```snello
package strings

import ("expect"; "testing")

func TestRepeat() {
    expect.Equal(Repeat("ab", 3), "ababab")
}

func TestFields() {
    cases := []struct{ name string; in string; want []string }{
        {"spaces", "a b", []string{"a", "b"}},
        {"runs", "a  b", []string{"a", "b"}},
    }
    for _, c := range cases {
        testing.Run(c.name, func() { expect.Equal(Fields(c.in), c.want) })
    }
}
```

No handle is passed, decided (decided 2026-11-07): Go's `t *testing.T` exists because Go has no `Result`, no destructors and no way for a helper to know which test called it, and Snello has all three. A failed assertion is a panic, which the runner catches at the test's boundary; cleanup is `Drop`; the runner knows which test runs on which task. A function named `Test...` with another signature is refused when the package is built for testing, `TestRepeat has the wrong signature: want func()`, so that a test is never silently not run. A test that reaches its end has passed.

## 3. The runtime catches a panic at a mark

A panic today prints its message, runs the task's guards, which are the drops of every value alive on the task, and ends the process. A test needs the first two and not the third. The runtime gains a mark: `sn_catch(fn, ctx, msg)` runs `fn` on the current task with a mark holding the guard count and a jump point, and reports whether a panic ended it, with the message in `msg`; a panic on a task with a mark unwinds the guards down to the mark, so every value with a `Drop` method the test made is dropped, a file closed and a lock given back, and jumps to the mark with the message instead of printing and exiting. The memory of the plain values in the frames the panic abandons, a slice of numbers or the message a `Sprintf` built, is not freed until the process ends, since a guard per owned value would cost every program for the sake of a failed test; so a test build is a release build, and the debug build's leak report runs over the mark's programs except the one that pins this (`catch-abandoned`). A follow-up worth its own line: the debug build pays for checks already, so the emitter could guard every owned local in a debug build alone, which would make a debug test build leak-free under a mark at no cost to release programs. A `Drop` that panics while a caught panic is unwinding ends the process, as a panic in a drop always has, printing both messages, since the first can no longer reach the mark. Marks nest, a stack per task, for a subtest inside a test. A panic on a task with no mark ends the process as it does today, so a program is not changed by this. The library reaches it through one bodiless function of the `testing` package, `Catch(f func()) string?`, exported because `expect` is another package and builds `Panics` on it, and kept out of programs by the rule of section 1 that `testing` is imported by test files alone; until that rule lands, the slice after this one, any file may import `testing` and call `Catch`, a gap the tree carries for one slice. `expect.Panics` and `expect.PanicsWith` ship with the mark as its first test, before the rest of the two packages.

This is the one runtime piece of the design, and it goes first, as a slice of its own, since it is also what `expect.Panics` and fuzzing in Snello wait on. A panic in a C function a package binds is not caught: C does not unwind, and the mark is the task's.

## 4. The `testing` and `expect` packages

Two packages of ordinary Snello, with nothing the standard library is privileged to but `testing.catch` above and the file, line and expression text the compiler hands their functions: `testing` runs tests and `expect` asserts. They are rich enough that no library outside them is needed to test a program well (decided 2026-11-07): what a Go programmer reaches for in `testify`, `go-cmp` and `goldie` is in them, and benchmarks are in them as they are in Go's `testing`. They land in two slices after the runtime's, the first the smallest thing that runs a test and the second the richness.

**The first slice.**

| Name | What it does |
|---|---|
| `expect.Equal(got, want)`, `expect.NotEqual` | fail with both printed, `got X, want Y`, for any `T: Eq`, with the file and line of the call; `expect.EqualSlice` for slices, whose elements compare but which `==` does not, saying where they first differ and by how much the lengths differ |
| `expect.True(cond)`, `expect.False(cond)` | fail naming the expression's text, which the compiler hands the call |
| `expect.Fail(msg)`, `expect.Failf(format, args...)` | fail with a message of the test's own |
| `testing.Run(name, func())` | run a subtest named `TestParent/name`, reported on its own, as Go's subtests are: how a table-driven test names its rows |
| `testing.Log(line)`, `testing.Logf(format, args...)` | record a line with its file and line, printed with the test's result when it fails or with `-v` |
| `testing.Skip(reason)` | end the test as skipped |
| `testing.TempDir() testing.Dir` | a directory of the test's own, removed when the value goes out of scope, with `Join(name)` and `Path()` |
| `testing.Main(pkg, tests []testing.Test)` | what the generated `main` calls: run the tests, print, exit 0 when every one passed and 1 otherwise (as built, with the package's name for the last line) |

**The second slice.**

| Name | What it does |
|---|---|
| `expect.Equal` on a string, a slice or a map that differs | printed as a diff, line by line for a string with newlines and element by element otherwise, which is what `go-cmp` gives Go |
| `expect.IsSome(o)`, `expect.IsNone(o)`, `expect.IsOk(r)`, `expect.IsErr(r)` | fail unless the option or result is as said, printing what it held; `IsOk` and `IsSome` return the value, so a test writes `v := expect.IsOk(Parse("1"))`; named with `Is` because `Some`, `Ok` and `Err` are the variants' names, which a function of the package would hide from its own patterns |
| `expect.Contains(s, sub)`, `expect.ContainsElem(xs, v)`, `expect.HasPrefix`, `expect.HasSuffix`, `expect.Len(xs, n)`, `expect.Empty(xs)`, `expect.EqualText(got, want)` with the first differing line; `expect.Matches` once `regexp` exists | over strings and slices |
| `expect.Panics(func())`, `expect.PanicsWith(func(), msg)` | fail unless the function panics, through the mark of section 3; shipped with the mark, as its first test |
| `expect.Golden(name, got ref string)` | compare with `testdata/NAME.golden` beside the package and fail with the first differing line; `snello test -update` writes the file instead, as `goldie` does, into a `testdata` directory the author makes until the library makes directories (task 9.11) |
| `testing.Parallel()` | the test runs beside the other parallel tests of its package after the sequential ones, each a task, the runner holding each one's lines until it ends so they never interleave |
| `func BenchmarkName()` with `for testing.Loop() { ... }` | run under `-bench NAME` for a count the runner raises until a run takes a second, reporting ns/op and bytes and allocations per op, counted by the runtime's allocator for the loop alone; `testing.ResetTimer`, `testing.ReportAllocs`; Go 1.24's `b.Loop` without the `b` |
| `testing.Short() bool`, `-short` | for a test to skip its long part |
| `-run NAME`, `-bench NAME`, `-v`, `-count N`, `-failfast`, `-timeout DURATION`, `-shuffle`, `-short`, `-update` | the flags, read by `Main` from the program's arguments, which `snello test` checks and passes through, `-count` as a number and `-timeout` as a duration handed to the program in whole seconds; NAME is held as a substring, since the compiler has no regular expressions, and `.` runs every test or benchmark; a value that is not a number ends the run with exit code 2 before any test (as built) |

What is left to libraries outside, and why: mocks generated from traits need reflection or code generation, and a fake in Snello is a struct that lists the trait, written by hand in the lines a generator would write; a behaviour-driven style, `ginkgo` and `gomega`, is a dialect and not a tool; property-based testing, `rapid`, is worth a library of its own once `rand` exists, and fuzzing in Snello waits on `rand` too, Go's engine serving task 9.3 until then.

**The failure message** is the point of the whole design (decided 2026-11-07: better than Go's and Rust's, and quick to read). Go prints whatever the test wrote; `testify` prints a block headed "Error Trace" that takes a moment to parse; Rust's `assert_eq!` prints `left` and `right` and leaves the reader to remember which was expected. The best in use is pytest's, which rewrites an assertion so that a failure shows the values of its parts. Snello's compiler hands the `expect` functions their call's text already, for the file and line; it hands them the parts as well. Five rules, and the form they give:

1. **The expression as the test wrote it, then got, then want**, in that order and with those two words, never `left` and `right`, never `expected` and `actual` in an order the reader must learn.
2. **Where they differ, in words.** The first difference and the size of the difference, before any dump: `element 1: got "" where "b" was wanted; got has 1 element more`, `byte 14: got 'x' where 'y' was wanted`, `key "port" is in got and not in want`. A dump follows only for values that fit in a few lines; a long string shows the differing line with the lines around it.
3. **The parts of a condition.** For `expect.True(n > limit)` the compiler hands the values of `n` and `limit`, so the message is `n > limit is false: n is 2, limit is 3`, as pytest's rewriting does and neither Go nor Rust can. The operands shown are names, fields, elements, literals and the predeclared `len` of those, which can be read twice without anyone noticing; a call's result is not shown, since showing it would run the call again (as built: `lower.Simple`).
4. **The row of a table-driven test.** A failure in a subtest names the subtest, `TestFields/runs`, which is the row's name when the table is run through `testing.Run` with it; printing the captured range variable as well, `case "runs"`, was the first draft and was dropped as built, since a closure's captured row is the test's to name and the subtest's name already does.
5. **One failure, whole.** The first failed assertion ends the test, so a report is one message, not a list; and a panic the test did not intend is reported with the runtime's message, `index out of range [5] with length 3`, under the test's name; a stack of calls for it is not kept (as built), since the runtime keeps none, and is a task of its own when a program asks.

```
--- FAIL: TestFields/runs (0.00s)
    strings_test.snello:31: expect.Equal(Fields(c.in), c.want)
        got:  ["a" "" "b"]   ([]ref string, 3 elements)
        want: ["a" "b"]      ([]string, 2 elements)
        element 1: got "" where "b" was wanted; got has 1 more
```

```
--- FAIL: TestLimit (0.00s)
    limit_test.snello:12: expect.True(n > limit) is false
        n is 2, limit is 3
```

What the compiler writes for it: before every call of an exported function of `expect`, and of `testing.Log`, `Logf` and `Skip`, the backend writes the call's file and line, and for `expect` the call's text as the test wrote it, and hands them to the runtime with the parts: the type compared, for `Equal`, or the operands of a condition with their values, when the condition is a comparison whose operands are names, fields, elements, literals or `len` of those, which can be read twice without anyone noticing. The runtime keeps the place on the running task, `sn_testing_line` and `sn_testing_call` in `runtime/c/testing.c`, and the packages read it back through the bodiless `where()` and `parts()`; it was a package variable of each at first, which two parallel tests shared, a race of the library's own found on 2026-11-07 while checking the book's claim below and fixed by the slot per task. The library does the printing: the type names, the sizes, the first difference and the dump are `expect`'s own Snello, over `fmt`. One limit follows from the one slot: an assertion written inside another assertion's arguments, `expect.Equal(ref expect.IsOk(r), ref 1)`, reports the outer call's place, since the inner call's place is written over the outer's before the outer reads it; the style the package is written for, and that its value-returning assertions exist for, is one assertion per line, `v := expect.IsOk(r)`.

**How the runner attributes a line to a test.** `testing.Log`, `Skip` and the assertions are free functions, so they must find the test that called them: the runner records which task runs which test, through one more bodiless function, `testing.task() int`, the running task's identity, in a map held in a `sync.Mutex` the package's registry variable owns, which is what Go's `t` carries by hand (as built, `runtime/c/testing.c`; the lock was the runtime's until task 9.2 put every package variable under `sync`, and the benchmark's state moved into the runtime then, so that `testing.Loop` costs one call). A subtest is a mark inside the test's mark on the same task, so a failure in it unwinds to the subtest's mark and the parent goes on with its next case, as Go's parent does. The benchmarks' allocations and bytes per operation are the allocator's two counts, `sn_alloc_count` and `sn_alloc_total`, read before and after the loop.

**How a test ends.** A test that returns has passed. A failed assertion panics with its message, the runner's mark catches it, the guards drop what the test made, and the test is reported failed with the message and its line; `Skip` panics with a skip message the runner tells apart. Go's "record and continue", `t.Errorf` without stopping, is not kept: Rust never had it, and a table-driven test with subtests makes each case independent anyway. A panic the test did not intend, an index out of range, is reported the same way with the runtime's message, the test named by its `=== RUN` line, and the run goes on to the next test, which is better than Go, whose runtime panic ends the whole run.

**What is printed** is Go's `-v` form, one line as a test starts and one as it ends, with its time, then the verdict:

```
=== RUN   TestRepeat
--- PASS: TestRepeat (0.00s)
=== RUN   TestFields
=== RUN   TestFields/runs
--- FAIL: TestFields/runs (0.00s)
    strings_test.snello:31: expect.EqualSlice(ref Fields(c.in), ref c.want)
        got:  ["a" "" "b"]   ([]string, 3 elements)
        want: ["a" "b"]   (2 elements)
        element 1: got "" where "b" was wanted; got has 1 more
FAIL
```

Without `-v` only the failing tests and the verdict are printed, `ok` or `FAIL` with the package's path and the time, as `go test` prints. A logged line and an assertion carry the file and line of the call, and an assertion the text of its expression, which `fmt` cannot know; the compiler supplies them for the `testing` and `expect` functions alone, as it supplies `os.Exit` and the `fmt` functions, through the same list of functions it writes.

## 5. `snello test DIR`

`snello test DIR` tests the package in DIR, the directory given as `snello build DIR` takes one, with the flags above passed through to the program. It builds one program and runs it, and exits as the program exits.

The program is the package with its test files, and a generated `main` that lists the tests and calls `testing.Main`. How the generated `main` reaches the package differs for a library and for a program, as it does in Go:

- **A library package** is imported by the generated `main` under its import path, the directory's path under the module's root, `import "shapes"`, and the tests are called as `shapes.TestArea`, which the capital letter exports. The importer, told the path under test, loads that package with its test files, where an import of the same package by anything else loads it without them. A library outside any module cannot be imported by a path, so `snello test` on one is refused, `shapes: no snello.mod above the package; snello test needs the module to name it`, as `go test` needs a module.
- **A main package** cannot be imported, so the generated file joins the package: the package's files are read with their `func main` left out, the test files and the generated runner are added, and the whole is built as the program. The program's own `main` is not called by its tests, as Go's cannot be either in practice.

The generated source is never written beside the package: it is given to the loader as a file in memory, named `snello_test_main.snello` in the package's directory so that its errors name a file and its imports are answered from the package's module. The standard library's packages are tested the same way by `go test ./std`, which runs the runner over each embedded package that has a test file, under the library's privileges; `snello test` itself does not reach into the embedded library, since its directories are not on the user's disk.

The build goes through the backend the flags choose, as `snello build` does, into a temporary directory, as a release build; the binary runs with its output passed through and its exit code returned. Test discovery is on the parsed test files, every `FuncDecl` named `Test` or `Benchmark` plus a capital with no receiver, in the order of their names, and the signature check is on the checked package, so the message names the function and what it wanted.

## 6. What is not here

- **External test packages**, Go's `package strings_test`, which test a package through its exported names alone. Internal tests see everything, which is more than enough for a library testing itself; the external form can come when a program needs to test a package as its users see it.
- **Examples as tests and coverage.** Each is a task when a program asks for it. Coverage needs the backends to count, which is a design of its own.
- **`recover` for programs.** The mark of section 3 is the runner's and the library's; a program cannot catch a panic, as before, since a panic is a bug and not an error (`01-language-design.md`).
- **Test caching.** `go test` caches results by the inputs' hashes; `snello test` builds and runs every time, as `snello build` does.
- **A race detector.** `go test -race` exists because two goroutines may write one variable through a shared pointer and Go's types do not stop them. In Snello a value reaches another task by a move, by `Send` inference or inside an `Arc`, and a `mut` of a shared value comes only through a lock that owns it, so a data race is a compile error and there is nothing for a detector to find (`03-ownership-across-tasks.md`; decided 2026-11-07: there will never be a `-race`). The runtime and a package's bound C are outside that guarantee, and a race there is found by the stress programs and by review; if a bound C library is ever suspected, a thread sanitizer on that C is a flag of its build, not of Snello's. A package variable was the one value outside the rule until 2026-11-07, found while checking this claim for the book and closed the same day by task 9.2 (`03-ownership-across-tasks.md`, package variables).

## Decisions

**Decided 1 (2026-11-07, proposed): internal test files, found by name, in the package.** The alternatives were tests as a separate package beside the one tested, Rust's `#[cfg(test)]` module inside the file, and tests found by an attribute. The file suffix is Go's, which a Go developer knows, it keeps the test out of the program with one rule in the file lister, and it needs no syntax; an attribute would be new syntax for nothing the name does not say.

**Decided 2 (2026-11-07, proposed): no handle; a failed assertion is a panic the runner catches.** Go's `t *testing.T` was the first draft, and the owner asked for something simpler that is not Go copied. The handle does three jobs in Go, reporting a failure, cleaning up and naming the test a helper reports to, and Snello has a better answer to each: a panic that already unwinds the task running every `Drop`, which a mark turns into a reported failure; `Drop` itself for cleanup; and the runtime's knowledge of which task runs. Tests returning `Result<(), Error>` with assertions ending in `?`, Zig's shape, was the other candidate, and loses on noise: every assertion line ends in `?` and every test in `return Ok(())`. What the panic shape gives up is Go's record-and-continue, which Rust never had and subtests replace.

**Decided 3 (2026-11-07, proposed): the generated `main` joins a main package and imports a library package.** One form for both was considered: the package under test compiled as `main` in every case, with its test files and the runner. It fails for the standard library, whose symbols the runtime defines under the package's own prefix, so a renamed package would not link, and it would make a library's tests a different program from the library. Two small forms, each Go's for its kind of package, cost less than one that bends.

**Decided 4 (2026-11-07, as built): the place of a call is the running task's.** The compiler wrote the file, line, call text and parts into a package variable of `testing` and of `expect`, which was simplest and wrong: two parallel tests write them at once, and the parts, an owned string dropped before each write, could be freed twice. It was found while checking the book's claim that a data race is a compile error, which that package variable, and any other, escapes until task 9.2 of the stage closes it. The place now goes to the runtime, `sn_testing_line` and `sn_testing_call`, which keep it on the task, and the packages read it back through bodiless `where()` and `parts()`; the alternative, a `sync.Mutex` in the package, would serialize every assertion of every parallel test for a value that is never shared on purpose.
