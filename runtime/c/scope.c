// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The scopes cancellation and deadlines belong to. A scope is a record
// of the runtime's own, counted: the scope it is under, which it holds,
// and for a task's own scope the group it was added to, which it holds
// too; its state; the tasks in it and the scopes under it, in lists of
// its lock; and the timer of its deadline. A task holds the scope it is
// in. A cancel marks a scope, and under its lock wakes each task in it
// and cancels each scope under it, so a cancel reaches every task
// started under the scope, wherever it has gone since; a scope already
// marked is passed over, so a walk ends. A wake a cancel sends is the
// scheduler's plain wake, which a running task keeps for its next park:
// each wait a cancel may end looks at the state of its task's scope
// before it parks and when it wakes before its waker came, and takes
// the wait back.
//
// A scope's lock is held alone, never with another's, and never under
// a timer wheel's: a cancel holds the lock of the scope it walks into
// while it marks and holds the scopes under it, and walks into each of
// those after it has let the lock go, so a walk is a loop over the
// scopes it holds, not a recursion, as the stack of the task that
// cancels is bounded by the analysis of its calls, which a recursion
// would leave unbounded; a release of the last hold of a scope is a
// loop over the scopes it held for the same reason. A scope is freed by
// its last holder, which unlinks it from the scopes above it under
// their locks first; a walk that holds the lock of a scope above can
// reach a scope whose count has come to zero, which it marks and does
// not hold, since such a scope has no task and no scope under it, each
// of those holding it, and a hold taken then would free it a second
// time. A deadline's timer holds its scope, and fires with the wheel
// let go; the Within that leaves takes the timer out and lets the
// timer's hold go when it did, and leaves it to the firing when the
// firing came first.
//
// A task spawned for a handle has a scope of its own, so that a group
// that takes the handle takes the task with every task it starts,
// whenever it starts them. It is made only when it is needed, since a
// handle's channel and its spawn are measured in nanoseconds: at the
// spawn when the spawner is in a scope, or when the task first starts a
// task, enters Within or makes a group. Until then the handle channel's
// word holds the task, tagged running; from then on, the scope, which
// the channel holds; and once the task has ended with none, the mark
// ended. A group that takes the handle of a task with no scope of its
// own puts the task in the group's scope itself, as a task started in
// it would be, and the word holds that scope, tagged in_group. The task
// and a group's Add each change the word with one compare-and-swap, so
// that a group takes in a task only while it runs, and a task that ends
// with no scope of its own pays that one instruction.
//
// A task is in its scope's list of tasks, which a cancel wakes, only
// from its first wait a cancel may end, or from the Add that took it:
// a task that never waits needs no wake, and reads the scope's state
// before each wait anyway, so the tasks of a scope that compute and
// end pay the scope's lock never, and the others once.
//
// The root is the scope of the program itself, which an interrupt the
// program did not ask for cancels: every scope made in no scope is
// under it, and a task in no scope, the main task and every task
// spawned in none, is in its list from its first wait a cancel may
// end, as a task of any scope is in its scope's. A task in no scope
// keeps no scope in its record, so a spawn in none pays nothing for
// the root and a spawn for a handle keeps its lazy word; the task is
// known to be the root's by that very record, and the group that takes
// a running task out of the root takes it out of the root's list under
// the root's lock, which the task's own enlisting takes too, reading
// the handle's word under it, so that the two cannot both put the task
// in a list. The root is never freed: its first hold is nobody's.

#include "internal.h"

#include <stdlib.h>
#include <string.h>

enum { under_parent = SN_SCOPE_UNDER_PARENT, under_group = SN_SCOPE_UNDER_GROUP, sides = SN_SCOPE_SIDES };

// running tags the word of the handle channel of a task with no scope
// of its own yet: the task's address, which is even, with this bit.
static const uintptr_t running = 1;

// in_group tags the word of the handle channel of a task a group took in
// before it had a scope of its own: the group's scope, with this bit.
// The channel does not hold that scope.
static const uintptr_t in_group = 2;

// tags are both bits, which a scope's address, aligned to eight, has
// neither of.
static const uintptr_t tags = running | in_group;

// ended is the record whose address the word holds once the task has
// ended with no scope of its own; it is never a scope.
static sn_scope ended;

// few_groups is how many groups a walk up the scopes keeps in its own
// frame before it takes a block for them.
enum { few_groups = 16 };

// adopt_mu orders the links of tasks' scopes under groups, so that two
// Adds racing cannot link two scopes under each other's groups and
// make a circle no release could ever free: each Add sees the other's
// link, or goes first.
static sn_qlock adopt_mu;

static void scope_cancel(sn_scope *s, int why);
static void scope_enlist(sn_task *t, sn_scope *s);
static void unlist(sn_task *t, sn_scope *s);

// sn_root is the program's own scope, whole before the program starts:
// one hold that is let go never, so that it is freed never, its lock
// free as zeroed.
sn_scope sn_root = { .refs = 1, .state = SN_SCOPE_LIVE };
static void leave_from(sn_task *t, sn_scope *s);
static sn_scope *held_above(sn_scope *freed);

// scope_settled waits for the Add that is putting the running task in s,
// under s's lock, to be done, so that the task is in s once it returns:
// the lock taken and let go.
SN_HELD_STATIC static void scope_settled(sn_scope *s) {
    sn_qlock_take(&s->mu);
    sn_qlock_give(&s->mu);
}

SN_HELD_STATIC static void retain(sn_scope *s) { atomic_fetch_add_explicit(&s->refs, 1, memory_order_relaxed); }

// retain_live holds s unless its count is zero, its last holder freeing
// it, and reports whether it did.
SN_HELD_STATIC static bool retain_live(sn_scope *s) {
    int64_t n = atomic_load_explicit(&s->refs, memory_order_relaxed);
    while (n != 0) {
        if (atomic_compare_exchange_weak_explicit(&s->refs, &n, n + 1, memory_order_acquire, memory_order_relaxed)) {
            return true;
        }
    }

    return false;
}

// link_under puts s among the scopes under above on side, which it holds,
// and gives s the state of above when that is no longer live, under
// above's lock, so that a cancel of above either finds s in its list or
// has marked above before s read it.
SN_HELD_STATIC static void link_under(sn_scope *s, sn_scope *above, int side) {
    retain(above);
    s->above[side] = above;
    sn_qlock_take(&above->mu);
    s->prev[side] = NULL;
    s->next[side] = above->below[side];
    if (above->below[side] != NULL) {
        above->below[side]->prev[side] = s;
    }

    above->below[side] = s;
    int why = atomic_load(&above->state);
    sn_qlock_give(&above->mu);
    if (why != SN_SCOPE_LIVE) {
        scope_cancel(s, why);
    }
}

// unlink_from takes s out from among the scopes under the one above it
// on side, and returns that scope, whose hold the caller lets go; NULL
// for none.
SN_HELD_STATIC static sn_scope *unlink_from(sn_scope *s, int side) {
    sn_scope *above = s->above[side];
    if (above == NULL) {
        return NULL;
    }

    sn_qlock_take(&above->mu);
    if (s->prev[side] != NULL) {
        s->prev[side]->next[side] = s->next[side];
    } else {
        above->below[side] = s->next[side];
    }

    if (s->next[side] != NULL) {
        s->next[side]->prev[side] = s->prev[side];
    }

    sn_qlock_give(&above->mu);
    return above;
}

// scope_new makes a scope under above, or under none, held once by the
// caller.
SN_HELD_STATIC static sn_scope *scope_new(sn_scope *above) {
    sn_scope *s = sn_alloc(sizeof *s); // counted with the program's blocks, so that a debug build reports one leaked
    memset(s, 0, sizeof *s);
    atomic_init(&s->refs, 1);
    atomic_init(&s->state, SN_SCOPE_LIVE);
    sn_qlock_init(&s->mu);
    link_under(s, above != NULL ? above : &sn_root, under_parent);
    return s;
}

// sn_scope_cancel_root cancels the program's scope: every task in no
// scope and every scope under none, which is every task and scope of
// the program.
SN_HELD void sn_scope_cancel_root(void) { scope_cancel(&sn_root, SN_SCOPE_CANCELLED); }

// root_enlist puts the task t, in no scope, in the root's list, unless
// a group has taken it meanwhile, which its handle's word says under
// the root's lock, the lock the group takes to take it out; t's record
// keeps no scope after, so that it stays a task in none, the root's
// hold on it kept until it leaves the list. The word is read first:
// a group that took the task writes its enlisted under the group's
// lock, and the word says so before, so that enlisted is read only
// while nothing else writes it.
SN_HELD_STATIC static void root_enlist(sn_task *t) {
    sn_scope *r = &sn_root;
    sn_scope_link *l = sn_task_scope(t);
    atomic_store(&l->enlisting, true); // before the lock, so that a group that changed the word first reads it
    sn_qlock_take(&r->mu);
    if ((l->handle == NULL || (atomic_load(sn_chan_word(l->handle)) & running) != 0) && !l->enlisted) {
        scope_enlist(t, r);
        atomic_store_explicit(&l->scope, NULL, memory_order_relaxed);
    }

    sn_qlock_give(&r->mu);
}

// root_unlink takes the task t out of the root's list, when it is in
// it, for the group that takes t in, which holds the group's lock: the
// root's lock is taken under a group's, never the other way. A task
// that never began to enlist, which the flag says, is in no list, and
// the lock is not taken for it: the flag is set before the task takes
// the lock and reads the word, so a task the group's write of the word
// did not reach has set it by the time the word was written, and one
// it reached takes no list.
SN_HELD_STATIC static void root_unlink(sn_task *t) {
    sn_scope_link *l = sn_task_scope(t);
    if (!atomic_load(&l->enlisting)) {
        return;
    }

    sn_scope *r = &sn_root;
    sn_qlock_take(&r->mu);
    bool was = l->enlisted;
    if (was) {
        unlist(t, r);
    }

    sn_qlock_give(&r->mu);
    if (was) {
        sn_scope_release(r);
    }
}

// sn_scope_release lets a hold of s go, and the last frees it: its
// places in the lists above it unlinked, and the holds it had on the
// scopes above it let go in turn; its deadline's timer, which holds it,
// is gone by then. A freed scope whose holds are still to be
// let go is kept on a list through its next, with the scopes it held
// in its above, until both are let go; the loop ends when the list is
// empty.
SN_HELD void sn_scope_release(sn_scope *s) {
    sn_scope *freed = NULL;
    for (;;) {
        if (atomic_fetch_sub_explicit(&s->refs, 1, memory_order_acq_rel) == 1) {
            for (int side = 0; side < sides; side++) {
                s->above[side] = unlink_from(s, side);
            }

            sn_qlock_free(&s->mu);
            s->next[under_parent] = freed;
            freed = s;
        }

        for (;;) {
            if (freed == NULL) {
                return;
            }

            s = held_above(freed);
            if (s != NULL) {
                break; // one more hold to let go
            }

            sn_scope *done = freed;
            freed = done->next[under_parent];
            sn_free(done);
        }
    }
}

// held_above takes the next scope the freed scope held out of its
// above, for its hold to be let go, or reports NULL once both are.
SN_HELD_STATIC static sn_scope *held_above(sn_scope *freed) {
    for (int side = 0; side < sides; side++) {
        sn_scope *s = freed->above[side];
        if (s != NULL) {
            freed->above[side] = NULL;
            return s;
        }
    }

    return NULL;
}

// mark marks s cancelled for why, and reports whether it was live: one
// cancel marks a scope, and walks into it.
SN_HELD_STATIC static bool mark(sn_scope *s, int why) {
    int live = SN_SCOPE_LIVE;
    return atomic_compare_exchange_strong(&s->state, &live, why);
}

// scope_cancel marks s cancelled for why, unless it is already, wakes
// its tasks and cancels the scopes under it: a walk over the scopes it
// marks, each held for the walk and let go after, through pending. A
// scope whose count is zero is marked and not walked into: nothing is
// in it.
SN_HELD_STATIC static void scope_cancel(sn_scope *s, int why) {
    if (!mark(s, why) || !retain_live(s)) {
        return;
    }

    s->pending = NULL;
    for (sn_scope *todo = s; todo != NULL;) {
        sn_scope *c = todo;
        todo = c->pending;
        sn_qlock_take(&c->mu);
        for (sn_task *t = c->tasks; t != NULL; t = sn_task_scope(t)->next) {
            sn_wake(t);
        }

        for (int side = 0; side < sides; side++) {
            for (sn_scope *b = c->below[side]; b != NULL; b = b->next[side]) {
                if (mark(b, why) && retain_live(b)) { // under c's lock, which b is in the list of until it is freed
                    b->pending = todo;
                    todo = b;
                }
            }
        }

        sn_qlock_give(&c->mu);
        sn_scope_release(c);
    }
}

// scope_expire is the deadline's timer, with the wheel let go: the
// scope's time has passed, and the timer's hold goes with the firing.
SN_HELD_STATIC static void scope_expire(void *arg) {
    scope_cancel(arg, SN_SCOPE_TIMED_OUT);
    sn_scope_release(arg);
}

// scope_enlist puts the task t among the tasks of s, whose lock the
// caller holds, which a cancel of s then wakes, and makes s its scope
// when it was not yet: enlisted is written before the scope, so that a
// task that reads its scope with acquire reads it enlisted.
SN_HELD_STATIC static void scope_enlist(sn_task *t, sn_scope *s) {
    sn_scope_link *l = sn_task_scope(t);
    l->enlisted = true;
    l->prev = NULL;
    l->next = s->tasks;
    if (s->tasks != NULL) {
        sn_task_scope(s->tasks)->prev = t;
    }

    s->tasks = t;
    if (atomic_load_explicit(&l->scope, memory_order_relaxed) != s) {
        retain(s);
        atomic_store_explicit(&l->scope, s, memory_order_release);
    }
}

// join makes s the scope of the task t, which holds it from then on: t
// is the running task, or one not yet started, and goes in s's list of
// tasks at its first wait a cancel may end, through
// sn_scope_enlist_self, since a task that never waits needs no wake and
// a wait reads the scope's state once it is in the list. scope_leave
// takes t out of its scope, and leave_from out of s, which it is in.
SN_HELD_STATIC static void join(sn_task *t, sn_scope *s) {
    retain(s);
    atomic_store_explicit(&sn_task_scope(t)->scope, s, memory_order_release);
}

SN_HELD void sn_scope_enlist_self(sn_task *t, sn_scope *s) {
    if (s == NULL) {
        root_enlist(t);
        return;
    }

    sn_qlock_take(&s->mu);
    scope_enlist(t, s);
    sn_qlock_give(&s->mu);
}

SN_HELD_STATIC static void scope_leave(sn_task *t) {
    sn_scope_link *l = sn_task_scope(t);
    sn_scope *s = atomic_load_explicit(&l->scope, memory_order_relaxed);
    if (s != NULL) {
        leave_from(t, s);
    } else if (l->enlisted) {
        leave_from(t, &sn_root); // in no scope, and in the root's list since a wait
    }
}

// unlist takes t out of s's list of tasks, under s's lock, which the
// caller holds.
SN_HELD_STATIC static void unlist(sn_task *t, sn_scope *s) {
    sn_scope_link *l = sn_task_scope(t);
    if (l->prev != NULL) {
        sn_task_scope(l->prev)->next = l->next;
    } else {
        s->tasks = l->next;
    }

    if (l->next != NULL) {
        sn_task_scope(l->next)->prev = l->prev;
    }

    l->enlisted = false;
}

SN_HELD_STATIC static void leave_from(sn_task *t, sn_scope *s) {
    sn_scope_link *l = sn_task_scope(t);
    if (l->enlisted) {
        sn_qlock_take(&s->mu);
        unlist(t, s);
        sn_qlock_give(&s->mu);
    }

    atomic_store_explicit(&l->scope, NULL, memory_order_relaxed);
    sn_scope_release(s);
}

// own makes the scope of its own of the task t, the running one, whose
// handle channel's word is w, the task tagged: a scope under none, with
// t in it, held by the channel and by t. It reports the scope the word
// holds after: the one made, or the group an Add put the task in first,
// whose lock is taken and let go so that the task is in it once own
// returns.
SN_HELD_STATIC static sn_scope *own(sn_task *t, uintptr_t w, _Atomic uintptr_t *word) {
    sn_scope *s = scope_new(NULL);
    if (atomic_compare_exchange_strong(word, &w, (uintptr_t)s)) {
        scope_leave(t); // out of the root's list, which no group can take it from now
        join(t, s);     // after the word says so: an Add that lost the word writes nothing of the task's, and one that
        return s;       // cancels s meanwhile finds no task in it, the task reading s's state at its next wait
    }

    sn_scope_release(s);                       // the maker's
    sn_scope *first = (sn_scope *)(w & ~tags); // the task runs, so the word is no ended: a group's, tagged
    scope_settled(first);                      // the Add put the task in it under its lock, writing its scope
    return first;
}

SN_HELD sn_scope *sn_scope_current(sn_task *t) {
    sn_scope_link *l = sn_task_scope(t);
    sn_scope *s = atomic_load_explicit(&l->scope, memory_order_acquire);
    if (s != NULL || l->handle == NULL) {
        return s;
    }

    _Atomic uintptr_t *word = sn_chan_word(l->handle);
    uintptr_t w = atomic_load_explicit(word, memory_order_acquire);
    if (w == (uintptr_t)&ended) {
        return NULL; // the task's work has ended: a drop after its offer that starts a task starts it in no scope
    }

    if ((w & running) == 0) { // its own, read again after a drop that follows the offer, or its group's, which an Add
                              // may still be putting it in
        sn_scope *first = (sn_scope *)(w & ~tags);
        scope_settled(first);
        return first;
    }

    return own(t, w, word);
}

SN_HELD void sn_scope_inherit(sn_task *child, sn_task *parent) {
    sn_scope *s = sn_scope_current(parent);
    if (s != NULL) {
        join(child, s);
    }
}

SN_HELD void sn_scope_own(sn_chan *done, sn_task *child, sn_task *parent) {
    _Atomic uintptr_t *word = sn_chan_word(done);
    sn_scope *above = sn_scope_current(parent);
    if (above == NULL) {
        atomic_store_explicit(word, (uintptr_t)child | running, memory_order_relaxed); // published by the spawn
        sn_task_scope(child)->handle = done;
        return;
    }

    sn_scope *s = scope_new(above); // held by the channel
    join(child, s);
    atomic_store_explicit(word, (uintptr_t)s, memory_order_relaxed);
}

// sn_scope_ends is the task's, as it offers its result: its work is
// done, and it leaves its scope, so that the drops of a result no one
// joins and of its handle, which come after, run in none.
SN_HELD void sn_scope_ends(sn_chan *done) {
    _Atomic uintptr_t *word = sn_chan_word(done);
    uintptr_t w = atomic_load_explicit(word, memory_order_relaxed);
    if ((w & running) != 0 && atomic_compare_exchange_strong(word, &w, (uintptr_t)&ended)) {
        scope_leave(
            sn_self()); // it ended with no scope of its own, and no group can take it now: out of the root's list
        return;
    }

    sn_scope *s = (sn_scope *)(w & ~tags);
    if ((w & in_group) != 0) {
        scope_settled(s); // an Add may still be putting the task in its group
    }

    leave_from(sn_self(), s); // its own, which the channel still holds, or its group's, which nothing holds for it now
    if ((w & in_group) != 0) {
        atomic_store(word, (uintptr_t)&ended);
    }
}

SN_HELD void sn_scope_dropped(sn_chan *done) {
    uintptr_t w = atomic_load_explicit(sn_chan_word(done), memory_order_acquire);
    if (w != (uintptr_t)&ended && (w & tags) == 0) {
        sn_scope_release((sn_scope *)w); // the channel's hold
    }
}

SN_HELD void sn_scope_end(sn_task *t) { scope_leave(t); }

// sn_task_enter has the running task enter a scope with a deadline d
// nanoseconds from now under the one it is in, for Within; a d of zero
// or less has passed already.
SN_HELD void sn_task_enter(int64_t d) {
    sn_task *t = sn_self();
    sn_scope *s = scope_new(sn_scope_current(t)); // which holds the scope it is under, the task's way back
    scope_leave(t);
    join(t, s);
    sn_scope_release(s); // the task holds it now
    if (d <= 0) {
        scope_cancel(s, SN_SCOPE_TIMED_OUT);
        return;
    }

    s->timed = true;
    retain(s); // the timer's, let go by the firing or by the leave that took the timer out
    sn_deadline_arm_counted(&s->deadline, d, scope_expire, s);
}

// sn_task_leave has the running task leave the scope sn_task_enter
// entered for the one it was in, which is freed once nothing holds it.
SN_HELD void sn_task_leave(void) {
    sn_task *t = sn_self();
    sn_scope *s = atomic_load_explicit(&sn_task_scope(t)->scope, memory_order_relaxed);
    sn_scope *above = s->above[under_parent];
    retain(s);
    if (s->timed && sn_deadline_disarm(&s->deadline)) {
        sn_scope_release(s); // the timer's hold: the timer was taken out before it fired
    }

    scope_leave(t);
    if (above != NULL && above != &sn_root) { // a task that was in no scope keeps none in its record, as before
        join(t, above);                       // above is held by s until the release below
    }

    sn_scope_release(s);
}

// sn_task_Cancelled reports whether the running task's work is cut
// short: its scope cancelled, or its deadline passed.
SN_HELD bool sn_task_Cancelled(void) { return sn_cancel_state(sn_self()) != SN_SCOPE_LIVE; }

// sn_task_newScope makes a group's scope, under the running task's, and
// returns it as the handle the group holds; sn_task_dropScope lets the
// group's hold go.
SN_HELD int64_t sn_task_newScope(void) { return (int64_t)(intptr_t)scope_new(sn_scope_current(sn_self())); }

SN_HELD void sn_task_dropScope(int64_t h) { sn_scope_release((sn_scope *)(intptr_t)h); }

SN_HELD void sn_task_cancelScope(int64_t h) { scope_cancel((sn_scope *)(intptr_t)h, SN_SCOPE_CANCELLED); }

// reaches reports whether s is up or above it, by either side: a walk
// up the scopes up was made under, and up the group of each on the way,
// which a scope that holds up holds too. The groups still to walk up
// from are kept in a list of the walk's own, a few in its frame and the
// rest in a block of the system's, so that it is no recursion.
SN_HELD_STATIC static bool reaches(sn_scope *up, sn_scope *s) {
    sn_scope *few[few_groups];
    sn_scope **groups = few;
    int n = 0, room = few_groups;
    bool found = false;
    for (;;) {
        while (up != NULL && up != s) {
            sn_scope *g = up->above[under_group];
            if (g != NULL) {
                if (n == room) {
                    sn_scope **more = malloc((size_t)room * 2 * sizeof *more);
                    if (more == NULL) {
                        sn_panic(SN_STR("out of memory", 13));
                    }

                    memcpy(more, groups, (size_t)n * sizeof *more);
                    if (groups != few) {
                        free(groups);
                    }

                    groups = more;
                    room *= 2;
                }

                groups[n++] = g;
            }

            up = up->above[under_parent];
        }

        if (up == s) {
            found = true;
            break;
        }

        if (n == 0) {
            break;
        }

        up = groups[--n];
    }

    if (groups != few) {
        free(groups);
    }

    return found;
}

// group_of is the group that took the task whose handle channel's word
// is w: the scope the task went into, or the group its own scope is
// under; NULL for none.
SN_HELD_STATIC static sn_scope *group_of(uintptr_t w) {
    if ((w & in_group) != 0) {
        return (sn_scope *)(w & ~tags);
    }

    if ((w & running) != 0 || w == (uintptr_t)&ended) {
        return NULL;
    }

    return ((sn_scope *)w)->above[under_group];
}

// group_fail cancels the group g for its task whose handle channel is done,
// which failed, and records it as the first to fail unless another was.
SN_HELD_STATIC static void group_fail(sn_scope *g, sn_chan *done) {
    sn_chan *none = NULL;
    atomic_compare_exchange_strong(&g->first, &none, done);
    scope_cancel(g, SN_SCOPE_CANCELLED);
}

// sn_handle_failed is the failed task's, as its panic is caught: it
// marks its handle channel, and cancels its group when one took it. A
// group that takes it after reads the mark and does the same, so that
// one of the two sees the other: each writes before it reads.
SN_HELD void sn_handle_failed(sn_chan *done) {
    atomic_store(sn_chan_failed(done), true);
    sn_scope *g = group_of(atomic_load(sn_chan_word(done)));
    if (g != NULL) {
        group_fail(g, done);
    }
}

// sn_task_firstFailed reports whether the task whose handle channel is
// done was the first of the group h's tasks to fail.
SN_HELD bool sn_task_firstFailed(int64_t h, sn_chan *done) {
    return atomic_load(&((sn_scope *)(intptr_t)h)->first) == done;
}

// adopt puts the task whose handle channel is done in the group g: in
// g's scope itself when it has no scope of its own yet and still runs,
// and otherwise its own scope under g; a task that has ended with none
// started nothing a cancel could reach. A group made inside that very
// task, which would then be under the scope it takes in, does not take
// it: a cancel is never asked to walk in a circle.
SN_HELD_STATIC static void adopt(sn_scope *g, sn_chan *done) {
    _Atomic uintptr_t *word = sn_chan_word(done);
    uintptr_t w = atomic_load(word);
    sn_scope *s = (sn_scope *)w;
    if (w == (uintptr_t)&ended) {
        return;
    }

    if ((w & running) != 0) {
        // A task that has started nothing yet goes in the group's scope
        // itself, as a task started in it would: no scope of its own to
        // make. The word then holds the group's, which the channel does
        // not hold: the task does, while it is in it.
        sn_qlock_take(&g->mu);
        if (atomic_compare_exchange_strong(word, &w, (uintptr_t)g | in_group)) {
            sn_task *t = (sn_task *)(w & ~running);
            root_unlink(t); // the word says the root's list is no longer its: out of it, under the root's lock
            scope_enlist(t, g);
            if (atomic_load(&g->state) != SN_SCOPE_LIVE) {
                sn_wake(t); // the group is cancelled already, and the task may be parked in a wait no cancel reached
            }

            sn_qlock_give(&g->mu);
            return;
        }

        sn_qlock_give(&g->mu);
        if (w == (uintptr_t)&ended) {
            return;
        }

        s = (sn_scope *)(w & ~tags); // the task made its own meanwhile
    }

    sn_qlock_take(&adopt_mu);
    bool circle = s->above[under_group] != NULL || reaches(g, s);
    if (!circle) {
        link_under(s, g, under_group);
    }

    sn_qlock_give(&adopt_mu);
}

// sn_task_adopt puts the task whose handle channel is done in the group
// h, as adopt says, and fails the group when the task has failed.
SN_HELD void sn_task_adopt(int64_t h, sn_chan *done) {
    sn_scope *g = (sn_scope *)(intptr_t)h;
    adopt(g, done);
    if (atomic_load(sn_chan_failed(done))) {
        group_fail(g, done); // it failed before the group took it, or as it did
    }
}
