# Stage 5: packages and performance

Goal: an initial working language, as the owner restated it on 2026-11-05: lean, without a collector, very fast, with a very small standard library that is rock solid and follows the reference implementations of the industry, and a core that any external library can be built on, as Rust's crates are built on its `std`. The stage was planned on 2026-10-21 as libraries and performance, with the libraries of examples 03, 04, 17 and 19 written into the standard library. On 2026-11-05 the owner narrowed it: `regexp`, `json`, `db` and `net/http` leave the standard library, as Rust keeps them out of its own, and in their place the stage gives Snello what a library outside the compiler needs, packages of the program's own and a way to bind C, and proves it by writing `regexp` as such a package with nothing the standard library is privileged to. The performance tasks stay: the three rows of the table where the measurement fell short, allocation, strings and a map, and tasks, reach the targets the owner set on 2026-10-21, measured on macOS and on both Linux runners. Nothing in the language changes except what the proof shows a library cannot do without; where an example disagrees with the language or the library, the disagreement is settled and recorded in the design document in the same change, as `AGENTS.md` requires.

## Task 5.1: Allocator

**Description:** `tree` takes 63 ms where Go takes 38. Task 4.3 deferred an allocator of the runtime's own to the benchmark, and the benchmark says to write it: size classes for small blocks with a free list per class per worker, so that an allocation and a free on one thread take no lock, and the system's allocator for large blocks. The checking build's tracker stays over it, so the leak report and the use-after-release checks are unchanged.

**Acceptance criteria:**
- [x] `tree` within 1.25 times Go's time (2026-10-21: 38 ms through the LLVM backend and 41 through C, against Go's 35, 1.09 times; it was 60)
- [x] No other row of the table slower, and `memory`'s peak no higher than today's 33 MB (2026-10-21: the peak is 33 MB; `words` went from 250 ms to 194 and `memory`'s mean round from 177 µs to 103, the rest unchanged)
- [x] The debug build of every example ends with a clean leak report (2026-10-21: the debug tracker sits on the new heap, so every debug build in the suites runs it)

**Verification:**
- [x] `SNELLO_BENCH=1 go test ./bench -v`, the numbers recorded in `../design/01-language-design.md` (2026-10-21)
- [x] Tests pass: `go test ./...` (2026-10-21)

**Progress:** 2026-10-21, `runtime/c/alloc.c`, after Go's `malloc.go`. Twenty size classes up to 1 KB, multiples of 16 to 128 and then four to each doubling; a span is 64 KB of one class. Each thread keeps a free list per class, Go's `mcache`, so the common allocation and free are a pop and a push with no lock; a list that runs dry takes a batch from the class's central list, Go's `mcentral`, or a new span, and a list grown past two batches gives one back. `sn_free` takes no size, so the spans are cut from one reservation of address space made before `main`, 64 GB if the system allows and halving down to 1 GB, and a pointer inside it finds its class in a table by its span's number; a block over 1 KB, or one asked for once the reservation is used up, is the system's, and anything outside the reservation goes back to the system, so a block the runtime took from `malloc` itself is freed right. Nothing in the allocator parks, so reading the thread's lists once per call is safe though a task may change threads between calls. The debug build's tracker now sits on the heap rather than on `malloc`, so every debug build runs it. Spans are never given back to the system, which `memory`'s flat peak allows; a program that frees much of one size and then needs another keeps both, and that is left until a program shows it. `examples/run/alloc.snello` makes strings of every class and larger in four tasks at once and frees them in `main`, checking each byte. Both backends now take the paths of the runtime's C files from `runtime.Paths` instead of naming them. Review 2026-10-21: the table of classes by size and the batch of each class are constants, so an allocation is right before the constructor that makes the reservation has run, and a free no longer divides; under a limit on the address space the reservation takes at most a quarter of it, leaving the rest to the tasks' stacks, and none under 4 GB; `TestNoReservation` runs `alloc.snello` in both builds under a limit of 2 GB on Linux, where every block is then the system's, which a probe of the allocator's own variables confirmed; the suite passes on arm64 Linux in a container with clang, as CI builds, and x86-64 is left to CI. Two findings wait for the contention benchmark: the central list is walked under its lock, and a new span goes whole to the thread that cut it. In a debug build a block of 993 to 1024 bytes is the system's, since the tracker's header puts it past the largest class.

**Dependencies:** Stage 4
**Files:** `runtime/c/alloc.c`, `runtime/c/snello.c`, `runtime/c/debug.c`, `runtime/runtime.go`, `examples/run/alloc.snello`
**Scope:** L

## Task 5.2: `heap`

**Description:** A generic binary heap written in Snello under `std/heap/`, ordered by `Ord` as `sort` is: `New<T: Ord>()`, `Push`, and `Pop` returning `T?`, the smallest first. Example 04 runs Dijkstra over it. `heap` is a placeholder in `../design/05-standard-library.md` today and joins the library table.

**Acceptance criteria:**
- [x] Example 04 runs with an expected output through both backends and leaves the pending list in `examples/suite_test.go` (2026-10-22: `std/heap` is a binary min-heap over a slice, the layout and algorithm of Go's `container/heap` and Rust's `BinaryHeap`: `New`, `From` in O(n), `Push`, `Pop` returning `T?`, `Peek`, `Len`, with the program `heap` in both backends. It moves elements with a new slice method, `Swap`, in the checker, both backends and the runtime, since `xs[i], xs[j] = xs[j], xs[i]` moves out of the slice. Found on the way and closed, all older than this task: the C backend wrote a local under its Snello name, so `cur := cur.Expect(...)` in a loop read itself, and a local or parameter named as a struct hid it, `shadowed-locals`, `toplevel-names`; a C `for` condition's temporary was declared in the loop's block and dropped outside it, and a step's label was written with no `continue` to it, `loop-owned-step`; the LLVM backend dropped a temporary lent `mut` twice, a slip of 5.13's fix, `mut-temporaries`; both backends copied a field or element of a temporary out and dropped it beside the whole, `temporary-parts`, now a place inside the temporary, `07-lowering.md`. The checker refused `match o { Some(o) => ... }` and `if o := o` as a use of the moved `o`, finding the value's names after the binding hid them, now resolved in the scope the value was written in; it let `mut` widen a `ref`, `mut r` and `mut xs[0:2]` of a `ref []int`; it let a borrow a call returned sit beside a `mut` of the same value in one call, `fmt.Println(m.Get("a"), m.Remove("b"))`, which `examples/run/maps.snello` and `arena.snello` did, now rewritten with their output unchanged; it let a part of a temporary be moved out by `??`, `?`, `range`, a binding, an argument or an assignment, `TestMoves`, `TestLending`; and the review after found it reading one source for every borrow, so a borrow chosen by an `if` or `match` expression, or the right side of `??`, was neither held, lent across a call, ranged, nor checked on `return`, which let accepted programs read freed memory: a value chosen at run time now borrows from every branch, `borrowSources`, `TestLending`. The differential test gained the kinds `part`, `element` and `lent`, each failing on the compiler before these fixes)
- [x] `heap` is in the library table of `05-standard-library.md`, and chapter 14 lists it (2026-10-22, with `Swap` in the slice row and chapter 9)

**Verification:**
- [x] Tests pass: `go test ./std ./examples` (2026-10-22: the full suite on macOS and arm64 Linux, the differential test's seven kinds, every example in a debug build, and the channel stress program twenty times)

**Dependencies:** Stage 4
**Files:** `std/heap/`, `examples/expected/04-graph.out`, `../design/05-standard-library.md`
**Scope:** S

## Task 5.3: Packages

**Description:** A Snello program is one file, and an import resolves only against the standard library embedded in the compiler, `std.Sources`, so no library can exist outside this repository. This task makes a directory of `.snello` files a package, Go's shape: the files of one directory are one package and see each other's declarations; the program's directory is the main package; a `snello.mod` file in the nearest directory up from the program names the module, `module example.com/app`, and an import path under that name resolves to a directory under it, while a path that is neither a standard package nor under the module is looked up in the file's `require` lines, each naming an import path and the directory on disk that holds it. No versions and no network: a dependency is a directory, as Go's `replace` makes one, and what a registry would add is a stage of its own. The importer gains a second source after the embedded library, the checker already checks a package into the shared `Info` with `Object.Pkg()`, and both backends already qualify a library name as `sn_<path>_<Name>` with `/` as `__`, so the work is the importer, the module file, the multi-file package and `snello build <dir>`. An import cycle is refused with the cycle named. What the checker today allows only to a library package, a function without a body, `...ref any`, `any`, `local`, and the lowered names, stays with the embedded library: a package of the program's own has none of it until task 5.5 decides what crosses to C.

**Acceptance criteria:**
- [ ] A package of two files in a directory beside the program, imported by path, with a function, a struct, an enum, a trait and a generic function of its used across the boundary, through both backends as a release and a debug build
- [x] A package that imports another package of the program's own and a standard package; an import cycle refused as `import cycle: a imports b imports a` (2026-11-05: `util` importing `shapes` and `strings`, and the cycle `a` to `b` to `a` named with its full paths, in `TestOwnPackages`)
- [x] A `snello.mod` with a `require` line resolving an import path to a directory elsewhere on disk (2026-11-05: a relative or absolute directory, a required module bringing its own `snello.mod` and require lines, a required directory with no module file, and a required module whose file names another path refused; `TestModFile`, `TestPackageDir`, `TestOwnPackages`)
- [x] The standard library's privileges refused in a package of the program's own with the existing messages (2026-11-05: a function without a body, `...ref any` and `local`, each with the program's message, since the privileges hang on `types.Config.Standard` and not on the import path; a method on another package's type refused with Go's message on the way)
- [ ] `snello build <dir>` builds the main package of a directory, and `snello build <file>` is unchanged
- [x] The layout of a package and the module file in `../design/08-packages.md`, chapter 2 of the book says how a program with a package is laid out, and Appendix B maps it to Go's modules (2026-11-05)

**Verification:**
- [ ] `examples/run/packaged/`, a program with a package directory, in the run suite of both backends
- [ ] Tests pass: `go test ./compiler/importer ./compiler/types ./examples`

Progress: the design written 2026-11-05, `../design/08-packages.md`, with the section on packages in chapter 2 of the book and the chapter 2 table of Appendix B. First slice the same day: `snello.mod` read by `importer.ParseModFile` and found by `FindModule`; the importer answers an import from the standard library, then the module, then its require lines, a required module bringing its own; the cycle message in the form above; a program, a relative path and a missing directory refused by the importer, and two paths sharing a symbol prefix by the backends through `lower.CheckPrefixes`; the standard library's privileges tied to `types.Config.Standard` rather than the empty import path, so a package of the program's own gets the program's messages; a method on another package's type refused with Go's message; the symbol prefix of a dotted path; `snello build`, `check` and `emit` finding the module above the file. Second slice 2026-11-06: a package of several files, parsed into a `token.FileSet` so that a position names its file, and checked by `types.Config.CheckFiles` a phase at a time over all of them, each file with its own imports; files disagreeing on the package clause, and a name declared in two files, refused with the other file named; `TestFileSet`, `TestCheckFiles`, and the two-file packages of `TestOwnPackages`. `snello build DIR` and `examples/run/packaged/` follow.

**Dependencies:** Stage 4
**Files:** `compiler/importer/`, `compiler/types/`, `cmd/snello/`, `examples/run/packaged/`, `../design/08-packages.md`, `../book/02-getting-started.md`
**Scope:** L

## Task 5.4: Example 17 and `net`

**Description:** `net` stays in the standard library, as Rust's `std::net` does. The example names `net.Socket` and calls `net.Dial("...")` for the socket itself; the package of task 4.5 has `Conn`, and its `Dial(host, port)` returns a `Result` and needs a peer listening. Which side moves is the owner's decision and is made first: the example to the package, with a `Conn` dialled to a listener the program opens, or the package to the example.

**Acceptance criteria:**
- [ ] The decision recorded in `05-standard-library.md`, and in `examples/README.md` if the example changes
- [ ] Example 17 runs with an expected output and leaves the pending list

**Verification:**
- [ ] Reviewed by the owner before the code
- [ ] Tests pass: `go test ./examples`

**Dependencies:** Stage 4
**Files:** `examples/17-state-machine.snello` or `std/net/`, `examples/expected/17-state-machine.out`
**Scope:** S

## Task 5.5: Binding C from a package

**Description:** The standard library reaches the runtime through functions declared without a body, which the checker reserves to library packages, and the runtime is C compiled with the program. A library outside the compiler that wraps a C library, as Rust's crates do through `extern "C"` blocks and `-sys` crates, needs the same two things: a declaration without a body bound to a C symbol, and C sources or a system library compiled and linked with the program. The design is written first and reviewed by the owner: Go's way, since Go has one, a function without a body is implemented elsewhere, bound to the symbol `sn_<path>_<name>` as the standard library's are, with the C signature derived from the Snello types by the ABI `runtime/c/snello.h` already defines; `.c` and `.h` files in the package's directory are compiled with the program, as `cgo` compiles the C files of a package; a system library is named by one line in `snello.mod`. The types that may cross are listed by the note, numbers, `bool`, `string` and `[]byte` as views, an owned pointer as the opaque handle a C library hands back, and anything else is refused at the declaration with a message naming the type; what the C side does with memory is the library author's responsibility, as it is inside Rust's `unsafe`, and the debug build's tracker sees what the C side allocates through `sn_alloc`. `any`, `...ref any`, `local` and the lowered names stay the compiler's, since each exists for one standard package and a library needs none of them.

**Acceptance criteria:**
- [ ] `../design/08-packages.md` says what crosses a binding and how, reviewed by the owner before the code
- [ ] A package of the program's own declares a function without a body, implements it in a `.c` file in its directory, and a program calls it through both backends as a release and a debug build
- [ ] A type that may not cross is refused at the declaration with its name in the message, and a body-less function in the main package is still refused
- [ ] A package that names a system library in `snello.mod` links it: `m`'s `cbrt` called from Snello
- [ ] Chapter 14 of the book has a section on binding C from a package

**Verification:**
- [ ] `examples/run/bound/`, a program with a package that binds C, in the run suite of both backends
- [ ] Tests pass: `go test ./compiler/... ./examples`

**Dependencies:** 5.3
**Files:** `compiler/types/`, `compiler/emit/`, `compiler/importer/`, `cmd/snello/`, `runtime/runtime.go`, `examples/run/bound/`, `../design/08-packages.md`, `../book/14-standard-library.md`
**Scope:** M

## Task 5.6: `regexp` as a package outside the compiler

**Description:** The proof that the core is enough: `regexp` written under `lib/regexp/` at the repository's root, outside `std/`, with nothing a package of the program's own cannot write, so that it compiles under the rules of task 5.3 and binds no C. Go's syntax, the RE2 subset, compiled to a program and run by a Pike VM, which is what Go's `regexp` and the `PikeVM` of Rust's `regex` crate run, so a match takes time linear in the input and no pattern backtracks; the subset is what example 19 uses and the common forms, literals, `.`, classes with ranges and `\d`, `\w`, `\s`, the anchors, `*`, `+`, `?`, `{n,m}`, alternation, groups and the `(?i)` flag, written in the package's doc comment. `Compile` returns a `Result` whose error carries Go's message for the pattern, `missing closing )`; `Match` says whether a line matches, `Find` returns the span of the first match or none. Every gap the package meets in the language is a finding of this task, closed in the core or recorded as a decision in the design note it belongs to; that is what the proof is for. The package is measured against Go's `regexp` and Rust's `regex` crate on a grep of a 100 MB file, as every standard package is measured, with the numbers in the package's README; Rust's crate runs a lazy DFA and is expected to be well ahead, Go's is the target.

**Acceptance criteria:**
- [ ] `lib/regexp/` compiles as a package of the program's own under task 5.3's rules, with no function without a body and none of the standard library's names
- [ ] Table tests for the matcher over every form of the subset, and a malformed pattern refused by `Compile` with Go's message
- [ ] `(a*)*b` against thirty `a`s and no `b`, which a backtracking matcher takes exponential time on, matches in under a millisecond
- [ ] Not slower than Go's `regexp` on the grep of a 100 MB file, measured against Go's and Rust's with the method of task 5.8 and recorded in `lib/regexp/README.md`
- [ ] Every language gap the package met is closed or recorded, listed in this task's progress

**Verification:**
- [ ] `go test ./lib/...`, which builds the package's tests through both backends
- [ ] Tests pass: `go test ./...`

**Dependencies:** 5.3
**Files:** `lib/regexp/`, `compiler/types/`, `compiler/emit/`
**Scope:** L

## Task 5.7: Examples 03 and 19 and the empty pending list

**Description:** Example 19 imports `regexp` from `lib/regexp/` through a `snello.mod` in `examples/`, since the examples show how Snello programs are written and a program with a dependency is one of them; this is the owner's decision, the alternative being a plain-substring grep over the standard library alone. Example 19 also fails the checker for three reasons outside the library: it names `bufio.Writer` without its type argument, it ranges over `bufio.Scanner.Lines`, which does not exist, and `fmt.Fprintf` cannot infer its writer from the argument; each is settled as a library change or an example change. Example 03 loses `net/http`, `db` and `json`, and keeps what it is for, long-lived shared state, per-request borrows and `Drop` for connections: it becomes a cache server over `net` speaking a line protocol, `GET key` and `SET key value`, a task per connection, its store behind `sync.Arc` with a connection's `Drop` reporting the close. Its checker errors are settled as example 19's are: `sync.NewArc` cannot infer its type argument, a map read is bound with `if x := v` as though it were an option, and `sync.Arc.New` is called where the library has `sync.NewArc`. The example serves forever, so the suite starts the program, connects over the loopback, sends a fixed script and compares the responses with an expected output.

**Acceptance criteria:**
- [ ] Example 19 runs with an expected output and the files it reads, through `lib/regexp`, and leaves the pending list; the decision recorded in `examples/README.md`
- [ ] Example 03 serves a script of requests over a real socket, made by the suite, and its responses match the expected output; the rewrite recorded in `examples/README.md`
- [ ] The six checker errors are settled, each decision recorded in the design note it belongs to
- [ ] The pending list in `examples/suite_test.go` is empty

**Verification:**
- [ ] Tests pass: `go test ./...`

**Dependencies:** 5.4, 5.6
**Files:** `examples/03-http-cache.snello`, `examples/19-cli-grep.snello`, `examples/snello.mod`, `examples/suite_test.go`, `examples/expected/`, `examples/README.md`
**Scope:** M

## Task 5.8: Benchmarks that can be trusted

**Description:** The table is the best of three runs on one Mac; it compares Rust only with its default allocator, the system's; and it times the allocator on one thread. Four changes make its numbers hold up, before the tasks that move them are measured. First, each binary runs once and the run is discarded, since the first run of a new binary on macOS pays for the system's check of it, then at least ten times, and the table reports the median with the least and the most; the programs that time themselves, `tasks` and `memory`, report the median of their own numbers the same way. Second, the CI workflow runs the benchmark natively on its x86-64 and arm64 Linux runners and writes the table to the job's summary; a program that prints the wrong thing fails the job, a time never does, since shared runners are too noisy for a time to be a test. Third, each Rust program is built twice, once as today and once with mimalloc as its global allocator, so that against Rust means against its best and not its default; this makes the Rust versions a Cargo project with one dependency, fetched over the network and pinned by a `Cargo.lock`. The zero-dependency rule binds the compiler, not the programs it is compared with, but the dependency is the owner's to accept. Fourth, `bench/churn`, in Snello, Go and Rust: one task per core allocating and freeing small blocks of mixed sizes at once, and blocks made in one task and freed in another across a channel, so that the allocator is timed under contention, the case its two open findings from task 5.1 wait on.

**Acceptance criteria:**
- [ ] The owner accepts the Rust dependency, or names another allocator, before it is added
- [ ] Every row reports the median, least and most of ten runs or more after a discarded first, and the timed programs their own numbers likewise
- [ ] A Rust column built with mimalloc beside the default one, for every program with a Rust version
- [ ] `bench/churn` in Snello, Go and Rust prints the same, with every task allocating at once and blocks crossing tasks
- [ ] The CI workflow runs the benchmark on x86-64 and arm64 Linux and writes the table to the job's summary; a wrong output fails the job and a time does not
- [ ] The table in `../design/01-language-design.md` recorded again under the method, for the Mac and for both Linux runners, with task 5.1's target checked again by median

**Verification:**
- [ ] `SNELLO_BENCH=1 go test ./bench -v` on macOS, and the job summaries of both Linux runners
- [ ] Tests pass: `go test ./...`

**Dependencies:** 5.1
**Files:** `bench/`, `bench/rust/`, `.github/workflows/test.yml`, `../design/01-language-design.md`
**Scope:** M

## Task 5.9: Strings and maps

**Description:** `words` takes 256 ms where Go and Rust take about 152. The design document puts the cause in the library, not the language: `fmt.Sprint` allocates every string it builds, and the map hashes every key with one general routine where Go's and Rust's are specialized by the key's type. The benchmark is profiled first, the profile recorded, and the fix follows what it shows.

**Acceptance criteria:**
- [ ] `words` within 1.1 times Rust's time
- [ ] The profile before and after recorded beside the table

**Verification:**
- [ ] `SNELLO_BENCH=1 go test ./bench -v`, the numbers recorded in `../design/01-language-design.md`
- [ ] Tests pass: `go test ./...`

**Dependencies:** 5.8
**Files:** `runtime/c/snello.c`, `std/fmt/`, `compiler/emit/llvm/`, `compiler/emit/c/`
**Scope:** M

## Task 5.10: Cheaper tasks

**Description:** A spawn costs 1620 ns where Go's costs 214, and a context switch 1415 ns where Go's costs 124. The design document names the causes: a task's stack is a 256 KB reservation where Go's starts at 2 KB, the stack pool and each worker's run queue take a lock, and a channel operation takes the channel's mutex where Go's fast path does not. Each cause is changed and measured on its own, so the table says what each was worth.

**Acceptance criteria:**
- [ ] A spawn and a context switch each within 2 times Go's cost
- [ ] `TestManyTasks` and `TestParallel` pass on the three CI runners

**Verification:**
- [ ] `SNELLO_BENCH=1 go test ./bench -v`, the numbers recorded in `../design/01-language-design.md`
- [ ] Tests pass: `go test ./...`

**Dependencies:** 5.8
**Files:** `runtime/c/sched.c`, `runtime/c/snello.c`
**Scope:** L

## Task 5.11: The standard library review's findings

**Description:** The review of 2026-10-21 held every package under `std/` and the runtime behind it to Go's standard library, `arena` and `rc` to the reference-counting and generational-arena libraries of other languages, with a program built and run for every finding. Sixteen findings are critical and each was reproduced before it was listed; three of them are holes in the checker the library exposed. They are fixed one per change, in this order, each with the test that would have caught it. The important findings, the completeness gaps and the missing tests are listed in the review and become tasks once these are closed.

**Acceptance criteria:**
- [x] An `if` or `match` binding out of a call's option holds what the call lent: `if v := a.Get(i) { a.Remove(i); v[0] }` is refused (2026-10-21: `bindThrough` holds the bindings of an owned scrutinee through the value's source, as `hold` does for `:=`; six cases in `TestBorrows`; the six programs of the review refused with the borrow named)
- [x] `_ = x` moves `x`, so it is dropped once (2026-10-21: a blank target in `=`, a multiple `=` and `:=` moves an owned value as a named one does; four cases in `TestMoves`, and `examples/run/drops.snello` drops a discarded value and the discarded half of a pair once each, in all four builds)
- [x] A method that changes a slice's length is refused on a borrow of a sub-slice (2026-10-21: refused at run time, as an index is checked, since `mut xs[2:4]` has the type of a whole slice and a function given it cannot tell; a sub-slice carries the capacity `SN_VIEW`, and `Push`, `Pop`, `Insert`, `Remove`, `RemoveValue`, `Clear` and replacing it panic before an element is touched, `cap` of one is its length; four programs in `compiler/emit/internal/backendtest`, and the review's four programs now panic in place of corrupting or leaking)
- [x] Dropping the value of an `rc.Rc` that holds the last `Weak` to its own cell frees the cell once (2026-10-21: the last `Rc` counts as one more `Weak` while the value is dropped, so the cell outlives the value's drop, the counting every reference-counted library uses; with locks bound by `Send` no program can now build such a value, and the bracket stays as the cell's own guarantee)
- [x] A cycle of `rc.Rc` through a lock is impossible or documented, by the owner's decision (2026-10-21: impossible, the promise the design and the book make; `sync.Mutex<T>` and `sync.RwLock<T>` require `T: Send`, and the review's two programs are refused, `rc.Rc<Node>? does not implement Send`)
- [x] `Clear` drops every element (2026-10-21: both backends drop each element before the length goes to zero; `examples/run/drops.snello` clears a slice of values with `Drop` and prints each drop)
- [x] An empty string built at run time owns no block (2026-10-21: `SN_OWNED` frees the block of a string of no bytes and gives the empty literal, for every runtime function that builds one; twenty million `"".ToUpper()` peak at 1.8 MB, where they reached 322; `examples/run/strings.snello` builds four empty strings in its debug build)
- [x] `bufio.Writer` keeps its first error and never writes a byte twice (2026-10-21: a failed `Flush` drops the bytes written before the failure from the buffer and keeps the error, which every later `Write` and `Flush` returns, as Go's does; `io.Error` and `io.Kind` implement `Clone` for it; `examples/run/failures.snello`)
- [x] `bufio.Reader.ReadString` keeps what it read before an error (2026-10-21: the bytes go back to the Reader's buffer and the next call returns them with what follows, since a `Result` cannot carry data and an error as Go's two results do; the `Scanner` gives what it read before a failure as the last line, then stops, which the review listed as important; `examples/run/failures.snello`)
- [x] `os.RemoveAll` refuses a path ending in `.` before removing anything (2026-10-21: `RemoveAll .: invalid argument`, Go's message; `examples/run/files.snello` asks it of its temporary directory and reads its file afterwards)
- [x] Map hashing spreads keys that differ only in their high bits (2026-10-21: every key's hash is finished with MurmurHash3's finalizer before it picks a slot; a million keys that are multiples of 65536 insert in 66 ms, where they took 126 s, and the `map-strided-keys` program would time out without it; a per-process seed against chosen keys is left with the review's suggestions)
- [x] Sorting floats orders NaN first, as Go does, and `IsSorted` agrees (2026-10-21: `Sort` and a number's `Cmp`, which `sort.Sort` and `IsSorted` use, take Go's order in both backends, a NaN before every other value and level with another; `examples/run/order.snello`)
- [x] `net.Accept` skips a connection reset before it was accepted (2026-10-21: a connection whose socket options fail, Darwin's sign of a reset, or that accept reports aborted, is closed and the next one taken, as Go's runtime does; `TestAcceptAfterReset` has Go send a reset connection and a live one, and fails without the change)
- [x] A pending `time.After` is an entry in the timer heap, not a task (2026-10-21: the timer puts the time in the channel itself with `sn_chan_offer`, a send that never waits; a hundred thousand pending use 25 MB where they used 1.2 GB, Go 10 MB; a `Tick` is an entry that goes back in the heap on its period, so a hundred ticks of 10 ms take 1001 ms where they took 1210, and two thousand abandoned ones leave no task; the `timers` program)
- [x] `time.Tick` with a duration of zero or less does not spin (2026-10-21: no timer is started, so the channel never gets a tick, as Go's `Tick` gives a channel nothing sends on; the `timers` program)
- [x] `time.Sleep` and `time.After` saturate a deadline past the largest time (2026-10-21: one `deadline` in the scheduler for both; the `timers` program races the longest `After` against 10 ms)

**Verification:**
- [x] Tests pass: `go test ./...`, on macOS and on arm64 Linux (2026-10-21, Linux in a container with clang as CI builds; x86-64 is CI's)

**Dependencies:** Stage 4
**Files:** `compiler/types/`, `std/`, `runtime/c/`, `compiler/emit/`
**Scope:** L

## Task 5.12: The review's important findings

**Description:** The important findings of the review of 2026-10-21, after task 5.11 closed the critical ones: output and errors that differ from Go's where nothing documents a difference, edge cases the packages miss, and costs a program meets at scale. They are fixed by area, each with a test that would have caught it, and where Snello's design decides otherwise the design notes say so. Five findings are features rather than defects and wait for a design of their own: resolving host names and IPv6, deadlines on a connection, reading and writing one connection from two tasks, a blocking file read that holds a worker, and stopping a `Tick`.

**Acceptance criteria:**
- [x] `fmt`, `strconv` and `math`: floats in Go's shortest form and exponent rule, `+Inf`, `-Inf` and `NaN` spelled as Go spells them, `f32` at its own precision, a verb checked against its argument and an extra argument refused when the program is built, precision on `%s`, `%d` and `%v`, `%q` escaped as `strconv.Quote` escapes, `Abs`, `Min`, `Max`, `min` and `max` right on `-0` and NaN, a `fmt.Stringer` honoured, `ParseFloat` taking Go's grammar; printing an `rc.Rc` builds (2026-10-21: all of it; the format is read by one grammar, `compiler/types/format.go`, which the checker and both backends share; `examples/run/formats.snello` matches a Go twin line for line; an `Rc` and an `Arena` print as their values)
- [x] `strings`, `sort` and the slice methods: case mapping, `Fields` and `Trim` on Unicode, `Split` by UTF-8 sequence, a substring search that is not quadratic, `Repeat` refusing a bad count, building strings in bulk, `Sort` on a slice of an `Ord` struct, and Go's panic messages for slicing and `make` (2026-10-21: all of it; the case, white-space and printable tables are written by `runtime/mkunicode.go` from the Unicode Character Database 17.0.0, not from Go's packages, and checked against Go's `unicode` rune by rune; a 50,001-byte needle in 20 MB takes 1 s where it took 37; `Trim` and `Lines` keep their names, with their difference from Go's recorded; `examples/run/unicode.snello`)
- [x] `io`, `bufio` and `os`: `os.Stdout` unbuffered as Go's, the standard descriptors never reused by a file, a `Scanner` with a limit on a line, `MkdirTemp`'s pattern as Go reads it, an `io.Error` a program can make, `os.Remove` and `os.WriteString`, and an interrupted system call retried (2026-10-21: all of it; `fmt.Println` writes its line in one `write`, at 1.25 times Go's time for a million lines; `examples/run/streams.snello` and four tests in `compiler/emit/llvm/streams_test.go` that fail against the old runtime)
- [x] `time` and `sync`: a `Time` across Go's range with `Sub` saturating, a monotonic clock under `Since` and the timers, locks without a global lock, a deadlock reported rather than hung, and an `RwLock` that lets a waiting writer in (2026-10-21: all of it; a lock is its address, so eight tasks on their own locks take 71 to 105 ms where they took about 900; a program whose tasks all wait with nothing to wake them ends with `fatal error: all tasks are asleep - deadlock!`; `examples/run/clocks.snello` and seven programs in `compiler/emit/internal/backendtest`; a `WaitGroup` is still never freed, which needs a count of its copies, and an unlock still wakes every waiter)
- [x] `net`: ports checked, errors that carry the address and the call as Go's do with a kind for refused and reset, `Dial` sure of its connection after a wake, and every socket `TCP_NODELAY` and close-on-exec (2026-10-21: all of it; a socket's error is an `io.Error` whose path is `tcp` and the address and whose message starts with the call, with the kinds `Refused`, `Reset` and `TimedOut`; a host name or an IPv6 address is refused with a message saying so; the poller wakes through a slot per descriptor, which a task empties before it leaves its wait; `examples/run/net.snello`, `TestResetMessage`, `net-stale-wakes`)

**Verification:**
- [x] Tests pass: `go test ./...`, on macOS and on arm64 Linux (2026-10-21, Linux in a container with clang as CI builds)

Found on the way: the C backend writes a product of integer literals as a C `int`, so `570 * 31556952` fails to compile with an overflow the language does not have (closed by the review below).

Review of 5.11 to 5.13, 2026-10-21, by four independent reviews of the runtime, the checker, the backends and the packages, each finding confirmed by a program. Two were memory-safety bugs in committed code, closed: a width on a string already wider than it, `%5s`, returned the lent string as the format's owned result and the next join freed the caller's block (`sn_pad` clones a lent string it would hand back; `pad-lent`); and a borrow a function returned was tied to the call's first argument where the escape rule ties it to the one borrowed parameter, so `if v := pick(0, ref m)` held `n` and `m` was free to change under `v` (`callSource` now serves the binding and the return rule alike; `borrowIn` looks into enums, results and arrays). The rest: `take` on a channel, an owned pointer, a borrow or a type holding one left nothing usable behind (`hasZero`); `%s` and `%q` refused `error`, a type parameter bounded by `fmt.Stringer` or `Error` and a trait value refining either; `return ref xs[0:2]` of a local returned a view of a freed buffer; a blank assignment of a `mut` borrow of a `Copy` type copied where a `mut` borrow never is; a stale wake inflated the poller's count of promised wakes for good, so a program that later deadlocked hung instead of reporting it (`armed` per slot, kept by the event or the close; `net-stale-promise`); `read` and `send` returned EINTR as an error; a registration that failed parked the task forever; `RemoveAll` recursed with a `PATH_MAX` buffer per level on a task's 256 KB stack, and now walks with `openat` and `unlinkat` over an explicit stack, as Go's does, with `sn_init` raising the limit on open files; a `select` whose every case returns fell off the end of a C function; a print built its line through a chain of joins; `Scanner.Buffer`'s limit held only for a reader handing out less than a block; `Duration` and `Time` printed as numbers, and print as Go's do now, with `Round`, `Truncate` and `Abs`; a `bufio.Reader` never asked its reader again after an end; a write after a failure of nothing returned `Ok(0)`; `NewWriterSize` of 0 gave one byte; `MkdirTemp` in a missing directory named its own call where Go names the `stat`. Four left over from that pass, closed in a second one the same day: a `type` declaration lists its traits after the type it is declared over, `type Duration i64 impl fmt.Stringer`, so a `Duration` prints as Go's does (`00-syntax-conventions.md`); the borrow a call through a package returns is tied to its argument, as one in the program is, which refused `examples/run/net.snello` writing to a connection a `bufio.Reader` held, so `Reader` and `Writer` lend what they wrap with `Get`; a format may be a constant, which the checker now records the literal of, and a `const` declared in a function, which crashed the LLVM backend, is a constant of its own there; and the LLVM backend left the signed digits of a `u64` formatted with `%d` behind, while the C backend wrote every integer literal as a C `int`, so `570 * 31556952` overflowed and the largest `u64` was refused: a literal carries a suffix of its type's width now (`integer-literals`, `const-formats`).

**Dependencies:** 5.11
**Files:** `std/`, `runtime/c/`, `compiler/emit/`, `compiler/types/`
**Scope:** L

## Task 5.13: Channels and wait groups freed by their last handle

**Description:** A channel and a `sync.WaitGroup` were `Copy` handles on runtime objects nothing freed, so a program making one per request, or per `time.After`, grew until it exited. They become owned handles with a count, as `06-tasks-and-channels.md` decides: moved like any owned value, cloned with one atomic add for another task, freed by the last handle, the buffered values of a channel dropped with it. The channel operations borrow the handle, so code within one task is unchanged; code that hands a channel or a group to another task clones it first.

**Acceptance criteria:**
- [x] `chan T` and its two directions are owned, with `Clone`; a send, a receive, `close`, `len`, `cap`, `for range` and a `select` case borrow the handle; a `ref chan T` converts to either direction; using a handle after it moved is the checker's error, with a test for each (2026-10-21: `IsCopy` says no for a channel, `Clone` is a predeclared method documented in `std/builtin`, `for range` lends a channel rather than consuming it, and every operation looks through a borrow to the channel (`chanOf`); a `ref` or `mut` of a channel both ways converts to a `ref` of either direction, never to a `mut` of one, which could store a narrower handle in the wider place; fourteen cases in `TestTaskBoundaries`; every program, example and book sample that hands a channel to a task clones it, `time.After` and `Tick` give the timer a clone, example 13's `Handle` is `Clone` with methods that borrow it, and `examples/run/tasks.snello` passes channels by `ref` through both backends)
- [x] The runtime counts a channel's handles atomically, and the last one frees the buffer, dropping what it holds, and the lock; a pending `After` holds a handle until it fires, a `Tick` while it ticks (2026-10-21: `sn_chan_clone` and `sn_chan_drop`, the element's drop passed by `make`, the channel on `sn_alloc` so a debug build sees one left; both backends drop a channel, clone it with `Clone`, and hold a handle a call returned as a temporary of the operation's statement, in a `select` too; a timer whose channel no one else holds stops, since the channel knows where its entry is in the heap, and at exit the pending timers give their handles back before the debug build counts)
- [x] `sync.WaitGroup` is `Clone` and `Drop`, built on `sync.Arc`, and its last handle frees its lock (2026-10-21: `struct WaitGroup impl Clone { a Arc<counter> }`, `Clone` derived and dropped through its `Arc`, which drops the counter with the last handle, the counter a struct whose `Drop` frees the runtime's lock, `Add`, `Done` and `Wait` on a `ref`, an empty group still refused with Go's message; a lock is now `sn_alloc`'s, so a debug build sees one left, and `waitgroup-clones` frees a thousand groups in both backends. Found on the way and closed: the LLVM backend never dropped a value built in a statement and lent `mut`, as a `Guard` is to `Get`, so `*m.Lock().Get() += 1` kept the lock and the next `Lock` waited forever, `guard-temporaries`. The review that followed added `compiler/emit/internal/differ`, which builds every statement form with every kind of temporary through both backends and requires one output, and it found three disagreements older than this task, closed: both backends dropped what the right side of `&&` or `||` would have built when the left decided, the C backend by building it first, which broke the short circuit, and the LLVM backend by dropping a slot never filled, which corrupted the heap in a release build; and the C backend held an `if` condition's temporaries through the body, so a guard taken in the condition deadlocked the body, where now a condition's temporaries end once it is decided, `07-lowering.md`)
- [x] A program making a million channels, and one waiting a million times in a `select` with `time.After`, stay flat in memory, and a debug build of each reports no block left (2026-10-21: `TestChannelsFlat` through both backends, the program `backendtest.Churn`, under 64 MB peak where keeping the `After`s to their deadline reaches 255 MB and never freeing 415 MB, both checked by breaking the runtime; its debug build reports nothing. Its review closed a `Tick` whose last handle went between an offer and the push for its next tick, which stayed one period, and lowered `==` and hashing of channels, which the checker accepted and neither backend wrote: two handles are equal when they are on one channel, as Go's are, `channel-equality`)
- [x] Every example, run program and book sample that hands a channel or a group to another task clones it; chapter 10, Appendix B and `03-ownership-across-tasks.md` say so (2026-10-21)

**Verification:**
- [x] Tests pass: `go test ./...`, on macOS and on arm64 Linux (2026-10-21, Linux in a container with clang as CI builds; a stress of 64 tasks a round dropping clones while their `After`s race, five runs a backend on each, debug builds clean)

**Dependencies:** 5.12
**Files:** `compiler/types/`, `compiler/emit/`, `runtime/c/`, `std/sync/`, `std/time/`, `examples/`, `_docs/book/10-concurrency.md`
**Scope:** L

## Checkpoint: stage 5

- [ ] All twenty examples run natively through both backends on macOS and on both Linux runners; the pending list is empty
- [ ] A package outside the compiler builds, binds C, and `regexp` is written as one with nothing the standard library is privileged to; the gaps it found are closed or recorded
- [ ] The standard library holds the packages of `05-standard-library.md` and nothing more, each on its reference algorithm and measured against Go and Rust, with the measurement recorded in the library table
- [ ] `tree`, `words` and `tasks` at their targets by the median of task 5.8, the table in the design document updated for the Mac and both Linux runners
- [ ] The book chapters this stage owns (see `../book/README.md`) are written and their status lines updated
- [ ] Reviewed by the owner
