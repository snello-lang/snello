// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// print and eprint: a value's text to standard output or to standard
// error.

#include "internal.h"

void sn_print_string(sn_string s) {
    sn_check_string(s);
    sn_write_fd(1, s.ptr, s.len);
}

void sn_print_owned(sn_string s) {
    sn_print_string(s);
    sn_string_free(s);
}

void sn_print_int(int64_t v) {
    char buf[24];
    char *end = buf + sizeof buf, *p = sn_format_uint(v < 0 ? 0 - (uint64_t)v : (uint64_t)v, end);
    if (v < 0) {
        *--p = '-';
    }

    sn_write_fd(1, (const uint8_t *)p, end - p);
}

void sn_print_uint(uint64_t v) {
    char buf[24];
    char *end = buf + sizeof buf, *p = sn_format_uint(v, end);
    sn_write_fd(1, (const uint8_t *)p, end - p);
}

void sn_eprint_string(sn_string s) {
    sn_check_string(s);
    sn_write_fd(2, s.ptr, s.len);
}

void sn_eprint_owned(sn_string s) {
    sn_eprint_string(s);
    sn_string_free(s);
}
