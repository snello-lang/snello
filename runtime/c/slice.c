// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Slices: making one, pushing, popping, inserting, removing, reversing,
// cloning and cutting; growing is snello.c's and sorting sort.c's.

#include "internal.h"

#include <stdio.h>
#include <string.h>

void sn_slice_own(const sn_slice *s, const char *what) {
    if (s->cap == SN_VIEW) {
        char buf[160];
        int n = snprintf(buf, sizeof buf, "%s on a sub-slice: it lends the elements of its slice and cannot change its length", what);
        sn_panic(SN_STR(buf, n));
    }
}

int64_t sn_slice_cap(const sn_slice *s) {
    return s->cap == SN_VIEW ? s->len : s->cap;
}

void sn_slice_free(sn_slice *s) {
    sn_check_slice(s);
    sn_slice_own(s, "replacing");
    sn_free(s->ptr);
    s->ptr = NULL;
    s->len = 0;
    s->cap = 0;
    SN_POISON(*s);
}

// sn_slice_bounds stops the program for the slice expression [lo:hi]
// on n elements, in the words of Go's runtime: the high bound first.
_Noreturn void sn_slice_bounds(int64_t lo, int64_t hi, int64_t n) {
    char buf[128];
    if (hi < 0 || hi > n) {
        snprintf(buf, sizeof buf, "slice bounds out of range [:%lld] with length %lld", (long long)hi, (long long)n);
    } else if (lo < 0) {
        snprintf(buf, sizeof buf, "slice bounds out of range [%lld:]", (long long)lo);
    } else {
        snprintf(buf, sizeof buf, "slice bounds out of range [%lld:%lld]", (long long)lo, (long long)hi);
    }

    sn_panic(SN_STR(buf, (int64_t)strlen(buf)));
}

void sn_slice_make(sn_slice *out, int64_t len, int64_t cap, size_t size) {
    if (len < 0) {
        sn_panic(SN_STR("makeslice: len out of range", 27));
    }

    if (cap < len) {
        sn_panic(SN_STR("makeslice: cap out of range", 27));
    }

    out->ptr = sn_alloc((size_t)cap * size);
    memset(out->ptr, 0, (size_t)cap * size);
    out->len = len;
    out->cap = cap;
}

void sn_slice_push(sn_slice *s, const void *v, size_t size) {
    sn_slice_own(s, "Push");
    sn_slice_grow(s, s->len + 1, size);
    memcpy((char *)s->ptr + (size_t)s->len * size, v, size);
    s->len++;
}

bool sn_slice_pop(sn_slice *s, void *out, size_t size) {
    sn_check_slice(s);
    sn_slice_own(s, "Pop");
    if (s->len == 0) {
        return false;
    }

    s->len--;
    memcpy(out, (char *)s->ptr + (size_t)s->len * size, size);
    return true;
}

void sn_slice_insert(sn_slice *s, int64_t i, const void *v, size_t size) {
    sn_check_slice(s);
    sn_slice_own(s, "Insert");
    if ((uint64_t)i > (uint64_t)s->len) {
        sn_slice_bounds(i, s->len, s->len); // Go's slices.Insert: [7:3]
    }

    sn_slice_grow(s, s->len + 1, size);
    char *at = (char *)s->ptr + (size_t)i * size;
    memmove(at + size, at, (size_t)(s->len - i) * size);
    memcpy(at, v, size);
    s->len++;
}

void sn_slice_remove(sn_slice *s, int64_t i, void *out, size_t size) {
    sn_slice_own(s, "Remove");
    sn_index(i, s->len);
    char *at = (char *)s->ptr + (size_t)i * size;
    memcpy(out, at, size);
    memmove(at, at + size, (size_t)(s->len - i - 1) * size);
    s->len--;
}

// exchange swaps the size bytes at a and b through a buffer on the
// stack, a piece at a time, so an element of any size fits.
static void exchange(char *a, char *b, size_t size) {
    char tmp[256];
    for (size_t done = 0; done < size; done += sizeof tmp) {
        size_t n = size - done < sizeof tmp ? size - done : sizeof tmp;
        memcpy(tmp, a + done, n);
        memcpy(a + done, b + done, n);
        memcpy(b + done, tmp, n);
    }
}

// reverse_core reverses the n elements at p; inlined into a copy for
// each common size, a swap is two loads and two stores.
static inline __attribute__((always_inline)) void reverse_core(char *p, int64_t n, size_t size) {
    char *lo = p, *hi = p + (size_t)(n - 1) * size;
    char tmp[size];
    for (; lo < hi; lo += size, hi -= size) {
        memcpy(tmp, lo, size);
        memcpy(lo, hi, size);
        memcpy(hi, tmp, size);
    }
}

// sn_slice_reserve makes room in s for at least need elements of size
// bytes, growing as Push does, after asking that s own its buffer, as
// every operation that changes a slice's length asks, naming what.
void sn_slice_reserve(sn_slice *s, int64_t need, size_t size, const char *what) {
    sn_slice_own(s, what);
    sn_slice_grow(s, need, size);
}

// sn_slice_index_byte returns the position of the first element of
// one byte equal to c among n at p, or -1, by memchr.
int64_t sn_slice_index_byte(const void *p, int64_t n, uint8_t c) {
    if (n <= 0) {
        return -1;
    }

    const uint8_t *at = memchr(p, c, (size_t)n);
    return at == NULL ? -1 : at - (const uint8_t *)p;
}

void sn_slice_reverse(sn_slice *s, size_t size) {
    sn_check_slice(s);
    if (s->len < 2 || size == 0) {
        return;
    }

    switch (size) {
    case 1:
        reverse_core(s->ptr, s->len, 1);
        break;
    case 4:
        reverse_core(s->ptr, s->len, 4);
        break;
    case 8:
        reverse_core(s->ptr, s->len, 8);
        break;
    case 16:
        reverse_core(s->ptr, s->len, 16);
        break;
    case 24:
        reverse_core(s->ptr, s->len, 24);
        break;
    default:
        if (size > 256) { // a large element a piece at a time, through a buffer on the stack
            char *lo = s->ptr, *hi = (char *)s->ptr + (size_t)(s->len - 1) * size;
            for (; lo < hi; lo += size, hi -= size) {
                exchange(lo, hi, size);
            }
        } else {
            reverse_core(s->ptr, s->len, size);
        }
    }
}

void sn_slice_clone(const sn_slice *s, sn_slice *out, size_t size) {
    sn_check_slice(s);
    out->ptr = sn_alloc((size_t)s->len * size);
    memcpy(out->ptr, s->ptr, (size_t)s->len * size);
    out->len = s->len;
    out->cap = s->len;
}

void sn_slice_sub(const sn_slice *s, int64_t lo, int64_t hi, sn_slice *out, size_t size) {
    sn_check_slice(s);
    if (lo < 0 || hi < lo || hi > s->len) {
        sn_slice_bounds(lo, hi, s->len);
    }

    out->ptr = (char *)s->ptr + (size_t)lo * size;
    out->len = hi - lo;
    out->cap = SN_VIEW;
}
