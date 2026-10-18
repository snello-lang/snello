// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#define _POSIX_C_SOURCE 200809L
#define _DARWIN_C_SOURCE

#include "teak.h"

#include <ctype.h>
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

tk_file tk_stdin = {0}, tk_stdout = {1}, tk_stderr = {2};
tk_slice tk_args = {0};

void *tk_alloc(size_t n) {
    void *p = malloc(n == 0 ? 1 : n);
    if (p == NULL) {
        tk_panic(TK_STR("out of memory", 13));
    }

    return p;
}

void tk_free(void *p) {
    free(p);
}

_Noreturn void tk_exit(int code) {
    fflush(stdout);
    exit(code);
}

_Noreturn void tk_panic(tk_string msg) {
    fflush(stdout);
    fputs("panic: ", stderr);
    fwrite(msg.ptr, 1, (size_t)msg.len, stderr);
    fputc('\n', stderr);
    exit(2);
}

_Noreturn void tk_bounds(int64_t i, int64_t len) {
    char buf[96];
    snprintf(buf, sizeof buf, "index out of range [%lld] with length %lld", (long long)i, (long long)len);
    tk_panic(TK_STR(buf, (int64_t)strlen(buf)));
}

// grow makes room for at least need elements, doubling as Teak's
// Push amortizes to constant time.
static void grow(tk_slice *s, int64_t need, size_t size) {
    if (need <= s->cap) {
        return;
    }

    int64_t cap = s->cap < 4 ? 4 : s->cap * 2;
    if (cap < need) {
        cap = need;
    }

    void *p = tk_alloc((size_t)cap * size);
    if (s->len > 0) {
        memcpy(p, s->ptr, (size_t)s->len * size);
    }

    tk_free(s->ptr);
    s->ptr = p;
    s->cap = cap;
}

void tk_slice_free(tk_slice *s) {
    tk_free(s->ptr);
    s->ptr = NULL;
    s->len = 0;
    s->cap = 0;
}

void tk_slice_make(tk_slice *out, int64_t len, int64_t cap, size_t size) {
    if (len < 0 || cap < len) {
        tk_panic(TK_STR("make: length out of range", 25));
    }

    out->ptr = tk_alloc((size_t)cap * size);
    memset(out->ptr, 0, (size_t)cap * size);
    out->len = len;
    out->cap = cap;
}

void tk_slice_push(tk_slice *s, const void *v, size_t size) {
    grow(s, s->len + 1, size);
    memcpy((char *)s->ptr + (size_t)s->len * size, v, size);
    s->len++;
}

bool tk_slice_pop(tk_slice *s, void *out, size_t size) {
    if (s->len == 0) {
        return false;
    }

    s->len--;
    memcpy(out, (char *)s->ptr + (size_t)s->len * size, size);
    return true;
}

void tk_slice_insert(tk_slice *s, int64_t i, const void *v, size_t size) {
    if ((uint64_t)i > (uint64_t)s->len) {
        tk_bounds(i, s->len);
    }

    grow(s, s->len + 1, size);
    char *at = (char *)s->ptr + (size_t)i * size;
    memmove(at + size, at, (size_t)(s->len - i) * size);
    memcpy(at, v, size);
    s->len++;
}

void tk_slice_remove(tk_slice *s, int64_t i, void *out, size_t size) {
    tk_index(i, s->len);
    char *at = (char *)s->ptr + (size_t)i * size;
    memcpy(out, at, size);
    memmove(at, at + size, (size_t)(s->len - i - 1) * size);
    s->len--;
}

void tk_slice_reverse(tk_slice *s, size_t size) {
    if (s->len < 2) {
        return;
    }

    char *lo = s->ptr;
    char *hi = (char *)s->ptr + (size_t)(s->len - 1) * size;
    char tmp[256];
    for (; lo < hi; lo += size, hi -= size) {
        for (size_t done = 0; done < size; done += sizeof tmp) {
            size_t n = size - done < sizeof tmp ? size - done : sizeof tmp;
            memcpy(tmp, lo + done, n);
            memcpy(lo + done, hi + done, n);
            memcpy(hi + done, tmp, n);
        }
    }
}

void tk_slice_clone(const tk_slice *s, tk_slice *out, size_t size) {
    out->ptr = tk_alloc((size_t)s->len * size);
    memcpy(out->ptr, s->ptr, (size_t)s->len * size);
    out->len = s->len;
    out->cap = s->len;
}

void tk_slice_sub(const tk_slice *s, int64_t lo, int64_t hi, tk_slice *out, size_t size) {
    if (lo < 0 || hi < lo || hi > s->len) {
        tk_bounds(hi, s->len);
    }

    out->ptr = (char *)s->ptr + (size_t)lo * size;
    out->len = hi - lo;
    out->cap = s->cap - lo;
}

// merge sorts the n elements at p, stably, using tmp as scratch of the
// same size.
static void merge(char *p, char *tmp, int64_t n, size_t size, bool (*less)(void *, const void *, const void *), void *ctx) {
    if (n < 2) {
        return;
    }

    int64_t half = n / 2;
    merge(p, tmp, half, size, less, ctx);
    merge(p + (size_t)half * size, tmp, n - half, size, less, ctx);
    int64_t i = 0, j = half, k = 0;
    while (i < half && j < n) {
        if (less(ctx, p + (size_t)j * size, p + (size_t)i * size)) {
            memcpy(tmp + (size_t)k++ * size, p + (size_t)j++ * size, size);
        } else {
            memcpy(tmp + (size_t)k++ * size, p + (size_t)i++ * size, size);
        }
    }

    memcpy(tmp + (size_t)k * size, p + (size_t)i * size, (size_t)(half - i) * size);
    k += half - i;
    memcpy(tmp + (size_t)k * size, p + (size_t)j * size, (size_t)(n - j) * size);
    memcpy(p, tmp, (size_t)n * size);
}

void tk_slice_sort(tk_slice *s, size_t size, bool (*less)(void *ctx, const void *a, const void *b), void *ctx) {
    if (s->len < 2) {
        return;
    }

    void *tmp = tk_alloc((size_t)s->len * size);
    merge(s->ptr, tmp, s->len, size, less, ctx);
    tk_free(tmp);
}

tk_string tk_concat(tk_string a, tk_string b) {
    uint8_t *p = tk_alloc((size_t)(a.len + b.len));
    memcpy(p, a.ptr, (size_t)a.len);
    memcpy(p + a.len, b.ptr, (size_t)b.len);
    return TK_OWNED(p, a.len + b.len);
}

tk_string tk_join(tk_string a, tk_string b, int owned) {
    tk_string s = tk_concat(a, b);
    if (owned & 1) {
        tk_string_drop(a);
    }

    if (owned & 2) {
        tk_string_drop(b);
    }

    return s;
}

bool tk_string_eq(tk_string a, tk_string b) {
    return a.len == b.len && memcmp(a.ptr, b.ptr, (size_t)a.len) == 0;
}

int tk_string_cmp(tk_string a, tk_string b) {
    int64_t n = a.len < b.len ? a.len : b.len;
    int c = memcmp(a.ptr, b.ptr, (size_t)n);
    if (c != 0) {
        return c;
    }

    return a.len < b.len ? -1 : a.len > b.len;
}

// formatted copies a small formatted number into an owned string.
static tk_string formatted(const char *buf) {
    size_t n = strlen(buf);
    uint8_t *p = tk_alloc(n);
    memcpy(p, buf, n);
    return TK_OWNED(p, (int64_t)n);
}

tk_string tk_int_to_string(int64_t v) {
    char buf[32];
    snprintf(buf, sizeof buf, "%lld", (long long)v);
    return formatted(buf);
}

tk_string tk_uint_to_string(uint64_t v) {
    char buf[32];
    snprintf(buf, sizeof buf, "%llu", (unsigned long long)v);
    return formatted(buf);
}

// tk_f64_to_string prints the shortest text that reads back as the
// same number, as Teak's fmt does: 15 for 15.0, 2.5 for 2.5, and no
// exponent while the magnitude is between 1e-4 and 1e21.
tk_string tk_f64_to_string(double v) {
    char buf[64];
    int prec = 1;
    for (; prec <= 17; prec++) {
        snprintf(buf, sizeof buf, "%.*g", prec, v);
        if (strtod(buf, NULL) == v) {
            break;
        }
    }

    if (strchr(buf, 'e') != NULL && v != 0 && v == v && v - v == 0) {
        int exp = (int)floor(log10(fabs(v)));
        if (exp >= -4 && exp < 21) {
            int frac = prec - 1 - exp;
            snprintf(buf, sizeof buf, "%.*f", frac < 0 ? 0 : frac, v);
        }
    }

    return formatted(buf);
}

tk_string tk_bool_to_string(bool v) {
    return v ? TK_STR("true", 4) : TK_STR("false", 5);
}

// tk_quote writes s as a string literal reads: in double quotes, with
// a quote, a backslash and the control characters escaped.
tk_string tk_quote(tk_string s) {
    uint8_t *p = tk_alloc((size_t)s.len * 4 + 2);
    int64_t n = 0;
    p[n++] = '"';
    for (int64_t i = 0; i < s.len; i++) {
        uint8_t c = s.ptr[i];
        const char *escape = NULL;
        switch (c) {
        case '"': escape = "\\\""; break;
        case '\\': escape = "\\\\"; break;
        case '\n': escape = "\\n"; break;
        case '\t': escape = "\\t"; break;
        case '\r': escape = "\\r"; break;
        }

        if (escape != NULL) {
            n += snprintf((char *)p + n, 3, "%s", escape);
        } else if (c < ' ' || c == 127) {
            n += snprintf((char *)p + n, 5, "\\x%02x", c);
        } else {
            p[n++] = c;
        }
    }

    p[n++] = '"';
    return TK_OWNED(p, n);
}

uint64_t tk_hash_bytes(const void *p, size_t n) {
    uint64_t h = 1469598103934665603u; // FNV-1a
    for (size_t i = 0; i < n; i++) {
        h = (h ^ ((const uint8_t *)p)[i]) * 1099511628211u;
    }

    return h;
}

uint64_t tk_hash_mix(uint64_t h, uint64_t v) {
    h ^= v + 0x9e3779b97f4a7c15u + (h << 6) + (h >> 2);
    return h * 1099511628211u;
}

static void *key_at(const tk_map *m, const tk_map_type *t, int64_t i) { return (char *)m->keys + (size_t)i * t->ksize; }
static void *val_at(const tk_map *m, const tk_map_type *t, int64_t i) { return (char *)m->vals + (size_t)i * t->vsize; }

// probe returns the index slot holding the key, or the empty slot
// where it would go; the index must have room.
static int64_t probe(const tk_map *m, const tk_map_type *t, const void *key) {
    uint64_t mask = (uint64_t)m->slots - 1;
    for (uint64_t i = t->hash(key) & mask;; i = (i + 1) & mask) {
        int64_t at = m->index[i];
        if (at == 0 || (m->live[at - 1] && t->eq(key_at(m, t, at - 1), key))) {
            return (int64_t)i;
        }
    }
}

// rebuild makes room for one more entry, growing the arrays, dropping
// dead entries, and indexing the live ones again.
static void rebuild(tk_map *m, const tk_map_type *t) {
    int64_t cap = m->cap < 8 ? 8 : m->cap * 2;
    if (m->count + 1 <= m->cap / 2) {
        cap = m->cap; // enough dead entries to drop
    }

    tk_map n = {0};
    n.cap = cap;
    n.slots = 16;
    while (n.slots < cap * 2) {
        n.slots *= 2;
    }

    n.keys = tk_alloc((size_t)cap * t->ksize);
    n.vals = tk_alloc((size_t)cap * t->vsize);
    n.live = tk_alloc((size_t)cap);
    n.index = tk_alloc((size_t)n.slots * sizeof(int64_t));
    memset(n.index, 0, (size_t)n.slots * sizeof(int64_t));
    for (int64_t i = 0; i < m->len; i++) {
        if (!m->live[i]) {
            continue;
        }

        memcpy(key_at(&n, t, n.len), key_at(m, t, i), t->ksize);
        memcpy(val_at(&n, t, n.len), val_at(m, t, i), t->vsize);
        n.live[n.len] = 1;
        n.len++;
        n.index[probe(&n, t, key_at(m, t, i))] = n.len;
    }

    n.count = n.len;
    tk_free(m->keys);
    tk_free(m->vals);
    tk_free(m->live);
    tk_free(m->index);
    *m = n;
}

void *tk_map_find(const tk_map *m, const tk_map_type *t, const void *key) {
    if (m->count == 0) {
        return NULL;
    }

    int64_t at = m->index[probe(m, t, key)];
    return at == 0 ? NULL : val_at(m, t, at - 1);
}

const void *tk_map_get(const tk_map *m, const tk_map_type *t, const void *key, const void *zero) {
    const void *v = tk_map_find(m, t, key);
    return v == NULL ? zero : v;
}

void *tk_map_slot(tk_map *m, const tk_map_type *t, const void *key) {
    void *v = tk_map_find(m, t, key);
    if (v != NULL) {
        return v;
    }

    if (m->len == m->cap) {
        rebuild(m, t);
    }

    int64_t i = m->len++;
    memcpy(key_at(m, t, i), key, t->ksize);
    memset(val_at(m, t, i), 0, t->vsize);
    m->live[i] = 1;
    m->count++;
    m->index[probe(m, t, key)] = i + 1;
    return val_at(m, t, i);
}

bool tk_map_remove(tk_map *m, const tk_map_type *t, const void *key, void *out) {
    if (m->count == 0) {
        return false;
    }

    int64_t slot = probe(m, t, key);
    int64_t at = m->index[slot];
    if (at == 0) {
        return false;
    }

    memcpy(out, val_at(m, t, at - 1), t->vsize);
    memset(val_at(m, t, at - 1), 0, t->vsize); // the value moved out
    if (t->kdrop != NULL) {
        t->kdrop(key_at(m, t, at - 1));
    }

    m->live[at - 1] = 0; // the entry stays, skipped by lookups and ranges, until the next rebuild drops it
    m->count--;
    return true;
}

void tk_map_free(tk_map *m, const tk_map_type *t) {
    for (int64_t i = tk_map_next(m, 0); i >= 0; i = tk_map_next(m, i + 1)) {
        if (t->kdrop != NULL) {
            t->kdrop(key_at(m, t, i));
        }

        if (t->vdrop != NULL) {
            t->vdrop(val_at(m, t, i));
        }
    }

    tk_free(m->keys);
    tk_free(m->vals);
    tk_free(m->live);
    tk_free(m->index);
    memset(m, 0, sizeof *m);
}

void tk_map_clone(const tk_map *m, const tk_map_type *t, tk_map *out) {
    tk_map n = {0};
    for (int64_t i = tk_map_next(m, 0); i >= 0; i = tk_map_next(m, i + 1)) {
        memcpy(tk_map_slot(&n, t, key_at(m, t, i)), val_at(m, t, i), t->vsize);
    }

    *out = n;
}

int64_t tk_map_next(const tk_map *m, int64_t i) {
    for (; i < m->len; i++) {
        if (m->live[i]) {
            return i;
        }
    }

    return -1;
}

tk_string tk_string_clone(tk_string s) {
    uint8_t *p = tk_alloc((size_t)s.len);
    memcpy(p, s.ptr, (size_t)s.len);
    return TK_OWNED(p, s.len);
}

tk_string tk_substr(tk_string s, int64_t lo, int64_t hi) {
    if (lo < 0 || hi < lo || hi > s.len) {
        tk_bounds(hi, s.len);
    }

    return TK_STR(s.ptr + lo, hi - lo);
}

// mapped copies s through f, one byte at a time: ASCII case only,
// until the library is written in Teak.
static tk_string mapped(tk_string s, int (*f)(int)) {
    uint8_t *p = tk_alloc((size_t)s.len);
    for (int64_t i = 0; i < s.len; i++) {
        p[i] = (uint8_t)f(s.ptr[i]);
    }

    return TK_OWNED(p, s.len);
}

tk_string tk_to_upper(tk_string s) { return mapped(s, toupper); }
tk_string tk_to_lower(tk_string s) { return mapped(s, tolower); }

static bool space(uint8_t c) {
    return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v';
}

tk_string tk_trim(tk_string s) {
    int64_t lo = 0, hi = s.len;
    while (lo < hi && space(s.ptr[lo])) {
        lo++;
    }

    while (hi > lo && space(s.ptr[hi - 1])) {
        hi--;
    }

    return TK_STR(s.ptr + lo, hi - lo);
}

bool tk_has_prefix(tk_string s, tk_string t) {
    return s.len >= t.len && memcmp(s.ptr, t.ptr, (size_t)t.len) == 0;
}

bool tk_has_suffix(tk_string s, tk_string t) {
    return s.len >= t.len && memcmp(s.ptr + s.len - t.len, t.ptr, (size_t)t.len) == 0;
}

// find returns the offset of t in s, or -1.
static int64_t find(tk_string s, tk_string t) {
    for (int64_t i = 0; i + t.len <= s.len; i++) {
        if (memcmp(s.ptr + i, t.ptr, (size_t)t.len) == 0) {
            return i;
        }
    }

    return -1;
}

bool tk_string_contains(tk_string s, tk_string t) { return find(s, t) >= 0; }

// tk_fields splits s around runs of white space into views of s.
void tk_fields(tk_string s, tk_slice *out) {
    out->ptr = NULL;
    out->len = 0;
    out->cap = 0;
    int64_t i = 0;
    while (i < s.len) {
        while (i < s.len && space(s.ptr[i])) {
            i++;
        }

        int64_t start = i;
        while (i < s.len && !space(s.ptr[i])) {
            i++;
        }

        if (i > start) {
            tk_string field = TK_STR(s.ptr + start, i - start);
            tk_slice_push(out, &field, sizeof field);
        }
    }
}

// tk_split splits s at every sep into views of s; an empty separator
// splits between every byte.
void tk_split(tk_string s, tk_string sep, tk_slice *out) {
    out->ptr = NULL;
    out->len = 0;
    out->cap = 0;
    int64_t start = 0;
    for (;;) {
        tk_string rest = TK_STR(s.ptr + start, s.len - start);
        int64_t at = sep.len == 0 ? (rest.len > 1 ? 1 : -1) : find(rest, sep);
        if (at < 0) {
            tk_slice_push(out, &rest, sizeof rest);
            return;
        }

        tk_string piece = TK_STR(rest.ptr, at);
        tk_slice_push(out, &piece, sizeof piece);
        start += at + sep.len;
    }
}

bool tk_split_once(tk_string s, tk_string sep, tk_string *before, tk_string *after) {
    int64_t at = find(s, sep);
    if (at < 0) {
        return false;
    }

    *before = TK_STR(s.ptr, at);
    *after = TK_STR(s.ptr + at + sep.len, s.len - at - sep.len);
    return true;
}

void tk_init(int argc, char **argv) {
    for (int i = 0; i < argc; i++) {
        tk_string arg = TK_STR(argv[i], (int64_t)strlen(argv[i]));
        tk_slice_push(&tk_args, &arg, sizeof arg);
    }
}

// cpath copies a Teak string into a C string for the system calls. A
// string holding a zero byte cannot be a path: the system would stop
// reading at the zero and open another file than the one named, so it
// is refused as an invalid argument.
static char *cpath(tk_string s) {
    if (memchr(s.ptr, 0, (size_t)s.len) != NULL) {
        errno = EINVAL;
        return NULL;
    }

    char *p = tk_alloc((size_t)s.len + 1);
    memcpy(p, s.ptr, (size_t)s.len);
    p[s.len] = '\0';
    return p;
}

// sysopen opens a path with flags, or fails with errno set.
static int sysopen(tk_string path, int flags) {
    char *p = cpath(path);
    if (p == NULL) {
        return -1;
    }

    int fd = open(p, flags, 0666);
    tk_free(p);
    return fd;
}

// failure builds the message "op path: reason" from errno, as Go's os
// package words it, with the reason in lower case.
static tk_string failure(const char *op, tk_string path) {
    const char *reason = strerror(errno);
    tk_string msg = tk_join(tk_concat(TK_STR(op, (int64_t)strlen(op)), TK_STR(" ", 1)), path, 1);
    msg = tk_join(msg, TK_STR(": ", 2), 1);
    tk_string r = TK_STR(reason, (int64_t)strlen(reason));
    if (r.len > 0 && r.ptr[0] >= 'A' && r.ptr[0] <= 'Z') {
        uint8_t *p = tk_alloc((size_t)r.len);
        memcpy(p, r.ptr, (size_t)r.len);
        p[0] = (uint8_t)(p[0] + ('a' - 'A'));
        r = TK_OWNED(p, r.len);
    }

    return tk_join(msg, r, 3);
}

tk_string tk_read_to_string(tk_string path, tk_string *out) {
    int fd = sysopen(path, O_RDONLY);
    if (fd < 0) {
        return failure("open", path);
    }

    tk_slice buf = {0};
    for (;;) {
        grow(&buf, buf.len + 4096, 1);
        ssize_t n = read(fd, (char *)buf.ptr + buf.len, (size_t)(buf.cap - buf.len));
        if (n < 0) {
            close(fd);
            return failure("read", path);
        }

        if (n == 0) {
            break;
        }

        buf.len += n;
    }

    close(fd);
    *out = (tk_string){buf.ptr, buf.len, buf.cap};
    return TK_STR("", 0);
}

tk_string tk_open(tk_string path, tk_file *out) {
    int fd = sysopen(path, O_RDONLY);
    if (fd < 0) {
        return failure("open", path);
    }

    out->fd = fd;
    return TK_STR("", 0);
}

tk_string tk_create(tk_string path, tk_file *out) {
    int fd = sysopen(path, O_WRONLY | O_CREAT | O_TRUNC);
    if (fd < 0) {
        return failure("open", path);
    }

    out->fd = fd;
    return TK_STR("", 0);
}

// tk_mkdir_temp makes a new directory under dir, or the system's
// temporary directory when dir is empty, named after pattern.
tk_string tk_mkdir_temp(tk_string dir, tk_string pattern, tk_string *out) {
    if (dir.len == 0) {
        const char *tmp = getenv("TMPDIR");
        if (tmp == NULL || tmp[0] == '\0') {
            tmp = "/tmp";
        }

        dir = TK_STR(tmp, (int64_t)strlen(tmp));
    }

    tk_string tmpl = tk_join(tk_join(dir, TK_STR("/", 1), 0), tk_join(pattern, TK_STR("XXXXXX", 6), 0), 3);
    char *p = cpath(tmpl);
    tk_string_drop(tmpl);
    if (p == NULL || mkdtemp(p) == NULL) {
        tk_free(p);
        return failure("mkdirtemp", dir);
    }

    *out = TK_OWNED(p, tmpl.len);
    return TK_STR("", 0);
}

static int remove_tree(const char *path) {
    struct stat st;
    if (lstat(path, &st) != 0) {
        return errno == ENOENT ? 0 : -1;
    }

    if (S_ISDIR(st.st_mode)) {
        DIR *d = opendir(path);
        if (d == NULL) {
            return -1;
        }

        struct dirent *ent;
        int rc = 0;
        while (rc == 0 && (ent = readdir(d)) != NULL) {
            if (strcmp(ent->d_name, ".") == 0 || strcmp(ent->d_name, "..") == 0) {
                continue;
            }

            size_t n = strlen(path) + 1 + strlen(ent->d_name) + 1;
            char *child = tk_alloc(n);
            snprintf(child, n, "%s/%s", path, ent->d_name);
            rc = remove_tree(child);
            tk_free(child);
        }

        closedir(d);
        return rc == 0 ? rmdir(path) : rc;
    }

    return unlink(path);
}

tk_string tk_remove_all(tk_string path) {
    char *p = cpath(path);
    int rc = p == NULL ? -1 : remove_tree(p);
    tk_free(p);
    if (rc != 0) {
        return failure("remove", path);
    }

    return TK_STR("", 0);
}

void tk_file_drop(tk_file f) {
    if (f.fd > 2) {
        close(f.fd);
    }
}

tk_string tk_file_close(tk_file f) {
    if (close(f.fd) != 0) {
        return failure("close", TK_STR("file", 4));
    }

    return TK_STR("", 0);
}

tk_string tk_file_sync(tk_file *f) {
    if (fsync(f->fd) != 0) {
        return failure("sync", TK_STR("file", 4));
    }

    return TK_STR("", 0);
}

// write_all writes every byte, through stdio for the standard streams
// so that prints and writes to them stay in order.
static bool write_all(int fd, const uint8_t *p, int64_t len) {
    if (fd == 1 || fd == 2) {
        return fwrite(p, 1, (size_t)len, fd == 1 ? stdout : stderr) == (size_t)len;
    }

    while (len > 0) {
        ssize_t n = write(fd, p, (size_t)len);
        if (n < 0) {
            return false;
        }

        p += n;
        len -= n;
    }

    return true;
}

tk_string tk_file_write_string(tk_file *f, tk_string s, int64_t *n) {
    if (!write_all(f->fd, s.ptr, s.len)) {
        return failure("write", TK_STR("file", 4));
    }

    *n = s.len;
    return TK_STR("", 0);
}

tk_string tk_io_copy(tk_file *dst, tk_file *src, int64_t *n) {
    uint8_t buf[32768];
    *n = 0;
    for (;;) {
        ssize_t got = read(src->fd, buf, sizeof buf);
        if (got < 0) {
            return failure("read", TK_STR("file", 4));
        }

        if (got == 0) {
            return TK_STR("", 0);
        }

        if (!write_all(dst->fd, buf, got)) {
            return failure("write", TK_STR("file", 4));
        }

        *n += got;
    }
}

void tk_print_string(tk_string s) { fwrite(s.ptr, 1, (size_t)s.len, stdout); }
void tk_print_owned(tk_string s) {
    tk_print_string(s);
    tk_string_drop(s);
}
void tk_print_int(int64_t v) { printf("%lld", (long long)v); }
void tk_print_uint(uint64_t v) { printf("%llu", (unsigned long long)v); }
void tk_print_f64(double v) { tk_print_owned(tk_f64_to_string(v)); }
void tk_print_bool(bool v) { fputs(v ? "true" : "false", stdout); }
void tk_print_sep(void) { fputc(' ', stdout); }
void tk_print_nl(void) { fputc('\n', stdout); }
void tk_eprint_string(tk_string s) { fwrite(s.ptr, 1, (size_t)s.len, stderr); }
void tk_eprint_owned(tk_string s) {
    tk_eprint_string(s);
    tk_string_drop(s);
}
