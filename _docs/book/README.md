# The Teak Programming Language

The book: how to write Teak, for programmers who know Go. It explains each feature by what it does and how it differs from Go, and every code sample is cut from a program in `../../examples/`, so the book and the compiler's acceptance suite cannot drift apart.

The book is written as the language is built. A chapter exists as soon as its feature is decided, and its status line says how much of it the compiler enforces today. The reasoning behind the decisions is not here; it is in `../design/`.

## Contents

| Chapter | Status |
|---|---|
| 1. Introduction | not written |
| 2. Getting started | waits for `teak build` (stage 1, task 1.5) |
| [3. Basics](03-basics.md) | syntax implemented (stage 0); checked from stage 1 |
| [4. Structs, enums and match](04-structs-enums-match.md) | syntax implemented (stage 0); checked from stage 1 |
| 5. Ownership: moves, `Copy`, `take`, `Drop` | written with stage 1, tasks 1.6 and 1.9 |
| 6. Borrows: `ref` and `mut` | written with stage 1, tasks 1.7 and 1.8 |
| 7. Owned pointers and recursive data | written with stage 1 |
| 8. Option, Result, `?` and `??` | written with stage 1, task 1.4 |
| 9. Collections: slices, maps and strings | written with stage 1 |
| 10. Concurrency: `spawn`, channels, `select` | written with stage 3 |
| 11. Traits | written with stage 2 |
| 12. Generics | written with stage 2 |
| 13. Arenas and indices | written with stage 2, task 2.6 |
| 14. The standard library | written with stage 2, task 2.6 |
| Appendix A. Keywords and grammar notes | written with stage 1 |

## How the book is kept current

- A slice that implements or decides a language feature updates the chapter that owns the feature in the same change, as it carries its tests. Each stage's checkpoint has a line for this.
- Every chapter starts with a status line: what is decided, and through which stage the compiler enforces it. The line changes when the compiler catches up.
- Samples come from `../../examples/`. When an example changes, the chapter quoting it changes with it.
- Go is the reference point. A feature is explained by what a Go developer would write, then by what changes. Rust or Scala vocabulary does not appear; see `../../AGENTS.md`.
