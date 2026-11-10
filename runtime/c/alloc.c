// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The allocator under sn_alloc and sn_free, after the Go runtime's
// (malloc.go). A small block is rounded up to one of a few size classes
// and carved from a span, 64 KB of blocks of one class. Each thread
// keeps a list of free blocks per class, as each P in Go keeps its
// mcache, so that an allocation and a free on one thread take no lock;
// the threads' lists meet in a central list per class, Go's mcentral,
// which a thread refills from and gives back to in batches. The spans
// are cut from one reservation of address space made before main, so a
// pointer inside it is a small block whose class is one table read
// away, and a free needs neither a size nor a header. A block larger
// than the largest class, or asked for once the reservation is used
// up, is the system's, and goes back to the system.
//
// A task may move to another thread when it parks, and nothing here
// parks, so each call reads its thread's lists once and is done with
// them before the task can move. A block freed on a thread other than
// the one that made it joins the freeing thread's list, as in tcmalloc.
// Spans are never given back to the system: a program's footprint is
// the most it ever held of each class.

#define _DEFAULT_SOURCE // glibc's mmap flags under -std=c11
#define _DARWIN_C_SOURCE

#include "internal.h"

#include <pthread.h>
#include <stdatomic.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/resource.h>

enum {
    span_shift = 16, // a span is 64 KB
    span_size = 1 << span_shift,
    largest = 1024, // the largest small block
    nclass = 20,
};

// The classes are the multiples of 16 up to 128, then four to each
// doubling, so that rounding up loses less than a fifth of a block.
static const uint16_t class_size[nclass] = {
    16, 32, 48, 64, 80, 96, 112, 128,
    160, 192, 224, 256, 320, 384, 448, 512, 640, 768, 896, 1024,
};

// class_of is the class of a size, indexed by the size in sixteens,
// rounded up: the first class at least that large. It and the batches
// are constants, so that an allocation is right before anything has
// run, a constructor of the runtime's own included.
static const uint8_t class_of[largest / 16 + 1] = {
    0, 0, 1, 2, 3, 4, 5, 6, 7,                      // 0 to 128
    8, 8, 9, 9, 10, 10, 11, 11,                     // to 256
    12, 12, 12, 12, 13, 13, 13, 13,                 // to 384
    14, 14, 14, 14, 15, 15, 15, 15,                 // to 512
    16, 16, 16, 16, 16, 16, 16, 16,                 // to 640
    17, 17, 17, 17, 17, 17, 17, 17,                 // to 768
    18, 18, 18, 18, 18, 18, 18, 18,                 // to 896
    19, 19, 19, 19, 19, 19, 19, 19,                 // to 1024
};

// batch is how many blocks of each class a thread takes from or gives
// to the central list at once: 8 KB's worth, and never fewer than
// eight. A thread's list gives a batch back once it holds two.
static const int32_t batch[nclass] = {
    512, 256, 170, 128, 102, 85, 73, 64,
    51, 42, 36, 32, 25, 21, 18, 16, 12, 10, 9, 8,
};

static uint8_t *base;       // the reservation
static size_t reserved;     // its size; zero if none could be made
static _Atomic size_t used; // what the spans have taken of it
static uint8_t *span_class; // the class of each span, by its number

// The blocks and the bytes sn_alloc has given out are counted for a
// benchmark's allocations per operation, by each thread in counts of
// its own, as Go keeps its allocation statistics by processor, and
// added up when they are asked for. One pair of counts for every thread
// would be one line of memory every processor writes at each
// allocation, and passes to the next: tasks allocating on eight
// processors then ran seventeen times slower than on counts of their
// own. A thread's counts are a line to themselves, written by that
// thread alone and read by any, and outlive the thread, whose
// allocations stay counted.
typedef struct counts {
    _Atomic int64_t blocks, bytes;
    struct counts *next;
} counts;

static _Atomic(counts *) threads; // every thread's counts, newest first
static counts shared;             // the counts of a thread that could be given none

// A free block's first word links it to the next on its list.
typedef struct cache {
    void *head[nclass];
    int32_t count[nclass];
    counts *counts;
} cache;

static _Thread_local cache local;

typedef struct central {
    pthread_mutex_t mu;
    void *head;
} central;

static central centrals[nclass];

// reserve runs before main, while the program has one thread. It asks
// for 64 GB of address space and settles for less, down to 1 GB; the
// pages are the system's to commit as they are touched. A limit on the
// address space, ulimit -v, counts the reservation whole, so under one
// it asks for no more than a quarter of the limit and leaves the rest
// to the tasks' stacks and the system's allocator; under a limit below
// 4 GB it makes none. Without a reservation every block is the system's.
__attribute__((constructor)) static void reserve(void) {
    for (int i = 0; i < nclass; i++) {
        pthread_mutex_init(&centrals[i].mu, NULL);
    }

    size_t most = (size_t)1 << 36;
    struct rlimit lim;
    if (getrlimit(RLIMIT_AS, &lim) == 0 && lim.rlim_cur != RLIM_INFINITY) {
        while (most > (size_t)lim.rlim_cur / 4) {
            most >>= 1;
        }
    }

    int flags = MAP_PRIVATE | MAP_ANONYMOUS;
#ifdef MAP_NORESERVE
    flags |= MAP_NORESERVE;
#endif
    for (size_t size = most; size >= (size_t)1 << 30; size >>= 1) {
        void *p = mmap(NULL, size, PROT_READ | PROT_WRITE, flags, -1, 0);
        if (p == MAP_FAILED) {
            continue;
        }

        void *t = mmap(NULL, size >> span_shift, PROT_READ | PROT_WRITE, flags, -1, 0);
        if (t == MAP_FAILED) {
            munmap(p, size);
            continue;
        }

        base = p;
        span_class = t;
        reserved = size;
        return;
    }
}

// carve takes a new span for class c and links its blocks into a list,
// whose first block it returns with the list's length in n; nothing
// when the reservation is used up.
static void *carve(int c, int32_t *n) {
    size_t at = atomic_fetch_add(&used, (size_t)span_size);
    if (at >= reserved) {
        return NULL;
    }

    uint8_t *s = base + at;
    span_class[at >> span_shift] = (uint8_t)c;
    size_t size = class_size[c];
    int32_t m = (int32_t)(span_size / size);
    for (int32_t i = 0; i < m - 1; i++) {
        *(void **)(s + (size_t)i * size) = s + (size_t)(i + 1) * size;
    }

    *(void **)(s + (size_t)(m - 1) * size) = NULL;
    *n = m;
    return s;
}

// refill gives the thread's empty list for class c a batch from the
// central list, or a new span when that is empty, and returns one block
// of it. A new span goes whole to the thread that cut it, more than a
// batch; its list gives the surplus back a batch at a time as it frees.
// The central list is walked under its lock, a batch's length of
// pointers, which costs nothing on one thread; whether it costs under
// contention is the contention benchmark's to say.
static void *refill(cache *k, int c) {
    central *z = &centrals[c];
    int32_t want = batch[c];
    int32_t n = 0;
    pthread_mutex_lock(&z->mu);
    void *first = z->head;
    void *last = NULL;
    for (void *p = first; p != NULL && n < want; p = *(void **)p) {
        last = p;
        n++;
    }

    if (n > 0) {
        z->head = *(void **)last;
        *(void **)last = NULL;
    }

    pthread_mutex_unlock(&z->mu);
    if (n == 0) {
        first = carve(c, &n);
        if (first == NULL) {
            return malloc(class_size[c]);
        }
    }

    k->head[c] = *(void **)first;
    k->count[c] = n - 1;
    return first;
}

// give_back moves a batch from the thread's list for class c, grown past
// twice a batch, to the central list.
static void give_back(cache *k, int c) {
    int32_t m = batch[c];
    void *first = k->head[c];
    void *last = first;
    for (int32_t i = 1; i < m; i++) {
        last = *(void **)last;
    }

    k->head[c] = *(void **)last;
    k->count[c] -= m;
    central *z = &centrals[c];
    pthread_mutex_lock(&z->mu);
    *(void **)last = z->head;
    z->head = first;
    pthread_mutex_unlock(&z->mu);
}

// A task may resume on another thread after any switch, and a compiler
// may work out the address of a thread's own variable once in a
// function and keep it across a call: a task into whose function the
// allocator was written would then take from, give to and count in the
// cache of the thread it left, while that thread used it, and the lists
// of free blocks would be torn. So the thread's cache is read only in
// the functions marked ENTRY, which the compiler may not write into
// their callers: each reads it afresh, and none parks a task. The
// helpers below them read it too, and are written into an ENTRY
// function and nowhere else.
#define ENTRY __attribute__((noinline))

static inline void *heap_alloc(size_t n) {
    if (n > largest) {
        return malloc(n);
    }

    int c = class_of[(n + 15) >> 4];
    cache *k = &local;
    void *p = k->head[c];
    if (p == NULL) {
        return refill(k, c);
    }

    k->head[c] = *(void **)p;
    k->count[c]--;
    return p;
}

static inline void heap_free(void *p) {
    uintptr_t off = (uintptr_t)p - (uintptr_t)base;
    if (off >= reserved) { // the system's block, or NULL
        free(p);
        return;
    }

    int c = span_class[off >> span_shift];
    cache *k = &local;
    *(void **)p = k->head[c];
    k->head[c] = p;
    if (++k->count[c] > 2 * batch[c]) {
        give_back(k, c);
    }
}

ENTRY void *sn_heap_alloc(size_t n) {
    return heap_alloc(n);
}

ENTRY void sn_heap_free(void *p) {
    heap_free(p);
}

// enlist gives the thread its counts, a line of memory of their own
// from the system, 128 bytes since the widest line of a processor
// Snello runs on is that, and adds them to the list every thread's are
// on.
static __attribute__((noinline, cold)) counts *enlist(cache *k) {
    void *line = NULL;
    if (posix_memalign(&line, 128, 128) != 0) {
        return k->counts = &shared;
    }

    counts *c = memset(line, 0, 128);
    c->next = atomic_load(&threads);
    while (!atomic_compare_exchange_weak(&threads, &c->next, c)) {
    }

    return k->counts = c;
}

// note counts a block of n bytes given out, in the thread's own counts:
// a load and a store of each, with no lock on the line, since no other
// thread writes it.
static inline void note(size_t n) {
    cache *k = &local;
    counts *c = k->counts;
    if (c == NULL) {
        c = enlist(k);
    }

    if (c == &shared) {
        atomic_fetch_add_explicit(&c->blocks, 1, memory_order_relaxed);
        atomic_fetch_add_explicit(&c->bytes, (int64_t)n, memory_order_relaxed);
        return;
    }

    atomic_store_explicit(&c->blocks, atomic_load_explicit(&c->blocks, memory_order_relaxed) + 1, memory_order_relaxed);
    atomic_store_explicit(&c->bytes, atomic_load_explicit(&c->bytes, memory_order_relaxed) + (int64_t)n, memory_order_relaxed);
}

ENTRY void sn_alloc_note(size_t n) {
    note(n);
}

// sn_alloc_counts adds up the blocks and the bytes every thread has
// been given.
void sn_alloc_counts(int64_t *blocks, int64_t *bytes) {
    *blocks = atomic_load_explicit(&shared.blocks, memory_order_relaxed);
    *bytes = atomic_load_explicit(&shared.bytes, memory_order_relaxed);
    for (counts *c = atomic_load(&threads); c != NULL; c = c->next) {
        *blocks += atomic_load_explicit(&c->blocks, memory_order_relaxed);
        *bytes += atomic_load_explicit(&c->bytes, memory_order_relaxed);
    }
}

// The allocator of a release build; debug.c has the poisoning one of a
// debug build, over the same heap and with the same counts.
#ifndef SN_DEBUG
ENTRY void *sn_alloc(size_t n) {
    void *p = heap_alloc(n);
    if (p == NULL) {
        sn_panic(SN_STR("out of memory", 13));
    }

    note(n);
    return p;
}

ENTRY void sn_free(void *p) {
    heap_free(p);
}
#endif
