// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The scheduler. A task is a coroutine with a stack of its own,
// reserved from the system and committed by it page by page as the
// task touches it, and a saved set of callee-saved registers; switching
// from one task to another is the assembly routine sn_switch below, one
// per processor.
// N worker threads, one per core with the main thread among them, run
// M tasks: each worker keeps a queue of runnable tasks and steals from
// another's when its own is empty; a worker with nothing to run waits
// to be told of work. A task that must wait, on a channel, a lock or
// a timer, parks: it switches to its worker's scheduler, which runs
// the next task, and whoever wakes it puts it back on a queue. A task
// runs until it parks, ends or is preempted: one that computes without
// waiting while a task is queued is taken off its worker by the
// monitor thread below after held_limit, as the section on preemption
// says, and a task it wakes meanwhile goes to another worker through
// the monitor, so that it holds no task but its own. The timers are
// timer.c's; the workers fire them between their tasks, as the section
// on them below says.
//
// The main thread's own stack is the main task's. When the main task
// first parks, the main thread takes a scheduler stack of its own and
// becomes a worker like the others; the main task may then resume on
// any worker, since a stack is only memory and the program keeps
// nothing per thread but what this file keeps per task: the guard
// stack a panic unwinds, which lives in the task.

#define _DEFAULT_SOURCE // glibc's mmap flags and clocks under -std=c11, as snello.c asks for its own
#define _GNU_SOURCE     // glibc's sched_getaffinity, for the processors the program may run on
#define _DARWIN_C_SOURCE

#include "internal.h"

#include <errno.h>
#include <stdatomic.h>
#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#if defined(_WIN32)
#include <winternl.h>
#else
#include <sched.h>
#include <signal.h>
#include <sys/mman.h>
#include <sys/ucontext.h>
#include <unistd.h>
#endif

#if defined(__APPLE__)
#include <fcntl.h>
#include <mach-o/getsect.h>
#include <mach-o/ldsyms.h>
#include <sys/event.h>
#endif

#ifdef __linux__
#include <link.h>
#include <sys/epoll.h>
#include <sys/eventfd.h>
#include <sys/prctl.h>
#include <sys/timerfd.h>
#endif

// A task's state is one atomic word, changed by compare-and-swap:
// RUNNING on a worker; RUNNABLE on a queue or in a slot; PARKING,
// switched away from but not yet settled by its worker; PARKED,
// waiting for a wake; DONE. WOKEN is a bit over the first three: a wake
// that came while the task was running or parking, kept for its park,
// so that a wake is never lost between a task deciding to wait and its
// worker taking it off the stack, and cleared when the task resumes,
// since the wake that resumed it is the one it waited for. From the
// time a wake can reach the task until it ends, every change of the
// word reads and writes it, a wake already kept written over with
// itself, so that each is ordered with the others and the park that
// takes a wake sees what its waker wrote before it.
enum { RUNNING, RUNNABLE, PARKING, PARKED, DONE, PREEMPTED, WOKEN = 8 };

// few_guards is the room a task's record holds for its first guards,
// so that a task holding a channel handle or a value with a Drop
// method, which most tasks do, registers it without an allocation; a
// task with more takes room for guards_grow from the system, and then
// doubles it.
enum { few_guards = 4, guards_grow = 16 };

// few_args is the room a task's record holds for the arguments of its
// spawn, the values moved into the task and the handle's channel, so
// that a spawn allocates nothing for them; a larger set goes to the
// system's heap, freed when the task ends.
enum { few_args = 64 };

// A worker's queue holds ring_size tasks, and the worker keeps up to
// stack_cache stacks of each class that ended tasks gave up, for the
// tasks it spawns next.
enum { ring_size = 256, stack_cache = 32 }; // a cache of 64 was measured no better

// A worker keeps the records of its last dead_cache ended tasks of each
// stack class, each with its stack, for the tasks spawned on it next:
// a spawn then allocates nothing and writes a stack top its worker
// touched lately. A full cache sends dead_batch to the pool under the
// pool's lock, and an empty one takes dead_batch from it.
enum { dead_cache = 64, dead_batch = 32 };

enum { guarded_bytes = 256 << 10 };             // a guarded stack, as step one's thread stack was
static const size_t stack_size = guarded_bytes; // in the type its sizes are worked out in

// A task's stack is of one of stack_classes classes. A task whose stack
// the analysis of the linked program bounds gets the smallest small
// class its bound fits: small_least bytes, each class twice the one
// before, packed with the others of its class in mappings of small_block
// bytes made writable whole, whose pages the system backs as the tasks
// first touch them, so that a spawn makes no system call and touches no
// page its task does not use. A task with no bound gets the guarded
// class: a reservation of stack_size bytes with a guard page at its low
// end, so that a recursion past it faults rather than writes over
// another task's.
//
// The guarded stacks are reserved block_stacks at a time, one mapping
// of 16 MB with no access, and a stack taken from a block is given
// access above its guard page, which keeps none: one system call a
// stack, where a mapping of its own and a guard made after cost two.
// Reserved with no access, each stack's writable range is bounded by
// pages with none, so Linux backs a first touch with a page of its own
// and not a huge page across the stacks still to be taken, which a
// block reserved writable was measured to do, at seven times the cost
// of the touch.
enum {
    small_least = 2 << 10,
    small_classes = 6, // 2 KB to 64 KB
    guarded = small_classes,
    stack_classes,
    small_block = 1 << 20,
    block_stacks = 64,
    guarded_pooled = 256, // measured against 1024: the deeper pool made a spawn a third slower
    stack_align = 16,     // what a stack pointer is a multiple of at a call, on both processors
    below_frame = 256, // the room sn_panic leaves below a frame it moves off: the red zone and what sn_on_stack pushes
};

// In a debug build a small stack is filled with stack_paint as its task
// is spawned, and read as the task ends for how deep the task wrote.
enum { stack_paint = 0xA5 };

// In a debug build a worker keeps its last ended_kept ended tasks
// unfreed, so that a wake that reaches a task that has ended reads a
// task still marked done, and stops the program, rather than memory
// given out again.
enum { ended_kept = 1024 };

// A worker with tasks to run looks past them once in outside_turns: it
// fires the other wheels' timers that are due and takes a task from the
// inject queue before its own, so that what the poller woke waits for
// no queue to empty. It is the interval at which Go's worker looks at
// its global queue and Tokio's looks past its tasks at its timers and
// sockets: a prime, so that it falls in step with no period a program
// has.
//
// Two tasks handing a value back and forth wake each other into the
// runnext slot, which runs before the queue, and would keep the worker
// between them for as long as they trade while the tasks queued behind
// them wait. The slot's task runs first next_most times in a row, as
// Tokio's does, and then the queue's head does.
enum { outside_turns = 61, next_most = 3 };

// The monitor looks at the workers' runnext slots every next_stale
// while any worker is busy, and moves a task left in a slot by a worker
// that has switched to no task since the look before to the inject
// queue, since its running task does not let go.
static const int64_t next_stale = 1000000; // a millisecond

// A thief that finds a task in another worker's runnext slot waits
// steal_wait for the owner to take it itself, which it is likely to
// within that time, and takes the task if it is still there.
static const int64_t steal_wait = 3000; // 3 µs

enum { steal_spins = 64 }; // pauses between a thief's readings of the clock while it waits

// A worker out of work spins for spin_for before it sleeps, while the
// spinning's worth, which spin_for raises up to worth_most, is above
// zero.
static const int64_t spin_for = 50000; // 50 µs

enum { worth_most = 8 };

// The keeper spins to a deadline nearer than keeper_near rather than
// sleeps, since no sleep in the kernel returns in time for it.
static const int64_t keeper_near = 20000; // 20 µs

// The monitor pauses between its looks for a stale slot, twice as long
// after each look that finds nothing, up to pause_most.
static const int64_t pause_most = 10000000; // 10 ms

// A worker whose running task has not let go for held_limit is held:
// computing without a wait, or blocked in a read or a bound function.
// With every worker held and a task queued, the monitor starts another
// worker, up to workers_per_core of them a core, and the system shares
// the processors between the threads.
static const int64_t held_limit = 10000000; // 10 ms

enum { workers_per_core = 16 };

// A task that has held its worker for held_limit while a task waits is
// preempted, as Go's runtime has preempted a goroutine since Go 1.14:
// on Linux and macOS its thread is sent preempt_signal, whose handler
// runs on a signal stack of signal_stack_bytes the worker keeps, never
// on a task's small stack; on Windows the thread is suspended and given
// another context. The task's registers go in a block of its own, as
// the system saved them, with a landing stack of landing_bytes the
// task switches to the scheduler from and has its registers put back
// on; context_most bounds what the system saved, the vector unit's
// state included. A task is preempted only outside the runtime's held
// section (internal.h, SN_HELD). A worker still on one task after
// preempt_tries attempts is blocked in a system call or a bound
// function, which no signal frees, and gets a thread started beside it
// as before.
enum { preempt_tries = 3, landing_bytes = 16 << 10, signal_stack_bytes = 64 << 10, context_most = 8 << 10 };

#if defined(_WIN32)
// The thread's information block, where the system keeps the stack's
// bounds, which the switch above reads and writes, and the last error.
enum { teb_stack_base = 0x08, teb_stack_limit = 0x10, teb_last_error = 0x68, teb_deallocation_stack = 0x1478 };
#else
enum { preempt_signal = SIGURG }; // unused by the system but for out-of-band data, and ignored by default
#endif

#if defined(__linux__) && defined(__x86_64__)
// The floating-point image a signal frame holds: FXSAVE's 512 bytes,
// and the extended state after them when the software-reserved words
// at fp_sw_reserved carry fp_xstate_magic and the image's whole size.
enum { fp_image_bytes = 512, fp_sw_reserved = 464, fp_xstate_magic = 0x46505853 };
#elif defined(__linux__) && defined(__aarch64__)
// The record the kernel leaves in a signal frame's reserved room when
// it put part of the vector unit's state outside the frame.
enum { context_extra_magic = 0x45585401 };
#endif

// The keeper's own events, among which the poller's descriptor is one
// more on Linux; on Windows they are the keys of the port's packets.
#if defined(__APPLE__)
enum { keeper_timer = 1, keeper_user = 2 };
#elif defined(_WIN32)
#else
enum { keeper_user = 1, keeper_timer = 2, keeper_poller = 3 };
#endif

// sn_switch saves the callee-saved registers and the stack pointer of
// the running context into *save, and resumes the context whose stack
// pointer is to, which was saved the same way, or made by make_context
// for a context that has not run yet. The frame it saves is 160 bytes
// on arm64: x19 to x28, x29 and x30, then d8 to d15; and 64 bytes on
// x86-64: rbp, rbx and r12 to r15, pushed, with the return address the
// caller left above them. On Windows it holds more, the stack's bounds
// and on x86-64 the registers that convention keeps besides, as the
// comment above the Windows switch says. The symbol carries a leading
// underscore on Darwin, as every C symbol does there.
void sn_switch(void **save, void *to);

// sn_on_stack calls fn(arg) on the stack whose top is top, aligned to
// sixteen bytes, and returns what fn returns, back on the stack it was
// called on, whose frame pointer keeps the way back: a panic takes a
// stack and runs its drops with it, where a task's small stack, bound
// to what its own code uses, has no room for either. ON_STACK, below,
// calls it with the bounds of the stack, which Windows keeps.

// stack_pointer returns the stack pointer of the function it is written
// in, inlined; a call made of it returns one below that frame, which
// serves its callers as well.
static inline uintptr_t stack_pointer(void) {
    uintptr_t sp;
#if defined(__aarch64__)
    __asm__ volatile("mov %0, sp" : "=r"(sp));
#else
    __asm__ volatile("movq %%rsp, %0" : "=r"(sp));
#endif
    return sp;
}

// SN_SYM is a symbol's name as the assembler spells it, and SN_TYPE
// marks one a function, which the ELF symbol table records and the
// Mach-O one does not: the trace of a panic reads the functions by
// their type, since an aarch64 ELF object marks its code and data with
// symbols of no type too.
// SN_HELD_SECTION puts the routines below in the runtime's held section
// (internal.h, SN_HELD), as the functions that call them are: a task is
// never preempted inside a switch.
#if defined(__APPLE__)
#define SN_HELD_SECTION ".section __TEXT,__sn_held,regular,pure_instructions"
#elif defined(_WIN32)
#define SN_HELD_SECTION ".section .text$sn_held_m,\"xr\""
#elif defined(__aarch64__)
#define SN_HELD_SECTION ".section sn_held,\"ax\",%progbits"
#else
#define SN_HELD_SECTION ".section sn_held,\"ax\",@progbits"
#endif

#if defined(__APPLE__)
#define SN_SYM(name)  "_" #name
#define SN_TYPE(name) ""
#elif defined(_WIN32)
#define SN_SYM(name)  #name
#define SN_TYPE(name) ".def " #name "\n.scl 2\n.type 32\n.endef\n" // an external function, in COFF's words
#elif defined(__aarch64__)
#define SN_SYM(name)  #name
#define SN_TYPE(name) ".type " #name ", %function\n"
#else
#define SN_SYM(name)  #name
#define SN_TYPE(name) ".type " #name ", @function\n"
#endif

// On Windows the switch also saves and restores the bounds of the stack
// the thread is on, which the system keeps in the thread's information
// block and reads when it probes a large frame (__chkstk), dispatches an
// exception or unwinds: a task runs on a stack the thread did not start
// with, so each switch writes the bounds of the stack it resumes into
// the block of the thread it resumes on, StackBase, StackLimit and
// DeallocationStack. On x86-64 the Windows convention has the callee
// keep more than System V's: rdi, rsi and xmm6 to xmm15 besides, with
// the vector unit's control words. The frame is 264 bytes: xmm6 to
// xmm15, saved unaligned so that a task's first frame may start as a
// called function's does, MXCSR and the x87 control word, the three
// bounds, a word of padding, then r15 to rbp, pushed, below the return
// address. sn_on_stack takes the bounds of the stack it calls on, or
// NULL to keep the thread's, and puts them back after.
typedef struct {
    void *base;        // the top of the stack, StackBase
    void *limit;       // the lowest address it may use, StackLimit
    void *reservation; // where its memory begins, DeallocationStack
} sn_stack_bounds;

#if defined(_WIN32)
void *sn_on_stack_bounded(void *top, void *(*fn)(void *), void *arg, const sn_stack_bounds *bounds);
#define ON_STACK(top, bounds, fn, arg) sn_on_stack_bounded(top, fn, arg, bounds)
#else
void *sn_on_stack(void *top, void *(*fn)(void *), void *arg);
#define ON_STACK(top, bounds, fn, arg) ((void)(bounds), sn_on_stack(top, fn, arg)) // a stack's bounds are no one's
#endif

// clang-format off
#if defined(_WIN32) && defined(__x86_64__)
#define SN_FRAME 312 // the frame, the return address and a fresh frame's room above it: a slot and 32 bytes of home space
#define SN_SAVED_FP 256 // rbp, pushed first, and the return address the call left above it
#define SN_SAVED_PC 264
#define SN_RESUMED_SP 272 // where the switch's caller resumes, past the return address ret pops
#define SN_SAVED_CSR 160 // MXCSR, and the x87 control word after it
// The control words a thread starts with on Windows: MXCSR with every
// exception masked and rounding to nearest, and the x87's with every
// exception masked, rounding to nearest and a double's precision.
#define SN_MXCSR_START 0x1F80
#define SN_X87_START   0x027F
#define SN_SAVED_BOUNDS 168 // the stack's reservation, its limit and its base
__asm__(
    SN_HELD_SECTION "\n"
    ".globl " SN_SYM(sn_switch) "\n" SN_TYPE(sn_switch) SN_SYM(sn_switch) ":\n"
    "  pushq %rbp\n"
    "  pushq %rbx\n"
    "  pushq %rdi\n"
    "  pushq %rsi\n"
    "  pushq %r12\n"
    "  pushq %r13\n"
    "  pushq %r14\n"
    "  pushq %r15\n"
    "  subq $200, %rsp\n"
    "  movups %xmm6, 0(%rsp)\n"
    "  movups %xmm7, 16(%rsp)\n"
    "  movups %xmm8, 32(%rsp)\n"
    "  movups %xmm9, 48(%rsp)\n"
    "  movups %xmm10, 64(%rsp)\n"
    "  movups %xmm11, 80(%rsp)\n"
    "  movups %xmm12, 96(%rsp)\n"
    "  movups %xmm13, 112(%rsp)\n"
    "  movups %xmm14, 128(%rsp)\n"
    "  movups %xmm15, 144(%rsp)\n"
    "  stmxcsr 160(%rsp)\n"
    "  fnstcw 164(%rsp)\n"
    "  movq %gs:0x30, %r10\n"
    "  movq 0x1478(%r10), %rax\n"
    "  movq %rax, 168(%rsp)\n"
    "  movq 0x10(%r10), %rax\n"
    "  movq %rax, 176(%rsp)\n"
    "  movq 0x08(%r10), %rax\n"
    "  movq %rax, 184(%rsp)\n"
    "  movq %rsp, (%rcx)\n"
    "  movq %rdx, %rsp\n"
    "  movups 0(%rsp), %xmm6\n"
    "  movups 16(%rsp), %xmm7\n"
    "  movups 32(%rsp), %xmm8\n"
    "  movups 48(%rsp), %xmm9\n"
    "  movups 64(%rsp), %xmm10\n"
    "  movups 80(%rsp), %xmm11\n"
    "  movups 96(%rsp), %xmm12\n"
    "  movups 112(%rsp), %xmm13\n"
    "  movups 128(%rsp), %xmm14\n"
    "  movups 144(%rsp), %xmm15\n"
    "  ldmxcsr 160(%rsp)\n"
    "  fldcw 164(%rsp)\n"
    "  movq %gs:0x30, %r10\n"
    "  movq 168(%rsp), %rax\n"
    "  movq %rax, 0x1478(%r10)\n"
    "  movq 176(%rsp), %rax\n"
    "  movq %rax, 0x10(%r10)\n"
    "  movq 184(%rsp), %rax\n"
    "  movq %rax, 0x08(%r10)\n"
    "  addq $200, %rsp\n"
    "  popq %r15\n"
    "  popq %r14\n"
    "  popq %r13\n"
    "  popq %r12\n"
    "  popq %rsi\n"
    "  popq %rdi\n"
    "  popq %rbx\n"
    "  popq %rbp\n"
    "  ret\n"
    // Its unwind data says rbp is its frame's register, so that the
    // system's unwinder, from a function it called on another stack,
    // finds the stack it was called on through rbp.
    ".globl " SN_SYM(sn_on_stack_bounded) "\n" SN_TYPE(sn_on_stack_bounded) SN_SYM(sn_on_stack_bounded) ":\n"
    ".seh_proc " SN_SYM(sn_on_stack_bounded) "\n"
    "  pushq %rbp\n"
    ".seh_pushreg %rbp\n"
    "  movq %rsp, %rbp\n"
    ".seh_setframe %rbp, 0\n"
    ".seh_endprologue\n"
    "  movq %gs:0x30, %r10\n"
    "  pushq 0x08(%r10)\n"
    "  pushq 0x10(%r10)\n"
    "  pushq 0x1478(%r10)\n"
    "  testq %r9, %r9\n"
    "  jz 1f\n"
    "  movq 0(%r9), %rax\n"
    "  movq %rax, 0x08(%r10)\n"
    "  movq 8(%r9), %rax\n"
    "  movq %rax, 0x10(%r10)\n"
    "  movq 16(%r9), %rax\n"
    "  movq %rax, 0x1478(%r10)\n"
    "1:\n"
    "  movq %rcx, %rsp\n"
    "  subq $32, %rsp\n"
    "  movq %r8, %rcx\n"
    "  callq *%rdx\n"
    "  movq %gs:0x30, %r10\n"
    "  movq -8(%rbp), %rcx\n"
    "  movq %rcx, 0x08(%r10)\n"
    "  movq -16(%rbp), %rcx\n"
    "  movq %rcx, 0x10(%r10)\n"
    "  movq -24(%rbp), %rcx\n"
    "  movq %rcx, 0x1478(%r10)\n"
    "  movq %rbp, %rsp\n"
    "  popq %rbp\n"
    "  ret\n"
    ".seh_endproc\n"
    // sn_setjmp_regs saves the registers Windows' x64 convention has a
    // callee keep into the buffer at rcx: rbx, rbp, rdi, rsi, r12 to
    // r15, the stack pointer as the caller has it after the return, the
    // return address, MXCSR and the x87 control word, and xmm6 to xmm15;
    // sn_longjmp_regs puts them back and returns 1 to that address.
    ".globl " SN_SYM(sn_setjmp_regs) "\n" SN_TYPE(sn_setjmp_regs) SN_SYM(sn_setjmp_regs) ":\n"
    "  movq %rbx, 0(%rcx)\n"
    "  movq %rbp, 8(%rcx)\n"
    "  movq %rdi, 16(%rcx)\n"
    "  movq %rsi, 24(%rcx)\n"
    "  movq %r12, 32(%rcx)\n"
    "  movq %r13, 40(%rcx)\n"
    "  movq %r14, 48(%rcx)\n"
    "  movq %r15, 56(%rcx)\n"
    "  leaq 8(%rsp), %rdx\n"
    "  movq %rdx, 64(%rcx)\n"
    "  movq (%rsp), %rdx\n"
    "  movq %rdx, 72(%rcx)\n"
    "  stmxcsr 80(%rcx)\n"
    "  fnstcw 84(%rcx)\n"
    "  movdqu %xmm6, 96(%rcx)\n"
    "  movdqu %xmm7, 112(%rcx)\n"
    "  movdqu %xmm8, 128(%rcx)\n"
    "  movdqu %xmm9, 144(%rcx)\n"
    "  movdqu %xmm10, 160(%rcx)\n"
    "  movdqu %xmm11, 176(%rcx)\n"
    "  movdqu %xmm12, 192(%rcx)\n"
    "  movdqu %xmm13, 208(%rcx)\n"
    "  movdqu %xmm14, 224(%rcx)\n"
    "  movdqu %xmm15, 240(%rcx)\n"
    "  xorl %eax, %eax\n"
    "  ret\n"
    ".globl " SN_SYM(sn_longjmp_regs) "\n" SN_TYPE(sn_longjmp_regs) SN_SYM(sn_longjmp_regs) ":\n"
    "  movq 0(%rcx), %rbx\n"
    "  movq 8(%rcx), %rbp\n"
    "  movq 16(%rcx), %rdi\n"
    "  movq 24(%rcx), %rsi\n"
    "  movq 32(%rcx), %r12\n"
    "  movq 40(%rcx), %r13\n"
    "  movq 48(%rcx), %r14\n"
    "  movq 56(%rcx), %r15\n"
    "  ldmxcsr 80(%rcx)\n"
    "  fldcw 84(%rcx)\n"
    "  movdqu 96(%rcx), %xmm6\n"
    "  movdqu 112(%rcx), %xmm7\n"
    "  movdqu 128(%rcx), %xmm8\n"
    "  movdqu 144(%rcx), %xmm9\n"
    "  movdqu 160(%rcx), %xmm10\n"
    "  movdqu 176(%rcx), %xmm11\n"
    "  movdqu 192(%rcx), %xmm12\n"
    "  movdqu 208(%rcx), %xmm13\n"
    "  movdqu 224(%rcx), %xmm14\n"
    "  movdqu 240(%rcx), %xmm15\n"
    "  movq 64(%rcx), %rsp\n"
    "  movl $1, %eax\n"
    "  jmpq *72(%rcx)\n");
#elif defined(_WIN32) && defined(__aarch64__)
#define SN_FRAME 192 // the frame and the three bounds, with a word of padding
#define SN_SAVED_FP 80 // x29 in the frame below, and x30, the return address, after it
#define SN_SAVED_PC 88
#define SN_RESUMED_SP SN_FRAME // where the switch's caller resumes, past the frame the switch drops
#define SN_SAVED_BOUNDS 160 // the stack's reservation, its limit and its base
__asm__(
    SN_HELD_SECTION "\n"
    ".globl " SN_SYM(sn_switch) "\n" SN_TYPE(sn_switch)
    ".p2align 2\n" SN_SYM(sn_switch) ":\n"
    "  sub sp, sp, #192\n"
    "  stp x19, x20, [sp, #0]\n"
    "  stp x21, x22, [sp, #16]\n"
    "  stp x23, x24, [sp, #32]\n"
    "  stp x25, x26, [sp, #48]\n"
    "  stp x27, x28, [sp, #64]\n"
    "  stp x29, x30, [sp, #80]\n"
    "  stp d8, d9, [sp, #96]\n"
    "  stp d10, d11, [sp, #112]\n"
    "  stp d12, d13, [sp, #128]\n"
    "  stp d14, d15, [sp, #144]\n"
    "  ldr x10, [x18, #0x1478]\n"
    "  ldr x11, [x18, #0x10]\n"
    "  stp x10, x11, [sp, #160]\n"
    "  ldr x10, [x18, #0x08]\n"
    "  str x10, [sp, #176]\n"
    "  mov x9, sp\n"
    "  str x9, [x0]\n"
    "  mov sp, x1\n"
    "  ldp x19, x20, [sp, #0]\n"
    "  ldp x21, x22, [sp, #16]\n"
    "  ldp x23, x24, [sp, #32]\n"
    "  ldp x25, x26, [sp, #48]\n"
    "  ldp x27, x28, [sp, #64]\n"
    "  ldp x29, x30, [sp, #80]\n"
    "  ldp d8, d9, [sp, #96]\n"
    "  ldp d10, d11, [sp, #112]\n"
    "  ldp d12, d13, [sp, #128]\n"
    "  ldp d14, d15, [sp, #144]\n"
    "  ldp x10, x11, [sp, #160]\n"
    "  str x10, [x18, #0x1478]\n"
    "  str x11, [x18, #0x10]\n"
    "  ldr x10, [sp, #176]\n"
    "  str x10, [x18, #0x08]\n"
    "  add sp, sp, #192\n"
    "  ret\n"
    ".globl " SN_SYM(sn_on_stack_bounded) "\n" SN_TYPE(sn_on_stack_bounded)
    ".p2align 2\n" SN_SYM(sn_on_stack_bounded) ":\n"
    "  stp x29, x30, [sp, #-48]!\n"
    "  mov x29, sp\n"
    "  ldr x10, [x18, #0x08]\n"
    "  ldr x11, [x18, #0x10]\n"
    "  stp x10, x11, [sp, #16]\n"
    "  ldr x10, [x18, #0x1478]\n"
    "  str x10, [sp, #32]\n"
    "  cbz x3, 1f\n"
    "  ldr x10, [x3, #0]\n"
    "  str x10, [x18, #0x08]\n"
    "  ldr x10, [x3, #8]\n"
    "  str x10, [x18, #0x10]\n"
    "  ldr x10, [x3, #16]\n"
    "  str x10, [x18, #0x1478]\n"
    "1:\n"
    "  mov sp, x0\n"
    "  mov x0, x2\n"
    "  blr x1\n"
    "  mov sp, x29\n"
    "  ldp x10, x11, [sp, #16]\n"
    "  str x10, [x18, #0x08]\n"
    "  str x11, [x18, #0x10]\n"
    "  ldr x10, [sp, #32]\n"
    "  str x10, [x18, #0x1478]\n"
    "  ldp x29, x30, [sp], #48\n"
    "  ret\n"
    ".globl " SN_SYM(sn_setjmp_regs) "\n" SN_TYPE(sn_setjmp_regs)
    ".p2align 2\n" SN_SYM(sn_setjmp_regs) ":\n"
    "  stp x19, x20, [x0, #0]\n"
    "  stp x21, x22, [x0, #16]\n"
    "  stp x23, x24, [x0, #32]\n"
    "  stp x25, x26, [x0, #48]\n"
    "  stp x27, x28, [x0, #64]\n"
    "  stp x29, x30, [x0, #80]\n"
    "  mov x9, sp\n"
    "  str x9, [x0, #96]\n"
    "  stp d8, d9, [x0, #104]\n"
    "  stp d10, d11, [x0, #120]\n"
    "  stp d12, d13, [x0, #136]\n"
    "  stp d14, d15, [x0, #152]\n"
    "  mov x0, #0\n"
    "  ret\n"
    ".globl " SN_SYM(sn_longjmp_regs) "\n" SN_TYPE(sn_longjmp_regs)
    ".p2align 2\n" SN_SYM(sn_longjmp_regs) ":\n"
    "  ldp x19, x20, [x0, #0]\n"
    "  ldp x21, x22, [x0, #16]\n"
    "  ldp x23, x24, [x0, #32]\n"
    "  ldp x25, x26, [x0, #48]\n"
    "  ldp x27, x28, [x0, #64]\n"
    "  ldp x29, x30, [x0, #80]\n"
    "  ldr x9, [x0, #96]\n"
    "  mov sp, x9\n"
    "  ldp d8, d9, [x0, #104]\n"
    "  ldp d10, d11, [x0, #120]\n"
    "  ldp d12, d13, [x0, #136]\n"
    "  ldp d14, d15, [x0, #152]\n"
    "  mov x0, #1\n"
    "  ret\n");
#elif defined(__aarch64__)
#define SN_FRAME 160
#define SN_SAVED_FP 80 // x29 in the frame below, and x30, the return address, after it
#define SN_SAVED_PC 88
#define SN_RESUMED_SP SN_FRAME // where the switch's caller resumes, past the frame the switch drops
__asm__(
    SN_HELD_SECTION "\n"
    ".globl " SN_SYM(sn_switch) "\n" SN_TYPE(sn_switch)
    ".p2align 2\n" SN_SYM(sn_switch) ":\n"
    "  sub sp, sp, #160\n"
    "  stp x19, x20, [sp, #0]\n"
    "  stp x21, x22, [sp, #16]\n"
    "  stp x23, x24, [sp, #32]\n"
    "  stp x25, x26, [sp, #48]\n"
    "  stp x27, x28, [sp, #64]\n"
    "  stp x29, x30, [sp, #80]\n"
    "  stp d8, d9, [sp, #96]\n"
    "  stp d10, d11, [sp, #112]\n"
    "  stp d12, d13, [sp, #128]\n"
    "  stp d14, d15, [sp, #144]\n"
    "  mov x9, sp\n"
    "  str x9, [x0]\n"
    "  mov sp, x1\n"
    "  ldp x19, x20, [sp, #0]\n"
    "  ldp x21, x22, [sp, #16]\n"
    "  ldp x23, x24, [sp, #32]\n"
    "  ldp x25, x26, [sp, #48]\n"
    "  ldp x27, x28, [sp, #64]\n"
    "  ldp x29, x30, [sp, #80]\n"
    "  ldp d8, d9, [sp, #96]\n"
    "  ldp d10, d11, [sp, #112]\n"
    "  ldp d12, d13, [sp, #128]\n"
    "  ldp d14, d15, [sp, #144]\n"
    "  add sp, sp, #160\n"
    "  ret\n"
    ".globl " SN_SYM(sn_on_stack) "\n" SN_TYPE(sn_on_stack)
    ".p2align 2\n" SN_SYM(sn_on_stack) ":\n"
    "  stp x29, x30, [sp, #-16]!\n"
    "  mov x29, sp\n"
    "  mov sp, x0\n"
    "  mov x0, x2\n"
    "  blr x1\n"
    "  mov sp, x29\n"
    "  ldp x29, x30, [sp], #16\n"
    "  ret\n");
#elif defined(__x86_64__)
#define SN_FRAME 64
#define SN_SAVED_FP 40 // rbp, pushed first, and the return address the call left above it
#define SN_SAVED_PC 48
#define SN_RESUMED_SP 56 // where the switch's caller resumes, past the return address ret pops
__asm__(
    SN_HELD_SECTION "\n"
    ".globl " SN_SYM(sn_switch) "\n" SN_TYPE(sn_switch) SN_SYM(sn_switch) ":\n"
    "  pushq %rbp\n"
    "  pushq %rbx\n"
    "  pushq %r12\n"
    "  pushq %r13\n"
    "  pushq %r14\n"
    "  pushq %r15\n"
    "  movq %rsp, (%rdi)\n"
    "  movq %rsi, %rsp\n"
    "  popq %r15\n"
    "  popq %r14\n"
    "  popq %r13\n"
    "  popq %r12\n"
    "  popq %rbx\n"
    "  popq %rbp\n"
    "  ret\n"
    ".globl " SN_SYM(sn_on_stack) "\n" SN_TYPE(sn_on_stack) SN_SYM(sn_on_stack) ":\n"
    "  pushq %rbp\n"
    "  movq %rsp, %rbp\n"
    "  movq %rdi, %rsp\n"
    "  movq %rdx, %rdi\n"
    "  callq *%rsi\n"
    "  movq %rbp, %rsp\n"
    "  popq %rbp\n"
    "  ret\n");
#else
#error "the Snello runtime has no context switch for this processor"
#endif
// clang-format on

// make_context lays out the frame sn_switch restores at the top of a
// fresh stack, so that the first switch to it returns into entry with
// an aligned stack and every saved register zero. On x86-64 the
// return address sits one word below the top, so that entry begins as
// a called function does, with the stack pointer eight past a
// sixteen-byte boundary; on Windows the vector unit's control words
// start at the values a thread starts with, and set_bounds writes the
// stack's bounds into the frame.
SN_HELD_STATIC static void *make_context(void *top, void (*entry)(void)) {
    uint8_t *sp = (uint8_t *)top - SN_FRAME;
    memset(sp, 0, SN_FRAME);
    ((void **)sp)[SN_SAVED_PC / sizeof(void *)] = (void *)entry; // where the switch returns to
#if defined(_WIN32) && defined(__x86_64__)
    *(uint32_t *)(sp + SN_SAVED_CSR) = SN_MXCSR_START;
    *(uint16_t *)(sp + SN_SAVED_CSR + 4) = SN_X87_START;
#endif
    return sp;
}

#if defined(_WIN32)
// set_bounds writes into a fresh frame the bounds of the stack it is on:
// its top, the lowest address its task may use and where its memory
// begins, which the switch puts in the thread's information block.
SN_HELD_STATIC static void set_bounds(void *sp, void *top, void *limit, void *reservation) {
    void **b = (void **)((uint8_t *)sp + SN_SAVED_BOUNDS);
    b[0] = reservation;
    b[1] = limit;
    b[2] = top;
}

// frame_bounds reads the bounds of the stack of a context switched
// away from, from its frame.
SN_HELD_STATIC static sn_stack_bounds frame_bounds(void *sp) {
    void **b = (void **)((uint8_t *)sp + SN_SAVED_BOUNDS);
    return (sn_stack_bounds){ b[2], b[1], b[0] };
}
#endif

typedef struct {
    void *p;
    void (*drop)(void *);
} guard;

struct sn_task {
    void *sp;        // the saved context, while not running
    void *stack;     // the reservation, NULL for the main task
    int stack_class; // of the stack
    void *large;     // the guarded stack a panic on its small stack moved to, kept until it ends
    _Atomic(struct preempt_block *) preempt; // its registers while preempted, and its landing stack, once it has one
#ifdef SN_DEBUG
    uint32_t bound; // what the analysis of the linked program bounded its stack at, which a debug build checks it to as
                    // it ends
#endif
    void (*run)(void *);
    void *arg; // the arguments run is given: a copy of the spawn's, in args or on the heap
    _Atomic int state;
    bool unwinding;       // beside state, in the room its alignment leaves, as each small field below is
    struct sn_task *next; // on a queue
    sn_scope_link scope;  // the scope it is in, scope.c's: beside next, early in the record, which every park and
                          // resume touches, so that a wait reads it with little more brought in
    guard *guards;        // the values a panic drops, innermost last: few, then a block of the system's
    int64_t nguards, guardcap;
    guard few[few_guards];
    sn_string unwound; // the message of the panic being unwound to a mark, for a drop that panics meanwhile to report
    struct sn_mark *marks; // the marks a panic unwinds to, innermost first; none for a program's task
    sn_chan *waits_on;     // the channel the task waits on, while it does, for the deadlock report
    int waits_dir;         // and whether to send or to receive on it, or both for a select
    int ntrace;            // how many frames trace holds
    sn_string places[3];   // the place of the test call being made, testing.c's: the task's own, so that parallel tests
                           // do not share it
    uintptr_t *trace; // the frames of its last panic, kept for a test's runner under a catch that keeps them, or NULL
    _Alignas(SN_MAX_ALIGN) uint8_t args[few_args]; // room for the arguments, aligned for any program value: read once,
                                                   // as the task starts, so last
#ifdef SN_DEBUG
    struct sn_task *debug_next; // the next older task held, on the list a debugger's script walks
    struct sn_task *debug_prev; // the next newer task held, for the unlink as its record is freed
#endif
};

// A mark is where a panic on the task stops: the jump back, the guard
// count at the mark, so that the values made since are dropped and the
// ones before are kept, and the message, for the caller of sn_catch.
// It lives in sn_catch's frame on the task's stack, below the frames
// the panic abandons.
typedef struct sn_mark {
    sn_jmp_buf jump;
    int64_t nguards;
    sn_string *out; // where the panic's message goes, the caller's
    bool report;    // print the panic as an uncaught one is printed, before the unwinding
    struct sn_mark *prev;
} sn_mark;

typedef struct sn_task task;

// A worker: its scheduler context, the queue of runnable tasks it
// owns, and for the main thread the scheduler stack it takes when the
// main task first parks. runnext is Go's: the task this worker's
// running task last woke, which the worker runs next, before its
// queue, so that two tasks handing values back and forth stay on one
// thread, one switch apart, where a wake through the queue would wake
// another thread to run the task there.
// SN_OWN_LINE puts what it marks on a cache line of its own: a worker's
// record, so that the words its thread writes as tasks move share no
// line with a neighbour's, whatever address the allocation lands on,
// and the counts below.
#define SN_OWN_LINE __attribute__((aligned(SN_CACHE_LINE)))

typedef struct worker {
    void *sched_sp;
    void *sched_stack;
#if defined(_WIN32)
    _Atomic(HANDLE) thread; // for the monitor to suspend, written by the thread as it starts and read by the monitor
    _Atomic(void *) teb;    // the thread's information block, whose stack bounds the monitor reads and writes
#else
    _Atomic(pthread_t) thread; // for the monitor to signal, written by the thread as it starts
#endif
    _Atomic(struct preempt_block *) spare; // a block the monitor keeps ready, which the preemption takes
    int preempt_tries;                     // attempts since the worker last switched, the monitor's
#if defined(_WIN32)
    _Alignas(16) CONTEXT restoring; // a preempted task's registers as they are put back, off its block
#endif
    task *last;                // the task that switched to the scheduler, to settle
    _Atomic(task *) runnext;   // the task its running task woke last, which it runs next: put with an exchange, taken
                               // with a compare-and-swap by the worker, a thief or the monitor
    _Atomic uint64_t switches; // the tasks it has switched to, counted by its own thread: a slot whose worker has
                               // switched to no task since the monitor's last look is stale
    _Atomic int64_t running;   // the tasks spawned on it less those that ended on it, by its own thread: the sum
                               // over the workers is the tasks running
    // The queue is Go's runq: a ring the worker's own thread alone
    // writes at the tail and takes from at the head, and other workers
    // take half of from the head, each take a compare-and-swap of the
    // head and no lock; a ring that is full sends half to the inject
    // queue. The counts only grow, so a slot's index is its count
    // modulo the size.
    _Atomic uint32_t qhead, qtail;
    _Atomic(task *) ring[ring_size]; // read by a stealer while the owner writes a neighbour: relaxed atomics make that
                                     // formally clean at no cost
    // The stacks of its tasks that ended, kept for its next spawns, as
    // Go's stack cache sits per P over the heap: taken and given back
    // by the worker's thread alone, with the pool behind it under its
    // lock, half the cache at a time.
    void *stacks[stack_classes][stack_cache];
    int nstacks[stack_classes];
    // The records of its ended tasks, each with its stack, linked through
    // next: what a spawn on this worker takes first.
    task *dead[stack_classes];
    int ndead[stack_classes];
    _Atomic uint32_t park;    // the word it sleeps on while it waits for work: set by the wake that takes it off the
                              // waiting workers, and cleared by it as it goes on
    struct worker *idle_next; // the worker that waited before it, under idle_mu
    int turns;       // tasks it runs before it looks at what comes from outside again: the other wheels' timers and the
                     // inject queue
    int streak;      // tasks it has run out of its runnext slot in a row
    int64_t owes_at; // a deadline it may have left no one to wake for: it sees to it before its next task, or keeps
                     // time itself in its wait; the greatest time for none
    bool owes_watch; // it left the keeper's wait while tasks wait on sockets: before its next task it wakes a worker to
                     // watch them, if none does
    bool owes_poll;  // it left the keeper's wait on the poller's events: its next look at the poller takes them
                     // whether or not a task waits on a socket, as they would end the next keeper's wait at once
#ifdef SN_DEBUG
    task *ended[ended_kept]; // its tasks that ended, kept unfreed, the oldest freed first
    int64_t nended;
#endif
} SN_OWN_LINE worker;

// The block a preempted task keeps: its registers as the system saved
// them when it was interrupted, put back when it resumes; errno, and
// on Windows the last error and its stack's bounds; and the landing
// stack, the task's while it is off its own.
typedef struct preempt_block {
    bool restoring; // the landing asks the handler to put the registers back
    int saved_errno;
#if defined(_WIN32)
    sn_stack_bounds bounds;
    DWORD last_error;
    _Alignas(16) CONTEXT context;
#elif defined(__APPLE__)
    size_t size;                                // of the machine context, uc_mcsize
    _Alignas(16) uint8_t context[context_most]; // the machine context, whichever form the processor's is
#elif defined(__x86_64__)
    gregset_t regs;
    size_t fp_size;
    _Alignas(64) uint8_t fp[context_most]; // the FXSAVE image and what XSAVE adds, as the kernel laid them out
#else
    mcontext_t context; // the registers and, in its reserved room, the vector unit's state
#endif
    _Alignas(16) uint8_t landing[landing_bytes];
} preempt_block;

static _Atomic int64_t preempted; // tasks taken off their workers, for a test

static worker *workers;      // room for workers_most of them, so that no worker's address moves
static int workers_most;     // what the monitor may grow the workers to
static _Atomic int nworkers; // the workers started, the main thread's among them: only grows

SN_HELD_STATIC static int workers_now(void) { return atomic_load_explicit(&nworkers, memory_order_relaxed); }
static sn_mutex start_mu = SN_MUTEX_INIT;
static bool started;

static _Thread_local worker *self_worker;
static _Thread_local task *current;

// A task may resume on another thread after any switch, and a compiler
// may compute the address of a thread-local once and keep it across a
// call, as it does for glibc's, which is a register read and an add:
// the task would then read the thread it left. Every read a task makes
// of the thread's own state goes through these, which the compiler can
// neither inline nor merge, since each one may write memory as far as
// it knows. The scheduler's own contexts never move, and read the
// variables directly.
SN_HELD_STATIC static __attribute__((noinline)) worker *my_worker(void) {
    __asm__ volatile("" ::: "memory");
    return self_worker;
}

SN_HELD_STATIC static __attribute__((noinline)) task *my_task(void) {
    __asm__ volatile("" ::: "memory");
    return current;
}

static task main_task; // the main thread's own stack and context

#ifdef SN_DEBUG
// debug_tasks is every task of a debug build whose record is still
// held, newest first, for a debugger's script: a task is linked once,
// as it is made, and unlinked as its record is freed, so that the
// script reads no freed memory, nor a record the allocator gave to a
// later task, which would link it twice and close the list in a cycle.
// debug_mu orders the links; the debugger reads the list while the
// program is stopped.
static task *_Atomic debug_tasks;
static sn_qlock debug_mu; // free as zeroed

SN_HELD_STATIC static void debug_list(task *t) {
    sn_qlock_take(&debug_mu);
    task *head = atomic_load_explicit(&debug_tasks, memory_order_relaxed);
    t->debug_next = head;
    t->debug_prev = NULL;
    if (head != NULL) {
        head->debug_prev = t;
    }

    atomic_store_explicit(&debug_tasks, t, memory_order_release);
    sn_qlock_give(&debug_mu);
}

SN_HELD_STATIC static void debug_unlist(task *t) {
    sn_qlock_take(&debug_mu);
    if (t->debug_prev != NULL) {
        t->debug_prev->debug_next = t->debug_next;
    } else {
        atomic_store_explicit(&debug_tasks, t->debug_next, memory_order_release);
    }

    if (t->debug_next != NULL) {
        t->debug_next->debug_prev = t->debug_prev;
    }

    sn_qlock_give(&debug_mu);
}

// The table of sn_debugger_table, kept by the linker though nothing of
// the program reads it.
__attribute__((used)) const sn_debugger_table sn_debugger = {
    .tasks = &debug_tasks,
    .next = offsetof(task, debug_next),
    .sp = offsetof(task, sp),
    .state = offsetof(task, state),
    .run = offsetof(task, run),
    .saved_fp = SN_SAVED_FP,
    .saved_pc = SN_SAVED_PC,
    .resumed_sp = SN_RESUMED_SP,
    .stack_room = guarded_bytes,
    .running = RUNNING,
    .runnable = RUNNABLE,
    .parking = PARKING,
    .parked = PARKED,
    .done = DONE,
    .woken = WOKEN,
};
#endif

// The counts the workers write as tasks move, each on a cache line of
// its own, so that a worker writing one does not take from the others
// the line of another, as Go's scheduler pads its own, and no variable
// of another file's, read on every timer or every switch, lands beside
// them by the order the files are linked in.
static struct {
    SN_OWN_LINE _Atomic int64_t moving; // tasks the monitor is moving out of a slot, until each is on the inject queue
    SN_OWN_LINE _Atomic int64_t
        outside; // wakes promised by threads that are not workers: the poller's, the timers', a resolver's
    SN_OWN_LINE _Atomic int64_t injected;  // tasks on the inject queue
    SN_OWN_LINE _Atomic int64_t displaced; // tasks on the displaced queue, taken off their workers
    SN_OWN_LINE _Atomic int idle;          // workers waiting for work
    SN_OWN_LINE _Atomic int spinning;      // workers looking for work before they wait, at most one
    SN_OWN_LINE _Atomic int
        waiting; // workers blocked in the idle wait for work alone, the keeper not among them, under idle_mu
} hot;

static sn_qlock idle_mu;  // free as zeroed; taken after a channel's or a lock's, never before one, since a wake
                          // under those takes it
static worker *idle_head; // the workers blocked waiting for work, the last to wait first, under idle_mu

// Who fires the timers, which timer.c keeps in a wheel per worker. No
// thread is kept for them: a timer is fired by a worker, on its way
// from one task to the next, so that the task it wakes runs next on the
// thread that fired it and no other thread is woken for it.
//
// A worker with tasks to run fires its own wheel's as they come due,
// and looks at the other wheels once in outside_turns of them. A worker
// with nothing to run fires what is due before it looks for work, and
// while it spins. One of the workers that wait for work keeps time: the
// keeper sleeps until the earliest deadline of any wheel and no longer,
// as the one thread of a scheduler without threads sleeps until its
// earliest, and the others wait for work alone. When every worker is
// busy with tasks that do not let go, no task a timer woke could run,
// and the monitor fires what is due at each of its looks, so that a
// task polling a timer's channel without a wait still finds its time
// there.
//
// keeping says a worker is the keeper and wake_at the time it wakes at,
// the greatest when none keeps; both are written under idle_mu, and
// keeping is read without it by a worker deciding whether to look at
// the sockets' poller, which the keeper waits in. Whoever
// starts a timer before wake_at wakes the keeper to read the deadlines
// again, or a waiting worker to be the keeper: it writes the deadline
// before it reads how many are idle, and a worker counts itself idle
// before it reads the deadlines, so one of the two sees the other. A
// keeper that wakes looks at the deadlines again before it waits or
// runs a task, so wake_at rises only when someone is looking.
static _Atomic int64_t wake_at = INT64_MAX;
static _Atomic bool keeping;
static void keeper_wake(void);
static worker *idle_take(int k);
static void idle_wake(worker *taken);
static void add_worker(void);
static void preempt_start(void);
static void preempt_thread_start(worker *w);
static void preempt_worker(worker *w);

// sn_sched_recheck has the waiting workers look again at whether any
// task can still be woken, when every worker waits.
SN_HELD void sn_sched_recheck(void) {
    if (atomic_load(&hot.idle) != workers_now()) {
        return;
    }

    sn_qlock_take(&idle_mu);
    worker *taken = idle_take(workers_now()); // every waiting worker
    if (keeping) {
        keeper_wake();
    }

    sn_qlock_give(&idle_mu);
    idle_wake(taken);
}

// sn_outside counts n more wakes promised by a thread that is not a
// worker, the poller's or a resolver's (lookup.c), or n fewer once one
// has reached its task. When none is left and every worker is idle, the
// idle workers look again at whether any task can still be woken. While a worker is busy there is no one to
// tell: it counts itself idle before it reads this count, and this
// reads the idle count after it changes its own, so one of the two
// sees the other.
SN_HELD void sn_outside(int64_t n) {
    if (atomic_fetch_add(&hot.outside, n) + n == 0) {
        sn_sched_recheck();
    }
}

// queued_now is how many tasks wait on the workers' rings and the
// inject queue, read from each ring's counts: no count of them all is
// kept, since every worker would write it on every push and every pop.
SN_HELD_STATIC static int64_t queued_now(void) {
    int64_t n = atomic_load(&hot.injected) + atomic_load(&hot.displaced);
    for (int i = 0; i < workers_now(); i++) {
        n += (int64_t)(atomic_load(&workers[i].qtail) - atomic_load(&workers[i].qhead));
    }

    return n;
}

// deadlocked reports, to an idle worker holding idle_mu, that no task
// can ever run again: every worker idle, so every task is parked or
// done, nothing in a runnext slot or on a queue, and no timer or socket
// left to wake one. An idle worker's slot is empty, since only its own
// thread fills it and it empties it before it sleeps, so with every
// worker idle a task to run can be in a slot only while the monitor is
// moving it to the inject queue, which it counts as moving from before
// the task leaves the slot until it is on the queue, and the counts are
// read here in the order it changes them. The main task is among the parked, since the
// main thread is a worker only once the main task has parked, so the
// program cannot end. Go's runtime stops such a program with the same
// message.
SN_HELD_STATIC static bool deadlocked(void) {
    // Whoever keeps a promise queues the task first and takes the
    // promise off its count second, and the monitor queues a task it
    // moves out of a slot before it takes it off the count of moves, so
    // the counts are read in the other order: a promise or a move read
    // gone means the task it put on a queue is counted there by the time
    // the queue is read. A slot holds a task only while its worker runs
    // one, so every worker idle means every slot empty, but for a task
    // the monitor is moving.
    return atomic_load(&hot.idle) == workers_now() && atomic_load(&hot.outside) == 0 && sn_timers_pending() == 0 &&
           atomic_load(&hot.moving) == 0 && queued_now() == 0;
}

// Freed stacks are pooled, a list a class, since making them is the
// costly part of a spawn: a pooled stack links to the next through its
// top word, which its task touched. The guarded pool holds
// guarded_pooled stacks and unmaps those it has no room for; a small
// stack shares its pages with its neighbours and stays pooled, so that
// a small class's pool holds what the most tasks of the class alive at
// once used, as a pool of stacks in any runtime does. The pool's lock
// is the runtime's own, which spins briefly and then sleeps: a lock that
// only spun was measured in its place and made a spawn a quarter slower
// by the median of eleven runs, the spinners costing the workers the
// cores the mutex's sleepers gave them, and the system's mutex put the
// spawning thread to sleep in the kernel for a twelfth of its time.
static sn_qlock pool_mu;          // free as zeroed
static void *pool[stack_classes]; // the first pooled stack of each class
static int pooled[stack_classes];
#ifndef SN_DEBUG
static task *dead_pool[stack_classes]; // the first pooled record with its stack, of each class, under pool_mu
static int dead_pooled[stack_classes];
#endif
static uint8_t *block_next[stack_classes]; // the next stack of the block each class takes from, under pool_mu
static size_t block_left[stack_classes];   // and how many stacks it has left

SN_HELD_STATIC static void pool_take(void) { sn_qlock_take(&pool_mu); }

SN_HELD_STATIC static void pool_give(void) { sn_qlock_give(&pool_mu); }

SN_HELD_STATIC static size_t class_size(int c) { return c == guarded ? stack_size : (size_t)small_least << c; }

// stack_class returns the class of a task whose stack sn_spawn was
// given the bound of: the smallest small class that holds it, or the
// guarded class for SN_UNBOUNDED or a bound past the small classes.
SN_HELD_STATIC static int stack_class(uint32_t bound) {
    for (int c = 0; c < small_classes; c++) {
        if (bound <= class_size(c)) {
            return c;
        }
    }

    return guarded;
}

SN_HELD_STATIC static void **pool_link(void *s, int c) { return (void **)((uint8_t *)s + class_size(c)) - 1; }

SN_HELD_STATIC static void pool_push(void *s, int c) {
    *pool_link(s, c) = pool[c];
    pool[c] = s;
    pooled[c]++;
}

SN_HELD_STATIC static void *pool_pop(int c) {
    void *s = pool[c];
    pool[c] = *pool_link(s, c);
    pooled[c]--;
    return s;
}

// pool_keeps reports whether the pool of class c has room for a stack.
SN_HELD_STATIC static bool pool_keeps(int c) { return c != guarded || pooled[c] < guarded_pooled; }

// unmap_guarded gives a guarded stack back to the system. Windows frees
// only a reservation whole, and a guarded stack is a part of a block's,
// so there its pages are decommitted and its addresses stay reserved,
// for the block they belong to.
SN_HELD_STATIC static void unmap_guarded(void *s) {
#if defined(_WIN32)
    (void)VirtualFree((uint8_t *)s + sn_page_size(), stack_size - sn_page_size(), MEM_DECOMMIT);
#else
    munmap(s, stack_size);
#endif
}

#ifndef SN_DEBUG // a debug build keeps a worker's last ended_kept ended records, then frees them, and reuses none
// dead_keeps reports whether the pool of class c has room for a record
// with its stack: the guarded ones are bounded as their stacks are.
SN_HELD_STATIC static bool dead_keeps(int c) { return c != guarded || dead_pooled[c] < guarded_pooled; }

// dead_take gives the record of an ended task of stack class c, with
// its stack, from w's cache, refilled with a batch from the pool when it
// is empty; NULL when there is none, or for a thread that is no worker.
SN_HELD_STATIC static task *dead_take(worker *w, int c) {
    if (w == NULL) {
        return NULL;
    }

    if (w->ndead[c] == 0) {
        pool_take();
        while (dead_pooled[c] > 0 && w->ndead[c] < dead_batch) {
            task *t = dead_pool[c];
            dead_pool[c] = t->next;
            dead_pooled[c]--;
            t->next = w->dead[c];
            w->dead[c] = t;
            w->ndead[c]++;
        }

        pool_give();
        if (w->ndead[c] == 0) {
            return NULL;
        }
    }

    task *t = w->dead[c];
    w->dead[c] = t->next;
    w->ndead[c]--;
    return t;
}

// dead_keep keeps the record of the ended task t, with its stack, in
// w's cache; a full cache sends a batch to the pool, and a guarded
// record the pool has no room for is torn down.
SN_HELD_STATIC static void dead_keep(worker *w, task *t) {
    int c = t->stack_class;
    if (w->ndead[c] == dead_cache) {
        pool_take();
        while (w->ndead[c] > dead_cache - dead_batch && dead_keeps(c)) {
            task *x = w->dead[c];
            w->dead[c] = x->next;
            w->ndead[c]--;
            x->next = dead_pool[c];
            dead_pool[c] = x;
            dead_pooled[c]++;
        }

        pool_give();
        while (w->ndead[c] > dead_cache - dead_batch) { // guarded records the pool has no room for
            task *x = w->dead[c];
            w->dead[c] = x->next;
            w->ndead[c]--;
            unmap_guarded(x->stack);
            sn_heap_free(x);
        }
    }

    t->next = w->dead[c];
    w->dead[c] = t;
    w->ndead[c]++;
}
#endif

// stack_new gives a stack of class c from w's cache, which is refilled
// with half a cache from the pool when it is empty, or from the pool
// alone for a thread that is no worker, and takes a new one from its
// class's block when both are empty. It returns NULL with the reason in
// *failure when the system has no memory to map, rather than panic,
// since the panic itself takes a stack with it.
SN_HELD_STATIC static void *stack_new(worker *w, int c, const char **failure) {
    if (w != NULL && w->nstacks[c] > 0) {
        return w->stacks[c][--w->nstacks[c]];
    }

    pool_take();
    if (w != NULL) {
        while (pooled[c] > 0 && w->nstacks[c] < stack_cache / 2) {
            w->stacks[c][w->nstacks[c]++] = pool_pop(c);
        }
    } else if (pooled[c] > 0) {
        void *s = pool_pop(c);
        pool_give();
        return s;
    }

    if (w != NULL && w->nstacks[c] > 0) {
        pool_give();
        return w->stacks[c][--w->nstacks[c]];
    }

    size_t size = class_size(c);
    if (block_left[c] == 0) { // a new block: under the lock, so that no two threads make one at once
        size_t n = c == guarded ? block_stacks * stack_size : small_block;
#if defined(_WIN32)
        // A small class's block is committed whole, as the other systems
        // make it writable; a guarded class's is reserved, each stack
        // committed but for its guard page as it is taken.
        void *b = VirtualAlloc(NULL, n, c == guarded ? MEM_RESERVE : MEM_RESERVE | MEM_COMMIT,
                               c == guarded ? PAGE_NOACCESS : PAGE_READWRITE);
        if (b == NULL) {
#else
        int flags = MAP_PRIVATE | MAP_ANONYMOUS;
#ifdef MAP_NORESERVE
        flags |= MAP_NORESERVE; // made writable without the system setting memory aside for it
#endif
        void *b = mmap(NULL, n, c == guarded ? PROT_NONE : PROT_READ | PROT_WRITE, flags, -1, 0);
        if (b == MAP_FAILED) {
#endif
            pool_give();
            *failure = "cannot reserve a task's stack";
            return NULL;
        }

        block_next[c] = b;
        block_left[c] = n / size;
    }

    uint8_t *s = block_next[c];
    block_next[c] += size;
    block_left[c]--;
    pool_give();
    if (c != guarded) {
        return s;
    }

    size_t page = sn_page_size();
#if defined(_WIN32)
    if (VirtualAlloc(s + page, size - page, MEM_COMMIT, PAGE_READWRITE) ==
        NULL) { // the guard page stays reserved, never committed
        *failure = "cannot reserve a task's stack: the system's commit limit is reached";
        return NULL;
    }
#else
    if (mprotect(s + page, size - page, PROT_READ | PROT_WRITE) !=
        0) { // the stack and its guard become mappings of their own, and the system's count of them is finite
        *failure = "cannot reserve a task's stack: the system's limit of memory mappings is reached";
        return NULL;
    }
#endif

    return s;
}

// stack_take is stack_new for a spawn and a scheduler, which panic when
// the system has no memory to map.
SN_HELD_STATIC static void *stack_take(worker *w, int c) {
    const char *failure = NULL;
    void *s = stack_new(w, c, &failure);
    if (s == NULL) {
        sn_panic(SN_STR(failure, (int64_t)strlen(failure)));
    }

    return s;
}

// stack_free keeps a stack of class c in w's cache; a full cache sends
// half to the pool under one lock, and a guarded stack the pool has no
// room for is unmapped. A thread that is no worker gives to the pool
// alone.
SN_HELD_STATIC static void stack_free(worker *w, void *s, int c) {
    if (w != NULL) {
        if (w->nstacks[c] == stack_cache) {
            pool_take();
            while (w->nstacks[c] > stack_cache / 2 && pool_keeps(c)) {
                pool_push(w->stacks[c][--w->nstacks[c]], c);
            }

            pool_give();
            while (w->nstacks[c] > stack_cache / 2) { // guarded stacks alone: a small class's pool keeps every one
                unmap_guarded(w->stacks[c][--w->nstacks[c]]);
            }
        }

        w->stacks[c][w->nstacks[c]++] = s;
        return;
    }

    pool_take();
    if (pool_keeps(c)) {
        pool_push(s, c);
        pool_give();
        return;
    }

    pool_give();
    unmap_guarded(s);
}

// sn_self returns the running task; on the main thread before any
// task exists, it makes the main task from the thread itself.
SN_HELD sn_task *sn_self(void) {
    task *t = my_task();
    if (t == NULL) {
        atomic_store(&main_task.state, RUNNING);
        current = t = &main_task;
#ifdef SN_DEBUG
        static atomic_bool listed; // made on each thread that asks before it runs a task, listed once
        if (!atomic_exchange(&listed, true)) {
            debug_list(t);
        }
#endif
    }

    return t;
}

// sn_task_waits records that the task t, the running one, waits on the
// channel c, to send or to receive as dir says, or waits on none for
// a NULL c: what the deadlock report says of the main task. The task
// is the caller's, which read it for its waiter already.
SN_HELD void sn_task_waits(sn_task *t, sn_chan *c, int dir) {
    t->waits_on = c;
    t->waits_dir = dir;
}

// sn_task_places returns the running task's slots for the place of a
// test's call, which testing.c fills and reads.
SN_HELD sn_string *sn_task_places(void) { return sn_self()->places; }

SN_HELD sn_scope_link *sn_task_scope(sn_task *t) { return &t->scope; }

// The loads are relaxed, and need no more: a cancel marks the scope and
// then wakes the task, and the park that takes that wake does so with a
// compare-and-swap of the task's state, which orders the mark before the
// task's next look; a look that comes before the wake and misses the
// mark is followed by that wake. An acquire load would wait, on arm64,
// for the stores the wait has just made to be seen, on every wait: it
// made a pipeline of buffered channels a quarter slower.
SN_HELD int sn_cancel_state(sn_task *t) {
    sn_scope *s = atomic_load_explicit(&t->scope.scope, memory_order_relaxed);
    if (s == NULL) {
        s = &sn_root; // a task in no scope is the program's, which an interrupt cancels
    }

    return atomic_load_explicit(&s->state, memory_order_relaxed);
}

SN_HELD int sn_cancel_wait_state(sn_task *t) {
    sn_scope *s = atomic_load_explicit(&t->scope.scope, memory_order_acquire); // an Add's write of it, and of enlisted
    if (!t->scope.enlisted) {
        sn_scope_enlist_self(t, s); // the lock taken and let go, so that the mark of a cancel that walked the list
                                    // without the task is read below; the root's for a task in no scope
        s = atomic_load_explicit(&t->scope.scope, memory_order_acquire); // a group may have taken it meanwhile
    }

    if (s == NULL) {
        s = &sn_root;
    }

    return atomic_load_explicit(&s->state, memory_order_relaxed);
}

// take_back claims a wait for its task, cancelled, unless an operation
// claimed it first. It is out of line and cold, as every cancelled path
// of the waits is, so that the code around a wait is what it was before
// a cancel could end one.
SN_HELD_STATIC static __attribute__((noinline, cold)) bool take_back(sn_wait_state *ws) {
    int open = 0;
    return atomic_compare_exchange_strong(&ws->claimed, &open, 1);
}

// sn_handoff_cancellable is sn_handoff_wait for a wait a cancel may end:
// it reports true once the waker is done with the task, and false, at
// once, when the task's scope is cancelled while the handoff is still
// open, so that the caller takes its wait back; it is here, beside the
// task's record, so that a task in no scope looks at one word of its
// own before each park and calls nothing more. sn_await_cancellable is
// it for a channel's wait, which a cancel takes back by the claim an
// operation makes, and which then waits for the operation that claimed
// it first, if one did, and is kept out of line: inlined by the
// link-time optimizer into a channel's receive, the wait made every
// receive save more registers and build a larger frame, the ones that
// find a value too, and a pipeline of buffered channels ran a quarter
// slower. A sleep, which always waits, gains by the inlining, as it
// does of sn_handoff_wait: kept out of line, it made a wave of sleeps a
// tenth slower.
SN_HELD bool sn_handoff_cancellable(sn_handoff *h, sn_task *t) {
    for (;;) {
        int at = atomic_load_explicit(h, memory_order_acquire);
        if (at == SN_HANDOFF_WOKEN) {
            return true;
        }

        if (at != SN_HANDOFF_OPEN) {
            sn_spin_hint(); // the claim is at the task: its few instructions
            continue;
        }

        if (sn_cancel_wait_state(t) != SN_SCOPE_LIVE) {
            return false;
        }

        sn_park(); // a cancel's wake, the waker's, or one kept from an earlier wait
    }
}

SN_HELD bool sn_await_cancellable(sn_task *t, sn_wait_state *ws) {
    if (sn_handoff_cancellable(&ws->done, t)) {
        return true;
    }

    if (take_back(ws)) {
        return false; // no operation can take the wait now
    }

    sn_handoff_wait(&ws->done); // an operation took it first
    return true;
}

static int handed; // spins given to woken workers not yet back from the wait, under idle_mu

// idle_take takes the k workers that waited last off the waiting
// workers, under idle_mu, and returns them linked, the first to wake
// first; each is woken by its own word once the lock is given back, so
// that no wake reaches a worker already on its way and no woken worker
// waits for the lock the waker holds.
SN_HELD_STATIC static worker *idle_take(int k) {
    worker *taken = NULL;
    for (; k > 0 && idle_head != NULL; k--) {
        worker *w = idle_head;
        idle_head = w->idle_next;
        w->idle_next = taken;
        taken = w;
        atomic_fetch_sub(&hot.waiting, 1);
    }

    return taken;
}

SN_HELD_STATIC static void idle_wake(worker *taken) {
    for (worker *w = taken; w != NULL;) {
        worker *next = w->idle_next; // read before the wake: woken, w may wait again and link elsewhere
        atomic_store_explicit(&w->park, 1, memory_order_release);
        sn_word_wake(&w->park);
        w = next;
    }
}

// idle_give gives idle_mu back and wakes k of the workers blocked
// waiting for work.
SN_HELD_STATIC static void idle_give(int k) {
    worker *taken = idle_take(k);
    sn_qlock_give(&idle_mu);
    idle_wake(taken);
}

// wake_idle wakes one worker blocked waiting for work, if one is and
// no worker is spinning, and makes the one it wakes the spinner before
// it wakes: every push until the woken worker runs sees a spinner and
// wakes no other, where before a burst of spawns woke a worker per
// spawn while one was on its way. The worker that returns from the
// wait with a spin handed out takes it, and gives it up once it has
// found work or spun its time out; a worker a broadcast or a spurious
// return woke finds none handed and takes nothing, so there is one
// spinner at a time still. A signal goes only to a worker counted
// blocked, under the lock it blocks with, so a spin is never handed to
// no one. The keeper is woken only when no other worker waits, so that
// the timers keep their keeper while there is another to run the work.
SN_HELD_STATIC static void wake_idle(void) {
    if (atomic_load(&hot.idle) == 0 || atomic_load(&hot.spinning) != 0) {
        return;
    }

    sn_qlock_take(&idle_mu);
    int expected = 0;
    int k = 0;
    if ((atomic_load(&hot.waiting) > 0 || keeping) && atomic_compare_exchange_strong(&hot.spinning, &expected, 1)) {
        handed++;
        int waiting = atomic_load(&hot.waiting);
        if (waiting > 0) {
            k = 1;
        } else {
            keeper_wake();
        }
    }

    idle_give(k);
}

// wake_waiting wakes a worker for each of n tasks just queued at once,
// as many as are blocked waiting for work and not woken already,
// and hands none of them the spin: each finds a task on the queues and
// runs it, so that tasks made ready together run side by side. Woken
// through the spin, one at a time, a worker took several of them and
// ran them in turn while the others slept. With none to signal it
// wakes as wake_idle does, so that a spinner or the keeper still comes.
SN_HELD_STATIC static void wake_waiting(int n) {
    if (atomic_load(&hot.idle) == 0) {
        return;
    }

    sn_qlock_take(&idle_mu);
    int free = atomic_load(&hot.waiting);
    if (free <= 0) {
        sn_qlock_give(&idle_mu);
        wake_idle();
        return;
    }

    idle_give(n < free ? n : free);
}

static bool push_slow(worker *w, task *t, uint32_t h, uint32_t tl);

// The queues. push puts a task on the ring of w, the worker whose
// thread runs it, and tells an idle worker; a worker with nothing in
// its own ring steals half of another worker's. A full ring sends
// half of itself with the task to the inject queue, as Go's runqput
// does to its global queue.
SN_HELD_STATIC static void push(worker *w, task *t) {
    for (;;) {
        uint32_t h = atomic_load_explicit(&w->qhead, memory_order_acquire);
        uint32_t tl = atomic_load_explicit(&w->qtail, memory_order_relaxed);
        if (tl - h < ring_size) {
            atomic_store_explicit(&w->ring[tl % ring_size], t, memory_order_relaxed);
            atomic_store(&w->qtail, tl + 1); // the slot is written before a taker sees the count, and the count
                                             // before the push reads who is idle: sequentially consistent, as the
                                             // idle worker's count of itself and its look at the rings are, so
                                             // one side sees the other
            break;
        }

        if (push_slow(w, t, h, tl)) {
            break;
        }
    }

    wake_idle(); // a spinner looks at the rings, and one that stops looks once more after it stops
}

// The inject queue holds the tasks woken by a thread that is not a
// worker, the poller's or the timers', as Go's global run queue holds
// what its netpoller and timers make ready, and the tasks the monitor
// moves out of a runnext slot. Every worker looks at it
// after its own queue, and a spinning worker looks at it alone: a task
// woken from outside is what a spin is for, since only it would
// otherwise cost a thread woken to run it, and work the workers make
// for one another stays where it was made.
static sn_qlock inject_mu; // free as zeroed
static task *inject_head, *inject_tail;

// The displaced queue, under the same lock: the tasks taken off their
// workers, which run after the inject queue's, since a task woken from
// outside waits for no task that held a core, and before a worker's
// own ring once in outside_turns, so that they are not starved by a
// stream of wakes; Go's runtime puts a preempted goroutine on its
// global queue, which its workers look at once in 61 turns too.
static task *displaced_head, *displaced_tail;

// queue_put puts t at the tail of the queue head and tail describe,
// under inject_mu.
SN_HELD_STATIC static void queue_put(task **head, task **tail, task *t) {
    t->next = NULL;
    if (*tail != NULL) {
        (*tail)->next = t;
    } else {
        *head = t;
    }

    *tail = t;
}

SN_HELD_STATIC static void inject(task *t) {
    sn_qlock_take(&inject_mu);
    queue_put(&inject_head, &inject_tail, t);
    sn_qlock_give(&inject_mu);
    atomic_fetch_add(&hot.injected, 1);
    wake_idle(); // a spinner stops spinning before it looks once more, and the count and the stop are sequentially
                 // consistent, so one of the two sees the other
}

// displace puts a task taken off its worker on the displaced queue.
SN_HELD_STATIC static void displace(task *t) {
    sn_qlock_take(&inject_mu);
    queue_put(&displaced_head, &displaced_tail, t);
    sn_qlock_give(&inject_mu);
    atomic_fetch_add(&hot.displaced, 1);
    wake_idle();
}

// inject_all puts the n tasks ts on the inject queue under one taking
// of its lock, in their order, and wakes a worker for each.
SN_HELD_STATIC static void inject_all(task **ts, int n) {
    for (int i = 0; i < n; i++) {
        ts[i]->next = i + 1 < n ? ts[i + 1] : NULL;
    }

    sn_qlock_take(&inject_mu);
    if (inject_tail != NULL) {
        inject_tail->next = ts[0];
    } else {
        inject_head = ts[0];
    }

    inject_tail = ts[n - 1];
    sn_qlock_give(&inject_mu);
    atomic_fetch_add(&hot.injected, n);
    wake_waiting(n); // as inject's wake: a worker counts itself idle before it looks at the queues
}

// push_slow moves half of w's full ring, with t after it, to the
// inject queue, and reports whether it did; a stealer that took from
// the ring meanwhile makes it fail, and push tries the ring again.
SN_HELD_STATIC static bool push_slow(worker *w, task *t, uint32_t h, uint32_t tl) {
    uint32_t n = (tl - h) / 2;
    task *batch[ring_size / 2 + 1];
    for (uint32_t i = 0; i < n; i++) {
        batch[i] = atomic_load_explicit(&w->ring[(h + i) % ring_size], memory_order_relaxed);
    }

    if (!atomic_compare_exchange_strong_explicit(&w->qhead, &h, h + n, memory_order_acq_rel, memory_order_relaxed)) {
        return false;
    }

    batch[n] = t;
    for (uint32_t i = 0; i < n; i++) {
        batch[i]->next = batch[i + 1];
    }

    batch[n]->next = NULL;
    sn_qlock_take(&inject_mu);
    if (inject_tail != NULL) {
        inject_tail->next = batch[0];
    } else {
        inject_head = batch[0];
    }

    inject_tail = batch[n];
    sn_qlock_give(&inject_mu);
    atomic_fetch_add(&hot.injected, (int64_t)n + 1);
    return true;
}

// take_queue takes the task at the head of the queue head and tail
// describe, counted by count, for w to run, and with it, when batch is
// set, a share of the rest onto w's own ring, which is then empty: as
// many as an even share among the workers and one more, at most half a
// ring. Taken
// one at a time, a burst of spawns that overflowed a ring into the
// queue had every worker take its lock once a task, and the workers
// queued on the lock rather than ran tasks. The look of a worker that
// has tasks of its own, to keep the queue moving, takes one.
SN_HELD_STATIC static task *take_queue(worker *w, bool batch, task **head, task **tail, _Atomic int64_t *count) {
    int64_t queued = atomic_load(count);
    if (queued == 0) {
        return NULL;
    }

    int64_t most = 1;
    if (batch) {
        most = queued / workers_now() + 1;
        most = most < ring_size / 2 ? most : ring_size / 2;
    }

    sn_qlock_take(&inject_mu);
    task *t = *head, *last = t;
    int64_t n = 0;
    while (last != NULL && ++n < most) {
        last = last->next;
    }

    if (last != NULL) {
        *head = last->next;
        last->next = NULL;
    } else {
        *head = NULL;
    }

    if (*head == NULL) {
        *tail = NULL;
    }

    sn_qlock_give(&inject_mu);
    if (t == NULL) {
        return NULL;
    }

    atomic_fetch_sub(count, n);
    if (n > 1) {
        uint32_t tl = atomic_load_explicit(&w->qtail, memory_order_relaxed);
        uint32_t i = 0;
        for (task *x = t->next; x != NULL; x = x->next) {
            atomic_store_explicit(&w->ring[(tl + i++) % ring_size], x, memory_order_relaxed);
        }

        atomic_store_explicit(&w->qtail, tl + i,
                              memory_order_release); // the slots are written before a stealer sees the count
    }

    return t;
}

// take_injected takes from the inject queue, and from the displaced
// queue when it is empty, one task at a time from that one, so that a
// worker's ring never fills with tasks taken off their workers, behind
// which a wake would wait a quantum each; displaced_first has it the
// other way round, the look a worker takes once in outside_turns, so
// that a stream of wakes never starves the tasks taken off their
// workers, as Go's global queue has its turn once in 61.
SN_HELD_STATIC static task *take_injected(worker *w, bool batch, bool displaced_first) {
    task *t = NULL;
    if (displaced_first) {
        t = take_queue(w, false, &displaced_head, &displaced_tail, &hot.displaced);
    }

    if (t == NULL) {
        t = take_queue(w, batch, &inject_head, &inject_tail, &hot.injected);
    }

    if (t == NULL && !displaced_first) {
        t = take_queue(w, false, &displaced_head, &displaced_tail, &hot.displaced);
    }

    return t;
}

// ring_put puts the n tasks ts on w's ring, by w's thread, which found
// it empty and is the one to push on it, so that they fit: a look at
// the sockets' poller wakes at most 2 * SN_POLL_EVENTS, half a ring.
_Static_assert(2 * SN_POLL_EVENTS <= ring_size / 2, "a look's tasks fit an empty ring");

SN_HELD_STATIC static void ring_put(worker *w, task **ts, int n) {
    uint32_t tl = atomic_load_explicit(&w->qtail, memory_order_relaxed);
    for (int i = 0; i < n; i++) {
        atomic_store_explicit(&w->ring[(tl + (uint32_t)i) % ring_size], ts[i], memory_order_relaxed);
    }

    atomic_store_explicit(&w->qtail, tl + (uint32_t)n,
                          memory_order_release); // the slots are written before a stealer sees the count
}

// home is the queue a task spawned here goes on: the running worker's,
// or the first worker's from the main thread before it is one.

// pop takes the task at the head of w's own ring, by the worker's
// thread, racing only with stealers for the head.
SN_HELD_STATIC static task *pop(worker *w) {
    for (;;) {
        uint32_t h = atomic_load_explicit(&w->qhead, memory_order_acquire);
        uint32_t tl = atomic_load_explicit(&w->qtail, memory_order_relaxed);
        if (tl == h) {
            return NULL;
        }

        task *t = atomic_load_explicit(&w->ring[h % ring_size], memory_order_relaxed);
        if (atomic_compare_exchange_weak_explicit(&w->qhead, &h, h + 1, memory_order_acq_rel, memory_order_relaxed)) {
            return t;
        }
    }
}

// steal takes half of the ring of from, rounded up so that a lone task
// is taken, for to, whose own ring is empty: the first is returned to
// run and the rest go on to's ring, as Go's runqsteal does. A head that
// moved under the read, a take by the owner or another stealer, makes
// the swap fail and the read is made again.
SN_HELD_STATIC static task *steal(worker *to, worker *from) {
    task *batch[ring_size / 2];
    for (;;) {
        uint32_t h = atomic_load_explicit(&from->qhead, memory_order_acquire);
        uint32_t tl = atomic_load_explicit(&from->qtail, memory_order_acquire);
        uint32_t n = tl - h;
        n = n - n / 2;
        if (n == 0) {
            return NULL;
        }

        if (n > ring_size / 2) {
            continue; // the two counts were read across a change: read again
        }

        for (uint32_t i = 0; i < n; i++) {
            batch[i] = atomic_load_explicit(&from->ring[(h + i) % ring_size], memory_order_relaxed);
        }

        if (!atomic_compare_exchange_strong_explicit(&from->qhead, &h, h + n, memory_order_acq_rel,
                                                     memory_order_relaxed)) {
            continue;
        }

        uint32_t t = atomic_load_explicit(&to->qtail, memory_order_relaxed);
        for (uint32_t i = 1; i < n; i++) {
            atomic_store_explicit(&to->ring[(t + i - 1) % ring_size], batch[i], memory_order_relaxed);
        }

        atomic_store_explicit(&to->qtail, t + n - 1, memory_order_release);
        return batch[0];
    }
}

// put_next puts t in w's runnext slot; a task it displaces goes on
// w's queue, where another worker may steal it. The slot is one word
// the worker's own thread alone fills, and no count anywhere else, so
// that a wake writes no line the other workers write.
SN_HELD_STATIC static void put_next(worker *w, task *t) {
    task *old = atomic_exchange(&w->runnext, t);
    if (old != NULL) {
        push(w, old);
    }
}

// take_next empties w's runnext slot and returns the task it held, or
// NULL: a compare-and-swap, since the worker, a thief and the monitor
// may reach for it at once.
SN_HELD_STATIC static task *take_next(worker *w) {
    task *t = atomic_load(&w->runnext);
    if (t == NULL || !atomic_compare_exchange_strong(&w->runnext, &t, NULL)) {
        return NULL;
    }

    return t;
}

// The workers watch the sockets themselves, with no thread of the
// poller's: a worker that runs out of tasks looks at the poller without
// waiting, after its own queue and the inject queue and before it
// steals, and runs the first task an event made runnable. Of the rest,
// as many as workers wait for work go on the inject queue with a worker
// woken for each, and the others on its own ring, which is empty, for
// it to run next and the others to steal: put on the inject queue
// alone, the tasks of one look spread over every busy worker, and the
// echoes on Darwin, where the system's socket calls are the cost, were
// slower than with one worker running them in turn. The thread that hears of
// an event so runs its task, where a thread of the poller's that heard
// of it had to wake a worker, and echoes over sockets cost two threads
// woken an event. No one looks while no task waits on a socket, nor
// while the keeper waits in the poller, which hears of the events
// itself, except the worker that left the keeper's wait because the
// poller held events: it takes them whatever waits, since an event no
// task waits for only marks its socket ready, and one left in the
// poller would end every keeper's wait at once. polls counts the looks,
// for the monitor to tell whether any was made since its last.
static _Atomic uint64_t polls;

SN_HELD_STATIC static task *poll_now(worker *w) {
    if (!w->owes_poll && (sn_net_waiting() == 0 || atomic_load(&keeping))) {
        return NULL;
    }

    w->owes_poll = false;

    sn_task *woke[2 * SN_POLL_EVENTS];
    int taken;
    int n = sn_net_poll(woke, &taken);
    atomic_fetch_add_explicit(&polls, 1, memory_order_relaxed);
    task **rest = (task **)woke + 1;
    int left = n - 1, shared = atomic_load(&hot.waiting);
    shared = left < shared ? left : shared;
    if (shared > 0) {
        inject_all(rest, shared);
    }

    if (left > shared) {
        ring_put(w, rest + shared, left - shared);
    }

    sn_net_kept(taken); // the first is the caller's to run, a busy worker's, so no idle worker counts on its promise
    return n > 0 ? (task *)woke[0] : NULL;
}

// next_task is the task w runs next: the one in its runnext slot, then
// its queue's head, the inject queue's, one a look at the sockets'
// poller made runnable, and half of another worker's queue. Once the
// slot's task has run first next_most times in a row, the queue's head
// runs before it.
SN_HELD_STATIC static task *next_task(worker *w) {
    bool turn_over = w->streak >= next_most;
    task *t = turn_over ? NULL : take_next(w);
    if (t != NULL) {
        w->streak++;
        return t;
    }

    w->streak = 0;
    t = pop(w);
    if (t != NULL) {
        return t;
    }

    if (turn_over && (t = take_next(w)) != NULL) { // nothing was queued behind it
        return t;
    }

    t = take_injected(w, true, false); // its ring is empty: room for a batch
    if (t != NULL) {
        return t;
    }

    t = poll_now(w);
    if (t != NULL) {
        return t;
    }

    for (int i = 0; i < workers_now(); i++) {
        worker *other = &workers[i];
        if (other != w && (t = steal(w, other)) != NULL) {
            return t;
        }
    }

    return NULL;
}

#ifdef SN_DEBUG
// stack_check ends the program when a task on a small stack wrote
// deeper than the bound the analysis of the linked program gave it:
// the analysis is wrong, and in a release build the task would have
// written over another's stack. A debug build of every program tested
// is so a test of the analysis.
SN_HELD_STATIC static void stack_check(task *t) {
    if (t->stack == NULL || t->stack_class == guarded) {
        return;
    }

    size_t size = class_size(t->stack_class), i = 0;
    const uint8_t *s = t->stack;
    while (i < size && s[i] == stack_paint) {
        i++;
    }

    if (size - i > t->bound) {
        fflush(stdout);
        fprintf(stderr,
                "fatal error: a task's stack went %zu bytes deep, past the bound of %u bytes its analysis gave it\n",
                size - i, (unsigned)t->bound);
        exit(2);
    }
}
#endif

// settle finishes what the task that last switched to the scheduler
// left behind: a task done is freed; a task parking is parked, or put
// back on the queue if a wake reached it first. It runs on the
// scheduler's stack, so a done task's stack is no longer in use.
SN_HELD_STATIC static void settle(worker *w) {
    task *t = w->last;
    if (t == NULL) {
        return;
    }

    w->last = NULL;
    switch (atomic_load(&t->state) & ~WOKEN) {
    case DONE:
#ifdef SN_DEBUG
        stack_check(t);
#endif
        if (t->large != NULL) {
            stack_free(w, t->large, guarded);
        }

        if (t->trace != NULL) {
            free(t->trace);
            t->trace = NULL;
        }

        if (t->guards != t->few) {
            free(t->guards);
        }

        if (t->arg != t->args) {
            free(t->arg); // the arguments the record had no room for
        }

        sn_string_drop(&t->places[SN_PLACE_PARTS]);
        preempt_block *b = atomic_load_explicit(&t->preempt, memory_order_relaxed);
        if (b != NULL) {
            atomic_store_explicit(&t->preempt, NULL, memory_order_relaxed);
            preempt_block *none = NULL;
            if (!atomic_compare_exchange_strong(&w->spare, &none, b)) { // kept for the worker's next, or given back
                sn_aligned_free(b);
            }
        }
#ifdef SN_DEBUG
        if (t->stack != NULL) {
            stack_free(w, t->stack, t->stack_class);
        }

        task *oldest = w->ended[w->nended % ended_kept]; // the oldest kept, or none yet
        if (oldest != NULL) {
            debug_unlist(oldest);
            free(oldest);
        }

        w->ended[w->nended % ended_kept] = t; // still marked done, for a wake that comes late
        w->nended++;
#else
        dead_keep(w, t); // the record and its stack, for a spawn to come
#endif
        atomic_fetch_sub_explicit(&w->running, 1,
                                  memory_order_relaxed); // the worker it ended on, whose count may go below zero
        break;
    case PARKING: {
        int expected = PARKING;
        if (!atomic_compare_exchange_strong(&t->state, &expected,
                                            PARKED)) { // woken while it switched away: it runs next, here
            // Read and written, not stored: a later wake's claim stays
            // ordered with the task, which sees what its waker wrote.
            atomic_exchange(&t->state, RUNNABLE);
            put_next(w, t);
        }

        break;
    }
    case PREEMPTED: {
        int s = atomic_load(&t->state);
        while (!atomic_compare_exchange_weak(&t->state, &s, RUNNABLE | (s & WOKEN))) { // a wake kept stays kept
        }

        displace(t); // behind the tasks that waited, as Go's runtime queues a preempted goroutine globally
        task *woken = take_injected(w, false, false); // what the preemption was for, a task woken from outside,
        if (woken != NULL) {                          // runs next here, as a task a worker wakes itself does
            put_next(w, woken);
        }

        break;
    }
    }
}

// run_task switches the worker to a task, and settles the task when it
// switches back.
SN_HELD_STATIC static void run_task(worker *w, task *t) {
    int s = atomic_load(&t->state);
    while (!atomic_compare_exchange_weak(&t->state, &s, RUNNING | (s & WOKEN))) { // a wake kept for its next park stays
    }

    current = t;
    w->last = t;
    atomic_store_explicit(&w->switches, atomic_load_explicit(&w->switches, memory_order_relaxed) + 1,
                          memory_order_relaxed); // its own thread alone writes it
    sn_switch(&w->sched_sp, t->sp);
    current = NULL;
    settle(w);
}

// steal_next takes a task another worker's running task woke into its
// slot and has not come back for. A thief waits steal_wait first, once
// a sweep, since the owner is likely to take the task itself within
// that time, and takes it if it is still there; what sits longer with
// every worker busy the monitor moves.
SN_HELD_STATIC static task *steal_next(worker *w) {
    int64_t until = 0; // the end of the wait, from the first slot found held: every slot after it is given the rest
    for (int i = 0; i < workers_now(); i++) {
        worker *other = &workers[i];
        if (other == w || atomic_load(&other->runnext) == NULL) {
            continue;
        }

        if (until == 0) {
            until = sn_time_mono() + steal_wait;
        }

        for (int spins = 1; atomic_load(&other->runnext) != NULL; spins++) {
            if (spins % steal_spins == 0 && sn_time_mono() >= until) { // the clock once in a while, as spin reads it
                break;
            }

            sn_spin_hint();
        }

        task *t = take_next(other);
        if (t != NULL) {
            return t;
        }
    }

    return NULL;
}

// A worker that runs out of tasks spins before it sleeps, as Go's
// runtime keeps an M spinning: it looks at the queues for up to
// spin_for and takes a task the poller or the timers woke meanwhile,
// or one a running task pushed, with no thread woken to run it, which
// is what such a task costs otherwise. One worker spins at a time, and
// a push or an inject while one does wakes no other; the spinner, once
// it has found work, wakes a worker for what is left: before, every
// push woke a worker while any was idle, a system call per spawn. The
// spinning adapts, as an adaptive mutex's
// does: worth tells whether it has paid lately, raised when a spin finds
// a task or a worker's sleep ends within spin_for, lowered when a spin
// finds none, and a worker spins only while it is above zero. A program
// trading messages keeps it up; one that ticks every millisecond, or
// waits on a socket that is quiet, brings it to zero and wastes no time
// spinning.
static _Atomic int worth = 1;

SN_HELD_STATIC static void spun(bool paid) {
    int w = atomic_load(&worth);
    int next = paid ? (w < worth_most ? w + 1 : w) : (w > 0 ? w - 1 : 0);
    if (next != w) {
        atomic_compare_exchange_strong(&worth, &w, next); // a lost race loses one step of a guess
    }
}

// spin looks at the queues, and at the sockets' poller each time it
// reads the clock, until spin_for has passed, and reports whether a
// task was queued or injected meanwhile, or one a look made runnable is
// in w's runnext slot. A worker woken to
// spin holds the spin already; another takes it if no one holds it.
SN_HELD_STATIC static bool spin(worker *w, bool holding) {
    int expected = 0;
    if (!holding && (atomic_load(&worth) <= 0 || !atomic_compare_exchange_strong(&hot.spinning, &expected, 1))) {
        return false;
    }

    int64_t until = sn_time_mono() + spin_for;
    bool found = false;
    for (int i = 1;; i++) {
        if (queued_now() > 0) { // the rings and the inject queue
            found = true;
            break;
        }

        if (i % 64 == 0) {
            int64_t now = sn_time_mono();
            if (sn_timers_next() <= now) { // a timer came due: firing it is the work
                found = true;
                break;
            }

            if (now > until) {
                break;
            }

            task *t = poll_now(w);
            if (t != NULL) {
                put_next(w, t); // what next_task takes first
                found = true;
                break;
            }
        }

        sn_spin_hint();
    }

    atomic_store(&hot.spinning, 0);
    found = found || queued_now() > 0; // a push or an inject that saw it spinning woke no one: it looks once more
    spun(found);
    return found;
}

// The keeper's wait: until a time of the monotonic clock, which the
// system never sets back, so that a change of the wall clock moves no
// timer, or until keeper_wake. How it sleeps is each system's most
// exact way that another thread can end: measured, a wait on a
// condition returns a quarter of the wait late on Darwin, where a
// kqueue's critical timer returns a fiftieth late, and Linux adds its
// timer slack of 50 µs to a thread's every timed wait unless the thread
// asks for none, as each worker does when it starts. A deadline nearer
// than keeper_near, which no sleep in the kernel returns in time for,
// is waited for by spinning. The caller holds idle_mu, which the wait
// lets go of while it sleeps, as a condition's does. A wake that finds
// the keeper between two waits is kept for the next, which then returns
// at once to a keeper that reads the deadlines again.
//
// The keeper also watches the sockets: its wait ends when the sockets'
// poller holds events, which it waits for as one descriptor among its
// own, the poller being readable while it holds any, and it then leaves
// the wait to look at the poller as a worker. On Linux and macOS the
// deadline and the wake are in a set of the keeper's alone, so that a
// worker looking at the poller never takes them; Windows has no such
// set, as the block below says. A keeper with no deadline, keeping
// only for the sockets, waits with none.
static _Atomic bool keeper_woken;

#if defined(_WIN32)
// On Windows the keeper waits on the sockets' completion port, net.c's,
// for the poll of a socket, its wake, posted to the port, and its
// deadline, a high-resolution waitable timer the system posts to the
// port through a wait completion packet, which ntdll's calls make,
// looked up once, as no import library names them. A worker looking at the
// port for the sockets may take the keeper's wake or deadline, and
// gives it back while the keeper waits; the keeper may take a poll,
// which it posts again for a worker and leaves the wait to take as one.
typedef LONG(NTAPI *wait_packet_create)(HANDLE *, ACCESS_MASK, void *);
typedef LONG(NTAPI *wait_packet_associate)(HANDLE, HANDLE, HANDLE, void *, void *, LONG, ULONG_PTR, BOOLEAN *);
typedef LONG(NTAPI *wait_packet_cancel)(HANDLE, BOOLEAN);

static HANDLE keeper_clock;  // the high-resolution timer of the deadline
static HANDLE keeper_packet; // the wait completion packet that posts it to the port
static wait_packet_associate associate;
static wait_packet_cancel cancel;
static _Atomic bool keeper_asleep; // the keeper waits on the port, for a packet of its own a worker took

SN_HELD_STATIC static void keeper_start(void) {
    HMODULE ntdll = GetModuleHandleW(L"ntdll.dll");
    wait_packet_create create = (wait_packet_create)(void *)GetProcAddress(ntdll, "NtCreateWaitCompletionPacket");
    associate = (wait_packet_associate)(void *)GetProcAddress(ntdll, "NtAssociateWaitCompletionPacket");
    cancel = (wait_packet_cancel)(void *)GetProcAddress(ntdll, "NtCancelWaitCompletionPacket");
    keeper_clock = CreateWaitableTimerExW(NULL, NULL, CREATE_WAITABLE_TIMER_HIGH_RESOLUTION, TIMER_ALL_ACCESS);
    if (keeper_clock == NULL) { // a system older than the high-resolution timer: a deadline then comes on its tick
        keeper_clock = CreateWaitableTimerExW(NULL, NULL, 0, TIMER_ALL_ACCESS);
    }

    if (sn_net_poller() < 0 || create == NULL || associate == NULL || cancel == NULL || keeper_clock == NULL ||
        create(&keeper_packet, GENERIC_ALL, NULL) < 0) {
        sn_panic(SN_STR("cannot start the scheduler", 26));
    }
}

SN_HELD_STATIC static bool keeper_sleep(int64_t when, int64_t now) {
    HANDLE port = sn_net_port();
    DWORD wait = INFINITE; // a deadline the timer cannot keep is kept by the wait's own, to the millisecond
    if (when != INT64_MAX) {
        LARGE_INTEGER due = { .QuadPart = -((when - now) + 99) / 100 }; // relative, in ticks of 100 ns
        if (!SetWaitableTimer(keeper_clock, &due, 0, NULL, NULL, FALSE) ||
            associate(keeper_packet, port, keeper_clock, (void *)(uintptr_t)SN_KEY_DEADLINE, NULL, 0, 0, NULL) < 0) {
            wait = (DWORD)((when - now + sn_ns_per_ms - 1) / sn_ns_per_ms);
        }
    }

    atomic_store(&keeper_asleep, true);
    sn_qlock_give(&idle_mu);
    OVERLAPPED_ENTRY got[8];
    ULONG n = 0;
    if (!GetQueuedCompletionStatusEx(port, got, 8, &n, wait, FALSE)) {
        n = 0;
    }

    atomic_store(&keeper_asleep, false);
    sn_qlock_take(&idle_mu);
    if (when != INT64_MAX) {
        CancelWaitableTimer(keeper_clock);
        cancel(keeper_packet, TRUE); // and its packet, posted and not yet taken
    }

    bool sockets = false;
    for (ULONG i = 0; i < n; i++) {
        if (got[i].lpCompletionKey != SN_KEY_WAKE && got[i].lpCompletionKey != SN_KEY_DEADLINE) {       // a poll's, a
            PostQueuedCompletionStatus(port, got[i].dwNumberOfBytesTransferred, got[i].lpCompletionKey, // child's or
                                       got[i].lpOverlapped);                                            // a pipe's
            sockets = true;
        }
    }

    return sockets;
}

static inline void sched_ready(void);

SN_HELD void *sn_wait_packet_make(void *target, uintptr_t key, void *context) {
    sched_ready();
    HANDLE packet = NULL;
    HMODULE ntdll = GetModuleHandleW(L"ntdll.dll");
    wait_packet_create create = (wait_packet_create)(void *)GetProcAddress(ntdll, "NtCreateWaitCompletionPacket");
    if (create == NULL) {
        SetLastError(ERROR_PROC_NOT_FOUND);
        return NULL;
    }

    LONG st = create(&packet, GENERIC_ALL, NULL);
    if (st >= 0) {
        st = associate(packet, sn_net_port(), target, (void *)key, context, 0, 0, NULL);
    }

    if (st < 0) {
        if (packet != NULL) {
            CloseHandle(packet);
        }

        SetLastError(RtlNtStatusToDosError(st)); // the calls set no last error of their own: the caller reads one
        return NULL;
    }

    return packet;
}

SN_HELD void sn_wait_packet_free(void *packet) {
    cancel(packet, TRUE);
    CloseHandle(packet);
}

SN_HELD_STATIC static void keeper_rouse(void) { PostQueuedCompletionStatus(sn_net_port(), 0, SN_KEY_WAKE, NULL); }

SN_HELD void sn_keeper_packet(uintptr_t key) {
    if (atomic_load(&keeper_asleep)) {
        PostQueuedCompletionStatus(sn_net_port(), 0, key, NULL);
    }
}
#elif defined(__APPLE__)
static int keeper_queue = -1;

SN_HELD_STATIC static void keeper_start(void) {
    keeper_queue = kqueue();
    int poller = sn_net_poller();
    struct kevent ev[2];
    EV_SET(&ev[0], keeper_user, EVFILT_USER, EV_ADD | EV_CLEAR, 0, 0, NULL);
    EV_SET(&ev[1], (uintptr_t)poller, EVFILT_READ, EV_ADD, 0, 0, NULL);
    if (keeper_queue < 0 || poller < 0 || fcntl(keeper_queue, F_SETFD, FD_CLOEXEC) < 0 ||
        kevent(keeper_queue, ev, 2, NULL, 0, NULL) < 0) {
        sn_panic(SN_STR("cannot start the scheduler", 26));
    }
}

SN_HELD_STATIC static bool keeper_sleep(int64_t when, int64_t now) {
    sn_qlock_give(&idle_mu);
    struct kevent ev, got[3];
    EV_SET(&ev, keeper_timer, EVFILT_TIMER, EV_ADD | EV_ONESHOT, NOTE_NSECONDS | NOTE_CRITICAL, when - now, NULL);
    int n = kevent(keeper_queue, &ev, when == INT64_MAX ? 0 : 1, got, 3,
                   NULL); // a wait cut short is a wake: the keeper reads the deadlines again
    sn_qlock_take(&idle_mu);
    bool sockets = false;
    for (int i = 0; i < n; i++) {
        sockets = sockets || got[i].filter == EVFILT_READ;
    }

    return sockets;
}

SN_HELD_STATIC static void keeper_rouse(void) {
    struct kevent ev;
    EV_SET(&ev, keeper_user, EVFILT_USER, 0, NOTE_TRIGGER, 0, NULL);
    (void)kevent(keeper_queue, &ev, 1, NULL, 0, NULL);
}
#else
// The keeper's set: an eventfd for the wake, a timerfd at an absolute
// time of CLOCK_MONOTONIC for the deadline, which, unlike a timed wait,
// takes no timer slack, and the poller. The keeper alone reads the
// first two, so a wake that comes between two waits is kept for the
// next, as the eventfd stays readable until it is read.
static int keeper_set = -1, keeper_event = -1, keeper_clock = -1;
static int64_t keeper_armed = INT64_MAX; // the deadline keeper_clock is set to, under idle_mu

SN_HELD_STATIC static void keeper_start(void) {
    keeper_set = epoll_create1(EPOLL_CLOEXEC);
    keeper_event = eventfd(0, EFD_CLOEXEC | EFD_NONBLOCK);
    keeper_clock = timerfd_create(CLOCK_MONOTONIC, TFD_CLOEXEC | TFD_NONBLOCK);
    int poller = sn_net_poller();
    struct epoll_event user = { .events = EPOLLIN, .data.u32 = keeper_user };
    struct epoll_event timer = { .events = EPOLLIN, .data.u32 = keeper_timer };
    struct epoll_event polled = { .events = EPOLLIN, .data.u32 = keeper_poller };
    if (keeper_set < 0 || keeper_event < 0 || keeper_clock < 0 || poller < 0 ||
        epoll_ctl(keeper_set, EPOLL_CTL_ADD, keeper_event, &user) < 0 ||
        epoll_ctl(keeper_set, EPOLL_CTL_ADD, keeper_clock, &timer) < 0 ||
        epoll_ctl(keeper_set, EPOLL_CTL_ADD, poller, &polled) < 0) {
        sn_panic(SN_STR("cannot start the scheduler", 26));
    }
}

SN_HELD_STATIC static bool keeper_sleep(int64_t when, int64_t now) {
    (void)now;
    if (when != keeper_armed) {
        int64_t at = when == INT64_MAX
                         ? 0
                         : when - 1; // a reading is the clock's and one, so that none is 0; a time of 0 disarms
        struct itimerspec ts = { .it_value = { (time_t)(at / 1000000000), (long)(at % 1000000000) } };
        (void)timerfd_settime(keeper_clock, TFD_TIMER_ABSTIME, &ts, NULL);
        keeper_armed = when;
    }

    sn_qlock_give(&idle_mu);
    struct epoll_event got[3];
    int n = epoll_wait(keeper_set, got, 3, -1); // a wait cut short is a wake: the keeper reads the deadlines again
    sn_qlock_take(&idle_mu);
    bool sockets = false;
    uint64_t count;
    for (int i = 0; i < n; i++) {
        switch (got[i].data.u32) {
        case keeper_user:
            (void)read(keeper_event, &count, sizeof count);
            break;
        case keeper_timer:
            (void)read(keeper_clock, &count, sizeof count);
            keeper_armed = INT64_MAX; // it fired, and is set again for the next
            break;
        default:
            sockets = true;
        }
    }

    return sockets;
}

SN_HELD_STATIC static void keeper_rouse(void) {
    uint64_t one = 1;
    (void)write(keeper_event, &one, sizeof one);
}
#endif

// keeper_wait is the keeper's wait until when, the greatest time for
// none; now is the clock's reading a moment ago. It reports whether the
// sockets' poller holds events.
SN_HELD_STATIC static bool keeper_wait(int64_t when, int64_t now) {
    atomic_store(&keeper_woken, false);
    if (when - now > keeper_near) {
        return keeper_sleep(when, now);
    }

    sn_qlock_give(&idle_mu);
    while (!atomic_load(&keeper_woken) && sn_time_mono() < when) {
        sn_spin_hint();
    }

    sn_qlock_take(&idle_mu);
    return false;
}

// keeper_wake ends the keeper's wait; the caller holds idle_mu and has
// found a keeper. wake_at is the least time from here until the keeper
// has the lock again, so that no one who starts a timer meanwhile wakes
// it twice: it is on its way to read the deadlines.
SN_HELD_STATIC static void keeper_wake(void) {
    if (atomic_load(&keeper_woken)) {
        return;
    }

    atomic_store(&keeper_woken, true);
    atomic_store(&wake_at, INT64_MIN);
    keeper_rouse();
}

// Preemption. A task that has held its worker for held_limit while a
// task waits is taken off it, at a point outside the runtime's held
// section, its registers saved as the system itself saved them, and
// put on the displaced queue; the worker runs the next task, and
// whichever worker takes the preempted task has its registers put back
// and runs it from where it was interrupted. Only a task that holds a
// core that long ever pays: nothing is added to the program's code or
// to a switch, and a preemption costs a signal and two copies.
//
// On Linux and macOS the monitor sends the worker's thread
// preempt_signal, whose handler runs on a signal stack of the worker's,
// so that a task's small stack never holds a signal frame. The handler
// copies the interrupted context, every register and the vector unit's
// state as the kernel keeps them for its own return, into a block of
// the task's own, and returns into preempt_land, on the block's landing
// stack, which marks the task preempted and switches to the scheduler
// as a park does. When a worker runs the task again, the landing
// resumes, raises the signal at itself, and the handler puts the saved
// context in the place of the live one, so that the kernel's return
// restores the task where it was interrupted, on its own stack, with
// its errno; the context is never read or written by the runtime's own
// code, only moved whole, so whatever state the kernel saves is kept.
// On Windows the monitor suspends the thread, reads its context, sets
// one that runs preempt_land, and resumes it; the landing restores the
// context itself, RtlRestoreContext, after the stack's bounds in the
// thread's information block, which the switch keeps there too.
//
// A point is safe when the program counter lies in the executable's
// own text, so never in the C library, where a task interrupted inside
// malloc would keep its lock on a thread that runs other tasks, and
// outside the runtime's held section (internal.h, SN_HELD), where every
// function that holds a lock, reads the thread's own state or switches
// is, so that a task resumed on another thread holds nothing of the one
// it left; the sections' addresses are read once, as the scheduler
// starts. Go's runtime decides the same way, by its safe points, with
// its own functions none. A point that is not safe is left as it is,
// and the monitor tries again at its next look.
static uintptr_t text_start, text_end; // the program's own code, where a task may be preempted
static uintptr_t held_start, held_end; // the runtime's held section within it, where it may not
#if defined(__APPLE__)
static _Atomic size_t context_size; // the size of the machine context of the first thread preempted
#endif

SN_HELD_STATIC static bool preempt_safe(uintptr_t pc) {
    return pc >= text_start && pc < text_end && (pc < held_start || pc >= held_end);
}

#if defined(_WIN32)
// The held section's bounds on Windows: the linker lays the .text$
// sections out in the order of their names, so a function first in
// .text$sn_held_a and one in .text$sn_held_z bound the functions in
// .text$sn_held_m between them.
__attribute__((section(".text$sn_held_a"), used)) static void held_first(void) {}
__attribute__((section(".text$sn_held_z"), used)) static void held_last(void) {}

SN_HELD_STATIC static void preempt_start(void) {
    uint8_t *image = (uint8_t *)GetModuleHandleW(NULL);
    IMAGE_NT_HEADERS *headers = (IMAGE_NT_HEADERS *)(image + ((IMAGE_DOS_HEADER *)image)->e_lfanew);
    text_start = (uintptr_t)image;
    text_end = text_start + headers->OptionalHeader.SizeOfImage;
    held_start = (uintptr_t)held_first;
    held_end = (uintptr_t)held_last;
}

SN_HELD_STATIC static void preempt_thread_start(worker *w) {
    HANDLE thread;
    if (!DuplicateHandle(GetCurrentProcess(), GetCurrentThread(), GetCurrentProcess(), &thread, 0, FALSE,
                         DUPLICATE_SAME_ACCESS)) {
        sn_panic(SN_STR("cannot start a thread", 21));
    }

    atomic_store(&w->teb, NtCurrentTeb());
    atomic_store(&w->thread, thread);
}

// teb_bounds reads the bounds of the stack a thread is on from its
// information block, and teb_set_bounds writes them.
SN_HELD_STATIC static sn_stack_bounds teb_bounds(uint8_t *teb) {
    sn_stack_bounds b;
    memcpy(&b.base, teb + teb_stack_base, sizeof b.base);
    memcpy(&b.limit, teb + teb_stack_limit, sizeof b.limit);
    memcpy(&b.reservation, teb + teb_deallocation_stack, sizeof b.reservation);
    return b;
}

SN_HELD_STATIC static void teb_set_bounds(uint8_t *teb, sn_stack_bounds b) {
    memcpy(teb + teb_stack_base, &b.base, sizeof b.base);
    memcpy(teb + teb_stack_limit, &b.limit, sizeof b.limit);
    memcpy(teb + teb_deallocation_stack, &b.reservation, sizeof b.reservation);
}
#else
#if defined(__APPLE__)
SN_HELD_STATIC static void preempt_text_init(void) {
    unsigned long size = 0;
    uint8_t *start = getsegmentdata(&_mh_execute_header, "__TEXT", &size);
    text_start = (uintptr_t)start;
    text_end = text_start + size;
    start = getsectiondata(&_mh_execute_header, "__TEXT", "__sn_held", &size);
    held_start = (uintptr_t)start;
    held_end = held_start + size;
}
#else
extern char __start_sn_held[], __stop_sn_held[]; // the linker's, around the section

// preempt_text_segment takes the program's own executable segments from
// the first object dl_iterate_phdr reports, which is the program itself,
// and stops the walk there.
SN_HELD_STATIC static int preempt_text_segment(struct dl_phdr_info *info, size_t size, void *data) {
    (void)size;
    (void)data;
    for (int i = 0; i < info->dlpi_phnum; i++) {
        const ElfW(Phdr) *h = &info->dlpi_phdr[i];
        if (h->p_type == PT_LOAD && (h->p_flags & PF_X) != 0) {
            uintptr_t lo = info->dlpi_addr + h->p_vaddr, hi = lo + h->p_memsz;
            if (text_start == 0 || lo < text_start) {
                text_start = lo;
            }

            if (hi > text_end) {
                text_end = hi;
            }
        }
    }

    return 1;
}

SN_HELD_STATIC static void preempt_text_init(void) {
    dl_iterate_phdr(preempt_text_segment, NULL);
    held_start = (uintptr_t)__start_sn_held;
    held_end = (uintptr_t)__stop_sn_held;
}
#endif

#if defined(__linux__) && defined(__x86_64__)
// fp_image_size is the size of the floating-point image a signal frame
// holds at fpregs: the whole extended image when the kernel marked one,
// FXSAVE's 512 bytes otherwise.
SN_HELD_STATIC static size_t fp_image_size(const void *fpregs) {
    uint32_t magic, extended;
    memcpy(&magic, (const uint8_t *)fpregs + fp_sw_reserved, sizeof magic);
    memcpy(&extended, (const uint8_t *)fpregs + fp_sw_reserved + sizeof magic, sizeof extended);
    return magic == fp_xstate_magic ? extended : fp_image_bytes;
}
#endif

#if defined(__linux__) && defined(__aarch64__)
// context_outside reports whether the kernel put part of the vector
// unit's state outside the frame, which it does when the state is too
// large for the frame's reserved room, as a very wide SVE or SME state
// is, and marks with a record of context_extra_magic among the records
// there: a copy of the frame would not carry it, so such a point is
// left as it is.
SN_HELD_STATIC static bool context_outside(const mcontext_t *mc) {
    const uint8_t *p = mc->__reserved, *end = p + sizeof mc->__reserved;
    while (p + 2 * sizeof(uint32_t) <= end) {
        uint32_t magic, size;
        memcpy(&magic, p, sizeof magic);
        memcpy(&size, p + sizeof magic, sizeof size);
        if (magic == 0 || size < 2 * sizeof(uint32_t)) {
            return false;
        }

        if (magic == context_extra_magic) {
            return true;
        }

        p += size;
    }

    return false;
}
#endif

#if defined(__APPLE__) && defined(__arm64e__)
#error "the Snello runtime restores a preempted task's registers as plain pointers, which arm64e signs"
#endif

// context_pc is the program counter of an interrupted context.
SN_HELD_STATIC static uintptr_t context_pc(const ucontext_t *uc) {
#if defined(__APPLE__) && defined(__aarch64__)
    return (uintptr_t)uc->uc_mcontext->__ss.__pc;
#elif defined(__APPLE__)
    return (uintptr_t)uc->uc_mcontext->__ss.__rip;
#elif defined(__aarch64__)
    return (uintptr_t)uc->uc_mcontext.pc;
#else
    return (uintptr_t)uc->uc_mcontext.gregs[REG_RIP];
#endif
}

// context_save copies the interrupted context into the block, whole,
// and reports whether the block had room for what the kernel saved.
SN_HELD_STATIC static bool context_save(preempt_block *b, const ucontext_t *uc) {
#if defined(__APPLE__)
    // The context's form is the thread's, and on x86-64 a thread that
    // has used wide vector registers gets a larger one: a task is
    // preempted only on a thread whose form is the first one seen, so
    // that it is restored into a frame of the same size.
    size_t expected = 0;
    if (!atomic_compare_exchange_strong(&context_size, &expected, uc->uc_mcsize) && expected != uc->uc_mcsize) {
        return false;
    }

    if (uc->uc_mcsize > sizeof b->context) {
        return false;
    }

    b->size = uc->uc_mcsize;
    memcpy(b->context, uc->uc_mcontext, uc->uc_mcsize);
#elif defined(__aarch64__)
    if (context_outside(&uc->uc_mcontext)) {
        return false;
    }

    b->context = uc->uc_mcontext;
#else
    if (uc->uc_mcontext.fpregs == NULL) {
        return false;
    }

    size_t size = fp_image_size(uc->uc_mcontext.fpregs);
    if (size > sizeof b->fp) {
        return false;
    }

    memcpy(b->regs, uc->uc_mcontext.gregs, sizeof b->regs);
    b->fp_size = size;
    memcpy(b->fp, uc->uc_mcontext.fpregs, size);
#endif
    return true;
}

// context_restore puts the block's context in the place of the live
// one, which the kernel returns into.
SN_HELD_STATIC static void context_restore(const preempt_block *b, ucontext_t *uc) {
#if defined(__APPLE__)
    if (uc->uc_mcsize != b->size) { // a frame of another form, which the save holds off: never
        static const char message[] = "fatal error: a preempted task's registers cannot be put back\n";
        (void)sn_write_fd(2, (const uint8_t *)message, sizeof message - 1);
        abort();
    }

    memcpy(uc->uc_mcontext, b->context, b->size);
#elif defined(__aarch64__)
    uc->uc_mcontext = b->context;
#else
    memcpy(uc->uc_mcontext.gregs, b->regs, sizeof b->regs);
    memcpy(uc->uc_mcontext.fpregs, b->fp, b->fp_size);
#endif
}

// context_land makes the live context return into fn(t) on the stack
// whose top is top: the frame and link registers cleared, so that a
// walk of the frames ends there, and on x86-64 a return address of
// zero pushed, so that fn begins as a called function does.
SN_HELD_STATIC static void context_land(ucontext_t *uc, void (*fn)(task *), uint8_t *top, task *t) {
#if defined(__APPLE__) && defined(__aarch64__)
    uc->uc_mcontext->__ss.__pc = (uint64_t)(uintptr_t)fn;
    uc->uc_mcontext->__ss.__sp = (uint64_t)(uintptr_t)top;
    uc->uc_mcontext->__ss.__x[0] = (uint64_t)(uintptr_t)t;
    uc->uc_mcontext->__ss.__fp = 0;
    uc->uc_mcontext->__ss.__lr = 0;
#elif defined(__APPLE__)
    memset(top - sizeof(void *), 0, sizeof(void *));
    uc->uc_mcontext->__ss.__rip = (uint64_t)(uintptr_t)fn;
    uc->uc_mcontext->__ss.__rsp = (uint64_t)(uintptr_t)(top - sizeof(void *));
    uc->uc_mcontext->__ss.__rdi = (uint64_t)(uintptr_t)t;
    uc->uc_mcontext->__ss.__rbp = 0;
#elif defined(__aarch64__)
    uc->uc_mcontext.pc = (uintptr_t)fn;
    uc->uc_mcontext.sp = (uintptr_t)top;
    uc->uc_mcontext.regs[0] = (uintptr_t)t;
    uc->uc_mcontext.regs[29] = 0;
    uc->uc_mcontext.regs[30] = 0;
#else
    memset(top - sizeof(void *), 0, sizeof(void *));
    uc->uc_mcontext.gregs[REG_RIP] = (greg_t)(uintptr_t)fn;
    uc->uc_mcontext.gregs[REG_RSP] = (greg_t)(uintptr_t)(top - sizeof(void *));
    uc->uc_mcontext.gregs[REG_RDI] = (greg_t)(uintptr_t)t;
    uc->uc_mcontext.gregs[REG_RBP] = 0;
#endif
}

static void preempt_land(task *t);
static void preempt_release(worker *w, task *t, preempt_block *b);

// preempt_handler is the signal's handler, on the worker's signal
// stack. Asked to restore, it puts the task's saved context in the
// live one's place. Otherwise it leaves the task at a safe point, as
// the comment above says, and at no other: the monitor looks again.
// A worker whose scheduler context is not made yet, the main thread
// before its first spawn or park, has nothing to switch to.
SN_HELD_STATIC static void preempt_handler(int sig, siginfo_t *info, void *context) {
    (void)sig;
    (void)info;
    int saved = errno;
    ucontext_t *uc = context;
    worker *w = self_worker;
    task *t = current;
    preempt_block *b = t != NULL ? atomic_load_explicit(&t->preempt, memory_order_relaxed) : NULL;
    if (b != NULL && b->restoring) { // the landing's own signal: the task resumes where it was interrupted
        b->restoring = false;
        context_restore(b, uc);
        errno = b->saved_errno;
        preempt_release(w, t, b); // its landing stack is done with: the frames left there are never returned to
        return;
    }

    if (w == NULL || t == NULL || w->sched_sp == NULL || !preempt_safe(context_pc(uc)) ||
        (atomic_load(&t->state) & ~WOKEN) != RUNNING) {
        errno = saved;
        return;
    }

    if (b == NULL) {
        b = atomic_exchange(&w->spare, NULL); // the monitor keeps one ready for the worker
        if (b == NULL) {
            errno = saved;
            return;
        }

        atomic_store_explicit(&t->preempt, b, memory_order_relaxed); // the task's from here, and still its when the
    } // save below fails: the monitor makes a new spare

    if (!context_save(b, uc)) {
        errno = saved;
        return;
    }

    b->saved_errno = saved;
    context_land(uc, preempt_land, b->landing + landing_bytes, t);
    atomic_fetch_add_explicit(&preempted, 1, memory_order_relaxed);
}

SN_HELD_STATIC static void preempt_start(void) {
    preempt_text_init();
    struct sigaction action;
    memset(&action, 0, sizeof action);
    action.sa_sigaction = preempt_handler;
    action.sa_flags = SA_SIGINFO | SA_ONSTACK | SA_RESTART;
    sigemptyset(&action.sa_mask);
    sigaction(preempt_signal, &action, NULL);
}

SN_HELD_STATIC static void preempt_thread_start(worker *w) {
    atomic_store(&w->thread, pthread_self());
    stack_t ss = { .ss_sp = malloc(signal_stack_bytes), .ss_size = signal_stack_bytes, .ss_flags = 0 };
    if (ss.ss_sp == NULL || sigaltstack(&ss, NULL) != 0) {
        sn_panic(SN_STR("cannot start a thread", 21));
    }
}
#endif

// preempt_spare keeps a block ready for the worker, made by the monitor
// so that the preemption, which may allocate nothing, has one to take.
SN_HELD_STATIC static bool preempt_spare(worker *w) {
    if (atomic_load(&w->spare) != NULL) {
        return true;
    }

    size_t size = (sizeof(preempt_block) + SN_CACHE_LINE - 1) & ~(size_t)(SN_CACHE_LINE - 1); // what the aligned
                                                                                              // allocation asks
    preempt_block *b = sn_aligned_alloc(SN_CACHE_LINE, size);
    if (b == NULL) {
        return false;
    }

    memset(b, 0, offsetof(preempt_block, landing)); // the landing stack is written as it is used
    preempt_block *none = NULL;
    if (!atomic_compare_exchange_strong(&w->spare, &none, b)) {
        sn_aligned_free(b);
    }

    return true;
}

// preempt_release takes the block off a task that has its registers
// back: kept for the worker's next preemption when it has no block
// ready, and otherwise left on the task, to be freed when the task
// ends, since the handler, where the release runs on Linux and macOS,
// may free nothing. A task is so left holding a block only while its
// worker holds one too.
SN_HELD_STATIC static void preempt_release(worker *w, task *t, preempt_block *b) {
    preempt_block *none = NULL;
    if (atomic_compare_exchange_strong(&w->spare, &none, b)) {
        atomic_store_explicit(&t->preempt, NULL, memory_order_relaxed);
    }
}

// preempt_restore puts a preempted task's registers back: through the
// signal's handler on Linux and macOS, through RtlRestoreContext on
// Windows, with the stack's bounds and the last error put back first,
// from a copy of the worker's own, so that the block may be released
// before the landing stack in it is left.
SN_HELD_STATIC static _Noreturn void preempt_restore(task *t) {
    preempt_block *b = atomic_load_explicit(&t->preempt, memory_order_relaxed);
#if defined(_WIN32)
    worker *w = self_worker;
    teb_set_bounds((uint8_t *)NtCurrentTeb(), b->bounds);
    SetLastError(b->last_error);
    w->restoring = b->context;
    preempt_release(w, t, b); // the landing stack is left by the restore and never returned to
    RtlRestoreContext(&w->restoring, NULL);
#else
    b->restoring = true;
    raise(preempt_signal);
#endif
    static const char message[] = "fatal error: a preempted task's registers were not put back\n"; // the restore returned
    (void)sn_write_fd(2, (const uint8_t *)message, sizeof message - 1);
    abort();
}

// preempt_land is where a preempted task's thread lands, on the task's
// landing stack with the registers saved: the task is marked preempted
// and the thread switches to the scheduler, which queues it. When a
// worker runs the task again its landing resumes here and has the
// registers put back.
SN_HELD_STATIC static void preempt_land(task *t) {
    int s = atomic_load(&t->state);
    while (!atomic_compare_exchange_weak(&t->state, &s, PREEMPTED | (s & WOKEN))) { // a wake kept stays kept
    }

    worker *w = my_worker();
    w->last = t;
    sn_switch(&t->sp, w->sched_sp);
    preempt_restore(t);
}

#if defined(_WIN32)
// preempt_worker suspends the worker's thread and, when its task is at
// a safe point, saves the task's context and the stack's bounds, sets
// a context that runs preempt_land on the landing stack, and resumes
// the thread; otherwise the thread goes on as it was. The task is read
// while the thread is suspended: its record is freed on that thread
// alone, after it has switched away.
SN_HELD_STATIC static void preempt_worker(worker *w) {
    w->preempt_tries++; // an attempt, whether or not it can be made: a worker never asked would never get a thread
    HANDLE thread = atomic_load(&w->thread);
    uint8_t *teb = atomic_load(&w->teb);
    if (thread == NULL || !preempt_spare(w) || SuspendThread(thread) == (DWORD)-1) {
        return; // a worker added has its thread once the thread has started
    }

    _Alignas(16) CONTEXT live;
    memset(&live, 0, sizeof live);
    live.ContextFlags = CONTEXT_FULL; // the control, integer and floating-point registers: what a restore puts back, and
                                      // not the debug registers, which a thread may not set from user mode
    task *t = w->last;
    if (!GetThreadContext(thread, &live) || t == NULL || w->sched_sp == NULL ||
#if defined(__x86_64__)
        !preempt_safe((uintptr_t)live.Rip) ||
#else
        !preempt_safe((uintptr_t)live.Pc) ||
#endif
        (atomic_load(&t->state) & ~WOKEN) != RUNNING) {
        ResumeThread(thread);
        return;
    }

    preempt_block *b = atomic_load_explicit(&t->preempt, memory_order_relaxed);
    if (b == NULL) {
        b = atomic_exchange(&w->spare, NULL);
        atomic_store_explicit(&t->preempt, b, memory_order_relaxed);
    }

    b->context = live;
    b->bounds = teb_bounds(teb);
    memcpy(&b->last_error, teb + teb_last_error, sizeof b->last_error);
    uint8_t *top = b->landing + landing_bytes;
    CONTEXT land = live;
#if defined(__x86_64__)
    memset(top - 5 * sizeof(void *), 0, sizeof(void *)); // a return address of zero above the home space
    land.Rip = (DWORD64)(uintptr_t)preempt_land;
    land.Rsp = (DWORD64)(uintptr_t)(top - 5 * sizeof(void *));
    land.Rcx = (DWORD64)(uintptr_t)t;
    land.Rbp = 0;
#else
    land.Pc = (DWORD64)(uintptr_t)preempt_land;
    land.Sp = (DWORD64)(uintptr_t)top;
    land.X0 = (DWORD64)(uintptr_t)t;
    land.Fp = 0;
    land.Lr = 0;
#endif
    teb_set_bounds(teb, (sn_stack_bounds){ top, b->landing, b->landing });
    if (!SetThreadContext(thread, &land)) { // the thread goes on as it was
        teb_set_bounds(teb, b->bounds);
    } else {
        atomic_fetch_add_explicit(&preempted, 1, memory_order_relaxed);
    }

    ResumeThread(thread);
}
#else
// preempt_worker asks the worker's thread to leave its task: a block is
// made ready for the handler, and the thread is signalled.
SN_HELD_STATIC static void preempt_worker(worker *w) {
    w->preempt_tries++; // an attempt, whether or not it can be made: a worker never asked would never get a thread
    pthread_t thread = atomic_load(&w->thread);
    if (thread != 0 && preempt_spare(w)) { // a worker added has its thread once the thread has started
        pthread_kill(thread, preempt_signal);
    }
}
#endif

// The monitor thread watches the workers for what they cannot see to
// themselves while each runs a task. A task woken by a running
// task goes in that worker's runnext slot, to run there when the
// running task lets go, and no worker is woken for it, since a thread
// woken for every hand-off is what the slot saves; Go's runtime wakes
// one, and lets it take the slot after a few microseconds. A task that
// does not let go, computing without a wait, would keep the slot's
// task, with every idle worker asleep by then and nothing to wake one.
// The monitor looks, while any worker is busy, for a task left in the
// slot of a worker that has switched to no task since its look before,
// next_stale or more ago, and moves it to the inject queue, which
// wakes an idle worker to run it. It sleeps while every worker is idle,
// as Go's sysmon does, since only a running task fills a slot, and the
// first worker to leave idle wakes it: it reads the count under its
// lock before it waits, and the worker signals under the same lock
// after the count changes, so no wake is lost. It pauses next_stale
// between looks after a move, and twice as long after each look that
// finds nothing, up to pause_most, as sysmon backs off, so that a
// program computing or blocked in a read costs a wake every ten
// milliseconds rather than every one: a task is moved within two
// milliseconds while hand-offs are being left and within about ten
// otherwise, the quantum a preempting scheduler gives a task. While
// every worker runs tasks that do not let go, no worker looks at the
// sockets' poller, and none waits in it: the monitor looks at it when
// no one has since its last look, and queues what it finds, so that a
// socket's task waits no longer than a stale slot's.
//
// The monitor also watches for a worker held: a task that never lets
// go, computing or blocked in a read or a bound function, with a task
// queued that it will not come back for. It keeps, for each worker,
// the time of its look that last found the worker switched to a task,
// and once a worker has been on one task for held_limit while a task
// is queued, it has the task taken off the worker, preempt_worker
// below: a signal to the thread on Linux and macOS, a suspension on
// Windows, which leaves the task at a safe point, and the worker runs
// the queued task. A task blocked in a system call or a bound function
// is at no safe point and never will be; when every worker has been
// asked preempt_tries times and stays, the monitor starts another
// worker, up to workers_per_core a core, which takes the queued task,
// and the system shares the processors between the threads, as Go's
// runtime hands a thread off from a goroutine blocked in a system
// call. An added worker is not ended: it waits when idle as the others
// do.
static sn_mutex monitor_mu = SN_MUTEX_INIT;
static sn_cond monitor_cond = SN_COND_INIT;

// move_next moves a task left stale in w's slot to the inject queue,
// and reports whether there was one: stale when w has switched to no
// task since the monitor's last look, which *seen keeps, so its running
// task has not let go in all that time. The task counts as moving from
// before it leaves the slot until it is on the queue, so that
// deadlocked never finds it in neither.
SN_HELD_STATIC static bool move_next(worker *w, uint64_t *seen) {
    uint64_t switches = atomic_load_explicit(&w->switches, memory_order_relaxed);
    bool stale = switches == *seen;
    *seen = switches;
    if (!stale || atomic_load(&w->runnext) == NULL) {
        return false;
    }

    atomic_fetch_add(&hot.moving, 1);
    task *t = take_next(w);
    if (t != NULL) {
        inject(t);
    }

    atomic_fetch_sub(&hot.moving, 1);
    return t != NULL;
}

SN_HELD_STATIC static void monitor_wake(void) {
    sn_mutex_lock(&monitor_mu);
    sn_cond_signal(&monitor_cond);
    sn_mutex_unlock(&monitor_mu);
}

// monitor_poll looks at the sockets' poller for the monitor when no
// one has since seen, the count of looks it read last, and reports
// whether the look made a task runnable.
SN_HELD_STATIC static bool monitor_poll(uint64_t seen) {
    if (atomic_load_explicit(&polls, memory_order_relaxed) != seen || sn_net_waiting() == 0 || atomic_load(&keeping)) {
        return false;
    }

    sn_task *woke[2 * SN_POLL_EVENTS];
    int taken;
    int n = sn_net_poll(woke, &taken);
    atomic_fetch_add_explicit(&polls, 1, memory_order_relaxed);
    if (n == 1) {
        inject((task *)woke[0]);
    } else if (n > 1) {
        inject_all((task **)woke, n);
    }

    sn_net_kept(taken);
    return n > 0;
}

SN_HELD_STATIC static void *monitor_main(void *arg) {
    (void)arg;
    int64_t pause = next_stale;
    uint64_t seen = 0;
    // Each worker's count of switches at the monitor's last look, and
    // when a look last found each switched to a task.
    uint64_t *switches = calloc((size_t)workers_most, sizeof *switches);
    int64_t *moved_at = calloc((size_t)workers_most, sizeof *moved_at);
    if (switches == NULL || moved_at == NULL) {
        sn_panic(SN_STR("out of memory", 13));
    }
    for (;;) {
        sn_mutex_lock(&monitor_mu);
        while (atomic_load(&hot.idle) == workers_now()) {
            sn_cond_wait(&monitor_cond, &monitor_mu);
            pause = next_stale;
        }

        sn_mutex_unlock(&monitor_mu);
        sn_sleep_ns(pause);
        int64_t now = sn_time_mono();
        bool moved = sn_timers_fire(now); // what came due with every worker busy
        if (moved) {
            sn_sched_timer_set(sn_timers_next()); // a worker may wait by now, and for no deadline
        }

        int n = workers_now();
        int64_t queued = queued_now();
        bool held = atomic_load(&hot.idle) == 0 && n < workers_most; // every worker busy, and room for one more
        bool tried = true; // and every one asked preempt_tries times to leave its task, to no avail: blocked, not
                           // computing, which no signal frees
        for (int i = 0; i < n; i++) {
            uint64_t before = switches[i];
            moved = move_next(&workers[i], &switches[i]) || moved;
            if (switches[i] != before || moved_at[i] == 0) {
                moved_at[i] = now;
                workers[i].preempt_tries = 0;
            }

            bool on = now - moved_at[i] >= held_limit;
            held = held && on;
            if (on && queued > 0 && workers[i].preempt_tries < preempt_tries) {
                preempt_worker(&workers[i]); // leaves its task at a safe point, or not: the next look sees which
                moved = true;
            }

            tried = tried && workers[i].preempt_tries >= preempt_tries;
        }

        if (held && tried && queued > 0) {
            add_worker();
            moved = true; // the next look comes soon: the new worker takes the queued task and slots may be stale
        }

        moved = monitor_poll(seen) || moved;
        seen = atomic_load_explicit(&polls, memory_order_relaxed);

        pause = moved ? next_stale : (pause * 2 < pause_most ? pause * 2 : pause_most);
    }

    return NULL; // never reached: a thread's function has a result to give
}

// sched_loop runs the worker: the next task, or the timers that are
// due, a spin and then a wait for one.
SN_HELD_STATIC static void sched_loop(worker *w) {
#ifdef __linux__
    prctl(
        PR_SET_TIMERSLACK, 1UL, 0UL, 0UL,
        0UL); // a keeper's wait ends at its deadline, not the 50 µs after it a thread's timed wait is given by default
#endif
    bool woken = false;   // out of a wait or a spin with work found: the pushes meanwhile woke no one, so it wakes a
                          // worker for what it leaves
    bool holding = false; // woken to spin, and holding the spin until it finds work or spins it out
    int64_t index = w - workers;
    for (;;) {
        // Its own wheel's timers are fired before it takes its next
        // task, so that a task a timer wakes into the slot is the one it
        // takes; the clock is read only when its wheel holds a timer,
        // since a reading costs about what a switch does. The other
        // wheels are looked at once in outside_turns.
        task *t = NULL;
        int64_t now = 0; // the clock, read once in a turn that reads it at all, since a reading costs about a switch;
                         // never 0 when read, as time.c keeps every clock above it
        bool look = --w->turns <= 0;
        if (look || sn_timers_next_of(index) != INT64_MAX) {
            now = sn_time_mono();
            bool fired = look ? sn_timers_fire(now) : sn_timers_fire_own(index, now);
            if (fired && atomic_load(&hot.idle) > 0) {
                sn_sched_timer_set(
                    sn_timers_next()); // what it fired may run long here: a waiting worker keeps time for the rest
            }
        }

        if (look) {
            w->turns = outside_turns;
            t = take_injected(w, false, true); // the displaced queue's turn
        }

        if (t == NULL) {
            t = next_task(w);
        }

        if (t == NULL) {
            t = steal_next(w);
        }

        if (t != NULL) {
            if (holding) {
                holding = false;
                atomic_store(&hot.spinning, 0);
            }

            if (w->owes_at != INT64_MAX) { // the task may run long: a waiting worker keeps time
                int64_t at = w->owes_at;
                w->owes_at = INT64_MAX;
                sn_sched_timer_set(at);
            }

            if (w->owes_watch) { // and watches the sockets, which a spinner does as well, on its way to keeping
                w->owes_watch = false;
                if (!atomic_load(&keeping) && sn_net_waiting() > 0) {
                    wake_idle();
                }
            }

            if (woken) {
                woken = false;
                if (queued_now() > 0) {
                    wake_idle(); // as Go's spinning M, once it finds work, starts another for the rest
                }
            }

            run_task(w, t);
            continue;
        }

        // Every wheel, at the reading above when there was one, and not
        // again after a look that fired them all.
        if (!look && sn_timers_fire(now != 0 ? now : sn_time_mono())) {
            w->owes_at = sn_timers_next();
            continue; // next_task takes what a timer woke
        }

        if (spin(w, holding)) {
            holding = false; // spin gave the spin up
            woken = true;
            w->owes_at = sn_timers_next(); // a timer started while it spun was told to no one, since a spinner is what
                                           // a wake finds first
            continue; // next_task takes what was found, the inject queue or a steal, or a timer came due
        }

        holding = false;

        bool kept = false;   // it was the keeper in this wait
        bool events = false; // and left it because the poller held events
        sn_qlock_take(&idle_mu);
        atomic_fetch_add(&hot.idle, 1);
        for (;;) {
            if (holding) { // woken to spin and the work taken meanwhile: the spin goes back before the queues are read,
                           // so a push that saw it held is seen here, or sees the worker idle and signals
                holding = false;
                atomic_store(&hot.spinning, 0);
            }

            if (queued_now() > 0) {
                break;
            }

            if (deadlocked()) {
                fflush(stdout);
                fputs("fatal error: all tasks are asleep - deadlock!\n", stderr);
                if (main_task.waits_on != NULL) { // the main task is asleep too: what it waits for names the channel
                    sn_chan_report(main_task.waits_on, main_task.waits_dir);
                }

                exit(2);
            }

            int64_t asleep = sn_time_mono();
            int64_t first = sn_timers_next();
            if (first <= asleep) {
                break; // a timer is due: it leaves the wait to fire it
            }

            bool sockets = false;
            if (!keeping && (first != INT64_MAX ||
                             sn_net_waiting() > 0)) { // no one wakes for the timers, or watches the sockets: it does
                keeping = true;
                atomic_store(&wake_at, first);
                sockets = keeper_wait(first, asleep);
                keeping = false;
                atomic_store(&wake_at, INT64_MAX);
                kept = true;
            } else {
                if (keeping && first < atomic_load(&wake_at)) {
                    keeper_wake(); // the keeper sleeps past the earliest timer: it reads the deadlines again
                }

                atomic_fetch_add(&hot.waiting, 1);
                w->idle_next = idle_head;
                idle_head = w;
                sn_qlock_give(&idle_mu);
                while (atomic_exchange_explicit(&w->park, 0, memory_order_acquire) == 0) { // until a wake took it off
                    sn_word_wait(&w->park, 0);                                             // the waiting workers
                }

                sn_qlock_take(&idle_mu);
            }

            if (handed > 0) { // a signal with a spin woke it; a broadcast or a spurious return hands none
                handed--;
                holding = true;
            }

            if (queued_now() > 0 && sn_time_mono() - asleep < spin_for) {
                spun(true); // a push or a wake from outside came soon enough that a spin would have caught it
            }

            if (sockets) {
                events = true;
                break; // the poller holds events: it looks at it as a worker, keeping no longer
            }
        }

        bool first = atomic_fetch_sub(&hot.idle, 1) == workers_now();
        sn_qlock_give(&idle_mu);
        woken = true;
        w->owes_at =
            sn_timers_next(); // it may have been the keeper, or woken to be: before it runs a task, another keeps
        w->owes_watch = kept; // and another watches the sockets
        w->owes_poll = events;
        if (first) {
            monitor_wake(); // the first worker busy again: the monitor's lock is never taken under idle_mu
        }
    }
}

SN_HELD_STATIC static void *worker_main(void *arg) {
    self_worker = arg;
    preempt_thread_start(self_worker);
    sched_loop(self_worker);
    return NULL;
}

// sched_entry is where the main thread's scheduler context begins,
// on the stack the main thread took when the main task first parked:
// that park is settled first.
SN_HELD_STATIC static void sched_entry(void) {
    settle(self_worker);
    sched_loop(self_worker);
}

// start_thread starts a detached thread of the runtime's own.
SN_HELD_STATIC static void start_thread(void *(*main)(void *), void *arg) {
    if (!sn_thread_start(main, arg)) {
        sn_panic(SN_STR("cannot start a thread", 21));
    }
}

// sn_cores is the number of processors the program may run on, at
// least one: the processors in the thread's affinity mask on Linux,
// as Go's runtime.NumCPU counts them, so that a program held to a
// few processors by taskset or a container starts a worker for each
// of those; the processors in the process's affinity mask on Windows;
// the processors online on macOS.
SN_HELD int64_t sn_cores(void) {
    long n = 0;
#if defined(_WIN32)
    // The processors in the process's affinity mask, in the group the
    // process runs in, so that a program held to a few by a job object
    // or start /affinity starts a worker for each of those.
    DWORD_PTR mask = 0, system = 0;
    if (GetProcessAffinityMask(GetCurrentProcess(), &mask, &system)) {
        n = __builtin_popcountll((unsigned long long)mask);
    }

    if (n <= 0) {
        n = (long)GetActiveProcessorCount(ALL_PROCESSOR_GROUPS);
    }
#else
#ifdef __linux__
    cpu_set_t set;
    if (sched_getaffinity(0, sizeof set, &set) == 0) {
        n = CPU_COUNT(&set);
    }
#endif
    if (n <= 0) {
        n = sysconf(_SC_NPROCESSORS_ONLN);
    }
#endif

    return n > 1 ? (int64_t)n : 1;
}

// start makes the workers, the main thread the first and one thread
// per further core, the timers' wheels and the monitor. The workers
// are allocated for the most the monitor may grow them to, so that no
// worker's address moves when one is added, and only the started ones
// are written, so that the pages of the others stay untouched until
// one is added.
SN_HELD_STATIC static void start(void) {
    sn_mutex_lock(&start_mu);
    if (!started) {
        int n = (int)sn_cores();
        workers_most = n * workers_per_core;
        workers = sn_aligned_alloc(SN_CACHE_LINE, (size_t)workers_most * sizeof *workers); // each on lines of its own
        if (workers == NULL) {
            sn_panic(SN_STR("out of memory", 13));
        }

        memset(workers, 0, (size_t)workers_most * sizeof *workers);

        keeper_start();
        sn_timers_start(n, workers_most);

        if (my_worker() == NULL) {
            self_worker = &workers[0]; // the main thread
        }

        preempt_start();
        preempt_thread_start(&workers[0]);
        for (int i = 0; i < n; i++) {
            workers[i].owes_at = INT64_MAX;
        }

        atomic_store(&nworkers, n);
        started = true;
        for (int i = 1; i < n; i++) {
            start_thread(worker_main, &workers[i]);
        }

        start_thread(monitor_main, NULL);
    }

    sn_mutex_unlock(&start_mu);
}

// add_worker starts one more worker, for the monitor: the worker and
// its wheel are made before the count of workers grows, and the thread
// starts last. The count grows before the thread, so that the worker's
// first idle is counted against a count that holds it; a thread that
// cannot start ends the program, as it does at start, so no counted
// worker is ever missing.
SN_HELD_STATIC static void add_worker(void) {
    int i = workers_now();
    workers[i].owes_at = INT64_MAX;
    sn_timers_add();
    atomic_store(&nworkers, i + 1);
    start_thread(worker_main, &workers[i]);
}

// sched_prepare starts the workers the first time, and gives the main
// thread the scheduler context it switches to when the main task parks.
// Both happen before the first task runs, on the main thread's own
// stack, and so through sn_on_stack below the caller's frame: a spawned
// task reaches the call, since a task spawns and parks as the main task
// does, but never makes it, since a task runs only once both are done,
// and what sched_prepare calls, the threads' start and a guarded
// stack's mapping, so counts against the bound of no task's small
// stack.
SN_HELD_STATIC static void *sched_prepare(void *arg) {
    (void)arg;
    if (!started) {
        start();
    }

    worker *w = my_worker();
    if (w->sched_sp == NULL) { // the main thread, before its first park
        w->sched_stack = stack_take(NULL, guarded);
        w->sched_sp = make_context((uint8_t *)w->sched_stack + stack_size, sched_entry);
#if defined(_WIN32)
        set_bounds(w->sched_sp, (uint8_t *)w->sched_stack + stack_size, (uint8_t *)w->sched_stack + sn_page_size(),
                   w->sched_stack);
#endif
    }

    return NULL;
}

static inline void sched_ready(void) {
    worker *w;
    if (!started || (w = my_worker()) == NULL || w->sched_sp == NULL) {
        uintptr_t sp = stack_pointer();
        ON_STACK((void *)((sp - below_frame) & ~(uintptr_t)(stack_align - 1)), NULL, sched_prepare,
                 NULL); // the stack it is on
    }
}

// sn_sched_start starts the workers if they are not, for a timer
// started before any task was spawned; sn_worker_index is the index of
// the current thread's worker, which every thread that starts a timer
// has.
SN_HELD void sn_sched_start(void) { sched_ready(); }

SN_HELD int64_t sn_worker_index(void) { return my_worker() - workers; }

SN_HELD int64_t sn_sched_preempted(void) { return atomic_load_explicit(&preempted, memory_order_relaxed); }

SN_HELD int64_t sn_sched_workers(void) { return workers_now(); }

// sn_sched_timer_set has a worker keep time for a timer started at
// when, if the keeper sleeps past it or there is none: the keeper reads
// the deadlines again, or a waiting worker is woken as it is for work,
// one at a time, and finds no task and keeps time in its wait; until it
// is there no other is woken, so that the workers running tasks that
// sleep take no lock for each. With no worker idle there is no one to
// tell: the first to wait reads the deadlines itself. A keeper that
// watches the sockets alone waits with no deadline, wake_at the
// greatest time, and is woken as any keeper is, not taken for none.
SN_HELD void sn_sched_timer_set(int64_t when) {
    int64_t kept = atomic_load(&wake_at);
    if (when >= kept || atomic_load(&hot.idle) == 0) {
        return;
    }

    if (kept == INT64_MAX && !atomic_load(&keeping)) {
        wake_idle();
        return;
    }

    sn_qlock_take(&idle_mu);
    if (keeping && when < atomic_load(&wake_at)) {
        keeper_wake();
    }

    sn_qlock_give(&idle_mu);
}

// sn_sched_owe notes a deadline the worker sees to once the task that
// started it has parked: on its way to its next task, or in its wait.
SN_HELD void sn_sched_owe(int64_t when) {
    worker *w = my_worker();
    if (when < w->owes_at) {
        w->owes_at = when;
    }
}

// task_entry is where a task's context begins: the function with its
// argument block, and then the switch back that run_task frees it
// after. It never returns, since nothing called it.
SN_HELD_STATIC static void task_entry(void) {
    task *t = my_task();
    t->run(t->arg);
    if (atomic_load_explicit(&t->scope.scope, memory_order_relaxed) != NULL || // the task's own word, which an Add
        t->scope.enlisted) { // wrote under a lock it took since; or in the root's list since a wait, in no scope
        sn_scope_end(t);     // before DONE: a cancel that finds it in its scope wakes a task that has not ended
    }
    atomic_store(&t->state, DONE);
    sn_switch(&t->sp, my_worker()->sched_sp);
    static const char message[] = "fatal error: a task that ended was resumed\n";
    (void)sn_write_fd(2, (const uint8_t *)message, sizeof message - 1);
    abort();
}

// spawn starts a task that runs run on a copy of its arguments, on a
// stack of bound bytes, what the analysis of the linked program proved
// the task can use, or on a guarded stack for SN_UNBOUNDED; the task is
// in its spawner's scope, or, for a handle whose channel is done, in a
// scope of its own under it, made when it is needed.
SN_HELD_STATIC static void spawn(void (*run)(void *), const void *args, size_t size, uint32_t bound, sn_chan *done) {
    sched_ready();

    task *self = sn_self();
    worker *w = my_worker(); // read once: the thread-local costs a call on Darwin
    // Its own block: a record at the top of the task's stack was measured
    // and made a spawn two thirds slower. A release build takes it from
    // the runtime's heap, uncounted, since the program did not ask for
    // it, whose caches suit a block freed by another thread than the one
    // that made it, as a task's record is: glibc's malloc made a spawn on
    // Linux two fifths dearer. A debug build allocates each anew, and
    // frees a record once it is older than its worker's last ended_kept.
    int c = stack_class(bound);
    void *heap = NULL;     // the arguments' room when the record's is too small: taken first, so that a panic for
    if (size > few_args) { // want of it leaves no record taken
        heap = malloc(size);
        if (heap == NULL) {
            sn_panic(SN_STR("out of memory", 13));
        }
    }

#ifdef SN_DEBUG
    task *t = calloc(1, sizeof *t);
#else
    task *t = dead_take(w, c);
    if (t != NULL) { // an ended task's record with its stack: cleared, but for the stack it keeps
        void *stack = t->stack;
        memset(t, 0, sizeof *t);
        t->stack = stack;
    } else if ((t = sn_heap_alloc(sizeof *t)) != NULL) {
        memset(t, 0, sizeof *t);
    }
#endif
    if (t == NULL) {
        sn_panic(SN_STR("out of memory", 13));
    }

    t->run = run;
    t->arg = heap != NULL ? heap : t->args;
    if (size > 0) {
        memcpy(t->arg, args, size);
    }

    t->stack_class = c;
    if (t->stack == NULL) {
        t->stack = stack_take(w, c); // the spawning thread's worker, or the pool for a thread that is none
    }

#ifdef SN_DEBUG
    debug_list(t);
    t->bound = bound;
    if (t->stack_class != guarded) {
        memset(t->stack, stack_paint, class_size(t->stack_class));
    }
#endif
    t->sp = make_context((uint8_t *)t->stack + class_size(t->stack_class), task_entry);
#if defined(_WIN32)
    set_bounds(t->sp, (uint8_t *)t->stack + class_size(t->stack_class),
               (uint8_t *)t->stack + (t->stack_class == guarded ? sn_page_size() : 0), t->stack);
#endif
    if (done != NULL) {
        sn_scope_own(done, t, self);
    } else if (atomic_load_explicit(&self->scope.scope, memory_order_relaxed) != NULL || self->scope.handle != NULL) {
        sn_scope_inherit(t, self); // in a scope, or spawned for a handle and so to have one of its own now
    }

    atomic_store(&t->state, RUNNABLE);
    if (w != NULL) {
        atomic_fetch_add_explicit(&w->running, 1, memory_order_relaxed); // its own line: no other worker writes it
        push(w, t); // the spawning thread's own ring, which its thread alone writes
    } else {
        atomic_fetch_add_explicit(&workers[0].running, 1, memory_order_relaxed); // a thread that is no worker, which
                                                                                 // none is today, counts on the first
        inject(t);
    }
}

SN_HELD void sn_spawn(void (*run)(void *), const void *args, size_t size, uint32_t bound) {
    spawn(run, args, size, bound, NULL);
}

// sn_spawn_handle starts a task for a handle, whose channel is done: the
// task has a scope of its own, made when it is needed, which a group
// that takes the handle takes in.
SN_HELD void sn_spawn_handle(void (*run)(void *), const void *args, size_t size, uint32_t bound, sn_chan *done) {
    spawn(run, args, size, bound, done);
}

SN_HELD int64_t sn_tasks_running(void) {
    int64_t n = 0;
    for (int i = 0; i < workers_now(); i++) {
        n += atomic_load_explicit(&workers[i].running, memory_order_relaxed);
    }

    return n;
}

// sn_park waits for a wake: the task switches to its worker's
// scheduler, which settles it as parked, and resumes when a wake puts
// it back on a queue and a worker runs it. A wake that came first is
// consumed at once. The main task sleeping before any spawn starts the
// workers, and the main thread's scheduler context is made the first
// time the main task parks, if no spawn made it.
SN_HELD void sn_park(void) {
    task *t = sn_self();
    sched_ready();
    worker *w = my_worker();

    for (;;) {
        int s = atomic_load(&t->state); // RUNNING, with WOKEN when a wake came first
        if (s & WOKEN) {
            if (atomic_compare_exchange_strong(&t->state, &s, RUNNING)) {
                return; // the wake is consumed and the task goes on
            }

            continue;
        }

        if (!atomic_compare_exchange_strong(&t->state, &s, PARKING)) {
            continue; // a wake came between the read and the swap
        }

        w->last = t;
        sn_switch(&t->sp, w->sched_sp);
        atomic_fetch_and(&t->state, ~WOKEN); // resumed by a wake, on whichever worker ran the queue: one that came
                                             // after it is the same wake, whose condition the caller reads again
        return;
    }
}

// A wake has two halves. sn_wake_claim makes a parked task runnable
// and reports that the caller is to queue it, or keeps the wake for a
// task still running or parking, which consumes it at its next park,
// and reports that there is nothing to queue. sn_wake_queue queues a
// task claimed. Between the two the task is the caller's alone: no one else
// can run it, so what is on its stack stands, and a caller that claimed
// under a lock of its own may queue once it has let the lock go.
SN_HELD bool sn_wake_claim(sn_task *t) {
    for (;;) {
        int s = atomic_load(&t->state);
        if (s == PARKED) {
            if (atomic_compare_exchange_strong(&t->state, &s, RUNNABLE)) {
                return true;
            }

            continue;
        }

#ifdef SN_DEBUG
        // Its waker went on after the task could end: a wake from outside
        // a lock that skipped the handoff.
        if (s == DONE) {
            fflush(stdout);
            fputs("fatal error: a wake reached a task that has ended\n", stderr);
            exit(2);
        }
#endif
        // A wake already kept is written over with itself: the task may
        // be consuming it at its park, and then reads what its waker
        // wrote before the wake, a handoff's waking or a cancel's mark.
        // A claim that wrote nothing would be in no order with that
        // consume, and a processor may move the waker's earlier store
        // past its read of the state, so the task would read the old
        // value, park, and never be queued. Written, the claim is ordered
        // with the consume: before it, the consume reads the claim's
        // write and with it what the waker wrote; after it, the
        // compare-and-swap fails and the claim reads the state again,
        // keeping a wake of its own or making the parked task runnable.
        if (s == DONE || atomic_compare_exchange_strong(&t->state, &s, s | WOKEN)) {
            return false; // the wake is kept for the task's park, or the task has ended
        }
    }
}

SN_HELD void sn_wake_queue(sn_task *t) {
    worker *w = my_worker();
    if (w != NULL) {
        put_next(w, t); // woken on a worker: it runs next there
    } else {
        inject(t); // by a resolver's thread, the monitor or a timer fired off the workers
    }
}

// sn_handoff_wake wakes t through h, as internal.h says: waking while
// the claim reads the task, woken once it is done with it, and the task
// queued last, when the claim made it runnable.
SN_HELD void sn_handoff_wake(sn_handoff *h, sn_task *t) {
    if (sn_handoff_claim(h, t)) {
        sn_wake_queue(t);
    }
}

// sn_handoff_claim is sn_handoff_wake's first half: it claims t through
// h, and reports whether the caller is to queue it, which it may do
// after the handoff says woken, since a task claimed runs nowhere.
SN_HELD bool sn_handoff_claim(sn_handoff *h, sn_task *t) {
    atomic_store_explicit(h, SN_HANDOFF_WAKING, memory_order_release);
    bool claimed = sn_wake_claim(t);
    atomic_store_explicit(h, SN_HANDOFF_WOKEN, memory_order_release);
    return claimed;
}

// sn_handoff_wait waits, in a task a waker took out, until the waker is
// done with it: parked while the handoff is open, since the wake is to
// come, and spinning while the claim is at the task. A park that ends
// for a wake kept from an earlier wait parks again.
SN_HELD void sn_handoff_wait(sn_handoff *h) {
    for (;;) {
        int at = atomic_load_explicit(h, memory_order_acquire);
        if (at == SN_HANDOFF_WOKEN) {
            return;
        }

        if (at == SN_HANDOFF_OPEN) {
            sn_park();
        } else {
            sn_spin_hint();
        }
    }
}

// sn_wake makes a parked task runnable, or keeps the wake for a task
// still running or parking.
SN_HELD void sn_wake(sn_task *t) {
    if (sn_wake_claim(t)) {
        sn_wake_queue(t);
    }
}

// The guard stack of a task: the values a panic drops, innermost
// last. Its first few are in the task's record; past them it grows
// with the system allocator, not sn_alloc, so a debug build does not
// count it among the program's blocks. A panic unwinds the task it
// happens in and no other, since the others are on other workers or
// parked in the middle of their work.
SN_HELD void sn_guard(void *p, void (*drop)(void *)) {
    task *t = sn_self();
    if (t->nguards == t->guardcap) {
        if (t->guards == NULL) {
            t->guards = t->few;
            t->guardcap = few_guards;
        } else {
            int64_t cap = t->guardcap < guards_grow ? guards_grow : t->guardcap * 2;
            guard *more = t->guards == t->few ? malloc((size_t)cap * sizeof *more)
                                              : realloc(t->guards, (size_t)cap * sizeof *more);
            if (more == NULL) {
                sn_panic(SN_STR("out of memory", 13)); // the stack so far is intact for the unwinding
            }

            if (t->guards == t->few) {
                memcpy(more, t->few, sizeof t->few);
            }

            t->guards = more;
            t->guardcap = cap;
        }
    }

    t->guards[t->nguards++] = (guard){ p, drop };
}

// sn_unguard removes the guard of p, searched from the top: a value is
// usually dropped in the reverse order of its registration, but a
// temporary of a statement may be dropped after a variable the
// statement declared.
SN_HELD void sn_unguard(void *p) {
    task *t = sn_self();
    for (int64_t i = t->nguards - 1; i >= 0; i--) {
        if (t->guards[i].p == p) {
            for (sn_mark *m = t->marks; m != NULL && m->nguards > i; m = m->prev) {
                m->nguards--; // a guard below a mark removed: the mark's count moves with it, though the ownership
                              // rules let no value declared above a mark be dropped under it
            }

            memmove(&t->guards[i], &t->guards[i + 1], (size_t)(t->nguards - i - 1) * sizeof *t->guards);
            t->nguards--;
            return;
        }
    }
}

// sn_panic prints the message, then runs the drops of the guarded
// values, innermost first, and ends the program. A Drop that panics
// during that ends the program with its own message at once.
// sn_catch runs fn on the current task under a mark and returns
// whether a panic ended it, with the panic's message in *msg, owned by
// the caller, when one did; with report the panic is printed as an
// uncaught one is, which a task started for a handle asks and a test
// does not. Marks nest, a subtest's inside its test's.
// The jump saves no signal mask, and the task may resume on another
// thread between the mark and the panic: the jump restores the
// registers and the stack pointer of the mark's frame, which are the
// task's own, and nothing of the thread's. Nothing of the mark or of
// the task is read after the jump: sn_panic wrote the message to the
// caller's slot before it jumped, and a local of this frame changed
// since the mark would be indeterminate to read.
SN_HELD bool sn_catch(void (*fn)(void *), void *ctx, sn_string *msg, bool report) {
    task *t = sn_self();
    if (!report && t->trace == NULL) { // a catch that reports not is a test's runner's, which prints the trace itself
        t->trace = malloc(SN_TRACE_FRAMES * sizeof *t->trace);
    }

    t->ntrace = 0;
    sn_mark m;
    m.nguards = t->nguards;
    m.out = msg;
    m.report = report;
    m.prev = t->marks;
    t->marks = &m;
    if (sn_setjmp(m.jump) == 0) {
        fn(ctx);
        sn_self()->marks =
            m.prev; // the task asked for again: no local of this frame set before the mark is read after it
        return false;
    }

    return true; // the panic took the mark off, unwound to it and wrote *msg
}

// The lazies: one lock and one condition for every lazy package
// variable, since making one is rare and short. A task that finds a
// variable being made by another waits here, holding its worker,
// which is what an initializer that waits for another task would
// deadlock on; an initializer is plain code.
static sn_mutex lazy_mu = SN_MUTEX_INIT;
static sn_cond lazy_cond = SN_COND_INIT;

// lazy_abandon is the guard of an initializer that panics: the
// variable is unmade again and whoever waits is woken, so that a later
// use under a test makes it anew.
SN_HELD_STATIC static void lazy_abandon(void *p) {
    sn_lazy *o = p;
    sn_mutex_lock(&lazy_mu);
    __atomic_store_n(&o->state, 0, __ATOMIC_RELEASE);
    o->maker = NULL;
    sn_cond_broadcast(&lazy_cond);
    sn_mutex_unlock(&lazy_mu);
}

// sn_lazy_make is never inlined, so that the analysis of a linked
// program finds its call of the initializer by its name and takes the
// initializers the backends write for its targets.
SN_HELD void sn_lazy_make(sn_lazy *o, void (*init)(void), const char *name) {
    task *self = sn_self();
    sn_mutex_lock(&lazy_mu);
    for (;;) {
        int state = __atomic_load_n(&o->state, __ATOMIC_ACQUIRE);
        if (state == 2) {
            sn_mutex_unlock(&lazy_mu);
            return;
        }

        if (state == 0) {
            break;
        }

        if (o->maker == self) {
            sn_mutex_unlock(&lazy_mu);
            char buf[192];
            snprintf(buf, sizeof buf, "package variable %s is read by its own initializer", name);
            sn_panic(SN_STR(buf, (int64_t)strlen(buf)));
        }

        sn_cond_wait(&lazy_cond, &lazy_mu);
    }

    __atomic_store_n(&o->state, 1, __ATOMIC_RELAXED);
    o->maker = self;
    sn_mutex_unlock(&lazy_mu);
    sn_guard(o, lazy_abandon);
    init();
    sn_unguard(o);
    sn_mutex_lock(&lazy_mu);
    o->maker = NULL;
    __atomic_store_n(&o->state, 2, __ATOMIC_RELEASE);
    sn_cond_broadcast(&lazy_cond);
    sn_mutex_unlock(&lazy_mu);
}

// contained counts the panics a task's handle contained: the memory
// of the frames each abandoned is never released, which the debug
// build's report of what was not released says.
static _Atomic int64_t contained;

SN_HELD int64_t sn_panics_contained(void) { return atomic_load(&contained); }

// print_panic writes the panic's line on the standard error stream,
// after what the program printed so far, in one piece: tasks on
// several workers may fail at once, and each call of stdio is locked
// alone, so the line is written under the stream's lock.
// print_panic prints the message, the message of the panic being
// unwound when a drop of that unwinding panicked, and the trace of the
// panicking task's frames, under the stream's lock, so that two tasks
// failing at once do not interleave.
SN_HELD_STATIC static void print_panic(task *t, sn_string msg) {
    fflush(stdout);
    sn_lock_file(stderr);
    fputs("panic: ", stderr);
    fwrite(msg.ptr, 1, (size_t)msg.len, stderr);
    fputc('\n', stderr);
    if (t != NULL && t->unwound.len > 0) {
        fputs("\twhile unwinding from panic: ", stderr);
        fwrite(t->unwound.ptr, 1, (size_t)t->unwound.len, stderr);
        fputc('\n', stderr);
    }

    sn_trace_write();
    sn_unlock_file(stderr);
}

// panic_args is what sn_panic hands the panic on the stack it runs on:
// the task, the message, and the frame pointer of sn_panic's own frame,
// on the task's stack, where the walk of the trace starts; on Windows'
// x64, where frame pointers make no chain, the registers sn_panic
// saved, which the system's unwinder starts from instead.
typedef struct {
    task *t;
    sn_string msg;
    void *fp;
#if defined(_WIN32) && defined(__x86_64__)
    const uint64_t *regs;
#endif
} panic_args;

// trace_fill walks the panicking task's frames from the panic's frame
// pointer into the thread's buffer, within the task's stack when it has
// one of its own and the guarded stack a panic moved it to, or on
// Windows' x64 unwinds them from its registers, and keeps a copy for
// the task when it keeps one.
SN_HELD_STATIC static void trace_fill(const panic_args *a) {
    task *t = a->t;
#if defined(_WIN32) && defined(__x86_64__)
    sn_trace_unwind(a->regs); // from sn_panic's registers, through the image's unwind data, here on the panic's stack
#else
    sn_stack_region regions[2] = { 0 }; // at most two: the task's stack and the guarded one
    int n = 0;
    if (t != NULL && t->stack != NULL) {
        regions[n++] = (sn_stack_region){ t->stack, (uint8_t *)t->stack + class_size(t->stack_class) };
        if (t->large != NULL) {
            regions[n++] = (sn_stack_region){ t->large, (uint8_t *)t->large + stack_size };
        }
    }

    sn_trace_fill(a->fp, regions, n);
#endif
    if (t != NULL && t->trace != NULL) {
        t->ntrace = sn_trace_copy(t->trace);
    }
}

// sn_task_trace gives the frames of the running task's last panic,
// kept under a catch that keeps them, and how many, for the test's
// runner; none when the task keeps none.
SN_HELD const uintptr_t *sn_task_trace(int *n) {
    task *t = sn_self();
    *n = t->ntrace;
    return t->trace;
}

// panic_stack takes the guarded stack a panic on a task's small stack
// runs on, called on the scheduler's stack, below the frames of the
// scheduler that switched to the task, and returns before the panic
// runs: a drop the panic runs may park the task, and the scheduler's
// frames then grow down over whatever was left there. With no memory to
// map it ends the process: the panic cannot be run, nor reported
// through a mark, without a stack.
SN_HELD_STATIC static void *panic_stack(void *arg) {
    (void)arg;
    const char *failure = NULL;
    void *s = stack_new(my_worker(), guarded, &failure);
    if (s == NULL) {
        fflush(stdout);
        fputs("fatal error: ", stderr);
        fputs(failure, stderr);
        fputs(" for a panic\n", stderr);
        exit(2);
    }

    return s;
}

// panic_unwind is the panic, on the stack sn_panic moved it to: to a
// mark it unwinds and returns the mark, for sn_panic to jump to from
// the task's own stack; with none it ends the process.
SN_HELD_STATIC static void *panic_unwind(void *arg) {
    panic_args *a = arg;
    task *t = a->t;
    sn_string msg = a->msg;
    trace_fill(a); // before the drops, which the frames are whole until; kept for a test's runner
    if (t != NULL && t->marks != NULL && !t->unwinding) {
        sn_mark *m = t->marks;
        t->marks = m->prev;
        if (m->report) {
            print_panic(t, msg);
            atomic_fetch_add(&contained, 1);
        }

        *m->out = sn_string_clone(msg); // before the unwinding drops what the message may be part of
        t->unwound = *m->out;
        t->unwinding = true;
        while (t->nguards > m->nguards) {
            guard g = t->guards[--t->nguards];
            g.drop(g.p);
        }

        t->unwinding = false;
        t->unwound = (sn_string){ 0 };
        return m;
    }

    if (t == NULL) {
        t = sn_self();
    }

    print_panic(t, msg);
    if (!t->unwinding) {
        t->unwinding = true;
        while (t->nguards > 0) {
            guard g = t->guards[--t->nguards];
            g.drop(g.p);
        }
    }

    fflush(stdout);
    exit(2);
}

// sn_panic ends the task's work with the message: on a task with a
// mark, by dropping the values guarded since the mark, with the
// message cloned first since those drops may free what it views, and
// jumping to the mark with it; on any other, and while a drop of that
// unwinding panics itself, by printing the message, dropping every
// guarded value and ending the process, as a panic always did. A drop
// that panics while unwinding to a mark ends the process too, since
// the first panic can no longer be reported through the mark, and
// its message is printed with the drop's so that neither is lost.
//
// A task on a small stack, which holds what its own code uses and no
// more, runs the panic on a guarded stack of its own, taken on the
// scheduler's stack the first time and kept until the task ends; any
// other runs it below the panic's frame. Either way the panic runs
// through sn_on_stack, so that what it runs, the drops of any value,
// never counts against the bound of a task's stack, and the jump to a
// mark is made from the task's own stack, up to a frame above it, as a
// checking build of the C library requires of a jump.
SN_HELD _Noreturn void sn_panic(sn_string msg) {
    // A thread of the runtime's own, the timers' or the poller's, has no
    // task and never jumps into the main task's mark.
    panic_args a = { .t = my_task(), .msg = msg, .fp = __builtin_frame_address(0) };
#if defined(_WIN32) && defined(__x86_64__)
    sn_jmp_buf regs; // the registers the trace unwinds from, a few words on the task's stack
    sn_setjmp_regs(regs);
    a.regs = regs;
#endif
    uintptr_t sp = stack_pointer();
    void *top = (void *)((sp - below_frame) & ~(uintptr_t)(stack_align - 1));
    task *t = a.t;
    if (t != NULL && t->stack_class != guarded && sp - (uintptr_t)t->stack < class_size(t->stack_class)) {
        if (t->large == NULL) {
            uintptr_t sched =
                (uintptr_t)my_worker()->sched_sp; // the scheduler that switched to the task, whose frames are above
            sn_stack_bounds on = { 0 };           // the scheduler's stack's, which Windows keeps
#if defined(_WIN32)
            on = frame_bounds((void *)sched);
#endif
            t->large =
                ON_STACK((void *)((sched - below_frame) & ~(uintptr_t)(stack_align - 1)), &on, panic_stack, NULL);
        }

        top = (uint8_t *)t->large + stack_size;
    }

    const sn_stack_bounds *bounds = NULL; // the guarded stack's the panic moves to, or none to stay on the one it is on
#if defined(_WIN32)
    sn_stack_bounds large;
    if (t != NULL && t->large != NULL && top == (uint8_t *)t->large + stack_size) {
        large = (sn_stack_bounds){ top, (uint8_t *)t->large + sn_page_size(), t->large };
        bounds = &large;
    }
#endif
    sn_mark *m = ON_STACK(top, bounds, panic_unwind, &a);
    sn_longjmp(m->jump);
}
