// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Reading a float as Go's strconv.ParseFloat reads one, in Go's grammar
// and by Go's steps: the digits read into an integer of at most
// nineteen and a power of ten; a number both of whose parts a float
// holds exactly, by one multiplication or division; any other by
// unrounded scaling (Russ Cox, "Floating-Point Printing and Parsing
// Can Be Simple And Fast"), one 128-bit multiplication by the power of
// ten from the table floats are printed with, whose result keeps
// whether anything was cut off, so that it rounds as the exact value
// does; and a number of more digits than the integer holds, when the
// digits left out could change the rounding, by the exact decimal
// arithmetic at the end of this file. Nothing is allocated, and
// nothing is asked of the C library, whose strtod reads by the
// program's locale.

#include "internal.h"

#include <string.h>

extern const uint64_t sn_pow10[696][2];

static uint8_t lower(uint8_t c) { return c | 0x20; }

// A float type's layout: the bits of its mantissa and of its exponent,
// and the exponent's bias.
typedef struct {
    int mantbits;
    int expbits;
    int bias;
} float_info;

static const float_info info32 = {23, 8, -127};
static const float_info info64 = {52, 11, -1023};

static double f64_of(uint64_t bits) {
    double f;
    memcpy(&f, &bits, 8);
    return f;
}

static float f32_of(uint32_t bits) {
    float f;
    memcpy(&f, &bits, 4);
    return f;
}

// fold reports whether the n bytes at p are word, in any case.
static bool fold(const uint8_t *p, int64_t n, const char *word) {
    if ((size_t)n != strlen(word)) {
        return false;
    }

    for (int64_t i = 0; i < n; i++) {
        if (lower(p[i]) != word[i]) {
            return false;
        }
    }

    return true;
}

// special reads an infinity or NaN: inf or infinity in any case, with a
// sign or none, and nan in any case without one. It reports whether s
// is one, and writes it.
static bool special(sn_string s, double *out) {
    const uint8_t *p = s.ptr;
    int64_t i = s.len > 0 && (p[0] == '+' || p[0] == '-') ? 1 : 0;
    if (fold(p + i, s.len - i, "inf") || fold(p + i, s.len - i, "infinity")) {
        *out = f64_of((uint64_t)(i == 1 && p[0] == '-') << 63 | (uint64_t)0x7FF << 52);
        return true;
    }

    if (fold(p, s.len, "nan")) {
        *out = f64_of(0x7FF8000000000001);
        return true;
    }

    return false;
}

// underscores_ok is Go's rule for the underscores in a number: each
// stands between two digits, a base prefix counting as a digit.
static bool underscores_ok(const uint8_t *p, int64_t n) {
    char saw = '^'; // the start, a 0 for a digit, an _, or ! for anything else
    if (n >= 1 && (p[0] == '+' || p[0] == '-')) {
        p++;
        n--;
    }

    int64_t i = 0;
    bool hex = false;
    if (n >= 2 && p[0] == '0' && (lower(p[1]) == 'b' || lower(p[1]) == 'o' || lower(p[1]) == 'x')) {
        i = 2;
        saw = '0';
        hex = lower(p[1]) == 'x';
    }

    for (; i < n; i++) {
        if (('0' <= p[i] && p[i] <= '9') || (hex && 'a' <= lower(p[i]) && lower(p[i]) <= 'f')) {
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

// A number as read: its digits as an integer, of at most nineteen
// decimal or sixteen hexadecimal ones, and the power of ten, or of two
// for a hexadecimal number, that scales it; trunc says digits that
// were not zero did not fit.
typedef struct {
    uint64_t mant;
    int64_t exp;
    bool neg;
    bool trunc;
    bool hex;
} number;

// read_float reads s, the whole of it, into x: a sign or none, decimal
// digits with a point or not and an exponent or not, or 0x and
// hexadecimal digits with the p exponent Go asks of them. It reports
// whether s is that.
static bool read_float(sn_string s, number *x) {
    const uint8_t *p = s.ptr;
    int64_t i = 0;
    if (s.len == 0) {
        return false;
    }

    if (p[0] == '+') {
        i++;
    } else if (p[0] == '-') {
        i++;
        x->neg = true;
    }

    uint64_t base = 10;
    int most = 19; // 10^19 fits in 64 bits
    uint8_t exp_char = 'e';
    if (i + 2 < s.len && p[i] == '0' && lower(p[i + 1]) == 'x') {
        base = 16;
        most = 16; // and so does 16^16
        i += 2;
        exp_char = 'p';
        x->hex = true;
    }

    bool underscores = false, sawdot = false, sawdigits = false;
    int64_t nd = 0, dp = 0; // the digits read past the leading zeros, and where the point stands among them
    int kept = 0;           // the digits in mant
    for (; i < s.len; i++) {
        uint8_t c = p[i];
        if (c == '_') {
            underscores = true;
        } else if (c == '.') {
            if (sawdot) {
                break;
            }

            sawdot = true;
            dp = nd;
        } else if ('0' <= c && c <= '9') {
            sawdigits = true;
            if (c == '0' && nd == 0) { // a leading zero
                dp--;
                continue;
            }

            nd++;
            if (kept < most) {
                x->mant = x->mant * base + (uint64_t)(c - '0');
                kept++;
            } else if (c != '0') {
                x->trunc = true;
            }
        } else if (base == 16 && 'a' <= lower(c) && lower(c) <= 'f') {
            sawdigits = true;
            nd++;
            if (kept < most) {
                x->mant = x->mant * 16 + (uint64_t)(lower(c) - 'a' + 10);
                kept++;
            } else {
                x->trunc = true;
            }
        } else {
            break;
        }
    }

    if (!sawdigits) {
        return false;
    }

    if (!sawdot) {
        dp = nd;
    }

    if (base == 16) {
        dp *= 4;
        kept *= 4;
    }

    if (i < s.len && lower(p[i]) == exp_char) {
        i++;
        if (i >= s.len) {
            return false;
        }

        int esign = 1;
        if (p[i] == '+') {
            i++;
        } else if (p[i] == '-') {
            i++;
            esign = -1;
        }

        if (i >= s.len || p[i] < '0' || p[i] > '9') {
            return false;
        }

        int e = 0; // an exponent past any float's is as good as any other past it
        for (; i < s.len && (('0' <= p[i] && p[i] <= '9') || p[i] == '_'); i++) {
            if (p[i] == '_') {
                underscores = true;
            } else if (e < 10000) {
                e = e * 10 + (p[i] - '0');
            }
        }

        dp += e * esign;
    } else if (base == 16) {
        return false; // a hexadecimal float has its exponent
    }

    if (x->mant != 0) {
        x->exp = dp - kept;
    }

    return i == s.len && (!underscores || underscores_ok(p, s.len));
}

// exact64 gives mant * 10^exp when a double computes it exactly, or
// correctly rounded by one operation: the integer itself, the integer
// times a power of ten a double holds, or divided by one.
static const double pow10_f64[] = {1e0,  1e1,  1e2,  1e3,  1e4,  1e5,  1e6,  1e7,  1e8,  1e9,  1e10, 1e11,
                                   1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18, 1e19, 1e20, 1e21, 1e22};

static bool exact64(uint64_t mant, int exp, bool neg, double *out) {
    if (mant >> 52 != 0) {
        return false;
    }

    double f = (double)mant;
    if (neg) {
        f = -f;
    }

    if (exp == 0) {
        *out = f;
        return true;
    }

    if (exp > 0 && exp <= 15 + 22) {
        if (exp > 22) { // zeros moved into the integer
            f *= pow10_f64[exp - 22];
            exp = 22;
        }

        if (f > 1e15 || f < -1e15) {
            return false;
        }

        *out = f * pow10_f64[exp];
        return true;
    }

    if (exp < 0 && exp >= -22) {
        *out = f / pow10_f64[-exp];
        return true;
    }

    return false;
}

static const float pow10_f32[] = {1e0f, 1e1f, 1e2f, 1e3f, 1e4f, 1e5f, 1e6f, 1e7f, 1e8f, 1e9f, 1e10f};

static bool exact32(uint64_t mant, int exp, bool neg, float *out) {
    if (mant >> 23 != 0) {
        return false;
    }

    float f = (float)mant;
    if (neg) {
        f = -f;
    }

    if (exp == 0) {
        *out = f;
        return true;
    }

    if (exp > 0 && exp <= 7 + 10) {
        if (exp > 10) {
            f *= pow10_f32[exp - 10];
            exp = 10;
        }

        if (f > 1e7f || f < -1e7f) {
            return false;
        }

        *out = f * pow10_f32[exp];
        return true;
    }

    if (exp < 0 && exp >= -10) {
        *out = f / pow10_f32[-exp];
        return true;
    }

    return false;
}

// An unrounded number is a value times four with its last bit set when
// anything below it was cut off: enough to round the value any way, as
// if from the exact one.
static uint64_t rounded(uint64_t u) { return (u + 1 + ((u >> 2) & 1)) >> 2; } // to nearest, a tie to even
static uint64_t unmin(uint64_t x) { return (x << 2) - 2; }                    // the least unrounded number that rounds to x

static int log2_pow10(int x) { return (x * 108853) >> 15; } // floor(x * log2(10))

// A scaler is the constants of scaling by 2^e * 10^p: the power of
// ten's 128 bits rounded up, as hi * 2^64 - lo, and the shift that
// takes the product to its place.
typedef struct {
    uint64_t hi;
    uint64_t lo;
    int s;
} scaler;

// prescale fills c for e and p, lp being log2_pow10(p). The table holds
// each power's 128 bits rounded down, the low word first; a power from
// 10^0 to 10^55 is exact in them, and any other is one more.
static void prescale(scaler *c, int e, int p, int lp) {
    uint64_t lo = sn_pow10[p + 348][0], hi = sn_pow10[p + 348][1];
    if (p < 0 || p > 55) {
        lo++;
        hi += lo == 0;
    }

    if (lo != 0) {
        hi++;
        lo = 0 - lo;
    }

    c->hi = hi;
    c->lo = lo;
    c->s = -(e + lp + 3);
}

// uscale gives x * 2^e * 10^p, unrounded, for the scaler of e and p; x
// has its top bit set, and c->s is below 64.
static uint64_t uscale(uint64_t x, const scaler *c) {
    unsigned __int128 m = (unsigned __int128)x * c->hi;
    uint64_t hi = (uint64_t)(m >> 64), mid = (uint64_t)m;
    int s = c->s & 63;
    if (hi >> s << s != hi) {
        return hi >> s | 1;
    }

    uint64_t mid2 = (uint64_t)(((unsigned __int128)x * c->lo) >> 64);
    hi -= mid < mid2;
    return hi >> s | (mid - mid2 > 1);
}

// scaled64 rounds d * 10^p, d not zero, to the nearest double, with
// sign as its sign bit; range is set when that is an infinity.
static double scaled64(uint64_t d, int p, uint64_t sign, bool *range) {
    int b = (int)sn_bits_Len64(d);
    int lp = log2_pow10(p);
    int e = 53 - b - lp;
    if (e > 1074) { // a subnormal has fewer bits
        e = 1074;
    }

    scaler pre;
    prescale(&pre, e - (64 - b), p, lp);
    if (pre.s >= 64) {
        return f64_of(sign);
    }

    uint64_t u = uscale(d << (64 - b), &pre);
    int s = u >= unmin((uint64_t)1 << 53); // one bit too many
    u = u >> s | (u & 1);
    e -= s;

    uint64_t m = sign | rounded(u);
    e = -e;
    if ((m & ((uint64_t)1 << 52)) == 0) {
        return f64_of(m); // a subnormal, or zero
    }

    if (e >= 0x7FF - 1075) {
        *range = true;
        return f64_of(sign | (uint64_t)0x7FF << 52);
    }

    return f64_of((m & ~((uint64_t)1 << 52)) | (uint64_t)(1075 + e) << 52);
}

// scaled32 is scaled64 for a float.
static float scaled32(uint64_t d, int p, uint32_t sign, bool *range) {
    int b = (int)sn_bits_Len64(d);
    int lp = log2_pow10(p);
    int e = 24 - b - lp;
    if (e > 149) {
        e = 149;
    }

    scaler pre;
    prescale(&pre, e - (64 - b), p, lp);
    if (pre.s >= 64) {
        return f32_of(sign);
    }

    uint64_t u = uscale(d << (64 - b), &pre);
    int s = u >= unmin((uint64_t)1 << 24);
    u = u >> s | (u & 1);
    e -= s;

    uint32_t m = sign | (uint32_t)rounded(u);
    e = -e;
    if ((m & ((uint32_t)1 << 23)) == 0) {
        return f32_of(m);
    }

    if (e >= 0xFF - 150) {
        *range = true;
        return f32_of(sign | (uint32_t)0xFF << 23);
    }

    return f32_of((m & ~((uint32_t)1 << 23)) | (uint32_t)(150 + e) << 23);
}

// hex_bits rounds the hexadecimal float mant * 2^exp to the bits of the
// type flt lays out, a tie to even; trunc says bits that were not zero
// are missing from mant, and range is set when the result is an
// infinity.
static uint64_t hex_bits(const float_info *flt, uint64_t mant, int64_t exp, bool neg, bool trunc, bool *range) {
    int64_t max_exp = ((int64_t)1 << flt->expbits) + flt->bias - 2;
    int64_t min_exp = flt->bias + 1;
    exp += flt->mantbits; // mant is now a fraction of 2^mantbits

    // A leading one, the mantissa's bits after it, and two more to
    // round by, the last standing for every bit below it.
    while (mant != 0 && mant >> (flt->mantbits + 2) == 0) {
        mant <<= 1;
        exp--;
    }

    if (trunc) {
        mant |= 1;
    }

    while (mant >> (1 + flt->mantbits + 2) != 0) {
        mant = mant >> 1 | (mant & 1);
        exp++;
    }

    while (mant > 1 && exp < min_exp - 2) { // too small: a subnormal, if anything
        mant = mant >> 1 | (mant & 1);
        exp++;
    }

    uint64_t round = mant & 3;
    mant >>= 2;
    round |= mant & 1; // a tie goes up when the mantissa is odd
    exp += 2;
    if (round == 3) {
        mant++;
        if (mant == (uint64_t)1 << (1 + flt->mantbits)) {
            mant >>= 1;
            exp++;
        }
    }

    if (mant >> flt->mantbits == 0) { // a subnormal, or zero
        exp = flt->bias;
    }

    if (exp > max_exp) {
        mant = (uint64_t)1 << flt->mantbits;
        exp = max_exp + 1;
        *range = true;
    }

    uint64_t bits = mant & (((uint64_t)1 << flt->mantbits) - 1);
    bits |= (uint64_t)((exp - flt->bias) & (((int64_t)1 << flt->expbits) - 1)) << flt->mantbits;
    if (neg) {
        bits |= (uint64_t)1 << flt->mantbits << flt->expbits;
    }

    return bits;
}

static uint64_t exact_bits(sn_string s, const float_info *flt, bool *range);

// sn_strconv_parseFloat reads a floating-point number as Go's
// strconv.ParseFloat does, the whole text being the number, rounded to
// the nearest double, a tie to even. 1 is a text that is not a number,
// 2 a number past the largest double; one too small to hold rounds to
// zero, as a literal does.
int64_t sn_strconv_parseFloat(sn_string s, double *out) {
    sn_check_string(s);
    if (special(s, out)) {
        return 0;
    }

    number x = {0};
    if (!read_float(s, &x)) {
        return 1;
    }

    bool range = false;
    double f = 0;
    if (x.hex) {
        f = f64_of(hex_bits(&info64, x.mant, x.exp, x.neg, x.trunc, &range));
    } else if (x.mant == 0 || x.exp < -345) {
        f = f64_of((uint64_t)x.neg << 63);
    } else if (x.exp > 310) {
        range = true;
    } else if (x.trunc || !exact64(x.mant, (int)x.exp, x.neg, &f)) {
        uint64_t sign = (uint64_t)x.neg << 63;
        f = scaled64(x.mant, (int)x.exp, sign, &range);
        if (x.trunc) { // the digits left out matter only when one more in the last place reads as another double
            bool above = false;
            double next = scaled64(x.mant + 1, (int)x.exp, sign, &above);
            if (f != next) {
                range = false;
                f = f64_of(exact_bits(s, &info64, &range));
            }
        }
    }

    if (range) {
        return 2;
    }

    *out = f;
    return 0;
}

// sn_strconv_parseFloat32 is sn_strconv_parseFloat for an f32: the
// text rounded once, to the nearest float, where reading a double and
// converting it rounds twice.
int64_t sn_strconv_parseFloat32(sn_string s, float *out) {
    sn_check_string(s);
    double wide;
    if (special(s, &wide)) {
        *out = (float)wide;
        return 0;
    }

    number x = {0};
    if (!read_float(s, &x)) {
        return 1;
    }

    bool range = false;
    float f = 0;
    if (x.hex) {
        f = f32_of((uint32_t)hex_bits(&info32, x.mant, x.exp, x.neg, x.trunc, &range));
    } else if (x.mant == 0 || x.exp < -70) {
        f = f32_of((uint32_t)x.neg << 31);
    } else if (x.exp > 40) {
        range = true;
    } else if (x.trunc || !exact32(x.mant, (int)x.exp, x.neg, &f)) {
        uint32_t sign = (uint32_t)x.neg << 31;
        f = scaled32(x.mant, (int)x.exp, sign, &range);
        if (x.trunc) {
            bool above = false;
            float next = scaled32(x.mant + 1, (int)x.exp, sign, &above);
            if (f != next) {
                range = false;
                f = f32_of((uint32_t)exact_bits(s, &info32, &range));
            }
        }
    }

    if (range) {
        return 2;
    }

    *out = f;
    return 0;
}

// The exact reading, Go's: the number as decimal digits, as many as
// eight hundred, with a point among them, multiplied or divided by
// powers of two a digit at a time until it lies in [0.5, 1), which
// gives the exponent, then scaled to the mantissa's bits and rounded,
// what was cut off the digits known to be zero or not.

// decimal_set reads the text read_float accepted into b.
static void decimal_set(sn_decimal *b, sn_string s) {
    const uint8_t *p = s.ptr;
    int64_t i = 0;
    b->nd = 0;
    b->dp = 0;
    b->neg = false;
    b->trunc = false;
    if (p[0] == '+') {
        i++;
    } else if (p[0] == '-') {
        i++;
        b->neg = true;
    }

    bool sawdot = false;
    for (; i < s.len; i++) {
        if (p[i] == '_') {
            continue;
        }

        if (p[i] == '.') {
            sawdot = true;
            b->dp = b->nd;
            continue;
        }

        if (p[i] < '0' || p[i] > '9') {
            break;
        }

        if (p[i] == '0' && b->nd == 0) { // a leading zero
            b->dp--;
        } else if (b->nd < SN_DECIMAL_DIGITS) {
            b->d[b->nd++] = p[i];
        } else if (p[i] != '0') {
            b->trunc = true;
        }
    }

    if (!sawdot) {
        b->dp = b->nd;
    }

    if (i < s.len) { // the exponent moves the point
        i++;
        int esign = 1;
        if (p[i] == '+') {
            i++;
        } else if (p[i] == '-') {
            i++;
            esign = -1;
        }

        int e = 0;
        for (; i < s.len; i++) {
            if (p[i] != '_' && e < 10000) {
                e = e * 10 + (p[i] - '0');
            }
        }

        b->dp += e * esign;
    }
}

// rounded_integer gives a rounded to an integer.
static uint64_t rounded_integer(const sn_decimal *a) {
    if (a->dp > 20) {
        return UINT64_MAX;
    }

    uint64_t n = 0;
    int64_t i = 0;
    for (; i < a->dp && i < a->nd; i++) {
        n = n * 10 + (uint64_t)(a->d[i] - '0');
    }

    for (; i < a->dp; i++) {
        n *= 10;
    }

    return n + sn_decimal_rounds_up(a, a->dp);
}

// exact_bits reads s, a decimal number read_float accepted, to the bits
// of the type flt lays out, rounded from every digit of it; range is
// set when the result is an infinity.
static uint64_t exact_bits(sn_string s, const float_info *flt, bool *range) {
    static const int powtab[] = {1, 3, 6, 9, 13, 16, 19, 23, 26}; // the power of two a power of ten is at least
    sn_decimal d;
    decimal_set(&d, s);
    uint64_t mant = 0;
    int exp = flt->bias;
    bool over = false;
    if (d.nd == 0 || d.dp < -330) {
        // zero
    } else if (d.dp > 310) {
        over = true;
    } else {
        exp = 0;
        while (d.dp > 0) { // down to below one
            int n = d.dp >= 9 ? 27 : powtab[d.dp];
            sn_decimal_shift(&d, -n);
            exp += n;
        }

        while (d.dp < 0 || (d.dp == 0 && d.d[0] < '5')) { // up to a half
            int n = -d.dp >= 9 ? 27 : powtab[-d.dp];
            sn_decimal_shift(&d, n);
            exp -= n;
        }

        exp--; // the digits lie in [0.5, 1), a float's mantissa in [1, 2)
        if (exp < flt->bias + 1) { // below the least exponent: a subnormal
            int n = flt->bias + 1 - exp;
            sn_decimal_shift(&d, -n);
            exp += n;
        }

        if (exp - flt->bias >= (1 << flt->expbits) - 1) {
            over = true;
        } else {
            sn_decimal_shift(&d, 1 + flt->mantbits);
            mant = rounded_integer(&d);
            if (mant == (uint64_t)2 << flt->mantbits) { // rounding added a bit
                mant >>= 1;
                exp++;
                over = exp - flt->bias >= (1 << flt->expbits) - 1;
            }

            if (!over && (mant & ((uint64_t)1 << flt->mantbits)) == 0) {
                exp = flt->bias; // a subnormal
            }
        }
    }

    if (over) {
        mant = 0;
        exp = (1 << flt->expbits) - 1 + flt->bias;
        *range = true;
    }

    uint64_t bits = mant & (((uint64_t)1 << flt->mantbits) - 1);
    bits |= (uint64_t)((exp - flt->bias) & ((1 << flt->expbits) - 1)) << flt->mantbits;
    if (d.neg) {
        bits |= (uint64_t)1 << flt->mantbits << flt->expbits;
    }

    return bits;
}
