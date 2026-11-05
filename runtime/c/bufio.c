// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "internal.h"

#include <string.h>

// The bufio package's moves of bytes, which its buffers make in blocks
// as Go's bufio does with copy and bytes.IndexByte: memmove and memchr,
// which the C library writes with vector instructions.

// sn_bufio_copyBytes copies the bytes of src that fit to the start of
// dst, as Go's copy does, the two possibly overlapping, and returns how
// many.
int64_t sn_bufio_copyBytes(sn_slice_u8 *dst, sn_slice_u8 src) {
    sn_check_slice((sn_slice *)dst);
    sn_check_slice((sn_slice *)&src);
    int64_t n = dst->len < src.len ? dst->len : src.len;
    if (n > 0) {
        memmove(dst->ptr, src.ptr, (size_t)n);
    }

    return n;
}

// sn_bufio_copyString is sn_bufio_copyBytes from a string.
int64_t sn_bufio_copyString(sn_slice_u8 *dst, sn_string s) {
    sn_check_slice((sn_slice *)dst);
    sn_check_string(s);
    int64_t n = dst->len < s.len ? dst->len : s.len;
    if (n > 0) {
        memmove(dst->ptr, s.ptr, (size_t)n);
    }

    return n;
}

// sn_bufio_indexByte returns the index of the first c in p, or -1.
int64_t sn_bufio_indexByte(sn_slice_u8 p, uint8_t c) {
    sn_check_slice((sn_slice *)&p);
    if (p.len == 0) {
        return -1;
    }

    const uint8_t *at = memchr(p.ptr, c, (size_t)p.len);
    return at == NULL ? -1 : (int64_t)(at - (const uint8_t *)p.ptr);
}

// sn_bufio_appendBytes appends the bytes of p to b in one copy, growing
// b once.
void sn_bufio_appendBytes(sn_slice_u8 *buf, sn_slice_u8 p) {
    sn_slice *b = (sn_slice *)buf;
    sn_check_slice((sn_slice *)&p);
    if (p.len == 0) {
        return;
    }

    sn_slice_own(b, "append");
    sn_slice_grow(b, b->len + p.len, 1);
    memmove((uint8_t *)b->ptr + b->len, p.ptr, (size_t)p.len);
    b->len += p.len;
}

// sn_bufio_bytesOf lends the bytes of s as a view of a []byte, which
// the program only reads.
sn_slice_u8 sn_bufio_bytesOf(sn_string s) {
    sn_check_string(s);
    return (sn_slice_u8){(uint8_t *)s.ptr, s.len, SN_VIEW};
}

// sn_bufio_slide moves the bytes of b from lo up to hi to its start,
// where a buffer keeps what it has not handed out yet.
void sn_bufio_slide(sn_slice_u8 *buf, int64_t lo, int64_t hi) {
    sn_check_slice((sn_slice *)buf);
    if (lo < 0 || hi < lo || hi > buf->len) {
        sn_slice_bounds(lo, hi, buf->len);
    }

    if (lo > 0 && hi > lo) {
        memmove(buf->ptr, (uint8_t *)buf->ptr + lo, (size_t)(hi - lo));
    }
}
