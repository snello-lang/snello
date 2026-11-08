# Stage 11: the language whole

Goal: Snello has what a programmer expects of any standard programming language at the bare minimum, as `language-gaps.md` lists it, decided 2026-11-08. Every task keeps the memory safety the borrow checker enforces and brings in no garbage collector, and says how in its design before any code: a feature that only adds refusals is safe by construction, and every value a feature makes has one owner and is freed by its `Drop`. Tasks 11.1 and 11.2 come before the rest of stage 9, since 11.2 changes `strings`, `sync`, `rc` and `arena`, which tasks 9.7 and 9.13 build on. The rest come before the release of stage 10. `Eq` on tuples and options in generic code, the one gap of the document not here, is task 9.7's.

## Task 11.1: No `switch`

**Description:** Snello does not support `switch`, decided 2026-11-08: `match` is the one way to branch on a value, and checks that every case is covered, and Go's tagless `switch` is an `if` and `else if` chain. Nothing in the library, the examples or `lib/` uses it, and neither backend compiles it. The word stays reserved, so a Go habit meets a clear error and not a confusing one.

**Acceptance criteria:**
- [ ] `switch` is reserved and refused by the parser with an error that names `match` and the `if` chain; the parser's and the checker's `switch` code removed, with the tests that covered it
- [ ] The syntax note's list of what is carried over from Go, chapter 3, appendix A and appendix B say so; the gaps of task 9.6 that name `switch` point here

**Verification:**
- [ ] `go test ./compiler/...`; `go test ./...`, `make test` and `make check`

**Dependencies:** none
**Files:** `compiler/parser/`, `compiler/ast/`, `compiler/types/`, `compiler/emit/`, `../design/00-syntax-conventions.md`, `../book/03-basics.md`, `../book/A-keywords-and-grammar.md`, `../book/B-snello-for-go-programmers.md`
**Scope:** S

## Task 11.2: No zero values, and no panic a caller can reach in the library

**Description:** Today `var p *int` compiles to a null that `*p` follows, and `var m sync.Mutex<int>` is an empty handle whose `Lock` panics. The rule, decided 2026-11-08: a type has no zero value when it implements `Drop`, when it is a type parameter, when it is a pointer, channel, function or trait value, or when it holds one of these. The checker refuses every way of making that zero: `var x T` with no value, local or package level; a struct literal that leaves such a field out; an array literal shorter than its length; `make([]T, n)`, while `make([]T, 0, n)` stays; a read `m[k]` of a map of such values, with a message naming `m.Get(k)`; and `take`, as today. A builtin `replace(place mut T, v T) T` moves a value out of a place and leaves `v` there, for every type. With the rule, the library panics on no mistake a caller can make: the `sync` and `rc` handles exist only from their constructors, `strings.Repeat` returns `string?`, `WaitGroup.Add` and `Done` return `false` for a count that would go below zero and leave it, and `arena.Remove` drops its `Expect`. A branch inside `rc` and `sync` that only an invariant broken can reach keeps its panic, as the library's rule allows.

**Acceptance criteria:**
- [ ] The design recorded and reviewed before the code, in the syntax note and the borrows note, with the argument that the rule only refuses and so keeps every guarantee
- [ ] Each refusal has a checker test with its message; the programs the rule refuses are rewritten, `examples/run/shared.snello`'s empty handles and `examples/run/formats.snello`'s `take` of an `Rc`
- [ ] `replace` through both backends, in release and debug builds, its ownership clean in `-debug`
- [ ] `sync`, `rc`, `strings` and `arena` changed as described, their `_test.snello` files with them; the sync and rc programs clean under `-debug` and AddressSanitizer on both backends
- [ ] The existing borrow and move tests pass unchanged
- [ ] A chapter of its own in the book, for developers and maintainers; chapters 5, 10 and 13 and appendix B, which say a zero handle is empty and panics, corrected

**Verification:**
- [ ] `go test ./compiler/... ./std ./examples`; `go test ./...`, `make test` and `make check`

**Dependencies:** none
**Files:** `compiler/types/`, `compiler/emit/`, `std/builtin/`, `std/sync/`, `std/rc/`, `std/strings/`, `std/arena/`, `runtime/c/sync.c`, `examples/run/`, `../design/00-syntax-conventions.md`, `../design/04-borrows-ref-and-mut.md`, `../book/`
**Scope:** L

## Task 11.3: Constant expressions

**Description:** A `const` takes only a literal today, and an array length is a literal. A `const` takes an expression of literals, other constants, the arithmetic, bitwise and comparison operators, and conversions between number types, folded by the checker, `const KB = 1 << 10`; an array length is a constant, `[N]int`. An expression that overflows its type, divides by zero or names a value that is not constant is refused at compile time. No `iota`, since an `enum` names its cases, and no function run at compile time.

**Acceptance criteria:**
- [ ] The design recorded and reviewed before the code, in the syntax note
- [ ] Constants folded through both backends, package level and local, an array length among them; each refusal tested with its message
- [ ] A chapter of its own in the book, or chapter 3's constants rewritten whole for it, as the review of the design decides

**Verification:**
- [ ] `go test ./compiler/...`; `go test ./...`, `make test` and `make check`

**Dependencies:** none
**Files:** `compiler/types/`, `compiler/emit/`, `../design/00-syntax-conventions.md`, `../book/`
**Scope:** M

## Task 11.4: Default field values

**Description:** Decided 2026-11-08 in place of parameter defaults and overloading. A struct field may declare a default, a constant expression: `struct ListenOptions { port int = 8080; backlog int = 128 }`. A literal that leaves a field out takes its default; a field with none takes its zero value, or is refused when its type has none, the rule of task 11.2. A function takes its settings as such a struct, every setting named, in any order, any of them skipped. A trait field keeps no default. Inferring the literal's type from the parameter, `f({port: 9000})`, is left for later.

**Acceptance criteria:**
- [ ] The design recorded and reviewed before the code, in the syntax note
- [ ] Defaults through both backends, in literals and positional literals alike; a default that is not constant, or of the wrong type, refused with its message
- [ ] One library function whose settings suit a struct uses one, chosen in the design review
- [ ] A chapter of its own in the book; chapter 4's structs and appendix B point at it

**Verification:**
- [ ] `go test ./compiler/... ./std ./examples`; `go test ./...`, `make test` and `make check`

**Dependencies:** 11.2, 11.3
**Files:** `compiler/parser/`, `compiler/ast/`, `compiler/types/`, `compiler/emit/`, `../design/00-syntax-conventions.md`, `../book/`
**Scope:** M

## Task 11.5: Variadic functions

**Description:** A program's own variadic parameter is "not supported yet", while the library's `...ref any` is. `func Sum(xs ...int)` receives an owned `[]int`, the arguments moved into it; `func Show(xs ...ref T)` receives a slice of borrows, each argument lent for the call alone; `Sum(s...)` passes a slice, moved or lent as the parameter says.

**Acceptance criteria:**
- [ ] The design recorded and reviewed before the code, in the syntax note
- [ ] Both forms through both backends, generic and not, with a slice passed on; the arguments' ownership clean in `-debug`; a use after a move into the slice refused
- [ ] A chapter of its own in the book, or the functions of chapter 3 rewritten for it, as the review decides

**Verification:**
- [ ] `go test ./compiler/...`; `go test ./...`, `make test` and `make check`

**Dependencies:** none
**Files:** `compiler/parser/`, `compiler/types/`, `compiler/emit/`, `../design/00-syntax-conventions.md`, `../book/`
**Scope:** M

## Task 11.6: Reflection at compile time

**Description:** Decided 2026-11-08: a standard trait the compiler derives for a type that lists it, as it derives `Eq`, `Hash` and `%v`, in the shape of Swift's `Codable`, and never reflection at run time. One method hands each field to a visitor by name, as a `ref`, the variant's name first for an enum; the other builds the value from a source by asking for each field by name, and returns a `Result`, a missing field an error. The visitor receives shared borrows only, the value is built by a literal that names every field, and nothing is kept at run time. The trait's name, the visitor's and the source's traits, and how a field is renamed, as a Go struct tag does, are decided in the design.

**Acceptance criteria:**
- [ ] The design recorded and reviewed before the code, a design note of its own, with the argument for memory safety and against any metadata at run time
- [ ] The trait derived for structs, enums, and the types inside them through both backends; a type with a field that does not implement it refused with a message naming the field
- [ ] A JSON encoder and decoder written over it, as a test program or under `lib/`, as the review decides, round-tripping example 11's values with no code written per type
- [ ] A chapter of its own in the book; the testing note's line on mocks corrected

**Verification:**
- [ ] `go test ./compiler/... ./std ./examples`; `go test ./...`, `make test` and `make check`

**Dependencies:** 11.2
**Files:** `compiler/types/`, `compiler/emit/`, `std/builtin/`, a new design note, `../design/09-testing.md`, `../book/`
**Scope:** L

## Task 11.7: A generic trait as a type

**Description:** `Iterator<int>` as a type when every type argument is given, a trait value as `Shape` is today: one owner, a box and a method table, freed by its `Drop`. `[]Iterator<int>` holds iterators of different types. Chapter 11's two limits, the type and the inference of a bound's arguments from the implementing type, are lifted or kept as the design decides.

**Acceptance criteria:**
- [ ] The design recorded and reviewed before the code, in the syntax note
- [ ] A slice of iterators of different types ranged through both backends, clean in `-debug`; a generic method called through it refused as it is through a trait value today
- [ ] Chapter 11's section rewritten

**Verification:**
- [ ] `go test ./compiler/...`; `go test ./...`, `make test` and `make check`

**Dependencies:** none
**Files:** `compiler/types/`, `compiler/emit/`, `../design/00-syntax-conventions.md`, `../book/11-traits.md`
**Scope:** M

## Task 11.8: A stack trace on a panic

**Description:** A panic prints its message and nothing of where it came from. The panicking task's stack, one line per frame with the function and its file and line, from a table the compiler writes of each function's name and lines, walked by the runtime over the frame pointers before the unwinding runs the drops. The walk allocates nothing, and a panic inside a test shows the trace under the failure.

**Acceptance criteria:**
- [ ] The design recorded and reviewed before the code
- [ ] A panic three calls deep prints the three frames with their files and lines through both backends, in release and debug builds; the drops still run
- [ ] A chapter of its own in the book, or chapter 17's panics extended, as the review decides

**Verification:**
- [ ] `go test ./compiler/emit/... ./runtime ./examples`; `go test ./...`, `make test` and `make check`

**Dependencies:** none
**Files:** `compiler/emit/`, `runtime/c/`, `../book/`
**Scope:** M

## Task 11.9: Debugger information

**Description:** The LLVM backend emits DWARF line tables and the locations of variables in a `-debug` build, and the C backend writes `#line` directives, so lldb and gdb stop at a Snello line and print a variable, and `perf` and Instruments name Snello functions and lines.

**Acceptance criteria:**
- [ ] The design recorded and reviewed before the code
- [ ] A test that runs lldb, or gdb on Linux, in batch mode on a program: a breakpoint at a line, a step, and a variable printed, through both backends
- [ ] A chapter of its own in the book, on debugging and profiling

**Verification:**
- [ ] `go test ./compiler/emit/...`; `go test ./...`, `make test` and `make check`

**Dependencies:** none
**Files:** `compiler/emit/llvm/`, `compiler/emit/c/`, `../book/`
**Scope:** M

## Task 11.10: Code for one platform

**Description:** Go's file names: `poll_linux.snello` and `poll_darwin_arm64.snello` are built only for their target, and no attribute inside a file chooses. The runtime's own platform code is unaffected; this is for packages, the library's and a program's, and a library binding C that differs between systems. Windows, which stage 10 decides, needs it.

**Acceptance criteria:**
- [ ] The design recorded and reviewed before the code, in the packages note
- [ ] A package with a file per system builds the right one on Linux and macOS; a name with an unknown system or architecture refused
- [ ] Chapter 15, binding C, and the packages chapter say so

**Verification:**
- [ ] `go test ./compiler/importer ./compiler/...`; `go test ./...`, `make test` and `make check`

**Dependencies:** none
**Files:** `compiler/importer/`, `../design/08-packages.md`, `../book/`
**Scope:** S

## Task 11.11: Building for another platform

**Description:** `snello build -target linux/arm64`: LLVM emits for the target, the runtime is compiled for it with the C compiler's own cross target, and the files of task 11.10 are chosen by it. The four platforms the runtime supports are the targets.

**Acceptance criteria:**
- [ ] The design recorded and reviewed before the code
- [ ] A program built on macOS for Linux on amd64 and on arm64 runs in the Linux image; built on Linux for the other Linux architecture runs under emulation or on CI's runner; an unknown target refused
- [ ] The getting-started chapter says how

**Verification:**
- [ ] `go test ./cmd/snello ./compiler/emit/...`; `go test ./...`, `make test` and `make check`

**Dependencies:** 11.10
**Files:** `cmd/snello/`, `compiler/emit/`, `runtime/`, `../book/02-getting-started.md`
**Scope:** M

## Task 11.12: Dependencies by version

**Description:** `require NAME URL VERSION` beside the local `require NAME DIR` of today: a module fetched from a git repository at a tag, into a cache under the user's directory, its hash recorded in a lock file beside `snello.mod` and checked on every build, so a dependency cannot change under a program. A registry comes later, if ever.

**Acceptance criteria:**
- [ ] The design recorded and reviewed before the code, in the packages note: the cache, the lock file's form, what a changed hash does, and building offline from the cache
- [ ] A program requiring a module from a local git repository by tag builds, records the hash, and refuses a tag moved to another commit
- [ ] The packages chapter rewritten for it

**Verification:**
- [ ] `go test ./compiler/importer ./cmd/snello`; `go test ./...`, `make test` and `make check`

**Dependencies:** none
**Files:** `compiler/importer/`, `cmd/snello/`, `../design/08-packages.md`, `../book/`
**Scope:** L

## Task 11.13: `snello doc`

**Description:** `snello doc PKG` and `snello doc PKG.NAME` print a package's exported declarations and their comments, from the checker's view of the package, for the library and a program's own packages alike. Pages for the web later.

**Acceptance criteria:**
- [ ] The design recorded and reviewed before the code
- [ ] `snello doc strings`, `snello doc strings.Builder` and a program's package print what is exported, with its comment, and nothing that is not
- [ ] The getting-started chapter and chapter 14 say so

**Verification:**
- [ ] `go test ./cmd/snello`; `go test ./...`, `make test` and `make check`

**Dependencies:** none
**Files:** `cmd/snello/`, `../book/`
**Scope:** M

## Task 11.14: Running programs and catching signals

**Description:** `os/exec`: a command run as a child, its handle owned and waited on through the scheduler's poller, its standard input, output and error pipes an `io.Writer` and `io.Reader`, every descriptor closed on exec in the child. `os/signal`: the interrupt and terminate signals delivered as values on a channel. A signal handler cannot be safe Snello code and the runtime owns the threads, so this is the library's and not a library's outside. The child, its pipes and the channel are owned handles, closed by their `Drop`; what dropping a child that was not waited on does is decided in the design. May run beside task 9.11.

**Acceptance criteria:**
- [ ] The design recorded and reviewed before the code, in the library note
- [ ] A program runs a child, writes to its input, reads its output and its exit status, through both backends, while another task runs; a program stopped by an interrupt runs its drops and exits
- [ ] A chapter of its own in the book, or chapter 14 extended, as the review decides

**Verification:**
- [ ] `go test ./std ./runtime ./examples`; `go test ./...`, `make test` and `make check`

**Dependencies:** none
**Files:** `std/os/exec/`, `std/os/signal/`, `runtime/c/`, `../design/05-standard-library.md`, `../book/`
**Scope:** L
