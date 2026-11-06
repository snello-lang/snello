// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Tasks and channels: _docs/design/06-tasks-and-channels.md. The
// tasks themselves are sched.c's; a channel is Go's hchan: a locked
// ring for a buffered channel, and two queues of the tasks waiting on
// it, the senders and the receivers.

#define _POSIX_C_SOURCE 200809L
#define _DARWIN_C_SOURCE

#include "internal.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

// The channel counts its handles: Clone adds one, a handle's drop takes
// one away, and the last frees the channel, dropping the values still
// in the ring with the element's drop the compiler gave, NULL for one
// that owns nothing. No task can be waiting on it then, since a waiting
// task holds a handle, and nothing else can reach it.
struct sn_chan {
    sn_qlock mu;
    _Atomic int64_t handles;
    _Atomic int64_t timer; // where the channel's After or Tick is in the timers' heap, -1 when it has none there
    void (*drop)(void *); // drops one element
    size_t size;  // of one element
    int64_t cap;  // zero for an unbuffered channel, which has no ring
    uint8_t *buf; // the ring of a buffered one
    int64_t head; // the oldest element of the ring
    int64_t len;  // elements in the ring
    bool closed;
    sn_waitq sendq; // senders waiting: the ring is full, or there is none
    sn_waitq recvq; // receivers waiting: the ring is empty
};

static uint8_t *slot(sn_chan *c, int64_t i) {
    return c->buf + (size_t)(i % c->cap) * c->size;
}

// sn_chan_make makes a channel with its first handle. Its memory is
// sn_alloc's, so a debug build reports a channel no handle freed.
sn_chan *sn_chan_make(size_t size, int64_t cap, void (*drop)(void *)) {
    sn_chan *c = sn_alloc(sizeof *c);
    memset(c, 0, sizeof *c);
    c->cap = cap;
    if (cap > 0) {
        c->buf = sn_alloc((size == 0 ? 1 : size) * (size_t)cap);
    }

    sn_qlock_init(&c->mu);
    atomic_init(&c->handles, 1);
    atomic_init(&c->timer, -1);
    c->drop = drop;
    c->size = size;
    return c;
}

// sn_chan_release takes one handle away, for sn_timer_abandon under the
// timers' lock, and reports whether the one left is the timer's.
bool sn_chan_release(sn_chan *c) {
    return atomic_fetch_sub(&c->handles, 1) == 2;
}

// sn_chan_timer and sn_chan_placeTimer read and set where the
// channel's timer is in the timers' heap, which sched.c keeps under its
// lock; -1 is none.
int64_t sn_chan_timer(sn_chan *c) {
    return atomic_load(&c->timer);
}

void sn_chan_placeTimer(sn_chan *c, int64_t at) {
    atomic_store(&c->timer, at);
}

// sn_chan_alone reports whether the caller's handle is the only one.
bool sn_chan_alone(sn_chan *c) {
    return atomic_load(&c->handles) == 1;
}

// sn_chan_clone gives another handle on c, for another task.
sn_chan *sn_chan_clone(sn_chan *c) {
    atomic_fetch_add(&c->handles, 1);
    return c;
}

// sn_chan_drop drops the handle at *c, leaving the place empty, as a
// move does; the last handle frees the channel and what it holds. When
// the one left is a pending timer's, which only a timer still in the
// heap holds, no one can receive from the channel again, and the timer
// is taken out and its handle dropped at once, so a select that took
// another case leaves no After behind it until its deadline. That is
// decided in sn_timer_abandon, under the timers' lock, since a channel
// is never touched after its handle is given up: the other holder may
// drop the last and free it the moment the count comes down, and the
// timers' thread once read the timer of a channel freed and reused.
void sn_chan_drop(sn_chan **h) {
    sn_chan *c = *h;
    *h = NULL;
    if (c == NULL) {
        return;
    }

    if (atomic_load(&c->timer) >= 0 && sn_timer_abandon(c)) {
        return;
    }

    if (atomic_fetch_sub(&c->handles, 1) != 1) {
        return;
    }

    if (c->drop != NULL) {
        for (int64_t i = 0; i < c->len; i++) {
            c->drop(slot(c, c->head + i));
        }
    }

    sn_qlock_free(&c->mu);
    if (c->buf != NULL) {
        sn_free(c->buf);
    }

    sn_free(c);
}

// try_send sends v on c without waiting, the caller holding c's lock:
// straight to a waiting receiver, or into the ring when it has room.
// It reports whether it could.
static bool try_send(sn_chan *c, const void *v) {
    sn_waiter *w = sn_dequeue(&c->recvq);
    if (w != NULL) {
        memcpy(w->elem, v, c->size);
        sn_finish_wait(w, true);
        return true;
    }

    if (c->len < c->cap) {
        memcpy(slot(c, c->head + c->len), v, c->size);
        c->len++;
        return true;
    }

    return false;
}

// try_recv receives from c without waiting, the caller holding c's
// lock: from a waiting sender, which on a buffered channel means the
// ring is full and the sender's value goes in behind the one taken, or
// from the ring. It reports whether it could.
static bool try_recv(sn_chan *c, void *out) {
    sn_waiter *w = sn_dequeue(&c->sendq);
    if (w != NULL) {
        if (c->cap == 0) {
            memcpy(out, w->elem, c->size);
        } else {
            memcpy(out, slot(c, c->head), c->size);
            memcpy(slot(c, c->head), w->elem, c->size); // the full ring's oldest place becomes its newest
            c->head = (c->head + 1) % c->cap;
        }

        sn_finish_wait(w, true);
        return true;
    }

    if (c->len > 0) {
        memcpy(out, slot(c, c->head), c->size);
        c->head = (c->head + 1) % c->cap;
        c->len--;
        return true;
    }

    return false;
}

void sn_chan_send(sn_chan *c, const void *v) {
    sn_qlock_take(&c->mu);
    if (c->closed) {
        sn_qlock_give(&c->mu);
        sn_panic(SN_STR("send on a closed channel", 24));
    }

    if (try_send(c, v)) {
        sn_qlock_give(&c->mu);
        return;
    }

    sn_wait_state ws = {0};
    sn_waiter me = {sn_self(), (void *)v, &ws, 0, false, false, NULL, NULL};
    sn_enqueue(&c->sendq, &me);
    sn_qlock_give(&c->mu);
    sn_await_wait(&ws);
    sn_qlock_take(&c->mu); // the operation that took the wait is done with it
    sn_qlock_give(&c->mu);
    if (!me.success) {
        sn_panic(SN_STR("channel closed while a send waited on it", 40)); // no receiver need ever come: a hang otherwise
    }
}

bool sn_chan_recv(sn_chan *c, void *out) {
    sn_qlock_take(&c->mu);
    if (try_recv(c, out)) {
        sn_qlock_give(&c->mu);
        return true;
    }

    if (c->closed) {
        sn_qlock_give(&c->mu);
        return false;
    }

    sn_wait_state ws = {0};
    sn_waiter me = {sn_self(), out, &ws, 0, false, false, NULL, NULL};
    sn_enqueue(&c->recvq, &me);
    sn_qlock_give(&c->mu);
    sn_await_wait(&ws);
    sn_qlock_take(&c->mu);
    sn_qlock_give(&c->mu);
    return me.success;
}

// sn_chan_offer puts v in a buffered channel that has room, or hands
// it to a waiting receiver, and drops it otherwise. It never waits, so
// a thread that is not a task may call it: the timer does, for After
// and Tick, whose channels only it sends on. An unbuffered channel has
// no room to offer into, and a value offered to one would be lost
// without a word: that is a fault of the runtime's own, not a full
// channel, and it stops the program as the scheduler's fatal errors do,
// since the thread offering may be no task. It reports whether anyone
// but the caller holds a handle: when none does, no one can ever
// receive, the value is not put, and the timer stops.
bool sn_chan_offer(sn_chan *c, const void *v) {
    if (c->cap == 0) {
        fflush(stdout);
        fputs("fatal error: offer on an unbuffered channel\n", stderr);
        exit(2);
    }

    if (sn_chan_alone(c)) {
        return false; // only the caller's: no handle can be cloned from it either
    }

    sn_qlock_take(&c->mu);
    if (!c->closed) {
        (void)try_send(c, v);
    }

    sn_qlock_give(&c->mu);
    return true;
}

void sn_chan_close(sn_chan *c) {
    sn_qlock_take(&c->mu);
    if (c->closed) {
        sn_qlock_give(&c->mu);
        sn_panic(SN_STR("close of a closed channel", 25));
    }

    c->closed = true;
    for (sn_waiter *w; (w = sn_dequeue(&c->recvq)) != NULL;) {
        sn_finish_wait(w, false);
    }

    for (sn_waiter *w; (w = sn_dequeue(&c->sendq)) != NULL;) {
        sn_finish_wait(w, false);
    }

    sn_qlock_give(&c->mu);
}

int64_t sn_chan_len(sn_chan *c) {
    sn_qlock_take(&c->mu);
    int64_t n = c->len;
    sn_qlock_give(&c->mu);
    return n;
}

int64_t sn_chan_cap(sn_chan *c) {
    return c->cap;
}

// channels_of lists the distinct channels of the cases in increasing
// address order, the order they are locked in, so that two selects
// over the same channels cannot deadlock. Returns the count.
static int channels_of(const sn_case *cases, int n, sn_chan **chans) {
    int m = 0;
    for (int i = 0; i < n; i++) {
        sn_chan *c = cases[i].chan;
        int j = 0;
        while (j < m && chans[j] < c) {
            j++;
        }

        if (j < m && chans[j] == c) {
            continue;
        }

        memmove(&chans[j + 1], &chans[j], (size_t)(m - j) * sizeof *chans);
        chans[j] = c;
        m++;
    }

    return m;
}

static void lock_all(sn_chan **chans, int m) {
    for (int i = 0; i < m; i++) {
        sn_qlock_take(&chans[i]->mu);
    }
}

static void unlock_all(sn_chan **chans, int m) {
    for (int i = 0; i < m; i++) {
        sn_qlock_give(&chans[i]->mu);
    }
}

// sn_select is Go's selectgo: the cases are tried in a random order,
// each without waiting, and the first that can proceed does, so each
// of several that can is as likely; with none, a default returns -1,
// and otherwise the select waits on every case's channel at once until
// an operation takes one of its waiters.
int sn_select(sn_case *cases, int n, bool has_default) {
    sn_chan *chans[n > 0 ? n : 1];
    int order[n > 0 ? n : 1];
    sn_waiter waiters[n > 0 ? n : 1];
    int m = channels_of(cases, n, chans);
    for (int i = 0; i < n; i++) { // a uniform permutation, built from the front
        int j = (int)(sn_next_random() % (uint64_t)(i + 1));
        order[i] = order[j];
        order[j] = i;
    }

    lock_all(chans, m);
    for (int o = 0; o < n; o++) {
        int i = order[o];
        sn_case *k = &cases[i];
        sn_chan *c = k->chan;
        if (k->send) {
            if (c->closed) {
                unlock_all(chans, m);
                sn_panic(SN_STR("send on a closed channel", 24));
            }

            if (try_send(c, k->value)) {
                unlock_all(chans, m);
                return i;
            }
        } else if (try_recv(c, k->value)) {
            k->ok = true;
            unlock_all(chans, m);
            return i;
        } else if (c->closed) { // closed and empty: the case proceeds with nothing
            k->ok = false;
            unlock_all(chans, m);
            return i;
        }
    }

    if (has_default) {
        unlock_all(chans, m);
        return -1;
    }

    sn_wait_state ws = {0};
    sn_task *self = sn_self();
    for (int i = 0; i < n; i++) {
        sn_chan *c = cases[i].chan;
        waiters[i] = (sn_waiter){self, cases[i].value, &ws, i, false, false, NULL, NULL};
        sn_enqueue(cases[i].send ? &c->sendq : &c->recvq, &waiters[i]);
    }

    unlock_all(chans, m);
    sn_await_wait(&ws);
    lock_all(chans, m);
    for (int i = 0; i < n; i++) {
        sn_chan *c = cases[i].chan;
        sn_remove_waiter(cases[i].send ? &c->sendq : &c->recvq, &waiters[i]);
    }

    unlock_all(chans, m);
    int i = ws.fired;
    if (cases[i].send) {
        if (!waiters[i].success) {
            sn_panic(SN_STR("send on a closed channel", 24));
        }
    } else {
        cases[i].ok = waiters[i].success;
    }

    return i;
}
