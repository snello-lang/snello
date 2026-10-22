// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// print and eprint: a value's text to standard output or to standard
// error.

#include "internal.h"

void tk_print_string(tk_string s) {
    tk_check_string(s);
    tk_write_fd(1, s.ptr, s.len);
}

void tk_print_owned(tk_string s) {
    tk_print_string(s);
    tk_string_free(s);
}

void tk_print_int(int64_t v) {
    char buf[24];
    char *end = buf + sizeof buf, *p = tk_format_uint(v < 0 ? 0 - (uint64_t)v : (uint64_t)v, end);
    if (v < 0) {
        *--p = '-';
    }

    tk_write_fd(1, (const uint8_t *)p, end - p);
}

void tk_print_uint(uint64_t v) {
    char buf[24];
    char *end = buf + sizeof buf, *p = tk_format_uint(v, end);
    tk_write_fd(1, (const uint8_t *)p, end - p);
}

void tk_eprint_string(tk_string s) {
    tk_check_string(s);
    tk_write_fd(2, s.ptr, s.len);
}

void tk_eprint_owned(tk_string s) {
    tk_eprint_string(s);
    tk_string_free(s);
}
