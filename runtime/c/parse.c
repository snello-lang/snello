// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Reading a float as Go's strconv.ParseFloat reads one, in Go's
// grammar.

#include "internal.h"

#include <ctype.h>
#include <errno.h>
#include <math.h>
#include <stdlib.h>
#include <string.h>

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
static bool go_float(sn_string s, char *out) {
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

// sn_strconv_parseFloat reads a floating-point number as Go's
// strconv.ParseFloat does, the whole text being the number. 1 is a text
// that is not a number, 2 one that does not fit.
int64_t sn_strconv_parseFloat(sn_string s, double *out) {
    sn_check_string(s);
    char *p = sn_alloc((size_t)s.len + 1);
    if (!go_float(s, p)) {
        sn_free(p);
        return 1;
    }

    char *end;
    errno = 0;
    double v = strtod(p, &end);
    int64_t code = *end != '\0' ? 1 : errno == ERANGE && fabs(v) == HUGE_VAL ? 2 : 0; // a number too small to hold rounds to zero, as a literal does
    sn_free(p);
    if (code == 0) {
        *out = v;
    }

    return code;
}
