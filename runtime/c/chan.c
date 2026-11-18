// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Channels. The tasks themselves are sched.c's. A buffered channel is a
// ring its senders and receivers move through with one compare-and-swap
// each and no lock, and two queues, under a lock, of the tasks waiting
// for it to have room or a value; an unbuffered channel is the two
// queues alone, a sender and a receiver meeting under the lock.

#define _POSIX_C_SOURCE 200809L
#define _DARWIN_C_SOURCE

#include "internal.h"

#include <sched.h>
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

// A thread in the ring that finds another between its two steps pauses
// before it looks again, twice as long each time up to 1 << spin_most
// pauses, and yields its processor after spin_yields such looks.
enum { spin_most = 6, spin_yields = 10 };

// A lap of the ring is lap_least or the smallest power of two the
// capacity fits, whichever is more: a lap of one would leave a place
// written and a place read one lap later with the same stamp. A place's
// stamp is stamp_bytes wide, the low half of a position: a place is
// never more than a lap and the capacity behind the positions, so the
// half tells its state, and every step a stamp allows is confirmed by a
// compare-and-swap on the whole position.
enum { lap_least = 2, stamp_bytes = sizeof(uint32_t) };

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
// The ring of a buffered channel is a bounded queue of places, each
// with a stamp, and two positions, the tail the senders advance and the
// head the receivers advance, each position an index into the places
// and a lap count above it. A place's stamp says which lap it is at: a
// place that holds nothing has the stamp of the tail that may write it,
// one that holds a value has that tail plus one, and a receiver that
// has read it stamps it for the next lap. A sender reads the tail,
// finds its place at the tail's stamp, moves the tail on with one
// compare-and-swap, writes its value and then the stamp, which is what
// a receiver waits for; a receiver reads the head, finds its place one
// past the head's stamp, moves the head on, reads the value and then
// stamps the place for the next lap, which is what a sender waits for.
// A sender whose place is a lap behind reads the head to learn whether
// the ring is full, and a receiver whose place is at its own stamp reads
// the tail to learn whether it is empty. Nothing is held across the
// steps, so a sender on one core costs the others no wait, and a value
// goes through the ring and never from a sender's frame to a receiver's.
// The tail and the head are a cache line apart, from each other and
// from the places, in a block of their own that only a buffered channel
// has, and the counts the handles' clones and drops change are a line
// from what every operation reads, so that the cores moving one do not
// take the others' lines from under them: one spawn a task, each given
// a handle of one channel it sends to, made a spawn 8% slower when the
// counts shared a line with what a send writes. A block is allocated
// with no more than the allocator's alignment, so the lines are kept
// apart by distance, a line of padding each.
//
// The tasks waiting for room or for a value are on the two queues,
// under the lock, each queue with a word that says whether anyone is on
// it, so that a sender or a receiver that finds no one waiting takes no
// lock. A task registers on a queue under the lock and tries the ring
// once more before it parks, and an operation that moved the ring reads
// the word after its move, so that one of the two sees the other: the
// task finds the value or the room, or the operation finds the task and
// wakes it. A woken task tries the ring again, since another may have
// taken what it was woken for, and registers again if it must.
typedef struct {
    _Atomic uint64_t tail; // the position the next sender takes, index and lap
    char apart_tail[SN_CACHE_LINE];
    _Atomic uint64_t head; // the position the next receiver takes
    char apart_head[SN_CACHE_LINE];
    uint8_t places[]; // the capacity's places, each its stamp and then its element
} sn_ring;

struct sn_chan {
    _Atomic int64_t handles;
    _Atomic bool half_gone; // the last handle of one kind has gone and done its work: the other kind's last frees
    bool handle;           // a task's handle: its receive is not cancelled, and word says where the task's own scope is
    _Atomic bool failed;   // for a task's handle, the task failed: scope.c's
    _Atomic int64_t timer; // the channel's After or Tick while it is in a timing wheel, -1 when it has none there
    _Atomic int32_t home;  // the wheel its timers go in, as timer.c records it
    _Atomic uintptr_t word;                                          // for a task's handle, scope.c's
    char apart[SN_CACHE_LINE - sizeof(uintptr_t) - sizeof(int32_t)]; // the line's spacer, less the word and its
                                                                     // alignment: the fields after it keep their places
    sn_qlock mu;                 // over the queues, and over an unbuffered channel's meetings
    void (*drop)(void *);        // drops one element
    size_t size;                 // of one element
    size_t stride;               // of one place of the ring: its stamp and the element, in whole stamps
    int64_t cap;                 // zero for an unbuffered channel, which has no ring
    uint64_t lap;                // what a position gains in a lap of the ring, a power of two above its index
    sn_ring *ring;               // a buffered channel's ring, NULL for an unbuffered one
    _Atomic bool ended;          // the last sending handle is gone
    _Atomic bool receiverless;   // the last receiving handle is gone: what a send reads, so that it never reads the
                                 // counts, which every clone and drop write
    _Atomic bool senders_wait;   // sendq has a waiter, written under the lock, read before taking it
    _Atomic bool receivers_wait; // recvq has a waiter, likewise
    const char *made;            // where the program made it, for the deadlock report
    sn_waitq sendq;              // senders waiting: the ring is full, or there is none
    sn_waitq recvq;              // receivers waiting: the ring is empty
};

// weight is what a handle of direction dir adds to the count word.
static int64_t weight(int dir) {
    return ((dir & SN_CHAN_SEND) != 0 ? one_sender : 0) + ((dir & SN_CHAN_RECV) != 0 ? one_receiver : 0);
}

static int64_t senders(int64_t handles) { return handles & senders_mask; }

static int64_t receivers(int64_t handles) { return handles >> receiver_shift; }

// The ring's positions: index_of is a position's place in the ring,
// lap_of its lap, and next_position the position one on, into the next lap past
// the last place. stamp_of is a place's stamp word and value_of its
// element.
static uint64_t index_of(const sn_chan *c, uint64_t pos) { return pos & (c->lap - 1); }

static uint64_t lap_of(const sn_chan *c, uint64_t pos) { return pos & ~(c->lap - 1); }

static uint64_t next_position(const sn_chan *c, uint64_t pos) {
    return index_of(c, pos) + 1 < (uint64_t)c->cap ? pos + 1 : lap_of(c, pos) + c->lap;
}

static _Atomic uint32_t *stamp_of(sn_chan *c, uint64_t index) {
    return (_Atomic uint32_t *)(c->ring->places + index * c->stride);
}

static uint8_t *value_of(sn_chan *c, uint64_t index) { return c->ring->places + index * c->stride + stamp_bytes; }

// ring_spin pauses a thread that found another between its two steps in the
// ring, longer each time, and yields its processor once it has waited
// spin_yields times.
static void ring_spin(unsigned *step) {
    if (*step < spin_yields) {
        for (unsigned i = 0; i < 1u << (*step < spin_most ? *step : spin_most); i++) {
            sn_spin_hint();
        }
    } else {
        sched_yield();
    }

    (*step)++;
}

// ring_push puts v in the ring without waiting, and reports whether it could:
// false when the ring is full.
static bool ring_push(sn_chan *c, const void *v) {
    unsigned step = 0;
    sn_ring *r = c->ring;
    uint64_t tail = atomic_load_explicit(&r->tail, memory_order_relaxed);
    for (;;) {
        uint64_t index = index_of(c, tail);
        uint32_t stamp = atomic_load_explicit(stamp_of(c, index), memory_order_acquire);
        if (stamp == (uint32_t)tail) { // the place is free for this lap
            if (atomic_compare_exchange_weak_explicit(&r->tail, &tail, next_position(c, tail), memory_order_seq_cst,
                                                      memory_order_relaxed)) {
                memcpy(value_of(c, index), v, c->size);
                atomic_store_explicit(stamp_of(c, index), (uint32_t)(tail + 1), memory_order_release);
                return true;
            }

            ring_spin(&step); // another sender took the place: tail holds its new value
        } else if (stamp + (uint32_t)c->lap == (uint32_t)(tail + 1)) { // the place holds a value of the lap before:
            atomic_thread_fence(memory_order_seq_cst);                 // full, unless read since
            uint64_t head = atomic_load_explicit(&r->head, memory_order_relaxed);
            if (head + c->lap == tail) {
                return false;
            }

            ring_spin(&step);
            tail = atomic_load_explicit(&r->tail, memory_order_relaxed);
        } else { // a receiver is between its step and its stamp
            ring_spin(&step);
            tail = atomic_load_explicit(&r->tail, memory_order_relaxed);
        }
    }
}

// ring_pop takes the ring's oldest value into out without waiting, and
// reports whether there was one.
static bool ring_pop(sn_chan *c, void *out) {
    unsigned step = 0;
    sn_ring *r = c->ring;
    uint64_t head = atomic_load_explicit(&r->head, memory_order_relaxed);
    for (;;) {
        uint64_t index = index_of(c, head);
        uint32_t stamp = atomic_load_explicit(stamp_of(c, index), memory_order_acquire);
        if (stamp == (uint32_t)(head + 1)) { // the place holds a value of this lap
            if (atomic_compare_exchange_weak_explicit(&r->head, &head, next_position(c, head), memory_order_seq_cst,
                                                      memory_order_relaxed)) {
                memcpy(out, value_of(c, index), c->size);
                atomic_store_explicit(stamp_of(c, index), (uint32_t)(head + c->lap), memory_order_release);
                return true;
            }

            ring_spin(&step);                 // another receiver took the place: head holds its new value
        } else if (stamp == (uint32_t)head) { // the place is free for this lap: empty, unless written since
            atomic_thread_fence(memory_order_seq_cst);
            uint64_t tail = atomic_load_explicit(&r->tail, memory_order_relaxed);
            if (tail == head) {
                return false;
            }

            ring_spin(&step);
            head = atomic_load_explicit(&r->head, memory_order_relaxed);
        } else { // a sender is between its step and its stamp
            ring_spin(&step);
            head = atomic_load_explicit(&r->head, memory_order_relaxed);
        }
    }
}

static sn_chan *chan_init(sn_chan *c, size_t size, int64_t cap, void (*drop)(void *), const char *made);

// sn_chan_make makes a channel with its first handle. Its memory is
// sn_alloc's, so a debug build reports a channel no handle freed.
sn_chan *sn_chan_make(size_t size, int64_t cap, void (*drop)(void *), const char *made) {
    size_t stride = stamp_bytes + ((size + stamp_bytes - 1) & ~(stamp_bytes - 1));
    if (cap < 0 || (uint64_t)cap > (SIZE_MAX - sizeof(sn_ring)) / stride) {
        sn_panic(SN_STR("makechan: size out of range", 27));
    }

    return chan_init(sn_alloc(sizeof(sn_chan)), size, cap, drop, made);
}

// sn_chan_handle makes the channel of one slot a spawn for a handle
// gives the task and the handle: with two handles, the handle's and the
// task's, counted at once, since no one else has it yet, so that the
// spawn makes no clone.
sn_chan *sn_chan_handle(size_t size, void (*drop)(void *), const char *made) {
    sn_chan *c = chan_init(sn_alloc(sizeof(sn_chan)), size, 1, drop, made);
    atomic_store_explicit(&c->handles, 2 * weight(SN_CHAN_BOTH), memory_order_relaxed);
    c->handle = true;
    return c;
}

// chan_init makes the block c a channel of cap places of size bytes.
static sn_chan *chan_init(sn_chan *c, size_t size, int64_t cap, void (*drop)(void *), const char *made) {
    size_t stride = stamp_bytes + ((size + stamp_bytes - 1) & ~(stamp_bytes - 1));
    memset(c, 0, sizeof *c);
    c->cap = cap;
    if (cap > 0) {
        c->lap = lap_least;
        while (c->lap < (uint64_t)cap) {
            c->lap <<= 1;
        }

        c->stride = stride;
        c->ring = sn_alloc(sizeof *c->ring + stride * (size_t)cap);
        atomic_init(&c->ring->tail, 0);
        atomic_init(&c->ring->head, 0);
        for (uint64_t i = 0; i < (uint64_t)cap; i++) {
            atomic_init(stamp_of(c, i), (uint32_t)i);
        }
    }

    atomic_init(&c->handles, weight(SN_CHAN_BOTH));
    atomic_init(&c->timer, -1); // none; home stays 0, none yet, as the timers read it
    sn_qlock_init(&c->mu);
    c->drop = drop;
    c->size = size;
    c->made = made;
    return c;
}

// chan_drop_value drops one value with the element's drop, out of line
// so that the analysis of a task's stack sees a call through a function
// pointer here and nowhere else in a channel's operations.
static __attribute__((noinline)) void chan_drop_value(sn_chan *c, void *v) { c->drop(v); }

// drop_value drops a value a send could not deliver, with the element's
// drop when the element owns something.
static void drop_value(sn_chan *c, void *v) {
    if (c->drop != NULL) {
        chan_drop_value(c, v);
    }
}

// gone stops a debug build's program that gives up a handle of an end
// of which none is left: the count would borrow from the other end's.
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

// give_up takes one handle of direction dir off the count and returns
// the count before.
static int64_t give_up(sn_chan *c, int dir) {
    int64_t before = atomic_fetch_sub(&c->handles, weight(dir));
    gone(before, dir);
    return before;
}

// chan_free frees the channel and the values still in its ring, once
// no handle is left and no task is in it.
static void chan_free(sn_chan *c) {
    if (c->drop != NULL && c->cap > 0) {
        uint64_t tail = atomic_load_explicit(&c->ring->tail, memory_order_relaxed);
        for (uint64_t pos = atomic_load_explicit(&c->ring->head, memory_order_relaxed); pos != tail;
             pos = next_position(c, pos)) {
            chan_drop_value(c, value_of(c, index_of(c, pos)));
        }
    }

    sn_qlock_free(&c->mu);
    if (c->ring != NULL) {
        sn_free(c->ring);
    }

    if (c->handle) {
        sn_scope_dropped(c);
    }

    sn_free(c);
}

// wake_all wakes every task on q with nothing, under the lock: the
// channel ends for its kind once.
static void wake_all(sn_waitq *q, _Atomic bool *waits) {
    for (sn_waiter *w; (w = sn_dequeue(q)) != NULL;) {
        sn_queue_claimed(sn_finish_wait(w, false));
    }

    atomic_store_explicit(waits, false, memory_order_seq_cst);
}

// sn_chan_given finishes the giving up of a handle of direction dir,
// the count having been before: a handle that was not the last of its
// kind asks nothing. The last sender ends the channel and wakes the
// receivers waiting with nothing; the last receiver marks the channel
// receiverless and wakes the senders waiting, each dropping its value.
// The mark is written before the lock is taken and the waiters woken,
// so that a task registering on the queue reads the mark after its
// registration, which is under the lock, and either sees the end or is
// on the queue when the waiters are woken. The channel is freed by the
// second kind to go, once its work is done: the first to finish sets
// half_gone and the second finds it set, so that neither touches the
// channel after the other has freed it, whichever way the two drops
// interleave. A handle of both ends that was the last of both frees
// the channel at once, since no other was ever the last of either, so
// no task is in it.
void sn_chan_given(sn_chan *c, int dir, int64_t before) {
    int64_t after_count = before - weight(dir);
    bool last_sender = senders(before) != 0 && senders(after_count) == 0;
    bool last_receiver = receivers(before) != 0 && receivers(after_count) == 0;
    if (!last_sender && !last_receiver) {
        return;
    }

    if (last_sender && last_receiver) {
        chan_free(c);
        return;
    }

    if (last_sender) {
        atomic_store_explicit(&c->ended, true, memory_order_seq_cst);
        sn_qlock_take(&c->mu);
        wake_all(&c->recvq, &c->receivers_wait);
    } else {
        atomic_store_explicit(&c->receiverless, true, memory_order_seq_cst);
        sn_qlock_take(&c->mu);
        wake_all(&c->sendq, &c->senders_wait);
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

_Atomic uintptr_t *sn_chan_word(sn_chan *c) { return &c->word; }

_Atomic bool *sn_chan_failed(sn_chan *c) { return &c->failed; }

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
// deadlock report while it does, and reports true; or until the task's
// scope is cancelled, when the task takes the wait back and it reports
// false, and the caller takes its waiters off their queues and does
// what the channel's end would have it do. A select names its first
// case's channel, whose kind decides for all its cases: a handle is
// never among them, since a handle's channel is the task package's own
// and no program writes a select over one.
static bool wait_on(sn_task *t, sn_chan *c, int dir, sn_wait_state *ws) {
    sn_task_waits(t, c, dir);
    bool done = true;
    if (c->handle) {
        sn_await_wait(ws); // a task's handle: Join waits for a task cancelled with it, and must not lose its result
    } else {
        done = sn_await_cancellable(t, ws);
    }

    sn_task_waits(t, NULL, 0);
    return done;
}

// notify wakes one task waiting on q, for a move of the ring that gave
// it what it waits for, a value or room, and does its operation for it
// when the ring still allows: a waiting sender's value goes into the
// ring, a waiting receiver's place is filled from it, and the task
// wakes with its operation done; when the ring has moved again
// meanwhile, the task wakes to try again. Eight senders on a full ring
// woken to push for themselves found the place taken again as often as
// not and parked again, a wake a try. An operation done for a task
// moves the ring the other way, so the other queue's first waiter is
// next, until a wake finds no one or does nothing. The word is read
// after the move, sequentially consistent with the mark a task
// registering writes before it tries the ring once more, so that one of
// the two sees the other; it is read before the lock, so that a channel
// no one waits on costs its senders and receivers no lock. The chain has
// no bound but the waiters: a task that does the work of several others
// in turn is delayed by as much, while each of them wakes with its own
// done.
static void notify(sn_chan *c, sn_waitq *q, _Atomic bool *waits) {
    for (;;) {
        if (!atomic_load_explicit(waits, memory_order_seq_cst)) {
            return;
        }

        sn_qlock_take(&c->mu);
        sn_waiter *w = sn_dequeue(q);
        if (q->head == NULL) {
            atomic_store_explicit(waits, false, memory_order_seq_cst);
        }

        bool done = false;
        sn_task *woken = NULL;
        if (w != NULL) {
            done = q == &c->sendq ? ring_push(c, w->elem) : ring_pop(c, w->elem);
            woken = sn_finish_wait(w, done);
        }

        sn_qlock_give(&c->mu);
        sn_queue_claimed(woken);
        if (!done) {
            return;
        }

        bool senders_next = q == &c->recvq;
        q = senders_next ? &c->sendq : &c->recvq;
        waits = senders_next ? &c->senders_wait : &c->receivers_wait;
    }
}

// enroll puts the waiter w of the task on q, under the lock the caller
// holds, and marks the queue waited on in its word, a ring's queues'
// alone; leave takes it off again, when the task found what it waited
// for before it parked.
static void enroll(sn_waitq *q, _Atomic bool *waits, sn_waiter *w) {
    sn_enqueue(q, w);
    if (waits != NULL) {
        atomic_store_explicit(waits, true, memory_order_seq_cst);
    }
}

static void leave(sn_waitq *q, _Atomic bool *waits, sn_waiter *w) {
    sn_remove_waiter(q, w);
    if (waits != NULL && q->head == NULL) {
        atomic_store_explicit(waits, false, memory_order_seq_cst);
    }
}

// withdraw takes the waiter w of a wait the task took back off q, and
// reports false, the receive's result: as from a channel that has
// ended. unsent does the same for a send, and drops the value, as a
// send no receiver will take. Both are out of line and cold, as
// take_back in sched.c is, so that a channel's operations compile as
// they did before a cancel could end their waits.
static __attribute__((noinline, cold)) bool withdraw(sn_chan *c, sn_waitq *q, _Atomic bool *waits, sn_waiter *w) {
    sn_qlock_take(&c->mu);
    leave(q, waits, w);
    sn_qlock_give(&c->mu);
    return false;
}

static __attribute__((noinline, cold)) void unsent(sn_chan *c, sn_waitq *q, _Atomic bool *waits, sn_waiter *w) {
    withdraw(c, q, waits, w);
    drop_value(c, w->elem);
}

// meet_receiver hands v to a receiver waiting on an unbuffered channel,
// the caller holding the lock, and reports whether one was there: the
// receiver's task goes in *woken for the caller to queue once it has
// let the lock go. meet_sender takes a waiting sender's value into out.
static bool meet_receiver(sn_chan *c, const void *v, sn_task **woken) {
    *woken = NULL;
    sn_waiter *w = sn_dequeue(&c->recvq);
    if (w == NULL) {
        return false;
    }

    memcpy(w->elem, v, c->size);
    *woken = sn_finish_wait(w, true);
    return true;
}

static bool meet_sender(sn_chan *c, void *out, sn_task **woken) {
    *woken = NULL;
    sn_waiter *w = sn_dequeue(&c->sendq);
    if (w == NULL) {
        return false;
    }

    memcpy(out, w->elem, c->size);
    *woken = sn_finish_wait(w, true);
    return true;
}

// send_unbuffered sends v on a channel with no ring: to a receiver
// waiting, or after a wait for one, which takes the value from the
// waiter; a wait the last receiver ended leaves the value with the
// sender, to drop.
static void send_unbuffered(sn_chan *c, const void *v) {
    sn_qlock_take(&c->mu);
    if (atomic_load_explicit(&c->receiverless, memory_order_relaxed)) {
        sn_qlock_give(&c->mu);
        drop_value(c, (void *)v);
        return;
    }

    sn_task *woken;
    if (meet_receiver(c, v, &woken)) {
        sn_qlock_give(&c->mu);
        sn_queue_claimed(woken);
        return;
    }

    sn_wait_state ws = { 0 };
    sn_waiter me = { sn_self(), (void *)v, &ws, 0, false, false, NULL, NULL };
    sn_enqueue(&c->sendq, &me);
    sn_qlock_give(&c->mu);
    if (!wait_on(me.t, c, SN_CHAN_SEND, &ws)) {
        unsent(c, &c->sendq, NULL, &me);
        return;
    }

    if (!me.success) {
        drop_value(c, (void *)v); // the last receiver went while the send waited
    }
}

// send_buffered sends v on a channel with a ring: into the ring, or
// after a wait for room. A sender the ring refuses registers as waiting
// under the lock and tries the ring once more before it parks, so that
// a receiver's move it did not see finds it registered; it wakes with
// the value in the ring, put there by the receiver that woke it, or to
// try again when the ring moved meanwhile.
static void send_buffered(sn_chan *c, const void *v) {
    for (;;) {
        if (atomic_load_explicit(&c->receiverless, memory_order_seq_cst)) {
            drop_value(c, (void *)v);
            return;
        }

        if (ring_push(c, v)) {
            notify(c, &c->recvq, &c->receivers_wait);
            return;
        }

        sn_wait_state ws = { 0 };
        sn_waiter me = { sn_self(), (void *)v, &ws, 0, false, false, NULL, NULL };
        sn_qlock_take(&c->mu);
        enroll(&c->sendq, &c->senders_wait, &me);
        if (ring_push(c, v)) {
            leave(&c->sendq, &c->senders_wait, &me);
            sn_qlock_give(&c->mu);
            notify(c, &c->recvq, &c->receivers_wait);
            return;
        }

        if (atomic_load_explicit(&c->receiverless, memory_order_seq_cst)) {
            leave(&c->sendq, &c->senders_wait, &me);
            sn_qlock_give(&c->mu);
            drop_value(c, (void *)v);
            return;
        }

        sn_qlock_give(&c->mu);
        if (!wait_on(me.t, c, SN_CHAN_SEND, &ws)) {
            unsent(c, &c->sendq, &c->senders_wait, &me);
            return;
        }

        if (me.success) {
            return; // the value went into the ring with the wake
        }
    }
}

// sn_chan_send sends v on c: into the ring, to a waiting receiver, or
// after a wait for either; when no receiving handle remains, now or by
// the time the wait ends, the value is dropped here, in the sending
// task, since no one can ever take it.
void sn_chan_send(sn_chan *c, const void *v) {
    if (c->cap == 0) {
        send_unbuffered(c, v);
    } else {
        send_buffered(c, v);
    }
}

// recv_unbuffered receives from a channel with no ring: a waiting
// sender's value, or after a wait for one, which puts the value in out;
// false once the channel has ended.
static bool recv_unbuffered(sn_chan *c, void *out) {
    sn_qlock_take(&c->mu);
    sn_task *woken;
    if (meet_sender(c, out, &woken)) {
        sn_qlock_give(&c->mu);
        sn_queue_claimed(woken);
        return true;
    }

    if (atomic_load_explicit(&c->ended, memory_order_relaxed)) {
        sn_qlock_give(&c->mu);
        return false;
    }

    sn_wait_state ws = { 0 };
    sn_waiter me = { sn_self(), out, &ws, 0, false, false, NULL, NULL };
    sn_enqueue(&c->recvq, &me);
    sn_qlock_give(&c->mu);
    if (!wait_on(me.t, c, SN_CHAN_RECV, &ws)) {
        return withdraw(c, &c->recvq, NULL, &me);
    }

    return me.success;
}

// drained reports whether the channel has ended and its ring is empty,
// for a receiver that found the ring empty: the ring is tried once more
// after the end is read, since a value sent before the last sender went
// is in the ring once the end is seen, and may have come after the
// receiver's look.
static bool drained(sn_chan *c, void *out, bool *got) {
    if (!atomic_load_explicit(&c->ended, memory_order_seq_cst)) {
        return false;
    }

    *got = ring_pop(c, out);
    return true;
}

// recv_buffered receives from a channel with a ring: its oldest value,
// or after a wait for one, registering and trying once more as
// send_buffered does, and waking with the value in out or to try again;
// false once the channel has ended and the ring is empty.
static bool recv_buffered(sn_chan *c, void *out) {
    bool got;
    for (;;) {
        if (ring_pop(c, out)) {
            notify(c, &c->sendq, &c->senders_wait);
            return true;
        }

        if (drained(c, out, &got)) {
            return got;
        }

        sn_wait_state ws = { 0 };
        sn_waiter me = { sn_self(), out, &ws, 0, false, false, NULL, NULL };
        sn_qlock_take(&c->mu);
        enroll(&c->recvq, &c->receivers_wait, &me);
        if (ring_pop(c, out)) {
            leave(&c->recvq, &c->receivers_wait, &me);
            sn_qlock_give(&c->mu);
            notify(c, &c->sendq, &c->senders_wait);
            return true;
        }

        if (drained(c, out, &got)) {
            leave(&c->recvq, &c->receivers_wait, &me);
            sn_qlock_give(&c->mu);
            return got;
        }

        sn_qlock_give(&c->mu);
        if (!wait_on(me.t, c, SN_CHAN_RECV, &ws)) {
            return withdraw(c, &c->recvq, &c->receivers_wait, &me);
        }

        if (me.success) {
            return true; // the value came with the wake
        }
    }
}

// sn_chan_recv receives from c into out and reports whether a value
// came: false once the channel has ended, its last sending handle gone,
// and the ring is empty.
bool sn_chan_recv(sn_chan *c, void *out) { return c->cap == 0 ? recv_unbuffered(c, out) : recv_buffered(c, out); }

// sn_chan_offer puts v in a buffered channel that has room, and drops it
// otherwise. It never waits, so a thread that is not a task may call
// it: the timer does, for After and Tick, whose channels only it sends
// on. An unbuffered channel has no room to offer into, and a value
// offered to one would be lost without a word: that is a fault of the
// runtime's own, not a full channel, and it stops the program as the
// scheduler's fatal errors do, since the thread offering may be no task.
// It reports whether a receiving handle remains: when none does, no one
// can ever receive, the value is not put, and the timer stops.
bool sn_chan_offer(sn_chan *c, const void *v) {
    if (c->cap == 0) {
        fflush(stdout);
        fputs("fatal error: offer on an unbuffered channel\n", stderr);
        exit(2);
    }

    if (c->handle) {
        sn_scope_ends(c); // the task offers its result as it ends
    }

    if (!sn_chan_open(c)) {
        return false;
    }

    if (ring_push(c, v)) {
        notify(c, &c->recvq, &c->receivers_wait);
    }

    return true;
}

// sn_chan_poll takes a value c holds into out, without waiting, and
// reports whether there was one: a stopped Timer takes back so the time
// it sent and no one received.
bool sn_chan_poll(sn_chan *c, void *out) {
    if (c->cap == 0 || !ring_pop(c, out)) {
        return false;
    }

    notify(c, &c->sendq, &c->senders_wait);
    return true;
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

// sn_chan_len is the values in the ring: the positions are read until
// the tail reads the same before and after the head, so that the two
// belong together.
int64_t sn_chan_len(sn_chan *c) {
    if (c->cap == 0) {
        return 0;
    }

    for (;;) {
        uint64_t tail = atomic_load_explicit(&c->ring->tail, memory_order_seq_cst);
        uint64_t head = atomic_load_explicit(&c->ring->head, memory_order_seq_cst);
        if (atomic_load_explicit(&c->ring->tail, memory_order_seq_cst) != tail) {
            continue;
        }

        uint64_t hi = index_of(c, head), ti = index_of(c, tail);
        if (hi < ti) {
            return (int64_t)(ti - hi);
        }

        if (hi > ti) {
            return c->cap - (int64_t)(hi - ti);
        }

        return tail == head ? 0 : c->cap;
    }
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

// try_case performs the case k without waiting, the caller holding the
// lock of its channel, and reports whether it could: a send into a ring
// or to a waiting receiver, or dropped when the receivers are gone; a
// receive from a ring or a waiting sender, or with nothing once the
// channel has ended and is empty. What waits for the locks to be given
// back is left for the caller: a task to queue in *woken, a queue to
// notify in *notified, and in *dropping a send whose value no one can
// ever take, dropped only once the locks are let go, since the
// element's drop is the program's code and may drop a handle of one of
// the select's own channels.
static bool try_case(sn_case *k, sn_task **woken, sn_waitq **notified, _Atomic bool **waits, bool *dropping) {
    sn_chan *c = k->chan;
    *woken = NULL;
    *notified = NULL;
    *dropping = false;
    if (k->send) {
        if (atomic_load_explicit(&c->receiverless, memory_order_seq_cst)) { // no one can ever take the value
            *dropping = true;
            return true;
        }

        if (c->cap == 0) {
            return meet_receiver(c, k->value, woken);
        }

        if (!ring_push(c, k->value)) {
            return false;
        }

        *notified = &c->recvq;
        *waits = &c->receivers_wait;
        return true;
    }

    if (c->cap == 0) {
        if (meet_sender(c, k->value, woken)) {
            k->ok = true;
            return true;
        }

        k->ok = false;
        return atomic_load_explicit(&c->ended, memory_order_relaxed); // ended: the case proceeds with nothing
    }

    if (ring_pop(c, k->value)) {
        k->ok = true;
        *notified = &c->sendq;
        *waits = &c->senders_wait;
        return true;
    }

    return drained(c, k->value, &k->ok);
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

// queue_of is the queue a case waits on, and waits_of its word, which
// only a ring's queues have: an unbuffered channel's meetings find their
// waiters under the lock.
static sn_waitq *queue_of(const sn_case *k) { return k->send ? &k->chan->sendq : &k->chan->recvq; }

static _Atomic bool *waits_of(const sn_case *k) {
    if (k->chan->cap == 0) {
        return NULL;
    }

    return k->send ? &k->chan->senders_wait : &k->chan->receivers_wait;
}

// settled finishes the case i a try settled, the locks let go: the task
// it took is queued, the queue it moved the ring for notified, and the
// value no one can ever take dropped. Returns i.
static int settled(sn_case *cases, int i, sn_task *woken, sn_waitq *notified, _Atomic bool *waits, bool dropping) {
    sn_queue_claimed(woken);
    if (notified != NULL) {
        notify(cases[i].chan, notified, waits);
    }

    if (dropping) {
        drop_value(cases[i].chan, cases[i].value);
    }

    return i;
}

// cancelled_case settles a select its task's scope cancelled: every
// channel is as if ended, so every case is ready, and the case i, the
// first of the random order, is taken. Out of line and cold, as
// withdraw is.
static __attribute__((noinline, cold)) int cancelled_case(sn_case *cases, int i) {
    if (cases[i].send) {
        drop_value(cases[i].chan, cases[i].value);
    } else {
        cases[i].ok = false;
    }

    return i;
}

// select_in is sn_select over working arrays of n places each. The
// cases are tried under every channel's lock; with none ready the
// select registers a waiter on each case's queue and tries the ring
// cases once more, as a send or a receive does, before it parks. An
// operation that takes a waiter of an unbuffered case has done the
// case, or ended it; one that wakes a waiter of a ring case has done
// it, or has only moved the ring, and then the select tries every case
// again.
static int select_in(sn_case *cases, int n, bool has_default, sn_chan **chans, int *order, sn_waiter *waiters) {
    int m = channels_of(cases, n, chans);
    for (int i = 0; i < n; i++) { // a uniform permutation, built from the front
        int j = (int)(sn_next_random() % (uint64_t)(i + 1));
        order[i] = order[j];
        order[j] = i;
    }

    sn_task *self = sn_self();
    for (;;) {
        lock_all(chans, m);
        sn_task *woken;
        sn_waitq *notified;
        _Atomic bool *waits;
        bool dropping;
        for (int o = 0; o < n; o++) {
            int i = order[o];
            if (try_case(&cases[i], &woken, &notified, &waits, &dropping)) {
                unlock_all(chans, m);
                return settled(cases, i, woken, notified, waits, dropping);
            }
        }

        if (has_default) {
            unlock_all(chans, m);
            return -1;
        }

        sn_wait_state ws = { 0 };
        for (int i = 0; i < n; i++) {
            waiters[i] = (sn_waiter){ self, cases[i].value, &ws, i, false, false, NULL, NULL };
            enroll(queue_of(&cases[i]), waits_of(&cases[i]), &waiters[i]);
        }

        for (int o = 0; o < n; o++) { // the rings once more, registered: a move since the try finds the waiters
            int i = order[o];
            if (cases[i].chan->cap > 0 && try_case(&cases[i], &woken, &notified, &waits, &dropping)) {
                for (int j = 0; j < n; j++) {
                    leave(queue_of(&cases[j]), waits_of(&cases[j]), &waiters[j]);
                }

                unlock_all(chans, m);
                return settled(cases, i, woken, notified, waits, dropping);
            }
        }

        unlock_all(chans, m);
        bool done = wait_on(self, cases[0].chan, SN_CHAN_BOTH, &ws); // a select: the first case's channel names it
        lock_all(chans, m);
        for (int i = 0; i < n; i++) {
            leave(queue_of(&cases[i]), waits_of(&cases[i]), &waiters[i]);
        }

        unlock_all(chans, m);
        if (!done) {
            return cancelled_case(cases, order[0]);
        }

        int i = ws.fired;
        if (cases[i].chan->cap > 0 && !waiters[i].success) {
            continue; // the ring moved: the select tries again
        }

        if (cases[i].send) {
            if (!waiters[i].success) {
                drop_value(cases[i].chan, cases[i].value); // the last receiver went while the send waited
            }
        } else {
            cases[i].ok = waiters[i].success;
        }

        return i;
    }
}
