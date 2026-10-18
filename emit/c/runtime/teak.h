// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The Teak runtime: the representations and operations the C
// emitter relies on. Every emitted program includes this header and is
// linked with teak.c. Names begin with tk_ so that no Teak identifier
// can collide with them.

#ifndef TEAK_H
#define TEAK_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>

typedef int64_t tk_int;
typedef uint64_t tk_uint;

// A string is a view of immutable UTF-8 bytes: a literal points at
// static data and owns nothing, an owned string points at bytes from
// tk_alloc and says so in cap, the size of that allocation. Dropping a
// string frees the bytes it owns.
typedef struct {
    const uint8_t *ptr;
    int64_t len;
    int64_t cap;
} tk_string;

#define TK_STR(s, n) ((tk_string){(const uint8_t *)(s), (n), 0})
#define TK_OWNED(s, n) ((tk_string){(const uint8_t *)(s), (n), (n)})

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
void tk_free(void *p);
_Noreturn void tk_panic(tk_string msg);

// The guards: the owned values whose drop runs a Drop method, which
// the emitted code registers where each is declared and removes where
// it is dropped, so that a panic can still run them, innermost first.
// A value is registered by its address, which a variable keeps for
// its whole life; a value that moved away leaves an empty place, whose
// drop does nothing.
void tk_guard(void *p, void (*drop)(void *));
void tk_unguard(void *p);
_Noreturn void tk_exit(int code);
_Noreturn void tk_bounds(int64_t i, int64_t len);

// An error is its message for now, and an empty message is no
// error: the os and io functions return one and store their result
// through a pointer. A file is a descriptor; the three standard streams
// are files too.
typedef struct {
    int fd;
} tk_file;

// A map keeps its entries in insertion order, which is the order a
// range visits them, and finds them through a hash index. The emitter
// gives each map type a tk_map_type naming the sizes, the hash and
// the equality of its keys. A map declared without a value is all
// zeros and works as an empty map.
typedef struct {
    void *keys;
    void *vals;
    uint8_t *live;  // one per entry: false after Remove
    int64_t *index; // hash slots holding entry positions plus one; zero is empty
    int64_t len;    // entries, dead ones included
    int64_t count;  // live entries
    int64_t cap;    // room for entries
    int64_t slots;  // size of the index, a power of two
} tk_map;

// A debug build, teak build -debug, compiles the runtime with TK_DEBUG
// and makes a mistake of the compiler a stopped program instead of a
// quiet one: every block tk_alloc gives is counted, and a program that
// ends with blocks still live reports them and fails; a released block
// is filled with the poison byte and kept from reuse for a while, and
// a dropped place is filled with it too, so a string, slice, map,
// pointer or file read after its release is seen by the runtime
// function that reads it, which panics naming it; an owned pointer
// read after it moved, which leaves a null, panics the same way. A
// release build does none of this.
#ifdef TK_DEBUG
#define TK_POISON(place) memset(&(place), 0xDD, sizeof(place))
#define TK_DEREF(p) (*(__typeof__(p))tk_deref(p))
_Noreturn void tk_released(const char *what);
void *tk_deref(void *p);
void tk_check_block(const void *p, const char *what);

// A poisoned length or capacity is negative, which no live value has;
// an owned string also points at a block, which must still be live.
// The block is what catches a borrow of a local that outlived it: the
// C compiler may drop the poisoning of a variable whose scope just
// ended, but never the poisoning of the heap.
static inline void tk_check_string(tk_string s) {
    if (s.len < 0 || s.cap < 0) {
        tk_released("string");
    }

    if (s.cap > 0) {
        tk_check_block(s.ptr, "string");
    }
}

static inline void tk_check_slice(const tk_slice *s) {
    if (s->len < 0 || s->cap < 0) {
        tk_released("slice");
    }
}

static inline void tk_check_len(int64_t len) {
    if (len < 0) {
        tk_released("slice");
    }
}

static inline void tk_check_map(const tk_map *m) {
    if (m->len < 0 || m->cap < 0) {
        tk_released("map");
    }

    if (m->keys != NULL) {
        tk_check_block(m->keys, "map");
    }
}

static inline void tk_check_file(tk_file f) {
    if (f.fd == (int)0xDDDDDDDD) {
        tk_released("file");
    }
}
#else
#define TK_POISON(place) ((void)0)
#define TK_DEREF(p) (*(p))
#define tk_check_string(s) ((void)0)
#define tk_check_slice(s) ((void)0)
#define tk_check_len(len) ((void)0)
#define tk_check_map(m) ((void)0)
#define tk_check_file(f) ((void)0)
#endif

// tk_index checks an index against a length and returns it, so every
// element access is checked, as in Teak.
static inline int64_t tk_index(int64_t i, int64_t len) {
    tk_check_len(len);
    if ((uint64_t)i >= (uint64_t)len) {
        tk_bounds(i, len);
    }

    return i;
}

// tk_finish ends a program that returned from main: in a debug build
// it reports the blocks still live and fails the program when there
// are any.
int tk_finish(void);

// Drops: each releases what a value owns and is a no-op on a zero
// value, which is what a moved-from place holds, and leaves the place
// poisoned in a debug build. A file closes its descriptor, never one
// of the three standard streams.
static inline void tk_string_drop(tk_string *s) {
    tk_check_string(*s);
    if (s->cap > 0) {
        tk_free((void *)s->ptr);
    }

    TK_POISON(*s);
}

// tk_string_free is the drop of a string the runtime was given to
// consume, which has no place to poison.
static inline void tk_string_free(tk_string s) {
    tk_string_drop(&s);
}

void tk_file_drop(tk_file *f);
void tk_slice_free(tk_slice *s);
void tk_release(void **p); // frees the block an owned pointer holds and empties the pointer

extern tk_file tk_stdin, tk_stdout, tk_stderr;
extern tk_slice tk_args; // the command line, as a slice of tk_string

void tk_init(int argc, char **argv);
tk_string tk_read_to_string(tk_string path, tk_string *out);
tk_string tk_open(tk_string path, tk_file *out);
tk_string tk_create(tk_string path, tk_file *out);
tk_string tk_mkdir_temp(tk_string dir, tk_string pattern, tk_string *out);
tk_string tk_remove_all(tk_string path);
tk_string tk_file_close(tk_file f);
tk_string tk_file_sync(tk_file *f);
tk_string tk_file_write_string(tk_file *f, tk_string s, int64_t *n);
tk_string tk_io_copy(tk_file *dst, tk_file *src, int64_t *n);

void tk_slice_make(tk_slice *out, int64_t len, int64_t cap, size_t size);
void tk_slice_push(tk_slice *s, const void *v, size_t size);
bool tk_slice_pop(tk_slice *s, void *out, size_t size);
void tk_slice_insert(tk_slice *s, int64_t i, const void *v, size_t size);
void tk_slice_remove(tk_slice *s, int64_t i, void *out, size_t size);
void tk_slice_reverse(tk_slice *s, size_t size);
void tk_slice_clone(const tk_slice *s, tk_slice *out, size_t size);
void tk_slice_sub(const tk_slice *s, int64_t lo, int64_t hi, tk_slice *out, size_t size);
void tk_slice_sort(tk_slice *s, size_t size, bool (*less)(void *ctx, const void *a, const void *b), void *ctx);

typedef struct {
    size_t ksize;
    size_t vsize;
    uint64_t (*hash)(const void *key);
    bool (*eq)(const void *a, const void *b);
    void (*kdrop)(void *key); // NULL when keys own nothing
    void (*vdrop)(void *val); // NULL when values own nothing
} tk_map_type;

void *tk_map_find(const tk_map *m, const tk_map_type *t, const void *key);
const void *tk_map_get(const tk_map *m, const tk_map_type *t, const void *key, const void *zero);
void *tk_map_slot(tk_map *m, const tk_map_type *t, const void *key);
bool tk_map_remove(tk_map *m, const tk_map_type *t, const void *key, void *out);
void tk_map_clone(const tk_map *m, const tk_map_type *t, tk_map *out);
void tk_map_free(tk_map *m, const tk_map_type *t);
int64_t tk_map_next(const tk_map *m, int64_t i);
uint64_t tk_hash_bytes(const void *p, size_t n);
uint64_t tk_hash_mix(uint64_t h, uint64_t v);

// tk_hash_string hashes a map key, checked first in a debug build, as
// a poisoned length would be read as an enormous one.
static inline uint64_t tk_hash_string(tk_string s) {
    tk_check_string(s);
    return tk_hash_bytes(s.ptr, (size_t)s.len);
}

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
tk_string tk_join(tk_string a, tk_string b, int owned); // concatenates, then drops the inputs owned says it may: 1 for a, 2 for b
bool tk_string_eq(tk_string a, tk_string b);
int tk_string_cmp(tk_string a, tk_string b);
tk_string tk_int_to_string(int64_t v);
tk_string tk_uint_to_string(uint64_t v);
tk_string tk_f64_to_string(double v);
tk_string tk_bool_to_string(bool v);
tk_string tk_quote(tk_string s);

// Printing: fmt.Println(a, b) is print a, separator, print b, newline.
void tk_print_string(tk_string s);
void tk_print_owned(tk_string s); // prints, then drops s
void tk_print_int(int64_t v);
void tk_print_uint(uint64_t v);
void tk_print_f64(double v);
void tk_print_bool(bool v);
void tk_print_sep(void);
void tk_print_nl(void);
void tk_eprint_string(tk_string s);
void tk_eprint_owned(tk_string s);

#endif
