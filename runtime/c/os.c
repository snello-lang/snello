// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Files and directories, and the reads and writes of the io and os
// packages, with the reasons of their errors.

#define _POSIX_C_SOURCE 200809L
#define _DARWIN_C_SOURCE

#include "internal.h"

#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

// sn_os_Cores is os.Cores: the processors the program may run on, the
// count the scheduler starts a worker per.
int64_t sn_os_Cores(void) {
    return sn_cores();
}

// cpath copies a Snello string into a C string for the system calls. A
// string holding a zero byte cannot be a path: the system would stop
// reading at the zero and open another file than the one named, so it
// is refused as an invalid argument.
static char *cpath(sn_string s) {
    sn_check_string(s);
    if (memchr(s.ptr, 0, (size_t)s.len) != NULL) {
        errno = EINVAL;
        return NULL;
    }

    char *p = sn_alloc((size_t)s.len + 1);
    memcpy(p, s.ptr, (size_t)s.len);
    p[s.len] = '\0';
    return p;
}

// sysopen opens a path with flags, or fails with errno set. The file is
// closed on exec, as Go opens every file, so that no program the
// process starts inherits it. An open a signal interrupts, as one of a
// FIFO may be, is asked again.
static int sysopen(sn_string path, int flags) {
    char *p = cpath(path);
    if (p == NULL) {
        return -1;
    }

    int fd;
    do {
        fd = open(p, flags | O_CLOEXEC, 0666);
    } while (fd < 0 && errno == EINTR);
    sn_free(p);
    return fd;
}

// sysread reads at most len bytes, asking again when a signal
// interrupts the call, and returns what read returns, with errno set
// on a failure.
static ssize_t sysread(int fd, void *p, size_t len) {
    ssize_t n;
    do {
        n = read(fd, p, len < SN_IO_MAX ? len : SN_IO_MAX);
    } while (n < 0 && errno == EINTR);
    return n;
}

// sn_io_reason is the system's text for an error number, with its
// first letter in lower case, as Go words it; the library puts the
// operation and the path in front.
sn_string sn_io_reason(int64_t code) {
    const char *reason = strerror((int)code);
    sn_string r = SN_STR(reason, (int64_t)strlen(reason));
    uint8_t *p = sn_alloc((size_t)r.len);
    memcpy(p, r.ptr, (size_t)r.len);
    if (r.len > 0 && p[0] >= 'A' && p[0] <= 'Z') {
        p[0] = (uint8_t)(p[0] + ('a' - 'A'));
    }

    return SN_OWNED(p, r.len);
}

// sn_io_kindOf sorts an error number as io.Kind does: 1 for a missing
// file, 2 for a permission refused, 3 for a file that exists, 4 for a
// connection refused, 5 for one reset or broken, 6 for one timed out,
// 0 for the rest.
int64_t sn_io_kindOf(int64_t code) {
    switch ((int)code) {
    case ENOENT:
        return 1;
    case EACCES:
    case EPERM:
        return 2;
    case EEXIST:
        return 3;
    case ECONNREFUSED:
        return 4;
    case ECONNRESET:
    case ECONNABORTED:
    case EPIPE:
        return 5;
    case ETIMEDOUT:
        return 6;
    }

    return 0;
}

// sn_os_readToString reads the named file whole, into one block of the
// size the file says it has, as Go's os.ReadFile does: one byte more,
// so that the read finding the end needs no room, and 512 bytes for a
// file that says nothing, a pipe or a file of /proc, which grows by
// doubling. The string owns that block, with no copy. On a failure,
// inRead says which call failed, 0 for the open and 1 for a read, so
// that the error names it as Go's does.
int64_t sn_os_readToString(sn_string path, sn_string *out, int64_t *inRead) {
    *inRead = 0;
    int fd = sysopen(path, O_RDONLY);
    if (fd < 0) {
        return errno;
    }

    int64_t size = 0;
    struct stat st;
    if (fstat(fd, &st) == 0 && S_ISREG(st.st_mode) && st.st_size > 0) {
        size = (int64_t)st.st_size;
    }

    sn_slice buf = {0};
    sn_slice_grow(&buf, size < 512 ? 512 : size + 1, 1);
    for (;;) {
        if (buf.len == buf.cap) {
            sn_slice_grow(&buf, buf.len + 1, 1);
        }

        ssize_t n = sysread(fd, (char *)buf.ptr + buf.len, (size_t)(buf.cap - buf.len));
        if (n < 0) {
            int code = errno;
            close(fd);
            sn_slice_free(&buf);
            *inRead = 1;
            return code;
        }

        if (n == 0) {
            break;
        }

        buf.len += n;
    }

    close(fd);
    sn_string_drop(out);
    *out = (sn_string){buf.ptr, buf.len, buf.cap};
    return 0;
}

int64_t sn_os_open(sn_string path, int64_t *fd) {
    int f = sysopen(path, O_RDONLY);
    if (f < 0) {
        return errno;
    }

    *fd = f;
    return 0;
}

int64_t sn_os_create(sn_string path, int64_t *fd) {
    int f = sysopen(path, O_WRONLY | O_CREAT | O_TRUNC);
    if (f < 0) {
        return errno;
    }

    *fd = f;
    return 0;
}

// sn_os_mkdirTemp makes a new directory under dir, or the system's
// temporary directory when dir is empty, named as Go names it: the
// pattern with its last * replaced by a random number, or followed by
// one when it has none, trying another number while the name is
// taken. On a failure out holds the name the error is about and op
// the call that failed: mkdirtemp, or stat when dir itself is
// missing, which Go's MkdirTemp reports by stating dir.
int64_t sn_os_mkdirTemp(sn_string dir, sn_string pattern, sn_string *out, sn_string *op) {
    sn_string_drop(op);
    *op = SN_STR("mkdirtemp", 9);
    if (dir.len == 0) {
        const char *tmp = getenv("TMPDIR");
        if (tmp == NULL || tmp[0] == '\0') {
            tmp = "/tmp";
        }

        dir = SN_STR(tmp, (int64_t)strlen(tmp));
    }

    int64_t star = pattern.len;
    for (int64_t i = pattern.len - 1; i >= 0; i--) {
        if (pattern.ptr[i] == '*') {
            star = i;
            break;
        }
    }

    sn_string prefix = sn_join(sn_join(dir, SN_STR("/", 1), 0), SN_STR(pattern.ptr, star), 1);
    sn_string suffix = star < pattern.len ? SN_STR(pattern.ptr + star + 1, pattern.len - star - 1) : SN_STR("", 0);
    int code = EEXIST;
    for (int try = 0; try < 10000 && code == EEXIST; try++) {
        char digits[24];
        int n = snprintf(digits, sizeof digits, "%u", (unsigned)(sn_next_random() >> 32));
        sn_string name = sn_join(sn_join(prefix, SN_STR(digits, n), 0), suffix, 1);
        char *p = cpath(name);
        if (p != NULL && mkdir(p, 0700) == 0) {
            sn_free(p);
            sn_string_free(prefix);
            sn_string_drop(out);
            *out = name;
            return 0;
        }

        code = errno;
        sn_free(p);
        sn_string_free(name);
    }

    if (code == ENOENT) {
        // The parent may be what is missing: Go stats it and reports
        // that when it is.
        struct stat st;
        char *p = cpath(dir);
        int missing = p != NULL && stat(p, &st) != 0 && errno == ENOENT;
        sn_free(p);
        if (missing) {
            sn_string_free(prefix);
            sn_string_drop(out);
            *out = sn_string_clone(dir);
            *op = SN_STR("stat", 4);
            return ENOENT;
        }
    }

    sn_string_drop(out);
    *out = sn_join(sn_join(prefix, SN_STR("*", 1), 1), suffix, 1); // the pattern of the names tried, as Go's error gives it
    return code;
}

// failed_at records, for a removal that failed, the entry it failed on
// and the call that failed, named as Go's error names it, keeping errno.
static int failed_at(const char *path, const char *op, sn_string *failed, sn_string *what) {
    int code = errno;
    sn_string_drop(failed);
    *failed = sn_string_clone(SN_STR(path, (int64_t)strlen(path)));
    sn_string_drop(what);
    *what = SN_STR(op, (int64_t)strlen(op));
    errno = code;
    return -1;
}

// A removal walks the tree as Go's removeAllFrom does: every call is
// relative to an open directory, so a path longer than the system
// allows is no obstacle, and the walk keeps a level per directory it
// is inside of, a few words each, on a slice of its own rather than
// the task's stack, which a tree a few thousand deep would outgrow. A
// level is an open directory, how long the path of the entry in hand
// is up to it, and whether a pass over its entries has handled any.
typedef struct {
    DIR *d;
    int64_t len;
    bool seen;
} level;

// remove_entry removes the entry name of the directory fd, which path
// names in full for the error, or opens it and pushes it on levels when
// it is a directory whose entries must go first; len is how long path
// is. unlinkat is tried first, since most entries are files, and only
// its refusal makes the directory question worth a stat; an entry that
// is already gone was removed by someone else, which is what was asked.
static int remove_entry(int fd, const char *name, const char *path, int64_t len, sn_slice *levels, sn_string *failed, sn_string *op) {
    if (unlinkat(fd, name, 0) == 0 || errno == ENOENT) {
        return 0;
    }

    int code = errno;
    if (code != EISDIR && code != EPERM && code != EACCES) {
        return failed_at(path, "unlinkat", failed, op);
    }

    struct stat st;
    if (fstatat(fd, name, &st, AT_SYMLINK_NOFOLLOW) != 0) {
        return errno == ENOENT ? 0 : failed_at(path, "fstatat", failed, op);
    }

    if (!S_ISDIR(st.st_mode)) {
        errno = code;
        return failed_at(path, "unlinkat", failed, op);
    }

    int sub = openat(fd, name, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
    if (sub < 0) {
        return errno == ENOENT ? 0 : failed_at(path, "openfdat", failed, op);
    }

    DIR *d = fdopendir(sub);
    if (d == NULL) {
        code = errno;
        close(sub);
        errno = code;
        return failed_at(path, "openfdat", failed, op);
    }

    level l = {d, len, false};
    sn_slice_push(levels, &l, sizeof l);
    return 0;
}

// remove_top removes the directory at the top of levels, emptied by the
// passes over it, from the level below, or from the working directory
// for the tree's root; path names it in full, and name is its name in
// that directory. A directory still not empty after a pass that removed
// something is read again, since a directory read while its entries go
// may skip some, as Go's RemoveAll notes; one that stays full after a
// pass that found nothing is the failure it reports.
static int remove_top(sn_slice *levels, char *path, sn_string *failed, sn_string *op) {
    level *top = &((level *)levels->ptr)[levels->len - 1];
    level *below = levels->len > 1 ? top - 1 : NULL;
    path[top->len] = '\0';
    int fd = below != NULL ? dirfd(below->d) : AT_FDCWD;
    const char *name = below != NULL ? path + below->len + 1 : path;
    if (unlinkat(fd, name, AT_REMOVEDIR) == 0 || errno == ENOENT) {
        closedir(top->d);
        levels->len--;
        return 0;
    }

    if ((errno == ENOTEMPTY || errno == EEXIST) && top->seen) {
        top->seen = false;
        rewinddir(top->d);
        return 0;
    }

    return failed_at(path, "unlinkat", failed, op);
}

// sn_os_removeAll removes a tree; a path that is not there is nothing to
// remove, not a failure, as Go's RemoveAll has it. On a failure, failed
// and op name the entry it failed on and the call, as Go's error names
// them, rather than the path asked for. The walk is depth first over
// the levels above, with the full path of the entry in hand kept in
// full, the path asked for and then a slash and a name per level.
int64_t sn_os_removeAll(sn_string path, sn_string *failed, sn_string *op) {
    char *p = cpath(path);
    if (p == NULL) {
        int code = errno;
        sn_string_drop(failed);
        *failed = sn_string_clone(path);
        sn_string_drop(op);
        *op = SN_STR("RemoveAll", 9);
        return code;
    }

    sn_slice levels = {0};
    sn_slice full = {0};
    sn_strings_appendString((sn_slice_u8 *)&full, SN_STR(p, (int64_t)strlen(p) + 1)); // with its zero byte
    int rc = remove_entry(AT_FDCWD, p, p, (int64_t)strlen(p), &levels, failed, op);
    while (rc == 0 && levels.len > 0) {
        level *top = &((level *)levels.ptr)[levels.len - 1];
        errno = 0;
        struct dirent *ent = readdir(top->d);
        if (ent == NULL) {
            if (errno != 0) {
                ((char *)full.ptr)[top->len] = '\0';
                rc = failed_at(full.ptr, "readdirent", failed, op);
                break;
            }

            rc = remove_top(&levels, full.ptr, failed, op);
            continue;
        }

        if (strcmp(ent->d_name, ".") == 0 || strcmp(ent->d_name, "..") == 0) {
            continue;
        }

        top->seen = true;
        full.len = top->len;
        sn_strings_appendString((sn_slice_u8 *)&full, SN_STR("/", 1));
        sn_strings_appendString((sn_slice_u8 *)&full, SN_STR(ent->d_name, (int64_t)strlen(ent->d_name) + 1));
        rc = remove_entry(dirfd(top->d), ent->d_name, full.ptr, full.len - 1, &levels, failed, op);
    }

    int code = errno;
    for (int64_t i = 0; i < levels.len; i++) {
        closedir(((level *)levels.ptr)[i].d);
    }

    sn_slice_free(&levels);
    sn_slice_free(&full);
    sn_free(p);
    return rc == 0 ? 0 : code;
}

// sn_os_remove removes a file or an empty directory, as Go's Remove
// does: unlink first, then rmdir, and when both fail the error of the
// one that fits, which rmdir's ENOTDIR on a file says.
int64_t sn_os_remove(sn_string path) {
    char *p = cpath(path);
    if (p == NULL) {
        return errno;
    }

    int code = 0;
    if (unlink(p) != 0) {
        int first = errno;
        if (rmdir(p) != 0) {
            code = errno == ENOTDIR ? first : errno;
        }
    }

    sn_free(p);
    return code;
}

int64_t sn_os_closeFile(int64_t fd) {
    return close((int)fd) == 0 ? 0 : errno;
}

int64_t sn_os_syncFile(int64_t fd) {
    return fsync((int)fd) == 0 ? 0 : errno;
}

// sn_os_seek moves the file's position as lseek does, whence one of
// SEEK_SET, SEEK_CUR and SEEK_END, which io.SeekFrom's variants are in
// order, and stores the new position counted from the start. A position
// past what an off_t holds, a Start of more than 2^63 - 1 bytes, is an
// invalid argument, as the system says of one before the start. The
// offset goes to lseek whole, so off_t must hold every int64_t.
_Static_assert(sizeof(off_t) == sizeof(int64_t), "off_t is not 64 bits");

int64_t sn_os_seek(int64_t fd, int64_t whence, int64_t off, uint64_t *at) {
    static const int whences[] = {SEEK_SET, SEEK_CUR, SEEK_END};
    if (whence < 0 || whence > 2) {
        return EINVAL;
    }

    off_t got = lseek((int)fd, (off_t)off, whences[whence]);
    if (got < 0) {
        return errno;
    }

    *at = (uint64_t)got;
    return 0;
}

// sn_io_reserve makes room in b for n more bytes in one allocation:
// io.ReadAll's slice of the exact length, before its blocks are copied
// in.
void sn_io_reserve(sn_slice_u8 *buf, int64_t n) {
    sn_strings_reserve(buf, n);
}

// The standard streams are written as any file is, straight to the
// descriptor with nothing held back, as Go's os.Stdout is: what fmt
// prints, what is written to os.Stdout and os.Stderr, and a panic's
// message come out in the order the program made them, a prompt
// without a newline shows at once, and a write that fails reports the
// failure. fmt builds each call's text whole and writes it at once, so
// two tasks' lines do not interleave.
// sn_os_write writes all of buf to the file and stores how much it
// wrote: all of it, or on a failure, a disk that filled, what went
// before it, which the library's Error carries as Go's Write returns it.
int64_t sn_os_write(int64_t fd, sn_slice_u8 buf, int64_t *n) {
    sn_check_slice((sn_slice *)&buf);
    *n = sn_write_fd((int)fd, buf.ptr, buf.len);
    return *n < buf.len ? errno : 0;
}

int64_t sn_os_writeString(int64_t fd, sn_string s, int64_t *n) {
    sn_check_string(s);
    *n = sn_write_fd((int)fd, s.ptr, s.len);
    return *n < s.len ? errno : 0;
}

int64_t sn_os_read(int64_t fd, sn_slice_u8 *buf, int64_t *n) {
    sn_check_slice((sn_slice *)buf);
    ssize_t got = sysread((int)fd, buf->ptr, (size_t)buf->len);
    if (got < 0) {
        return errno;
    }

    *n = got;
    return 0;
}
