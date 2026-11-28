// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The os package's calls on Windows, which os.c leaves out there: a
// path is converted from Snello's UTF-8 to the system's UTF-16 at each
// call, a file is opened with CreateFileW and kept as a descriptor of
// the C library's, which every read, write and close of the runtime
// takes as it takes one elsewhere, and the directories, a file's facts,
// the host's name and the temporary directory are the system's own
// calls. A failure is reported as the error number the same failure has
// on the other systems, which io.Kind and the messages read.

#if defined(_WIN32)

#include "internal.h"

#include <errno.h>
#include <fcntl.h>
#include <io.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <tlhelp32.h> // the process list, for sn_os_Getppid
#include <wchar.h>
#include <winioctl.h> // FSCTL_GET_REPARSE_POINT, for sn_os_readlink

// What a removal and a directory's read say failed, as the library's
// errors name the call, and the separator a path's parts are joined
// with.
#define OP_REMOVE  "remove"
#define OP_OPEN    "open"
#define OP_READDIR "readdir"
#define SEPARATOR  "\\"

// The permission bits a file's attributes give it: a read-only file is
// read by all, any other read and written by all, and a directory is
// searched by all besides, as the os package reports a mode where the
// system keeps none.
enum { mode_read_only = 0444, mode_read_write = 0666, mode_owner = 0600, mode_search = 0111, mode_write = 0200 };

// The shortest path the system's path calls refuse where long paths
// are not enabled, a directory's limit, room left in MAX_PATH for a
// file's name of 8.3; the prefix under which they take a path as it is,
// and its form for a share's; the length of the first.
enum { long_path = 248, verbatim_len = 4, verbatim_share_len = 8 };
#define VERBATIM       L"\\\\?\\"
#define VERBATIM_SHARE L"\\\\?\\UNC\\"

// The UTF-8 bytes converted for a console at a time, and the most a
// character takes; the character that ends a console's input when a
// line starts with it, Ctrl-Z.
enum { console_chunk = 256, utf8_most = 4, console_end = 0x1A };

// Whether a standard stream is a console: not yet known, or known.
enum { console_unknown = 0, console_yes = 1, console_no = 2 };

// The time a file's facts are given in: ticks of 100 nanoseconds since
// the start of 1601, filetime_epoch ticks before the start of 1970.
enum { tick_ns = 100, ticks_per_s = 10000000 };
#define FILETIME_EPOCH UINT64_C(116444736000000000)

// sn_errno_of is the error number of a failure the system reports by
// its own code, the one the same failure has on the other systems.
SN_HELD int sn_errno_of(unsigned long code) {
    switch (code) {
    case ERROR_FILE_NOT_FOUND:
    case ERROR_PATH_NOT_FOUND:
    case ERROR_INVALID_DRIVE:
    case ERROR_BAD_NETPATH:
    case ERROR_BAD_NET_NAME:
        return ENOENT;
    case ERROR_ACCESS_DENIED:
    case ERROR_SHARING_VIOLATION:
    case ERROR_LOCK_VIOLATION:
        return EACCES;
    case ERROR_PRIVILEGE_NOT_HELD:
        return EPERM;
    case ERROR_INVALID_HANDLE:
        return EBADF;
    case ERROR_CANT_RESOLVE_FILENAME:
        return ELOOP;
    case ERROR_NOT_SUPPORTED:
        return ENOTSUP;
    case ERROR_FILE_EXISTS:
    case ERROR_ALREADY_EXISTS:
        return EEXIST;
    case ERROR_DIR_NOT_EMPTY:
        return ENOTEMPTY;
    case ERROR_DIRECTORY:
        return ENOTDIR;
    case ERROR_INVALID_NAME:
    case ERROR_INVALID_PARAMETER:
    case ERROR_BAD_PATHNAME:
    case ERROR_NEGATIVE_SEEK:
        return EINVAL;
    case ERROR_FILENAME_EXCED_RANGE:
        return ENAMETOOLONG;
    case ERROR_NOT_ENOUGH_MEMORY:
    case ERROR_OUTOFMEMORY:
        return ENOMEM;
    case ERROR_TOO_MANY_OPEN_FILES:
        return EMFILE;
    case ERROR_DISK_FULL:
    case ERROR_HANDLE_DISK_FULL:
        return ENOSPC;
    case ERROR_NOT_SAME_DEVICE:
        return EXDEV;
    case ERROR_WRITE_PROTECT:
        return EROFS;
    case ERROR_BROKEN_PIPE:
    case ERROR_NO_DATA:
        return EPIPE;
    }

    return EIO;
}

SN_HELD const char *sn_errno_text(int code) {
    switch (code) {
    case ENOENT:
        return "No such file or directory";
    case EACCES:
        return "Permission denied";
    case EPERM:
        return "Operation not permitted";
    case EEXIST:
        return "File exists";
    case ENOTEMPTY:
        return "Directory not empty";
    case ENOTDIR:
        return "Not a directory";
    case EISDIR:
        return "Is a directory";
    case EINVAL:
        return "Invalid argument";
    case ENAMETOOLONG:
        return "File name too long";
    case ENOMEM:
        return "Cannot allocate memory";
    case EMFILE:
        return "Too many open files";
    case ENOSPC:
        return "No space left on device";
    case EXDEV:
        return "Invalid cross-device link";
    case EROFS:
        return "Read-only file system";
    case EPIPE:
        return "Broken pipe";
    case EIO:
        return "Input/output error";
    case EBADF:
        return "Bad file descriptor";
    case ELOOP:
        return "Too many levels of symbolic links";
    case ENOTSUP:
    case EOPNOTSUPP:
        return "Operation not supported";
    case EAGAIN:
    case EWOULDBLOCK:
        return "Resource temporarily unavailable";
    case EINTR:
        return "Interrupted system call";
    case EINPROGRESS:
        return "Operation now in progress";
    case EALREADY:
        return "Operation already in progress";
    case EISCONN:
        return "Transport endpoint is already connected";
    case ENOTCONN:
        return "Transport endpoint is not connected";
    case ECONNREFUSED:
        return "Connection refused";
    case ECONNRESET:
        return "Connection reset by peer";
    case ECONNABORTED:
        return "Software caused connection abort";
    case ETIMEDOUT:
        return "Connection timed out";
    case EADDRINUSE:
        return "Address already in use";
    case EADDRNOTAVAIL:
        return "Cannot assign requested address";
    case EAFNOSUPPORT:
        return "Address family not supported by protocol";
    case EPROTONOSUPPORT:
        return "Protocol not supported";
    case ENETUNREACH:
        return "Network is unreachable";
    case EHOSTUNREACH:
        return "No route to host";
    case ENETRESET:
        return "Network dropped connection on reset";
    case ENETDOWN:
        return "Network is down";
    case ENOBUFS:
        return "No buffer space available";
    case ENOTSOCK:
        return "Socket operation on non-socket";
    case EMSGSIZE:
        return "Message too long";
    case ENOPROTOOPT:
        return "Protocol not available";
    }

    return NULL;
}

// last_errno is the error number of the system's last failure on this
// thread, set as errno too.
SN_HELD_STATIC static int last_errno(void) { return errno = sn_errno_of(GetLastError()); }

// copy_path is a copy of the zero-ended path w in a block of the
// runtime's.
SN_HELD_STATIC static wchar_t *copy_path(const wchar_t *w) {
    size_t n = wcslen(w) + 1;
    wchar_t *out = sn_alloc(n * sizeof *out);
    memcpy(out, w, n * sizeof *out);
    return out;
}

// verbatim is the path w made absolute and given the prefix under which
// the system takes a path as it is, in a new block of the runtime's: up
// to 32,767 units long, and with any name a directory's listing gives,
// one ending in a dot or a space among them. The system's own reading
// of the path, its separators, its . and .., is made first, by
// GetFullPathNameW, as the prefix turns it off. A path in a device's
// form, or already in this one, is copied as it is, and so is one the
// system cannot read, for the call it is made for to fail on.
SN_HELD_STATIC static wchar_t *verbatim(const wchar_t *w) {
    if (w[0] == L'\\' && w[1] == L'\\' && (w[2] == L'?' || w[2] == L'.') && w[3] == L'\\') {
        return copy_path(w);
    }

    DWORD room = GetFullPathNameW(w, 0, NULL, NULL);
    wchar_t *full = room == 0 ? NULL : sn_alloc((size_t)room * sizeof *full);
    DWORD n = full == NULL ? 0 : GetFullPathNameW(w, room, full, NULL);
    if (n == 0 || n >= room) {
        sn_free(full);
        return copy_path(w);
    }

    if (full[0] == L'\\' && full[1] == L'\\' && (full[2] == L'?' || full[2] == L'.')) {
        return full; // a device's name, CON or NUL, becomes \\.\CON
    }

    bool share = full[0] == L'\\' && full[1] == L'\\';
    const wchar_t *rest = share ? full + 2 : full; // a share's \\server\share is \\?\UNC\server\share
    size_t prefix = share ? verbatim_share_len : verbatim_len;
    size_t len = wcslen(rest);
    wchar_t *out = sn_alloc((prefix + len + 1) * sizeof *out);
    memcpy(out, share ? VERBATIM_SHARE : VERBATIM, prefix * sizeof *out);
    memcpy(out + prefix, rest, (len + 1) * sizeof *out);
    sn_free(full);
    return out;
}

// wide converts a Snello string to UTF-16 for the system, in a block of
// the runtime's. A string holding a zero byte, or bytes that are not
// UTF-8, cannot be given to the system and is refused as an invalid
// argument.
SN_HELD wchar_t *sn_os_wide(sn_string s) {
    sn_check_string(s);
    if (memchr(s.ptr, 0, (size_t)s.len) != NULL || s.len > INT32_MAX) {
        errno = EINVAL;
        return NULL;
    }

    int n =
        s.len == 0 ? 0 : MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, (const char *)s.ptr, (int)s.len, NULL, 0);
    if (s.len > 0 && n == 0) {
        errno = EINVAL;
        return NULL;
    }

    wchar_t *w = sn_alloc(((size_t)n + 1) * sizeof *w);
    if (n > 0) {
        MultiByteToWideChar(CP_UTF8, 0, (const char *)s.ptr, (int)s.len, w, n);
    }

    w[n] = L'\0';
    return w;
}

// wpath converts a Snello string to a UTF-16 path as wide does, a long
// one in verbatim's form, so that a path longer than the system's calls
// take on their own is taken as on the other systems.
SN_HELD_STATIC static wchar_t *wpath(sn_string s) {
    wchar_t *w = sn_os_wide(s);
    if (w == NULL || wcslen(w) < long_path) {
        return w;
    }

    wchar_t *v = verbatim(w);
    sn_free(w);
    return v;
}

// utf8 converts n UTF-16 units, all of a zero-ended string for n < 0,
// to an owned Snello string.
SN_HELD_STATIC static sn_string utf8(const wchar_t *w, int n) {
    int size = WideCharToMultiByte(CP_UTF8, 0, w, n, NULL, 0, NULL, NULL);
    if (n < 0 && size > 0) {
        size--; // the zero after it
    }

    if (size <= 0) {
        return SN_STR("", 0);
    }

    char *p = sn_alloc((size_t)size + 1);
    WideCharToMultiByte(CP_UTF8, 0, w, n, p, n < 0 ? size + 1 : size, NULL, NULL);
    return SN_OWNED((uint8_t *)p, size);
}

// shown is the path w as a message shows it, without the prefix
// verbatim gave it.
SN_HELD_STATIC static sn_string shown(const wchar_t *w) {
    if (wcsncmp(w, VERBATIM_SHARE, verbatim_share_len) == 0) {
        return sn_join(SN_STR("\\\\", 2), utf8(w + verbatim_share_len, -1), 2);
    }

    return utf8(wcsncmp(w, VERBATIM, verbatim_len) == 0 ? w + verbatim_len : w, -1);
}

// win_open opens a path for the access and the creation flags of the C
// library's open, with the sharing every Windows program expects, so
// that another may read, write, rename or remove the file while it is
// open, and a handle no program the process starts inherits; it returns
// a descriptor of the C library's, or -1 with errno set. perm is the
// mode a file the call makes is made with: a file with no write bit is
// read-only. O_SYNC, which the C library's flags lack, is sync.
SN_HELD_STATIC static int win_open(sn_string path, int flags, uint32_t perm, bool sync) {
    wchar_t *p = wpath(path);
    if (p == NULL) {
        return -1;
    }

    DWORD access = (flags & _O_RDWR)     ? GENERIC_READ | GENERIC_WRITE
                   : (flags & _O_WRONLY) ? GENERIC_WRITE
                                         : GENERIC_READ;
    if (flags & _O_APPEND) {
        access = (access & ~(DWORD)GENERIC_WRITE) | FILE_APPEND_DATA;
    }

    DWORD create = OPEN_EXISTING;
    if ((flags & (_O_CREAT | _O_EXCL)) == (_O_CREAT | _O_EXCL)) {
        create = CREATE_NEW;
    } else if ((flags & (_O_CREAT | _O_TRUNC)) == (_O_CREAT | _O_TRUNC)) {
        create = CREATE_ALWAYS;
    } else if (flags & _O_CREAT) {
        create = OPEN_ALWAYS;
    } else if (flags & _O_TRUNC) {
        create = TRUNCATE_EXISTING;
    }

    DWORD attrs = FILE_ATTRIBUTE_NORMAL;
    if ((flags & _O_CREAT) && (perm & mode_write) == 0) {
        attrs = FILE_ATTRIBUTE_READONLY;
    }

    if (sync) {
        attrs |= FILE_FLAG_WRITE_THROUGH;
    }

    DWORD share = FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE;
    HANDLE h = INVALID_HANDLE_VALUE;
    bool settled = false; // opened, or failed for a reason the open reports
    if (create == CREATE_ALWAYS && (attrs & FILE_ATTRIBUTE_READONLY)) {
        // A file that is there keeps its attributes, as it keeps its
        // mode on the other systems, which the creation would change:
        // it is truncated, and made only when it is not there.
        DWORD kept = attrs & FILE_FLAG_WRITE_THROUGH;
        h = CreateFileW(p, access, share, NULL, TRUNCATE_EXISTING, kept, NULL);
        DWORD failed = h == INVALID_HANDLE_VALUE ? GetLastError() : ERROR_SUCCESS;
        settled = failed != ERROR_FILE_NOT_FOUND && failed != ERROR_PATH_NOT_FOUND && failed != ERROR_BAD_NETPATH;
    }

    if (!settled) {
        DWORD directory = create == OPEN_EXISTING && access == GENERIC_READ ? FILE_FLAG_BACKUP_SEMANTICS
                                                                            : 0; // which opens a directory, read alone
        h = CreateFileW(p, access, share, NULL, create, attrs | directory, NULL);
    }

    sn_free(p);
    if (h == INVALID_HANDLE_VALUE) {
        last_errno();
        return -1;
    }

    int fd = _open_osfhandle((intptr_t)h, flags & (_O_APPEND | _O_RDONLY | _O_WRONLY | _O_RDWR));
    if (fd < 0) {
        int code = errno;
        CloseHandle(h);
        errno = code;
    }

    return fd;
}

// A standard stream's console, when it is one: whether it is, known
// at its first use, console_unknown before; on the write side, the
// first bytes of a character a write ended in the middle of, as a
// buffered writer's flush may, which the next write completes; on the
// read side, a line's text converted and not yet given, from at, and
// the first half of a pair of UTF-16 units a read ended in the middle
// of. The lock keeps a stream's writes, or its reads, whole.
typedef struct {
    sn_mutex mu;
    _Atomic int kind;
    uint8_t tail[utf8_most];
    int tail_len;
    uint8_t left[console_chunk * 3];
    int left_len;
    int left_at;
    wchar_t high;
} console_stream;

static console_stream consoles[3] = { { .mu = SN_MUTEX_INIT }, { .mu = SN_MUTEX_INIT }, { .mu = SN_MUTEX_INIT } };

SN_HELD bool sn_console(int fd) {
    if (fd < 0 || fd > 2) {
        return false;
    }

    int kind = atomic_load_explicit(&consoles[fd].kind, memory_order_relaxed);
    if (kind == console_unknown) {
        DWORD mode;
        kind = GetConsoleMode((HANDLE)_get_osfhandle(fd), &mode) ? console_yes : console_no;
        atomic_store_explicit(&consoles[fd].kind, kind, memory_order_relaxed);
    }

    return kind == console_yes;
}

// sn_console_reset forgets whether descriptor fd is a console, and the
// part of a character a write to it left, for a descriptor pointed
// elsewhere: the next write asks again.
SN_HELD void sn_console_reset(int fd) {
    if (fd < 0 || fd > 2) {
        return;
    }

    sn_mutex_lock(&consoles[fd].mu);
    consoles[fd].tail_len = 0;
    atomic_store_explicit(&consoles[fd].kind, console_unknown, memory_order_relaxed);
    sn_mutex_unlock(&consoles[fd].mu);
}

// whole_len is how many of the n bytes at p end on a character's
// boundary: all of them, or all but the first bytes of a character the
// last ones begin and do not finish. Bytes that are not UTF-8 are
// written as the replacement character, which the conversion makes.
SN_HELD_STATIC static int whole_len(const uint8_t *p, int n) {
    for (int back = 1; back < utf8_most && back <= n; back++) {
        uint8_t b = p[n - back];
        if ((b & 0xC0) == 0x80) { // a continuation byte: the character starts further back
            continue;
        }

        int need = b >= 0xF0 ? 4 : b >= 0xE0 ? 3 : b >= 0xC0 ? 2 : 1;
        return need > back ? n - back : n;
    }

    return n;
}

SN_HELD int64_t sn_console_write(int fd, const uint8_t *p, int64_t len) {
    console_stream *c = &consoles[fd];
    HANDLE h = (HANDLE)_get_osfhandle(fd);
    uint8_t bytes[utf8_most + console_chunk];
    wchar_t text[utf8_most + console_chunk];
    int64_t done = 0;
    sn_mutex_lock(&c->mu);
    while (done < len) {
        int n = c->tail_len;
        memcpy(bytes, c->tail, (size_t)n);
        int take = len - done < console_chunk ? (int)(len - done) : console_chunk;
        memcpy(bytes + n, p + done, (size_t)take);
        n += take;
        int whole = whole_len(bytes, n);
        c->tail_len = n - whole;
        memcpy(c->tail, bytes + whole, (size_t)c->tail_len);
        int units = whole == 0 ? 0
                               : MultiByteToWideChar(CP_UTF8, 0, (const char *)bytes, whole, text,
                                                     (int)(sizeof text / sizeof text[0]));
        for (int at = 0; at < units;) {
            DWORD wrote = 0;
            BOOL ok = WriteConsoleW(h, text + at, (DWORD)(units - at), &wrote, NULL);
            if (!ok || wrote == 0) { // a console that takes nothing would hold the write forever
                errno = ok ? EIO : sn_errno_of(GetLastError());
                sn_mutex_unlock(&c->mu);
                return done;
            }

            at += (int)wrote;
        }

        done += take;
    }

    sn_mutex_unlock(&c->mu);
    return done;
}

// console_read reads at most len bytes of a console's text into p: what
// the last read converted and did not give, or a line's text, as much
// as a read of the console gives, converted to UTF-8. It returns the
// count, 0 at the end, a line that starts with Ctrl-Z, or -1 with errno
// set.
SN_HELD_STATIC static int console_read(int fd, void *p, size_t len) {
    console_stream *c = &consoles[fd];
    sn_mutex_lock(&c->mu);
    while (c->left_at == c->left_len) {
        wchar_t text[console_chunk];
        int units = 0;
        if (c->high != 0) { // the first half of a pair, whose second this read brings
            text[units++] = c->high;
            c->high = 0;
        }

        DWORD got = 0;
        uint32_t handled = sn_signals_handled(); // before the read: an interrupt during it moves the count past this
        SetLastError(ERROR_SUCCESS);
        if (!ReadConsoleW((HANDLE)_get_osfhandle(fd), text + units, (DWORD)(console_chunk - units), &got, NULL)) {
            last_errno();
            sn_mutex_unlock(&c->mu);
            return -1;
        }

        if (got == 0 && GetLastError() == ERROR_OPERATION_ABORTED) {
            // Ctrl-C or Ctrl-Break interrupted the read, which succeeds
            // with nothing read: not the end of the input. When the
            // interrupt cancelled the program, the read fails as a
            // cancelled call does; else it is made again, and waits for
            // input as before.
            if (units > 0) { // the pair's first half the read was to complete waits again
                c->high = text[0];
            }

            sn_signals_wait_handler(handled); // the handler runs on a thread of its own, after the read returned
            if (sn_cancel_state(sn_self()) != SN_SCOPE_LIVE) {
                errno = ECANCELED;
                sn_mutex_unlock(&c->mu);
                return -1;
            }

            continue;
        }

        units += (int)got;
        if (got == 0 || text[0] == console_end) {
            sn_mutex_unlock(&c->mu);
            return 0;
        }

        if (text[units - 1] >= 0xD800 && text[units - 1] <= 0xDBFF) { // a pair the read cut: its first half waits
            c->high = text[--units];
        }

        c->left_at = 0;
        c->left_len =
            units == 0 ? 0
                       : WideCharToMultiByte(CP_UTF8, 0, text, units, (char *)c->left, (int)sizeof c->left, NULL, NULL);
    }

    size_t n = (size_t)(c->left_len - c->left_at) < len ? (size_t)(c->left_len - c->left_at) : len;
    memcpy(p, c->left + c->left_at, n);
    c->left_at += (int)n;
    sn_mutex_unlock(&c->mu);
    return (int)n;
}

// is_directory reports whether the handle is a directory's, which a
// read of it is refused for.
SN_HELD_STATIC static bool is_directory(HANDLE h) {
    BY_HANDLE_FILE_INFORMATION info;
    return GetFileInformationByHandle(h, &info) && (info.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY);
}

// win_read reads at most len bytes from the file the descriptor names,
// straight from its handle, with no lock of the C library's, or from
// its console's text, and returns
// the count, 0 at the end, which a pipe whose writer closed it is, or -1
// with errno set.
SN_HELD_STATIC static int win_read(int fd, void *p, size_t len) {
    if (sn_console(fd)) {
        return console_read(fd, p, len);
    }

    HANDLE h = (HANDLE)_get_osfhandle(fd);
    DWORD got = 0;
    BOOL ok = ReadFile(h, p, (DWORD)(len < SN_IO_MAX ? len : SN_IO_MAX), &got, NULL);
    DWORD failure = ok ? ERROR_SUCCESS : GetLastError();
    if ((!ok || got == 0) && is_directory(h)) { // a directory has no bytes to read, as on the other systems
        errno = EISDIR;
        return -1;
    }

    if (ok) {
        return (int)got;
    }

    if (failure == ERROR_BROKEN_PIPE || failure == ERROR_HANDLE_EOF) {
        return 0;
    }

    errno = sn_errno_of(failure);
    return -1;
}

// win_read_whole reads the named file whole into buf, in one block of the
// size the file says it has and a byte more, or of 512 bytes doubling
// for one that says none; inRead says which call failed, 0 for the open
// and 1 for a read.
SN_HELD_STATIC static int64_t win_read_whole(sn_string path, sn_slice *buf, int64_t *inRead) {
    *inRead = 0;
    int fd = win_open(path, _O_RDONLY, 0, false);
    if (fd < 0) {
        return errno;
    }

    int64_t size = 0;
    LARGE_INTEGER n;
    if (GetFileSizeEx((HANDLE)_get_osfhandle(fd), &n) && n.QuadPart > 0) {
        size = n.QuadPart;
    }

    sn_slice_grow(buf, size < 512 ? 512 : size + 1, 1);
    for (;;) {
        if (buf->len == buf->cap) {
            sn_slice_grow(buf, buf->len + 1, 1);
        }

        int got = win_read(fd, (char *)buf->ptr + buf->len, (size_t)(buf->cap - buf->len));
        if (got < 0) {
            int code = errno;
            _close(fd);
            sn_slice_free(buf);
            *inRead = 1;
            return code;
        }

        if (got == 0) {
            break;
        }

        buf->len += got;
    }

    _close(fd);
    return 0;
}

SN_HELD int64_t sn_os_readToString(sn_string path, sn_string *out, int64_t *inRead) {
    sn_slice buf = { 0 };
    int64_t code = win_read_whole(path, &buf, inRead);
    if (code != 0) {
        return code;
    }

    sn_string_drop(out);
    *out = (sn_string){ buf.ptr, buf.len, buf.cap };
    return 0;
}

SN_HELD int64_t sn_os_readFile(sn_string path, sn_slice_u8 *out, int64_t *inRead) {
    sn_slice buf = { 0 };
    int64_t code = win_read_whole(path, &buf, inRead);
    if (code != 0) {
        return code;
    }

    sn_slice_free((sn_slice *)out);
    *out = (sn_slice_u8){ buf.ptr, buf.len, buf.cap };
    return 0;
}

SN_HELD int64_t sn_os_open(sn_string path, int64_t *fd) {
    int f = win_open(path, _O_RDONLY, 0, false);
    if (f < 0) {
        return errno;
    }

    *fd = f;
    return 0;
}

SN_HELD int64_t sn_os_create(sn_string path, int64_t *fd) {
    int f = win_open(path, _O_RDWR | _O_CREAT | _O_TRUNC, mode_read_write, false); // read-write, as os.Create says
    if (f < 0) {
        return errno;
    }

    *fd = f;
    return 0;
}

SN_HELD int64_t sn_os_openFile(sn_string path, int64_t flag, uint32_t perm, int64_t *fd) {
    const int64_t known = SN_O_WRONLY | SN_O_RDWR | SN_O_APPEND | SN_O_CREATE | SN_O_EXCL | SN_O_SYNC | SN_O_TRUNC;
    if ((flag & ~known) != 0 || (flag & 3) == 3) {
        return EINVAL;
    }

    int flags = (flag & 3) == SN_O_WRONLY ? _O_WRONLY : (flag & 3) == SN_O_RDWR ? _O_RDWR : _O_RDONLY;
    flags |= (flag & SN_O_APPEND) != 0 ? _O_APPEND : 0;
    flags |= (flag & SN_O_CREATE) != 0 ? _O_CREAT : 0;
    flags |= (flag & SN_O_EXCL) != 0 ? _O_EXCL : 0;
    flags |= (flag & SN_O_TRUNC) != 0 ? _O_TRUNC : 0;
    int f = win_open(path, flags, perm, (flag & SN_O_SYNC) != 0);
    if (f < 0) {
        return errno;
    }

    *fd = f;
    return 0;
}

SN_HELD int64_t sn_os_read(int64_t fd, sn_slice_u8 *buf, int64_t *n) {
    sn_check_slice((sn_slice *)buf);
    int got = win_read((int)fd, buf->ptr, (size_t)buf->len);
    if (got < 0) {
        return errno;
    }

    *n = got;
    return 0;
}

// at_offset runs one ReadFile or WriteFile at the offset off of the
// file h, which an OVERLAPPED carries to a synchronous handle, for at
// most SN_IO_MAX bytes, and stores how many bytes moved: the end of the
// file reads as zero. A synchronous handle moves its position with the
// call, so the callers save the position first and put it back after.
static int64_t at_offset(HANDLE h, bool write, uint8_t *p, int64_t len, uint64_t off, int64_t *moved) {
    OVERLAPPED at = { 0 };
    at.Offset = (DWORD)off;
    at.OffsetHigh = (DWORD)(off >> 32);
    DWORD count = 0, want = (DWORD)(len < (int64_t)SN_IO_MAX ? len : (int64_t)SN_IO_MAX);
    BOOL ok = write ? WriteFile(h, p, want, &count, &at) : ReadFile(h, p, want, &count, &at);
    if (!ok) {
        DWORD failure = GetLastError();
        if (!write && failure == ERROR_HANDLE_EOF) {
            *moved = 0;
            return 0;
        }

        return sn_errno_of(failure);
    }

    *moved = count;
    return 0;
}

// position_of stores where the file h is, and restore_position puts it
// back there: around a read or write at an offset, which moves a
// synchronous handle's position as a plain one does. Two tasks reading
// one file at offsets at once may thus see each other's position for a
// moment, which the other systems' pread does not; a program that
// shares a file between tasks reads at offsets alone.
static bool position_of(HANDLE h, LARGE_INTEGER *at) {
    LARGE_INTEGER zero = { 0 };
    return SetFilePointerEx(h, zero, at, FILE_CURRENT);
}

static void restore_position(HANDLE h, LARGE_INTEGER at) { SetFilePointerEx(h, at, NULL, FILE_BEGIN); }

// sn_os_pread fills buf from the file at the offset off, leaving the
// file's position where it was, and stores how many bytes it read: the
// system is asked as often as it takes to fill buf, so the count is
// short only at the end of the file and zero at or past it.
SN_HELD int64_t sn_os_pread(int64_t fd, sn_slice_u8 *buf, uint64_t off, int64_t *n) {
    sn_check_slice((sn_slice *)buf);
    *n = 0;
    if (off > (uint64_t)INT64_MAX) {
        return 0; // past the end of any file: nothing to read
    }

    HANDLE h = (HANDLE)_get_osfhandle((int)fd);
    if (GetFileType(h) != FILE_TYPE_DISK) {
        return ESPIPE; // a pipe or console has no offsets, as the other systems' pread says
    }

    LARGE_INTEGER was;
    bool seekable = position_of(h, &was);
    int64_t code = 0;
    while (*n < buf->len) {
        int64_t got;
        code = at_offset(h, false, buf->ptr + *n, buf->len - *n, off + (uint64_t)*n, &got);
        if (code != 0 || got == 0) {
            break;
        }

        *n += got;
    }

    if (seekable) {
        restore_position(h, was);
    }

    return code;
}

// sn_os_pwrite writes all of buf to the file at the offset off, leaving
// the file's position where it was, and stores how many bytes it wrote,
// all of them or those before a failure.
SN_HELD int64_t sn_os_pwrite(int64_t fd, sn_slice_u8 buf, uint64_t off, int64_t *n) {
    sn_check_slice((sn_slice *)&buf);
    if (off > (uint64_t)INT64_MAX) {
        return EINVAL;
    }

    HANDLE h = (HANDLE)_get_osfhandle((int)fd);
    if (GetFileType(h) != FILE_TYPE_DISK) {
        return ESPIPE;
    }

    LARGE_INTEGER was;
    bool seekable = position_of(h, &was);
    *n = 0;
    int64_t code = 0;
    while (*n < buf.len) {
        int64_t wrote;
        code = at_offset(h, true, buf.ptr + *n, buf.len - *n, off + (uint64_t)*n, &wrote);
        if (code != 0) {
            break;
        }

        *n += wrote;
    }

    if (seekable) {
        restore_position(h, was);
    }

    return code;
}

SN_HELD int64_t sn_os_closeFile(int64_t fd) { return _close((int)fd) == 0 ? 0 : errno; }

SN_HELD int64_t sn_os_syncFile(int64_t fd) { return _commit((int)fd) == 0 ? 0 : errno; }

SN_HELD int64_t sn_os_seek(int64_t fd, int64_t whence, int64_t off, uint64_t *at) {
    static const int whences[] = { SEEK_SET, SEEK_CUR, SEEK_END };
    if (whence < 0 || whence > 2) {
        return EINVAL;
    }

    int64_t got = _lseeki64((int)fd, off, whences[whence]);
    if (got < 0) {
        return errno;
    }

    *at = (uint64_t)got;
    return 0;
}

// make_dir makes the directory at p, a converted path.
SN_HELD_STATIC static int make_dir(const wchar_t *p) { return CreateDirectoryW(p, NULL) ? 0 : last_errno(); }

// sn_os_mkdir is os.Mkdir; Windows keeps no permissions to give it.
SN_HELD int64_t sn_os_mkdir(sn_string path, uint32_t perm) {
    (void)perm;
    wchar_t *p = wpath(path);
    if (p == NULL) {
        return errno;
    }

    int code = make_dir(p);
    sn_free(p);
    return code;
}

// remove_one removes the file or the empty directory at p, a converted
// path, by the call that fits what it is: a directory, or a link to one,
// which a removal takes away without following, by RemoveDirectoryW, and
// anything else by DeleteFileW. A read-only file is made writable first,
// since the system refuses to delete one, which every other system
// allows to the owner of its directory.
SN_HELD_STATIC static int remove_one(const wchar_t *p) {
    DWORD attrs = GetFileAttributesW(p);
    if (attrs == INVALID_FILE_ATTRIBUTES) {
        return last_errno();
    }

    if (attrs & FILE_ATTRIBUTE_DIRECTORY) {
        return RemoveDirectoryW(p) ? 0 : last_errno();
    }

    if ((attrs & FILE_ATTRIBUTE_READONLY) && !SetFileAttributesW(p, attrs & ~(DWORD)FILE_ATTRIBUTE_READONLY)) {
        return last_errno();
    }

    return DeleteFileW(p) ? 0 : last_errno();
}

// sn_os_remove removes a file or an empty directory, as os.Remove does.
SN_HELD int64_t sn_os_remove(sn_string path) {
    wchar_t *p = wpath(path);
    if (p == NULL) {
        return errno;
    }

    int code = remove_one(p);
    sn_free(p);
    return code;
}

// win_failed_at records, for a removal that failed, the entry it failed on
// and the call that failed, and returns the error number.
SN_HELD_STATIC static int win_failed_at(const wchar_t *path, const char *op, int code, sn_string *failed,
                                        sn_string *what) {
    sn_string_drop(failed);
    *failed = shown(path);
    sn_string_drop(what);
    *what = SN_STR(op, (int64_t)strlen(op));
    return code;
}

// A removal walks the tree with the directories it is inside of on a
// stack of its own, never the task's, which a deep tree would outgrow:
// a pass lists the directory at the top, removes its files and links
// and pushes every subdirectory, and a directory a pass finds nothing
// left to push in is removed and popped, so each directory is listed
// once to empty it and once more to find it empty. A path is kept whole for each level, in a block
// of the runtime's.
typedef struct {
    wchar_t *path;
} level;

// push_level pushes the directory at path, length n, a copy of it.
SN_HELD_STATIC static void push_level(sn_slice *levels, const wchar_t *path, size_t n) {
    wchar_t *copy = sn_alloc((n + 1) * sizeof *copy);
    memcpy(copy, path, n * sizeof *copy);
    copy[n] = L'\0';
    level l = { copy };
    sn_slice_push(levels, &l, sizeof l);
}

// join_name is dir, a separator and name, in a block of the runtime's.
SN_HELD_STATIC static wchar_t *join_name(const wchar_t *dir, const wchar_t *name) {
    size_t d = wcslen(dir), n = wcslen(name);
    wchar_t *p = sn_alloc((d + n + 2) * sizeof *p);
    memcpy(p, dir, d * sizeof *p);
    p[d] = L'\\';
    memcpy(p + d + 1, name, (n + 1) * sizeof *p);
    return p;
}

// is_dot reports whether a directory's entry is itself or its parent.
SN_HELD_STATIC static bool is_dot(const wchar_t *name) {
    return name[0] == L'.' && (name[1] == L'\0' || (name[1] == L'.' && name[2] == L'\0'));
}

// clear_level removes what the directory at the top of levels holds
// but its subdirectories, and pushes them; it reports whether it pushed
// any, or sets code on a failure.
SN_HELD_STATIC static bool clear_level(sn_slice *levels, int *code, sn_string *failed, sn_string *op) {
    int64_t at = levels->len - 1; // the directory's level, which a push may move
    const wchar_t *dir = ((level *)levels->ptr)[at].path;
    wchar_t *pattern = join_name(dir, L"*");
    WIN32_FIND_DATAW e;
    HANDLE find =
        FindFirstFileExW(pattern, FindExInfoBasic, &e, FindExSearchNameMatch, NULL, FIND_FIRST_EX_LARGE_FETCH);
    sn_free(pattern);
    if (find == INVALID_HANDLE_VALUE) {
        int c = last_errno();
        *code = c == ENOENT ? 0 : win_failed_at(dir, OP_OPEN, c, failed, op);
        return false;
    }

    bool pushed = false;
    do {
        if (is_dot(e.cFileName)) {
            continue;
        }

        wchar_t *p = join_name(dir, e.cFileName);
        bool sub =
            (e.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) && !(e.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT);
        if (sub) { // a directory of its own, emptied first; a link to one is removed as it is
            push_level(levels, p, wcslen(p));
            dir = ((level *)levels->ptr)[at].path;
            sn_free(p);
            pushed = true;
            continue;
        }

        int c = remove_one(p);
        if (c != 0 && c != ENOENT) {
            *code = win_failed_at(p, OP_REMOVE, c, failed, op);
            sn_free(p);
            FindClose(find);
            return false;
        }

        sn_free(p);
    } while (FindNextFileW(find, &e));

    if (GetLastError() != ERROR_NO_MORE_FILES) {
        *code = win_failed_at(dir, OP_READDIR, last_errno(), failed, op);
    }

    FindClose(find);
    return pushed;
}

// sn_os_removeAll removes a tree; a path that is not there is nothing to
// remove, as os.RemoveAll has it. On a failure, failed and op name the
// entry it failed on and the call.
SN_HELD int64_t sn_os_removeAll(sn_string path, sn_string *failed, sn_string *op) {
    wchar_t *p = wpath(path);
    if (p == NULL) {
        int code = errno;
        sn_string_drop(failed);
        *failed = sn_string_clone(path);
        sn_string_drop(op);
        *op = SN_STR("RemoveAll", 9);
        return code;
    }

    int code = remove_one(p);
    if (code == 0 || code == ENOENT) {
        sn_free(p);
        return 0;
    }

    DWORD attrs = GetFileAttributesW(p);
    if (attrs == INVALID_FILE_ATTRIBUTES || !(attrs & FILE_ATTRIBUTE_DIRECTORY) ||
        (attrs & FILE_ATTRIBUTE_REPARSE_POINT)) {
        code = win_failed_at(p, OP_REMOVE, code, failed, op);
        sn_free(p);
        return code;
    }

    sn_slice levels = { 0 };
    wchar_t *root = verbatim(p); // so that every path joined under it is taken as it is, whatever its length or names
    push_level(&levels, root, wcslen(root));
    sn_free(root);
    sn_free(p);
    code = 0;
    while (code == 0 && levels.len > 0) {
        if (clear_level(&levels, &code, failed, op) || code != 0) {
            continue;
        }

        level *top = &((level *)levels.ptr)[levels.len - 1];
        int c = RemoveDirectoryW(top->path) ? 0 : last_errno();
        if (c != 0 && c != ENOENT) {
            code = win_failed_at(top->path, OP_REMOVE, c, failed, op);
            break;
        }

        sn_free(top->path);
        levels.len--;
    }

    for (int64_t i = 0; i < levels.len; i++) {
        sn_free(((level *)levels.ptr)[i].path);
    }

    sn_slice_free(&levels);
    return code;
}

// mode_of is the os.FileMode of a file of the attributes given and, for
// a reparse point, the tag that says what it is. A reparse point that
// names another file, a symbolic link or a junction, is not a directory
// even when it names one, as a link on the other systems is not, so
// that a walk does not follow it: a symbolic link is one, and any other
// such point irregular. A reparse point of another kind is the file
// it stands in place of, a directory a cloud's files keep one, with
// the mode it has besides: a socket of a Unix domain, the files of a
// deduplicated volume, which the system turns into such points, as
// regular, and any other as irregular.
SN_HELD_STATIC static uint32_t mode_of(DWORD attrs, DWORD tag) {
    uint32_t mode = (attrs & FILE_ATTRIBUTE_READONLY) ? mode_read_only : mode_read_write;
    bool reparse = (attrs & FILE_ATTRIBUTE_REPARSE_POINT) != 0;
    if ((!reparse || !IsReparseTagNameSurrogate(tag)) && (attrs & FILE_ATTRIBUTE_DIRECTORY)) {
        mode |= SN_MODE_DIR | mode_search;
    }

    if (reparse) {
        switch (tag) {
        case IO_REPARSE_TAG_SYMLINK:
            mode |= SN_MODE_SYMLINK;
            break;
        case IO_REPARSE_TAG_AF_UNIX:
            mode |= SN_MODE_SOCKET;
            break;
        case IO_REPARSE_TAG_DEDUP:
            break;
        default:
            mode |= SN_MODE_IRREGULAR;
        }
    }

    return mode;
}

// unix_time splits a file's time into the seconds since 1970, below
// zero for a time before it, and the nanoseconds past them.
SN_HELD_STATIC static void unix_time(FILETIME ft, int64_t *sec, int64_t *nsec) {
    int64_t t = (int64_t)((uint64_t)ft.dwHighDateTime << 32 | ft.dwLowDateTime) - (int64_t)FILETIME_EPOCH;
    int64_t past = t % ticks_per_s;
    if (past < 0) {
        past += ticks_per_s;
    }

    *sec = (t - past) / ticks_per_s;
    *nsec = past * tick_ns;
}

// listed_stat is the stat of a file another program holds open without
// sharing it, which cannot be opened even to read its facts, the page
// file among them, from its directory's listing instead: all but its
// volume and number, which are 0. A reparse point is not read so, since
// the listing's facts are the point's and not those of the file it
// names; it reports the open's failure, a sharing violation.
SN_HELD_STATIC static int listed_stat(const wchar_t *p, int64_t *size, uint32_t *mode, int64_t *sec, int64_t *nsec,
                                      uint64_t *dev, uint64_t *ino) {
    WIN32_FIND_DATAW e;
    HANDLE find = FindFirstFileExW(p, FindExInfoBasic, &e, FindExSearchNameMatch, NULL, 0);
    if (find == INVALID_HANDLE_VALUE) {
        return last_errno();
    }

    FindClose(find);
    if (e.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT) {
        return errno = sn_errno_of(ERROR_SHARING_VIOLATION);
    }

    *size = (int64_t)((uint64_t)e.nFileSizeHigh << 32 | e.nFileSizeLow);
    *mode = mode_of(e.dwFileAttributes, 0);
    unix_time(e.ftLastWriteTime, sec, nsec);
    *dev = 0;
    *ino = 0;
    return 0;
}

// sn_os_stat is os.Stat when follow is set and os.Lstat when it is not:
// the size, the mode, the last change's time since 1970, and the volume
// and the file's number on it, which say whether two paths name one
// file.
SN_HELD int64_t sn_os_stat(sn_string path, bool follow, int64_t *size, uint32_t *mode, int64_t *sec, int64_t *nsec,
                           uint64_t *dev, uint64_t *ino) {
    wchar_t *p = wpath(path);
    if (p == NULL) {
        return errno;
    }

    DWORD flags = FILE_FLAG_BACKUP_SEMANTICS | (follow ? 0 : FILE_FLAG_OPEN_REPARSE_POINT);
    HANDLE h = CreateFileW(p, FILE_READ_ATTRIBUTES, FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE, NULL,
                           OPEN_EXISTING, flags, NULL);
    if (h == INVALID_HANDLE_VALUE) {
        int code =
            GetLastError() == ERROR_SHARING_VIOLATION ? listed_stat(p, size, mode, sec, nsec, dev, ino) : last_errno();
        sn_free(p);
        return code;
    }

    sn_free(p);
    BY_HANDLE_FILE_INFORMATION info;
    FILE_ATTRIBUTE_TAG_INFO tag = { 0 };
    bool ok = GetFileInformationByHandle(h, &info);
    if (ok && (info.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT)) {
        ok = GetFileInformationByHandleEx(h, FileAttributeTagInfo, &tag, sizeof tag);
    }

    int code = ok ? 0 : last_errno();
    CloseHandle(h);
    if (code != 0) {
        return code;
    }

    *size = (int64_t)((uint64_t)info.nFileSizeHigh << 32 | info.nFileSizeLow);
    *mode = mode_of(info.dwFileAttributes, tag.ReparseTag);
    unix_time(info.ftLastWriteTime, sec, nsec);
    *dev = info.dwVolumeSerialNumber;
    *ino = (uint64_t)info.nFileIndexHigh << 32 | info.nFileIndexLow;
    return 0;
}

// sn_os_rename is the system's rename, which replaces a file at to, as
// rename does on the other systems.
SN_HELD int64_t sn_os_rename(sn_string from, sn_string to) {
    wchar_t *f = wpath(from);
    if (f == NULL) {
        return errno;
    }

    wchar_t *t = wpath(to);
    if (t == NULL) {
        sn_free(f);
        return errno;
    }

    int code = MoveFileExW(f, t, MOVEFILE_REPLACE_EXISTING) ? 0 : last_errno();
    sn_free(f);
    sn_free(t);
    return code;
}

SN_HELD int64_t sn_os_chdir(sn_string path) {
    wchar_t *p = wpath(path);
    if (p == NULL) {
        return errno;
    }

    int code = SetCurrentDirectoryW(p) ? 0 : last_errno();
    sn_free(p);
    return code;
}

SN_HELD int64_t sn_os_getcwd(sn_string *out) {
    DWORD n = GetCurrentDirectoryW(0, NULL); // the length with the zero after it
    for (;;) {
        wchar_t *w = sn_alloc((size_t)n * sizeof *w);
        DWORD got = GetCurrentDirectoryW(n, w);
        if (got == 0) {
            sn_free(w);
            return last_errno();
        }

        if (got < n) {
            sn_string_drop(out);
            *out = utf8(w, (int)got);
            sn_free(w);
            return 0;
        }

        sn_free(w);
        n = got + 1; // it changed between the two calls: the length it has now
    }
}

// sn_os_readDir reads the names in a directory into out, each as the
// four bytes of its os.FileMode's kind, lowest first, then its name and
// a zero byte, as os.c packs them on the other systems.
SN_HELD int64_t sn_os_readDir(sn_string path, sn_slice_u8 *out, sn_string *op) {
    sn_string_drop(op);
    *op = SN_STR(OP_OPEN, sizeof OP_OPEN - 1);
    if (path.len == 0) { // which would list the root of the drive
        return ENOENT;
    }

    wchar_t *p = wpath(path);
    if (p == NULL) {
        return errno;
    }

    wchar_t *pattern = join_name(p, L"*");
    sn_free(p);
    WIN32_FIND_DATAW e;
    HANDLE find =
        FindFirstFileExW(pattern, FindExInfoBasic, &e, FindExSearchNameMatch, NULL, FIND_FIRST_EX_LARGE_FETCH);
    sn_free(pattern);
    if (find == INVALID_HANDLE_VALUE) {
        return last_errno();
    }

    sn_slice *buf = (sn_slice *)out;
    buf->len = 0;
    do {
        if (is_dot(e.cFileName)) {
            continue;
        }

        uint32_t type =
            mode_of(e.dwFileAttributes, e.dwReserved0) & SN_MODE_TYPE; // dwReserved0 holds a reparse point's tag
        sn_string name = utf8(e.cFileName, -1);
        if (buf->cap - buf->len < name.len + 5) {
            sn_slice_grow(buf, buf->len + name.len + 5, 1);
        }

        uint8_t *at = (uint8_t *)buf->ptr + buf->len;
        at[0] = (uint8_t)type;
        at[1] = (uint8_t)(type >> 8);
        at[2] = (uint8_t)(type >> 16);
        at[3] = (uint8_t)(type >> 24);
        memcpy(at + 4, name.ptr, (size_t)name.len);
        at[4 + name.len] = '\0';
        buf->len += name.len + 5;
        sn_string_free(name);
    } while (FindNextFileW(find, &e));

    int code = GetLastError() == ERROR_NO_MORE_FILES ? 0 : last_errno();
    FindClose(find);
    if (code != 0) {
        *op = SN_STR(OP_READDIR, sizeof OP_READDIR - 1);
    }

    return code;
}

SN_HELD int64_t sn_os_Getpid(void) { return (int64_t)GetCurrentProcessId(); }

// sn_os_hostname is os.Hostname: the machine's name as its DNS host name.
SN_HELD int64_t sn_os_hostname(sn_string *out) {
    DWORD n = 0;
    GetComputerNameExW(ComputerNamePhysicalDnsHostname, NULL, &n); // the room it needs, with the zero after it
    wchar_t *w = sn_alloc(((size_t)n + 1) * sizeof *w);
    if (!GetComputerNameExW(ComputerNamePhysicalDnsHostname, w, &n)) {
        sn_free(w);
        return last_errno();
    }

    sn_string_drop(out);
    *out = utf8(w, (int)n);
    sn_free(w);
    return 0;
}

// sn_os_TempDir is os.TempDir: the system's directory for temporary
// files, which it finds from TMP, TEMP and the user's profile, without
// the separator it ends in, but for the root of a drive.
SN_HELD sn_string sn_os_TempDir(void) {
    DWORD room = MAX_PATH + 1;
    for (;;) {
        wchar_t *w = sn_alloc((size_t)room * sizeof *w);
        DWORD n = GetTempPathW(room, w);
        if (n == 0) {
            sn_free(w);
            return SN_STR("C:\\Windows\\Temp", sizeof "C:\\Windows\\Temp" - 1); // the system's own
        }

        if (n >= room) { // the length it needs, with the zero after it
            sn_free(w);
            room = n + 1;
            continue;
        }

        if (n > 3 && w[n - 1] == L'\\') {
            n--;
        }

        sn_string dir = utf8(w, (int)n);
        sn_free(w);
        return dir;
    }
}

SN_HELD void sn_os_env_system(sn_string key, const sn_string *value) {
    wchar_t *k = sn_os_wide(key);
    wchar_t *v = value == NULL ? NULL : sn_os_wide(*value);
    if (k != NULL && (value == NULL || v != NULL)) {
        (void)SetEnvironmentVariableW(k, v);
    }

    sn_free(k);
    sn_free(v);
}

SN_HELD char **sn_os_environ_utf8(void) {
    wchar_t *block = GetEnvironmentStringsW();
    int64_t n = 0;
    for (const wchar_t *at = block; at != NULL && *at != L'\0'; at += wcslen(at) + 1) {
        n++;
    }

    char **out = calloc((size_t)n + 1, sizeof *out);
    if (out == NULL) {
        sn_panic(SN_STR("out of memory", 13));
    }

    int64_t i = 0;
    for (const wchar_t *at = block; at != NULL && *at != L'\0'; at += wcslen(at) + 1) {
        int size = WideCharToMultiByte(CP_UTF8, 0, at, -1, NULL, 0, NULL, NULL);
        char *s = size > 0 ? malloc((size_t)size) : NULL;
        if (s == NULL) {
            continue;
        }

        WideCharToMultiByte(CP_UTF8, 0, at, -1, s, size, NULL, NULL);
        out[i++] = s;
    }

    FreeEnvironmentStringsW(block);
    return out;
}

// The file operations of os that came after the first ones: the modes,
// the times, the sizes, the links, the temporary files, the program's
// own path and its parent. Each takes paths through wpath and reports
// the system's failure as an error number, as the functions above do.

// file_time is the system's time for seconds and nanoseconds since
// 1970, in its ticks of 100 ns since 1601, or false for a time the
// system has no number for: before 1601, or past what its ticks count.
SN_HELD_STATIC static bool file_time(int64_t sec, int64_t nsec, FILETIME *ft) {
    int64_t ticks;
    if (__builtin_mul_overflow(sec, (int64_t)ticks_per_s, &ticks) ||
        __builtin_add_overflow(ticks, (int64_t)FILETIME_EPOCH + nsec / tick_ns, &ticks) || ticks < 0) {
        return false;
    }

    ft->dwLowDateTime = (DWORD)(uint64_t)ticks;
    ft->dwHighDateTime = (DWORD)((uint64_t)ticks >> 32);
    return true;
}

// open_attributes opens the file at p for a change of its attributes or
// its data with the access asked, a directory too, sharing it as every
// Windows program expects; INVALID_HANDLE_VALUE with errno set on a
// failure.
SN_HELD_STATIC static HANDLE open_attributes(const wchar_t *p, DWORD access) {
    HANDLE h = CreateFileW(p, access, FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE, NULL, OPEN_EXISTING,
                           FILE_FLAG_BACKUP_SEMANTICS, NULL);
    if (h == INVALID_HANDLE_VALUE) {
        (void)last_errno();
    }

    return h;
}

// sn_os_chmod is os.Chmod: the system keeps no permissions, so the
// owner's write bit alone counts, a mode without it making the file
// read-only and one with it writable, as os.FileMode says of Windows.
SN_HELD int64_t sn_os_chmod(sn_string path, uint32_t mode) {
    wchar_t *p = wpath(path);
    if (p == NULL) {
        return errno;
    }

    DWORD attrs = GetFileAttributesW(p);
    int code = 0;
    if (attrs == INVALID_FILE_ATTRIBUTES) {
        code = last_errno();
    } else {
        DWORD want = (mode & mode_write) == 0 ? attrs | FILE_ATTRIBUTE_READONLY : attrs & ~(DWORD)FILE_ATTRIBUTE_READONLY;
        if (want != attrs && !SetFileAttributesW(p, want)) {
            code = last_errno();
        }
    }

    sn_free(p);
    return code;
}

// sn_os_chtimes is os.Chtimes: the time last read and the time last
// changed, each set when its flag says so and left as it is otherwise,
// which the system takes as a time of NULL.
SN_HELD int64_t sn_os_chtimes(sn_string path, bool setAccess, int64_t asec, int64_t ansec, bool setChange,
                              int64_t msec, int64_t mnsec) {
    FILETIME access, change;
    if ((setAccess && !file_time(asec, ansec, &access)) || (setChange && !file_time(msec, mnsec, &change))) {
        return EINVAL;
    }

    wchar_t *p = wpath(path);
    if (p == NULL) {
        return errno;
    }

    HANDLE h = open_attributes(p, FILE_WRITE_ATTRIBUTES);
    int code = h == INVALID_HANDLE_VALUE ? errno : 0;
    sn_free(p);
    if (code != 0) {
        return code;
    }

    code = SetFileTime(h, NULL, setAccess ? &access : NULL, setChange ? &change : NULL) ? 0 : last_errno();
    CloseHandle(h);
    return code;
}

// set_size sets the size of the open file h, cutting it or filling it
// with zeros, in one call that moves no position.
SN_HELD_STATIC static int set_size(HANDLE h, int64_t size) {
    FILE_END_OF_FILE_INFO info;
    info.EndOfFile.QuadPart = size;
    return SetFileInformationByHandle(h, FileEndOfFileInfo, &info, sizeof info) ? 0 : last_errno();
}

// sn_os_truncate is os.Truncate, and sn_os_ftruncate File.Truncate.
SN_HELD int64_t sn_os_truncate(sn_string path, int64_t size) {
    wchar_t *p = wpath(path);
    if (p == NULL) {
        return errno;
    }

    HANDLE h = open_attributes(p, GENERIC_WRITE);
    int code = h == INVALID_HANDLE_VALUE ? errno : 0;
    sn_free(p);
    if (code != 0) {
        return code;
    }

    code = set_size(h, size);
    CloseHandle(h);
    return code;
}

SN_HELD int64_t sn_os_ftruncate(int64_t fd, int64_t size) {
    HANDLE h = (HANDLE)_get_osfhandle((int)fd);
    if (h == INVALID_HANDLE_VALUE) {
        return EBADF;
    }

    return set_size(h, size);
}

// sn_os_link is os.Link.
SN_HELD int64_t sn_os_link(sn_string from, sn_string to) {
    wchar_t *f = wpath(from);
    if (f == NULL) {
        return errno;
    }

    wchar_t *t = wpath(to);
    if (t == NULL) {
        sn_free(f);
        return EINVAL;
    }

    int code = CreateHardLinkW(t, f, NULL) ? 0 : last_errno();
    sn_free(f);
    sn_free(t);
    return code;
}

// links_directory reports whether the target of a link to be made at
// link names a directory now, as the system will read the target: as it
// is when it has a volume or names a share, on the link's volume when
// it starts at a root, and under the link's own directory when
// relative; a link to a directory is made as a directory link.
SN_HELD_STATIC static bool links_directory(sn_string target, sn_string link) {
    sn_string path = target;
    bool joined = false;
    bool separator = target.len > 0 && (target.ptr[0] == '\\' || target.ptr[0] == '/');
    bool share = target.len > 1 && separator && (target.ptr[1] == '\\' || target.ptr[1] == '/');
    bool volume = target.len > 1 && target.ptr[1] == ':';
    if (separator && !share && link.len > 1 && link.ptr[1] == ':') { // rooted: the link's volume
        path = sn_join(SN_STR(link.ptr, 2), target, 0);
        joined = true;
    } else if (!separator && !volume) { // relative: under the link's directory
        int64_t end = link.len;
        while (end > 0 && link.ptr[end - 1] != '\\' && link.ptr[end - 1] != '/') {
            end--;
        }

        path = sn_join(SN_STR(link.ptr, end), target, 0);
        joined = true;
    }

    wchar_t *p = wpath(path);
    if (joined) {
        sn_string_free(path);
    }

    if (p == NULL) {
        return false;
    }

    DWORD attrs = GetFileAttributesW(p);
    sn_free(p);
    return attrs != INVALID_FILE_ATTRIBUTES && (attrs & FILE_ATTRIBUTE_DIRECTORY) != 0;
}

// sn_os_symlink is os.Symlink: the target is kept as written, so it is
// converted without the prefix a long path takes; a link to a directory
// is made as a directory link; and the right a user has to make a link
// without a privilege, developer mode, is asked for first, and the call
// made again without it on a system too old to know the flag.
SN_HELD int64_t sn_os_symlink(sn_string target, sn_string link) {
    wchar_t *t = sn_os_wide(target);
    if (t == NULL) {
        return errno;
    }

    wchar_t *l = wpath(link);
    if (l == NULL) {
        sn_free(t);
        return EINVAL;
    }

    DWORD flags = links_directory(target, link) ? SYMBOLIC_LINK_FLAG_DIRECTORY : 0;
    int code = 0;
    if (!CreateSymbolicLinkW(l, t, flags | SYMBOLIC_LINK_FLAG_ALLOW_UNPRIVILEGED_CREATE)) {
        code = last_errno();
        if (GetLastError() == ERROR_INVALID_PARAMETER) {
            code = CreateSymbolicLinkW(l, t, flags) ? 0 : last_errno();
        }
    }

    sn_free(t);
    sn_free(l);
    return code;
}

// A reparse point's data, as the system lays it out for a symbolic link
// and for a mount point, a junction, which has no Flags: the two names
// are at offsets into PathBuffer, in UTF-16 units.
typedef struct {
    ULONG tag;
    USHORT dataLength;
    USHORT reserved;
    USHORT substituteOffset;
    USHORT substituteLength;
    USHORT printOffset;
    USHORT printLength;
    ULONG flags; // a symbolic link's; a mount point's PathBuffer starts here
    WCHAR pathBuffer[1];
} reparse_data;

// without_prefix is the path w of n units without the prefix the system
// gives a link's target, \??\ or \??\UNC\, which os.Readlink gives
// back as the path it stands for, in an owned Snello string.
SN_HELD_STATIC static sn_string without_prefix(const wchar_t *w, int n) {
    if (n >= 8 && wcsncmp(w, L"\\??\\UNC\\", 8) == 0) {
        return sn_join(SN_STR("\\\\", 2), utf8(w + 8, n - 8), 2);
    }

    if (n >= 4 && wcsncmp(w, L"\\??\\", 4) == 0) {
        return utf8(w + 4, n - 4);
    }

    return utf8(w, n);
}

// sn_os_readlink is os.Readlink: the target a symbolic link or a mount
// point holds, read from its reparse point; a file that is neither is
// refused as an invalid argument, as on the other systems.
SN_HELD int64_t sn_os_readlink(sn_string path, sn_string *out) {
    wchar_t *p = wpath(path);
    if (p == NULL) {
        return errno;
    }

    HANDLE h = CreateFileW(p, 0, FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE, NULL, OPEN_EXISTING,
                           FILE_FLAG_BACKUP_SEMANTICS | FILE_FLAG_OPEN_REPARSE_POINT, NULL);
    sn_free(p);
    if (h == INVALID_HANDLE_VALUE) {
        return last_errno();
    }

    char *block = sn_alloc(MAXIMUM_REPARSE_DATA_BUFFER_SIZE);
    DWORD got = 0;
    int code = 0;
    if (!DeviceIoControl(h, FSCTL_GET_REPARSE_POINT, NULL, 0, block, MAXIMUM_REPARSE_DATA_BUFFER_SIZE, &got, NULL)) {
        code = GetLastError() == ERROR_NOT_A_REPARSE_POINT ? EINVAL : last_errno();
    }

    CloseHandle(h);
    if (code == 0) {
        // The data is what whoever set the reparse point wrote, so its
        // offsets are held to the bytes the system gave back before the
        // names are read: a point of another kind, or one too short or
        // pointing outside its data, is no link.
        reparse_data *data = (reparse_data *)block;
        bool mount = got >= offsetof(reparse_data, flags) && data->tag == IO_REPARSE_TAG_MOUNT_POINT;
        bool symlink = got >= offsetof(reparse_data, pathBuffer) && data->tag == IO_REPARSE_TAG_SYMLINK;
        const char *names = mount ? (const char *)&data->flags : (const char *)data->pathBuffer;
        DWORD room = (mount || symlink) ? got - (DWORD)(names - block) : 0;
        if ((mount || symlink) && (DWORD)data->substituteOffset + data->substituteLength <= room) {
            sn_string_drop(out);
            *out = without_prefix((const wchar_t *)(names + data->substituteOffset),
                                  (int)(data->substituteLength / sizeof(wchar_t)));
        } else {
            code = EINVAL;
        }
    }

    sn_free(block);
    return code;
}

// sn_os_readExecutable reads the path of the program's file, once, for
// os.Executable, in a block that doubles while the path does not fit.
SN_HELD int64_t sn_os_readExecutable(sn_string *out) {
    for (DWORD cap = 260;; cap *= 2) {
        wchar_t *w = sn_alloc(cap * sizeof *w);
        DWORD n = GetModuleFileNameW(NULL, w, cap);
        if (n == 0) {
            int code = last_errno();
            sn_free(w);
            return code;
        }

        if (n < cap) {
            sn_string_drop(out);
            *out = shown(w);
            sn_free(w);
            return 0;
        }

        sn_free(w);
    }
}

// sn_os_fstat is File.Stat: what sn_os_stat says of a path, of an open
// file.
SN_HELD int64_t sn_os_fstat(int64_t fd, int64_t *size, uint32_t *mode, int64_t *sec, int64_t *nsec, uint64_t *dev,
                            uint64_t *ino) {
    HANDLE h = (HANDLE)_get_osfhandle((int)fd);
    if (h == INVALID_HANDLE_VALUE) {
        return EBADF;
    }

    BY_HANDLE_FILE_INFORMATION info;
    FILE_ATTRIBUTE_TAG_INFO tag = { 0 };
    bool ok = GetFileInformationByHandle(h, &info);
    if (ok && (info.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT)) {
        ok = GetFileInformationByHandleEx(h, FileAttributeTagInfo, &tag, sizeof tag);
    }

    if (!ok) {
        return last_errno();
    }

    *size = (int64_t)((uint64_t)info.nFileSizeHigh << 32 | info.nFileSizeLow);
    *mode = mode_of(info.dwFileAttributes, tag.ReparseTag);
    unix_time(info.ftLastWriteTime, sec, nsec);
    *dev = info.dwVolumeSerialNumber;
    *ino = (uint64_t)info.nFileIndexHigh << 32 | info.nFileIndexLow;
    return 0;
}

// sn_os_Getppid is os.Getppid: the parent's id from the system's list
// of processes, the entry of this process naming it; 0 when the list
// cannot be read or the process is not in it.
SN_HELD int64_t sn_os_Getppid(void) {
    HANDLE snap = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0);
    if (snap == INVALID_HANDLE_VALUE) {
        return 0;
    }

    DWORD self = GetCurrentProcessId();
    int64_t parent = 0;
    PROCESSENTRY32W e;
    e.dwSize = sizeof e;
    for (BOOL more = Process32FirstW(snap, &e); more; more = Process32NextW(snap, &e)) {
        if (e.th32ProcessID == self) {
            parent = (int64_t)e.th32ParentProcessID;
            break;
        }
    }

    CloseHandle(snap);
    return parent;
}

#endif
