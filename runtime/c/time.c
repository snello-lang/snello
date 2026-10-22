// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The clocks, and time.Sleep.

#define _POSIX_C_SOURCE 200809L
#define _DARWIN_C_SOURCE

#include "internal.h"

#include <errno.h>
#include <time.h>

// tk_time_now reads the system's clock: nanoseconds since the start of
// 1970 in UTC.
int64_t tk_time_now(void) {
    struct timespec ts;
    clock_gettime(CLOCK_REALTIME, &ts);
    return (int64_t)ts.tv_sec * 1000000000 + ts.tv_nsec;
}

// tk_time_mono reads the monotonic clock, which the system never sets
// back: nanoseconds since a moment of its own, plus one, so that a
// reading is never 0, which a Time without one holds.
int64_t tk_time_mono(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000000000 + ts.tv_nsec + 1;
}

// tk_time_clock is package time's clock: the wall clock's seconds and
// nanoseconds, and the monotonic clock's reading.
void tk_time_clock(int64_t *sec, int64_t *nsec, int64_t *mono) {
    struct timespec ts;
    clock_gettime(CLOCK_REALTIME, &ts);
    *sec = (int64_t)ts.tv_sec;
    *nsec = ts.tv_nsec;
    *mono = tk_time_mono();
}

// tk_time_read is a Time of now, for the timer to put in a channel.
tk_time tk_time_read(void) {
    tk_time t;
    tk_time_clock(&t.sec, &t.nsec, &t.mono);
    return t;
}

// tk_time_Sleep parks the task for d nanoseconds; the worker runs
// others meanwhile.
void tk_time_Sleep(int64_t d) {
    tk_sleep(d);
}
