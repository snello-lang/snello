// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The scheduler: step two of _docs/design/06-tasks-and-channels.md.
// A task is a coroutine with a stack of its own, reserved from the
// system and committed by it page by page as the task touches it, and
// a saved set of callee-saved registers; switching from one task to
// another is the assembly routine tk_switch below, one per processor.
// N worker threads, one per core with the main thread among them, run
// M tasks: each worker keeps a queue of runnable tasks and steals from
// another's when its own is empty; a worker with nothing to run waits
// to be told of work. A task that must wait, on a channel, a lock or
// a timer, parks: it switches to its worker's scheduler, which runs
// the next task, and whoever wakes it puts it back on a queue. A task
// runs until it parks or ends: the scheduler does not preempt, so a
// task that computes without ever waiting keeps its worker, and the
// others keep running on the rest.
//
// The main thread's own stack is the main task's. When the main task
// first parks, the main thread takes a scheduler stack of its own and
// becomes a worker like the others; the main task may then resume on
// any worker, since a stack is only memory and the program keeps
// nothing per thread but what this file keeps per task: the guard
// stack a panic unwinds, which lives in the task.

#define _DEFAULT_SOURCE // glibc's mmap flags and clocks under -std=c11, as teak.c asks for its own
#define _DARWIN_C_SOURCE

#include "internal.h"

#include <errno.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <time.h>
#include <unistd.h>

// tk_switch saves the callee-saved registers and the stack pointer of
// the running context into *save, and resumes the context whose stack
// pointer is to, which was saved the same way, or made by make_context
// for a context that has not run yet. The frame it saves is 160 bytes
// on arm64: x19 to x28, x29 and x30, then d8 to d15; and 64 bytes on
// x86-64: rbp, rbx and r12 to r15, pushed, with the return address the
// caller left above them. The symbol carries a leading underscore on
// Darwin, as every C symbol does there.
void tk_switch(void **save, void *to);

#if defined(__APPLE__)
#define TK_SYM(name) "_" #name
#else
#define TK_SYM(name) #name
#endif

#if defined(__aarch64__)
#define TK_FRAME 160
__asm__(
    ".text\n"
    ".globl " TK_SYM(tk_switch) "\n"
    ".p2align 2\n" TK_SYM(tk_switch) ":\n"
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
#define TK_FRAME 64
__asm__(
    ".text\n"
    ".globl " TK_SYM(tk_switch) "\n" TK_SYM(tk_switch) ":\n"
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
#error "the Teak runtime has no context switch for this processor"
#endif

// make_context lays out the frame tk_switch restores at the top of a
// fresh stack, so that the first switch to it returns into entry with
// an aligned stack and every saved register zero. On x86-64 the
// return address sits one word below the top, so that entry begins as
// a called function does, with the stack pointer eight past a
// sixteen-byte boundary.
static void *make_context(void *top, void (*entry)(void)) {
    uint8_t *sp = (uint8_t *)top - TK_FRAME;
    memset(sp, 0, TK_FRAME);
#if defined(__aarch64__)
    ((void **)sp)[11] = (void *)entry; // x30, the return address
#else
    ((void **)sp)[6] = (void *)entry; // above the six pushed registers
#endif
    return sp;
}

// A task's states: running on a worker; runnable on a queue; parking,
// switched away from but not yet settled by its worker; parked,
// waiting for a wake; done. A wake that arrives while the task is
// running or parking is kept as the woken flag, which park consumes,
// so a wake is never lost between a task deciding to wait and its
// worker taking it off the stack.
enum { RUNNING, RUNNABLE, PARKING, PARKED, DONE };

typedef struct {
    void *p;
    void (*drop)(void *);
} guard;

struct tk_task {
    void *sp;        // the saved context, while not running
    void *stack;     // the reservation, NULL for the main task
    size_t size;     // of the reservation, guard page included
    void (*run)(void *);
    void *arg;
    pthread_mutex_t mu; // over state and woken
    int state;
    bool woken;
    struct tk_task *next; // on a queue
    guard *guards;        // the values a panic drops, innermost last
    int64_t nguards, guardcap;
    bool unwinding;
};

typedef struct tk_task task;

// A worker: its scheduler context, the queue of runnable tasks it
// owns, and for the main thread the scheduler stack it takes when the
// main task first parks. runnext is Go's: the task this worker's
// running task last woke, which the worker runs next, before its
// queue, so that two tasks handing values back and forth stay on one
// thread, one switch apart, where a wake through the queue would wake
// another thread to run the task there.
typedef struct {
    void *sched_sp;
    void *sched_stack;
    task *last; // the task that switched to the scheduler, to settle
    pthread_mutex_t mu;
    task *head, *tail;
    int64_t len;
    task *runnext;      // under mu
    int64_t runnext_at; // when it was put there, on the monotonic clock
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
static _Atomic int64_t outside; // wakes promised by threads that are not workers: the timers in the heap, the sockets the poller watches

// tk_outside counts n more wakes promised by a thread that is not a
// worker, or n fewer once one has reached its task. When none is left,
// the idle workers look again at whether any task can still be woken.
void tk_outside(int64_t n) {
    if (atomic_fetch_add(&outside, n) + n == 0) {
        pthread_mutex_lock(&idle_mu);
        pthread_cond_broadcast(&idle_cond);
        pthread_mutex_unlock(&idle_mu);
    }
}

// deadlocked reports, to an idle worker holding idle_mu, that no task
// can ever run again: every worker idle, so every task is parked or
// done, nothing on a queue, and no timer or socket left to wake one.
// The main task is among the parked, since the main thread is a worker
// only once the main task has parked, so the program cannot end. Go's
// runtime stops such a program with the same message.
static bool deadlocked(void) {
    return atomic_load(&idle) == nworkers && atomic_load(&queued) == 0 && atomic_load(&outside) == 0;
}

static const size_t stack_size = 256 << 10; // a task's stack, as step one's thread stack was

// A stack is a reservation with a guard page at its low end, so that
// a recursion past it faults rather than writes over another task's.
// Freed stacks are pooled, since mapping is the costly part of a spawn.
static pthread_mutex_t pool_mu = PTHREAD_MUTEX_INITIALIZER;
static void *pool[256];
static int pooled;

static void *stack_new(size_t *size) {
    pthread_mutex_lock(&pool_mu);
    if (pooled > 0) {
        void *s = pool[--pooled];
        pthread_mutex_unlock(&pool_mu);
        *size = stack_size;
        return s;
    }

    pthread_mutex_unlock(&pool_mu);
    size_t page = (size_t)sysconf(_SC_PAGESIZE);
    int flags = MAP_PRIVATE | MAP_ANONYMOUS;
#ifdef MAP_NORESERVE
    flags |= MAP_NORESERVE;
#endif
    void *s = mmap(NULL, stack_size, PROT_READ | PROT_WRITE, flags, -1, 0);
    if (s == MAP_FAILED) {
        tk_panic(TK_STR("cannot reserve a task's stack", 29));
    }

    if (mprotect(s, page, PROT_NONE) != 0) { // the guard page is a mapping of its own, and the system's count of them is finite
        munmap(s, stack_size);
        tk_panic(TK_STR("cannot reserve a task's stack: the system's limit of memory mappings is reached", 79));
    }

    *size = stack_size;
    return s;
}

static void stack_free(void *s, size_t size) {
    pthread_mutex_lock(&pool_mu);
    if (pooled < (int)(sizeof pool / sizeof pool[0])) {
        pool[pooled++] = s;
        pthread_mutex_unlock(&pool_mu);
        return;
    }

    pthread_mutex_unlock(&pool_mu);
    munmap(s, size);
}

// tk_self returns the running task; on the main thread before any
// task exists, it makes the main task from the thread itself.
tk_task *tk_self(void) {
    task *t = my_task();
    if (t == NULL) {
        pthread_mutex_init(&main_task.mu, NULL);
        main_task.state = RUNNING;
        current = t = &main_task;
    }

    return t;
}

// wake_idle wakes one worker waiting for work, if one is.
static void wake_idle(void) {
    if (atomic_load(&idle) > 0) {
        pthread_mutex_lock(&idle_mu);
        pthread_cond_signal(&idle_cond);
        pthread_mutex_unlock(&idle_mu);
    }
}

// The queues. push puts a task on a worker's queue and tells an idle
// worker; a worker with nothing in its own queue steals half of
// another worker's.
static void push(worker *w, task *t) {
    pthread_mutex_lock(&w->mu);
    t->next = NULL;
    if (w->tail != NULL) {
        w->tail->next = t;
    } else {
        w->head = t;
    }

    w->tail = t;
    w->len++;
    pthread_mutex_unlock(&w->mu);
    atomic_fetch_add(&queued, 1);
    wake_idle(); // a worker counts itself idle before it reads queued, and both are sequentially consistent, so one of the two sees the other
}

// The inject queue holds the tasks woken by a thread that is not a
// worker, the poller's or the timers', as Go's global run queue holds
// what its netpoller and timers make ready. Every worker looks at it
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
    if (atomic_load(&spinning) == 0) { // a spinner stops spinning before it reads injected, and both are sequentially consistent, so one of the two sees the other
        wake_idle();
    }
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
static worker *home(void) {
    worker *w = my_worker();
    return w != NULL ? w : &workers[0];
}

static task *pop(worker *w) {
    pthread_mutex_lock(&w->mu);
    task *t = w->head;
    if (t != NULL) {
        w->head = t->next;
        if (w->head == NULL) {
            w->tail = NULL;
        }

        w->len--;
    }

    pthread_mutex_unlock(&w->mu);
    if (t != NULL) {
        atomic_fetch_sub(&queued, 1);
    }

    return t;
}

// steal moves half of the queue of from, rounded up so that a lone
// task is taken, onto the queue of to, and returns whether anything
// moved.
static bool steal(worker *to, worker *from) {
    pthread_mutex_lock(&from->mu);
    int64_t n = (from->len + 1) / 2;
    if (n == 0) {
        pthread_mutex_unlock(&from->mu);
        return false;
    }

    task *first = from->head, *last = first;
    for (int64_t i = 1; i < n; i++) {
        last = last->next;
    }

    from->head = last->next;
    if (from->head == NULL) {
        from->tail = NULL;
    }

    from->len -= n;
    pthread_mutex_unlock(&from->mu);
    last->next = NULL;
    pthread_mutex_lock(&to->mu);
    if (to->tail != NULL) {
        to->tail->next = first;
    } else {
        to->head = first;
    }

    to->tail = last;
    to->len += n;
    pthread_mutex_unlock(&to->mu);
    return true;
}

// put_next puts t in w's runnext slot; a task it displaces goes on
// w's queue, where another worker may steal it.
static void put_next(worker *w, task *t) {
    pthread_mutex_lock(&w->mu);
    task *old = w->runnext;
    w->runnext = t;
    w->runnext_at = tk_time_mono();
    pthread_mutex_unlock(&w->mu);
    if (old != NULL) {
        push(w, old);
    } else {
        atomic_fetch_add(&nexts, 1);
    }
}

// take_next takes the task in w's runnext slot: any when stale is
// zero, else only one that has waited there longer than stale, for an
// idle worker taking it from a worker whose running task has not let it
// go, since tasks are not preempted.
static task *take_next(worker *w, int64_t stale) {
    pthread_mutex_lock(&w->mu);
    task *t = w->runnext;
    if (t != NULL && (stale == 0 || tk_time_mono() - w->runnext_at > stale)) {
        w->runnext = NULL;
    } else {
        t = NULL;
    }

    pthread_mutex_unlock(&w->mu);
    if (t != NULL) {
        atomic_fetch_sub(&nexts, 1);
    }

    return t;
}

static task *next_task(worker *w) {
    task *t = take_next(w, 0);
    if (t != NULL) {
        return t;
    }

    t = pop(w);
    if (t != NULL) {
        return t;
    }

    t = take_injected();
    if (t != NULL) {
        return t;
    }

    for (int i = 0; i < nworkers; i++) {
        worker *other = &workers[i];
        if (other != w && steal(w, other)) {
            return pop(w);
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
    switch (t->state) {
    case DONE:
        if (t->stack != NULL) {
            stack_free(t->stack, t->size);
        }

        pthread_mutex_destroy(&t->mu);
        free(t->guards);
        free(t);
        atomic_fetch_sub(&running, 1);
        break;
    case PARKING:
        pthread_mutex_lock(&t->mu);
        if (t->woken) { // woken while it switched away: it runs next, here
            t->state = RUNNABLE;
            pthread_mutex_unlock(&t->mu);
            put_next(w, t);
        } else {
            t->state = PARKED;
            pthread_mutex_unlock(&t->mu);
        }

        break;
    }
}

// run_task switches the worker to a task, and settles the task when it
// switches back.
static void run_task(worker *w, task *t) {
    pthread_mutex_lock(&t->mu);
    t->state = RUNNING;
    pthread_mutex_unlock(&t->mu);
    current = t;
    w->last = t;
    tk_switch(&w->sched_sp, t->sp);
    current = NULL;
    settle(w);
}

// A task left in another worker's runnext slot this long, its worker
// busy with a task that does not let go, is taken by an idle worker.
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
// runtime keeps an M spinning: it looks at the inject queue for up to
// spin_for and takes a task the poller or the timers woke meanwhile with
// no thread woken to run it, which is what such a task costs otherwise,
// since their threads are not workers. One worker spins at a time, and
// an inject while one does wakes no other. The spinning adapts, as an adaptive mutex's
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

// spin looks at the inject queue until spin_for has passed, and reports
// whether a task was injected meanwhile.
static bool spin(void) {
    int expected = 0;
    if (atomic_load(&worth) <= 0 || !atomic_compare_exchange_strong(&spinning, &expected, 1)) {
        return false;
    }

    int64_t until = tk_time_mono() + spin_for;
    bool found = false;
    for (int i = 1;; i++) {
        if (atomic_load(&injected) > 0) {
            found = true;
            break;
        }

        if (i % 64 == 0 && tk_time_mono() > until) {
            break;
        }

        tk_spin_hint();
    }

    atomic_store(&spinning, 0);
    found = found || atomic_load(&injected) > 0; // an inject that saw it spinning woke no one: it looks once more
    spun(found);
    return found;
}

// sched_loop runs the worker: the next task, or a spin and then a wait
// for one. While a runnext slot holds a task, an idle worker looks again
// every next_stale, rather than sleep until a queue has one.
static void sched_loop(worker *w) {
    for (;;) {
        task *t = next_task(w);
        if (t == NULL) {
            t = steal_next(w);
        }

        if (t != NULL) {
            run_task(w, t);
            continue;
        }

        if (spin()) { // injects it saw woke no one: when more than its own task waits, it wakes a worker for the rest, as Go's spinning M does
            t = take_injected();
            if (atomic_load(&injected) > 0) {
                wake_idle();
            }

            if (t != NULL) {
                run_task(w, t);
            }

            continue;
        }

        pthread_mutex_lock(&idle_mu);
        atomic_fetch_add(&idle, 1);
        while (atomic_load(&queued) == 0) {
            if (deadlocked()) {
                fflush(stdout);
                fputs("fatal error: all tasks are asleep - deadlock!\n", stderr);
                exit(2);
            }

            if (atomic_load(&nexts) > 0) {
                struct timespec until;
                clock_gettime(CLOCK_REALTIME, &until);
                until.tv_nsec += next_stale;
                if (until.tv_nsec >= 1000000000) {
                    until.tv_sec++;
                    until.tv_nsec -= 1000000000;
                }

                pthread_cond_timedwait(&idle_cond, &idle_mu, &until);
                break;
            }

            int64_t asleep = tk_time_mono();
            pthread_cond_wait(&idle_cond, &idle_mu);
            if (atomic_load(&injected) > 0 && tk_time_mono() - asleep < spin_for) {
                spun(true); // a wake from outside came soon enough that a spin would have caught it
            }
        }

        atomic_fetch_sub(&idle, 1);
        pthread_mutex_unlock(&idle_mu);
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

// start makes the workers: the main thread is the first, and one
// thread per further core.
static void start(void) {
    pthread_mutex_lock(&start_mu);
    if (!started) {
        long cores = sysconf(_SC_NPROCESSORS_ONLN);
        nworkers = cores > 1 ? (int)cores : 1;
        workers = calloc((size_t)nworkers, sizeof *workers);
        if (workers == NULL) {
            tk_panic(TK_STR("out of memory", 13));
        }

        for (int i = 0; i < nworkers; i++) {
            pthread_mutex_init(&workers[i].mu, NULL);
        }

        if (my_worker() == NULL) {
            self_worker = &workers[0]; // the main thread
        }

        started = true;
        for (int i = 1; i < nworkers; i++) {
            pthread_attr_t attr;
            pthread_attr_init(&attr);
            pthread_attr_setdetachstate(&attr, PTHREAD_CREATE_DETACHED);
            pthread_t id;
            if (pthread_create(&id, &attr, worker_main, &workers[i]) != 0) {
                tk_panic(TK_STR("cannot start a worker", 21));
            }

            pthread_attr_destroy(&attr);
        }
    }

    pthread_mutex_unlock(&start_mu);
}

// task_entry is where a task's context begins: the function with its
// argument block, and then the switch back that run_task frees it
// after. It never returns, since nothing called it.
static void task_entry(void) {
    task *t = my_task();
    t->run(t->arg);
    t->state = DONE;
    tk_switch(&t->sp, my_worker()->sched_sp);
    abort(); // a done task is never resumed
}

void tk_spawn(void (*run)(void *), void *arg) {
    if (!started) {
        start();
    }

    tk_self();
    task *t = calloc(1, sizeof *t);
    if (t == NULL) {
        tk_panic(TK_STR("out of memory", 13));
    }

    t->run = run;
    t->arg = arg;
    t->stack = stack_new(&t->size);
    t->sp = make_context((uint8_t *)t->stack + t->size, task_entry);
    pthread_mutex_init(&t->mu, NULL);
    t->state = RUNNABLE;
    atomic_fetch_add(&running, 1);
    push(home(), t);
}

int64_t tk_tasks_running(void) {
    return atomic_load(&running);
}

// tk_park waits for a wake: the task switches to its worker's
// scheduler, which settles it as parked, and resumes when a wake puts
// it back on a queue and a worker runs it. A wake that came first is
// consumed at once. The main thread's scheduler context is made the
// first time the main task parks.
void tk_park(void) {
    task *t = tk_self();
    if (!started) {
        start(); // the main task sleeping before any spawn: the thread still needs its scheduler
    }

    worker *w = my_worker();
    if (w->sched_sp == NULL) { // the main thread, parking for the first time
        size_t size;
        w->sched_stack = stack_new(&size);
        w->sched_sp = make_context((uint8_t *)w->sched_stack + size, sched_entry);
    }

    pthread_mutex_lock(&t->mu);
    while (!t->woken) {
        t->state = PARKING;
        pthread_mutex_unlock(&t->mu);
        w->last = t;
        tk_switch(&t->sp, w->sched_sp);
        w = my_worker(); // resumed, on whichever worker ran the queue
        pthread_mutex_lock(&t->mu);
    }

    t->woken = false;
    pthread_mutex_unlock(&t->mu);
}

// tk_wake makes a parked task runnable, or keeps the wake for a task
// still running or parking, which consumes it at its next park.
void tk_wake(tk_task *t) {
    pthread_mutex_lock(&t->mu);
    t->woken = true;
    if (t->state == PARKED) {
        t->state = RUNNABLE;
        pthread_mutex_unlock(&t->mu);
        worker *w = my_worker();
        if (w != NULL) {
            put_next(w, t); // woken by a task: it runs next on this worker
        } else {
            inject(t); // by the timer's or the poller's thread
        }

        return;
    }

    pthread_mutex_unlock(&t->mu);
}

// Timers: a heap of deadlines, served by one thread that sleeps until
// the earliest. A timer either wakes a sleeping task, or, for After and
// Tick, puts the time in a channel, or calls a function of the runtime's
// own, as a socket's deadline does; a ticking one goes back in the heap
// for its next tick. An After or a Tick is so an entry in the heap, as
// in Go's runtime, and no task waits for it. A channel's timer holds a
// handle of its own: an After drops it once it fired, a Tick once no
// other handle is left to receive with, and so the channel of an After
// no one received from is freed when it fires.
typedef struct {
    int64_t when;
    task *t;
    bool *fired;    // the sleeping task's own flag, set under timers_mu
    tk_chan *ch;    // or the channel the time goes to
    int64_t every;  // and, for a Tick, its period
    void (*fire)(void *); // or the function to call, with arg
    void *arg;
    int64_t *at; // where the function's timer is kept: one more than its index in the heap, 0 when out of it
} timer;

// The deadlines are on the monotonic clock, which the system never sets
// back, so a change of the wall clock moves no timer. timers_cond is
// made by start_timing, waiting on that clock where the system lets a
// condition choose it, and the timer's thread waits a relative time
// where it does not.
static pthread_mutex_t timers_mu = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t timers_cond;
static timer *timers;
static int64_t ntimers, captimers;
static bool timing;
static int64_t firing;   // channel timers fired and not yet done with their handle
static bool stopping;    // the program is ending: no timer fires again
static pthread_cond_t fired_cond = PTHREAD_COND_INITIALIZER;

// place puts x at index i of the heap, and tells a channel's timer
// where it is, so that the timer can be taken out when no one is left
// to receive what it would send. The caller holds timers_mu.
static void place(int64_t i, timer x) {
    timers[i] = x;
    if (x.ch != NULL) {
        tk_chan_placeTimer(x.ch, i);
    } else if (x.at != NULL) {
        *x.at = i + 1;
    }
}

// sift_up and sift_down put x where the heap's order wants it, from the
// hole at i.
static void sift_up(int64_t i, timer x) {
    while (i > 0 && timers[(i - 1) / 2].when > x.when) {
        place(i, timers[(i - 1) / 2]);
        i = (i - 1) / 2;
    }

    place(i, x);
}

static void sift_down(int64_t i, timer x) {
    for (;;) {
        int64_t l = 2 * i + 1, r = l + 1, m = i;
        if (l < ntimers && timers[l].when < x.when) {
            m = l;
        }

        if (r < ntimers && timers[r].when < (m == i ? x.when : timers[m].when)) {
            m = r;
        }

        if (m == i) {
            break;
        }

        place(i, timers[m]);
        i = m;
    }

    place(i, x);
}

// timer_push puts x in the heap; the caller holds timers_mu. Every
// timer is counted outside until what it wakes is woken.
static void timer_push(timer x) {
    tk_outside(1);
    if (ntimers == captimers) {
        captimers = captimers == 0 ? 64 : captimers * 2;
        timers = realloc(timers, (size_t)captimers * sizeof *timers);
        if (timers == NULL) {
            tk_panic(TK_STR("out of memory", 13));
        }
    }

    sift_up(ntimers++, x);
}

// timer_remove takes the timer at i out of the heap and returns it; the
// caller holds timers_mu.
static timer timer_remove(int64_t i) {
    timer gone = timers[i];
    timer x = timers[--ntimers];
    if (i < ntimers) {
        if (i > 0 && timers[(i - 1) / 2].when > x.when) {
            sift_up(i, x);
        } else {
            sift_down(i, x);
        }
    }

    if (gone.ch != NULL) {
        tk_chan_placeTimer(gone.ch, -1);
    } else if (gone.at != NULL) {
        *gone.at = 0;
    }

    return gone;
}

static timer timer_pop(void) {
    return timer_remove(0);
}

static void *timer_main(void *arg) {
    (void)arg;
    pthread_mutex_lock(&timers_mu);
    for (;;) {
        if (ntimers == 0 || stopping) {
            pthread_cond_wait(&timers_cond, &timers_mu);
            continue;
        }

        int64_t when = timers[0].when;
        int64_t now = tk_time_mono();
        if (when <= now) {
            timer due = timer_pop();
            if (due.fire != NULL) { // under the lock, so that a disarm either takes it out first or waits for it to finish
                due.fire(due.arg);
                tk_outside(-1);
                continue;
            }

            if (due.ch == NULL) { // wake first, then fire, both under the lock: once the task sees its flag, nothing here touches it again
                tk_wake(due.t);
                *due.fired = true;
                tk_outside(-1);
                continue;
            }

            // A Tick's next tick is on the period, past the ticks a slow
            // receiver missed.
            int64_t next = due.every > 0 ? now + due.every - (now - due.when) % due.every : 0;
            firing++;
            pthread_mutex_unlock(&timers_mu); // the channel's lock is never taken under this one
            tk_time at = tk_time_read();
            bool heard = tk_chan_offer(due.ch, &at);
            pthread_mutex_lock(&timers_mu);
            bool again = heard && due.every > 0 && !stopping;
            if (again) {
                due.when = next;
                timer_push(due); // counted outside before this one stops counting, so the count never passes through zero
                if (tk_chan_alone(due.ch)) {
                    // The program's last handle went after the offer and
                    // before the push, so its drop found no timer in the
                    // heap to take out: this does it. The drop takes away
                    // its handle before it looks for the timer, and the
                    // push places the timer before this looks at the
                    // handles, so one of the two always sees the other.
                    timer_remove(tk_chan_timer(due.ch));
                    tk_outside(-1);
                    again = false;
                }
            }

            if (again) {
                due.ch = NULL; // the handle went back in the heap with the Tick
            }

            pthread_mutex_unlock(&timers_mu);
            tk_chan_drop(&due.ch); // an After's handle, or a Tick's no one can receive from: nothing when it went back in the heap
            tk_outside(-1);
            pthread_mutex_lock(&timers_mu);
            firing--;
            pthread_cond_broadcast(&fired_cond);
            continue;
        }

#if defined(__APPLE__)
        int64_t wait = when - now;
        struct timespec rel = {(time_t)(wait / 1000000000), (long)(wait % 1000000000)};
        pthread_cond_timedwait_relative_np(&timers_cond, &timers_mu, &rel);
#else
        struct timespec ts = {(time_t)(when / 1000000000), (long)(when % 1000000000)};
        pthread_cond_timedwait(&timers_cond, &timers_mu, &ts);
#endif
    }

    return NULL;
}

// deadline is the time d from now on the monotonic clock, the largest
// time when that is past it, so that a Sleep or an After of the longest
// Duration waits for as long as there is rather than wrapping round to
// fire at once.
static int64_t deadline(int64_t d) {
    int64_t now = tk_time_mono();
    return d > INT64_MAX - now ? INT64_MAX : now + d;
}

// start_timing makes the timer's condition and starts its thread, once;
// the caller holds timers_mu.
static void start_timing(void) {
    if (timing) {
        return;
    }

    pthread_condattr_t cattr;
    pthread_condattr_init(&cattr);
#if !defined(__APPLE__)
    pthread_condattr_setclock(&cattr, CLOCK_MONOTONIC);
#endif
    pthread_cond_init(&timers_cond, &cattr);
    pthread_condattr_destroy(&cattr);
    pthread_t id;
    pthread_attr_t attr;
    pthread_attr_init(&attr);
    pthread_attr_setdetachstate(&attr, PTHREAD_CREATE_DETACHED);
    if (pthread_create(&id, &attr, timer_main, NULL) != 0) {
        tk_panic(TK_STR("cannot start the timer", 22));
    }

    pthread_attr_destroy(&attr);
    timing = true;
}

// tk_deadline_arm calls fire with arg d from now, on the timers'
// thread, under timers_mu: a function that takes no lock the timers'
// thread waits behind. The timer is kept at *at, which is 0 while it is
// out of the heap; arming it again moves it. Like every timer it is a
// wake promised from outside the workers until it fires or is disarmed.
void tk_deadline_arm(int64_t *at, int64_t d, void (*fire)(void *), void *arg) {
    pthread_mutex_lock(&timers_mu);
    start_timing();
    if (*at > 0) {
        timer_remove(*at - 1);
        tk_outside(-1);
    }

    timer_push((timer){.when = deadline(d), .fire = fire, .arg = arg, .at = at});
    pthread_cond_signal(&timers_cond);
    pthread_mutex_unlock(&timers_mu);
}

// tk_deadline_disarm takes the timer kept at *at out of the heap, if it
// is in it. Once it returns, the timer's function is not running and
// will not run.
void tk_deadline_disarm(int64_t *at) {
    pthread_mutex_lock(&timers_mu);
    if (*at > 0) {
        timer_remove(*at - 1);
        tk_outside(-1);
    }

    pthread_mutex_unlock(&timers_mu);
}

// tk_time_startTimer puts the time in ch once, d from now, and then
// every period, when every is above zero.
void tk_time_startTimer(tk_chan *ch, int64_t d, int64_t every) {
    pthread_mutex_lock(&timers_mu);
    start_timing();
    timer_push((timer){.when = deadline(d), .ch = ch, .every = every});
    pthread_cond_signal(&timers_cond);
    pthread_mutex_unlock(&timers_mu);
}

// tk_timer_abandon takes out the timer of c, whose last handle but the
// timer's own was dropped: no one can receive what it would send, so it
// is no promise of a wake, and its handle, the last, frees the channel
// now rather than at its deadline. A timer that fired meanwhile is out
// of the heap already and drops its handle itself, and at the end of
// the program tk_timers_stop drops them all.
void tk_timer_abandon(tk_chan *c) {
    pthread_mutex_lock(&timers_mu);
    int64_t at = tk_chan_timer(c);
    if (at < 0 || stopping) {
        pthread_mutex_unlock(&timers_mu);
        return;
    }

    timer gone = timer_remove(at);
    pthread_mutex_unlock(&timers_mu);
    tk_outside(-1);
    tk_chan_drop(&gone.ch);
}

// tk_timers_stop stops the timers as the program ends and drops the
// handles the channel timers hold, after any timer firing now is done
// with its own, so that a debug build's count of blocks finds the
// channels of a pending After or a running Tick freed.
void tk_timers_stop(void) {
    pthread_mutex_lock(&timers_mu);
    stopping = true;
    while (firing > 0) {
        pthread_cond_wait(&fired_cond, &timers_mu);
    }

    int64_t n = 0;
    tk_chan **held = malloc((size_t)(ntimers > 0 ? ntimers : 1) * sizeof *held);
    if (held == NULL) {
        tk_panic(TK_STR("out of memory", 13));
    }

    for (int64_t i = 0; i < ntimers; i++) {
        if (timers[i].ch != NULL) {
            held[n++] = timers[i].ch;
        }
    }

    ntimers = 0; // the heap held the handles taken here, and no entry may point at a channel they free
    pthread_mutex_unlock(&timers_mu);
    for (int64_t i = 0; i < n; i++) {
        tk_chan_drop(&held[i]);
    }

    free(held);
}

// tk_sleep parks the task until its timer fires. A park can return for
// a wake meant for an earlier wait, one a select's second channel sent
// before the select unlinked from it, so the task parks until its own
// flag says the timer fired; returning sooner would cut the sleep short
// and leave the timer to wake a task that may be gone.
void tk_sleep(int64_t d) {
    if (d <= 0) {
        return;
    }

    task *t = tk_self();
    pthread_mutex_lock(&timers_mu);
    start_timing();
    bool fired = false;
    timer_push((timer){.when = deadline(d), .t = t, .fired = &fired});
    pthread_cond_signal(&timers_cond);
    while (!fired) {
        pthread_mutex_unlock(&timers_mu);
        tk_park();
        pthread_mutex_lock(&timers_mu);
    }

    pthread_mutex_unlock(&timers_mu);
}

// The guard stack of a task: the values a panic drops, innermost
// last. It grows with the system allocator, not tk_alloc, so a debug
// build does not count it among the program's blocks. A panic unwinds
// the task it happens in and no other, since the others are on other
// workers or parked in the middle of their work.
void tk_guard(void *p, void (*drop)(void *)) {
    task *t = tk_self();
    if (t->nguards == t->guardcap) {
        int64_t cap = t->guardcap < 16 ? 16 : t->guardcap * 2;
        guard *more = realloc(t->guards, (size_t)cap * sizeof *more);
        if (more == NULL) {
            tk_panic(TK_STR("out of memory", 13)); // the stack so far is intact for the unwinding
        }

        t->guards = more;
        t->guardcap = cap;
    }

    t->guards[t->nguards++] = (guard){p, drop};
}

// tk_unguard removes the guard of p, searched from the top: a value is
// usually dropped in the reverse order of its registration, but a
// temporary of a statement may be dropped after a variable the
// statement declared.
void tk_unguard(void *p) {
    task *t = tk_self();
    for (int64_t i = t->nguards - 1; i >= 0; i--) {
        if (t->guards[i].p == p) {
            memmove(&t->guards[i], &t->guards[i + 1], (size_t)(t->nguards - i - 1) * sizeof *t->guards);
            t->nguards--;
            return;
        }
    }
}

// tk_panic prints the message, then runs the drops of the guarded
// values, innermost first, and ends the program. A Drop that panics
// during that ends the program with its own message at once.
_Noreturn void tk_panic(tk_string msg) {
    fflush(stdout);
    fputs("panic: ", stderr);
    fwrite(msg.ptr, 1, (size_t)msg.len, stderr);
    fputc('\n', stderr);
    task *t = tk_self();
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
