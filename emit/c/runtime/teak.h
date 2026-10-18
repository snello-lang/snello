// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The Teak runtime, stage 1: the representations and operations the C
// emitter relies on. Every emitted program includes this header and is
// linked with teak.c. Names begin with tk_ so that no Teak identifier
// can collide with them.

#ifndef TEAK_H
#define TEAK_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

typedef int64_t tk_int;
typedef uint64_t tk_uint;

// A string is a view of immutable UTF-8 bytes: a literal points at
// static data, an owned string at bytes from tk_alloc. Stage 1 frees
// nothing; task 1.9 adds the releases the checker computes.
typedef struct {
    const uint8_t *ptr;
    int64_t len;
} tk_string;

#define TK_STR(s, n) ((tk_string){(const uint8_t *)(s), (n)})

// The unit value (), which C has no type for: a Result<(), E> or a
// tuple holds one of these.
typedef struct {
    char tk_pad;
} tk_unit;

#define TK_UNIT ((tk_unit){0})

// A function the program never calls is not an error, as it is not in
// Teak; every emitted function carries this.
#define TK_UNUSED __attribute__((unused))

void *tk_alloc(size_t n);
_Noreturn void tk_panic(tk_string msg);
_Noreturn void tk_exit(int code);

tk_string tk_concat(tk_string a, tk_string b);
bool tk_string_eq(tk_string a, tk_string b);
int tk_string_cmp(tk_string a, tk_string b);
tk_string tk_int_to_string(int64_t v);
tk_string tk_uint_to_string(uint64_t v);
tk_string tk_f64_to_string(double v);
tk_string tk_bool_to_string(bool v);
tk_string tk_quote(tk_string s);

// Printing: fmt.Println(a, b) is print a, separator, print b, newline.
void tk_print_string(tk_string s);
void tk_print_int(int64_t v);
void tk_print_uint(uint64_t v);
void tk_print_f64(double v);
void tk_print_bool(bool v);
void tk_print_sep(void);
void tk_print_nl(void);
void tk_eprint_string(tk_string s);

#endif
