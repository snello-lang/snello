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
#include <float.h>
#include <limits.h>
#include <math.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/resource.h>
#include <sys/stat.h>
#include <time.h>
#include <unistd.h>

tk_slice tk_os_Args = {0};

// The allocator is alloc.c's; a release build has nothing to check at
// the end, and debug.c has the report of a debug build.
#ifndef TK_DEBUG
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

void tk_slice_own(const tk_slice *s, const char *what) {
    if (s->cap == TK_VIEW) {
        char buf[160];
        int n = snprintf(buf, sizeof buf, "%s on a sub-slice: it lends the elements of its slice and cannot change its length", what);
        tk_panic(TK_STR(buf, n));
    }
}

int64_t tk_slice_cap(const tk_slice *s) {
    return s->cap == TK_VIEW ? s->len : s->cap;
}

void tk_slice_free(tk_slice *s) {
    tk_check_slice(s);
    tk_slice_own(s, "replacing");
    tk_free(s->ptr);
    s->ptr = NULL;
    s->len = 0;
    s->cap = 0;
    TK_POISON(*s);
}

// tk_slice_bounds stops the program for the slice expression [lo:hi]
// on n elements, in the words of Go's runtime: the high bound first.
_Noreturn void tk_slice_bounds(int64_t lo, int64_t hi, int64_t n) {
    char buf[128];
    if (hi < 0 || hi > n) {
        snprintf(buf, sizeof buf, "slice bounds out of range [:%lld] with length %lld", (long long)hi, (long long)n);
    } else if (lo < 0) {
        snprintf(buf, sizeof buf, "slice bounds out of range [%lld:]", (long long)lo);
    } else {
        snprintf(buf, sizeof buf, "slice bounds out of range [%lld:%lld]", (long long)lo, (long long)hi);
    }

    tk_panic(TK_STR(buf, (int64_t)strlen(buf)));
}

void tk_slice_make(tk_slice *out, int64_t len, int64_t cap, size_t size) {
    if (len < 0) {
        tk_panic(TK_STR("makeslice: len out of range", 27));
    }

    if (cap < len) {
        tk_panic(TK_STR("makeslice: cap out of range", 27));
    }

    out->ptr = tk_alloc((size_t)cap * size);
    memset(out->ptr, 0, (size_t)cap * size);
    out->len = len;
    out->cap = cap;
}

void tk_slice_push(tk_slice *s, const void *v, size_t size) {
    tk_slice_own(s, "Push");
    grow(s, s->len + 1, size);
    memcpy((char *)s->ptr + (size_t)s->len * size, v, size);
    s->len++;
}

bool tk_slice_pop(tk_slice *s, void *out, size_t size) {
    tk_check_slice(s);
    tk_slice_own(s, "Pop");
    if (s->len == 0) {
        return false;
    }

    s->len--;
    memcpy(out, (char *)s->ptr + (size_t)s->len * size, size);
    return true;
}

void tk_slice_insert(tk_slice *s, int64_t i, const void *v, size_t size) {
    tk_check_slice(s);
    tk_slice_own(s, "Insert");
    if ((uint64_t)i > (uint64_t)s->len) {
        tk_slice_bounds(i, s->len, s->len); // Go's slices.Insert: [7:3]
    }

    grow(s, s->len + 1, size);
    char *at = (char *)s->ptr + (size_t)i * size;
    memmove(at + size, at, (size_t)(s->len - i) * size);
    memcpy(at, v, size);
    s->len++;
}

void tk_slice_remove(tk_slice *s, int64_t i, void *out, size_t size) {
    tk_slice_own(s, "Remove");
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
        tk_slice_bounds(lo, hi, s->len);
    }

    out->ptr = (char *)s->ptr + (size_t)lo * size;
    out->len = hi - lo;
    out->cap = TK_VIEW;
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

// Floats print as Go's strconv prints them. A decimal is the digits of
// a positive finite number, without trailing zeros, and where the point
// goes among them: 0.25 is "25" with the point at 0, 120 is "12" with
// the point at 3. The digits come from the C library's correctly
// rounded %e, and the layout around them is Go's.
typedef struct {
    char *d;  // the digits, from tk_alloc
    int nd;   // how many
    int dp;   // the point's place
} decimal;

// decimal_of fills d with v's digits: with prec at zero the fewest that
// read back as v, as a float32 when f32 says v is one, and with prec
// above zero v rounded to prec significant digits.
static void decimal_of(double v, int prec, bool f32, decimal *d) {
    size_t size = (size_t)(prec > 17 ? prec : 17) + 16;
    char *buf = tk_alloc(size);
    if (prec == 0) {
        for (int p = 1; p <= (f32 ? 9 : 17); p++) {
            snprintf(buf, size, "%.*e", p - 1, v);
            if (f32 ? strtof(buf, NULL) == (float)v : strtod(buf, NULL) == v) {
                break;
            }
        }
    } else {
        snprintf(buf, size, "%.*e", prec - 1, v);
    }

    d->nd = 0;
    char *p = buf;
    for (; *p != 'e'; p++) {
        if (*p != '.') {
            buf[d->nd++] = *p;
        }
    }

    d->dp = atoi(p + 1) + 1;
    while (d->nd > 1 && buf[d->nd - 1] == '0') {
        d->nd--;
    }

    d->d = buf;
}

// The text being built: a block from tk_alloc and how much of it is
// written.
typedef struct {
    char *p;
    int64_t n;
} out_text;

static void out_byte(out_text *o, char c) { o->p[o->n++] = c; }

// put_e writes d as %e does, with prec digits after the point.
static void put_e(out_text *o, const decimal *d, int prec) {
    out_byte(o, d->d[0]);
    if (prec > 0) {
        out_byte(o, '.');
        for (int i = 1; i <= prec; i++) {
            out_byte(o, i < d->nd ? d->d[i] : '0');
        }
    }

    int exp = d->dp - 1;
    out_byte(o, 'e');
    out_byte(o, exp < 0 ? '-' : '+');
    exp = exp < 0 ? -exp : exp;
    o->n += snprintf(o->p + o->n, 8, "%02d", exp);
}

// put_f writes d as %f does, with prec digits after the point.
static void put_f(out_text *o, const decimal *d, int prec) {
    if (d->dp > 0) {
        for (int i = 0; i < d->dp; i++) {
            out_byte(o, i < d->nd ? d->d[i] : '0');
        }
    } else {
        out_byte(o, '0');
    }

    if (prec > 0) {
        out_byte(o, '.');
        for (int i = 0; i < prec; i++) {
            int j = d->dp + i;
            out_byte(o, j >= 0 && j < d->nd ? d->d[j] : '0');
        }
    }
}

// special writes the text of a float with no digits, an infinity or
// NaN, as Go spells them, or reports that v has digits.
static bool special(double v, tk_string *s) {
    if (v != v) {
        *s = TK_STR("NaN", 3);
    } else if (v > DBL_MAX) {
        *s = TK_STR("+Inf", 4);
    } else if (v < -DBL_MAX) {
        *s = TK_STR("-Inf", 4);
    } else {
        return false;
    }

    return true;
}

// format_g writes v as Go's %g does: shortest, the fewest digits that
// read back as v, as a float32 when f32 says so, or with prec
// significant digits; an exponent when the number's own is below -4 or
// at least the precision, which is 6 for the shortest form, so 1e+06
// and 123456 print as Go prints them.
static tk_string format_g(double v, int prec, bool f32) {
    tk_string s;
    if (special(v, &s)) {
        return s;
    }

    bool shortest = prec < 0;
    out_text o = {tk_alloc((size_t)(prec > 0 ? prec : 0) + 340), 0}; // a double's point is never 310 digits from its first
    if (signbit(v)) {
        out_byte(&o, '-');
        v = -v;
    }

    if (v == 0) {
        out_byte(&o, '0');
        return TK_OWNED(o.p, o.n);
    }

    decimal d;
    decimal_of(v, shortest ? 0 : (prec == 0 ? 1 : prec), f32, &d);
    if (shortest) {
        prec = d.nd;
    } else if (prec == 0) {
        prec = 1;
    }

    int eprec = prec;
    if (eprec > d.nd && d.nd >= d.dp) {
        eprec = d.nd;
    }

    if (shortest) {
        eprec = 6;
    }

    int exp = d.dp - 1;
    if (exp < -4 || exp >= eprec) {
        put_e(&o, &d, (prec > d.nd ? d.nd : prec) - 1);
    } else {
        int digits = prec > d.dp ? d.nd : prec;
        put_f(&o, &d, digits - d.dp > 0 ? digits - d.dp : 0);
    }

    tk_free(d.d);
    return TK_OWNED(o.p, o.n);
}

// tk_f64_to_string prints v as Go's %v and strconv.FormatFloat(v, 'g',
// -1, 64) do: the shortest text that reads back as v, 15 for 15.0,
// 1e+06 for a million, +Inf and NaN.
tk_string tk_f64_to_string(double v) {
    return format_g(v, -1, false);
}

// tk_f32_to_string is tk_f64_to_string for an f32: the fewest digits
// that read back as the same f32, so 0.1 prints as 0.1.
tk_string tk_f32_to_string(float v) {
    return format_g((double)v, -1, true);
}

// tk_f64_g prints v with prec significant digits, as Go's %.3v and
// %.3g do on a float.
tk_string tk_f64_g(double v, int64_t prec) {
    return format_g(v, (int)prec, false);
}

tk_string tk_bool_to_string(bool v) {
    return v ? TK_STR("true", 4) : TK_STR("false", 5);
}

// decode and encode read and write one rune's UTF-8 sequence; they are
// defined with the case mapping below.
static int decode(const uint8_t *p, int64_t n, int32_t *r);
static int encode(int32_t r, uint8_t *out);

// escape_rune writes r into p as Go's strconv writes it between quote
// marks: the quote and a backslash escaped, a printable rune as itself,
// the seven short escapes, a control byte as \xNN, and any other rune
// as \u or \U with its hex digits. It returns the bytes written.
static int64_t escape_rune(char *p, int32_t r, char quote) {
    if (r == quote || r == '\\') {
        p[0] = '\\';
        p[1] = (char)r;
        return 2;
    }

    if (tk_isprint(r)) {
        return encode(r, (uint8_t *)p);
    }

    const char *short_escape = NULL;
    switch (r) {
    case '\a': short_escape = "\\a"; break;
    case '\b': short_escape = "\\b"; break;
    case '\f': short_escape = "\\f"; break;
    case '\n': short_escape = "\\n"; break;
    case '\r': short_escape = "\\r"; break;
    case '\t': short_escape = "\\t"; break;
    case '\v': short_escape = "\\v"; break;
    }

    if (short_escape != NULL) {
        memcpy(p, short_escape, 2);
        return 2;
    }

    if (r < ' ' || r == 0x7F) {
        return snprintf(p, 5, "\\x%02x", (unsigned)r);
    }

    if (r < 0x10000) {
        return snprintf(p, 7, "\\u%04x", (unsigned)r);
    }

    return snprintf(p, 11, "\\U%08x", (unsigned)r);
}

// tk_quote writes s as Go's strconv.Quote and %q write a string: in
// double quotes, a byte that starts no UTF-8 sequence as \xNN, and every
// rune as escape_rune writes it.
tk_string tk_quote(tk_string s) {
    tk_check_string(s);
    char *p = tk_alloc((size_t)s.len * 4 + 3); // \xNN is the most one byte takes, and snprintf ends with a zero
    int64_t n = 0;
    p[n++] = '"';
    for (int64_t i = 0; i < s.len;) {
        int32_t r;
        int width = decode(s.ptr + i, s.len - i, &r);
        if (r == 0xFFFD && width == 1) { // a byte that starts no rune, as strconv.Quote tells it from a written U+FFFD
            n += snprintf(p + n, 5, "\\x%02x", s.ptr[i]);
        } else {
            n += escape_rune(p + n, r, '"');
        }

        i += width;
    }

    p[n++] = '"';
    return TK_OWNED(p, n);
}

// tk_quote_rune writes r as Go's strconv.QuoteRune and %q write a rune:
// in single quotes, with a value that is no rune, past U+10FFFF or a
// surrogate, written as U+FFFD.
tk_string tk_quote_rune(int64_t r) {
    if (r < 0 || r > 0x10FFFF || (r >= 0xD800 && r <= 0xDFFF)) {
        r = 0xFFFD;
    }

    char *p = tk_alloc(16);
    int64_t n = 0;
    p[n++] = '\'';
    n += escape_rune(p + n, (int32_t)r, '\'');
    p[n++] = '\'';
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

// spread finishes a key's hash so that every bit of it moves the low
// bits a slot is chosen by: the hashes of a type are built by mixing
// fields with a multiply, which only carries upward, so keys that
// differ in their high bits alone, multiples of 4096, would all land
// on a few slots and every probe would walk them. This is the final
// step of MurmurHash3, the finalizer Go's runtime uses where it has no
// AES hardware.
static uint64_t spread(uint64_t h) {
    h ^= h >> 33;
    h *= 0xff51afd7ed558ccdu;
    h ^= h >> 33;
    h *= 0xc4ceb9fe1a85ec53u;
    h ^= h >> 33;
    return h;
}

// probe returns the index slot holding the key, or the empty slot
// where it would go; the index must have room.
static int64_t probe(const tk_map *m, const tk_map_type *t, const void *key) {
    uint64_t mask = (uint64_t)m->slots - 1;
    for (uint64_t i = spread(t->hash(key)) & mask;; i = (i + 1) & mask) {
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
        tk_slice_bounds(lo, hi, s.len);
    }

    return TK_STR(s.ptr + lo, hi - lo);
}

// decode reads the rune at p, at most n bytes long, as Go's
// utf8.DecodeRune does: a sequence that is not valid UTF-8, cut short,
// overlong or a surrogate, reads as the replacement rune U+FFFD, one
// byte wide.
static int decode(const uint8_t *p, int64_t n, int32_t *r) {
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

// decode_last reads the rune that ends the n bytes at p, as Go's
// utf8.DecodeLastRune does.
static int decode_last(const uint8_t *p, int64_t n, int32_t *r) {
    if (p[n - 1] < 0x80) {
        *r = p[n - 1];
        return 1;
    }

    int64_t start = n - 1, lim = n - 4 < 0 ? 0 : n - 4;
    for (start--; start >= lim; start--) {
        if ((p[start] & 0xC0) != 0x80) {
            break;
        }
    }

    if (start < 0) {
        start = 0;
    }

    int w = decode(p + start, n - start, r);
    if (start + w != n) {
        *r = 0xFFFD;
        return 1;
    }

    return w;
}

// encode writes r as UTF-8 at out and returns its length.
static int encode(int32_t r, uint8_t *out) {
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

// mapped copies s with every rune through f, as Go's strings.Map does
// for ToUpper and ToLower: a byte that is not valid UTF-8 becomes the
// replacement rune, and a rune may change its length, so the length is
// counted before the copy. Text of ASCII alone is copied byte by byte.
static tk_string mapped(tk_string s, int32_t (*f)(int32_t)) {
    tk_check_string(s);
    bool ascii = true;
    for (int64_t i = 0; i < s.len && ascii; i++) {
        ascii = s.ptr[i] < 0x80;
    }

    if (ascii) {
        uint8_t *p = tk_alloc((size_t)s.len);
        for (int64_t i = 0; i < s.len; i++) {
            p[i] = (uint8_t)f(s.ptr[i]);
        }

        return TK_OWNED(p, s.len);
    }

    uint8_t tmp[4];
    int64_t n = 0;
    for (int64_t i = 0; i < s.len;) {
        int32_t r;
        i += decode(s.ptr + i, s.len - i, &r);
        n += encode(f(r), tmp);
    }

    uint8_t *p = tk_alloc((size_t)n);
    for (int64_t i = 0, at = 0; i < s.len;) {
        int32_t r;
        i += decode(s.ptr + i, s.len - i, &r);
        at += encode(f(r), p + at);
    }

    return TK_OWNED(p, n);
}

tk_string tk_to_upper(tk_string s) { return mapped(s, tk_rune_upper); }
tk_string tk_to_lower(tk_string s) { return mapped(s, tk_rune_lower); }

// space_at reports whether the rune at p, at most n bytes long, is
// white space, and its width in w.
static bool space_at(const uint8_t *p, int64_t n, int *w) {
    int32_t r;
    *w = decode(p, n, &r);
    return tk_rune_space(r);
}

// tk_trim lends s without the white space at either end, the runes
// Go's unicode.IsSpace reports, as strings.TrimSpace does.
tk_string tk_trim(tk_string s) {
    tk_check_string(s);
    int64_t lo = 0, hi = s.len;
    int w;
    while (lo < hi && space_at(s.ptr + lo, hi - lo, &w)) {
        lo += w;
    }

    while (hi > lo) {
        int32_t r;
        int back = decode_last(s.ptr + lo, hi - lo, &r);
        if (!tk_rune_space(r)) {
            break;
        }

        hi -= back;
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

// rabin_karp returns the offset of t in s, or -1, by a rolling hash of
// every window of s, compared with t only where the hashes agree: time
// in proportion to the length of s, whatever t is. It is Go's
// indexRabinKarp, with Go's prime.
static int64_t rabin_karp(const uint8_t *s, int64_t n, const uint8_t *t, int64_t m) {
    const uint32_t prime = 16777619;
    uint32_t ht = 0, h = 0, pow = 1;
    for (int64_t i = 0; i < m; i++) {
        ht = ht * prime + t[i];
        h = h * prime + s[i];
        pow *= prime;
    }

    if (h == ht && memcmp(s, t, (size_t)m) == 0) {
        return 0;
    }

    for (int64_t i = m; i < n; i++) {
        h = h * prime + s[i] - pow * s[i - m];
        if (h == ht && memcmp(s + i - m + 1, t, (size_t)m) == 0) {
            return i - m + 1;
        }
    }

    return -1;
}

// find returns the offset of t in s, or -1, as Go's strings.Index
// finds it: memchr to the next place the first byte matches, and the
// whole of t compared there; once the misses outrun the progress, the
// rest of s is searched by rabin_karp, so that no t, however close to
// matching everywhere, makes a search cost the product of the lengths.
static int64_t find(tk_string s, tk_string t) {
    tk_check_string(s);
    tk_check_string(t);
    int64_t n = s.len, m = t.len;
    if (m == 0) {
        return 0;
    }

    if (m > n) {
        return -1;
    }

    int64_t last = n - m; // the last offset t fits at
    int64_t fails = 0;
    for (int64_t i = 0; i <= last;) {
        const uint8_t *p = memchr(s.ptr + i, t.ptr[0], (size_t)(last - i + 1));
        if (p == NULL) {
            return -1;
        }

        i = p - s.ptr;
        if (memcmp(p, t.ptr, (size_t)m) == 0) {
            return i;
        }

        i++;
        fails++;
        if (fails >= 4 + (i >> 4) && i <= last) {
            int64_t at = rabin_karp(s.ptr + i, n - i, t.ptr, m);
            return at < 0 ? -1 : i + at;
        }
    }

    return -1;
}

bool tk_string_contains(tk_string s, tk_string t) { return find(s, t) >= 0; }

// tk_fields splits s around runs of white space, the runes Go's
// unicode.IsSpace reports, into views of s, as strings.Fields does.
void tk_fields(tk_string s, tk_slice *out) {
    tk_check_string(s);
    out->ptr = NULL;
    out->len = 0;
    out->cap = 0;
    int64_t i = 0;
    int w;
    while (i < s.len) {
        while (i < s.len && space_at(s.ptr + i, s.len - i, &w)) {
            i += w;
        }

        int64_t start = i;
        while (i < s.len && !space_at(s.ptr + i, s.len - i, &w)) {
            i += w;
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

// tk_split splits s at every sep into views of s. An empty separator
// splits after every UTF-8 sequence, a byte that is not one counting
// as one, and gives no pieces of an empty string, as Go's strings.Split
// does.
void tk_split(tk_string s, tk_string sep, tk_slice *out) {
    tk_check_string(s);
    tk_check_string(sep);
    out->ptr = NULL;
    out->len = 0;
    out->cap = 0;
    if (sep.len == 0) {
        for (int64_t i = 0; i < s.len;) {
            int32_t r;
            int w = decode(s.ptr + i, s.len - i, &r);
            tk_string piece = TK_STR(s.ptr + i, w);
            tk_slice_push(out, &piece, sizeof piece);
            i += w;
        }

        return;
    }

    int64_t start = 0;
    for (;;) {
        tk_string rest = TK_STR(s.ptr + start, s.len - start);
        int64_t at = find(rest, sep);
        if (at < 0) {
            tk_slice_push(out, &rest, sizeof rest);
            return;
        }

        tk_string piece = TK_STR(rest.ptr, at);
        tk_slice_push(out, &piece, sizeof piece);
        start += at + sep.len;
    }
}

// tk_strings_appendString appends the bytes of s to b in one copy,
// growing it once: the strings package's Builder and Repeat write
// through it.
void tk_strings_appendString(tk_slice_u8 *buf, tk_string s) {
    tk_slice *b = (tk_slice *)buf; // the same three words
    tk_check_string(s);
    if (s.len == 0) {
        return;
    }

    tk_slice_own(b, "WriteString");
    grow(b, b->len + s.len, 1);
    memcpy((uint8_t *)b->ptr + b->len, s.ptr, (size_t)s.len);
    b->len += s.len;
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

// sysopen opens a path with flags, or fails with errno set. An open a
// signal interrupts, as one of a FIFO may be, is asked again.
static int sysopen(tk_string path, int flags) {
    char *p = cpath(path);
    if (p == NULL) {
        return -1;
    }

    int fd;
    do {
        fd = open(p, flags, 0666);
    } while (fd < 0 && errno == EINTR);
    tk_free(p);
    return fd;
}

// The most one read or write asks of the system, as Go's runtime caps
// it: Darwin refuses a count past INT_MAX, so a larger buffer is read
// or written in turns.
#define TK_IO_MAX ((size_t)1 << 30)

// sysread reads at most len bytes, asking again when a signal
// interrupts the call, and returns what read returns, with errno set
// on a failure.
static ssize_t sysread(int fd, void *p, size_t len) {
    ssize_t n;
    do {
        n = read(fd, p, len < TK_IO_MAX ? len : TK_IO_MAX);
    } while (n < 0 && errno == EINTR);
    return n;
}

// write_fd writes every byte of p, asking again after a short write or
// an interrupted call, and returns how many it wrote: all of len, or
// fewer with errno saying why.
static int64_t write_fd(int fd, const uint8_t *p, int64_t len) {
    int64_t done = 0;
    while (done < len) {
        size_t want = (size_t)(len - done);
        ssize_t n = write(fd, p + done, want < TK_IO_MAX ? want : TK_IO_MAX);
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
// file, 2 for a permission refused, 3 for a file that exists, 4 for a
// connection refused, 5 for one reset or broken, 6 for one timed out,
// 0 for the rest.
int64_t tk_io_kindOf(int64_t code) {
    switch ((int)code) {
    case ENOENT:
        return 1;
    case EACCES:
    case EPERM:
        return 2;
    case EEXIST:
        return 3;
    case ECONNREFUSED:
        return 4;
    case ECONNRESET:
    case ECONNABORTED:
    case EPIPE:
        return 5;
    case ETIMEDOUT:
        return 6;
    }

    return 0;
}

// tk_os_readToString reads the named file whole. On a failure, inRead
// says which call failed, 0 for the open and 1 for a read, so that the
// error names it as Go's does.
int64_t tk_os_readToString(tk_string path, tk_string *out, int64_t *inRead) {
    *inRead = 0;
    int fd = sysopen(path, O_RDONLY);
    if (fd < 0) {
        return errno;
    }

    tk_slice buf = {0};
    for (;;) {
        grow(&buf, buf.len + 4096, 1);
        ssize_t n = sysread(fd, (char *)buf.ptr + buf.len, (size_t)(buf.cap - buf.len));
        if (n < 0) {
            int code = errno;
            close(fd);
            tk_slice_free(&buf);
            *inRead = 1;
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

static uint64_t next_random(void);

// tk_os_mkdirTemp makes a new directory under dir, or the system's
// temporary directory when dir is empty, named as Go names it: the
// pattern with its last * replaced by a random number, or followed by
// one when it has none, trying another number while the name is
// taken. On a failure out holds the name the error is about and op
// the call that failed: mkdirtemp, or stat when dir itself is
// missing, which Go's MkdirTemp reports by stating dir.
int64_t tk_os_mkdirTemp(tk_string dir, tk_string pattern, tk_string *out, tk_string *op) {
    tk_string_drop(op);
    *op = TK_STR("mkdirtemp", 9);
    if (dir.len == 0) {
        const char *tmp = getenv("TMPDIR");
        if (tmp == NULL || tmp[0] == '\0') {
            tmp = "/tmp";
        }

        dir = TK_STR(tmp, (int64_t)strlen(tmp));
    }

    int64_t star = pattern.len;
    for (int64_t i = pattern.len - 1; i >= 0; i--) {
        if (pattern.ptr[i] == '*') {
            star = i;
            break;
        }
    }

    tk_string prefix = tk_join(tk_join(dir, TK_STR("/", 1), 0), TK_STR(pattern.ptr, star), 1);
    tk_string suffix = star < pattern.len ? TK_STR(pattern.ptr + star + 1, pattern.len - star - 1) : TK_STR("", 0);
    int code = EEXIST;
    for (int try = 0; try < 10000 && code == EEXIST; try++) {
        char digits[24];
        int n = snprintf(digits, sizeof digits, "%u", (unsigned)(next_random() >> 32));
        tk_string name = tk_join(tk_join(prefix, TK_STR(digits, n), 0), suffix, 1);
        char *p = cpath(name);
        if (p != NULL && mkdir(p, 0700) == 0) {
            tk_free(p);
            tk_string_free(prefix);
            tk_string_drop(out);
            *out = name;
            return 0;
        }

        code = errno;
        tk_free(p);
        tk_string_free(name);
    }

    if (code == ENOENT) {
        // The parent may be what is missing: Go stats it and reports
        // that when it is.
        struct stat st;
        char *p = cpath(dir);
        int missing = p != NULL && stat(p, &st) != 0 && errno == ENOENT;
        tk_free(p);
        if (missing) {
            tk_string_free(prefix);
            tk_string_drop(out);
            *out = tk_string_clone(dir);
            *op = TK_STR("stat", 4);
            return ENOENT;
        }
    }

    tk_string_drop(out);
    *out = tk_join(tk_join(prefix, TK_STR("*", 1), 1), suffix, 1); // the pattern of the names tried, as Go's error gives it
    return code;
}

// failed_at records, for a removal that failed, the entry it failed on
// and the call that failed, named as Go's error names it, keeping errno.
static int failed_at(const char *path, const char *op, tk_string *failed, tk_string *what) {
    int code = errno;
    tk_string_drop(failed);
    *failed = tk_string_clone(TK_STR(path, (int64_t)strlen(path)));
    tk_string_drop(what);
    *what = TK_STR(op, (int64_t)strlen(op));
    errno = code;
    return -1;
}

// A removal walks the tree as Go's removeAllFrom does: every call is
// relative to an open directory, so a path longer than the system
// allows is no obstacle, and the walk keeps a level per directory it
// is inside of, a few words each, on a slice of its own rather than
// the task's stack, which a tree a few thousand deep would outgrow. A
// level is an open directory, how long the path of the entry in hand
// is up to it, and whether a pass over its entries has handled any.
typedef struct {
    DIR *d;
    int64_t len;
    bool seen;
} level;

// remove_entry removes the entry name of the directory fd, which path
// names in full for the error, or opens it and pushes it on levels when
// it is a directory whose entries must go first; len is how long path
// is. unlinkat is tried first, since most entries are files, and only
// its refusal makes the directory question worth a stat; an entry that
// is already gone was removed by someone else, which is what was asked.
static int remove_entry(int fd, const char *name, const char *path, int64_t len, tk_slice *levels, tk_string *failed, tk_string *op) {
    if (unlinkat(fd, name, 0) == 0 || errno == ENOENT) {
        return 0;
    }

    int code = errno;
    if (code != EISDIR && code != EPERM && code != EACCES) {
        return failed_at(path, "unlinkat", failed, op);
    }

    struct stat st;
    if (fstatat(fd, name, &st, AT_SYMLINK_NOFOLLOW) != 0) {
        return errno == ENOENT ? 0 : failed_at(path, "fstatat", failed, op);
    }

    if (!S_ISDIR(st.st_mode)) {
        errno = code;
        return failed_at(path, "unlinkat", failed, op);
    }

    int sub = openat(fd, name, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
    if (sub < 0) {
        return errno == ENOENT ? 0 : failed_at(path, "openfdat", failed, op);
    }

    DIR *d = fdopendir(sub);
    if (d == NULL) {
        code = errno;
        close(sub);
        errno = code;
        return failed_at(path, "openfdat", failed, op);
    }

    level l = {d, len, false};
    tk_slice_push(levels, &l, sizeof l);
    return 0;
}

// remove_top removes the directory at the top of levels, emptied by the
// passes over it, from the level below, or from the working directory
// for the tree's root; path names it in full, and name is its name in
// that directory. A directory still not empty after a pass that removed
// something is read again, since a directory read while its entries go
// may skip some, as Go's RemoveAll notes; one that stays full after a
// pass that found nothing is the failure it reports.
static int remove_top(tk_slice *levels, char *path, tk_string *failed, tk_string *op) {
    level *top = &((level *)levels->ptr)[levels->len - 1];
    level *below = levels->len > 1 ? top - 1 : NULL;
    path[top->len] = '\0';
    int fd = below != NULL ? dirfd(below->d) : AT_FDCWD;
    const char *name = below != NULL ? path + below->len + 1 : path;
    if (unlinkat(fd, name, AT_REMOVEDIR) == 0 || errno == ENOENT) {
        closedir(top->d);
        levels->len--;
        return 0;
    }

    if ((errno == ENOTEMPTY || errno == EEXIST) && top->seen) {
        top->seen = false;
        rewinddir(top->d);
        return 0;
    }

    return failed_at(path, "unlinkat", failed, op);
}

// tk_os_removeAll removes a tree; a path that is not there is nothing to
// remove, not a failure, as Go's RemoveAll has it. On a failure, failed
// and op name the entry it failed on and the call, as Go's error names
// them, rather than the path asked for. The walk is depth first over
// the levels above, with the full path of the entry in hand kept in
// full, the path asked for and then a slash and a name per level.
int64_t tk_os_removeAll(tk_string path, tk_string *failed, tk_string *op) {
    char *p = cpath(path);
    if (p == NULL) {
        int code = errno;
        tk_string_drop(failed);
        *failed = tk_string_clone(path);
        tk_string_drop(op);
        *op = TK_STR("RemoveAll", 9);
        return code;
    }

    tk_slice levels = {0};
    tk_slice full = {0};
    tk_strings_appendString((tk_slice_u8 *)&full, TK_STR(p, (int64_t)strlen(p) + 1)); // with its zero byte
    int rc = remove_entry(AT_FDCWD, p, p, (int64_t)strlen(p), &levels, failed, op);
    while (rc == 0 && levels.len > 0) {
        level *top = &((level *)levels.ptr)[levels.len - 1];
        errno = 0;
        struct dirent *ent = readdir(top->d);
        if (ent == NULL) {
            if (errno != 0) {
                ((char *)full.ptr)[top->len] = '\0';
                rc = failed_at(full.ptr, "readdirent", failed, op);
                break;
            }

            rc = remove_top(&levels, full.ptr, failed, op);
            continue;
        }

        if (strcmp(ent->d_name, ".") == 0 || strcmp(ent->d_name, "..") == 0) {
            continue;
        }

        top->seen = true;
        full.len = top->len;
        tk_strings_appendString((tk_slice_u8 *)&full, TK_STR("/", 1));
        tk_strings_appendString((tk_slice_u8 *)&full, TK_STR(ent->d_name, (int64_t)strlen(ent->d_name) + 1));
        rc = remove_entry(dirfd(top->d), ent->d_name, full.ptr, full.len - 1, &levels, failed, op);
    }

    int code = errno;
    for (int64_t i = 0; i < levels.len; i++) {
        closedir(((level *)levels.ptr)[i].d);
    }

    tk_slice_free(&levels);
    tk_slice_free(&full);
    tk_free(p);
    return rc == 0 ? 0 : code;
}

// tk_os_remove removes a file or an empty directory, as Go's Remove
// does: unlink first, then rmdir, and when both fail the error of the
// one that fits, which rmdir's ENOTDIR on a file says.
int64_t tk_os_remove(tk_string path) {
    char *p = cpath(path);
    if (p == NULL) {
        return errno;
    }

    int code = 0;
    if (unlink(p) != 0) {
        int first = errno;
        if (rmdir(p) != 0) {
            code = errno == ENOTDIR ? first : errno;
        }
    }

    tk_free(p);
    return code;
}

int64_t tk_os_closeFile(int64_t fd) {
    return close((int)fd) == 0 ? 0 : errno;
}

int64_t tk_os_syncFile(int64_t fd) {
    return fsync((int)fd) == 0 ? 0 : errno;
}

// The standard streams are written as any file is, straight to the
// descriptor with nothing held back, as Go's os.Stdout is: what fmt
// prints, what is written to os.Stdout and os.Stderr, and a panic's
// message come out in the order the program made them, a prompt
// without a newline shows at once, and a write that fails reports the
// failure. fmt builds each call's text whole and writes it at once, so
// two tasks' lines do not interleave.
int64_t tk_os_write(int64_t fd, tk_slice_u8 buf, int64_t *n) {
    tk_check_slice((tk_slice *)&buf);
    if (write_fd((int)fd, buf.ptr, buf.len) < buf.len) {
        return errno;
    }

    *n = buf.len;
    return 0;
}

int64_t tk_os_writeString(int64_t fd, tk_string s, int64_t *n) {
    tk_check_string(s);
    if (write_fd((int)fd, s.ptr, s.len) < s.len) {
        return errno;
    }

    *n = s.len;
    return 0;
}

int64_t tk_os_read(int64_t fd, tk_slice_u8 *buf, int64_t *n) {
    tk_check_slice((tk_slice *)buf);
    ssize_t got = sysread((int)fd, buf->ptr, (size_t)buf->len);
    if (got < 0) {
        return errno;
    }

    *n = got;
    return 0;
}

void tk_print_string(tk_string s) {
    tk_check_string(s);
    write_fd(1, s.ptr, s.len);
}

void tk_print_owned(tk_string s) {
    tk_print_string(s);
    tk_string_free(s);
}

void tk_print_int(int64_t v) {
    char buf[24];
    int n = snprintf(buf, sizeof buf, "%lld", (long long)v);
    write_fd(1, (const uint8_t *)buf, n);
}

void tk_print_uint(uint64_t v) {
    char buf[24];
    int n = snprintf(buf, sizeof buf, "%llu", (unsigned long long)v);
    write_fd(1, (const uint8_t *)buf, n);
}

// tk_f64_fixed prints v with prec digits after the point, as %.2f
// does, however many that takes, and an infinity or NaN as Go spells
// it.
tk_string tk_f64_fixed(double v, int prec) {
    tk_string s;
    if (special(v, &s)) {
        return s;
    }

    int n = snprintf(NULL, 0, "%.*f", prec, v);
    char *p = tk_alloc((size_t)n + 1);
    snprintf(p, (size_t)n + 1, "%.*f", prec, v);
    return TK_OWNED(p, n);
}

// tk_int_digits gives the decimal text of an integer, which it takes,
// at least prec digits long, zeros after the sign making up the rest,
// as Go's %.3d does; a precision of zero prints zero as nothing.
tk_string tk_int_digits(tk_string s, int64_t prec) {
    tk_check_string(s);
    int64_t sign = s.len > 0 && s.ptr[0] == '-' ? 1 : 0;
    int64_t digits = s.len - sign;
    if (prec == 0 && digits == 1 && s.ptr[sign] == '0') {
        tk_string_free(s);
        return TK_STR("", 0);
    }

    if (digits >= prec) {
        return s;
    }

    int64_t n = sign + prec;
    char *p = tk_alloc((size_t)n);
    memcpy(p, s.ptr, (size_t)sign);
    memset(p + sign, '0', (size_t)(prec - digits));
    memcpy(p + sign + prec - digits, s.ptr + sign, (size_t)digits);
    tk_string_free(s);
    return TK_OWNED(p, n);
}

// tk_truncate gives the first prec characters of s, a character being
// a UTF-8 sequence, as Go's %.2s does; s is dropped when the caller
// owned it, and the result is owned then too.
tk_string tk_truncate(tk_string s, int64_t prec, bool owned) {
    tk_check_string(s);
    int64_t i = 0;
    for (int64_t chars = 0; i < s.len && chars < prec; chars++) {
        int32_t r;
        i += decode(s.ptr + i, s.len - i, &r);
    }

    if (!owned) {
        return (tk_string){s.ptr, i, 0};
    }

    char *p = tk_alloc((size_t)(i > 0 ? i : 1));
    memcpy(p, s.ptr, (size_t)i);
    tk_string_free(s);
    return TK_OWNED(p, i);
}

// tk_f64_min and tk_f64_max are the predeclared min and max on floats,
// as Go's specification has them: NaN when either is NaN, and -0 below
// +0.
double tk_f64_min(double a, double b) {
    if (a != a || b != b) {
        return a != a ? a : b;
    }

    if (a == b) {
        return signbit(a) ? a : b;
    }

    return a < b ? a : b;
}

double tk_f64_max(double a, double b) {
    if (a != a || b != b) {
        return a != a ? a : b;
    }

    if (a == b) {
        return signbit(a) ? b : a;
    }

    return a > b ? a : b;
}

double tk_math_Sqrt(double x) { return sqrt(x); }
void tk_eprint_string(tk_string s) {
    tk_check_string(s);
    write_fd(2, s.ptr, s.len);
}

void tk_eprint_owned(tk_string s) {
    tk_eprint_string(s);
    tk_string_free(s);
}

// fold reports whether the n bytes at p are word, in any case.
static bool fold(const uint8_t *p, int64_t n, const char *word) {
    if ((size_t)n != strlen(word)) {
        return false;
    }

    for (int64_t i = 0; i < n; i++) {
        if ((p[i] | 0x20) != word[i]) {
            return false;
        }
    }

    return true;
}

// underscores_ok is Go's rule for the underscores in a number: each
// stands between two digits, a base prefix counting as a digit.
static bool underscores_ok(const uint8_t *p, int64_t n) {
    char saw = '^'; // the start, a 0 for a digit, an _, or ! for anything else
    int64_t i = 0;
    if (n >= 1 && (p[0] == '+' || p[0] == '-')) {
        i = 1;
    }

    bool hex = false;
    if (n - i >= 2 && p[i] == '0' && (p[i + 1] | 0x20) == 'x') {
        i += 2;
        saw = '0';
        hex = true;
    }

    for (; i < n; i++) {
        if (isdigit(p[i]) || (hex && isxdigit(p[i]))) {
            saw = '0';
        } else if (p[i] == '_') {
            if (saw != '0') {
                return false;
            }

            saw = '_';
        } else {
            if (saw == '_') {
                return false;
            }

            saw = '!';
        }
    }

    return saw != '_';
}

// go_float writes s into out as C's strtod reads it, without its
// underscores, when s is a float as Go's strconv.ParseFloat reads one:
// a sign or none; then inf or infinity in any case, nan in any case
// without a sign, decimal digits with a point and an exponent or not,
// or 0x and hexadecimal digits with a point or not and the p exponent
// Go asks of them.
static bool go_float(tk_string s, char *out) {
    const uint8_t *p = s.ptr;
    int64_t i = 0, n = 0;
    if (s.len > 0 && (p[0] == '+' || p[0] == '-')) {
        out[n++] = (char)p[i++];
    }

    if (fold(p + i, s.len - i, "inf") || fold(p + i, s.len - i, "infinity") || (i == 0 && fold(p, s.len, "nan"))) {
        memcpy(out + n, p + i, (size_t)(s.len - i));
        out[n + s.len - i] = '\0';
        return true;
    }

    if (!underscores_ok(p, s.len)) {
        return false;
    }

    bool hex = s.len - i >= 2 && p[i] == '0' && (p[i + 1] | 0x20) == 'x';
    if (hex) {
        out[n++] = '0';
        out[n++] = 'x';
        i += 2;
    }

    bool digits = false, point = false, exp = false;
    for (; i < s.len; i++) {
        uint8_t c = p[i];
        if (c == '_') {
            continue;
        }

        if (c == '.' && !point) {
            point = true;
        } else if (hex ? isxdigit(c) : isdigit(c)) {
            digits = true;
        } else if (digits && (c | 0x20) == (hex ? 'p' : 'e')) {
            exp = true;
            out[n++] = (char)c;
            i++;
            if (i < s.len && (p[i] == '+' || p[i] == '-')) {
                out[n++] = (char)p[i++];
            }

            bool any = false;
            for (; i < s.len; i++) {
                if (isdigit(p[i])) {
                    out[n++] = (char)p[i];
                    any = true;
                } else if (p[i] != '_') {
                    return false;
                }
            }

            if (!any) {
                return false;
            }

            break;
        } else {
            return false;
        }

        out[n++] = (char)c;
    }

    out[n] = '\0';
    return digits && (!hex || exp);
}

// tk_strconv_parseFloat reads a floating-point number as Go's
// strconv.ParseFloat does, the whole text being the number. 1 is a text
// that is not a number, 2 one that does not fit.
int64_t tk_strconv_parseFloat(tk_string s, double *out) {
    tk_check_string(s);
    char *p = tk_alloc((size_t)s.len + 1);
    if (!go_float(s, p)) {
        tk_free(p);
        return 1;
    }

    char *end;
    errno = 0;
    double v = strtod(p, &end);
    int64_t code = *end != '\0' ? 1 : errno == ERANGE && fabs(v) == HUGE_VAL ? 2 : 0; // a number too small to hold rounds to zero, as a literal does
    tk_free(p);
    if (code == 0) {
        *out = v;
    }

    return code;
}

// tk_pad widens s to width characters with spaces, before it or after
// it when left says so, as a width in a format does; a character is a
// UTF-8 sequence, counted by its first byte. s is dropped when the
// caller owned it, and the result is owned whatever s was: the emitted
// code frees it, so a string already as wide comes back as it is only
// when it was owned, and as a copy when it was lent, a variable or a
// field whose bytes the caller keeps.
tk_string tk_pad(tk_string s, int64_t width, bool left, bool owned) {
    tk_check_string(s);
    int64_t chars = 0;
    for (int64_t i = 0; i < s.len; i++) {
        chars += (s.ptr[i] & 0xC0) != 0x80;
    }

    if (chars >= width) {
        return owned ? s : tk_string_clone(s);
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

// tk_time_mono reads the monotonic clock, which the system never sets
// back: nanoseconds since a moment of its own, plus one, so that a
// reading is never 0, which a Time without one holds.
int64_t tk_time_mono(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000000000 + ts.tv_nsec + 1;
}

// tk_time_clock is package time's clock: the wall clock's seconds and
// nanoseconds, and the monotonic clock's reading.
void tk_time_clock(int64_t *sec, int64_t *nsec, int64_t *mono) {
    struct timespec ts;
    clock_gettime(CLOCK_REALTIME, &ts);
    *sec = (int64_t)ts.tv_sec;
    *nsec = ts.tv_nsec;
    *mono = tk_time_mono();
}

// tk_time_read is a Time of now, for the timer to put in a channel.
tk_time tk_time_read(void) {
    tk_time t;
    tk_time_clock(&t.sec, &t.nsec, &t.mono);
    return t;
}

// tk_time_Sleep parks the task for d nanoseconds; the worker runs
// others meanwhile.
void tk_time_Sleep(int64_t d) {
    tk_sleep(d);
}

tk_string tk_quote_owned(tk_string s) {
    tk_string q = tk_quote(s);
    tk_string_free(s);
    return q;
}

// Tasks and channels: _docs/design/06-tasks-and-channels.md. The
// tasks themselves are sched.c's; a channel is a locked ring buffer
// with a list of the tasks waiting on it, and a task that must wait
// parks until a send, receive or close wakes it.

// A task that must wait on a channel, or on several in a select, puts
// a link to itself on each channel's list, releases the channels and
// parks; every send, receive and close on a channel wakes the tasks
// on its list, which take the channel's lock again and recheck. A
// wake kept by the scheduler while the task was between the release
// and the park is consumed by the park, and a wake from a channel the
// task has stopped waiting on costs it one recheck. A link lives in
// the frame of the wait, one per channel waited on, and is taken off
// the list before that frame ends.
typedef struct waitlink {
    tk_task *t;
    struct waitlink *next;
} waitlink;

// An unbuffered channel is a ring of one element, the hand-off slot,
// whose sender waits on until a receiver has taken its value: the
// deposits and takes are counted so that a sender knows when its own
// value, and not a later sender's, was taken. The channel counts its
// handles: Clone adds one, a handle's drop takes one away, and the
// last frees the channel, dropping the values still in the ring with
// the element's drop the compiler gave, NULL for one that owns nothing.
// No task can be waiting on it then, since a waiting task holds a
// handle, and nothing else can reach it.
struct tk_chan {
    pthread_mutex_t mu;
    _Atomic int64_t handles;
    _Atomic int64_t timer; // where the channel's After or Tick is in the timers' heap, -1 when it has none there
    void (*drop)(void *); // drops one element
    size_t size;      // of one element
    int64_t cap;      // zero for an unbuffered channel
    uint8_t *buf;     // the ring: cap elements, or one
    int64_t head;     // the oldest element of the ring
    int64_t len;      // elements in the ring
    int64_t deposits; // values put in so far
    int64_t takes;    // values taken out so far
    int64_t receiving; // tasks waiting to receive, which is what lets a select send on an unbuffered channel
    bool closed;
    waitlink *waiters;
};

// room is the ring's size.
static int64_t room(tk_chan *c) {
    return c->cap > 0 ? c->cap : 1;
}

static uint8_t *slot(tk_chan *c, int64_t i);

// tk_chan_make makes a channel with its first handle. Its memory is
// tk_alloc's, so a debug build reports a channel no handle freed.
tk_chan *tk_chan_make(size_t size, int64_t cap, void (*drop)(void *)) {
    tk_chan *c = tk_alloc(sizeof *c);
    memset(c, 0, sizeof *c);
    c->cap = cap;
    c->buf = tk_alloc((size == 0 ? 1 : size) * (size_t)room(c));
    pthread_mutex_init(&c->mu, NULL);
    atomic_init(&c->handles, 1);
    atomic_init(&c->timer, -1);
    c->drop = drop;
    c->size = size;
    return c;
}

// tk_chan_timer and tk_chan_placeTimer read and set where the
// channel's timer is in the timers' heap, which sched.c keeps under its
// lock; -1 is none.
int64_t tk_chan_timer(tk_chan *c) {
    return atomic_load(&c->timer);
}

void tk_chan_placeTimer(tk_chan *c, int64_t at) {
    atomic_store(&c->timer, at);
}

// tk_chan_alone reports whether the caller's handle is the only one.
bool tk_chan_alone(tk_chan *c) {
    return atomic_load(&c->handles) == 1;
}

// tk_chan_clone gives another handle on c, for another task.
tk_chan *tk_chan_clone(tk_chan *c) {
    atomic_fetch_add(&c->handles, 1);
    return c;
}

// tk_chan_drop drops the handle at *c, leaving the place empty, as a
// move does; the last handle frees the channel and what it holds. When
// the one left is a pending timer's, which only a timer still in the
// heap holds, no one can receive from the channel again, and the timer
// is taken out and its handle dropped at once, so a select that took
// another case leaves no After behind it until its deadline.
void tk_chan_drop(tk_chan **h) {
    tk_chan *c = *h;
    *h = NULL;
    if (c == NULL) {
        return;
    }

    int64_t before = atomic_fetch_sub(&c->handles, 1);
    if (before == 2 && atomic_load(&c->timer) >= 0) {
        tk_timer_abandon(c);
        return;
    }

    if (before != 1) {
        return;
    }

    if (c->drop != NULL) {
        for (int64_t i = 0; i < c->len; i++) {
            c->drop(slot(c, c->head + i));
        }
    }

    pthread_mutex_destroy(&c->mu);
    tk_free(c->buf);
    tk_free(c);
}

// The list of waiting tasks, and the wait on it, belong to whatever
// holds the list: a channel here, a lock below. wake_all signals every
// task on the list and empties it: a task that still has to wait puts
// itself back.
static void wake_all(waitlink **list) {
    for (waitlink *l = *list; l != NULL; l = l->next) {
        tk_wake(l->t);
    }

    *list = NULL;
}

// unlink_from takes l off the list when it is still there: a wake from
// another channel may have brought the task back before this one woke
// it.
static void unlink_from(waitlink **list, waitlink *l) {
    for (waitlink **p = list; *p != NULL; p = &(*p)->next) {
        if (*p == l) {
            *p = l->next;
            return;
        }
    }
}

// wait_on puts the calling task on the list, releases the mutex that
// guards it, waits for a wake, and takes the mutex again.
static void wait_on(pthread_mutex_t *mu, waitlink **list) {
    waitlink l = {tk_self(), *list};
    *list = &l;
    pthread_mutex_unlock(mu);
    tk_park();
    pthread_mutex_lock(mu);
    unlink_from(list, &l);
}

static uint8_t *slot(tk_chan *c, int64_t i) {
    return c->buf + (size_t)(i % room(c)) * c->size;
}

// tk_chan_send_locked is a send on a channel the caller holds the
// lock of, which it releases; a select calls it for the case it chose.
static void tk_chan_send_locked(tk_chan *c, const void *v) {
    while (c->len == room(c) && !c->closed) {
        wait_on(&c->mu, &c->waiters);
    }

    if (c->closed) {
        pthread_mutex_unlock(&c->mu);
        tk_panic(TK_STR("send on a closed channel", 24));
    }

    memcpy(slot(c, c->head + c->len), v, c->size);
    c->len++;
    int64_t mine = ++c->deposits;
    wake_all(&c->waiters);
    while (c->cap == 0 && c->takes < mine) { // unbuffered: until a receiver has taken this value
        if (c->closed) {
            pthread_mutex_unlock(&c->mu);
            tk_panic(TK_STR("channel closed while a send waited on it", 40)); // no receiver need ever come: a hang otherwise
        }

        wait_on(&c->mu, &c->waiters);
    }
}

void tk_chan_send(tk_chan *c, const void *v) {
    pthread_mutex_lock(&c->mu);
    tk_chan_send_locked(c, v);
    pthread_mutex_unlock(&c->mu);
}

static bool tk_chan_recv_locked(tk_chan *c, void *out) {
    while (c->len == 0 && !c->closed) {
        c->receiving++;
        wait_on(&c->mu, &c->waiters);
        c->receiving--;
    }

    if (c->len == 0) {
        return false;
    }

    memcpy(out, slot(c, c->head), c->size);
    c->head = (c->head + 1) % room(c);
    c->len--;
    c->takes++;
    wake_all(&c->waiters);
    return true;
}

// tk_chan_offer puts v in a buffered channel that has room and drops it
// when it has none. It never waits, so a thread that is not a task may
// call it: the timer does, for After and Tick, whose channels only it
// sends on. An unbuffered channel has no room to offer into, and a
// value offered to one would be lost without a word: that is a fault
// of the runtime's own, not a full channel, and it stops the program
// as the scheduler's fatal errors do, since the thread offering may
// be no task. It reports whether anyone but the caller holds a handle:
// when none does, no one can ever receive, the value is not put, and
// the timer stops.
bool tk_chan_offer(tk_chan *c, const void *v) {
    if (c->cap == 0) {
        fflush(stdout);
        fputs("fatal error: offer on an unbuffered channel\n", stderr);
        exit(2);
    }

    if (tk_chan_alone(c)) {
        return false; // only the caller's: no handle can be cloned from it either
    }

    pthread_mutex_lock(&c->mu);
    if (c->len < room(c) && !c->closed) {
        memcpy(slot(c, c->head + c->len), v, c->size);
        c->len++;
        c->deposits++;
        wake_all(&c->waiters);
    }

    pthread_mutex_unlock(&c->mu);
    return true;
}

bool tk_chan_recv(tk_chan *c, void *out) {
    pthread_mutex_lock(&c->mu);
    bool got = tk_chan_recv_locked(c, out);
    pthread_mutex_unlock(&c->mu);
    return got;
}

void tk_chan_close(tk_chan *c) {
    pthread_mutex_lock(&c->mu);
    if (c->closed) {
        pthread_mutex_unlock(&c->mu);
        tk_panic(TK_STR("close of a closed channel", 25));
    }

    c->closed = true;
    wake_all(&c->waiters);
    pthread_mutex_unlock(&c->mu);
}

int64_t tk_chan_len(tk_chan *c) {
    pthread_mutex_lock(&c->mu);
    int64_t n = c->len;
    pthread_mutex_unlock(&c->mu);
    return n;
}

int64_t tk_chan_cap(tk_chan *c) {
    return c->cap;
}

// A select's random choice among the cases that can proceed, so that
// a busy channel does not starve the others; a small generator per
// worker thread, seeded from its address and the clock, read through
// a call of its own so that a select resumed on another thread reads
// that thread's (sched.c says why).
static _Thread_local uint64_t rng;

static __attribute__((noinline)) uint64_t next_random(void) { // never inlined into tk_select, whose task may move threads at its park
    if (rng == 0) {
        rng = ((uint64_t)(uintptr_t)&rng ^ (uint64_t)tk_time_now()) | 1;
    }

    rng ^= rng << 13;
    rng ^= rng >> 7;
    rng ^= rng << 17;
    return rng;
}

// ready reports whether a case can proceed now: a receive when a value
// waits or the channel is closed; a send when there is room, which on
// an unbuffered channel means a receiver is waiting, or the channel is
// closed, which the send then reports as a panic.
static bool ready(const tk_case *k) {
    tk_chan *c = k->chan;
    if (!k->send) {
        return c->len > 0 || c->closed;
    }

    return c->closed || (c->len < room(c) && (c->cap > 0 || c->receiving > 0));
}

// channels_of lists the distinct channels of the cases in increasing
// address order, the order they are locked in, so that two selects
// over the same channels cannot deadlock. Returns the count.
static int channels_of(const tk_case *cases, int n, tk_chan **chans) {
    int m = 0;
    for (int i = 0; i < n; i++) {
        tk_chan *c = cases[i].chan;
        int j = 0;
        while (j < m && chans[j] < c) {
            j++;
        }

        if (j < m && chans[j] == c) {
            continue;
        }

        memmove(&chans[j + 1], &chans[j], (size_t)(m - j) * sizeof *chans);
        chans[j] = c;
        m++;
    }

    return m;
}

static void lock_all(tk_chan **chans, int m) {
    for (int i = 0; i < m; i++) {
        pthread_mutex_lock(&chans[i]->mu);
    }
}

static void unlock_all(tk_chan **chans, int m, tk_chan *except) {
    for (int i = 0; i < m; i++) {
        if (chans[i] != except) {
            pthread_mutex_unlock(&chans[i]->mu);
        }
    }
}

// receivers counts the receive cases on c, each of which stands for a
// waiting receiver while the select sleeps.
static int64_t receivers(const tk_case *cases, int n, tk_chan *c) {
    int64_t k = 0;
    for (int i = 0; i < n; i++) {
        if (cases[i].chan == c && !cases[i].send) {
            k++;
        }
    }

    return k;
}

int tk_select(tk_case *cases, int n, bool has_default) {
    tk_chan *chans[n > 0 ? n : 1];
    waitlink links[n > 0 ? n : 1];
    int m = channels_of(cases, n, chans);
    tk_task *self = tk_self();
    lock_all(chans, m);
    for (;;) {
        int pick = -1, seen = 0;
        for (int i = 0; i < n; i++) {
            if (ready(&cases[i]) && next_random() % (uint64_t)++seen == 0) {
                pick = i; // each ready case equally likely, whatever their number
            }
        }

        if (pick >= 0) {
            tk_case *k = &cases[pick];
            tk_chan *c = k->chan;
            unlock_all(chans, m, c);
            if (k->send) {
                tk_chan_send_locked(c, k->value);
            } else {
                k->ok = tk_chan_recv_locked(c, k->value);
            }

            pthread_mutex_unlock(&c->mu);
            return pick;
        }

        if (has_default) {
            unlock_all(chans, m, NULL);
            return -1;
        }

        for (int i = 0; i < m; i++) {
            links[i] = (waitlink){self, chans[i]->waiters};
            chans[i]->waiters = &links[i];
            chans[i]->receiving += receivers(cases, n, chans[i]);
        }

        unlock_all(chans, m, NULL);
        tk_park();
        lock_all(chans, m);
        for (int i = 0; i < m; i++) {
            unlink_from(&chans[i]->waiters, &links[i]);
            chans[i]->receiving -= receivers(cases, n, chans[i]);
        }
    }
}

// Locks, as _docs/design/06-tasks-and-channels.md lays them out: one
// object serves the sync package's Mutex, RwLock and WaitGroup, a lock
// held by one writer or by any number of readers, with the wait
// group's count and the list of waiting tasks a channel has. The
// library holds a lock as an int, as it holds a file descriptor: the
// handle is the lock's address, so a task reaches its lock with no
// table between and no mutex but the lock's own, and 0, which no lock
// has, is the zero handle of a WaitGroup, the one handle the library
// gives out unmade. A task that must wait links itself on the lock's
// list and parks, exactly as on a channel. A writer waiting for the
// lock holds back the readers that come after it, as Go's RWMutex
// does, so that a stream of readers cannot keep a writer out for ever;
// a reader therefore must not take the lock again while it holds it.
typedef struct {
    pthread_mutex_t mu;
    int64_t readers; // tasks holding the lock shared
    bool writer;     // a task holds the lock alone
    int64_t writers; // tasks waiting to hold it alone
    int64_t count;   // the wait group's
    waitlink *waiters;
} tk_lock;

// The lock's memory is tk_alloc's, so a debug build reports a lock
// that nothing freed.
int64_t tk_sync_newLock(void) {
    tk_lock *l = tk_alloc(sizeof *l);
    memset(l, 0, sizeof *l);
    pthread_mutex_init(&l->mu, NULL);
    return (int64_t)(intptr_t)l;
}

// tk_sync_freeLock frees the lock. The Mutex, RwLock or WaitGroup
// counter that frees its lock owns it, the last handle in a WaitGroup's
// case, so no task holds or waits on it by then.
void tk_sync_freeLock(int64_t h) {
    tk_lock *l = (tk_lock *)(intptr_t)h;
    pthread_mutex_destroy(&l->mu);
    tk_free(l);
}

// lock_of returns the lock h names with its mutex taken. A Mutex and an
// RwLock catch their own empty handles, so an empty one here is a
// WaitGroup's.
static tk_lock *lock_of(int64_t h) {
    tk_lock *l = (tk_lock *)(intptr_t)h;
    if (l == NULL) {
        tk_panic(TK_STR("sync: use of an empty WaitGroup", 31));
    }

    pthread_mutex_lock(&l->mu);
    return l;
}

// A writer waits for every holder, counted among the waiting writers
// while it does; a reader waits while a writer holds the lock or waits
// for it.
void tk_sync_lock(int64_t h) {
    tk_lock *l = lock_of(h);
    while (l->writer || l->readers > 0) {
        l->writers++;
        wait_on(&l->mu, &l->waiters);
        l->writers--;
    }

    l->writer = true;
    pthread_mutex_unlock(&l->mu);
}

void tk_sync_unlock(int64_t h) {
    tk_lock *l = lock_of(h);
    l->writer = false;
    wake_all(&l->waiters);
    pthread_mutex_unlock(&l->mu);
}

void tk_sync_lockShared(int64_t h) {
    tk_lock *l = lock_of(h);
    while (l->writer || l->writers > 0) {
        wait_on(&l->mu, &l->waiters);
    }

    l->readers++;
    pthread_mutex_unlock(&l->mu);
}

void tk_sync_unlockShared(int64_t h) {
    tk_lock *l = lock_of(h);
    l->readers--;
    if (l->readers == 0) {
        wake_all(&l->waiters);
    }

    pthread_mutex_unlock(&l->mu);
}

// tk_sync_count adds n to the count and wakes the waiters when it
// reaches zero; a count below zero is a Done without its Add.
void tk_sync_count(int64_t h, int64_t n) {
    tk_lock *l = lock_of(h);
    l->count += n;
    if (l->count < 0) {
        pthread_mutex_unlock(&l->mu);
        tk_panic(TK_STR("sync: negative WaitGroup count", 30));
    }

    if (l->count == 0) {
        wake_all(&l->waiters);
    }

    pthread_mutex_unlock(&l->mu);
}

void tk_sync_wait(int64_t h) {
    tk_lock *l = lock_of(h);
    while (l->count > 0) {
        wait_on(&l->mu, &l->waiters);
    }

    pthread_mutex_unlock(&l->mu);
}

// tk_sync_atomicAdd adds d to *n as one step no other task's add can
// interleave with, and returns the new value; the Arc's count is kept
// with it, and the handle that brings it to zero drops the value.
int64_t tk_sync_atomicAdd(int64_t *n, int64_t d) {
    return __atomic_add_fetch(n, d, __ATOMIC_SEQ_CST);
}
