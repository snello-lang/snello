# Programs that build and run

Every program in this folder goes through `teak build` today and runs as a native binary. The file beside each program, `name.out`, is exactly what it prints. This is the playground for the backend: as each slice of the C emitter lands, the programs it makes buildable are added here, so there is always something real to compile and run.

To try one:

```
go build -o teak ./cmd/teak
./teak build examples/run/fib.teak
./fib
```

The binary is written to the current directory, named after the file. `go test ./emit/c/` builds every program here and compares its output with the `.out` file; a program without one fails the test.

| File | What it exercises |
|---|---|
| hello.teak | One `fmt.Println` |
| fib.teak | Recursion, integer and float arithmetic, sized integers, conversions |
| strings.teak | A `ref string` parameter, concatenation, comparison, byte indexing, `Sprintf`, `Printf`, `Sprint` |
| control.teak | `if`/`else if`, the three forms of `for`, `continue` and `break`, a named number type, swap by parallel assignment |
| corners.teak | What the lowering to C must get right: literal bases, wrapping overflow, trigraph-looking strings, float printing |
| shapes.teak | Structs and enums: literals, `ref` and `mut` receivers, `match` with bindings, a guard and an or-pattern, a payload changed in place, printing of composite values |
| options.teak | `Some`/`None`, `Ok`/`Err`, `?`, `??` with a value, a `panic` and a `return`, `if` with a binding, `if` as an expression, and the order of evaluation when an operand needs statements |
| boxes.teak | `&x` moving a value to the heap, a recursive type through `*Node?`, a pointer lent as what it holds, tuples built, destructured, matched and printed, `()` in a Result |

The programs under `../core/` are the stage 1 acceptance suite. `calc.teak` builds and runs; the others need the third slice (slices, maps, string methods, closures, `defer`, files) or the drops of task 1.9. When one of them builds, its expected output moves from its comments into a `.out` file beside it, and the same test picks it up.
