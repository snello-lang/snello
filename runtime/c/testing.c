// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The runtime's half of the testing package, which a test build alone
// links into use: the running task's identity, for the runner to keep
// a state per test when tests run in parallel; the benchmark's state,
// with the allocator's counts for its allocations and bytes per
// operation; and the place of the call being made, which the compiler
// writes before every call of testing.Log, Logf and Skip and of an
// exported function of expect and the packages read back, kept on the
// running task so that two parallel tests never share it
// (_docs/design/09-testing.md, section 4).

#include "internal.h"

#include <stdatomic.h>

// sn_testing_task is testing.task: the running task, as a number.
int64_t sn_testing_task(void) {
    return (int64_t)(uintptr_t)sn_self();
}

// The benchmark's state, one benchmark at a time, since the runner
// runs them one after another on one task: how many iterations this
// run makes, how many the loop has counted, when the timer started
// and what it has counted, and the allocator's counts at the start,
// which become the counts of the loop alone at its end. It lives here
// so that testing.Loop costs one call and no lock, and so that the
// package has no variable a task writes.
static int64_t bench_n, bench_i, bench_start, bench_nanos, bench_allocs, bench_bytes;
static bool bench_report;

void sn_testing_benchStart(int64_t n) {
    bench_n = n;
    bench_i = 0;
    bench_nanos = 0;
    bench_report = false;
}

bool sn_testing_benchLoop(void) {
    if (bench_i == 0) {
        bench_start = sn_time_mono();
        sn_alloc_counts(&bench_allocs, &bench_bytes);
    }

    if (bench_i >= bench_n) {
        int64_t allocs, bytes;
        bench_nanos += sn_time_mono() - bench_start;
        sn_alloc_counts(&allocs, &bytes);
        bench_allocs = allocs - bench_allocs;
        bench_bytes = bytes - bench_bytes;
        return false;
    }

    bench_i++;
    return true;
}

void sn_testing_benchReset(void) {
    bench_start = sn_time_mono();
    bench_nanos = 0;
    sn_alloc_counts(&bench_allocs, &bench_bytes);
}

void sn_testing_benchReport(void) {
    bench_report = true;
}

int64_t sn_testing_benchNanos(void) {
    return bench_nanos;
}

int64_t sn_testing_benchAllocs(void) {
    return bench_allocs;
}

int64_t sn_testing_benchBytes(void) {
    return bench_bytes;
}

bool sn_testing_benchReported(void) {
    return bench_report;
}

// sn_testing_line is written before a call of testing.Log, Logf or
// Skip: the file and line, a literal of the program's.
void sn_testing_line(sn_string where) {
    sn_task_places()[SN_PLACE_LINE] = where;
}

// sn_testing_call is written before a call of an exported function of
// expect: the file, line and call's text, a literal, and the parts
// the compiler found, an owned string the task frees with the next
// call's or at its end.
void sn_testing_call(sn_string where, sn_string parts) {
    sn_string *places = sn_task_places();
    places[SN_PLACE_CALL] = where;
    sn_string_drop(&places[SN_PLACE_PARTS]);
    places[SN_PLACE_PARTS] = parts;
}

// sn_testing_where, sn_expect_where and sn_expect_parts are
// testing.where, expect.where and expect.parts: a copy of the place,
// the caller's to drop.
sn_string sn_testing_where(void) {
    return sn_string_clone(sn_task_places()[SN_PLACE_LINE]);
}

sn_string sn_expect_where(void) {
    return sn_string_clone(sn_task_places()[SN_PLACE_CALL]);
}

sn_string sn_expect_parts(void) {
    return sn_string_clone(sn_task_places()[SN_PLACE_PARTS]);
}
