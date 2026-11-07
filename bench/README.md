# Benchmarks

The programs behind the performance table in `../_docs/design/01-language-design.md`: each exists in Snello and in Go, written the same way in both so that what is measured is the language and its runtime, not the program. Go is the comparison because Snello is written against it, Go's syntax and concurrency without a collector; Rust versions were kept until 2026-11-07 and removed at the owner's word, so that the table has one comparison and no dependency to keep up.

| Program | What it measures |
|---|---|
| `fib` | CPU-bound code: recursion and integer arithmetic, nothing allocated |
| `tree` | Allocation-heavy code: binary trees of 131,071 nodes built, walked and freed twenty times, one block per node |
| `words` | Strings and maps: two million short strings made and counted in a map of five thousand keys |
| `churn` | The allocator under contention: a task per core allocating and freeing small blocks of mixed sizes at once, then blocks made in one task and freed in another, handed around a ring of channels in batches |
| `tasks` | Tasks and channels: one hundred thousand tasks spawned and waited for, then a value handed back and forth over unbuffered channels half a million times; it prints the cost of a spawn and of a switch, in nanoseconds |
| `memory` | Memory without a collector: a tree of a million nodes kept alive while two thousand small trees are built and dropped beside it; it prints the mean round, the longest, and how many took over twice the mean |

`go test ./bench` builds every Snello program through both backends and in Go and checks that they print the same; a program that times itself prints its timings on its last line, which is not compared. It also runs `TestNoCollector`, which builds `memory` with one round, with two thousand and with ten thousand, and fails if the peak memory grows by a tenth: freed memory is reused at once, so a run that frees eighty million blocks peaks where one that frees eight thousand does.

`SNELLO_BENCH=1 go test ./bench -run TestBenchmarks -v` times every binary: one run discarded, since the first run of a new binary pays for the system's check of it, then ten runs, and the table reports the median with the least and the most, each binary's peak memory, and for a program that times itself its own numbers summarized the same way, `997 (980–1020)`. That is what the design document records. It is not run by default, since it takes minutes and its numbers depend on the machine. With `SNELLO_BENCH_OUT=FILE` the table is written to the file as well; the CI workflow runs the timing on its x86-64 and arm64 Linux runners and puts the table in the job's summary, where a program that prints the wrong thing fails the job and a time never does, since shared runners are too noisy for a time to be a test.
