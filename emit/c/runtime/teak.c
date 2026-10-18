// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "teak.h"

#include <ctype.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

void *tk_alloc(size_t n) {
    void *p = malloc(n == 0 ? 1 : n);
    if (p == NULL) {
        tk_panic(TK_STR("out of memory", 13));
    }

    return p;
}

_Noreturn void tk_exit(int code) {
    fflush(stdout);
    exit(code);
}

_Noreturn void tk_panic(tk_string msg) {
    fflush(stdout);
    fputs("panic: ", stderr);
    fwrite(msg.ptr, 1, (size_t)msg.len, stderr);
    fputc('\n', stderr);
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

    s->ptr = p;
    s->cap = cap;
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
    if (s->len == 0) {
        return false;
    }

    s->len--;
    memcpy(out, (char *)s->ptr + (size_t)s->len * size, size);
    return true;
}

void tk_slice_insert(tk_slice *s, int64_t i, const void *v, size_t size) {
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
    out->ptr = tk_alloc((size_t)s->len * size);
    memcpy(out->ptr, s->ptr, (size_t)s->len * size);
    out->len = s->len;
    out->cap = s->len;
}

void tk_slice_sub(const tk_slice *s, int64_t lo, int64_t hi, tk_slice *out, size_t size) {
    if (lo < 0 || hi < lo || hi > s->len) {
        tk_bounds(hi, s->len);
    }

    out->ptr = (char *)s->ptr + (size_t)lo * size;
    out->len = hi - lo;
    out->cap = s->cap - lo;
}

tk_string tk_concat(tk_string a, tk_string b) {
    uint8_t *p = tk_alloc((size_t)(a.len + b.len));
    memcpy(p, a.ptr, (size_t)a.len);
    memcpy(p + a.len, b.ptr, (size_t)b.len);
    return TK_STR(p, a.len + b.len);
}

bool tk_string_eq(tk_string a, tk_string b) {
    return a.len == b.len && memcmp(a.ptr, b.ptr, (size_t)a.len) == 0;
}

int tk_string_cmp(tk_string a, tk_string b) {
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
    return TK_STR(p, (int64_t)n);
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

tk_string tk_quote(tk_string s) {
    uint8_t *p = tk_alloc((size_t)s.len * 2 + 2);
    int64_t n = 0;
    p[n++] = '"';
    for (int64_t i = 0; i < s.len; i++) {
        uint8_t c = s.ptr[i];
        if (c == '"' || c == '\\') {
            p[n++] = '\\';
        }

        p[n++] = c;
    }

    p[n++] = '"';
    return TK_STR(p, n);
}

tk_string tk_string_clone(tk_string s) {
    uint8_t *p = tk_alloc((size_t)s.len);
    memcpy(p, s.ptr, (size_t)s.len);
    return TK_STR(p, s.len);
}

tk_string tk_substr(tk_string s, int64_t lo, int64_t hi) {
    if (lo < 0 || hi < lo || hi > s.len) {
        tk_bounds(hi, s.len);
    }

    return TK_STR(s.ptr + lo, hi - lo);
}

// mapped copies s through f, one byte at a time: ASCII case only,
// until the library is written in Teak.
static tk_string mapped(tk_string s, int (*f)(int)) {
    uint8_t *p = tk_alloc((size_t)s.len);
    for (int64_t i = 0; i < s.len; i++) {
        p[i] = (uint8_t)f(s.ptr[i]);
    }

    return TK_STR(p, s.len);
}

tk_string tk_to_upper(tk_string s) { return mapped(s, toupper); }
tk_string tk_to_lower(tk_string s) { return mapped(s, tolower); }

static bool space(uint8_t c) {
    return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v';
}

tk_string tk_trim(tk_string s) {
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
    return s.len >= t.len && memcmp(s.ptr, t.ptr, (size_t)t.len) == 0;
}

bool tk_has_suffix(tk_string s, tk_string t) {
    return s.len >= t.len && memcmp(s.ptr + s.len - t.len, t.ptr, (size_t)t.len) == 0;
}

// find returns the offset of t in s, or -1.
static int64_t find(tk_string s, tk_string t) {
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

// tk_split splits s at every sep into views of s; an empty separator
// splits between every byte.
void tk_split(tk_string s, tk_string sep, tk_slice *out) {
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

void tk_print_string(tk_string s) { fwrite(s.ptr, 1, (size_t)s.len, stdout); }
void tk_print_int(int64_t v) { printf("%lld", (long long)v); }
void tk_print_uint(uint64_t v) { printf("%llu", (unsigned long long)v); }
void tk_print_f64(double v) { tk_print_string(tk_f64_to_string(v)); }
void tk_print_bool(bool v) { fputs(v ? "true" : "false", stdout); }
void tk_print_sep(void) { fputc(' ', stdout); }
void tk_print_nl(void) { fputc('\n', stdout); }
void tk_eprint_string(tk_string s) { fwrite(s.ptr, 1, (size_t)s.len, stderr); }
