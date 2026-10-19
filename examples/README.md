# Example programs

Twenty programs written in Teak before Teak exists. They are the design's test suite: every rule in `../_docs/design/00-syntax-conventions.md` must let each of these read naturally, and every stage of the compiler must run them all. Numbers do not mean difficulty; the first five are the ones most likely to break the memory model.

| # | File | What it stresses |
|---|---|---|
| 01 | 01-tree/ | Parent pointers, three ways: Go pointers, hand-rolled slice, Arena<T> |
| 02 | 02-pipeline/ | Channels and ownership crossing tasks, Go beside Teak |
| 03 | http-cache.teak | Long-lived shared state, per-request borrows, Drop for connections |
| 04 | graph.teak | Arena graph, BFS, heap with Ord bound |
| 05 | parser.teak | Sum types, match, owned pointers for recursion, a borrow-holding struct |
| 06 | linked-list.teak | `*Node?` links, take, partial moves, returning a ref from a ref receiver |
| 07 | traits.teak | impl, default methods, trait fields, super, trait as a type |
| 08 | generics.teak | Bounds, consume vs borrow in generic functions, conditional impl |
| 09 | errors.teak | Result, custom error enums, ?, ?? |
| 10 | wordfreq.teak | Owned string vs borrowed slice, maps, sorting |
| 11 | json.teak | Recursive enum with collections, encoder/decoder |
| 12 | worker-pool.teak | select, timeouts, cancellation token |
| 13 | actor.teak | Task-owned state, message passing, reply channels |
| 14 | files.teak | Drop vs defer, consuming Close |
| 15 | lru.teak | Map into an Arena-backed linked list; the two-slots aliasing rule |
| 16 | matrix.teak | Copy types, operator traits, no hidden allocation |
| 17 | state-machine.teak | Enum states, consuming transitions |
| 18 | iterators.teak | Closure capture modes, lazy iterator trait |
| 19 | cli-grep.teak | Args, stdin/files, per-line borrows, exit codes |
| 20 | bst.teak | Reborrowing down a chain of boxes, in-place delete |

## Decisions these forced

Writing the programs surfaced seven questions. All are now decided and recorded in `../_docs/design/00-syntax-conventions.md`; the examples follow them.

| Question | Decision | Where |
|---|---|---|
| A struct holding a borrow (a lexer) | Allowed; such a struct is itself a borrow | 05, 11 |
| Returning a borrow from a method | Allowed into the receiver only | 06, 15, 20 |
| Reborrowing down a chain of owned pointers | Allowed; a borrow may be replaced by one derived from itself | 20 |
| Two `mut` paths into one slice | Rejected; copy values out first | 15 |
| Moving an element out of a slice | Not by indexing; use `Remove`, `Pop`, or `take` | 19 |
| Channel receive on a closed channel | `<-ch` yields `T?`; `range ch` yields `T` | 12 |
| The `??` operator | Kept; the else may `return`, `break`, or `continue` | 06, 09, 20 |

The owned heap pointer is spelled `*T` and created with `&expr`, as in Go; there is no `Box<T>` (05, 06, 20).

## The suite

`suite_test.go` builds and runs every program here that has its expected output under `expected/`, as a release build and as a debug build, and names the rest as pending: `go test ./examples`. The stage specs under `../_docs/specs/` say what each pending program waits for.

## The core set

`core/` holds eight of these programs rewritten in core Teak, without traits or user generics, as the acceptance suite for stage 1. See `core/README.md`.
