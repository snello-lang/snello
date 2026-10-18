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

// A slice of T is emitted as a struct of a T pointer, a length and a
// capacity, in this order, so every one has the shape of tk_slice and
// the operations below work on any of them given the element size.
typedef struct {
    void *ptr;
    int64_t len;
    int64_t cap;
} tk_slice;

void *tk_alloc(size_t n);
_Noreturn void tk_panic(tk_string msg);
_Noreturn void tk_exit(int code);
_Noreturn void tk_bounds(int64_t i, int64_t len);

// tk_index checks an index against a length and returns it, so every
// element access is checked, as in Teak.
static inline int64_t tk_index(int64_t i, int64_t len) {
    if ((uint64_t)i >= (uint64_t)len) {
        tk_bounds(i, len);
    }

    return i;
}

void tk_slice_make(tk_slice *out, int64_t len, int64_t cap, size_t size);
void tk_slice_push(tk_slice *s, const void *v, size_t size);
bool tk_slice_pop(tk_slice *s, void *out, size_t size);
void tk_slice_insert(tk_slice *s, int64_t i, const void *v, size_t size);
void tk_slice_remove(tk_slice *s, int64_t i, void *out, size_t size);
void tk_slice_reverse(tk_slice *s, size_t size);
void tk_slice_clone(const tk_slice *s, tk_slice *out, size_t size);
void tk_slice_sub(const tk_slice *s, int64_t lo, int64_t hi, tk_slice *out, size_t size);

tk_string tk_string_clone(tk_string s);
tk_string tk_substr(tk_string s, int64_t lo, int64_t hi);
tk_string tk_to_upper(tk_string s);
tk_string tk_to_lower(tk_string s);
tk_string tk_trim(tk_string s);
bool tk_has_prefix(tk_string s, tk_string t);
bool tk_has_suffix(tk_string s, tk_string t);
bool tk_string_contains(tk_string s, tk_string t);
void tk_fields(tk_string s, tk_slice *out);
void tk_split(tk_string s, tk_string sep, tk_slice *out);
bool tk_split_once(tk_string s, tk_string sep, tk_string *before, tk_string *after);

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
