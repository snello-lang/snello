// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Strings, and the runtime's half of the strings package: building,
// comparing and cutting, case mapping, trimming, searching, splitting
// and repeating.

#include "internal.h"

#include <string.h>

// sn_string_append is s += x: in place when s owns room past its end,
// which only a string built by the program can, and else into a block
// twice as large, so that a string built by += in a loop costs
// amortized time a byte, as Rust's String does, and not a copy of the
// whole on every pass. The borrow rules keep anything from viewing s
// while it changes; x may be a part of s, which is read before the old
// block is freed.
void sn_string_append(sn_string *s, const sn_string *x) {
    sn_check_string(*s);
    sn_check_string(*x);
    if (x->len == 0) {
        return;
    }

    int64_t n = s->len + x->len;
    if (s->cap >= n) {
        memcpy((uint8_t *)s->ptr + s->len, x->ptr, (size_t)x->len);
        s->len = n;
        return;
    }

    int64_t cap = s->cap * 2;
    if (cap < n) {
        cap = n;
    }

    if (cap < 16) {
        cap = 16;
    }

    uint8_t *p = sn_alloc((size_t)cap);
    memcpy(p, s->ptr, (size_t)s->len);
    memcpy(p + s->len, x->ptr, (size_t)x->len);
    if (s->cap > 0) {
        sn_free((void *)s->ptr);
    }

    *s = (sn_string){p, n, cap};
}

sn_string sn_concat(sn_string a, sn_string b) {
    sn_check_string(a);
    sn_check_string(b);
    uint8_t *p = sn_alloc((size_t)(a.len + b.len));
    memcpy(p, a.ptr, (size_t)a.len);
    memcpy(p + a.len, b.ptr, (size_t)b.len);
    return SN_OWNED(p, a.len + b.len);
}

sn_string sn_join(sn_string a, sn_string b, int owned) {
    sn_string s = sn_concat(a, b);
    if (owned & 1) {
        sn_string_free(a);
    }

    if (owned & 2) {
        sn_string_free(b);
    }

    return s;
}

bool sn_string_eq(sn_string a, sn_string b) {
    sn_check_string(a);
    sn_check_string(b);
    return a.len == b.len && memcmp(a.ptr, b.ptr, (size_t)a.len) == 0;
}

int sn_string_cmp(sn_string a, sn_string b) {
    sn_check_string(a);
    sn_check_string(b);
    int64_t n = a.len < b.len ? a.len : b.len;
    int c = memcmp(a.ptr, b.ptr, (size_t)n);
    if (c != 0) {
        return c;
    }

    return a.len < b.len ? -1 : a.len > b.len;
}

sn_string sn_string_clone(sn_string s) {
    sn_check_string(s);
    if (s.len == 0) {
        return SN_STR("", 0); // an owned string of no bytes would be a block with cap 0, which nothing frees
    }

    uint8_t *p = sn_alloc((size_t)s.len);
    memcpy(p, s.ptr, (size_t)s.len);
    return SN_OWNED(p, s.len);
}

sn_string sn_substr(sn_string s, int64_t lo, int64_t hi) {
    sn_check_string(s);
    if (lo < 0 || hi < lo || hi > s.len) {
        sn_slice_bounds(lo, hi, s.len);
    }

    return SN_STR(s.ptr + lo, hi - lo);
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

    int w = sn_utf8_decode(p + start, n - start, r);
    if (start + w != n) {
        *r = 0xFFFD;
        return 1;
    }

    return w;
}

// mapped copies s with every rune through f, as Go's strings.Map does
// for ToUpper and ToLower: a byte that is not valid UTF-8 becomes the
// replacement rune, and a rune may change its length, so the length is
// counted before the copy. Text of ASCII alone is copied byte by byte.
static sn_string mapped(sn_string s, int32_t (*f)(int32_t)) {
    sn_check_string(s);
    bool ascii = true;
    for (int64_t i = 0; i < s.len && ascii; i++) {
        ascii = s.ptr[i] < 0x80;
    }

    if (ascii) { // a byte's case by arithmetic, which the compiler writes with vector instructions
        uint8_t *p = sn_alloc((size_t)s.len);
        if (f == sn_rune_upper) {
            for (int64_t i = 0; i < s.len; i++) {
                uint8_t c = s.ptr[i];
                p[i] = (uint8_t)(c - ((c >= 'a' && c <= 'z') ? 32 : 0));
            }
        } else if (f == sn_rune_lower) {
            for (int64_t i = 0; i < s.len; i++) {
                uint8_t c = s.ptr[i];
                p[i] = (uint8_t)(c + ((c >= 'A' && c <= 'Z') ? 32 : 0));
            }
        } else {
            for (int64_t i = 0; i < s.len; i++) {
                p[i] = (uint8_t)f(s.ptr[i]);
            }
        }

        return SN_OWNED(p, s.len);
    }

    uint8_t tmp[4];
    int64_t n = 0;
    for (int64_t i = 0; i < s.len;) {
        int32_t r;
        i += sn_utf8_decode(s.ptr + i, s.len - i, &r);
        n += sn_utf8_encode(f(r), tmp);
    }

    uint8_t *p = sn_alloc((size_t)n);
    for (int64_t i = 0, at = 0; i < s.len;) {
        int32_t r;
        i += sn_utf8_decode(s.ptr + i, s.len - i, &r);
        at += sn_utf8_encode(f(r), p + at);
    }

    return SN_OWNED(p, n);
}

sn_string sn_to_upper(sn_string s) { return mapped(s, sn_rune_upper); }
sn_string sn_to_lower(sn_string s) { return mapped(s, sn_rune_lower); }

// ascii_space is Go's asciiSpace: the white space below 0x80, by byte.
static const uint8_t ascii_space[256] = {['\t'] = 1, ['\n'] = 1, ['\v'] = 1, ['\f'] = 1, ['\r'] = 1, [' '] = 1};

// space_at reports whether the rune at p, at most n bytes long, is
// white space, and its width in w: an ASCII byte by the table, any other
// rune by Unicode's White_Space.
static inline bool space_at(const uint8_t *p, int64_t n, int *w) {
    if (p[0] < 0x80) {
        *w = 1;
        return ascii_space[p[0]];
    }

    int32_t r;
    *w = sn_utf8_decode(p, n, &r);
    return sn_rune_space(r);
}

// sn_trim lends s without the white space at either end, the runes
// Go's unicode.IsSpace reports, as strings.TrimSpace does.
sn_string sn_trim(sn_string s) {
    sn_check_string(s);
    int64_t lo = 0, hi = s.len;
    int w;
    while (lo < hi && space_at(s.ptr + lo, hi - lo, &w)) {
        lo += w;
    }

    while (hi > lo) {
        if (s.ptr[hi - 1] < 0x80) {
            if (!ascii_space[s.ptr[hi - 1]]) {
                break;
            }

            hi--;
            continue;
        }

        int32_t r;
        int back = decode_last(s.ptr + lo, hi - lo, &r);
        if (!sn_rune_space(r)) {
            break;
        }

        hi -= back;
    }

    return SN_STR(s.ptr + lo, hi - lo);
}

bool sn_has_prefix(sn_string s, sn_string t) {
    sn_check_string(s);
    sn_check_string(t);
    return s.len >= t.len && memcmp(s.ptr, t.ptr, (size_t)t.len) == 0;
}

bool sn_has_suffix(sn_string s, sn_string t) {
    sn_check_string(s);
    sn_check_string(t);
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
// finds it. A one-byte t is memchr. A longer one is found by its first
// two bytes, eight places of s at a time, as glibc's memmem filters a
// short needle: the two bytes compared against a word of s and the
// word one byte on, the places where both match picked out of the
// difference at once, and the whole of t compared there. Once the
// misses outrun the progress, the rest of s is searched by rabin_karp,
// so that no t, however close to matching everywhere, makes a search
// cost the product of the lengths.
static int64_t find(sn_string s, sn_string t) {
    sn_check_string(s);
    sn_check_string(t);
    int64_t n = s.len, m = t.len;
    if (m == 0) {
        return 0;
    }

    if (m > n) {
        return -1;
    }

    const uint8_t *p = s.ptr, *q = t.ptr;
    if (m == 1) {
        const uint8_t *at = memchr(p, q[0], (size_t)n);
        return at == NULL ? -1 : at - p;
    }

    const uint64_t lo = 0x0101010101010101u, hi = 0x8080808080808080u;
    uint64_t first = lo * q[0], second = lo * q[1];
    int64_t last = n - m; // the last offset t fits at
    int64_t i = 0, fails = 0;
    for (; i + 7 <= last; i += 8) { // the word one byte on ends at p[i+8], within s as m is two or more
        uint64_t w0, w1;
        memcpy(&w0, p + i, 8);
        memcpy(&w1, p + i + 1, 8);
        uint64_t z = (w0 ^ first) | (w1 ^ second);
        for (uint64_t hits = (z - lo) & ~z & hi; hits != 0; hits &= hits - 1) {
            int64_t at = i + __builtin_ctzll(hits) / 8;
            if (memcmp(p + at, q, (size_t)m) == 0) {
                return at;
            }

            if (++fails >= 4 + (at >> 4)) {
                int64_t found = rabin_karp(p + at + 1, n - at - 1, q, m);
                return found < 0 ? -1 : at + 1 + found;
            }
        }
    }

    for (; i <= last; i++) {
        if (p[i] == q[0] && p[i + 1] == q[1] && memcmp(p + i, q, (size_t)m) == 0) {
            return i;
        }
    }

    return -1;
}

bool sn_string_contains(sn_string s, sn_string t) { return find(s, t) >= 0; }

// sn_fields splits s around runs of white space, the runes Go's
// unicode.IsSpace reports, into views of s, as strings.Fields does.
void sn_fields(sn_string s, sn_slice *out) {
    sn_check_string(s);
    out->ptr = NULL;
    out->len = 0;
    out->cap = 0;

    // ASCII text, as Go's Fields does it: the fields counted first, so
    // that the result is made once at its length.
    int64_t n = 0;
    bool was_space = true, ascii = true;
    for (int64_t j = 0; j < s.len; j++) {
        uint8_t c = s.ptr[j];
        if (c >= 0x80) {
            ascii = false;
            break;
        }

        bool is = ascii_space[c];
        n += was_space && !is;
        was_space = is;
    }

    if (ascii) {
        if (n == 0) {
            return;
        }

        sn_slice_make(out, 0, n, sizeof(sn_string));
        sn_string *f = out->ptr;
        int64_t j = 0;
        while (j < s.len) {
            while (j < s.len && ascii_space[s.ptr[j]]) {
                j++;
            }

            int64_t start = j;
            while (j < s.len && !ascii_space[s.ptr[j]]) {
                j++;
            }

            if (j > start) {
                f[out->len++] = SN_STR(s.ptr + start, j - start);
            }
        }

        return;
    }

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
            sn_string field = SN_STR(s.ptr + start, i - start);
            sn_slice_push(out, &field, sizeof field);
        }
    }
}

// sn_lines splits s at every newline into views of s, each without
// its newline and a carriage return before it; a last line without a
// newline is a line, a trailing newline does not make an empty one.
void sn_lines(sn_string s, sn_slice *out) {
    sn_check_string(s);
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

        sn_string line = SN_STR(s.ptr + start, end - start);
        sn_slice_push(out, &line, sizeof line);
        start = i + 1;
    }
}

// sn_split splits s at every sep into views of s. An empty separator
// splits after every UTF-8 sequence, a byte that is not one counting
// as one, and gives no pieces of an empty string, as Go's strings.Split
// does.
void sn_split(sn_string s, sn_string sep, sn_slice *out) {
    sn_check_string(s);
    sn_check_string(sep);
    out->ptr = NULL;
    out->len = 0;
    out->cap = 0;
    if (sep.len == 0) {
        for (int64_t i = 0; i < s.len;) {
            int32_t r;
            int w = sn_utf8_decode(s.ptr + i, s.len - i, &r);
            sn_string piece = SN_STR(s.ptr + i, w);
            sn_slice_push(out, &piece, sizeof piece);
            i += w;
        }

        return;
    }

    int64_t start = 0;
    for (;;) {
        sn_string rest = SN_STR(s.ptr + start, s.len - start);
        int64_t at = find(rest, sep);
        if (at < 0) {
            sn_slice_push(out, &rest, sizeof rest);
            return;
        }

        sn_string piece = SN_STR(rest.ptr, at);
        sn_slice_push(out, &piece, sizeof piece);
        start += at + sep.len;
    }
}

// sn_strings_repeat writes s n times into one allocation: s once, then
// what is written so far again, doubling, as Go's strings.Repeat does,
// in about log2(n) copies. The library checked n and the length.
sn_string sn_strings_repeat(sn_string s, int64_t n) {
    sn_check_string(s);
    int64_t total = s.len * n;
    if (total == 0) {
        return SN_STR("", 0);
    }

    uint8_t *p = sn_alloc((size_t)total);
    memcpy(p, s.ptr, (size_t)s.len);
    for (int64_t filled = s.len; filled < total;) {
        int64_t chunk = filled < total - filled ? filled : total - filled;
        memcpy(p + filled, p, (size_t)chunk);
        filled += chunk;
    }

    return SN_OWNED(p, total);
}

// sn_strings_ownString makes the bytes of b, which it takes, a string's:
// a string's cap is its allocation's size, as a slice's is, so the
// buffer passes over whole, with no copy.
sn_string sn_strings_ownString(sn_slice_u8 b) {
    sn_check_slice((sn_slice *)&b);
    if (b.len == 0) {
        if (b.ptr != NULL) { // a block of no bytes is a block too
            sn_free(b.ptr);
        }

        return SN_STR("", 0);
    }

    return (sn_string){b.ptr, b.len, b.cap};
}

// sn_strings_appendString appends the bytes of s to b in one copy,
// growing it once: the strings package's Builder and Repeat write
// through it.
void sn_strings_appendString(sn_slice_u8 *buf, sn_string s) {
    sn_slice *b = (sn_slice *)buf; // the same three words
    sn_check_string(s);
    if (s.len == 0) {
        return;
    }

    sn_slice_own(b, "WriteString");
    sn_slice_grow(b, b->len + s.len, 1);
    memcpy((uint8_t *)b->ptr + b->len, s.ptr, (size_t)s.len);
    b->len += s.len;
}

bool sn_split_once(sn_string s, sn_string sep, sn_string *before, sn_string *after) {
    int64_t at = find(s, sep);
    if (at < 0) {
        return false;
    }

    *before = SN_STR(s.ptr, at);
    *after = SN_STR(s.ptr + at + sep.len, s.len - at - sep.len);
    return true;
}
