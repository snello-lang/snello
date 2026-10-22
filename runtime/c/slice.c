// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Slices: making one, pushing, popping, inserting, removing, reversing,
// cloning and cutting; growing is teak.c's and sorting sort.c's.

#include "internal.h"

#include <stdio.h>
#include <string.h>

void tk_slice_own(const tk_slice *s, const char *what) {
    if (s->cap == TK_VIEW) {
        char buf[160];
        int n = snprintf(buf, sizeof buf, "%s on a sub-slice: it lends the elements of its slice and cannot change its length", what);
        tk_panic(TK_STR(buf, n));
    }
}

int64_t tk_slice_cap(const tk_slice *s) {
    return s->cap == TK_VIEW ? s->len : s->cap;
}

void tk_slice_free(tk_slice *s) {
    tk_check_slice(s);
    tk_slice_own(s, "replacing");
    tk_free(s->ptr);
    s->ptr = NULL;
    s->len = 0;
    s->cap = 0;
    TK_POISON(*s);
}

// tk_slice_bounds stops the program for the slice expression [lo:hi]
// on n elements, in the words of Go's runtime: the high bound first.
_Noreturn void tk_slice_bounds(int64_t lo, int64_t hi, int64_t n) {
    char buf[128];
    if (hi < 0 || hi > n) {
        snprintf(buf, sizeof buf, "slice bounds out of range [:%lld] with length %lld", (long long)hi, (long long)n);
    } else if (lo < 0) {
        snprintf(buf, sizeof buf, "slice bounds out of range [%lld:]", (long long)lo);
    } else {
        snprintf(buf, sizeof buf, "slice bounds out of range [%lld:%lld]", (long long)lo, (long long)hi);
    }

    tk_panic(TK_STR(buf, (int64_t)strlen(buf)));
}

void tk_slice_make(tk_slice *out, int64_t len, int64_t cap, size_t size) {
    if (len < 0) {
        tk_panic(TK_STR("makeslice: len out of range", 27));
    }

    if (cap < len) {
        tk_panic(TK_STR("makeslice: cap out of range", 27));
    }

    out->ptr = tk_alloc((size_t)cap * size);
    memset(out->ptr, 0, (size_t)cap * size);
    out->len = len;
    out->cap = cap;
}

void tk_slice_push(tk_slice *s, const void *v, size_t size) {
    tk_slice_own(s, "Push");
    tk_slice_grow(s, s->len + 1, size);
    memcpy((char *)s->ptr + (size_t)s->len * size, v, size);
    s->len++;
}

bool tk_slice_pop(tk_slice *s, void *out, size_t size) {
    tk_check_slice(s);
    tk_slice_own(s, "Pop");
    if (s->len == 0) {
        return false;
    }

    s->len--;
    memcpy(out, (char *)s->ptr + (size_t)s->len * size, size);
    return true;
}

void tk_slice_insert(tk_slice *s, int64_t i, const void *v, size_t size) {
    tk_check_slice(s);
    tk_slice_own(s, "Insert");
    if ((uint64_t)i > (uint64_t)s->len) {
        tk_slice_bounds(i, s->len, s->len); // Go's slices.Insert: [7:3]
    }

    tk_slice_grow(s, s->len + 1, size);
    char *at = (char *)s->ptr + (size_t)i * size;
    memmove(at + size, at, (size_t)(s->len - i) * size);
    memcpy(at, v, size);
    s->len++;
}

void tk_slice_remove(tk_slice *s, int64_t i, void *out, size_t size) {
    tk_slice_own(s, "Remove");
    tk_index(i, s->len);
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

void tk_slice_reverse(tk_slice *s, size_t size) {
    tk_check_slice(s);
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

void tk_slice_clone(const tk_slice *s, tk_slice *out, size_t size) {
    tk_check_slice(s);
    out->ptr = tk_alloc((size_t)s->len * size);
    memcpy(out->ptr, s->ptr, (size_t)s->len * size);
    out->len = s->len;
    out->cap = s->len;
}

void tk_slice_sub(const tk_slice *s, int64_t lo, int64_t hi, tk_slice *out, size_t size) {
    tk_check_slice(s);
    if (lo < 0 || hi < lo || hi > s->len) {
        tk_slice_bounds(lo, hi, s->len);
    }

    out->ptr = (char *)s->ptr + (size_t)lo * size;
    out->len = hi - lo;
    out->cap = TK_VIEW;
}
