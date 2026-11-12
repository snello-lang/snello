// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The clocks, and time.Sleep.

#define _POSIX_C_SOURCE 200809L
#define _DARWIN_C_SOURCE

#include "internal.h"

#include <errno.h>
#include <string.h>
#include <time.h>

// The absolute epoch a moment's days are counted from, in years before
// year 0, and what the seconds since 1970 are shifted by to count from
// it.
#define ABSOLUTE_YEARS INT64_C(292277022400)
#define UNIX_TO_ABSOLUTE UINT64_C(9223372028741760000)

// sn_time_now reads the system's clock: nanoseconds since the start of
// 1970 in UTC.
int64_t sn_time_now(void) {
    struct timespec ts;
    clock_gettime(CLOCK_REALTIME, &ts);
    return (int64_t)ts.tv_sec * 1000000000 + ts.tv_nsec;
}

// sn_time_mono reads the monotonic clock, which the system never sets
// back: nanoseconds since a moment of its own, plus one, so that a
// reading is never 0, which a Time without one holds.
int64_t sn_time_mono(void) {
#if defined(__APPLE__)
    // Darwin's CLOCK_MONOTONIC is built from the boot time and the time
    // of day, at several times the cost; this one is the processor's
    // counter, which stands still while the machine sleeps, as Linux's
    // CLOCK_MONOTONIC does, and is the clock Go reads on Darwin.
    return (int64_t)clock_gettime_nsec_np(CLOCK_UPTIME_RAW) + 1;
#else
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000000000 + ts.tv_nsec + 1;
#endif
}

// sn_time_clock is package time's clock: the wall clock's seconds and
// nanoseconds, and the monotonic clock's reading.
void sn_time_clock(int64_t *sec, int64_t *nsec, int64_t *mono) {
    struct timespec ts;
    clock_gettime(CLOCK_REALTIME, &ts);
    *sec = (int64_t)ts.tv_sec;
    *nsec = ts.tv_nsec;
    *mono = sn_time_mono();
}

// sn_time_read is a Time of now, for the timer to put in a channel.
sn_time sn_time_read(void) {
    sn_time t;
    sn_time_clock(&t.sec, &t.nsec, &t.mono);
    return t;
}

// sn_time_Sleep parks the task for d nanoseconds; the worker runs
// others meanwhile.
void sn_time_Sleep(int64_t d) {
    sn_sleep(d);
}

// two_digits says which of a moment's two-digit numbers a directive's
// letter writes, counted from one, or 0 for a letter that writes none.
static const uint8_t two_digits[256] = {['m'] = 1, ['d'] = 2, ['H'] = 3, ['M'] = 4, ['S'] = 5, ['y'] = 6};

// Where a reading keeps each of its two-digit numbers, which directive
// reads each, counted from one, and the range each must be in.
enum { MONTH, DAY, HOUR, MINUTE, SECOND };
static const uint8_t two_fields[256] = {['m'] = 1, ['d'] = 2, ['H'] = 3, ['M'] = 4, ['S'] = 5};
static const int8_t least[5] = {1, 1, 0, 0, 0}, most[5] = {12, 31, 23, 59, 59};

// The ways a text is not what a layout says, which the library words.
enum {
    PARSE_OK,
    PARSE_LITERAL,   // the text at at is not the layout's byte at li
    PARSE_FIELD,     // the text at at is not what the directive at li reads
    PARSE_MONTH,     // the rest are a field out of its range
    PARSE_DAY,
    PARSE_HOUR,
    PARSE_MINUTE,
    PARSE_SECOND,
    PARSE_YEARDAY,
    PARSE_OFFSET,
    PARSE_UNREAD,    // the directive at li says no day or time
    PARSE_EXTRA,     // text is left at at
    PARSE_HALF,      // %I without %p
    PARSE_MISMATCH,  // the day of the year is not the date's
};

// The names a layout writes and reads, the days from Monday as
// ISO 8601 orders them.
static const char *const month_names[12] = {"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"};
static const char *const day_names[7] = {"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"};

static bool leap_year(int64_t year) {
    return year % 4 == 0 && (year % 100 != 0 || year % 400 == 0);
}

static int days_in(int month, int64_t year) {
    return month == 2 ? (leap_year(year) ? 29 : 28) : 30 + ((month + (month >> 3)) & 1);
}

// year_day is the day of the year of a date, from 1.
static int year_day(int64_t year, int month, int day) {
    int before = (214 * month - 211) / 7;
    if (month >= 3) {
        before -= leap_year(year) ? 1 : 2;
    }

    return before + day;
}

// A moment as a layout writes it: its date, its seconds since midnight
// and since 1970, and the nanoseconds past the second.
typedef struct {
    int64_t year;
    int month, day, clock;
    int64_t sec, nsec;
    int two[6]; // what the commonest directives write in two digits: %m, %d, %H, %M, %S, %y
} moment;

// moment_of is the moment of sec and nsec, its date found as Neri and
// Schneider find it, from the days since an absolute epoch, March 1 of
// a year far enough back that every second a Time holds is counted from
// it unsigned: with multiplications in place of divisions, 2939745 /
// 2^32 being a year in quarter-days and 2141 / 2^16 a month in days.
static void moment_of(moment *m, int64_t sec, int64_t nsec) {
    uint64_t abs = (uint64_t)sec + UNIX_TO_ABSOLUTE;
    uint64_t d = 4 * (abs / 86400) + 3;
    uint64_t century = d / 146097;
    uint64_t prod = UINT64_C(2939745) * ((uint32_t)(d % 146097) | 3);
    uint32_t ayday = (uint32_t)prod / 2939745 / 4;
    uint32_t md = 2141 * ayday + 197913;
    int jan_feb = ayday >= 306; // a day in January or February belongs to the next calendar year
    m->year = (int64_t)(century * 100) - ABSOLUTE_YEARS + (int64_t)(prod >> 32) + jan_feb;
    m->month = (int)(md >> 16) - jan_feb * 12;
    m->day = 1 + (int)((md & 0xFFFF) / 2141);
    m->clock = (int)(abs % 86400);
    m->sec = sec;
    m->nsec = nsec;
    m->two[0] = m->month;
    m->two[1] = m->day;
    m->two[2] = m->clock / 3600;
    m->two[3] = m->clock % 3600 / 60;
    m->two[4] = m->clock % 60;
    m->two[5] = (int)(((m->year % 100) + 100) % 100);
}

// week_day is the day of the week of a moment, 0 for Monday to 6: the
// first day of 1970 was a Thursday.
static int week_day(const moment *m) {
    int64_t days = (m->sec - m->clock) / 86400;
    return (int)(((days % 7) + 10) % 7);
}

// put_number writes v at p in at least width digits, with zeros before
// it, or spaces when flag is an underscore, or nothing when it is a
// dash, and returns where it ends.
static uint8_t *put_number(uint8_t *p, int64_t v, int width, uint8_t flag) {
    if (width == 2 && flag == 0 && v >= 0 && v < 100) { // an hour, a day: the commonest
        p[0] = (uint8_t)('0' + v / 10);
        p[1] = (uint8_t)('0' + v % 10);
        return p + 2;
    }

    uint64_t u = (uint64_t)v;
    if (v < 0) {
        *p++ = '-';
        u = 0 - u;
    }

    uint8_t digits[20];
    int n = 0;
    do {
        digits[n++] = (uint8_t)('0' + u % 10);
        u /= 10;
    } while (u != 0);

    if (flag != '-') {
        for (int k = n; k < width; k++) {
            *p++ = flag == '_' ? ' ' : '0';
        }
    }

    while (n > 0) {
        *p++ = digits[--n];
    }

    return p;
}

static uint8_t *put_text(uint8_t *p, const char *s, size_t n) {
    memcpy(p, s, n);
    return p + n;
}

// format_into appends a moment as layout says to out, which is made
// longer whenever what is left of it would not hold the rest of the
// layout's own bytes and the longest thing one directive writes. Where
// it writes is kept here and not in out, which is brought up to date
// only where out is made longer and at the end, so that a byte written
// costs a store and no more. A layout the compiler passed has no
// directive but those here.
static void format_into(sn_slice *out, sn_string layout, const moment *m) {
    const uint8_t *l = layout.ptr;
    int64_t n = layout.len;
    uint8_t *p = (uint8_t *)out->ptr + out->len;
    uint8_t *limit = (uint8_t *)out->ptr + out->cap;
    for (int64_t i = 0; i < n;) {
        if (limit - p < 48 + (n - i)) {
            out->len = p - (uint8_t *)out->ptr;
            sn_slice_grow(out, out->len + 48 + (n - i), 1);
            p = (uint8_t *)out->ptr + out->len;
            limit = (uint8_t *)out->ptr + out->cap;
        }

        while (i < n && l[i] != '%') { // the layout's own bytes
            *p++ = l[i++];
        }

        if (i == n) {
            break;
        }

        i++; // the percent sign
        if (i == n) {
            *p++ = '%';
            break;
        }

        // The commonest directives first, each two digits copied from
        // the pairs, and a year of four digits as two of them.
        uint8_t which = two_digits[l[i]];
        if (which != 0) {
            memcpy(p, sn_digit_pairs + 2 * m->two[which - 1], 2);
            p += 2;
            i++;
            continue;
        }

        if (l[i] == 'Y' && (uint64_t)m->year < 10000) {
            memcpy(p, sn_digit_pairs + 2 * (m->year / 100), 2);
            memcpy(p + 2, sn_digit_pairs + 2 * (m->year % 100), 2);
            p += 4;
            i++;
            continue;
        }

        if (l[i] == ':' && i + 1 < n && l[i + 1] == 'z') { // the zone of RFC 3339
            memcpy(p, "+00:00", 6);
            p += 6;
            i += 2;
            continue;
        }

        uint8_t flag = 0;
        if (l[i] == '-' || l[i] == '_') {
            flag = l[i++];
        }

        if (i < n && l[i] == '.') { // %.f, %.3f, %.6f, %.9f
            int digits = 0;
            if (i + 1 < n && l[i + 1] != 'f') {
                digits = l[i + 1] - '0';
                if (digits != 3 && digits != 6 && digits != 9) {
                    digits = 0; // the checker lets no other through; a layout that got past it writes no more than %.9f does
                }
                i++;
            }

            i += 2;
            int64_t ns = m->nsec;
            if (digits == 0 && ns != 0) { // as few of three, six or nine digits as hold it
                digits = ns % 1000000 == 0 ? 3 : ns % 1000 == 0 ? 6 : 9;
            }

            if (digits != 0) {
                *p++ = '.';
                int64_t scale = 100000000;
                for (int k = 0; k < digits; k++, scale /= 10) {
                    *p++ = (uint8_t)('0' + ns / scale % 10);
                }
            }

            continue;
        }

        bool colon = i < n && l[i] == ':';
        if (colon) {
            i++;
        }

        if (i == n) {
            break;
        }

        int hour = m->clock / 3600;
        switch (l[i++]) {
        case 'Y':
            p = put_number(p, m->year, 4, flag);
            break;
        case 'm':
            p = put_number(p, m->month, 2, flag);
            break;
        case 'd':
            p = put_number(p, m->day, 2, flag);
            break;
        case 'e':
            p = put_number(p, m->day, 2, flag == 0 ? '_' : flag);
            break;
        case 'H':
            p = put_number(p, hour, 2, flag);
            break;
        case 'M':
            p = put_number(p, m->clock % 3600 / 60, 2, flag);
            break;
        case 'S':
            p = put_number(p, m->clock % 60, 2, flag);
            break;
        case 'I':
            p = put_number(p, (hour + 11) % 12 + 1, 2, flag);
            break;
        case 'p':
            p = put_text(p, hour < 12 ? "AM" : "PM", 2);
            break;
        case 'P':
            p = put_text(p, hour < 12 ? "am" : "pm", 2);
            break;
        case 'f':
            p = put_number(p, m->nsec, 9, flag);
            break;
        case 'z':
            p = colon ? put_text(p, "+00:00", 6) : put_text(p, "+0000", 5);
            break;
        case 'Z':
            p = put_text(p, "UTC", 3);
            break;
        case 'B':
            p = put_text(p, month_names[m->month - 1], strlen(month_names[m->month - 1]));
            break;
        case 'b':
        case 'h':
            p = put_text(p, month_names[m->month - 1], 3);
            break;
        case 'A':
            p = put_text(p, day_names[week_day(m)], strlen(day_names[week_day(m)]));
            break;
        case 'a':
            p = put_text(p, day_names[week_day(m)], 3);
            break;
        case 'j':
            p = put_number(p, year_day(m->year, m->month, m->day), 3, flag);
            break;
        case 'y':
            p = put_number(p, ((m->year % 100) + 100) % 100, 2, flag);
            break;
        case 'C': { // a year before year 0 has a century as any other has
            int64_t in = ((m->year % 100) + 100) % 100;
            p = put_number(p, (m->year - in) / 100, 2, flag);
            break;
        }
        case 'u':
            p = put_number(p, week_day(m) + 1, 1, flag);
            break;
        case 'w':
            p = put_number(p, (week_day(m) + 1) % 7, 1, flag);
            break;
        case 'U':
            p = put_number(p, (year_day(m->year, m->month, m->day) + 6 - (week_day(m) + 1) % 7) / 7, 2, flag);
            break;
        case 'W':
            p = put_number(p, (year_day(m->year, m->month, m->day) + 6 - week_day(m)) / 7, 2, flag);
            break;
        case 'G':
        case 'g':
        case 'V': {
            // The ISO 8601 week is the one its Thursday is in, and its
            // year the year of that Thursday.
            int64_t year = m->year;
            int thursday = year_day(year, m->month, m->day) + 3 - week_day(m);
            if (thursday < 1) {
                year--;
                thursday += leap_year(year) ? 366 : 365;
            } else if (thursday > (leap_year(year) ? 366 : 365)) {
                thursday -= leap_year(year) ? 366 : 365;
                year++;
            }

            uint8_t d = l[i - 1];
            p = d == 'V' ? put_number(p, (thursday - 1) / 7 + 1, 2, flag) : d == 'G' ? put_number(p, year, 4, flag) : put_number(p, ((year % 100) + 100) % 100, 2, flag);
            break;
        }
        case 's':
            p = put_number(p, m->sec, 1, flag);
            break;
        case 'n':
            *p++ = '\n';
            break;
        case 't':
            *p++ = '\t';
            break;
        case 'F':
        case 'T':
        case 'X':
        case 'R':
        case 'D':
        case 'x':
        case 'r':
        case 'c': { // several at once: the layout each stands for
            uint8_t d = l[i - 1];
            sn_string part = d == 'F' ? SN_STR("%Y-%m-%d", 8) : d == 'R' ? SN_STR("%H:%M", 5) : (d == 'D' || d == 'x') ? SN_STR("%m/%d/%y", 8) : d == 'r' ? SN_STR("%I:%M:%S %p", 11) : d == 'c' ? SN_STR("%a %b %e %H:%M:%S %Y", 20) : SN_STR("%H:%M:%S", 8);
            out->len = p - (uint8_t *)out->ptr;
            format_into(out, part, m);
            p = (uint8_t *)out->ptr + out->len;
            limit = (uint8_t *)out->ptr + out->cap;
            break;
        }
        default:
            *p++ = l[i - 1]; // %%
        }
    }

    out->len = p - (uint8_t *)out->ptr;
}

// sn_time_format is package time's format: the moment of sec and nsec
// written as layout says, in UTC, in a string made once.
sn_string sn_time_format(sn_string layout, int64_t sec, int64_t nsec) {
    sn_check_string(layout);
    moment m;
    moment_of(&m, sec, nsec);
    if (layout.len == 20 && (uint64_t)m.year < 10000 && memcmp(layout.ptr, "%Y-%m-%dT%H:%M:%S%:z", 20) == 0) {
        // RFC 3339, the layout programs exchange times in: written in
        // place, with no layout to read.
        uint8_t *p = sn_alloc(25);
        memcpy(p, sn_digit_pairs + 2 * (m.year / 100), 2);
        memcpy(p + 2, sn_digit_pairs + 2 * (m.year % 100), 2);
        p[4] = '-';
        memcpy(p + 5, sn_digit_pairs + 2 * m.two[0], 2);
        p[7] = '-';
        memcpy(p + 8, sn_digit_pairs + 2 * m.two[1], 2);
        p[10] = 'T';
        memcpy(p + 11, sn_digit_pairs + 2 * m.two[2], 2);
        p[13] = ':';
        memcpy(p + 14, sn_digit_pairs + 2 * m.two[3], 2);
        p[16] = ':';
        memcpy(p + 17, sn_digit_pairs + 2 * m.two[4], 2);
        memcpy(p + 19, "+00:00", 6);
        return (sn_string){p, 25, 25};
    }

    sn_slice out = {0};
    sn_slice_grow(&out, layout.len + 48, 1);
    format_into(&out, layout, &m);
    if (out.len == 0) { // a string of no bytes owns none
        sn_slice_free(&out);
        return SN_STR("", 0);
    }

    return (sn_string){out.ptr, out.len, out.cap};
}

// What a layout read so far: the fields a directive gave, and the rest
// as parse_start leaves them.
typedef struct {
    int64_t year;
    int two[5];        // the month and the day, 0 for none, then the hour, the minute and the second
    int yday, weekday; // 0 for none; the weekday -1
    int half;          // 1 for AM, 2 for PM
    bool twelve;       // the hour is of a half day
    int64_t nsec, offset;
} reading;


// digits_at reads up to most digits of v from at into *out and returns
// how many there were.
static int digits_at(sn_string v, int64_t at, int most, int64_t *out) {
    int64_t x = 0;
    int n = 0;
    while (n < most && at + n < v.len && sn_is_digit(v.ptr[at + n])) {
        x = x * 10 + (v.ptr[at + n] - '0');
        n++;
    }

    *out = x;
    return n;
}

// same_word reports whether v has the n letters of word at at, their
// case apart.
static bool same_word(sn_string v, int64_t at, const char *word, int64_t n) {
    if (at + n > v.len) {
        return false;
    }

    for (int64_t k = 0; k < n; k++) {
        if ((v.ptr[at + k] | 0x20) != ((uint8_t)word[k] | 0x20)) {
            return false;
        }
    }

    return true;
}

// parse_into reads v from *at as layout says into r, and returns
// PARSE_OK with *at where it stopped, or what is wrong with *at where
// and *li the directive or the byte of the layout it was reading.
static int parse_into(sn_string layout, sn_string v, int64_t *at, int64_t *li, reading *r) {
    const uint8_t *l = layout.ptr;
    int64_t n = layout.len;
    int64_t a = *at;
    for (int64_t i = 0; i < n;) {
        *at = a;
        *li = i;
        uint8_t c = l[i++];
        if (c != '%' || i == n) {
            if (a >= v.len || v.ptr[a] != c) {
                return PARSE_LITERAL;
            }

            a++;
            continue;
        }

        // The commonest directives first: two digits, read where they
        // lie, and held to the field's range.
        *at = a;
        uint8_t which = two_fields[l[i]];
        if (which != 0) {
            *li = i++;
            if (a + 2 > v.len) {
                return PARSE_FIELD;
            }

            unsigned tens = (unsigned)v.ptr[a] - '0', ones = (unsigned)v.ptr[a + 1] - '0';
            if (tens > 9 || ones > 9) {
                return PARSE_FIELD;
            }

            int x = (int)(tens * 10 + ones);
            if (x < least[which - 1] || x > most[which - 1]) {
                return PARSE_MONTH + which - 1;
            }

            r->two[which - 1] = x;
            a += 2;
            continue;
        }

        uint8_t flag = 0;
        if (l[i] == '-' || l[i] == '_') {
            flag = l[i++];
        }

        if (i < n && l[i] == '.') { // %.f, %.3f, %.6f, %.9f
            int digits = 0;
            if (i + 1 < n && l[i + 1] != 'f') {
                digits = l[i + 1] - '0';
                if (digits != 3 && digits != 6 && digits != 9) {
                    digits = 0; // as format_into takes it
                }
                i++;
            }

            i += 2;
            if (a < v.len && v.ptr[a] == '.') {
                int64_t x;
                int count = digits_at(v, a + 1, 9, &x);
                if (count == 0 || (digits != 0 && count != digits) || (a + 1 + count < v.len && sn_is_digit(v.ptr[a + 1 + count]))) {
                    return PARSE_FIELD;
                }

                for (int k = count; k < 9; k++) {
                    x *= 10;
                }

                r->nsec = x;
                a += 1 + count;
            } else if (digits != 0) {
                return PARSE_FIELD;
            }

            continue;
        }

        bool colon = i < n && l[i] == ':';
        if (colon) {
            i++;
        }

        if (i == n) {
            break;
        }

        *li = i;
        uint8_t d = l[i++];
        int64_t x = 0;
        int width = d == 'Y' ? 4 : d == 'j' ? 3 : d == 'f' ? 9 : 2;
        switch (d) {
        case 'Y':
        case 'm':
        case 'd':
        case 'e':
        case 'H':
        case 'M':
        case 'S':
        case 'I':
        case 'j':
        case 'y':
        case 'f': {
            // A number: of its width, or of one digit to its width after
            // a dash, or with spaces for its first zeros after an
            // underscore, which %e has of itself. A year is a sign and
            // four digits or more, as %Y writes it, up to the twelve a
            // year can have.
            if (d == 'Y' && flag == 0) {
                bool negative = a < v.len && v.ptr[a] == '-';
                if (negative) {
                    a++;
                }

                int count = digits_at(v, a, 12, &x);
                if (count < 4) {
                    return PARSE_FIELD;
                }

                if (negative) {
                    x = -x;
                }

                a += count;
            } else if (flag == 0 && d != 'e') { // of its width, as most are: read where it lies
                if (a + width > v.len) {
                    return PARSE_FIELD;
                }

                for (int k = 0; k < width; k++) {
                    uint8_t digit = (uint8_t)(v.ptr[a + k] - '0');
                    if (digit > 9) {
                        return PARSE_FIELD;
                    }

                    x = x * 10 + digit;
                }

                a += width;
            } else {
                int64_t from = a;
                if (flag != '-') {
                    while (a < v.len && a - from < width - 1 && v.ptr[a] == ' ') {
                        a++;
                    }
                }

                int count = digits_at(v, a, width - (int)(a - from), &x);
                bool whole = flag == '-' ? count > 0 : a - from + count == width;
                if (!whole) {
                    return PARSE_FIELD;
                }

                a += count;
            }

            switch (d) {
            case 'Y':
                r->year = x;
                break;
            case 'y': // the years 1969 to 2068, as POSIX reads two digits
                r->year = x >= 69 ? 1900 + x : 2000 + x;
                break;
            case 'm':
                if (x < 1 || x > 12) {
                    return PARSE_MONTH;
                }

                r->two[MONTH] = (int)x;
                break;
            case 'd':
            case 'e':
                if (x < 1 || x > 31) {
                    return PARSE_DAY;
                }

                r->two[DAY] = (int)x;
                break;
            case 'H':
                if (x > 23) {
                    return PARSE_HOUR;
                }

                r->two[HOUR] = (int)x;
                break;
            case 'I':
                if (x < 1 || x > 12) {
                    return PARSE_HOUR;
                }

                r->two[HOUR] = (int)x;
                r->twelve = true;
                break;
            case 'M':
                if (x > 59) {
                    return PARSE_MINUTE;
                }

                r->two[MINUTE] = (int)x;
                break;
            case 'S':
                if (x > 59) {
                    return PARSE_SECOND;
                }

                r->two[SECOND] = (int)x;
                break;
            case 'j':
                if (x < 1 || x > 366) {
                    return PARSE_YEARDAY;
                }

                r->yday = (int)x;
                break;
            default:
                r->nsec = x;
            }

            break;
        }
        case 'z': {
            if (a < v.len && v.ptr[a] == 'Z') {
                a++;
                break;
            }

            int w = colon ? 6 : 5;
            int64_t hours, minutes;
            if (a + w > v.len || (v.ptr[a] != '+' && v.ptr[a] != '-') || (colon && v.ptr[a + 3] != ':') || digits_at(v, a + 1, 2, &hours) != 2 || digits_at(v, a + w - 2, 2, &minutes) != 2) {
                return PARSE_FIELD;
            }

            if (hours > 23 || minutes > 59) {
                return PARSE_OFFSET;
            }

            r->offset = (hours * 3600 + minutes * 60) * (v.ptr[a] == '-' ? -1 : 1);
            a += w;
            break;
        }
        case 'Z':
            if (!same_word(v, a, "UTC", 3) && !same_word(v, a, "GMT", 3)) {
                return PARSE_FIELD;
            }

            a += 3;
            break;
        case 'B':
        case 'b':
        case 'h': {
            int found = 0;
            for (int k = 0; k < 12 && found == 0; k++) {
                int64_t len = d == 'B' ? (int64_t)strlen(month_names[k]) : 3;
                if (same_word(v, a, month_names[k], len)) {
                    found = k + 1;
                    a += len;
                }
            }

            if (found == 0) {
                return PARSE_FIELD;
            }

            r->two[MONTH] = found;
            break;
        }
        case 'A':
        case 'a': {
            int found = -1;
            for (int k = 0; k < 7 && found < 0; k++) {
                int64_t len = d == 'A' ? (int64_t)strlen(day_names[k]) : 3;
                if (same_word(v, a, day_names[k], len)) {
                    found = k;
                    a += len;
                }
            }

            if (found < 0) {
                return PARSE_FIELD;
            }

            r->weekday = found;
            break;
        }
        case 'p':
        case 'P':
            if (same_word(v, a, "AM", 2)) {
                r->half = 1;
            } else if (same_word(v, a, "PM", 2)) {
                r->half = 2;
            } else {
                return PARSE_FIELD;
            }

            a += 2;
            break;
        case 'n':
        case 't':
        case '%':
            if (a >= v.len || v.ptr[a] != (d == 'n' ? '\n' : d == 't' ? '\t' : '%')) {
                return PARSE_LITERAL;
            }

            a++;
            break;
        case 'F':
        case 'T':
        case 'X':
        case 'R':
        case 'D':
        case 'x':
        case 'r':
        case 'c': {
            sn_string part = d == 'F' ? SN_STR("%Y-%m-%d", 8) : d == 'R' ? SN_STR("%H:%M", 5) : (d == 'D' || d == 'x') ? SN_STR("%m/%d/%y", 8) : d == 'r' ? SN_STR("%I:%M:%S %p", 11) : d == 'c' ? SN_STR("%a %b %e %H:%M:%S %Y", 20) : SN_STR("%H:%M:%S", 8);
            int64_t inner = 0;
            *at = a;
            int bad = parse_into(part, v, at, &inner, r);
            if (bad == PARSE_LITERAL || bad == PARSE_FIELD) { // the layout's own directive is what it could not read, from where it began
                *at = a;
                return PARSE_FIELD;
            }

            if (bad != PARSE_OK) {
                return bad;
            }

            a = *at;
            break;
        }
        default:
            return PARSE_UNREAD;
        }
    }

    *at = a;
    return PARSE_OK;
}

// sn_time_parse is package time's parse: value read as layout says,
// into the date and the seconds since midnight it names, the
// nanoseconds, the seconds its offset is east of UTC, and its weekday
// when it names one, -1 when it does not. It returns 0, or what is
// wrong, with at where in value and li where in layout.
int64_t sn_time_parse(sn_string layout, sn_string value, int64_t *year, int64_t *month, int64_t *day, int64_t *clock, int64_t *nsec, int64_t *offset, int64_t *weekday, int64_t *at, int64_t *li) {
    sn_check_string(layout);
    sn_check_string(value);
    reading r = {.year = 1970, .weekday = -1};
    *at = 0;
    *li = 0;
    int bad = parse_into(layout, value, at, li, &r);
    if (bad != PARSE_OK) {
        return bad;
    }

    if (*at != value.len) {
        return PARSE_EXTRA;
    }

    if (r.twelve) {
        if (r.half == 0) {
            return PARSE_HALF;
        }

        r.two[HOUR] = r.two[HOUR] % 12 + (r.half == 2 ? 12 : 0);
    }

    if (r.yday != 0) {
        if (r.yday > (leap_year(r.year) ? 366 : 365)) {
            return PARSE_YEARDAY;
        }

        int m = 1, d = r.yday;
        while (d > days_in(m, r.year)) {
            d -= days_in(m, r.year);
            m++;
        }

        if ((r.two[MONTH] != 0 && r.two[MONTH] != m) || (r.two[DAY] != 0 && r.two[DAY] != d)) {
            return PARSE_MISMATCH;
        }

        r.two[MONTH] = m;
        r.two[DAY] = d;
    }

    r.two[MONTH] = r.two[MONTH] == 0 ? 1 : r.two[MONTH];
    r.two[DAY] = r.two[DAY] == 0 ? 1 : r.two[DAY];
    if (r.two[DAY] > days_in(r.two[MONTH], r.year)) {
        return PARSE_DAY;
    }

    *year = r.year;
    *month = r.two[MONTH];
    *day = r.two[DAY];
    *clock = r.two[HOUR] * 3600 + r.two[MINUTE] * 60 + r.two[SECOND];
    *nsec = r.nsec;
    *offset = r.offset;
    *weekday = r.weekday;
    return PARSE_OK;
}
