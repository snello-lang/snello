# The Snello Programming Language

The book: how to write Snello. It describes the language on its own terms, one feature at a time, and every code sample is cut from a program in `../../examples/`, so the book and the compiler's acceptance suite cannot drift apart. Readers coming from Go have Appendix B, which says per chapter what carries over and what to unlearn.

The book is written as the language is built: a chapter exists as soon as its feature is decided, and says plainly what the compiler does not do yet. The reasoning behind the decisions is not here; it is in `../design/`.

## Contents

| Chapter | What it covers |
|---|---|
| [1. Introduction](01-introduction.md) | what Snello is, and core Snello as `snello build` compiles it |
| [2. Getting started](02-getting-started.md) | `snello build`, `-debug` and the two backends; packages of the program's own through `snello.mod`; tests with `snello test` |
| [3. Basics](03-basics.md) | variables, constants, the number types, functions, control flow, `match` on conditions |
| [4. Structs, enums and match](04-structs-enums-match.md) | structs and the positional literal; enums, their methods, traits, derived order, printing and type parameters; patterns |
| [5. Ownership: moves, `Copy`, `take`, `Drop`](05-ownership.md) | moves, `Copy` checked against the fields, a moved variable given back by assignment, drops at scope exit, return and panic, and what no collector buys, measured by `bench/memory.snello` |
| [6. Borrows: `ref` and `mut`](06-borrows.md) | the six rules; how long a borrow lives, along a function's paths: blocks, loops, fields, closures, `mut` parameters, deferred calls and `Drop`; closures that copy a `Copy` value they only read |
| [7. Owned pointers and recursive data](07-owned-pointers.md) | `*T`, `&expr` and recursive types |
| [8. Option, Result, `?` and `??`](08-option-result.md) | `T?`, `Result`, `Map` and `MapErr`, and `?` passing an error into the variant that holds its type or into a trait value |
| [9. Collections: slices, maps and strings](09-collections.md) | slices, maps with keys that are `Eq` and `Hash`, and strings |
| [10. Concurrency: `spawn`, channels, `select`](10-concurrency.md) | the rule of the task boundary, `Send` inferred from structure, `spawn`, channels, `close`, `for range ch` and `select`, `sync`'s handles and guards, `time.After`, package variables, and the races the checker rejects |
| [11. Traits](11-traits.md) | declarations, `impl`, refinement and bounds; default methods, the resolution order, trait fields and `super`; a trait as a type; the standard and operator traits and derivation; generic traits |
| [12. Generics](12-generics.md) | generic types and functions, inference, one copy per instance, and bounds |
| [13. Arenas and indices](13-arenas-and-indices.md) | `arena.Arena<T>` and generation-checked `arena.Index<T>` |
| [14. The standard library](14-standard-library.md) | the packages under `std/`, `fmt` and its checked formats, and what stays out of the library |
| [15. Binding C](15-binding-c.md) | a function without a body defined in C beside the package, what crosses, a handle wrapped with `Drop`, and `link` in the module file |
| [16. Testing](16-testing.md) | test files, tests as plain functions, `expect` and `testing`, `snello test` and its flags, subtests, parallel tests, benchmarks and golden files, and the library's own tests |
| [17. Errors](17-errors.md) | the `Error` trait with `Error()` and `Source()`, derived messages, `?` across error types, `%+v` printing the chain |
| [18. No empty values](18-no-empty-values.md) | why no value is ever null or empty, which types have a zero value, what the compiler refuses and what to write instead, `replace`, and how the rule stays memory safe |
| [19. Text: strings, runes and Unicode](19-text.md) | runes and Unicode whole: `unicode`, `unicode/utf8`, `unicode/utf16`, case mapping, `unicode/norm` and `unicode/segment` |
| [Appendix A. Keywords and grammar notes](A-keywords-and-grammar.md) | the keywords and the grammar's fine points |
| [Appendix B. Snello for Go programmers](B-snello-for-go-programmers.md) | per chapter, what carries over from Go and what to unlearn |

## How the book is kept current

- A change that implements or decides a language feature updates the chapter that owns the feature in the same change, as it carries its tests.
- A chapter describes what the compiler does today; what it does not do yet is said plainly where it matters.
- Samples come from `../../examples/`. When an example changes, the chapter quoting it changes with it.
- The chapters name no other language. Snello is explained by what it is and what it is for. Comparisons for readers coming from Go live in Appendix B, and a chapter that gains a feature adds its row there.
