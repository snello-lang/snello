# The Teak Programming Language

The book: how to write Teak. It describes the language on its own terms, one feature at a time, and every code sample is cut from a program in `../../examples/`, so the book and the compiler's acceptance suite cannot drift apart. Readers coming from Go have Appendix B, which says per chapter what carries over and what to unlearn.

The book is written as the language is built. A chapter exists as soon as its feature is decided, and its status line says how much of it the compiler enforces today. The reasoning behind the decisions is not here; it is in `../design/`.

## Contents

| Chapter | Status |
|---|---|
| [1. Introduction](01-introduction.md) | written at the stage 1 checkpoint; describes core Teak as `teak build` compiles it |
| [2. Getting started](02-getting-started.md) | `teak build` compiles all of core Teak, `teak build -debug` checks the drops (stage 1, tasks 1.5 and 1.9) |
| [3. Basics](03-basics.md) | implemented, checked and compiled (stage 1, task 1.5) |
| [4. Structs, enums and match](04-structs-enums-match.md) | implemented, checked and compiled (stage 1, task 1.5); the positional struct literal (stage 2, task 2.7) |
| [5. Ownership: moves, `Copy`, `take`, `Drop`](05-ownership.md) | moves checked (stage 1, task 1.6); drops at scope exit, return and panic, checked by `teak build -debug` (stage 1, task 1.9); `impl Copy` checked against the fields, and a moved variable given back by assignment on every path (stage 2, task 2.5) |
| [6. Borrows: `ref` and `mut`](06-borrows.md) | decided; compiled (stage 1, task 1.5); all six rules checked (stage 1, tasks 1.7 and 1.8); a closure copies a `Copy` value it only reads and may then be returned (stage 2, task 2.7) |
| [7. Owned pointers and recursive data](07-owned-pointers.md) | implemented, checked, compiled and freed (stage 1, tasks 1.5 and 1.9) |
| [8. Option, Result, `?` and `??`](08-option-result.md) | implemented, checked and compiled (stage 1, task 1.5); `Map` and `MapErr` (stage 2, task 2.6) |
| [9. Collections: slices, maps and strings](09-collections.md) | implemented, checked and compiled (stage 1, task 1.5); a map key must be `Eq` and `Hash` (stage 2, task 2.5) |
| 10. Concurrency: `spawn`, channels, `select` | written with stage 3 |
| [11. Traits](11-traits.md) | declarations, `impl`, refinement and bounds checked and compiled (stage 2, task 2.2); default methods, the resolution order, trait fields and `super` (task 2.3); a trait as a type checked and compiled (task 2.4); the standard traits' methods, derivation from the fields and the map key rule checked (task 2.5, first slice); the operator traits with their argument, checked and compiled (second slice); the derived methods and the predeclared types' own compiled (third slice, task 2.5 complete); generic traits and default methods with type parameters of their own (task 2.7) |
| [12. Generics](12-generics.md) | generic types and functions checked, inferred and compiled one copy per instance (stage 2, task 2.1); bounds are traits (task 2.2) |
| [13. Arenas and indices](13-arenas-and-indices.md) | `arena.Arena<T>` and `arena.Index<T>` written in Teak under `std/arena/`, with generation-checked indices (stage 2, task 2.6, seventh slice); a range over an arena waits for the iterator trait |
| [14. The standard library](14-standard-library.md) | packages written in Teak under `std/`, imported and compiled with the program: `math`, `io`, `os`, `strings`, `strconv`, `sort`, `time`, `arena`, `rc` and `bufio` (stage 2, task 2.6, nine slices); every package is declared under `std/`, `fmt` and `os.Exit` without bodies, since the compiler writes them |
| [Appendix A. Keywords and grammar notes](A-keywords-and-grammar.md) | implemented (stage 0) |
| [Appendix B. Teak for Go programmers](B-teak-for-go-programmers.md) | covers chapters 3 to 9, 11, 12 and 14; grows with them |

## How the book is kept current

- A slice that implements or decides a language feature updates the chapter that owns the feature in the same change, as it carries its tests. Each stage's checkpoint has a line for this.
- Every chapter starts with a status line: what is decided, and through which stage the compiler enforces it. The line changes when the compiler catches up.
- Samples come from `../../examples/`. When an example changes, the chapter quoting it changes with it.
- The chapters name no other language. Teak is explained by what it is and what it is for. Comparisons for readers coming from Go live in Appendix B, and a chapter that gains a feature adds its row there.
