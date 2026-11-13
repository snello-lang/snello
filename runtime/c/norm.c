// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Normalization, the runtime's half of unicode/norm, as the standard's
// annex 15 defines it: a string is decomposed, canonically for NFD and
// NFC and by compatibility as well for NFKD and NFKC, each rune to the
// end of its mappings and a Hangul syllable by its algorithm; the marks
// of each run are put in canonical order by their combining class, a
// stable sort; and for NFC and NFKC the runes are composed again, a
// mark with the starter before it when nothing between blocks it and
// the pair has a composition the exclusions leave in. The tables are
// unicode.c's.

#include "internal.h"

// The Hangul syllables' algorithm, section 3.12 of the standard.
enum {
    S_BASE = 0xAC00,
    L_BASE = 0x1100,
    V_BASE = 0x1161,
    T_BASE = 0x11A7,
    L_COUNT = 19,
    V_COUNT = 21,
    T_COUNT = 28,
    N_COUNT = V_COUNT * T_COUNT,
    S_COUNT = L_COUNT * N_COUNT,
};

// The forms, numbered as unicode/norm's Form.
enum { NFC, NFD, NFKC, NFKD };

// A buffer of runes that grows as Push does.
typedef struct {
    int32_t *r;
    int64_t n, cap;
} runes;

static void norm_push(runes *b, int32_t r) {
    if (b->n == b->cap) {
        int64_t cap = b->cap < 16 ? 16 : b->cap * 2;
        int32_t *grown = sn_alloc((size_t)cap * sizeof(int32_t));
        for (int64_t i = 0; i < b->n; i++) {
            grown[i] = b->r[i];
        }

        if (b->r != NULL) {
            sn_free(b->r);
        }

        b->r = grown;
        b->cap = cap;
    }

    b->r[b->n++] = r;
}

// decompose appends the full decomposition of r to b.
static void decompose(runes *b, int32_t r, bool compat) {
    if (r >= S_BASE && r < S_BASE + S_COUNT) {
        int32_t s = r - S_BASE;
        norm_push(b, L_BASE + s / N_COUNT);
        norm_push(b, V_BASE + (s % N_COUNT) / T_COUNT);
        if (s % T_COUNT != 0) {
            norm_push(b, T_BASE + s % T_COUNT);
        }

        return;
    }

    const int32_t *to;
    int n = sn_uc_decomp(r, compat, &to);
    if (n == 0) {
        norm_push(b, r);
        return;
    }

    for (int i = 0; i < n; i++) {
        decompose(b, to[i], compat);
    }
}

// sortRun puts the n marks at r in canonical order, stably by combining
// class: a counting sort over the 255 classes, so a run of any length,
// which untrusted text may hold, takes time in proportion to it.
static void sortRun(int32_t *r, int64_t n) {
    if (n < 2) {
        return;
    }

    int64_t count[257] = {0};
    for (int64_t i = 0; i < n; i++) {
        count[sn_uc_ccc(r[i]) + 1]++;
    }

    for (int i = 1; i < 257; i++) {
        count[i] += count[i - 1];
    }

    int32_t small[32];
    int32_t *sorted = n <= 32 ? small : sn_alloc((size_t)n * sizeof(int32_t));
    for (int64_t i = 0; i < n; i++) {
        sorted[count[sn_uc_ccc(r[i])]++] = r[i];
    }

    for (int64_t i = 0; i < n; i++) {
        r[i] = sorted[i];
    }

    if (sorted != small) {
        sn_free(sorted);
    }
}

// order puts every run of marks, the runes of a combining class above
// zero between two starters, in canonical order.
static void order(runes *b) {
    for (int64_t i = 0; i < b->n;) {
        if (sn_uc_ccc(b->r[i]) == 0) {
            i++;
            continue;
        }

        int64_t j = i;
        while (j < b->n && sn_uc_ccc(b->r[j]) != 0) {
            j++;
        }

        sortRun(b->r + i, j - i);
        i = j;
    }
}

// pair returns the composition of a and b, a Hangul one included, or -1.
static int32_t pair(int32_t a, int32_t b) {
    if (a >= L_BASE && a < L_BASE + L_COUNT && b >= V_BASE && b < V_BASE + V_COUNT) {
        return S_BASE + ((a - L_BASE) * V_COUNT + (b - V_BASE)) * T_COUNT;
    }

    if (a >= S_BASE && a < S_BASE + S_COUNT && (a - S_BASE) % T_COUNT == 0 && b > T_BASE && b < T_BASE + T_COUNT) {
        return a + (b - T_BASE);
    }

    return sn_uc_compose(a, b);
}

// compose composes b in place: each rune with the last starter when no
// rune between has a combining class as high or higher, or is a starter.
static void compose(runes *b) {
    if (b->n == 0) {
        return;
    }

    int64_t starter = 0, out = 1;
    bool started = sn_uc_ccc(b->r[0]) == 0; // a string that starts with a mark has no starter to compose with until one comes
    int last = 0;                            // the combining class of the last rune kept since the starter
    for (int64_t i = 1; i < b->n; i++) {
        int32_t c = b->r[i];
        int cc = sn_uc_ccc(c);
        int32_t comp = started ? pair(b->r[starter], c) : -1;
        if (comp >= 0 && (last < cc || last == 0)) {
            b->r[starter] = comp;
            continue;
        }

        if (cc == 0) {
            starter = out;
            started = true;
            last = 0;
        } else {
            last = cc;
        }

        b->r[out++] = c;
    }

    b->n = out;
}

// normalize returns s in form f, a new string.
static sn_string normalize(sn_string s, int64_t f) {
    sn_check_string(s);
    runes b = {NULL, 0, 0};
    bool compat = f == NFKC || f == NFKD;
    for (int64_t i = 0; i < s.len;) {
        int32_t r;
        i += sn_utf8_decode(s.ptr + i, s.len - i, &r);
        decompose(&b, r, compat);
    }

    order(&b);
    if (f == NFC || f == NFKC) {
        compose(&b);
    }

    uint8_t tmp[4];
    int64_t size = 0;
    for (int64_t i = 0; i < b.n; i++) {
        size += sn_utf8_encode(b.r[i], tmp);
    }

    uint8_t *p = sn_alloc((size_t)(size > 0 ? size : 1));
    for (int64_t i = 0, at = 0; i < b.n; i++) {
        at += sn_utf8_encode(b.r[i], p + at);
    }

    if (b.r != NULL) {
        sn_free(b.r);
    }

    return SN_OWNED(p, size);
}

sn_string sn_unicode__norm_normalize(sn_string s, int64_t f) { return normalize(s, f); }

// The quick check of the standard's annex 15, section 9: whether s is
// in form f, from the quick-check values and the combining classes
// alone, answering yes, no, or maybe when only normalizing can tell.
enum { QC_YES, QC_NO, QC_MAYBE };

static int quick(sn_string s, int64_t f) {
    int no = f == NFC ? SN_QC_NFC_NO : f == NFD ? SN_QC_NFD_NO : f == NFKC ? SN_QC_NFKC_NO : SN_QC_NFKD_NO;
    int maybe = f == NFC ? SN_QC_NFC_MAYBE : f == NFKC ? SN_QC_NFKC_MAYBE : 0;
    int result = QC_YES, last = 0;
    for (int64_t i = 0; i < s.len;) {
        if (s.ptr[i] < 0x80) { // ASCII is in every form, and a starter
            i++;
            last = 0;
            continue;
        }

        int32_t r;
        int w = sn_utf8_decode(s.ptr + i, s.len - i, &r);
        if (r == 0xFFFD && w == 1) {
            return QC_NO; // a byte that is not UTF-8 normalizes to U+FFFD, so the string is not normal
        }

        i += w;
        int cc = sn_uc_ccc(r);
        if (last > cc && cc != 0) {
            return QC_NO;
        }

        int q = sn_uc_quick(r);
        if (q & no) {
            return QC_NO;
        }

        if (q & maybe) {
            result = QC_MAYBE;
        }

        last = cc;
    }

    return result;
}

// sn_unicode__norm_isNormal reports whether s is already in form f: the
// quick check, and for its maybe the normalized string, compared and
// released.
bool sn_unicode__norm_isNormal(sn_string s, int64_t f) {
    sn_check_string(s);
    int q = quick(s, f);
    if (q != QC_MAYBE) {
        return q == QC_YES;
    }

    sn_string t = normalize(s, f);
    bool same = t.len == s.len;
    for (int64_t i = 0; same && i < s.len; i++) {
        same = t.ptr[i] == s.ptr[i];
    }

    sn_string_drop(&t);
    return same;
}
