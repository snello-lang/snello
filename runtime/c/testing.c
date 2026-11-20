// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The runtime's half of the testing package, which a test build alone
// links into use: the running task's identity, for the runner to keep
// a state per test when tests run in parallel; the benchmark's state,
// with the allocator's counts for its allocations and bytes per
// operation; an example's standard output pointed at a file and back;
// and the place of the call being made, which the compiler
// writes before every call of testing.Log, Logf and Skip and of an
// exported function of expect and the packages read back, kept on the
// running task so that two parallel tests never share it.

#define _POSIX_C_SOURCE 200809L // glibc's F_DUPFD_CLOEXEC under -std=c11, for the example's saved output

#include "internal.h"

#include <stdatomic.h>
#include <stdio.h>
#if defined(_WIN32)
#include <io.h>
#else
#include <fcntl.h>
#include <unistd.h>
#endif

// stdout_fd is the descriptor of the standard output, which an
// example's run points at a file.
enum { stdout_fd = 1 };

// sn_testing_task is testing.task: the running task, as a number.
int64_t sn_testing_task(void) { return (int64_t)(uintptr_t)sn_self(); }

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

void sn_testing_benchReport(void) { bench_report = true; }

int64_t sn_testing_benchNanos(void) { return bench_nanos; }

int64_t sn_testing_benchAllocs(void) { return bench_allocs; }

int64_t sn_testing_benchBytes(void) { return bench_bytes; }

bool sn_testing_benchReported(void) { return bench_report; }

// sn_testing_line is written before a call of testing.Log, Logf or
// Skip: the file and line, a literal of the program's.
void sn_testing_line(sn_string where) { sn_task_places()[SN_PLACE_LINE] = where; }

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
sn_string sn_testing_where(void) { return sn_string_clone(sn_task_places()[SN_PLACE_LINE]); }

// trace_line appends one line of a trace to the string being built for
// the test's runner: the indent, the name, " at ", the file, a colon,
// the line and a newline.
typedef struct {
    sn_string out;
    sn_string indent;
} trace_lines;

static void trace_line(void *ctx, const char *name, const char *file, int32_t at) {
    trace_lines *l = ctx;
    char line[SN_TRACE_LINE_ROOM];
    int n = sn_trace_line(line, sizeof line, name, file, at);
    sn_string_append(&l->out, &l->indent);
    sn_string piece = { (const uint8_t *)line, n, 0 };
    sn_string_append(&l->out, &piece);
}

// sn_testing_trace is package testing's trace: the frames of the panic
// the test's Catch caught, which the task kept at the panic, as lines
// each after indent, without the last newline; empty when no trace was
// kept.
sn_string sn_testing_trace(sn_string indent) {
    trace_lines l = { { 0 }, indent };
    int n;
    const uintptr_t *walked = sn_task_trace(&n);
    sn_trace_each(walked, n, trace_line, &l);
    if (l.out.len > 0) {
        l.out.len--; // the last newline: the runner ends its line
    }

    return l.out;
}

sn_string sn_expect_where(void) { return sn_string_clone(sn_task_places()[SN_PLACE_CALL]); }

sn_string sn_expect_parts(void) { return sn_string_clone(sn_task_places()[SN_PLACE_PARTS]); }

// stdout_moved tells the system and the runtime that descriptor 1 now
// points elsewhere: on Windows the standard output handle a child
// process inherits is pointed where the descriptor is, and the
// runtime's cache of whether the descriptor is a console is cleared,
// with the part of a character it held; elsewhere a child inherits the
// descriptor itself and nothing is cached.
static void stdout_moved(void) {
#if defined(_WIN32)
    SetStdHandle(STD_OUTPUT_HANDLE, (HANDLE)_get_osfhandle(stdout_fd));
    sn_console_reset(stdout_fd);
#endif
}

// save_stdout returns a new descriptor of what descriptor 1 points at,
// kept from child processes, or -1: on Windows a handle duplicated as
// not inheritable, which a CRT descriptor is opened over, since a CRT
// _dup is inheritable.
static int save_stdout(void) {
#if defined(_WIN32)
    HANDLE h;
    if (!DuplicateHandle(GetCurrentProcess(), (HANDLE)_get_osfhandle(stdout_fd), GetCurrentProcess(), &h, 0, FALSE,
                         DUPLICATE_SAME_ACCESS)) {
        return -1;
    }

    int fd = _open_osfhandle((intptr_t)h, 0);
    if (fd < 0) {
        CloseHandle(h);
    }

    return fd;
#else
    return fcntl(stdout_fd, F_DUPFD_CLOEXEC, 0);
#endif
}

// close_fd closes a descriptor.
static void close_fd(int fd) {
#if defined(_WIN32)
    _close(fd);
#else
    close(fd);
#endif
}

// point_stdout points descriptor 1 where fd points, reporting whether
// it did, and closes fd.
static bool point_stdout(int fd) {
#if defined(_WIN32)
    bool moved = _dup2(fd, stdout_fd) == 0;
#else
    bool moved = dup2(fd, stdout_fd) == stdout_fd;
#endif
    close_fd(fd);
    return moved;
}

// sn_testing_captureStart is testing.captureStart: it points descriptor
// 1, the standard output, at the file at path, made empty, and returns
// a descriptor of what 1 pointed at before, for captureEnd to put back,
// kept from child processes; -1 when the file cannot be made or the
// descriptor moved, the output then left where it was, or when 1 was
// closed, which the file then took. What C's stdio holds for the
// output is written out first, at the start and at the end, so that
// what a bound C library printed lands where it was printed. It runs on
// the runner's own task, between examples, so that no other test writes
// meanwhile.
int64_t sn_testing_captureStart(sn_string path) {
    fflush(stdout);
    int64_t file = -1; // no file until the create gives one: gcc's link-time view cannot see it set on every success
    if (sn_os_create(path, &file) != 0) {
        return -1;
    }

    if (file == stdout_fd) { // 1 was closed, and the file took it: there is nothing to save, and 1 is closed again
        close_fd(stdout_fd);
        return -1;
    }

    int saved = save_stdout();
    if (saved < 0) {
        close_fd((int)file);
        return -1;
    }

    if (!point_stdout((int)file)) {
        sn_testing_captureEnd(saved);
        return -1;
    }

    stdout_moved();
    return saved;
}

// sn_testing_captureEnd is testing.captureEnd: it points descriptor 1
// back where saved points, as captureStart found it, and closes saved.
void sn_testing_captureEnd(int64_t saved) {
    fflush(stdout);
    point_stdout((int)saved);
    stdout_moved();
}
