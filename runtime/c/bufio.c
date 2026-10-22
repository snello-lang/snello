// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "internal.h"

#include <string.h>

// The bufio package's moves of bytes, which its buffers make in blocks
// as Go's bufio does with copy and bytes.IndexByte: memmove and memchr,
// which the C library writes with vector instructions.

// tk_bufio_copyBytes copies the bytes of src that fit to the start of
// dst, as Go's copy does, the two possibly overlapping, and returns how
// many.
int64_t tk_bufio_copyBytes(tk_slice_u8 *dst, tk_slice_u8 src) {
    tk_check_slice((tk_slice *)dst);
    tk_check_slice((tk_slice *)&src);
    int64_t n = dst->len < src.len ? dst->len : src.len;
    if (n > 0) {
        memmove(dst->ptr, src.ptr, (size_t)n);
    }

    return n;
}

// tk_bufio_copyString is tk_bufio_copyBytes from a string.
int64_t tk_bufio_copyString(tk_slice_u8 *dst, tk_string s) {
    tk_check_slice((tk_slice *)dst);
    tk_check_string(s);
    int64_t n = dst->len < s.len ? dst->len : s.len;
    if (n > 0) {
        memmove(dst->ptr, s.ptr, (size_t)n);
    }

    return n;
}

// tk_bufio_indexByte returns the index of the first c in p, or -1.
int64_t tk_bufio_indexByte(tk_slice_u8 p, uint8_t c) {
    tk_check_slice((tk_slice *)&p);
    if (p.len == 0) {
        return -1;
    }

    const uint8_t *at = memchr(p.ptr, c, (size_t)p.len);
    return at == NULL ? -1 : (int64_t)(at - (const uint8_t *)p.ptr);
}

// tk_bufio_appendBytes appends the bytes of p to b in one copy, growing
// b once.
void tk_bufio_appendBytes(tk_slice_u8 *buf, tk_slice_u8 p) {
    tk_slice *b = (tk_slice *)buf;
    tk_check_slice((tk_slice *)&p);
    if (p.len == 0) {
        return;
    }

    tk_slice_own(b, "append");
    tk_slice_grow(b, b->len + p.len, 1);
    memmove((uint8_t *)b->ptr + b->len, p.ptr, (size_t)p.len);
    b->len += p.len;
}

// tk_bufio_bytesOf lends the bytes of s as a view of a []byte, which
// the program only reads.
tk_slice_u8 tk_bufio_bytesOf(tk_string s) {
    tk_check_string(s);
    return (tk_slice_u8){(uint8_t *)s.ptr, s.len, TK_VIEW};
}

// tk_bufio_slide moves the bytes of b from lo up to hi to its start,
// where a buffer keeps what it has not handed out yet.
void tk_bufio_slide(tk_slice_u8 *buf, int64_t lo, int64_t hi) {
    tk_check_slice((tk_slice *)buf);
    if (lo < 0 || hi < lo || hi > buf->len) {
        tk_slice_bounds(lo, hi, buf->len);
    }

    if (lo > 0 && hi > lo) {
        memmove(buf->ptr, (uint8_t *)buf->ptr + lo, (size_t)(hi - lo));
    }
}
