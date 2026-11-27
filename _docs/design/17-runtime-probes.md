# Probes: a Snello program observed by any tool

Status: proposed; nothing is built. This note is the design the issues are to be filed from.

## Why

Snello ships no profiler of its own, and is not to ship one. A program is profiled with the system's own tools, `perf` on Linux and Instruments on macOS, as chapter 30 says, and that already works for where a program spends its time: every Snello function that calls another keeps its frame pointer, and a release build keeps its symbols, so a sampling profiler walks and names every stack it interrupts.

Two questions a sampling profiler cannot answer about a Snello program:

- **Where do the tasks wait?** A sampler interrupts threads. A task parked on a channel, a lock, a timer or a socket is on no thread, so it is in no sample, and a program that is slow because its tasks wait looks idle.
- **What allocates, and what is still held?** An allocation is a call into the runtime like any other; nothing tells a tool that this call took 64 bytes and that one gave them back.

Go answers both inside its runtime, with pprof's goroutine and heap profiles and `runtime/trace`, in Go's own formats, read by Go's own tools. That is the shape this note rejects: a tool that serves one language, which a developer learns once per language.

The answer here is for the runtime to **announce its events at named points that any tracing tool on the system can attach to**: the task spawned, run, parked and woken, the memory taken and given back. Snello ships the points; bpftrace, BCC, `perf`, DTrace, Windows' performance tools, and any profiler built on them, eBPF's whole-system profilers among them, do the observing, as they do for every other program on the machine.

## Precedents

- **USDT**, user-level statically defined tracing: DTrace's probes compiled into a program, from Solaris and on macOS, and on Linux through SystemTap's `sys/sdt.h`, which writes the same probes into an ELF note that every Linux tracer reads: bpftrace (`usdt:`), BCC, libbpf, and `perf probe`. glibc carries such probes in its allocator and `setjmp` when built with `--enable-systemtap`, as the major distributions build it; Java's HotSpot, Python and PostgreSQL carry them for their own events, Python's and PostgreSQL's only when built with `--with-dtrace` and `--enable-dtrace`.
- **ETW** on Windows, and its TraceLogging form, which declares an event where it is written, with no manifest: a provider the program registers, events that cost a check of a flag while no session listens, read by Windows Performance Recorder and Analyzer, PerfView and `tracerpt`.
- **Go's `runtime/trace` and pprof**: the events and the profiles kept in the program and written in Go's formats; the reference for what is worth announcing, not for how.
- **heaptrack** and similar tools trace `malloc` and `free` of any native program by interposing on them; the `alloc` and `free` probes give the same view without an interposed library, of the allocator Snello actually uses.

## What the runtime announces

One provider, `snello`. A probe's arguments are integers: addresses, sizes and small codes; a tool reads anything more from the stack it captures at the probe.

| Probe | Where | Arguments |
|---|---|---|
| `task-spawn` | `sn_spawn`, `sn_spawn_handle` (`runtime/c/sched.c`) | the new task's record, the spawning task's record, the new task's stack bound |
| `task-run` | `run_task`, as a worker switches to a task | the task, the worker's index |
| `task-wait` | `sn_park`, before the switch away | the task, what it waits on (a channel, a lock, a timer, a socket, a child process), that object's address, the direction for a channel |
| `task-wake` | `sn_wake_claim`, the one step from parked to runnable, which every wake takes: a channel's and a lock's through `sn_finish_wait`, a socket's through `sn_handoff_claim`, a cancel's through `sn_wake` | the task woken, the task that woke it, or none for a timer or the poller |
| `task-preempt` | `preempt_land` | the task, the worker's index |
| `task-end` | where a task ends, before its record is kept for reuse (`dead_keep`) or freed | the task, whether a panic ended it |
| `alloc` | `sn_alloc`, `runtime/c/alloc.c` in a release build and `runtime/c/debug.c` in a debug build | the address, the size |
| `free` | `sn_free`, in the same two files | the address |

**What a task waits on is recorded at each wait.** Today `sn_task_waits` records a channel and its direction alone, for the deadlock report; a lock, a timer, a socket and a child record nothing. `task-wait` needs each wait to record its kind and object as a channel's does: one store of two words a wait, on paths that park anyway, which the cost below counts.

**A task is named by its record's address.** A task has no number today, and a number would be a shared counter every spawn writes, which the scheduler was built to avoid. An address is reused once a task ends, which `task-end` says, so a tool names a task by its address between its `task-spawn` and its `task-end`.

What a developer gets from them, with tools already on the system:

- **Where tasks wait**: the stack at each `task-wait`, captured by the tracer while the task is still on its thread, counted by stack or weighed by the time until its `task-wake`: an off-CPU profile of tasks rather than of threads.
- **How long a task waits to run**: `task-wake` to `task-run`, per worker, which says whether the scheduler or the program is the cause.
- **What allocates, and what is held**: the stack at each `alloc`, and the allocations with no `free` yet, by stack: a heap profile and a leak finder, for a program that has no collector to blame.
- **What a channel does**: every wait on one address, by task.

## How, per system

- **Linux**: the probes are written in `sys/sdt.h`'s form: a `nop` at the probe's address, and an ELF note, `.note.stapsdt`, naming the provider, the probe, the address and where each argument is, a register or a stack slot. The header is SystemTap's, which is in the public domain, kept in `runtime/c` so that a build needs no system package, `systemtap-sdt-dev` or its kin, and a build for Linux from another system writes the same notes. `bpftrace -e 'usdt:./prog:snello:task__wait { @[ustack] = count(); }'` is the whole of a wait profile.
- **macOS**: the same probes through DTrace's form: the provider described in `snello.d`, its header written once by `dtrace -h` and kept in `runtime/c`, so that a build needs no `dtrace`; the linker turns each probe's call into `nop`s and records it in the binary. `sudo dtrace -n 'snello$target:::task-wait { @[ustack()] = count(); }' -c ./prog`.
- **Windows**: an ETW provider through TraceLogging, `TraceLoggingProvider.h` from the Windows SDK the build already links against, registered as the program starts and unregistered as it ends; each probe is a `TraceLoggingWrite` of its arguments, with the provider's stack capture for the stacks. Read with Windows Performance Recorder, PerfView or `tracerpt`.

**Always compiled in**, in a release build and a debug build alike, with no flag. A probe that needs a rebuild is never there when the program that needs it is running in production, which is why the distributions build glibc with its probes, and why a language runtime's probes behind a build flag are seldom on the machine that has the problem.

## What it costs

- **With nothing attached**, a probe on Linux and macOS is a `nop` where it sits, and its arguments, which are values the code at the site already holds; on Windows, a load of the provider's flag and a branch not taken. Each wait also records its kind and object, two words stored on a path about to park. The probes sit on the runtime's paths alone, never in the program's own code, so a function the compiler writes costs what it cost.
- **With a tracer attached**, each event traps into the kernel, on Linux a uprobe of the order of a microsecond: tracing `alloc` and `free` slows an allocating program many times over, as any allocation tracer does, and the tracer chooses which probes to attach.
- **Measured before it lands**: every row of the bench table with the probes compiled in, against `main`, within each row's spread, on both Linux machines and the Mac; and the size of a built program, which gains the notes.

## Memory safety, and no collector

The probes read; they never change the runtime's state, take a lock or allocate, and their arguments are integers. A tracer that reads memory at an address it was given reads it from outside the program, through the kernel, as a debugger does, and the program's own code never sees the read; the language's guarantees are what they were. Attaching needs the privileges a debugger needs, root or the tracing capabilities on Linux, the DTrace privileges on macOS, an administrator's session on Windows, so the probes expose nothing a debugger could not already see. No collector is involved: `free` reports each free where the compiler placed it, at its owner's end.

## Left out

- **A profiler, a format, an endpoint or a `snello profile` command.** The system's tools are the tools.
- **Sampling inside the program**, a signal that records stacks: the system's sampler does it better, for every language at once.
- **A probe on every function's entry and exit.** The tracers attach to any function's symbol already, and a probe the compiler wrote into every function would cost every call.
- **eBPF programs shipped with Snello.** The book carries the scripts, short enough to read.

## The book

Chapter 30 gains **Tracing tasks and allocations**: the probes and their arguments; bpftrace scripts for where tasks wait, how long they wait to run, and the heap by stack; the same in DTrace; Windows Performance Recorder's steps; and, for maintainers, the header, the provider, the sites and the tests. Each script is run as written before the chapter lands.

## Landing plan

1. **Linux**: the header, the probes at their sites, a test that reads a built program's `.note.stapsdt` with Go's `debug/elf` and finds every probe with its arguments, and a bpftrace run in the Linux image that sees `task-spawn` and `task-wait` of a program that spawns and waits; the bench table against `main`.
2. **macOS**: the provider, its header, the probes, a test that finds them in a built program, and a DTrace run.
3. **Windows**: the provider and the events, and a test that reads them from a session.
4. **The chapter**, every script run as written.

## Open questions

- **The Mac's DTrace under System Integrity Protection**, which limits what DTrace may attach to, and whether `ld64.lld`, which links a program for macOS built on another system, records DTrace's probes as Apple's linker does. Both are to be tried before the macOS step is planned.
- **A test of the Windows events without an administrator**, since starting an ETW session usually needs one; the CI runner has one, a developer's machine may not.
- **The scheduler's own events**, a worker sleeping, waking and stealing, which say why a program does not use its cores: in this set, or after it, once the task probes are in use.
- **A task's number**, should the address prove too hard for tools to follow across reuse: a number given at spawn from each worker's own counter, which costs no shared write.

## Proposed

**Proposed 1: no Snello profiler; the runtime announces its events to the system's tracers.** USDT on Linux and macOS and ETW on Windows, because every tracer and profiler on each system already reads them, for every language that carries them, and a developer learns the tools once.

**Proposed 2: always compiled in.** A probe is a `nop` until attached, and a probe that needs a rebuild is not there when it is needed.

**Proposed 3: tasks and memory, nothing per function.** Spawn, run, wait, wake, preemption and end, and allocation and free: what a sampler cannot see; what a sampler can see is left to it.

**Proposed 4: a task is its record's address.** No shared counter on the spawn path; `task-end` marks the address free for the next task.
