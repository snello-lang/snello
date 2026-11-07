// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The runtime's half of the testing package, which a test build alone
// links into use: the running task's identity, for the runner to keep
// a state per test when tests run in parallel; a lock over that
// state, held across no park; the allocator's counts, for a
// benchmark's allocations and bytes per operation; and the place of
// the call being made, which the compiler writes before every call
// of testing.Log, Logf and Skip and of an exported function of expect
// and the packages read back, kept on the running task so that two
// parallel tests never share it (_docs/design/09-testing.md, section 4).

#include "internal.h"

#include <pthread.h>
#include <stdatomic.h>

static pthread_mutex_t testing_mu = PTHREAD_MUTEX_INITIALIZER;

// sn_testing_task is testing.task: the running task, as a number.
int64_t sn_testing_task(void) {
    return (int64_t)(uintptr_t)sn_self();
}

// sn_testing_lock and sn_testing_unlock are testing.lock and unlock:
// a mutex of the runtime's, taken for the runner's map of states.
void sn_testing_lock(void) {
    pthread_mutex_lock(&testing_mu);
}

void sn_testing_unlock(void) {
    pthread_mutex_unlock(&testing_mu);
}

// sn_testing_allocs and sn_testing_allocBytes are testing.allocs and
// allocBytes: the blocks and the bytes sn_alloc has given out so far.
int64_t sn_testing_allocs(void) {
    return atomic_load(&sn_alloc_count);
}

int64_t sn_testing_allocBytes(void) {
    return atomic_load(&sn_alloc_total);
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
