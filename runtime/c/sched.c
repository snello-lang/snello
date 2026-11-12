// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The scheduler: step two of _docs/design/06-tasks-and-channels.md.
// A task is a coroutine with a stack of its own, reserved from the
// system and committed by it page by page as the task touches it, and
// a saved set of callee-saved registers; switching from one task to
// another is the assembly routine sn_switch below, one per processor.
// N worker threads, one per core with the main thread among them, run
// M tasks: each worker keeps a queue of runnable tasks and steals from
// another's when its own is empty; a worker with nothing to run waits
// to be told of work. A task that must wait, on a channel, a lock or
// a timer, parks: it switches to its worker's scheduler, which runs
// the next task, and whoever wakes it puts it back on a queue. A task
// runs until it parks or ends: the scheduler does not preempt, so a
// task that computes without ever waiting keeps its worker, and the
// others keep running on the rest; a task it wakes meanwhile goes to
// another worker through the monitor thread below, so that it holds
// no task but its own. The timers are timer.c's; the workers fire them
// between their tasks, as the section on them below says.
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
#include <pthread.h>
#include <setjmp.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <time.h>
#include <unistd.h>

#if defined(__APPLE__)
#include <fcntl.h>
#include <sys/event.h>
#endif

#include <sched.h>

#ifdef __linux__
#include <sys/prctl.h>
#endif

// sn_switch saves the callee-saved registers and the stack pointer of
// the running context into *save, and resumes the context whose stack
// pointer is to, which was saved the same way, or made by make_context
// for a context that has not run yet. The frame it saves is 160 bytes
// on arm64: x19 to x28, x29 and x30, then d8 to d15; and 64 bytes on
// x86-64: rbp, rbx and r12 to r15, pushed, with the return address the
// caller left above them. The symbol carries a leading underscore on
// Darwin, as every C symbol does there.
void sn_switch(void **save, void *to);

#if defined(__APPLE__)
#define SN_SYM(name) "_" #name
#else
#define SN_SYM(name) #name
#endif

#if defined(__aarch64__)
#define SN_FRAME 160
__asm__(
    ".text\n"
    ".globl " SN_SYM(sn_switch) "\n"
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
    "  ret\n");
#elif defined(__x86_64__)
#define SN_FRAME 64
__asm__(
    ".text\n"
    ".globl " SN_SYM(sn_switch) "\n" SN_SYM(sn_switch) ":\n"
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
    "  ret\n");
#else
#error "the Snello runtime has no context switch for this processor"
#endif

// make_context lays out the frame sn_switch restores at the top of a
// fresh stack, so that the first switch to it returns into entry with
// an aligned stack and every saved register zero. On x86-64 the
// return address sits one word below the top, so that entry begins as
// a called function does, with the stack pointer eight past a
// sixteen-byte boundary.
static void *make_context(void *top, void (*entry)(void)) {
    uint8_t *sp = (uint8_t *)top - SN_FRAME;
    memset(sp, 0, SN_FRAME);
#if defined(__aarch64__)
    ((void **)sp)[11] = (void *)entry; // x30, the return address
#else
    ((void **)sp)[6] = (void *)entry; // above the six pushed registers
#endif
    return sp;
}

// A task's state is one atomic word, changed by compare-and-swap, as
// Go's casgstatus changes a goroutine's: RUNNING on a worker; RUNNABLE
// on a queue or in a slot; PARKING, switched away from but not yet
// settled by its worker; PARKED, waiting for a wake; DONE. WOKEN is a
// bit over the first three: a wake that came while the task was
// running or parking, kept for its park, so that a wake is never lost
// between a task deciding to wait and its worker taking it off the
// stack, and cleared when the task resumes, since the wake that
// resumed it is the one it waited for. Before, a mutex per task
// guarded a state and a flag, made and destroyed per spawn and taken
// twice per park and wake.
enum { RUNNING, RUNNABLE, PARKING, PARKED, DONE, WOKEN = 8 };

typedef struct {
    void *p;
    void (*drop)(void *);
} guard;

struct sn_task {
    void *sp;        // the saved context, while not running
    void *stack;     // the reservation, NULL for the main task
    size_t size;     // of the reservation, guard page included
    void (*run)(void *);
    void *arg;
    _Atomic int state;
    struct sn_task *next; // on a queue
    guard *guards;        // the values a panic drops, innermost last
    int64_t nguards, guardcap;
    bool unwinding;
    sn_string unwound;     // the message of the panic being unwound to a mark, for a drop that panics meanwhile to report
    struct sn_mark *marks; // the marks a panic unwinds to, innermost first; none for a program's task
    sn_string places[3];   // the place of the test call being made, testing.c's: the task's own, so that parallel tests do not share it
};

// A mark is where a panic on the task stops: the jump back, the guard
// count at the mark, so that the values made since are dropped and the
// ones before are kept, and the message, for the caller of sn_catch.
// It lives in sn_catch's frame on the task's stack, below the frames
// the panic abandons (_docs/design/09-testing.md, section 3).
typedef struct sn_mark {
    sigjmp_buf jump;
    int64_t nguards;
    sn_string *out; // where the panic's message goes, the caller's
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
enum { ring_size = 256, stack_cache = 32 }; // a cache of 64 was measured no better

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

typedef struct {
    void *sched_sp;
    void *sched_stack;
    task *last; // the task that switched to the scheduler, to settle
    pthread_mutex_t mu;
    task *runnext;      // under mu
    int64_t runnext_at; // when it was put there, on the monotonic clock
    // The queue is Go's runq: a ring the worker's own thread alone
    // writes at the tail and takes from at the head, and other workers
    // take half of from the head, each take a compare-and-swap of the
    // head and no lock; a ring that is full sends half to the inject
    // queue. The counts only grow, so a slot's index is its count
    // modulo the size.
    _Atomic uint32_t qhead, qtail;
    _Atomic(task *) ring[ring_size]; // read by a stealer while the owner writes a neighbour: relaxed atomics make that formally clean at no cost
    // The stacks of its tasks that ended, kept for its next spawns, as
    // Go's stack cache sits per P over the heap: taken and given back
    // by the worker's thread alone, with the pool behind it under its
    // lock, half the cache at a time.
    void *stacks[stack_cache];
    int nstacks;
    int turns;       // tasks it runs before it looks at what comes from outside again: the other wheels' timers and the inject queue
    int streak;      // tasks it has run out of its runnext slot in a row
    int64_t owes_at; // a deadline it may have left no one to wake for: it sees to it before its next task, or keeps time itself in its wait; the greatest time for none
} worker;

static worker *workers;
static int nworkers;
static pthread_mutex_t start_mu = PTHREAD_MUTEX_INITIALIZER;
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
static __attribute__((noinline)) worker *my_worker(void) {
    __asm__ volatile("" ::: "memory");
    return self_worker;
}

static __attribute__((noinline)) task *my_task(void) {
    __asm__ volatile("" ::: "memory");
    return current;
}

static task main_task; // the main thread's own stack and context

static _Atomic int64_t running;  // tasks started and not yet done, the main task not counted
static _Atomic int64_t queued;   // tasks on any queue
static _Atomic int64_t nexts;    // workers' runnext slots holding a task
static pthread_mutex_t idle_mu = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t idle_cond = PTHREAD_COND_INITIALIZER;
static _Atomic int idle; // workers waiting for work
static _Atomic int spinning; // workers looking for work before they wait, at most one
static _Atomic int64_t outside; // wakes promised by the poller's thread: the sockets it watches

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
// the greatest when none keeps; both are written under idle_mu. Whoever
// starts a timer before wake_at wakes the keeper to read the deadlines
// again, or a waiting worker to be the keeper: it writes the deadline
// before it reads how many are idle, and a worker counts itself idle
// before it reads the deadlines, so one of the two sees the other. A
// keeper that wakes looks at the deadlines again before it waits or
// runs a task, so wake_at rises only when someone is looking.
static _Atomic int64_t wake_at = INT64_MAX;
static bool keeping;
static void keeper_wake(void);

// sn_sched_recheck has the waiting workers look again at whether any
// task can still be woken, when every worker waits.
void sn_sched_recheck(void) {
    if (atomic_load(&idle) != nworkers) {
        return;
    }

    pthread_mutex_lock(&idle_mu);
    pthread_cond_broadcast(&idle_cond);
    if (keeping) {
        keeper_wake();
    }

    pthread_mutex_unlock(&idle_mu);
}

// sn_outside counts n more wakes promised by the poller's thread, or n
// fewer once one has reached its task. When none is left
// and every worker is idle, the idle workers look again at whether any
// task can still be woken. While a worker is busy there is no one to
// tell: it counts itself idle before it reads this count, and this
// reads the idle count after it changes its own, so one of the two
// sees the other.
void sn_outside(int64_t n) {
    if (atomic_fetch_add(&outside, n) + n == 0) {
        sn_sched_recheck();
    }
}

// deadlocked reports, to an idle worker holding idle_mu, that no task
// can ever run again: every worker idle, so every task is parked or
// done, nothing in a runnext slot or on a queue, and no timer or socket
// left to wake one. An idle worker's slot is empty, since only its own
// thread fills it and it empties it before it sleeps, so a slot counted
// with every worker idle holds a task the monitor is moving to the
// inject queue, which is a task to run: the monitor counts it in the
// slot until it is on the queue, and the counts are read here in the
// order it changes them. The main task is among the parked, since the
// main thread is a worker only once the main task has parked, so the
// program cannot end. Go's runtime stops such a program with the same
// message.
static bool deadlocked(void) {
    // Whoever keeps a promise queues the task first and takes the
    // promise off its count second, and the monitor queues a slot's task
    // before it takes it off the count of slots, so the counts are read
    // in the other order: a promise or a slot read gone means the task
    // it put on a queue is counted there by the time the queue is read.
    return atomic_load(&idle) == nworkers && atomic_load(&outside) == 0 && sn_timers_pending() == 0 && atomic_load(&nexts) == 0 && atomic_load(&queued) == 0;
}

static const size_t stack_size = 256 << 10; // a task's stack, as step one's thread stack was

// A stack is a reservation with a guard page at its low end, so that
// a recursion past it faults rather than writes over another task's.
// Freed stacks are pooled, since mapping is the costly part of a spawn.
// The pool's lock is a mutex: a spin lock was measured in its place
// and made a spawn a quarter slower by the median of eleven runs, the
// spinners costing the workers the cores the mutex's sleepers gave
// them.
static pthread_mutex_t pool_mu = PTHREAD_MUTEX_INITIALIZER;
static void *pool[256]; // measured against 1024: the deeper pool made a spawn a third slower
static int pooled;

static void pool_take(void) {
    pthread_mutex_lock(&pool_mu);
}

static void pool_give(void) {
    pthread_mutex_unlock(&pool_mu);
}

// stack_new gives a stack from w's cache, which is refilled with half
// a cache from the pool when it is empty, or from the pool alone for
// a thread that is no worker, and maps a new one when both are empty.
static void *stack_new(worker *w, size_t *size) {
    *size = stack_size;
    if (w != NULL && w->nstacks > 0) {
        return w->stacks[--w->nstacks];
    }

    pool_take();
    if (w != NULL) {
        while (pooled > 0 && w->nstacks < stack_cache / 2) {
            w->stacks[w->nstacks++] = pool[--pooled];
        }
    } else if (pooled > 0) {
        void *s = pool[--pooled];
        pool_give();
        return s;
    }

    pool_give();
    if (w != NULL && w->nstacks > 0) {
        return w->stacks[--w->nstacks];
    }

    size_t page = (size_t)sysconf(_SC_PAGESIZE);
    int flags = MAP_PRIVATE | MAP_ANONYMOUS;
#ifdef MAP_NORESERVE
    flags |= MAP_NORESERVE;
#endif
    void *s = mmap(NULL, stack_size, PROT_READ | PROT_WRITE, flags, -1, 0);
    if (s == MAP_FAILED) {
        sn_panic(SN_STR("cannot reserve a task's stack", 29));
    }

    if (mprotect(s, page, PROT_NONE) != 0) { // the guard page is a mapping of its own, and the system's count of them is finite
        munmap(s, stack_size);
        sn_panic(SN_STR("cannot reserve a task's stack: the system's limit of memory mappings is reached", 79));
    }

    return s;
}

// stack_free keeps a stack in w's cache; a full cache sends half to
// the pool under one lock, and what the pool has no room for is
// unmapped. A thread that is no worker gives to the pool alone.
static void stack_free(worker *w, void *s, size_t size) {
    if (w != NULL) {
        if (w->nstacks == stack_cache) {
            pool_take();
            while (w->nstacks > stack_cache / 2 && pooled < (int)(sizeof pool / sizeof pool[0])) {
                pool[pooled++] = w->stacks[--w->nstacks];
            }

            pool_give();
            while (w->nstacks > stack_cache / 2) {
                munmap(w->stacks[--w->nstacks], size);
            }
        }

        w->stacks[w->nstacks++] = s;
        return;
    }

    pool_take();
    if (pooled < (int)(sizeof pool / sizeof pool[0])) {
        pool[pooled++] = s;
        pool_give();
        return;
    }

    pool_give();
    munmap(s, size);
}

// sn_self returns the running task; on the main thread before any
// task exists, it makes the main task from the thread itself.
sn_task *sn_self(void) {
    task *t = my_task();
    if (t == NULL) {
        atomic_store(&main_task.state, RUNNING);
        current = t = &main_task;
    }

    return t;
}

// sn_task_places returns the running task's slots for the place of a
// test's call, which testing.c fills and reads.
sn_string *sn_task_places(void) {
    return sn_self()->places;
}

static _Atomic int waiting; // workers blocked in the idle wait for work alone, the keeper not among them, under idle_mu
static int handed;          // spins given to signalled workers not yet back from the wait, under idle_mu

// wake_idle wakes one worker blocked waiting for work, if one is and
// no worker is spinning, and makes the one it wakes the spinner before
// it wakes, as Go's wakep starts an M spinning: every push until the
// woken worker runs sees a spinner and wakes no other, where before a
// burst of spawns woke a worker per spawn while one was on its way.
// The worker that returns from the wait with a spin handed out takes
// it, and gives it up once it has found work or spun its time out; a
// worker a broadcast or a spurious return woke finds none handed and
// takes nothing, so there is one spinner at a time still. A signal
// goes only to a worker counted blocked, under the lock it blocks
// with, so a spin is never handed to no one. The keeper is woken only
// when no other worker waits, so that the timers keep their keeper
// while there is another to run the work.
static void wake_idle(void) {
    if (atomic_load(&idle) == 0 || atomic_load(&spinning) != 0) {
        return;
    }

    pthread_mutex_lock(&idle_mu);
    int expected = 0;
    if ((atomic_load(&waiting) > 0 || keeping) && atomic_compare_exchange_strong(&spinning, &expected, 1)) {
        handed++;
        if (atomic_load(&waiting) > 0) {
            pthread_cond_signal(&idle_cond);
        } else {
            keeper_wake();
        }
    }

    pthread_mutex_unlock(&idle_mu);
}

static bool push_slow(worker *w, task *t, uint32_t h, uint32_t tl);

// The queues. push puts a task on the ring of w, the worker whose
// thread runs it, and tells an idle worker; a worker with nothing in
// its own ring steals half of another worker's. A full ring sends
// half of itself with the task to the inject queue, as Go's runqput
// does to its global queue.
static void push(worker *w, task *t) {
    for (;;) {
        uint32_t h = atomic_load_explicit(&w->qhead, memory_order_acquire);
        uint32_t tl = atomic_load_explicit(&w->qtail, memory_order_relaxed);
        if (tl - h < ring_size) {
            atomic_store_explicit(&w->ring[tl % ring_size], t, memory_order_relaxed);
            atomic_store_explicit(&w->qtail, tl + 1, memory_order_release); // the slot is written before a taker sees the count
            break;
        }

        if (push_slow(w, t, h, tl)) {
            break;
        }
    }

    atomic_fetch_add(&queued, 1);
    wake_idle(); // a spinner looks at the queues, and one that stops reads queued after it stops; a worker counts itself idle before it reads queued; all are sequentially consistent, so one side sees the other
}

// The inject queue holds the tasks woken by a thread that is not a
// worker, the poller's or the timers', as Go's global run queue holds
// what its netpoller and timers make ready, and the tasks the monitor
// moves out of a runnext slot. Every worker looks at it
// after its own queue, and a spinning worker looks at it alone: a task
// woken from outside is what a spin is for, since only it would
// otherwise cost a thread woken to run it, and work the workers make
// for one another stays where it was made.
static pthread_mutex_t inject_mu = PTHREAD_MUTEX_INITIALIZER;
static task *inject_head, *inject_tail;
static _Atomic int64_t injected;

static void inject(task *t) {
    pthread_mutex_lock(&inject_mu);
    t->next = NULL;
    if (inject_tail != NULL) {
        inject_tail->next = t;
    } else {
        inject_head = t;
    }

    inject_tail = t;
    pthread_mutex_unlock(&inject_mu);
    atomic_fetch_add(&queued, 1);
    atomic_fetch_add(&injected, 1);
    wake_idle(); // a spinner stops spinning before it reads queued, and both are sequentially consistent, so one of the two sees the other
}

// push_slow moves half of w's full ring, with t after it, to the
// inject queue, and reports whether it did; a stealer that took from
// the ring meanwhile makes it fail, and push tries the ring again.
static bool push_slow(worker *w, task *t, uint32_t h, uint32_t tl) {
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
    pthread_mutex_lock(&inject_mu);
    if (inject_tail != NULL) {
        inject_tail->next = batch[0];
    } else {
        inject_head = batch[0];
    }

    inject_tail = batch[n];
    pthread_mutex_unlock(&inject_mu);
    atomic_fetch_add(&injected, (int64_t)n + 1); // the n moved stay counted in queued; push counts t
    return true;
}

static task *take_injected(void) {
    if (atomic_load(&injected) == 0) {
        return NULL;
    }

    pthread_mutex_lock(&inject_mu);
    task *t = inject_head;
    if (t != NULL) {
        inject_head = t->next;
        if (inject_head == NULL) {
            inject_tail = NULL;
        }
    }

    pthread_mutex_unlock(&inject_mu);
    if (t != NULL) {
        atomic_fetch_sub(&injected, 1);
        atomic_fetch_sub(&queued, 1);
    }

    return t;
}

// home is the queue a task spawned here goes on: the running worker's,
// or the first worker's from the main thread before it is one.

// pop takes the task at the head of w's own ring, by the worker's
// thread, racing only with stealers for the head.
static task *pop(worker *w) {
    for (;;) {
        uint32_t h = atomic_load_explicit(&w->qhead, memory_order_acquire);
        uint32_t tl = atomic_load_explicit(&w->qtail, memory_order_relaxed);
        if (tl == h) {
            return NULL;
        }

        task *t = atomic_load_explicit(&w->ring[h % ring_size], memory_order_relaxed);
        if (atomic_compare_exchange_weak_explicit(&w->qhead, &h, h + 1, memory_order_acq_rel, memory_order_relaxed)) {
            atomic_fetch_sub(&queued, 1);
            return t;
        }
    }
}

// steal takes half of the ring of from, rounded up so that a lone task
// is taken, for to, whose own ring is empty: the first is returned to
// run and the rest go on to's ring, as Go's runqsteal does. A head that
// moved under the read, a take by the owner or another stealer, makes
// the swap fail and the read is made again.
static task *steal(worker *to, worker *from) {
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

        if (!atomic_compare_exchange_strong_explicit(&from->qhead, &h, h + n, memory_order_acq_rel, memory_order_relaxed)) {
            continue;
        }

        uint32_t t = atomic_load_explicit(&to->qtail, memory_order_relaxed);
        for (uint32_t i = 1; i < n; i++) {
            atomic_store_explicit(&to->ring[(t + i - 1) % ring_size], batch[i], memory_order_relaxed);
        }

        atomic_store_explicit(&to->qtail, t + n - 1, memory_order_release);
        atomic_fetch_sub(&queued, 1); // the one returned; the rest stay queued
        return batch[0];
    }
}

// put_next puts t in w's runnext slot; a task it displaces goes on
// w's queue, where another worker may steal it.
static void put_next(worker *w, task *t) {
    pthread_mutex_lock(&w->mu);
    task *old = w->runnext;
    w->runnext = t;
    w->runnext_at = sn_time_mono();
    pthread_mutex_unlock(&w->mu);
    if (old != NULL) {
        push(w, old);
    } else {
        atomic_fetch_add(&nexts, 1);
    }
}

// clear_next empties w's runnext slot and returns the task it held: any
// when stale is zero, else only one that has waited there longer than
// stale, left by a running task that has not let go, since tasks are
// not preempted. The caller takes the task out of the count of slots
// once it is somewhere a worker can find it.
static task *clear_next(worker *w, int64_t stale) {
    pthread_mutex_lock(&w->mu);
    task *t = w->runnext;
    if (t != NULL && (stale == 0 || sn_time_mono() - w->runnext_at > stale)) {
        w->runnext = NULL;
    } else {
        t = NULL;
    }

    pthread_mutex_unlock(&w->mu);
    return t;
}

// take_next takes the task in w's runnext slot for the worker to run.
static task *take_next(worker *w, int64_t stale) {
    task *t = clear_next(w, stale);
    if (t != NULL) {
        atomic_fetch_sub(&nexts, 1);
    }

    return t;
}

// next_task is the task w runs next: the one in its runnext slot, then
// its queue's head, the inject queue's, and half of another worker's
// queue. Once the slot's task has run first next_most times in a row,
// the queue's head runs before it.
static task *next_task(worker *w) {
    bool turn_over = w->streak >= next_most;
    task *t = turn_over ? NULL : take_next(w, 0);
    if (t != NULL) {
        w->streak++;
        return t;
    }

    w->streak = 0;
    t = pop(w);
    if (t != NULL) {
        return t;
    }

    if (turn_over && (t = take_next(w, 0)) != NULL) { // nothing was queued behind it
        return t;
    }

    t = take_injected();
    if (t != NULL) {
        return t;
    }

    for (int i = 0; i < nworkers; i++) {
        worker *other = &workers[i];
        if (other != w && (t = steal(w, other)) != NULL) {
            return t;
        }
    }

    return NULL;
}

// settle finishes what the task that last switched to the scheduler
// left behind: a task done is freed; a task parking is parked, or put
// back on the queue if a wake reached it first. It runs on the
// scheduler's stack, so a done task's stack is no longer in use.
static void settle(worker *w) {
    task *t = w->last;
    if (t == NULL) {
        return;
    }

    w->last = NULL;
    switch (atomic_load(&t->state) & ~WOKEN) {
    case DONE:
        if (t->stack != NULL) {
            stack_free(w, t->stack, t->size);
        }

        free(t->guards);
        sn_string_drop(&t->places[SN_PLACE_PARTS]);
        free(t);
        atomic_fetch_sub(&running, 1);
        break;
    case PARKING: {
        int expected = PARKING;
        if (!atomic_compare_exchange_strong(&t->state, &expected, PARKED)) { // woken while it switched away: it runs next, here
            atomic_store(&t->state, RUNNABLE);
            put_next(w, t);
        }

        break;
    }
    }
}

// run_task switches the worker to a task, and settles the task when it
// switches back.
static void run_task(worker *w, task *t) {
    int s = atomic_load(&t->state);
    while (!atomic_compare_exchange_weak(&t->state, &s, RUNNING | (s & WOKEN))) { // a wake kept for its next park stays
    }

    current = t;
    w->last = t;
    sn_switch(&w->sched_sp, t->sp);
    current = NULL;
    settle(w);
}

// A task left in another worker's runnext slot this long, its worker
// busy with a task that does not let go, is taken by a worker looking
// for work, or moved by the monitor when none is.
static const int64_t next_stale = 1000000; // a millisecond

// steal_next takes a task some worker has left in its runnext slot
// longer than next_stale.
static task *steal_next(worker *w) {
    if (atomic_load(&nexts) == 0) {
        return NULL;
    }

    for (int i = 0; i < nworkers; i++) {
        if (&workers[i] != w) {
            task *t = take_next(&workers[i], next_stale);
            if (t != NULL) {
                return t;
            }
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
static const int64_t spin_for = 50000; // 50 µs

enum { worth_most = 8 };

static _Atomic int worth = 1;

static void spun(bool paid) {
    int w = atomic_load(&worth);
    int next = paid ? (w < worth_most ? w + 1 : w) : (w > 0 ? w - 1 : 0);
    if (next != w) {
        atomic_compare_exchange_strong(&worth, &w, next); // a lost race loses one step of a guess
    }
}

// spin looks at the queues until spin_for has passed, and reports
// whether a task was queued or injected meanwhile. A worker woken to
// spin holds the spin already; another takes it if no one holds it.
static bool spin(bool holding) {
    int expected = 0;
    if (!holding && (atomic_load(&worth) <= 0 || !atomic_compare_exchange_strong(&spinning, &expected, 1))) {
        return false;
    }

    int64_t until = sn_time_mono() + spin_for;
    bool found = false;
    for (int i = 1;; i++) {
        if (atomic_load(&queued) > 0) { // queued counts the injected too
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
        }

        sn_spin_hint();
    }

    atomic_store(&spinning, 0);
    found = found || atomic_load(&queued) > 0; // a push or an inject that saw it spinning woke no one: it looks once more
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
static const int64_t keeper_near = 20000; // 20 µs

static _Atomic bool keeper_woken;

#if defined(__APPLE__)
enum { keeper_timer = 1, keeper_user = 2 }; // the two events of the keeper's queue

static int keeper_queue = -1;

static void keeper_start(void) {
    keeper_queue = kqueue();
    struct kevent ev;
    EV_SET(&ev, keeper_user, EVFILT_USER, EV_ADD | EV_CLEAR, 0, 0, NULL);
    if (keeper_queue < 0 || fcntl(keeper_queue, F_SETFD, FD_CLOEXEC) < 0 || kevent(keeper_queue, &ev, 1, NULL, 0, NULL) < 0) {
        sn_panic(SN_STR("cannot start the scheduler", 26));
    }
}

static void keeper_sleep(int64_t when, int64_t now) {
    pthread_mutex_unlock(&idle_mu);
    struct kevent ev, got[2];
    EV_SET(&ev, keeper_timer, EVFILT_TIMER, EV_ADD | EV_ONESHOT, NOTE_NSECONDS | NOTE_CRITICAL, when - now, NULL);
    (void)kevent(keeper_queue, &ev, 1, got, 2, NULL); // a wait cut short is a wake: the keeper reads the deadlines again
    pthread_mutex_lock(&idle_mu);
}

static void keeper_rouse(void) {
    struct kevent ev;
    EV_SET(&ev, keeper_user, EVFILT_USER, 0, NOTE_TRIGGER, 0, NULL);
    (void)kevent(keeper_queue, &ev, 1, NULL, 0, NULL);
}
#else
static pthread_cond_t keeper_cond;

static void keeper_start(void) {
    pthread_condattr_t attr;
    pthread_condattr_init(&attr);
    pthread_condattr_setclock(&attr, CLOCK_MONOTONIC);
    pthread_cond_init(&keeper_cond, &attr);
    pthread_condattr_destroy(&attr);
}

static void keeper_sleep(int64_t when, int64_t now) {
    (void)now;
    when--; // a reading is the clock's and one, so that none is 0
    struct timespec ts = {(time_t)(when / 1000000000), (long)(when % 1000000000)};
    pthread_cond_timedwait(&keeper_cond, &idle_mu, &ts);
}

static void keeper_rouse(void) {
    pthread_cond_signal(&keeper_cond);
}
#endif

// keeper_wait is the keeper's wait until when; now is the clock's
// reading a moment ago.
static void keeper_wait(int64_t when, int64_t now) {
    atomic_store(&keeper_woken, false);
    if (when - now > keeper_near) {
        keeper_sleep(when, now);
        return;
    }

    pthread_mutex_unlock(&idle_mu);
    while (!atomic_load(&keeper_woken) && sn_time_mono() < when) {
        sn_spin_hint();
    }

    pthread_mutex_lock(&idle_mu);
}

// keeper_wake ends the keeper's wait; the caller holds idle_mu and has
// found a keeper. wake_at is the least time from here until the keeper
// has the lock again, so that no one who starts a timer meanwhile wakes
// it twice: it is on its way to read the deadlines.
static void keeper_wake(void) {
    if (atomic_load(&keeper_woken)) {
        return;
    }

    atomic_store(&keeper_woken, true);
    atomic_store(&wake_at, INT64_MIN);
    keeper_rouse();
}

// The monitor thread watches the workers for the one thing a scheduler
// without preemption cannot leave to them. A task woken by a running
// task goes in that worker's runnext slot, to run there when the
// running task lets go, and no worker is woken for it, since a thread
// woken for every hand-off is what the slot saves; Go's runtime wakes
// one, and lets it take the slot after a few microseconds. A task that
// does not let go, computing without a wait, would keep the slot's
// task, with every idle worker asleep by then and nothing to wake one.
// The monitor looks, while any worker is busy, for a task left in a
// slot longer than next_stale and moves it to the inject queue, which
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
// otherwise, Go's quantum before it preempts.
static const int64_t pause_most = 10000000; // 10 ms

static pthread_mutex_t monitor_mu = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t monitor_cond = PTHREAD_COND_INITIALIZER;

// move_next moves a task left stale in w's slot to the inject queue,
// and reports whether there was one. The task counts in the slot until
// it is on the queue, so that deadlocked never finds it in neither.
static bool move_next(worker *w) {
    task *t = clear_next(w, next_stale);
    if (t == NULL) {
        return false;
    }

    inject(t);
    atomic_fetch_sub(&nexts, 1);
    return true;
}

static void monitor_wake(void) {
    pthread_mutex_lock(&monitor_mu);
    pthread_cond_signal(&monitor_cond);
    pthread_mutex_unlock(&monitor_mu);
}

static void *monitor_main(void *arg) {
    (void)arg;
    int64_t pause = next_stale;
    for (;;) {
        pthread_mutex_lock(&monitor_mu);
        while (atomic_load(&idle) == nworkers) {
            pthread_cond_wait(&monitor_cond, &monitor_mu);
            pause = next_stale;
        }

        pthread_mutex_unlock(&monitor_mu);
        struct timespec until = {0, (long)pause};
        nanosleep(&until, NULL);
        bool moved = sn_timers_fire(sn_time_mono()); // what came due with every worker busy
        if (moved) {
            sn_sched_timer_set(sn_timers_next()); // a worker may wait by now, and for no deadline
        }

        if (atomic_load(&nexts) > 0) {
            for (int i = 0; i < nworkers; i++) {
                moved = move_next(&workers[i]) || moved;
            }
        }

        pause = moved ? next_stale : (pause * 2 < pause_most ? pause * 2 : pause_most);
    }

    return NULL; // never reached: a thread's function has a result to give
}

// sched_loop runs the worker: the next task, or the timers that are
// due, a spin and then a wait for one.
static void sched_loop(worker *w) {
#ifdef __linux__
    prctl(PR_SET_TIMERSLACK, 1UL, 0UL, 0UL, 0UL); // a keeper's wait ends at its deadline, not the 50 µs after it a thread's timed wait is given by default
#endif
    bool woken = false;   // out of a wait or a spin with work found: the pushes meanwhile woke no one, so it wakes a worker for what it leaves
    bool holding = false; // woken to spin, and holding the spin until it finds work or spins it out
    int64_t index = w - workers;
    for (;;) {
        // Its own wheel's timers are fired before it takes its next
        // task, so that a task a timer wakes into the slot is the one it
        // takes; the clock is read only when its wheel holds a timer,
        // since a reading costs about what a switch does. The other
        // wheels are looked at once in outside_turns.
        task *t = NULL;
        bool look = --w->turns <= 0;
        if (look || sn_timers_next_of(index) != INT64_MAX) {
            int64_t now = sn_time_mono();
            bool fired = look ? sn_timers_fire(now) : sn_timers_fire_own(index, now);
            if (fired && atomic_load(&idle) > 0) {
                sn_sched_timer_set(sn_timers_next()); // what it fired may run long here: a waiting worker keeps time for the rest
            }
        }

        if (look) {
            w->turns = outside_turns;
            t = take_injected();
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
                atomic_store(&spinning, 0);
            }

            if (w->owes_at != INT64_MAX) { // the task may run long: a waiting worker keeps time
                int64_t at = w->owes_at;
                w->owes_at = INT64_MAX;
                sn_sched_timer_set(at);
            }

            if (woken) {
                woken = false;
                if (atomic_load(&queued) > 0) {
                    wake_idle(); // as Go's spinning M, once it finds work, starts another for the rest
                }
            }

            run_task(w, t);
            continue;
        }

        if (sn_timers_fire(sn_time_mono())) {
            w->owes_at = sn_timers_next();
            continue; // next_task takes what a timer woke
        }

        if (spin(holding)) {
            holding = false; // spin gave the spin up
            woken = true;
            w->owes_at = sn_timers_next(); // a timer started while it spun was told to no one, since a spinner is what a wake finds first
            continue; // next_task takes what was found, the inject queue or a steal, or a timer came due
        }

        holding = false;

        pthread_mutex_lock(&idle_mu);
        atomic_fetch_add(&idle, 1);
        for (;;) {
            if (holding) { // woken to spin and the work taken meanwhile: the spin goes back before the queues are read, so a push that saw it held is seen here, or sees the worker idle and signals
                holding = false;
                atomic_store(&spinning, 0);
            }

            if (atomic_load(&queued) > 0) {
                break;
            }

            if (deadlocked()) {
                fflush(stdout);
                fputs("fatal error: all tasks are asleep - deadlock!\n", stderr);
                exit(2);
            }

            int64_t asleep = sn_time_mono();
            int64_t first = sn_timers_next();
            if (first <= asleep) {
                break; // a timer is due: it leaves the wait to fire it
            }

            if (!keeping && first != INT64_MAX) { // no one wakes for the timers: it does
                keeping = true;
                atomic_store(&wake_at, first);
                keeper_wait(first, asleep);
                keeping = false;
                atomic_store(&wake_at, INT64_MAX);
            } else {
                if (keeping && first < atomic_load(&wake_at)) {
                    keeper_wake(); // the keeper sleeps past the earliest timer: it reads the deadlines again
                }

                atomic_fetch_add(&waiting, 1);
                pthread_cond_wait(&idle_cond, &idle_mu);
                atomic_fetch_sub(&waiting, 1);
            }

            if (handed > 0) { // a signal with a spin woke it; a broadcast or a spurious return hands none
                handed--;
                holding = true;
            }

            if (atomic_load(&queued) > 0 && sn_time_mono() - asleep < spin_for) {
                spun(true); // a push or a wake from outside came soon enough that a spin would have caught it
            }
        }

        bool first = atomic_fetch_sub(&idle, 1) == nworkers;
        pthread_mutex_unlock(&idle_mu);
        woken = true;
        w->owes_at = sn_timers_next(); // it may have been the keeper, or woken to be: before it runs a task, another keeps
        if (first) {
            monitor_wake(); // the first worker busy again: the monitor's lock is never taken under idle_mu
        }
    }
}

static void *worker_main(void *arg) {
    self_worker = arg;
    sched_loop(self_worker);
    return NULL;
}

// sched_entry is where the main thread's scheduler context begins,
// on the stack the main thread took when the main task first parked:
// that park is settled first.
static void sched_entry(void) {
    settle(self_worker);
    sched_loop(self_worker);
}

// start_thread starts a detached thread of the runtime's own.
static void start_thread(void *(*main)(void *), void *arg) {
    pthread_attr_t attr;
    pthread_attr_init(&attr);
    pthread_attr_setdetachstate(&attr, PTHREAD_CREATE_DETACHED);
    pthread_t id;
    if (pthread_create(&id, &attr, main, arg) != 0) {
        sn_panic(SN_STR("cannot start a thread", 21));
    }

    pthread_attr_destroy(&attr);
}

// sn_cores is the number of processors the program may run on, at
// least one: the processors in the thread's affinity mask on Linux,
// as Go's runtime.NumCPU counts them, so that a program held to a
// few processors by taskset or a container starts a worker for each
// of those; the processors online elsewhere.
int64_t sn_cores(void) {
    long n = 0;
#ifdef __linux__
    cpu_set_t set;
    if (sched_getaffinity(0, sizeof set, &set) == 0) {
        n = CPU_COUNT(&set);
    }
#endif
    if (n <= 0) {
        n = sysconf(_SC_NPROCESSORS_ONLN);
    }

    return n > 1 ? (int64_t)n : 1;
}

// start makes the workers, the main thread the first and one thread
// per further core, the timers' wheels and the monitor.
static void start(void) {
    pthread_mutex_lock(&start_mu);
    if (!started) {
        nworkers = (int)sn_cores();
        workers = calloc((size_t)nworkers, sizeof *workers);
        if (workers == NULL) {
            sn_panic(SN_STR("out of memory", 13));
        }

        for (int i = 0; i < nworkers; i++) {
            pthread_mutex_init(&workers[i].mu, NULL);
        }

        keeper_start();
        sn_timers_start(nworkers);

        if (my_worker() == NULL) {
            self_worker = &workers[0]; // the main thread
        }

        for (int i = 0; i < nworkers; i++) {
            workers[i].owes_at = INT64_MAX;
        }

        started = true;
        for (int i = 1; i < nworkers; i++) {
            start_thread(worker_main, &workers[i]);
        }

        start_thread(monitor_main, NULL);
    }

    pthread_mutex_unlock(&start_mu);
}

// sn_sched_start starts the workers if they are not, for a timer
// started before any task was spawned; sn_worker_index is the index of
// the current thread's worker, which every thread that starts a timer
// has.
void sn_sched_start(void) {
    if (!started) {
        start();
    }
}

int64_t sn_worker_index(void) {
    return my_worker() - workers;
}

// sn_sched_timer_set has a worker keep time for a timer started at
// when, if the keeper sleeps past it or there is none: the keeper reads
// the deadlines again, or a waiting worker is woken as it is for work,
// one at a time, and finds no task and keeps time in its wait; until it
// is there no other is woken, so that the workers running tasks that
// sleep take no lock for each. With no worker idle there is no one to
// tell: the first to wait reads the deadlines itself.
void sn_sched_timer_set(int64_t when) {
    int64_t kept = atomic_load(&wake_at);
    if (when >= kept || atomic_load(&idle) == 0) {
        return;
    }

    if (kept == INT64_MAX) {
        wake_idle();
        return;
    }

    pthread_mutex_lock(&idle_mu);
    if (keeping && when < atomic_load(&wake_at)) {
        keeper_wake();
    }

    pthread_mutex_unlock(&idle_mu);
}

// sn_sched_owe notes a deadline the worker sees to once the task that
// started it has parked: on its way to its next task, or in its wait.
void sn_sched_owe(int64_t when) {
    worker *w = my_worker();
    if (when < w->owes_at) {
        w->owes_at = when;
    }
}

// task_entry is where a task's context begins: the function with its
// argument block, and then the switch back that run_task frees it
// after. It never returns, since nothing called it.
static void task_entry(void) {
    task *t = my_task();
    t->run(t->arg);
    atomic_store(&t->state, DONE);
    sn_switch(&t->sp, my_worker()->sched_sp);
    abort(); // a done task is never resumed
}

void sn_spawn(void (*run)(void *), void *arg) {
    if (!started) {
        start();
    }

    sn_self();
    task *t = calloc(1, sizeof *t); // its own block: a record at the top of the task's stack was measured and made a spawn two thirds slower
    if (t == NULL) {
        sn_panic(SN_STR("out of memory", 13));
    }

    t->run = run;
    t->arg = arg;
    t->stack = stack_new(my_worker(), &t->size); // the spawning thread's worker, or the pool for a thread that is none
    t->sp = make_context((uint8_t *)t->stack + t->size, task_entry);
    atomic_store(&t->state, RUNNABLE);
    atomic_fetch_add(&running, 1);
    worker *w = my_worker();
    if (w != NULL) {
        push(w, t); // the spawning thread's own ring, which its thread alone writes
    } else {
        inject(t); // a thread that is no worker, which none is today: the main thread is one from start on
    }
}

int64_t sn_tasks_running(void) {
    return atomic_load(&running);
}

// sn_park waits for a wake: the task switches to its worker's
// scheduler, which settles it as parked, and resumes when a wake puts
// it back on a queue and a worker runs it. A wake that came first is
// consumed at once. The main thread's scheduler context is made the
// first time the main task parks.
void sn_park(void) {
    task *t = sn_self();
    if (!started) {
        start(); // the main task sleeping before any spawn: the thread still needs its scheduler
    }

    worker *w = my_worker();
    if (w->sched_sp == NULL) { // the main thread, parking for the first time
        size_t size;
        w->sched_stack = stack_new(NULL, &size);
        w->sched_sp = make_context((uint8_t *)w->sched_stack + size, sched_entry);
    }

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
        atomic_fetch_and(&t->state, ~WOKEN); // resumed by a wake, on whichever worker ran the queue: one that came after it is the same wake, whose condition the caller reads again
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
bool sn_wake_claim(sn_task *t) {
    for (;;) {
        int s = atomic_load(&t->state);
        if (s == PARKED) {
            if (atomic_compare_exchange_strong(&t->state, &s, RUNNABLE)) {
                return true;
            }

            continue;
        }

        if ((s & WOKEN) != 0 || s == DONE || atomic_compare_exchange_strong(&t->state, &s, s | WOKEN)) {
            return false; // the wake is kept for the task's park, or was already
        }
    }
}

void sn_wake_queue(sn_task *t) {
    worker *w = my_worker();
    if (w != NULL) {
        put_next(w, t); // woken on a worker: it runs next there
    } else {
        inject(t); // by the poller's thread or the monitor
    }
}

// sn_wake makes a parked task runnable, or keeps the wake for a task
// still running or parking.
void sn_wake(sn_task *t) {
    if (sn_wake_claim(t)) {
        sn_wake_queue(t);
    }
}

// The guard stack of a task: the values a panic drops, innermost
// last. It grows with the system allocator, not sn_alloc, so a debug
// build does not count it among the program's blocks. A panic unwinds
// the task it happens in and no other, since the others are on other
// workers or parked in the middle of their work.
void sn_guard(void *p, void (*drop)(void *)) {
    task *t = sn_self();
    if (t->nguards == t->guardcap) {
        int64_t cap = t->guardcap < 16 ? 16 : t->guardcap * 2;
        guard *more = realloc(t->guards, (size_t)cap * sizeof *more);
        if (more == NULL) {
            sn_panic(SN_STR("out of memory", 13)); // the stack so far is intact for the unwinding
        }

        t->guards = more;
        t->guardcap = cap;
    }

    t->guards[t->nguards++] = (guard){p, drop};
}

// sn_unguard removes the guard of p, searched from the top: a value is
// usually dropped in the reverse order of its registration, but a
// temporary of a statement may be dropped after a variable the
// statement declared.
void sn_unguard(void *p) {
    task *t = sn_self();
    for (int64_t i = t->nguards - 1; i >= 0; i--) {
        if (t->guards[i].p == p) {
            for (sn_mark *m = t->marks; m != NULL && m->nguards > i; m = m->prev) {
                m->nguards--; // a guard below a mark removed: the mark's count moves with it, though the ownership rules let no value declared above a mark be dropped under it
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
// the caller, when one did. Marks nest, a subtest's inside its test's.
// The jump saves no signal mask, and the task may resume on another
// thread between the mark and the panic: the jump restores the
// registers and the stack pointer of the mark's frame, which are the
// task's own, and nothing of the thread's. Nothing of the mark or of
// the task is read after the jump: sn_panic wrote the message to the
// caller's slot before it jumped, and a local of this frame changed
// since the mark would be indeterminate to read.
bool sn_catch(void (*fn)(void *), void *ctx, sn_string *msg) {
    task *t = sn_self();
    sn_mark m;
    m.nguards = t->nguards;
    m.out = msg;
    m.prev = t->marks;
    t->marks = &m;
    if (sigsetjmp(m.jump, 0) == 0) {
        fn(ctx);
        sn_self()->marks = m.prev; // the task asked for again: no local of this frame set before the mark is read after it
        return false;
    }

    return true; // the panic took the mark off, unwound to it and wrote *msg
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
// The lazies: one lock and one condition for every lazy package
// variable, since making one is rare and short. A task that finds a
// variable being made by another waits here, holding its worker,
// which is what an initializer that waits for another task would
// deadlock on; an initializer is plain code.
static pthread_mutex_t lazy_mu = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t lazy_cond = PTHREAD_COND_INITIALIZER;

// lazy_abandon is the guard of an initializer that panics: the
// variable is unmade again and whoever waits is woken, so that a later
// use under a test makes it anew.
static void lazy_abandon(void *p) {
    sn_lazy *o = p;
    pthread_mutex_lock(&lazy_mu);
    __atomic_store_n(&o->state, 0, __ATOMIC_RELEASE);
    o->maker = NULL;
    pthread_cond_broadcast(&lazy_cond);
    pthread_mutex_unlock(&lazy_mu);
}

void sn_lazy_make(sn_lazy *o, void (*init)(void), const char *name) {
    task *self = sn_self();
    pthread_mutex_lock(&lazy_mu);
    for (;;) {
        int state = __atomic_load_n(&o->state, __ATOMIC_ACQUIRE);
        if (state == 2) {
            pthread_mutex_unlock(&lazy_mu);
            return;
        }

        if (state == 0) {
            break;
        }

        if (o->maker == self) {
            pthread_mutex_unlock(&lazy_mu);
            char buf[192];
            snprintf(buf, sizeof buf, "package variable %s is read by its own initializer", name);
            sn_panic(SN_STR(buf, (int64_t)strlen(buf)));
        }

        pthread_cond_wait(&lazy_cond, &lazy_mu);
    }

    __atomic_store_n(&o->state, 1, __ATOMIC_RELAXED);
    o->maker = self;
    pthread_mutex_unlock(&lazy_mu);
    sn_guard(o, lazy_abandon);
    init();
    sn_unguard(o);
    pthread_mutex_lock(&lazy_mu);
    o->maker = NULL;
    __atomic_store_n(&o->state, 2, __ATOMIC_RELEASE);
    pthread_cond_broadcast(&lazy_cond);
    pthread_mutex_unlock(&lazy_mu);
}

_Noreturn void sn_panic(sn_string msg) {
    task *t = my_task(); // a thread of the runtime's own, the timers' or the poller's, has none and never jumps into the main task's mark
    if (t != NULL && t->marks != NULL && !t->unwinding) {
        sn_mark *m = t->marks;
        t->marks = m->prev;
        *m->out = sn_string_clone(msg); // before the unwinding drops what the message may be part of
        t->unwound = *m->out;
        t->unwinding = true;
        while (t->nguards > m->nguards) {
            guard g = t->guards[--t->nguards];
            g.drop(g.p);
        }

        t->unwinding = false;
        t->unwound = (sn_string){0};
        siglongjmp(m->jump, 1);
    }

    if (t == NULL) {
        t = sn_self();
    }

    fflush(stdout);
    fputs("panic: ", stderr);
    fwrite(msg.ptr, 1, (size_t)msg.len, stderr);
    fputc('\n', stderr);
    if (t->unwound.len > 0) {
        fputs("\twhile unwinding from panic: ", stderr);
        fwrite(t->unwound.ptr, 1, (size_t)t->unwound.len, stderr);
        fputc('\n', stderr);
    }

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
