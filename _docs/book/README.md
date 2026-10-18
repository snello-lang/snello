# The Teak Programming Language

The book: how to write Teak. It describes the language on its own terms, one feature at a time, and every code sample is cut from a program in `../../examples/`, so the book and the compiler's acceptance suite cannot drift apart. Readers coming from Go have Appendix B, which says per chapter what carries over and what to unlearn.

The book is written as the language is built. A chapter exists as soon as its feature is decided, and its status line says how much of it the compiler enforces today. The reasoning behind the decisions is not here; it is in `../design/`.

## Contents

| Chapter | Status |
|---|---|
| 1. Introduction | not written |
| [2. Getting started](02-getting-started.md) | `teak build` compiles all of core Teak but `Drop` (stage 1, task 1.5) |
| [3. Basics](03-basics.md) | implemented, checked and compiled (stage 1, task 1.5) |
| [4. Structs, enums and match](04-structs-enums-match.md) | implemented, checked and compiled (stage 1, task 1.5) |
| [5. Ownership: moves, `Copy`, `take`, `Drop`](05-ownership.md) | moves checked (stage 1, task 1.6); drops from task 1.9 |
| [6. Borrows: `ref` and `mut`](06-borrows.md) | decided; compiled (stage 1, task 1.5); all six rules checked (stage 1, tasks 1.7 and 1.8) |
| [7. Owned pointers and recursive data](07-owned-pointers.md) | implemented, checked and compiled (stage 1, task 1.5); frees in task 1.9 |
| [8. Option, Result, `?` and `??`](08-option-result.md) | implemented, checked and compiled (stage 1, task 1.5) |
| [9. Collections: slices, maps and strings](09-collections.md) | implemented, checked and compiled (stage 1, task 1.5) |
| 10. Concurrency: `spawn`, channels, `select` | written with stage 3 |
| 11. Traits | written with stage 2 |
| 12. Generics | written with stage 2 |
| 13. Arenas and indices | written with stage 2, task 2.6 |
| 14. The standard library | written with stage 2, task 2.6 |
| [Appendix A. Keywords and grammar notes](A-keywords-and-grammar.md) | implemented (stage 0) |
| [Appendix B. Teak for Go programmers](B-teak-for-go-programmers.md) | covers chapters 3 to 9; grows with them |

## How the book is kept current

- A slice that implements or decides a language feature updates the chapter that owns the feature in the same change, as it carries its tests. Each stage's checkpoint has a line for this.
- Every chapter starts with a status line: what is decided, and through which stage the compiler enforces it. The line changes when the compiler catches up.
- Samples come from `../../examples/`. When an example changes, the chapter quoting it changes with it.
- The chapters name no other language. Teak is explained by what it is and what it is for. Comparisons for readers coming from Go live in Appendix B, and a chapter that gains a feature adds its row there.
