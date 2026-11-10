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
    // An owned string of no bytes would be a block with cap 0, which
    // nothing frees. No length is below zero, and the test says so to a
    // compiler that joins this to its caller and, not knowing it, takes
    // the copy below for one of a size no block has.
    if (s.len <= 0) {
        return SN_STR("", 0);
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

// asciiCase copies s, which holds ASCII alone, with each letter in
// upper or lower case: a byte's case by arithmetic, which the compiler
// writes with vector instructions. No mapping of Unicode's but the
// simple one touches ASCII.
static sn_string asciiCase(sn_string s, bool upper) {
    uint8_t *p = sn_alloc((size_t)(s.len > 0 ? s.len : 1));
    for (int64_t i = 0; i < s.len; i++) {
        uint8_t c = s.ptr[i];
        if (upper) {
            p[i] = (uint8_t)(c - ((c >= 'a' && c <= 'z') ? 32 : 0));
        } else {
            p[i] = (uint8_t)(c + ((c >= 'A' && c <= 'Z') ? 32 : 0));
        }
    }

    return SN_OWNED(p, s.len);
}

// caseOf writes into to what the rune rs[k] of n maps to by Unicode's
// full case mapping of kind, SN_UC_UPPER, SN_UC_LOWER or SN_UC_TITLE,
// and returns how many runes that is: the mapping of SpecialCasing.txt
// that holds in every language, else the simple one; lowered, a capital
// sigma at the end of a word is the final form, by the rule Final_Sigma,
// a cased letter before it, through case-ignorable runes, and none
// after.
static int caseOf(int kind, const int32_t *rs, int64_t n, int64_t k, int32_t to[3]) {
    int m = sn_uc_special(kind, rs[k], to);
    if (m == 0) {
        to[0] = kind == SN_UC_UPPER ? sn_rune_upper(rs[k]) : kind == SN_UC_LOWER ? sn_rune_lower(rs[k]) : sn_rune_title(rs[k]);
        m = 1;
    }

    if (kind == SN_UC_LOWER && rs[k] == 0x03A3) {
        int64_t j = k - 1;
        while (j >= 0 && sn_uc_case_ignorable(rs[j])) {
            j--;
        }

        int64_t l = k + 1;
        while (l < n && sn_uc_case_ignorable(rs[l])) {
            l++;
        }

        if (j >= 0 && sn_uc_cased(rs[j]) && !(l < n && sn_uc_cased(rs[l]))) {
            to[0] = 0x03C2;
        }
    }

    return m;
}

// cased maps the runes of s, each by the kind kinds gives it, or kind
// for all when kinds is NULL, into a new string, which may be longer
// than s: the length is counted on a first pass and written on a
// second. The runes are decoded first, since the final sigma looks on
// both sides.
static sn_string cased(sn_string s, int kind, int32_t *rs, int64_t n, const uint8_t *kinds) {
    uint8_t tmp[4];
    uint8_t *p = NULL;
    int64_t size = 0;
    for (int pass = 0; pass < 2; pass++) {
        int64_t at = 0;
        for (int64_t k = 0; k < n; k++) {
            int32_t to[3];
            int m = caseOf(kinds != NULL ? kinds[k] : kind, rs, n, k, to);
            for (int i = 0; i < m; i++) {
                at += sn_utf8_encode(to[i], pass == 0 ? tmp : p + at);
            }
        }

        if (pass == 0) {
            size = at;
            p = sn_alloc((size_t)(size > 0 ? size : 1));
        }
    }

    (void)s;
    return SN_OWNED(p, size);
}

// decoded returns the runes of s in a new array, and their number in n.
static int32_t *decoded(sn_string s, int64_t *n) {
    int32_t *rs = sn_alloc((size_t)(s.len > 0 ? s.len : 1) * sizeof(int32_t)); // no more runes than bytes
    *n = 0;
    for (int64_t i = 0; i < s.len; (*n)++) {
        i += sn_utf8_decode(s.ptr + i, s.len - i, &rs[*n]);
    }

    return rs;
}

static bool ascii(sn_string s) {
    for (int64_t i = 0; i < s.len; i++) {
        if (s.ptr[i] >= 0x80) {
            return false;
        }
    }

    return true;
}

// whole maps every rune of s by the full case mapping of kind.
static sn_string whole(sn_string s, int kind) {
    sn_check_string(s);
    if (ascii(s)) {
        return asciiCase(s, kind == SN_UC_UPPER);
    }

    int64_t n;
    int32_t *rs = decoded(s, &n);
    sn_string out = cased(s, kind, rs, n, NULL);
    sn_free(rs);
    return out;
}

sn_string sn_to_upper(sn_string s) { return whole(s, SN_UC_UPPER); }
sn_string sn_to_lower(sn_string s) { return whole(s, SN_UC_LOWER); }

// starts reports whether r is where a word's title case begins: a
// cased rune, or a letter, number, symbol or private-use rune, so that
// a quote before a word is passed over and a digit that starts one,
// as in "3rd", is its start.
static bool starts(int32_t r) {
    if (sn_uc_cased(r)) {
        return true;
    }

    int c = sn_rune_category(r);
    char m = sn_category_name(c)[0];
    return m == 'L' || m == 'N' || m == 'S' || c == SN_GC_Co;
}

// sn_strings_toTitle title-cases s as Unicode's toTitlecase does, its
// start adjusted as ICU's is by default: in each word, by the word
// boundaries of annex 29, the first rune that starts it to its full
// title case mapping and every other rune to lower case, so "straße
// ﬁx" is "Straße Fix", "'tis" is "'Tis" and "3RD" is "3rd".
sn_string sn_strings_toTitle(sn_string s) {
    sn_check_string(s);
    int64_t n;
    int32_t *rs = decoded(s, &n);
    uint8_t *kinds = sn_alloc((size_t)(n > 0 ? n : 1));
    int64_t k = 0;
    for (int64_t at = 0; at < s.len;) {
        int64_t end = at + sn_unicode__segment_next(s, at, 1);
        bool first = true;
        for (int64_t i = at; i < end; k++) {
            int32_t r;
            i += sn_utf8_decode(s.ptr + i, end - i, &r);
            bool title = first && starts(r);
            first = first && !title;
            kinds[k] = title ? SN_UC_TITLE : SN_UC_LOWER;
        }

        at = end;
    }

    sn_string out = cased(s, SN_UC_LOWER, rs, n, kinds);
    sn_free(kinds);
    sn_free(rs);
    return out;
}

// sn_strings_toValidUTF8 copies s with each run of bytes that are not
// UTF-8 replaced by rep, once a run.
sn_string sn_strings_toValidUTF8(sn_string s, sn_string rep) {
    sn_check_string(s);
    sn_check_string(rep);
    int64_t size = 0;
    uint8_t *p = NULL;
    for (int pass = 0; pass < 2; pass++) {
        int64_t at = 0;
        bool bad = false;
        for (int64_t i = 0; i < s.len;) {
            int32_t r;
            int w = sn_utf8_decode(s.ptr + i, s.len - i, &r);
            if (r == 0xFFFD && w == 1) {
                if (!bad) {
                    if (pass == 1) {
                        memcpy(p + at, rep.ptr, (size_t)rep.len);
                    }

                    at += rep.len;
                }

                bad = true;
            } else {
                if (pass == 1) {
                    memcpy(p + at, s.ptr + i, (size_t)w);
                }

                at += w;
                bad = false;
            }

            i += w;
        }

        if (pass == 0) {
            size = at;
            p = sn_alloc((size_t)(size > 0 ? size : 1));
        }
    }

    return SN_OWNED(p, size);
}

// sn_string_from_rune and sn_string_from_runes build the string of one
// rune or of a []rune, U+FFFD for a value that is not a code point: the
// conversions string(r) and string(rs).
sn_string sn_string_from_rune(int32_t r) {
    uint8_t *p = sn_alloc(4);
    return SN_OWNED(p, sn_utf8_encode(r, p));
}

sn_string sn_string_from_runes(sn_slice rs) {
    const int32_t *r = rs.ptr;
    uint8_t tmp[4];
    int64_t size = 0;
    for (int64_t i = 0; i < rs.len; i++) {
        size += sn_utf8_encode(r[i], tmp);
    }

    uint8_t *p = sn_alloc((size_t)(size > 0 ? size : 1));
    for (int64_t i = 0, at = 0; i < rs.len; i++) {
        at += sn_utf8_encode(r[i], p + at);
    }

    return SN_OWNED(p, size);
}

// A folder reads a string's runes through full case folding, the runes
// of one rune's folding queued until taken.
typedef struct {
    sn_string s;
    int64_t i;
    int32_t queue[3];
    int head, n;
} folder;

// folded takes the next rune of the folding, returning false at the
// end of the string.
static bool folded(folder *f, int32_t *r) {
    if (f->head == f->n) {
        if (f->i >= f->s.len) {
            return false;
        }

        int32_t c;
        f->i += sn_utf8_decode(f->s.ptr + f->i, f->s.len - f->i, &c);
        f->n = sn_uc_special(SN_UC_FOLD, c, f->queue);
        if (f->n == 0) {
            f->queue[0] = sn_rune_fold(c);
            f->n = 1;
        }

        f->head = 0;
    }

    *r = f->queue[f->head++];
    return true;
}

// sn_strings_equalIgnoreCase reports whether s and t are equal under full
// case folding, statuses C and F of CaseFolding.txt: Unicode's default
// caseless match.
bool sn_strings_equalIgnoreCase(sn_string s, sn_string t) {
    folder a = {s, 0, {0}, 0, 0}, b = {t, 0, {0}, 0, 0};
    for (;;) {
        int32_t x, y;
        bool more = folded(&a, &x), other = folded(&b, &y);
        if (more != other) {
            return false;
        }

        if (!more) {
            return true;
        }

        if (x != y) {
            return false;
        }
    }
}

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

// mismatch returns the index of the first byte where a and b, m bytes
// each, differ, or m when they are equal: eight bytes at a time, the
// lowest differing byte of a word picked out of their exclusive or. A
// search counts what its failed comparisons cost by it. It stays this
// small so that it is inlined into the filters: a first pass of
// thirty-two bytes at a time measured the common search two and a half
// times slower for a tenth gained on a long needle that matches.
static int64_t mismatch(const uint8_t *a, const uint8_t *b, int64_t m) {
    int64_t i = 0;
    for (; i + 8 <= m; i += 8) {
        uint64_t x, y;
        memcpy(&x, a + i, 8);
        memcpy(&y, b + i, 8);
        if (x != y) {
            return i + __builtin_ctzll(x ^ y) / 8;
        }
    }

    while (i < m && a[i] == b[i]) {
        i++;
    }

    return i;
}

// TWO_WAY writes the Two-Way search of Crochemore and Perrin, the one
// glibc's memmem, musl and Rust's str::find make: linear in the lengths
// whatever the text and the pattern, with no table and no allocation.
// The pattern is cut where its two maximal suffixes, under the order of
// bytes and its reverse, say: the right part is matched first, left to
// right, and the left part after it, right to left; a mismatch moves
// the window by as much as the part matched allows, and a pattern that
// repeats remembers how much of it matched already. X(i) and Y(i) read
// byte i of the pattern x and of the text y, so that one template
// searches forwards, and backwards over the reversed bytes.
#define TWO_WAY(name, X, Y)                                                          \
    static int64_t name(const uint8_t *y, int64_t n, const uint8_t *x, int64_t m) { \
        int64_t ell, per;                                                            \
        {                                                                            \
            int64_t ms[2], ps[2];                                                    \
            for (int order = 0; order < 2; order++) {                                \
                int64_t a = -1, j = 0, k = 1, p = 1;                                 \
                while (j + k < m) {                                                  \
                    uint8_t u = X(j + k), v = X(a + k);                              \
                    if (order == 0 ? u < v : u > v) {                                \
                        j += k;                                                      \
                        k = 1;                                                       \
                        p = j - a;                                                   \
                    } else if (u == v) {                                             \
                        if (k != p) {                                                \
                            k++;                                                     \
                        } else {                                                     \
                            j += p;                                                  \
                            k = 1;                                                   \
                        }                                                            \
                    } else {                                                         \
                        a = j;                                                       \
                        j = a + 1;                                                   \
                        k = p = 1;                                                   \
                    }                                                                \
                }                                                                    \
                ms[order] = a;                                                       \
                ps[order] = p;                                                       \
            }                                                                        \
            int which = ms[0] > ms[1] ? 0 : 1;                                       \
            ell = ms[which];                                                         \
            per = ps[which];                                                         \
        }                                                                            \
        bool periodic = per + ell + 1 <= m;                                          \
        for (int64_t i = 0; periodic && i <= ell; i++) {                             \
            periodic = X(i) == X(i + per);                                           \
        }                                                                            \
        if (periodic) {                                                              \
            int64_t memory = -1;                                                     \
            for (int64_t j = 0; j <= n - m;) {                                       \
                int64_t i = (ell > memory ? ell : memory) + 1;                       \
                while (i < m && X(i) == Y(i + j)) {                                  \
                    i++;                                                             \
                }                                                                    \
                if (i >= m) {                                                        \
                    i = ell;                                                         \
                    while (i > memory && X(i) == Y(i + j)) {                         \
                        i--;                                                         \
                    }                                                                \
                    if (i <= memory) {                                               \
                        return j;                                                    \
                    }                                                                \
                    j += per;                                                        \
                    memory = m - per - 1;                                            \
                } else {                                                             \
                    j += i - ell;                                                    \
                    memory = -1;                                                     \
                }                                                                    \
            }                                                                        \
        } else {                                                                     \
            per = (ell + 1 > m - ell - 1 ? ell + 1 : m - ell - 1) + 1;               \
            for (int64_t j = 0; j <= n - m;) {                                       \
                int64_t i = ell + 1;                                                 \
                while (i < m && X(i) == Y(i + j)) {                                  \
                    i++;                                                             \
                }                                                                    \
                if (i >= m) {                                                        \
                    i = ell;                                                         \
                    while (i >= 0 && X(i) == Y(i + j)) {                             \
                        i--;                                                         \
                    }                                                                \
                    if (i < 0) {                                                     \
                        return j;                                                    \
                    }                                                                \
                    j += per;                                                        \
                } else {                                                             \
                    j += i - ell;                                                    \
                }                                                                    \
            }                                                                        \
        }                                                                            \
        return -1;                                                                   \
    }

#define FORWARD_X(i) x[i]
#define FORWARD_Y(i) y[i]
#define BACKWARD_X(i) x[m - 1 - (i)]
#define BACKWARD_Y(i) y[n - 1 - (i)]

// two_way returns the offset of the first x in y, or -1.
TWO_WAY(two_way, FORWARD_X, FORWARD_Y)

// two_way_back returns the offset, from the end, of the last x in y:
// the first of the reversed pattern in the reversed text.
TWO_WAY(two_way_back, BACKWARD_X, BACKWARD_Y)

// two_way_last returns the offset of the last x in y, or -1.
static int64_t two_way_last(const uint8_t *y, int64_t n, const uint8_t *x, int64_t m) {
    if (n < m) {
        return -1;
    }

    int64_t j = two_way_back(y, n, x, m);
    return j < 0 ? -1 : n - m - j;
}

#ifdef SN_DEBUG
// sn_two_way and sn_two_way_last run the Two-Way search alone, in a
// checking build, for the runtime's tests: the filter settles most
// inputs before it would hand them over.
int64_t sn_two_way(sn_string s, sn_string t) { return two_way(s.ptr, s.len, t.ptr, t.len); }
int64_t sn_two_way_last(sn_string s, sn_string t) { return two_way_last(s.ptr, s.len, t.ptr, t.len); }
#endif

// find returns the offset of t in s, or -1. A one-byte t is memchr. A
// longer one is found by its first two bytes, eight places of s at a
// time, as glibc's memmem filters a short needle: the two bytes
// compared against a word of s and the word one byte on, the places
// where both match picked out of the difference at once, and the whole
// of t compared there. The bytes those comparisons cost are counted,
// and once they outrun what the search moved, eight to a byte, with a
// few hundred and four lengths of t to start, the rest of s is searched
// by two_way, so that no t,
// however close to matching everywhere, makes a search cost more than
// in proportion to the lengths.
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
    int64_t i = 0, work = 0;
    for (; i + 7 <= last; i += 8) { // the word one byte on ends at p[i+8], within s as m is two or more
        uint64_t w0, w1;
        memcpy(&w0, p + i, 8);
        memcpy(&w1, p + i + 1, 8);
        uint64_t z = (w0 ^ first) | (w1 ^ second);
        for (uint64_t hits = (z - lo) & ~z & hi; hits != 0; hits &= hits - 1) {
            int64_t at = i + __builtin_ctzll(hits) / 8;
            int64_t same = mismatch(p + at, q, m);
            if (same == m) {
                return at;
            }

            work += same + 1;
            if (work > 8 * at + 256 + 4 * m) { // one near miss of the whole pattern is not yet a pattern of them
                int64_t found = two_way(p + at + 1, n - at - 1, q, m);
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

int64_t sn_string_index(sn_string s, sn_string t) { return find(s, t); }

// last_byte returns the offset of the last c in p, or -1: the memrchr
// glibc has and the other C libraries lack, eight bytes at a time from
// the end, the highest matching byte of a word picked out of it.
static int64_t last_byte(const uint8_t *p, int64_t n, uint8_t c) {
    const uint64_t lo = 0x0101010101010101u, hi = 0x8080808080808080u, seven = 0x7f7f7f7f7f7f7f7fu;
    uint64_t pattern = lo * c;
    int64_t i = n;
    for (; i >= 8; i -= 8) {
        uint64_t w;
        memcpy(&w, p + i - 8, 8);
        uint64_t z = w ^ pattern;
        uint64_t zero = ~(((z & seven) + seven) | z | seven) & hi; // exact: no byte above a zero one is marked
        if (zero != 0) {
            return i - 8 + (63 - __builtin_clzll(zero)) / 8;
        }
    }

    while (i > 0) {
        if (p[--i] == c) {
            return i;
        }
    }

    return -1;
}

// sn_string_last_index returns the offset of the last t in s, or -1,
// by find's search mirrored: a one-byte t by last_byte; a longer one by
// its first two bytes, eight places of s at a time from the end, the
// whole of t compared where both match; and the rest of s searched by
// two_way_last once what the comparisons cost outruns the progress.
int64_t sn_string_last_index(sn_string s, sn_string t) {
    sn_check_string(s);
    sn_check_string(t);
    int64_t n = s.len, m = t.len;
    if (m == 0) {
        return n;
    }

    if (m > n) {
        return -1;
    }

    const uint8_t *p = s.ptr, *q = t.ptr;
    if (m == 1) {
        return last_byte(p, n, q[0]);
    }

    const uint64_t lo = 0x0101010101010101u, hi = 0x8080808080808080u;
    uint64_t first = lo * q[0], second = lo * q[1];
    int64_t last = n - m; // the last offset t fits at
    int64_t i = last - 7, work = 0;
    for (; i >= 0; i -= 8) { // the offsets i to i+7, the word one byte on ending at p[i+8], within s as m is two or more
        uint64_t w0, w1;
        memcpy(&w0, p + i, 8);
        memcpy(&w1, p + i + 1, 8);
        uint64_t z = (w0 ^ first) | (w1 ^ second);
        uint64_t hits = (z - lo) & ~z & hi; // a marked byte above a true one may be false: the comparison decides
        while (hits != 0) {
            int bit = 63 - __builtin_clzll(hits);
            int64_t at = i + bit / 8;
            int64_t same = mismatch(p + at, q, m);
            if (same == m) {
                return at;
            }

            work += same + 1;
            if (work > 8 * (last - at) + 256 + 4 * m) {
                return two_way_last(p, at + m - 1, q, m);
            }

            hits &= ~(1ull << bit);
        }
    }

    for (int64_t j = i + 7; j >= 0; j--) {
        if (p[j] == q[0] && p[j + 1] == q[1] && memcmp(p + j, q, (size_t)m) == 0) {
            return j;
        }
    }

    return -1;
}

// count_byte counts the bytes of p equal to c, in runs of 255 whose
// count fits a byte: a loop the C compiler turns into vector compares
// and adds, sixteen or thirty-two bytes at a time.
static int64_t count_byte(const uint8_t *p, int64_t n, uint8_t c) {
    int64_t count = 0;
    for (int64_t i = 0; i < n; i += 255) {
        int64_t end = n - i < 255 ? n : i + 255;
        uint8_t run = 0;
        for (int64_t j = i; j < end; j++) {
            run += p[j] == c;
        }

        count += run;
    }

    return count;
}

// runes counts the UTF-8 sequences of s, a byte that starts none
// counting as one, as a range over the string steps.
static int64_t runes(sn_string s) {
    int64_t n = 0;
    int32_t r;
    for (int64_t i = 0; i < s.len; n++) {
        i += sn_utf8_decode(s.ptr + i, s.len - i, &r);
    }

    return n;
}

// count_upto counts the occurrences of t in s that do not overlap, up
// to limit, where it stops looking: Replace of the first n needs no
// more than n.
static int64_t count_upto(sn_string s, sn_string t, int64_t limit) {
    if (t.len == 0) {
        int64_t n = runes(s) + 1;
        return n < limit ? n : limit;
    }

    if (t.len == 1 && limit == INT64_MAX) {
        return count_byte(s.ptr, s.len, t.ptr[0]);
    }

    int64_t n = 0;
    for (int64_t start = 0; n < limit;) {
        int64_t at = find(SN_STR(s.ptr + start, s.len - start), t);
        if (at < 0) {
            break;
        }

        n++;
        start += at + t.len;
    }

    return n;
}

// sn_string_count returns the occurrences of t in s that do not
// overlap; an empty t counts the places between the runes, their
// number plus one.
int64_t sn_string_count(sn_string s, sn_string t) {
    sn_check_string(s);
    sn_check_string(t);
    return count_upto(s, t, INT64_MAX);
}

// sn_string_replace returns s with its first n occurrences of old
// replaced by new; n is at most the number of them for every one. The
// occurrences are counted first and the length computed, so the result
// is made in one allocation and filled by memcpy. An empty old matches
// before every rune and at the end. The result is owned: a copy of s
// when nothing matched or n is not positive.
sn_string sn_string_replace(sn_string s, sn_string old, sn_string new, int64_t n) {
    sn_check_string(s);
    sn_check_string(old);
    sn_check_string(new);
    if (n <= 0) {
        return sn_string_clone(s);
    }

    int64_t k = count_upto(s, old, n);
    if (k == 0) {
        return sn_string_clone(s);
    }

    int64_t grow, len;
    if (__builtin_mul_overflow(k, new.len - old.len, &grow) || __builtin_add_overflow(s.len, grow, &len)) {
        sn_panic(SN_STR("out of memory", 13)); // a result no int can measure, which no allocation would hold
    }

    if (len == 0) {
        return SN_STR("", 0);
    }

    uint8_t *out = sn_alloc((size_t)len), *w = out;
    int64_t start = 0;
    for (int64_t i = 0; i < k; i++) {
        int64_t at;
        if (old.len == 0) {
            at = 0;
            if (i > 0 && start < s.len) { // one rune between this match and the last
                int32_t r;
                at = sn_utf8_decode(s.ptr + start, s.len - start, &r);
            }
        } else {
            at = find(SN_STR(s.ptr + start, s.len - start), old);
        }

        memcpy(w, s.ptr + start, (size_t)at);
        w += at;
        memcpy(w, new.ptr, (size_t)new.len);
        w += new.len;
        start += at + old.len;
    }

    memcpy(w, s.ptr + start, (size_t)(s.len - start));
    return SN_OWNED(out, len);
}

sn_string sn_trim_prefix(sn_string s, sn_string p) {
    return sn_has_prefix(s, p) ? SN_STR(s.ptr + p.len, s.len - p.len) : s;
}

sn_string sn_trim_suffix(sn_string s, sn_string p) {
    return sn_has_suffix(s, p) ? SN_STR(s.ptr, s.len - p.len) : s;
}

// sn_split_n splits s at sep into at most n views of s, the last
// holding the rest, as sn_split splits it; n of zero or less gives
// none. The cuts are found once to count the pieces, so the result is
// made at its length.
void sn_split_n(sn_string s, sn_string sep, int64_t n, sn_slice *out) {
    sn_check_string(s);
    sn_check_string(sep);
    out->ptr = NULL;
    out->len = 0;
    out->cap = 0;
    if (n <= 0 || (sep.len == 0 && s.len == 0)) {
        return;
    }

    int64_t pieces = 1;
    if (sep.len == 0) {
        pieces = runes(s);
        if (pieces > n) {
            pieces = n;
        }
    } else {
        for (int64_t start = 0; pieces < n;) {
            int64_t at = find(SN_STR(s.ptr + start, s.len - start), sep);
            if (at < 0) {
                break;
            }

            pieces++;
            start += at + sep.len;
        }
    }

    sn_slice_make(out, 0, pieces, sizeof(sn_string));
    sn_string *f = out->ptr;
    int64_t start = 0;
    for (int64_t i = 0; i < pieces - 1; i++) {
        int64_t at;
        if (sep.len == 0) {
            int32_t r;
            at = sn_utf8_decode(s.ptr + start, s.len - start, &r);
        } else {
            at = find(SN_STR(s.ptr + start, s.len - start), sep);
        }

        f[out->len++] = SN_STR(s.ptr + start, at);
        start += at + sep.len;
    }

    f[out->len++] = SN_STR(s.ptr + start, s.len - start);
}

// sn_bytes_equal reports whether a and b hold the same bytes.
bool sn_bytes_equal(sn_slice_u8 a, sn_slice_u8 b) {
    return a.len == b.len && (a.len == 0 || memcmp(a.ptr, b.ptr, (size_t)a.len) == 0);
}

// sn_bytes_find returns the offset of the first run of a's bytes equal
// to b, or -1, by the search a string's Index makes.
int64_t sn_bytes_find(sn_slice_u8 a, sn_slice_u8 b) {
    return find(SN_STR(a.ptr, a.len), SN_STR(b.ptr, b.len));
}

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

// sn_runes writes the runes of s into a new slice of exactly their
// number, read by the decoder a range over a string uses.
void sn_runes(sn_string s, sn_slice *out) {
    sn_check_string(s);
    int64_t n = 0;
    int32_t r;
    for (int64_t i = 0; i < s.len; n++) {
        i += sn_utf8_decode(s.ptr + i, s.len - i, &r);
    }

    out->ptr = n > 0 ? sn_alloc((size_t)n * sizeof(int32_t)) : NULL;
    out->len = n;
    out->cap = n;
    int32_t *at = out->ptr;
    for (int64_t i = 0, k = 0; i < s.len; k++) {
        i += sn_utf8_decode(s.ptr + i, s.len - i, &at[k]);
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

// sn_strings_reserve makes room in b for n more bytes, growing it once
// as a Push grows: the strings package's Builder.Grow. A room no int
// can measure is a hint that cannot be taken, and changes nothing.
void sn_strings_reserve(sn_slice_u8 *buf, int64_t n) {
    sn_slice *b = (sn_slice *)buf; // the same three words
    sn_slice_own(b, "Grow");
    int64_t need;
    if (__builtin_add_overflow(b->len, n, &need)) {
        return;
    }

    sn_slice_grow(b, need, 1);
}

// sn_strings_copyString copies the bytes of s that fit to the start of
// dst and returns how many: the strings package's Reader.
int64_t sn_strings_copyString(sn_slice_u8 *dst, sn_string s) {
    sn_check_slice((sn_slice *)dst);
    sn_check_string(s);
    int64_t n = dst->len < s.len ? dst->len : s.len;
    if (n > 0) {
        memcpy(dst->ptr, s.ptr, (size_t)n);
    }

    return n;
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
