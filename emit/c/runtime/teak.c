// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "teak.h"

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

void tk_print_string(tk_string s) { fwrite(s.ptr, 1, (size_t)s.len, stdout); }
void tk_print_int(int64_t v) { printf("%lld", (long long)v); }
void tk_print_uint(uint64_t v) { printf("%llu", (unsigned long long)v); }
void tk_print_f64(double v) { tk_print_string(tk_f64_to_string(v)); }
void tk_print_bool(bool v) { fputs(v ? "true" : "false", stdout); }
void tk_print_sep(void) { fputc(' ', stdout); }
void tk_print_nl(void) { fputc('\n', stdout); }
void tk_eprint_string(tk_string s) { fwrite(s.ptr, 1, (size_t)s.len, stderr); }
