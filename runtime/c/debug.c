// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The debug build's allocator and checks, compiled into every program
// and empty unless SN_DEBUG is defined, which snello build -debug does.
// Every block carries a header saying how large it is and whether it
// is live; a released block is filled with the poison byte and held
// in a quarantine before it goes back to alloc.c's heap, so that a read
// through a stale pointer sees poison instead of whatever the block
// was reused for; and the program fails at its end when blocks are
// still live, which is a drop the compiler forgot.

#include "internal.h"

#ifdef SN_DEBUG

#include <pthread.h>
#include <stdatomic.h>
#include <stdio.h>

#define LIVE 0x4C49564500000000ULL
#define DEAD 0x4445414400000000ULL

// A live block's header links it into the list of live blocks, which
// the report at the end walks.
typedef struct block {
    size_t size;
    uint64_t magic;
    struct block *prev, *next;
} block;

static int64_t live_blocks, live_bytes;
static block *first;

// The quarantine holds released blocks in a ring, oldest out first;
// it is bounded in blocks and in bytes so a debug build of a program
// that churns through memory still runs.
enum { ring = 1024, limit = 16 << 20 };

static block *held[ring];
static int next;
static size_t held_bytes;

// One lock covers the list, the counts and the quarantine, which every
// task shares.
static pthread_mutex_t lock = PTHREAD_MUTEX_INITIALIZER;

// The header adds 32 bytes, so a block of 993 to 1024 bytes, the
// heap's largest class in a release build, is the system's in this one.
_Atomic int64_t sn_alloc_count, sn_alloc_total;

void *sn_alloc(size_t n) {
    block *b = sn_heap_alloc(sizeof *b + (n == 0 ? 1 : n));
    if (b == NULL) {
        sn_panic(SN_STR("out of memory", 13));
    }

    atomic_fetch_add_explicit(&sn_alloc_count, 1, memory_order_relaxed);
    atomic_fetch_add_explicit(&sn_alloc_total, (int64_t)n, memory_order_relaxed);
    pthread_mutex_lock(&lock);
    b->size = n;
    b->magic = LIVE;
    b->prev = NULL;
    b->next = first;
    if (first != NULL) {
        first->prev = b;
    }

    first = b;
    live_blocks++;
    live_bytes += (int64_t)n;
    pthread_mutex_unlock(&lock);
    return b + 1;
}

// evict gives the block in slot i, if any, back to the heap.
static void evict(int i) {
    block *old = held[i];
    if (old != NULL) {
        held_bytes -= old->size;
        sn_heap_free(old);
        held[i] = NULL;
    }
}

void sn_free(void *p) {
    if (p == NULL) {
        return;
    }

    block *b = (block *)p - 1;
    pthread_mutex_lock(&lock);
    if (b->magic == DEAD) {
        sn_panic(SN_STR("a block was released twice", 26));
    }

    if (b->magic != LIVE) {
        sn_panic(SN_STR("a pointer not from the allocator was released", 45));
    }

    b->magic = DEAD;
    if (b->prev != NULL) {
        b->prev->next = b->next;
    } else {
        first = b->next;
    }

    if (b->next != NULL) {
        b->next->prev = b->prev;
    }

    live_blocks--;
    live_bytes -= (int64_t)b->size;
    memset(p, 0xDD, b->size);
    evict(next); // the slot b takes held the oldest block
    held[next] = b;
    held_bytes += b->size;
    next = (next + 1) % ring;
    for (int i = next, n = 0; held_bytes > limit && n < ring; i = (i + 1) % ring, n++) {
        evict(i); // oldest first, until the quarantine is under its limit
    }

    pthread_mutex_unlock(&lock);
}

_Noreturn void sn_released(const char *what) {
    char buf[64];
    snprintf(buf, sizeof buf, "a released %s was used", what);
    sn_panic(SN_STR(buf, (int64_t)strlen(buf)));
}

// sn_check_block panics when p, the start of a block from sn_alloc,
// was released.
void sn_check_block(const void *p, const char *what) {
    if ((uintptr_t)p == (uintptr_t)0xDDDDDDDDDDDDDDDDULL) {
        sn_released(what);
    }

    const block *b = (const block *)p - 1;
    if (b->magic == DEAD) {
        sn_released(what);
    }
}

// sn_deref checks an owned pointer before the program reads through
// it: a null is a pointer whose value moved away, a poisoned one was
// released.
void *sn_deref(void *p) {
    if (p == NULL) {
        sn_panic(SN_STR("a moved pointer was used", 24));
    }

    sn_check_block(p, "pointer");
    return p;
}

int sn_finish(void) {
    sn_string_drop(&sn_task_places()[SN_PLACE_PARTS]); // the main task's, which no scheduler teardown frees
    for (int64_t i = 0; i < sn_os_Args.len; i++) { // the program may have stored strings of its own in it
        sn_string_drop(&((sn_string *)sn_os_Args.ptr)[i]);
    }

    sn_slice_free(&sn_os_Args);
    int64_t n = sn_tasks_running();
    for (int i = 0; n > 0 && i < 200; i++) { // a task that has sent its last word may still be ending
        sn_time_Sleep(1000000);
        n = sn_tasks_running();
    }

    if (n > 0) {
        fflush(stdout);
        fprintf(stderr, "snello: %lld tasks still running at exit; the blocks were not checked\n", (long long)n);
        return 0; // what a running task owns is not a leak
    }

    sn_timers_stop(); // a pending After's channel is the timer's, which ends here

    if (live_blocks == 0) {
        return 0;
    }

    fflush(stdout);
    fprintf(stderr, "snello: %lld blocks (%lld bytes) not released\n", (long long)live_blocks, (long long)live_bytes);
    for (block *b = first; b != NULL; b = b->next) {
        // Each block's bytes, as text as far as they are printable,
        // which names a leaked string outright.
        fprintf(stderr, "  %zu bytes:", b->size);
        const unsigned char *p = (const unsigned char *)(b + 1);
        for (size_t i = 0; i < b->size && i < 32; i++) {
            fprintf(stderr, p[i] >= 0x20 && p[i] < 0x7f ? " %c" : " %02x", p[i]);
        }

        fputc('\n', stderr);
    }

    return 3;
}

#endif
