// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The tables of a panic's trace as a program that carries none has
// them, which the program's own, written by the compiler, replace at
// the link: the runtime's probes and a C program of its own link with
// these. This file is compiled to native code whatever the build's
// optimization, so that the link, and not the optimizer, binds the
// runtime's reads of the tables: optimized as one with the runtime,
// these definitions would be folded into its code, and the program's
// tables, linked after, would answer nothing.

#include "snello.h"

__attribute__((weak)) const sn_trace_table sn_trace = { SN_TRACE_UNWRITTEN, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0 };
__attribute__((
    weak)) uint8_t sn_trace_parts[1] = { 0 }; // defined, not tentative, so that the program's replaces it whole
__attribute__((weak)) const sn_trace_name sn_trace_names[1];
__attribute__((weak)) const int64_t sn_trace_names_count = 0;
