// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Files and directories, and the reads and writes of the io and os
// packages, with the reasons of their errors.

#define _POSIX_C_SOURCE 200809L
#define _DEFAULT_SOURCE // glibc's kinds of a directory's entries and the sticky bit under -std=c11
#define _DARWIN_C_SOURCE

#include "internal.h"

#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <pthread.h>
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

// read_whole reads the named file whole, into one block of the size the
// file says it has, as Go's os.ReadFile does: one byte more, so that
// the read finding the end needs no room, and 512 bytes for a file that
// says nothing, a pipe or a file of /proc, which grows by doubling. On
// a failure, inRead says which call failed, 0 for the open and 1 for a
// read, so that the error names it as Go's does.
//
// sn_os_readToString is os.ReadToString: the string owns that block,
// with no copy.
static int64_t read_whole(sn_string path, sn_slice *buf, int64_t *inRead) {
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

int64_t sn_os_readToString(sn_string path, sn_string *out, int64_t *inRead) {
    sn_slice buf = {0};
    int64_t code = read_whole(path, &buf, inRead);
    if (code != 0) {
        return code;
    }

    sn_string_drop(out);
    *out = (sn_string){buf.ptr, buf.len, buf.cap};
    return 0;
}

// sn_os_readFile is os.ReadFile: the same read, into bytes the slice
// owns.
int64_t sn_os_readFile(sn_string path, sn_slice_u8 *out, int64_t *inRead) {
    sn_slice buf = {0};
    int64_t code = read_whole(path, &buf, inRead);
    if (code != 0) {
        return code;
    }

    sn_slice_free((sn_slice *)out);
    *out = (sn_slice_u8){buf.ptr, buf.len, buf.cap};
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
static int64_t mkdir_temp(sn_string dir, sn_string pattern, sn_string *out, sn_string *op) {
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

int64_t sn_os_mkdirTemp(sn_string dir, sn_string pattern, sn_string *out, sn_string *op) {
    if (dir.len > 0) {
        return mkdir_temp(dir, pattern, out, op);
    }

    sn_string tmp = sn_os_TempDir();
    int64_t code = mkdir_temp(tmp, pattern, out, op);
    sn_string_free(tmp);
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

// The bits of an os.FileMode, which are Go's: the permissions in the
// low nine, and a bit for each kind of file that is not a regular one.
#define SN_MODE_DIR (UINT32_C(1) << 31)
#define SN_MODE_SYMLINK (UINT32_C(1) << 27)
#define SN_MODE_DEVICE (UINT32_C(1) << 26)
#define SN_MODE_PIPE (UINT32_C(1) << 25)
#define SN_MODE_SOCKET (UINT32_C(1) << 24)
#define SN_MODE_SETUID (UINT32_C(1) << 23)
#define SN_MODE_SETGID (UINT32_C(1) << 22)
#define SN_MODE_CHAR (UINT32_C(1) << 21)
#define SN_MODE_STICKY (UINT32_C(1) << 20)
#define SN_MODE_IRREGULAR (UINT32_C(1) << 19)
#define SN_MODE_TYPE (SN_MODE_DIR | SN_MODE_SYMLINK | SN_MODE_DEVICE | SN_MODE_PIPE | SN_MODE_SOCKET | SN_MODE_CHAR | SN_MODE_IRREGULAR)

// mode_from_sys is the os.FileMode of a file the system reports the mode
// of, as Go's fillFileStatFromSys makes it.
static uint32_t mode_from_sys(mode_t m) {
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
// directory is made with, as Go's syscallMode makes it.
static mode_t mode_to_sys(uint32_t perm) {
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
// BSDs, as Go's setStickyBit does after the make.
static int set_sticky(const char *p) {
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
int64_t sn_os_stat(sn_string path, bool follow, int64_t *size, uint32_t *mode, int64_t *sec, int64_t *nsec, uint64_t *dev, uint64_t *ino) {
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
int64_t sn_os_mkdir(sn_string path, uint32_t perm) {
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
int64_t sn_os_rename(sn_string from, sn_string to) {
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
int64_t sn_os_chdir(sn_string path) {
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
int64_t sn_os_getcwd(sn_string *out) {
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

// The flags of os.OpenFile, which are Snello's own numbers, the same on
// every system: Go's differ from one system to the next, as the
// system's do.
#define SN_O_WRONLY 1
#define SN_O_RDWR 2
#define SN_O_APPEND 8
#define SN_O_CREATE 64
#define SN_O_EXCL 128
#define SN_O_SYNC 256
#define SN_O_TRUNC 512

// sn_os_openFile is os.OpenFile. A flag the package does not name, or
// both of O_WRONLY and O_RDWR, is an invalid argument.
int64_t sn_os_openFile(sn_string path, int64_t flag, uint32_t perm, int64_t *fd) {
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
    // the system drops it, as Go's openFileNolog does.
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
// the directory gives none, as Go's ReadDir finds it; a name removed
// between the two is left out.
// On a failure op names the call that failed as Go's error names it:
// open, or the read of the directory, which Go calls readdirent on
// Linux and readdir on Darwin.
int64_t sn_os_readDir(sn_string path, sn_slice_u8 *out, sn_string *op) {
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
int64_t sn_os_Getpid(void) {
    return (int64_t)getpid();
}

// sn_os_hostname is os.Hostname: the name the system gives the machine.
int64_t sn_os_hostname(sn_string *out) {
    char name[256];
    if (gethostname(name, sizeof name) != 0) {
        return errno;
    }

    name[sizeof name - 1] = '\0';
    sn_string_drop(out);
    *out = sn_string_clone(SN_STR(name, (int64_t)strlen(name)));
    return 0;
}

// The program's environment is a copy the runtime makes of the one the
// process was started with, the first time it is asked about, and keeps
// under a lock, as Go's syscall package keeps its own: a read finds a
// variable through a hash index and copies its value out, and a write
// changes the copy alone. The lock is the one sync.RwLock is, Go's
// RWMutex, which a reader takes with one atomic add. The C library's
// environment is never written, since its setenv moves memory that
// another thread's getenv may be reading, which no lock of the
// runtime's can stop a C library doing.
//
// The variables the process started with are copied into one block that
// lives as long as the program, and each is lent from it, as a literal
// and an argument of the command line are: a string that owns nothing,
// which a read hands out without copying, since nothing frees or
// changes the block. A variable the program set owns its bytes, which a
// later Setenv or Unsetenv frees, so a read copies it out.
static struct {
    int64_t lock; // a sync.RwLock's handle
    pthread_once_t once;
    _Atomic bool ready; // the copy is made: what every call after the first reads, in place of asking once
    uint8_t *block;     // the variables the process started with, one after another
    sn_string *entries; // KEY=VALUE each, lent from block or owning its bytes, and one of no bytes where a variable was unset
    int64_t len, cap;
    int32_t *slots; // the index: an entry's number and one, 0 for a free slot, -1 for one whose variable was unset
    int64_t nslots; // a power of two, at least twice cap: every slot that is not free was an entry's, so half are free
} env = {.once = PTHREAD_ONCE_INIT};

extern char **environ;

// env_key_len is the length of the key of KEY=VALUE, or -1 without an =.
static int64_t env_key_len(sn_string kv) {
    const uint8_t *eq = memchr(kv.ptr, '=', (size_t)kv.len);
    return eq == NULL ? -1 : (int64_t)(eq - kv.ptr);
}

// env_find returns the slot of key in the index, or the slot to put it
// in when it is not there, with found saying which.
static int64_t env_find(const uint8_t *key, int64_t n, bool *found) {
    int64_t mask = env.nslots - 1;
    int64_t at = (int64_t)(sn_hash_bytes(key, (size_t)n) & (uint64_t)mask);
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
        if (kv.len > n && kv.ptr[n] == '=' && memcmp(kv.ptr, key, (size_t)n) == 0) {
            *found = true;
            return at;
        }
    }
}

// env_rebuild builds the index anew over the entries that hold a
// variable, moved together first, with room for as many again when
// they fill more than half of it.
static void env_rebuild(void) {
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
        if (found) { // a second mention of a key: the first stands, as Go keeps it
            sn_string_drop(&env.entries[i]);
            env.entries[i] = (sn_string){0};
            continue;
        }

        env.slots[at] = (int32_t)(i + 1);
    }
}

// env_copy takes the process's environment, the entries with a key,
// into the block.
static void env_copy(void) {
    env.lock = sn_sync_newRwLock();
    int64_t n = 0, bytes = 0;
    while (environ != NULL && environ[n] != NULL) {
        bytes += (int64_t)strlen(environ[n]);
        n++;
    }

    env.cap = n < 16 ? 16 : n;
    env.entries = sn_alloc((size_t)env.cap * sizeof *env.entries);
    env.block = bytes > 0 ? sn_alloc((size_t)bytes) : NULL;
    uint8_t *at = env.block;
    for (int64_t i = 0; i < n; i++) {
        sn_string kv = SN_STR(environ[i], (int64_t)strlen(environ[i]));
        if (env_key_len(kv) > 0) {
            memcpy(at, kv.ptr, (size_t)kv.len);
            env.entries[env.len++] = SN_STR(at, kv.len);
            at += kv.len;
        }
    }

    env_rebuild();
    atomic_store_explicit(&env.ready, true, memory_order_release);
}

// env_load makes the copy the first time the environment is asked
// about, and reads a flag after.
static inline void env_load(void) {
    if (!atomic_load_explicit(&env.ready, memory_order_acquire)) {
        pthread_once(&env.once, env_copy);
    }
}

// sn_os_lookupEnv is os.LookupEnv: the value of key, copied out, and
// whether the variable is set.
bool sn_os_lookupEnv(sn_string key, sn_string *value) {
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
// byte, and a value holding a zero byte are invalid arguments, as Go's
// are.
int64_t sn_os_setenv(sn_string key, sn_string value) {
    sn_check_string(key);
    sn_check_string(value);
    env_load();
    if (key.len == 0 || memchr(key.ptr, '=', (size_t)key.len) != NULL || memchr(key.ptr, 0, (size_t)key.len) != NULL || memchr(value.ptr, 0, (size_t)value.len) != NULL) {
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
    return 0;
}

// sn_os_Unsetenv is os.Unsetenv.
void sn_os_Unsetenv(sn_string key) {
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
        *old = (sn_string){0};
        env.slots[at] = -1;
    }

    sn_sync_unlock(env.lock);
}

// sn_os_TempDir is os.TempDir: the directory for temporary files, the
// TMPDIR of the environment, or /tmp when it is unset or empty.
sn_string sn_os_TempDir(void) {
    sn_string dir = {0};
    if (!sn_os_lookupEnv(SN_STR("TMPDIR", 6), &dir) || dir.len == 0) {
        sn_string_drop(&dir);
        return SN_STR("/tmp", 4);
    }

    return dir;
}

// sn_os_environ is os.Environ's: every variable as KEY=VALUE, lent from
// the block or a copy the slice owns, in a slice made once for their
// count, read under the lock so that it is the environment of one
// moment. out is a
// []string that holds nothing, whose layout is every slice's.
void sn_os_environ(void *out) {
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
void sn_os_env_free(void) {
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
