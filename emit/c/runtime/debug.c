// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The debug build's allocator and checks, compiled into every program
// and empty unless TK_DEBUG is defined, which teak build -debug does.
// Every block carries a header saying how large it is and whether it
// is live; a released block is filled with the poison byte and held
// in a quarantine before it goes back to the system, so that a read
// through a stale pointer sees poison instead of whatever the block
// was reused for; and the program fails at its end when blocks are
// still live, which is a drop the compiler forgot.

#include "teak.h"

#ifdef TK_DEBUG

#include <stdio.h>
#include <stdlib.h>

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

void *tk_alloc(size_t n) {
    block *b = malloc(sizeof *b + (n == 0 ? 1 : n));
    if (b == NULL) {
        tk_panic(TK_STR("out of memory", 13));
    }

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
    return b + 1;
}

// evict gives the block in slot i, if any, back to the system.
static void evict(int i) {
    block *old = held[i];
    if (old != NULL) {
        held_bytes -= old->size;
        free(old);
        held[i] = NULL;
    }
}

void tk_free(void *p) {
    if (p == NULL) {
        return;
    }

    block *b = (block *)p - 1;
    if (b->magic == DEAD) {
        tk_panic(TK_STR("a block was released twice", 26));
    }

    if (b->magic != LIVE) {
        tk_panic(TK_STR("a pointer not from the allocator was released", 45));
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
}

_Noreturn void tk_released(const char *what) {
    char buf[64];
    snprintf(buf, sizeof buf, "a released %s was used", what);
    tk_panic(TK_STR(buf, (int64_t)strlen(buf)));
}

// tk_check_block panics when p, the start of a block from tk_alloc,
// was released.
void tk_check_block(const void *p, const char *what) {
    if ((uintptr_t)p == (uintptr_t)0xDDDDDDDDDDDDDDDDULL) {
        tk_released(what);
    }

    const block *b = (const block *)p - 1;
    if (b->magic == DEAD) {
        tk_released(what);
    }
}

// tk_deref checks an owned pointer before the program reads through
// it: a null is a pointer whose value moved away, a poisoned one was
// released.
void *tk_deref(void *p) {
    if (p == NULL) {
        tk_panic(TK_STR("a moved pointer was used", 24));
    }

    tk_check_block(p, "pointer");
    return p;
}

int tk_finish(void) {
    tk_slice_free(&tk_args);
    if (live_blocks == 0) {
        return 0;
    }

    fflush(stdout);
    fprintf(stderr, "teak: %lld blocks (%lld bytes) not released\n", (long long)live_blocks, (long long)live_bytes);
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
