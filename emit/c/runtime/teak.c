// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#define _POSIX_C_SOURCE 200809L
#define _DARWIN_C_SOURCE

#include "teak.h"

#include <ctype.h>
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <time.h>
#include <unistd.h>

tk_slice tk_os_Args = {0};

// The allocator of a release build is the system's; debug.c has the
// counting, poisoning one of a debug build.
#ifndef TK_DEBUG
void *tk_alloc(size_t n) {
    void *p = malloc(n == 0 ? 1 : n);
    if (p == NULL) {
        tk_panic(TK_STR("out of memory", 13));
    }

    return p;
}

void tk_free(void *p) {
    free(p);
}

int tk_finish(void) {
    return 0;
}
#endif

void tk_release(void **p) {
    tk_free(*p);
    *p = NULL;
    TK_POISON(*p);
}

_Noreturn void tk_exit(int code) {
    fflush(stdout);
    exit(code);
}

typedef struct {
    void *p;
    void (*drop)(void *);
} guard;

// The guard stack grows with the system allocator, not tk_alloc, so a
// debug build does not count it among the program's blocks.
static guard *guards;
static int64_t nguards, guardcap;
static bool unwinding;

void tk_guard(void *p, void (*drop)(void *)) {
    if (nguards == guardcap) {
        int64_t cap = guardcap < 16 ? 16 : guardcap * 2;
        guard *more = realloc(guards, (size_t)cap * sizeof *guards);
        if (more == NULL) {
            tk_panic(TK_STR("out of memory", 13)); // the stack so far is intact for the unwinding
        }

        guards = more;
        guardcap = cap;
    }

    guards[nguards++] = (guard){p, drop};
}

// tk_unguard removes the guard of p, searched from the top: a value is
// usually dropped in the reverse order of its registration, but a
// temporary of a statement may be dropped after a variable the
// statement declared.
void tk_unguard(void *p) {
    for (int64_t i = nguards - 1; i >= 0; i--) {
        if (guards[i].p == p) {
            memmove(&guards[i], &guards[i + 1], (size_t)(nguards - i - 1) * sizeof *guards);
            nguards--;
            return;
        }
    }
}

// tk_panic prints the message, then runs the drops of the guarded
// values, innermost first, and ends the program. A Drop that panics
// during that ends the program with its own message at once.
_Noreturn void tk_panic(tk_string msg) {
    fflush(stdout);
    fputs("panic: ", stderr);
    fwrite(msg.ptr, 1, (size_t)msg.len, stderr);
    fputc('\n', stderr);
    if (!unwinding) {
        unwinding = true;
        while (nguards > 0) {
            guard g = guards[--nguards];
            g.drop(g.p);
        }
    }

    fflush(stdout);
    exit(2);
}

_Noreturn void tk_bounds(int64_t i, int64_t len) {
    char buf[96];
    snprintf(buf, sizeof buf, "index out of range [%lld] with length %lld", (long long)i, (long long)len);
    tk_panic(TK_STR(buf, (int64_t)strlen(buf)));
}

// grow makes room for at least need elements, doubling as Teak's
// Push amortizes to constant time.
static void grow(tk_slice *s, int64_t need, size_t size) {
    tk_check_slice(s);
    if (need <= s->cap) {
        return;
    }

    int64_t cap = s->cap < 4 ? 4 : s->cap * 2;
    if (cap < need) {
        cap = need;
    }

    void *p = tk_alloc((size_t)cap * size);
    if (s->len > 0) {
        memcpy(p, s->ptr, (size_t)s->len * size);
    }

    tk_free(s->ptr);
    s->ptr = p;
    s->cap = cap;
}

void tk_slice_free(tk_slice *s) {
    tk_check_slice(s);
    tk_free(s->ptr);
    s->ptr = NULL;
    s->len = 0;
    s->cap = 0;
    TK_POISON(*s);
}

void tk_slice_make(tk_slice *out, int64_t len, int64_t cap, size_t size) {
    if (len < 0 || cap < len) {
        tk_panic(TK_STR("make: length out of range", 25));
    }

    out->ptr = tk_alloc((size_t)cap * size);
    memset(out->ptr, 0, (size_t)cap * size);
    out->len = len;
    out->cap = cap;
}

void tk_slice_push(tk_slice *s, const void *v, size_t size) {
    grow(s, s->len + 1, size);
    memcpy((char *)s->ptr + (size_t)s->len * size, v, size);
    s->len++;
}

bool tk_slice_pop(tk_slice *s, void *out, size_t size) {
    tk_check_slice(s);
    if (s->len == 0) {
        return false;
    }

    s->len--;
    memcpy(out, (char *)s->ptr + (size_t)s->len * size, size);
    return true;
}

void tk_slice_insert(tk_slice *s, int64_t i, const void *v, size_t size) {
    tk_check_slice(s);
    if ((uint64_t)i > (uint64_t)s->len) {
        tk_bounds(i, s->len);
    }

    grow(s, s->len + 1, size);
    char *at = (char *)s->ptr + (size_t)i * size;
    memmove(at + size, at, (size_t)(s->len - i) * size);
    memcpy(at, v, size);
    s->len++;
}

void tk_slice_remove(tk_slice *s, int64_t i, void *out, size_t size) {
    tk_index(i, s->len);
    char *at = (char *)s->ptr + (size_t)i * size;
    memcpy(out, at, size);
    memmove(at, at + size, (size_t)(s->len - i - 1) * size);
    s->len--;
}

void tk_slice_reverse(tk_slice *s, size_t size) {
    tk_check_slice(s);
    if (s->len < 2) {
        return;
    }

    char *lo = s->ptr;
    char *hi = (char *)s->ptr + (size_t)(s->len - 1) * size;
    char tmp[256];
    for (; lo < hi; lo += size, hi -= size) {
        for (size_t done = 0; done < size; done += sizeof tmp) {
            size_t n = size - done < sizeof tmp ? size - done : sizeof tmp;
            memcpy(tmp, lo + done, n);
            memcpy(lo + done, hi + done, n);
            memcpy(hi + done, tmp, n);
        }
    }
}

void tk_slice_clone(const tk_slice *s, tk_slice *out, size_t size) {
    tk_check_slice(s);
    out->ptr = tk_alloc((size_t)s->len * size);
    memcpy(out->ptr, s->ptr, (size_t)s->len * size);
    out->len = s->len;
    out->cap = s->len;
}

void tk_slice_sub(const tk_slice *s, int64_t lo, int64_t hi, tk_slice *out, size_t size) {
    tk_check_slice(s);
    if (lo < 0 || hi < lo || hi > s->len) {
        tk_bounds(hi, s->len);
    }

    out->ptr = (char *)s->ptr + (size_t)lo * size;
    out->len = hi - lo;
    out->cap = s->cap - lo;
}

// merge sorts the n elements at p, stably, using tmp as scratch of the
// same size.
static void merge(char *p, char *tmp, int64_t n, size_t size, bool (*less)(void *, const void *, const void *), void *ctx) {
    if (n < 2) {
        return;
    }

    int64_t half = n / 2;
    merge(p, tmp, half, size, less, ctx);
    merge(p + (size_t)half * size, tmp, n - half, size, less, ctx);
    int64_t i = 0, j = half, k = 0;
    while (i < half && j < n) {
        if (less(ctx, p + (size_t)j * size, p + (size_t)i * size)) {
            memcpy(tmp + (size_t)k++ * size, p + (size_t)j++ * size, size);
        } else {
            memcpy(tmp + (size_t)k++ * size, p + (size_t)i++ * size, size);
        }
    }

    memcpy(tmp + (size_t)k * size, p + (size_t)i * size, (size_t)(half - i) * size);
    k += half - i;
    memcpy(tmp + (size_t)k * size, p + (size_t)j * size, (size_t)(n - j) * size);
    memcpy(p, tmp, (size_t)n * size);
}

void tk_slice_sort(tk_slice *s, size_t size, bool (*less)(void *ctx, const void *a, const void *b), void *ctx) {
    tk_check_slice(s);
    if (s->len < 2) {
        return;
    }

    void *tmp = tk_alloc((size_t)s->len * size);
    merge(s->ptr, tmp, s->len, size, less, ctx);
    tk_free(tmp);
}

tk_string tk_concat(tk_string a, tk_string b) {
    tk_check_string(a);
    tk_check_string(b);
    uint8_t *p = tk_alloc((size_t)(a.len + b.len));
    memcpy(p, a.ptr, (size_t)a.len);
    memcpy(p + a.len, b.ptr, (size_t)b.len);
    return TK_OWNED(p, a.len + b.len);
}

tk_string tk_join(tk_string a, tk_string b, int owned) {
    tk_string s = tk_concat(a, b);
    if (owned & 1) {
        tk_string_free(a);
    }

    if (owned & 2) {
        tk_string_free(b);
    }

    return s;
}

bool tk_string_eq(tk_string a, tk_string b) {
    tk_check_string(a);
    tk_check_string(b);
    return a.len == b.len && memcmp(a.ptr, b.ptr, (size_t)a.len) == 0;
}

int tk_string_cmp(tk_string a, tk_string b) {
    tk_check_string(a);
    tk_check_string(b);
    int64_t n = a.len < b.len ? a.len : b.len;
    int c = memcmp(a.ptr, b.ptr, (size_t)n);
    if (c != 0) {
        return c;
    }

    return a.len < b.len ? -1 : a.len > b.len;
}

// formatted copies a small formatted number into an owned string.
static tk_string formatted(const char *buf) {
    size_t n = strlen(buf);
    uint8_t *p = tk_alloc(n);
    memcpy(p, buf, n);
    return TK_OWNED(p, (int64_t)n);
}

tk_string tk_int_to_string(int64_t v) {
    char buf[32];
    snprintf(buf, sizeof buf, "%lld", (long long)v);
    return formatted(buf);
}

tk_string tk_uint_to_string(uint64_t v) {
    char buf[32];
    snprintf(buf, sizeof buf, "%llu", (unsigned long long)v);
    return formatted(buf);
}

// tk_f64_to_string prints the shortest text that reads back as the
// same number, as Teak's fmt does: 15 for 15.0, 2.5 for 2.5, and no
// exponent while the magnitude is between 1e-4 and 1e21.
tk_string tk_f64_to_string(double v) {
    char buf[64];
    int prec = 1;
    for (; prec <= 17; prec++) {
        snprintf(buf, sizeof buf, "%.*g", prec, v);
        if (strtod(buf, NULL) == v) {
            break;
        }
    }

    if (strchr(buf, 'e') != NULL && v != 0 && v == v && v - v == 0) {
        int exp = (int)floor(log10(fabs(v)));
        if (exp >= -4 && exp < 21) {
            int frac = prec - 1 - exp;
            snprintf(buf, sizeof buf, "%.*f", frac < 0 ? 0 : frac, v);
        }
    }

    return formatted(buf);
}

tk_string tk_bool_to_string(bool v) {
    return v ? TK_STR("true", 4) : TK_STR("false", 5);
}

// tk_quote writes s as a string literal reads: in double quotes, with
// a quote, a backslash and the control characters escaped.
tk_string tk_quote(tk_string s) {
    tk_check_string(s);
    uint8_t *p = tk_alloc((size_t)s.len * 4 + 2);
    int64_t n = 0;
    p[n++] = '"';
    for (int64_t i = 0; i < s.len; i++) {
        uint8_t c = s.ptr[i];
        const char *escape = NULL;
        switch (c) {
        case '"': escape = "\\\""; break;
        case '\\': escape = "\\\\"; break;
        case '\n': escape = "\\n"; break;
        case '\t': escape = "\\t"; break;
        case '\r': escape = "\\r"; break;
        }

        if (escape != NULL) {
            n += snprintf((char *)p + n, 3, "%s", escape);
        } else if (c < ' ' || c == 127) {
            n += snprintf((char *)p + n, 5, "\\x%02x", c);
        } else {
            p[n++] = c;
        }
    }

    p[n++] = '"';
    return TK_OWNED(p, n);
}

uint64_t tk_hash_bytes(const void *p, size_t n) {
    uint64_t h = 1469598103934665603u; // FNV-1a
    for (size_t i = 0; i < n; i++) {
        h = (h ^ ((const uint8_t *)p)[i]) * 1099511628211u;
    }

    return h;
}

uint64_t tk_hash_mix(uint64_t h, uint64_t v) {
    h ^= v + 0x9e3779b97f4a7c15u + (h << 6) + (h >> 2);
    return h * 1099511628211u;
}

static void *key_at(const tk_map *m, const tk_map_type *t, int64_t i) { return (char *)m->keys + (size_t)i * t->ksize; }
static void *val_at(const tk_map *m, const tk_map_type *t, int64_t i) { return (char *)m->vals + (size_t)i * t->vsize; }

// probe returns the index slot holding the key, or the empty slot
// where it would go; the index must have room.
static int64_t probe(const tk_map *m, const tk_map_type *t, const void *key) {
    uint64_t mask = (uint64_t)m->slots - 1;
    for (uint64_t i = t->hash(key) & mask;; i = (i + 1) & mask) {
        int64_t at = m->index[i];
        if (at == 0 || (m->live[at - 1] && t->eq(key_at(m, t, at - 1), key))) {
            return (int64_t)i;
        }
    }
}

// rebuild makes room for one more entry, growing the arrays, dropping
// dead entries, and indexing the live ones again.
static void rebuild(tk_map *m, const tk_map_type *t) {
    int64_t cap = m->cap < 8 ? 8 : m->cap * 2;
    if (m->count + 1 <= m->cap / 2) {
        cap = m->cap; // enough dead entries to drop
    }

    tk_map n = {0};
    n.cap = cap;
    n.slots = 16;
    while (n.slots < cap * 2) {
        n.slots *= 2;
    }

    n.keys = tk_alloc((size_t)cap * t->ksize);
    n.vals = tk_alloc((size_t)cap * t->vsize);
    n.live = tk_alloc((size_t)cap);
    n.index = tk_alloc((size_t)n.slots * sizeof(int64_t));
    memset(n.index, 0, (size_t)n.slots * sizeof(int64_t));
    for (int64_t i = 0; i < m->len; i++) {
        if (!m->live[i]) {
            continue;
        }

        memcpy(key_at(&n, t, n.len), key_at(m, t, i), t->ksize);
        memcpy(val_at(&n, t, n.len), val_at(m, t, i), t->vsize);
        n.live[n.len] = 1;
        n.len++;
        n.index[probe(&n, t, key_at(m, t, i))] = n.len;
    }

    n.count = n.len;
    tk_free(m->keys);
    tk_free(m->vals);
    tk_free(m->live);
    tk_free(m->index);
    *m = n;
}

void *tk_map_find(const tk_map *m, const tk_map_type *t, const void *key) {
    tk_check_map(m);
    if (m->count == 0) {
        return NULL;
    }

    int64_t at = m->index[probe(m, t, key)];
    return at == 0 ? NULL : val_at(m, t, at - 1);
}

const void *tk_map_get(const tk_map *m, const tk_map_type *t, const void *key, const void *zero) {
    const void *v = tk_map_find(m, t, key);
    return v == NULL ? zero : v;
}

void *tk_map_slot(tk_map *m, const tk_map_type *t, const void *key) {
    void *v = tk_map_find(m, t, key);
    if (v != NULL) {
        return v;
    }

    if (m->len == m->cap) {
        rebuild(m, t);
    }

    int64_t i = m->len++;
    memcpy(key_at(m, t, i), key, t->ksize);
    memset(val_at(m, t, i), 0, t->vsize);
    m->live[i] = 1;
    m->count++;
    m->index[probe(m, t, key)] = i + 1;
    return val_at(m, t, i);
}

bool tk_map_remove(tk_map *m, const tk_map_type *t, const void *key, void *out) {
    tk_check_map(m);
    if (m->count == 0) {
        return false;
    }

    int64_t slot = probe(m, t, key);
    int64_t at = m->index[slot];
    if (at == 0) {
        return false;
    }

    memcpy(out, val_at(m, t, at - 1), t->vsize);
    memset(val_at(m, t, at - 1), 0, t->vsize); // the value moved out
    if (t->kdrop != NULL) {
        t->kdrop(key_at(m, t, at - 1));
    }

    m->live[at - 1] = 0; // the entry stays, skipped by lookups and ranges, until the next rebuild drops it
    m->count--;
    return true;
}

void tk_map_free(tk_map *m, const tk_map_type *t) {
    tk_check_map(m);
    for (int64_t i = tk_map_next(m, 0); i >= 0; i = tk_map_next(m, i + 1)) {
        if (t->kdrop != NULL) {
            t->kdrop(key_at(m, t, i));
        }

        if (t->vdrop != NULL) {
            t->vdrop(val_at(m, t, i));
        }
    }

    tk_free(m->keys);
    tk_free(m->vals);
    tk_free(m->live);
    tk_free(m->index);
    memset(m, 0, sizeof *m);
    TK_POISON(*m);
}

void tk_map_clone(const tk_map *m, const tk_map_type *t, tk_map *out) {
    tk_check_map(m);
    tk_map n = {0};
    for (int64_t i = tk_map_next(m, 0); i >= 0; i = tk_map_next(m, i + 1)) {
        memcpy(tk_map_slot(&n, t, key_at(m, t, i)), val_at(m, t, i), t->vsize);
    }

    *out = n;
}

int64_t tk_map_next(const tk_map *m, int64_t i) {
    tk_check_map(m);
    for (; i < m->len; i++) {
        if (m->live[i]) {
            return i;
        }
    }

    return -1;
}

tk_string tk_string_clone(tk_string s) {
    tk_check_string(s);
    if (s.len == 0) {
        return TK_STR("", 0); // an owned string of no bytes would be a block with cap 0, which nothing frees
    }

    uint8_t *p = tk_alloc((size_t)s.len);
    memcpy(p, s.ptr, (size_t)s.len);
    return TK_OWNED(p, s.len);
}

tk_string tk_substr(tk_string s, int64_t lo, int64_t hi) {
    tk_check_string(s);
    if (lo < 0 || hi < lo || hi > s.len) {
        tk_bounds(hi, s.len);
    }

    return TK_STR(s.ptr + lo, hi - lo);
}

// mapped copies s through f, one byte at a time: ASCII case only,
// until the library is written in Teak.
static tk_string mapped(tk_string s, int (*f)(int)) {
    tk_check_string(s);
    uint8_t *p = tk_alloc((size_t)s.len);
    for (int64_t i = 0; i < s.len; i++) {
        p[i] = (uint8_t)f(s.ptr[i]);
    }

    return TK_OWNED(p, s.len);
}

tk_string tk_to_upper(tk_string s) { return mapped(s, toupper); }
tk_string tk_to_lower(tk_string s) { return mapped(s, tolower); }

static bool space(uint8_t c) {
    return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v';
}

tk_string tk_trim(tk_string s) {
    tk_check_string(s);
    int64_t lo = 0, hi = s.len;
    while (lo < hi && space(s.ptr[lo])) {
        lo++;
    }

    while (hi > lo && space(s.ptr[hi - 1])) {
        hi--;
    }

    return TK_STR(s.ptr + lo, hi - lo);
}

bool tk_has_prefix(tk_string s, tk_string t) {
    tk_check_string(s);
    tk_check_string(t);
    return s.len >= t.len && memcmp(s.ptr, t.ptr, (size_t)t.len) == 0;
}

bool tk_has_suffix(tk_string s, tk_string t) {
    tk_check_string(s);
    tk_check_string(t);
    return s.len >= t.len && memcmp(s.ptr + s.len - t.len, t.ptr, (size_t)t.len) == 0;
}

// find returns the offset of t in s, or -1.
static int64_t find(tk_string s, tk_string t) {
    tk_check_string(s);
    tk_check_string(t);
    for (int64_t i = 0; i + t.len <= s.len; i++) {
        if (memcmp(s.ptr + i, t.ptr, (size_t)t.len) == 0) {
            return i;
        }
    }

    return -1;
}

bool tk_string_contains(tk_string s, tk_string t) { return find(s, t) >= 0; }

// tk_fields splits s around runs of white space into views of s.
void tk_fields(tk_string s, tk_slice *out) {
    tk_check_string(s);
    out->ptr = NULL;
    out->len = 0;
    out->cap = 0;
    int64_t i = 0;
    while (i < s.len) {
        while (i < s.len && space(s.ptr[i])) {
            i++;
        }

        int64_t start = i;
        while (i < s.len && !space(s.ptr[i])) {
            i++;
        }

        if (i > start) {
            tk_string field = TK_STR(s.ptr + start, i - start);
            tk_slice_push(out, &field, sizeof field);
        }
    }
}

// tk_lines splits s at every newline into views of s, each without
// its newline and a carriage return before it; a last line without a
// newline is a line, a trailing newline does not make an empty one.
void tk_lines(tk_string s, tk_slice *out) {
    tk_check_string(s);
    out->ptr = NULL;
    out->len = 0;
    out->cap = 0;
    int64_t start = 0;
    for (int64_t i = 0; i <= s.len; i++) {
        if (i < s.len && s.ptr[i] != '\n') {
            continue;
        }

        if (i == s.len && start == s.len) {
            break; // nothing after the last newline, or an empty string
        }

        int64_t end = i;
        if (end > start && s.ptr[end - 1] == '\r') {
            end--;
        }

        tk_string line = TK_STR(s.ptr + start, end - start);
        tk_slice_push(out, &line, sizeof line);
        start = i + 1;
    }
}

// tk_split splits s at every sep into views of s; an empty separator
// splits between every byte.
void tk_split(tk_string s, tk_string sep, tk_slice *out) {
    tk_check_string(s);
    tk_check_string(sep);
    out->ptr = NULL;
    out->len = 0;
    out->cap = 0;
    int64_t start = 0;
    for (;;) {
        tk_string rest = TK_STR(s.ptr + start, s.len - start);
        int64_t at = sep.len == 0 ? (rest.len > 1 ? 1 : -1) : find(rest, sep);
        if (at < 0) {
            tk_slice_push(out, &rest, sizeof rest);
            return;
        }

        tk_string piece = TK_STR(rest.ptr, at);
        tk_slice_push(out, &piece, sizeof piece);
        start += at + sep.len;
    }
}

bool tk_split_once(tk_string s, tk_string sep, tk_string *before, tk_string *after) {
    int64_t at = find(s, sep);
    if (at < 0) {
        return false;
    }

    *before = TK_STR(s.ptr, at);
    *after = TK_STR(s.ptr + at + sep.len, s.len - at - sep.len);
    return true;
}

void tk_init(int argc, char **argv) {
    for (int i = 0; i < argc; i++) {
        tk_string arg = TK_STR(argv[i], (int64_t)strlen(argv[i]));
        tk_slice_push(&tk_os_Args, &arg, sizeof arg);
    }
}

// cpath copies a Teak string into a C string for the system calls. A
// string holding a zero byte cannot be a path: the system would stop
// reading at the zero and open another file than the one named, so it
// is refused as an invalid argument.
static char *cpath(tk_string s) {
    tk_check_string(s);
    if (memchr(s.ptr, 0, (size_t)s.len) != NULL) {
        errno = EINVAL;
        return NULL;
    }

    char *p = tk_alloc((size_t)s.len + 1);
    memcpy(p, s.ptr, (size_t)s.len);
    p[s.len] = '\0';
    return p;
}

// sysopen opens a path with flags, or fails with errno set.
static int sysopen(tk_string path, int flags) {
    char *p = cpath(path);
    if (p == NULL) {
        return -1;
    }

    int fd = open(p, flags, 0666);
    tk_free(p);
    return fd;
}

// tk_io_reason is the system's text for an error number, with its
// first letter in lower case, as Go words it; the library puts the
// operation and the path in front.
tk_string tk_io_reason(int64_t code) {
    const char *reason = strerror((int)code);
    tk_string r = TK_STR(reason, (int64_t)strlen(reason));
    uint8_t *p = tk_alloc((size_t)r.len);
    memcpy(p, r.ptr, (size_t)r.len);
    if (r.len > 0 && p[0] >= 'A' && p[0] <= 'Z') {
        p[0] = (uint8_t)(p[0] + ('a' - 'A'));
    }

    return TK_OWNED(p, r.len);
}

// tk_io_kindOf sorts an error number as io.Kind does: 1 for a missing
// file, 2 for a permission refused, 3 for a file that exists, 0 for
// the rest.
int64_t tk_io_kindOf(int64_t code) {
    switch ((int)code) {
    case ENOENT:
        return 1;
    case EACCES:
    case EPERM:
        return 2;
    case EEXIST:
        return 3;
    }

    return 0;
}

int64_t tk_os_readToString(tk_string path, tk_string *out) {
    int fd = sysopen(path, O_RDONLY);
    if (fd < 0) {
        return errno;
    }

    tk_slice buf = {0};
    for (;;) {
        grow(&buf, buf.len + 4096, 1);
        ssize_t n = read(fd, (char *)buf.ptr + buf.len, (size_t)(buf.cap - buf.len));
        if (n < 0) {
            int code = errno;
            close(fd);
            tk_slice_free(&buf);
            return code;
        }

        if (n == 0) {
            break;
        }

        buf.len += n;
    }

    close(fd);
    tk_string_drop(out);
    *out = (tk_string){buf.ptr, buf.len, buf.cap};
    return 0;
}

int64_t tk_os_open(tk_string path, int64_t *fd) {
    int f = sysopen(path, O_RDONLY);
    if (f < 0) {
        return errno;
    }

    *fd = f;
    return 0;
}

int64_t tk_os_create(tk_string path, int64_t *fd) {
    int f = sysopen(path, O_WRONLY | O_CREAT | O_TRUNC);
    if (f < 0) {
        return errno;
    }

    *fd = f;
    return 0;
}

// tk_os_mkdirTemp makes a new directory under dir, or the system's
// temporary directory when dir is empty, named after pattern.
int64_t tk_os_mkdirTemp(tk_string dir, tk_string pattern, tk_string *out) {
    if (dir.len == 0) {
        const char *tmp = getenv("TMPDIR");
        if (tmp == NULL || tmp[0] == '\0') {
            tmp = "/tmp";
        }

        dir = TK_STR(tmp, (int64_t)strlen(tmp));
    }

    tk_string tmpl = tk_join(tk_join(dir, TK_STR("/", 1), 0), tk_join(pattern, TK_STR("XXXXXX", 6), 0), 3);
    char *p = cpath(tmpl);
    tk_string_free(tmpl);
    if (p == NULL || mkdtemp(p) == NULL) {
        int code = errno;
        tk_free(p);
        return code;
    }

    tk_string_drop(out);
    *out = TK_OWNED(p, tmpl.len);
    return 0;
}

static int remove_tree(const char *path) {
    struct stat st;
    if (lstat(path, &st) != 0) {
        return -1;
    }

    if (S_ISDIR(st.st_mode)) {
        DIR *d = opendir(path);
        if (d == NULL) {
            return -1;
        }

        struct dirent *ent;
        int rc = 0;
        while (rc == 0 && (ent = readdir(d)) != NULL) {
            if (strcmp(ent->d_name, ".") == 0 || strcmp(ent->d_name, "..") == 0) {
                continue;
            }

            size_t n = strlen(path) + 1 + strlen(ent->d_name) + 1;
            char *child = tk_alloc(n);
            snprintf(child, n, "%s/%s", path, ent->d_name);
            rc = remove_tree(child);
            tk_free(child);
        }

        closedir(d);
        if (rc != 0) {
            return rc;
        }

        return rmdir(path);
    }

    return unlink(path);
}

// tk_os_removeAll removes a tree; a path that is not there is nothing to
// remove, not a failure, as Go's RemoveAll has it.
int64_t tk_os_removeAll(tk_string path) {
    char *p = cpath(path);
    int rc = p == NULL ? -1 : remove_tree(p);
    int code = errno;
    tk_free(p);
    if (rc != 0 && p != NULL && code == ENOENT) {
        return 0;
    }

    return rc == 0 ? 0 : code;
}

int64_t tk_os_closeFile(int64_t fd) {
    return close((int)fd) == 0 ? 0 : errno;
}

int64_t tk_os_syncFile(int64_t fd) {
    return fsync((int)fd) == 0 ? 0 : errno;
}

// write_all writes every byte, through stdio for the standard streams
// so that prints and writes to them stay in order.
static bool write_all(int fd, const uint8_t *p, int64_t len) {
    if (fd == 1 || fd == 2) {
        return fwrite(p, 1, (size_t)len, fd == 1 ? stdout : stderr) == (size_t)len;
    }

    while (len > 0) {
        ssize_t n = write(fd, p, (size_t)len);
        if (n < 0) {
            return false;
        }

        p += n;
        len -= n;
    }

    return true;
}

int64_t tk_os_write(int64_t fd, tk_slice_u8 buf, int64_t *n) {
    tk_check_slice((tk_slice *)&buf);
    if (!write_all((int)fd, buf.ptr, buf.len)) {
        return errno;
    }

    *n = buf.len;
    return 0;
}

int64_t tk_os_writeString(int64_t fd, tk_string s, int64_t *n) {
    tk_check_string(s);
    if (!write_all((int)fd, s.ptr, s.len)) {
        return errno;
    }

    *n = s.len;
    return 0;
}

int64_t tk_os_read(int64_t fd, tk_slice_u8 *buf, int64_t *n) {
    tk_check_slice((tk_slice *)buf);
    ssize_t got = read((int)fd, buf->ptr, (size_t)buf->len);
    if (got < 0) {
        return errno;
    }

    *n = got;
    return 0;
}

void tk_print_string(tk_string s) {
    tk_check_string(s);
    fwrite(s.ptr, 1, (size_t)s.len, stdout);
}

void tk_print_owned(tk_string s) {
    tk_print_string(s);
    tk_string_free(s);
}
void tk_print_int(int64_t v) { printf("%lld", (long long)v); }
void tk_print_uint(uint64_t v) { printf("%llu", (unsigned long long)v); }
void tk_print_f64(double v) { tk_print_owned(tk_f64_to_string(v)); }

// tk_f64_fixed prints v with prec digits after the point, as %.2f
// does; a number too long for the buffer, which takes the largest
// double with a hundred decimals, falls back to an exponent.
tk_string tk_f64_fixed(double v, int prec) {
    char buf[448];
    if (snprintf(buf, sizeof buf, "%.*f", prec, v) >= (int)sizeof buf) {
        snprintf(buf, sizeof buf, "%.*e", prec, v);
    }

    return formatted(buf);
}

double tk_math_Sqrt(double x) { return sqrt(x); }
void tk_print_bool(bool v) { fputs(v ? "true" : "false", stdout); }
void tk_print_sep(void) { fputc(' ', stdout); }
void tk_print_nl(void) { fputc('\n', stdout); }
void tk_eprint_string(tk_string s) {
    tk_check_string(s);
    fwrite(s.ptr, 1, (size_t)s.len, stderr);
}

void tk_eprint_owned(tk_string s) {
    tk_eprint_string(s);
    tk_string_free(s);
}

// tk_strconv_parseFloat reads a floating-point number as C does, and
// asks that the whole text be the number: no space before it and
// nothing after it. 1 is a text that is not a number, 2 one that does
// not fit.
int64_t tk_strconv_parseFloat(tk_string s, double *out) {
    tk_check_string(s);
    if (s.len == 0 || isspace(s.ptr[0]) || memchr(s.ptr, 0, (size_t)s.len) != NULL) {
        return 1;
    }

    int64_t digits = s.ptr[0] == '+' || s.ptr[0] == '-' ? 1 : 0;
    if (s.len > digits + 1 && s.ptr[digits] == '0' && (s.ptr[digits + 1] == 'x' || s.ptr[digits + 1] == 'X')) {
        return 1; // C reads a hexadecimal float; a Teak literal has none
    }

    char *p = tk_alloc((size_t)s.len + 1);
    memcpy(p, s.ptr, (size_t)s.len);
    p[s.len] = '\0';
    char *end;
    errno = 0;
    double v = strtod(p, &end);
    int64_t code = end != p + s.len ? 1 : errno == ERANGE && fabs(v) == HUGE_VAL ? 2 : 0; // a number too small to hold rounds to zero, as a literal does
    tk_free(p);
    if (code == 0) {
        *out = v;
    }

    return code;
}

// tk_pad widens s to width characters with spaces, before it or after
// it when left says so, as a width in a format does; a character is a
// UTF-8 sequence, counted by its first byte. s is dropped when the
// caller owned it. A string already as wide comes back as it is, which
// drops as a view when it was one.
tk_string tk_pad(tk_string s, int64_t width, bool left, bool owned) {
    tk_check_string(s);
    int64_t chars = 0;
    for (int64_t i = 0; i < s.len; i++) {
        chars += (s.ptr[i] & 0xC0) != 0x80;
    }

    if (chars >= width) {
        return s;
    }

    int64_t fill = width - chars;
    uint8_t *p = tk_alloc((size_t)(s.len + fill));
    memset(p, ' ', (size_t)(s.len + fill));
    memcpy(p + (left ? 0 : fill), s.ptr, (size_t)s.len);
    width = s.len + fill;
    if (owned) {
        tk_string_free(s);
    }

    return TK_OWNED(p, width);
}

// tk_time_now reads the system's clock: nanoseconds since the start of
// 1970 in UTC.
int64_t tk_time_now(void) {
    struct timespec ts;
    clock_gettime(CLOCK_REALTIME, &ts);
    return (int64_t)ts.tv_sec * 1000000000 + ts.tv_nsec;
}

// tk_time_Sleep pauses for d nanoseconds, going back to sleep when a
// signal cuts the pause short.
void tk_time_Sleep(int64_t d) {
    if (d <= 0) {
        return;
    }

    struct timespec ts = {(time_t)(d / 1000000000), (long)(d % 1000000000)};
    while (nanosleep(&ts, &ts) != 0 && errno == EINTR) {
    }
}

tk_string tk_quote_owned(tk_string s) {
    tk_string q = tk_quote(s);
    tk_string_free(s);
    return q;
}
