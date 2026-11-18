// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The C side of package sysname on macOS: the kernel's name from
// sysctl, kern.ostype.

#include "sysname.snello.h"

#include <string.h>
#include <sys/sysctl.h>

bool snm_sysname_isKernel(sn_string name) {
    char buf[64];
    size_t len = sizeof buf;
    if (sysctlbyname("kern.ostype", buf, &len, NULL, 0) != 0) {
        return false;
    }

    return strlen(buf) == (size_t)name.len && memcmp(buf, name.ptr, (size_t)name.len) == 0;
}
