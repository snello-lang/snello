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
| [8. Option, Result, `?` and `??`](08-option-result.md) | `T?`, `Result`, `Map`, `MapErr`, `AndThen`, `OrElse`, `Ok`, `Err` and `UnwrapOrElse`, and `?` passing an error into the variant that holds its type or into a trait value |
| [9. Collections: slices, maps and strings](09-collections.md) | slices, maps with keys that are `Eq` and `Hash`, and strings, with their methods: searches that give `int?`, trims that lend, `Replace`, `SplitN`, `Extend`, `Truncate`, `GetOrInsert`, and the bound each asks of the element |
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
| [20. Numbers: `math`, `math/bits` and `math/rand`](20-numbers.md) | the math functions and their special cases, the limits of the number types, the bit operations the compiler writes as instructions, a generator with a seed that gives Go's numbers, and the package functions that draw from the system |
| [21. Constants](21-constants.md) | what a constant is made of, its type, how the compiler computes it and what it refuses, constants in any order, array lengths |
| [22. Variadic functions](22-variadic-functions.md) | `...T`, which owns its arguments, `...ref T`, which borrows them for the call, `f(xs...)` passing a slice on, and what each costs |
| [23. Default field values](23-default-field-values.md) | a field's default, a constant; literals that leave fields out, positional ones included; why such a struct has no zero value; settings passed as a struct, `os.OpenOptions` |
| [24. Reflection at compile time](24-reflection.md) | why a function looks at the type it was given, `match T`, a struct's fields with `fields(T)` and `v.[f]`, building a value from its fields, an enum's variants, what reflection may reach and `Reflect`, `encoding/json` |
| [25. Attributes](25-attributes.md) | why a field, a variant or a type carries a typed constant, `@` and the `Attribute` trait, where one may be written, `FieldAttribute`, `VariantAttribute` and `TypeAttribute`, reading one at compile time with `attr`, a library's own attributes beside another's, what is refused and why |
| [26. Functions of a type](26-functions-of-a-type.md) | why a type has functions that take no value of it, `func Point.Of(...)`, on a generic type, with parameters of their own, as values, and required by a trait, `T.Parse(s)` |
| [27. Writing Snello](27-writing-snello.md) | the one way Snello code is written: a file top to bottom, a type's exported methods before its helpers, names that say what a thing does, named constants and `match` on conditions where an arm would compare with one, comments that state the rule, errors as values, what a signature says of ownership, and the layout |
| [Appendix A. Keywords and grammar notes](A-keywords-and-grammar.md) | the keywords and the grammar's fine points |
| [Appendix B. Snello for Go programmers](B-snello-for-go-programmers.md) | per chapter, what carries over from Go and what to unlearn |

## How the book is kept current

- A change that implements or decides a language feature updates the chapter that owns the feature in the same change, as it carries its tests.
- A chapter describes what the compiler does today; what it does not do yet is said plainly where it matters.
- Samples come from `../../examples/`. When an example changes, the chapter quoting it changes with it.
- The chapters name no other language. Snello is explained by what it is and what it is for. Comparisons for readers coming from Go live in Appendix B, and a chapter that gains a feature adds its row there.
