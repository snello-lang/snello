// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The runtime's half of the unicode and unicode/utf8 packages: each
// function the packages declare without a body, over the tables of
// unicode.c and the one UTF-8 decoder of snello.c.

#include "internal.h"

#include <string.h>

// major is the first letter of r's general category: L, M, N, P, S, Z
// or C.
static char major(int32_t r) { return sn_category_name(sn_rune_category(r))[0]; }

bool sn_unicode_IsLetter(int32_t r) { return sn_rune_letter(r); }
bool sn_unicode_IsDigit(int32_t r) { return sn_rune_digit(r); }
bool sn_unicode_IsSpace(int32_t r) { return sn_rune_space(r); }
// derived reports whether r has the derived property bit, from
// DerivedCoreProperties.txt or emoji-data.txt.
static bool derived(int32_t r, int bit) { return r >= 0 && r <= 0x10FFFF && (sn_uc_derived(r) & bit) != 0; }

bool sn_unicode_IsUpper(int32_t r) { return derived(r, SN_UC_UPPERCASE); }
bool sn_unicode_IsLower(int32_t r) { return derived(r, SN_UC_LOWERCASE); }
bool sn_unicode_IsAlphabetic(int32_t r) { return derived(r, SN_UC_ALPHABETIC); }
bool sn_unicode_IsEmoji(int32_t r) { return derived(r, SN_UC_EMOJI); }
bool sn_unicode_IsIDStart(int32_t r) { return derived(r, SN_UC_XID_START); }
bool sn_unicode_IsIDContinue(int32_t r) { return derived(r, SN_UC_XID_CONTINUE); }

int64_t sn_unicode_digit(int32_t r) { return r < 0 || r > 0x10FFFF ? -1 : sn_uc_decimal(r); }

bool sn_unicode_numeric(int32_t r, int64_t *num, int64_t *den) {
    int32_t d;
    if (r < 0 || r > 0x10FFFF || !sn_uc_numeric(r, num, &d)) {
        return false;
    }

    *den = d;
    return true;
}
bool sn_unicode_IsTitle(int32_t r) { return sn_rune_category(r) == SN_GC_Lt; }
bool sn_unicode_IsMark(int32_t r) { return major(r) == 'M'; }
bool sn_unicode_IsNumber(int32_t r) { return major(r) == 'N'; }
bool sn_unicode_IsPunct(int32_t r) { return major(r) == 'P'; }
bool sn_unicode_IsSymbol(int32_t r) { return major(r) == 'S'; }
bool sn_unicode_IsControl(int32_t r) { return sn_rune_category(r) == SN_GC_Cc; }
bool sn_unicode_IsPrint(int32_t r) { return r >= 0 && r <= 0x10FFFF && sn_isprint(r); }

bool sn_unicode_IsGraphic(int32_t r) {
    char m = major(r);
    return m == 'L' || m == 'M' || m == 'N' || m == 'P' || m == 'S' || sn_rune_category(r) == SN_GC_Zs;
}

int32_t sn_unicode_ToUpper(int32_t r) { return sn_rune_upper(r); }
int32_t sn_unicode_ToLower(int32_t r) { return sn_rune_lower(r); }
int32_t sn_unicode_ToTitle(int32_t r) { return sn_rune_title(r); }
int32_t sn_unicode_CaseFold(int32_t r) { return sn_rune_fold(r); }

// A category's or a script's name is a literal of the tables, which
// the string lends: a string of capacity zero owns nothing.
sn_string sn_unicode_Category(int32_t r) {
    const char *name = sn_category_name(sn_rune_category(r));
    return SN_STR(name, (int64_t)strlen(name));
}

sn_string sn_unicode_Script(int32_t r) {
    const char *name = sn_script_name(sn_rune_script(r));
    return SN_STR(name, (int64_t)strlen(name));
}

sn_string sn_unicode_ScriptCode(int32_t r) {
    const char *code = sn_script_code(sn_rune_script(r));
    return SN_STR(code, (int64_t)strlen(code));
}

// same reports whether the C string s is the string t.
static bool same(const char *s, sn_string t) { return (int64_t)strlen(s) == t.len && memcmp(s, t.ptr, (size_t)t.len) == 0; }

// sn_unicode_InScript reports whether r's script is the one named, by
// its long name, Latin, or its four-letter code, Latn.
bool sn_unicode_InScript(int32_t r, sn_string name) {
    int s = sn_rune_script(r);
    return same(sn_script_name(s), name) || same(sn_script_code(s), name);
}

int64_t sn_unicode__utf8_decode(sn_slice_u8 p, int32_t *r) {
    if (p.len == 0) {
        return 0;
    }

    return sn_utf8_decode(p.ptr, p.len, r);
}

int64_t sn_unicode__utf8_decodeString(sn_string s, int32_t *r) {
    if (s.len == 0) {
        return 0;
    }

    return sn_utf8_decode(s.ptr, s.len, r);
}

// valid reports whether the n bytes at p are UTF-8 throughout: the
// decoder reads a byte that starts no valid sequence as U+FFFD of
// width one, where the rune itself takes three bytes.
static bool valid(const uint8_t *p, int64_t n) {
    for (int64_t i = 0; i < n;) {
        if (p[i] < 0x80) {
            i++;
            continue;
        }

        int32_t r;
        int w = sn_utf8_decode(p + i, n - i, &r);
        if (r == 0xFFFD && w == 1) {
            return false;
        }

        i += w;
    }

    return true;
}

bool sn_unicode__utf8_valid(sn_slice_u8 p) { return valid(p.ptr, p.len); }
bool sn_unicode__utf8_validString(sn_string s) { return valid(s.ptr, s.len); }

// count counts the runes of the n bytes at p as a range reads them.
static int64_t count(const uint8_t *p, int64_t n) {
    int64_t k = 0;
    for (int64_t i = 0; i < n; k++) {
        if (p[i] < 0x80) {
            i++;
            continue;
        }

        int32_t r;
        i += sn_utf8_decode(p + i, n - i, &r);
    }

    return k;
}

int64_t sn_unicode__utf8_count(sn_slice_u8 p) { return count(p.ptr, p.len); }
int64_t sn_unicode__utf8_countString(sn_string s) { return count(s.ptr, s.len); }

// full reports whether the n bytes at p begin with a whole rune, or
// with a byte sequence that is no rune's start and so reads as U+FFFD
// whatever follows; false only for the start of a rune cut short.
static bool full(const uint8_t *p, int64_t n) {
    if (n == 0) {
        return false;
    }

    uint8_t c = p[0];
    int w = 1;
    uint8_t lo = 0x80, hi = 0xBF; // the second byte's range
    if (c >= 0xC2 && c <= 0xDF) {
        w = 2;
    } else if (c >= 0xE0 && c <= 0xEF) {
        w = 3;
        lo = c == 0xE0 ? 0xA0 : 0x80;
        hi = c == 0xED ? 0x9F : 0xBF;
    } else if (c >= 0xF0 && c <= 0xF4) {
        w = 4;
        lo = c == 0xF0 ? 0x90 : 0x80;
        hi = c == 0xF4 ? 0x8F : 0xBF;
    }

    if (n >= w || w == 1) {
        return true;
    }

    for (int64_t i = 1; i < n; i++) {
        uint8_t b = p[i];
        bool ok = i == 1 ? b >= lo && b <= hi : b >= 0x80 && b <= 0xBF;
        if (!ok) {
            return true; // reads as U+FFFD of width one, whole already
        }
    }

    return false;
}

bool sn_unicode__utf8_full(sn_slice_u8 p) { return full(p.ptr, p.len); }
bool sn_unicode__utf8_fullString(sn_string s) { return full(s.ptr, s.len); }

// last reads the rune that ends the n bytes at p into r and returns its
// width: the decoder's reading of the last rune that starts within four
// bytes of the end and runs to it, or U+FFFD of width one; zero for no
// bytes.
static int64_t last(const uint8_t *p, int64_t n, int32_t *r) {
    if (n == 0) {
        return 0;
    }

    if (p[n - 1] < 0x80) {
        *r = p[n - 1];
        return 1;
    }

    int64_t lim = n > 4 ? n - 4 : 0;
    for (int64_t start = n - 1; start >= lim; start--) {
        if ((p[start] & 0xC0) != 0x80) { // a byte that starts a rune, or no continuation
            int32_t got;
            int w = sn_utf8_decode(p + start, n - start, &got);
            if (start + w == n) {
                *r = got;
                return w;
            }

            break;
        }
    }

    *r = 0xFFFD;
    return 1;
}

int64_t sn_unicode__utf8_decodeLast(sn_slice_u8 p, int32_t *r) { return last(p.ptr, p.len, r); }
int64_t sn_unicode__utf8_decodeLastString(sn_string s, int32_t *r) { return last(s.ptr, s.len, r); }
