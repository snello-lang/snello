// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// What the runtime's files share and the program's C does not see: the
// helpers teak.c defines for the others, and the types and the lock of
// the queues a task waits on, which chan.c and sync.c both use. The
// names begin with tk_, as teak.h's do, since a function here is
// external and meets the program's names and the system's when linked.
// A file of the runtime includes this header first, after the feature
// macros it asks for.
#ifndef TK_INTERNAL_H
#define TK_INTERNAL_H

#include "teak.h"

#include <pthread.h>
#include <stdatomic.h>
#ifdef __APPLE__
#include <os/lock.h>
#endif

// The most one read or write asks of the system, as Go's runtime caps
// it: Darwin refuses a count past INT_MAX, so a larger buffer is read
// or written in turns.
#define TK_IO_MAX ((size_t)1 << 30)

// A task waiting on a channel is a waiter on the channel's queue of
// senders or of receivers, as Go's sudog is: the task, and the value it
// sends or the place it receives into. An operation that finds a
// waiter does its half for it, copying the value straight from the
// sender or into the receiver, and wakes that one task, which has
// nothing left to do; a close wakes every waiter, each to learn it got
// nothing. A select waits on all its channels at once, one waiter a
// case, and the first operation to take one of them takes the select:
// the waiters share a state the taking operation claims with one
// compare-and-swap, and a waiter of a select already taken is dropped
// when another operation meets it. Waiters live in the frame of the
// wait; the operation that takes one finishes with it under the lock
// of its channel, which the woken task takes before its frame ends.
typedef struct {
    _Atomic int claimed; // 0 until an operation takes the wait
    _Atomic int done;    // set by that operation once it is done
    int fired;           // the case it took, for a select
} tk_wait_state;

typedef struct tk_waiter {
    tk_task *t;
    void *elem; // the value sent, or the place to receive into
    tk_wait_state *ws;
    int index;    // the case, for a select
    bool success; // a value moved; false when a close woke the waiter
    bool queued;
    struct tk_waiter *prev;
    struct tk_waiter *next;
} tk_waiter;

typedef struct {
    tk_waiter *head;
    tk_waiter *tail;
} tk_waitq;

// tk_qlock is the lock over the queues of a channel or of a lock. It is
// held for a few instructions, by tasks on every worker at once when
// many send to one channel, and never across a park: the system's
// fastest lock, os_unfair_lock on Darwin, whose pthread mutex is fair
// and hands off through the kernel, and glibc's pthread mutex on Linux,
// a futex with no system call when uncontended. A task never parks
// holding it, so the thread that takes it is the one that gives it
// back, as os_unfair_lock requires.
#ifdef __APPLE__
typedef os_unfair_lock tk_qlock;
static inline void tk_qlock_init(tk_qlock *l) { *l = OS_UNFAIR_LOCK_INIT; }
static inline void tk_qlock_take(tk_qlock *l) { os_unfair_lock_lock(l); }
static inline void tk_qlock_give(tk_qlock *l) { os_unfair_lock_unlock(l); }
static inline void tk_qlock_free(tk_qlock *l) { (void)l; }
#else
typedef pthread_mutex_t tk_qlock;
static inline void tk_qlock_init(tk_qlock *l) { pthread_mutex_init(l, NULL); }
static inline void tk_qlock_take(tk_qlock *l) { pthread_mutex_lock(l); }
static inline void tk_qlock_give(tk_qlock *l) { pthread_mutex_unlock(l); }
static inline void tk_qlock_free(tk_qlock *l) { pthread_mutex_destroy(l); }
#endif

// tk_spin_hint tells the core a loop is waiting on memory.
static inline void tk_spin_hint(void) {
#if defined(__aarch64__)
    __asm__ volatile("yield");
#elif defined(__x86_64__)
    __asm__ volatile("pause");
#endif
}

// tk_slice_grow makes room in s for at least need elements of size
// bytes, doubling, as Push amortizes to constant time.
void tk_slice_grow(tk_slice *s, int64_t need, size_t size);

// tk_utf8_decode reads the rune at p, at most n bytes, as Go's
// utf8.DecodeRune does, and returns its width; tk_utf8_encode writes r
// at out and returns its length.
int tk_utf8_decode(const uint8_t *p, int64_t n, int32_t *r);
int tk_utf8_encode(int32_t r, uint8_t *out);

// tk_format_uint writes u's decimal digits ending at end and returns
// where they begin; tk_format_int makes the owned string of u, negated
// when neg.
char *tk_format_uint(uint64_t u, char *end);
tk_string tk_format_int(uint64_t u, bool neg);

// tk_write_fd writes every byte of p to fd and returns how many it
// wrote, all of len or fewer with errno saying why.
int64_t tk_write_fd(int fd, const uint8_t *p, int64_t len);

// tk_next_random is the calling thread's next random number.
uint64_t tk_next_random(void);

// tk_deadline_arm calls fire(arg) d nanoseconds from now on the timers'
// thread, keeping the timer at *at, 0 while it is out of the heap, so
// that arming it again moves it and tk_deadline_disarm takes it out;
// once that returns, fire is not running and will not run. sched.c's.
void tk_deadline_arm(int64_t *at, int64_t d, void (*fire)(void *), void *arg);
void tk_deadline_disarm(int64_t *at);

// The queues of waiters: tk_enqueue adds w at the tail of q and
// tk_remove_waiter takes it out wherever it is; tk_dequeue takes the
// first waiter whose wait is still open and claims it; tk_finish_wait
// ends a wait tk_dequeue took and wakes its task; tk_await_wait parks
// until an operation has finished the wait.
void tk_enqueue(tk_waitq *q, tk_waiter *w);
void tk_remove_waiter(tk_waitq *q, tk_waiter *w);
tk_waiter *tk_dequeue(tk_waitq *q);
void tk_finish_wait(tk_waiter *w, bool success);
void tk_await_wait(tk_wait_state *ws);

#endif
