# Benchmarks

The programs behind the performance table in `../_docs/design/01-language-design.md`: each exists in Snello, in Go and, where the comparison is the point, in Rust, written the same way in each language so that what is measured is the language and its runtime, not the program.

| Program | What it measures |
|---|---|
| `fib` | CPU-bound code: recursion and integer arithmetic, nothing allocated |
| `tree` | Allocation-heavy code: binary trees of 131,071 nodes built, walked and freed twenty times, one block per node |
| `words` | Strings and maps: two million short strings made and counted in a map of five thousand keys |
| `tasks` | Tasks and channels: one hundred thousand tasks spawned and waited for, then a value handed back and forth over unbuffered channels half a million times; it prints the cost of a spawn and of a switch, in nanoseconds |
| `memory` | Memory without a collector: a tree of a million nodes kept alive while two thousand small trees are built and dropped beside it; it prints the mean round, the longest, and how many took over twice the mean |

`go test ./bench` builds every Snello program through both backends and in Go and checks that they print the same; a program that times itself prints its timings on its last line, which is not compared. It also runs `TestNoCollector`, which builds `memory` with one round, with two thousand and with ten thousand, and fails if the peak memory grows by a tenth: freed memory is reused at once, so a run that frees eighty million blocks peaks where one that frees eight thousand does. `SNELLO_BENCH=1 go test ./bench -v` builds the Rust versions too, times every binary three times, and prints the table of the best runs with each binary's peak memory, which is what the design document records; it is not run by default, since it takes a minute and its numbers depend on the machine.
