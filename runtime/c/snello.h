// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The Snello runtime: the representations and operations the C
// emitter relies on. Every emitted program includes this header and is
// linked with the runtime's files. Names begin with sn_ so that no Snello
// identifier can collide with them.

#ifndef SNELLO_H
#define SNELLO_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>

typedef int64_t sn_int;
typedef uint64_t sn_uint;

// A string is a view of immutable UTF-8 bytes: a literal points at
// static data and owns nothing, an owned string points at bytes from
// sn_alloc and says so in cap, the size of that allocation. Dropping a
// string frees the bytes it owns.
typedef struct {
    const uint8_t *ptr;
    int64_t len;
    int64_t cap;
} sn_string;

#define SN_STR(s, n) ((sn_string){(const uint8_t *)(s), (n), 0})
#define SN_OWNED(s, n) sn_owned((s), (n))

// The unit value (), which C has no type for: a Result<(), E> or a
// tuple holds one of these.
typedef struct {
    char sn_pad;
} sn_unit;

#define SN_UNIT ((sn_unit){0})

// A function the program never calls is not an error, as it is not in
// Snello; every emitted function carries this.
#define SN_UNUSED __attribute__((unused))

// A slice of T is emitted as a struct of a T pointer, a length and a
// capacity, in this order, so every one has the shape of sn_slice and
// the operations below work on any of them given the element size.
typedef struct {
    void *ptr;
    int64_t len;
    int64_t cap;
} sn_slice;

// SN_VIEW is the capacity of a sub-slice, xs[lo:hi]: it lends the
// buffer of the slice it was cut from, so it may change its elements
// but not its length, and it is never freed.
#define SN_VIEW INT64_MIN

void *sn_alloc(size_t n);
void sn_free(void *p);

// sn_owned makes the string that owns the n bytes at p, a block from
// sn_alloc. A string of no bytes owns none, since a capacity of zero
// says a string owns nothing: the block is freed, and the empty
// literal returned.
static inline sn_string sn_owned(void *p, int64_t n) {
    if (n == 0) {
        sn_free(p);
        return SN_STR("", 0);
    }

    return (sn_string){(const uint8_t *)p, n, n};
}

// sn_heap_alloc and sn_heap_free are alloc.c's heap, under sn_alloc and
// sn_free in both builds; sn_heap_alloc gives nothing when the system
// is out of memory, and sn_heap_free takes nothing quietly.
void *sn_heap_alloc(size_t n);
void sn_heap_free(void *p);
_Noreturn void sn_panic(sn_string msg);
bool sn_catch(void (*fn)(void *), void *ctx, sn_string *msg); // runs fn under a mark: true with the message when a panic ended it (testing.Catch)

// The guards: the owned values whose drop runs a Drop method, which
// the emitted code registers where each is declared and removes where
// it is dropped, so that a panic can still run them, innermost first.
// A value is registered by its address, which a variable keeps for
// its whole life; a value that moved away leaves an empty place, whose
// drop does nothing.
void sn_guard(void *p, void (*drop)(void *));
void sn_unguard(void *p);

// A package variable whose initializer is a call is made once, on
// first use, under a lazy: its state is 0 before, 1 while the maker
// task runs the initializer, 2 after. The emitted code checks the
// state with one acquire load and calls sn_lazy_make when it is not 2,
// which makes the variable or waits for the task making it; a
// variable read by its own initializer is a panic naming it, and an
// initializer that panics under a mark leaves the variable unmade
// (_docs/design/03-ownership-across-tasks.md, package variables).
typedef struct sn_lazy {
    int state;
    struct sn_task *maker;
} sn_lazy;

static inline bool sn_lazy_ready(sn_lazy *o) {
    return __atomic_load_n(&o->state, __ATOMIC_ACQUIRE) == 2;
}

void sn_lazy_make(sn_lazy *o, void (*init)(void), const char *name);
_Noreturn void sn_exit(int code);
_Noreturn void sn_bounds(int64_t i, int64_t len);

// A []byte is the one slice type the runtime names, since the library's
// reading and writing functions take one; its layout is every slice's.
typedef struct sn_slice_u8 sn_slice_u8;
struct sn_slice_u8 {
    uint8_t *ptr;
    int64_t len;
    int64_t cap;
};

// A map keeps its entries in insertion order, which is the order a
// range visits them, and finds them through a hash index. The emitter
// gives each map type a sn_map_type naming the sizes, the hash and
// the equality of its keys. A map declared without a value is all
// zeros and works as an empty map.
typedef struct {
    void *keys;        // the entries' block: keys, then values, then hashes
    void *vals;
    uint64_t *hashes;  // one per entry: its key's hash, zero after Remove
    uint8_t *ctrl;     // the index's block: a control byte a slot, then the slots' entry numbers
    int64_t len;       // entries, emptied ones included
    int64_t count;     // live entries
    int64_t cap;       // room for entries
    int64_t slots;     // slots of the index, a power of two, eight or more
} sn_map;

// The functions of math/bits, which the C backend calls in place of the
// library's: each is the compiler's builtin, which is the processor's
// instruction, and every one is defined for zero, which the builtins
// that count zeros are not. A width under 64 is counted in a 64-bit
// word with a one set just past the value's bits, so that zero gives
// the width with no test. A rotation turns by the count modulo the
// width, a negative count to the right. A compiler with no builtin to
// reverse bits swaps the neighbours, the pairs and the nibbles and
// then the bytes, as Go's math/bits does.
#define SN_ALWAYS_INLINE static inline __attribute__((always_inline))

SN_ALWAYS_INLINE int64_t sn_bits_LeadingZeros64(uint64_t x) { return x == 0 ? 64 : __builtin_clzll(x); }
SN_ALWAYS_INLINE int64_t sn_bits_TrailingZeros64(uint64_t x) { return x == 0 ? 64 : __builtin_ctzll(x); }
SN_ALWAYS_INLINE int64_t sn_bits_OnesCount64(uint64_t x) { return __builtin_popcountll(x); }
SN_ALWAYS_INLINE int64_t sn_bits_Len64(uint64_t x) { return 64 - sn_bits_LeadingZeros64(x); }
SN_ALWAYS_INLINE uint64_t sn_bits_RotateLeft64(uint64_t x, int64_t k) {
    unsigned s = (unsigned)k & 63;
    return x << s | x >> ((64 - s) & 63);
}

#if defined(__has_builtin) // asked in a line of its own: a compiler without it cannot read the question
#if __has_builtin(__builtin_bitreverse64)
#define SN_BITREVERSE 1
#endif
#endif

SN_ALWAYS_INLINE uint64_t sn_bits_Reverse64(uint64_t x) {
#ifdef SN_BITREVERSE
    return __builtin_bitreverse64(x);
#else
    x = (x >> 1 & 0x5555555555555555u) | (x & 0x5555555555555555u) << 1;
    x = (x >> 2 & 0x3333333333333333u) | (x & 0x3333333333333333u) << 2;
    x = (x >> 4 & 0x0f0f0f0f0f0f0f0fu) | (x & 0x0f0f0f0f0f0f0f0fu) << 4;
    return __builtin_bswap64(x);
#endif
}

#define SN_BITS(W, T)                                                                                                              \
    SN_ALWAYS_INLINE int64_t sn_bits_LeadingZeros##W(T x) { return __builtin_clzll((uint64_t)x << (64 - W) | (uint64_t)1 << (63 - W)); } \
    SN_ALWAYS_INLINE int64_t sn_bits_TrailingZeros##W(T x) { return __builtin_ctzll((uint64_t)x | (uint64_t)1 << W); }               \
    SN_ALWAYS_INLINE int64_t sn_bits_OnesCount##W(T x) { return __builtin_popcountll(x); }                                           \
    SN_ALWAYS_INLINE int64_t sn_bits_Len##W(T x) { return W - sn_bits_LeadingZeros##W(x); }                                          \
    SN_ALWAYS_INLINE T sn_bits_Reverse##W(T x) { return (T)(sn_bits_Reverse64(x) >> (64 - W)); }                                     \
    SN_ALWAYS_INLINE T sn_bits_RotateLeft##W(T x, int64_t k) {                                                                       \
        unsigned s = (unsigned)k & (W - 1);                                                                                          \
        return (T)((T)(x << s) | x >> ((W - s) & (W - 1)));                                                                          \
    }

SN_BITS(8, uint8_t)
SN_BITS(16, uint16_t)
SN_BITS(32, uint32_t)
#undef SN_BITS

// The same functions under the library's names, for a program that
// keeps one as a value and calls it through the value.
#define SN_BITS_NAMED(S, W, T)                  \
    int64_t sn_math__bits_LeadingZeros##S(T x);  \
    int64_t sn_math__bits_TrailingZeros##S(T x); \
    int64_t sn_math__bits_OnesCount##S(T x);     \
    int64_t sn_math__bits_Len##S(T x);           \
    T sn_math__bits_Reverse##S(T x);             \
    T sn_math__bits_RotateLeft##S(T x, int64_t k);

SN_BITS_NAMED(, 64, uint64_t)
SN_BITS_NAMED(8, 8, uint8_t)
SN_BITS_NAMED(16, 16, uint16_t)
SN_BITS_NAMED(32, 32, uint32_t)
SN_BITS_NAMED(64, 64, uint64_t)
#undef SN_BITS_NAMED

// A debug build, snello build -debug, compiles the runtime with SN_DEBUG
// and makes a mistake of the compiler a stopped program instead of a
// quiet one: every block sn_alloc gives is counted, and a program that
// ends with blocks still live reports them and fails; a released block
// is filled with the poison byte and kept from reuse for a while, and
// a dropped place is filled with it too, so a string, slice, map,
// pointer or file read after its release is seen by the runtime
// function that reads it, which panics naming it; an owned pointer
// read after it moved, which leaves a null, panics the same way; and a
// task that ends is kept unfreed for a while, so that a wake that
// reaches it stops the program. A release build does none of this.
// The character classes of a byte, ASCII only, as a byte is one.
static inline bool sn_is_digit(uint8_t c) { return c >= '0' && c <= '9'; }
static inline bool sn_is_space(uint8_t c) { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'; }
static inline bool sn_is_upper(uint8_t c) { return c >= 'A' && c <= 'Z'; }
static inline bool sn_is_lower(uint8_t c) { return c >= 'a' && c <= 'z'; }
static inline bool sn_is_letter(uint8_t c) { return sn_is_upper(c) || sn_is_lower(c); }

#ifdef SN_DEBUG
#define SN_POISON(place) memset(&(place), 0xDD, sizeof(place))
#define SN_DEREF(p) (*(__typeof__(p))sn_deref(p))
_Noreturn void sn_released(const char *what);
void *sn_deref(void *p);
void sn_check_block(const void *p, const char *what);

// A poisoned length or capacity is negative, which no live value has;
// an owned string also points at a block, which must still be live.
// The block is what catches a borrow of a local that outlived it: the
// C compiler may drop the poisoning of a variable whose scope just
// ended, but never the poisoning of the heap.
static inline void sn_check_string(sn_string s) {
    if (s.len < 0 || s.cap < 0) {
        sn_released("string");
    }

    if (s.cap > 0) {
        sn_check_block(s.ptr, "string");
    }
}

static inline void sn_check_slice(const sn_slice *s) {
    if (s->len < 0 || (s->cap < 0 && s->cap != SN_VIEW)) {
        sn_released("slice");
    }
}

static inline void sn_check_len(int64_t len) {
    if (len < 0) {
        sn_released("slice");
    }
}

static inline void sn_check_map(const sn_map *m) {
    if (m->len < 0 || m->cap < 0) {
        sn_released("map");
    }

    if (m->keys != NULL) {
        sn_check_block(m->keys, "map");
    }
}

#else
#define SN_POISON(place) ((void)0)
#define SN_DEREF(p) (*(p))
#define sn_check_string(s) ((void)0)
#define sn_check_slice(s) ((void)0)
#define sn_check_len(len) ((void)0)
#define sn_check_map(m) ((void)0)
#endif

// sn_index checks an index against a length and returns it, so every
// element access is checked, as in Snello.
static inline int64_t sn_index(int64_t i, int64_t len) {
    sn_check_len(len);
    if ((uint64_t)i >= (uint64_t)len) {
        sn_bounds(i, len);
    }

    return i;
}

// sn_finish ends a program that returned from main: in a debug build
// it reports the blocks still live and fails the program when there
// are any.
int sn_finish(void);

// Drops: each releases what a value owns and is a no-op on a zero
// value, which is what a moved-from place holds, and leaves the place
// poisoned in a debug build. A file closes its descriptor, never one
// of the three standard streams.
static inline void sn_string_drop(sn_string *s) {
    sn_check_string(*s);
    if (s->cap > 0) {
        sn_free((void *)s->ptr);
    }

    SN_POISON(*s);
}

// sn_string_free is the drop of a string the runtime was given to
// consume, which has no place to poison.
static inline void sn_string_free(sn_string s) {
    sn_string_drop(&s);
}

// sn_slice_own panics, naming what, when s is a view; every operation
// that changes a slice's length or replaces its buffer asks it first,
// before it touches an element.
void sn_slice_own(const sn_slice *s, const char *what);
int64_t sn_slice_cap(const sn_slice *s); // a view's room is its length

// sn_utf8_decode reads the rune at p, at most n bytes, and returns its
// width: a byte that does not start a valid sequence is U+FFFD of width
// one, the one rule every part of the language reads a string by, a
// range over it included. sn_utf8_encode writes r at out and returns
// its length, U+FFFD for a value that is not a code point.
int sn_utf8_decode(const uint8_t *p, int64_t n, int32_t *r);
int sn_utf8_encode(int32_t r, uint8_t *out);

// sn_rune_upper and sn_rune_lower map a rune to its upper or lower
// case and sn_rune_space says whether it is white space, as Go's
// unicode.ToUpper, ToLower and IsSpace do, from the tables unicode.c
// holds.
_Noreturn void sn_slice_bounds(int64_t lo, int64_t hi, int64_t n);
void sn_strings_appendString(sn_slice_u8 *buf, sn_string s);
void sn_strings_reserve(sn_slice_u8 *buf, int64_t n);
int64_t sn_strings_copyString(sn_slice_u8 *dst, sn_string s);
sn_string sn_strings_repeat(sn_string s, int64_t n);
void sn_string_append(sn_string *s, const sn_string *x);
sn_string sn_strings_ownString(sn_slice_u8 b);
int64_t sn_bufio_copyBytes(sn_slice_u8 *dst, sn_slice_u8 src);
int64_t sn_bufio_copyString(sn_slice_u8 *dst, sn_string s);
int64_t sn_bufio_indexByte(sn_slice_u8 p, uint8_t c);
void sn_bufio_appendBytes(sn_slice_u8 *b, sn_slice_u8 p);
void sn_bufio_slide(sn_slice_u8 *b, int64_t lo, int64_t hi);
sn_slice_u8 sn_bufio_bytesOf(sn_string s);
int64_t sn_bytes_copyBytes(sn_slice_u8 *dst, sn_slice_u8 src);
int64_t sn_bytes_indexByte(sn_slice_u8 p, uint8_t c);
void sn_bytes_slide(sn_slice_u8 *buf, int64_t lo, int64_t hi);
void sn_bytes_appendString(sn_slice_u8 *buf, sn_string s);
void sn_bytes_reserve(sn_slice_u8 *buf, int64_t n);

int32_t sn_rune_upper(int32_t r);
int32_t sn_rune_lower(int32_t r);
bool sn_rune_space(int32_t r);

// sn_rune_letter, sn_rune_digit, sn_rune_is_upper and sn_rune_is_lower
// say whether a rune's general category is a letter, L, a decimal
// digit, Nd, an uppercase letter, Lu, or a lowercase letter, Ll, from
// the tables unicode.c holds; a value that is not a code point is none.
bool sn_rune_letter(int32_t r);
bool sn_rune_digit(int32_t r);
bool sn_rune_is_upper(int32_t r);
bool sn_rune_is_lower(int32_t r);

// The functions the unicode packages and strings declare without a
// body: the classes, mappings, categories and scripts of unicode and
// the decoding of unicode/utf8, in rune.c; full case mapping and
// folding on strings, in string.c; normalization, in norm.c; and text
// segmentation, in segment.c.
bool sn_unicode_IsLetter(int32_t r);
bool sn_unicode_IsDigit(int32_t r);
bool sn_unicode_IsSpace(int32_t r);
bool sn_unicode_IsUpper(int32_t r);
bool sn_unicode_IsLower(int32_t r);
bool sn_unicode_IsTitle(int32_t r);
bool sn_unicode_IsMark(int32_t r);
bool sn_unicode_IsNumber(int32_t r);
bool sn_unicode_IsPunct(int32_t r);
bool sn_unicode_IsSymbol(int32_t r);
bool sn_unicode_IsControl(int32_t r);
bool sn_unicode_IsGraphic(int32_t r);
bool sn_unicode_IsPrint(int32_t r);
int32_t sn_unicode_ToUpper(int32_t r);
int32_t sn_unicode_ToLower(int32_t r);
int32_t sn_unicode_ToTitle(int32_t r);
int32_t sn_unicode_CaseFold(int32_t r);
sn_string sn_unicode_Category(int32_t r);
sn_string sn_unicode_Script(int32_t r);
sn_string sn_unicode_ScriptCode(int32_t r);
bool sn_unicode_InScript(int32_t r, sn_string name);
bool sn_unicode_IsAlphabetic(int32_t r);
bool sn_unicode_IsEmoji(int32_t r);
bool sn_unicode_IsIDStart(int32_t r);
bool sn_unicode_IsIDContinue(int32_t r);
int64_t sn_unicode_digit(int32_t r);
bool sn_unicode_numeric(int32_t r, int64_t *num, int64_t *den);
int64_t sn_unicode__utf8_decode(sn_slice_u8 p, int32_t *r);
int64_t sn_unicode__utf8_decodeString(sn_string s, int32_t *r);
bool sn_unicode__utf8_valid(sn_slice_u8 p);
bool sn_unicode__utf8_validString(sn_string s);
int64_t sn_unicode__utf8_count(sn_slice_u8 p);
int64_t sn_unicode__utf8_countString(sn_string s);
bool sn_unicode__utf8_full(sn_slice_u8 p);
bool sn_unicode__utf8_fullString(sn_string s);
int64_t sn_unicode__utf8_decodeLast(sn_slice_u8 p, int32_t *r);
int64_t sn_unicode__utf8_decodeLastString(sn_string s, int32_t *r);
sn_string sn_strings_toTitle(sn_string s);
sn_string sn_strings_toValidUTF8(sn_string s, sn_string rep);
sn_string sn_string_from_rune(int32_t r);
sn_string sn_string_from_runes(sn_slice rs);
bool sn_strings_equalIgnoreCase(sn_string s, sn_string t);
sn_string sn_unicode__norm_normalize(sn_string s, int64_t form);
bool sn_unicode__norm_isNormal(sn_string s, int64_t form);
int64_t sn_unicode__segment_next(sn_string s, int64_t at, int64_t kind);

void sn_slice_free(sn_slice *s);
void sn_release(void **p); // frees the block an owned pointer holds and empties the pointer

void sn_init(int argc, char **argv);

void sn_slice_make(sn_slice *out, int64_t len, int64_t cap, size_t size);
void *sn_alloc_elems(int64_t n, size_t size);
// SN_PUSH_FAST and SN_POP_FAST say when the C backend's Push and Pop
// write in place, without the runtime: a push with room, and a pop of a
// slice that owns its elements and has one; a sub-slice's capacity,
// SN_VIEW, is below every length. A debug build always calls the
// runtime, for its checks.
#ifdef SN_DEBUG
#define SN_PUSH_FAST(s) 0
#define SN_POP_FAST(s) 0
#else
#define SN_PUSH_FAST(s) ((s).len < (s).cap)
#define SN_POP_FAST(s) ((s).len > 0 && (s).cap >= 0)
#endif
void sn_slice_push(sn_slice *s, const void *v, size_t size);
bool sn_slice_pop(sn_slice *s, void *out, size_t size);
void sn_slice_insert(sn_slice *s, int64_t i, const void *v, size_t size);
void sn_slice_remove(sn_slice *s, int64_t i, void *out, size_t size);
int64_t sn_slice_index_byte(const void *p, int64_t n, uint8_t c);
void sn_slice_reserve(sn_slice *s, int64_t need, size_t size, const char *what);
void sn_slice_reverse(sn_slice *s, size_t size);
void sn_slice_clone(const sn_slice *s, sn_slice *out, size_t size);
void sn_slice_sub(const sn_slice *s, int64_t lo, int64_t hi, sn_slice *out, size_t size);
void sn_slice_sort(sn_slice *s, size_t size, bool (*less)(void *ctx, const void *a, const void *b), void *ctx);

typedef struct {
    size_t ksize;
    size_t vsize;
    uint64_t (*hash)(const void *key);
    bool (*eq)(const void *a, const void *b);
    void (*kdrop)(void *key); // NULL when keys own nothing
    void (*vdrop)(void *val); // NULL when values own nothing
} sn_map_type;

void *sn_map_find(const sn_map *m, const sn_map_type *t, const void *key);
const void *sn_map_get(const sn_map *m, const sn_map_type *t, const void *key, const void *zero);
// sn_map_slot returns the value's place for key, making the entry when
// the map has none, its value zero: added is then the place of the key
// the entry keeps, a copy of key's bytes, and NULL when the entry was
// there. One hash and one probe either way.
void *sn_map_slot(sn_map *m, const sn_map_type *t, const void *key, void **added);
void sn_map_reserve(sn_map *m, const sn_map_type *t, int64_t n);
bool sn_map_remove(sn_map *m, const sn_map_type *t, const void *key, void *out);
void sn_map_clone(const sn_map *m, const sn_map_type *t, sn_map *out);
void sn_map_free(sn_map *m, const sn_map_type *t);
int64_t sn_map_next(const sn_map *m, int64_t i);
void sn_hash_init(void);
uint64_t sn_hash_bytes(const void *p, size_t n);
uint64_t sn_hash_mix(uint64_t h, uint64_t v);

// sn_hash_string hashes a map key, checked first in a debug build, as
// a poisoned length would be read as an enormous one.
static inline uint64_t sn_hash_string(sn_string s) {
    sn_check_string(s);
    return sn_hash_bytes(s.ptr, (size_t)s.len);
}

sn_string sn_string_clone(sn_string s);
sn_string sn_substr(sn_string s, int64_t lo, int64_t hi);
sn_string sn_to_upper(sn_string s);
sn_string sn_to_lower(sn_string s);
sn_string sn_trim(sn_string s);
bool sn_has_prefix(sn_string s, sn_string t);
bool sn_has_suffix(sn_string s, sn_string t);
bool sn_string_contains(sn_string s, sn_string t);
int64_t sn_string_index(sn_string s, sn_string t);
int64_t sn_string_last_index(sn_string s, sn_string t);
int64_t sn_string_count(sn_string s, sn_string t);
sn_string sn_string_replace(sn_string s, sn_string old, sn_string new, int64_t n);
sn_string sn_trim_prefix(sn_string s, sn_string p);
sn_string sn_trim_suffix(sn_string s, sn_string p);
void sn_split_n(sn_string s, sn_string sep, int64_t n, sn_slice *out);
bool sn_bytes_equal(sn_slice_u8 a, sn_slice_u8 b);
int64_t sn_bytes_find(sn_slice_u8 a, sn_slice_u8 b);
void sn_fields(sn_string s, sn_slice *out);
void sn_lines(sn_string s, sn_slice *out);
void sn_runes(sn_string s, sn_slice *out);
void sn_split(sn_string s, sn_string sep, sn_slice *out);
bool sn_split_once(sn_string s, sn_string sep, sn_string *before, sn_string *after);

sn_string sn_concat(sn_string a, sn_string b);
sn_string sn_join(sn_string a, sn_string b, int owned); // concatenates, then drops the inputs owned says it may: 1 for a, 2 for b
bool sn_string_eq(sn_string a, sn_string b);
int sn_string_cmp(sn_string a, sn_string b);
sn_string sn_int_to_string(int64_t v);
sn_string sn_uint_to_string(uint64_t v);
sn_string sn_f64_to_string(double v);
sn_string sn_f32_to_string(float v);
sn_string sn_f64_g(double v, int64_t prec);
sn_string sn_f64_fixed(double v, int64_t prec);
sn_string sn_f64_e(double v, int64_t prec);
sn_string sn_int_base(int64_t v, int64_t base, bool upper);
sn_string sn_uint_base(uint64_t v, int64_t base, bool upper);
sn_string sn_char(int64_t r);
sn_string sn_hex(sn_string s, bool upper, bool owned);
sn_string sn_hex_bytes(sn_slice b, bool upper);
sn_string sn_pad_zero(sn_string s, int64_t width, bool owned);
sn_string sn_bool_to_string(bool v);
sn_string sn_quote(sn_string s);
sn_string sn_quote_owned(sn_string s);
sn_string sn_quote_rune(int64_t r);
sn_string sn_pad(sn_string s, int64_t width, bool left, bool owned);
sn_string sn_int_digits(sn_string s, int64_t prec);
sn_string sn_truncate(sn_string s, int64_t prec, bool owned);
double sn_f64_min(double a, double b);
double sn_f64_max(double a, double b);
bool sn_isprint(int32_t r); // a letter, mark, number, punctuation or symbol, or the ASCII space: printable as strconv.IsPrint defines it

// A string and a []byte have one layout, so a conversion copies the
// bytes into a new owned value of the other kind.
static inline sn_string sn_bytes_to_string(sn_slice_u8 b) {
    return sn_string_clone((sn_string){b.ptr, b.len, 0});
}

static inline sn_slice_u8 sn_string_to_bytes(sn_string s) {
    sn_string c = sn_string_clone(s);
    if (c.cap == 0) {
        return (sn_slice_u8){0}; // no bytes: an empty slice, not a view of static data
    }

    return (sn_slice_u8){(uint8_t *)c.ptr, c.len, c.cap};
}

// Printing: fmt.Println(a, b) builds the line, a, separator, b and
// newline, and writes it to standard output at once, as Go's does.
void sn_print_string(sn_string s);
void sn_print_owned(sn_string s); // prints, then drops s
void sn_print_int(int64_t v);
void sn_print_uint(uint64_t v);
void sn_eprint_string(sn_string s);
void sn_eprint_owned(sn_string s);

// The functions and variables the standard library declares without a
// body or a value, under the names the compiler gives them: the
// package's import path and the name. The os functions return the
// system's error number, zero on success, and store their result
// through their last parameter.
double sn_math_Sqrt(double x);
double sn_math_Cbrt(double x);
double sn_math_Floor(double x);
double sn_math_Ceil(double x);
double sn_math_Trunc(double x);
double sn_math_Round(double x);
double sn_math_Mod(double x, double y);
double sn_math_Pow(double x, double y);
double sn_math_Exp(double x);
double sn_math_Log(double x);
double sn_math_Log2(double x);
double sn_math_Log10(double x);
double sn_math_Sin(double x);
double sn_math_Cos(double x);
double sn_math_Tan(double x);
double sn_math_Asin(double x);
double sn_math_Acos(double x);
double sn_math_Atan(double x);
double sn_math_Atan2(double y, double x);
double sn_math_Sinh(double x);
double sn_math_Cosh(double x);
double sn_math_Tanh(double x);
double sn_math_Hypot(double p, double q);
double sn_math_Inf(int64_t sign);
double sn_math_NaN(void);
bool sn_math_IsNaN(double x);
bool sn_math_IsInf(double x, int64_t sign);
bool sn_math_Signbit(double x);
double sn_math_Copysign(double x, double sign);
uint64_t sn_math__rand_random(void);
uint64_t sn_math__rand_mul64(uint64_t x, uint64_t y, uint64_t *lo);
sn_string sn_io_reason(int64_t code);
int64_t sn_io_kindOf(int64_t code);
extern sn_slice sn_os_Args; // the command line, as a slice of sn_string
int64_t sn_os_open(sn_string path, int64_t *fd);
int64_t sn_os_create(sn_string path, int64_t *fd);
int64_t sn_os_readToString(sn_string path, sn_string *out, int64_t *inRead);
int64_t sn_os_mkdirTemp(sn_string dir, sn_string pattern, sn_string *out, sn_string *op);
int64_t sn_os_removeAll(sn_string path, sn_string *failed, sn_string *op);
int64_t sn_os_remove(sn_string path);
int64_t sn_os_Cores(void);
int64_t sn_testing_task(void); // the running task's identity, for the test runner's state per test
void sn_testing_benchStart(int64_t n); // the benchmark's state, one at a time; see testing.c
bool sn_testing_benchLoop(void);
void sn_testing_benchReset(void);
void sn_testing_benchReport(void);
int64_t sn_testing_benchNanos(void);
int64_t sn_testing_benchAllocs(void);
int64_t sn_testing_benchBytes(void);
bool sn_testing_benchReported(void);
void sn_testing_line(sn_string where);                  // the compiler writes these before a call of testing.Log, Logf, Skip, and of an exported function of expect
void sn_testing_call(sn_string where, sn_string parts); // the place of the call, with the parts the compiler found, owned
sn_string sn_testing_where(void);                        // testing.where, expect.where and expect.parts read them back
sn_string sn_expect_where(void);
sn_string sn_expect_parts(void);
int64_t sn_os_read(int64_t fd, sn_slice_u8 *buf, int64_t *n);
int64_t sn_os_write(int64_t fd, sn_slice_u8 buf, int64_t *n);
int64_t sn_os_writeString(int64_t fd, sn_string s, int64_t *n);
int64_t sn_os_syncFile(int64_t fd);
int64_t sn_os_closeFile(int64_t fd);
int64_t sn_os_seek(int64_t fd, int64_t whence, int64_t off, uint64_t *at);
int64_t sn_os_readFile(sn_string path, sn_slice_u8 *out, int64_t *inRead);
int64_t sn_os_stat(sn_string path, bool follow, int64_t *size, uint32_t *mode, int64_t *sec, int64_t *nsec, uint64_t *dev, uint64_t *ino);
int64_t sn_os_mkdir(sn_string path, uint32_t perm);
int64_t sn_os_rename(sn_string from, sn_string to);
int64_t sn_os_chdir(sn_string path);
int64_t sn_os_getcwd(sn_string *out);
int64_t sn_os_openFile(sn_string path, int64_t flag, uint32_t perm, int64_t *fd);
int64_t sn_os_readDir(sn_string path, sn_slice_u8 *out, sn_string *op);
int64_t sn_os_Getpid(void);
int64_t sn_os_hostname(sn_string *out);
bool sn_os_lookupEnv(sn_string key, sn_string *value);
int64_t sn_os_setenv(sn_string key, sn_string value);
void sn_os_Unsetenv(sn_string key);
sn_string sn_os_TempDir(void);
void sn_os_environ(void *out); // a []string that holds nothing
void sn_os_env_free(void); // the copy of the environment, released at the end of a debug build
void sn_io_reserve(sn_slice_u8 *buf, int64_t n);
int64_t sn_strconv_parseFloat(sn_string s, double *out);
int64_t sn_strconv_parseFloat32(sn_string s, float *out);
sn_string sn_strconv_unquote(sn_string s, bool *ok);
sn_string sn_strconv_formatBits(uint64_t u, int64_t base, bool neg);
sn_string sn_strconv_formatFixed(double v, int64_t prec);
int64_t sn_time_now(void);
void sn_time_Sleep(int64_t d);

// A time.Time as the runtime makes one, field for field the struct of
// package time: the wall clock's seconds since the start of 1970 in UTC
// and the nanoseconds past them, and the monotonic clock's reading, in
// nanoseconds, that durations between two Nows are measured on.
typedef struct {
    int64_t sec;
    int64_t nsec;
    int64_t mono;
} sn_time;

sn_time sn_time_read(void);
void sn_time_clock(int64_t *sec, int64_t *nsec, int64_t *mono); // package time's clock
sn_string sn_time_format(sn_string layout, int64_t sec, int64_t nsec);
int64_t sn_time_parse(sn_string layout, sn_string value, int64_t *year, int64_t *month, int64_t *day, int64_t *clock, int64_t *nsec, int64_t *offset, int64_t *weekday, int64_t *at, int64_t *li);
int64_t sn_time_mono(void); // the monotonic clock, in nanoseconds, never 0
void sn_outside(int64_t n); // wakes promised by a thread that is not a worker; see sched.c
int64_t sn_sync_newLock(void); // a sync lock's handle is its address; see the lock section of snello.c
int64_t sn_sync_newRwLock(void);
void sn_sync_freeLock(int64_t h);
void sn_sync_lock(int64_t h);
void sn_sync_unlock(int64_t h);
void sn_sync_lockShared(int64_t h);
void sn_sync_unlockShared(int64_t h);
bool sn_sync_count(int64_t h, int64_t n);
void sn_sync_wait(int64_t h);
int64_t sn_sync_atomicAdd(int64_t *n, int64_t d);
int64_t sn_sync_cellNew(int64_t v); // a Counter's word, named by its handle, as a lock is; see sync.c
int64_t sn_sync_cellLoad(int64_t h);
void sn_sync_cellStore(int64_t h, int64_t v);
int64_t sn_sync_cellAdd(int64_t h, int64_t d);
bool sn_sync_cellSwap(int64_t h, int64_t old, int64_t v);
int64_t sn_sync_cellExchange(int64_t h, int64_t v);
void sn_sync_cellFree(int64_t h);
int64_t sn_net_listen(sn_string host, int64_t port, int64_t *fd, int64_t *got, sn_string *call); // the net package's, in net.c
int64_t sn_net_accept(int64_t fd, int64_t *out);
int64_t sn_net_dial(sn_string host, int64_t port, int64_t timeout, int64_t *fd, sn_string *call);
int64_t sn_net_setDeadline(int64_t fd, int64_t write, int64_t d, int64_t set);
int64_t sn_net_addrs(int64_t fd, sn_string *local, sn_string *remote);
int64_t sn_net_read(int64_t fd, sn_slice_u8 *buf, int64_t *n);
int64_t sn_net_write(int64_t fd, sn_slice_u8 buf, int64_t *n);
int64_t sn_net_closeConn(int64_t fd);
int64_t sn_net_lookup(sn_string host, int64_t timeout, void *out, sn_string *reason); // in lookup.c

// Tasks and channels, as _docs/design/06-tasks-and-channels.md lays
// them out: this is the whole interface the emitted code uses, and the
// scheduler of a later stage implements the same one. A task is a
// thread here; a channel is a locked ring buffer, or a hand-off slot
// when unbuffered, with a list of the tasks waiting on it and a count
// of its handles: sn_chan_clone gives another, sn_chan_drop takes one
// away and empties its place, and the last frees the channel, with the
// values still in it dropped by the drop sn_chan_make was given, NULL
// for an element that owns nothing. A value is copied into the channel
// by its bytes and out again: the sender's place was emptied by the
// move, and the receiver owns what it took. sn_chan_recv returns false
// once the channel is closed and empty, which the emitted code turns
// into None.
typedef struct sn_chan sn_chan;

// A case of a select: the channel, the value to send or the place to
// receive into, and for a receive whether a value came, false once the
// channel is closed and empty. sn_select waits for a case that can
// proceed, performs it and returns its index, choosing at random among
// several; with a default it returns -1 at once when none can.
typedef struct {
    sn_chan *chan;
    void *value;
    bool send;
    bool ok;
} sn_case;

void sn_spawn(void (*run)(void *), void *arg);
int sn_select(sn_case *cases, int n, bool has_default);
int64_t sn_tasks_running(void);
void sn_timers_stop(void);
bool sn_timer_abandon(sn_chan *c);
int64_t sn_chan_timer(sn_chan *c);
bool sn_chan_alone(sn_chan *c);
bool sn_chan_release(sn_chan *c);
void sn_chan_placeTimer(sn_chan *c, int64_t at);

// The scheduler's own interface (sched.c), which the channels, the
// locks and the timers in snello.c are written over and the emitted
// code never calls: the running task, parking it until a wake, waking
// one, and parking it for a while.
typedef struct sn_task sn_task;
sn_task *sn_self(void);
void sn_park(void);
void sn_wake(sn_task *t);
void sn_sleep(int64_t d);
sn_chan *sn_chan_make(size_t size, int64_t cap, void (*drop)(void *));
sn_chan *sn_chan_clone(sn_chan *c);
void sn_chan_drop(sn_chan **c);
void sn_chan_send(sn_chan *c, const void *v);
bool sn_chan_offer(sn_chan *c, const void *v);
void sn_time_startTimer(sn_chan *ch, int64_t d, int64_t every);
bool sn_time_stopTimer(sn_chan *ch);
bool sn_time_resetTimer(sn_chan *ch, int64_t d, int64_t every);
bool sn_chan_poll(sn_chan *c, void *out);
bool sn_chan_recv(sn_chan *c, void *out);
void sn_chan_close(sn_chan *c);
int64_t sn_chan_len(sn_chan *c);
int64_t sn_chan_cap(sn_chan *c);

#endif
