# Implementation plan

This folder is the traceable record of the compiler's construction. `plan.md` holds the decisions, the order, the risks, and an index of every task. One file per stage holds the tasks themselves with checkboxes, acceptance criteria, and verification steps. A task is checked off only when its verification steps have been run and passed; the date goes next to the box.

The stages come from `../design/01-language-design.md`. The twenty programs under `../../examples/` are the acceptance suite for every stage.

## Overview

Build the Teak compiler in Go, one stage at a time, each stage leaving a working tool that processes all twenty example programs. Stages 0 to 3 emit Go source so the type system can be validated before a runtime exists. Stage 4 replaces the backend with LLVM and a native runtime. Stage 5 extends the native backend to accelerators.

## Architecture decisions

- **Packages mirror the Go toolchain.** `token`, `scanner`, `ast`, `parser`, `types`, then backends under `emit/`. When unsure how to shape something, read the Go counterpart.
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
| 0 | [stage-0-lexer-parser.md](stage-0-lexer-parser.md) | 0.1 to 0.11 | all tasks done 2026-10-18; checkpoint review by the owner pending |
| 1 | [stage-1-core-checker.md](stage-1-core-checker.md) | 1.0 to 1.10 | 1.0 done 2026-10-18 (standard library design, added after noticing the examples use library names no document defines; owner's review pending); 1.1 next |
| 2 | [stage-2-traits-generics.md](stage-2-traits-generics.md) | 2.1 to 2.7 | not started |
| 3 | [stage-3-send.md](stage-3-send.md) | 3.1 to 3.4 | not started |
| 4 | [stage-4-native.md](stage-4-native.md) | 4.1 to 4.7 | not started |
| 5 | [stage-5-accelerators.md](stage-5-accelerators.md) | 5.1 to 5.3 | not started |

## Checkpoints

A checkpoint is a human review before the next stage begins. Each stage file ends with its checkpoint list.

| After | What is reviewed |
|---|---|
| Stage 0 | All twenty examples parse; the AST printer output reads correctly; the grammar decisions the parser made are recorded in the syntax conventions; the standard library design (task 1.0) is written and decided before any checker code |
| Stage 1 | The core examples type-check and run as Go; the negative tests are rejected with the planned messages; **decision point: does the ownership model hold, or does the design change?** |
| Stage 2 | All twenty examples compile and run with their expected output |
| Stage 3 | The concurrency examples compile; the race programs are rejected |
| Stage 4 | All twenty run natively; benchmark numbers compared with the performance table in the design document |
| Stage 5 | One kernel runs on a GPU |

## Risks

| Risk | Impact | Mitigation |
|---|---|---|
| The ownership rules reject a program the design says is legal | High: the design changes | Stage 1 runs before traits exist and has its own decision checkpoint; the examples were written to surface exactly this |
| Borrow checking within a function is larger than estimated (reborrows, conditional moves, loops) | High: stage 1 slips | Tasks 1.6 to 1.8 are separate so progress is visible; a dataflow pass is planned from the start, not retrofitted |
| `<` as both less-than and generic bracket is ambiguous to parse | Medium | Task 0.6 decides the rule early; Go's own parser faced the same with `[` and the examples give the cases |
| Monomorphization makes compile times unacceptable | Medium | Measured at the stage 2 checkpoint; dictionary passing is the planned relief |
| The Go backend's garbage collector hides a checker bug | High: a wrong checker ships | Task 1.9 emits explicit releases and a debug mode that asserts nothing is reachable after release |
| Stage 4 is a second compiler's worth of work | High: year two | Each 4.x task has a runnable milestone; the Go backend stays as the reference implementation |

## Decisions on the open questions

Decided 2026-10-18.

- **Stage 1 includes enums and `match`.** Ownership cannot be tested on programs that cannot express absence; every core program needs `T?`, which is an enum, and `T?` needs `match` or `??` to be used. A temporary nil-like placeholder for one stage would cost more than keeping them. What stage 1 leaves out is everything that needs type parameters in user code: generic types, generic functions, traits. `Option` and `Result` are built in for the stage, as `map` and `chan` are built-in generics in Go.
- **Each stage's standard library is the minimum its own tests call.** Stage 0: none. Stage 1: `fmt.Println`, `os` open, create, read and exit, `strings` basics, slice methods and `take`, each a direct mapping onto the Go function of the same name, held in a table in the emitter rather than a package. Stage 2 (task 2.6): the first real library, `Option` and `Result` methods, `Arena<T>`, `Rc`, `Weak`, and the shims the twenty examples import, written in Teak wherever Teak can express it. Stage 3: `Arc`, `Mutex`, `RwLock`, `Once`, `WaitGroup`. Stage 4: the runtime replaces the Go mappings; library code written in Teak is unchanged. The rule: library code in Teak validates the language for free; library code as Go mappings is debt that stage 4 repays, so keep the second kind small.
