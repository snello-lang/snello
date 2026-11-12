// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The runtime's core: the start and the end of a program, its panics
// and the messages of its bounds checks, and what more than one of the
// runtime's files uses, declared in internal.h: a slice's growth, a
// rune's UTF-8, an integer's decimal digits, a write of every byte to a
// descriptor, a random number per thread, and the queues a task waits
// on for a channel or a lock. Each part of the library's runtime has a
// file of its own: slice.c, sort.c, string.c, format.c, quote.c,
// parse.c, map.c, bufio.c, os.c, print.c, math.c, time.c, chan.c and
// sync.c.

#define _POSIX_C_SOURCE 200809L
#define _DARWIN_C_SOURCE

#include "internal.h"

#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/resource.h>
#include <unistd.h>

sn_slice sn_os_Args = {0};

// The allocator is alloc.c's; a release build has nothing to check at
// the end, and debug.c has the report of a debug build.
#ifndef SN_DEBUG
int sn_finish(void) {
    sn_string_drop(&sn_task_places()[SN_PLACE_PARTS]); // the main task's, which no scheduler teardown frees
    return 0;
}
#endif

void sn_release(void **p) {
    sn_free(*p);
    *p = NULL;
    SN_POISON(*p);
}

_Noreturn void sn_exit(int code) {
    fflush(stdout);
    exit(code);
}

_Noreturn void sn_bounds(int64_t i, int64_t len) {
    char buf[96];
    snprintf(buf, sizeof buf, "index out of range [%lld] with length %lld", (long long)i, (long long)len);
    sn_panic(SN_STR(buf, (int64_t)strlen(buf)));
}

// sn_slice_grow makes room for at least need elements, doubling as Snello's
// Push amortizes to constant time.
void sn_slice_grow(sn_slice *s, int64_t need, size_t size) {
    sn_check_slice(s);
    if (need <= s->cap) {
        return;
    }

    int64_t cap = s->cap < 4 ? 4 : s->cap * 2;
    if (cap < need) {
        cap = need;
    }

    void *p = sn_alloc((size_t)cap * size);
    if (s->len > 0) {
        memcpy(p, s->ptr, (size_t)s->len * size);
    }

    sn_free(s->ptr);
    s->ptr = p;
    s->cap = cap;
}

// Integers are written as Go's strconv.formatBits writes them: two
// digits a step from a table, from the end of a buffer on the stack,
// then copied once into a block of exactly their length. 0 to 99 are
// Go's small numbers, views of the table that own nothing.
const char sn_digit_pairs[201] = "00010203040506070809101112131415161718192021222324252627282930313233343536373839404142434445464748495051525354555657585960616263646566676869707172737475767778798081828384858687888990919293949596979899";

char *sn_format_uint(uint64_t u, char *end) {
    while (u >= 100) {
        uint64_t two = (u % 100) * 2;
        u /= 100;
        *--end = sn_digit_pairs[two + 1];
        *--end = sn_digit_pairs[two];
    }

    if (u >= 10) {
        *--end = sn_digit_pairs[u * 2 + 1];
        *--end = sn_digit_pairs[u * 2];
    } else {
        *--end = (char)('0' + u);
    }

    return end;
}

sn_string sn_format_int(uint64_t u, bool neg) {
    if (!neg && u < 100) {
        return SN_STR(sn_digit_pairs + u * 2 + (u < 10), u < 10 ? 1 : 2);
    }

    char buf[24];
    char *end = buf + sizeof buf, *p = sn_format_uint(u, end);
    if (neg) {
        *--p = '-';
    }

    size_t n = (size_t)(end - p);
    uint8_t *q = sn_alloc(n);
    memcpy(q, p, n);
    return SN_OWNED(q, (int64_t)n);
}

// sn_utf8_decode reads the rune at p, at most n bytes long, as Go's
// utf8.DecodeRune does: a sequence that is not valid UTF-8, cut short,
// overlong or a surrogate, reads as the replacement rune U+FFFD, one
// byte wide.
int sn_utf8_decode(const uint8_t *p, int64_t n, int32_t *r) {
    uint8_t c = p[0];
    if (c < 0x80) {
        *r = c;
        return 1;
    }

    int w;
    int32_t v;
    uint8_t lo = 0x80, hi = 0xBF; // the second byte's range
    if (c >= 0xC2 && c <= 0xDF) {
        w = 2;
        v = c & 0x1F;
    } else if (c >= 0xE0 && c <= 0xEF) {
        w = 3;
        v = c & 0x0F;
        lo = c == 0xE0 ? 0xA0 : 0x80;
        hi = c == 0xED ? 0x9F : 0xBF;
    } else if (c >= 0xF0 && c <= 0xF4) {
        w = 4;
        v = c & 0x07;
        lo = c == 0xF0 ? 0x90 : 0x80;
        hi = c == 0xF4 ? 0x8F : 0xBF;
    } else {
        *r = 0xFFFD;
        return 1;
    }

    if (n < w || p[1] < lo || p[1] > hi) {
        *r = 0xFFFD;
        return 1;
    }

    v = v << 6 | (p[1] & 0x3F);
    for (int i = 2; i < w; i++) {
        if ((p[i] & 0xC0) != 0x80) {
            *r = 0xFFFD;
            return 1;
        }

        v = v << 6 | (p[i] & 0x3F);
    }

    *r = v;
    return w;
}

// sn_utf8_encode writes r as UTF-8 at out and returns its length, and
// writes U+FFFD for a value that is not a code point UTF-8 can hold.
int sn_utf8_encode(int32_t r, uint8_t *out) {
    if (r < 0 || r > 0x10FFFF || (r >= 0xD800 && r <= 0xDFFF)) {
        r = 0xFFFD; // not a code point UTF-8 can hold
    }

    if (r < 0x80) {
        out[0] = (uint8_t)r;
        return 1;
    }

    if (r < 0x800) {
        out[0] = (uint8_t)(0xC0 | r >> 6);
        out[1] = (uint8_t)(0x80 | (r & 0x3F));
        return 2;
    }

    if (r < 0x10000) {
        out[0] = (uint8_t)(0xE0 | r >> 12);
        out[1] = (uint8_t)(0x80 | (r >> 6 & 0x3F));
        out[2] = (uint8_t)(0x80 | (r & 0x3F));
        return 3;
    }

    out[0] = (uint8_t)(0xF0 | r >> 18);
    out[1] = (uint8_t)(0x80 | (r >> 12 & 0x3F));
    out[2] = (uint8_t)(0x80 | (r >> 6 & 0x3F));
    out[3] = (uint8_t)(0x80 | (r & 0x3F));
    return 4;
}

void sn_init(int argc, char **argv) {
    sn_hash_init(); // before any map hashes a key
    // A program started with one of the standard streams closed has
    // its descriptor free, and the first file the program opened would
    // take it: read as stdin, or written by every print. As Go's
    // runtime does, a closed one is opened on /dev/null first; open
    // takes the lowest free descriptor, which is the closed one.
    for (int fd = 0; fd <= 2; fd++) {
        if (fcntl(fd, F_GETFD) < 0 && errno == EBADF) {
            (void)open("/dev/null", fd == 0 ? O_RDONLY : O_WRONLY);
        }
    }

    // The descriptors a process may hold open are raised to the most
    // the system allows, as Go's runtime raises them at start: the
    // soft limit is a default from the days of select, and a removal
    // holds a directory open per level of the tree it walks. Darwin
    // refuses a limit above its OPEN_MAX.
    struct rlimit lim;
    if (getrlimit(RLIMIT_NOFILE, &lim) == 0 && lim.rlim_cur != lim.rlim_max) {
        lim.rlim_cur = lim.rlim_max;
#if defined(__APPLE__)
        if (lim.rlim_cur > OPEN_MAX) {
            lim.rlim_cur = OPEN_MAX;
        }
#endif
        (void)setrlimit(RLIMIT_NOFILE, &lim);
    }

    for (int i = 0; i < argc; i++) {
        sn_string arg = SN_STR(argv[i], (int64_t)strlen(argv[i]));
        sn_slice_push(&sn_os_Args, &arg, sizeof arg);
    }
}

// sn_write_fd writes every byte of p, asking again after a short write or
// an interrupted call, and returns how many it wrote: all of len, or
// fewer with errno saying why.
int64_t sn_write_fd(int fd, const uint8_t *p, int64_t len) {
    int64_t done = 0;
    while (done < len) {
        size_t want = (size_t)(len - done);
        ssize_t n = write(fd, p + done, want < SN_IO_MAX ? want : SN_IO_MAX);
        if (n < 0) {
            if (errno == EINTR) {
                continue;
            }

            return done;
        }

        done += n;
    }

    return done;
}

// The queues a task waits on for a channel or a lock, whose waiters
// internal.h lays out. sn_enqueue adds w at the tail of q, and
// sn_remove_waiter takes it out from wherever it is.
void sn_enqueue(sn_waitq *q, sn_waiter *w) {
    w->next = NULL;
    w->prev = q->tail;
    if (q->tail != NULL) {
        q->tail->next = w;
    } else {
        q->head = w;
    }

    q->tail = w;
    w->queued = true;
}

void sn_remove_waiter(sn_waitq *q, sn_waiter *w) {
    if (!w->queued) {
        return;
    }

    if (w->prev != NULL) {
        w->prev->next = w->next;
    } else {
        q->head = w->next;
    }

    if (w->next != NULL) {
        w->next->prev = w->prev;
    } else {
        q->tail = w->prev;
    }

    w->queued = false;
}

// sn_dequeue takes the first waiter whose wait is still open off the
// queue, and claims its wait; the waiters of selects other operations
// took first are dropped on the way.
sn_waiter *sn_dequeue(sn_waitq *q) {
    for (sn_waiter *w = q->head; w != NULL; w = q->head) {
        sn_remove_waiter(q, w);
        int open = 0;
        if (atomic_compare_exchange_strong(&w->ws->claimed, &open, 1)) {
            return w;
        }
    }

    return NULL;
}

// sn_finish_wait ends a wait sn_dequeue took and wakes its task. The
// caller holds the lock of the queue the waiter was on.
void sn_finish_wait(sn_waiter *w, bool success) {
    w->success = success;
    w->ws->fired = w->index;
    sn_task *t = w->t;
    atomic_store_explicit(&w->ws->done, 1, memory_order_release);
    sn_wake(t);
}

// sn_await_wait parks until an operation has finished the wait; a wake
// kept from an earlier wait costs one more park.
void sn_await_wait(sn_wait_state *ws) {
    while (!atomic_load_explicit(&ws->done, memory_order_acquire)) {
        sn_park();
    }
}

// A random number per worker thread: a select's random order of its
// cases, so that a busy channel does not starve the others, and the
// names os.MkdirTemp tries. A small generator, seeded from its address
// and the clock, read through a call of its own so that a select
// resumed on another thread reads that thread's (sched.c says why).
static _Thread_local uint64_t rng;

__attribute__((noinline)) uint64_t sn_next_random(void) { // never inlined into sn_select, whose task may move threads at its park
    if (rng == 0) {
        rng = ((uint64_t)(uintptr_t)&rng ^ (uint64_t)sn_time_now()) | 1;
    }

    rng ^= rng << 13;
    rng ^= rng >> 7;
    rng ^= rng << 17;
    return rng;
}
