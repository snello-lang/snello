// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The programs a program runs, on Windows: a child made by
// CreateProcessW with the three standard handles it is given and no
// other, its end posted to the scheduler's completion port by a wait
// completion packet on its process handle, and its pipes named pipes
// whose end here is overlapped, each read or write one operation whose
// completion the port posts, so that a task waits on a child or a pipe
// as it waits on a socket. process.c has the signals, and the forms of
// these calls for Linux and macOS.

#include "internal.h"

#if defined(_WIN32)
#include <errno.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <windows.h>

// Where a child's standard streams go, exec.Stdio's values, as
// process.c has them.
enum { stdio_inherit = 0, stdio_piped = 1, stdio_null = 2 };

// The pipes' buffers, the most a name of one takes, and how many names
// are tried: the process number and a random number, so that two
// programs never share a name and no other program can guess the next,
// and each made as the first instance of its name, so that a name in
// use fails, and another is tried, rather than joins what is there.
enum { pipe_buffer = 1 << 16, pipe_name_room = 64, pipe_name_tries = 10 };
static const wchar_t *pipe_name_format = L"\\\\.\\pipe\\snello-%lu-%016llx";
static const wchar_t *null_device = L"NUL";

// A child is a program this one started: its process handle and
// number, the descriptor record its exit readies and the wait packet
// that posts it, and its exit code once it has ended.
typedef struct {
    HANDLE process;
    DWORD pid;
    void *pd;
    void *packet;
    bool ended;
    DWORD code;
} child;

static _Atomic unsigned long pipes_made; // counts the pipes, for their names

// quoted appends arg to the command line as the C runtime's parser of
// a command line reads it back: unchanged when it holds no space, tab
// or quote, and else in quotes, each quote escaped and the backslashes
// before a quote, or before the end, doubled.
static void append(sn_string *line, const char *s, int64_t n) {
    sn_string piece = SN_STR(s, n);
    sn_string_append(line, &piece);
}

static void quoted(sn_string arg, sn_string *line) {
    bool plain = arg.len > 0;
    for (int64_t i = 0; i < arg.len && plain; i++) {
        plain = arg.ptr[i] != ' ' && arg.ptr[i] != '\t' && arg.ptr[i] != '"';
    }

    if (plain) {
        sn_string_append(line, &arg);
        return;
    }

    append(line, "\"", 1);
    int64_t slashes = 0;
    for (int64_t i = 0; i < arg.len; i++) {
        uint8_t c = arg.ptr[i];
        if (c == '\\') {
            slashes++;
            continue;
        }

        for (int64_t k = 0; k < (c == '"' ? 2 * slashes + 1 : slashes); k++) {
            append(line, "\\", 1);
        }

        slashes = 0;
        append(line, (const char *)&c, 1);
    }

    for (int64_t k = 0; k < 2 * slashes; k++) {
        append(line, "\\", 1);
    }

    append(line, "\"", 1);
}

// wide_block converts a block of zero-ended strings ending in an empty
// one, as an environment is laid out, to UTF-16, NULL when it cannot.
static wchar_t *wide_block(const uint8_t *p, int64_t len) {
    int n = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, (const char *)p, (int)len, NULL, 0);
    if (n <= 0) {
        return NULL;
    }

    wchar_t *w = sn_alloc((size_t)n * sizeof *w);
    MultiByteToWideChar(CP_UTF8, 0, (const char *)p, (int)len, w, n);
    return w;
}

// make_pipe makes a pipe for the child's stream i, this program's end
// overlapped and bound to the port, the child's end inheritable, and
// returns the system's error number.
static int make_pipe(int i, HANDLE *parent, HANDLE *theirs) {
    bool in = i == 0; // the child reads its input: this program writes that pipe
    wchar_t name[pipe_name_room];
    DWORD failed = ERROR_ACCESS_DENIED; // what a name in use fails with, as the first instance of it
    for (int attempt = 0; attempt < pipe_name_tries && (failed == ERROR_ACCESS_DENIED || failed == ERROR_PIPE_BUSY);
         attempt++) {
        unsigned long long r = 0;
        if (!sn_system_random(&r, sizeof r)) {
            r = (unsigned long long)GetTickCount64() ^ atomic_fetch_add(&pipes_made, 1);
        }

        swprintf(name, pipe_name_room, pipe_name_format, (unsigned long)GetCurrentProcessId(), r);
        *parent = CreateNamedPipeW(
            name,
            (in ? PIPE_ACCESS_OUTBOUND : PIPE_ACCESS_INBOUND) | FILE_FLAG_OVERLAPPED | FILE_FLAG_FIRST_PIPE_INSTANCE,
            PIPE_TYPE_BYTE | PIPE_WAIT | PIPE_REJECT_REMOTE_CLIENTS, 1, pipe_buffer, pipe_buffer, 0, NULL);
        failed = *parent == INVALID_HANDLE_VALUE ? GetLastError() : 0;
    }

    if (*parent == INVALID_HANDLE_VALUE) {
        return sn_errno_of(failed);
    }

    SECURITY_ATTRIBUTES inheritable = { sizeof inheritable, NULL, TRUE };
    *theirs = CreateFileW(name, in ? GENERIC_READ : GENERIC_WRITE, 0, &inheritable, OPEN_EXISTING, 0, NULL);
    if (*theirs == INVALID_HANDLE_VALUE) {
        int code = sn_errno_of(GetLastError());
        CloseHandle(*parent);
        *parent = INVALID_HANDLE_VALUE;
        return code;
    }

    int code = sn_net_watch_handle(*parent);
    if (code != 0) {
        CloseHandle(*parent);
        CloseHandle(*theirs);
        *parent = *theirs = INVALID_HANDLE_VALUE;
    }

    return code;
}

// stream_handle gives the child's handle for its stream i as the mode
// says, inheritable: the null device, this program's own duplicated,
// or a pipe's end, and returns the system's error number.
static int stream_handle(int i, int64_t mode, HANDLE *parent, HANDLE *theirs) {
    SECURITY_ATTRIBUTES inheritable = { sizeof inheritable, NULL, TRUE };
    switch (mode) {
    case stdio_piped:
        return make_pipe(i, parent, theirs);
    case stdio_null:
        *theirs = CreateFileW(null_device, i == 0 ? GENERIC_READ : GENERIC_WRITE, FILE_SHARE_READ | FILE_SHARE_WRITE,
                              &inheritable, OPEN_EXISTING, 0, NULL);
        return *theirs == INVALID_HANDLE_VALUE ? sn_errno_of(GetLastError()) : 0;
    default: {
        HANDLE own = GetStdHandle(i == 0 ? STD_INPUT_HANDLE : i == 1 ? STD_OUTPUT_HANDLE : STD_ERROR_HANDLE);
        if (own == NULL ||
            own == INVALID_HANDLE_VALUE) { // a program started with the stream closed: the child's is too
            *theirs = INVALID_HANDLE_VALUE;
            return 0;
        }

        if (!DuplicateHandle(GetCurrentProcess(), own, GetCurrentProcess(), theirs, 0, TRUE, DUPLICATE_SAME_ACCESS)) {
            return sn_errno_of(GetLastError());
        }

        return 0;
    }
    }
}

static void close_handles(HANDLE *hs, int n) {
    for (int i = 0; i < n; i++) {
        if (hs[i] != INVALID_HANDLE_VALUE) {
            CloseHandle(hs[i]);
        }
    }
}

// sn_os__exec_start is exec's start, as process.c describes it: here
// the child is made by CreateProcessW, given the three handles alone
// through the attribute list, on this program's console, with the
// command line quoted as the C runtime reads one and a UTF-16
// environment, and its process handle watched by a wait completion
// packet on the port.
int64_t sn_os__exec_start(sn_string path, void *args_slice, sn_string dir, void *env_slice, bool has_env,
                          int64_t in_mode, int64_t out_mode, int64_t err_mode, int64_t *pid, int64_t *handle,
                          int64_t *in_fd, int64_t *out_fd, int64_t *err_fd) {
    sn_slice args = *(const sn_slice *)args_slice, env = *(const sn_slice *)env_slice;
    int64_t modes[3] = { in_mode, out_mode, err_mode };
    int64_t *parents[3] = { in_fd, out_fd, err_fd };
    HANDLE theirs[3] = { INVALID_HANDLE_VALUE, INVALID_HANDLE_VALUE, INVALID_HANDLE_VALUE };
    HANDLE ours[3] = { INVALID_HANDLE_VALUE, INVALID_HANDLE_VALUE, INVALID_HANDLE_VALUE };
    int code = 0;
    for (int i = 0; i < 3 && code == 0; i++) {
        code = stream_handle(i, modes[i], &ours[i], &theirs[i]);
    }

    wchar_t *wpath = NULL, *wline = NULL, *wdir = NULL, *wenv = NULL;
    if (code == 0) {
        sn_string line = SN_STR("", 0);
        quoted(path, &line);
        for (int64_t i = 0; i < args.len; i++) {
            append(&line, " ", 1);
            quoted(((const sn_string *)args.ptr)[i], &line);
        }

        wpath = sn_os_wide(path);
        wline = sn_os_wide(line);
        sn_string_drop(&line);
        wdir = dir.len > 0 ? sn_os_wide(dir) : NULL;
        if (has_env) {
            sn_string block = SN_STR("", 0);
            for (int64_t i = 0; i < env.len; i++) {
                sn_string_append(&block, &((const sn_string *)env.ptr)[i]);
                append(&block, "", 1); // a zero after each
            }

            append(&block, "", 1); // and one more after the last
            wenv = wide_block(block.ptr, block.len);
            sn_string_drop(&block);
        }

        if (wpath == NULL || wline == NULL || (dir.len > 0 && wdir == NULL) || (has_env && wenv == NULL)) {
            code = EINVAL;
        }
    }

    PROCESS_INFORMATION info;
    memset(&info, 0, sizeof info);
    if (code == 0) {
        SIZE_T size = 0;
        InitializeProcThreadAttributeList(NULL, 1, 0, &size);
        LPPROC_THREAD_ATTRIBUTE_LIST list = malloc(size);
        HANDLE given[3];
        int n = 0;
        for (int i = 0; i < 3; i++) {
            if (theirs[i] != INVALID_HANDLE_VALUE) {
                given[n++] = theirs[i];
            }
        }

        STARTUPINFOEXW si;
        memset(&si, 0, sizeof si);
        si.StartupInfo.cb = sizeof si;
        si.StartupInfo.dwFlags = STARTF_USESTDHANDLES;
        si.StartupInfo.hStdInput = theirs[0];
        si.StartupInfo.hStdOutput = theirs[1];
        si.StartupInfo.hStdError = theirs[2];
        si.lpAttributeList = list;
        if (list == NULL || !InitializeProcThreadAttributeList(list, 1, 0, &size) ||
            (n > 0 && !UpdateProcThreadAttribute(list, 0, PROC_THREAD_ATTRIBUTE_HANDLE_LIST, given,
                                                 (SIZE_T)n * sizeof given[0], NULL, NULL)) ||
            !CreateProcessW(wpath, wline, NULL, NULL, TRUE, EXTENDED_STARTUPINFO_PRESENT | CREATE_UNICODE_ENVIRONMENT,
                            wenv, wdir, &si.StartupInfo, &info)) {
            code = list == NULL ? ENOMEM : sn_errno_of(GetLastError());
        }

        if (list != NULL) {
            DeleteProcThreadAttributeList(list);
            free(list);
        }
    }

    sn_free(wpath);
    sn_free(wline);
    sn_free(wdir);
    sn_free(wenv);
    close_handles(theirs, 3);
    if (code != 0) {
        close_handles(ours, 3);
        return code;
    }

    CloseHandle(info.hThread);
    child *c = sn_alloc(sizeof *c);
    memset(c, 0, sizeof *c);
    c->process = info.hProcess;
    c->pid = info.dwProcessId;
    c->pd = sn_net_watch_proc(0);
    c->packet = c->pd == NULL ? NULL : sn_wait_packet_make(c->process, SN_KEY_CHILD, c->pd);
    if (c->packet == NULL) {
        code = sn_errno_of(GetLastError());
        close_handles(ours, 3);
        if (c->pd != NULL) {
            sn_net_unwatch_proc(c->pd);
        }

        TerminateProcess(c->process, 1); // started, and not to be watched: ended here, so that nothing is left behind
        CloseHandle(c->process);
        sn_free(c);
        return code;
    }

    for (int i = 0; i < 3; i++) {
        *parents[i] = ours[i] == INVALID_HANDLE_VALUE ? -1 : (int64_t)(intptr_t)ours[i];
    }

    *pid = c->pid;
    *handle = (int64_t)(intptr_t)c;
    return 0;
}

// ended reads the child's exit code once the system says it has
// ended, and reports whether it has.
static bool ended(child *c) {
    if (c->ended) {
        return true;
    }

    if (WaitForSingleObject(c->process, 0) != WAIT_OBJECT_0 || !GetExitCodeProcess(c->process, &c->code)) {
        return false;
    }

    c->ended = true;
    return true;
}

int64_t sn_os__exec_wait(int64_t handle, bool *exited, int64_t *code, int64_t *sig) {
    child *c = (child *)(intptr_t)handle;
    for (;;) {
        if (ended(c)) {
            *exited = true;
            *code = (int64_t)c->code;
            *sig = 0;
            return 0;
        }

        int cut = sn_net_wait_proc(c->pd);
        if (cut != 0) {
            return cut;
        }
    }
}

// sn_os__exec_signal ends the child, whatever the signal asked for,
// since Windows has none to send: TerminateProcess with the exit code
// 1, nothing once the child has ended.
int64_t sn_os__exec_signal(int64_t handle, int64_t sig) {
    (void)sig;
    child *c = (child *)(intptr_t)handle;
    if (ended(c) || TerminateProcess(c->process, 1) || GetLastError() == ERROR_ACCESS_DENIED) {
        return 0; // access denied is a process that has ended
    }

    return sn_errno_of(GetLastError());
}

// child_release lets the child's record go: its packet, its exit record
// and its process handle. Windows keeps no zombie: nothing is reaped.
static void child_release(child *c) {
    sn_wait_packet_free(c->packet);
    sn_net_unwatch_proc(c->pd);
    CloseHandle(c->process);
    sn_free(c);
}

void sn_os__exec_detach(int64_t handle) { child_release((child *)(intptr_t)handle); }

void sn_os__exec_closeChild(int64_t handle) { child_release((child *)(intptr_t)handle); }

// finish waits for the operation op on the pipe h to complete, after
// its call returned pending, or at once, and stores the bytes moved:
// the system's error number, or a cancel's code, after which the
// operation is cancelled and its completion still waited for, since the
// system holds the buffer until it comes.
static int finish(HANDLE h, void *op, DWORD *moved) {
    int cut = sn_net_wait_op(op);
    if (cut != 0) {
        CancelIoEx(h, sn_net_pipe_overlapped(op));
        sn_net_wait_op_done(op);
    }

    if (!GetOverlappedResult(h, sn_net_pipe_overlapped(op), moved, FALSE)) {
        DWORD code = GetLastError();
        return cut != 0 && code == ERROR_OPERATION_ABORTED ? cut : sn_errno_of(code);
    }

    return cut;
}

int64_t sn_os__exec_read(int64_t fd, sn_slice_u8 *buf, int64_t *n) {
    sn_check_slice((sn_slice *)buf);
    *n = 0;
    if (buf->len == 0) {
        return 0;
    }

    HANDLE h = (HANDLE)(intptr_t)fd;
    void *op = sn_net_pipe_op();
    DWORD moved = 0;
    int code = 0;
    int64_t most = (int64_t)SN_IO_MAX;
    if (!ReadFile(h, buf->ptr, (DWORD)(buf->len < most ? buf->len : most), NULL, sn_net_pipe_overlapped(op)) &&
        GetLastError() != ERROR_IO_PENDING) {
        code = GetLastError() == ERROR_BROKEN_PIPE ? 0 : sn_errno_of(GetLastError()); // the child closed its end: the
    } else {                                                                          // end; a call that failed at
                                                                                      // once posts no completion
        code = finish(h, op, &moved);
        if (code == EPIPE) {
            code = 0; // the child closed its end as the read waited
        }
    }

    sn_net_pipe_op_free(op);
    *n = moved;
    return code;
}

int64_t sn_os__exec_write(int64_t fd, sn_slice_u8 buf, int64_t *n) {
    sn_check_slice((sn_slice *)&buf);
    HANDLE h = (HANDLE)(intptr_t)fd;
    *n = 0;
    int64_t most = (int64_t)SN_IO_MAX;
    while (*n < buf.len) {
        void *op = sn_net_pipe_op();
        DWORD moved = 0;
        int code = 0;
        if (!WriteFile(h, buf.ptr + *n, (DWORD)(buf.len - *n < most ? buf.len - *n : most), NULL,
                       sn_net_pipe_overlapped(op)) &&
            GetLastError() != ERROR_IO_PENDING) {
            code = sn_errno_of(GetLastError()); // a call that failed at once posts no completion
        } else {
            code = finish(h, op, &moved);
        }

        sn_net_pipe_op_free(op);
        *n += moved;
        if (code != 0) {
            return code;
        }
    }

    return 0;
}

void sn_os__exec_closePipe(int64_t fd) { CloseHandle((HANDLE)(intptr_t)fd); }
#endif
