// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The C side of package sysname on Linux: the kernel's name from
// /proc/sys/kernel/ostype, which ends in a newline.

#include "sysname.snello.h"

#include <stdio.h>
#include <string.h>

bool snm_sysname_isKernel(sn_string name) {
    FILE *f = fopen("/proc/sys/kernel/ostype", "r");
    if (f == NULL) {
        return false;
    }

    char buf[64];
    size_t len = fread(buf, 1, sizeof buf, f);
    fclose(f);
    if (len > 0 && buf[len - 1] == '\n') {
        len--;
    }

    return len == (size_t)name.len && memcmp(buf, name.ptr, len) == 0;
}
