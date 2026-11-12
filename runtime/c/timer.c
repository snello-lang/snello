// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Timers: a timing wheel per worker, each under a lock of its own, fired
// by the workers between their tasks, as sched.c says. A timer either
// wakes a sleeping task, or, for After, Tick, a Timer and a Ticker, puts
// the time in a channel, or calls a function of the runtime's own, as a
// socket's deadline does; a ticking one goes back in its wheel for its
// next tick. A channel's timer holds a handle of its own: an After drops
// it once it fired, a Tick once no other handle is left to receive with,
// and so the channel of an After no one received from is freed when it
// fires.
//
// A wheel is the hierarchical timing wheel of Varghese and Lauck
// ("Hashed and hierarchical timing wheels", 1987), in the form Tokio
// gives it: ten levels of sixty-four slots, a slot of the first level
// one tick of 256 ns wide and a slot of each level after it as wide as
// the whole level before, so that the ten reach past the longest wait a
// clock counts. A timer goes in the slot its deadline falls in, at the
// lowest level whose span from now holds it, found from the highest bit
// in which the deadline's tick and the wheel's differ; a bit a slot says
// which slots of a level hold any. Putting a timer in and taking one out
// are so a few instructions whatever else the wheel holds, where a heap
// moves an entry up or down a path as long as the logarithm of their
// count. When time reaches a slot of a higher level, its timers move
// down to the levels their deadlines now call for, each at most once a
// level.
//
// A timer goes in the wheel of the worker that starts it, as Go's
// timers go on the P that starts them, so that the workers running
// tasks that sleep write no line they share: a task's sleep is in its
// worker's wheel, and the timer of a channel or of a socket's deadline
// in the wheel chosen the first time one was started on it, which the
// channel or the deadline remembers, so that whoever stops it finds its
// wheel without reading the timer. A worker fires its own wheel's
// timers as they come due and looks at the other wheels now and then,
// and a worker with nothing to run keeps time for them all.

#define _DEFAULT_SOURCE
#define _DARWIN_C_SOURCE

#include "internal.h"

#include <sched.h>
#include <stdatomic.h>
#include <stdlib.h>
#include <string.h>

enum {
    tick_shift = 8, // a tick is 256 ns
    wheel_levels = 10,
    slot_bits = 6,
    level_slots = 1 << slot_bits,
    spin_tries = 128, // turns a thread looks at a held lock before it gives its processor up
};

typedef struct timer timer;
struct timer {
    timer *prev, *next; // in its slot, or among the wheel's soon
    int64_t when;
    sn_task *t;     // a sleeping task
    sn_handoff *fired; // and the handoff its firing wakes it through
    sn_chan *ch;    // or the channel the time goes to
    int64_t every;  // and, for a Tick, its period
    void (*fire)(void *); // or the function to call, with arg
    void *arg;
    sn_deadline *at; // where the function's timer is kept
    int8_t level;    // the level it is at, -1 among the soon, -2 in no wheel
    uint8_t slot;
};

// A wheel's first line is what other workers read, its earliest
// deadline, and the lock, so that a worker putting a timer in its own
// wheel touches a line of its own.
typedef struct {
    _Atomic int64_t next; // when it is to be looked at again: no timer of its own is due before
    _Atomic int locked;
    _Atomic int64_t pending; // its timers that have yet to wake what they wake: wakes promised
    uint64_t elapsed; // the tick the wheel has reached: every timer in a slot is after it
    uint64_t occupied[wheel_levels];
    timer *slots[wheel_levels][level_slots];
    timer *soon;    // timers of the tick the wheel is at, or before it, that are not due yet: looked at one by one
    int64_t firing; // its channel timers fired and not yet done with their handle
} __attribute__((aligned(SN_CACHE_LINE))) wheel;

// The wheels, one a worker, made before any thread but the first runs
// and read by every worker on every timer it arms, fires or takes out:
// on a cache line of their own, so that no counter of another file's,
// written on every switch between tasks, lands beside them by the order
// the files are linked in.
static struct {
    wheel *at;
    int n;
} __attribute__((aligned(SN_CACHE_LINE))) wheels_of;
static _Atomic bool stopping; // the program is ending: no timer fires again

// sn_timers_start makes a wheel per worker, before any thread but the
// first runs.
void sn_timers_start(int64_t nworkers) {
    wheels_of.n = (int)nworkers;
    wheels_of.at = aligned_alloc(SN_CACHE_LINE, (size_t)wheels_of.n * sizeof *wheels_of.at);
    if (wheels_of.at == NULL) {
        sn_panic(SN_STR("out of memory", 13));
    }

    memset(wheels_of.at, 0, (size_t)wheels_of.n * sizeof *wheels_of.at);
    for (int i = 0; i < wheels_of.n; i++) {
        atomic_store(&wheels_of.at[i].next, INT64_MAX);
    }
}

// sn_timers_next is the earliest deadline any wheel holds, the greatest
// time when there is none or the program is ending; sn_timers_next_of
// is one wheel's.
int64_t sn_timers_next(void) {
    if (atomic_load(&stopping)) {
        return INT64_MAX;
    }

    int64_t first = INT64_MAX;
    for (int i = 0; i < wheels_of.n; i++) {
        int64_t next = atomic_load(&wheels_of.at[i].next);
        if (next < first) {
            first = next;
        }
    }

    return first;
}

int64_t sn_timers_next_of(int64_t worker) {
    return atomic_load(&wheels_of.at[worker].next);
}

// sn_timers_pending is how many timers have yet to wake what they wake.
// Each wheel counts its own, so that tasks starting and stopping timers
// on different workers write no count they share, and the counts are
// added up only here, for a worker that finds every worker idle.
int64_t sn_timers_pending(void) {
    int64_t n = 0;
    for (int i = 0; i < wheels_of.n; i++) {
        n += atomic_load(&wheels_of.at[i].pending);
    }

    return n;
}

// timers_done takes n timers of w off the count of those pending, each
// woken or taken out, and when they were the wheel's last has the
// workers look again at whether any task can still be woken.
static void timers_done(wheel *w, int64_t n) {
    if (atomic_fetch_sub(&w->pending, n) == n) {
        sn_sched_recheck();
    }
}

// home is the wheel a channel's or a deadline's timers go in: the one
// recorded, or the current worker's, recorded now; a record is the
// wheel's index and one, so that 0 is none yet.
static wheel *home(_Atomic int32_t *record) {
    int32_t h = atomic_load(record);
    if (h == 0) {
        int32_t none = 0;
        h = (int32_t)sn_worker_index() + 1;
        if (!atomic_compare_exchange_strong(record, &none, h)) {
            h = none; // another recorded first
        }
    }

    return &wheels_of.at[h - 1];
}

// A wheel's lock is held for a few instructions, to link a timer or to
// take one out, and for a deadline's function, which wakes a task, so a
// thread that finds it held looks again rather than sleeps, and gives
// its processor up if it keeps finding it so.
static void wheel_lock(wheel *w) {
    for (int i = 0;; i++) {
        int open = 0;
        if (atomic_compare_exchange_weak_explicit(&w->locked, &open, 1, memory_order_acquire, memory_order_relaxed)) {
            return;
        }

        if (i < spin_tries) {
            sn_spin_hint();
        } else {
            sched_yield();
        }
    }
}

// wheel_trylock takes the wheel's lock if no one holds it, and reports
// whether it did.
static bool wheel_trylock(wheel *w) {
    int open = 0;
    return atomic_compare_exchange_strong_explicit(&w->locked, &open, 1, memory_order_acquire, memory_order_relaxed);
}

static void wheel_unlock(wheel *w) {
    atomic_store_explicit(&w->locked, 0, memory_order_release);
}

// wheel_link puts x at the head of a list of the wheel's.
static void wheel_link(timer **head, timer *x, int level, int slot) {
    x->prev = NULL;
    x->next = *head;
    if (*head != NULL) {
        (*head)->prev = x;
    }

    *head = x;
    x->level = (int8_t)level;
    x->slot = (uint8_t)slot;
}

// wheel_insert puts x in the slot its deadline falls in, or among the
// soon when its tick is one the wheel has reached; the caller holds the
// lock.
static void wheel_insert(wheel *w, timer *x) {
    uint64_t tick = (uint64_t)x->when >> tick_shift;
    if (tick <= w->elapsed) {
        wheel_link(&w->soon, x, -1, 0);
        return;
    }

    int level = (63 - __builtin_clzll((w->elapsed ^ tick) | (level_slots - 1))) / slot_bits;
    int slot = (int)((tick >> (slot_bits * level)) & (level_slots - 1));
    wheel_link(&w->slots[level][slot], x, level, slot);
    w->occupied[level] |= UINT64_C(1) << slot;
}

// wheel_remove takes x out of the wheel; the caller holds the lock.
static void wheel_remove(wheel *w, timer *x) {
    timer **head = x->level < 0 ? &w->soon : &w->slots[x->level][x->slot];
    if (x->prev != NULL) {
        x->prev->next = x->next;
    } else {
        *head = x->next;
    }

    if (x->next != NULL) {
        x->next->prev = x->prev;
    }

    if (x->level >= 0 && *head == NULL) {
        w->occupied[x->level] &= ~(UINT64_C(1) << x->slot);
    }

    x->level = -2;
}

// wheel_next finds the slot that comes due first and the tick it comes
// due at, and reports whether any slot holds a timer. It is the first
// such slot of the lowest level that has one: none of a higher level
// comes before it.
static bool wheel_next(const wheel *w, int *level, int *slot, uint64_t *due) {
    for (int l = 0; l < wheel_levels; l++) {
        uint64_t held = w->occupied[l];
        if (held == 0) {
            continue;
        }

        int shift = slot_bits * l;
        unsigned now = (unsigned)((w->elapsed >> shift) & (level_slots - 1));
        uint64_t from_now = (held >> now) | (held << ((64 - now) & 63));
        unsigned s = ((unsigned)__builtin_ctzll(from_now) + now) & (level_slots - 1);
        uint64_t slot_span = UINT64_C(1) << shift;
        uint64_t level_span = slot_span << slot_bits;
        uint64_t at = (w->elapsed & ~(level_span - 1)) + s * slot_span;
        if (at <= w->elapsed) { // a slot of the last level, one turn of it on
            at += level_span;
        }

        *level = l;
        *slot = (int)s;
        *due = at;
        return true;
    }

    return false;
}

// wheel_poll takes out of the wheel every timer due at now, linked
// through next in the order their slots came due, moves the timers of
// the slots time has reached down to the levels their deadlines call
// for, and brings the wheel to now; the caller holds the lock.
static timer *wheel_poll(wheel *w, int64_t now) {
    timer *due = NULL, **tail = &due;
    for (timer *x = w->soon, *after; x != NULL; x = after) {
        after = x->next;
        if (x->when <= now) {
            wheel_remove(w, x);
            *tail = x;
            tail = &x->next;
        }
    }

    uint64_t now_tick = (uint64_t)now >> tick_shift;
    int level, slot;
    uint64_t at;
    while (wheel_next(w, &level, &slot, &at) && at <= now_tick) {
        timer *list = w->slots[level][slot];
        w->slots[level][slot] = NULL;
        w->occupied[level] &= ~(UINT64_C(1) << slot);
        w->elapsed = at;
        for (timer *x = list, *after; x != NULL; x = after) {
            after = x->next;
            x->level = -2;
            if (x->when <= now) {
                *tail = x;
                tail = &x->next;
            } else {
                wheel_insert(w, x);
            }
        }
    }

    if (now_tick > w->elapsed) {
        w->elapsed = now_tick;
    }

    *tail = NULL;
    return due;
}

// wheel_renew sets when the wheel is to be looked at again: at the
// earliest of the soon, or when its next slot comes due, which is no
// later than any timer in it; the caller holds the lock.
static void wheel_renew(wheel *w) {
    int64_t next = INT64_MAX;
    for (timer *x = w->soon; x != NULL; x = x->next) {
        if (x->when < next) {
            next = x->when;
        }
    }

    int level, slot;
    uint64_t at;
    if (wheel_next(w, &level, &slot, &at) && at <= (uint64_t)INT64_MAX >> tick_shift && (int64_t)(at << tick_shift) < next) {
        next = (int64_t)(at << tick_shift);
    }

    atomic_store(&w->next, next);
}

// wheel_place puts x in the wheel and reports whether it is now the
// wheel's earliest, so that whoever keeps time is to be told; the caller
// holds the lock, and tells once it has let it go.
static bool wheel_place(wheel *w, timer *x) {
    wheel_insert(w, x);
    if (x->when >= atomic_load(&w->next)) {
        return false;
    }

    atomic_store(&w->next, x->when);
    return !atomic_load(&stopping); // the program ending, no one is to wake for it
}

// timer_arm places x, counted among the wheel's pending until what it
// wakes is woken.
static bool timer_arm(wheel *w, timer *x) {
    atomic_fetch_add(&w->pending, 1);
    return wheel_place(w, x);
}

// wheel_fire fires the timers of w due at now, and reports whether it
// fired any. A worker waits for its own wheel's lock, which it seldom
// finds held; another worker's it leaves for the next look when it is
// held, by a thread firing it or for the few instructions a timer takes
// to start or stop, rather than have the workers queue on one wheel.
static bool wheel_fire(wheel *w, int64_t now, bool own) {
    int64_t woken = 0;
    timer *chans = NULL, **tail = &chans;
    timer *sleepers = NULL, **last = &sleepers;
    if (own) {
        wheel_lock(w);
    } else if (!wheel_trylock(w)) {
        return false;
    }

    if (atomic_load(&stopping)) {
        wheel_unlock(w);
        return false;
    }

    for (timer *x = wheel_poll(w, now), *after; x != NULL; x = after) {
        after = x->next;
        if (x->fire != NULL) { // under the lock, so that a disarm either takes it out first or waits for it to finish
            x->at->timer = NULL;
            x->fire(x->arg);
            sn_free(x);
            woken++;
        } else if (x->ch == NULL) { // woken once the lock is let go, so that it is held for no queue's
            x->next = NULL;
            *last = x;
            last = &x->next;
            woken++;
        } else { // the channel's lock is never taken under this one
            sn_chan_placeTimer(x->ch, -1);
            w->firing++;
            x->next = NULL;
            *tail = x;
            tail = &x->next;
        }
    }

    wheel_renew(w);
    wheel_unlock(w);
    // A sleeping task's timer is on its stack, and the task may be
    // running, brought out of its park by a wake meant for an earlier
    // wait; it leaves the sleep, and may end and be freed, only once the
    // firing is done with it, so the firing wakes it through its
    // handoff (internal.h), and reads nothing of the timer after.
    for (timer *x = sleepers, *after; x != NULL; x = after) {
        after = x->next;
        sn_handoff_wake(x->fired, x->t);
    }

    if (woken > 0) {
        timers_done(w, woken);
    }

    for (timer *x = chans, *after; x != NULL; x = after) {
        after = x->next;
        // A Tick's next tick is on the period, past the ticks a slow
        // receiver missed.
        int64_t next = x->every > 0 ? now + x->every - (now - x->when) % x->every : 0;
        sn_time at = sn_time_read();
        bool heard = sn_chan_offer(x->ch, &at);
        bool tell = false;
        wheel_lock(w);
        bool again = heard && x->every > 0 && !atomic_load(&stopping);
        if (again) {
            x->when = next;
            tell = timer_arm(w, x); // counted again before this firing stops counting, so the count never passes through zero
            sn_chan_placeTimer(x->ch, (int64_t)(intptr_t)x);
            if (sn_chan_alone(x->ch)) {
                // The program's last handle went after the offer and
                // before the timer was back in the wheel, so its drop
                // found no timer to take out: this does it. The drop
                // takes away its handle before it looks for the timer,
                // and the timer is placed before this looks at the
                // handles, so one of the two always sees the other.
                wheel_remove(w, x);
                sn_chan_placeTimer(x->ch, -1);
                atomic_fetch_sub(&w->pending, 1); // the count it took a moment ago; the firing's own goes below, with the lock let go
                again = false;
            }
        }

        wheel_unlock(w);
        if (tell && again) {
            sn_sched_timer_set(next);
        }

        if (!again) { // an After's handle, or a Tick's no one can receive from
            sn_chan_drop(&x->ch);
            sn_free(x);
        }

        timers_done(w, 1);
        wheel_lock(w);
        w->firing--;
        wheel_unlock(w);
    }

    return woken > 0 || chans != NULL;
}

// sn_timers_fire fires every timer due at now, on the thread that calls
// it, and reports whether it fired any; sn_timers_fire_own fires one
// worker's wheel, as a worker does between its tasks, as Go's worker
// runs its own P's timers at each schedule and looks at the others'
// only when it has nothing to run. Any number of threads may fire at
// once, each at the wheels the others are not at.
bool sn_timers_fire(int64_t now) {
    bool fired = false;
    for (int i = 0; i < wheels_of.n; i++) {
        wheel *w = &wheels_of.at[i];
        if (atomic_load(&w->next) <= now) {
            fired = wheel_fire(w, now, false) || fired;
        }
    }

    return fired;
}

bool sn_timers_fire_own(int64_t worker, int64_t now) {
    wheel *w = &wheels_of.at[worker];
    return atomic_load(&w->next) <= now && wheel_fire(w, now, true);
}

// deadline is the time d from now on the monotonic clock, the largest
// time when that is past it, so that a Sleep or an After of the longest
// Duration waits for as long as there is rather than wrapping round to
// fire at once.
static int64_t deadline(int64_t d) {
    int64_t now = sn_time_mono();
    return d > INT64_MAX - now ? INT64_MAX : now + d;
}

// timer_new is a timer in a block of its own, which whoever takes it
// out of its wheel for the last time frees.
static timer *timer_new(timer from) {
    timer *x = sn_alloc(sizeof *x);
    *x = from;
    x->level = -2;
    return x;
}

// sn_deadline_arm calls fire with arg d from now, on the thread that
// fires the timer, a worker between two tasks or the monitor, under the
// lock of the timer's wheel, which is what lets a disarm promise the
// function is not running once it returns: a function that parks no
// task and takes no lock held across a start or a stop of a timer, and
// is short, since the wheel's lock is held for it. The timer is kept in
// *at, which holds none while it is in no wheel; arming it again moves
// it. Like every timer it is a wake promised until it fires or is
// disarmed.
void sn_deadline_arm(sn_deadline *at, int64_t d, void (*fire)(void *), void *arg) {
    sn_sched_start();
    timer *x = timer_new((timer){.when = deadline(d), .fire = fire, .arg = arg, .at = at});
    wheel *w = home(&at->home);
    wheel_lock(w);
    timer *old = at->timer;
    if (old != NULL) {
        wheel_remove(w, old);
        wheel_renew(w);
    }

    bool tell = timer_arm(w, x);
    at->timer = x;
    wheel_unlock(w);
    if (old != NULL) {
        sn_free(old);
        timers_done(w, 1);
    }

    if (tell) {
        sn_sched_timer_set(x->when);
    }
}

// sn_deadline_disarm takes the timer kept in *at out of its wheel, if
// it is in one. Once it returns, the timer's function is not running
// and will not run.
void sn_deadline_disarm(sn_deadline *at) {
    if (atomic_load(&at->home) == 0) {
        return; // never armed
    }

    wheel *w = home(&at->home);
    wheel_lock(w);
    timer *old = at->timer;
    if (old != NULL) {
        wheel_remove(w, old);
        wheel_renew(w);
        at->timer = NULL;
    }

    wheel_unlock(w);
    if (old != NULL) {
        sn_free(old);
        timers_done(w, 1);
    }
}

// sn_time_startTimer puts the time in ch once, d from now, and then
// every period, when every is above zero.
void sn_time_startTimer(sn_chan *ch, int64_t d, int64_t every) {
    sn_sched_start();
    timer *x = timer_new((timer){.when = deadline(d), .ch = ch, .every = every});
    wheel *w = home(sn_chan_home(ch));
    wheel_lock(w);
    bool tell = timer_arm(w, x);
    sn_chan_placeTimer(ch, (int64_t)(intptr_t)x);
    wheel_unlock(w);
    if (tell) {
        sn_sched_timer_set(x->when);
    }
}

// timer_stop stops the timer of c, a Timer's or a Ticker's channel, and
// reports whether it stopped anything: a timer still in its wheel,
// which is taken out and its handle dropped, or a time the timer sent
// that no one received yet, which is taken back. It waits for a timer
// of the wheel firing now to be done first, so that once it returns
// nothing more is sent and nothing sent before is left to receive, as
// Go's Stop promises since its timers' channels hold nothing back.
static bool timer_stop(sn_chan *c) {
    wheel *w = home(sn_chan_home(c));
    wheel_lock(w);
    while (w->firing > 0) {
        wheel_unlock(w);
        sched_yield();
        wheel_lock(w);
    }

    timer *x = (timer *)(intptr_t)sn_chan_timer(c);
    if (x != (timer *)(intptr_t)-1) {
        wheel_remove(w, x);
        wheel_renew(w);
        sn_chan_placeTimer(c, -1);
        wheel_unlock(w);
        timers_done(w, 1);
        sn_chan_drop(&x->ch);
        sn_free(x);
        return true;
    }

    wheel_unlock(w);
    sn_time unread;
    return sn_chan_poll(c, &unread);
}

// sn_time_stopTimer is package time's stopTimer: it stops the timer of
// ch and drops ch, the handle it was given to name the channel by.
bool sn_time_stopTimer(sn_chan *ch) {
    bool stopped = timer_stop(ch);
    sn_chan_drop(&ch);
    return stopped;
}

// sn_time_resetTimer is package time's resetTimer: it makes the timer
// of ch send d from now and then every period when every is above zero,
// and reports what a stop would have. A timer still in its wheel is
// moved there, with nothing freed or made, and ch, the handle it was
// given to name the channel by, is dropped; one that fired is started
// again with ch as its own handle.
bool sn_time_resetTimer(sn_chan *ch, int64_t d, int64_t every) {
    int64_t when = deadline(d);
    wheel *w = home(sn_chan_home(ch));
    wheel_lock(w);
    while (w->firing > 0) {
        wheel_unlock(w);
        sched_yield();
        wheel_lock(w);
    }

    timer *x = (timer *)(intptr_t)sn_chan_timer(ch);
    if (x != (timer *)(intptr_t)-1) {
        wheel_remove(w, x);
        wheel_renew(w);
        x->when = when;
        x->every = every;
        bool tell = wheel_place(w, x);
        wheel_unlock(w);
        sn_chan_drop(&ch);
        if (tell) {
            sn_sched_timer_set(when);
        }

        return true;
    }

    wheel_unlock(w);
    sn_time unread;
    bool stopped = sn_chan_poll(ch, &unread);
    sn_time_startTimer(ch, d, every);
    return stopped;
}

// sn_timer_abandon drops a handle of c, whose After or Tick was in a
// wheel a moment ago, and reports whether it did. Under the wheel's
// lock, which whoever fires a timer holds to take it out, the timer is
// in the wheel or it is not: if it is, the handle comes down there, and
// when it was the last but the timer's own, no one can receive what the
// timer would send, so it is no promise of a wake, and the timer is
// taken out and its handle, the last, frees the channel now rather than
// at its deadline. A timer that fired meanwhile, or the end of the
// program, which drops the timers' handles in sn_timers_stop, leaves
// the handle to the caller, who drops it as any other.
bool sn_timer_abandon(sn_chan *c) {
    wheel *w = home(sn_chan_home(c));
    wheel_lock(w);
    timer *x = (timer *)(intptr_t)sn_chan_timer(c);
    if (x == (timer *)(intptr_t)-1 || atomic_load(&stopping)) {
        wheel_unlock(w);
        return false;
    }

    if (!sn_chan_release(c)) { // another handle of the program's remains
        wheel_unlock(w);
        return true;
    }

    wheel_remove(w, x);
    wheel_renew(w);
    sn_chan_placeTimer(c, -1);
    wheel_unlock(w);
    timers_done(w, 1);
    sn_chan_drop(&x->ch);
    sn_free(x);
    return true;
}

// sn_timers_stop stops the timers as the program ends and drops the
// handles the channel timers hold, after any timer firing now is done
// with its own, so that a debug build's count of blocks finds the
// channels of a pending After or a running Tick freed.
void sn_timers_stop(void) {
    atomic_store(&stopping, true);
    for (int i = 0; i < wheels_of.n; i++) {
        wheel *w = &wheels_of.at[i];
        wheel_lock(w);
        while (w->firing > 0) {
            wheel_unlock(w);
            sched_yield();
            wheel_lock(w);
        }

        // Every timer comes out, so that none points at a channel freed
        // here: a channel's with its handle, a deadline's with its
        // place cleared; a sleeping task's is on its own stack.
        timer *all = w->soon;
        w->soon = NULL;
        for (int l = 0; l < wheel_levels; l++) {
            for (int s = 0; s < level_slots && w->occupied[l] != 0; s++) {
                for (timer *x = w->slots[l][s], *after; x != NULL; x = after) {
                    after = x->next;
                    x->next = all;
                    all = x;
                }

                w->slots[l][s] = NULL;
            }

            w->occupied[l] = 0;
        }

        atomic_store(&w->next, INT64_MAX);
        wheel_unlock(w);
        for (timer *x = all, *after; x != NULL; x = after) {
            after = x->next;
            x->level = -2;
            if (x->ch != NULL) {
                sn_chan_placeTimer(x->ch, -1);
                sn_chan_drop(&x->ch);
                sn_free(x);
            } else if (x->fire != NULL) {
                x->at->timer = NULL;
                sn_free(x);
            }
        }
    }
}

// sn_sleep parks the task until its timer fires. A park can return for
// a wake meant for an earlier wait, one a select's second channel sent
// before the select unlinked from it, so the task waits on its handoff
// until the firing is done with it; returning sooner would cut the
// sleep short and leave the timer to wake a task that may be gone. The timer is on
// the task's own stack, which stands until the sleep is over, and in
// the wheel of the worker the task is on. No one is woken to keep time
// for it: the task parks now, and its worker, on its way to its next
// task or to its wait, keeps time or has another keep it.
void sn_sleep(int64_t d) {
    if (d <= 0) {
        return;
    }

    sn_sched_start();
    sn_task *t = sn_self();
    sn_handoff fired = SN_HANDOFF_OPEN;
    timer x = {.when = deadline(d), .t = t, .fired = &fired, .level = -2};
    wheel *w = &wheels_of.at[sn_worker_index()];
    wheel_lock(w);
    bool tell = timer_arm(w, &x);
    wheel_unlock(w);
    if (tell) {
        sn_sched_owe(x.when);
    }

    sn_handoff_wait(&fired);
}
