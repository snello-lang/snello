// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Text segmentation, the runtime's half of unicode/segment, as the
// standard's annex 29 defines it: the boundaries of grapheme clusters,
// rules GB3 to GB999; of words, WB3 to WB999; and of sentences, SB3 to
// SB998. sn_unicode__segment_next measures the segment that starts at a
// boundary; a segment starts afresh at each boundary, since no rule
// reaches back past one. The properties are unicode.c's.

#include "internal.h"

// The kinds, numbered as unicode/segment's calls name them.
enum { GRAPHEMES, WORDS, SENTENCES };

// A cursor decodes the runes of a string from a start as far as a rule
// asks, keeping each rune and the offset it starts at.
typedef struct {
    const uint8_t *p;
    int64_t n, pos;
    int32_t *r;
    int64_t *off;
    int64_t len, cap;
    int32_t small_r[32];
    int64_t small_off[33];
} cursor;

// get makes rune k decoded and reports whether the string has it.
static bool get(cursor *c, int64_t k) {
    while (c->len <= k) {
        if (c->pos >= c->n) {
            return false;
        }

        if (c->len + 1 >= c->cap) {
            int64_t cap = c->cap * 2;
            int32_t *r = sn_alloc((size_t)cap * sizeof(int32_t));
            int64_t *off = sn_alloc((size_t)(cap + 1) * sizeof(int64_t));
            for (int64_t i = 0; i < c->len; i++) {
                r[i] = c->r[i];
                off[i] = c->off[i];
            }

            off[c->len] = c->off[c->len];
            if (c->r != c->small_r) {
                sn_free(c->r);
                sn_free(c->off);
            }

            c->r = r;
            c->off = off;
            c->cap = cap;
        }

        c->off[c->len] = c->pos;
        c->pos += sn_utf8_decode(c->p + c->pos, c->n - c->pos, &c->r[c->len]);
        c->len++;
        c->off[c->len] = c->pos;
    }

    return true;
}

static void release(cursor *c) {
    if (c->r != c->small_r) {
        sn_free(c->r);
        sn_free(c->off);
    }
}

// Grapheme clusters. prev is the property of the rune before the
// candidate boundary, and the state the rules after GB9 need: how many
// regional indicators end the cluster, whether it ends in an emoji
// sequence, Extended_Pictographic Extend* then ZWJ, and whether it ends
// in a conjunct linker, InCB=Linker then InCB=Extend*, which GB9c, as
// Unicode 18 states it, joins to the consonant after it.
static int64_t grapheme(cursor *c) {
    get(c, 0);
    int32_t r = c->r[0];
    int prev = sn_uc_gcb(r);
    int ri = prev == SN_GCB_RI ? 1 : 0;
    int emoji = sn_uc_extpict(r) ? 1 : 0; // 1 after ExtPict Extend*, 2 after its ZWJ
    bool linked = sn_uc_incb(r) == SN_INCB_Linker; // after a linker and its extenders
    for (int64_t k = 1; get(c, k); k++) {
        r = c->r[k];
        int p = sn_uc_gcb(r);
        int incb = sn_uc_incb(r);
        bool join;
        if (prev == SN_GCB_CR && p == SN_GCB_LF) {
            join = true; // GB3
        } else if (prev == SN_GCB_Control || prev == SN_GCB_CR || prev == SN_GCB_LF) {
            join = false; // GB4
        } else if (p == SN_GCB_Control || p == SN_GCB_CR || p == SN_GCB_LF) {
            join = false; // GB5
        } else if (prev == SN_GCB_L && (p == SN_GCB_L || p == SN_GCB_V || p == SN_GCB_LV || p == SN_GCB_LVT)) {
            join = true; // GB6
        } else if ((prev == SN_GCB_LV || prev == SN_GCB_V) && (p == SN_GCB_V || p == SN_GCB_T)) {
            join = true; // GB7
        } else if ((prev == SN_GCB_LVT || prev == SN_GCB_T) && p == SN_GCB_T) {
            join = true; // GB8
        } else if (p == SN_GCB_Extend || p == SN_GCB_ZWJ || p == SN_GCB_SpacingMark || prev == SN_GCB_Prepend) {
            join = true; // GB9, GB9a, GB9b
        } else if (incb == SN_INCB_Consonant && linked) {
            join = true; // GB9c
        } else if (prev == SN_GCB_ZWJ && emoji == 2 && sn_uc_extpict(r)) {
            join = true; // GB11
        } else if (prev == SN_GCB_RI && p == SN_GCB_RI && ri % 2 == 1) {
            join = true; // GB12, GB13
        } else {
            join = false; // GB999
        }

        if (!join) {
            return c->off[k];
        }

        ri = p == SN_GCB_RI ? ri + 1 : 0;
        if (sn_uc_extpict(r)) {
            emoji = 1;
        } else if (emoji == 1 && p == SN_GCB_Extend) {
            emoji = 1;
        } else if (emoji == 1 && p == SN_GCB_ZWJ) {
            emoji = 2;
        } else {
            emoji = 0;
        }

        linked = incb == SN_INCB_Linker || (linked && incb == SN_INCB_Extend);

        prev = p;
    }

    return c->n;
}

// Words. wb gives the property of rune k, and the rules from WB4 on see
// a rune with the Extend, Format and ZWJ runes after it folded into it.
static int wb(cursor *c, int64_t k) { return get(c, k) ? sn_uc_wb(c->r[k]) : -1; }

static bool ignored(int p) { return p == SN_WB_Extend || p == SN_WB_Format || p == SN_WB_ZWJ; }
static bool ahletter(int p) { return p == SN_WB_ALetter || p == SN_WB_Hebrew_Letter; }
static bool midnumletq(int p) { return p == SN_WB_MidNumLet || p == SN_WB_Single_Quote; }

// before returns the index of the rune the rules see before index k,
// skipping the runes WB4 folds into it, or -1.
static int64_t before(cursor *c, int64_t k) {
    int64_t i = k - 1;
    while (i > 0 && ignored(wb(c, i))) {
        i--;
    }

    return i;
}

// after returns the index of the rune the rules see after index k, or
// -1 at the end.
static int64_t after(cursor *c, int64_t k) {
    int64_t i = k + 1;
    while (get(c, i) && ignored(wb(c, i))) {
        i++;
    }

    return get(c, i) ? i : -1;
}

static int64_t word(cursor *c) {
    get(c, 0);
    for (int64_t k = 1; get(c, k); k++) {
        int a = wb(c, k - 1), b = wb(c, k);
        bool join;
        if (a == SN_WB_CR && b == SN_WB_LF) {
            join = true; // WB3
        } else if (a == SN_WB_Newline || a == SN_WB_CR || a == SN_WB_LF) {
            join = false; // WB3a
        } else if (b == SN_WB_Newline || b == SN_WB_CR || b == SN_WB_LF) {
            join = false; // WB3b
        } else if (a == SN_WB_ZWJ && sn_uc_extpict(c->r[k])) {
            join = true; // WB3c
        } else if (a == SN_WB_WSegSpace && b == SN_WB_WSegSpace) {
            join = true; // WB3d
        } else if (ignored(b)) {
            join = true; // WB4
        } else {
            int64_t pi = before(c, k);
            int prev = wb(c, pi);
            int64_t ppi = pi > 0 ? before(c, pi) : -1;
            int pprev = ppi >= 0 ? wb(c, ppi) : -1;
            int64_t ni = after(c, k);
            int next = ni >= 0 ? wb(c, ni) : -1;
            int ri = 0; // the regional indicators the rules see before k, WB15 and WB16
            for (int64_t i = pi; i >= 0 && wb(c, i) == SN_WB_RI; i = i > 0 ? before(c, i) : -1) {
                ri++;
                if (i == 0) {
                    break;
                }
            }

            if (ahletter(prev) && ahletter(b)) {
                join = true; // WB5
            } else if (ahletter(prev) && (b == SN_WB_MidLetter || midnumletq(b)) && ahletter(next)) {
                join = true; // WB6
            } else if (ahletter(pprev) && (prev == SN_WB_MidLetter || midnumletq(prev)) && ahletter(b)) {
                join = true; // WB7
            } else if (prev == SN_WB_Hebrew_Letter && b == SN_WB_Single_Quote) {
                join = true; // WB7a
            } else if (prev == SN_WB_Hebrew_Letter && b == SN_WB_Double_Quote && next == SN_WB_Hebrew_Letter) {
                join = true; // WB7b
            } else if (pprev == SN_WB_Hebrew_Letter && prev == SN_WB_Double_Quote && b == SN_WB_Hebrew_Letter) {
                join = true; // WB7c
            } else if (prev == SN_WB_Numeric && b == SN_WB_Numeric) {
                join = true; // WB8
            } else if (ahletter(prev) && b == SN_WB_Numeric) {
                join = true; // WB9
            } else if (prev == SN_WB_Numeric && ahletter(b)) {
                join = true; // WB10
            } else if (pprev == SN_WB_Numeric && (prev == SN_WB_MidNum || midnumletq(prev)) && b == SN_WB_Numeric) {
                join = true; // WB11
            } else if (prev == SN_WB_Numeric && (b == SN_WB_MidNum || midnumletq(b)) && next == SN_WB_Numeric) {
                join = true; // WB12
            } else if (prev == SN_WB_Katakana && b == SN_WB_Katakana) {
                join = true; // WB13
            } else if ((ahletter(prev) || prev == SN_WB_Numeric || prev == SN_WB_Katakana || prev == SN_WB_ExtendNumLet) && b == SN_WB_ExtendNumLet) {
                join = true; // WB13a
            } else if (prev == SN_WB_ExtendNumLet && (ahletter(b) || b == SN_WB_Numeric || b == SN_WB_Katakana)) {
                join = true; // WB13b
            } else if (prev == SN_WB_RI && b == SN_WB_RI && ri % 2 == 1) {
                join = true; // WB15, WB16
            } else {
                join = false; // WB999
            }
        }

        if (!join) {
            return c->off[k];
        }
    }

    return c->n;
}

// Sentences. sb gives the property of rune k, and the rules from SB5
// on see a rune with the Extend and Format runes after it folded in.
static int sb(cursor *c, int64_t k) { return get(c, k) ? sn_uc_sb(c->r[k]) : -1; }

static bool sfolded(int p) { return p == SN_SB_Extend || p == SN_SB_Format; }
static bool parasep(int p) { return p == SN_SB_Sep || p == SN_SB_CR || p == SN_SB_LF; }
static bool saterm(int p) { return p == SN_SB_STerm || p == SN_SB_ATerm; }

// lowerAhead reports whether, from rune k on, the first rune the rules
// see that is an OLetter, Upper, Lower, ParaSep or SATerm is a Lower:
// the right side of SB8. The first such rune found is kept in *stop,
// with whether it is a Lower in *lower, and reused while k has not
// passed it, so that the scans of one sentence read each rune once.
static bool lowerAhead(cursor *c, int64_t k, int64_t *stop, bool *lower) {
    if (*stop >= k) {
        return *lower;
    }

    for (int64_t j = k; get(c, j); j++) {
        int p = sb(c, j);
        if (p == SN_SB_OLetter || p == SN_SB_Upper || p == SN_SB_Lower || parasep(p) || saterm(p)) {
            *stop = j;
            *lower = p == SN_SB_Lower;
            return *lower;
        }
    }

    *stop = INT64_MAX; // none to the end: the answer holds for every k after
    *lower = false;
    return false;
}

// sentence carries forward what the rules after SB5 ask of the runes
// before a candidate boundary, the runes SB5 folds left out: the last
// one and the one before it, and whether they end in SATerm Close* Sp*,
// with the terminator and whether a space came after it. Each rune is
// read a bounded number of times, so a sentence takes time in
// proportion to its length.
static int64_t sentence(cursor *c) {
    get(c, 0);
    int last = sb(c, 0), pre = -1;
    int term = saterm(last) ? last : -1;
    bool sp = false;
    int64_t stop = -1;
    bool lower = false;
    for (int64_t k = 1; get(c, k); k++) {
        int a = sb(c, k - 1), b = sb(c, k);
        bool join;
        if (a == SN_SB_CR && b == SN_SB_LF) {
            join = true; // SB3
        } else if (parasep(a)) {
            join = false; // SB4
        } else if (sfolded(b)) {
            join = true; // SB5
        } else if (last == SN_SB_ATerm && b == SN_SB_Numeric) {
            join = true; // SB6
        } else if (last == SN_SB_ATerm && b == SN_SB_Upper && (pre == SN_SB_Upper || pre == SN_SB_Lower)) {
            join = true; // SB7
        } else if (term == SN_SB_ATerm && lowerAhead(c, k, &stop, &lower)) {
            join = true; // SB8
        } else if (term >= 0 && (b == SN_SB_SContinue || saterm(b))) {
            join = true; // SB8a
        } else if (term >= 0 && !sp && (b == SN_SB_Close || b == SN_SB_Sp || parasep(b))) {
            join = true; // SB9
        } else if (term >= 0 && (b == SN_SB_Sp || parasep(b))) {
            join = true; // SB10
        } else {
            join = term < 0; // SB11 breaks after a sentence's end; SB998 joins anything else
        }

        if (!join) {
            return c->off[k];
        }

        if (sfolded(b)) {
            continue; // SB5: the rune before keeps standing for both
        }

        if (saterm(b)) {
            term = b;
            sp = false;
        } else if (term >= 0 && b == SN_SB_Close && !sp) {
            // a closing mark after the terminator
        } else if (term >= 0 && b == SN_SB_Sp) {
            sp = true;
        } else {
            term = -1;
            sp = false;
        }

        pre = last;
        last = b;
    }

    return c->n;
}

// sn_unicode__segment_next returns the length in bytes of the segment of
// the kind that starts at the boundary at, or 0 at the end of s; an at
// outside s is an index out of range.
int64_t sn_unicode__segment_next(sn_string s, int64_t at, int64_t kind) {
    sn_check_string(s);
    if (at < 0 || at > s.len) {
        sn_bounds(at, s.len);
    }

    if (at == s.len) {
        return 0;
    }

    cursor c = {s.ptr + at, s.len - at, 0, NULL, NULL, 0, 32, {0}, {0}};
    c.r = c.small_r;
    c.off = c.small_off;
    int64_t n;
    switch (kind) {
    case GRAPHEMES:
        n = grapheme(&c);
        break;
    case WORDS:
        n = word(&c);
        break;
    default:
        n = sentence(&c);
        break;
    }

    release(&c);
    return n;
}
