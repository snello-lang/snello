# Implementation plan

This folder is the traceable record of the compiler's construction. `plan.md` holds the decisions, the order, the risks, and an index of every task. One file per stage holds the tasks themselves with checkboxes, acceptance criteria, and verification steps. A task is checked off only when its verification steps have been run and passed; the date goes next to the box.

The stages come from `../design/01-language-design.md`. The twenty programs under `../../examples/` are the acceptance suite for every stage.

## Overview

Build the Teak compiler in Go, one stage at a time, each stage leaving a working tool that processes all twenty example programs. Stages 1 to 3 emit C and build it with the system C compiler over a small C runtime, so every stage produces native binaries. Stage 4 replaces the C emitter with LLVM IR and keeps the runtime. Stage 5 writes the libraries the last four examples wait for and brings the slow rows of the performance table to their targets. Stage 6 extends the native backend to accelerators.

## Architecture decisions

- **Packages mirror the Go toolchain.** `token`, `scanner`, `ast`, `parser`, `types`, then backends under `compiler/emit/`. When unsure how to shape something, read the Go counterpart.
- **Vertical slices inside each stage.** Every task ends with a command that does something visible on a real file, not with a layer that cannot be run. The parser is built construct by construct, each construct landing with its tests and a working `teak parse`.
- **The ownership checker comes before traits and generics** (stage 1 before stage 2), because ownership is the design's bet and must be tested first. Stage 1 therefore has its own reduced example set.
- **Negative tests are first-class.** Each checker stage has programs that must be rejected, annotated with the expected message, in the style of the Go compiler's errorcheck tests. The commented-out "compile error" lines in the examples are the seed.
- **Zero dependencies, standard `testing` only**, as the coding standards require.

## Definition of done, for every task

- `gofmt -l .` prints nothing; `go vet ./...` and `go test ./...` are clean.
- Every new `.go` and `.teak` file carries the license header.
- A package that processes source has a benchmark over the examples, and the task's commit records the allocations-per-operation figure if it changed.
- The design documents are updated in the same change when the task changes what they describe.
- The task's own verification steps in its stage file have been run.

## Task index

| Stage | File | Tasks | State |
|---|---|---|---|
| 0 | [stage-0-lexer-parser.md](stage-0-lexer-parser.md) | 0.1 to 0.11 | complete: all tasks done and checkpoint reviewed by the owner 2026-10-18 |
| 1 | [stage-1-core-checker.md](stage-1-core-checker.md) | 1.0 to 1.10 | complete: all tasks done and checkpoint reviewed by the owner 2026-10-18 (the backend changed from Go source to C on 2026-10-18, see the design document) |
| 2 | [stage-2-traits-generics.md](stage-2-traits-generics.md) | 2.1 to 2.7 | 2.1 done 2026-10-18: generics checked, inferred and compiled one copy per instance; 2.2 done 2026-10-19: trait declarations, impl checks and bounds; 2.3 done 2026-10-19: default methods, the resolution order, trait fields and super; 2.4 done 2026-10-19: a trait as a type, checked and compiled as a boxed value with a method table; 2.5 first slice 2026-10-19: the standard traits' methods, derivation from fields and the map key rule in the checker; 2.5 done 2026-10-19: standard traits derived and compiled, operator traits; 2.6 done 2026-10-19: the standard library written in Teak under `std/`, loaded by an importer and compiled with the program: `math`, `io`, `os`, `strings`, `strconv`, `sort`, `time`, `arena`, `rc` and `bufio`, `fmt` declared there and written by the compiler, `Option` and `Result` `Map` and `MapErr`, and `builtin` documenting the predeclared names; reviewed whole. 2.7 done 2026-10-19 as far as the stage reaches: `examples/suite_test.go` builds and runs every example with an expected output under `examples/expected/`, thirteen of the twenty, and names the seven that wait for stage 3 or a later library; on the way the examples were brought to the decided design, and the language gained `byte.IsDigit`, `string.Lines`, `min` and `max`, the positional struct literal, arrays in the backend, generic traits with default methods of their own type parameters, and closures that copy a `Copy` value in. Compile time, measured by the suite test: about 0.3 ms per example in the compiler, about 220 ms per build in `cc`, so the C compiler is the whole of the build time and monomorphization is not visible in it; no dictionary passing is needed for debug builds at this size |
| 3 | [stage-3-send.md](stage-3-send.md) | 3.1 to 3.5 | plan reviewed 2026-10-20: the runtime and emitter work became a task of its own (3.3) and the shared-state library follows it; 3.1 done 2026-10-20: `Send` inferred from structure as a predeclared trait, `rc` kept in its task by a `local` field; 3.2 done 2026-10-20: a `spawn` moves what its closure mentions, `Send` checked at every crossing, `close`, `for range ch`, with two decisions on the way, `()` as the signal in place of an anonymous struct and `break` meaning the innermost loop; 3.3 done 2026-10-20: tasks on threads and channels in the C runtime behind the interface of `../design/06-tasks-and-channels.md`, a spawned function, method or closure, `select`; examples 02, 13 and 18 run; 3.4 done 2026-10-20: `sync` in Teak over numbered runtime locks, with `Arc`, `Mutex` and `RwLock` boxing their value since a `ref` is a copy in C, guards as borrow-holding structs, one lowered step from `ref` to `mut`, `time.After` and `Tick`; example 12 runs; 3.5 done 2026-10-20: the race programs of the checkpoint under `examples/core/errors/`, eleven in all, the stage's decisions recorded in the design note, chapter 10 implemented; checkpoint passed 2026-10-20, reviewed and merged by the owner: the task model holds as designed, see the decision in `../design/01-language-design.md` |
| 4 | [stage-4-native.md](stage-4-native.md) | 4.1 to 4.7 | 4.1 to 4.7 implemented 2026-10-20: the LLVM backend is the default, the C backend the reference the differential test holds it to; the scheduler and the poller run every concurrent program; the benchmark table is measured; example 03 and the three others wait for libraries; the checkpoint waits on the review |
| 5 | [stage-5-libraries-performance.md](stage-5-libraries-performance.md) | 5.1 to 5.13 | planned 2026-10-21: the allocator, the libraries of examples 03, 04, 17 and 19, benchmarks that can be trusted, strings and maps, and tasks against the targets the owner set; 5.1 done 2026-10-21: the runtime's own allocator, size classes with a free list per thread, `tree` at 1.09 times Go; 5.11 done 2026-10-21: the sixteen critical findings of the standard library review fixed, three of them holes in the checker, each with the test that would have caught it; 5.12 done 2026-10-21: the review's important findings in every package, five features deferred to designs of their own |
| 6 | [stage-6-accelerators.md](stage-6-accelerators.md) | 6.1 to 6.3 | not started |

## Checkpoints

A checkpoint is a human review before the next stage begins. Each stage file ends with its checkpoint list, and each list includes the book chapters the stage owns (`../book/README.md`): the book is written as features land, not after.

| After | What is reviewed |
|---|---|
| Stage 0 | All twenty examples parse; the AST printer output reads correctly; the grammar decisions the parser made are recorded in the syntax conventions; the standard library design (task 1.0) is written and decided before any checker code |
| Stage 1 | The core examples type-check and run as native binaries; the negative tests are rejected with the planned messages; **decision point: does the ownership model hold, or does the design change?** |
| Stage 2 | All twenty examples compile and run with their expected output |
| Stage 3 | The concurrency examples compile; the race programs are rejected |
| Stage 4 | Every example whose libraries exist runs natively, the rest named as pending; benchmark numbers compared with the performance table in the design document |
| Stage 5 | All twenty run natively; `tree` within 1.25 times Go, `words` within 1.1 times Rust, a spawn and a context switch within 2 times Go |
| Stage 6 | One kernel runs on a GPU |

## Risks

| Risk | Impact | Mitigation |
|---|---|---|
| The ownership rules reject a program the design says is legal | High: the design changes | Stage 1 runs before traits exist and has its own decision checkpoint; the examples were written to surface exactly this |
| Borrow checking within a function is larger than estimated (reborrows, conditional moves, loops) | High: stage 1 slips | Tasks 1.6 to 1.8 are separate so progress is visible; a dataflow pass is planned from the start, not retrofitted |
| `<` as both less-than and generic bracket is ambiguous to parse | Medium | Task 0.6 decides the rule early; Go's own parser faced the same with `[` and the examples give the cases |
| Monomorphization makes compile times unacceptable | Medium | Measured at the stage 2 checkpoint; dictionary passing is the planned relief |
| A missing release leaks, or a wrong one frees early | High: a wrong checker ships | Task 1.9 frees at every computed drop, a debug build poisons freed memory and asserts nothing reachable after release, and a leak checker runs over the suite |
| Stage 4 is a second compiler's worth of work | High: year two | Each 4.x task has a runnable milestone; the C backend stays as the reference implementation |

## Decisions on the open questions

Decided 2026-10-18.

- **Stage 1 includes enums and `match`.** Ownership cannot be tested on programs that cannot express absence; every core program needs `T?`, which is an enum, and `T?` needs `match` or `??` to be used. A temporary nil-like placeholder for one stage would cost more than keeping them. What stage 1 leaves out is everything that needs type parameters in user code: generic types, generic functions, traits. `Option` and `Result` are built in for the stage, as `map` and `chan` are built-in generics in Go.
- **Each stage's standard library is the minimum its own tests call.** Stage 0: none. Stage 1: `fmt.Println`, `os` open, create, read and exit, `strings` basics, slice methods and `take`, each a direct mapping onto a C runtime function of the same meaning, held in a table in the emitter rather than a package. Stage 2 (task 2.6): the first real library, `Option` and `Result` methods, `Arena<T>`, `Rc`, `Weak`, and the shims the twenty examples import, written in Teak wherever Teak can express it. Stage 3: `Arc`, `Mutex`, `RwLock`, `Once`, `WaitGroup`. Stage 4: the LLVM backend keeps the runtime; library code written in Teak is unchanged. The rule: library code in Teak validates the language for free; library code as runtime mappings is debt the Teak library repays, so keep the second kind small.
