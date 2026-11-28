// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Files and directories, and the reads and writes of the io and os
// packages, with the reasons of their errors.

#define _POSIX_C_SOURCE 200809L
#define _DEFAULT_SOURCE // glibc's kinds of a directory's entries and the sticky bit under -std=c11
#define _DARWIN_C_SOURCE

#include "internal.h"

#include <errno.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#if !defined(_WIN32)
#include <dirent.h>
#include <fcntl.h>
#include <sys/stat.h>
#include <unistd.h>
#endif

// sn_os_Cores is os.Cores: the processors the program may run on, the
// count the scheduler starts a worker per.
SN_HELD int64_t sn_os_Cores(void) { return sn_cores(); }

#if !defined(_WIN32) // the paths, the opens and the reads of Linux and macOS; os_windows.c has Windows'
// cpath copies a Snello string into a C string for the system calls. A
// string holding a zero byte cannot be a path: the system would stop
// reading at the zero and open another file than the one named, so it
// is refused as an invalid argument.
SN_HELD_STATIC static char *cpath(sn_string s) {
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
// closed on exec, as every file is opened, so that no program the
// process starts inherits it. An open a signal interrupts, as one of a
// FIFO may be, is asked again.
SN_HELD_STATIC static int sysopen(sn_string path, int flags) {
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
SN_HELD_STATIC static ssize_t sysread(int fd, void *p, size_t len) {
    ssize_t n;
    do {
        n = read(fd, p, len < SN_IO_MAX ? len : SN_IO_MAX);
    } while (n < 0 && errno == EINTR);
    return n;
}
#endif

// sn_io_reason is the system's text for an error number, with its
// first letter in lower case; the library puts the
// operation and the path in front.
SN_HELD sn_string sn_io_reason(int64_t code) {
#if defined(_WIN32)
    const char *reason = sn_errno_text((int)code); // the C library's own wording differs from the other systems'
    if (reason == NULL) {
        reason = strerror((int)code);
    }
#else
    const char *reason = strerror((int)code);
#endif
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
// connection refused, 5 for one reset, 6 for one timed out, 7 for a
// pipe whose reader is gone, 8 for a call the task's cancel ended, 0
// for the rest.
SN_HELD int64_t sn_io_kindOf(int64_t code) {
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
        return 5;
    case ETIMEDOUT:
        return 6;
    case EPIPE:
        return 7;
    case ECANCELED:
        return 8;
    }

    return 0;
}

#if !defined(_WIN32) // the files and the removals of Linux and macOS; os_windows.c has Windows'
// read_whole reads the named file whole, into one block of the size the
// file says it has and one byte more, so that the read finding the end
// needs no room, and 512 bytes for a file that says nothing, a pipe or
// a file of /proc, which grows by doubling. On a failure, inRead says
// which call failed, 0 for the open and 1 for a read, so that the error
// names it.
//
// sn_os_readToString is os.ReadToString: the string owns that block,
// with no copy.
SN_HELD_STATIC static int64_t read_whole(sn_string path, sn_slice *buf, int64_t *inRead) {
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

    sn_slice_grow(buf, size < 512 ? 512 : size + 1, 1);
    for (;;) {
        if (buf->len == buf->cap) {
            sn_slice_grow(buf, buf->len + 1, 1);
        }

        ssize_t n = sysread(fd, (char *)buf->ptr + buf->len, (size_t)(buf->cap - buf->len));
        if (n < 0) {
            int code = errno;
            close(fd);
            sn_slice_free(buf);
            *inRead = 1;
            return code;
        }

        if (n == 0) {
            break;
        }

        buf->len += n;
    }

    close(fd);
    return 0;
}

SN_HELD int64_t sn_os_readToString(sn_string path, sn_string *out, int64_t *inRead) {
    sn_slice buf = { 0 };
    int64_t code = read_whole(path, &buf, inRead);
    if (code != 0) {
        return code;
    }

    sn_string_drop(out);
    *out = (sn_string){ buf.ptr, buf.len, buf.cap };
    return 0;
}

// sn_os_readFile is os.ReadFile: the same read, into bytes the slice
// owns.
SN_HELD int64_t sn_os_readFile(sn_string path, sn_slice_u8 *out, int64_t *inRead) {
    sn_slice buf = { 0 };
    int64_t code = read_whole(path, &buf, inRead);
    if (code != 0) {
        return code;
    }

    sn_slice_free((sn_slice *)out);
    *out = (sn_slice_u8){ buf.ptr, buf.len, buf.cap };
    return 0;
}

SN_HELD int64_t sn_os_open(sn_string path, int64_t *fd) {
    int f = sysopen(path, O_RDONLY);
    if (f < 0) {
        return errno;
    }

    *fd = f;
    return 0;
}

SN_HELD int64_t sn_os_create(sn_string path, int64_t *fd) {
    int f = sysopen(path, O_WRONLY | O_CREAT | O_TRUNC);
    if (f < 0) {
        return errno;
    }

    *fd = f;
    return 0;
}

// sn_os_mkdirTemp makes a new directory under dir, or the system's
// temporary directory when dir is empty, named by its pattern: the
// pattern with its last * replaced by a random number, or followed by
// one when it has none, trying another number while the name is
// taken. On a failure out holds the name the error is about and op
// the call that failed: mkdirtemp, or stat when dir itself is
// missing, which it finds by stating dir.
SN_HELD_STATIC static int64_t mkdir_temp(sn_string dir, sn_string pattern, sn_string *out, sn_string *op) {
    sn_string_drop(op);
    *op = SN_STR("mkdirtemp", 9);
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
        // The parent may be what is missing: it is stated, and that
        // reported when it is.
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
    *out = sn_join(sn_join(prefix, SN_STR("*", 1), 1), suffix,
                   1); // the pattern of the names tried
    return code;
}

SN_HELD int64_t sn_os_mkdirTemp(sn_string dir, sn_string pattern, sn_string *out, sn_string *op) {
    if (dir.len > 0) {
        return mkdir_temp(dir, pattern, out, op);
    }

    sn_string tmp = sn_os_TempDir();
    int64_t code = mkdir_temp(tmp, pattern, out, op);
    sn_string_free(tmp);
    return code;
}

// failed_at records, for a removal that failed, the entry it failed on
// and the call that failed, keeping errno.
SN_HELD_STATIC static int failed_at(const char *path, const char *op, sn_string *failed, sn_string *what) {
    int code = errno;
    sn_string_drop(failed);
    *failed = sn_string_clone(SN_STR(path, (int64_t)strlen(path)));
    sn_string_drop(what);
    *what = SN_STR(op, (int64_t)strlen(op));
    errno = code;
    return -1;
}

// A removal walks the tree by open directories: every call is
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
SN_HELD_STATIC static int remove_entry(int fd, const char *name, const char *path, int64_t len, sn_slice *levels,
                                       sn_string *failed, sn_string *op) {
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

    level l = { d, len, false };
    sn_slice_push(levels, &l, sizeof l);
    return 0;
}

// remove_top removes the directory at the top of levels, emptied by the
// passes over it, from the level below, or from the working directory
// for the tree's root; path names it in full, and name is its name in
// that directory. A directory still not empty after a pass that removed
// something is read again, since a directory read while its entries go
// may skip some; one that stays full after a
// pass that found nothing is the failure it reports.
SN_HELD_STATIC static int remove_top(sn_slice *levels, char *path, sn_string *failed, sn_string *op) {
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
// remove, not a failure. On a failure, failed and op name the entry it
// failed on and the call, rather than the path asked for. The walk is
// depth first over the levels above, with the full path of the entry
// in hand kept in full, the path asked for and then a slash and a name
// per level.
SN_HELD int64_t sn_os_removeAll(sn_string path, sn_string *failed, sn_string *op) {
    char *p = cpath(path);
    if (p == NULL) {
        int code = errno;
        sn_string_drop(failed);
        *failed = sn_string_clone(path);
        sn_string_drop(op);
        *op = SN_STR("RemoveAll", 9);
        return code;
    }

    sn_slice levels = { 0 };
    sn_slice full = { 0 };
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

// sn_os_remove removes a file or an empty directory: unlink first,
// then rmdir, and when both fail the error of the one that fits, which
// rmdir's ENOTDIR on a file says.
SN_HELD int64_t sn_os_remove(sn_string path) {
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

SN_HELD int64_t sn_os_closeFile(int64_t fd) { return close((int)fd) == 0 ? 0 : errno; }

SN_HELD int64_t sn_os_syncFile(int64_t fd) { return fsync((int)fd) == 0 ? 0 : errno; }

// sn_os_seek moves the file's position as lseek does, whence one of
// SEEK_SET, SEEK_CUR and SEEK_END, which io.SeekFrom's variants are in
// order, and stores the new position counted from the start. A position
// past what an off_t holds, a Start of more than 2^63 - 1 bytes, is an
// invalid argument, as the system says of one before the start. The
// offset goes to lseek whole, so off_t must hold every int64_t.
_Static_assert(sizeof(off_t) == sizeof(int64_t), "off_t is not 64 bits");

SN_HELD int64_t sn_os_seek(int64_t fd, int64_t whence, int64_t off, uint64_t *at) {
    static const int whences[] = { SEEK_SET, SEEK_CUR, SEEK_END };
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
#endif

// sn_io_reserve makes room in b for n more bytes in one allocation:
// io.ReadAll's slice of the exact length, before its blocks are copied
// in.
SN_HELD void sn_io_reserve(sn_slice_u8 *buf, int64_t n) { sn_strings_reserve(buf, n); }

#if !defined(_WIN32) // the modes, the directories and the host of Linux and macOS; os_windows.c has Windows'
// mode_from_sys is the os.FileMode of a file the system reports the mode
// of.
SN_HELD_STATIC static uint32_t mode_from_sys(mode_t m) {
    uint32_t mode = (uint32_t)(m & 0777);
    switch (m & S_IFMT) {
    case S_IFBLK:
        mode |= SN_MODE_DEVICE;
        break;
    case S_IFCHR:
        mode |= SN_MODE_DEVICE | SN_MODE_CHAR;
        break;
    case S_IFDIR:
        mode |= SN_MODE_DIR;
        break;
    case S_IFIFO:
        mode |= SN_MODE_PIPE;
        break;
    case S_IFLNK:
        mode |= SN_MODE_SYMLINK;
        break;
    case S_IFREG:
        break;
    case S_IFSOCK:
        mode |= SN_MODE_SOCKET;
        break;
    default:
        mode |= SN_MODE_IRREGULAR;
    }

    if (m & S_ISGID) {
        mode |= SN_MODE_SETGID;
    }

    if (m & S_ISUID) {
        mode |= SN_MODE_SETUID;
    }

    if (m & S_ISVTX) {
        mode |= SN_MODE_STICKY;
    }

    return mode;
}

// mode_to_sys is the mode the system takes for an os.FileMode a file or a
// directory is made with.
SN_HELD_STATIC static mode_t mode_to_sys(uint32_t perm) {
    mode_t m = (mode_t)(perm & 0777);
    if (perm & SN_MODE_SETUID) {
        m |= S_ISUID;
    }

    if (perm & SN_MODE_SETGID) {
        m |= S_ISGID;
    }

    if (perm & SN_MODE_STICKY) {
        m |= S_ISVTX;
    }

    return m;
}

// set_sticky gives a new file or directory its sticky bit where the
// system drops it from the mode a file is made with, Darwin and the
// BSDs, after the make.
SN_HELD_STATIC static int set_sticky(const char *p) {
#if defined(__APPLE__) || defined(__FreeBSD__) || defined(__NetBSD__) || defined(__OpenBSD__) || defined(__DragonFly__)
    struct stat st;
    if (stat(p, &st) != 0 || chmod(p, (st.st_mode & 07777) | S_ISVTX) != 0) {
        return errno;
    }
#else
    (void)p;
#endif
    return 0;
}

// sn_os_stat is os.Stat when follow is set and os.Lstat when it is not:
// the size, the os.FileMode, the time of the last change in seconds and
// nanoseconds since 1970, and the device and file numbers that say
// whether two paths name one file.
SN_HELD int64_t sn_os_stat(sn_string path, bool follow, int64_t *size, uint32_t *mode, int64_t *sec, int64_t *nsec,
                           uint64_t *dev, uint64_t *ino) {
    char *p = cpath(path);
    if (p == NULL) {
        return errno;
    }

    struct stat st;
    int r;
    do {
        r = follow ? stat(p, &st) : lstat(p, &st);
    } while (r != 0 && errno == EINTR);
    int code = r == 0 ? 0 : errno;
    sn_free(p);
    if (code != 0) {
        return code;
    }

    *size = (int64_t)st.st_size;
    *mode = mode_from_sys(st.st_mode);
#if defined(__APPLE__)
    *sec = (int64_t)st.st_mtimespec.tv_sec;
    *nsec = (int64_t)st.st_mtimespec.tv_nsec;
#else
    *sec = (int64_t)st.st_mtim.tv_sec;
    *nsec = (int64_t)st.st_mtim.tv_nsec;
#endif
    *dev = (uint64_t)st.st_dev;
    *ino = (uint64_t)st.st_ino;
    return 0;
}

// sn_os_mkdir is os.Mkdir.
SN_HELD int64_t sn_os_mkdir(sn_string path, uint32_t perm) {
    char *p = cpath(path);
    if (p == NULL) {
        return errno;
    }

    int r;
    do {
        r = mkdir(p, mode_to_sys(perm));
    } while (r != 0 && errno == EINTR);
    int code = r == 0 ? 0 : errno;
    if (code == 0 && (perm & SN_MODE_STICKY) != 0) {
        code = set_sticky(p);
        if (code != 0) {
            (void)rmdir(p);
        }
    }

    sn_free(p);
    return code;
}

// sn_os_rename is the system's rename, which os.Rename calls once it
// has refused a rename onto a directory.
SN_HELD int64_t sn_os_rename(sn_string from, sn_string to) {
    char *f = cpath(from);
    if (f == NULL) {
        return errno;
    }

    char *t = cpath(to);
    if (t == NULL) {
        sn_free(f);
        return EINVAL;
    }

    int r;
    do {
        r = rename(f, t);
    } while (r != 0 && errno == EINTR);
    int code = r == 0 ? 0 : errno;
    sn_free(f);
    sn_free(t);
    return code;
}

// sn_os_chdir is os.Chdir.
SN_HELD int64_t sn_os_chdir(sn_string path) {
    char *p = cpath(path);
    if (p == NULL) {
        return errno;
    }

    int code = chdir(p) == 0 ? 0 : errno;
    sn_free(p);
    return code;
}

// sn_os_getcwd asks the system for the current directory, in a block
// that doubles while the path does not fit.
SN_HELD int64_t sn_os_getcwd(sn_string *out) {
    size_t cap = 1024;
    for (;;) {
        char *p = sn_alloc(cap);
        if (getcwd(p, cap) != NULL) {
            sn_string_drop(out);
            *out = sn_string_clone(SN_STR(p, (int64_t)strlen(p)));
            sn_free(p);
            return 0;
        }

        int code = errno;
        sn_free(p);
        if (code != ERANGE) {
            return code;
        }

        cap *= 2;
    }
}

// sn_os_openFile is os.OpenFile. A flag the package does not name, or
// both of O_WRONLY and O_RDWR, is an invalid argument.
SN_HELD int64_t sn_os_openFile(sn_string path, int64_t flag, uint32_t perm, int64_t *fd) {
    const int64_t known = SN_O_WRONLY | SN_O_RDWR | SN_O_APPEND | SN_O_CREATE | SN_O_EXCL | SN_O_SYNC | SN_O_TRUNC;
    if ((flag & ~known) != 0 || (flag & 3) == 3) {
        return EINVAL;
    }

    int flags = (flag & 3) == SN_O_WRONLY ? O_WRONLY : (flag & 3) == SN_O_RDWR ? O_RDWR : O_RDONLY;
    flags |= (flag & SN_O_APPEND) != 0 ? O_APPEND : 0;
    flags |= (flag & SN_O_CREATE) != 0 ? O_CREAT : 0;
    flags |= (flag & SN_O_EXCL) != 0 ? O_EXCL : 0;
    flags |= (flag & SN_O_SYNC) != 0 ? O_SYNC : 0;
    flags |= (flag & SN_O_TRUNC) != 0 ? O_TRUNC : 0;
    char *p = cpath(path);
    if (p == NULL) {
        return errno;
    }

    // The sticky bit of a file this call makes is set after it, where
    // the system drops it.
    struct stat st;
    bool sticky = (flag & SN_O_CREATE) != 0 && (perm & SN_MODE_STICKY) != 0 && stat(p, &st) != 0 && errno == ENOENT;
    int f;
    do {
        f = open(p, flags | O_CLOEXEC, mode_to_sys(perm));
    } while (f < 0 && errno == EINTR);
    int code = f < 0 ? errno : 0;
    if (code == 0 && sticky) {
        (void)set_sticky(p);
    }

    sn_free(p);
    if (code != 0) {
        return code;
    }

    *fd = f;
    return 0;
}

// sn_os_readDir reads the names in a directory into out, each as the
// four bytes of its os.FileMode, the bits of its kind alone, lowest
// first, then its name and a zero byte, which no name holds. The kind
// is the one the directory gives, and the file's own, by lstat, where
// the directory gives none; a name removed between the two is left
// out.
// On a failure op names the call that failed: open, or the read of the
// directory, readdirent on Linux and readdir on Darwin.
SN_HELD int64_t sn_os_readDir(sn_string path, sn_slice_u8 *out, sn_string *op) {
#if defined(__APPLE__)
    const sn_string read_op = SN_STR("readdir", 7);
#else
    const sn_string read_op = SN_STR("readdirent", 10);
#endif
    sn_string_drop(op);
    *op = SN_STR("open", 4);
    int fd = sysopen(path, O_RDONLY | O_DIRECTORY);
    if (fd < 0) {
        return errno;
    }

    DIR *d = fdopendir(fd);
    if (d == NULL) {
        int code = errno;
        close(fd);
        return code;
    }

    sn_slice *buf = (sn_slice *)out;
    buf->len = 0;
    for (;;) {
        errno = 0;
        struct dirent *e = readdir(d);
        if (e == NULL) {
            if (errno != 0) {
                int code = errno;
                closedir(d);
                *op = read_op;
                return code;
            }

            break;
        }

        const char *name = e->d_name;
        if (name[0] == '.' && (name[1] == '\0' || (name[1] == '.' && name[2] == '\0'))) {
            continue;
        }

        uint32_t type; // the os.FileMode bits of the entry's kind
        switch (e->d_type) {
        case DT_REG:
            type = 0;
            break;
        case DT_DIR:
            type = SN_MODE_DIR;
            break;
        case DT_LNK:
            type = SN_MODE_SYMLINK;
            break;
        case DT_FIFO:
            type = SN_MODE_PIPE;
            break;
        case DT_SOCK:
            type = SN_MODE_SOCKET;
            break;
        case DT_CHR:
            type = SN_MODE_DEVICE | SN_MODE_CHAR;
            break;
        case DT_BLK:
            type = SN_MODE_DEVICE;
            break;
        default: {
            struct stat st;
            if (fstatat(fd, name, &st, AT_SYMLINK_NOFOLLOW) != 0) {
                if (errno == ENOENT) {
                    continue;
                }

                int code = errno;
                closedir(d);
                *op = read_op;
                return code;
            }

            type = mode_from_sys(st.st_mode) & SN_MODE_TYPE;
        }
        }

        int64_t n = (int64_t)strlen(name);
        if (buf->cap - buf->len < n + 5) {
            sn_slice_grow(buf, buf->len + n + 5, 1);
        }

        uint8_t *at = (uint8_t *)buf->ptr + buf->len;
        at[0] = (uint8_t)type;
        at[1] = (uint8_t)(type >> 8);
        at[2] = (uint8_t)(type >> 16);
        at[3] = (uint8_t)(type >> 24);
        memcpy(at + 4, name, (size_t)n + 1);
        buf->len += n + 5;
    }

    closedir(d);
    return 0;
}

// sn_os_Getpid is os.Getpid.
SN_HELD int64_t sn_os_Getpid(void) { return (int64_t)getpid(); }

// sn_os_hostname is os.Hostname: the name the system gives the machine.
SN_HELD int64_t sn_os_hostname(sn_string *out) {
    char name[256];
    if (gethostname(name, sizeof name) != 0) {
        return errno;
    }

    name[sizeof name - 1] = '\0';
    sn_string_drop(out);
    *out = sn_string_clone(SN_STR(name, (int64_t)strlen(name)));
    return 0;
}
#endif

// The program's environment is a copy the runtime makes of the one the
// process was started with, the first time it is asked about, and keeps
// under a lock: a read finds a variable through a hash index and
// copies its value out, and a write changes the copy alone. The lock
// is the one sync.RwLock is, which a reader takes with one atomic add.
// The C library's environment is never written, since its setenv
// moves memory that another thread's getenv may be reading, which no
// lock of the runtime's can stop a C library doing.
//
// The variables the process started with are copied into one block that
// lives as long as the program, and each is lent from it, as a literal
// and an argument of the command line are: a string that owns nothing,
// which a read hands out without copying, since nothing frees or
// changes the block. A variable the program set owns its bytes, which a
// later Setenv or Unsetenv frees, so a read copies it out.
static struct {
    int64_t lock; // a sync.RwLock's handle
    sn_once once;
    uint8_t *block; // the variables the process started with, one after another
    sn_string
        *entries; // KEY=VALUE each, lent from block or owning its bytes, and one of no bytes where a variable was unset
    int64_t len, cap;
    int32_t *slots; // the index: an entry's number and one, 0 for a free slot, -1 for one whose variable was unset
    int64_t nslots; // a power of two, at least twice cap: every slot that is not free was an entry's, so half are free
} env = { .once = SN_ONCE_INIT };

#if defined(_WIN32)
// sn_os_environ_utf8 returns the process's environment as UTF-8,
// KEY=VALUE each, in an array ended by NULL, read by os_windows.c from
// the system's wide block; the array and its strings are the caller's,
// from the C library's malloc.
char **sn_os_environ_utf8(void);
#else
extern char **environ;
#endif

#if defined(_WIN32)
// On Windows a variable's name is matched with no regard to case, as the
// system matches it, so the index hashes and compares names with their
// ASCII letters folded to lower case; env_fold folds a name's byte.
static inline uint8_t env_fold(uint8_t c) { return c >= 'A' && c <= 'Z' ? (uint8_t)(c + ('a' - 'A')) : c; }

SN_HELD_STATIC static uint64_t env_hash(const uint8_t *key, int64_t n) {
    uint8_t few[64];
    uint8_t *folded = n <= (int64_t)sizeof few ? few : sn_alloc((size_t)n);
    for (int64_t i = 0; i < n; i++) {
        folded[i] = env_fold(key[i]);
    }

    uint64_t h = sn_hash_bytes(folded, (size_t)n);
    if (folded != few) {
        sn_free(folded);
    }

    return h;
}

SN_HELD_STATIC static bool env_same_key(const uint8_t *a, const uint8_t *b, int64_t n) {
    for (int64_t i = 0; i < n; i++) {
        if (env_fold(a[i]) != env_fold(b[i])) {
            return false;
        }
    }

    return true;
}
#else
// On Linux and macOS a variable's name is its bytes.
static inline uint64_t env_hash(const uint8_t *key, int64_t n) { return sn_hash_bytes(key, (size_t)n); }

static inline bool env_same_key(const uint8_t *a, const uint8_t *b, int64_t n) { return memcmp(a, b, (size_t)n) == 0; }
#endif

// env_key_len is the length of the key of KEY=VALUE, or -1 without an =.
SN_HELD_STATIC static int64_t env_key_len(sn_string kv) {
    const uint8_t *eq = memchr(kv.ptr, '=', (size_t)kv.len);
    return eq == NULL ? -1 : (int64_t)(eq - kv.ptr);
}

// env_find returns the slot of key in the index, or the slot to put it
// in when it is not there, with found saying which.
SN_HELD_STATIC static int64_t env_find(const uint8_t *key, int64_t n, bool *found) {
    int64_t mask = env.nslots - 1;
    int64_t at = (int64_t)(env_hash(key, n) & (uint64_t)mask);
    int64_t spare = -1;
    for (;; at = (at + 1) & mask) {
        int32_t slot = env.slots[at];
        if (slot == 0) {
            *found = false;
            return spare >= 0 ? spare : at;
        }

        if (slot < 0) {
            if (spare < 0) {
                spare = at;
            }

            continue;
        }

        sn_string kv = env.entries[slot - 1];
        if (kv.len > n && kv.ptr[n] == '=' && env_same_key(kv.ptr, key, n)) {
            *found = true;
            return at;
        }
    }
}

// env_rebuild builds the index anew over the entries that hold a
// variable, moved together first, with room for as many again when
// they fill more than half of it.
SN_HELD_STATIC static void env_rebuild(void) {
    int64_t live = 0;
    for (int64_t i = 0; i < env.len; i++) {
        if (env.entries[i].len > 0) {
            env.entries[live++] = env.entries[i];
        }
    }

    env.len = live;
    if (live * 2 > env.cap) {
        env.cap *= 2;
        sn_string *more = sn_alloc((size_t)env.cap * sizeof *more);
        memcpy(more, env.entries, (size_t)env.len * sizeof *more);
        sn_free(env.entries);
        env.entries = more;
    }

    int64_t n = 64;
    while (n < env.cap * 2) {
        n *= 2;
    }

    sn_free(env.slots);
    env.slots = sn_alloc((size_t)n * sizeof *env.slots);
    memset(env.slots, 0, (size_t)n * sizeof *env.slots);
    env.nslots = n;
    for (int64_t i = 0; i < env.len; i++) {
        bool found;
        int64_t at = env_find(env.entries[i].ptr, env_key_len(env.entries[i]), &found);
        if (found) { // a second mention of a key: the first stands
            sn_string_drop(&env.entries[i]);
            env.entries[i] = (sn_string){ 0 };
            continue;
        }

        env.slots[at] = (int32_t)(i + 1);
    }
}

// env_copy takes the process's environment, the entries with a key,
// into the block.
SN_HELD static void env_copy(void) {
    env.lock = sn_sync_newRwLock();
#if defined(_WIN32)
    char **vars = sn_os_environ_utf8();
#else
    char **vars = environ;
#endif
    int64_t n = 0, bytes = 0;
    while (vars != NULL && vars[n] != NULL) {
        bytes += (int64_t)strlen(vars[n]);
        n++;
    }

    env.cap = n < 16 ? 16 : n;
    env.entries = sn_alloc((size_t)env.cap * sizeof *env.entries);
    env.block = bytes > 0 ? sn_alloc((size_t)bytes) : NULL;
    uint8_t *at = env.block;
    for (int64_t i = 0; i < n; i++) {
        sn_string kv = SN_STR(vars[i], (int64_t)strlen(vars[i]));
        if (env_key_len(kv) > 0) {
            memcpy(at, kv.ptr, (size_t)kv.len);
            env.entries[env.len++] = SN_STR(at, kv.len);
            at += kv.len;
        }
    }

#if defined(_WIN32)
    for (int64_t i = 0; i < n; i++) {
        free(vars[i]);
    }

    free(vars);
#endif
    env_rebuild();
}

// env_load makes the copy the first time the environment is asked
// about; after, it reads the once's word, done.
static inline void env_load(void) { sn_once_call(&env.once, env_copy); }

// sn_os_lookupEnv is os.LookupEnv: the value of key, copied out, and
// whether the variable is set.
SN_HELD bool sn_os_lookupEnv(sn_string key, sn_string *value) {
    sn_check_string(key);
    env_load();
    if (key.len == 0) {
        return false;
    }

    sn_sync_lockShared(env.lock);
    bool found;
    int64_t at = env_find(key.ptr, key.len, &found);
    if (found) {
        sn_string kv = env.entries[env.slots[at] - 1];
        sn_string v = SN_STR(kv.ptr + key.len + 1, kv.len - key.len - 1);
        sn_string_drop(value);
        *value = kv.cap == 0 ? v : sn_string_clone(v); // lent from the block, or copied from bytes a Setenv may free
    }

    sn_sync_unlockShared(env.lock);
    return found;
}

// sn_os_setenv is os.Setenv: an empty key, one holding an = or a zero
// byte, and a value holding a zero byte are invalid arguments.
SN_HELD int64_t sn_os_setenv(sn_string key, sn_string value) {
    sn_check_string(key);
    sn_check_string(value);
    env_load();
    if (key.len == 0 || memchr(key.ptr, '=', (size_t)key.len) != NULL || memchr(key.ptr, 0, (size_t)key.len) != NULL ||
        memchr(value.ptr, 0, (size_t)value.len) != NULL) {
        return EINVAL;
    }

    sn_string kv = sn_join(sn_join(key, SN_STR("=", 1), 0), value, 1);
    sn_sync_lock(env.lock);
    bool found;
    int64_t at = env_find(key.ptr, key.len, &found);
    if (found) {
        sn_string *old = &env.entries[env.slots[at] - 1];
        sn_string_drop(old);
        *old = kv;
    } else {
        if (env.len == env.cap) { // no room: the unset ones go, and the index is made anew
            env_rebuild();
            at = env_find(key.ptr, key.len, &found);
        }

        env.entries[env.len] = kv;
        env.slots[at] = (int32_t)(++env.len);
    }

    sn_sync_unlock(env.lock);
#if defined(_WIN32)
    sn_os_env_system(key, &value);
#endif
    return 0;
}

// sn_os_Unsetenv is os.Unsetenv.
SN_HELD void sn_os_Unsetenv(sn_string key) {
    sn_check_string(key);
    env_load();
    if (key.len == 0) {
        return;
    }

    sn_sync_lock(env.lock);
    bool found;
    int64_t at = env_find(key.ptr, key.len, &found);
    if (found) {
        sn_string *old = &env.entries[env.slots[at] - 1];
        sn_string_drop(old);
        *old = (sn_string){ 0 };
        env.slots[at] = -1;
    }
#if defined(_WIN32)
    sn_os_env_system(key, NULL);
#endif

    sn_sync_unlock(env.lock);
}

#if !defined(_WIN32) // os_windows.c has Windows'
// sn_os_TempDir is os.TempDir: the directory for temporary files, the
// TMPDIR of the environment, or /tmp when it is unset or empty.
SN_HELD sn_string sn_os_TempDir(void) {
    sn_string dir = { 0 };
    if (!sn_os_lookupEnv(SN_STR("TMPDIR", 6), &dir) || dir.len == 0) {
        sn_string_drop(&dir);
        return SN_STR("/tmp", 4);
    }

    return dir;
}
#endif

// sn_os_environ is os.Environ's: every variable as KEY=VALUE, lent from
// the block or a copy the slice owns, in a slice made once for their
// count, read under the lock so that it is the environment of one
// moment. out is a
// []string that holds nothing, whose layout is every slice's.
SN_HELD void sn_os_environ(void *out) {
    sn_slice *vars = out;
    env_load();
    sn_sync_lockShared(env.lock);
    int64_t n = 0;
    for (int64_t i = 0; i < env.len; i++) {
        n += env.entries[i].len > 0;
    }

    if (n > 0) {
        sn_slice_make(vars, 0, n, sizeof(sn_string));
        sn_string *at = vars->ptr;
        for (int64_t i = 0; i < env.len; i++) {
            if (env.entries[i].len > 0) {
                at[vars->len++] = env.entries[i].cap == 0 ? env.entries[i] : sn_string_clone(env.entries[i]);
            }
        }
    }

    sn_sync_unlockShared(env.lock);
}

// sn_os_env_free releases the copy of the environment, at the end of a
// debug build, which counts every block.
SN_HELD void sn_os_env_free(void) {
    for (int64_t i = 0; i < env.len; i++) {
        sn_string_drop(&env.entries[i]);
    }

    sn_free(env.entries);
    sn_free(env.slots);
    sn_free(env.block);
    env.block = NULL;
    if (env.lock != 0) {
        sn_sync_freeLock(env.lock);
        env.lock = 0;
    }

    env.entries = NULL;
    env.slots = NULL;
    env.len = env.cap = env.nslots = 0;
}

// The standard streams are written as any file is, straight to the
// descriptor with nothing held back: what fmt prints, what is written
// to os.Stdout and os.Stderr, and a panic's message come out in the
// order the program made them, a prompt without a newline shows at
// once, and a write that fails reports the
// failure. fmt builds each call's text whole and writes it at once, so
// two tasks' lines do not interleave.
// sn_os_write writes all of buf to the file and stores how much it
// wrote: all of it, or on a failure, a disk that filled, what went
// before it, which the library's Error carries.
SN_HELD int64_t sn_os_write(int64_t fd, sn_slice_u8 buf, int64_t *n) {
    sn_check_slice((sn_slice *)&buf);
    *n = sn_write_fd((int)fd, buf.ptr, buf.len);
    return *n < buf.len ? errno : 0;
}

SN_HELD int64_t sn_os_writeString(int64_t fd, sn_string s, int64_t *n) {
    sn_check_string(s);
    *n = sn_write_fd((int)fd, s.ptr, s.len);
    return *n < s.len ? errno : 0;
}

#if !defined(_WIN32) // os_windows.c has Windows'
SN_HELD int64_t sn_os_read(int64_t fd, sn_slice_u8 *buf, int64_t *n) {
    sn_check_slice((sn_slice *)buf);
    ssize_t got = sysread((int)fd, buf->ptr, (size_t)buf->len);
    if (got < 0) {
        return errno;
    }

    *n = got;
    return 0;
}

// sn_os_pread fills buf from the file at the offset off, leaving the
// file's position where it was, and stores how many bytes it read: the
// system is asked as often as it takes to fill buf, each call for at
// most SN_IO_MAX bytes, so the count is short only at the end of the
// file and zero at or past it; a read a signal interrupted is asked
// again.
SN_HELD int64_t sn_os_pread(int64_t fd, sn_slice_u8 *buf, uint64_t off, int64_t *n) {
    sn_check_slice((sn_slice *)buf);
    *n = 0;
    if (off > (uint64_t)INT64_MAX) {
        return 0; // past the end of any file: nothing to read
    }

    while (*n < buf->len) {
        size_t want = (size_t)(buf->len - *n);
        ssize_t got = pread((int)fd, buf->ptr + *n, want < SN_IO_MAX ? want : SN_IO_MAX, (off_t)(off + (uint64_t)*n));
        if (got < 0) {
            if (errno == EINTR) {
                continue;
            }

            return errno;
        }

        if (got == 0) {
            break; // the end of the file
        }

        *n += got;
    }

    return 0;
}

// sn_os_pwrite writes all of buf to the file at the offset off, leaving
// the file's position where it was, asking the system as often as it
// takes, and stores how many bytes it wrote, all of them or those
// before a failure.
SN_HELD int64_t sn_os_pwrite(int64_t fd, sn_slice_u8 buf, uint64_t off, int64_t *n) {
    sn_check_slice((sn_slice *)&buf);
    if (off > (uint64_t)INT64_MAX) {
        return EINVAL;
    }

    *n = 0;
    while (*n < buf.len) {
        size_t want = (size_t)(buf.len - *n);
        ssize_t wrote = pwrite((int)fd, buf.ptr + *n, want < SN_IO_MAX ? want : SN_IO_MAX, (off_t)(off + (uint64_t)*n));
        if (wrote < 0) {
            if (errno == EINTR) {
                continue;
            }

            return errno;
        }

        *n += wrote;
    }

    return 0;
}
#endif
