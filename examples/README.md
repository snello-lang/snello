# Examples

Programs written in Snello, one folder each, with what the program prints beside it in `name.out`. Start with `hello/`, then read whichever shows what you came for. `tree/` and `pipeline/` are written in Go as well, to put the two side by side.

To build and run one:

```
go build -o snello ./cmd/snello
./snello build examples/hello/hello.snello
./hello
```

The binary is written to the current directory, named after the file, or after the folder for a program of several packages.

| Folder | What it shows |
|---|---|
| hello/ | The smallest program: one line printed |
| tree/ | Parent pointers three ways: Go pointers, indices into a slice, an `Arena` |
| pipeline/ | Channels and ownership crossing tasks, Go beside Snello |
| cache-server/ | Long-lived shared state, a borrow per request, `Drop` for a connection; a server over `net` |
| graph/ | A graph in an arena, breadth-first search, a heap with an `Ord` bound |
| parser/ | Sum types, `match`, owned pointers for recursion, a struct holding a borrow |
| linked-list/ | `*Node?` links, `take`, partial moves, a borrow returned from a `ref` receiver |
| traits/ | `impl`, default methods, trait fields, `super`, a trait as a type |
| generics/ | Bounds, consuming against borrowing in generic functions, a conditional `impl` |
| errors/ | `Result`, an error enum of one's own, `?` and `??` |
| wordfreq/ | An owned string against borrowed pieces of it, maps, sorting |
| text/ | The everyday methods of strings and slices: searches that give an option, trims that lend, `Replace`, `SplitN`, `GetOrInsert`, `Extend` and `Truncate` |
| numbers/ | `math`, `math/bits` and `math/rand`: a throw followed with the math functions, the limits of the number types, a board kept in the bits of a `u64`, and a deck shuffled by a generator with a seed, which deals the same hand on every run |
| records/ | `bytes`, `io` and `bufio` for a file format of the program's own: records encoded into a `bytes.Buffer` and decoded from any reader with `io.ReadFull`, a cut record reported as an error, `Seek` back to a record, a header looked at with `Peek`, `ReadBytes`, `UnreadByte`, a word `Scanner`, `io.ReadAll` and `io.Discard` |
| json/ | A JSON value type, encoder and decoder written in the example itself, over `strings` and `strconv`: a recursive enum holding collections, a decoder holding a borrow of its input |
| worker-pool/ | `select`, timeouts, a cancellation token |
| actor/ | State a task owns, message passing, reply channels |
| files/ | `Drop` against `defer`, a consuming `Close` |
| lru/ | A map into a linked list kept in an arena; two `mut` paths into one slice refused |
| matrix/ | `Copy` types, operator traits, no hidden allocation |
| state-machine/ | Enum states, consuming transitions, a connection closed where its state is dropped |
| iterators/ | Closure capture modes, a lazy iterator trait |
| cli-grep/ | Arguments, standard input and files, a borrow per line, exit codes; waits for `regexp`, so it has no `.out` yet |
| bst/ | A `mut` borrow walked down a chain of boxes, delete in place |
| packaged/ | A program of two files with a package of its own beside it, and the package's tests: `snello test examples/packaged/shapes` |
| bound/ | A program with two packages beside it that bind C: `snello build examples/bound` |

A program that reads files has them in `name.files/` beside it, copied beside the binary before it runs. The cache server serves rather than prints, so `cache-server.script` beside it holds what the suite sends and expects back, a section per connection.

## The suite

`go test ./examples` builds every program here that has its `.out`, through both backends, as a release and a debug build, and compares what it prints; a program without one must be named as pending in `suite_test.go`, with what it waits for. The two programs of several packages, the folders with a `snello.mod`, are built by the backends' tests.

The programs that test one feature at a time are in `compiler/emit/internal/backendtest/testdata/`, and the programs the checker must refuse, one per rule with its message, are in `compiler/types/testdata/errors/`.
