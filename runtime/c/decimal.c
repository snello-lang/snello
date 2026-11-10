// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// A number as decimal digits, as many as eight hundred, with a point
// among them: Go's strconv decimal, which multiplies and divides by
// powers of two a digit at a time and rounds to a number of digits.
// It is exact where a double's arithmetic is not, and slow, so it reads
// the floats whose rounding nineteen digits do not settle, parse.c, and
// writes the ones asked for with more digits than seventeen, format.c.
// A double has at most 767 digits that are not zero, so every one of
// its digits fits.

#include "internal.h"

// trim drops the zeros that end a.
static void trim(sn_decimal *a) {
    while (a->nd > 0 && a->d[a->nd - 1] == '0') {
        a->nd--;
    }

    if (a->nd == 0) {
        a->dp = 0;
    }
}

// sn_decimal_assign sets a to the integer v.
void sn_decimal_assign(sn_decimal *a, uint64_t v) {
    uint8_t buf[24];
    int n = 0;
    for (; v > 0; v /= 10) {
        buf[n++] = (uint8_t)('0' + v % 10);
    }

    a->nd = 0;
    a->neg = false;
    a->trunc = false;
    for (n--; n >= 0; n--) {
        a->d[a->nd++] = buf[n];
    }

    a->dp = a->nd;
    trim(a);
}

// right_shift divides a by 2^k, k at most 60.
static void right_shift(sn_decimal *a, unsigned k) {
    int r = 0, w = 0;
    uint64_t n = 0;
    for (; n >> k == 0; r++) {
        if (r >= a->nd) {
            if (n == 0) {
                a->nd = 0;
                return;
            }

            while (n >> k == 0) {
                n *= 10;
                r++;
            }

            break;
        }

        n = n * 10 + (uint64_t)(a->d[r] - '0');
    }

    a->dp -= r - 1;
    uint64_t mask = ((uint64_t)1 << k) - 1;
    for (; r < a->nd; r++) {
        uint64_t digit = n >> k;
        n &= mask;
        a->d[w++] = (uint8_t)(digit + '0');
        n = n * 10 + (uint64_t)(a->d[r] - '0');
    }

    while (n > 0) {
        uint64_t digit = n >> k;
        n &= mask;
        if (w < SN_DECIMAL_DIGITS) {
            a->d[w++] = (uint8_t)(digit + '0');
        } else if (digit > 0) {
            a->trunc = true;
        }

        n *= 10;
    }

    a->nd = w;
    trim(a);
}

// Multiplying by 2^k adds delta digits, or one fewer when the digits
// are below 5^k's, the cutoff: Go's table, which saves counting them.
static const struct {
    int delta;
    const char *cutoff;
} cheats[] = {
    {0, ""},
    {1, "5"},
    {1, "25"},
    {1, "125"},
    {2, "625"},
    {2, "3125"},
    {2, "15625"},
    {3, "78125"},
    {3, "390625"},
    {3, "1953125"},
    {4, "9765625"},
    {4, "48828125"},
    {4, "244140625"},
    {4, "1220703125"},
    {5, "6103515625"},
    {5, "30517578125"},
    {5, "152587890625"},
    {6, "762939453125"},
    {6, "3814697265625"},
    {6, "19073486328125"},
    {7, "95367431640625"},
    {7, "476837158203125"},
    {7, "2384185791015625"},
    {7, "11920928955078125"},
    {8, "59604644775390625"},
    {8, "298023223876953125"},
    {8, "1490116119384765625"},
    {9, "7450580596923828125"},
    {9, "37252902984619140625"},
    {9, "186264514923095703125"},
    {10, "931322574615478515625"},
    {10, "4656612873077392578125"},
    {10, "23283064365386962890625"},
    {10, "116415321826934814453125"},
    {11, "582076609134674072265625"},
    {11, "2910383045673370361328125"},
    {11, "14551915228366851806640625"},
    {12, "72759576141834259033203125"},
    {12, "363797880709171295166015625"},
    {12, "1818989403545856475830078125"},
    {13, "9094947017729282379150390625"},
    {13, "45474735088646411895751953125"},
    {13, "227373675443232059478759765625"},
    {13, "1136868377216160297393798828125"},
    {14, "5684341886080801486968994140625"},
    {14, "28421709430404007434844970703125"},
    {14, "142108547152020037174224853515625"},
    {15, "710542735760100185871124267578125"},
    {15, "3552713678800500929355621337890625"},
    {15, "17763568394002504646778106689453125"},
    {16, "88817841970012523233890533447265625"},
    {16, "444089209850062616169452667236328125"},
    {16, "2220446049250313080847263336181640625"},
    {16, "11102230246251565404236316680908203125"},
    {17, "55511151231257827021181583404541015625"},
    {17, "277555756156289135105907917022705078125"},
    {17, "1387778780781445675529539585113525390625"},
    {18, "6938893903907228377647697925567626953125"},
    {18, "34694469519536141888238489627838134765625"},
    {18, "173472347597680709441192448139190673828125"},
    {19, "867361737988403547205962240695953369140625"},
};

// prefix_less reports whether the n digits at b are below the digits s,
// digits missing from b counting as below any.
static bool prefix_less(const uint8_t *b, int n, const char *s) {
    for (int i = 0; s[i] != '\0'; i++) {
        if (i >= n) {
            return true;
        }

        if (b[i] != (uint8_t)s[i]) {
            return b[i] < (uint8_t)s[i];
        }
    }

    return false;
}

// left_shift multiplies a by 2^k, k at most 60.
static void left_shift(sn_decimal *a, unsigned k) {
    int delta = cheats[k].delta;
    if (prefix_less(a->d, a->nd, cheats[k].cutoff)) {
        delta--;
    }

    int r = a->nd, w = a->nd + delta;
    uint64_t n = 0;
    for (r--; r >= 0; r--) {
        n += (uint64_t)(a->d[r] - '0') << k;
        uint64_t quo = n / 10, rem = n - 10 * quo;
        w--;
        if (w < SN_DECIMAL_DIGITS) {
            a->d[w] = (uint8_t)(rem + '0');
        } else if (rem != 0) {
            a->trunc = true;
        }

        n = quo;
    }

    while (n > 0) {
        uint64_t quo = n / 10, rem = n - 10 * quo;
        w--;
        if (w < SN_DECIMAL_DIGITS) {
            a->d[w] = (uint8_t)(rem + '0');
        } else if (rem != 0) {
            a->trunc = true;
        }

        n = quo;
    }

    a->nd += delta;
    if (a->nd > SN_DECIMAL_DIGITS) {
        a->nd = SN_DECIMAL_DIGITS;
    }

    a->dp += delta;
    trim(a);
}

// sn_decimal_shift multiplies a by 2^k, or divides it when k is
// negative.
void sn_decimal_shift(sn_decimal *a, int k) {
    if (a->nd == 0) {
        return;
    }

    for (; k > 60; k -= 60) {
        left_shift(a, 60);
    }

    for (; k < -60; k += 60) {
        right_shift(a, 60);
    }

    if (k > 0) {
        left_shift(a, (unsigned)k);
    } else if (k < 0) {
        right_shift(a, (unsigned)-k);
    }
}

// sn_decimal_rounds_up reports whether a cut to nd digits rounds up: a
// tie goes to the even digit, unless digits that were not zero lie past
// the tie.
bool sn_decimal_rounds_up(const sn_decimal *a, int64_t nd) {
    if (nd < 0 || nd >= a->nd) {
        return false;
    }

    if (a->d[nd] == '5' && nd + 1 == a->nd) {
        return a->trunc || (nd > 0 && (a->d[nd - 1] - '0') % 2 != 0);
    }

    return a->d[nd] >= '5';
}

// sn_decimal_round cuts a to nd digits, rounded to nearest, a tie to
// the even digit; a already that short, or an nd below zero, is left as
// it is.
void sn_decimal_round(sn_decimal *a, int64_t nd) {
    if (nd < 0 || nd >= a->nd) {
        return;
    }

    if (!sn_decimal_rounds_up(a, nd)) {
        a->nd = (int)nd;
        trim(a);
        return;
    }

    for (int64_t i = nd - 1; i >= 0; i--) {
        if (a->d[i] < '9') { // the digits after it are dropped
            a->d[i]++;
            a->nd = (int)i + 1;
            return;
        }
    }

    a->d[0] = '1'; // every digit was a nine
    a->nd = 1;
    a->dp++;
}
