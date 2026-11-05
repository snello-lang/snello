// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Quoting as Go's strconv.Quote quotes: %q of a string or a rune.

#include "internal.h"

#include <stdio.h>
#include <string.h>

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

    if (sn_isprint(r)) {
        return sn_utf8_encode(r, (uint8_t *)p);
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

// sn_quote writes s as Go's strconv.Quote and %q write a string: in
// double quotes, a byte that starts no UTF-8 sequence as \xNN, and every
// rune as escape_rune writes it.
sn_string sn_quote(sn_string s) {
    sn_check_string(s);
    char *p = sn_alloc((size_t)s.len * 4 + 3); // \xNN is the most one byte takes, and snprintf ends with a zero
    int64_t n = 0;
    p[n++] = '"';
    for (int64_t i = 0; i < s.len;) {
        int32_t r;
        int width = sn_utf8_decode(s.ptr + i, s.len - i, &r);
        if (r == 0xFFFD && width == 1) { // a byte that starts no rune, as strconv.Quote tells it from a written U+FFFD
            n += snprintf(p + n, 5, "\\x%02x", s.ptr[i]);
        } else {
            n += escape_rune(p + n, r, '"');
        }

        i += width;
    }

    p[n++] = '"';
    return SN_OWNED(p, n);
}

// sn_quote_rune writes r as Go's strconv.QuoteRune and %q write a rune:
// in single quotes, with a value that is no rune, past U+10FFFF or a
// surrogate, written as U+FFFD.
sn_string sn_quote_rune(int64_t r) {
    if (r < 0 || r > 0x10FFFF || (r >= 0xD800 && r <= 0xDFFF)) {
        r = 0xFFFD;
    }

    char *p = sn_alloc(16);
    int64_t n = 0;
    p[n++] = '\'';
    n += escape_rune(p + n, (int32_t)r, '\'');
    p[n++] = '\'';
    return SN_OWNED(p, n);
}

sn_string sn_quote_owned(sn_string s) {
    sn_string q = sn_quote(s);
    sn_string_free(s);
    return q;
}
