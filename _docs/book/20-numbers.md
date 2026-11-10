# 20. Numbers: `math`, `math/bits` and `math/rand`

Three packages work on numbers: `math` has the functions and constants of floating-point arithmetic and the limits of the number types, `math/bits` counts and moves the bits of an unsigned integer, and `math/rand` draws random numbers. `examples/numbers/` runs every name in this chapter on a fixed input.

This chapter is for two readers: a developer computing with numbers, and a maintainer of the compiler, the runtime or the library who needs to know where each piece lives. The last section is the maintainer's.

No function in the three packages panics on a mistake of its caller's. Every function of `math` and `math/bits` has an answer for every argument, and a random number asked for below a bound that leaves none is `None`.

## `math`: the functions

Every function takes and returns an `f64`. A program working in another float type converts at the call, `f64(x)`, and a literal written as an argument takes the type on its own, so `math.Pow(2, 10)` needs no `2.0`.

| Functions | What they return |
|---|---|
| `Floor`, `Ceil`, `Trunc`, `Round` | the whole number below, above, toward zero, and nearest, a half going away from zero |
| `Sqrt`, `Cbrt`, `Pow`, `Hypot` | the square root, the cube root, `x` to the power `y`, and the square root of `p*p + q*q` without overflow on the way |
| `Exp`, `Log`, `Log2`, `Log10` | e to the power `x`, and the logarithm to base e, 2 and 10 |
| `Sin`, `Cos`, `Tan` | the sine, cosine and tangent of an angle in radians |
| `Asin`, `Acos`, `Atan`, `Atan2` | the angle of a sine, a cosine and a tangent; `Atan2(y, x)` is the angle of the point, in the quadrant the two signs say |
| `Sinh`, `Cosh`, `Tanh` | the hyperbolic sine, cosine and tangent |
| `Mod` | the remainder of `x` divided by `y`, with the sign of `x` |
| `Abs`, `Min`, `Max` | the absolute value, the smaller and the larger |
| `Inf`, `NaN`, `IsInf`, `IsNaN` | an infinity by its sign, a value that is not a number, and the tests for them |
| `Signbit`, `Copysign` | whether a value is negative, negative zero included, and a value with another's sign |

```go
fmt.Println(math.Floor(-2.5), math.Ceil(-2.5), math.Trunc(-2.5), math.Round(-2.5))   // -3 -2 -2 -3
fmt.Println(math.Sqrt(144), math.Mod(-7, 3))                                         // 12 -1
fmt.Printf("%.6f %.6f %.6f\n", math.Pow(2, 10), math.Log2(1024), math.Hypot(3, 4))   // 1024.000000 10.000000 5.000000
fmt.Printf("%.6f %.6f\n", math.Sin(math.Pi / 6), math.Atan2(1, -1))                  // 0.500000 2.356194
```

The special cases are those of IEEE 754 as the C standard lists them, which Go's `math` follows too: the square root or the logarithm of a negative number is `NaN`, the logarithm of zero is `-Inf`, anything to the power zero is 1, and a remainder by zero is `NaN`. None of them is an error to handle: the result says what happened, and `IsNaN` and `IsInf` read it.

```go
nan := math.Sqrt(-1)
fmt.Println(nan == nan, math.IsNaN(nan))                       // false true
fmt.Println(math.Log(0), math.IsInf(math.Log(0), -1))          // -Inf true
```

A `NaN` equals nothing, itself included, so `x == math.NaN()` is always false and `IsNaN` is the test. `IsInf(x, sign)` asks for the positive infinity with a sign above zero, the negative one with a sign below, and either with zero.

**How exact the results are.** `Sqrt`, `Floor`, `Ceil`, `Trunc`, `Round`, `Mod`, `Abs` and `Copysign` are exact, the same on every system. The others, the powers, logarithms and trigonometry, are the system's math library's, as Rust's and Swift's are: each is within a few units in the last place of the true result, most within one and the cube root on Linux within four, so the last digits may differ between macOS and Linux. The cube root of 27 is exactly 3 on one system and 3.0000000000000004 on another. A program whose output must be the same everywhere prints them to the places it means, `%.6f`, and compares them within a tolerance, never with `==`.

## `math`: the constants and the limits

`Pi`, `E` and `Ln2` are `f64`s. The limits each have the type they are the limit of:

| Constants | Type |
|---|---|
| `MaxInt`, `MinInt` | `int` |
| `MaxInt8`, `MinInt8` to `MaxInt64`, `MinInt64` | `i8` to `i64` |
| `MaxUint`, `MaxUint8` to `MaxUint64` | `uint`, `u8` to `u64` |
| `MaxFloat64`, `SmallestNonzeroFloat64` | `f64`: the largest finite value and the smallest above zero |
| `MaxFloat32`, `SmallestNonzeroFloat32` | `f32` |

```go
var top u8 = math.MaxUint8     // 255
top += 1                       // 0: integer arithmetic wraps
total := int(math.MaxInt32) + 1   // 2147483648: converted first, so the sum is an int's
```

A typed limit compares and assigns to a value of its own type with no conversion, and needs one beside a value of another, as any two number types do (chapter 3). A sum that must not wrap is `CheckedAdd` (chapter 3).

## `math/bits`

`math/bits` has six operations on the bits of an unsigned integer, each for `uint` and, with the width in its name, for `u8`, `u16`, `u32` and `u64`:

| Function | What it returns |
|---|---|
| `LeadingZeros(x)` | the zero bits above the highest one bit |
| `TrailingZeros(x)` | the zero bits below the lowest one bit |
| `OnesCount(x)` | the one bits |
| `Len(x)` | the bits needed to write `x` |
| `RotateLeft(x, k)` | `x` turned left by `k` bits, the bits leaving at the top coming back at the bottom |
| `Reverse(x)` | `x` with its bits in the opposite order |

```go
var board u64 = 0x00ff00000000ff00
fmt.Println(bits.OnesCount64(board), bits.TrailingZeros64(board), bits.Len64(board))   // 16 8 56

var row u8 = 0b00010110
fmt.Println(bits.Reverse8(row), bits.RotateLeft8(row, 3), bits.LeadingZeros8(row))     // 104 176 3
```

Every one is defined for every argument. Zero has as many leading and trailing zeros as the type has bits, `bits.LeadingZeros8(0)` is 8, and a length of zero. A rotation by a negative count turns right, and a count of the width or more turns as the count modulo the width does, so `RotateLeft8(x, 8)` is `x`.

A call of one of these is not a call when the program runs: the compiler writes the processor's instruction in its place, in a debug build as in a release build. A function of the package is still a value a program may keep and pass on, `f := bits.OnesCount64`, and a call through the value is an ordinary call.

## `math/rand`: a generator with a seed

`rand.New(seed1, seed2)` returns a `Rand`, a generator that gives the same numbers for the same two seed words, on every system, in every build and through both backends. It is for a simulation, a test or a game, where a run must be repeatable.

```go
r := rand.New(2026, 79)
roll := r.IntN(6) ?? 0          // 0 to 5
chance := r.Float64()           // 0 up to, not including, 1
r.Shuffle(mut deck)             // the deck in a random order
order := r.Perm(6)              // 0 to 5 in a random order
```

| Method | What it returns |
|---|---|
| `Int()`, `Int64()` | a random `int` or `i64` that is not negative |
| `IntN(n)` | an `int?`: a number from 0 up to, not including, `n`, each as likely as another, or `None` when `n` is zero or negative |
| `Float64()` | an `f64` from 0 up to, not including, 1 |
| `Shuffle(xs)` | nothing: the elements of `xs` are put in a random order, every order as likely as another |
| `Perm(n)` | the numbers 0 to `n-1` in a random order, in a slice it allocates; no numbers when `n` is zero or negative |

`IntN` returns an option because a bound of zero leaves no number to return, and the library panics on no mistake a caller can make. `Perm` allocates its `n` numbers, and a count more than memory holds ends the program as any allocation that fails does. A program that knows its bound is positive writes `r.IntN(6) ?? 0`; one that draws an index, `xs[r.IntN(len(xs))?]`, hands the empty slice to its caller as `None` instead of indexing nothing.

`Shuffle` takes the slice itself, by `mut`, and exchanges its elements in place, so it shuffles a slice of any element, a string or a struct with a `Drop` as well as an `int`: nothing is copied.

A `Rand` is a plain struct of two words, the whole of its state, and it is copied as any struct of plain values is (chapter 5). A copy goes on from where the original was and gives the same numbers:

```go
r := rand.New(1, 2)
saved := r                             // the place to replay from
fmt.Println(r.Int() == saved.Int())    // true: the same number from both
```

That is how a program replays a sequence or tries two branches of a simulation from one point. To draw one sequence from several functions, lend the generator: a function that draws takes `r mut rand.Rand`, and the caller passes `mut r`. A `Rand` crosses into a task as any plain value does, and the task draws from its own copy.

The generator is PCG, the permuted congruential generator in its DXSM form with 128 bits of state, and the numbers are the ones Go's `math/rand/v2` gives for `rand.New(rand.NewPCG(seed1, seed2))`: a sequence a Go program recorded is the sequence a Snello program gets. What it gives can be worked out from what it gave. It is no source of secrets.

## `math/rand`: the package functions

`rand.Int`, `rand.IntN`, `rand.Int64`, `rand.Float64`, `rand.Shuffle` and `rand.Perm` are the same six with no generator to make and no seed:

```go
roll := rand.IntN(6) ?? 0
rand.Shuffle(mut players)
```

They draw from a source in the runtime that is keyed from the system's random bytes, and nobody can tell what they will give, from one run to the next or from the numbers before. The source is ChaCha8, the ChaCha stream cipher at eight rounds, as Go's package functions use: a program that draws a session identifier from `rand.Int()`, as programs do whatever a manual says, is not caught out.

Each thread has a generator of its own, so tasks draw with no lock and one task's numbers never slow another's. There is nothing to seed and nothing to share.

The key comes from the system's random bytes, which every system Snello runs on gives. Were a system to give none, the program would still run, on a key made of the clock: numbers that one who knows when the program started could work out, and no longer fit for an identifier that must not be guessed.

Use a `Rand` when a run must repeat, and the package functions everywhere else.

## For maintainers

- **`math`.** `std/math/math.snello` declares each function without a body, and `runtime/c/math.c` defines it as one call of the C math library's, `sn_math_Floor` calling `floor`, declared in `snello.h`. `Abs`, `Min` and `Max` are Snello, with Go's answers on zeros and `NaN`. The constants and limits are typed literals, since a constant is a literal. The special cases are the C library's, which are IEEE 754's; `math_test.snello` holds each against its exact value or, for the functions the system computes, within a few units in the last place.
- **`math/bits`: the declarations.** `std/math/bits/bits.snello` declares the thirty functions, six operations at five widths, each with a plain signature, so the checker knows nothing of the package.
- **`math/bits`: the lowering.** `BitOp` in `compiler/emit/lower/lower.go` reads a function's key, `math/bits.LeadingZeros8`, into its operation and width, and `Key` returns the key so that both backends write the call themselves. The C backend, `libraryCall` in `compiler/emit/c/library.go`, calls the header's `sn_bits_LeadingZeros8`, a function in `snello.h` that is always written in place, over the compiler's builtins: `__builtin_clzll`, `__builtin_ctzll`, `__builtin_popcountll`, a shift pair the compiler reads as a rotation, and `__builtin_bitreverse64` where the compiler has it, else Go's swap of neighbours, pairs, nibbles and bytes. A width under 64 is counted in a 64-bit word with a one set just past the value, so zero gives the width with no test. The LLVM backend, `bitOp` in `compiler/emit/llvm/library.go`, writes `llvm.ctlz`, `llvm.cttz`, both told that zero is no poison, `llvm.ctpop`, `llvm.bitreverse` and `llvm.fshl` with the value as both halves, on the width's own integer type.
- **`math/bits`: a function as a value.** `runtime/c/math.c` defines every function under the library's name too, `sn_math__bits_OnesCount64`, each the header's. The `selector` of each backend lets a function of the package be a value, where every other function the compiler writes is refused as one, and the value names that definition.
- **The seeded generator.** `std/math/rand/rand.snello` is Snello but for two functions of the runtime. `Rand.next` is Go's `PCG.next` and `PCG.Uint64`: the state times a 128-bit multiplier plus an increment, then DXSM, with the carry out of the low half written as a comparison. `below` is Go's `uint64n`: a mask for a power of two, else Lemire's multiply, the high half of the 128-bit product of 64 random bits and the bound, with a redraw when the low half falls under `-n % n`, tested against `n` first so that the division is rare. `shuffle` is Go's Fisher and Yates loop from the last element down. Each drawing is written once, generic over the private trait `source`, which `Rand` and the runtime's source both implement, so a method and its package function are one body compiled twice.
- **The runtime's source.** `runtime/c/rand.c` is Go's `internal/chacha8rand`: a block is four ChaCha8 blocks with their words interleaved, 32 numbers, written on the compiler's four-lane vector type so that it is the processor's vector instructions, as Go's assembly is; a generator keys itself again from its own last four numbers after sixteen blocks. The keys are Go's runtime's as well: `thread_key` keeps one generator for the program, keyed by `system_key` from `getentropy`, or `/dev/urandom` where that fails, or by `clock_key`, Go's `readTimeRandom`, where both do; a thread's generator takes four numbers of it under a lock, and it then keys itself again, so no two threads share a key. `sn_math__rand_random` reaches the thread's generator in a function the compiler may not inline, since a task may resume on another thread (`sched.c` says why). `sn_math__rand_mul64` is the 128-bit product.
- **The tests.** `TestRandom` in `runtime/runtime_test.go` keys the thread's generator with `sn_rand_key`, a debug build's, and holds 3,000 numbers drawn as the package functions draw them against Go's `rand.NewChaCha8` for the same key, through its refills and re-keyings; it holds the multiply against Go's `bits.Mul64`, and four threads' first numbers against each other and their bits to an even split, keyed from the system and, with `sn_rand_no_system` set, from the clock. `rand_test.snello` holds the seeded generator against numbers Go gave for the same seed, checks every bound's range, a die's evenness from both sources, and eight tasks drawing at once. `bits_test.snello` covers each operation at each width, zero and the rotations past the width. `TestBitOp` in `compiler/emit/lower/lower_test.go` and `TestBitOps` in each backend's `library_test.go` hold the lowering: the builtin or the intrinsic at a call, and the library's function at a value. The backend programs `math-functions`, `bit-functions` and `random-numbers` in `compiler/emit/internal/backendtest/backendtest.go` run all three packages through both backends in release and debug, each against output Go produced.
