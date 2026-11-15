// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Channels. The tasks themselves are sched.c's; a channel is a locked
// ring for a buffered channel, and two queues of the tasks waiting on
// it, the senders and the receivers.

#define _POSIX_C_SOURCE 200809L
#define _DARWIN_C_SOURCE

#include "internal.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

// A select of up to select_inline cases keeps its working arrays in its
// own frame, of a size known before it runs; one of more takes them from
// the system, once, for the select.
enum { select_inline = 8 };

// A handle of each direction weighs one in its half of the count word:
// the sending handles in the low half, the receiving ones in the high
// half, which begins at bit receiver_shift.
enum { receiver_shift = 32 };
static const int64_t one_sender = 1, one_receiver = (int64_t)1 << receiver_shift,
                     senders_mask = ((int64_t)1 << receiver_shift) - 1;

// The channel counts its handles of each direction in one word, the
// sending handles in its low half and the receiving ones in its high
// half, so that a clone or a drop of a handle of both ends is one
// atomic add, as it was with one count; each half holds up to 2^31
// handles, and a debug build stops a program that gives up a handle of
// an end already gone, since the low half would borrow from the high
// one. The last sending handle to go ends the channel: the receivers
// waiting wake with nothing, and a receive yields nothing once the ring
// is empty. The last receiving handle to go frees the senders: one
// waiting wakes and drops its value, and a send from then on drops its
// value at once. The last of either kind frees the channel, dropping
// the values still in the ring with the element's drop the compiler
// gave, NULL for one that owns nothing. No task can be waiting on it
// then, since a waiting task holds a handle, and nothing else can reach
// it. The last handle of each kind has work to do in the channel as it
// goes, and the two may go at once in two tasks, so the channel is freed
// by the second of them once its work is done, which half_gone tells:
// the first to finish leaves it set and the second finds it.
//
// The counts a handle's clone and drop change are a cache line away
// from what a send and a receive change, the lock and the ring, so
// that a task cloning a handle as tasks on other cores send does not
// take the line from under them: one spawn a task, each given a handle
// of one channel it sends to, made a spawn 8% slower when they shared
// it. A channel is allocated with no more than the allocator's
// alignment, so the two are kept apart by distance, a line of padding.
struct sn_chan {
    _Atomic int64_t handles;
    _Atomic bool half_gone; // the last handle of one kind has gone and done its work: the other kind's last frees
    _Atomic int64_t timer;  // the channel's After or Tick while it is in a timing wheel, -1 when it has none there
    _Atomic int32_t home;   // the wheel its timers go in, as timer.c records it
    char apart[SN_CACHE_LINE];
    sn_qlock mu;
    void (*drop)(void *); // drops one element
    size_t size;          // of one element
    int64_t cap;          // zero for an unbuffered channel, which has no ring
    uint8_t *buf;         // the ring of a buffered one
    int64_t head;         // the oldest element of the ring
    int64_t len;          // elements in the ring
    bool ended;           // the last sending handle is gone, set under the lock
    bool receiverless;    // the last receiving handle is gone, set under the lock: what a send reads, so that it never
                          // reads the counts, which every clone and drop write
    const char *made;     // where the program made it, for the deadlock report
    sn_waitq sendq;       // senders waiting: the ring is full, or there is none
    sn_waitq recvq;       // receivers waiting: the ring is empty
};

// weight is what a handle of direction dir adds to the count word.
static int64_t weight(int dir) {
    return ((dir & SN_CHAN_SEND) != 0 ? one_sender : 0) + ((dir & SN_CHAN_RECV) != 0 ? one_receiver : 0);
}

static int64_t senders(int64_t handles) { return handles & senders_mask; }

static int64_t receivers(int64_t handles) { return handles >> receiver_shift; }

// slot is the ring's place i, counted from the ring's start, for i
// below twice the capacity: the oldest element is at head, below the
// capacity, and the newest at head + len - 1, so no place is further.
static uint8_t *slot(sn_chan *c, int64_t i) {
    if (i >= c->cap) {
        i -= c->cap;
    }

    return c->buf + (size_t)i * c->size;
}

// shift moves the ring's oldest place on by one.
static void shift(sn_chan *c) { c->head = c->head + 1 == c->cap ? 0 : c->head + 1; }

// sn_chan_make makes a channel with its first handle. Its memory is
// sn_alloc's, so a debug build reports a channel no handle freed.
sn_chan *sn_chan_make(size_t size, int64_t cap, void (*drop)(void *), const char *made) {
    sn_chan *c = sn_alloc(sizeof *c);
    memset(c, 0, sizeof *c);
    c->cap = cap;
    if (cap > 0) {
        c->buf = sn_alloc((size == 0 ? 1 : size) * (size_t)cap);
    }

    sn_qlock_init(&c->mu);
    atomic_init(&c->handles, weight(SN_CHAN_BOTH));
    atomic_init(&c->timer, -1);
    c->drop = drop;
    c->size = size;
    c->made = made;
    return c;
}

// chan_drop_value drops one value of c's elements with the drop the
// compiler gave. It is the one function that calls that drop, and is
// never inlined, so that the analysis of the linked program finds that
// call here by name and takes the drops the backend writes for channels
// for its targets.
static __attribute__((noinline)) void chan_drop_value(sn_chan *c, void *v) { c->drop(v); }

// drop_value drops a value a send could not deliver, in the sending
// task: an element that owns nothing has no drop and needs none.
static void drop_value(sn_chan *c, void *v) {
    if (c->drop != NULL) {
        chan_drop_value(c, v);
    }
}

// gone checks, in a debug build, that a handle of direction dir given
// up came off a count that held one: the low half would otherwise
// borrow from the high one, and both counts would be wrong from then
// on, which only a compiler writing the wrong direction can cause.
static void gone(int64_t before, int dir) {
#ifdef SN_DEBUG
    if (((dir & SN_CHAN_SEND) != 0 && senders(before) == 0) || ((dir & SN_CHAN_RECV) != 0 && receivers(before) == 0)) {
        fflush(stdout);
        fputs("fatal error: a channel handle was given up twice\n", stderr);
        exit(2);
    }
#else
    (void)before;
    (void)dir;
#endif
}

// give_up takes a handle of direction dir off the count and returns the
// count as it was before, checked in a debug build.
static int64_t give_up(sn_chan *c, int dir) {
    int64_t before = atomic_fetch_sub(&c->handles, weight(dir));
    gone(before, dir);
    return before;
}

// chan_free frees the channel and the values still in its ring, once
// no handle is left and no task is in it.
static void chan_free(sn_chan *c) {
    if (c->drop != NULL) {
        for (int64_t i = 0; i < c->len; i++) {
            chan_drop_value(c, slot(c, c->head + i));
        }
    }

    sn_qlock_free(&c->mu);
    if (c->buf != NULL) {
        sn_free(c->buf);
    }

    sn_free(c);
}

// sn_chan_given finishes the giving up of a handle of direction dir,
// the count having been before: a handle that was not the last of its
// kind asks nothing. The last sender ends the channel and wakes the
// receivers waiting with nothing; the last receiver marks the channel
// receiverless and wakes the senders waiting, each dropping its value.
// Both happen under the lock, after the count came down, so a sender
// that reads the flag under the lock either sees the receivers gone or
// is on the queue when they are woken. The channel is freed by the
// second kind to go, once its work is done: the first to finish sets
// half_gone and the second finds it set, so that neither touches the
// channel after the other has freed it, whichever way the two drops
// interleave. A handle of both ends that was the last of both frees
// the channel at once, since no other was ever the last of either, so
// no task is in it.
void sn_chan_given(sn_chan *c, int dir, int64_t before) {
    int64_t after = before - weight(dir);
    bool last_sender = senders(before) != 0 && senders(after) == 0;
    bool last_receiver = receivers(before) != 0 && receivers(after) == 0;
    if (!last_sender && !last_receiver) {
        return;
    }

    if (last_sender && last_receiver) {
        chan_free(c);
        return;
    }

    sn_qlock_take(&c->mu);
    sn_waitq *waiting = &c->sendq;
    if (last_sender) {
        c->ended = true;
        waiting = &c->recvq;
    } else {
        c->receiverless = true;
    }

    for (sn_waiter *w; (w = sn_dequeue(waiting)) != NULL;) { // under the lock: a channel ends once
        sn_queue_claimed(sn_finish_wait(w, false));
    }

    sn_qlock_give(&c->mu);
    if (atomic_exchange(&c->half_gone, true)) {
        chan_free(c); // the other kind went first and has finished
    }
}

// sn_chan_release takes a handle of direction dir away, for
// sn_timer_abandon under the timers' lock, and reports whether the one
// left is the timer's, a sending handle; the count before is the
// caller's, which finishes the giving up with sn_chan_given once it
// has let the timers' lock go.
bool sn_chan_release(sn_chan *c, int dir, int64_t *before) {
    *before = give_up(c, dir);
    return *before - weight(dir) == one_sender;
}

// sn_chan_timer and sn_chan_placeTimer read and set the channel's timer
// while it is in a timing wheel, which timer.c keeps under the wheel's
// lock; -1 is none.
int64_t sn_chan_timer(sn_chan *c) { return atomic_load(&c->timer); }

_Atomic int32_t *sn_chan_home(sn_chan *c) { return &c->home; }

void sn_chan_placeTimer(sn_chan *c, int64_t at) { atomic_store(&c->timer, at); }

// sn_chan_open reports whether a receiving handle remains: whether a
// send would deliver its value, or drop it.
bool sn_chan_open(sn_chan *c) { return receivers(atomic_load(&c->handles)) > 0; }

// sn_chan_clone gives another handle on c of direction dir, for another
// task.
sn_chan *sn_chan_clone(sn_chan *c, int dir) {
    atomic_fetch_add(&c->handles, weight(dir));
    return c;
}

// sn_chan_narrow makes a handle of both ends one of the end kept, giving
// the other up as a drop of a handle of that end would: a chan T moved
// where a <-chan T or a chan<- T is wanted. The end kept holds the
// channel, so the giving up never frees it.
sn_chan *sn_chan_narrow(sn_chan *c, int kept) {
    int given = SN_CHAN_BOTH & ~kept;
    sn_chan_given(c, given, give_up(c, given));
    return c;
}

// sn_chan_drop drops the handle at *c, leaving the place empty, as a
// move does; the last handle frees the channel and what it holds. When
// the one left is a pending timer's, which only a timer still in the
// wheel holds, no one can receive from the channel again, and the timer
// is taken out and its handle dropped at once, so a select that took
// another case leaves no After behind it until its deadline. That is
// decided in sn_timer_abandon, under the lock of the timer's wheel,
// since a channel is never touched after its handle is given up: the
// other holder may drop the last and free it the moment the count comes
// down, and a timer firing once read the timer of a channel freed and
// reused.
void sn_chan_drop(sn_chan **h, int dir) {
    sn_chan *c = *h;
    *h = NULL;
    if (c == NULL) {
        return;
    }

    if (atomic_load(&c->timer) >= 0 && sn_timer_abandon(c, dir)) {
        return;
    }

    sn_chan_drop_untimed(c, dir);
}

// sn_chan_drop_untimed takes one handle of c of direction dir away,
// freeing the channel with the last of either kind, for a channel with
// no timer in a wheel: the timer's own handle, which sn_timer_abandon
// drops once it has taken the timer out, or a handle sn_chan_drop has
// found no timer for.
void sn_chan_drop_untimed(sn_chan *c, int dir) { sn_chan_given(c, dir, give_up(c, dir)); }

// wait_on parks the task t on c in direction dir until the operation
// that takes its waiter wakes it, recording what it waits on for the
// deadlock report while it does.
static void wait_on(sn_task *t, sn_chan *c, int dir, sn_wait_state *ws) {
    sn_task_waits(t, c, dir);
    sn_await_wait(ws);
    sn_task_waits(t, NULL, 0);
}

// try_send sends v on c without waiting, the caller holding c's lock:
// straight to a waiting receiver, whose task it leaves in *woken for
// the caller to queue once it has let the lock go, or into the ring
// when it has room. It reports whether it could.
static bool try_send(sn_chan *c, const void *v, sn_task **woken) {
    *woken = NULL;
    sn_waiter *w = sn_dequeue(&c->recvq);
    if (w != NULL) {
        memcpy(w->elem, v, c->size);
        *woken = sn_finish_wait(w, true);
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
// from the ring. The sender's task is left in *woken for the caller to
// queue once it has let the lock go. It reports whether it could.
static bool try_recv(sn_chan *c, void *out, sn_task **woken) {
    *woken = NULL;
    sn_waiter *w = sn_dequeue(&c->sendq);
    if (w != NULL) {
        if (c->cap == 0) {
            memcpy(out, w->elem, c->size);
        } else {
            memcpy(out, slot(c, c->head), c->size);
            memcpy(slot(c, c->head), w->elem, c->size); // the full ring's oldest place becomes its newest
            shift(c);
        }

        *woken = sn_finish_wait(w, true);
        return true;
    }

    if (c->len > 0) {
        memcpy(out, slot(c, c->head), c->size);
        shift(c);
        c->len--;
        return true;
    }

    return false;
}

// sn_chan_send sends v on c: to a waiting receiver, into the ring, or
// after a wait for either; when no receiving handle remains, now or by
// the time the wait ends, the value is dropped here, in the sending
// task, since no one can ever take it.
void sn_chan_send(sn_chan *c, const void *v) {
    sn_qlock_take(&c->mu);
    if (c->receiverless) {
        sn_qlock_give(&c->mu);
        drop_value(c, (void *)v);
        return;
    }

    sn_task *woken;
    if (try_send(c, v, &woken)) {
        sn_qlock_give(&c->mu);
        sn_queue_claimed(woken);
        return;
    }

    sn_wait_state ws = { 0 };
    sn_waiter me = { sn_self(), (void *)v, &ws, 0, false, false, NULL, NULL };
    sn_enqueue(&c->sendq, &me);
    sn_qlock_give(&c->mu);
    wait_on(me.t, c, SN_CHAN_SEND, &ws);
    if (!me.success) {
        drop_value(c, (void *)v); // the last receiver went while the send waited
    }
}

// sn_chan_recv receives from c into out and reports whether a value
// came: false once the channel has ended, its last sending handle gone,
// and the ring is empty.
bool sn_chan_recv(sn_chan *c, void *out) {
    sn_qlock_take(&c->mu);
    sn_task *woken;
    if (try_recv(c, out, &woken)) {
        sn_qlock_give(&c->mu);
        sn_queue_claimed(woken);
        return true;
    }

    if (c->ended) {
        sn_qlock_give(&c->mu);
        return false;
    }

    sn_wait_state ws = { 0 };
    sn_waiter me = { sn_self(), out, &ws, 0, false, false, NULL, NULL };
    sn_enqueue(&c->recvq, &me);
    sn_qlock_give(&c->mu);
    wait_on(me.t, c, SN_CHAN_RECV, &ws);
    return me.success;
}

// sn_chan_offer puts v in a buffered channel that has room, or hands
// it to a waiting receiver, and drops it otherwise. It never waits, so
// a thread that is not a task may call it: the timer does, for After
// and Tick, whose channels only it sends on. An unbuffered channel has
// no room to offer into, and a value offered to one would be lost
// without a word: that is a fault of the runtime's own, not a full
// channel, and it stops the program as the scheduler's fatal errors do,
// since the thread offering may be no task. It reports whether a
// receiving handle remains: when none does, no one can ever receive,
// the value is not put, and the timer stops.
bool sn_chan_offer(sn_chan *c, const void *v) {
    if (c->cap == 0) {
        fflush(stdout);
        fputs("fatal error: offer on an unbuffered channel\n", stderr);
        exit(2);
    }

    if (!sn_chan_open(c)) {
        return false;
    }

    sn_qlock_take(&c->mu);
    sn_task *woken;
    (void)try_send(c, v, &woken);
    sn_qlock_give(&c->mu);
    sn_queue_claimed(woken);
    return true;
}

// sn_chan_poll takes a value c holds into out, without waiting, and
// reports whether there was one: a stopped Timer takes back so the time
// it sent and no one received.
bool sn_chan_poll(sn_chan *c, void *out) {
    sn_qlock_take(&c->mu);
    sn_task *woken;
    bool got = try_recv(c, out, &woken);
    sn_qlock_give(&c->mu);
    sn_queue_claimed(woken);
    return got;
}

// sn_chan_report writes to the standard error stream what a task that
// waits on c, in direction dir, waits for: where the channel was made,
// the handles of each kind that remain, and the tasks waiting to send
// and to receive on it. It is called with every task asleep, so the
// counts are read as they are.
void sn_chan_report(sn_chan *c, int dir) {
    int64_t handles = atomic_load(&c->handles);
    int64_t sending = 0, receiving = 0;
    for (sn_waiter *w = c->sendq.head; w != NULL; w = w->next) {
        sending++;
    }

    for (sn_waiter *w = c->recvq.head; w != NULL; w = w->next) {
        receiving++;
    }

    const char *how = dir == SN_CHAN_SEND   ? "waits to send"
                      : dir == SN_CHAN_RECV ? "waits to receive"
                                            : "waits in a select whose first case is";
    fprintf(stderr,
            "  the main task %s on the channel made at %s, which %lld sending and %lld receiving handles "
            "remain on, with %lld tasks waiting to send and %lld to receive\n",
            how, c->made != NULL ? c->made : "?", (long long)senders(handles), (long long)receivers(handles),
            (long long)sending, (long long)receiving);
}

int64_t sn_chan_len(sn_chan *c) {
    sn_qlock_take(&c->mu);
    int64_t n = c->len;
    sn_qlock_give(&c->mu);
    return n;
}

int64_t sn_chan_cap(sn_chan *c) { return c->cap; }

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

// sn_select tries the cases in a random order, each without waiting,
// and the first that can proceed does, so each of several that can is
// as likely; with none, a default returns -1,
// and otherwise the select waits on every case's channel at once until
// an operation takes one of its waiters.
static int select_in(sn_case *cases, int n, bool has_default, sn_chan **chans, int *order, sn_waiter *waiters);

int sn_select(sn_case *cases, int n, bool has_default) {
    if (n <= select_inline) {
        sn_chan *chans[select_inline];
        int order[select_inline];
        sn_waiter waiters[select_inline];
        return select_in(cases, n, has_default, chans, order, waiters);
    }

    sn_chan **chans = malloc((size_t)n * sizeof *chans); // the system's: working space no program value holds
    int *order = malloc((size_t)n * sizeof *order);
    sn_waiter *waiters = malloc((size_t)n * sizeof *waiters);
    if (chans == NULL || order == NULL || waiters == NULL) {
        sn_panic(SN_STR("out of memory", 13));
    }

    int i = select_in(cases, n, has_default, chans, order, waiters);
    free(chans);
    free(order);
    free(waiters);
    return i;
}

// select_in is sn_select over working arrays of n places each.
static int select_in(sn_case *cases, int n, bool has_default, sn_chan **chans, int *order, sn_waiter *waiters) {
    int m = channels_of(cases, n, chans);
    for (int i = 0; i < n; i++) { // a uniform permutation, built from the front
        int j = (int)(sn_next_random() % (uint64_t)(i + 1));
        order[i] = order[j];
        order[j] = i;
    }

    lock_all(chans, m);
    sn_task *woken;
    for (int o = 0; o < n; o++) {
        int i = order[o];
        sn_case *k = &cases[i];
        sn_chan *c = k->chan;
        if (k->send) {
            if (c->receiverless) { // no one can ever take the value: the case proceeds by dropping it
                unlock_all(chans, m);
                drop_value(c, k->value);
                return i;
            }

            if (try_send(c, k->value, &woken)) {
                unlock_all(chans, m);
                sn_queue_claimed(woken);
                return i;
            }
        } else if (try_recv(c, k->value, &woken)) {
            k->ok = true;
            unlock_all(chans, m);
            sn_queue_claimed(woken);
            return i;
        } else if (c->ended) { // ended and empty: the case proceeds with nothing
            k->ok = false;
            unlock_all(chans, m);
            return i;
        }
    }

    if (has_default) {
        unlock_all(chans, m);
        return -1;
    }

    sn_wait_state ws = { 0 };
    sn_task *self = sn_self();
    for (int i = 0; i < n; i++) {
        sn_chan *c = cases[i].chan;
        waiters[i] = (sn_waiter){ self, cases[i].value, &ws, i, false, false, NULL, NULL };
        sn_enqueue(cases[i].send ? &c->sendq : &c->recvq, &waiters[i]);
    }

    unlock_all(chans, m);
    wait_on(self, cases[0].chan, SN_CHAN_BOTH, &ws); // a select: the first case's channel names it
    lock_all(chans, m);
    for (int i = 0; i < n; i++) {
        sn_chan *c = cases[i].chan;
        sn_remove_waiter(cases[i].send ? &c->sendq : &c->recvq, &waiters[i]);
    }

    unlock_all(chans, m);
    int i = ws.fired;
    if (cases[i].send) {
        if (!waiters[i].success) {
            drop_value(cases[i].chan, cases[i].value); // the last receiver went while the send waited
        }
    } else {
        cases[i].ok = waiters[i].success;
    }

    return i;
}
