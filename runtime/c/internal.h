// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// What the runtime's files share and the program's C does not see: the
// helpers snello.c defines for the others, and the types and the lock of
// the queues a task waits on, which chan.c and sync.c both use. The
// names begin with sn_, as snello.h's do, since a function here is
// external and meets the program's names and the system's when linked.
// A file of the runtime includes this header first, after the feature
// macros it asks for.
#ifndef SN_INTERNAL_H
#define SN_INTERNAL_H

#include "snello.h"

#include <pthread.h>
#include <stdatomic.h>
#ifdef __APPLE__
#include <os/lock.h>
#endif

// SN_CACHE_LINE is the widest cache line of the processors the runtime
// runs on, Apple's arm64 cores', which data written by one thread and
// read by others is aligned to so that it shares a line with nothing
// else.
#define SN_CACHE_LINE 128

// The most one read or write asks of the system, as Go's runtime caps
// it: Darwin refuses a count past INT_MAX, so a larger buffer is read
// or written in turns.
#define SN_IO_MAX ((size_t)1 << 30)

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
} sn_wait_state;

typedef struct sn_waiter {
    sn_task *t;
    void *elem; // the value sent, or the place to receive into
    sn_wait_state *ws;
    int index;    // the case, for a select
    bool success; // a value moved; false when a close woke the waiter
    bool queued;
    struct sn_waiter *prev;
    struct sn_waiter *next;
} sn_waiter;

typedef struct {
    sn_waiter *head;
    sn_waiter *tail;
} sn_waitq;

// sn_qlock is the lock over the queues of a channel or of a lock. It is
// held for a few instructions, by tasks on every worker at once when
// many send to one channel, and never across a park: the system's
// fastest lock, os_unfair_lock on Darwin, whose pthread mutex is fair
// and hands off through the kernel, and glibc's pthread mutex on Linux,
// a futex with no system call when uncontended. A task never parks
// holding it, so the thread that takes it is the one that gives it
// back, as os_unfair_lock requires.
#ifdef __APPLE__
typedef os_unfair_lock sn_qlock;
static inline void sn_qlock_init(sn_qlock *l) { *l = OS_UNFAIR_LOCK_INIT; }
static inline void sn_qlock_take(sn_qlock *l) { os_unfair_lock_lock(l); }
static inline void sn_qlock_give(sn_qlock *l) { os_unfair_lock_unlock(l); }
static inline void sn_qlock_free(sn_qlock *l) { (void)l; }
#else
typedef pthread_mutex_t sn_qlock;
static inline void sn_qlock_init(sn_qlock *l) { pthread_mutex_init(l, NULL); }
static inline void sn_qlock_take(sn_qlock *l) { pthread_mutex_lock(l); }
static inline void sn_qlock_give(sn_qlock *l) { pthread_mutex_unlock(l); }
static inline void sn_qlock_free(sn_qlock *l) { pthread_mutex_destroy(l); }
#endif

// sn_spin_hint tells the core a loop is waiting on memory.
static inline void sn_spin_hint(void) {
#if defined(__aarch64__)
    __asm__ volatile("yield");
#elif defined(__x86_64__)
    __asm__ volatile("pause");
#endif
}

#ifdef SN_DEBUG
int64_t sn_two_way(sn_string s, sn_string t);
int64_t sn_two_way_last(sn_string s, sn_string t);
void sn_rand_key(const uint8_t seed[32]);
extern bool sn_rand_no_system;
#endif

// sn_digit_pairs is 00 to 99, two digits each, one after another: what
// a number is written from two digits at a time.
extern const char sn_digit_pairs[201];

// sn_slice_grow makes room in s for at least need elements of size
// bytes, doubling, as Push amortizes to constant time.
void sn_slice_grow(sn_slice *s, int64_t need, size_t size);

// sn_format_uint writes u's decimal digits ending at end and returns
// where they begin; sn_format_int makes the owned string of u, negated
// when neg.
char *sn_format_uint(uint64_t u, char *end);
sn_string sn_format_int(uint64_t u, bool neg);

// A number as decimal digits with a point among them, which
// multiplies and divides by powers of two exactly: decimal.c's.
// sn_decimal_assign sets one to an integer, sn_decimal_shift
// multiplies it by 2^k, or divides it for a negative k,
// sn_decimal_round cuts it to nd digits, rounded to nearest with a tie
// to the even digit, and sn_decimal_rounds_up says which way that cut
// goes.
enum { SN_DECIMAL_DIGITS = 800 };

typedef struct {
    uint8_t d[SN_DECIMAL_DIGITS]; // the digits, the most significant first
    int nd;                       // how many
    int64_t dp;                   // the point's place among them
    bool neg;
    bool trunc; // digits that were not zero lie past d
} sn_decimal;

void sn_decimal_assign(sn_decimal *a, uint64_t v);
void sn_decimal_shift(sn_decimal *a, int k);
void sn_decimal_round(sn_decimal *a, int64_t nd);
bool sn_decimal_rounds_up(const sn_decimal *a, int64_t nd);

// sn_write_fd writes every byte of p to fd and returns how many it
// wrote, all of len or fewer with errno saying why.
int64_t sn_write_fd(int fd, const uint8_t *p, int64_t len);

// sn_next_random is the calling thread's next random number.
uint64_t sn_next_random(void);

// sn_cores is the number of processors the program may run on, which
// the scheduler starts a worker per and os.Cores reports. sched.c's.
int64_t sn_cores(void);

// sn_alloc_note counts a block of n bytes that sn_alloc gives out, in a
// release build and a debug build alike, and sn_alloc_counts adds up
// the blocks and the bytes given out so far, for a benchmark's
// allocations per operation. alloc.c's, which keeps the counts by
// thread.
void sn_alloc_note(size_t n);
void sn_alloc_counts(int64_t *blocks, int64_t *bytes);

// sn_task_places returns the running task's slots for the place of a
// test's call: the file and line of a testing.Log, the file, line and
// text of an expect call, and that call's parts, owned. sched.c's,
// filled and read by testing.c, the parts freed with the task.
enum { SN_PLACE_LINE, SN_PLACE_CALL, SN_PLACE_PARTS };
sn_string *sn_task_places(void);

// The timers, timer.c's. A deadline is where a timer of the runtime's
// own is kept: the timer while it is in a wheel, and the wheel it goes
// in, the index and one of the worker's that first armed it, 0 before.
// sn_deadline_arm calls fire(arg) d nanoseconds from now, on whichever
// thread fires the timer, and arming it again moves it; sn_deadline_disarm
// takes it out, and once that returns, fire is not running and will not
// run. A deadline starts as all zero.
typedef struct {
    void *timer;
    _Atomic int32_t home;
} sn_deadline;

void sn_deadline_arm(sn_deadline *at, int64_t d, void (*fire)(void *), void *arg);
void sn_deadline_disarm(sn_deadline *at);

// What the scheduler asks of the timers: a wheel per worker made before
// any thread but the first runs, the earliest deadline any wheel holds
// and one wheel's, firing what is due at now, on the calling thread, at
// every wheel or at one worker's, and how many timers have yet to wake
// what they wake.
void sn_timers_start(int64_t nworkers);
int64_t sn_timers_next(void);
int64_t sn_timers_next_of(int64_t worker);
bool sn_timers_fire(int64_t now);
bool sn_timers_fire_own(int64_t worker, int64_t now);
int64_t sn_timers_pending(void);

// What the timers ask of the scheduler, sched.c's: the workers started,
// the index of the current thread's worker, a wake in two halves (a
// parked task claimed, which the claimer alone may then queue, so that
// a timer's lock is held across no queue), a look by the idle workers
// at whether any task can still be woken, and someone to wake for a
// timer started at when: now, or, for a task that parks on its own
// timer, once its worker is on its way to its next task.
void sn_sched_start(void);
int64_t sn_worker_index(void);
bool sn_wake_claim(sn_task *t);
void sn_wake_queue(sn_task *t);
void sn_sched_recheck(void);
void sn_sched_timer_set(int64_t when);
void sn_sched_owe(int64_t when);

// A handoff is how a wake reaches a task from a thread that holds no
// lock the task takes after its wait: a timer that fires, the poller's
// thread, a resolver's. Such a waker finds the task in a word and takes
// it out, and the task may then go on without parking, after a wake
// kept from an earlier wait or a look at a flag the waker set first,
// end, and be freed, while the waker has yet to wake it. So the waker
// wakes through the handoff, which says waking before the wake reads
// the task and woken once nothing of the task is read but to queue a
// task claimed, which is runnable and running nowhere and cannot be
// freed; and a task a waker took out leaves its wait only once the
// handoff says woken, parking while it is open and spinning the few
// instructions it says waking. A handoff starts open, and the task
// opens it again before it next puts itself where a waker finds it.
typedef _Atomic int sn_handoff;
enum { SN_HANDOFF_OPEN = 0, SN_HANDOFF_WAKING = 1, SN_HANDOFF_WOKEN = 2 };
void sn_handoff_wake(sn_handoff *h, sn_task *t);
void sn_handoff_wait(sn_handoff *h);

// sn_chan_home is where the channel records the wheel its timers go in,
// for timer.c; chan.c's.
_Atomic int32_t *sn_chan_home(sn_chan *c);

// The queues of waiters: sn_enqueue adds w at the tail of q and
// sn_remove_waiter takes it out wherever it is; sn_dequeue takes the
// first waiter whose wait is still open and claims it; sn_finish_wait
// ends a wait sn_dequeue took and wakes its task; sn_await_wait parks
// until an operation has finished the wait.
void sn_enqueue(sn_waitq *q, sn_waiter *w);
void sn_remove_waiter(sn_waitq *q, sn_waiter *w);
sn_waiter *sn_dequeue(sn_waitq *q);
void sn_finish_wait(sn_waiter *w, bool success);
void sn_await_wait(sn_wait_state *ws);

// Unicode's properties, as mkunicode.go numbers them in unicode.c: the
// general categories, the grapheme, word and sentence break properties
// and Indic_Conjunct_Break. Each list is in the generator's order, and
// the generator stops on a value it does not know.
enum {
    SN_GC_Cn, SN_GC_Lu, SN_GC_Ll, SN_GC_Lt, SN_GC_Lm, SN_GC_Lo, SN_GC_Mn, SN_GC_Mc, SN_GC_Me, SN_GC_Nd,
    SN_GC_Nl, SN_GC_No, SN_GC_Pc, SN_GC_Pd, SN_GC_Ps, SN_GC_Pe, SN_GC_Pi, SN_GC_Pf, SN_GC_Po, SN_GC_Sm,
    SN_GC_Sc, SN_GC_Sk, SN_GC_So, SN_GC_Zs, SN_GC_Zl, SN_GC_Zp, SN_GC_Cc, SN_GC_Cf, SN_GC_Cs, SN_GC_Co,
};

enum {
    SN_GCB_Other, SN_GCB_CR, SN_GCB_LF, SN_GCB_Control, SN_GCB_Extend, SN_GCB_ZWJ, SN_GCB_RI,
    SN_GCB_Prepend, SN_GCB_SpacingMark, SN_GCB_L, SN_GCB_V, SN_GCB_T, SN_GCB_LV, SN_GCB_LVT,
};

enum {
    SN_WB_Other, SN_WB_CR, SN_WB_LF, SN_WB_Newline, SN_WB_Extend, SN_WB_ZWJ, SN_WB_RI, SN_WB_Format,
    SN_WB_Katakana, SN_WB_Hebrew_Letter, SN_WB_ALetter, SN_WB_Single_Quote, SN_WB_Double_Quote,
    SN_WB_MidNumLet, SN_WB_MidLetter, SN_WB_MidNum, SN_WB_Numeric, SN_WB_ExtendNumLet, SN_WB_WSegSpace,
};

enum {
    SN_SB_Other, SN_SB_CR, SN_SB_LF, SN_SB_Extend, SN_SB_Sep, SN_SB_Format, SN_SB_Sp, SN_SB_Lower,
    SN_SB_Upper, SN_SB_OLetter, SN_SB_Numeric, SN_SB_ATerm, SN_SB_SContinue, SN_SB_STerm, SN_SB_Close,
};

enum { SN_INCB_None, SN_INCB_Linker, SN_INCB_Consonant, SN_INCB_Extend };

// The derived properties sn_uc_derived gives, as bits, and the
// normalization quick-check values sn_uc_quick gives that are not Yes.
enum {
    SN_UC_ALPHABETIC = 1 << 0,
    SN_UC_UPPERCASE = 1 << 1,
    SN_UC_LOWERCASE = 1 << 2,
    SN_UC_XID_START = 1 << 3,
    SN_UC_XID_CONTINUE = 1 << 4,
    SN_UC_EMOJI = 1 << 5,
};

enum {
    SN_QC_NFD_NO = 1 << 0,
    SN_QC_NFC_NO = 1 << 1,
    SN_QC_NFC_MAYBE = 1 << 2,
    SN_QC_NFKD_NO = 1 << 3,
    SN_QC_NFKC_NO = 1 << 4,
    SN_QC_NFKC_MAYBE = 1 << 5,
};

// The kinds of full mapping sn_uc_special reads.
enum { SN_UC_UPPER, SN_UC_LOWER, SN_UC_TITLE, SN_UC_FOLD };

// The lookups of unicode.c the hand-written algorithms read: the
// category and script of a rune, by number, and their names; a full
// mapping of SpecialCasing.txt or CaseFolding.txt into out, returning
// its length, or 0 when the simple mapping holds; the Cased and
// Case_Ignorable properties; the combining class; a decomposition,
// canonical alone unless compat, returning its length and setting out;
// the canonical composition of a pair, or -1; and the break properties.
int sn_rune_category(int32_t r);
const char *sn_category_name(int c);
int sn_rune_script(int32_t r);
const char *sn_script_name(int s);
int sn_script_count(void);
const char *sn_script_code(int s);
int sn_uc_derived(int32_t r);
int sn_uc_quick(int32_t r);
int sn_uc_decimal(int32_t r);
bool sn_uc_numeric(int32_t r, int64_t *num, int32_t *den);
int32_t sn_rune_title(int32_t r);
int32_t sn_rune_fold(int32_t r);
int sn_uc_special(int kind, int32_t r, int32_t out[3]);
bool sn_uc_cased(int32_t r);
bool sn_uc_case_ignorable(int32_t r);
int sn_uc_ccc(int32_t r);
int sn_uc_decomp(int32_t r, bool compat, const int32_t **out);
int32_t sn_uc_compose(int32_t a, int32_t b);
int sn_uc_gcb(int32_t r);
int sn_uc_wb(int32_t r);
int sn_uc_sb(int32_t r);
bool sn_uc_extpict(int32_t r);
int sn_uc_incb(int32_t r);

// What the net package's runtime calls return beside the system's error
// numbers, net.snello's codes: a host that is not an IP address but a
// name, a deadline or a timeout passed, a name with no address, and a
// failure of the resolver's own, whose reason it stores.
enum { SN_NET_NOT_ADDRESS = -1, SN_NET_TIMED_OUT = -2, SN_NET_NO_SUCH_HOST = -3, SN_NET_RESOLVER_FAILED = -4 };

// Addresses as text, lookup.c's, for the sockets of net.c: an IP
// address read into a socket address with its port, returning the
// address's length, 0 for a host that is not one, and a socket address
// printed as Go prints it, with its port or without, as an owned string.
struct sockaddr;
struct sockaddr_storage;
int sn_net_parse(sn_string host, int64_t port, struct sockaddr_storage *addr);
sn_string sn_net_format(const struct sockaddr *addr, bool port);

#endif
