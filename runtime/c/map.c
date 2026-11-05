// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Maps: wyhash, and a Swiss table indexing the entries in the order
// they were inserted.

#define _POSIX_C_SOURCE 200809L
#define _DARWIN_C_SOURCE

#include "internal.h"

#include <string.h>
#include <sys/random.h>
#include <unistd.h>

// Hashing is wyhash's (Wang Yi's, in the public domain), the function
// Go's runtime falls back to where it has no AES instructions: eight
// bytes a step, folded by a 64-by-64-bit multiply whose two halves are
// xored. Every hash is keyed by a seed drawn from the system's random
// source when the program starts, as Go seeds every map and Rust every
// HashMap, so that keys chosen to collide cannot be computed ahead of
// time: without the seed a server's map could be filled with keys that
// all fall in one slot, and every lookup would walk them (HashDoS).
static uint64_t hash_seed[2] = {0x2d358dccaa6c78a5u, 0x8bb84b93962eacc9u};

static const uint64_t wyp[4] = {0x2d358dccaa6c78a5u, 0x8bb84b93962eacc9u, 0x4b33a62ed433d4a3u, 0x4d5a2da51de1aa47u};

static inline uint64_t wymix(uint64_t a, uint64_t b) {
    __uint128_t r = (__uint128_t)a * b;
    return (uint64_t)r ^ (uint64_t)(r >> 64);
}

static inline uint64_t wyr8(const uint8_t *p) {
    uint64_t v;
    memcpy(&v, p, 8);
    return v;
}

static inline uint64_t wyr4(const uint8_t *p) {
    uint32_t v;
    memcpy(&v, p, 4);
    return v;
}

static inline uint64_t wyr3(const uint8_t *p, size_t k) {
    return ((uint64_t)p[0] << 16) | ((uint64_t)p[k >> 1] << 8) | p[k - 1];
}

// sn_hash_init draws the seeds. A system that gives no random bytes
// leaves the fixed ones, which hash as well and are only predictable.
void sn_hash_init(void) {
    uint64_t seed[2];
    if (getentropy(seed, sizeof seed) == 0) {
        hash_seed[0] = seed[0] ^ wyp[0];
        hash_seed[1] = seed[1] ^ wyp[1];
    }
}

uint64_t sn_hash_bytes(const void *key, size_t len) {
    const uint8_t *p = key;
    uint64_t seed = hash_seed[0];
    seed ^= wymix(seed ^ wyp[0], wyp[1]);
    uint64_t a, b;
    if (len <= 16) {
        if (len >= 4) {
            a = (wyr4(p) << 32) | wyr4(p + ((len >> 3) << 2));
            b = (wyr4(p + len - 4) << 32) | wyr4(p + len - 4 - ((len >> 3) << 2));
        } else if (len > 0) {
            a = wyr3(p, len);
            b = 0;
        } else {
            a = b = 0;
        }
    } else {
        size_t i = len;
        if (i > 48) {
            uint64_t see1 = seed, see2 = seed;
            do {
                seed = wymix(wyr8(p) ^ wyp[1], wyr8(p + 8) ^ seed);
                see1 = wymix(wyr8(p + 16) ^ wyp[2], wyr8(p + 24) ^ see1);
                see2 = wymix(wyr8(p + 32) ^ wyp[3], wyr8(p + 40) ^ see2);
                p += 48;
                i -= 48;
            } while (i > 48);
            seed ^= see1 ^ see2;
        }

        while (i > 16) {
            seed = wymix(wyr8(p) ^ wyp[1], wyr8(p + 8) ^ seed);
            i -= 16;
            p += 16;
        }

        a = wyr8(p + i - 16);
        b = wyr8(p + i - 8);
    }

    a ^= wyp[1];
    b ^= seed;
    __uint128_t r = (__uint128_t)a * b;
    a = (uint64_t)r;
    b = (uint64_t)(r >> 64);
    return wymix(a ^ wyp[0] ^ len, b ^ wyp[1]);
}

// sn_hash_mix folds the hash v of a part of a key into h, the hash of
// the parts before it, keyed by the seed: a struct, a tuple or an
// option hashes its parts in turn, and a number is sn_hash_mix(0, n).
uint64_t sn_hash_mix(uint64_t h, uint64_t v) {
    return wymix(h ^ hash_seed[1] ^ wyp[2], v ^ wyp[3]);
}

// A map keeps its entries in the order they were inserted, which is the
// order a range visits them, in one block: the keys, the values, and the
// hash of each key, zero for an entry Remove emptied. An index finds
// them: a Swiss table, the design of Go's maps since 1.24 and of Rust's
// HashMap (hashbrown), over slots holding entry numbers, one control
// byte a slot. A full slot's byte is the top seven bits of its key's
// hash, an empty one's 0xFF and one whose entry was removed 0x80. A
// lookup reads the bytes of eight slots at once as one word and compares
// keys only where the seven bits match, one key in 128 by chance; the
// entries array and an index of entry numbers is Rust's indexmap,
// which keeps insertion order as this map must. The slots are a power
// of two, eight to a group; a probe visits groups in triangular order,
// which reaches every group, and stops at the first group with an
// empty slot. The index is at most seven eighths full, so one is
// always there.
enum { ctrl_empty = 0xFF, ctrl_deleted = 0x80 };

static const uint64_t lo_bits = 0x0101010101010101u, hi_bits = 0x8080808080808080u;

static void *key_at(const sn_map *m, const sn_map_type *t, int64_t i) { return (char *)m->keys + (size_t)i * t->ksize; }
static void *val_at(const sn_map *m, const sn_map_type *t, int64_t i) { return (char *)m->vals + (size_t)i * t->vsize; }
static uint32_t *slot_entries(const sn_map *m) { return (uint32_t *)(m->ctrl + m->slots); }

static inline uint64_t group_at(const sn_map *m, uint64_t g) {
    uint64_t w;
    memcpy(&w, m->ctrl + g * 8, 8); // little-endian: byte j of the group is bits 8j to 8j+7
    return w;
}

// match_byte returns a bit at the top of every byte of the group equal
// to b, and perhaps of a byte after one, which the caller's comparison
// of hashes rules out.
static inline uint64_t match_byte(uint64_t w, uint8_t b) {
    uint64_t x = w ^ (lo_bits * b);
    return (x - lo_bits) & ~x & hi_bits;
}

static inline uint64_t match_empty(uint64_t w) { return w & (w << 1) & hi_bits; }
static inline uint64_t match_free(uint64_t w) { return w & hi_bits; } // empty or deleted

// key_hash is the hash a map files a key under: the type's hash, keyed
// once more, since a Hash method of the program's may return the key
// itself; never zero, which marks an emptied entry.
static inline uint64_t key_hash(const sn_map_type *t, const void *key) {
    uint64_t h = wymix(t->hash(key) ^ hash_seed[0], wyp[1]);
    return h != 0 ? h : 1;
}

static inline uint8_t tag(uint64_t h) { return (uint8_t)(h >> 57); }

// find_slot returns the slot holding key, of hash h, or -1.
static int64_t find_slot(const sn_map *m, const sn_map_type *t, const void *key, uint64_t h) {
    uint64_t gmask = ((uint64_t)m->slots >> 3) - 1;
    uint64_t g = h & gmask;
    const uint32_t *entries = slot_entries(m);
    for (uint64_t step = 1;; step++) {
        uint64_t w = group_at(m, g);
        for (uint64_t bits = match_byte(w, tag(h)); bits != 0; bits &= bits - 1) {
            uint64_t s = g * 8 + (uint64_t)__builtin_ctzll(bits) / 8;
            uint32_t e = entries[s];
            if (m->hashes[e] == h && t->eq(key_at(m, t, e), key)) {
                return (int64_t)s;
            }
        }

        if (match_empty(w) != 0) {
            return -1;
        }

        g = (g + step) & gmask;
    }
}

// free_slot returns the first empty or deleted slot on the probe path
// of hash h: a new key goes there, before any group a lookup of it
// would stop at.
static uint64_t free_slot(const sn_map *m, uint64_t h) {
    uint64_t gmask = ((uint64_t)m->slots >> 3) - 1;
    uint64_t g = h & gmask;
    for (uint64_t step = 1;; step++) {
        uint64_t bits = match_free(group_at(m, g));
        if (bits != 0) {
            return g * 8 + (uint64_t)__builtin_ctzll(bits) / 8;
        }

        g = (g + step) & gmask;
    }
}

static size_t round16(size_t n) { return (n + 15) & ~(size_t)15; }

// rebuild gives the map room for need live entries: it keeps the room
// when dropping the emptied entries makes enough, else doubles it, and
// indexes the live entries again from their stored hashes, with no key
// hashed or compared. The live entries keep their order.
static void rebuild(sn_map *m, const sn_map_type *t, int64_t need) {
    int64_t cap = m->cap < 8 ? 8 : m->cap;
    if (need > cap / 2 && m->cap > 0) {
        cap *= 2;
    }

    while (cap < need) {
        cap *= 2;
    }

    if (cap > (int64_t)UINT32_MAX) {
        sn_panic(SN_STR("map: too many entries", 21));
    }

    size_t koff = 0, voff = round16((size_t)cap * t->ksize), hoff = voff + round16((size_t)cap * t->vsize);
    char *block = sn_alloc(hoff + (size_t)cap * sizeof(uint64_t));
    sn_map n = {0};
    n.keys = block + koff;
    n.vals = block + voff;
    n.hashes = (uint64_t *)(block + hoff);
    n.cap = cap;
    n.slots = 8;
    while (n.slots - n.slots / 8 < cap) {
        n.slots *= 2;
    }

    n.ctrl = sn_alloc((size_t)n.slots * (1 + sizeof(uint32_t)));
    memset(n.ctrl, ctrl_empty, (size_t)n.slots);
    uint32_t *entries = slot_entries(&n);
    for (int64_t i = 0; i < m->len; i++) {
        uint64_t h = m->hashes[i];
        if (h == 0) {
            continue;
        }

        int64_t e = n.len++;
        memcpy(key_at(&n, t, e), key_at(m, t, i), t->ksize);
        memcpy(val_at(&n, t, e), val_at(m, t, i), t->vsize);
        n.hashes[e] = h;
        uint64_t s = free_slot(&n, h);
        n.ctrl[s] = tag(h);
        entries[s] = (uint32_t)e;
    }

    n.count = n.len;
    sn_free(m->keys);
    sn_free(m->ctrl);
    *m = n;
}

void *sn_map_find(const sn_map *m, const sn_map_type *t, const void *key) {
    sn_check_map(m);
    if (m->count == 0) {
        return NULL;
    }

    int64_t s = find_slot(m, t, key, key_hash(t, key));
    return s < 0 ? NULL : val_at(m, t, slot_entries(m)[s]);
}

const void *sn_map_get(const sn_map *m, const sn_map_type *t, const void *key, const void *zero) {
    const void *v = sn_map_find(m, t, key);
    return v == NULL ? zero : v;
}

void *sn_map_slot(sn_map *m, const sn_map_type *t, const void *key, void **added) {
    sn_check_map(m);
    uint64_t h = key_hash(t, key);
    if (m->count > 0) {
        int64_t s = find_slot(m, t, key, h);
        if (s >= 0) {
            *added = NULL;
            return val_at(m, t, slot_entries(m)[s]);
        }
    }

    if (m->len == m->cap) {
        rebuild(m, t, m->count + 1);
    }

    int64_t e = m->len++;
    memcpy(key_at(m, t, e), key, t->ksize);
    memset(val_at(m, t, e), 0, t->vsize);
    m->hashes[e] = h;
    m->count++;
    uint64_t s = free_slot(m, h);
    m->ctrl[s] = tag(h);
    slot_entries(m)[s] = (uint32_t)e;
    *added = key_at(m, t, e);
    return val_at(m, t, e);
}

void sn_map_reserve(sn_map *m, const sn_map_type *t, int64_t n) {
    sn_check_map(m);
    if (n < 0) {
        sn_panic(SN_STR("makemap: size out of range", 26));
    }

    if (n > m->cap) {
        rebuild(m, t, n);
    }
}

bool sn_map_remove(sn_map *m, const sn_map_type *t, const void *key, void *out) {
    sn_check_map(m);
    if (m->count == 0) {
        return false;
    }

    int64_t s = find_slot(m, t, key, key_hash(t, key));
    if (s < 0) {
        return false;
    }

    uint32_t e = slot_entries(m)[s];
    memcpy(out, val_at(m, t, e), t->vsize);
    memset(val_at(m, t, e), 0, t->vsize); // the value moved out
    if (t->kdrop != NULL) {
        t->kdrop(key_at(m, t, e));
    }

    m->hashes[e] = 0; // emptied: skipped by ranges until the next rebuild drops it
    m->count--;
    if (m->count == 0) { // nothing live: the entries and the index start again
        m->len = 0;
        memset(m->ctrl, ctrl_empty, (size_t)m->slots);
        return true;
    }

    // A slot in a group with an empty slot becomes empty itself: a probe
    // reaching the group stops there whatever this slot holds. Elsewhere
    // it is marked deleted, so that probes go on past it.
    m->ctrl[s] = match_empty(group_at(m, (uint64_t)s / 8)) != 0 ? ctrl_empty : ctrl_deleted;
    return true;
}

void sn_map_free(sn_map *m, const sn_map_type *t) {
    sn_check_map(m);
    for (int64_t i = sn_map_next(m, 0); i >= 0; i = sn_map_next(m, i + 1)) {
        if (t->kdrop != NULL) {
            t->kdrop(key_at(m, t, i));
        }

        if (t->vdrop != NULL) {
            t->vdrop(val_at(m, t, i));
        }
    }

    sn_free(m->keys);
    sn_free(m->ctrl);
    memset(m, 0, sizeof *m);
    SN_POISON(*m);
}

// sn_map_clone copies the bytes of the live entries in order into a map
// of their own, indexed from their hashes: the backends clone what the
// keys and values own.
void sn_map_clone(const sn_map *m, const sn_map_type *t, sn_map *out) {
    sn_check_map(m);
    sn_map n = {0};
    if (m->count > 0) {
        rebuild(&n, t, m->count);
        uint32_t *entries = slot_entries(&n);
        for (int64_t i = sn_map_next(m, 0); i >= 0; i = sn_map_next(m, i + 1)) {
            int64_t e = n.len++;
            memcpy(key_at(&n, t, e), key_at(m, t, i), t->ksize);
            memcpy(val_at(&n, t, e), val_at(m, t, i), t->vsize);
            n.hashes[e] = m->hashes[i];
            uint64_t s = free_slot(&n, n.hashes[e]);
            n.ctrl[s] = tag(n.hashes[e]);
            entries[s] = (uint32_t)e;
        }

        n.count = n.len;
    }

    *out = n;
}

int64_t sn_map_next(const sn_map *m, int64_t i) {
    sn_check_map(m);
    for (; i < m->len; i++) {
        if (m->hashes[i] != 0) {
            return i;
        }
    }

    return -1;
}
