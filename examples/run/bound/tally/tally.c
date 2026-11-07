// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The C side of package tally: each function tally.snello declares
// without a body, defined under the symbol tally.snello.h declares.
// The header is written by the compiler from the Snello signatures, so
// a definition that disagrees with its declaration is an error here.

#include "tally.snello.h"

#include <stdlib.h>
#include <string.h>

// A counter is the C side's own object, outside the program's memory:
// the handle the program holds is its address as an integer, and
// release frees it when the Tally holding it is dropped. live counts
// the counters not yet released, for the program to see the drop.
struct counter {
    int64_t n;
};

static int64_t live;

sn_int snm_tally_open(sn_int start) {
    struct counter *c = malloc(sizeof *c);
    c->n = start;
    live++;
    return (sn_int)(intptr_t)c;
}

sn_int snm_tally_Live(void) {
    return live;
}

sn_int snm_tally_add(sn_int handle, sn_int n) {
    struct counter *c = (struct counter *)(intptr_t)handle;
    c->n += n;
    return c->n;
}

void snm_tally_release(sn_int handle) {
    free((struct counter *)(intptr_t)handle);
    live--;
}

sn_int snm_tally_Sum(sn_slice_u8 xs) {
    sn_int sum = 0;
    for (int64_t i = 0; i < xs.len; i++) {
        sum += xs.ptr[i];
    }

    return sum;
}

sn_int snm_tally_Fill(sn_slice_u8 *buf, uint8_t b) {
    memset(buf->ptr, b, (size_t)buf->cap);
    buf->len = buf->cap;
    return buf->len;
}

bool snm_tally_Parse(sn_string s, sn_int *n) {
    if (s.len == 0) {
        return false;
    }

    sn_int v = 0;
    for (int64_t i = 0; i < s.len; i++) {
        if (s.ptr[i] < '0' || s.ptr[i] > '9') {
            return false;
        }

        v = v * 10 + (s.ptr[i] - '0');
    }

    *n = v;
    return true;
}
