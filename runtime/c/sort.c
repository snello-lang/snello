// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Sort is Rust's driftsort, the stable sort of its standard library
// since 1.81 (Orson Peters and Lukas Bergdoll, MIT or Apache-2.0):
// stable, adaptive to order already in the input, and a quicksort where
// there is none. The input is cut into runs: a stretch already in order,
// or strictly descending and reversed, of at least about √n elements is
// a sorted run, and anything else an unsorted one, left as it is. Runs
// are merged as powersort's stack says, near the optimal order of
// merging, but two unsorted runs that fit in the buffer together are
// joined and not sorted; an unsorted run is sorted only when it must be
// merged with another, by a stable quicksort. Its partition writes the
// elements less than the pivot from the front of the buffer and the
// others from the back, choosing where with a select and not a branch,
// and copies them back in order; its pivot is the median of three, or of
// three medians of three, recursively, on a long slice; a pivot not
// greater than one an earlier partition used puts every element equal to
// it on the left and leaves them, so that k distinct values sort in
// n log k; and past 2 log₂ n partitions it falls back to merge sort, so
// no input is quadratic. Slices of 32 or fewer are sorted by sorting
// networks of four and eight elements merged from both ends at once.
// A merge first finds, by binary search, the elements of either run
// already where they belong and does nothing when the two are in order,
// and picks each next element with a select. The buffer is half the
// slice, where Rust's is the whole slice up to 8 MB: a sort costs half
// its input in memory, for one more pass of merging. Sorted, reversed
// and nearly sorted input costs n comparisons. A comparison that is not
// a consistent order, a float NaN or a function that contradicts itself,
// leaves the elements in an unspecified order but every one in the
// slice exactly once. The element's size is known at the call: the core
// is inlined into a copy for each common size, so that moving an
// element is a load and a store and not a call of memcpy.
#include "snello.h"

#include <string.h>

typedef bool (*less_fn)(void *ctx, const void *a, const void *b);

enum { min_run = 32, max_runs = 85, small_sort_max = 32, small_sort_room = small_sort_max + 16 };

typedef struct {
    int64_t base;
    int64_t len;
    int power;
    bool sorted;
} sort_run;

#define AT(i) (p + (size_t)(i) * size)
#define INLINE static inline __attribute__((always_inline))

static inline int ilog2(int64_t n) {
    return 63 - __builtin_clzll((uint64_t)n | 1);
}

// insert_tail moves p[i] left past the greater elements of p[0:i],
// which are in order, and stops after an equal one, so the sort is
// stable.
INLINE void insert_tail(char *p, int64_t i, size_t size, less_fn less, void *ctx, char *one) {
    if (!less(ctx, AT(i), AT(i - 1))) {
        return;
    }

    memcpy(one, AT(i), size);
    int64_t j = i;
    do {
        memcpy(AT(j), AT(j - 1), size);
        j--;
    } while (j > 0 && less(ctx, one, AT(j - 1)));
    memcpy(AT(j), one, size);
}

// insertion sorts p[0:n], of which p[0:sorted], at least one element,
// is in order.
INLINE void insertion(char *p, int64_t sorted, int64_t n, size_t size, less_fn less, void *ctx, char *one) {
    for (int64_t i = sorted; i < n; i++) {
        insert_tail(p, i, size, less, ctx, one);
    }
}

INLINE void reverse_run(char *p, int64_t n, size_t size, char *one) {
    for (int64_t i = 0, j = n - 1; i < j; i++, j--) {
        memcpy(one, AT(i), size);
        memcpy(AT(i), AT(j), size);
        memcpy(AT(j), one, size);
    }
}

// find_run returns the length of the run at the start of p[0:n], in
// order or, when down is set, strictly descending.
INLINE int64_t find_run(char *p, int64_t n, size_t size, less_fn less, void *ctx, bool *down) {
    *down = false;
    if (n < 2) {
        return n;
    }

    int64_t k = 2;
    if (less(ctx, AT(1), AT(0))) {
        *down = true;
        while (k < n && less(ctx, AT(k), AT(k - 1))) {
            k++;
        }
    } else {
        while (k < n && !less(ctx, AT(k), AT(k - 1))) {
            k++;
        }
    }

    return k;
}

// merge_runs merges p[0:na] and p[na:na+nb], both in order and neither
// empty, through buf, which holds the smaller of the two.
INLINE void merge_runs(char *p, int64_t na, int64_t nb, size_t size, less_fn less, void *ctx, char *buf) {
    // The elements of the first run not after the second's first are in
    // place already, and so are the second's not before the first's last.
    int64_t lo = 0, hi = na;
    while (lo < hi) {
        int64_t mid = lo + (hi - lo) / 2;
        if (less(ctx, AT(na), AT(mid))) {
            hi = mid;
        } else {
            lo = mid + 1;
        }
    }

    p += (size_t)lo * size;
    na -= lo;
    if (na == 0) {
        return;
    }

    lo = 0;
    hi = nb;
    while (lo < hi) {
        int64_t mid = lo + (hi - lo) / 2;
        if (less(ctx, AT(na + mid), AT(na - 1))) {
            lo = mid + 1;
        } else {
            hi = mid;
        }
    }

    nb = lo;
    if (nb == 0) {
        return;
    }

    if (na <= nb) { // the first run into the buffer, merged from the front
        memcpy(buf, p, (size_t)na * size);
        char *a = buf, *aend = buf + (size_t)na * size, *b = AT(na), *bend = AT(na + nb), *out = p;
        while (a < aend && b < bend) { // the second run's element only when it is less: stable
            bool second = less(ctx, b, a);
            memcpy(out, second ? b : a, size);
            b += second ? size : 0;
            a += second ? 0 : size;
            out += size;
        }

        memcpy(out, a, (size_t)(aend - a));
    } else { // the second run into the buffer, merged from the back
        memcpy(buf, AT(na), (size_t)nb * size);
        char *a = AT(na), *b = buf + (size_t)nb * size, *out = AT(na + nb);
        while (a > p && b > buf) { // the first run's element last only when it is greater: stable
            bool first = less(ctx, b - size, a - size);
            out -= size;
            memcpy(out, first ? a - size : b - size, size);
            a -= first ? size : 0;
            b -= first ? 0 : size;
        }

        memcpy(p, buf, (size_t)(b - buf));
    }
}

// power is powersort's node power of the boundary between two runs:
// how deep in the binary splitting of [0, n) their midpoints part.
static int power(int64_t s1, int64_t n1, int64_t n2, int64_t n) {
    int result = 0;
    int64_t a = 2 * s1 + n1, b = a + n1 + n2;
    for (;;) {
        ++result;
        if (a >= n) {
            a -= n;
            b -= n;
        } else if (b >= n) {
            break;
        }

        a <<= 1;
        b <<= 1;
    }

    return result;
}

INLINE void small_sort(char *p, int64_t n, size_t size, less_fn less, void *ctx, char *scratch, char *one);

// merge_sort sorts p[0:n] by its runs, each shorter than min_run
// replaced by min_run elements sorted by small_sort, merged as
// powersort's stack says through buf, room for n/2 elements and at
// least small_sort_room: the sort of a short slice, and the quicksort's
// fallback after too many poor pivots.
INLINE void merge_sort(char *base, int64_t n, size_t size, less_fn less, void *ctx, char *buf, char *one) {
    sort_run runs[max_runs];
    int top = 0;
    for (int64_t i = 0; i < n;) {
        char *p = base + (size_t)i * size;
        bool down;
        int64_t k = find_run(p, n - i, size, less, ctx, &down);
        if (down) {
            reverse_run(p, k, size, one);
        }

        if (k < min_run) {
            k = n - i < min_run ? n - i : min_run;
            small_sort(p, k, size, less, ctx, buf, one);
        }

        if (top > 0) {
            int pw = power(runs[top - 1].base, runs[top - 1].len, k, n);
            while (top > 1 && runs[top - 2].power > pw) {
                sort_run *a = &runs[top - 2], *b = &runs[top - 1];
                merge_runs(base + (size_t)a->base * size, a->len, b->len, size, less, ctx, buf);
                a->len += b->len;
                top--;
            }

            runs[top - 1].power = pw;
        }

        runs[top++] = (sort_run){i, k, 0, true};
        i += k;
    }

    while (top > 1) {
        sort_run *a = &runs[top - 2], *b = &runs[top - 1];
        merge_runs(base + (size_t)a->base * size, a->len, b->len, size, less, ctx, buf);
        a->len += b->len;
        top--;
    }
}

// sort4 writes the four elements at v to dst in order, stably, with
// five comparisons, each element copied once and chosen by a select.
INLINE void sort4(const char *v, char *dst, size_t size, less_fn less, void *ctx) {
    bool c1 = less(ctx, v + size, v), c2 = less(ctx, v + 3 * size, v + 2 * size);
    const char *a = v + (size_t)c1 * size, *b = v + (size_t)!c1 * size;
    const char *c = v + (size_t)(2 + c2) * size, *d = v + (size_t)(2 + !c2) * size;
    bool c3 = less(ctx, c, a), c4 = less(ctx, d, b); // the least is a or c, the greatest b or d
    const char *min = c3 ? c : a, *max = c4 ? b : d;
    const char *left = c3 ? a : (c4 ? c : b), *right = c4 ? d : (c3 ? b : c);
    bool c5 = less(ctx, right, left);
    memcpy(dst, min, size);
    memcpy(dst + size, c5 ? right : left, size);
    memcpy(dst + 2 * size, c5 ? left : right, size);
    memcpy(dst + 3 * size, max, size);
}

// merge_halves merges src[0:n/2] and src[n/2:n], each in order, into
// dst from both ends at once, n/2 steps that each write the least
// element left and the greatest one right. It returns false when less
// is not a consistent order: then the two ends crossed and dst holds
// some element twice.
INLINE bool merge_halves(const char *src, int64_t n, char *dst, size_t size, less_fn less, void *ctx) {
    int64_t half = n / 2, l = 0, r = half, lback = half - 1, rback = n - 1;
    char *out = dst, *outback = dst + (size_t)(n - 1) * size;
    for (int64_t k = 0; k < half; k++) {
        bool left = !less(ctx, src + (size_t)r * size, src + (size_t)l * size);
        memcpy(out, src + (size_t)(left ? l : r) * size, size);
        l += left;
        r += !left;
        out += size;
        bool leftback = less(ctx, src + (size_t)rback * size, src + (size_t)lback * size);
        memcpy(outback, src + (size_t)(leftback ? lback : rback) * size, size);
        lback -= leftback;
        rback -= !leftback;
        outback -= size;
    }

    if (n % 2 != 0) {
        bool left = l <= lback;
        memcpy(out, src + (size_t)(left ? l : r) * size, size);
        l += left;
        r += !left;
    }

    return l == lback + 1 && r == rback + 1;
}

// small_sort sorts p[0:n], n at most small_sort_max, through scratch,
// room for n + 16 elements: each half is begun by sorting networks into
// scratch, extended by insertion sort there, and the halves merged back
// from both ends. p is not written until that last merge, so an
// inconsistent order found before it is sorted in place by insertion,
// and one found by it is undone by copying the halves back.
INLINE void small_sort(char *p, int64_t n, size_t size, less_fn less, void *ctx, char *scratch, char *one) {
    if (n < 2) {
        return;
    }

    int64_t half = n / 2, sorted;
    char *s = scratch;
    if (size <= 16 && n >= 16) {
        char *t = scratch + (size_t)n * size;
        sort4(AT(0), t, size, less, ctx);
        sort4(AT(4), t + 4 * size, size, less, ctx);
        sort4(AT(half), t + 8 * size, size, less, ctx);
        sort4(AT(half + 4), t + 12 * size, size, less, ctx);
        if (!merge_halves(t, 8, s, size, less, ctx) || !merge_halves(t + 8 * size, 8, s + (size_t)half * size, size, less, ctx)) {
            insertion(p, 1, n, size, less, ctx, one);
            return;
        }

        sorted = 8;
    } else if (n >= 8) {
        sort4(AT(0), s, size, less, ctx);
        sort4(AT(half), s + (size_t)half * size, size, less, ctx);
        sorted = 4;
    } else {
        memcpy(s, AT(0), size);
        memcpy(s + (size_t)half * size, AT(half), size);
        sorted = 1;
    }

    for (int64_t i = sorted; i < half; i++) {
        memcpy(s + (size_t)i * size, AT(i), size);
        insert_tail(s, i, size, less, ctx, one);
    }

    char *s2 = s + (size_t)half * size;
    for (int64_t i = sorted; i < n - half; i++) {
        memcpy(s2 + (size_t)i * size, AT(half + i), size);
        insert_tail(s2, i, size, less, ctx, one);
    }

    if (!merge_halves(s, n, p, size, less, ctx)) {
        memcpy(p, s, (size_t)n * size);
    }
}

INLINE const char *median3(const char *a, const char *b, const char *c, less_fn less, void *ctx) {
    bool x = less(ctx, a, b), y = less(ctx, a, c);
    if (x == y) { // a is the least or the greatest: the median is the greater or the lesser of b and c
        return less(ctx, b, c) ^ x ? c : b;
    }

    return a;
}

// median3_rec is the median of the medians of three of each of the
// three regions of n elements at a, b and c, recursively: Tukey's
// ninther, deepened, so that a pivot of a long slice is near its median.
static const char *median3_rec(const char *a, const char *b, const char *c, int64_t n, size_t size, less_fn less, void *ctx) {
    if (n * 8 >= 64) {
        int64_t n8 = n / 8;
        a = median3_rec(a, a + (size_t)(n8 * 4) * size, a + (size_t)(n8 * 7) * size, n8, size, less, ctx);
        b = median3_rec(b, b + (size_t)(n8 * 4) * size, b + (size_t)(n8 * 7) * size, n8, size, less, ctx);
        c = median3_rec(c, c + (size_t)(n8 * 4) * size, c + (size_t)(n8 * 7) * size, n8, size, less, ctx);
    }

    return median3(a, b, c, less, ctx);
}

INLINE int64_t choose_pivot(const char *p, int64_t n, size_t size, less_fn less, void *ctx) {
    int64_t n8 = n / 8;
    const char *a = p, *b = AT(n8 * 4), *c = AT(n8 * 7);
    const char *m = n < 64 ? median3(a, b, c, less, ctx) : median3_rec(a, b, c, n8, size, less, ctx);
    return (int64_t)((size_t)(m - p) / size);
}

// partition moves the elements of p[0:n] less than pivot, a copy of
// p[at], or with le those not greater, ahead of the others, keeping the
// order of each side, and returns how many there are. The ones going
// left are written from the front of scratch and the others from its
// back, the place chosen by a select, and both copied back in order; the
// pivot itself is not compared with itself.
INLINE int64_t partition(char *p, int64_t n, size_t size, less_fn less, void *ctx, char *scratch, int64_t at, const char *pivot, bool le) {
    char *back = scratch + (size_t)n * size;
    int64_t left = 0;
    const char *e = p;
    for (int64_t end = at;; end = n) {
        for (const char *stop = AT(end); e < stop; e += size) {
            bool l = le ? !less(ctx, pivot, e) : less(ctx, e, pivot);
            back -= size;
            memcpy((l ? scratch : back) + (size_t)left * size, e, size);
            left += l;
        }

        if (end == n) {
            break;
        }

        back -= size;
        memcpy((le ? scratch : back) + (size_t)left * size, e, size);
        left += le;
        e += size;
    }

    memcpy(p, scratch, (size_t)left * size);
    for (int64_t i = 0; i < n - left; i++) {
        memcpy(AT(left + i), scratch + (size_t)(n - 1 - i) * size, size);
    }

    return left;
}

typedef struct {
    int64_t base;
    int64_t len;
    int limit;
    int ancestor;
} sort_part;

// quicksort sorts base[0:n] stably through scratch, room for n elements
// and at least small_sort_room, keeping each pivot in pivots, room for
// 2 log₂ n + 1 elements. The right side of a partition is sorted first
// and its left side waits on a stack, with the pivot its ancestor: the
// right side's elements are none less than it, so a pivot there not
// greater than it equals it.
INLINE void quicksort(char *base, int64_t n, size_t size, less_fn less, void *ctx, char *scratch, char *pivots, char *one) {
    sort_part waiting[128];
    int top = 0, limit = 2 * ilog2(n), ancestor = -1;
    int64_t at = 0, len = n;
    for (;;) {
        char *p = base + (size_t)at * size;
        if (len <= small_sort_max) {
            small_sort(p, len, size, less, ctx, scratch, one);
        } else if (limit == 0) {
            merge_sort(p, len, size, less, ctx, scratch, one);
        } else {
            limit--;
            int64_t pos = choose_pivot(p, len, size, less, ctx);
            char *pivot = pivots + (size_t)top * size;
            memcpy(pivot, AT(pos), size);
            bool equal = ancestor >= 0 && !less(ctx, pivots + (size_t)ancestor * size, pivot);
            int64_t left = 0;
            if (!equal) {
                left = partition(p, len, size, less, ctx, scratch, pos, pivot, false);
                equal = left == 0;
            }

            if (equal) { // the elements equal to the pivot go left, in order, and are done
                int64_t k = partition(p, len, size, less, ctx, scratch, pos, pivot, true);
                at += k;
                len -= k;
                ancestor = -1;
                continue;
            }

            waiting[top++] = (sort_part){at, left, limit, ancestor};
            at += left;
            len -= left;
            ancestor = top - 1;
            continue;
        }

        if (top == 0) {
            return;
        }

        sort_part w = waiting[--top];
        at = w.base;
        len = w.len;
        limit = w.limit;
        ancestor = w.ancestor;
    }
}

// sqrt_approx is about √n: 2 to half of log₂ n, with a step of Newton's
// method.
static inline int64_t sqrt_approx(int64_t n) {
    int shift = (1 + ilog2(n)) / 2;
    return ((INT64_C(1) << shift) + (n >> shift)) / 2;
}

// drift sorts base[0:n] by its runs, through scratch, room for room
// elements, fewer than n; two unsorted runs that fit in it together are
// joined, and the rest merged, each unsorted one sorted first. Since
// room is less than n, the last merge sorts.
INLINE void drift(char *base, int64_t n, size_t size, less_fn less, void *ctx, char *scratch, int64_t room, char *pivots, char *one) {
    int64_t good = n <= 64 * 64 ? (n - n / 2 < 32 ? n - n / 2 : 32) : sqrt_approx(n);
    sort_run runs[max_runs];
    int top = 0;
    for (int64_t i = 0;;) {
        sort_run next = {i, 0, 0, false};
        int pw = 0; // past the end, every run left is merged
        if (i < n) {
            char *p = base + (size_t)i * size;
            if (n - i >= good) {
                bool down;
                int64_t k = find_run(p, n - i, size, less, ctx, &down);
                if (k >= good) {
                    if (down) {
                        reverse_run(p, k, size, one);
                    }

                    next.len = k;
                    next.sorted = true;
                }
            }

            if (!next.sorted) {
                next.len = n - i < good ? n - i : good;
            }

            if (top > 0) {
                pw = power(runs[top - 1].base, runs[top - 1].len, next.len, n);
            }
        }

        while (top > 1 && runs[top - 2].power > pw) {
            sort_run *a = &runs[top - 2], *b = &runs[top - 1], *both[2] = {a, b};
            char *p = base + (size_t)a->base * size;
            if (a->sorted || b->sorted || a->len + b->len > room) {
                for (int k = 0; k < 2; k++) {
                    if (!both[k]->sorted) {
                        quicksort(base + (size_t)both[k]->base * size, both[k]->len, size, less, ctx, scratch, pivots, one);
                    }
                }

                merge_runs(p, a->len, b->len, size, less, ctx, scratch);
                a->sorted = true;
            }

            a->len += b->len;
            top--;
        }

        if (i >= n) {
            return;
        }

        if (top > 0) {
            runs[top - 1].power = pw;
        }

        runs[top++] = next;
        i += next.len;
    }
}

INLINE void sort_core(char *base, int64_t n, size_t size, less_fn less, void *ctx) {
    char one[size];
    if (n <= 20) {
        insertion(base, 1, n, size, less, ctx, one);
        return;
    }

    int64_t room = n - n / 2;
    if (room < small_sort_room) {
        room = small_sort_room;
    }

    size_t need = (size_t)(room + 2 * ilog2(room) + 2) * size;
    char local[4096];
    char *scratch = need <= sizeof local ? local : sn_alloc(need);
    if (n <= 2 * small_sort_max) {
        merge_sort(base, n, size, less, ctx, scratch, one);
    } else {
        drift(base, n, size, less, ctx, scratch, room, scratch + (size_t)room * size, one);
    }

    if (scratch != local) {
        sn_free(scratch);
    }
}

#undef INLINE
#undef AT

// sn_slice_sort is inlined whole into each of the program's sorters,
// one for each element type and order, when the program is optimized
// with the runtime: there the element's size and less are constants, so
// the switch keeps one case and clang inlines the comparison into it.
__attribute__((always_inline)) inline void sn_slice_sort(sn_slice *s, size_t size, bool (*less)(void *ctx, const void *a, const void *b), void *ctx) {
    sn_check_slice(s);
    if (s->len < 2 || size == 0) {
        return;
    }

    switch (size) { // each a copy of the sort with the element's size a constant
    case 4:
        sort_core(s->ptr, s->len, 4, less, ctx);
        break;
    case 8:
        sort_core(s->ptr, s->len, 8, less, ctx);
        break;
    case 16:
        sort_core(s->ptr, s->len, 16, less, ctx);
        break;
    case 24:
        sort_core(s->ptr, s->len, 24, less, ctx);
        break;
    default:
        sort_core(s->ptr, s->len, size, less, ctx);
    }
}
