// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The C side of package sysname on Windows: the kernel's platform from
// RtlGetVersion, ntdll's, which says Windows NT as VER_PLATFORM_WIN32_NT.
// No import library names the call, so it is looked up in ntdll, which
// every program has loaded.

#include "sysname.snello.h"

#define WIN32_LEAN_AND_MEAN
#include <windows.h>

#include <string.h>

typedef LONG(WINAPI *rtl_get_version)(OSVERSIONINFOW *);

bool snm_sysname_isKernel(sn_string name) {
    rtl_get_version get = (rtl_get_version)(void *)GetProcAddress(GetModuleHandleW(L"ntdll.dll"), "RtlGetVersion");
    OSVERSIONINFOW info = { .dwOSVersionInfoSize = sizeof info };
    if (get == NULL || get(&info) != 0 || info.dwPlatformId != VER_PLATFORM_WIN32_NT) {
        return false;
    }

    const char *kernel = "Windows_NT";
    return strlen(kernel) == (size_t)name.len && memcmp(kernel, name.ptr, (size_t)name.len) == 0;
}
