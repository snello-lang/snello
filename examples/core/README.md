# Core programs

The core acceptance suite: eight programs written in core Snello, the part of the language without traits or user generics, the first the checker and the C backend handled. Core Snello is structs, enums and `match`, functions and methods with `ref`, `mut` and value receivers, `*T` and `&expr`, `T?` and `Result` with `?` and `??`, slices, maps, strings, `Copy` computed from structure, `Drop`, `defer`, and closures. It has no traits and no user generics; the one `impl` that appears is `impl Drop`, which is how a type gets a `Drop` method.

Each program is the core version of one of the twenty programs in the parent folder. What was removed, and what each one stresses, is in its header comment.

| File | From | What it stresses |
|---|---|---|
| tree.snello | 01-tree/tree-slice | `ref`/`mut` receivers, a borrow of one slice element, a closure taking a borrow, cloning out before mutating |
| list.snello | 06-linked-list | `*Node?` links, `take`, a partial move out of a heap node, a borrow returned from a `ref` receiver |
| state.snello | 17-state-machine | Enums with data, exhaustive tuple match with guards, consuming transitions, `Drop` on a dropped state |
| files.snello | 14-files | `Drop` versus `defer`, a consuming `Close`, `?` on library calls, a user `Drop` type |
| calc.snello | 05-parser | Sum types, owned pointers for recursion, a Copy enum, a struct holding a borrow |
| bst.snello | 20-bst | A `mut` borrow walked down a chain of `*Node?` slots, in-place delete through `take` |
| wordfreq.snello | 10-wordfreq | Owned string versus borrows of its pieces, owned map keys, consuming a map, sorting through `mut` |
| config.snello | 09-errors | `?` across Results, `??` whose else returns, a move on the diverging path only, a borrow of a map value |

## Library surface

The programs call only these library names, so that they ran before the library was written in Snello, when the emitter mapped each onto a C runtime function of the same meaning.

- Predeclared, from section 1 of `../../_docs/design/05-standard-library.md`: `len`, `make`, `take`, the `Expect` method on options and results, and the methods `Push`, `Clone`, `RemoveValue`, `Reverse`, `SortBy` on slices, `Get`, `Insert` on maps, `Fields`, `Split`, `SplitOnce`, `Trim`, `ToLower`, `Clone` on strings.
- `fmt`: `Println`, `Sprintf`, `Sprint`.
- `os`: `ReadToString`, `Open`, `Create`, `MkdirTemp`, `RemoveAll`, `Exit`, and the `Close` method on `os.File`.
- `io`: `Copy`.

## Expected output

Every `main` says in its comments what it prints. When a program builds, that text becomes a `.out` file beside it, and `go test ./compiler/emit/c/` builds the program and compares its output with the file; all eight have theirs. A program that reads files, such as `config.snello` reading `app.conf`, has them in a folder named after it with the suffix `.files`, which the test copies into the directory the binary runs in. `state.out` shows every `Drop` line exactly once.

## Negative programs

`errors/` holds the programs the checker must reject, one rule each, with the expected message on the offending line in a `// ERROR "..."` comment.
