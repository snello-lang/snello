// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#define _POSIX_C_SOURCE 200809L
#define _DARWIN_C_SOURCE

#include "internal.h"

// Locks, as _docs/design/06-tasks-and-channels.md lays them out: one
// object serves the sync package's Mutex, RwLock and WaitGroup. The
// library holds a lock as an int, as it holds a file descriptor: the
// handle is the lock's address, and 0, which no lock has, is the zero
// handle of a WaitGroup, the one handle the library gives out unmade.
//
// The lock held alone is Rust's std::sync::Mutex, its futex mutex: one
// word, 0 when free, 1 when held, 2 when held and a task may be waiting.
// Taking a free lock is one compare-and-swap and giving it back one
// exchange, with no other memory touched; a task finding it held spins
// a little, since a lock is usually held briefly, and then parks until
// the word is no longer 2, and giving back a lock at 2 wakes one of
// those that wait. It is not fair, as neither Rust's nor Go's normal
// mode is: a task arriving as the lock is given back may take it ahead
// of one that waited, which keeps the lock busy instead of idle while
// a woken task is switched in.
//
// An RwLock adds Go's RWMutex over that: readers count themselves in
// with one atomic add, and a writer, once it holds the word, subtracts
// max_readers from the count, so that readers after it wait, and waits
// for the readers already in to leave, which the last of them signals.
// A writer waiting so holds back the readers that come after it, as
// Go's does, so that a stream of readers cannot keep a writer out for
// ever; a reader therefore must not take the lock again while it holds
// it. The waits are semaphores over the lock's queues.
typedef struct {
    _Atomic int64_t count; // units free
    _Atomic int64_t nwait; // tasks in, or on their way into, the queue
    sn_waitq q;
} semaphore;

typedef struct {
    _Atomic int32_t state;     // the futex mutex's word
    bool rw;                   // an RwLock's, which counts readers
    _Atomic int64_t readers;   // Go's readerCount
    _Atomic int64_t departing; // Go's readerWait: readers a writer waits for
    sn_qlock mu;              // over the queues and the count
    sn_waitq lockq;               // tasks waiting for the word to leave 2
    semaphore reading;         // readers waiting for a writer
    semaphore writing;         // the writer waiting for the readers before it
    int64_t count;             // the wait group's
    sn_waitq groupq;              // tasks waiting for the count to reach zero
} sn_lock;

static const int64_t max_readers = (int64_t)1 << 30;

static sn_lock *new_lock(bool rw) {
    sn_lock *l = sn_alloc(sizeof *l);
    memset(l, 0, sizeof *l);
    l->rw = rw;
    sn_qlock_init(&l->mu);
    return l;
}

// The lock's memory is sn_alloc's, so a debug build reports a lock
// that nothing freed.
int64_t sn_sync_newLock(void) {
    return (int64_t)(intptr_t)new_lock(false);
}

int64_t sn_sync_newRwLock(void) {
    return (int64_t)(intptr_t)new_lock(true);
}

// sn_sync_freeLock frees the lock. The Mutex, RwLock or WaitGroup
// counter that frees its lock owns it, the last handle in a WaitGroup's
// case, so no task holds or waits on it by then.
void sn_sync_freeLock(int64_t h) {
    sn_lock *l = (sn_lock *)(intptr_t)h;
    sn_qlock_free(&l->mu);
    sn_free(l);
}

// park_while parks the task while the word is 2, as a futex wait does:
// the word is read under the queue's lock, which a waker takes too, so
// a wake cannot fall between the read and the park.
static void park_while(sn_lock *l) {
    sn_qlock_take(&l->mu);
    if (atomic_load(&l->state) != 2) {
        sn_qlock_give(&l->mu);
        return;
    }

    sn_wait_state ws = {0};
    sn_waiter me = {sn_self(), NULL, &ws, 0, false, false, NULL, NULL};
    sn_enqueue(&l->lockq, &me);
    sn_qlock_give(&l->mu);
    sn_await_wait(&ws);
    sn_qlock_take(&l->mu);
    sn_qlock_give(&l->mu);
}

static void wake_one(sn_lock *l) {
    sn_qlock_take(&l->mu);
    sn_waiter *w = sn_dequeue(&l->lockq);
    if (w != NULL) {
        sn_finish_wait(w, true);
    }

    sn_qlock_give(&l->mu);
}

// spin waits a little for a lock held by a running task, and returns
// the word as it last read it.
static int32_t spin(sn_lock *l) {
    int32_t s = atomic_load_explicit(&l->state, memory_order_relaxed);
    for (int i = 0; i < 100 && s == 1; i++) {
        sn_spin_hint();
        s = atomic_load_explicit(&l->state, memory_order_relaxed);
    }

    return s;
}

static void mutex_lock(sn_lock *l) {
    int32_t s = 0;
    if (atomic_compare_exchange_strong_explicit(&l->state, &s, 1, memory_order_acquire, memory_order_relaxed)) {
        return;
    }

    s = spin(l);
    if (s == 0 && atomic_compare_exchange_strong_explicit(&l->state, &s, 1, memory_order_acquire, memory_order_relaxed)) {
        return;
    }

    for (;;) {
        // Marked 2 when taken this way: a task may be waiting still.
        if (s != 2 && atomic_exchange_explicit(&l->state, 2, memory_order_acquire) == 0) {
            return;
        }

        park_while(l);
        s = spin(l);
    }
}

static void mutex_unlock(sn_lock *l) {
    if (atomic_exchange_explicit(&l->state, 0, memory_order_release) == 2) {
        wake_one(l);
    }
}

// A semaphore is Go's runtime semaphore: a release adds a unit and
// wakes a waiter, which takes the unit if no other task did first, and
// waits again if one did. nwait lets a release with no one waiting skip
// the queue's lock: a waiter counts itself before it looks at the
// units, and a release adds its unit before it looks at nwait, so one
// of the two sees the other.
static bool sema_try(semaphore *s) {
    int64_t c = atomic_load(&s->count);
    while (c > 0) {
        if (atomic_compare_exchange_weak(&s->count, &c, c - 1)) {
            return true;
        }
    }

    return false;
}

static void sema_acquire(sn_lock *l, semaphore *s) {
    if (sema_try(s)) {
        return;
    }

    sn_qlock_take(&l->mu);
    atomic_fetch_add(&s->nwait, 1);
    while (!sema_try(s)) {
        sn_wait_state ws = {0};
        sn_waiter me = {sn_self(), NULL, &ws, 0, false, false, NULL, NULL};
        sn_enqueue(&s->q, &me);
        sn_qlock_give(&l->mu);
        sn_await_wait(&ws);
        sn_qlock_take(&l->mu);
    }

    atomic_fetch_sub(&s->nwait, 1);
    sn_qlock_give(&l->mu);
}

static void sema_release(sn_lock *l, semaphore *s) {
    atomic_fetch_add(&s->count, 1);
    if (atomic_load(&s->nwait) == 0) {
        return;
    }

    sn_qlock_take(&l->mu);
    sn_waiter *w = sn_dequeue(&s->q);
    if (w != NULL) {
        sn_finish_wait(w, true);
    }

    sn_qlock_give(&l->mu);
}

void sn_sync_lock(int64_t h) {
    sn_lock *l = (sn_lock *)(intptr_t)h;
    mutex_lock(l);
    if (l->rw) { // hold back new readers, and wait for those in to leave
        int64_t r = atomic_fetch_sub(&l->readers, max_readers);
        if (r != 0 && atomic_fetch_add(&l->departing, r) + r != 0) {
            sema_acquire(l, &l->writing);
        }
    }
}

void sn_sync_unlock(int64_t h) {
    sn_lock *l = (sn_lock *)(intptr_t)h;
    if (l->rw) { // let in the readers that waited
        int64_t r = atomic_fetch_add(&l->readers, max_readers) + max_readers;
        for (int64_t i = 0; i < r; i++) {
            sema_release(l, &l->reading);
        }
    }

    mutex_unlock(l);
}

void sn_sync_lockShared(int64_t h) {
    sn_lock *l = (sn_lock *)(intptr_t)h;
    if (atomic_fetch_add(&l->readers, 1) + 1 < 0) { // a writer holds the lock or waits for it
        sema_acquire(l, &l->reading);
    }
}

void sn_sync_unlockShared(int64_t h) {
    sn_lock *l = (sn_lock *)(intptr_t)h;
    if (atomic_fetch_sub(&l->readers, 1) - 1 < 0 && atomic_fetch_sub(&l->departing, 1) - 1 == 0) {
        sema_release(l, &l->writing); // the last reader a writer waited for
    }
}

// lock_of returns the lock h names; an empty handle is a WaitGroup's,
// since a Mutex and an RwLock catch their own.
static sn_lock *lock_of(int64_t h) {
    sn_lock *l = (sn_lock *)(intptr_t)h;
    if (l == NULL) {
        sn_panic(SN_STR("sync: use of an empty WaitGroup", 31));
    }

    return l;
}

// sn_sync_count adds n to the count and wakes the waiters when it
// reaches zero; a count below zero is a Done without its Add.
void sn_sync_count(int64_t h, int64_t n) {
    sn_lock *l = lock_of(h);
    sn_qlock_take(&l->mu);
    l->count += n;
    if (l->count < 0) {
        sn_qlock_give(&l->mu);
        sn_panic(SN_STR("sync: negative WaitGroup count", 30));
    }

    if (l->count == 0) {
        for (sn_waiter *w; (w = sn_dequeue(&l->groupq)) != NULL;) {
            sn_finish_wait(w, true);
        }
    }

    sn_qlock_give(&l->mu);
}

void sn_sync_wait(int64_t h) {
    sn_lock *l = lock_of(h);
    sn_qlock_take(&l->mu);
    if (l->count == 0) {
        sn_qlock_give(&l->mu);
        return;
    }

    sn_wait_state ws = {0};
    sn_waiter me = {sn_self(), NULL, &ws, 0, false, false, NULL, NULL};
    sn_enqueue(&l->groupq, &me);
    sn_qlock_give(&l->mu);
    sn_await_wait(&ws);
    sn_qlock_take(&l->mu);
    sn_qlock_give(&l->mu);
}

// sn_sync_atomicAdd adds d to *n as one step no other task's add can
// interleave with, and returns the new value; the Arc's count is kept
// with it, and the handle that brings it to zero drops the value.
int64_t sn_sync_atomicAdd(int64_t *n, int64_t d) {
    return __atomic_add_fetch(n, d, __ATOMIC_SEQ_CST);
}

// The words of sync.Counter: one each on the
// heap, named by its address as a lock is, so that a handle is a plain
// int the package's value holds and every operation is one atomic
// instruction on the word. Sequentially consistent, as Go's atomics
// are: the cost is a fence on arm64, and a program that needs less
// is a program that has measured.
int64_t sn_sync_cellNew(int64_t v) {
    int64_t *p = sn_alloc(sizeof *p);
    __atomic_store_n(p, v, __ATOMIC_SEQ_CST);
    return (int64_t)(intptr_t)p;
}

int64_t sn_sync_cellLoad(int64_t h) {
    return __atomic_load_n((int64_t *)(intptr_t)h, __ATOMIC_SEQ_CST);
}

void sn_sync_cellStore(int64_t h, int64_t v) {
    __atomic_store_n((int64_t *)(intptr_t)h, v, __ATOMIC_SEQ_CST);
}

int64_t sn_sync_cellAdd(int64_t h, int64_t d) {
    return __atomic_add_fetch((int64_t *)(intptr_t)h, d, __ATOMIC_SEQ_CST);
}

bool sn_sync_cellSwap(int64_t h, int64_t old, int64_t v) {
    return __atomic_compare_exchange_n((int64_t *)(intptr_t)h, &old, v, false, __ATOMIC_SEQ_CST, __ATOMIC_SEQ_CST);
}

void sn_sync_cellFree(int64_t h) {
    sn_free((void *)(intptr_t)h);
}
