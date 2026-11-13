// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The random source behind the package functions of math/rand, and the
// double-width multiply its generator and its bounded numbers are
// written on.
//
// The source is Go's: ChaCha8, the ChaCha stream cipher at eight
// rounds, one generator a thread as Go's runtime keeps one a thread.
// What it gives cannot be told from what it gave before, which a small
// generator's can, and a program that draws a token from rand.Int is
// not caught out by it. A block is four ChaCha blocks side by side, 32
// numbers at a draw, so a number costs a load and an add between
// refills; and after 16 blocks the generator keys itself again from
// its own last four numbers, so that a key read out of memory does not
// give back what came before.
//
// The keys are Go's too. One generator for the whole program is keyed
// from the system, 32 bytes, when the first thread draws; each thread's
// generator takes its key from that one, under a lock, and that one
// then keys itself again, so no two threads share a key and the key a
// thread was handed is in no memory but its own.

#define _POSIX_C_SOURCE 200809L
#define _DARWIN_C_SOURCE

#include "internal.h"

#include <fcntl.h>
#include <pthread.h>
#include <string.h>
#include <sys/random.h>
#include <time.h>
#include <unistd.h>

enum {
    counter_step = 4,  // a refill is four blocks, each its own counter
    counter_max = 16,  // the generator is keyed again when the counter reaches it
    chunk = 32,        // a refill gives 32 numbers
    rekey = 4,         // the last four numbers of the last refill are the next key
};

typedef struct {
    uint64_t buf[chunk];
    uint64_t seed[4];
    uint32_t i, n, c;
    bool ready;
} chacha8;

// A word of the four blocks, one lane a block: the compiler writes the
// arithmetic on it as the processor's vector instructions, as Go's
// assembly does by hand, and as plain words where there are none.
typedef uint32_t lanes __attribute__((vector_size(16)));

#define QR(a, b, c, d)                     \
    a += b, d ^= a, d = d << 16 | d >> 16; \
    c += d, b ^= c, b = b << 12 | b >> 20; \
    a += b, d ^= a, d = d << 8 | d >> 24;  \
    c += d, b ^= c, b = b << 7 | b >> 25;

// block fills buf from the key and the counter: four ChaCha8 blocks,
// the words of each interleaved with the others', as Go lays them out,
// so that the numbers are Go's for the same key. The first four words
// and the last four of each block are left as the rounds made them,
// without the addition of the input ChaCha ends on: the constants and
// the counter are known, and adding them hides nothing.
static void block(const uint64_t seed[4], uint64_t buf[chunk], uint32_t counter) {
    lanes key[8], x[16];
    for (int r = 0; r < 4; r++) {
        uint32_t lo = (uint32_t)seed[r], hi = (uint32_t)(seed[r] >> 32);
        key[2 * r] = (lanes){lo, lo, lo, lo};
        key[2 * r + 1] = (lanes){hi, hi, hi, hi};
    }

    x[0] = (lanes){0x61707865, 0x61707865, 0x61707865, 0x61707865};
    x[1] = (lanes){0x3320646e, 0x3320646e, 0x3320646e, 0x3320646e};
    x[2] = (lanes){0x79622d32, 0x79622d32, 0x79622d32, 0x79622d32};
    x[3] = (lanes){0x6b206574, 0x6b206574, 0x6b206574, 0x6b206574};
    for (int r = 0; r < 8; r++) {
        x[4 + r] = key[r];
    }

    x[12] = (lanes){counter, counter + 1, counter + 2, counter + 3};
    x[13] = x[14] = x[15] = (lanes){0, 0, 0, 0};
    for (int round = 0; round < 4; round++) {
        QR(x[0], x[4], x[8], x[12])
        QR(x[1], x[5], x[9], x[13])
        QR(x[2], x[6], x[10], x[14])
        QR(x[3], x[7], x[11], x[15])
        QR(x[0], x[5], x[10], x[15])
        QR(x[1], x[6], x[11], x[12])
        QR(x[2], x[7], x[8], x[13])
        QR(x[3], x[4], x[9], x[14])
    }

    for (int r = 0; r < 8; r++) {
        x[4 + r] += key[r];
    }

    for (int r = 0; r < 16; r++) { // a number is two neighbouring lanes, the first the low half
        buf[2 * r] = (uint64_t)x[r][0] | (uint64_t)x[r][1] << 32;
        buf[2 * r + 1] = (uint64_t)x[r][2] | (uint64_t)x[r][3] << 32;
    }
}

static void init(chacha8 *s, const uint64_t seed[4]) {
    memcpy(s->seed, seed, sizeof s->seed);
    block(s->seed, s->buf, 0);
    s->c = 0;
    s->i = 0;
    s->n = chunk;
    s->ready = true;
}

// rand_refill draws the next 32 numbers. The refill before the counter
// comes round gives four fewer: its last four are the key of the next.
static void rand_refill(chacha8 *s) {
    s->c += counter_step;
    if (s->c == counter_max) {
        memcpy(s->seed, s->buf + chunk - rekey, sizeof s->seed);
        s->c = 0;
    }

    block(s->seed, s->buf, s->c);
    s->i = 0;
    s->n = s->c == counter_max - counter_step ? chunk - rekey : chunk;
}

static uint64_t draw(chacha8 *s) {
    if (s->i >= s->n) {
        rand_refill(s);
    }

    return s->buf[s->i++];
}

#ifdef SN_DEBUG
bool sn_rand_no_system; // a test's: the system gives no random bytes
#endif

// system_key fills seed with 32 bytes from the system: its call for
// them, and where that fails the device a system without the call has.
// It reports whether it had them all, and not all of them zero.
static bool system_key(uint64_t seed[4]) {
#ifdef SN_DEBUG
    if (sn_rand_no_system) {
        return false;
    }
#endif

    bool got = getentropy(seed, 4 * sizeof seed[0]) == 0;
    if (!got) {
        int fd = open("/dev/urandom", O_RDONLY | O_CLOEXEC);
        if (fd >= 0) {
            got = read(fd, seed, 4 * sizeof seed[0]) == (ssize_t)(4 * sizeof seed[0]);
            close(fd);
        }
    }

    return got && (seed[0] | seed[1] | seed[2] | seed[3]) != 0;
}

// clock_key is the key of a system that gives no random bytes, made of
// the clock as Go's runtime makes one, a multiply and a turn for each
// word: a program still runs, on numbers that one who knows when it
// started could work out.
static void clock_key(uint64_t seed[4]) {
    struct timespec ts = {0};
    clock_gettime(CLOCK_MONOTONIC, &ts);
    uint64_t v = (uint64_t)ts.tv_sec * 1000000000u + (uint64_t)ts.tv_nsec;
    for (int i = 0; i < 4; i++) {
        v ^= 0xa0761d6478bd642fu;
        v *= 0xe7037ed1a0b428dbu;
        seed[i] = v;
        v = v >> 32 | v << 32;
    }
}

// wipe clears a key that has been used. The compiler may not drop the
// writes as dead: they go through a volatile pointer.
static void wipe(uint64_t seed[4]) {
    volatile uint64_t *p = seed;
    for (int i = 0; i < 4; i++) {
        p[i] = 0;
    }
}

static pthread_mutex_t first_mu = PTHREAD_MUTEX_INITIALIZER;
static chacha8 first; // the program's generator, which keys the threads'

// thread_key fills seed with a thread's key, four numbers of the
// program's generator, which is keyed at its first use and keys itself
// again after each key it hands out.
static void thread_key(uint64_t seed[4]) {
    uint64_t next[4];
    pthread_mutex_lock(&first_mu);
    if (!first.ready) {
        if (!system_key(next)) {
            clock_key(next);
        }

        init(&first, next);
    }

    for (int i = 0; i < 4; i++) {
        seed[i] = draw(&first);
    }

    for (int i = 0; i < 4; i++) {
        next[i] = draw(&first);
    }

    init(&first, next);
    wipe(next);
    pthread_mutex_unlock(&first_mu);
}

static _Thread_local chacha8 source;

// refresh gives the thread's generator numbers to draw: the next 32, or
// its key at the thread's first draw.
static __attribute__((noinline, cold)) void refresh(chacha8 *s) {
    if (s->ready) {
        rand_refill(s);
        return;
    }

    uint64_t seed[4];
    thread_key(seed);
    init(s, seed);
    wipe(seed);
}

// sn_math__rand_random is the next number of the thread's generator: a
// load from the numbers drawn, and a refill when they are used up, a
// generator not yet keyed having none. A task may resume on another
// thread after any switch, so the thread's generator is reached here,
// in a function the compiler can neither inline nor merge (sched.c
// says why), and never across a call that parks.
__attribute__((noinline)) uint64_t sn_math__rand_random(void) {
    __asm__ volatile("" ::: "memory");
    chacha8 *s = &source;
    if (s->i >= s->n) {
        refresh(s);
    }

    return s->buf[s->i++ & (chunk - 1)];
}

// sn_math__rand_mul64 is the 128-bit product of x and y: the high half
// returned and the low half stored, one multiply instruction.
uint64_t sn_math__rand_mul64(uint64_t x, uint64_t y, uint64_t *lo) {
    unsigned __int128 p = (unsigned __int128)x * y;
    *lo = (uint64_t)p;
    return (uint64_t)(p >> 64);
}

#ifdef SN_DEBUG
// sn_rand_key keys the calling thread's generator with seed, eight
// bytes a word, the low byte first, for the test that holds what the
// package functions draw against Go's numbers for the same key.
void sn_rand_key(const uint8_t seed[32]) {
    uint64_t key[4];
    for (int i = 0; i < 4; i++) {
        key[i] = 0;
        for (int j = 7; j >= 0; j--) {
            key[i] = key[i] << 8 | seed[8 * i + j];
        }
    }

    init(&source, key);
}
#endif
