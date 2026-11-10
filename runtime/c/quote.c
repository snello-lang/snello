// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Quoting as Go's strconv.Quote quotes, %q of a string or a rune, and
// reading a literal back as Go's strconv.Unquote reads one.

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

// unhex gives the value of a hexadecimal digit, or -1.
static int32_t unhex(uint8_t c) {
    if ('0' <= c && c <= '9') {
        return c - '0';
    }

    c |= 0x20;
    return 'a' <= c && c <= 'f' ? c - 'a' + 10 : -1;
}

// unquote_char reads the character or escape at p, of n bytes and not
// empty, inside the quote marks given, and writes what it stands for at
// out: a rune as its UTF-8, a byte that starts none as U+FFFD, and a
// \x or octal escape as its one byte. It returns the bytes read, or 0
// for a bare quote mark, an escape Go's syntax has none of, or one cut
// short; *wrote is the bytes written.
static int64_t unquote_char(const uint8_t *p, int64_t n, uint8_t quote, uint8_t *out, int64_t *wrote) {
    uint8_t c = p[0];
    if (c == quote) {
        return 0;
    }

    if (c >= 0x80) {
        int32_t r;
        int width = sn_utf8_decode(p, n, &r);
        *wrote = sn_utf8_encode(r, out);
        return width;
    }

    *wrote = 1;
    if (c != '\\') {
        out[0] = c;
        return 1;
    }

    if (n < 2) {
        return 0;
    }

    c = p[1];
    switch (c) {
    case 'a': out[0] = '\a'; return 2;
    case 'b': out[0] = '\b'; return 2;
    case 'f': out[0] = '\f'; return 2;
    case 'n': out[0] = '\n'; return 2;
    case 'r': out[0] = '\r'; return 2;
    case 't': out[0] = '\t'; return 2;
    case 'v': out[0] = '\v'; return 2;
    case '\\': out[0] = '\\'; return 2;
    case '\'':
    case '"':
        if (c != quote) {
            return 0;
        }

        out[0] = c;
        return 2;
    case 'x':
    case 'u':
    case 'U': {
        int digits = c == 'x' ? 2 : c == 'u' ? 4 : 8;
        if (n - 2 < digits) {
            return 0;
        }

        uint32_t v = 0;
        for (int j = 0; j < digits; j++) {
            int32_t x = unhex(p[2 + j]);
            if (x < 0) {
                return 0;
            }

            v = v << 4 | (uint32_t)x;
        }

        if (c == 'x') {
            out[0] = (uint8_t)v;
        } else if (v > 0x10FFFF || (v >= 0xD800 && v <= 0xDFFF)) {
            return 0; // no rune
        } else {
            *wrote = sn_utf8_encode((int32_t)v, out);
        }

        return 2 + digits;
    }
    case '0': case '1': case '2': case '3': case '4': case '5': case '6': case '7': {
        if (n - 2 < 2) {
            return 0;
        }

        int32_t v = c - '0';
        for (int j = 0; j < 2; j++) { // one digit read, and two more
            int32_t x = p[2 + j] - '0';
            if (x < 0 || x > 7) {
                return 0;
            }

            v = v << 3 | x;
        }

        if (v > 255) {
            return 0;
        }

        out[0] = (uint8_t)v;
        return 4;
    }
    }

    return 0;
}

// sn_strconv_unquote reads s as Go's strconv.Unquote reads a literal
// and gives the text it stands for: a string in double quotes, with
// Go's escapes and no bare newline; one in back quotes, taken as it is
// without its carriage returns; or one character in single quotes,
// given as a string. *ok says whether s is one of the three, the whole
// of it; the result is empty when it is not.
sn_string sn_strconv_unquote(sn_string s, bool *ok) {
    sn_check_string(s);
    *ok = false;
    if (s.len < 2) {
        return SN_STR("", 0);
    }

    const uint8_t *p = s.ptr;
    uint8_t quote = p[0];
    if (quote != '"' && quote != '\'' && quote != '`') {
        return SN_STR("", 0);
    }

    // What a literal stands for is no longer than it is, but for a byte
    // that starts no rune, written as the three of U+FFFD: such a byte
    // is above the ASCII ones, so two more for each of those is room
    // for any.
    int64_t room = s.len;
    for (int64_t k = 0; k < s.len; k++) {
        room += 2 * (p[k] >> 7);
    }

    uint8_t *out = sn_alloc((size_t)room);
    int64_t n = 0, i = 1;
    if (quote == '`') {
        for (; i < s.len && p[i] != '`'; i++) {
            if (p[i] != '\r') {
                out[n++] = p[i];
            }
        }
    } else {
        while (i < s.len && p[i] != quote) {
            int64_t wrote = 0;
            int64_t width = p[i] == '\n' ? 0 : unquote_char(p + i, s.len - i, quote, out + n, &wrote);
            if (width == 0) {
                break;
            }

            i += width;
            n += wrote;
            if (quote == '\'') {
                break; // one character
            }
        }
    }

    if (i != s.len - 1 || p[i] != quote || (quote == '\'' && i == 1)) { // the closing mark ends s
        sn_free(out);
        return SN_STR("", 0);
    }

    *ok = true;
    if (n == 0) {
        sn_free(out);
        return SN_STR("", 0);
    }

    return SN_OWNED(out, n);
}
