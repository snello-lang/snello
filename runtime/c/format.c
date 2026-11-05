// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Numbers as text: integers by two digits a step, floats in Go's
// shortest form by Ryū and to a precision by Ryū's fixed form, laid out
// as %e, %f and %g lay them out, and the width and precision fmt pads
// and cuts to.

#include "internal.h"

#include <float.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

sn_string sn_int_to_string(int64_t v) {
    return v < 0 ? sn_format_int(0 - (uint64_t)v, true) : sn_format_int((uint64_t)v, false);
}

sn_string sn_uint_to_string(uint64_t v) {
    return sn_format_int(v, false);
}

// Floats print as Go's strconv prints them. A decimal is the digits of
// a positive finite number, without trailing zeros, and where the point
// goes among them: 0.25 is "25" with the point at 0, 120 is "12" with
// the point at 3. The layout around the digits is Go's.
typedef struct {
    char *d;  // the digits, from sn_alloc
    int nd;   // how many
    int dp;   // the point's place
} decimal;

// Shortest digits are Ryū's (Ulf Adams, 2018), as Go's strconv writes
// them, ported from ftoaryu.go: the number's interval of values that
// read back as it, scaled by a power of ten to an integer, and the
// fewest digits inside it, with the correctly rounded one when several
// are. One 128-bit multiply per bound and no division by anything but
// constants, where reading candidates back with strtod costs a dozen
// library calls a number. sn_pow10 is mkpow10.go's table.
extern const uint64_t sn_pow10[696][2];

typedef struct {
    char buf[32];
    char *d; // into buf
    int nd;
    int dp;
} ryu_dec;

static int mul_log2_log10(int x) { return (x * 78913) >> 18; }  // floor(x * log10(2))
static int mul_log10_log2(int x) { return (x * 108853) >> 15; } // floor(x * log2(10))

// ryu_digits32 writes the digits of a number below 1e9 from the bounds
// of its interval, central rounded up when cup says so, ending at
// endindex.
static void ryu_digits32(ryu_dec *d, uint32_t lower, uint32_t central, uint32_t upper, bool c0, bool cup, int endindex) {
    if (upper == 0) {
        d->dp = endindex + 1;
        return;
    }

    int trimmed = 0;
    uint32_t next = 0; // the last digit trimmed, for rounding
    while (upper > 0) {
        uint32_t l = (lower + 9) / 10, c = central / 10, cdigit = central % 10, u = upper / 10;
        if (l > u) {
            break; // trimming more would leave the interval
        }

        if (l == c + 1 && c < u) {
            c++;
            cdigit = 0;
            cup = false;
        }

        trimmed++;
        c0 = c0 && next == 0;
        next = cdigit;
        lower = l;
        central = c;
        upper = u;
    }

    if (trimmed > 0) {
        cup = next > 5 || (next == 5 && !c0) || (next == 5 && c0 && (central & 1) == 1);
    }

    if (central < upper && cup) {
        central++;
    }

    endindex -= trimmed;
    uint32_t v = central;
    int n = endindex;
    while (n > d->nd) {
        uint32_t two = v % 100;
        v /= 100;
        d->d[n] = (char)('0' + two % 10);
        d->d[n - 1] = (char)('0' + two / 10);
        n -= 2;
    }

    if (n == d->nd) {
        d->d[n] = (char)('0' + v);
    }

    d->nd = endindex + 1;
    d->dp = d->nd + trimmed;
}

// ryu_digits writes the shortest digits in [lower, upper] nearest to
// central, a number below 2^64, nine digits at a time.
static void ryu_digits(ryu_dec *d, uint64_t lower, uint64_t central, uint64_t upper, bool c0, bool cup) {
    uint32_t lhi = (uint32_t)(lower / 1000000000), llo = (uint32_t)(lower % 1000000000);
    uint32_t chi = (uint32_t)(central / 1000000000), clo = (uint32_t)(central % 1000000000);
    uint32_t uhi = (uint32_t)(upper / 1000000000), ulo = (uint32_t)(upper % 1000000000);
    if (uhi == 0) {
        ryu_digits32(d, llo, clo, ulo, c0, cup, 8);
    } else if (lhi < uhi) {
        if (llo != 0) {
            lhi++;
        }

        c0 = c0 && clo == 0;
        cup = clo > 500000000 || (clo == 500000000 && cup);
        ryu_digits32(d, lhi, chi, uhi, c0, cup, 8);
        d->dp += 9;
    } else {
        d->nd = 0;
        int n = 9;
        for (uint32_t v = chi; v > 0; v /= 10) {
            d->d[--n] = (char)('0' + v % 10);
        }

        d->d += n;
        d->nd = 9 - n;
        ryu_digits32(d, llo, clo, ulo, c0, cup, d->nd + 8);
    }

    while (d->nd > 0 && d->d[d->nd - 1] == '0') {
        d->nd--;
    }

    while (d->nd > 0 && d->d[0] == '0') {
        d->nd--;
        d->dp--;
        d->d++;
    }
}

// mult64_pow10 is m times 10^q in 32 significant bits, for a float32;
// exact says whether no bit was lost.
static uint32_t mult64_pow10(uint32_t m, int *e2, int q, bool *exact) {
    if (q == 0) {
        *exact = true;
        *e2 -= 6;
        return m << 6;
    }

    uint64_t pow = sn_pow10[q + 348][1];
    if (q < 0) {
        pow += 1; // an inverse power is rounded up
    }

    __uint128_t r = (__uint128_t)m * pow;
    uint64_t hi = (uint64_t)(r >> 64), lo = (uint64_t)r;
    *e2 += mul_log10_log2(q) - 63 + 57;
    *exact = (lo << 7) == 0;
    return (uint32_t)(hi << 7 | lo >> 57);
}

// mult128_pow10 is m times 10^q in 64 significant bits, for a double.
static uint64_t mult128_pow10(uint64_t m, int *e2, int q, bool *exact) {
    if (q == 0) { // 10^0 is 2^127 in the table's terms
        *exact = true;
        *e2 -= 8;
        return m << 8;
    }

    uint64_t p0 = sn_pow10[q + 348][0], p1 = sn_pow10[q + 348][1];
    if (q < 0) {
        p0 += 1;
    }

    // The 192-bit product's bits from 119 up: m is below 2^56, so 64 of
    // them hold it.
    *e2 += mul_log10_log2(q) - 127 + 119;
    __uint128_t l = (__uint128_t)m * p0, h = (__uint128_t)m * p1;
    uint64_t l1 = (uint64_t)(l >> 64), l0 = (uint64_t)l, h1 = (uint64_t)(h >> 64), h0 = (uint64_t)h;
    uint64_t mid = l1 + h0;
    h1 += mid < l1;
    *exact = (mid << 9) == 0 && l0 == 0;
    return h1 << 9 | mid >> 55;
}

static bool divisible_by_pow5(uint64_t m, int k) {
    if (m == 0) {
        return true;
    }

    for (int i = 0; i < k; i++) {
        if (m % 5 != 0) {
            return false;
        }

        m /= 5;
    }

    return true;
}

// ryu_shortest writes the shortest digits of mant*2^exp that read back
// as it, a float32 when f32 says so.
static void ryu_shortest(ryu_dec *d, uint64_t mant, int exp, bool f32) {
    int mantbits = f32 ? 23 : 52, bias = f32 ? -127 : -1023;
    d->d = d->buf;
    d->nd = 0;
    d->dp = 0;
    if (mant == 0) {
        return;
    }

    if (exp <= 0 && __builtin_ctzll(mant) >= -exp) { // an integer: no neighbour is closer
        mant >>= -exp;
        ryu_digits(d, mant, mant, mant, true, false);
        return;
    }

    uint64_t ml, mc, mu;
    int e2;
    if (mant != (uint64_t)1 << mantbits || exp == bias + 1 - mantbits) {
        ml = 2 * mant - 1;
        mc = 2 * mant;
        mu = 2 * mant + 1;
        e2 = exp - 1;
    } else { // the bottom of a binade: the interval below is half as wide
        ml = 4 * mant - 1;
        mc = 4 * mant;
        mu = 4 * mant + 2;
        e2 = exp - 2;
    }

    if (e2 == 0) {
        ryu_digits(d, ml, mc, mu, true, false);
        return;
    }

    int q = mul_log2_log10(-e2) + 1;
    uint64_t dl, dc, du;
    bool dl0, dc0, du0;
    int el = e2, ec = e2, eu = e2;
    if (f32) {
        dl = mult64_pow10((uint32_t)ml, &el, q, &dl0);
        dc = mult64_pow10((uint32_t)mc, &ec, q, &dc0);
        du = mult64_pow10((uint32_t)mu, &eu, q, &du0);
    } else {
        dl = mult128_pow10(ml, &el, q, &dl0);
        dc = mult128_pow10(mc, &ec, q, &dc0);
        du = mult128_pow10(mu, &eu, q, &du0);
    }

    e2 = eu;
    if (q > 55) { // a large power of ten is never exact
        dl0 = dc0 = du0 = false;
    }

    if (q < 0 && q >= -24) { // a division by a power of ten may be
        dl0 = dl0 || divisible_by_pow5(ml, -q);
        dc0 = dc0 || divisible_by_pow5(mc, -q);
        du0 = du0 || divisible_by_pow5(mu, -q);
    }

    unsigned extra = (unsigned)-e2;
    uint64_t mask = ((uint64_t)1 << extra) - 1, half = (uint64_t)1 << (extra - 1);
    uint64_t fracl = dl & mask, fracc = dc & mask, fracu = du & mask;
    dl >>= extra;
    dc >>= extra;
    du >>= extra;
    bool uok = !du0 || fracu > 0;
    if (du0 && fracu == 0) {
        uok = (mant & 1) == 0; // the upper bound reads back as the number only when its mantissa is even
    }

    if (!uok) {
        du--;
    }

    bool cup = dc0 ? (fracc > half || (fracc == half && (dc & 1) == 1)) : (fracc >> (extra - 1)) == 1;
    bool lok = dl0 && fracl == 0 && (mant & 1) == 0;
    if (!lok) {
        dl++;
    }

    ryu_digits(d, dl, dc, du, dc0 && fracc == 0, cup);
    d->dp -= q;
}

static void ryu_fixed(ryu_dec *d, uint64_t mant, int exp, int prec);

// decimal_of fills d with v's digits, v positive and finite: with prec
// at zero the fewest that read back as v, as a float32 when f32 says v
// is one, by Ryū; with prec above zero v rounded to prec significant
// digits, by the C library's correctly rounded %e.
static void decimal_of(double v, int prec, bool f32, decimal *d) {
    if (prec == 0) {
        ryu_dec r;
        if (f32) {
            float f = (float)v;
            uint32_t bits;
            memcpy(&bits, &f, 4);
            int exp = (int)(bits >> 23) & 0xFF;
            uint64_t mant = bits & 0x7FFFFF;
            if (exp == 0) {
                exp++;
            } else {
                mant |= (uint64_t)1 << 23;
            }

            ryu_shortest(&r, mant, exp - 127 - 23, true);
        } else {
            uint64_t bits;
            memcpy(&bits, &v, 8);
            int exp = (int)(bits >> 52) & 0x7FF;
            uint64_t mant = bits & (((uint64_t)1 << 52) - 1);
            if (exp == 0) {
                exp++;
            } else {
                mant |= (uint64_t)1 << 52;
            }

            ryu_shortest(&r, mant, exp - 1023 - 52, false);
        }

        d->d = sn_alloc((size_t)r.nd + 1);
        memcpy(d->d, r.d, (size_t)r.nd);
        d->nd = r.nd;
        d->dp = r.dp;
        return;
    }

    if (prec <= 17) { // Ryū's fixed digits, as Go's %e and %g with a precision
        uint64_t bits;
        memcpy(&bits, &v, 8);
        int exp = (int)(bits >> 52) & 0x7FF;
        uint64_t mant = bits & (((uint64_t)1 << 52) - 1);
        if (exp == 0) {
            exp++;
        } else {
            mant |= (uint64_t)1 << 52;
        }

        ryu_dec r;
        ryu_fixed(&r, mant, exp - 1023 - 52, prec);
        d->d = sn_alloc((size_t)r.nd + 1);
        memcpy(d->d, r.d, (size_t)r.nd);
        d->nd = r.nd;
        d->dp = r.dp;
        return;
    }

    size_t size = (size_t)(prec > 17 ? prec : 17) + 16;
    char *buf = sn_alloc(size);
    snprintf(buf, size, "%.*e", prec - 1, v);
    d->nd = 0;
    char *p = buf;
    for (; *p != 'e'; p++) {
        if (*p != '.') {
            buf[d->nd++] = *p;
        }
    }

    d->dp = atoi(p + 1) + 1;
    while (d->nd > 1 && buf[d->nd - 1] == '0') {
        d->nd--;
    }

    d->d = buf;
}

// The text being built: a block from sn_alloc and how much of it is
// written.
typedef struct {
    char *p;
    int64_t n;
} out_text;

static void out_byte(out_text *o, char c) { o->p[o->n++] = c; }

// put_e writes d as %e does, with prec digits after the point.
static void put_e(out_text *o, const decimal *d, int prec) {
    out_byte(o, d->d[0]);
    if (prec > 0) {
        out_byte(o, '.');
        for (int i = 1; i <= prec; i++) {
            out_byte(o, i < d->nd ? d->d[i] : '0');
        }
    }

    int exp = d->dp - 1;
    out_byte(o, 'e');
    out_byte(o, exp < 0 ? '-' : '+');
    exp = exp < 0 ? -exp : exp;
    o->n += snprintf(o->p + o->n, 8, "%02d", exp);
}

// put_f writes d as %f does, with prec digits after the point.
static void put_f(out_text *o, const decimal *d, int prec) {
    if (d->dp > 0) {
        for (int i = 0; i < d->dp; i++) {
            out_byte(o, i < d->nd ? d->d[i] : '0');
        }
    } else {
        out_byte(o, '0');
    }

    if (prec > 0) {
        out_byte(o, '.');
        for (int i = 0; i < prec; i++) {
            int j = d->dp + i;
            out_byte(o, j >= 0 && j < d->nd ? d->d[j] : '0');
        }
    }
}

// special writes the text of a float with no digits, an infinity or
// NaN, as Go spells them, or reports that v has digits.
static bool special(double v, sn_string *s) {
    if (v != v) {
        *s = SN_STR("NaN", 3);
    } else if (v > DBL_MAX) {
        *s = SN_STR("+Inf", 4);
    } else if (v < -DBL_MAX) {
        *s = SN_STR("-Inf", 4);
    } else {
        return false;
    }

    return true;
}

// format_g writes v as Go's %g does: shortest, the fewest digits that
// read back as v, as a float32 when f32 says so, or with prec
// significant digits; an exponent when the number's own is below -4 or
// at least the precision, which is 6 for the shortest form, so 1e+06
// and 123456 print as Go prints them.
static sn_string format_g(double v, int prec, bool f32) {
    sn_string s;
    if (special(v, &s)) {
        return s;
    }

    bool shortest = prec < 0;
    out_text o = {sn_alloc((size_t)(prec > 0 ? prec : 0) + 340), 0}; // a double's point is never 310 digits from its first
    if (signbit(v)) {
        out_byte(&o, '-');
        v = -v;
    }

    if (v == 0) {
        out_byte(&o, '0');
        return SN_OWNED(o.p, o.n);
    }

    decimal d;
    decimal_of(v, shortest ? 0 : (prec == 0 ? 1 : prec), f32, &d);
    if (shortest) {
        prec = d.nd;
    } else if (prec == 0) {
        prec = 1;
    }

    int eprec = prec;
    if (eprec > d.nd && d.nd >= d.dp) {
        eprec = d.nd;
    }

    if (shortest) {
        eprec = 6;
    }

    int exp = d.dp - 1;
    if (exp < -4 || exp >= eprec) {
        put_e(&o, &d, (prec > d.nd ? d.nd : prec) - 1);
    } else {
        int digits = prec > d.dp ? d.nd : prec;
        put_f(&o, &d, digits - d.dp > 0 ? digits - d.dp : 0);
    }

    sn_free(d.d);
    return SN_OWNED(o.p, o.n);
}

// sn_f64_to_string prints v as Go's %v and strconv.FormatFloat(v, 'g',
// -1, 64) do: the shortest text that reads back as v, 15 for 15.0,
// 1e+06 for a million, +Inf and NaN.
sn_string sn_f64_to_string(double v) {
    return format_g(v, -1, false);
}

// sn_f32_to_string is sn_f64_to_string for an f32: the fewest digits
// that read back as the same f32, so 0.1 prints as 0.1.
sn_string sn_f32_to_string(float v) {
    return format_g((double)v, -1, true);
}

// sn_f64_g prints v with prec significant digits, as Go's %.3v and
// %.3g do on a float.
sn_string sn_f64_g(double v, int64_t prec) {
    return format_g(v, (int)prec, false);
}

sn_string sn_bool_to_string(bool v) {
    return v ? SN_STR("true", 4) : SN_STR("false", 5);
}

// Fixed-precision digits are Ryū's too, Go's ryuFtoaFixed64: the number
// scaled by a power of ten to an integer of the digits asked for, with
// one 128-bit multiply, and rounded half to even on the exact value,
// with what was cut off known to be zero or not. Go reaches it for %e
// and %g; %f takes the same path here, the number of digits being the
// number's exact magnitude, read from seventeen digits, which tell any
// two doubles apart, plus the digits after the point.
static const uint64_t pow10_u64[19] = {
    1u, 10u, 100u, 1000u, 10000u, 100000u, 1000000u, 10000000u, 100000000u, 1000000000u,
    10000000000u, 100000000000u, 1000000000000u, 10000000000000u, 100000000000000u,
    1000000000000000u, 10000000000000000u, 100000000000000000u, 1000000000000000000u,
};

// mult128_top is m times 10^q in the top 64 bits of the 192-bit
// product, for an m of 64 bits, its top bit set: bits 128 up, which 64
// bits always hold, and of which at least 62 are significant.
static uint64_t mult128_top(uint64_t m, int *e2, int q, bool *exact) {
    if (q == 0) { // 10^0 is 2^127 in the table's terms
        *exact = (m & 1) == 0;
        *e2 += 1;
        return m >> 1;
    }

    uint64_t p0 = sn_pow10[q + 348][0], p1 = sn_pow10[q + 348][1];
    if (q < 0) {
        p0 += 1;
    }

    *e2 += mul_log10_log2(q) + 1;
    __uint128_t l = (__uint128_t)m * p0, h = (__uint128_t)m * p1;
    uint64_t l1 = (uint64_t)(l >> 64), l0 = (uint64_t)l, h1 = (uint64_t)(h >> 64), h0 = (uint64_t)h;
    uint64_t mid = l1 + h0;
    h1 += mid < l1;
    *exact = mid == 0 && l0 == 0;
    return h1;
}

// format_decimal writes prec digits of m, rounding what is cut off: Go's
// formatDecimal. trunc says m is already short of the number.
static void format_decimal(ryu_dec *d, uint64_t m, bool trunc, bool round_up, int prec) {
    uint64_t most = pow10_u64[prec];
    int trimmed = 0;
    while (m >= most) {
        uint64_t b = m % 10;
        m /= 10;
        trimmed++;
        if (b > 5) {
            round_up = true;
        } else if (b < 5) {
            round_up = false;
        } else {
            round_up = trunc || (m & 1) == 1; // a tie goes to the even digit
        }

        if (b != 0) {
            trunc = true;
        }
    }

    if (round_up) {
        m++;
    }

    if (m >= most) { // 999…9 rounded up
        m /= 10;
        trimmed++;
    }

    d->d = d->buf;
    d->nd = prec;
    int n = prec;
    uint64_t v = m;
    while (v >= 100) {
        uint64_t two = v % 100;
        v /= 100;
        n -= 2;
        d->d[n + 1] = (char)('0' + two % 10);
        d->d[n] = (char)('0' + two / 10);
    }

    if (v > 0) {
        d->d[--n] = (char)('0' + v % 10);
    }

    if (v >= 10) {
        d->d[--n] = (char)('0' + v / 10);
    }

    while (d->nd > 0 && d->d[d->nd - 1] == '0') {
        d->nd--;
        trimmed++;
    }

    d->dp = d->nd + trimmed;
}

// ryu_fixed writes the prec significant digits of mant*2^exp, rounded,
// prec from 1 to 18.
static void ryu_fixed(ryu_dec *d, uint64_t mant, int exp, int prec) {
    int b = 64 - __builtin_clzll(mant);
    int e2 = exp;
    if (b < 64) {
        mant <<= 64 - b;
        e2 += b - 64;
    }

    int q = -mul_log2_log10(e2 + 63) + prec - 1;
    bool exact = q <= 55 && q >= 0, d0;
    uint64_t di = mult128_top(mant, &e2, q, &d0);
    if (q < 0 && q >= -22 && divisible_by_pow5(mant, -q)) {
        exact = true;
        d0 = true;
    }

    unsigned extra = (unsigned)-e2;
    uint64_t mask = ((uint64_t)1 << extra) - 1, half = (uint64_t)1 << (extra - 1);
    uint64_t frac = di & mask;
    di >>= extra;
    bool round_up = exact ? (frac > half || (frac == half && !d0) || (frac == half && d0 && (di & 1) == 1)) : (frac >> (extra - 1)) == 1;
    if (frac != 0) {
        d0 = false;
    }

    format_decimal(d, di, !d0, round_up, prec);
    d->dp -= q;
}

// fixed_digits writes v, not zero, with prec digits after the point as
// %f does, and reports false when the digits needed are more than
// seventeen or none, which the C library's correctly rounded printf
// writes instead.
static bool fixed_digits(double v, int prec, sn_string *out) {
    bool neg = signbit(v);
    double a = neg ? -v : v;
    uint64_t bits;
    memcpy(&bits, &a, 8);
    int exp = (int)(bits >> 52) & 0x7FF;
    uint64_t mant = bits & (((uint64_t)1 << 52) - 1);
    if (exp == 0) {
        exp++;
    } else {
        mant |= (uint64_t)1 << 52;
    }

    exp -= 1023 + 52;
    ryu_dec d;
    ryu_fixed(&d, mant, exp, 17); // the exact magnitude
    int digits = d.dp + prec;
    if (digits < 1 || digits > 17) {
        return false;
    }

    ryu_fixed(&d, mant, exp, digits);
    int whole = d.dp > 0 ? d.dp : 1;
    char *p = sn_alloc((size_t)(neg + whole + 1 + prec));
    int64_t n = 0;
    if (neg) {
        p[n++] = '-';
    }

    if (d.dp > 0) {
        for (int i = 0; i < d.dp; i++) {
            p[n++] = i < d.nd ? d.d[i] : '0';
        }
    } else {
        p[n++] = '0';
    }

    if (prec > 0) {
        p[n++] = '.';
        for (int i = 0; i < prec; i++) {
            int j = d.dp + i;
            p[n++] = j >= 0 && j < d.nd ? d.d[j] : '0';
        }
    }

    *out = SN_OWNED(p, n);
    return true;
}

// sn_f64_fixed prints v with prec digits after the point, as %.2f
// does, however many that takes, and an infinity or NaN as Go spells
// it.
sn_string sn_f64_fixed(double v, int prec) {
    sn_string s;
    if (special(v, &s)) {
        return s;
    }

    if (v != 0 && prec >= 0 && prec <= 17 && fixed_digits(v, prec, &s)) {
        return s;
    }

    int n = snprintf(NULL, 0, "%.*f", prec, v);
    char *p = sn_alloc((size_t)n + 1);
    snprintf(p, (size_t)n + 1, "%.*f", prec, v);
    return SN_OWNED(p, n);
}

// sn_int_digits gives the decimal text of an integer, which it takes,
// at least prec digits long, zeros after the sign making up the rest,
// as Go's %.3d does; a precision of zero prints zero as nothing.
sn_string sn_int_digits(sn_string s, int64_t prec) {
    sn_check_string(s);
    int64_t sign = s.len > 0 && s.ptr[0] == '-' ? 1 : 0;
    int64_t digits = s.len - sign;
    if (prec == 0 && digits == 1 && s.ptr[sign] == '0') {
        sn_string_free(s);
        return SN_STR("", 0);
    }

    if (digits >= prec) {
        return s;
    }

    int64_t n = sign + prec;
    char *p = sn_alloc((size_t)n);
    memcpy(p, s.ptr, (size_t)sign);
    memset(p + sign, '0', (size_t)(prec - digits));
    memcpy(p + sign + prec - digits, s.ptr + sign, (size_t)digits);
    sn_string_free(s);
    return SN_OWNED(p, n);
}

// sn_truncate gives the first prec characters of s, a character being
// a UTF-8 sequence, as Go's %.2s does; s is dropped when the caller
// owned it, and the result is owned then too.
sn_string sn_truncate(sn_string s, int64_t prec, bool owned) {
    sn_check_string(s);
    int64_t i = 0;
    for (int64_t chars = 0; i < s.len && chars < prec; chars++) {
        int32_t r;
        i += sn_utf8_decode(s.ptr + i, s.len - i, &r);
    }

    if (!owned) {
        return (sn_string){s.ptr, i, 0};
    }

    char *p = sn_alloc((size_t)(i > 0 ? i : 1));
    memcpy(p, s.ptr, (size_t)i);
    sn_string_free(s);
    return SN_OWNED(p, i);
}

// sn_pad widens s to width characters with spaces, before it or after
// it when left says so, as a width in a format does; a character is a
// UTF-8 sequence, counted by its first byte. s is dropped when the
// caller owned it, and the result is owned whatever s was: the emitted
// code frees it, so a string already as wide comes back as it is only
// when it was owned, and as a copy when it was lent, a variable or a
// field whose bytes the caller keeps.
sn_string sn_pad(sn_string s, int64_t width, bool left, bool owned) {
    sn_check_string(s);
    int64_t chars = 0; // runes as Go counts them: an invalid byte is one
    for (int64_t i = 0; i < s.len; chars++) {
        int32_t r;
        i += sn_utf8_decode(s.ptr + i, s.len - i, &r);
    }

    if (chars >= width) {
        return owned ? s : sn_string_clone(s);
    }

    int64_t fill = width - chars;
    uint8_t *p = sn_alloc((size_t)(s.len + fill));
    memset(p, ' ', (size_t)(s.len + fill));
    memcpy(p + (left ? 0 : fill), s.ptr, (size_t)s.len);
    width = s.len + fill;
    if (owned) {
        sn_string_free(s);
    }

    return SN_OWNED(p, width);
}
